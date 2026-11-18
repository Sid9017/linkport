package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/updater"
)

type updateRoundTripFunc func(*http.Request) (*http.Response, error)

func (f updateRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

// Native Wails error callbacks are asynchronous. The controller must work
// without receiving those callbacks before DownloadAndInstall returns.
type updateTestHost struct{}

func (updateTestHost) Emit(string, ...any) bool                              { return false }
func (updateTestHost) OnEvent(string, func(any)) func()                      { return func() {} }
func (updateTestHost) OpenWindow(updater.WindowOptions) updater.WindowHandle { return nil }
func (updateTestHost) Quit()                                                 {}

type updateTestProvider struct {
	check    func(context.Context, updater.CheckRequest) (*updater.Release, error)
	download func(context.Context, *updater.Release, io.Writer, func(int64, int64)) error
}

func (updateTestProvider) Name() string { return "isolated-update-fixture" }
func (provider updateTestProvider) Check(ctx context.Context, request updater.CheckRequest) (*updater.Release, error) {
	return provider.check(ctx, request)
}
func (provider updateTestProvider) Download(ctx context.Context, release *updater.Release, target io.Writer, progress func(int64, int64)) error {
	return provider.download(ctx, release, target, progress)
}

func TestUpdateRetryClearsErrorsAndAvoidsConcurrentDownloads(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	controller := &updateController{
		ctx: ctx, settings: &preferenceStore{settings: preferences{AutoUpdate: true}},
		status: updateStatus{Configured: true, Error: "previous failure", ErrorCode: "download-timeout", Progress: 0.75},
	}
	payload := []byte("verified update fixture")
	digest := sha256.Sum256(payload)
	started, proceed := make(chan struct{}), make(chan struct{})
	var checks, downloads atomic.Int32
	provider := updateTestProvider{
		check: func(context.Context, updater.CheckRequest) (*updater.Release, error) {
			checks.Add(1)
			if status := controller.snapshot(); status.Error != "" || status.ErrorCode != "" || status.Progress != 0 {
				t.Error("new attempt retained previous error or progress")
			}
			return &updater.Release{
				Version: "0.0.3", Artifact: updater.Artifact{Filename: "fixture.bin"},
				Verification: &updater.Verification{DigestAlgo: "sha256", Digest: digest[:]},
			}, nil
		},
		download: func(ctx context.Context, _ *updater.Release, target io.Writer, _ func(int64, int64)) error {
			if downloads.Add(1) == 1 {
				close(started)
				select {
				case <-proceed:
					_, err := target.Write([]byte("invalid bytes"))
					return err
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			_, err := target.Write(payload)
			return err
		},
	}
	controller.updater = updater.New(updateTestHost{})
	if err := controller.updater.Init(updater.Config{CurrentVersion: "0.0.2", Providers: []updater.Provider{provider}, Window: updater.WindowNone}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if path := controller.updater.DownloadedPath(); path != "" {
			parent := filepath.Dir(path)
			if !strings.HasPrefix(filepath.Base(parent), "wails-update-") {
				t.Error("unexpected fixture staging path")
				return
			}
			if err := os.RemoveAll(parent); err != nil {
				t.Error(err)
			}
		}
	})
	waitForJob := func() {
		t.Helper()
		deadline := time.After(5 * time.Second)
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for {
			controller.mu.Lock()
			busy := controller.busy
			controller.mu.Unlock()
			if !busy {
				return
			}
			select {
			case <-tick.C:
			case <-deadline:
				t.Fatal("update worker did not stop")
			}
		}
	}
	if err := controller.runJob(false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("download never started")
	}
	if err := controller.runJob(false); err != nil {
		t.Fatal(err)
	}
	close(proceed)
	waitForJob()
	if status := controller.snapshot(); status.ErrorStage != updater.StageVerify || status.ErrorCode != "verification-failed" {
		t.Fatalf("verification error misreported: %+v", status)
	}
	if checks.Load() != 1 || downloads.Load() != 1 {
		t.Fatal("concurrent request started a duplicate job")
	}
	if err := controller.runJob(false); err != nil {
		t.Fatal(err)
	}
	waitForJob()
	if status := controller.snapshot(); status.State != "ready" || status.Error != "" || status.ErrorCode != "" {
		t.Fatalf("successful retry retained an error: %+v", status)
	}
	if err := controller.runJob(false); err != nil {
		t.Fatal(err)
	}
	if checks.Load() != 2 || downloads.Load() != 2 {
		t.Fatal("ready update was downloaded again")
	}
}

func TestUpdateOperationsHaveIndependentDeadlines(t *testing.T) {
	for _, operation := range []string{"check", "download"} {
		t.Run(operation, func(t *testing.T) {
			limit := updateCheckTimeout
			if operation == "download" {
				limit = updateDownloadTimeout
			}
			called := false
			client := &http.Client{Timeout: 30 * time.Second, Transport: updateRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				called = true
				deadline, ok := request.Context().Deadline()
				remaining := time.Until(deadline)
				if !ok || remaining > limit || remaining < limit/2 {
					t.Errorf("%s deadline: %v, want %v", operation, remaining, limit)
				}
				return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
			})}
			provider, err := newSignedProvider("https://updates.example", "stable", nil, client)
			if err != nil {
				t.Fatal(err)
			}
			if operation == "check" {
				_, err = provider.Check(context.Background(), updater.CheckRequest{CurrentVersion: "0.0.2", Platform: "darwin", Arch: "arm64"})
			} else {
				err = provider.Download(context.Background(), &updater.Release{Metadata: map[string]any{"endpoint.artifact.url": "https://updates.example/downloads/fixture.zip"}}, io.Discard, func(int64, int64) {})
			}
			if err != nil || !called {
				t.Fatalf("operation did not complete: called=%v err=%v", called, err)
			}
			if client.Timeout != 30*time.Second {
				t.Fatal("modified the caller's HTTP client")
			}
		})
	}
}

