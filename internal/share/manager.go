package share

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/Sid9017/linkport/internal/config"
	"github.com/Sid9017/linkport/internal/serialport"
)

var (
	ErrNotFound        = errors.New("serial port not found")
	ErrAmbiguous       = errors.New("serial port identity is ambiguous")
	ErrAlreadyRunning  = errors.New("serial port watcher already running")
	ErrSubscriberLimit = errors.New("serial port subscriber limit reached")
)

type LocalPort struct {
	serialport.Port
	Shared    bool `json:"shared"`
	Ambiguous bool `json:"ambiguous"`
}

type SharedPort struct {
	ID           string `json:"id"`
	Path         string `json:"path"`
	IsUSB        bool   `json:"is_usb"`
	VID          string `json:"vid,omitempty"`
	PID          string `json:"pid,omitempty"`
	SerialNumber string `json:"serial_number,omitempty"`
	Product      string `json:"product,omitempty"`
	Online       bool   `json:"online"`
	Ambiguous    bool   `json:"ambiguous"`
}

type Snapshot struct {
	Revision uint64       `json:"revision"`
	Ports    []SharedPort `json:"ports"`
}

type Options struct {
	PollInterval   time.Duration
	MaxSubscribers int
}

type Manager struct {
	enumerator     serialport.Enumerator
	store          config.Store
	interval       time.Duration
	maxSubscribers int
	operations     sync.Mutex
	mu             sync.Mutex
	local          []serialport.Port
	choices        map[string]config.Choice
	snapshot       Snapshot
	watching       bool
	listeners      map[chan Snapshot]struct{}
	errors         chan error
	choicesDirty   bool
}

func NewManager(ctx context.Context, enumerator serialport.Enumerator, store config.Store, options Options) (*Manager, error) {
	if enumerator == nil || store == nil {
		return nil, errors.New("serial enumerator and sharing store are required")
	}
	if options.PollInterval < 0 {
		return nil, errors.New("poll interval must not be negative")
	}
	if options.MaxSubscribers < 0 {
		return nil, errors.New("subscriber limit must not be negative")
	}
	state, err := store.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("load sharing choices: %w", err)
	}
	if err := config.Validate(state); err != nil {
		return nil, err
	}
	interval := options.PollInterval
	if interval == 0 {
		interval = time.Second
	}
	maxSubscribers := options.MaxSubscribers
	if maxSubscribers == 0 {
		maxSubscribers = 64
	}
	manager := &Manager{
		enumerator:     enumerator,
		store:          store,
		interval:       interval,
		maxSubscribers: maxSubscribers,
		choices:        make(map[string]config.Choice, len(state.Shares)),
		listeners:      make(map[chan Snapshot]struct{}),
		errors:         make(chan error, 1),
		snapshot:       Snapshot{Revision: 1, Ports: []SharedPort{}},
	}
	for _, choice := range state.Shares {
		manager.choices[choice.Port.ID] = choice
	}
	manager.snapshot.Ports = manager.sharedPortsLocked()
	return manager, nil
}

