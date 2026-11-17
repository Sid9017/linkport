package p2p

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/tmc/go-iroh/endpointticket"
	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/iroh/mdns"
	"github.com/tmc/go-iroh/key"
	"github.com/tmc/go-iroh/netaddr"
)

const ALPN = "linkport/1"

type Options struct {
	Directory string
	Disabled  bool
	// LocalOnly isolates automated tests from public relay/discovery services.
	LocalOnly   bool
	bindOptions []iroh.Option
}

type Status struct {
	State           string        `json:"state"`
	NodeID          string        `json:"nodeID"`
	HostName        string        `json:"hostName"`
	Ticket          string        `json:"ticket"`
	HomeRelay       string        `json:"homeRelay,omitempty"`
	RelayConnected  bool          `json:"relayConnected"`
	Config          NetworkConfig `json:"config"`
	RestartRequired bool          `json:"restartRequired"`
	Error           string        `json:"error,omitempty"`
}

type Invitation struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type invitePayload struct {
	Version int       `json:"version"`
	Peer    Peer      `json:"peer"`
	Token   string    `json:"token"`
	Expires time.Time `json:"expires"`
}

type pairRequest struct {
	Peer  Peer   `json:"peer"`
	Token string `json:"token"`
}

type pairingState struct {
	pending int
}

type peerContextKey struct{}

type Network struct {
	ctx       context.Context
	store     *Store
	endpoint  *iroh.Endpoint
	listener  *iroh.StreamListener
	publisher *iroh.PkarrPublisher
	cancel    context.CancelFunc
	mu        sync.Mutex
	dialLocks [32]sync.Mutex
	server    *http.Server
	conns     map[string]map[*iroh.Conn]struct{}
	invites   map[[32]byte]time.Time
	pairing   map[string]*pairingState
	restart   bool
	stopped   bool
	closeOnce sync.Once
}

func New(ctx context.Context, opts Options) (_ *Network, err error) {
	store, err := OpenStore(opts.Directory)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	n := &Network{
		ctx: ctx, store: store, cancel: cancel, conns: make(map[string]map[*iroh.Conn]struct{}),
		invites: make(map[[32]byte]time.Time), pairing: make(map[string]*pairingState),
	}
	defer func() {
		if err != nil {
			n.Close()
		}
	}()
	if opts.Disabled {
		return n, nil
	}
	secret := store.secret()
	cfg := store.Config()
	var lookup iroh.AddressLookupServices
	options := []iroh.Option{
		iroh.WithSecretKey(secret), iroh.WithALPNs(ALPN), iroh.WithHooks(n),
		iroh.WithAddressLookup(&lookup), iroh.WithSourceAddressValidation(func(net.Addr) bool { return true }),
		iroh.WithTransportConfig(&iroh.QUICTransportConfig{
			KeepAlivePeriod: 15 * time.Second, MaxIdleTimeout: time.Minute, MaxIncomingStreams: 128,
		}),
	}
	if opts.LocalOnly {
		options = append(options, iroh.WithBindAddr(netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), 0)))
	} else {
		options = append(options, iroh.WithRelayMode(relayMode(cfg)))
		lan := mdns.New(secret.Public().EndpointID())
		lookup.AddResolver(lan)
		lookup.AddPublisher(lan)
		go func() { _ = lan.Start(ctx) }()
		if cfg.RelayMode != "disabled" {
			if cfg.DiscoveryURL == "" && cfg.RelayMode == "default" {
				n.publisher, err = iroh.N0PkarrPublisher(secret, nil)
				lookup.AddResolver(iroh.N0DNSAddressLookup(nil))
			} else if cfg.DiscoveryURL != "" {
				n.publisher, err = iroh.NewPkarrPublisher(secret, cfg.DiscoveryURL, nil)
				if err == nil {
					var resolver *iroh.PkarrResolver
					resolver, err = iroh.NewPkarrResolver(cfg.DiscoveryURL, nil)
					if err == nil {
						lookup.AddResolver(resolver)
					}
				}
			}
			if err != nil {
				return nil, err
			}
			if n.publisher != nil {
				lookup.AddPublisher(n.publisher)
			}
		}
	}
	n.endpoint, err = iroh.Bind(ctx, append(options, opts.bindOptions...)...)
	if err != nil {
		return nil, err
	}
	n.listener, err = n.endpoint.ListenStreams()
	if err != nil {
		return nil, err
	}
	return n, nil
}

