//go:build !windows

package config

import "os"

func openConfigFile(path string) (*os.File, error) {
	return os.Open(path)
}

func replaceConfigFile(oldPath, newPath string) error {
	return os.Rename(oldPath, newPath)
}
