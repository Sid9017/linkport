package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"os"
	"path/filepath"
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
	encoded, err := os.ReadFile(archive + ".sig")
	if err != nil {
		t.Fatal(err)
	}
	signature, err := base64.StdEncoding.DecodeString(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(contents)
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