func TestUpdateProviderValidatesConfiguration(t *testing.T) {
	for _, test := range []struct{ name, source, channel string }{
		{"missing host", "", "stable"},
		{"malformed URL", "://", "stable"},
		{"credentials", "https://user:fixture@example.test", "stable"},
		{"query", "https://example.test?token=fixture", "stable"},
		{"fragment", "https://example.test/#fragment", "stable"},
		{"unsupported channel", "https://example.test", "dev"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := newSignedProvider(test.source, test.channel, nil, nil); err == nil {
				t.Fatal("invalid update configuration accepted")
			}
		})
	}
	if _, err := newSignedProvider("https://example.test", "stable", nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateDownloadOutlivesManifestClientTimeout(t *testing.T) {
	payload := []byte("slow fixture archive")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		timer := time.NewTimer(100 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
		case <-timer.C:
			_, _ = w.Write(payload)
		}
	}))
	defer server.Close()
	client := server.Client()
	client.Timeout = 25 * time.Millisecond
	provider, err := newSignedProvider(server.URL, "stable", nil, client)
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	err = provider.Download(t.Context(), &updater.Release{Metadata: map[string]any{"endpoint.artifact.url": server.URL + "/downloads/fixture.zip"}}, &body, func(int64, int64) {})
	if err != nil {
		t.Fatalf("healthy slow download was aborted: %v", err)
	}
	if !bytes.Equal(body.Bytes(), payload) {
		t.Fatal("download bytes changed")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	err = provider.Download(ctx, &updater.Release{Metadata: map[string]any{"endpoint.artifact.url": server.URL + "/downloads/fixture.zip"}}, io.Discard, func(int64, int64) {})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shorter caller deadline was ignored: %v", err)
	}
}

