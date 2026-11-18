//go:build !windows && (!darwin || !cgo)

package main

import "testing"

func TestLocaleEnvironmentPrecedence(t *testing.T) {
	for _, key := range []string{"LC_ALL", "LANGUAGE", "LC_MESSAGES", "LANG"} {
		t.Setenv(key, "")
	}
	if got := systemLocale(); got != "" {
		t.Fatalf("unexpected locale without environment settings: %q", got)
	}
	for _, test := range []struct{ key, value string }{
		{"LANG", "en_US.UTF-8"},
		{"LC_MESSAGES", "fr_FR.UTF-8"},
		{"LANGUAGE", "zh_CN:en_US"},
		{"LC_ALL", "C"},
	} {
		t.Setenv(test.key, test.value)
		if got := systemLocale(); got != test.value {
			t.Fatalf("%s did not take precedence: %q", test.key, got)
		}
	}
}
