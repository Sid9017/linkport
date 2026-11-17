package p2p

import (
	"errors"

	"github.com/Sid9017/linkport/internal/relayservice"
)

// ConfigureHostedRelay is an explicit local operation. It does not change the
// endpoint's trust list or its outbound relay policy. Disable before rebinding.
func (n *Network) ConfigureHostedRelay(cfg relayservice.HostConfig) error {
	if !n.localOnly {
		return errors.New("public relay hosting is configured by the operator, not desktop users")
	}
	if err := validateHostedRelay(cfg); err != nil {
		return err
	}
	n.hostMu.Lock()
	defer n.hostMu.Unlock()
	n.mu.Lock()
	if n.stopped || n.endpoint == nil {
		n.mu.Unlock()
		return errors.New("network is not running")
	}
	old := n.hosted
	network := n.store.Config()
	if old.Status().Running && cfg.Enabled {
		if network.HostedRelay == cfg {
			n.mu.Unlock()
			return nil
		}
		n.mu.Unlock()
		return errors.New("disable the hosted relay before changing its listener")
	}
	var next *relayservice.Host
	var err error
	if cfg.Enabled {
		next, err = relayservice.StartHost(n.ctx, cfg)
		if err != nil {
			n.mu.Unlock()
			return err
		}
	}
	network.HostedRelay = cfg
	if err = n.store.SaveConfig(network); err != nil {
		n.mu.Unlock()
		if next != nil {
			_ = next.Close()
		}
		return err
	}
	n.hosted = next
	n.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	n.wakeDiscovery()
	return nil
}

func validateHostedRelay(cfg relayservice.HostConfig) error {
	if !cfg.Enabled {
		return nil
	}
	if cfg.MaxConnections < 0 || cfg.MaxConnections > 256 {
		return errors.New("hosted relay connection limit must be 1–256 (or 0 for default)")
	}
	if len(cfg.PublicURL) > 256 {
		return errors.New("hosted relay URL is too long")
	}
	if cfg.PublicURL != "" {
		return validateNetwork(NetworkConfig{RelayMode: "custom", RelayURLs: []string{cfg.PublicURL}})
	}
	return nil
}
