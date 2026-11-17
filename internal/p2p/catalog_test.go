package p2p

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sid9017/linkport/internal/relaycatalog"
	"github.com/Sid9017/linkport/internal/relayservice"

	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"
)

func TestManagedCatalogPersistenceAndPolicy(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	sign := func(version uint64, expires time.Time, urls ...string) []byte {
		t.Helper()
		raw, err := relaycatalog.Sign(relaycatalog.Manifest{Format: 1, Version: version, PublishedAt: now.Add(-time.Hour), ExpiresAt: expires, Relays: urls}, private)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	oldKey, oldBootstrap := relaycatalog.PublicKey, relaycatalog.Bootstrap
	relaycatalog.PublicKey = base64.StdEncoding.EncodeToString(public)
	relaycatalog.Bootstrap = sign(1, now.Add(time.Hour), "https://207.148.110.58")
	defer func() { relaycatalog.PublicKey, relaycatalog.Bootstrap = oldKey, oldBootstrap }()
	directory := t.TempDir()
	store, err := OpenStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	identity := store.secret().Public().String()
	if err := store.SaveConfig(NetworkConfig{RelayMode: "custom", RelayURLs: []string{"https://1.1.1.1"}, DiscoveryURL: "https://example.com"}); err != nil {
		t.Fatal(err)
	}
	store.Close()
	n, err := New(context.Background(), Options{Directory: directory, Disabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if n.Status().NodeID != identity || n.Status().Config.RelayMode != "default" || len(n.Status().Config.RelayURLs) != 0 || n.Status().Config.DiscoveryURL != "" {
		t.Fatal("legacy migration replaced identity or retained user infrastructure")
	}
	for _, cfg := range []NetworkConfig{{RelayMode: "custom", RelayURLs: []string{"https://1.1.1.1"}}, {RelayMode: "default", DiscoveryURL: "https://example.com"}, {RelayMode: "default", HostedRelay: relayservice.HostConfig{Enabled: true}}} {
		if err := n.SaveConfig(cfg); err == nil {
			t.Fatal("allowed user relay customization")
		}
	}
	if err := n.ConfigureHostedRelay(relayservice.HostConfig{Enabled: true}); err == nil {
		t.Fatal("allowed desktop to configure a public relay")
	}
	next := sign(2, now.Add(2*time.Hour), "https://1.1.1.1")
	if err := n.ApplyRelayCatalog(next); err != nil {
		t.Fatal(err)
	}
	if n.Status().RelayDirectoryVersion != 2 || n.Status().NodeID != identity {
		t.Fatal("directory update changed identity")
	}
	if err := n.ApplyRelayCatalog(next); err != nil {
		t.Fatal("identical update not idempotent", err)
	}
	for _, raw := range [][]byte{relaycatalog.Bootstrap, sign(2, now.Add(2*time.Hour), "https://8.8.8.8"), sign(3, now.Add(-time.Minute), "https://8.8.8.8"), []byte(`{}`)} {
		if err := n.ApplyRelayCatalog(raw); err == nil {
			t.Fatal("accepted rollback, version conflict, expiry or invalid signature")
		}
		if n.Status().RelayDirectoryVersion != 2 {
			t.Fatal("rejected update changed directory")
		}
	}
	// A peer's ticket cannot add an arbitrary relay outside the signed catalogue.
	id := n.store.secret().Public().EndpointID()
	unknown, _ := netaddr.ParseRelayURL("https://8.8.8.8")
	filtered := n.withRelayCandidates(netaddr.NewEndpointAddr(id).WithRelayURL(unknown))
	for _, addr := range filtered.Addrs() {
		if r, ok := addr.(netaddr.RelayAddr); ok && r.URL == unknown {
			t.Fatal("adopted unmaintained peer relay")
		}
	}
	n.Close()
	n, err = New(context.Background(), Options{Directory: directory, Disabled: true})
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	if n.Status().RelayDirectoryVersion != 2 {
		t.Fatal("restart lost directory rollback floor")
	}
	// Direct-only configuration accepts signed updates without activating a relay.
	if err := n.SaveConfig(NetworkConfig{RelayMode: "disabled"}); err != nil {
		t.Fatal(err)
	}
	if err := n.ApplyRelayCatalog(sign(3, now.Add(time.Hour), "https://8.8.8.8")); err != nil {
		t.Fatal(err)
	}
	if n.Status().Config.RelayMode != "disabled" || n.endpoint != nil {
		t.Fatal("directory update activated disabled networking")
	}
	persisted, err := os.ReadFile(filepath.Join(directory, "relay-directory.json"))
	if err != nil {
		t.Fatal(err)
	}
	if verified, err := relaycatalog.Verify(persisted, relaycatalog.PublicKey, time.Now()); err != nil || verified.Version() != 3 {
		t.Fatal("signed cache not persisted", err)
	}
	n.Close()
	relaycatalog.Bootstrap = sign(4, now.Add(time.Hour), "https://207.148.110.58")
	upgraded, err := New(context.Background(), Options{Directory: directory, Disabled: true})
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if upgraded.Status().RelayDirectoryVersion != 4 {
		t.Fatal("new application bootstrap did not supersede cached directory")
	}
	upgraded.catalogPath = filepath.Join(directory, "unavailable-parent", "relay-directory.json")
	if err := upgraded.ApplyRelayCatalog(sign(5, now.Add(time.Hour), "https://8.8.8.8")); err == nil {
		t.Fatal("cache write failure ignored")
	}
	if upgraded.Status().RelayDirectoryVersion != 4 {
		t.Fatal("cache failure replaced working directory")
	}

	// Route every relay connection to a refusing loopback proxy so a real relay
	// actor can be exercised without contacting any public relay or discovery.
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL)
	isolatedDir := t.TempDir()
	active, err := New(context.Background(), Options{Directory: isolatedDir, LocalOnly: true, bindOptions: []iroh.Option{
		iroh.WithRelayMode(managedRelayMode([]string{"https://207.148.110.58"})),
		iroh.WithProxy(func(*url.URL) (*url.URL, error) { return proxyURL, nil }),
		iroh.WithoutNetReport(), iroh.WithoutIPTransports(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close()
	active.catalog, err = relaycatalog.Verify(relaycatalog.Bootstrap, relaycatalog.PublicKey, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	active.localOnly = false
	active.catalogPath = filepath.Join(isolatedDir, "relay-directory.json")
	endpoint := active.endpoint
	if err := active.ApplyRelayCatalog(sign(5, now.Add(time.Hour), "https://1.1.1.1")); err != nil {
		t.Fatal(err)
	}
	if active.endpoint != endpoint {
		t.Fatal("catalog update rebuilt endpoint")
	}
	oldURL, _ := netaddr.ParseRelayURL("https://207.148.110.58")
	newURL, _ := netaddr.ParseRelayURL("https://1.1.1.1")
	if active.endpoint.RemoveRelay(oldURL) != nil {
		t.Fatal("retired relay remained configured")
	}
	if active.endpoint.RemoveRelay(newURL) == nil {
		t.Fatal("new relay not inserted")
	}
	if _, err := active.endpoint.InsertRelay(newURL, nil); err != nil {
		t.Fatal(err)
	}
	active.catalogPath = filepath.Join(isolatedDir, "missing-parent", "relay-directory.json")
	if err := active.ApplyRelayCatalog(sign(6, now.Add(time.Hour), "https://8.8.8.8")); err == nil {
		t.Fatal("active update ignored persistence failure")
	}
	rejectedURL, _ := netaddr.ParseRelayURL("https://8.8.8.8")
	if active.endpoint.RemoveRelay(rejectedURL) != nil || active.Status().RelayDirectoryVersion != 5 {
		t.Fatal("failed active update did not roll back")
	}
	active.endpoint.RemoveRelay(newURL)
	active.failedRelays = map[string]time.Time{newURL.String(): now.Add(time.Minute)}
	active.catalogPath = filepath.Join(isolatedDir, "relay-directory.json")
	if err := active.ApplyRelayCatalog(sign(6, now.Add(time.Hour), "https://1.1.1.1")); err != nil {
		t.Fatal(err)
	}
	if active.endpoint.RemoveRelay(newURL) == nil {
		t.Fatal("updated directory lost a parked relay")
	}

}
