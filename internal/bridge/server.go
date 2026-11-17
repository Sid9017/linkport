package bridge

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sid9017/linkport/internal/rfc2217"
	"github.com/Sid9017/linkport/internal/share"
	"github.com/Sid9017/linkport/internal/vport"
	"go.bug.st/serial"
)

const Upgrade = "linkport-rfc2217"

var DefaultSettings = vport.LineSettings{BaudRate: 115200, DataBits: 8, Parity: vport.ParityNone, StopBits: vport.StopBits1}

type Server struct {
	Manager *share.Manager
	Open    func(string, *serial.Mode) (serial.Port, error)
	mu      sync.Mutex
	active  map[string]context.CancelFunc
	closed  bool
}

func NewServer(manager *share.Manager) *Server {
	return &Server{Manager: manager, Open: serial.Open, active: make(map[string]context.CancelFunc)}
}

func (server *Server) Close() {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.closed = true
	for _, cancel := range server.active {
		cancel()
	}
}

func (server *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || !strings.EqualFold(request.Header.Get("Upgrade"), Upgrade) ||
		!strings.Contains(strings.ToLower(request.Header.Get("Connection")), "upgrade") {
		http.Error(writer, "RFC 2217 upgrade required", http.StatusUpgradeRequired)
		return
	}
	identity := request.URL.Query().Get("id")
	settings := DefaultSettings
	if value := request.URL.Query().Get("baud"); value != "" {
		baud, err := strconv.Atoi(value)
		if err != nil {
			http.Error(writer, "invalid baud rate", http.StatusBadRequest)
			return
		}
		settings.BaudRate = baud
	}
	if err := rfc2217.Validate(settings); err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	path := ""
	for _, port := range server.Manager.LocalPorts() {
		if port.ID == identity && port.Shared && !port.Ambiguous {
			path = port.Path
			break
		}
	}
	if path == "" {
		http.Error(writer, "shared serial port unavailable", http.StatusNotFound)
		return
	}
	ctx, cancel := context.WithCancel(request.Context())
	defer cancel()
	server.mu.Lock()
	if server.closed || len(server.active) >= 64 {
		server.mu.Unlock()
		http.Error(writer, "sharing service unavailable", http.StatusServiceUnavailable)
		return
	}
	if _, exists := server.active[identity]; exists {
		server.mu.Unlock()
		http.Error(writer, "serial port already in use", http.StatusConflict)
		return
	}
	server.active[identity] = cancel
	server.mu.Unlock()
	defer func() {
		server.mu.Lock()
		delete(server.active, identity)
		server.mu.Unlock()
	}()
	port, err := server.Open(path, rfc2217.Mode(settings))
	if err != nil {
		http.Error(writer, "unable to open shared serial port (busy or permission denied)", http.StatusConflict)
		return
	}
	defer port.Close()
	updates, unsubscribe, err := server.Manager.Subscribe(ctx)
	if err != nil {
		http.Error(writer, "sharing service unavailable", http.StatusServiceUnavailable)
		return
	}
	defer unsubscribe()
	hijacker, ok := writer.(http.Hijacker)
	if !ok {
		http.Error(writer, "serial streaming unavailable", http.StatusInternalServerError)
		return
	}
	conn, buffered, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := fmt.Fprintf(buffered, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: %s\r\n\r\n", Upgrade); err != nil {
		return
	}
	if err := buffered.Flush(); err != nil {
		return
	}
	conn.SetDeadline(time.Time{})
	var closeOnce sync.Once
	closeAll := func() { closeOnce.Do(func() { cancel(); conn.Close(); port.Close() }) }
	defer closeAll()
	go func() {
		for {
			select {
			case <-ctx.Done():
				closeAll()
				return
			case snapshot, open := <-updates:
				if !open {
					closeAll()
					return
				}
				available := false
				for _, shared := range snapshot.Ports {
					if shared.ID == identity && shared.Online && !shared.Ambiguous {
						available = true
						break
					}
				}
				if !available {
					closeAll()
					return
				}
			}
		}
	}()
	_ = rfc2217.Serve(ctx, rfc2217.New(conn, buffered.Reader, true), port, settings)
}

func Dial(ctx context.Context, address, identity string, baud int) (*rfc2217.Stream, error) {
	conn, err := (&net.Dialer{Timeout: 4 * time.Second, KeepAlive: 15 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	return DialConn(ctx, conn, address, identity, baud)
}

// DialConn performs Linkport's RFC 2217 upgrade over an already authenticated
// transport, including an iroh bidirectional stream.
func DialConn(ctx context.Context, conn net.Conn, address, identity string, baud int) (*rfc2217.Stream, error) {
	stream, err := upgrade(ctx, conn, address, identity, baud)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return stream, nil
}

func upgrade(ctx context.Context, conn net.Conn, address, identity string, baud int) (*rfc2217.Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+address+"/v1/connect", nil)
	if err != nil {
		return nil, err
	}
	query := request.URL.Query()
	query.Set("id", identity)
	query.Set("baud", strconv.Itoa(baud))
	request.URL.RawQuery = query.Encode()
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", Upgrade)
	if err := request.Write(conn); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusSwitchingProtocols || !strings.EqualFold(response.Header.Get("Upgrade"), Upgrade) {
		response.Body.Close()
		if response.StatusCode == http.StatusConflict {
			return nil, errors.New("远端串口正被占用，或无法打开串口")
		}
		if response.StatusCode == http.StatusNotFound {
			return nil, errors.New("远端串口已离线或已取消共享")
		}
		return nil, fmt.Errorf("远端拒绝连接：HTTP %d", response.StatusCode)
	}
	conn.SetDeadline(time.Time{})
	return rfc2217.New(conn, reader, false), nil
}
