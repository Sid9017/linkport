package relayservice

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestHostLifecycle(t *testing.T) {
	var absent *Host
	if absent.Status().Running || absent.Close() != nil {
		t.Fatal("nil host is not stopped")
	}
	h, err := StartHost(context.Background(), HostConfig{Enabled: true, Listen: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	status := h.Status()
	if !status.Running || status.Address == "" {
		t.Fatal("host did not expose its listener")
	}
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	response, err := client.Get("http://" + status.Address + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.Status)
	}
	if err = h.Close(); err != nil {
		t.Fatal(err)
	}
	if h.Status().Running || h.Status().Error != "" {
		t.Fatal("host did not terminate cleanly")
	}
	if err = h.Close(); err != nil {
		t.Fatal("second close failed", err)
	}
}

func TestHostRejectsInvalidStartup(t *testing.T) {
	for _, cfg := range []HostConfig{
		{},
		{Enabled: true, Listen: "invalid"},
		{Enabled: true, Listen: "0.0.0.0:0"},
		{Enabled: true, Listen: "localhost:0"},
		{Enabled: true, Listen: "127.0.0.1:0", TLSCert: "missing"},
		{Enabled: true, Listen: "127.0.0.1:0", TLSCert: "missing", TLSKey: "missing"},
		{Enabled: true, Listen: "127.0.0.1:0", MaxConnections: -1},
	} {
		if h, err := StartHost(context.Background(), cfg); err == nil {
			h.Close()
			t.Fatalf("accepted invalid config %+v", cfg)
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if h, err := StartHost(context.Background(), HostConfig{Enabled: true, Listen: ln.Addr().String()}); err == nil {
		h.Close()
		t.Fatal("bound an occupied port")
	}
}
