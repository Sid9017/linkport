// Package p2p provides Linkport's device identity, pairing and iroh transport.
package p2p

import (
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gofrs/flock"
	"github.com/tmc/go-iroh/endpointticket"
	"github.com/tmc/go-iroh/key"
)

type Peer struct {
	ID       string    `json:"id"`
	HostName string    `json:"hostName"`
	Ticket   string    `json:"ticket"`
	AddedAt  time.Time `json:"addedAt"`
}

// NetworkConfig is applied at startup. Public address publication contains only
// relay addresses, never the port inventory or private LAN addresses.
type NetworkConfig struct {
	RelayMode    string   `json:"relayMode"` // default, custom, disabled
	RelayURLs    []string `json:"relayURLs"`
	DiscoveryURL string   `json:"discoveryURL,omitempty"`
}

type diskState struct {
	Version int           `json:"version"`
	Seed    string        `json:"seed"`
	Name    string        `json:"name"`
	Peers   []Peer        `json:"peers"`
	Network NetworkConfig `json:"network"`
}

// Store owns an exclusive process lock: two Linkport processes must not silently
// replace the same identity or overwrite one another's trusted-device changes.
type Store struct {
	mu    sync.RWMutex
	path  string
	lock  *flock.Flock
	state diskState
}

func OpenStore(directory string) (_ *Store, err error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	lock := flock.New(filepath.Join(directory, "devices.lock"))
	locked, err := lock.TryLock()
	if err != nil {
		return nil, err
	}
	if !locked {
		return nil, errors.New("Linkport 正在使用此设备配置，请先关闭另一个实例")
	}
	defer func() {
		if err != nil {
			_ = lock.Close()
		}
	}()
	store := &Store{path: filepath.Join(directory, "devices.json"), lock: lock}
	info, err := os.Lstat(store.path)
	if errors.Is(err, os.ErrNotExist) {
		secret, keyErr := key.GenerateSecretKey()
		if keyErr != nil {
			return nil, keyErr
		}
		seed := secret.Bytes()
		name, _ := os.Hostname()
		if name == "" {
			name = "Linkport"
		}
		store.state = diskState{
			Version: 1, Seed: hex.EncodeToString(seed[:]), Name: name, Peers: []Peer{},
			Network: NetworkConfig{RelayMode: "default", RelayURLs: []string{}},
		}
		if err := store.save(store.state); err != nil {
			return nil, err
		}
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("device identity must be a regular file")
	}
	if info.Size() > 2*1024*1024 {
		return nil, errors.New("device configuration is too large")
	}
	if err := os.Chmod(store.path, 0600); err != nil {
		return nil, err
	}
	file, err := os.Open(store.path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&store.state); err != nil {
		return nil, fmt.Errorf("read device identity (file preserved): %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("device configuration must contain one JSON object")
	}
	if err := validateState(store.state); err != nil {
		return nil, err
	}
	return store, nil
}

func validateState(state diskState) error {
	if state.Version != 1 {
		return errors.New("unsupported device configuration version")
	}
	if _, err := key.ParseSecretKey(state.Seed); err != nil {
		return errors.New("invalid device identity; restore the original devices.json")
	}
	if err := validateName(state.Name); err != nil {
		return err
	}
	if err := validateNetwork(state.Network); err != nil {
		return err
	}
	seen := make(map[string]bool)
	if len(state.Peers) > 128 {
		return errors.New("trusted device limit exceeded")
	}
	for _, peer := range state.Peers {
		if _, err := validatePeer(peer); err != nil {
			return err
		}
		if seen[peer.ID] {
			return errors.New("duplicate trusted device")
		}
		seen[peer.ID] = true
	}
	return nil
}

func validateName(name string) error {
	if strings.TrimSpace(name) == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 80 {
		return errors.New("设备名称不能为空，且不能超过 80 个字符")
	}
	for _, r := range name {
		if r < 32 || r == 127 {
			return errors.New("设备名称包含控制字符")
		}
	}
	return nil
}

func validatePeer(peer Peer) (key.EndpointID, error) {
	if err := validateName(peer.HostName); err != nil {
		return key.EndpointID{}, err
	}
	id, err := key.ParseEndpointID(peer.ID)
	if err != nil || id.String() != peer.ID {
		return key.EndpointID{}, errors.New("invalid device public key")
	}
	if len(peer.Ticket) > 16*1024 {
		return key.EndpointID{}, errors.New("device address is too large")
	}
	address, err := endpointticket.Decode(peer.Ticket)
	if err != nil || address.ID != id || len(address.Addrs()) > 32 {
		return key.EndpointID{}, errors.New("device address does not match its public key")
	}
	return id, nil
}

func (store *Store) secret() key.SecretKey {
	store.mu.RLock()
	defer store.mu.RUnlock()
	secret, _ := key.ParseSecretKey(store.state.Seed)
	return secret
}

func (store *Store) Name() string {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return store.state.Name
}

func (store *Store) Peers() []Peer {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return append([]Peer{}, store.state.Peers...)
}

func (store *Store) Trusted(id string) bool {
	store.mu.RLock()
	defer store.mu.RUnlock()
	for _, peer := range store.state.Peers {
		if peer.ID == id {
			return true
		}
	}
	return false
}

func (store *Store) remember(peer Peer) error {
	if _, err := validatePeer(peer); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	secret, _ := key.ParseSecretKey(store.state.Seed)
	if peer.ID == secret.Public().String() {
		return errors.New("不能添加本机")
	}
	next := store.state
	next.Peers = append([]Peer{}, next.Peers...)
	for i, prior := range next.Peers {
		if prior.ID == peer.ID {
			peer.AddedAt = prior.AddedAt
			next.Peers[i] = peer
			return store.commit(next)
		}
	}
	if len(next.Peers) >= 128 {
		return errors.New("最多添加 128 台设备")
	}
	peer.AddedAt = time.Now().UTC()
	next.Peers = append(next.Peers, peer)
	return store.commit(next)
}

func (store *Store) forget(id string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	next := store.state
	next.Peers = []Peer{}
	for _, peer := range store.state.Peers {
		if peer.ID != id {
			next.Peers = append(next.Peers, peer)
		}
	}
	return store.commit(next)
}

func (store *Store) Config() NetworkConfig {
	store.mu.RLock()
	defer store.mu.RUnlock()
	cfg := store.state.Network
	cfg.RelayURLs = append([]string{}, cfg.RelayURLs...)
	return cfg
}

func (store *Store) SaveConfig(cfg NetworkConfig) error {
	if err := validateNetwork(cfg); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	next := store.state
	next.Network = cfg
	return store.commit(next)
}

func (store *Store) ResetIdentity(confirmation string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	old, _ := key.ParseSecretKey(store.state.Seed)
	if subtle.ConstantTimeCompare([]byte(confirmation), []byte(old.Public().String())) != 1 {
		return errors.New("请使用完整的本机设备 ID 确认重置")
	}
	fresh, err := key.GenerateSecretKey()
	if err != nil {
		return err
	}
	seed := fresh.Bytes()
	next := store.state
	next.Seed = hex.EncodeToString(seed[:])
	next.Peers = []Peer{}
	return store.commit(next)
}

func (store *Store) commit(next diskState) error {
	if err := store.save(next); err != nil {
		return err
	}
	store.state = next
	return nil
}

func (store *Store) save(next diskState) error {
	if err := validateState(next); err != nil {
		return err
	}
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(store.path), ".linkport-devices-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), store.path)
}

func (store *Store) Close() error { return store.lock.Close() }