func TestUpdateRequestsHonorCallerCancellation(t *testing.T) {
	for _, operation := range []string{"check", "download"} {
		t.Run(operation, func(t *testing.T) {
			started := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if operation == "download" {
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
				}
				close(started)
				<-r.Context().Done()
			}))
			defer server.Close()
			provider, err := newSignedProvider(server.URL, "stable", nil, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				if operation == "check" {
					_, err := provider.Check(ctx, updater.CheckRequest{CurrentVersion: "0.0.2", Platform: "darwin", Arch: "arm64"})
					done <- err
				} else {
					done <- provider.Download(ctx, &updater.Release{Metadata: map[string]any{"endpoint.artifact.url": server.URL + "/downloads/fixture.zip"}}, io.Discard, func(int64, int64) {})
				}
			}()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("request never started")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation lost: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("cancelled operation did not stop")
			}
		})
	}
}

func TestUpdateErrorCodesPreserveFailureStage(t *testing.T) {
	for _, test := range []struct {
		stage updater.Stage
		err   error
		want  string
	}{
		{updater.StageCheck, context.DeadlineExceeded, "check-timeout"},
		{updater.StageCheck, errors.New("all providers failed: fetch manifest: context deadline exceeded"), "check-timeout"},
		{updater.StageDownload, &url.Error{Op: "Get", URL: "https://example.test", Err: context.DeadlineExceeded}, "download-timeout"},
		{updater.StageDownload, errors.New("Client.Timeout exceeded while reading body"), "download-timeout"},
		{updater.StageDownload, errors.New("operation timed out"), "download-timeout"},
		{updater.StageCheck, errors.New("HTTP 503"), "check-failed"},
		{updater.StageDownload, errors.New("HTTP 503"), "download-failed"},
		{updater.StageCheck, fmt.Errorf("check: %w", errUpdateVerification), "verification-failed"},
		{updater.StageCheck, errors.New(errUpdateVerification.Error()), "verification-failed"},
		{updater.StageVerify, errors.New("digest mismatch"), "verification-failed"},
		{updater.StageInstall, errors.New("invalid archive"), "install-failed"},
		{"restart", errors.New("helper failed"), "restart-failed"},
		{updater.StageDownload, context.Canceled, "cancelled"},
		{updater.StageDownload, errors.New("context canceled"), "cancelled"},
		{"unknown", errors.New("unknown"), "update-failed"},
	} {
		t.Run(test.want, func(t *testing.T) {
			if got := updateErrorCode(test.stage, test.err); got != test.want {
				t.Fatalf("got %s, want %s", got, test.want)
			}
		})
	}
	controller := &updateController{}
	controller.recordError(updater.StageDownload, errors.New("updater: digest mismatch"))
	if status := controller.snapshot(); status.ErrorStage != updater.StageVerify || status.ErrorCode != "verification-failed" {
		t.Fatalf("precise failure stage lost: %+v", status)
	}
	controller.mu.Lock()
	controller.clearErrorLocked()
	controller.mu.Unlock()
	if status := controller.snapshot(); status.Error != "" || status.ErrorCode != "" || status.ErrorStage != "" {
		t.Fatal("retry retained stale error")
	}
}

func TestUpdateReturnedErrorsIdentifyVerificationAndStaging(t *testing.T) {
	for _, test := range []struct {
		stage   updater.Stage
		message string
		want    updater.Stage
	}{
		{updater.StageDownload, "updater: digest mismatch", updater.StageVerify},
		{updater.StageDownload, "updater: ed25519 signature did not verify", updater.StageVerify},
		{updater.StageDownload, "updater: signature requires a public key", updater.StageVerify},
		{updater.StageDownload, "updater: archive must contain exactly one top-level entry", updater.StageInstall},
		{updater.StageDownload, "updater: zip open: invalid zip", updater.StageInstall},
		{updater.StageDownload, "updater: finalise: permission denied", updater.StageInstall},
		{updater.StageDownload, "endpoint: download: context deadline exceeded", updater.StageDownload},
		{updater.StageCheck, "updater: digest mismatch", updater.StageCheck},
		{"restart", "helper did not start", "restart"},
	} {
		t.Run(test.message, func(t *testing.T) {
			if got := updateFailureStage(test.stage, errors.New(test.message)); got != test.want {
				t.Fatalf("got %s, want %s", got, test.want)
			}
		})
	}
}

