//go:build !windows && (!darwin || !cgo)

package main

import "os"

func systemLocale() string {
	for _, key := range []string{"LC_ALL", "LANGUAGE", "LC_MESSAGES", "LANG"} {
		if locale := os.Getenv(key); locale != "" {
			return locale
		}
	}
	return ""
}
