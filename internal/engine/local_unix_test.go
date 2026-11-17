//go:build darwin || linux

package engine

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sid9017/linkport/internal/api"
	"github.com/Sid9017/linkport/internal/bridge"
	"github.com/Sid9017/linkport/internal/serialport"
	"github.com/creack/pty"
)

func waitLocalState(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.After(4 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for !condition() {
		select {
		case <-deadline:
			t.Fatal("local serial state did not update")
		case <-ticker.C:
		}
	}
}

func TestLocalMonitorWorksWithoutNetworkAndSharesPhysicalLock(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { master.Close(); slave.Close() })
	port := serialport.Normalize(serialport.Port{Path: slave.Name(), Product: "isolated test TTY"})
	var present atomic.Bool
	present.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	live, err := New(ctx, Options{
		Directory: t.TempDir(), DeviceDirectory: t.TempDir(), DisableNetworkListener: true,
		Enumerator: serialport.EnumerateFunc(func(context.Context) ([]serialport.Port, error) {
			if !present.Load() {
				return []serialport.Port{}, nil
			}
			return []serialport.Port{port}, nil
		}),
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); live.Run() }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(4 * time.Second):
			t.Error("local monitor did not shut down")
		}
	})
	local, err := live.MonitorLocal(ctx, port.ID, port.Path, 9600)
	if err != nil {
		t.Fatal(err)
	}
	if !local.Local || !local.Connected || local.PortPath != port.Path || local.BaudRate != 9600 {
		t.Fatalf("unexpected local session: %+v", local)
	}
	incoming := []byte{0, 255, '\r', '\n', 128, 'A'}
	if _, err := master.Write(incoming); err != nil {
		t.Fatal(err)
	}
	waitLocalState(t, func() bool {
		var received []byte
		for _, state := range live.sessions.States() {
			if state.ID == local.ID {
				for _, record := range state.Records {
					if record.Kind == "RX" {
						received = append(received, record.Data...)
					}
				}
			}
		}
		return bytes.Equal(received, incoming)
	})
	outgoing := []byte{'A', 'T', 255, 0, '\r', '\n'}
	if err := live.sessions.Send(local.ID, outgoing); err != nil {
		t.Fatal(err)
	}
	read := make(chan error, 1)
	go func() {
		buffer := make([]byte, len(outgoing))
		_, err := io.ReadFull(master, buffer)
		if err == nil && !bytes.Equal(buffer, outgoing) {
			err = os.ErrInvalid
		}
		read <- err
	}()
	select {
	case err := <-read:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("local serial send timed out")
	}
	if err := live.SetShared(ctx, port.ID, true); err != nil {
		t.Fatal(err)
	}
	remote := live.newBridge()
	t.Cleanup(remote.Close)
	handler, err := api.NewHandler(live.manager, api.Options{Bridge: remote, Auth: func(next http.Handler) http.Handler { return next }})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	if stream, err := bridge.Dial(ctx, strings.TrimPrefix(server.URL, "http://"), port.ID, 9600); err == nil {
		stream.Close()
		t.Fatal("remote connection opened a physical port already monitored locally")
	}
	if err := live.SetShared(ctx, port.ID, false); err != nil {
		t.Fatal(err)
	}
	waitLocalState(t, func() bool { return live.State().Network.State == "Disabled" })
	states := live.sessions.States()
	if len(states) != 1 || !states[0].Connected {
		t.Fatal("network/share state closed independent local monitoring")
	}
	contents, err := live.sessions.Export(local.ID)
	if err != nil || !bytes.Contains(contents, []byte("00ff0d0a8041")) {
		t.Fatalf("binary local export: %v", err)
	}
	present.Store(false)
	waitLocalState(t, func() bool { return !live.sessions.States()[0].Connected })
	if live.sessions.States()[0].Error == "" {
		t.Fatal("removed local port did not retain disconnect reason")
	}
}

func TestLocalMonitorRejectsPathOutsideEnumeratedInventory(t *testing.T) {
	live := testEngine(t)
	defer live.broker.Close()
	identity := serialport.Identity(serialport.Port{Path: "/dev/real-enumerated-port"})
	if _, err := live.MonitorLocal(context.Background(), identity, "/etc/passwd", 9600); err == nil {
		t.Fatal("opened an arbitrary path rather than an enumerated serial device")
	}
}