func (n *Network) Status() Status {
	n.mu.Lock()
	defer n.mu.Unlock()
	state := Status{
		State: "Disabled", NodeID: n.store.secret().Public().String(), HostName: n.store.Name(),
		Config: n.store.Config(), RestartRequired: n.restart,
	}
	if n.endpoint != nil && !n.stopped {
		state.State = "Running"
		state.Ticket = endpointticket.Encode(n.endpoint.Addr())
		if home := n.endpoint.HomeRelayStatus().Current(); home != nil {
			state.HomeRelay = home.URL.String()
			state.RelayConnected = home.IsConnected()
		}
	}
	if n.restart {
		state.State = "RestartRequired"
	}
	return state
}

func (n *Network) Peers() []Peer          { return n.store.Peers() }
func (n *Network) Trusted(id string) bool { return n.store.Trusted(id) }

func (n *Network) Listener() net.Listener {
	if n.listener == nil {
		return nil
	}
	return challengeListener{Listener: n.listener}
}

// ConnContext uses the QUIC-authenticated identity, never an HTTP header,
// caller-supplied node ID, source IP address or relay identity.
func (n *Network) ConnContext(ctx context.Context, conn net.Conn) context.Context {
	if authenticated, ok := conn.(interface{ RemoteID() key.EndpointID }); ok {
		return context.WithValue(ctx, peerContextKey{}, authenticated.RemoteID().String())
	}
	return ctx
}

func (n *Network) Authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ := r.Context().Value(peerContextKey{}).(string)
		if id == "" || r.Header.Get("Origin") != "" {
			http.Error(w, "authenticated Linkport device required", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/v1/pair" {
			n.acceptPair(w, r, id)
			return
		}
		if !n.store.Trusted(id) {
			http.Error(w, "device is not paired", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (n *Network) Serve(handler http.Handler) {
	if n.listener == nil {
		return
	}
	server := &http.Server{
		Handler: n.Authorize(handler), ConnContext: n.ConnContext,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 * 1024,
	}
	n.mu.Lock()
	if n.stopped {
		n.mu.Unlock()
		return
	}
	n.server = server
	n.mu.Unlock()
	_ = server.Serve(n.Listener())
}

// NewClient is restricted to a single endpoint identity. HTTP redirects and
// environment proxy settings cannot redirect requests onto the host network.
func (n *Network) NewClient(id string, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			Proxy: nil, MaxIdleConns: 2, MaxIdleConnsPerHost: 2, IdleConnTimeout: 15 * time.Second,
			MaxResponseHeaderBytes: 16 * 1024, ResponseHeaderTimeout: 5 * time.Second,
			DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
				host, _, err := net.SplitHostPort(address)
				if err != nil || host != id {
					return nil, errors.New("request target differs from paired device")
				}
				return n.Dial(ctx, id)
			},
		},
	}
}

func (n *Network) Dial(ctx context.Context, id string) (net.Conn, error) {
	for _, peer := range n.store.Peers() {
		if peer.ID == id {
			addr, err := endpointticket.Decode(peer.Ticket)
			if err != nil {
				return nil, err
			}
			return n.dial(ctx, addr)
		}
	}
	return nil, errors.New("设备未配对或已移除")
}

