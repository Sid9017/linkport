//go:build darwin || linux

package cliinstall

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegisterNewShellAndUpgrade(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home with 'quotes' and $(false)")
	root := filepath.Join(home, "data", "cli")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(home, ".bash_profile")
	original := "# keep user settings\nexport EXISTING_SETTING=kept\n"
	if err := os.WriteFile(profile, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := Install(root, []byte("#!/bin/sh\nprintf 'first:%s:%s' \"$EXISTING_SETTING\" \"$1\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	zsh := filepath.Join(home, "zsh")
	config := filepath.Join(home, "config")
	for range 2 {
		if err := Register(root, first, home, zsh, config); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(profile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), original) || strings.Count(string(data), profileMarker) != 1 {
		t.Fatalf("modified/duplicated startup file: %s", data)
	}
	info, _ := os.Stat(profile)
	if info.Mode().Perm() != 0600 {
		t.Fatal("changed startup file permissions")
	}
	run := func(profile, want string) {
		t.Helper()
		command := exec.Command("/bin/sh", "-c", ". \"$1\"; linkport 'arg with spaces'", "test", profile)
		command.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
		got, err := command.CombinedOutput()
		if err != nil || string(got) != want {
			t.Fatalf("new shell CLI: %q, %v, want %q", got, err, want)
		}
	}
	run(profile, "first:kept:arg with spaces")
	run(filepath.Join(zsh, ".zshrc"), "first::arg with spaces")
	for _, path := range []string{filepath.Join(home, ".profile"), filepath.Join(home, ".bashrc"), filepath.Join(zsh, ".zprofile"), filepath.Join(config, "fish", "conf.d", "linkport.fish")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
	second, err := Install(root, []byte("#!/bin/sh\nprintf 'second:%s' \"$1\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := Register(root, second, home, zsh, config); err != nil {
		t.Fatal(err)
	}
	run(profile, "second:arg with spaces")
	// Updating the command leaves old immutable executables available to running processes.
	if _, err := os.Stat(first); err != nil {
		t.Fatal(err)
	}
}

func TestRegisterPreservesUnrelatedCommandsAndProfiles(t *testing.T) {
	for _, kind := range []string{"file", "symlink", "profile"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			root := filepath.Join(home, "cli")
			binary, err := Install(root, []byte("CLI"))
			if err != nil {
				t.Fatal(err)
			}
			command := filepath.Join(root, "bin", "linkport")
			if err := os.MkdirAll(filepath.Dir(command), 0700); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "file":
				err = os.WriteFile(command, []byte("unrelated"), 0755)
			case "symlink":
				err = os.Symlink("/unrelated/linkport", command)
			case "profile":
				err = os.WriteFile(filepath.Join(home, ".profile"), []byte(profileMarker+"\ncustom configuration\n"), 0644)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := Register(root, binary, home, "", ""); err == nil {
				t.Fatal("overwrote unrelated configuration")
			}
			if kind == "file" {
				if got, _ := os.ReadFile(command); string(got) != "unrelated" {
					t.Fatal("changed unrelated command")
				}
			}
			if kind == "symlink" {
				if got, _ := os.Readlink(command); got != "/unrelated/linkport" {
					t.Fatal("changed unrelated symlink")
				}
			}
		})
	}
}

func TestRegisterReportsUnwritableLayout(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "cli")
	binary, err := Install(root, []byte("CLI"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin"), []byte("unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Register(root, binary, home, "", ""); err == nil {
		t.Fatal("accepted a file as the command directory")
	}
	if err := appendSnippet(home, "snippet"); err == nil {
		t.Fatal("accepted a directory as a shell profile")
	}
	blocked := filepath.Join(home, "blocked")
	if err := os.WriteFile(blocked, []byte("unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := appendSnippet(filepath.Join(blocked, "profile"), "snippet"); err == nil {
		t.Fatal("accepted blocked parent")
	}
}

func TestRegisteredCommandWorksInInstalledShells(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "cli")
	binary, err := Install(root, []byte("#!/bin/sh\nprintf '%s' \"$1\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := Register(root, binary, home, "", ""); err != nil {
		t.Fatal(err)
	}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			path, err := exec.LookPath(shell)
			if err != nil {
				t.Skipf("shell not installed: %s", shell)
			}
			profile := filepath.Join(home, ".bashrc")
			script := `. "$HOME/.bashrc"; linkport 'argument with spaces'`
			if shell == "zsh" {
				profile = filepath.Join(home, ".zshrc")
				script = `. "$HOME/.zshrc"; linkport 'argument with spaces'`
			}
			if shell == "fish" {
				profile = filepath.Join(home, ".config", "fish", "conf.d", "linkport.fish")
				script = `source "$HOME/.config/fish/conf.d/linkport.fish"; linkport 'argument with spaces'`
			}
			if _, err := os.Stat(profile); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(path, "-c", script)
			command.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "ZDOTDIR=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config")}
			got, err := command.CombinedOutput()
			if err != nil || string(got) != "argument with spaces" {
				t.Fatalf("shell execution: %q, %v", got, err)
			}
		})
	}
}
