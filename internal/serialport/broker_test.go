package serialport

import (
	"errors"
	"os"
	"sync/atomic"
	"testing"

	"go.bug.st/serial"
)

type closeCounter struct {
	serial.Port
	closes atomic.Int32
}

func (port *closeCounter) Close() error { port.closes.Add(1); return nil }

func TestBrokerReservesEachPhysicalPathAndReleasesAfterClose(t *testing.T) {
	broker := NewBroker()
	defer broker.Close()
	opened := 0
	broker.OpenSerial = func(string, *serial.Mode) (serial.Port, error) { opened++; return &closeCounter{}, nil }
	first, err := broker.Open("COM1", &serial.Mode{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := broker.Open("COM1", &serial.Mode{}); !errors.Is(err, ErrBusy) || opened != 1 {
		t.Fatalf("duplicate open: %v, calls=%d", err, opened)
	}
	second, err := broker.Open("COM2", &serial.Mode{})
	if err != nil {
		t.Fatal(err)
	}
	second.Close()
	first.Close()
	first.Close()
	if _, err := broker.Open("COM1", &serial.Mode{}); err != nil {
		t.Fatalf("reopening released port: %v", err)
	}
	if opened != 3 {
		t.Fatalf("physical opens=%d, want 3", opened)
	}
}

func TestBrokerShutdownClosesAnOpenThatCompletesLate(t *testing.T) {
	broker := NewBroker()
	started, proceed := make(chan struct{}), make(chan struct{})
	physical := &closeCounter{}
	broker.OpenSerial = func(string, *serial.Mode) (serial.Port, error) {
		close(started)
		<-proceed
		return physical, nil
	}
	result := make(chan error, 1)
	go func() { _, err := broker.Open("/dev/test-port", &serial.Mode{}); result <- err }()
	<-started
	broker.Close()
	close(proceed)
	if err := <-result; !errors.Is(err, os.ErrClosed) {
		t.Fatalf("late open: %v", err)
	}
	if physical.closes.Load() != 1 {
		t.Fatalf("late physical port not closed exactly once: %d", physical.closes.Load())
	}
	if _, err := broker.Open("/dev/other", &serial.Mode{}); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("broker reopened after shutdown: %v", err)
	}
}