func TestPublicUpdatesVerifySignaturesAndKeepChannelsSeparate(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("signed fixture archive"))
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(private, digest[:]))
	currentVersion, channel := "0.0.2", "stable"
	artifactURL := "/downloads/v0.0.3/linkport-desktop_0.0.3_darwin_arm64.zip"
	artifactName := "linkport-desktop_0.0.3_darwin_arm64.zip"
	nextVersion := "0.0.3"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-Linkport-Token") != "" {
			t.Error("public update check sent a credential")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"schemaVersion": 1, "version": nextVersion, "channel": channel,
			"artifacts": []map[string]any{{
				"url": artifactURL, "filename": artifactName, "filetype": "zip",
				"platform": "darwin", "arch": "arm64",
				"digestAlgo": "sha256", "digest": base64.StdEncoding.EncodeToString(digest[:]),
				"signatureAlgo": "ed25519", "signature": signature,
			}},
		})
	}))
	defer server.Close()
	provider, err := newSignedProvider(server.URL, "stable", public, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	request := updater.CheckRequest{CurrentVersion: currentVersion, Platform: "darwin", Arch: "arm64"}
	release, err := provider.Check(t.Context(), request)
	if err != nil || release == nil || release.Version != "0.0.3" {
		t.Fatal("signed public update unavailable", err)
	}
	signature = base64.StdEncoding.EncodeToString(make([]byte, 64))
	if _, err := provider.Check(t.Context(), request); err == nil {
		t.Fatal("offered an update without a valid publisher signature")
	}
	signature = base64.StdEncoding.EncodeToString(ed25519.Sign(private, digest[:]))
	channel = "nightly"
	if release, err := provider.Check(t.Context(), request); err != nil || release != nil {
		t.Fatal("stable build adopted the nightly channel", err)
	}
	channel = "stable"
	artifactURL = "https://example.com/private-or-unrelated.zip"
	if _, err := provider.Check(t.Context(), request); err == nil {
		t.Fatal("accepted artifact outside the public download service")
	}
	artifactURL = "/downloads/v0.0.3/linkport-desktop_0.0.3_darwin_arm64.zip"
	request.CurrentVersion = "0.0.3"
	if release, err := provider.Check(t.Context(), request); err != nil || release != nil {
		t.Fatal("offered the same installed version", err)
	}
	request.CurrentVersion = "0.0.2"
	nextVersion = "0.0.3-nightly.20261117180000.1"
	channel = "nightly"
	artifactName = "linkport-desktop_" + nextVersion + "_darwin_arm64.zip"
	artifactURL = "/downloads/v" + nextVersion + "/" + artifactName
	nightly, err := newSignedProvider(server.URL, "nightly", public, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	request.CurrentVersion = "0.0.3-nightly.20261116180000.99"
	if release, err := nightly.Check(t.Context(), request); err != nil || release == nil {
		t.Fatal("newer chronological nightly unavailable", err)
	}
	request.CurrentVersion = nextVersion
	if release, err := nightly.Check(t.Context(), request); err != nil || release != nil {
		t.Fatal("nightly offered itself again", err)
	}
}

func TestPublicFeedFailuresStayErrors(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusServiceUnavailable, http.StatusNoContent} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			}))
			defer server.Close()
			provider, err := newSignedProvider(server.URL, "stable", nil, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			release, err := provider.Check(t.Context(), updater.CheckRequest{CurrentVersion: "0.0.2", Platform: "darwin", Arch: "arm64"})
			if release != nil || (err == nil) != (status == http.StatusNoContent) {
				t.Fatalf("HTTP %d produced release=%v error=%v", status, release, err)
			}
		})
	}
}
