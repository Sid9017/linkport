package p2p

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/tmc/go-iroh/endpointticket"
	"github.com/tmc/go-iroh/key"
	"github.com/tmc/go-iroh/netaddr"
)

const discoveryTTL = time.Hour
const discoveryLimit = 32 << 10

// AddressRecord is a device-signed contact hint. It carries neither trust grants
// nor inventories. HostedRelayURL is an offer, never an instruction to change policy.
type AddressRecord struct {
	Version        int       `json:"version"`
	ID             string    `json:"id"`
	Sequence       uint64    `json:"sequence"`
	ExpiresAt      time.Time `json:"expiresAt"`
	Ticket         string    `json:"ticket"`
	HostedRelayURL string    `json:"hostedRelayURL,omitempty"`
	Signature      string    `json:"signature"`
}

func (r AddressRecord) signingBytes() []byte {
	r.Signature = ""
	b, _ := json.Marshal(r)
	return append([]byte("linkport-discovery-v1\n"), b...)
}

func (r AddressRecord) verify() error {
	if r.Version != 1 || r.Sequence == 0 || len(r.Ticket) > 8192 || len(r.Signature) != 128 || len(r.HostedRelayURL) > 256 {
		return errors.New("invalid discovery record")
	}
	id, err := key.ParseEndpointID(r.ID)
	if err != nil || id.String() != r.ID {
		return errors.New("invalid discovery identity")
	}
	address, err := endpointticket.Decode(r.Ticket)
	if err != nil || address.ID != id {
		return errors.New("discovery ticket identity mismatch")
	}
	var sig key.Signature
	if err = sig.UnmarshalText([]byte(r.Signature)); err != nil {
		return err
	}
	if err = id.PublicKey().Verify(r.signingBytes(), sig); err != nil {
		return errors.New("invalid discovery signature")
	}
	if r.HostedRelayURL != "" {
		if err = validateNetwork(NetworkConfig{RelayMode: "custom", RelayURLs: []string{r.HostedRelayURL}}); err != nil {
			return err
		}
	}
	return nil
}

func cloneRecords(in map[string]AddressRecord) map[string]AddressRecord {
	out := make(map[string]AddressRecord, len(in)+1)
	for id, r := range in {
		out[id] = r
	}
	return out
}

func (s *Store) record(id string) (AddressRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.state.Discovery[id]
	return r, ok
}

func (n *Network) ownRecord() (AddressRecord, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.stopped || n.endpoint == nil {
		return AddressRecord{}, errors.New("network is stopped")
	}
	ticket := endpointticket.Encode(n.contactAddress())
	s := n.store
	s.mu.Lock()
	defer s.mu.Unlock()
	secret, err := key.ParseSecretKey(s.state.Seed)
	if err != nil {
		return AddressRecord{}, err
	}
	id := secret.Public().String()
	offer := ""
	if n.hosted.Status().Running {
		offer = s.state.Network.HostedRelay.PublicURL
	}
	old := s.state.Discovery[id]
	if old.Ticket == ticket && old.HostedRelayURL == offer && time.Until(old.ExpiresAt) > discoveryTTL/2 {
		return old, nil
	}
	if s.state.DiscoverySeq == math.MaxUint64 {
		return AddressRecord{}, errors.New("discovery sequence exhausted")
	}
	next := s.state
	next.DiscoverySeq++
	r := AddressRecord{Version: 1, ID: id, Sequence: next.DiscoverySeq, ExpiresAt: time.Now().UTC().Add(discoveryTTL), Ticket: ticket, HostedRelayURL: offer}
	r.Signature = secret.Sign(r.signingBytes()).String()
	if err = r.verify(); err != nil {
		return AddressRecord{}, err
	}
	next.Discovery = cloneRecords(next.Discovery)
	next.Discovery[id] = r
	if err = s.commit(next); err != nil {
		return AddressRecord{}, err
	}
	return r, nil
}

