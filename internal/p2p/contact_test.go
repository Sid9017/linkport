package p2p

import (
	"context"
	"io"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"github.com/tmc/go-iroh/endpointticket"
	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/key"
	"github.com/tmc/go-iroh/netaddr"
	"github.com/tmc/go-iroh/relay"
)

func TestContactCandidatesPreserveIdentityAndRelay(t *testing.T) {
	secret, err := key.GenerateSecretKey()
	if err != nil {
		t.Fatal(err)
	}
	id := secret.Public().EndpointID()
	relay, _ := netaddr.ParseRelayURL("https://relay.example")
	initial := netaddr.NewEndpointAddr(id).WithRelayURL(relay)
	ip := func(value string) netip.Addr { return netip.MustParseAddr(value) }
	bound := netip.MustParseAddrPort("[::]:4321")
	result := withContactIPs(initial, bound, []netip.Addr{
		ip("192.168.31.20"), ip("192.168.31.20"), ip("fd00::20"),
		ip("127.0.0.1"), ip("::1"), ip("0.0.0.0"), ip("::"),
		ip("169.254.1.1"), ip("fe80::1"), ip("224.0.0.1"),
	})
	if result.ID != id || len(result.RelayURLs()) != 1 || result.RelayURLs()[0] != relay {
		t.Fatal("contact candidates changed authenticated identity or relay")
	}
	got := result.IPAddrs()
	if len(got) != 2 || got[0] != netip.MustParseAddrPort("192.168.31.20:4321") ||
		got[1] != netip.MustParseAddrPort("[fd00::20]:4321") {
		t.Fatalf("unexpected LAN candidates: %v", got)
	}
	v4 := withContactIPs(initial, netip.MustParseAddrPort("0.0.0.0:4321"), []netip.Addr{ip("fd00::20"), ip("192.168.31.20")})
	if len(v4.IPAddrs()) != 1 || !v4.IPAddrs()[0].Addr().Is4() {
		t.Fatal("IPv4-only socket advertised an IPv6 candidate")
	}
	if got := withContactIPs(initial, netip.MustParseAddrPort("0.0.0.0:0"), []netip.Addr{ip("192.168.31.20")}); len(got.IPAddrs()) != 0 {
		t.Fatal("advertised a zero-port candidate")
	}
}

func TestContactCandidateLimit(t *testing.T) {
	secret, err := key.GenerateSecretKey()
	if err != nil {
		t.Fatal(err)
	}
	address := netaddr.NewEndpointAddr(secret.Public().EndpointID())
	var ips []netip.Addr
	for i := byte(1); i <= 40; i++ {
		ips = append(ips, netip.AddrFrom4([4]byte{192, 168, 31, i}))
	}
	result := withContactIPs(address, netip.MustParseAddrPort("0.0.0.0:4321"), ips)
	if len(result.Addrs()) != 32 {
		t.Fatal("contact candidates exceeded the peer address limit")
	}
}

func TestWildcardBoundEndpointsPairWithoutRelay(t *testing.T) {
	newEndpoint := func() *Network {
		n, err := New(t.Context(), Options{
			Directory: t.TempDir(), LocalOnly: true,
			bindOptions: []iroh.Option{
				iroh.WithBindAddr(netip.MustParseAddrPort("0.0.0.0:0")),
				iroh.WithRelayMode(relay.ModeDisabled()), iroh.WithoutNetReport(),
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(n.Close)
		// Exercise production contact enumeration without enabling public
		// discovery, STUN, or relay connections in this test.
		n.localOnly = false
		return n
	}
	a, b := newEndpoint(), newEndpoint()
	invite, err := a.CreateInvitation()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := parseInvitation(invite.Code)
	if err != nil {
		t.Fatal(err)
	}
	addr, err := endpointticket.Decode(payload.Peer.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	if len(addr.IPAddrs()) == 0 {
		t.Skip("no active LAN interface available; candidate filtering is tested separately")
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "authenticated direct connection")
	})
	go a.Serve(handler)
	go b.Serve(handler)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := b.AddDevice(ctx, invite.Code); err != nil {
		t.Fatalf("wildcard-bound endpoints could not pair directly: %v", err)
	}
	client := b.NewClient(payload.Peer.ID, 5*time.Second)
	defer client.CloseIdleConnections()
	response, err := client.Get("http://" + payload.Peer.ID + "/v1/info")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || string(body) != "authenticated direct connection" {
		t.Fatal("direct connection did not carry the authenticated request")
	}
}
