//go:build !bundledcli

package main

// Source-only runs do not install commands or edit the developer's environment.
func installBundledCLI() error { return nil }
