//go:build bundledcli

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/Sid9017/linkport/internal/cliinstall"

	_ "embed"
)

//go:embed assets/cli/linkport.bin
var cliPayload []byte

func installBundledCLI() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	root := filepath.Join(home, ".local", "share", "linkport", "cli")
	if runtime.GOOS == "windows" {
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			return fmt.Errorf("LOCALAPPDATA is unavailable")
		}
		root = filepath.Join(local, "Linkport", "cli")
	}
	executable, err := cliinstall.Install(root, cliPayload)
	if err != nil {
		return err
	}
	return cliinstall.Register(root, executable, home, os.Getenv("ZDOTDIR"), os.Getenv("XDG_CONFIG_HOME"))
}
