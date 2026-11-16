package tailnet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

type Peer struct {
	ID       string    `json:"id"`
	HostName string    `json:"hostName"`
	DNSName  string    `json:"dnsName"`
	OS       string    `json:"os"`
	IPs      []string  `json:"ips"`
	Online   bool      `json:"online"`
	LastSeen time.Time `json:"lastSeen,omitempty"`
}

type Status struct {
	Installed bool     `json:"installed"`
	State     string   `json:"state"`
	NodeID    string   `json:"nodeID"`
	HostName  string   `json:"hostName"`
	DNSName   string   `json:"dnsName"`
	IPs       []string `json:"ips"`
	Peers     []Peer   `json:"peers"`
	Health    []string `json:"health"`
	Error     string   `json:"error,omitempty"`
	Stale     bool     `json:"stale,omitempty"`
}

type Client struct {
	Binary func() (string, error)
	Run    func(context.Context, string, ...string) ([]byte, error)
}

func Binary() (string, error) {
	if binary, err := exec.LookPath("tailscale"); err == nil {
		return binary, nil
	}
	candidates := []string{"/usr/local/bin/tailscale", "/opt/homebrew/bin/tailscale", "/usr/bin/tailscale"}
	if runtime.GOOS == "darwin" {
		candidates = append(candidates, "/Applications/Tailscale.app/Contents/MacOS/Tailscale")
	}
	if runtime.GOOS == "windows" {
		candidates = append(candidates, filepath.Join(os.Getenv("ProgramFiles"), "Tailscale", "tailscale.exe"))
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate, nil
		}
	}
	return "", errors.New("Tailscale is not installed")
}

func (client *Client) command(ctx context.Context, args ...string) ([]byte, error) {
	resolve := client.Binary
	if resolve == nil {
		resolve = Binary
	}
	binary, err := resolve()
	if err != nil {
		return nil, err
	}
	queryCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	if client.Run != nil {
		return client.Run(queryCtx, binary, args...)
	}
	return exec.CommandContext(queryCtx, binary, args...).Output()
}

func (client *Client) Status(ctx context.Context) Status {
	hostName, _ := os.Hostname()
	status := Status{HostName: hostName, IPs: []string{}, Peers: []Peer{}, Health: []string{}}
	resolve := client.Binary
	if resolve == nil {
		resolve = Binary
	}
	if _, err := resolve(); err != nil {
		status.State = "NotInstalled"
		return status
	}
	status.Installed = true
	output, commandErr := client.command(ctx, "status", "--json")
	var raw struct {
		BackendState string
		TailscaleIPs []string
		Self         *rawPeer
		Peer         map[string]rawPeer
		Health       []string
	}
	if err := json.Unmarshal(output, &raw); err != nil || raw.BackendState == "" {
		status.State = "Unavailable"
		status.Error = "Unable to read Tailscale status"
		if errors.Is(ctx.Err(), context.Canceled) {
			status.Error = "Tailscale query canceled"
		}
		return status
	}
	status.State = raw.BackendState
	if raw.Self != nil {
		status.NodeID = raw.Self.ID
		status.DNSName = strings.TrimSuffix(raw.Self.DNSName, ".")
		if raw.Self.HostName != "" {
			status.HostName = raw.Self.HostName
		}
	}
	if len(raw.Health) != 0 {
		status.Health = raw.Health
	}
	if status.State != "Running" {
		return status
	}
	if commandErr != nil {
		status.Error = "Tailscale status command reported an error"
	}
	status.IPs = validIPs(raw.TailscaleIPs)
	for key, entry := range raw.Peer {
		identity := entry.ID
		if identity == "" {
			identity = key
		}
		if identity == status.NodeID || identity == "" {
			continue
		}
		name := entry.HostName
		dns := strings.TrimSuffix(entry.DNSName, ".")
		if name == "" {
			name = strings.SplitN(dns, ".", 2)[0]
		}
		if name == "" {
			name = identity
		}
		status.Peers = append(status.Peers, Peer{
			ID: identity, HostName: name, DNSName: dns, OS: entry.OS,
			IPs: validIPs(entry.TailscaleIPs), Online: entry.Online, LastSeen: entry.LastSeen,
		})
	}
	sort.Slice(status.Peers, func(first, second int) bool {
		left, right := status.Peers[first], status.Peers[second]
		if left.Online != right.Online {
			return left.Online
		}
		if left.HostName != right.HostName {
			return strings.ToLower(left.HostName) < strings.ToLower(right.HostName)
		}
		return left.ID < right.ID
	})
	return status
}

type rawPeer struct {
	ID           string
	HostName     string
	DNSName      string
	OS           string
	TailscaleIPs []string
	Online       bool
	LastSeen     time.Time
}

func IsAddress(address netip.Addr) bool {
	address = address.Unmap()
	return netip.MustParsePrefix("100.64.0.0/10").Contains(address) ||
		netip.MustParsePrefix("fd7a:115c:a1e0::/48").Contains(address)
}

func validIPs(values []string) []string {
	addresses := []string{}
	for _, value := range values {
		address, err := netip.ParseAddr(value)
		if err == nil && IsAddress(address) {
			addresses = append(addresses, address.Unmap().String())
		}
	}
	sort.SliceStable(addresses, func(first, second int) bool {
		return strings.Contains(addresses[second], ":") && !strings.Contains(addresses[first], ":")
	})
	return addresses
}

func (client *Client) WhoIs(ctx context.Context, remote string) (string, error) {
	address, err := netip.ParseAddrPort(remote)
	if err != nil || !IsAddress(address.Addr()) {
		return "", errors.New("Tailscale source address required")
	}
	output, err := client.command(ctx, "whois", "--json", remote)
	if err != nil {
		return "", errors.New("unable to authenticate Tailscale peer")
	}
	var info struct {
		Node *struct {
			StableID  string
			Addresses []netip.Prefix
		}
	}
	if err := json.Unmarshal(output, &info); err != nil || info.Node == nil || info.Node.StableID == "" {
		return "", errors.New("Tailscale peer identity unavailable")
	}
	for _, prefix := range info.Node.Addresses {
		if prefix.Addr().Unmap() == address.Addr().Unmap() {
			return info.Node.StableID, nil
		}
	}
	return "", fmt.Errorf("Tailscale identity does not match source address")
}
