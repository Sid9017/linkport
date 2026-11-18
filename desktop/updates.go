package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Sid9017/linkport/internal/version"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/endpoint"
)

var updatePublicKey = "hRHv6nvu8IA0lGNwJUnXCCgQG+QUeVpI+PVcs3+aHW4="
var updateBaseURL = "https://linkport.grezl.link"

const (
	updateCheckTimeout    = 30 * time.Second
	updateDownloadTimeout = 30 * time.Minute
)

var errUpdateVerification = errors.New("release checksum or publisher signature rejected")

type updateStatus struct {
	Configured bool          `json:"configured"`
	State      string        `json:"state"`
	Version    string        `json:"version"`
	Channel    string        `json:"channel"`
	Commit     string        `json:"commit"`
	Next       string        `json:"next,omitempty"`
	Progress   float64       `json:"progress"`
	Error      string        `json:"error,omitempty"`
	ErrorStage updater.Stage `json:"errorStage,omitempty"`
	ErrorCode  string        `json:"errorCode,omitempty"`
}

type updateController struct {
	ctx         context.Context
	updater     *updater.Updater
	settings    *preferenceStore
	mu          sync.Mutex
	busy        bool
	provider    *signedProvider
	pending     *updater.Release
	packagePath string
	prepared    bool
	nativeState string
	status      updateStatus
}

func newUpdateController(ctx context.Context, app *application.App, settings *preferenceStore) (*updateController, error) {
	controller := &updateController{
		ctx: ctx, updater: app.Updater, settings: settings,
		status: updateStatus{State: "unconfigured", Version: version.Version, Channel: version.Channel, Commit: version.Commit},
	}
	if version.Channel == "dev" {
		controller.status.State = "development"
		return controller, nil
	}
	if version.Channel != "stable" && version.Channel != "nightly" {
		return nil, errors.New("unsupported release channel")
	}
	if updatePublicKey == "" {
		return controller, nil
	}
	key, err := base64.StdEncoding.DecodeString(updatePublicKey)
	if err != nil || len(key) != 32 {
		return nil, errors.New("invalid embedded update public key")
	}
	provider, err := newSignedProvider(updateBaseURL, version.Channel, key, nil)
	if err != nil {
		return nil, err
	}
	if err := app.Updater.Init(updater.Config{
		CurrentVersion: version.Version,
		Providers:      []updater.Provider{provider},
		PublicKey:      key,
		Window:         updater.WindowNone,
	}); err != nil {
		return nil, err
	}
	controller.provider = provider
	controller.status.Configured = true
	app.Event.On(updater.EventDownloadProgress, func(event *application.CustomEvent) {
		controller.mu.Lock()
		defer controller.mu.Unlock()
		if payload, ok := event.Data.(updater.Progress); ok && controller.busy && controller.updater.State() == updater.StateDownloading {
			if payload.Total > 0 {
				controller.status.Progress = float64(payload.Written) / float64(payload.Total)
			}
		}
	})
	return controller, nil
}

func (controller *updateController) snapshot() updateStatus {
	if controller == nil {
		return updateStatus{State: "unconfigured"}
	}
	controller.mu.Lock()
	status := controller.status
	nativeState, busy, prepared := controller.nativeState, controller.busy, controller.prepared
	controller.mu.Unlock()
	if status.Configured {
		status.State = string(controller.updater.State())
		if status.State == "ready" && busy && !prepared {
			status.State = "installing"
		}
		if nativeState != "" {
			status.State = nativeState
		}
	}
	if status.Error != "" && nativeState != "package-ready" && nativeState != "installer-opened" {
		status.State = "error"
	}
	return status
}

