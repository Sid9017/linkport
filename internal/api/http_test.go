package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sid9017/linkport/internal/config"
	"github.com/Sid9017/linkport/internal/serialport"
	"github.com/Sid9017/linkport/internal/share"
)

func testManager(t *testing.T) (*share.Manager, serialport.Port, serialport.Port) {
	t.Helper()
	shared := serialport.Normalize(serialport.Port{
		Path: "/dev/ttyUSB0", IsUSB: true, VID: "0403", PID: "6001", SerialNumber: "public-serial",
	})
	private := serialport.Normalize(serialport.Port{
		Path: "/dev/private-port", IsUSB: true, VID: "1234", PID: "5678", SerialNumber: "private-serial",
	})
	enumerator := serialport.EnumerateFunc(func(ctx context.Context) ([]serialport.Port, error) {
		return []serialport.Port{shared, private}, nil
	})
	manager, err := share.NewManager(context.Background(), enumerator, config.NewFileStore(filepath.Join(t.TempDir(), "shares.json")), share.Options{MaxSubscribers: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetShared(context.Background(), shared.ID, true); err != nil {
		t.Fatal(err)
	}
	return manager, shared, private
}

func testAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "test-authorized" {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func testHandler(t *testing.T, manager *share.Manager) http.Handler {
	t.Helper()
	handler, err := NewHandler(manager, Options{
		Version: "test", Auth: testAuth, HeartbeatInterval: 10 * time.Millisecond, WriteTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestPublicRoutesRequireAuthAndRejectMutations(t *testing.T) {
	manager, shared, private := testManager(t)
	handler := testHandler(t, manager)
	for _, path := range []string{"/v1/info", "/v1/ports", "/v1/events"} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodHead} {
			request := httptest.NewRequest(method, path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("unauthenticated %s %s: %d", method, path, response.Code)
			}
			if method == http.MethodGet && path == "/v1/events" {
				continue
			}
			request.Header.Set("Authorization", "test-authorized")
			response = httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			want := http.StatusOK
			if method != http.MethodGet {
				want = http.StatusMethodNotAllowed
			}
			if response.Code != want {
				t.Fatalf("%s %s: %d, want %d", method, path, response.Code, want)
			}
			if strings.Contains(response.Body.String(), private.Path) || strings.Contains(response.Body.String(), private.SerialNumber) {
				t.Fatalf("private port exposed by %s", path)
			}
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/ports", nil)
	request.Header.Set("Authorization", "test-authorized")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var snapshot share.Snapshot
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil || len(snapshot.Ports) != 1 {
		t.Fatalf("snapshot response: %#v, %v", snapshot, err)
	}
	if snapshot.Ports[0].Path != shared.Path {
		t.Fatal("public snapshot omitted shared path")
	}
	request = httptest.NewRequest(http.MethodGet, "/v1/info", nil)
	request.Header.Set("Authorization", "test-authorized")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var info Info
	if err := json.Unmarshal(response.Body.Bytes(), &info); err != nil || info.RemoteDataBridge || info.APIVersion != "v1" {
		t.Fatalf("unexpected capabilities: %#v, %v", info, err)
	}
	if _, err := NewHandler(manager, Options{}); err == nil {
		t.Fatal("handler accepted missing auth middleware")
	}
}

func connectEvents(t *testing.T, server *httptest.Server, ctx context.Context, lastID string) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "test-authorized")
	request.Header.Set("Last-Event-ID", lastID)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func readSnapshot(reader *bufio.Reader) (share.Snapshot, error) {
	var snapshot share.Snapshot
	event := ""
	identity := ""
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return snapshot, err
		}
		switch {
		case strings.HasPrefix(line, "id: "):
			identity = strings.TrimSpace(strings.TrimPrefix(line, "id: "))
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
		case strings.HasPrefix(line, "data: "):
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &snapshot); err != nil {
				return snapshot, err
			}
		case line == "\n" && event != "":
			if event != "snapshot" || identity != fmt.Sprint(snapshot.Revision) {
				return snapshot, errors.New("invalid SSE snapshot framing")
			}
			return snapshot, nil
		}
	}
}

func TestSSEInitialSnapshotUnshareCancellationAndReconnect(t *testing.T) {
	manager, port, _ := testManager(t)
	handler := testHandler(t, manager)
	exited := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer func() { exited <- struct{}{} }()
		handler.ServeHTTP(writer, request)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response := connectEvents(t, server, ctx, "999999")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("SSE response: %d, %v", response.StatusCode, response.Header)
	}
	reader := bufio.NewReader(response.Body)
	first, err := readSnapshot(reader)
	if err != nil || len(first.Ports) != 1 || first.Ports[0].ID != port.ID {
		t.Fatalf("initial full snapshot: %#v, %v", first, err)
	}
	if err := manager.SetShared(context.Background(), port.ID, false); err != nil {
		t.Fatal(err)
	}
	next, err := readSnapshot(reader)
	if err != nil || len(next.Ports) != 0 || next.Revision <= first.Revision {
		t.Fatalf("unshare snapshot: %#v, %v", next, err)
	}
	cancel()
	response.Body.Close()
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("SSE request did not stop after cancellation")
	}
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	response = connectEvents(t, server, ctx, fmt.Sprint(next.Revision))
	defer response.Body.Close()
	reconnected, err := readSnapshot(bufio.NewReader(response.Body))
	if err != nil || reconnected.Revision != next.Revision || len(reconnected.Ports) != 0 {
		t.Fatalf("reconnect did not send full snapshot: %#v, %v", reconnected, err)
	}
	stop()
	response.Body.Close()
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("reconnected SSE request did not stop")
	}
}

