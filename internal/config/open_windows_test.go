package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestReplaceConfigFileUnsupportedRename(t *testing.T) {
	for _, unsupported := range []error{windows.ERROR_INVALID_PARAMETER, windows.ERROR_NOT_SUPPORTED} {
		for _, heldOpen := range []bool{false, true} {
			name := unsupported.Error()
			if heldOpen {
				name += "/open-reader"
			}
			t.Run(name, func(t *testing.T) {
				directory := filepath.Join(t.TempDir(), "共享设置", strings.Repeat("long-config-path-", 6), strings.Repeat("long-config-path-", 6))
				if err := os.MkdirAll(directory, 0700); err != nil {
					t.Fatal(err)
				}
				source := filepath.Join(directory, "next.json")
				target := filepath.Join(directory, "shares.json")
				if err := os.WriteFile(source, []byte("next"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, []byte("previous"), 0600); err != nil {
					t.Fatal(err)
				}
				if heldOpen {
					reader, err := os.Open(target)
					if err != nil {
						t.Fatal(err)
					}
					defer reader.Close()
				}
				err := replaceConfigFileWithInfo(source, target, func(windows.Handle, uint32, *byte, uint32) error {
					return unsupported
				})
				expected := "next"
				if heldOpen {
					if err == nil {
						t.Fatal("legacy rename replaced an open destination")
					}
					expected = "previous"
					contents, readErr := os.ReadFile(source)
					if readErr != nil || string(contents) != "next" {
						t.Fatalf("failed rename changed source: %q, %v", contents, readErr)
					}
				} else if err != nil {
					t.Fatalf("unsupported POSIX rename did not fall back: %v", err)
				} else if _, err := os.Stat(source); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("successful rename retained source: %v", err)
				}
				contents, readErr := os.ReadFile(target)
				if readErr != nil || string(contents) != expected {
					t.Fatalf("destination contents: %q, %v", contents, readErr)
				}
			})
		}
	}
}

func TestReplaceConfigFileDoesNotFallBackOnOtherErrors(t *testing.T) {
	for _, failure := range []error{windows.ERROR_ACCESS_DENIED, windows.ERROR_SHARING_VIOLATION} {
		t.Run(failure.Error(), func(t *testing.T) {
			directory := t.TempDir()
			source := filepath.Join(directory, "next.json")
			target := filepath.Join(directory, "shares.json")
			for path, contents := range map[string]string{source: "next", target: "previous"} {
				if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
					t.Fatal(err)
				}
			}
			err := replaceConfigFileWithInfo(source, target, func(windows.Handle, uint32, *byte, uint32) error {
				return failure
			})
			if !errors.Is(err, failure) {
				t.Fatalf("unexpected rename error: %v", err)
			}
			for path, expected := range map[string]string{source: "next", target: "previous"} {
				contents, readErr := os.ReadFile(path)
				if readErr != nil || string(contents) != expected {
					t.Fatalf("failed rename changed %s: %q, %v", path, contents, readErr)
				}
			}
		})
	}
}
