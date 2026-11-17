// Package relaycatalog verifies the operator-maintained public relay directory.
// It never accepts a signing key or directory URL from the desktop UI.
package relaycatalog

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"strconv"
	"time"

	_ "embed" // Embeds the publisher-signed bootstrap directory.
)

// These public values are set by the publisher build, not device configuration.
var PublicKey = "f9O9q0EY9/eTxvYLHnyRUtsvw4kyVUaNGMXaVKRZNIc="
var SourceURL = "https://207.148.110.58/relay-directory.json"

//go:embed bootstrap.json
var Bootstrap []byte

const MaxSize = 16 << 10
const domain = "linkport-relay-directory-v1\n"

type Manifest struct {
	Format      int       `json:"format"`
	Version     uint64    `json:"version"`
	PublishedAt time.Time `json:"publishedAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
	Relays      []string  `json:"relays"`
	Signature   string    `json:"signature"`
}

// Verified can only be constructed by signature validation in this package.
type Verified struct {
	manifest Manifest
	raw      []byte
}

func (v Verified) Version() uint64      { return v.manifest.Version }
func (v Verified) URLs() []string       { return append([]string(nil), v.manifest.Relays...) }
func (v Verified) ExpiresAt() time.Time { return v.manifest.ExpiresAt }
func (v Verified) Bytes() []byte        { return append([]byte(nil), v.raw...) }

// Equal compares authenticated content independently of JSON formatting.
func (v Verified) Equal(other Verified) bool {
	return v.Version() > 0 && v.manifest.Signature == other.manifest.Signature
}

func payload(m Manifest) ([]byte, error) {
	m.Signature = ""
	b, err := json.Marshal(m)
	return append([]byte(domain), b...), err
}

func validate(m Manifest, now time.Time) error {
	if m.Format != 1 || m.Version == 0 || len(m.Relays) == 0 || len(m.Relays) > 8 {
		return errors.New("invalid relay directory format or size")
	}
	if m.PublishedAt.After(now.Add(5*time.Minute)) || !m.ExpiresAt.After(m.PublishedAt) || m.ExpiresAt.Sub(m.PublishedAt) > 90*24*time.Hour {
		return errors.New("invalid relay directory validity")
	}
	seen := map[string]bool{}
	for _, raw := range m.Relays {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || net.ParseIP(u.Hostname()) == nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || len(raw) > 256 {
			return errors.New("relays must be unique HTTPS public-IP URLs")
		}
		ip := net.ParseIP(u.Hostname())
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return errors.New("relay directory cannot target local networks")
		}
		port := u.Port()
		if port == "" {
			port = "443"
		}
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return errors.New("invalid relay port")
		}
		canonical := net.JoinHostPort(ip.String(), strconv.Itoa(number))
		if seen[canonical] {
			return errors.New("duplicate relay address")
		}
		seen[canonical] = true
	}
	return nil
}

// Verify verifies authenticity. Previously installed records can preserve the
// last working relay set during an outage; callers must use Fresh before
// applying or presenting a new update. Version checks prevent network rollback.
func Verify(raw []byte, public string, now time.Time) (Verified, error) {
	if len(raw) > MaxSize {
		return Verified{}, errors.New("relay directory too large")
	}
	var m Manifest
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&m); err != nil {
		return Verified{}, err
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Verified{}, errors.New("expected one relay directory")
	}
	if err := validate(m, now); err != nil {
		return Verified{}, err
	}
	pk, err := base64.StdEncoding.DecodeString(public)
	if err != nil || len(pk) != ed25519.PublicKeySize {
		return Verified{}, errors.New("relay directory signing key is not configured")
	}
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return Verified{}, errors.New("invalid relay directory signature")
	}
	b, err := payload(m)
	if err != nil {
		return Verified{}, err
	}
	if !ed25519.Verify(pk, b, sig) {
		return Verified{}, errors.New("relay directory signature rejected")
	}
	return Verified{manifest: m, raw: append([]byte(nil), raw...)}, nil
}

func (v Verified) Fresh(now time.Time) bool {
	return v.manifest.Version > 0 && now.Before(v.manifest.ExpiresAt)
}

func Sign(m Manifest, private ed25519.PrivateKey) ([]byte, error) {
	if len(private) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid relay directory private key")
	}
	if err := validate(m, time.Now()); err != nil {
		return nil, err
	}
	b, err := payload(m)
	if err != nil {
		return nil, err
	}
	m.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(private, b))
	return json.MarshalIndent(m, "", "  ")
}
