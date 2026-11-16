package p2p

import (
	"errors"
	"net"
	"net/url"

	"github.com/tmc/go-iroh/netaddr"
	"github.com/tmc/go-iroh/relay"
)

func validateNetwork(cfg NetworkConfig) error {
	switch cfg.RelayMode {
	case "default", "disabled":
		if len(cfg.RelayURLs) != 0 {
			return errors.New("自定义中继地址需要选择 custom 模式")
		}
	case "custom":
		if len(cfg.RelayURLs) == 0 || len(cfg.RelayURLs) > 8 {
			return errors.New("请配置 1–8 个 HTTPS 中继地址")
		}
	default:
		return errors.New("未知的中继模式")
	}
	for _, raw := range append(append([]string{}, cfg.RelayURLs...), cfg.DiscoveryURL) {
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("无效的中继或地址发现服务地址")
		}
		local := u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback()
		if u.Scheme != "https" && !(u.Scheme == "http" && local) {
			return errors.New("服务地址必须使用 HTTPS；仅 loopback 测试地址允许 HTTP")
		}
	}
	for _, raw := range cfg.RelayURLs {
		if _, err := netaddr.ParseRelayURL(raw); err != nil {
			return errors.New("无效的中继地址")
		}
	}
	return nil
}

func relayMode(cfg NetworkConfig) relay.Mode {
	switch cfg.RelayMode {
	case "disabled":
		return relay.ModeDisabled()
	case "custom":
		var urls []netaddr.RelayURL
		for _, raw := range cfg.RelayURLs {
			u, _ := netaddr.ParseRelayURL(raw)
			urls = append(urls, u)
		}
		return relay.ModeCustomURLs(urls...)
	default:
		return relay.ModeDefault()
	}
}
