package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sid9017/linkport/internal/p2p"
	"github.com/Sid9017/linkport/internal/relaycatalog"
)

func TestRelayDirectoryUpdates(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	sign := func(version uint64, expiry time.Time) []byte {
		t.Helper()
		raw, err := relaycatalog.Sign(relaycatalog.Manifest{Format: 1, Version: version, PublishedAt: now.Add(-time.Hour), ExpiresAt: expiry, Relays: []string{"https://207.148.110.58"}}, private)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	oldKey, oldBootstrap, oldSource := relaycatalog.PublicKey, relaycatalog.Bootstrap, relaycatalog.SourceURL
	defer func() {
		relaycatalog.PublicKey, relaycatalog.Bootstrap, relaycatalog.SourceURL = oldKey, oldBootstrap, oldSource
	}()
	relaycatalog.PublicKey = base64.StdEncoding.EncodeToString(public)
	relaycatalog.Bootstrap = sign(1, now.Add(time.Hour))
	relaycatalog.SourceURL = "https://example.com/relay-directory.json"
	n, err := p2p.New(context.Background(), p2p.Options{Directory: t.TempDir(), Disabled: true})
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	settings, err := newPreferenceStore(filepath.Join(t.TempDir(), "desktop.json"))
	if err != nil {
		t.Fatal(err)
	}
	pref := settings.get()
	pref.AutoUpdate = false
	if err := settings.save(pref); err != nil {
		t.Fatal(err)
	}
	var body atomic.Value
	body.Store(sign(2, now.Add(time.Hour)))
	var redirect atomic.Bool
	var redirected atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-Linkport-Token") != "" {
			t.Error("public directory request exposed an authentication credential")
		}
		if r.Header.Get("Cache-Control") != "no-cache" {
			t.Error("directory check did not request cache revalidation")
		}
		if r.URL.Path == "/trap" {
			redirected.Add(1)
		}
		if redirect.Load() {
			w.Header().Set("Location", "/trap")
			w.WriteHeader(http.StatusFound)
			return
		}
		// Static directory hosts may serve JSON with a text/plain content type.
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(body.Load().([]byte))
	}))
	defer server.Close()
	relaycatalog.SourceURL = server.URL
	c := newRelayUpdateController(context.Background(), n, settings)
	c.client.Transport = server.Client().Transport
	if err := c.fetch(); err != nil {
		t.Fatal(err)
	}
	if c.snapshot().State != "available" || n.Status().RelayDirectoryVersion != 1 {
		t.Fatal("manual update preference ignored")
	}
	if err := c.apply(); err != nil {
		t.Fatal(err)
	}
	if c.snapshot().State != "up-to-date" || n.Status().RelayDirectoryVersion != 2 {
		t.Fatal("verified directory not applied")
	}
	tampered := []byte(strings.ReplaceAll(string(sign(3, now.Add(time.Hour))), "207.148.110.58", "8.8.8.8"))
	for _, invalid := range [][]byte{sign(1, now.Add(time.Hour)), sign(3, now.Add(-time.Minute)), tampered, []byte(`{"format":1}`), make([]byte, relaycatalog.MaxSize+1)} {
		body.Store(invalid)
		if err := c.fetch(); err == nil {
			t.Fatal("invalid directory accepted")
		}
		if n.Status().RelayDirectoryVersion != 2 {
			t.Fatal("failed update replaced working directory")
		}
	}
	redirect.Store(true)
	if err := c.fetch(); err == nil || redirected.Load() != 0 {
		t.Fatal("directory source redirected outside fixed endpoint")
	}
	redirect.Store(false)
	pref.AutoUpdate = true
	if err := settings.save(pref); err != nil {
		t.Fatal(err)
	}
	body.Store(sign(3, now.Add(time.Hour)))
	if err := c.fetch(); err != nil {
		t.Fatal(err)
	}
	if n.Status().RelayDirectoryVersion != 3 {
		t.Fatal("automatic relay update not applied")
	}
	server.Close()
	if err := c.fetch(); err == nil {
		t.Fatal("offline source succeeded")
	}
	if n.Status().RelayDirectoryVersion != 3 {
		t.Fatal("offline check discarded working directory")
	}
	// The protected native API drives the same asynchronous controller used by
	// the real desktop, rather than a browser-only update fixture.
	api := &nativeAPI{token: "relay-update-test", relayUpdates: c}
	handler := api.handler()
	request := httptest.NewRequest(http.MethodGet, "/native/v1/relay-update", nil)
	request.Header.Set("X-Linkport-Token", api.token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatal("authorized relay status unavailable")
	}
	request = httptest.NewRequest(http.MethodPost, "/native/v1/relay-update/check", nil)
	request.Header.Set("X-Linkport-Token", api.token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatal("native check did not start")
	}
	deadline := time.Now().Add(time.Second)
	for {
		c.mu.Lock()
		busy := c.busy
		c.mu.Unlock()
		if !busy {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("offline check worker did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	if c.snapshot().State != "error" || n.Status().RelayDirectoryVersion != 3 {
		t.Fatal("native offline check lost active configuration")
	}

}

type bundledRelayTransport struct{ requests atomic.Int32 }

func (r *bundledRelayTransport) RoundTrip(*http.Request) (*http.Response, error) {
	r.requests.Add(1)
	return nil, errors.New("unexpected relay directory request")
}

func TestBundledRelaysDoNotCheckForDirectoryUpdates(t *testing.T) {
	oldSource := relaycatalog.SourceURL
	relaycatalog.SourceURL = ""
	defer func() { relaycatalog.SourceURL = oldSource }()
	network, err := p2p.New(t.Context(), p2p.Options{Directory: t.TempDir(), Disabled: true})
	if err != nil {
		t.Fatal(err)
	}
	defer network.Close()
	settings, err := newPreferenceStore(filepath.Join(t.TempDir(), "desktop.json"))
	if err != nil {
		t.Fatal(err)
	}
	controller := newRelayUpdateController(t.Context(), network, settings)
	transport := &bundledRelayTransport{}
	controller.client.Transport = transport
	controller.start()
	if err := controller.check(); err != nil {
		t.Fatal(err)
	}
	if err := controller.fetch(); err != nil {
		t.Fatal(err)
	}
	if err := controller.apply(); err == nil {
		t.Fatal("bundled relays offered a runtime directory update")
	}
	status := controller.snapshot()
	if status.State != "bundled" || status.Version != network.Status().RelayDirectoryVersion || status.Error != "" {
		t.Fatalf("bundled relay status became an update or error: %+v", status)
	}
	controller.mu.Lock()
	busy := controller.busy
	controller.mu.Unlock()
	if busy || transport.requests.Load() != 0 {
		t.Fatal("bundled relays started a directory check")
	}
}

// Ordinary tests stay offline. Explicit publication acceptance uses the real
// desktop fetch/verification/application controller with an isolated identity
// and networking disabled, so it never depends on a running public relay.
func TestPublishedRelayDirectory(t *testing.T) {
	if relaycatalog.SourceURL == "" {
		t.Skip("relay directory is bundled; no publication source is configured")
	}
	if os.Getenv("LINKPORT_TEST_PUBLIC_DIRECTORY") != "1" {
		t.Skip("set LINKPORT_TEST_PUBLIC_DIRECTORY=1 to verify the published directory")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	network, err := p2p.New(ctx, p2p.Options{Directory: t.TempDir(), Disabled: true})
	if err != nil {
		t.Fatal(err)
	}
	defer network.Close()
	settings, err := newPreferenceStore(filepath.Join(t.TempDir(), "desktop.json"))
	if err != nil {
		t.Fatal(err)
	}
	pref := settings.get()
	pref.AutoUpdate = false
	if err := settings.save(pref); err != nil {
		t.Fatal(err)
	}
	before := network.Status()
	controller := newRelayUpdateController(ctx, network, settings)
	defer controller.client.CloseIdleConnections()
	if err := controller.fetch(); err != nil {
		t.Fatal(err)
	}
	if controller.snapshot().State == "available" {
		if err := controller.apply(); err != nil {
			t.Fatal(err)
		}
	}
	after := network.Status()
	if controller.snapshot().State != "up-to-date" || after.RelayDirectoryVersion < before.RelayDirectoryVersion {
		t.Fatal("published directory did not preserve the verified version floor")
	}
	if after.NodeID != before.NodeID || after.State != "Disabled" {
		t.Fatal("directory acceptance changed identity or activated networking")
	}
	t.Logf("verified public feed %s: version=%d relays=%d; networking remains disabled",
		relaycatalog.SourceURL, after.RelayDirectoryVersion, len(after.PublicRelays))
}
