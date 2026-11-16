package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sid9017/linkport/internal/api"
	"github.com/Sid9017/linkport/internal/bridge"
	"github.com/Sid9017/linkport/internal/config"
	"github.com/Sid9017/linkport/internal/rfc2217"
	"github.com/Sid9017/linkport/internal/serialport"
	"github.com/Sid9017/linkport/internal/session"
	"github.com/Sid9017/linkport/internal/share"
	"github.com/Sid9017/linkport/internal/tailnet"
	"github.com/Sid9017/linkport/internal/version"
	"github.com/Sid9017/linkport/internal/vport"
	"go.bug.st/serial"
)

const ServicePort = 7700

type Tailnet interface {
	Status(context.Context) tailnet.Status
	WhoIs(context.Context, string) (string, error)
}

type Device struct {
	tailnet.Peer
	Service string             `json:"service"`
	Version string             `json:"version,omitempty"`
	Bridge  bool               `json:"bridge"`
	Ports   []share.SharedPort `json:"ports"`
	Error   string             `json:"error,omitempty"`
	address string
}

type State struct {
	Tailscale    tailnet.Status     `json:"tailscale"`
	LocalPorts   []share.LocalPort  `json:"localPorts"`
	SharedPorts  []share.SharedPort `json:"sharedPorts"`
	Devices      []Device           `json:"devices"`
	Sessions     []session.State    `json:"sessions"`
	Refreshing   bool               `json:"refreshing"`
	UpdatedAt    time.Time          `json:"updatedAt"`
	LocalError   string             `json:"localError,omitempty"`
	ServiceError string             `json:"serviceError,omitempty"`
	Listening    []string           `json:"listening"`
}

type Options struct {
	Directory              string
	DeviceDirectory        string
	Tailnet                Tailnet
	Enumerator             serialport.Enumerator
	SharingConfig          string
	DisableNetworkListener bool
}

type Engine struct {
	ctx                    context.Context
	tailnet                Tailnet
	manager                *share.Manager
	sessions               *session.Manager
	mu                     sync.Mutex
	state                  State
	refresh                chan struct{}
	client                 *http.Client
	listeners              map[string]*http.Server
	bridge                 *bridge.Server
	broker                 *serialport.Broker
	nodeID                 string
	disableNetworkListener bool
}

