// Package relayservice hosts the pinned iroh relay without application state.
package relayservice

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/tmc/go-iroh/metrics"
	"github.com/tmc/go-iroh/relayserver"
)

type Config struct {
	MaxConnections       int
	ClientBytesPerSecond int64
	ReadHeaderTimeout    time.Duration
	IdleTimeout          time.Duration
	ShutdownTimeout      time.Duration
	CertFile             string
	KeyFile              string
}

func DefaultConfig() Config {
	return Config{MaxConnections: 128, ClientBytesPerSecond: 4 << 20, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, ShutdownTimeout: 10 * time.Second}
}

// Server is single-use. Serve owns its listener and all accepted connections,
// including WebSockets hijacked out of net/http's shutdown bookkeeping.
type Server struct {
	cfg      Config
	relay    *relayserver.Server
	mu       sync.Mutex
	conns    map[*trackedConn]struct{}
	draining bool
	started  bool
	active   sync.WaitGroup
}

func New(cfg Config) (*Server, error) {
	if cfg.MaxConnections < 1 || cfg.ClientBytesPerSecond < 1 || cfg.ReadHeaderTimeout <= 0 || cfg.IdleTimeout <= 0 || cfg.ShutdownTimeout <= 0 {
		return nil, errors.New("connection limit, byte rate and timeouts must be positive")
	}
	if (cfg.CertFile == "") != (cfg.KeyFile == "") {
		return nil, errors.New("TLS certificate and key must be supplied together")
	}
	return &Server{cfg: cfg, relay: relayserver.NewWithOptions(relayserver.WithClientRate(cfg.ClientBytesPerSecond)), conns: make(map[*trackedConn]struct{})}, nil
}

// Snapshot exposes aggregate upstream counters to local operators/tests only.
func (s *Server) Snapshot() metrics.Snapshot { return s.relay.Snapshot() }
func (s *Server) Connections() int           { s.mu.Lock(); defer s.mu.Unlock(); return len(s.conns) }

func (s *Server) handler(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.draining {
		s.mu.Unlock()
		http.Error(w, "draining", http.StatusServiceUnavailable)
		return
	}
	s.active.Add(1)
	s.mu.Unlock()
	defer s.active.Done()
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	switch r.URL.Path {
	case "/healthz":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte("ok\n"))
	case "/relay", "/ping":
		s.relay.ServeHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return errors.New("relay server already started")
	}
	s.started = true
	s.mu.Unlock()
	base, cancel := context.WithCancel(ctx)
	defer cancel()
	srv := &http.Server{Handler: http.HandlerFunc(s.handler), ReadHeaderTimeout: s.cfg.ReadHeaderTimeout, IdleTimeout: s.cfg.IdleTimeout, MaxHeaderBytes: 16 << 10,
		BaseContext: func(net.Listener) context.Context { return base }, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	stopped := make(chan struct{})
	done := make(chan error, 1)
	limited := &trackedListener{Listener: ln, server: s}
	go func() {
		select {
		case <-ctx.Done():
		case <-stopped:
		}
		s.mu.Lock()
		s.draining = true
		connections := make([]*trackedConn, 0, len(s.conns))
		for c := range s.conns {
			connections = append(connections, c)
		}
		s.mu.Unlock()
		cancel()
		// Shutdown alone does not close hijacked relay WebSockets.
		_ = limited.Close()
		for _, c := range connections {
			_ = c.Close()
		}
		shutdown, finish := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
		defer finish()
		err := srv.Shutdown(shutdown)
		handlersDone := make(chan struct{})
		go func() { s.active.Wait(); close(handlersDone) }()
		select {
		case <-handlersDone:
		case <-shutdown.Done():
			err = shutdown.Err()
		}
		done <- err
	}()
	var err error
	if s.cfg.CertFile != "" {
		err = srv.ServeTLS(limited, s.cfg.CertFile, s.cfg.KeyFile)
	} else {
		err = srv.Serve(limited)
	}
	close(stopped)
	shutdownErr := <-done
	if errors.Is(err, http.ErrServerClosed) || (ctx.Err() != nil && errors.Is(err, net.ErrClosed)) {
		err = nil
	}
	if err != nil {
		return fmt.Errorf("relay HTTP server: %w", err)
	}
	return shutdownErr
}

type trackedListener struct {
	net.Listener
	server    *Server
	closeOnce sync.Once
	closeErr  error
}

func (l *trackedListener) Close() error {
	l.closeOnce.Do(func() { l.closeErr = l.Listener.Close() })
	return l.closeErr
}

func (l *trackedListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		s := l.server
		s.mu.Lock()
		if s.draining || len(s.conns) >= s.cfg.MaxConnections {
			s.mu.Unlock()
			_ = c.Close()
			continue
		}
		tracked := &trackedConn{Conn: c, server: s}
		s.conns[tracked] = struct{}{}
		s.mu.Unlock()
		return tracked, nil
	}
}

type trackedConn struct {
	net.Conn
	server *Server
	once   sync.Once
	err    error
}

func (c *trackedConn) Close() error {
	c.once.Do(func() {
		c.err = c.Conn.Close()
		c.server.mu.Lock()
		delete(c.server.conns, c)
		c.server.mu.Unlock()
	})
	return c.err
}
