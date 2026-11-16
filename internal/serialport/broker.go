package serialport

import (
	"errors"
	"os"
	"sync"

	"go.bug.st/serial"
)

var ErrBusy = errors.New("串口正在被另一个连接使用")

type Broker struct {
	mu         sync.Mutex
	active     map[string]*lease
	closed     bool
	OpenSerial func(string, *serial.Mode) (serial.Port, error)
}

type lease struct {
	serial.Port
	broker *Broker
	path   string
	mu     sync.Mutex
	closed bool
}

func NewBroker() *Broker {
	return &Broker{active: make(map[string]*lease), OpenSerial: serial.Open}
}

func (broker *Broker) Open(path string, mode *serial.Mode) (serial.Port, error) {
	broker.mu.Lock()
	if broker.closed {
		broker.mu.Unlock()
		return nil, os.ErrClosed
	}
	if broker.active[path] != nil {
		broker.mu.Unlock()
		return nil, ErrBusy
	}
	entry := &lease{broker: broker, path: path}
	broker.active[path] = entry
	broker.mu.Unlock()
	port, err := broker.OpenSerial(path, mode)
	if err != nil {
		entry.Close()
		return nil, err
	}
	entry.mu.Lock()
	if entry.closed {
		entry.mu.Unlock()
		port.Close()
		return nil, os.ErrClosed
	}
	entry.Port = port
	entry.mu.Unlock()
	return entry, nil
}

func (entry *lease) Close() error {
	entry.mu.Lock()
	if entry.closed {
		entry.mu.Unlock()
		return nil
	}
	entry.closed = true
	port := entry.Port
	entry.mu.Unlock()
	var err error
	if port != nil {
		err = port.Close()
	}
	entry.broker.mu.Lock()
	if entry.broker.active[entry.path] == entry {
		delete(entry.broker.active, entry.path)
	}
	entry.broker.mu.Unlock()
	return err
}

func (broker *Broker) Close() {
	broker.mu.Lock()
	broker.closed = true
	entries := make([]*lease, 0, len(broker.active))
	for _, entry := range broker.active {
		entries = append(entries, entry)
	}
	broker.mu.Unlock()
	for _, entry := range entries {
		entry.Close()
	}
}
