package share

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/Sid9017/linkport/internal/config"
	"github.com/Sid9017/linkport/internal/serialport"
)

type fakeEnumerator struct {
	mu    sync.Mutex
	ports []serialport.Port
	err   error
	calls chan struct{}
}

func (enumerator *fakeEnumerator) Enumerate(ctx context.Context) ([]serialport.Port, error) {
	enumerator.mu.Lock()
	defer enumerator.mu.Unlock()
	if enumerator.calls != nil {
		select {
		case enumerator.calls <- struct{}{}:
		default:
		}
	}
	return append([]serialport.Port{}, enumerator.ports...), enumerator.err
}

func (enumerator *fakeEnumerator) set(ports []serialport.Port, err error) {
	enumerator.mu.Lock()
	defer enumerator.mu.Unlock()
	enumerator.ports = ports
	enumerator.err = err
}

type failingStore struct {
	config.Store
	err error
}

func (store *failingStore) Save(ctx context.Context, state config.State) error {
	if store.err != nil {
		return store.err
	}
	return store.Store.Save(ctx, state)
}

func usb(path, serial string) serialport.Port {
	return serialport.Normalize(serialport.Port{
		Path: path, IsUSB: true, VID: "0403", PID: "6001", SerialNumber: serial, Product: "USB adapter",
	})
}

