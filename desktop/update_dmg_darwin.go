package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Wails v3.1.0 accepts a directory at DownloadedPath and validates it against
// the final .app target. Preserve that path while replacing the verified DMG
// with its validated application bundle before allowing Restart.
func prepareNativeUpdate(ctx context.Context, path, version string) (err error) {
	if !strings.HasSuffix(path, ".dmg") {
		return nil
	}
	validate := func(bundle string) error {
		data, err := exec.CommandContext(ctx, "/usr/bin/plutil", "-convert", "json", "-o", "-", filepath.Join(bundle, "Contents/Info.plist")).Output()
		if err != nil {
			return fmt.Errorf("read update bundle: %w", err)
		}
		var info map[string]any
		if err := json.Unmarshal(data, &info); err != nil {
			return err
		}
		if info["CFBundleIdentifier"] != "com.linkport.desktop" || info["CFBundleExecutable"] != "linkport-desktop" || info["CFBundleShortVersionString"] != strings.SplitN(version, "-", 2)[0] {
			return fmt.Errorf("update bundle identity or version rejected")
		}
		return exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", bundle).Run()
	}
	if stat, e := os.Stat(path); e == nil && stat.IsDir() {
		return validate(path)
	}
	mount, err := os.MkdirTemp("", "linkport-update-mount-")
	if err != nil {
		return err
	}
	mounted := false
	defer func() {
		if mounted {
			cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if e := exec.CommandContext(cleanup, "/usr/bin/hdiutil", "detach", mount).Run(); e != nil {
				if err == nil {
					err = fmt.Errorf("detach update image: %w", e)
				}
				return
			}
		}
		_ = os.Remove(mount)
	}()
	if err = exec.CommandContext(ctx, "/usr/bin/hdiutil", "attach", "-readonly", "-nobrowse", "-mountpoint", mount, path).Run(); err != nil {
		return err
	}
	mounted = true
	bundle := filepath.Join(mount, "Linkport.app")
	if err = validate(bundle); err != nil {
		return err
	}
	work, err := os.MkdirTemp(filepath.Dir(path), "bundle-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	staged := filepath.Join(work, "Linkport.app")
	if err = exec.CommandContext(ctx, "/usr/bin/ditto", bundle, staged).Run(); err != nil {
		return err
	}
	if err = validate(staged); err != nil {
		return err
	}
	cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	err = exec.CommandContext(cleanup, "/usr/bin/hdiutil", "detach", mount).Run()
	cancel()
	if err != nil {
		return err
	}
	mounted = false
	backup := filepath.Join(work, "verified.dmg")
	if err = os.Rename(path, backup); err != nil {
		return err
	}
	if err = os.Rename(staged, path); err != nil {
		_ = os.Rename(backup, path)
		return err
	}
	return nil
}
