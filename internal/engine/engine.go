package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Sid9017/linkport/internal/api"
	"github.com/Sid9017/linkport/internal/bridge"
	"github.com/Sid9017/linkport/internal/config"
	"github.com/Sid9017/linkport/internal/p2p"
	"github.com/Sid9017/linkport/internal/rfc2217"
	"github.com/Sid9017/linkport/internal/serialport"
	"github.com/Sid9017/linkport/internal/session"
	"github.com/Sid9017/linkport/internal/share"
	"github.com/Sid9017/linkport/internal/version"
	"github.com/Sid9017/linkport/internal/vport"
	"go.bug.st/serial"
)

type Device struct {
	p2p.Peer
	Online   bool               `json:"online"`
	Service  string             `json:"service"`
	Version  string             `json:"version,omitempty"`
	Bridge   bool               `json:"bridge"`
	Ports    []share.SharedPort `json:"ports"`
	LastSeen time.Time          `json:"lastSeen,omitempty"`
	Error    string             `json:"error,omitempty"`
}

type State struct {
	Network     p2p.Status         `json:"network"`
	LocalPorts  []share.LocalPort  `json:"localPorts"`
	SharedPorts []share.SharedPort `json:"sharedPorts"`
	Devices     []Device           `json:"devices"`
	Sessions    []session.State    `json:"sessions"`
	Refreshing  bool               `json:"refreshing"`
	UpdatedAt   time.Time          `json:"updatedAt"`
	LocalError  string             `json:"localError,omitempty"`
}

type Options struct {
	Directory              string
	DeviceDirectory        string
	Enumerator             serialport.Enumerator
	SharingConfig          string
	DisableNetworkListener bool
	LocalOnly              bool
}

type Engine struct {
	ctx       context.Context
	network   *p2p.Network
	manager   *share.Manager
	sessions  *session.Manager
	mu        sync.Mutex
	state     State
	refresh   chan struct{}
	bridge    *bridge.Server
	broker    *serialport.Broker
	closeOnce sync.Once
}

func New(ctx context.Context, options Options) (*Engine, error) {
	if options.Directory == "" {
		return nil, errors.New("configuration directory required")
	}
	if options.Enumerator == nil {
		options.Enumerator = serialport.NewEnumerator()
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
	network, err := p2p.New(ctx, p2p.Options{Directory: options.Directory, Disabled: options.DisableNetworkListener, LocalOnly: options.LocalOnly})
	if err != nil {
		return nil, err
	}
	engine := &Engine{
		ctx: ctx, network: network, manager: manager, sessions: session.New(options.DeviceDirectory),
		refresh: make(chan struct{}, 1), broker: serialport.NewBroker(), state: State{Devices: []Device{}},
	}
	engine.bridge = engine.newBridge()
	engine.sessions.Dial = func(ctx context.Context, nodeID, portID string, baud int) (*rfc2217.Stream, error) {
		dialCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		conn, err := network.Dial(dialCtx, nodeID)
		if err != nil {
			return nil, err
		}
		return bridge.DialConn(dialCtx, conn, nodeID, portID, baud)
	}
	return engine, nil
}

func (engine *Engine) Run() {
	ctx, cancel := context.WithCancel(engine.ctx)
	defer cancel()
	defer engine.Close()
	handler, err := api.NewHandler(engine.manager, api.Options{
		Name: engine.network.Status().HostName, NodeID: engine.network.Status().NodeID,
		Version: version.Version, Bridge: engine.bridge, Auth: func(next http.Handler) http.Handler { return next },
	})
	if err != nil {
		return
	}
	go engine.network.Serve(handler)
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

func (engine *Engine) Close() {
	engine.closeOnce.Do(func() {
		engine.network.Close()
		engine.bridge.Close()
		engine.sessions.DisconnectAll(context.Canceled)
		engine.broker.Close()
	})
}

func (engine *Engine) Network() *p2p.Network { return engine.network }

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
	engine.mu.Unlock()
	state.Network = engine.network.Status()
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
	if state.Network.State != "Running" {
		return session.State{}, errors.New("iroh 网络未运行，请检查网络设置或重启 Linkport")
	}
	if !engine.network.Trusted(nodeID) {
		return session.State{}, errors.New("请先添加并配对设备")
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
				PortPath: port.Path, Label: label, Address: device.ID, BaudRate: baud, VPort: virtual,
			})
		}
		return session.State{}, errors.New("对端已取消共享该串口")
	}
	return session.State{}, errors.New("设备不在已配对目录中")
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
	previous := make(map[string]time.Time)
	for _, device := range engine.state.Devices {
		previous[device.ID] = device.LastSeen
	}
	engine.mu.Unlock()
	peers := engine.network.Peers()
	devices := make([]Device, len(peers))
	probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var workers sync.WaitGroup
	jobs := make(chan int)
	for worker := 0; worker < min(8, len(peers)); worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				devices[index] = engine.probe(probeCtx, peers[index])
				if !devices[index].Online {
					devices[index].LastSeen = previous[peers[index].ID]
				}
			}
		}()
	}
	for i := range peers {
		jobs <- i
	}
	close(jobs)
	workers.Wait()
	engine.mu.Lock()
	engine.state.Devices = devices
	engine.state.UpdatedAt = time.Now()
	engine.state.Refreshing = false
	engine.mu.Unlock()
}

func (engine *Engine) probe(ctx context.Context, peer p2p.Peer) Device {
	device := Device{Peer: peer, Service: "unreachable", Ports: []share.SharedPort{}}
	client := engine.network.NewClient(peer.ID, 5*time.Second)
	defer client.CloseIdleConnections()
	var info api.Info
	if err := getJSON(ctx, client, peer.ID, "/v1/info", &info); err != nil {
		return device
	}
	if info.APIVersion != "v1" || info.NodeID != peer.ID {
		device.Service = "incompatible"
		return device
	}
	var snapshot share.Snapshot
	if err := getJSON(ctx, client, peer.ID, "/v1/ports", &snapshot); err != nil {
		return device
	}
	if len(snapshot.Ports) > 1024 {
		device.Error = "对端串口清单过大"
		return device
	}
	device.Online, device.Service, device.Version, device.Bridge = true, "available", info.Version, info.RemoteDataBridge
	device.Ports, device.LastSeen = append([]share.SharedPort{}, snapshot.Ports...), time.Now()
	return device
}

func getJSON(ctx context.Context, client *http.Client, nodeID, path string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+nodeID+path, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
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

func (engine *Engine) AddDevice(ctx context.Context, code string) (p2p.Peer, error) {
	peer, err := engine.network.AddDevice(ctx, code)
	if err == nil {
		engine.Refresh()
	}
	return peer, err
}

func (engine *Engine) RemoveDevice(id string) error {
	if err := engine.network.RemoveDevice(id); err != nil {
		return err
	}
	for _, state := range engine.sessions.States() {
		if !state.Local && state.NodeID == id {
			_ = engine.sessions.DisconnectWithReason(state.ID, errors.New("设备已移除，访问授权已撤销"))
		}
	}
	engine.Refresh()
	return nil
}
