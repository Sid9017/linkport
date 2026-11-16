//go:build com0com_integration

package vport

import (
	"bytes"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"go.bug.st/serial"
)

func openDriverPair(t *testing.T) (Port, serial.Port) {
	t.Helper()
	path := os.Getenv("LINKPORT_TEST_COM0COM_PATH")
	pair := os.Getenv("LINKPORT_TEST_COM0COM_PAIR")
	if path == "" || pair == "" {
		if os.Getenv("LINKPORT_TEST_COM0COM_REQUIRED") == "1" {
			t.Fatal("real driver testing requires LINKPORT_TEST_COM0COM_PATH and LINKPORT_TEST_COM0COM_PAIR")
		}
		t.Skip("set both dedicated com0com test endpoints to run driver integration")
	}
	if path == pair {
		t.Fatal("com0com test endpoints must be different")
	}
	port, err := Open(Options{Path: path, PairDevice: pair})
	if err != nil {
		t.Fatalf("open actual com0com backend: %v", err)
	}
	t.Cleanup(func() { port.Close() })
	application, err := serial.Open(path, &serial.Mode{
		BaudRate: 115200, DataBits: 8,
		InitialStatusBits: &serial.ModemOutputBits{},
	})
	if err != nil {
		t.Fatalf("open paired application endpoint: %v", err)
	}
	t.Cleanup(func() { application.Close() })
	if err := application.SetReadTimeout(3 * time.Second); err != nil {
		t.Fatal(err)
	}
	return port, application
}

func driverRead(t *testing.T, reader io.Reader, expected []byte) {
	t.Helper()
	completed := make(chan error, 1)
	go func() {
		actual := make([]byte, len(expected))
		_, err := io.ReadFull(reader, actual)
		if err == nil && !bytes.Equal(actual, expected) {
			err = errors.New("com0com changed binary bytes")
		}
		completed <- err
	}()
	select {
	case err := <-completed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("com0com binary transfer timed out")
	}
}

func TestCom0comDriverBinarySettingsAndControlLines(t *testing.T) {
	port, application := openDriverPair(t)
	receivedEvents := make(chan Event, 128)
	go func() {
		for event := range port.Events() {
			receivedEvents <- event
		}
		close(receivedEvents)
	}()
	if err := application.SetMode(&serial.Mode{BaudRate: 57600, DataBits: 8}); err != nil {
		t.Fatal(err)
	}
	if err := application.SetDTR(true); err != nil {
		t.Fatal(err)
	}
	if err := application.SetRTS(true); err != nil {
		t.Fatal(err)
	}
	payload := []byte{0, 0x8e, 0xff, '\r', '\n', 0x03, 0x7f}
	if _, err := application.Write(payload); err != nil {
		t.Fatal(err)
	}
	driverRead(t, port, payload)
	if _, err := port.Write(payload); err != nil {
		t.Fatal(err)
	}
	driverRead(t, application, payload)
	settingsSeen, controlSeen := false, false
	deadline := time.After(5 * time.Second)
	for !settingsSeen || !controlSeen {
		select {
		case event, open := <-receivedEvents:
			if !open {
				t.Fatal("com0com events closed before settings arrived")
			}
			settingsSeen = settingsSeen || event.Kind == EventLineSettings && event.Line.BaudRate == 57600 && event.Line.DataBits == 8
			controlSeen = controlSeen || event.Kind == EventControlLines && event.Control.DTR && event.Control.RTS
		case <-deadline:
			t.Fatalf("com0com reports settings=%v control=%v", settingsSeen, controlSeen)
		}
	}
	if err := port.SetModemStatus(ModemStatus{CTS: true, DSR: true}); err != nil {
		t.Fatal(err)
	}
	modemDeadline := time.Now().Add(3 * time.Second)
	for {
		status, err := application.GetModemStatusBits()
		if err != nil {
			t.Fatal(err)
		}
		if status.CTS && status.DSR {
			break
		}
		if time.Now().After(modemDeadline) {
			t.Fatalf("paired endpoint did not observe CTS/DSR: %+v", status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCom0comDriverCloseCancelsReadAndAllowsReopen(t *testing.T) {
	port, application := openDriverPair(t)
	go func() {
		for range port.Events() {
		}
	}()
	completed := make(chan error, 1)
	go func() {
		_, err := port.Read(make([]byte, 32))
		completed <- err
	}()
	select {
	case err := <-completed:
		t.Fatalf("read finished before Close: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := port.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-completed:
		if !errors.Is(err, os.ErrClosed) {
			t.Fatalf("cancelled read returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not cancel overlapped read")
	}
	if err := application.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, _ := openDriverPair(t)
	if reopened.Path() != port.Path() {
		t.Fatal("reopened pair changed the application endpoint")
	}
}
