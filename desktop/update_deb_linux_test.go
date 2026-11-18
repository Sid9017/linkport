package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/updater"
)

func TestDebianPackageInstallerHandoff(t *testing.T) {
	if _, err := exec.LookPath("dpkg-deb"); err != nil {
		t.Skip("Debian package tools required")
	}
	root := t.TempDir()
	control := filepath.Join(root, "DEBIAN")
	if err := os.Mkdir(control, 0755); err != nil {
		t.Fatal(err)
	}
	metadata := "Package: linkport\nVersion: 0.0.7\nArchitecture: " + runtime.GOARCH + "\nMaintainer: Linkport <test@example.invalid>\nDescription: Installer fixture\n"
	if err := os.WriteFile(filepath.Join(control, "control"), []byte(metadata), 0644); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "fixture.deb")
	if output, err := exec.Command("dpkg-deb", "--build", "--root-owner-group", root, archive).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	if err := validateDeb(t.Context(), archive, "0.0.7", runtime.GOARCH); err != nil {
		t.Fatal(err)
	}
	if err := validateDeb(t.Context(), archive, "0.0.8", runtime.GOARCH); err == nil {
		t.Fatal("wrong version accepted")
	}
	body, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	key, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(body)
	release := &updater.Release{Version: "0.0.7", Artifact: updater.Artifact{Filename: "linkport-desktop_0.0.7_linux_" + runtime.GOARCH + ".deb", Size: int64(len(body)), Filetype: "deb"}, Verification: &updater.Verification{DigestAlgo: "sha256", Digest: digest[:], SignatureAlgo: "ed25519", Signature: ed25519.Sign(private, digest[:])}}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	controller := &updateController{ctx: t.Context(), provider: &signedProvider{Provider: packageProvider{body: body}, key: key}, pending: release}
	controller.downloadDeb(t.Context(), release)
	if controller.status.Error != "" || controller.nativeState != "package-ready" {
		t.Fatalf("not ready: %+v", controller.status)
	}
	scripts := t.TempDir()
	marker := filepath.Join(scripts, "opened")
	t.Setenv("LINKPORT_INSTALLER_TEST_MARKER", marker)
	if err := os.WriteFile(filepath.Join(scripts, "xdg-open"), []byte("#!/bin/sh\nprintf '%s' \"$1\" > \"$LINKPORT_INSTALLER_TEST_MARKER\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", scripts+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := controller.openDeb(controller.packagePath); err != nil {
		t.Fatal(err)
	}
	if controller.nativeState != "installer-opened" {
		t.Fatal("handoff was not reported")
	}
	opened, err := os.ReadFile(marker)
	if err != nil || string(opened) != controller.packagePath {
		t.Fatal("wrong installer payload")
	}
	if err := os.WriteFile(controller.packagePath, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := controller.openDeb(controller.packagePath); err == nil {
		t.Fatal("tampered package handed to installer")
	}
	_ = os.RemoveAll(filepath.Dir(controller.packagePath))
}
