package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
		if r.URL.Path == "/trap" {
			redirected.Add(1)
		}
		if redirect.Load() {
			w.Header().Set("Location", "/trap")
			w.WriteHeader(http.StatusFound)
			return
		}
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
	for _, invalid := range [][]byte{sign(1, now.Add(time.Hour)), sign(3, now.Add(-time.Minute)), []byte(`{"format":1}`), make([]byte, relaycatalog.MaxSize+1)} {
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
