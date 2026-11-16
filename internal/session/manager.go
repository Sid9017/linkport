package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/Sid9017/linkport/internal/bridge"
	"github.com/Sid9017/linkport/internal/rfc2217"
	"github.com/Sid9017/linkport/internal/vport"
	"go.bug.st/serial"
)

type Record struct {
	Sequence uint64    `json:"sequence"`
	Time     time.Time `json:"time"`
	Kind     string    `json:"kind"`
	Data     []byte    `json:"data,omitempty"`
	Message  string    `json:"message,omitempty"`
}

type State struct {
	ID           string             `json:"id"`
	NodeID       string             `json:"nodeID"`
	HostName     string             `json:"hostName"`
	PortID       string             `json:"portID"`
	PortPath     string             `json:"portPath"`
	Label        string             `json:"label"`
	LocalPath    string             `json:"localPath"`
	Connected    bool               `json:"connected"`
	Local        bool               `json:"local"`
	Error        string             `json:"error,omitempty"`
	BaudRate     int                `json:"baudRate"`
	Capabilities vport.Capabilities `json:"capabilities"`
	Records      []Record           `json:"records"`
}

type Target struct {
	NodeID   string
	HostName string
	PortID   string
	PortPath string
	Label    string
	Address  string
	BaudRate int
	VPort    vport.Options
}

type connection struct {
	state     State
	stream    *rfc2217.Stream
	virtual   vport.Port
	physical  serial.Port
	ctx       context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once
	writeMu   sync.Mutex
	sequence  uint64
	logBytes  int
}

type Manager struct {
	directory   string
	mu          sync.Mutex
	connectMu   sync.Mutex
	connections map[string]*connection
	OpenVirtual func(vport.Options) (vport.Port, error)
	Dial        func(context.Context, string, string, int) (*rfc2217.Stream, error)
}

func New(directory string) *Manager {
	return &Manager{
		directory: directory, connections: make(map[string]*connection),
		OpenVirtual: vport.Open, Dial: bridge.Dial,
	}
}

func Identity(nodeID, portID string) string {
	hash := sha256.Sum256([]byte(nodeID + "\x00" + portID))
	return hex.EncodeToString(hash[:10])
}

var slugPattern = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)
var windowsPortPattern = regexp.MustCompile(`(?i)^COM[1-9][0-9]{0,4}$`)
var windowsPairPattern = regexp.MustCompile(`(?i)^(?:CNC[AB][0-9]{1,5}|COM[1-9][0-9]{0,4})$`)

