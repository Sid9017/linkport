package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareNativeDiskImage(t *testing.T) {
	source := os.Getenv("LINKPORT_TEST_DMG")
	if source == "" {
		t.Skip("set LINKPORT_TEST_DMG for native disk image integration")
	}
	payload, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "payload.dmg")
	if err = os.WriteFile(destination, payload, 0600); err != nil {
		t.Fatal(err)
	}
	version := os.Getenv("LINKPORT_TEST_DMG_VERSION")
	if err = prepareNativeUpdate(t.Context(), destination, "wrong-version"); err == nil {
		t.Fatal("accepted wrong bundle version")
	}
	if stat, err := os.Stat(destination); err != nil || stat.IsDir() {
		t.Fatal("failed preparation changed original image")
	}
	if err = prepareNativeUpdate(t.Context(), destination, version); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(destination, "Contents/MacOS/linkport-desktop")); err != nil {
		t.Fatal(err)
	}
	if err = prepareNativeUpdate(t.Context(), destination, version); err != nil {
		t.Fatal("ready bundle cannot be retried", err)
	}
}