type stalledWriter struct {
	header   http.Header
	mu       sync.Mutex
	deadline time.Time
	started  chan struct{}
	once     sync.Once
}

func (writer *stalledWriter) Header() http.Header {
	return writer.header
}

func (writer *stalledWriter) WriteHeader(status int) {}

func (writer *stalledWriter) Write(contents []byte) (int, error) {
	writer.once.Do(func() { close(writer.started) })
	writer.mu.Lock()
	deadline := writer.deadline
	writer.mu.Unlock()
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	<-timer.C
	return 0, context.DeadlineExceeded
}

func (writer *stalledWriter) SetWriteDeadline(deadline time.Time) error {
	writer.mu.Lock()
	writer.deadline = deadline
	writer.mu.Unlock()
	return nil
}

func (writer *stalledWriter) Flush() {}

func TestSlowSSEWriteExpiresWithoutBlockingManager(t *testing.T) {
	manager, port, _ := testManager(t)
	handler, err := NewHandler(manager, Options{Auth: testAuth, WriteTimeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	writer := &stalledWriter{header: make(http.Header), started: make(chan struct{})}
	request := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	request.Header.Set("Authorization", "test-authorized")
	completed := make(chan struct{})
	go func() {
		defer close(completed)
		handler.ServeHTTP(writer, request)
	}()
	select {
	case <-writer.started:
	case <-time.After(time.Second):
		t.Fatal("SSE did not start writing")
	}
	if _, _, err := manager.Subscribe(context.Background()); !errors.Is(err, share.ErrSubscriberLimit) {
		t.Fatalf("SSE did not consume subscriber slot: %v", err)
	}
	if err := manager.SetShared(context.Background(), port.ID, false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("slow SSE write did not expire")
	}
	updates, cancel, err := manager.Subscribe(context.Background())
	if err != nil {
		t.Fatalf("slow SSE client leaked subscription: %v", err)
	}
	defer cancel()
	if snapshot := <-updates; len(snapshot.Ports) != 0 {
		t.Fatal("slow client prevented unshare")
	}
}

func TestSSESubscriptionLimitReturnsServiceUnavailable(t *testing.T) {
	manager, _, _ := testManager(t)
	_, cancel, err := manager.Subscribe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	server := httptest.NewServer(testHandler(t, manager))
	defer server.Close()
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	response := connectEvents(t, server, ctx, "")
	defer response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("subscriber limit status: %d, %s", response.StatusCode, body)
	}
}

func TestSSEHTTP2SurvivesIdleWriteDeadline(t *testing.T) {
	manager, port, _ := testManager(t)
	handler, err := NewHandler(manager, Options{
		Auth: testAuth, WriteTimeout: 50 * time.Millisecond, HeartbeatInterval: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	response := connectEvents(t, server, ctx, "")
	defer response.Body.Close()
	if response.ProtoMajor != 2 {
		t.Fatalf("expected HTTP/2, got %s", response.Proto)
	}
	reader := bufio.NewReader(response.Body)
	if _, err := readSnapshot(reader); err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(150 * time.Millisecond)
	defer timer.Stop()
	<-timer.C
	if err := manager.SetShared(context.Background(), port.ID, false); err != nil {
		t.Fatal(err)
	}
	snapshot, err := readSnapshot(reader)
	if err != nil || len(snapshot.Ports) != 0 {
		t.Fatalf("idle HTTP/2 stream expired: %#v, %v", snapshot, err)
	}
}
