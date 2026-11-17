package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type preferences struct {
	FontSize    int    `json:"fontSize"`
	Language    string `json:"language"`
	Theme       string `json:"theme"`
	CloseToTray bool   `json:"closeToTray"`
	AutoUpdate  bool   `json:"autoUpdate"`
}

func defaultPreferences() preferences {
	return preferences{FontSize: 13, Language: "zh-CN", Theme: "system", CloseToTray: true, AutoUpdate: true}
}

func (settings preferences) validate() error {
	if settings.FontSize < 11 || settings.FontSize > 18 {
		return errors.New("fontSize must be between 11 and 18")
	}
	if settings.Language != "zh-CN" && settings.Language != "en" {
		return errors.New("unsupported language")
	}
	if settings.Theme != "system" && settings.Theme != "light" && settings.Theme != "dark" {
		return errors.New("unsupported theme")
	}
	return nil
}

type preferenceStore struct {
	mu       sync.RWMutex
	path     string
	settings preferences
}

func newPreferenceStore(path string) (*preferenceStore, error) {
	store := &preferenceStore{path: path, settings: defaultPreferences()}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(content, &store.settings); err != nil {
		return nil, fmt.Errorf("read preferences: %w", err)
	}
	if err := store.settings.validate(); err != nil {
		return nil, err
	}
	return store, nil
}

func (store *preferenceStore) get() preferences {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return store.settings
}

func (store *preferenceStore) save(settings preferences) error {
	if err := settings.validate(); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	content, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	directory := filepath.Dir(store.path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".desktop-preferences-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(append(content, '\n')); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), store.path); err != nil {
		return err
	}
	store.settings = settings
	return nil
}
