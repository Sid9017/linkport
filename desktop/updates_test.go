package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/updater"
)

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