func (n *Network) dial(ctx context.Context, addr netaddr.EndpointAddr) (net.Conn, error) {
	digest := sha256.Sum256([]byte(addr.ID.String()))
	lock := &n.dialLocks[int(digest[0])%len(n.dialLocks)]
	lock.Lock()
	defer lock.Unlock()
	n.mu.Lock()
	closed := n.stopped || n.restart || n.endpoint == nil
	allowed := n.store.Trusted(addr.ID.String()) || n.pairing[addr.ID.String()] != nil
	n.mu.Unlock()
	if closed {
		return nil, errors.New("iroh 网络未运行")
	}
	if !allowed {
		return nil, errors.New("设备未配对或已移除")
	}
	var conn *iroh.Conn
	var stream net.Conn
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		n.mu.Lock()
		for candidate := range n.conns[addr.ID.String()] {
			if candidate.Context().Err() == nil {
				conn = candidate
				break
			}
		}
		n.mu.Unlock()
		if conn == nil {
			cachedCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			conn, err = n.endpoint.Connect(cachedCtx, addr, ALPN)
			cancel()
			if err != nil && ctx.Err() == nil {
				// A saved ticket can become stale after changing networks.
				conn, err = n.endpoint.Connect(ctx, netaddr.NewEndpointAddr(addr.ID), ALPN)
			}
			if err != nil {
				return nil, err
			}
		}
		stream, err = conn.OpenStreamConn(ctx)
		if err == nil {
			break
		}
		_ = conn.Close()
		n.mu.Lock()
		delete(n.conns[addr.ID.String()], conn)
		n.mu.Unlock()
		conn = nil
	}
	if err != nil {
		return nil, err
	}
	// Closing this stream leaves other sessions on the peer connection intact.
	owned := stream
	deadline := time.Now().Add(5 * time.Second)
	if limit, ok := ctx.Deadline(); ok && limit.Before(deadline) {
		deadline = limit
	}
	_ = owned.SetDeadline(deadline)
	defer owned.SetDeadline(time.Time{})
	if _, err := io.WriteString(owned, "LP1\n"); err != nil {
		owned.Close()
		return nil, err
	}
	var nonce [32]byte
	if _, err := io.ReadFull(owned, nonce[:]); err != nil {
		owned.Close()
		return nil, err
	}
	if _, err := owned.Write(nonce[:]); err != nil {
		owned.Close()
		return nil, err
	}
	return owned, nil
}

// Every stream completes a fresh server challenge before HTTP is parsed.
// go-iroh may resume QUIC sessions with 0-RTT enabled; a previous stream's early
// data cannot replay pairing, serial opens or serial writes past this barrier.
type challengeListener struct{ net.Listener }

func (l challengeListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &challengeConn{Conn: conn}, nil
}

type challengeConn struct {
	net.Conn
	once sync.Once
	err  error
}

func (c *challengeConn) RemoteID() key.EndpointID {
	return c.Conn.(interface{ RemoteID() key.EndpointID }).RemoteID()
}

func (c *challengeConn) Read(p []byte) (int, error) {
	c.once.Do(func() {
		_ = c.Conn.SetDeadline(time.Now().Add(5 * time.Second))
		defer func() {
			_ = c.Conn.SetWriteDeadline(time.Time{})
			_ = c.Conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		}()
		var prefix [4]byte
		if _, c.err = io.ReadFull(c.Conn, prefix[:]); c.err != nil {
			return
		}
		if string(prefix[:]) != "LP1\n" {
			c.err = errors.New("invalid Linkport stream")
			return
		}
		var nonce, echoed [32]byte
		if _, c.err = rand.Read(nonce[:]); c.err != nil {
			return
		}
		if _, c.err = c.Conn.Write(nonce[:]); c.err != nil {
			return
		}
		if _, c.err = io.ReadFull(c.Conn, echoed[:]); c.err != nil {
			return
		}
		if nonce != echoed {
			c.err = errors.New("invalid Linkport stream challenge")
		}
	})
	if c.err != nil {
		return 0, c.err
	}
	return c.Conn.Read(p)
}

func (n *Network) BeforeConnect(_ context.Context, addr netaddr.EndpointAddr, _ string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.stopped || n.restart || (!n.store.Trusted(addr.ID.String()) && n.pairing[addr.ID.String()] == nil) {
		return errors.New("device is not paired")
	}
	return nil
}

