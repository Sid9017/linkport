package p2p

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"
	"github.com/tmc/go-iroh/relay"
)

type relayProcessResult struct {
	log string
	err error
}
type relayProcess struct {
	cmd     *exec.Cmd
	url     string
	done    chan relayProcessResult
	stopped bool
}

func buildRelayBinary(t *testing.T) string {
	t.Helper()
	name := "linkport-relay"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", binary, "../../cmd/linkport-relay")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build standalone relay: %v\n%s", err, output)
	}
	return binary
}

func startRelayProcess(t *testing.T, binary string, extra ...string) *relayProcess {
	t.Helper()
	args := append([]string{"--listen", "127.0.0.1:0"}, extra...)
	p := &relayProcess{cmd: exec.Command(binary, args...), done: make(chan relayProcessResult, 1)}
	stderr, err := p.cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stderr)
		var log strings.Builder
		first := true
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Fprintln(&log, line)
			if first {
				ready <- line
				first = false
			}
		}
		if first {
			ready <- ""
		}
		scanErr := scanner.Err()
		waitErr := p.cmd.Wait()
		if scanErr != nil {
			waitErr = fmt.Errorf("read relay output: %w (wait: %v)", scanErr, waitErr)
		}
		p.done <- relayProcessResult{log: log.String(), err: waitErr}
	}()
	t.Cleanup(func() {
		if p.stopped {
			return
		}
		_ = p.cmd.Process.Kill()
		select {
		case result := <-p.done:
			t.Logf("relay cleanup:\n%s", result.log)
		case <-time.After(5 * time.Second):
			t.Error("relay cleanup did not finish")
		}
	})
	select {
	case line := <-ready:
		const prefix = "linkport-relay listening on "
		if !strings.HasPrefix(line, prefix) {
			t.Fatalf("relay failed startup: %s", line)
		}
		address, _, ok := strings.Cut(strings.TrimPrefix(line, prefix), " ")
		if !ok {
			t.Fatalf("invalid relay startup: %s", line)
		}
		p.url = "http://" + address
	case <-time.After(10 * time.Second):
		t.Fatal("relay startup timed out")
	}
	return p
}

func (p *relayProcess) terminate(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Fatal("graceful process signals are not supported on Windows")
	}
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-p.done:
		p.stopped = true
		if result.err != nil {
			t.Fatalf("relay exit: %v\n%s", result.err, result.log)
		}
		if !strings.Contains(result.log, "connections=0") {
			t.Fatalf("relay retained connections:\n%s", result.log)
		}
		return result.log
	case <-time.After(12 * time.Second):
		t.Fatal("relay SIGTERM timed out")
	}
	return ""
}

func relayExitCounters(t *testing.T, log string) map[string]uint64 {
	t.Helper()
	_, raw, ok := strings.Cut(log, "counters=")
	if !ok {
		t.Fatalf("missing relay counters: %s", log)
	}
	var counters map[string]uint64
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &counters); err != nil {
		t.Fatalf("invalid relay counters: %v", err)
	}
	return counters
}

