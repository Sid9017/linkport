package p2p

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tmc/go-iroh/endpointticket"
	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"
	"github.com/tmc/go-iroh/relay"
	"github.com/tmc/go-iroh/relayserver"
)

func testNetwork(t *testing.T) *Network {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	n, err := New(ctx, Options{Directory: t.TempDir(), LocalOnly: true})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); n.Close() })
	go n.Serve(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hold" {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "open\n")
			_ = http.NewResponseController(w).Flush()
			<-r.Context().Done()
			return
		}
		_, _ = io.WriteString(w, "paired device only")
	}))
	return n
}

func TestIdentityPersistenceLockAndPrivateStorage(t *testing.T) {
	dir := t.TempDir()
	n, err := New(context.Background(), Options{Directory: dir, Disabled: true})
	if err != nil {
		t.Fatal(err)
	}
	id := n.Status().NodeID
	if _, err := OpenStore(dir); err == nil {
		t.Fatal("another process/store can overwrite the active device identity")
	}
	file := filepath.Join(dir, "devices.json")
	contents, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var saved diskState
	if err := json.Unmarshal(contents, &saved); err != nil {
		t.Fatal(err)
	}
	public, _ := json.Marshal(n.Status())
	if strings.Contains(string(public), saved.Seed) || strings.Contains(string(public), `"seed"`) {
		t.Fatal("public status exposes the private identity")
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(file)
		if info.Mode().Perm() != 0600 {
			t.Fatalf("identity permissions: %v", info.Mode())
		}
	}
	n.Close()
	reopened, err := New(context.Background(), Options{Directory: dir, Disabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Status().NodeID != id {
		t.Fatal("restarting changed the device identity")
	}
	if err := reopened.ResetIdentity("incorrect"); err == nil {
		t.Fatal("identity reset accepted without full ID confirmation")
	}
	if err := reopened.ResetIdentity(id); err != nil {
		t.Fatal(err)
	}
	if reopened.Status().NodeID == id || !reopened.Status().RestartRequired {
		t.Fatal("identity reset did not replace the key and require restart")
	}
	reopened.Close()
	if err := os.WriteFile(file, []byte(`{"version":1,"seed":"corrupt"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(dir); err == nil {
		t.Fatal("corrupt identity silently replaced")
	}
	contents, _ = os.ReadFile(file)
	if !strings.Contains(string(contents), "corrupt") {
		t.Fatal("corrupt identity was overwritten")
	}
}

func TestPairingRequiresInvitationAndPinsBothPublicKeys(t *testing.T) {
	a, b := testNetwork(t), testNetwork(t)
	invite, err := a.CreateInvitation()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := parseInvitation(invite.Code)
	if err != nil {
		t.Fatal(err)
	}
	// An invitation permits a handshake, never an unauthenticated inventory read.
	b.mu.Lock()
	b.pairing[payload.Peer.ID]++
	b.mu.Unlock()
	client := b.NewClient(payload.Peer.ID, 5*time.Second)
	addr, _ := endpointticket.Decode(payload.Peer.Ticket)
	client.Transport.(*http.Transport).DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return b.dial(ctx, addr)
	}
	resp, err := client.Get("http://" + payload.Peer.ID + "/v1/ports")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	client.CloseIdleConnections()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatal("unpaired device read the shared inventory")
	}
	b.mu.Lock()
	delete(b.pairing, payload.Peer.ID)
	b.mu.Unlock()
	peer, err := b.AddDevice(context.Background(), invite.Code)
	if err != nil {
		t.Fatal(err)
	}
	if peer.ID != a.Status().NodeID || !a.Trusted(b.Status().NodeID) || !b.Trusted(a.Status().NodeID) {
		t.Fatal("pairing did not persist mutual authenticated identities")
	}
	if _, err := b.AddDevice(context.Background(), invite.Code); err == nil {
		t.Fatal("single-use invitation was replayed")
	}
	client = b.NewClient(peer.ID, 5*time.Second)
	defer client.CloseIdleConnections()
	response, err := client.Get("http://" + peer.ID + "/v1/ports")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "paired device only" {
		t.Fatalf("paired encrypted stream failed: %d %s", response.StatusCode, body)
	}
	if _, err := client.Get("http://127.0.0.1:1/"); err == nil {
		t.Fatal("paired-device client accepted an arbitrary host")
	}
}

func TestRevocationTerminatesActiveStreamsAndBlocksReconnect(t *testing.T) {
	a, b := testNetwork(t), testNetwork(t)
	invite, _ := a.CreateInvitation()
	peer, err := b.AddDevice(context.Background(), invite.Code)
	if err != nil {
		t.Fatal(err)
	}
	client := b.NewClient(peer.ID, 5*time.Second)
	defer client.CloseIdleConnections()
	response, err := client.Get("http://" + peer.ID + "/hold")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.RemoveDevice(b.Status().NodeID); err != nil {
		t.Fatal(err)
	}
	_, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr == nil {
		t.Fatal("revocation left an active stream usable")
	}
	if a.Trusted(b.Status().NodeID) {
		t.Fatal("revoked peer remains trusted")
	}
	if resp, err := client.Get("http://" + peer.ID + "/v1/ports"); err == nil {
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Fatal("revoked device reconnected successfully")
		}
	}
}

func TestInvitationExpirationReplacementAndTampering(t *testing.T) {
	a, b := testNetwork(t), testNetwork(t)
	first, _ := a.CreateInvitation()
	payload, _ := parseInvitation(first.Code)
	encode := func(payload invitePayload) string {
		data, _ := json.Marshal(payload)
		return "linkport1." + base64.RawURLEncoding.EncodeToString(data)
	}
	expired := payload
	expired.Expires = time.Now().Add(-time.Minute)
	if _, err := b.AddDevice(context.Background(), encode(expired)); err == nil {
		t.Fatal("expired invitation accepted")
	}
	tampered := payload
	tampered.Token = base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	if _, err := b.AddDevice(context.Background(), encode(tampered)); err == nil {
		t.Fatal("forged invitation token accepted")
	}
	second, _ := a.CreateInvitation()
	if _, err := b.AddDevice(context.Background(), first.Code); err == nil {
		t.Fatal("replacement did not revoke the old invitation")
	}
	if _, err := b.AddDevice(context.Background(), second.Code); err != nil {
		t.Fatal(err)
	}
}

func TestNetworkSettingsValidationAndPersistence(t *testing.T) {
	for _, cfg := range []NetworkConfig{
		{RelayMode: "unknown"}, {RelayMode: "custom"},
		{RelayMode: "custom", RelayURLs: []string{"http://public.example"}},
		{RelayMode: "custom", RelayURLs: []string{"https://user:secret@example.com"}},
	} {
		if err := validateNetwork(cfg); err == nil {
			t.Fatalf("accepted invalid network configuration: %+v", cfg)
		}
	}
	n := testNetwork(t)
	if err := n.SaveConfig(NetworkConfig{RelayMode: "disabled", RelayURLs: []string{}}); err != nil {
		t.Fatal(err)
	}
	if !n.Status().RestartRequired || n.store.Config().RelayMode != "disabled" {
		t.Fatal("network configuration was not saved")
	}
	if _, err := n.CreateInvitation(); err == nil {
		t.Fatal("stopped network issued a usable invitation")
	}
}

func TestPairingAndDataWorkWithRelayOnly(t *testing.T) {
	relayHandler := relayserver.New()
	server := httptest.NewServer(relayHandler)
	defer server.Close()
	url, err := netaddr.ParseRelayURL(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	create := func() *Network {
		n, err := New(ctx, Options{
			Directory: t.TempDir(), LocalOnly: true,
			bindOptions: []iroh.Option{
				iroh.WithRelayMode(relay.ModeCustomURLs(url)), iroh.WithoutIPTransports(), iroh.WithoutNetReport(),
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(n.Close)
		go n.Serve(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "encrypted relay round trip")
		}))
		if err := n.endpoint.Online(ctx); err != nil {
			t.Fatal(err)
		}
		return n
	}
	a, b := create(), create()
	invite, err := a.CreateInvitation()
	if err != nil {
		t.Fatal(err)
	}
	peer, err := b.AddDevice(ctx, invite.Code)
	if err != nil {
		t.Fatal(err)
	}
	client := b.NewClient(peer.ID, 5*time.Second)
	defer client.CloseIdleConnections()
	response, err := client.Get("http://" + peer.ID + "/v1/info")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(body) != "encrypted relay round trip" || relayHandler.Snapshot()["datagrams_forwarded"] == 0 {
		t.Fatalf("did not transfer over relay: %s", body)
	}
}

func TestOldStreamChallengeCannotReplayBusinessRequest(t *testing.T) {
	a, b := testNetwork(t), testNetwork(t)
	invite, _ := a.CreateInvitation()
	if _, err := b.AddDevice(context.Background(), invite.Code); err != nil {
		t.Fatal(err)
	}
	address := a.endpoint.Addr()
	conn, err := b.endpoint.Connect(context.Background(), address, ALPN)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	stream, err := conn.OpenStreamConn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	_ = stream.SetDeadline(time.Now().Add(5 * time.Second))
	// A replay contains an old challenge response, rather than the new nonce.
	replay := append([]byte("LP1\n"), make([]byte, 32)...)
	replay = append(replay, []byte("GET /v1/ports HTTP/1.1\r\nHost: device\r\n\r\n")...)
	if _, err := stream.Write(replay); err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(stream)
	if strings.Contains(string(body), "paired device only") {
		t.Fatal("old stream data reached a business handler")
	}
}
