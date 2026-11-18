//go:build bundledcli && (darwin || linux)

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sid9017/linkport/internal/version"
)

func TestBundledCLIInNewTerminal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ZDOTDIR", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if err := installBundledCLI(); err != nil {
		t.Fatal(err)
	}
	if err := installBundledCLI(); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("/bin/sh", "-c", `. "$HOME/.profile"; linkport version`)
	command.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), version.Version) {
		t.Fatalf("bundled CLI version: %q, %v", output, err)
	}
	info, err := os.Stat(filepath.Join(home, ".local", "share", "linkport", "cli", "bin", "linkport"))
	if err != nil || info.Mode()&0111 == 0 {
		t.Fatalf("CLI is not executable: %v", err)
	}
}
