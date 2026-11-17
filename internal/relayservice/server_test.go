package relayservice

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"
	"github.com/tmc/go-iroh/relay"
)

func startTestServer(t *testing.T, cfg Config) (*Server, string, func()) {
	t.Helper()
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(12 * time.Second):
				t.Error("shutdown timed out")
			}
		})
	}
	return s, "http://" + ln.Addr().String(), stop
}

func TestRoutesAndShutdownHijackedConnections(t *testing.T) {
	s, url, stop := startTestServer(t, DefaultConfig())
	defer stop()
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	for _, tc := range []struct {
		path string
		code int
	}{{"/healthz", 200}, {"/ping", 200}, {"/", 404}, {"/metrics", 404}} {
		resp, err := client.Get(url + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != tc.code {
			t.Fatalf("%s: status=%d err=%v", tc.path, resp.StatusCode, err)
		}
		if tc.path == "/healthz" && string(body) != "ok\n" {
			t.Fatalf("health: %q", body)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, url+"/relay", &websocket.DialOptions{Subprotocols: []string{"iroh-relay-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	// Keep a hijacked connection pending authentication, then cancel the service.
	if _, _, err = ws.Read(ctx); err != nil {
		t.Fatal(err)
	}
	// Shutdown must close the hijacked socket as well as HTTP connections.
	if s.Connections() == 0 {
		t.Fatal("hijacked connection not tracked")
	}
	stop()
	if s.Connections() != 0 {
		t.Fatal("shutdown retained connections")
	}
	if _, _, err = ws.Read(ctx); err == nil {
		t.Fatal("shutdown left websocket usable")
	}
}

func TestConnectionLimitAndHeaderTimeout(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxConnections = 1
	cfg.ReadHeaderTimeout = 100 * time.Millisecond
	s, url, stop := startTestServer(t, cfg)
	defer stop()
	addr := url[len("http://"):]
	first, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err = io.WriteString(first, "GET /healthz HTTP/1.1\r\n"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for s.Connections() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	second, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	_ = second.SetReadDeadline(time.Now().Add(time.Second))
	if _, err = second.Read(make([]byte, 1)); err == nil {
		t.Fatal("excess connection admitted")
	}
	_ = first.SetReadDeadline(time.Now().Add(time.Second))
	_, _ = io.Copy(io.Discard, first)
	deadline = time.Now().Add(time.Second)
	for s.Connections() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.Connections() != 0 {
		t.Fatal("header timeout did not release connection")
	}
	resp, err := (&http.Client{Timeout: time.Second}).Get(url + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

func TestInvalidConfig(t *testing.T) {
	for _, mutate := range []func(*Config){func(c *Config) { c.MaxConnections = 0 }, func(c *Config) { c.ClientBytesPerSecond = 0 }, func(c *Config) { c.ReadHeaderTimeout = 0 }, func(c *Config) { c.IdleTimeout = 0 }, func(c *Config) { c.ShutdownTimeout = 0 }, func(c *Config) { c.CertFile = "missing" }} {
		cfg := DefaultConfig()
		mutate(&cfg)
		if _, err := New(cfg); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
}

func TestTLSListenerAndHijackedShutdown(t *testing.T) {
	// Borrow Go's test-only certificate; trust it explicitly on the client.
	fixture := httptest.NewTLSServer(http.NotFoundHandler())
	cert := fixture.TLS.Certificates[0]
	transport := fixture.Client().Transport.(*http.Transport).Clone()
	fixture.Close()
	defer transport.CloseIdleConnections()
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.CertFile = filepath.Join(dir, "cert.pem")
	cfg.KeyFile = filepath.Join(dir, "key.pem")
	key, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(cfg.CertFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(cfg.KeyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0600); err != nil {
		t.Fatal(err)
	}
	s, url, stop := startTestServer(t, cfg)
	defer stop()
	url = strings.Replace(url, "http:", "https:", 1)
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	response, err := client.Get(url + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.Status)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, url+"/relay", &websocket.DialOptions{HTTPClient: client, Subprotocols: []string{"iroh-relay-v2"}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	if _, _, err = ws.Read(ctx); err != nil {
		t.Fatal(err)
	}
	stop()
	if s.Connections() != 0 {
		t.Fatal("TLS connection leaked")
	}
	if _, _, err = ws.Read(ctx); err == nil {
		t.Fatal("TLS websocket survived shutdown")
	}
}

func TestShutdownAuthenticatedEndpoint(t *testing.T) {
	s, url, stop := startTestServer(t, DefaultConfig())
	defer stop()
	relayURL, err := netaddr.ParseRelayURL(url)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	lookup := &iroh.AddressLookupServices{}
	endpoint, err := iroh.Bind(ctx, iroh.WithRelayMode(relay.ModeCustomURLs(relayURL)), iroh.WithoutIPTransports(), iroh.WithoutNetReport(), iroh.WithAddressLookup(lookup))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdown, finish := context.WithTimeout(context.Background(), 3*time.Second)
		defer finish()
		_ = endpoint.Shutdown(shutdown)
	}()
	if err = endpoint.Online(ctx); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot()["clients_accepted"] == 0 {
		t.Fatal("endpoint was not authenticated")
	}
	stop()
	if s.Connections() != 0 {
		t.Fatal("authenticated endpoint connection leaked")
	}
}
