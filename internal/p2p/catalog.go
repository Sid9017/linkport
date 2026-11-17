package p2p

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Sid9017/linkport/internal/relaycatalog"

	"github.com/tmc/go-iroh/netaddr"
	"github.com/tmc/go-iroh/relay"
)

func (n *Network) loadManagedCatalog(directory string) error {
	n.catalogPath = filepath.Join(directory, "relay-directory.json")
	f, err := os.Open(n.catalogPath)
	var raw []byte
	if errors.Is(err, os.ErrNotExist) {
		raw = relaycatalog.Bootstrap
	} else if err != nil {
		return err
	} else {
		defer f.Close()
		raw, err = io.ReadAll(io.LimitReader(f, relaycatalog.MaxSize+1))
		if err != nil {
			return err
		}
	}
	n.catalog, err = relaycatalog.Verify(raw, relaycatalog.PublicKey, time.Now())
	if err != nil {
		return err
	}
	bootstrap, err := relaycatalog.Verify(relaycatalog.Bootstrap, relaycatalog.PublicKey, time.Now())
	if err != nil {
		return err
	}
	if bootstrap.Version() > n.catalog.Version() {
		if err := writeCatalog(n.catalogPath, bootstrap.Bytes()); err != nil {
			return err
		}
		n.catalog = bootstrap
	} else if bootstrap.Version() == n.catalog.Version() && !bootstrap.Equal(n.catalog) {
		return errors.New("embedded relay directory version conflict")
	}
	return nil
}

func (n *Network) relayURLsLocked() []string {
	if n.store.Config().RelayMode == "disabled" {
		return nil
	}
	if n.localOnly {
		return n.store.Config().RelayURLs
	}
	return n.catalog.URLs()
}

func managedRelayMode(urls []string) relay.Mode {
	var parsed []netaddr.RelayURL
	for _, raw := range urls {
		if u, err := netaddr.ParseRelayURL(raw); err == nil {
			parsed = append(parsed, u)
		}
	}
	return relay.ModeCustomURLs(parsed...)
}

// ApplyRelayCatalog authenticates against the embedded operator key. A caller
// cannot substitute its own verification key or lower the persisted version.
// Existing endpoints are retained; independent serial streams are not replayed.
func (n *Network) ApplyRelayCatalog(raw []byte) error {
	next, err := relaycatalog.Verify(raw, relaycatalog.PublicKey, time.Now())
	if err != nil {
		return err
	}
	if !next.Fresh(time.Now()) {
		return errors.New("relay directory expired")
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.localOnly {
		return errors.New("managed relay updates unavailable in isolated tests")
	}
	if next.Version() < n.catalog.Version() {
		return errors.New("relay directory rollback rejected")
	}
	if next.Version() == n.catalog.Version() {
		if !next.Equal(n.catalog) {
			return errors.New("relay directory version conflict")
		}
		return nil
	}
	old := make(map[string]netaddr.RelayURL)
	for _, raw := range n.catalog.URLs() {
		u, _ := netaddr.ParseRelayURL(raw)
		old[u.String()] = u
	}
	nextURLs := make(map[string]netaddr.RelayURL)
	for _, raw := range next.URLs() {
		u, _ := netaddr.ParseRelayURL(raw)
		nextURLs[u.String()] = u
	}
	enabled := n.endpoint != nil && !n.stopped && n.store.Config().RelayMode != "disabled"
	inserted := []netaddr.RelayURL{}
	rollback := func() {
		for _, u := range inserted {
			n.endpoint.RemoveRelay(u)
		}
	}
	if enabled {
		for canonical, u := range nextURLs {
			if _, exists := old[canonical]; exists && n.failedRelays[canonical].IsZero() {
				continue
			}
			if _, err := n.endpoint.InsertRelay(u, nil); err != nil {
				rollback()
				return err
			}
			inserted = append(inserted, u)
		}
	}
	if err := writeCatalog(n.catalogPath, next.Bytes()); err != nil {
		if enabled {
			rollback()
		}
		return err
	}
	n.catalog = next
	if enabled {
		for canonical, u := range old {
			if _, keep := nextURLs[canonical]; !keep {
				n.endpoint.RemoveRelay(u)
			}
		}
	}
	n.failedRelays = make(map[string]time.Time)
	n.wakeDiscovery()
	return nil
}

func writeCatalog(path string, raw []byte) (err error) {
	f, err := os.CreateTemp(filepath.Dir(path), ".relay-directory-*")
	if err != nil {
		return err
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	if err = f.Chmod(0600); err != nil {
		return err
	}
	if _, err = f.Write(raw); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
