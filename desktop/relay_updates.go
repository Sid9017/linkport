package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/Sid9017/linkport/internal/p2p"
	"github.com/Sid9017/linkport/internal/relaycatalog"
)

type relayUpdateStatus struct {
	State   string `json:"state"`
	Version uint64 `json:"version"`
	Next    uint64 `json:"next,omitempty"`
	Error   string `json:"error,omitempty"`
}

type relayUpdateController struct {
	ctx      context.Context
	network  *p2p.Network
	settings *preferenceStore
	client   *http.Client
	source   string
	mu       sync.Mutex
	status   relayUpdateStatus
	pending  []byte
	busy     bool
}

func newRelayUpdateController(ctx context.Context, network *p2p.Network, settings *preferenceStore) *relayUpdateController {
	state := "idle"
	if relaycatalog.SourceURL == "" {
		state = "bundled"
	}
	return &relayUpdateController{ctx: ctx, network: network, settings: settings, source: relaycatalog.SourceURL, status: relayUpdateStatus{State: state, Version: network.Status().RelayDirectoryVersion}, client: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *relayUpdateController) snapshot() relayUpdateStatus {
	if c == nil {
		return relayUpdateStatus{State: "unavailable"}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}
func (c *relayUpdateController) check() error {
	if c == nil {
		return errors.New("relay updates unavailable")
	}
	if c.source == "" {
		return nil
	}
	c.mu.Lock()
	if c.busy {
		c.mu.Unlock()
		return nil
	}
	c.busy = true
	c.status.State = "checking"
	c.status.Error = ""
	c.mu.Unlock()
	go func() {
		err := c.fetch()
		c.mu.Lock()
		defer c.mu.Unlock()
		c.busy = false
		if err != nil {
			c.status.Error = err.Error()
			if len(c.pending) > 0 {
				c.status.State = "available"
			} else {
				c.status.State = "error"
			}
		}
	}()
	return nil
}
func (c *relayUpdateController) fetch() error {
	if c.source == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(c.ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.source, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cache-Control", "no-cache")
	response, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("relay directory: HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, relaycatalog.MaxSize+1))
	if err != nil {
		return err
	}
	next, err := relaycatalog.Verify(raw, relaycatalog.PublicKey, time.Now())
	if err != nil {
		return err
	}
	if !next.Fresh(time.Now()) {
		return errors.New("relay directory expired")
	}
	current := c.network.Status().RelayDirectoryVersion
	if next.Version() < current {
		return errors.New("relay directory rollback rejected")
	}
	if next.Version() == current {
		if err := c.network.ApplyRelayCatalog(raw); err != nil {
			return err
		}
		c.mu.Lock()
		c.status = relayUpdateStatus{State: "up-to-date", Version: current}
		c.pending = nil
		c.mu.Unlock()
		return nil
	}
	c.mu.Lock()
	c.pending = append([]byte(nil), raw...)
	c.status = relayUpdateStatus{State: "available", Version: current, Next: next.Version()}
	c.mu.Unlock()
	if c.settings.get().AutoUpdate {
		return c.applyPending()
	}
	return nil
}
func (c *relayUpdateController) applyPending() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pending) == 0 {
		return errors.New("no verified relay update available")
	}
	if err := c.network.ApplyRelayCatalog(c.pending); err != nil {
		c.status.Error = err.Error()
		return err
	}
	c.status = relayUpdateStatus{State: "up-to-date", Version: c.network.Status().RelayDirectoryVersion}
	c.pending = nil
	return nil
}
func (c *relayUpdateController) apply() error {
	if c == nil {
		return errors.New("relay updates unavailable")
	}
	if c.source == "" {
		return errors.New("relay configuration is updated with the application")
	}
	c.mu.Lock()
	busy := c.busy
	c.mu.Unlock()
	if busy {
		return errors.New("relay update is busy")
	}
	return c.applyPending()
}
func (c *relayUpdateController) start() {
	if c.source == "" {
		return
	}
	go func() {
		defer c.client.CloseIdleConnections()
		timer := time.NewTimer(30 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-c.ctx.Done():
				return
			case <-timer.C:
				_ = c.check()
				timer.Reset(6 * time.Hour)
			}
		}
	}()
}