func New(ctx context.Context, options Options) (*Engine, error) {
	if options.Tailnet == nil {
		options.Tailnet = &tailnet.Client{}
	}
	if options.Enumerator == nil {
		options.Enumerator = serialport.NewEnumerator()
	}
	if options.Directory == "" {
		return nil, errors.New("configuration directory required")
	}
	if options.DeviceDirectory == "" {
		homeDirectory, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		options.DeviceDirectory = filepath.Join(homeDirectory, ".linkport", "dev")
	}
	if options.SharingConfig == "" {
		options.SharingConfig = filepath.Join(options.Directory, "shares.json")
	}
	manager, err := share.NewManager(ctx, options.Enumerator, config.NewFileStore(options.SharingConfig), share.Options{})
	if err != nil {
		return nil, err
	}
	engine := &Engine{
		ctx: ctx, tailnet: options.Tailnet, manager: manager,
		sessions: session.New(options.DeviceDirectory),
		refresh:  make(chan struct{}, 1), listeners: make(map[string]*http.Server),
		disableNetworkListener: options.DisableNetworkListener,
		state:                  State{Devices: []Device{}, Listening: []string{}},
		client: &http.Client{
			Timeout: 2 * time.Second,
			Transport: &http.Transport{
				Proxy: nil, DialContext: (&net.Dialer{Timeout: time.Second, KeepAlive: 15 * time.Second}).DialContext,
				MaxIdleConns: 32, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second,
				ResponseHeaderTimeout: 2 * time.Second, MaxResponseHeaderBytes: 16 * 1024,
			},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	engine.broker = serialport.NewBroker()
	engine.bridge = engine.newBridge()
	return engine, nil
}

func (engine *Engine) Run() {
	ctx, cancel := context.WithCancel(engine.ctx)
	defer cancel()
	defer engine.client.CloseIdleConnections()
	defer engine.broker.Close()
	defer engine.closeNetwork()
	defer engine.sessions.DisconnectAll(context.Canceled)
	go engine.watchLocal(ctx)
	for {
		engine.update(ctx)
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-engine.refresh:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (engine *Engine) Refresh() {
	select {
	case engine.refresh <- struct{}{}:
	default:
	}
}

func (engine *Engine) State() State {
	engine.mu.Lock()
	state := engine.state
	state.Devices = append([]Device{}, state.Devices...)
	state.Listening = append([]string{}, state.Listening...)
	engine.mu.Unlock()
	state.LocalPorts = engine.manager.LocalPorts()
	state.SharedPorts = engine.manager.Snapshot().Ports
	state.Sessions = engine.sessions.States()
	return state
}

func (engine *Engine) SetShared(ctx context.Context, identity string, enabled bool) error {
	if err := engine.manager.Refresh(ctx); err != nil {
		return err
	}
	return engine.manager.SetShared(ctx, identity, enabled)
}

func (engine *Engine) Sessions() *session.Manager { return engine.sessions }

func (engine *Engine) watchLocal(ctx context.Context) {
	for {
		err := engine.manager.Refresh(ctx)
		if err == nil {
			available := make(map[string]string)
			for _, port := range engine.manager.LocalPorts() {
				available[port.Path] = port.ID
			}
			engine.sessions.ReconcileLocal(available)
		}
		engine.mu.Lock()
		engine.state.LocalError = ""
		if err != nil {
			engine.state.LocalError = err.Error()
		}
		engine.mu.Unlock()
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (engine *Engine) Connect(ctx context.Context, nodeID, portID string, baud int, virtual vport.Options) (session.State, error) {
	if err := ctx.Err(); err != nil {
		return session.State{}, err
	}
	if baud == 0 {
		baud = bridge.DefaultSettings.BaudRate
	}
	if err := validateBaud(baud); err != nil {
		return session.State{}, err
	}
	state := engine.State()
	if state.Tailscale.Stale {
		return session.State{}, errors.New("Tailscale 状态暂时无法确认，请稍后刷新")
	}
	if state.Tailscale.State != "Running" {
		return session.State{}, errors.New("请先登录并连接 Tailscale")
	}
	for _, device := range state.Devices {
		if device.ID != nodeID {
			continue
		}
		if !device.Online || device.Service != "available" || !device.Bridge {
			return session.State{}, errors.New("对端 Linkport 串口服务不可用")
		}
		for _, port := range device.Ports {
			if port.ID != portID {
				continue
			}
			if !port.Online || port.Ambiguous {
				return session.State{}, errors.New("远端串口离线或身份不明确")
			}
			label := port.Product
			if label == "" {
				label = port.Path
			}
			return engine.sessions.Connect(engine.ctx, session.Target{
				NodeID: device.ID, HostName: device.HostName, PortID: port.ID,
				PortPath: port.Path, Label: label, Address: device.address, BaudRate: baud, VPort: virtual,
			})
		}
		return session.State{}, errors.New("对端已取消共享该串口")
	}
	return session.State{}, errors.New("Tailscale 设备不在当前可访问的目录中")
}

func (engine *Engine) MonitorLocal(ctx context.Context, identity, path string, baud int) (session.State, error) {
	if baud == 0 {
		baud = bridge.DefaultSettings.BaudRate
	}
	if err := validateBaud(baud); err != nil {
		return session.State{}, err
	}
	if err := engine.manager.Refresh(ctx); err != nil {
		return session.State{}, err
	}
	for _, port := range engine.manager.LocalPorts() {
		if port.ID != identity || port.Path != path {
			continue
		}
		hostName, _ := os.Hostname()
		label := port.Product
		if label == "" {
			label = port.Path
		}
		mode := rfc2217.Mode(vport.LineSettings{BaudRate: baud, DataBits: 8})
		mode.InitialStatusBits = nil
		return engine.sessions.MonitorLocal(engine.ctx, session.Target{
			HostName: hostName, PortID: port.ID, PortPath: port.Path, Label: label, BaudRate: baud,
		}, func() (serial.Port, error) { return engine.broker.Open(port.Path, mode) })
	}
	return session.State{}, errors.New("本机串口已不存在，请刷新后重新选择")
}

func (engine *Engine) newBridge() *bridge.Server {
	server := bridge.NewServer(engine.manager)
	server.Open = engine.broker.Open
	return server
}

func validateBaud(baud int) error {
	if baud < 1 || baud > 4000000 {
		return errors.New("波特率范围为 1–4000000")
	}
	return nil
}

func (engine *Engine) update(ctx context.Context) {
	engine.mu.Lock()
	engine.state.Refreshing = true
	engine.mu.Unlock()
	status := engine.tailnet.Status(ctx)
	if status.State == "Unavailable" {
		engine.mu.Lock()
		previous := engine.state.Tailscale
		engine.mu.Unlock()
		if previous.State == "Running" {
			previous.Stale, previous.Error = true, status.Error
			status = previous
		}
	}
	online := make(map[string]bool, len(status.Peers))
	for _, peer := range status.Peers {
		online[peer.ID] = peer.Online
	}
	for _, current := range engine.sessions.States() {
		if current.Connected && !current.Local && !online[current.NodeID] {
			engine.sessions.DisconnectWithReason(current.ID, errors.New("对端 Tailscale 设备已离线或不再可访问"))
		}
	}
	if engine.nodeID != status.NodeID || status.State != "Running" {
		engine.closeNetwork()
		engine.sessions.DisconnectRemote(errors.New("Tailscale 网络已断开或账号已切换"))
		engine.nodeID = status.NodeID
		engine.bridge = engine.newBridge()
	}
	serviceError := ""
	if status.State == "Running" {
		if status.NodeID == "" {
			serviceError = "无法读取本机 Tailscale 设备身份"
		} else {
			if !engine.disableNetworkListener {
				serviceError = engine.bind(status)
			}
		}
	}
	devices := make([]Device, len(status.Peers))
	probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	jobs := make(chan int)
	var workers sync.WaitGroup
	for worker := 0; worker < min(8, len(devices)); worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				devices[index] = engine.probe(probeCtx, status.Peers[index])
			}
		}()
	}
	for index, peer := range status.Peers {
		devices[index] = Device{Peer: peer, Service: "unreachable", Ports: []share.SharedPort{}}
	}
	for index := range status.Peers {
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	listening := make([]string, 0, len(engine.listeners))
	for address := range engine.listeners {
		listening = append(listening, address)
	}
	sort.Strings(listening)
	engine.mu.Lock()
	engine.state.Tailscale = status
	engine.state.Devices = devices
	engine.state.Refreshing = false
	engine.state.ServiceError = serviceError
	engine.state.Listening = listening
	engine.state.UpdatedAt = time.Now()
	engine.mu.Unlock()
}

func (engine *Engine) closeNetwork() {
	for address, server := range engine.listeners {
		server.Close()
		delete(engine.listeners, address)
	}
	if engine.bridge != nil {
		engine.bridge.Close()
	}
}

func (engine *Engine) bind(status tailnet.Status) string {
	wanted := make(map[string]bool)
	for _, raw := range status.IPs {
		address, err := netip.ParseAddr(raw)
		if err == nil && tailnet.IsAddress(address) {
			wanted[net.JoinHostPort(address.String(), strconv.Itoa(ServicePort))] = true
		}
	}
	for address, server := range engine.listeners {
		if !wanted[address] {
			server.Close()
			delete(engine.listeners, address)
		}
	}
	var failures []string
	for address := range wanted {
		if engine.listeners[address] != nil {
			continue
		}
		listener, err := net.Listen("tcp", address)
		if err != nil {
			failures = append(failures, "无法监听 "+address+"："+err.Error())
			continue
		}
		handler, err := api.NewHandler(engine.manager, api.Options{
			Name: status.HostName, NodeID: status.NodeID, Version: version.Version,
			Bridge: engine.bridge, Auth: engine.authenticate,
		})
		if err != nil {
			listener.Close()
			failures = append(failures, err.Error())
			continue
		}
		server := &http.Server{
			Handler: handler, ReadHeaderTimeout: 4 * time.Second, IdleTimeout: 30 * time.Second,
			MaxHeaderBytes: 16 * 1024,
		}
		engine.listeners[address] = server
		go server.Serve(listener)
	}
	return strings.Join(failures, "; ")
}

func (engine *Engine) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Origin") != "" {
			http.Error(writer, "browser origins are not accepted", http.StatusForbidden)
			return
		}
		source, err := netip.ParseAddrPort(request.RemoteAddr)
		if err != nil || !tailnet.IsAddress(source.Addr()) {
			http.Error(writer, "Tailscale source address required", http.StatusForbidden)
			return
		}
		if _, err := engine.tailnet.WhoIs(request.Context(), request.RemoteAddr); err != nil {
			http.Error(writer, "Tailscale peer authentication required", http.StatusForbidden)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func (engine *Engine) probe(ctx context.Context, peer tailnet.Peer) Device {
	device := Device{Peer: peer, Service: "offline", Ports: []share.SharedPort{}}
	if !peer.Online {
		return device
	}
	device.Service = "unreachable"
	for _, raw := range peer.IPs {
		address, err := netip.ParseAddr(raw)
		if err != nil || !tailnet.IsAddress(address) {
			continue
		}
		endpoint := net.JoinHostPort(address.String(), strconv.Itoa(ServicePort))
		var info api.Info
		if err := engine.getJSON(ctx, endpoint, "/v1/info", &info); err != nil {
			continue
		}
		if info.APIVersion != "v1" || info.NodeID != peer.ID {
			device.Service = "incompatible"
			continue
		}
		var snapshot share.Snapshot
		if err := engine.getJSON(ctx, endpoint, "/v1/ports", &snapshot); err != nil {
			continue
		}
		if len(snapshot.Ports) > 1024 {
			device.Error = "对端串口清单过大"
			continue
		}
		device.Service, device.Version, device.Bridge, device.address = "available", info.Version, info.RemoteDataBridge, endpoint
		device.Ports = append([]share.SharedPort{}, snapshot.Ports...)
		return device
	}
	return device
}

func (engine *Engine) getJSON(ctx context.Context, endpoint, path string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+endpoint+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	response, err := engine.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Linkport API: HTTP %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1024*1024))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("invalid Linkport response")
	}
	return nil
}
