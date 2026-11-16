//go:build darwin || linux

package session_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sid9017/linkport/internal/api"
	"github.com/Sid9017/linkport/internal/bridge"
	"github.com/Sid9017/linkport/internal/config"
	"github.com/Sid9017/linkport/internal/serialport"
	"github.com/Sid9017/linkport/internal/session"
	"github.com/Sid9017/linkport/internal/share"
	"go.bug.st/serial"
)

type simulatedSerial struct {
	net.Conn
	modes chan serial.Mode
}

func (port *simulatedSerial) SetMode(mode *serial.Mode) error {
	select {
	case port.modes <- *mode:
	default:
	}
	return nil
}
func (port *simulatedSerial) Drain() error                       { return nil }
func (port *simulatedSerial) ResetInputBuffer() error            { return nil }
func (port *simulatedSerial) ResetOutputBuffer() error           { return nil }
func (port *simulatedSerial) SetDTR(bool) error                  { return nil }
func (port *simulatedSerial) SetRTS(bool) error                  { return nil }
func (port *simulatedSerial) SetReadTimeout(time.Duration) error { return nil }
func (port *simulatedSerial) GetModemStatusBits() (*serial.ModemStatusBits, error) {
	return &serial.ModemStatusBits{CTS: true}, nil
}
func (port *simulatedSerial) Break(time.Duration) error { return errors.ErrUnsupported }

type hardware struct {
	conn  net.Conn
	modes chan serial.Mode
}

func receive(t *testing.T, reader io.Reader, expected []byte) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		buffer := make([]byte, len(expected))
		_, err := io.ReadFull(reader, buffer)
		if err == nil && !bytes.Equal(buffer, expected) {
			err = errors.New("binary bytes changed: " + string(buffer))
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("serial transfer timed out")
	}
}

func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.After(4 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if condition() {
			return
		}
		select {
		case <-deadline:
			t.Fatal("state change timed out")
		case <-ticker.C:
		}
	}
}

func TestRealPTYMappingsBridgeBinaryIndependentlyAndUnshareRevokes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ports := []serialport.Port{
		serialport.Normalize(serialport.Port{Path: "test-port-0", Product: "Device zero"}),
		serialport.Normalize(serialport.Port{Path: "test-port-1", Product: "Device one"}),
	}
	manager, err := share.NewManager(ctx,
		serialport.EnumerateFunc(func(context.Context) ([]serialport.Port, error) { return ports, nil }),
		config.NewFileStore(filepath.Join(t.TempDir(), "shares.json")), share.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	for _, port := range ports {
		if err := manager.SetShared(ctx, port.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	server := bridge.NewServer(manager)
	t.Cleanup(server.Close)
	var hardwareMu sync.Mutex
	devices := make(map[string]hardware)
	server.Open = func(path string, mode *serial.Mode) (serial.Port, error) {
		physical, device := net.Pipe()
		modes := make(chan serial.Mode, 16)
		hardwareMu.Lock()
		devices[path] = hardware{conn: device, modes: modes}
		hardwareMu.Unlock()
		t.Cleanup(func() { physical.Close(); device.Close() })
		return &simulatedSerial{Conn: physical, modes: modes}, nil
	}
	handler, err := api.NewHandler(manager, api.Options{NodeID: "node-remote", Version: "0.0.1", Bridge: server, Auth: func(next http.Handler) http.Handler { return next }})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)
	address := strings.TrimPrefix(httpServer.URL, "http://")
	consumer := session.New(t.TempDir())
	t.Cleanup(func() { consumer.DisconnectAll(nil) })
	states := make([]session.State, 2)
	for index, port := range ports {
		state, err := consumer.Connect(ctx, session.Target{
			NodeID: "node-remote", HostName: "test-machine", PortID: port.ID,
			PortPath: port.Path, Label: port.Product, Address: address, BaudRate: 115200,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !state.Connected || state.LocalPath == "" {
			t.Fatalf("not connected: %+v", state)
		}
		states[index] = state
	}
	if states[0].LocalPath == states[1].LocalPath {
		t.Fatal("sessions reused local PTY")
	}
	app, err := os.OpenFile(states[0].LocalPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close() })
	hardwareMu.Lock()
	first, second := devices[ports[0].Path], devices[ports[1].Path]
	hardwareMu.Unlock()
	rx := []byte{0, 255, '\r', '\n', 128, 'A'}
	go first.conn.Write(rx)
	receive(t, app, rx)
	tx := []byte{255, 255, 0, 'X', '\r', '\n'}
	if _, err := app.Write(tx); err != nil {
		t.Fatal(err)
	}
	receive(t, first.conn, tx)
	if err := consumer.Send(states[1].ID, []byte("only-session-one")); err != nil {
		t.Fatal(err)
	}
	receive(t, second.conn, []byte("only-session-one"))
	for _, state := range consumer.States() {
		for _, record := range state.Records {
			if state.ID == states[0].ID && bytes.Contains(record.Data, []byte("only-session-one")) {
				t.Fatal("logs crossed sessions")
			}
		}
	}
	configurer, err := serial.Open(states[0].LocalPath, &serial.Mode{BaudRate: 19200, DataBits: 8, StopBits: serial.OneStopBit})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { configurer.Close() })
	configured := false
	eventually(t, func() bool {
		for {
			select {
			case mode := <-first.modes:
				if mode.BaudRate == 19200 {
					configured = true
				}
			default:
				return configured
			}
		}
	})
	other := session.New(t.TempDir())
	t.Cleanup(func() { other.DisconnectAll(nil) })
	if _, err := other.Connect(ctx, session.Target{NodeID: "node-remote", HostName: "test", PortID: ports[0].ID, Address: address, BaudRate: 115200}); err == nil {
		t.Fatal("second consumer acquired busy physical serial")
	}
	if err := manager.SetShared(ctx, ports[0].ID, false); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		for _, state := range consumer.States() {
			if state.ID == states[0].ID {
				return !state.Connected
			}
		}
		return false
	})
	if _, err := os.Lstat(states[0].LocalPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("revoked mapping still exists: %v", err)
	}
	if err := consumer.Send(states[1].ID, []byte("still-connected")); err != nil {
		t.Fatal(err)
	}
	receive(t, second.conn, []byte("still-connected"))
	exported, err := consumer.Export(states[1].ID)
	if err != nil || !bytes.Contains(exported, []byte("6f6e6c792d73657373696f6e2d6f6e65")) {
		t.Fatalf("export did not preserve binary log: %v", err)
	}
}
