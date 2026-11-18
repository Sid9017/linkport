package p2p

import (
	"time"

	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"
)

// Count relays observed by the latest network probe, plus the currently
// connected home relay (which may not support the probe protocol).
// A configured address alone is not evidence that a relay was detected.
func detectedRelayCount(report iroh.NetReport, connectedHome string) int {
	detected := make(map[string]struct{}, len(report.RelayLatencies)+1)
	for url := range report.RelayLatencies {
		if url.String() != "" {
			detected[url.String()] = struct{}{}
		}
	}
	if connectedHome != "" {
		detected[connectedHome] = struct{}{}
	}
	return len(detected)
}

// relayRecoveryLoop rotates a failed configured home relay without rebuilding the
// endpoint. A cooldown prevents repeatedly selecting a known-dead first entry.
// It does not replay serial writes or promise survival of an expired QUIC session.
func (n *Network) relayRecoveryLoop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	var failedSince time.Time
	var observed string
	for {
		select {
		case <-n.ctx.Done():
			return
		case now := <-ticker.C:
			n.mu.Lock()
			if n.failedRelays == nil {
				n.failedRelays = make(map[string]time.Time)
			}
			urls := n.relayURLsLocked()
			if n.stopped || n.endpoint == nil || len(urls) < 2 {
				n.mu.Unlock()
				continue
			}
			configured := make(map[string]bool, len(urls))
			for _, raw := range urls {
				u, _ := netaddr.ParseRelayURL(raw)
				configured[u.String()] = true
			}
			for raw, until := range n.failedRelays {
				if !configured[raw] {
					delete(n.failedRelays, raw)
					continue
				}
				if !now.Before(until) {
					u, _ := netaddr.ParseRelayURL(raw)
					_, _ = n.endpoint.InsertRelay(u, nil)
					delete(n.failedRelays, raw)
				}
			}
			home := n.endpoint.HomeRelayStatus().Current()
			if home == nil || home.IsConnected() {
				failedSince = time.Time{}
				observed = ""
				n.mu.Unlock()
				continue
			}
			raw := home.URL.String()
			if observed != raw || failedSince.IsZero() {
				observed = raw
				failedSince = now
			}
			if now.Sub(failedSince) >= 2*time.Second && len(configured)-len(n.failedRelays) > 1 && configured[raw] {
				n.endpoint.RemoveRelay(home.URL)
				n.failedRelays[raw] = now.Add(30 * time.Second)
				failedSince = time.Time{}
				n.wakeDiscovery()
			}
			n.mu.Unlock()
		}
	}
}

func (n *Network) withRelayCandidates(addr netaddr.EndpointAddr) netaddr.EndpointAddr {
	n.mu.Lock()
	defer n.mu.Unlock()
	urls := n.relayURLsLocked()
	if len(urls) == 0 && n.localOnly {
		return addr
	}
	now := time.Now()
	filtered := netaddr.NewEndpointAddr(addr.ID)
	for _, ta := range addr.Addrs() {
		if relayAddr, ok := ta.(netaddr.RelayAddr); ok {
			if now.Before(n.failedRelays[relayAddr.URL.String()]) {
				continue
			}
			if !n.localOnly {
				allowed := false
				for _, raw := range urls {
					u, _ := netaddr.ParseRelayURL(raw)
					if u == relayAddr.URL {
						allowed = true
					}
				}
				if !allowed {
					continue
				}
			}
		}
		filtered = filtered.WithAddrs(ta)
	}
	for _, raw := range urls {
		url, err := netaddr.ParseRelayURL(raw)
		if err == nil && !now.Before(n.failedRelays[url.String()]) {
			filtered = filtered.WithRelayURL(url)
		}
	}
	return filtered
}
