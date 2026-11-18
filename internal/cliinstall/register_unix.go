//go:build darwin || linux

package cliinstall

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const profileMarker = "# Linkport desktop CLI"

// Register adds a stable command to the user's shell startup files. Paths are
// shell-quoted and appended to PATH, preserving commands installed elsewhere.
func Register(root, executable, home, zshDirectory, configDirectory string) error {
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		return err
	}
	command := filepath.Join(bin, "linkport")
	if target, err := os.Readlink(command); err == nil {
		if target != executable && !managedExecutable(root, target) {
			return fmt.Errorf("refusing to replace unrelated command: %s", command)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("refusing to replace CLI destination: %w", err)
	}
	temporary, err := os.CreateTemp(bin, ".linkport-*")
	if err != nil {
		return err
	}
	temporary.Close()
	os.Remove(temporary.Name())
	if err := os.Symlink(executable, temporary.Name()); err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if err := os.Rename(temporary.Name(), command); err != nil {
		return err
	}
	snippet := "\n" + profileMarker + "\ncase \":$PATH:\" in\n  *:" + shellQuote(bin) + ":*) ;;\n  *) export PATH=\"${PATH:+$PATH:}\"" + shellQuote(bin) + " ;;\nesac\n"
	if zshDirectory == "" {
		zshDirectory = home
	}
	profiles := []string{filepath.Join(home, ".profile"), filepath.Join(home, ".bashrc"), filepath.Join(zshDirectory, ".zprofile"), filepath.Join(zshDirectory, ".zshrc")}
	// Bash reads only the first existing login profile. Do not create a new
	// .bash_profile that would prevent an existing .bash_login/.profile loading.
	for _, name := range []string{".bash_profile", ".bash_login"} {
		path := filepath.Join(home, name)
		if _, err := os.Stat(path); err == nil {
			profiles = append(profiles, path)
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	for _, path := range profiles {
		if err := appendSnippet(path, snippet); err != nil {
			return err
		}
	}
	if configDirectory == "" {
		configDirectory = filepath.Join(home, ".config")
	}
	fish := filepath.Join(configDirectory, "fish", "conf.d", "linkport.fish")
	return appendSnippet(fish, "\n"+profileMarker+"\ncontains -- "+shellQuote(bin)+" $PATH; or set -gx PATH $PATH "+shellQuote(bin)+"\n")
}

func managedExecutable(root, target string) bool {
	relative, err := filepath.Rel(filepath.Join(root, "versions"), target)
	if err != nil {
		return false
	}
	parts := strings.Split(relative, string(filepath.Separator))
	if len(parts) != 2 || len(parts[0]) != 64 || parts[1] != "linkport" {
		return false
	}
	for _, c := range parts[0] {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

func appendSnippet(path, snippet string) error {
	contents, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if strings.Contains(string(contents), snippet) {
		return nil
	}
	if strings.Contains(string(contents), profileMarker) {
		return fmt.Errorf("existing CLI configuration needs manual repair: %s", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(snippet)
	return errors.Join(writeErr, file.Close())
}
