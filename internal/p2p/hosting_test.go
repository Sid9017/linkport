package p2p

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Sid9017/linkport/internal/relayservice"
)

func TestEmbeddedRelayLifecycleAndPersistence(t *testing.T) {
	dir := t.TempDir()
	n := manualDiscoveryNetwork(t, dir)
	cfg := relayservice.HostConfig{Enabled: true, Listen: "127.0.0.1:0", MaxConnections: 16}
	if err := n.ConfigureHostedRelay(cfg); err != nil {
		t.Fatal(err)
	}
	address := n.Status().HostedRelay.Address
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	response, err := client.Get("http://" + address + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.Status)
	}
	if err = n.ConfigureHostedRelay(cfg); err != nil {
		t.Fatal(err)
	}
	if err = n.ConfigureHostedRelay(relayservice.HostConfig{Enabled: true, Listen: "127.0.0.1:1"}); err == nil {
		t.Fatal("silently rebound an active relay")
	}
	n.Close()
	restarted, err := New(context.Background(), Options{Directory: dir, LocalOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if !restarted.Status().HostedRelay.Running {
		t.Fatal("opt-in hosting did not survive restart")
	}
	if err = restarted.ConfigureHostedRelay(relayservice.HostConfig{}); err != nil {
		t.Fatal(err)
	}
	if restarted.Status().HostedRelay.Running || restarted.store.Config().HostedRelay.Enabled {
		t.Fatal("hosting did not stop")
	}
	if err = restarted.ConfigureHostedRelay(relayservice.HostConfig{Enabled: true, Listen: "0.0.0.0:3340"}); err == nil {
		t.Fatal("public plaintext relay accepted")
	}
}