func (controller *updateController) runJob(downloadOnly bool) error {
	controller.mu.Lock()
	if !controller.status.Configured {
		controller.mu.Unlock()
		return errors.New("automatic updates need an embedded release signing key")
	}
	if controller.busy {
		controller.mu.Unlock()
		return nil
	}
	if controller.packagePath != "" || (controller.updater.State() == updater.StateReady && controller.prepared) {
		controller.mu.Unlock()
		return nil
	}
	controller.busy = true
	controller.clearErrorLocked()
	controller.status.Progress = 0
	controller.mu.Unlock()
	go func() {
		defer func() {
			controller.mu.Lock()
			controller.busy = false
			controller.mu.Unlock()
		}()
		var err error
		if !downloadOnly && controller.updater.State() != updater.StateReady {
			ctx, cancel := context.WithTimeout(controller.ctx, updateCheckTimeout)
			var release *updater.Release
			release, err = controller.updater.Check(ctx)
			cancel()
			controller.mu.Lock()
			controller.status.Next = ""
			if err == nil && release != nil {
				controller.status.Next = release.Version
				controller.pending = release
			}
			controller.mu.Unlock()
			if err != nil || release == nil || !controller.settings.get().AutoUpdate {
				controller.recordError(updater.StageCheck, err)
				return
			}
		}
		ctx, cancel := context.WithTimeout(controller.ctx, updateDownloadTimeout)
		defer cancel()
		controller.mu.Lock()
		pending := controller.pending
		controller.mu.Unlock()
		if runtime.GOOS == "linux" && pending != nil && pending.Artifact.Filetype == "deb" {
			controller.downloadDeb(ctx, pending)
			return
		}
		if controller.updater.State() != updater.StateReady {
			if err := controller.updater.DownloadAndInstall(ctx); err != nil {
				controller.recordError(updater.StageDownload, err)
				return
			}
		}
		next := controller.snapshot().Next
		if err := prepareNativeUpdate(ctx, controller.updater.DownloadedPath(), next); err != nil {
			controller.recordError(updater.StageInstall, err)
			return
		}
		controller.mu.Lock()
		controller.prepared = true
		controller.mu.Unlock()
	}()
	return nil
}

func (controller *updateController) clearErrorLocked() {
	controller.status.Error = ""
	controller.status.ErrorStage = ""
	controller.status.ErrorCode = ""
}

func (controller *updateController) recordError(stage updater.Stage, err error) {
	if err != nil {
		controller.mu.Lock()
		stage = updateFailureStage(stage, err)
		controller.status.Error = err.Error()
		controller.status.ErrorStage = stage
		controller.status.ErrorCode = updateErrorCode(stage, err)
		controller.mu.Unlock()
	}
}

func updateFailureStage(fallback updater.Stage, err error) updater.Stage {
	// Error events arrive asynchronously and can outlive a failed attempt.
	// Classify the pinned updater's returned errors instead; this only chooses
	// UI wording and never changes whether an update is accepted.
	if fallback != updater.StageDownload {
		return fallback
	}
	message := err.Error()
	if strings.HasPrefix(message, "updater: ") {
		for _, token := range []string{"digest", "signature", "public key"} {
			if strings.Contains(message, token) {
				return updater.StageVerify
			}
		}
	}
	for _, prefix := range []string{"updater: extract", "updater: archive", "updater: zip", "updater: tar", "updater: remove staged archive", "updater: promote extracted entry", "updater: finalise"} {
		if strings.HasPrefix(message, prefix) {
			return updater.StageInstall
		}
	}
	return fallback
}

func updateErrorCode(stage updater.Stage, err error) string {
	message := strings.ToLower(err.Error())
	if errors.Is(err, context.Canceled) || strings.Contains(message, "context canceled") {
		return "cancelled"
	}
	var networkError net.Error
	timedOut := errors.Is(err, context.DeadlineExceeded) ||
		(errors.As(err, &networkError) && networkError.Timeout()) ||
		strings.Contains(message, "deadline exceeded") || strings.Contains(message, "timeout") ||
		strings.Contains(message, "timed out")
	// Provider fallback errors may contain messages rather than error chains.
	if stage == updater.StageVerify || errors.Is(err, errUpdateVerification) ||
		strings.Contains(message, "publisher signature rejected") {
		return "verification-failed"
	}
	switch stage {
	case updater.StageCheck:
		if timedOut {
			return "check-timeout"
		}
		return "check-failed"
	case updater.StageDownload:
		if timedOut {
			return "download-timeout"
		}
		return "download-failed"
	case updater.StageInstall:
		return "install-failed"
	case "restart":
		return "restart-failed"
	default:
		return "update-failed"
	}
}

func (controller *updateController) start() {
	if !controller.snapshot().Configured {
		return
	}
	go func() {
		timer := time.NewTimer(30 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-controller.ctx.Done():
				return
			case <-timer.C:
				_ = controller.runJob(false)
				timer.Reset(time.Minute)
			}
		}
	}()
}