func (n *Network) AfterHandshake(_ context.Context, conn *iroh.Conn) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	id := conn.RemoteID().String()
	allowed := n.store.Trusted(id) || n.pairing[id] != nil
	for _, expires := range n.invites {
		allowed = allowed || time.Now().Before(expires)
	}
	if n.stopped || n.restart || !allowed {
		return iroh.RejectHandshake(1, "pairing required")
	}
	total := 0
	for _, conns := range n.conns {
		total += len(conns)
	}
	if total >= 256 || len(n.conns[id]) >= 64 {
		return iroh.RejectHandshake(2, "connection limit exceeded")
	}
	if n.conns[id] == nil {
		n.conns[id] = make(map[*iroh.Conn]struct{})
	}
	n.conns[id][conn] = struct{}{}
	if conn.Side() == iroh.SideClient && n.listener != nil {
		// The endpoint listener accepts incoming connections. Outbound
		// connections also carry peer-initiated streams, so dispatch those to
		// the same HTTP listener instead of leaving them unread.
		go func() { _ = n.listener.Handler().Accept(n.ctx, conn) }()
	}
	if !n.store.Trusted(id) {
		time.AfterFunc(30*time.Second, func() {
			if !n.store.Trusted(id) {
				_ = conn.CloseWithError(1, "pairing not completed")
			}
		})
	}
	go func() {
		<-conn.Context().Done()
		n.mu.Lock()
		delete(n.conns[id], conn)
		if len(n.conns[id]) == 0 {
			delete(n.conns, id)
		}
		n.mu.Unlock()
	}()
	return nil
}

func (n *Network) self() Peer {
	return Peer{
		ID: n.endpoint.ID().String(), HostName: n.store.Name(),
		Ticket: endpointticket.Encode(n.endpoint.Addr()),
	}
}

