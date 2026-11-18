package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/updater"
)

const maxNativePackageSize = 512 << 20

type boundedPackageWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *boundedPackageWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, errors.New("update exceeds declared package size")
	}
	n, err := w.writer.Write(p)
	w.remaining -= int64(n)
	return n, err
}

// Debian packages install into root-owned paths. Stage in the user's cache and
// hand installation to the OS only after an explicit click; never self-swap /usr/bin.
func (controller *updateController) downloadDeb(ctx context.Context, release *updater.Release) {
	controller.mu.Lock()
	controller.nativeState = "downloading"
	controller.mu.Unlock()
	path, err := downloadNativePackage(ctx, controller.provider, release, func(written, total int64) {
		controller.mu.Lock()
		controller.status.Progress = float64(written) / float64(total)
		controller.mu.Unlock()
	})
	if err == nil {
		err = validateDeb(ctx, path, release.Version, runtime.GOARCH)
	}
	controller.mu.Lock()
	if err == nil {
		controller.packagePath = path
		controller.nativeState = "package-ready"
	} else {
		controller.nativeState = ""
	}
	controller.mu.Unlock()
	if err != nil && path != "" {
		_ = os.RemoveAll(filepath.Dir(path))
	}
	controller.recordError(updater.StageDownload, err)
}

func downloadNativePackage(ctx context.Context, provider *signedProvider, release *updater.Release, progress func(int64, int64)) (result string, err error) {
	if release.Artifact.Size <= 0 || release.Artifact.Size > maxNativePackageSize {
		return "", errors.New("invalid update package size")
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	root := filepath.Join(cache, "linkport", "updates")
	if err = os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(root, "verified-")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()
	// Never accept an arbitrary path even if a caller bypasses provider.Check.
	name := release.Artifact.Filename
	if filepath.Base(name) != name || strings.ContainsAny(name, "/\\") {
		return "", errors.New("invalid package filename")
	}
	path := filepath.Join(dir, name)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	writer := &boundedPackageWriter{writer: io.MultiWriter(file, hash), remaining: release.Artifact.Size}
	err = provider.Download(ctx, release, writer, func(written, total int64) { progress(written, release.Artifact.Size) })
	closeErr := file.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	v := release.Verification
	digest := hash.Sum(nil)
	if writer.remaining != 0 || v == nil || v.DigestAlgo != "sha256" || v.SignatureAlgo != "ed25519" || !bytes.Equal(digest, v.Digest) || len(provider.key) != ed25519.PublicKeySize || !ed25519.Verify(provider.key, digest, v.Signature) {
		return "", errUpdateVerification
	}
	return path, nil
}

func validateDeb(ctx context.Context, path, version, arch string) error {
	data, err := exec.CommandContext(ctx, "dpkg-deb", "--show", "--showformat=${Package}\n${Version}\n${Architecture}\n", path).Output()
	if err != nil {
		return fmt.Errorf("inspect Debian update package: %w", err)
	}
	if string(data) != "linkport\n"+strings.Replace(version, "-", "~", 1)+"\n"+arch+"\n" {
		return errors.New("Debian update identity, version or architecture rejected")
	}
	return nil
}

func (controller *updateController) openDeb(path string) error {
	ctx, cancel := context.WithTimeout(controller.ctx, 30*time.Second)
	defer cancel()
	// Recheck the exact bytes immediately before passing the file to the installer.
	controller.mu.Lock()
	pending := controller.pending
	controller.mu.Unlock()
	if pending == nil {
		return errors.New("no pending Debian update")
	}
	file, err := os.Open(path)
	if err == nil {
		hash := sha256.New()
		_, err = io.Copy(hash, io.LimitReader(file, maxNativePackageSize+1))
		_ = file.Close()
		if err == nil && !bytes.Equal(hash.Sum(nil), pending.Verification.Digest) {
			err = errUpdateVerification
		}
	}
	if err == nil {
		err = validateDeb(ctx, path, pending.Version, runtime.GOARCH)
	}
	if err == nil {
		err = exec.CommandContext(ctx, "xdg-open", path).Run()
	}
	if err != nil {
		controller.recordError(updater.StageInstall, err)
		return err
	}
	controller.mu.Lock()
	controller.clearErrorLocked()
	controller.nativeState = "installer-opened"
	controller.mu.Unlock()
	return nil
}
