package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Sid9017/linkport/internal/version"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/github"
)

var updatePublicKey string
var updateRepository = "Sid9017/linkport"

type updateStatus struct {
	Configured bool    `json:"configured"`
	State      string  `json:"state"`
	Version    string  `json:"version"`
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
		status: updateStatus{State: "unconfigured", Version: version.Version},
	}
	if updatePublicKey == "" {
		return controller, nil
	}
	key, err := base64.StdEncoding.DecodeString(updatePublicKey)
	if err != nil || len(key) != 32 {
		return nil, errors.New("invalid embedded update public key")
	}
	provider, err := github.New(github.Config{
		Repository: updateRepository, Prerelease: true,
		ChecksumAsset: "desktop-checksums.txt",
		HTTPClient:    &http.Client{},
		AssetMatcher: func(request updater.CheckRequest, assets []github.ReleaseAsset) int {
			suffix := "_" + request.Platform + "_" + request.Arch + ".zip"
			for index, asset := range assets {
				if strings.HasPrefix(asset.Name, "linkport-desktop_") && strings.HasSuffix(asset.Name, suffix) {
					return index
				}
			}
			return -1
		},
	})
	if err != nil {
		return nil, err
	}
	signed := &signedProvider{Provider: provider, client: &http.Client{}}
	if err := app.Updater.Init(updater.Config{
		CurrentVersion: version.Version,
		Providers:      []updater.Provider{signed},
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
			if err == nil && release != nil {
				controller.mu.Lock()
				controller.status.Next = release.Version
				controller.mu.Unlock()
			}
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
				if controller.settings.get().AutoUpdate {
					_ = controller.runJob(false)
				}
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
	client *http.Client
}

func (provider *signedProvider) Check(ctx context.Context, request updater.CheckRequest) (*updater.Release, error) {
	release, err := provider.Provider.Check(ctx, request)
	if err != nil || release == nil {
		return release, err
	}
	if release.Verification == nil || len(release.Verification.Digest) != 32 {
		return nil, errors.New("release is missing its SHA-256 checksum")
	}
	tag, _ := release.Metadata["github.release.tag"].(string)
	if tag == "" {
		return nil, errors.New("release tag is missing")
	}
	address := "https://github.com/" + updateRepository + "/releases/download/" + url.PathEscape(tag) + "/" + url.PathEscape(release.Artifact.Filename+".sig")
	query, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	response, err := provider.client.Do(query)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release signature unavailable: HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	if err != nil {
		return nil, err
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(body)))
	if err != nil || len(signature) != 64 {
		return nil, errors.New("invalid release signature")
	}
	release.Verification.SignatureAlgo = "ed25519"
	release.Verification.Signature = signature
	return release, nil
}
