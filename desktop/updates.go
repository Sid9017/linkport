package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
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

type updateStatus struct {
	Configured bool    `json:"configured"`
	State      string  `json:"state"`
	Version    string  `json:"version"`
	Channel    string  `json:"channel"`
	Commit     string  `json:"commit"`
	Next       string  `json:"next,omitempty"`
	Progress   float64 `json:"progress"`
	Error      string  `json:"error,omitempty"`
}

type updateController struct {
	ctx      context.Context
	updater  *updater.Updater
	settings *preferenceStore
	mu       sync.Mutex
	busy     bool
	status   updateStatus
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
	provider, err := newSignedProvider(updateBaseURL, version.Channel, key, &http.Client{Timeout: 30 * time.Second})
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
	controller.status.Configured = true
	for _, name := range []string{updater.EventDownloadProgress, updater.EventError} {
		app.Event.On(name, func(event *application.CustomEvent) {
			controller.mu.Lock()
			defer controller.mu.Unlock()
			switch payload := event.Data.(type) {
			case updater.Progress:
				if payload.Total > 0 {
					controller.status.Progress = float64(payload.Written) / float64(payload.Total)
				}
			case updater.ErrorInfo:
				controller.status.Error = payload.Message
			}
		})
	}
	return controller, nil
}

func (controller *updateController) snapshot() updateStatus {
	if controller == nil {
		return updateStatus{State: "unconfigured"}
	}
	controller.mu.Lock()
	status := controller.status
	controller.mu.Unlock()
	if status.Configured {
		status.State = string(controller.updater.State())
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
	if controller.updater.State() == updater.StateReady {
		controller.mu.Unlock()
		return nil
	}
	controller.busy = true
	controller.status.Error = ""
	controller.mu.Unlock()
	go func() {
		defer func() {
			controller.mu.Lock()
			controller.busy = false
			controller.mu.Unlock()
		}()
		var err error
		if !downloadOnly {
			ctx, cancel := context.WithTimeout(controller.ctx, 30*time.Second)
			var release *updater.Release
			release, err = controller.updater.Check(ctx)
			cancel()
			controller.mu.Lock()
			controller.status.Next = ""
			if err == nil && release != nil {
				controller.status.Next = release.Version
			}
			controller.mu.Unlock()
			if err != nil || release == nil || !controller.settings.get().AutoUpdate {
				controller.recordError(err)
				return
			}
		}
		ctx, cancel := context.WithTimeout(controller.ctx, 30*time.Minute)
		defer cancel()
		controller.recordError(controller.updater.DownloadAndInstall(ctx))
	}()
	return nil
}

func (controller *updateController) recordError(err error) {
	if err != nil {
		controller.mu.Lock()
		controller.status.Error = err.Error()
		controller.mu.Unlock()
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
				timer.Reset(6 * time.Hour)
			}
		}
	}()
}

func (controller *updateController) restart() error {
	if controller.updater.State() != updater.StateReady {
		return errors.New("no verified update is ready")
	}
	time.AfterFunc(300*time.Millisecond, func() {
		controller.recordError(controller.updater.Restart(controller.ctx))
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
		client = &http.Client{Timeout: 30 * time.Second}
	}
	copyClient := *client
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
		return nil, errors.New("release checksum or publisher signature rejected")
	}
	address, _ := release.Metadata["endpoint.artifact.url"].(string)
	target, err := url.Parse(address)
	if err != nil || target.Scheme != provider.baseURL.Scheme || target.Host != provider.baseURL.Host ||
		target.User != nil || target.RawQuery != "" || target.Fragment != "" || !strings.HasPrefix(target.Path, "/downloads/") ||
		release.Artifact.Filename != "linkport-desktop_"+release.Version+"_"+request.Platform+"_"+request.Arch+".zip" || release.Artifact.Filetype != "zip" {
		return nil, errors.New("release artifact is outside the public download service")
	}
	return release, nil
}
