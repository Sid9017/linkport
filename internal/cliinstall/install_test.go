package cliinstall

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallVersionsAndCorruption(t *testing.T) {
	root := t.TempDir()
	first, err := Install(root, []byte("first CLI"))
	if err != nil {
		t.Fatal(err)
	}
	again, err := Install(root, []byte("first CLI"))
	if err != nil || again != first {
		t.Fatalf("not idempotent: %q, %v", again, err)
	}
	next, err := Install(root, []byte("next CLI"))
	if err != nil || next == first {
		t.Fatalf("upgrade did not create a new executable: %q, %v", next, err)
	}
	if got, err := os.ReadFile(first); err != nil || string(got) != "first CLI" {
		t.Fatalf("old executable changed: %q, %v", got, err)
	}
	if err := os.WriteFile(first, []byte("unexpected"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(root, []byte("first CLI")); err == nil {
		t.Fatal("silently overwrote modified executable")
	}
	if _, err := Install(root, nil); err == nil {
		t.Fatal("accepted empty executable")
	}
}

func TestInstallRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target, err := Install(root, []byte("CLI"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("CLI"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, target); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Install(root, []byte("CLI")); err == nil {
		t.Fatal("followed unexpected symlink")
	}
}

func TestInstallReportsBlockedDestination(t *testing.T) {
	root := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(root, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(root, []byte("CLI")); err == nil {
		t.Fatal("accepted a file as installation directory")
	}
}

func TestInstallRejectsDirectoryAtExecutablePath(t *testing.T) {
	root := t.TempDir()
	target, err := Install(root, []byte("CLI"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(root, []byte("CLI")); err == nil {
		t.Fatal("accepted directory as executable")
	}
}
