package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseSignatureAuthenticatesSHA256Digest(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LINKPORT_UPDATE_PRIVATE_KEY", base64.StdEncoding.EncodeToString(private))
	t.Setenv("LINKPORT_UPDATE_PUBLIC_KEY", base64.StdEncoding.EncodeToString(public))
	directory := t.TempDir()
	archive := filepath.Join(directory, "linkport-desktop_0.0.2_darwin_arm64.zip")
	contents := []byte("update artifact")
	if err := os.WriteFile(archive, contents, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run("", directory); err != nil {
		t.Fatal(err)
	}
	metadata, err := os.ReadFile(filepath.Join(directory, "desktop-checksums.txt"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(metadata)), "\n")
	if len(lines) != 2 {
		t.Fatalf("unexpected checksum metadata: %q", metadata)
	}
	fields := strings.Fields(lines[1])
	if len(fields) != 4 || fields[0] != "#" || fields[1] != "ed25519" || fields[3] != filepath.Base(archive) {
		t.Fatalf("invalid inline signature: %q", lines[1])
	}
	signature, err := base64.StdEncoding.DecodeString(fields[2])
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(contents)
	if lines[0] != fmt.Sprintf("%x  %s", digest, filepath.Base(archive)) {
		t.Fatalf("invalid checksum: %q", lines[0])
	}
	if _, err := os.Stat(archive + ".sig"); !os.IsNotExist(err) {
		t.Fatal("standalone signature file was created")
	}
	if !ed25519.Verify(public, digest[:], signature) {
		t.Fatal("release signature does not authenticate the digest")
	}
	digest[0] ^= 1
	if ed25519.Verify(public, digest[:], signature) {
		t.Fatal("modified artifact accepted")
	}
}

func TestGenerateRefusesToOverwritePrivateKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	if err := run(path, ""); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := run(path, ""); err == nil {
		t.Fatal("overwrote private key")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("private key changed")
	}
}

func TestNativePackageSignaturesStayInline(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LINKPORT_UPDATE_PRIVATE_KEY", base64.StdEncoding.EncodeToString(private))
	t.Setenv("LINKPORT_UPDATE_PUBLIC_KEY", base64.StdEncoding.EncodeToString(public))
	directory := t.TempDir()
	names := []string{}
	for platform, extension := range map[string]string{"darwin": "dmg", "windows": "exe", "linux": "deb"} {
		for _, arch := range []string{"amd64", "arm64"} {
			name := "linkport-desktop_0.0.7_" + platform + "_" + arch + "." + extension
			names = append(names, name)
			if err := os.WriteFile(filepath.Join(directory, name), []byte(name), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := run("", directory); err != nil {
		t.Fatal(err)
	}
	metadata, err := os.ReadFile(filepath.Join(directory, "desktop-checksums.txt"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(metadata)), "\n")
	if len(lines) != 12 {
		t.Fatal("expected six inline signatures")
	}
	for i := 0; i < len(lines); i += 2 {
		fields := strings.Fields(lines[i+1])
		if len(fields) != 4 {
			t.Fatal("invalid signature metadata")
		}
		signature, err := base64.StdEncoding.DecodeString(fields[2])
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256([]byte(fields[3]))
		if !ed25519.Verify(public, digest[:], signature) {
			t.Fatal("native signature rejected")
		}
	}
	files, err := os.ReadDir(directory)
	if err != nil || len(files) != 7 {
		t.Fatal("unexpected release assets")
	}
}
