package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sid9017/linkport/internal/relaycatalog"
)

func TestVerifyPublishedDirectory(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	previousKey := relaycatalog.PublicKey
	relaycatalog.PublicKey = base64.StdEncoding.EncodeToString(public)
	t.Cleanup(func() { relaycatalog.PublicKey = previousKey })
	now := time.Now().UTC().Truncate(time.Second)
	manifest := relaycatalog.Manifest{
		Format: 1, Version: 2, PublishedAt: now.Add(-time.Hour),
		ExpiresAt: now.Add(time.Hour), Relays: []string{"https://207.148.110.58"},
	}
	signed, err := relaycatalog.Sign(manifest, private)
	if err != nil {
		t.Fatal(err)
	}
	manifest.ExpiresAt = now.Add(-time.Minute)
	expired, err := relaycatalog.Sign(manifest, private)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		raw  []byte
		ok   bool
	}{
		{"valid", signed, true},
		{"tampered", bytes.ReplaceAll(signed, []byte("207.148.110.58"), []byte("8.8.8.8")), false},
		{"expired", expired, false},
		{"oversized", make([]byte, relaycatalog.MaxSize+1), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "relay-directory.json")
			if err := os.WriteFile(path, test.raw, 0600); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			err := run([]string{"-verify", path}, &output)
			if (err == nil) != test.ok {
				t.Fatalf("verification result: %v", err)
			}
			if test.ok && !strings.Contains(output.String(), "version=2 relays=1") {
				t.Fatal("verification did not report authenticated metadata")
			}
		})
	}
}

func TestSignThenVerify(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	previousKey := relaycatalog.PublicKey
	relaycatalog.PublicKey = base64.StdEncoding.EncodeToString(public)
	t.Cleanup(func() { relaycatalog.PublicKey = previousKey })
	directory := t.TempDir()
	keyPath := filepath.Join(directory, "offline.key")
	secret := base64.StdEncoding.EncodeToString(private)
	if err := os.WriteFile(keyPath, []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "relay-directory.json")
	var output bytes.Buffer
	if err := run([]string{"-key", keyPath, "-version", "3", "-relays", "https://207.148.110.58", "-output", path}, &output); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-verify", path}, &output); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), secret) {
		t.Fatal("publisher output exposed its signing key")
	}
	if err := run([]string{"-verify", path, "-key", keyPath}, &output); err == nil {
		t.Fatal("verification accepted signing options")
	}
}