// mergeRecord checks trust under the same lock as removal. An in-flight sync
// cannot resurrect a forgotten record; equal versions must have identical bytes.
func (n *Network) mergeRecord(r AddressRecord) error {
	if err := r.verify(); err != nil {
		return err
	}
	now := time.Now()
	if !r.ExpiresAt.After(now) || r.ExpiresAt.After(now.Add(2*discoveryTTL)) {
		return errors.New("discovery record expired or too far in the future")
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.stopped || !n.store.Trusted(r.ID) {
		return errors.New("discovery subject is not paired")
	}
	s := n.store
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.state.Discovery[r.ID]
	if ok && r.Sequence < old.Sequence {
		return errors.New("discovery rollback")
	}
	if ok && r.Sequence == old.Sequence {
		if r != old {
			return errors.New("conflicting discovery generation")
		}
		return nil
	}
	next := s.state
	next.Discovery = cloneRecords(next.Discovery)
	next.Discovery[r.ID] = r
	return s.commit(next)
}

type discoveryRequest struct {
	Record AddressRecord `json:"record"`
	Want   string        `json:"want,omitempty"`
}
type discoveryResponse struct {
	Self  AddressRecord  `json:"self"`
	Found *AddressRecord `json:"found,omitempty"`
}

func (n *Network) serveDiscovery(w http.ResponseWriter, r *http.Request, peerID string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", 405)
		return
	}
	var request discoveryRequest
	if err := decodeOne(http.MaxBytesReader(w, r.Body, discoveryLimit), &request); err != nil || request.Record.ID != peerID {
		http.Error(w, "invalid discovery request", 400)
		return
	}
	if request.Want != "" {
		if _, err := key.ParseEndpointID(request.Want); err != nil {
			http.Error(w, "invalid discovery subject", 400)
			return
		}
	}
	if err := n.mergeRecord(request.Record); err != nil {
		http.Error(w, "discovery record rejected", 403)
		return
	}
	self, err := n.ownRecord()
	if err != nil {
		http.Error(w, "discovery unavailable", 503)
		return
	}
	result := discoveryResponse{Self: self}
	// Exact-key queries only. Never enumerate or publish the local trust list.
	if request.Want != "" && n.Trusted(request.Want) {
		if found, ok := n.store.record(request.Want); ok && found.ExpiresAt.After(time.Now()) {
			result.Found = &found
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (n *Network) cachedAddress(id string, fallback netaddr.EndpointAddr) netaddr.EndpointAddr {
	if r, ok := n.store.record(id); ok && r.ExpiresAt.After(time.Now()) {
		if addr, err := endpointticket.Decode(r.Ticket); err == nil {
			return addr
		}
	}
	return fallback
}

// SyncDiscovery exchanges self records with one paired peer, and optionally
// asks for one already-paired target. It cannot add any device to the trust list.
func (n *Network) SyncDiscovery(ctx context.Context, peerID, want string) error {
	if want != "" && !n.Trusted(want) {
		return errors.New("discovery target is not paired")
	}
	var peer Peer
	for _, p := range n.Peers() {
		if p.ID == peerID {
			peer = p
			break
		}
	}
	if peer.ID == "" {
		return errors.New("discovery peer is not paired")
	}
	self, err := n.ownRecord()
	if err != nil {
		return err
	}
	data, _ := json.Marshal(discoveryRequest{Record: self, Want: want})
	addr, err := endpointticket.Decode(peer.Ticket)
	if err != nil {
		return err
	}
	addr = n.cachedAddress(peer.ID, addr)
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, ResponseHeaderTimeout: 3 * time.Second, MaxResponseHeaderBytes: 16 << 10}
	transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) { return n.dial(ctx, addr) }
	defer transport.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+peerID+"/v1/discovery", bytes.NewReader(data))
	if err != nil {
		return err
	}
	response, err := (&http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("discovery HTTP %d", response.StatusCode)
	}
	var result discoveryResponse
	if err = decodeOne(io.LimitReader(response.Body, discoveryLimit+1), &result); err != nil {
		return err
	}
	if result.Self.ID != peerID {
		return errors.New("discovery reply identity mismatch")
	}
	if err = n.mergeRecord(result.Self); err != nil {
		return err
	}
	if result.Found != nil {
		if result.Found.ID != want {
			return errors.New("unsolicited discovery record")
		}
		return n.mergeRecord(*result.Found)
	}
	return nil
}

func (n *Network) discoverViaPeers(ctx context.Context, target string) bool {
	lookup, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	before, _ := n.store.record(target)
	jobs := make(chan string, 128)
	for _, peer := range n.Peers() {
		if peer.ID != target {
			jobs <- peer.ID
		}
	}
	close(jobs)
	found := make(chan struct{}, 1)
	var workers sync.WaitGroup
	for range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for id := range jobs {
				if lookup.Err() != nil {
					return
				}
				_ = n.SyncDiscovery(lookup, id, target)
				if after, ok := n.store.record(target); ok && after.Sequence > before.Sequence {
					select {
					case found <- struct{}{}:
					default:
					}
					return
				}
			}
		}()
	}
	done := make(chan struct{})
	go func() { workers.Wait(); close(done) }()
	var success bool
	select {
	case <-found:
		success = true
	case <-done:
		select {
		case <-found:
			success = true
		default:
		}
	case <-lookup.Done():
	}
	cancel()
	<-done
	return success
}

func (n *Network) discoveryLoop() {
	offset := 0
	for {
		ctx, cancel := context.WithTimeout(n.ctx, 8*time.Second)
		var wg sync.WaitGroup
		slots := make(chan struct{}, 4)
		peers := n.Peers()
		if len(peers) > 0 {
			offset %= len(peers)
			peers = append(peers[offset:], peers[:offset]...)
		}
		scheduled := 0
		for _, peer := range peers {
			if ctx.Err() != nil {
				break
			}
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				continue
			}
			scheduled++
			wg.Add(1)
			go func(id string) { defer wg.Done(); defer func() { <-slots }(); _ = n.SyncDiscovery(ctx, id, "") }(peer.ID)
		}
		offset += scheduled
		wg.Wait()
		cancel()
		timer := time.NewTimer(30 * time.Second)
		select {
		case <-n.ctx.Done():
			timer.Stop()
			return
		case <-n.syncWake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (n *Network) wakeDiscovery() {
	select {
	case n.syncWake <- struct{}{}:
	default:
	}
}
