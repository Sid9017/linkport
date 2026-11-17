package p2p

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/Sid9017/linkport/internal/relayservice"

	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"
	"github.com/tmc/go-iroh/relay"
)

type failoverRelay struct {
	url    netaddr.RelayURL
	server *relayservice.Server
	stop   func()
}

func startFailoverRelay(t *testing.T) failoverRelay {
	t.Helper()
	server, err := relayservice.New(relayservice.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url, err := netaddr.ParseRelayURL("http://" + ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, ln) }()
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
				t.Error("relay shutdown timed out")
			}
		})
	}
	t.Cleanup(stop)
	return failoverRelay{url: url, server: server, stop: stop}
}

func TestRelayFailureRecoversWithoutEndpointRestart(t *testing.T) {
	relays := []failoverRelay{startFailoverRelay(t), startFailoverRelay(t)}
	sort.Slice(relays, func(i, j int) bool { return relays[i].url.String() < relays[j].url.String() })
	urls := []netaddr.RelayURL{relays[0].url, relays[1].url}
	create := func() *Network {
		dir := t.TempDir()
		store, err := OpenStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		err = store.SaveConfig(NetworkConfig{RelayMode: "custom", RelayURLs: []string{urls[0].String(), urls[1].String()}})
		_ = store.Close()
		if err != nil {
			t.Fatal(err)
		}
		n, err := New(context.Background(), Options{Directory: dir, LocalOnly: true, bindOptions: []iroh.Option{
			iroh.WithRelayMode(relay.ModeCustomURLs(urls...)), iroh.WithoutIPTransports(), iroh.WithoutNetReport(),
			iroh.WithTransportConfig(&iroh.QUICTransportConfig{KeepAlivePeriod: 500 * time.Millisecond, MaxIdleTimeout: 3 * time.Second, MaxIncomingStreams: 128}),
		}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(n.Close)
		go n.Serve(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "read failed", 400)
				return
			}
			_, _ = w.Write(body)
		}))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err = n.endpoint.Online(ctx); err != nil {
			t.Fatal(err)
		}
		return n
	}
	a, b := create(), create()
	pairDiscoveryNodes(t, a, b)
	identity := a.Status().NodeID
	payload := bytes.Repeat([]byte{0, 0xff, 0x8e, 0x0d, 0x0a}, 1024)
	roundTrip := func() error {
		client := b.NewClient(identity, 2*time.Second)
		defer client.CloseIdleConnections()
		response, err := client.Post("http://"+identity+"/v1/info", "application/octet-stream", bytes.NewReader(payload))
		if err != nil {
			return err
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			return err
		}
		if response.StatusCode != 200 || !bytes.Equal(body, payload) {
			return io.ErrUnexpectedEOF
		}
		return nil
	}
	if err := roundTrip(); err != nil {
		t.Fatal(err)
	}
	if relays[0].server.Snapshot()["datagrams_forwarded"] == 0 {
		t.Fatal("primary relay did not carry initial traffic")
	}
	relays[0].stop() // Real listener and authenticated sockets disappear.
	deadline := time.Now().Add(15 * time.Second)
	var last error
	recovered := false
	for time.Now().Before(deadline) {
		as, bs := a.Status(), b.Status()
		if as.HomeRelay == urls[1].String() && bs.HomeRelay == urls[1].String() && as.RelayConnected && bs.RelayConnected {
			if last = roundTrip(); last == nil {
				recovered = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !recovered {
		t.Fatalf("secondary relay could not carry new requests: %v", last)
	}
	if relays[1].server.Snapshot()["datagrams_forwarded"] == 0 {
		t.Fatalf("no fallback traffic; A=%+v B=%+v", a.Status(), b.Status())
	}
	if a.Status().NodeID != identity || a.Status().RestartRequired || !a.Trusted(b.self().ID) {
		t.Fatal("relay recovery changed identity or pairing")
	}
	// Even after recovery, revocation still denies traffic over the alternate relay.
	if err := a.RemoveDevice(b.self().ID); err != nil {
		t.Fatal(err)
	}
	if err := roundTrip(); err == nil {
		t.Fatal("revoked peer regained access on fallback relay")
	}
}