// This suite crosses the OS process boundary and uses only loopback endpoints.
// No user's config, serial hardware, public relay, or discovery is used.
func TestStandaloneRelayIntegration(t *testing.T) {
	binary := buildRelayBinary(t)
	t.Run("pairing-bytes-stream-isolation-revocation", func(t *testing.T) {
		p := startRelayProcess(t, binary)
		url, err := netaddr.ParseRelayURL(p.url)
		if err != nil {
			t.Fatal(err)
		}
		runRelayPairingScenario(t, url)
		if runtime.GOOS != "windows" {
			log := p.terminate(t)
			if counters := relayExitCounters(t, log); counters["datagrams_forwarded"] == 0 || counters["clients_accepted"] < 2 {
				t.Fatalf("no datagrams forwarded:\n%s", log)
			}
			t.Log(log)
		}
	})
	t.Run("health-and-http-boundaries", func(t *testing.T) {
		p := startRelayProcess(t, binary)
		client := &http.Client{Timeout: 3 * time.Second}
		defer client.CloseIdleConnections()
		for _, tc := range []struct {
			method, path string
			status       int
		}{{"GET", "/healthz", 200}, {"GET", "/ping", 200}, {"POST", "/healthz", 405}, {"GET", "/unknown", 404}, {"GET", "/metrics", 404}} {
			req, err := http.NewRequest(tc.method, p.url+tc.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil || resp.StatusCode != tc.status {
				t.Fatalf("%s %s: status=%d err=%v", tc.method, tc.path, resp.StatusCode, err)
			}
			if tc.method == "GET" && tc.path == "/healthz" && string(body) != "ok\n" {
				t.Fatalf("health body=%q", body)
			}
		}
		if runtime.GOOS != "windows" {
			p.terminate(t)
		}
	})
	t.Run("shutdown-closes-online-endpoints", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Windows does not support child-process SIGTERM; handler shutdown is covered on every platform")
		}
		p := startRelayProcess(t, binary)
		url, err := netaddr.ParseRelayURL(p.url)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var endpoints []*iroh.Endpoint
		for range 2 {
			lookup := &iroh.AddressLookupServices{}
			ep, err := iroh.Bind(ctx, iroh.WithALPNs(ALPN), iroh.WithRelayMode(relay.ModeCustomURLs(url)), iroh.WithAddressLookup(lookup), iroh.WithoutIPTransports(), iroh.WithoutNetReport())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_ = ep.Shutdown(ctx)
			})
			if err = ep.Online(ctx); err != nil {
				t.Fatal(err)
			}
			endpoints = append(endpoints, ep)
		}
		// Leave a real QUIC stream open when the relay process shuts down.
		accept := make(chan *iroh.Conn, 1)
		go func() {
			conn, err := endpoints[0].Accept(ctx)
			if err == nil {
				accept <- conn
			}
		}()
		outbound, err := endpoints[1].Connect(ctx, endpoints[0].Addr(), ALPN)
		if err != nil {
			t.Fatal(err)
		}
		defer outbound.Close()
		var inbound *iroh.Conn
		select {
		case inbound = <-accept:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		defer inbound.Close()
		stream, err := outbound.OpenStreamConn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		_ = stream.SetDeadline(time.Now().Add(3 * time.Second))
		payload := []byte{0, 0xff, 0x8e, 1}
		if _, err = stream.Write(payload); err != nil {
			t.Fatal(err)
		}
		received, err := inbound.AcceptStreamConn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer received.Close()
		_ = received.SetDeadline(time.Now().Add(3 * time.Second))
		data := make([]byte, len(payload))
		if _, err = io.ReadFull(received, data); err != nil || !bytes.Equal(data, payload) {
			t.Fatalf("stream before shutdown: %x %v", data, err)
		}
		log := p.terminate(t)
		if relayExitCounters(t, log)["clients_accepted"] < 2 {
			t.Fatalf("no authenticated endpoints:\n%s", log)
		}
		// Relay shutdown closes transport sockets immediately. The endpoints may wait
		// for QUIC idle timeout; do not mislabel that as instant session reconnection.
		probe, err := net.DialTimeout("tcp", strings.TrimPrefix(p.url, "http://"), time.Second)
		if err == nil {
			probe.Close()
			t.Fatal("relay listener remained open after exit")
		}
	})
	t.Run("connection-cap-and-timeout-recovery", func(t *testing.T) {
		p := startRelayProcess(t, binary, "--max-connections", "1", "--read-header-timeout", "200ms")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ws, _, err := websocket.Dial(ctx, p.url+"/relay", &websocket.DialOptions{Subprotocols: []string{"iroh-relay-v1"}})
		if err != nil {
			t.Fatal(err)
		}
		defer ws.CloseNow()
		if _, _, err = ws.Read(ctx); err != nil {
			t.Fatal(err)
		}
		transport := &http.Transport{DisableKeepAlives: true}
		client := &http.Client{Transport: transport, Timeout: time.Second}
		defer transport.CloseIdleConnections()
		if resp, err := client.Get(p.url + "/healthz"); err == nil {
			resp.Body.Close()
			t.Fatal("connection cap admitted another client")
		}
		ws.CloseNow()
		// Closing the only admitted connection must restore capacity.
		deadline := time.Now().Add(2 * time.Second)
		for {
			resp, err := client.Get(p.url + "/healthz")
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode != 200 {
					t.Fatal(resp.Status)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("capacity did not recover: %v", err)
			}
			time.Sleep(5 * time.Millisecond)
		}
		slow, err := net.DialTimeout("tcp", strings.TrimPrefix(p.url, "http://"), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer slow.Close()
		if _, err = io.WriteString(slow, "GET /healthz HTTP/1.1\r\nHost: localhost\r\n"); err != nil {
			t.Fatal(err)
		}
		_ = slow.SetReadDeadline(time.Now().Add(time.Second))
		if _, err = io.Copy(io.Discard, slow); err != nil {
			t.Fatalf("slow headers were not closed by server: %v", err)
		}
		resp, err := client.Get(p.url + "/healthz")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatal(resp.Status)
		}
		if runtime.GOOS != "windows" {
			p.terminate(t)
		}
	})
	t.Run("unsafe-public-plaintext-rejected", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, "--listen", "0.0.0.0:0")
		output, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(output), "plaintext listener requires") {
			t.Fatalf("unsafe listener accepted or unexpected failure: %v %s", err, output)
		}
	})
}
