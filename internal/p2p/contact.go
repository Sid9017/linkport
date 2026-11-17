package p2p

import (
	"net"
	"net/netip"

	"github.com/tmc/go-iroh/netaddr"
)

// contactAddress includes LAN candidates in invitations and authenticated
// contact exchange. The pinned endpoint does not enumerate interface addresses
// when its UDP socket is bound to an unspecified address. Public pkarr
// publication remains the endpoint's separate relay-only publisher.
func (n *Network) contactAddress() netaddr.EndpointAddr {
	address := n.endpoint.Addr()
	bound := n.endpoint.LocalAddr()
	if n.localOnly || !bound.IsValid() || bound.Port() == 0 || !bound.Addr().IsUnspecified() {
		return address
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return address
	}
	var ips []netip.Addr
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&(net.FlagLoopback|net.FlagPointToPoint) != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, raw := range addrs {
			prefix, err := netip.ParsePrefix(raw.String())
			if err == nil {
				ips = append(ips, prefix.Addr())
			}
		}
	}
	return withContactIPs(address, bound, ips)
}

func withContactIPs(address netaddr.EndpointAddr, bound netip.AddrPort, ips []netip.Addr) netaddr.EndpointAddr {
	seen := make(map[netip.AddrPort]bool)
	for _, ip := range address.IPAddrs() {
		seen[ip] = true
	}
	count := len(address.Addrs())
	for _, ip := range ips {
		ip = ip.Unmap()
		if count >= 32 {
			break
		}
		if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
			(bound.Addr().Is4() && !ip.Is4()) || bound.Port() == 0 {
			continue
		}
		candidate := netip.AddrPortFrom(ip, bound.Port())
		if seen[candidate] {
			continue
		}
		address = address.WithIP(candidate)
		seen[candidate] = true
		count++
	}
	return address
}