func (controller *updateController) restart() error {
	controller.mu.Lock()
	busy, path, prepared := controller.busy, controller.packagePath, controller.prepared
	controller.mu.Unlock()
	if busy {
		return errors.New("update preparation is still running")
	}
	if path != "" {
		return controller.openDeb(path)
	}
	if !prepared {
		return errors.New("update preparation has not completed")
	}
	if controller.updater.State() != updater.StateReady {
		return errors.New("no verified update is ready")
	}
	controller.mu.Lock()
	controller.clearErrorLocked()
	controller.mu.Unlock()
	time.AfterFunc(300*time.Millisecond, func() {
		controller.recordError("restart", controller.updater.Restart(controller.ctx))
	})
	return nil
}

type signedProvider struct {
	updater.Provider
	key     ed25519.PublicKey
	baseURL *url.URL
	channel string
}

type updateTransport struct{ base http.RoundTripper }

func (t updateTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err == nil && response.StatusCode == http.StatusNotFound {
		response.Body.Close()
		return nil, errors.New("public update endpoint or package not found")
	}
	return response, err
}

func newSignedProvider(source, channel string, key ed25519.PublicKey, client *http.Client) (*signedProvider, error) {
	if channel != "stable" && channel != "nightly" {
		return nil, errors.New("unsupported update channel")
	}
	base, err := url.Parse(source)
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("invalid update source")
	}
	if client == nil {
		client = &http.Client{}
	}
	copyClient := *client
	// Client.Timeout covers the entire response body, so sharing a short
	// manifest timeout with a large download aborts otherwise healthy transfers.
	// Bound each operation with its own context instead.
	copyClient.Timeout = 0
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	// The public service deliberately uses 204 for an unpublished channel.
	// A missing deployment/route must remain an error, rather than the
	// endpoint provider's generic interpretation of 404 as "up to date".
	copyClient.Transport = updateTransport{base: transport}
	p, err := endpoint.New(endpoint.Config{
		URL:     strings.TrimRight(source, "/") + "/updates/" + channel + ".json",
		Channel: channel, HTTPClient: &copyClient,
	})
	if err != nil {
		return nil, err
	}
	return &signedProvider{Provider: p, key: key, baseURL: base, channel: channel}, nil
}

func (provider *signedProvider) Check(ctx context.Context, request updater.CheckRequest) (*updater.Release, error) {
	ctx, cancel := context.WithTimeout(ctx, updateCheckTimeout)
	defer cancel()
	release, err := provider.Provider.Check(ctx, request)
	if err != nil || release == nil {
		return release, err
	}
	if release.Channel != provider.channel ||
		(provider.channel == "stable" && strings.Contains(release.Version, "-")) ||
		(provider.channel == "nightly" && !strings.Contains(release.Version, "-nightly.")) {
		return nil, errors.New("release channel rejected")
	}
	v := release.Verification
	if v == nil || v.DigestAlgo != "sha256" || len(v.Digest) != 32 ||
		v.SignatureAlgo != "ed25519" || len(provider.key) != ed25519.PublicKeySize ||
		!ed25519.Verify(provider.key, v.Digest, v.Signature) {
		return nil, errUpdateVerification
	}
	address, _ := release.Metadata["endpoint.artifact.url"].(string)
	target, err := url.Parse(address)
	if err != nil || target.Scheme != provider.baseURL.Scheme || target.Host != provider.baseURL.Host ||
		target.User != nil || target.RawQuery != "" || target.Fragment != "" || !strings.HasPrefix(target.Path, "/downloads/") ||
		!validUpdateArtifact(release, request) {
		return nil, errors.New("release artifact is outside the public download service")
	}
	return release, nil
}

func (provider *signedProvider) Download(ctx context.Context, release *updater.Release, destination io.Writer, progress func(int64, int64)) error {
	ctx, cancel := context.WithTimeout(ctx, updateDownloadTimeout)
	defer cancel()
	return provider.Provider.Download(ctx, release, destination, progress)
}

func validUpdateArtifact(release *updater.Release, request updater.CheckRequest) bool {
	format := map[string]string{"darwin": "dmg", "windows": "exe", "linux": "deb"}[request.Platform]
	if format == "" || (request.Arch != "amd64" && request.Arch != "arm64") {
		return false
	}
	// Legacy published versions remain readable during the format migration.
	if release.Artifact.Filetype != format && release.Artifact.Filetype != "zip" {
		return false
	}
	return release.Artifact.Filename == "linkport-desktop_"+release.Version+"_"+request.Platform+"_"+request.Arch+"."+release.Artifact.Filetype
}
