package cliinstall

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Register changes only the current user's PATH. Versioned directories let a
// running old CLI finish while new terminals use the new desktop's payload.
func Register(root, executable, home, zshDirectory, configDirectory string) error {
	if strings.Contains(filepath.Dir(executable), ";") {
		return fmt.Errorf("CLI directory contains a PATH separator")
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	previous, kind, err := key.GetStringValue("Path")
	if err == registry.ErrNotExist {
		previous, kind, err = "", registry.EXPAND_SZ, nil
	}
	if err != nil {
		return err
	}
	next := updatedPath(previous, root, filepath.Dir(executable))
	if next == previous {
		return nil
	}
	if kind == registry.EXPAND_SZ {
		err = key.SetExpandStringValue("Path", next)
	} else {
		err = key.SetStringValue("Path", next)
	}
	if err != nil {
		return err
	}
	// Tell Explorer to refresh the environment used for subsequently launched apps.
	message, err := windows.UTF16PtrFromString("Environment")
	if err != nil {
		return err
	}
	send := windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")
	var result uintptr
	ok, _, callErr := send.Call(0xffff, 0x001a, 0, uintptr(unsafe.Pointer(message)), 0x0002, uintptr((5*time.Second)/time.Millisecond), uintptr(unsafe.Pointer(&result)))
	if ok == 0 {
		return fmt.Errorf("CLI PATH saved, but environment notification failed (sign out and back in): %w", callErr)
	}
	return nil
}

func updatedPath(previous, root, directory string) string {
	var entries []string
	if previous == "" {
		return directory
	}
	for _, entry := range strings.Split(previous, ";") {
		parent := filepath.Dir(entry)
		leaf := filepath.Base(entry)
		managed := strings.EqualFold(parent, filepath.Join(root, "versions")) && len(leaf) == 64
		if managed {
			for _, c := range leaf {
				if !strings.ContainsRune("0123456789abcdef", c) {
					managed = false
					break
				}
			}
		}
		if !managed {
			entries = append(entries, entry)
		}
	}
	return strings.Join(append(entries, directory), ";")
}