func (manager *Manager) Refresh(ctx context.Context) error {
	manager.operations.Lock()
	defer manager.operations.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	choices, dirty, err := manager.loadChoices(ctx)
	if err != nil {
		return err
	}
	ports, err := manager.enumerator.Enumerate(ctx)
	var enumerationErr error
	if err != nil {
		enumerationErr = fmt.Errorf("enumerate serial ports: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	local := make([]serialport.Port, len(ports))
	for index, port := range ports {
		if port.Path == "" {
			enumerationErr = errors.New("enumerator returned a serial port without a path")
			break
		}
		local[index] = serialport.Normalize(port)
	}
	if enumerationErr == nil {
		sort.Slice(local, func(first, second int) bool {
			if local[first].ID == local[second].ID {
				return local[first].Path < local[second].Path
			}
			return local[first].ID < local[second].ID
		})
	}
	manager.mu.Lock()
	if enumerationErr == nil {
		manager.local = local
	}
	manager.choices = choices
	manager.choicesDirty = dirty
	manager.quarantineLocked()
	manager.publishLocked()
	dirty = manager.choicesDirty
	state := stateFromChoices(manager.choices)
	manager.mu.Unlock()
	if dirty {
		if err := manager.store.Save(ctx, state); err != nil {
			return errors.Join(enumerationErr, fmt.Errorf("save ambiguous sharing choices: %w", err))
		}
		manager.mu.Lock()
		manager.choicesDirty = false
		manager.mu.Unlock()
	}
	return enumerationErr
}

func (manager *Manager) quarantineLocked() {
	counts := manager.countsLocked()
	for identity, choice := range manager.choices {
		if counts[identity] > 1 && !choice.NeedsConfirmation {
			choice.NeedsConfirmation = true
			manager.choices[identity] = choice
			manager.choicesDirty = true
		}
	}
}

func (manager *Manager) LocalPorts() []LocalPort {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	counts := manager.countsLocked()
	ports := make([]LocalPort, 0, len(manager.local))
	for _, port := range manager.local {
		choice, shared := manager.choices[port.ID]
		ports = append(ports, LocalPort{
			Port:      port,
			Shared:    shared && counts[port.ID] == 1 && !choice.NeedsConfirmation,
			Ambiguous: counts[port.ID] > 1 || choice.NeedsConfirmation,
		})
	}
	return ports
}

func (manager *Manager) Snapshot() Snapshot {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return cloneSnapshot(manager.snapshot)
}

func (manager *Manager) SetShared(ctx context.Context, identity string, enabled bool) error {
	manager.operations.Lock()
	defer manager.operations.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	loaded, dirty, err := manager.loadChoices(ctx)
	if err != nil {
		return err
	}
	manager.mu.Lock()
	manager.choices = loaded
	manager.choicesDirty = dirty
	manager.quarantineLocked()
	manager.publishLocked()
	choices := make(map[string]config.Choice, len(manager.choices)+1)
	for savedID, choice := range manager.choices {
		choices[savedID] = choice
	}
	if enabled {
		var candidate serialport.Port
		count := 0
		for _, port := range manager.local {
			if port.ID == identity {
				candidate = port
				count++
			}
		}
		if count != 1 {
			manager.mu.Unlock()
			if count > 1 {
				return ErrAmbiguous
			}
			return ErrNotFound
		}
		if choice, exists := choices[identity]; exists && !choice.NeedsConfirmation && !manager.choicesDirty {
			manager.mu.Unlock()
			return nil
		}
		choices[identity] = config.Choice{Port: candidate}
	} else {
		if _, exists := choices[identity]; !exists {
			manager.mu.Unlock()
			return nil
		}
		delete(choices, identity)
	}
	manager.mu.Unlock()
	state := stateFromChoices(choices)
	if err := manager.store.Save(ctx, state); err != nil {
		return fmt.Errorf("save sharing choices: %w", err)
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.choices = choices
	manager.choicesDirty = false
	manager.publishLocked()
	return nil
}

func (manager *Manager) Run(ctx context.Context) error {
	manager.mu.Lock()
	if manager.watching {
		manager.mu.Unlock()
		return ErrAlreadyRunning
	}
	manager.watching = true
	manager.mu.Unlock()
	defer func() {
		manager.mu.Lock()
		manager.watching = false
		manager.mu.Unlock()
	}()
	ticker := time.NewTicker(manager.interval)
	defer ticker.Stop()
	for {
		if err := manager.Refresh(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			select {
			case manager.errors <- err:
			default:
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (manager *Manager) Errors() <-chan error {
	return manager.errors
}

func (manager *Manager) Subscribe(ctx context.Context) (<-chan Snapshot, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	updates := make(chan Snapshot, 1)
	done := make(chan struct{})
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			manager.mu.Lock()
			delete(manager.listeners, updates)
			close(updates)
			manager.mu.Unlock()
			close(done)
		})
	}
	manager.mu.Lock()
	if len(manager.listeners) >= manager.maxSubscribers {
		manager.mu.Unlock()
		return nil, nil, ErrSubscriberLimit
	}
	manager.listeners[updates] = struct{}{}
	updates <- cloneSnapshot(manager.snapshot)
	manager.mu.Unlock()
	if ctx.Err() != nil {
		cancel()
	} else {
		go func() {
			select {
			case <-ctx.Done():
				cancel()
			case <-done:
			}
		}()
	}
	return updates, cancel, nil
}

func (manager *Manager) countsLocked() map[string]int {
	counts := make(map[string]int, len(manager.local))
	for _, port := range manager.local {
		counts[port.ID]++
	}
	return counts
}

func (manager *Manager) sharedPortsLocked() []SharedPort {
	current := make(map[string][]serialport.Port, len(manager.local))
	for _, port := range manager.local {
		current[port.ID] = append(current[port.ID], port)
	}
	ports := make([]SharedPort, 0, len(manager.choices))
	for identity, choice := range manager.choices {
		matches := current[identity]
		port := choice.Port
		if len(matches) == 1 && !choice.NeedsConfirmation {
			port = matches[0]
		}
		ports = append(ports, SharedPort{
			ID:           identity,
			Path:         port.Path,
			IsUSB:        port.IsUSB,
			VID:          port.VID,
			PID:          port.PID,
			SerialNumber: port.SerialNumber,
			Product:      port.Product,
			Online:       len(matches) == 1 && !choice.NeedsConfirmation,
			Ambiguous:    len(matches) > 1 || choice.NeedsConfirmation,
		})
	}
	sort.Slice(ports, func(first, second int) bool {
		return ports[first].ID < ports[second].ID
	})
	return ports
}

func (manager *Manager) publishLocked() {
	ports := manager.sharedPortsLocked()
	if reflect.DeepEqual(manager.snapshot.Ports, ports) {
		return
	}
	manager.snapshot.Revision++
	manager.snapshot.Ports = ports
	for listener := range manager.listeners {
		select {
		case <-listener:
		default:
		}
		listener <- cloneSnapshot(manager.snapshot)
	}
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	snapshot.Ports = append([]SharedPort{}, snapshot.Ports...)
	return snapshot
}

func stateFromChoices(choices map[string]config.Choice) config.State {
	state := config.State{Version: config.Version, Shares: make([]config.Choice, 0, len(choices))}
	for _, choice := range choices {
		state.Shares = append(state.Shares, choice)
	}
	sort.Slice(state.Shares, func(first, second int) bool {
		return state.Shares[first].Port.ID < state.Shares[second].Port.ID
	})
	return state
}

func (manager *Manager) loadChoices(ctx context.Context) (map[string]config.Choice, bool, error) {
	state, err := manager.store.Load(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("reload sharing choices: %w", err)
	}
	if err := config.Validate(state); err != nil {
		return nil, false, err
	}
	choices := make(map[string]config.Choice, len(state.Shares))
	dirty := false
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for _, choice := range state.Shares {
		if previous, exists := manager.choices[choice.Port.ID]; manager.choicesDirty && exists && previous.NeedsConfirmation && !choice.NeedsConfirmation {
			choice.NeedsConfirmation = true
			dirty = true
		}
		choices[choice.Port.ID] = choice
	}
	return choices, dirty, nil
}