func (n *Network) CreateInvitation() (Invitation, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.stopped || n.endpoint == nil || n.restart {
		return Invitation{}, errors.New("请先启动或重启 Linkport 网络")
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Invitation{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(random[:])
	expires := time.Now().UTC().Add(5 * time.Minute)
	payload := invitePayload{Version: 1, Peer: n.self(), Token: token, Expires: expires}
	data, err := json.Marshal(payload)
	if err != nil {
		return Invitation{}, err
	}
	// Creating a replacement invitation immediately revokes the previous one.
	n.invites = map[[32]byte]time.Time{sha256.Sum256([]byte(token)): expires}
	return Invitation{Code: "linkport1." + base64.RawURLEncoding.EncodeToString(data), ExpiresAt: expires}, nil
}

func parseInvitation(code string) (invitePayload, error) {
	var payload invitePayload
	if len(code) > 24*1024 {
		return payload, errors.New("配对邀请过长")
	}
	raw, ok := strings.CutPrefix(strings.TrimSpace(code), "linkport1.")
	if !ok {
		return payload, errors.New("无效的 Linkport 配对邀请")
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return payload, errors.New("无效的 Linkport 配对邀请")
	}
	if err := decodeOne(bytes.NewReader(data), &payload); err != nil {
		return payload, errors.New("无效的 Linkport 配对邀请")
	}
	token, err := base64.RawURLEncoding.DecodeString(payload.Token)
	if err != nil || len(token) != 32 || payload.Version != 1 {
		return payload, errors.New("无效的配对密钥或版本")
	}
	if !time.Now().Before(payload.Expires) || time.Until(payload.Expires) > 6*time.Minute {
		return payload, errors.New("配对邀请已过期，请在另一台机器重新生成")
	}
	if _, err := validatePeer(payload.Peer); err != nil {
		return payload, err
	}
	return payload, nil
}

func (n *Network) AddDevice(ctx context.Context, code string) (Peer, error) {
	payload, err := parseInvitation(code)
	if err != nil {
		return Peer{}, err
	}
	n.mu.Lock()
	if n.stopped || n.endpoint == nil || n.restart || payload.Peer.ID == n.endpoint.ID().String() {
		n.mu.Unlock()
		return Peer{}, errors.New("无法配对本机或未运行的网络")
	}
	pairing := n.pairing[payload.Peer.ID]
	if pairing == nil {
		pairing = &pairingState{}
		n.pairing[payload.Peer.ID] = pairing
	}
	pairing.pending++
	self := n.self()
	n.mu.Unlock()
	defer func() {
		n.mu.Lock()
		if n.pairing[payload.Peer.ID] == pairing {
			pairing.pending--
			if pairing.pending == 0 {
				delete(n.pairing, payload.Peer.ID)
			}
		}
		n.mu.Unlock()
	}()
	addr, _ := endpointticket.Decode(payload.Peer.Ticket)
	client := n.NewClient(payload.Peer.ID, 20*time.Second)
	client.Transport.(*http.Transport).DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return n.dial(ctx, addr)
	}
	defer client.CloseIdleConnections()
	body, _ := json.Marshal(pairRequest{Peer: self, Token: payload.Token})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+payload.Peer.ID+"/v1/pair", bytes.NewReader(body))
	if err != nil {
		return Peer{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return Peer{}, fmt.Errorf("无法联系邀请设备：%w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Peer{}, errors.New("配对被拒绝，邀请可能已使用或失效")
	}
	var peer Peer
	if err := decodeOne(io.LimitReader(response.Body, 20*1024), &peer); err != nil {
		return Peer{}, errors.New("无效的配对响应")
	}
	if _, err := validatePeer(peer); err != nil || peer.ID != payload.Peer.ID {
		return Peer{}, errors.New("配对设备身份不匹配")
	}
	return n.rememberPairing(peer, pairing)
}

func (n *Network) rememberPairing(peer Peer, pairing *pairingState) (Peer, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.stopped || n.restart || pairing == nil || n.pairing[peer.ID] != pairing {
		return Peer{}, errors.New("配对已取消")
	}
	if err := n.store.remember(peer); err != nil {
		return Peer{}, err
	}
	return peer, nil
}

func (n *Network) acceptPair(w http.ResponseWriter, r *http.Request, remoteID string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var pair pairRequest
	if err := decodeOne(http.MaxBytesReader(w, r.Body, 20*1024), &pair); err != nil {
		http.Error(w, "invalid pairing request", http.StatusBadRequest)
		return
	}
	if _, err := validatePeer(pair.Peer); err != nil || pair.Peer.ID != remoteID {
		http.Error(w, "pairing identity mismatch", http.StatusForbidden)
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	digest := sha256.Sum256([]byte(pair.Token))
	expires, ok := n.invites[digest]
	if n.stopped || !ok || !time.Now().Before(expires) {
		http.Error(w, "invitation unavailable", http.StatusForbidden)
		return
	}
	if err := n.store.remember(pair.Peer); err != nil {
		http.Error(w, "unable to save paired device", http.StatusInternalServerError)
		return
	}
	delete(n.invites, digest)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(n.self())
}

func decodeOne(reader io.Reader, value any) error {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("expected one JSON object")
	}
	return nil
}

func (n *Network) RemoveDevice(id string) error {
	n.mu.Lock()
	if err := n.store.forget(id); err != nil {
		n.mu.Unlock()
		return err
	}
	conns := make([]*iroh.Conn, 0, len(n.conns[id]))
	for conn := range n.conns[id] {
		conns = append(conns, conn)
	}
	clear(n.invites)
	delete(n.pairing, id)
	n.mu.Unlock()
	for _, conn := range conns {
		_ = conn.CloseWithError(3, "device removed")
	}
	return nil
}

func (n *Network) SaveConfig(cfg NetworkConfig) error {
	n.mu.Lock()
	if err := n.store.SaveConfig(cfg); err != nil {
		n.mu.Unlock()
		return err
	}
	n.restart = true
	n.invites = make(map[[32]byte]time.Time)
	n.mu.Unlock()
	// Stop the old transports immediately so disabling public services takes
	// effect before restart, rather than continuing to publish the old state.
	n.stopTransport()
	return nil
}

func (n *Network) ResetIdentity(confirmation string) error {
	n.mu.Lock()
	if err := n.store.ResetIdentity(confirmation); err != nil {
		n.mu.Unlock()
		return err
	}
	n.restart = true
	n.invites = make(map[[32]byte]time.Time)
	n.mu.Unlock()
	n.stopTransport()
	return nil
}

func (n *Network) stopTransport() {
	n.mu.Lock()
	if n.stopped {
		n.mu.Unlock()
		return
	}
	n.stopped = true
	server := n.server
	n.mu.Unlock()
	n.cancel()
	if server != nil {
		_ = server.Close()
	}
	if n.listener != nil {
		_ = n.listener.Close()
	}
	if n.endpoint != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = n.endpoint.Shutdown(ctx)
	}
	if n.publisher != nil {
		_ = n.publisher.Close()
	}
}

func (n *Network) Close() {
	n.closeOnce.Do(func() {
		n.stopTransport()
		_ = n.store.Close()
	})
}
