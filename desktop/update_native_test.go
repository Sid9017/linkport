package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/updater"
)

func TestNativeArtifactPlatformBinding(t *testing.T) {
	formats := map[string]string{"darwin": "dmg", "windows": "exe", "linux": "deb"}
	for platform, format := range formats {
		for _, arch := range []string{"amd64", "arm64"} {
			request := updater.CheckRequest{Platform: platform, Arch: arch}
			for _, extension := range []string{"zip", "dmg", "exe", "deb"} {
				release := &updater.Release{Version: "0.0.7", Artifact: updater.Artifact{Filename: "linkport-desktop_0.0.7_" + platform + "_" + arch + "." + extension, Filetype: extension}}
				if got := validUpdateArtifact(release, request); got != (extension == format || extension == "zip") {
					t.Fatalf("%s %s %s: %v", platform, arch, extension, got)
				}
				release.Artifact.Filename = "../" + release.Artifact.Filename
				if validUpdateArtifact(release, request) {
					t.Fatal("accepted foreign filename")
				}
			}
		}
	}
}

type packageProvider struct {
	updater.Provider
	body []byte
	err  error
}

func (p packageProvider) Download(_ context.Context, _ *updater.Release, w io.Writer, progress func(int64, int64)) error {
	if p.err != nil {
		return p.err
	}
	n, err := w.Write(p.body)
	progress(int64(n), int64(len(p.body)))
	return err
}

func TestNativeDownloadVerificationAndCleanup(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
	// macOS uses HOME/Library/Caches; setting HOME isolates that path as well.
	t.Setenv("HOME", t.TempDir())
	key, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("Debian fixture bytes")
	digest := sha256.Sum256(body)
	for _, mode := range []string{"valid", "tampered", "short", "overflow", "cancelled", "signature", "path"} {
		t.Run(mode, func(t *testing.T) {
			release := &updater.Release{Artifact: updater.Artifact{Filename: "linkport-desktop_0.0.7_linux_arm64.deb", Size: int64(len(body))}, Verification: &updater.Verification{DigestAlgo: "sha256", Digest: digest[:], SignatureAlgo: "ed25519", Signature: ed25519.Sign(private, digest[:])}}
			source := packageProvider{body: bytes.Clone(body)}
			switch mode {
			case "tampered":
				source.body[0] ^= 1
			case "short":
				source.body = source.body[:len(body)-1]
			case "overflow":
				source.body = append(source.body, 1)
			case "cancelled":
				source.err = context.Canceled
			case "signature":
				release.Verification.Signature = make([]byte, 64)
			case "path":
				release.Artifact.Filename = "../escape.deb"
			}
			provider := &signedProvider{Provider: source, key: key}
			path, err := downloadNativePackage(t.Context(), provider, release, func(int64, int64) {})
			if mode == "valid" {
				if err != nil {
					t.Fatal(err)
				}
				got, e := os.ReadFile(path)
				if e != nil || !bytes.Equal(got, body) {
					t.Fatal("download changed")
				}
				_ = os.RemoveAll(filepath.Dir(path))
				return
			}
			if err == nil || path != "" {
				t.Fatalf("accepted %s: %s %v", mode, path, err)
			}
			if mode == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			cache, _ := os.UserCacheDir()
			entries, _ := os.ReadDir(filepath.Join(cache, "linkport", "updates"))
			if len(entries) != 0 {
				t.Fatal("failed download left staging files")
			}
		})
	}
}

func TestSignedNativePlatformChecks(t *testing.T) {
	key, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("native release"))
	base, _ := url.Parse("https://updates.example")
	for platform, format := range map[string]string{"darwin": "dmg", "windows": "exe", "linux": "deb"} {
		for _, arch := range []string{"amd64", "arm64"} {
			name := "linkport-desktop_0.0.7_" + platform + "_" + arch + "." + format
			release := &updater.Release{Version: "0.0.7", Channel: "stable", Artifact: updater.Artifact{Filename: name, Filetype: format}, Verification: &updater.Verification{DigestAlgo: "sha256", Digest: digest[:], SignatureAlgo: "ed25519", Signature: ed25519.Sign(private, digest[:])}, Metadata: map[string]any{"endpoint.artifact.url": "https://updates.example/downloads/v0.0.7/" + name}}
			p := &signedProvider{Provider: checkPackageProvider{release: release}, key: key, baseURL: base, channel: "stable"}
			if _, err := p.Check(t.Context(), updater.CheckRequest{Platform: platform, Arch: arch}); err != nil {
				t.Fatal(err)
			}
			wrong := "amd64"
			if arch == wrong {
				wrong = "arm64"
			}
			if _, err := p.Check(t.Context(), updater.CheckRequest{Platform: platform, Arch: wrong}); err == nil {
				t.Fatal("accepted another architecture")
			}
		}
	}
}

type checkPackageProvider struct {
	updater.Provider
	release *updater.Release
}

func (p checkPackageProvider) Check(context.Context, updater.CheckRequest) (*updater.Release, error) {
	return p.release, nil
}
