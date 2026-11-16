package engine

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sid9017/linkport/internal/serialport"
	"github.com/Sid9017/linkport/internal/tailnet"
	"github.com/Sid9017/linkport/internal/vport"
)

type testTailnet struct {
	status    tailnet.Status
	authError error
}

func (network testTailnet) Status(context.Context) tailnet.Status { return network.status }
func (network testTailnet) WhoIs(context.Context, string) (string, error) {
	return "authenticated-node", network.authError
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func testEngine(t *testing.T, network testTailnet) *Engine {
	t.Helper()
	engine, err := New(context.Background(), Options{
		Directory: t.TempDir(), DeviceDirectory: t.TempDir(), Tailnet: network,
		DisableNetworkListener: true,
		Enumerator: serialport.EnumerateFunc(func(context.Context) ([]serialport.Port, error) {
			return []serialport.Port{{Path: "/dev/real-enumerated-port"}}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(engine.closeNetwork)
	t.Cleanup(engine.client.CloseIdleConnections)
	return engine
}

func TestLocalInventoryRequiresExplicitPersistentSharing(t *testing.T) {
	engine := testEngine(t, testTailnet{})
	if err := engine.manager.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := engine.State()
	if len(state.LocalPorts) != 1 || state.LocalPorts[0].Shared || len(state.SharedPorts) != 0 || len(state.Sessions) != 0 {
		t.Fatalf("unexpected initial state: %+v", state)
	}
	identity := state.LocalPorts[0].ID
	if err := engine.SetShared(context.Background(), identity, true); err != nil {
		t.Fatal(err)
	}
	if state := engine.State(); !state.LocalPorts[0].Shared || len(state.SharedPorts) != 1 {
		t.Fatalf("opt-in not applied: %+v", state)
	}
	if err := engine.SetShared(context.Background(), identity, false); err != nil {
		t.Fatal(err)
	}
	if len(engine.State().SharedPorts) != 0 {
		t.Fatal("unshare did not remove advertised port")
	}
}

func TestDiscoveryChecksNodeIdentityAndNeverFollowsArbitraryAddresses(t *testing.T) {
	engine := testEngine(t, testTailnet{})
	peer := tailnet.Peer{ID: "expected-peer", HostName: "real-node", IPs: []string{"127.0.0.1", "100.64.0.2"}, Online: true}
	for _, identity := range []string{"wrong-node", "expected-peer"} {
		requests := 0
		engine.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
			requests++
			if request.URL.Hostname() != "100.64.0.2" {
				t.Errorf("probed non-Tailscale address %q", request.URL.Hostname())
			}
			body := `{"revision":1,"ports":[{"id":"port","path":"/dev/ttyUSB0","online":true}]}`
			if request.URL.Path == "/v1/info" {
				body = `{"node_id":"` + identity + `","api_version":"v1","version":"0.0.1","remote_data_bridge":true}`
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		})
		device := engine.probe(context.Background(), peer)
		if identity == "wrong-node" {
			if device.Service != "incompatible" || len(device.Ports) != 0 || requests != 1 {
				t.Fatalf("accepted identity mismatch: %+v", device)
			}
		} else if device.Service != "available" || len(device.Ports) != 1 || !device.Bridge || requests != 2 {
			t.Fatalf("discovery failed: %+v", device)
		}
	}
}

func TestIncomingPeerMustAuthenticateAndBrowserOriginIsRejected(t *testing.T) {
	for _, authError := range []error{nil, errors.New("not authenticated")} {
		engine := testEngine(t, testTailnet{authError: authError})
		handler := engine.authenticate(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { writer.WriteHeader(http.StatusNoContent) }))
		for _, origin := range []string{"", "https://unrelated.example"} {
			request := httptest.NewRequest(http.MethodGet, "/v1/ports", nil)
			request.RemoteAddr = "100.64.0.2:1234"
			request.Header.Set("Origin", origin)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			expected := http.StatusNoContent
			if origin != "" || authError != nil {
				expected = http.StatusForbidden
			}
			if response.Code != expected {
				t.Fatalf("got %d, want %d", response.Code, expected)
			}
		}
	}
}

func TestConnectRejectsDeviceOutsideCatalog(t *testing.T) {
	engine := testEngine(t, testTailnet{})
	engine.state.Tailscale.State = "Running"
	if _, err := engine.Connect(context.Background(), "injected-host", "port", 115200, vport.Options{}); err == nil {
		t.Fatal("accepted target outside actual peer catalog")
	}
}

func TestTemporaryStatusFailurePreservesKnownNetworkButBlocksNewSessions(t *testing.T) {
	engine := testEngine(t, testTailnet{status: tailnet.Status{Installed: true, State: "Unavailable", Error: "query timed out"}})
	engine.nodeID = "self"
	engine.state.Tailscale = tailnet.Status{Installed: true, State: "Running", NodeID: "self", HostName: "real-host", IPs: []string{"100.64.0.1"}, Peers: []tailnet.Peer{}}
	engine.update(context.Background())
	state := engine.State()
	if !state.Tailscale.Stale || state.Tailscale.State != "Running" || state.Tailscale.Error != "query timed out" || state.Tailscale.NodeID != "self" {
		t.Fatalf("temporary failure destroyed known network: %+v", state.Tailscale)
	}
	if _, err := engine.Connect(context.Background(), "peer", "port", 115200, vport.Options{}); err == nil || !strings.Contains(err.Error(), "暂时无法确认") {
		t.Fatalf("new connection allowed with unconfirmed directory: %v", err)
	}
}
