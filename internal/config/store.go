package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/Sid9017/linkport/internal/serialport"
)

const Version = 1

type Choice struct {
	Port              serialport.Port `json:"port"`
	NeedsConfirmation bool            `json:"needs_confirmation,omitempty"`
}

type State struct {
	Version int      `json:"version"`
	Shares  []Choice `json:"shares"`
}

type Store interface {
	Load(context.Context) (State, error)
	Save(context.Context, State) error
}

type FileStore struct {
	path string
	mu   sync.Mutex
}

func NewFileStore(path string) *FileStore {
	return &FileStore{path: path}
}

func Validate(state State) error {
	if state.Version != Version {
		return fmt.Errorf("unsupported sharing config version %d", state.Version)
	}
	identities := make(map[string]bool, len(state.Shares))
	for _, choice := range state.Shares {
		port := choice.Port
		if port.Path == "" || port.ID != serialport.Identity(port) {
			return fmt.Errorf("invalid saved port identity %q", port.ID)
		}
		if identities[port.ID] {
			return fmt.Errorf("duplicate saved port identity %q", port.ID)
		}
		identities[port.ID] = true
	}
	return nil
}

func (store *FileStore) Load(ctx context.Context) (State, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	file, err := os.Open(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return State{Version: Version, Shares: []Choice{}}, nil
	}
	if err != nil {
		return State{}, err
	}
	defer file.Close()
	var state State
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&state); err != nil {
		return State{}, fmt.Errorf("decode sharing config: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return State{}, errors.New("sharing config must contain one JSON object")
	}
	if err := Validate(state); err != nil {
		return State{}, err
	}
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	return state, nil
}

func (store *FileStore) Save(ctx context.Context, state State) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := Validate(state); err != nil {
		return err
	}
	contents, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	directory := filepath.Dir(store.path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".linkport-shares-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(append(contents, '\n')); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), store.path); err != nil {
		return err
	}
	return nil
}