func (manager *Manager) Connect(ctx context.Context, target Target) (State, error) {
	manager.connectMu.Lock()
	defer manager.connectMu.Unlock()
	identity := Identity(target.NodeID, target.PortID)
	manager.mu.Lock()
	if existing := manager.connections[identity]; existing != nil && existing.state.Connected {
		state := copyState(existing.state)
		manager.mu.Unlock()
		return state, nil
	}
	if len(manager.connections) >= 64 && manager.connections[identity] == nil {
		manager.mu.Unlock()
		return State{}, errors.New("会话数量已达上限，请重启应用以释放历史会话")
	}
	options := target.VPort
	if runtime.GOOS == "windows" {
		if !windowsPortPattern.MatchString(options.Path) || !windowsPairPattern.MatchString(options.PairDevice) || options.Path == options.PairDevice {
			manager.mu.Unlock()
			return State{}, errors.New("请指定已安装的 com0com 端口对，例如 COM10 和 CNCB0")
		}
		for _, existing := range manager.connections {
			if existing.state.Connected && existing.state.LocalPath == options.Path {
				manager.mu.Unlock()
				return State{}, errors.New("该虚拟串口已分配给另一条连接")
			}
		}
	} else {
		name := slugPattern.ReplaceAllString(target.HostName, "-")
		if len(name) > 36 {
			name = name[:36]
		}
		if name == "" {
			name = "peer"
		}
		options = vport.Options{Path: filepath.Join(manager.directory, name+"-"+identity)}
	}
	manager.mu.Unlock()
	if target.BaudRate == 0 {
		target.BaudRate = bridge.DefaultSettings.BaudRate
	}
	stream, err := manager.Dial(ctx, target.Address, target.PortID, target.BaudRate)
	if err != nil {
		return State{}, err
	}
	virtual, err := manager.OpenVirtual(options)
	if err != nil {
		stream.Close()
		return State{}, err
	}
	sessionCtx, cancel := context.WithCancel(ctx)
	entry := &connection{
		stream: stream, virtual: virtual, ctx: sessionCtx, cancel: cancel,
		state: State{
			ID: identity, NodeID: target.NodeID, HostName: target.HostName,
			PortID: target.PortID, PortPath: target.PortPath, Label: target.Label,
			LocalPath: virtual.Path(), BaudRate: target.BaudRate,
			Capabilities: virtual.Capabilities(), Records: []Record{},
		},
	}
	manager.mu.Lock()
	manager.connections[identity] = entry
	manager.mu.Unlock()
	if err := stream.Negotiate(); err != nil {
		manager.finish(entry, err)
		return State{}, err
	}
	stream.CommandHandler = func(command byte, payload []byte) error {
		if command == 107 && len(payload) == 1 && virtual.Capabilities().ModemStatus {
			value := payload[0]
			return virtual.SetModemStatus(vport.ModemStatus{
				CTS: value&16 != 0, DSR: value&32 != 0, RI: value&64 != 0, DCD: value&128 != 0,
			})
		}
		return nil
	}
	received := make(chan []byte, 256)
	go func() {
		buffer := make([]byte, 4096)
		for {
			count, err := stream.Read(buffer)
			if count > 0 {
				content := append([]byte(nil), buffer[:count]...)
				manager.record(entry, "RX", content, "")
				select {
				case received <- content:
				case <-sessionCtx.Done():
					return
				default:
					manager.finish(entry, errors.New("本地虚拟串口接收缓冲区已满，请读取映射端口"))
					return
				}
			}
			if err != nil {
				manager.finish(entry, err)
				return
			}
		}
	}()
	go func() {
		for {
			select {
			case <-sessionCtx.Done():
				return
			case content := <-received:
				if _, err := virtual.Write(content); err != nil {
					manager.finish(entry, err)
					return
				}
			}
		}
	}()
	readyCtx, readyCancel := context.WithTimeout(sessionCtx, 5*time.Second)
	err = stream.WaitReady(readyCtx)
	readyCancel()
	if err != nil {
		manager.finish(entry, err)
		return State{}, err
	}
	manager.mu.Lock()
	if sessionCtx.Err() != nil {
		manager.mu.Unlock()
		return State{}, errors.New("串口连接在建立时已关闭")
	}
	entry.state.Connected = true
	manager.mu.Unlock()
	manager.record(entry, "SYS", nil, "已连接 "+target.HostName+" / "+target.PortPath)
	go manager.pumpVirtual(entry)
	go manager.pumpSettings(entry)
	go func() { <-sessionCtx.Done(); manager.finish(entry, sessionCtx.Err()) }()
	if virtual.Capabilities().ModemStatus {
		if err := stream.Command(11, []byte{255}); err != nil {
			manager.finish(entry, err)
		}
	}
	manager.mu.Lock()
	state := copyState(entry.state)
	manager.mu.Unlock()
	return state, nil
}

func (manager *Manager) applySettings(entry *connection) error {
	for {
		select {
		case event, open := <-entry.virtual.Events():
			if !open {
				return os.ErrClosed
			}
			ctx, cancel := context.WithTimeout(entry.ctx, 4*time.Second)
			var err error
			switch event.Kind {
			case vport.EventLineSettings:
				err = entry.stream.Configure(ctx, event.Line)
				if err == nil {
					manager.mu.Lock()
					entry.state.BaudRate = event.Line.BaudRate
					manager.mu.Unlock()
				}
			case vport.EventControlLines:
				err = entry.stream.SetControl(ctx, event.Control)
			}
			cancel()
			if err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

func (manager *Manager) pumpSettings(entry *connection) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-entry.ctx.Done():
			return
		case <-ticker.C:
			entry.writeMu.Lock()
			err := manager.applySettings(entry)
			entry.writeMu.Unlock()
			if err != nil {
				manager.finish(entry, err)
				return
			}
		}
	}
}

