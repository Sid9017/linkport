// Package cliinstall installs the CLI carried by a desktop build without admin access.
package cliinstall

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Install stores each payload in an immutable directory. Existing CLI processes
// keep their executable during an upgrade, including on Windows.
func Install(root string, payload []byte) (string, error) {
	if len(payload) == 0 {
		return "", errors.New("empty bundled CLI")
	}
	digest := sha256.Sum256(payload)
	name := "linkport"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	directory := filepath.Join(root, "versions", hex.EncodeToString(digest[:]))
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	target := filepath.Join(directory, name)
	if info, err := os.Lstat(target); err == nil {
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("CLI destination is not a regular file: %s", target)
		}
		data, err := os.ReadFile(target)
		if err != nil {
			return "", err
		}
		if !bytes.Equal(data, payload) {
			return "", fmt.Errorf("CLI destination has unexpected contents: %s", target)
		}
		if err := os.Chmod(target, 0755); err != nil {
			return "", err
		}
		return target, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	file, err := os.CreateTemp(directory, ".cli-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(payload); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Chmod(0755); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(file.Name(), target); err != nil {
		return "", err
	}
	return target, nil
}
