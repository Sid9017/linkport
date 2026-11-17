package p2p

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/go-iroh/endpointticket"
	"github.com/tmc/go-iroh/key"
	"github.com/tmc/go-iroh/netaddr"
)

// Manual workers make record propagation deterministic while exercising the
// actual challenge, QUIC identity, authorization and discovery HTTP handlers.
func manualDiscoveryNetwork(t *testing.T, dir string) *Network {
	t.Helper()
	if dir == "" {
		dir = t.TempDir()
	}
	n, err := New(context.Background(), Options{Directory: dir, LocalOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: n.Authorize(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") })), ConnContext: n.ConnContext}
	go func() { _ = server.Serve(n.Listener()) }()
	t.Cleanup(func() { _ = server.Close(); n.Close() })
	return n
}

func pairDiscoveryNodes(t *testing.T, a, b *Network) {
	t.Helper()
	invite, err := a.CreateInvitation()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err = b.AddDevice(ctx, invite.Code); err != nil {
		t.Fatal(err)
	}
}

func TestAddressRecordSignatureAndBoundaries(t *testing.T) {
	secret, err := key.GenerateSecretKey()
	if err != nil {
		t.Fatal(err)
	}
	id := secret.Public().EndpointID()
	ticket := endpointticket.Encode(netaddr.NewEndpointAddr(id).WithIP(netip.MustParseAddrPort("127.0.0.1:1234")))
	base := AddressRecord{Version: 1, ID: id.String(), Sequence: 1, ExpiresAt: time.Now().UTC().Add(time.Hour), Ticket: ticket}
	base.Signature = secret.Sign(base.signingBytes()).String()
	if err = base.verify(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*AddressRecord){
		"tamper":    func(r *AddressRecord) { r.Sequence++ },
		"signature": func(r *AddressRecord) { r.Signature = strings.Repeat("0", 128) },
		"version":   func(r *AddressRecord) { r.Version = 2 },
		"ticket":    func(r *AddressRecord) { r.Ticket = "invalid" },
		"identity":  func(r *AddressRecord) { r.ID = strings.Repeat("z", 64) },
		"oversize":  func(r *AddressRecord) { r.Ticket = strings.Repeat("a", 8193) },
	} {
		t.Run(name, func(t *testing.T) {
			r := base
			mutate(&r)
			if r.verify() == nil {
				t.Fatal("invalid record accepted")
			}
		})
	}
}

func TestPeerAssistedDiscoveryAfterAddressChangeAndRestart(t *testing.T) {
	a, b := manualDiscoveryNetwork(t, ""), manualDiscoveryNetwork(t, "")
	dir := t.TempDir()
	c := manualDiscoveryNetwork(t, dir)
	pairDiscoveryNodes(t, a, b)
	pairDiscoveryNodes(t, b, c)
	pairDiscoveryNodes(t, a, c)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := a.SyncDiscovery(ctx, c.self().ID, ""); err != nil {
		t.Fatal(err)
	}
	old, _ := a.store.record(c.self().ID)
	cID := c.self().ID
	c.Close()
	replacement := manualDiscoveryNetwork(t, dir)
	if err := replacement.SyncDiscovery(ctx, b.self().ID, ""); err != nil {
		t.Fatal(err)
	}
	fresh, _ := b.store.record(cID)
	if fresh.Sequence <= old.Sequence || fresh.Ticket == old.Ticket {
		t.Fatal("address change did not advance the signed record")
	}
	// A still has the old ticket. Dial must query B, learn C's new contact and
	// authenticate C's original public key, without a public discovery server.
	conn, err := a.Dial(ctx, cID)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	learned, _ := a.store.record(cID)
	if learned != fresh {
		t.Fatal("did not learn the latest record from the other desktop")
	}
	aDir := filepath.Dir(a.store.path)
	a.Close()
	reopened, err := OpenStore(aDir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got, ok := reopened.record(cID); !ok || got != fresh {
		t.Fatal("discovery cache did not survive restart")
	}
	encoded, _ := json.Marshal(learned)
	if strings.Contains(string(encoded), "seed") || strings.Contains(string(encoded), "token") {
		t.Fatal("record exposes private identity or invitation")
	}
}

func TestDiscoveryRollbackExpiryAndRevocation(t *testing.T) {
	a, b := manualDiscoveryNetwork(t, ""), manualDiscoveryNetwork(t, "")
	pairDiscoveryNodes(t, a, b)
	record, err := b.ownRecord()
	if err != nil {
		t.Fatal(err)
	}
	if err = a.mergeRecord(record); err != nil {
		t.Fatal(err)
	}
	sign := func(r AddressRecord) AddressRecord {
		r.Signature = b.store.secret().Sign(r.signingBytes()).String()
		return r
	}
	next := record
	next.Sequence++
	next.ExpiresAt = next.ExpiresAt.Add(time.Second)
	next = sign(next)
	if err = a.mergeRecord(next); err != nil {
		t.Fatal(err)
	}
	if err = a.mergeRecord(record); err == nil {
		t.Fatal("rollback accepted")
	}
	conflict := next
	conflict.HostedRelayURL = "https://relay.example.com"
	conflict = sign(conflict)
	if err = a.mergeRecord(conflict); err == nil {
		t.Fatal("conflicting version accepted")
	}
	expired := next
	expired.Sequence++
	expired.ExpiresAt = time.Now().Add(-time.Second)
	expired = sign(expired)
	if err = a.mergeRecord(expired); err == nil {
		t.Fatal("expired record accepted")
	}
	future := next
	future.Sequence++
	future.ExpiresAt = time.Now().Add(3 * time.Hour)
	future = sign(future)
	if err = a.mergeRecord(future); err == nil {
		t.Fatal("unbounded lifetime accepted")
	}
	if err = a.RemoveDevice(b.self().ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.store.record(b.self().ID); ok {
		t.Fatal("removed peer retained discovery record")
	}
	if err = a.mergeRecord(next); err == nil {
		t.Fatal("in-flight record restored a revoked peer")
	}
	if err = a.SyncDiscovery(context.Background(), b.self().ID, ""); err == nil {
		t.Fatal("queried a removed peer")
	}
}

func TestBackgroundDiscoveryAndShutdown(t *testing.T) {
	a, b := testNetwork(t), testNetwork(t)
	pairDiscoveryNodes(t, a, b)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := a.store.record(b.self().ID); ok {
			a.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("pairing did not trigger background discovery exchange")
}

func TestDiscoveryDoesNotGrantTrust(t *testing.T) {
	a, b, c := manualDiscoveryNetwork(t, ""), manualDiscoveryNetwork(t, ""), manualDiscoveryNetwork(t, "")
	pairDiscoveryNodes(t, a, b)
	pairDiscoveryNodes(t, b, c)
	if err := a.SyncDiscovery(context.Background(), b.self().ID, c.self().ID); err == nil {
		t.Fatal("queried an unpaired subject")
	}
	record, err := c.ownRecord()
	if err != nil {
		t.Fatal(err)
	}
	if err = a.mergeRecord(record); err == nil || a.Trusted(c.self().ID) {
		t.Fatal("record granted transitive trust")
	}
}

func TestDiscoveryHTTPRejectsMalformedRecords(t *testing.T) {
	a, b := manualDiscoveryNetwork(t, ""), manualDiscoveryNetwork(t, "")
	pairDiscoveryNodes(t, a, b)
	record, err := a.ownRecord()
	if err != nil {
		t.Fatal(err)
	}
	client := a.NewClient(b.self().ID, 3*time.Second)
	defer client.CloseIdleConnections()
	good, _ := json.Marshal(discoveryRequest{Record: record})
	wrongID := record
	wrongID.ID = b.self().ID
	mismatch, _ := json.Marshal(discoveryRequest{Record: wrongID})
	tampered := record
	tampered.Sequence++
	invalidSig, _ := json.Marshal(discoveryRequest{Record: tampered})
	badWant, _ := json.Marshal(discoveryRequest{Record: record, Want: "invalid"})
	for _, tc := range []struct {
		name, method string
		body         []byte
		status       int
	}{
		{"valid", http.MethodPost, good, 200},
		{"method", http.MethodGet, nil, 405},
		{"json", http.MethodPost, []byte(`{"unknown":1}`), 400},
		{"multiple", http.MethodPost, append(append([]byte{}, good...), []byte("{}")...), 400},
		{"identity", http.MethodPost, mismatch, 400},
		{"signature", http.MethodPost, invalidSig, 403},
		{"subject", http.MethodPost, badWant, 400},
		{"oversize", http.MethodPost, []byte(`{"record":{"ticket":"` + strings.Repeat("a", discoveryLimit) + `"}}`), 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(tc.method, "http://"+b.self().ID+"/v1/discovery", bytes.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.status {
				t.Fatalf("status=%d want=%d", resp.StatusCode, tc.status)
			}
		})
	}
}
