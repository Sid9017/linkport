package relayservice

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"sync"
)

// HostConfig selects the opt-in relay role in a desktop or headless host.
// TLSKey is a file path, never the key contents.
type HostConfig struct {
	Enabled        bool   `json:"enabled"`
	Listen         string `json:"listen"`
	PublicURL      string `json:"publicURL,omitempty"`
	TLSCert        string `json:"tlsCert,omitempty"`
	TLSKey         string `json:"tlsKey,omitempty"`
	MaxConnections int    `json:"maxConnections"`
}

type HostStatus struct {
	Running bool   `json:"running"`
	Address string `json:"address,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Host owns a relay listener and its bounded shutdown.
type Host struct {
	server  *Server
	address string
	cancel  context.CancelFunc
	done    chan struct{}
	err     error // published by closing done
	once    sync.Once
}

// StartHost validates TLS before returning success. Plain HTTP is loopback-only.
func StartHost(ctx context.Context, cfg HostConfig) (*Host, error) {
	if !cfg.Enabled {
		return nil, errors.New("relay hosting is disabled")
	}
	address := cfg.Listen
	if address == "" {
		address = "127.0.0.1:3340"
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if cfg.TLSCert == "" && !net.ParseIP(host).IsLoopback() {
		return nil, errors.New("public relay listener requires TLS")
	}
	sc := DefaultConfig()
	sc.CertFile, sc.KeyFile = cfg.TLSCert, cfg.TLSKey
	sc.MaxConnections = cfg.MaxConnections
	if sc.MaxConnections == 0 {
		sc.MaxConnections = 16
	}
	server, err := New(sc)
	if err != nil {
		return nil, err
	}
	if cfg.TLSCert != "" {
		if _, err = tls.LoadX509KeyPair(cfg.TLSCert, cfg.TLSKey); err != nil {
			return nil, err
		}
	}
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	run, cancel := context.WithCancel(ctx)
	h := &Host{server: server, address: ln.Addr().String(), cancel: cancel, done: make(chan struct{})}
	go func() { h.err = server.Serve(run, ln); close(h.done) }()
	return h, nil
}

func (h *Host) Status() HostStatus {
	if h == nil {
		return HostStatus{}
	}
	status := HostStatus{Running: true, Address: h.address}
	select {
	case <-h.done:
		status.Running = false
		if h.err != nil {
			status.Error = h.err.Error()
		}
	default:
	}
	return status
}

func (h *Host) Close() error {
	if h == nil {
		return nil
	}
	h.once.Do(h.cancel)
	<-h.done
	return h.err
}