func (manager *Manager) pumpVirtual(entry *connection) {
	buffer := make([]byte, 4096)
	for {
		count, err := entry.virtual.Read(buffer)
		if count > 0 {
			entry.writeMu.Lock()
			err = manager.applySettings(entry)
			if err == nil {
				_, err = entry.stream.Write(buffer[:count])
			}
			entry.writeMu.Unlock()
			if err == nil {
				manager.record(entry, "TX", buffer[:count], "")
			}
		}
		if err != nil {
			manager.finish(entry, err)
			return
		}
	}
}

func (manager *Manager) finish(entry *connection, reason error) {
	entry.closeOnce.Do(func() {
		entry.cancel()
		if entry.stream != nil {
			entry.stream.Close()
		}
		if entry.virtual != nil {
			entry.virtual.Close()
		}
		if entry.physical != nil {
			entry.physical.Close()
		}
		manager.mu.Lock()
		entry.state.Connected = false
		if reason != nil && !errors.Is(reason, context.Canceled) {
			entry.state.Error = reason.Error()
		}
		manager.mu.Unlock()
		message := "连接已关闭"
		if reason != nil && !errors.Is(reason, context.Canceled) {
			message += "：" + reason.Error()
		}
		manager.record(entry, "SYS", nil, message)
	})
}

func (manager *Manager) record(entry *connection, kind string, content []byte, message string) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	entry.sequence++
	record := Record{Sequence: entry.sequence, Time: time.Now(), Kind: kind, Data: append([]byte(nil), content...), Message: message}
	entry.state.Records = append(entry.state.Records, record)
	entry.logBytes += len(content) + len(message)
	for len(entry.state.Records) > 500 || entry.logBytes > 128*1024 {
		first := entry.state.Records[0]
		entry.logBytes -= len(first.Data) + len(first.Message)
		entry.state.Records = entry.state.Records[1:]
	}
}

func (manager *Manager) Send(identity string, content []byte) error {
	if len(content) == 0 || len(content) > 64*1024 {
		return errors.New("发送内容长度必须在 1–65536 字节之间")
	}
	manager.mu.Lock()
	entry := manager.connections[identity]
	active := entry != nil && entry.state.Connected
	manager.mu.Unlock()
	if !active {
		return errors.New("会话未连接")
	}
	entry.writeMu.Lock()
	if entry.physical != nil {
		sent, err := writeLocal(entry.physical, content)
		entry.writeMu.Unlock()
		if sent > 0 {
			manager.record(entry, "TX", content[:sent], "")
		}
		if err != nil {
			manager.finish(entry, err)
		}
		return err
	}
	err := manager.applySettings(entry)
	if err == nil {
		_, err = entry.stream.Write(content)
	}
	entry.writeMu.Unlock()
	if err != nil {
		manager.finish(entry, err)
		return err
	}
	manager.record(entry, "TX", content, "")
	return nil
}

func (manager *Manager) MonitorLocal(ctx context.Context, target Target, open func() (serial.Port, error)) (State, error) {
	manager.connectMu.Lock()
	defer manager.connectMu.Unlock()
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	identity := Identity("local", target.PortID+"\x00"+target.PortPath)
	manager.mu.Lock()
	if existing := manager.connections[identity]; existing != nil && existing.state.Connected {
		state := copyState(existing.state)
		manager.mu.Unlock()
		return state, nil
	}
	if len(manager.connections) >= 64 && manager.connections[identity] == nil {
		manager.mu.Unlock()
		return State{}, errors.New("会话数量已达上限，请先释放历史会话")
	}
	manager.mu.Unlock()
	port, err := open()
	if err != nil {
		return State{}, err
	}
	if err := port.SetReadTimeout(250 * time.Millisecond); err != nil {
		port.Close()
		return State{}, err
	}
	if err := ctx.Err(); err != nil {
		port.Close()
		return State{}, err
	}
	sessionCtx, cancel := context.WithCancel(ctx)
	entry := &connection{
		physical: port, ctx: sessionCtx, cancel: cancel,
		state: State{
			ID: identity, NodeID: "local", HostName: target.HostName,
			PortID: target.PortID, PortPath: target.PortPath, Label: target.Label,
			LocalPath: target.PortPath, Local: true, Connected: true,
			BaudRate: target.BaudRate, Records: []Record{},
		},
	}
	manager.mu.Lock()
	manager.connections[identity] = entry
	manager.mu.Unlock()
	manager.record(entry, "SYS", nil, "已打开本机串口 "+target.PortPath)
	go func() {
		buffer := make([]byte, 4096)
		for sessionCtx.Err() == nil {
			count, err := port.Read(buffer)
			if count > 0 {
				manager.record(entry, "RX", buffer[:count], "")
			}
			if err != nil {
				manager.finish(entry, err)
				return
			}
		}
	}()
	go func() { <-sessionCtx.Done(); manager.finish(entry, sessionCtx.Err()) }()
	manager.mu.Lock()
	state := copyState(entry.state)
	manager.mu.Unlock()
	return state, nil
}

