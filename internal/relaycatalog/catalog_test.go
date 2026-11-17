package relaycatalog

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func TestSignedDirectory(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	manifest := Manifest{Format: 1, Version: 2, PublishedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), Relays: []string{"https://207.148.110.58"}}
	raw, err := Sign(manifest, private)
	if err != nil {
		t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString(public)
	verified, err := Verify(raw, key, now)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Version() != 2 || !verified.Fresh(now) {
		t.Fatal("valid directory not fresh")
	}
	urls := verified.URLs()
	urls[0] = "https://1.1.1.1"
	if verified.URLs()[0] != manifest.Relays[0] {
		t.Fatal("directory URLs escaped ownership")
	}
	saved := verified.Bytes()
	saved[0] = 'x'
	if _, err := Verify(verified.Bytes(), key, now); err != nil {
		t.Fatal("directory bytes escaped ownership", err)
	}
	expired, err := Verify(raw, key, now.Add(2*time.Hour))
	if err != nil || expired.Fresh(now.Add(2*time.Hour)) {
		t.Fatal("expired directory must authenticate for rollback protection only", err)
	}
	for _, test := range []struct {
		name string
		raw  []byte
		key  string
	}{
		{"wrong key", raw, base64.StdEncoding.EncodeToString(make([]byte, 32))},
		{"trailing object", append(append([]byte(nil), raw...), []byte(" {}")...), key},
		{"oversize", make([]byte, MaxSize+1), key},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Verify(test.raw, test.key, now); err == nil {
				t.Fatal("accepted invalid directory")
			}
		})
	}
	var altered Manifest
	if err := json.Unmarshal(raw, &altered); err != nil {
		t.Fatal(err)
	}
	altered.Relays = []string{"https://1.1.1.1"}
	tampered, _ := json.Marshal(altered)
	if _, err := Verify(tampered, key, now); err == nil {
		t.Fatal("accepted modified signed relay")
	}
}

func TestRejectUnsafeRelayURLs(t *testing.T) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, address := range []string{"http://207.148.110.58", "https://127.0.0.1", "https://10.0.0.1", "https://[::1]", "https://169.254.169.254", "https://[fe80::1]", "https://example.com", "https://user@207.148.110.58", "https://207.148.110.58/path", "https://207.148.110.58?query=1", "https://207.148.110.58:0", "https://207.148.110.58:65536"} {
		t.Run(address, func(t *testing.T) {
			_, err := Sign(Manifest{Format: 1, Version: 1, PublishedAt: now, ExpiresAt: now.Add(time.Hour), Relays: []string{address}}, private)
			if err == nil {
				t.Fatal("accepted unsafe relay URL")
			}
		})
	}
}

func TestRejectDuplicateCanonicalRelay(t *testing.T) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	_, err = Sign(Manifest{Format: 1, Version: 1, PublishedAt: now, ExpiresAt: now.Add(time.Hour), Relays: []string{"https://207.148.110.58", "https://207.148.110.58:443/"}}, private)
	if err == nil {
		t.Fatal("accepted duplicate relay under a different URL spelling")
	}
}

func TestEmbeddedDirectory(t *testing.T) {
	if _, err := Verify(Bootstrap, PublicKey, time.Now()); err != nil {
		t.Fatal(err)
	}
}
