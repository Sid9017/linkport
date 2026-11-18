package cliinstall

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdatedPathPreservesOtherProgramsAndReplacesOnlyManagedVersion(t *testing.T) {
	root := `C:\Users\test\AppData\Local\Linkport\cli`
	old := filepath.Join(root, "versions", strings.Repeat("a", 64))
	next := filepath.Join(root, "versions", strings.Repeat("b", 64))
	unrelated := filepath.Join(root, "versions", "user-tools")
	original := `C:\Windows;` + old + ";" + unrelated + `;C:\Tools`
	want := `C:\Windows;` + unrelated + `;C:\Tools;` + next
	if got := updatedPath(original, root, next); got != want {
		t.Fatalf("PATH = %q, want %q", got, want)
	}
	if got := updatedPath(want, root, next); got != want {
		t.Fatalf("not idempotent: %q", got)
	}
}