func newManager(t *testing.T, enumerator serialport.Enumerator, store config.Store, options Options) *Manager {
	t.Helper()
	manager, err := NewManager(context.Background(), enumerator, store, options)
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func refresh(t *testing.T, manager *Manager) {
	t.Helper()
	if err := manager.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func setShared(t *testing.T, manager *Manager, identity string, enabled bool) {
	t.Helper()
	if err := manager.SetShared(context.Background(), identity, enabled); err != nil {
		t.Fatal(err)
	}
}

func TestOptInPersistenceHotplugAndOfflineUnshare(t *testing.T) {
	ctx := context.Background()
	first := usb("/dev/ttyUSB0", "first")
	second := usb("/dev/ttyUSB1", "second")
	enumerator := &fakeEnumerator{ports: []serialport.Port{first, second}}
	store := config.NewFileStore(filepath.Join(t.TempDir(), "shares.json"))
	manager := newManager(t, enumerator, store, Options{})
	refresh(t, manager)
	if got := manager.LocalPorts(); len(got) != 2 || got[0].Shared || got[1].Shared {
		t.Fatalf("new ports shared: %#v", got)
	}
	if got := manager.Snapshot(); len(got.Ports) != 0 {
		t.Fatalf("private ports exposed: %#v", got)
	}
	setShared(t, manager, first.ID, true)
	if got := manager.Snapshot(); len(got.Ports) != 1 || got.Ports[0].ID != first.ID || !got.Ports[0].Online {
		t.Fatalf("shared snapshot: %#v", got)
	}
	enumerator.set([]serialport.Port{second}, nil)
	refresh(t, manager)
	if got := manager.Snapshot(); len(got.Ports) != 1 || got.Ports[0].Online {
		t.Fatalf("offline share lost: %#v", got)
	}
	restarted := newManager(t, enumerator, store, Options{})
	if got := restarted.Snapshot(); len(got.Ports) != 1 || got.Ports[0].Online {
		t.Fatalf("restart lost saved share: %#v", got)
	}
	first.Path = "/dev/ttyUSB9"
	enumerator.set([]serialport.Port{first, second}, nil)
	refresh(t, restarted)
	if got := restarted.Snapshot(); len(got.Ports) != 1 || !got.Ports[0].Online {
		t.Fatalf("USB did not reconnect by serial: %#v", got)
	}
	enumerator.set(nil, nil)
	refresh(t, restarted)
	setShared(t, restarted, first.ID, false)
	restarted = newManager(t, enumerator, store, Options{})
	enumerator.set([]serialport.Port{first, second}, nil)
	refresh(t, restarted)
	if len(restarted.Snapshot().Ports) != 0 {
		t.Fatal("unshared port shared again on reconnect")
	}
	state, err := store.Load(ctx)
	if err != nil || len(state.Shares) != 0 {
		t.Fatalf("unshare not persisted: %#v, %v", state, err)
	}
}

func TestAmbiguousIdentityRequiresExplicitConfirmation(t *testing.T) {
	first := usb("/dev/ttyUSB0", "duplicate")
	second := usb("/dev/ttyUSB1", "duplicate")
	enumerator := &fakeEnumerator{ports: []serialport.Port{first, second}}
	store := config.NewFileStore(filepath.Join(t.TempDir(), "shares.json"))
	manager := newManager(t, enumerator, store, Options{})
	refresh(t, manager)
	if err := manager.SetShared(context.Background(), first.ID, true); !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("ambiguous opt-in: %v", err)
	}
	enumerator.set([]serialport.Port{first}, nil)
	refresh(t, manager)
	setShared(t, manager, first.ID, true)
	enumerator.set([]serialport.Port{first, second}, nil)
	refresh(t, manager)
	if got := manager.Snapshot().Ports[0]; got.Online || !got.Ambiguous {
		t.Fatalf("ambiguous share still resolved: %#v", got)
	}
	for _, port := range manager.LocalPorts() {
		if port.Shared || !port.Ambiguous {
			t.Fatalf("ambiguous local port marked shared: %#v", port)
		}
	}
	manager = newManager(t, enumerator, store, Options{})
	enumerator.set([]serialport.Port{second}, nil)
	refresh(t, manager)
	if got := manager.Snapshot().Ports[0]; got.Online || !got.Ambiguous {
		t.Fatalf("silently rebound after ambiguity and restart: %#v", got)
	}
	if got := manager.Snapshot().Ports[0].Path; got != first.Path {
		t.Fatalf("unresolved share guessed a replacement path: %q", got)
	}
	setShared(t, manager, second.ID, true)
	if got := manager.Snapshot().Ports[0]; !got.Online || got.Ambiguous {
		t.Fatalf("explicit confirmation failed: %#v", got)
	}
	if got := manager.Snapshot().Ports[0].Path; got != second.Path {
		t.Fatalf("confirmed share did not update path: %q", got)
	}
}

func TestPathFallbackDoesNotFollowNewPath(t *testing.T) {
	first := serialport.Normalize(serialport.Port{Path: "/dev/tty0"})
	enumerator := &fakeEnumerator{ports: []serialport.Port{first}}
	manager := newManager(t, enumerator, config.NewFileStore(filepath.Join(t.TempDir(), "shares.json")), Options{})
	refresh(t, manager)
	setShared(t, manager, first.ID, true)
	enumerator.set([]serialport.Port{{Path: "/dev/tty1"}}, nil)
	refresh(t, manager)
	if got := manager.Snapshot(); len(got.Ports) != 1 || got.Ports[0].Online {
		t.Fatalf("fallback guessed new path: %#v", got)
	}
	if manager.LocalPorts()[0].Shared {
		t.Fatal("new path automatically shared")
	}
}

func TestEnumerationErrorsPreserveLastKnownState(t *testing.T) {
	port := usb("/dev/ttyUSB0", "unique")
	enumerator := &fakeEnumerator{ports: []serialport.Port{port}}
	manager := newManager(t, enumerator, config.NewFileStore(filepath.Join(t.TempDir(), "shares.json")), Options{})
	refresh(t, manager)
	setShared(t, manager, port.ID, true)
	snapshot := manager.Snapshot()
	local := manager.LocalPorts()
	failure := errors.New("enumeration failed")
	enumerator.set(nil, failure)
	if err := manager.Refresh(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("enumeration error: %v", err)
	}
	if !reflect.DeepEqual(snapshot, manager.Snapshot()) || !reflect.DeepEqual(local, manager.LocalPorts()) {
		t.Fatal("enumeration error wiped last known state")
	}
	enumerator.set([]serialport.Port{{}}, nil)
	if err := manager.Refresh(context.Background()); err == nil {
		t.Fatal("invalid enumeration accepted")
	}
	if !reflect.DeepEqual(snapshot, manager.Snapshot()) || !reflect.DeepEqual(local, manager.LocalPorts()) {
		t.Fatal("invalid enumeration wiped last known state")
	}
}

func TestPersistenceFailureDoesNotCommitOptInOrUnshare(t *testing.T) {
	port := usb("/dev/ttyUSB0", "unique")
	enumerator := &fakeEnumerator{ports: []serialport.Port{port}}
	failure := errors.New("disk unavailable")
	store := &failingStore{Store: config.NewFileStore(filepath.Join(t.TempDir(), "shares.json")), err: failure}
	manager := newManager(t, enumerator, store, Options{})
	refresh(t, manager)
	if err := manager.SetShared(context.Background(), port.ID, true); !errors.Is(err, failure) {
		t.Fatalf("failed opt-in: %v", err)
	}
	if len(manager.Snapshot().Ports) != 0 || manager.LocalPorts()[0].Shared {
		t.Fatal("failed save committed opt-in")
	}
	store.err = nil
	setShared(t, manager, port.ID, true)
	store.err = failure
	if err := manager.SetShared(context.Background(), port.ID, false); !errors.Is(err, failure) {
		t.Fatalf("failed unshare: %v", err)
	}
	if len(manager.Snapshot().Ports) != 1 {
		t.Fatal("failed save silently discarded persisted share")
	}
}

func TestAmbiguityFailsClosedWhenPersistenceFails(t *testing.T) {
	first := usb("/dev/ttyUSB0", "duplicate")
	second := usb("/dev/ttyUSB1", "duplicate")
	enumerator := &fakeEnumerator{ports: []serialport.Port{first}}
	store := &failingStore{Store: config.NewFileStore(filepath.Join(t.TempDir(), "shares.json"))}
	manager := newManager(t, enumerator, store, Options{})
	refresh(t, manager)
	setShared(t, manager, first.ID, true)
	store.err = errors.New("disk unavailable")
	enumerator.set([]serialport.Port{first, second}, nil)
	if err := manager.Refresh(context.Background()); err == nil {
		t.Fatal("persistence failure not reported")
	}
	enumerator.set([]serialport.Port{second}, nil)
	if err := manager.Refresh(context.Background()); err == nil {
		t.Fatal("dirty ambiguity was not retried")
	}
	if got := manager.Snapshot().Ports[0]; got.Online || !got.Ambiguous {
		t.Fatal("persistence error allowed an ambiguous port to reconnect")
	}
	store.err = nil
	refresh(t, manager)
	restarted := newManager(t, enumerator, store, Options{})
	refresh(t, restarted)
	if got := restarted.Snapshot().Ports[0]; got.Online || !got.Ambiguous {
		t.Fatal("ambiguity was not persisted after recovery")
	}
}

func TestSubscriptionsCoalesceCancelAndLimit(t *testing.T) {
	port := usb("/dev/ttyUSB0", "unique")
	enumerator := &fakeEnumerator{ports: []serialport.Port{port}}
	manager := newManager(t, enumerator, config.NewFileStore(filepath.Join(t.TempDir(), "shares.json")), Options{MaxSubscribers: 1})
	refresh(t, manager)
	ctx, cancel := context.WithCancel(context.Background())
	updates, unsubscribe, err := manager.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	if _, _, err := manager.Subscribe(context.Background()); !errors.Is(err, ErrSubscriberLimit) {
		t.Fatalf("subscriber limit: %v", err)
	}
	completed := make(chan struct{})
	go func() {
		defer close(completed)
		for index := 0; index < 50; index++ {
			if err := manager.SetShared(context.Background(), port.ID, index%2 == 0); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	select {
	case <-completed:
	case <-time.After(5 * time.Second):
		t.Fatal("slow subscriber blocked mutations")
	}
	if len(updates) != 1 {
		t.Fatalf("subscriber queue unbounded: %d", len(updates))
	}
	latest := <-updates
	if !reflect.DeepEqual(latest, manager.Snapshot()) {
		t.Fatalf("subscriber did not resync latest state: %#v", latest)
	}
	cancel()
	select {
	case _, open := <-updates:
		if open {
			t.Fatal("canceled subscription still open")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled subscription leaked")
	}
	reconnected, stop, err := manager.Subscribe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if got := <-reconnected; !reflect.DeepEqual(got, manager.Snapshot()) {
		t.Fatal("reconnect omitted full snapshot")
	}
}

func TestSnapshotAndSubscriptionAreIndependentCopies(t *testing.T) {
	port := usb("/dev/ttyUSB0", "unique")
	enumerator := &fakeEnumerator{ports: []serialport.Port{port}}
	manager := newManager(t, enumerator, config.NewFileStore(filepath.Join(t.TempDir(), "shares.json")), Options{})
	refresh(t, manager)
	setShared(t, manager, port.ID, true)
	updates, cancel, err := manager.Subscribe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	snapshot := manager.Snapshot()
	snapshot.Ports[0].ID = "changed"
	event := <-updates
	event.Ports[0].ID = "also changed"
	local := manager.LocalPorts()
	local[0].Path = "changed"
	if manager.Snapshot().Ports[0].ID != port.ID || manager.LocalPorts()[0].Path != port.Path {
		t.Fatal("caller mutated manager state")
	}
}

func TestWatcherRetriesErrorsAndCancels(t *testing.T) {
	port := usb("/dev/ttyUSB0", "unique")
	enumerator := &fakeEnumerator{err: errors.New("temporary error"), calls: make(chan struct{}, 10)}
	manager := newManager(t, enumerator, config.NewFileStore(filepath.Join(t.TempDir(), "shares.json")), Options{PollInterval: 5 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- manager.Run(ctx) }()
	select {
	case <-manager.Errors():
	case <-time.After(time.Second):
		t.Fatal("watcher did not report initial enumeration error")
	}
	if err := manager.Run(context.Background()); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("duplicate watcher: %v", err)
	}
	enumerator.set([]serialport.Port{port}, nil)
	deadline := time.After(time.Second)
	for len(manager.LocalPorts()) == 0 {
		select {
		case <-enumerator.calls:
		case <-deadline:
			t.Fatal("watcher did not recover")
		}
	}
	if len(manager.Snapshot().Ports) != 0 {
		t.Fatal("watcher automatically shared hotplug port")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("watcher returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("watcher ignored cancellation")
	}
}

func TestMissingPortCannotBeSharedAndBrokenConfigFailsStartup(t *testing.T) {
	enumerator := &fakeEnumerator{}
	store := config.NewFileStore(filepath.Join(t.TempDir(), "shares.json"))
	manager := newManager(t, enumerator, store, Options{})
	if err := manager.SetShared(context.Background(), "unknown", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing share: %v", err)
	}
	if _, err := NewManager(context.Background(), enumerator, config.NewFileStore(t.TempDir()), Options{}); err == nil {
		t.Fatal("unreadable config accepted")
	}
}

func TestRefreshObservesSharesFromOtherManager(t *testing.T) {
	first := usb("/dev/ttyUSB0", "first")
	second := usb("/dev/ttyUSB1", "second")
	enumerator := &fakeEnumerator{ports: []serialport.Port{first, second}}
	path := filepath.Join(t.TempDir(), "shares.json")
	serving := newManager(t, enumerator, config.NewFileStore(path), Options{})
	cli := newManager(t, enumerator, config.NewFileStore(path), Options{})
	refresh(t, serving)
	refresh(t, cli)
	setShared(t, cli, first.ID, true)
	refresh(t, serving)
	if got := serving.Snapshot(); len(got.Ports) != 1 || got.Ports[0].ID != first.ID {
		t.Fatalf("external opt-in not observed: %#v", got)
	}
	setShared(t, serving, second.ID, true)
	setShared(t, cli, first.ID, false)
	refresh(t, serving)
	if got := serving.Snapshot(); len(got.Ports) != 1 || got.Ports[0].ID != second.ID {
		t.Fatalf("external unshare lost unrelated opt-in: %#v", got)
	}
	enumerator.set(nil, errors.New("enumeration unavailable"))
	setShared(t, cli, second.ID, false)
	if err := serving.Refresh(context.Background()); err == nil {
		t.Fatal("enumeration error not reported")
	}
	if len(serving.Snapshot().Ports) != 0 || len(serving.LocalPorts()) != 2 {
		t.Fatal("external unshare must apply while retaining the failed enumeration's last local list")
	}
}

func TestExternalConfirmationCannotBypassCurrentAmbiguity(t *testing.T) {
	first := usb("/dev/ttyUSB0", "duplicate")
	second := usb("/dev/ttyUSB1", "duplicate")
	enumerator := &fakeEnumerator{ports: []serialport.Port{first}}
	path := filepath.Join(t.TempDir(), "shares.json")
	cli := newManager(t, enumerator, config.NewFileStore(path), Options{})
	serving := newManager(t, enumerator, config.NewFileStore(path), Options{})
	refresh(t, cli)
	setShared(t, cli, first.ID, true)
	enumerator.set([]serialport.Port{first, second}, nil)
	refresh(t, serving)
	state := config.State{Version: config.Version, Shares: []config.Choice{{Port: second}}}
	if err := config.NewFileStore(path).Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	refresh(t, serving)
	if got := serving.Snapshot().Ports[0]; got.Online || !got.Ambiguous {
		t.Fatalf("external config bypassed current ambiguity: %#v", got)
	}
	enumerator.set([]serialport.Port{second}, nil)
	refresh(t, serving)
	if got := serving.Snapshot().Ports[0]; got.Online || !got.Ambiguous {
		t.Fatal("external config reset durable ambiguity")
	}
	refresh(t, cli)
	setShared(t, cli, second.ID, true)
	refresh(t, serving)
	if got := serving.Snapshot().Ports[0]; !got.Online || got.Ambiguous {
		t.Fatal("explicit external confirmation did not resolve share")
	}
}

func TestWatcherObservesExternalChoicesWithoutRestart(t *testing.T) {
	port := usb("/dev/ttyUSB0", "unique")
	enumerator := &fakeEnumerator{ports: []serialport.Port{port}}
	path := filepath.Join(t.TempDir(), "shares.json")
	serving := newManager(t, enumerator, config.NewFileStore(path), Options{PollInterval: 5 * time.Millisecond})
	cli := newManager(t, enumerator, config.NewFileStore(path), Options{})
	refresh(t, cli)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- serving.Run(ctx) }()
	updates, stop, err := serving.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	<-updates
	setShared(t, cli, port.ID, true)
	select {
	case snapshot := <-updates:
		if len(snapshot.Ports) != 1 || snapshot.Ports[0].ID != port.ID {
			t.Fatalf("watcher external opt-in: %#v", snapshot)
		}
	case <-time.After(time.Second):
		t.Fatal("watcher did not reload external opt-in")
	}
	setShared(t, cli, port.ID, false)
	select {
	case snapshot := <-updates:
		if len(snapshot.Ports) != 0 {
			t.Fatalf("watcher external unshare: %#v", snapshot)
		}
	case <-time.After(time.Second):
		t.Fatal("watcher did not reload external unshare")
	}
	cancel()
	select {
	case <-result:
	case <-time.After(time.Second):
		t.Fatal("watcher did not stop")
	}
}

func TestBrokenReloadRetainsLastKnownState(t *testing.T) {
	port := usb("/dev/ttyUSB0", "unique")
	enumerator := &fakeEnumerator{ports: []serialport.Port{port}}
	path := filepath.Join(t.TempDir(), "shares.json")
	manager := newManager(t, enumerator, config.NewFileStore(path), Options{})
	refresh(t, manager)
	setShared(t, manager, port.ID, true)
	before := manager.Snapshot()
	local := manager.LocalPorts()
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	enumerator.set(nil, nil)
	if err := manager.Refresh(context.Background()); err == nil {
		t.Fatal("invalid external config not reported")
	}
	if !reflect.DeepEqual(before, manager.Snapshot()) || !reflect.DeepEqual(local, manager.LocalPorts()) {
		t.Fatal("broken config reload wiped last known state")
	}
	if err := manager.SetShared(context.Background(), port.ID, false); err == nil {
		t.Fatal("mutation overwrote broken config")
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != "{" {
		t.Fatal("mutation replaced broken config")
	}
}
