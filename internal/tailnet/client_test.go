package tailnet

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func fakeClient(output string, commandError error) *Client {
	return &Client{
		Binary: func() (string, error) { return "tailscale-test", nil },
		Run:    func(context.Context, string, ...string) ([]byte, error) { return []byte(output), commandError },
	}
}

func TestStatusUsesActualPeerIDsAndTailnetAddresses(t *testing.T) {
	client := fakeClient(`{
		"BackendState":"Running","TailscaleIPs":["fd7a:115c:a1e0::1","100.64.0.1","192.168.1.8"],
		"Self":{"ID":"self","HostName":"workstation","DNSName":"workstation.example.ts.net."},
		"Peer":{
			"key-a":{"ID":"remote-a","HostName":"a","Online":true,"OS":"linux","TailscaleIPs":["100.64.0.2","127.0.0.1"]},
			"key-b":{"ID":"remote-b","HostName":"b","Online":false,"TailscaleIPs":["100.64.0.3"]}
		},"AuthURL":"https://login.tailscale.com/a/secret"
	}`, nil)
	status := client.Status(context.Background())
	if status.NodeID != "self" || status.HostName != "workstation" || status.DNSName != "workstation.example.ts.net" {
		t.Fatalf("unexpected self: %+v", status)
	}
	if len(status.IPs) != 2 || status.IPs[0] != "100.64.0.1" || len(status.Peers) != 2 || status.Peers[0].ID != "remote-a" || len(status.Peers[0].IPs) != 1 {
		t.Fatalf("unexpected devices: %+v", status)
	}
	serialized, _ := json.Marshal(status)
	if strings.Contains(string(serialized), "secret") || strings.Contains(string(serialized), "127.0.0.1") {
		t.Fatal("status leaked auth URL or non-Tailscale peer address")
	}
}

func TestNeedsLoginCannotPublishStaleDevices(t *testing.T) {
	client := fakeClient(`{"BackendState":"NeedsLogin","TailscaleIPs":["100.64.0.1"],"Peer":{"key":{"ID":"stale","Online":true}}}`, errors.New("exit 1"))
	status := client.Status(context.Background())
	if status.State != "NeedsLogin" || len(status.Peers) != 0 || len(status.IPs) != 0 {
		t.Fatalf("stale data after logout: %+v", status)
	}
}

func TestWhoIsRequiresMatchingNodeSourceAddress(t *testing.T) {
	client := fakeClient(`{"Node":{"StableID":"peer-id","Addresses":["100.64.0.2/32","fd7a:115c:a1e0::2/128"]}}`, nil)
	for _, remote := range []string{"100.64.0.2:3456", "[fd7a:115c:a1e0::2]:3456"} {
		if identity, err := client.WhoIs(context.Background(), remote); err != nil || identity != "peer-id" {
			t.Fatalf("WhoIs(%q): %q, %v", remote, identity, err)
		}
	}
	for _, remote := range []string{"127.0.0.1:10", "192.168.0.5:10", "100.64.0.3:10", "example.com:10"} {
		if _, err := client.WhoIs(context.Background(), remote); err == nil {
			t.Fatalf("accepted source %q", remote)
		}
	}
}

func TestMissingAndUnavailableTailscaleAreDistinct(t *testing.T) {
	client := &Client{Binary: func() (string, error) { return "", errors.New("missing") }}
	if status := client.Status(context.Background()); status.Installed || status.State != "NotInstalled" {
		t.Fatalf("missing installation: %+v", status)
	}
	client = fakeClient("not json", errors.New("unreachable"))
	if status := client.Status(context.Background()); !status.Installed || status.State != "Unavailable" || status.Error == "" {
		t.Fatalf("unavailable daemon: %+v", status)
	}
}
