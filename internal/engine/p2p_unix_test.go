//go:build darwin || linux

package engine

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/Sid9017/linkport/internal/serialport"
	"github.com/Sid9017/linkport/internal/vport"
	"github.com/creack/pty"
	"go.bug.st/serial"
)

func runningEngine(t *testing.T, enumerator serialport.Enumerator) *Engine {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	engine, err := New(ctx, Options{
		Directory: t.TempDir(), DeviceDirectory: t.TempDir(), LocalOnly: true, Enumerator: enumerator,
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); engine.Run() }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("iroh engine did not stop")
		}
	})
	return engine
}

func TestPairedIrohDevicesTransferActualSerialBytesAndRevoke(t *testing.T) {
	physical, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { physical.Close(); slave.Close() })
	port := serialport.Normalize(serialport.Port{Path: slave.Name(), Product: "iroh test UART"})
	provider := runningEngine(t, serialport.EnumerateFunc(func(context.Context) ([]serialport.Port, error) {
		return []serialport.Port{port}, nil
	}))
	// A PTY supports real byte/framing I/O but has no physical modem outputs.
	provider.broker.OpenSerial = func(path string, mode *serial.Mode) (serial.Port, error) {
		copy := *mode
		copy.InitialStatusBits = nil
		return serial.Open(path, &copy)
	}
	consumer := runningEngine(t, serialport.EnumerateFunc(func(context.Context) ([]serialport.Port, error) {
		return []serialport.Port{}, nil
	}))
	invite, err := provider.Network().CreateInvitation()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := consumer.AddDevice(context.Background(), invite.Code); err != nil {
		t.Fatal(err)
	}
	if err := provider.SetShared(context.Background(), port.ID, true); err != nil {
		t.Fatal(err)
	}
	consumer.Refresh()
	waitLocalState(t, func() bool {
		devices := consumer.State().Devices
		return len(devices) == 1 && devices[0].Online && len(devices[0].Ports) == 1
	})
	session, err := consumer.Connect(context.Background(), provider.Network().Status().NodeID, port.ID, 115200, vport.Options{})
	if err != nil {
		t.Fatal(err)
	}
	virtual, err := os.OpenFile(session.LocalPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer virtual.Close()
	checkRead := func(reader io.Reader, expected []byte) {
		t.Helper()
		done := make(chan error, 1)
		go func() {
			got := make([]byte, len(expected))
			_, err := io.ReadFull(reader, got)
			if err == nil && !bytes.Equal(got, expected) {
				err = os.ErrInvalid
			}
			done <- err
		}()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("serial data did not traverse authenticated iroh stream")
		}
	}
	rx := []byte{0x00, 0xff, 0x80, 'R', 'X', '\r', '\n'}
	if _, err := physical.Write(rx); err != nil {
		t.Fatal(err)
	}
	checkRead(virtual, rx)
	tx := []byte{'T', 'X', 0xff, 0x00, '\r', '\n'}
	if _, err := virtual.Write(tx); err != nil {
		t.Fatal(err)
	}
	checkRead(physical, tx)
	if err := provider.RemoveDevice(consumer.Network().Status().NodeID); err != nil {
		t.Fatal(err)
	}
	waitLocalState(t, func() bool {
		for _, state := range consumer.Sessions().States() {
			if state.ID == session.ID {
				return !state.Connected
			}
		}
		return false
	})
	waitLocalState(t, func() bool {
		state, err := provider.MonitorLocal(context.Background(), port.ID, port.Path, 115200)
		if err != nil {
			return false
		}
		_ = provider.Sessions().Disconnect(state.ID)
		return true
	})
}