func writeLocal(writer serial.Port, content []byte) (int, error) {
	written := 0
	for written < len(content) {
		count, err := writer.Write(content[written:])
		written += count
		if err != nil {
			return written, err
		}
		if count == 0 {
			return written, errors.New("串口写入未取得进展")
		}
	}
	return written, nil
}

func (manager *Manager) ReconcileLocal(available map[string]string) {
	manager.mu.Lock()
	var missing []*connection
	for _, entry := range manager.connections {
		if entry.state.Local && entry.state.Connected && available[entry.state.PortPath] != entry.state.PortID {
			missing = append(missing, entry)
		}
	}
	manager.mu.Unlock()
	for _, entry := range missing {
		manager.finish(entry, errors.New("本机串口已拔出或设备身份已改变"))
	}
}

func (manager *Manager) DisconnectRemote(reason error) {
	manager.mu.Lock()
	var entries []*connection
	for _, entry := range manager.connections {
		if !entry.state.Local {
			entries = append(entries, entry)
		}
	}
	manager.mu.Unlock()
	for _, entry := range entries {
		manager.finish(entry, reason)
	}
}

func (manager *Manager) Disconnect(identity string) error {
	return manager.DisconnectWithReason(identity, nil)
}

func (manager *Manager) DisconnectWithReason(identity string, reason error) error {
	manager.mu.Lock()
	entry := manager.connections[identity]
	manager.mu.Unlock()
	if entry == nil {
		return errors.New("会话不存在")
	}
	manager.finish(entry, reason)
	return nil
}

func (manager *Manager) Clear(identity string) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	entry := manager.connections[identity]
	if entry == nil {
		return errors.New("会话不存在")
	}
	entry.state.Records = []Record{}
	entry.logBytes = 0
	return nil
}

func (manager *Manager) DisconnectAll(reason error) {
	manager.mu.Lock()
	entries := make([]*connection, 0, len(manager.connections))
	for _, entry := range manager.connections {
		entries = append(entries, entry)
	}
	manager.mu.Unlock()
	for _, entry := range entries {
		manager.finish(entry, reason)
	}
}

func (manager *Manager) States() []State {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	states := make([]State, 0, len(manager.connections))
	for _, entry := range manager.connections {
		states = append(states, copyState(entry.state))
	}
	sort.Slice(states, func(first, second int) bool { return states[first].ID < states[second].ID })
	return states
}

func copyState(state State) State {
	state.Records = append([]Record{}, state.Records...)
	return state
}

func (manager *Manager) String() string {
	return fmt.Sprintf("serial sessions at %s", manager.directory)
}

func (manager *Manager) Export(identity string) ([]byte, error) {
	manager.mu.Lock()
	entry := manager.connections[identity]
	if entry == nil {
		manager.mu.Unlock()
		return nil, errors.New("会话不存在")
	}
	state := copyState(entry.state)
	manager.mu.Unlock()
	var output bytes.Buffer
	fmt.Fprintf(&output, "Linkport %s / %s -> %s\n", state.HostName, state.PortPath, state.LocalPath)
	for _, record := range state.Records {
		if record.Kind == "SYS" {
			fmt.Fprintf(&output, "%s SYS %s\n", record.Time.Format(time.RFC3339Nano), record.Message)
		} else {
			fmt.Fprintf(&output, "%s %s HEX %x | %q\n", record.Time.Format(time.RFC3339Nano), record.Kind, record.Data, record.Data)
		}
	}
	return output.Bytes(), nil
}
