package version

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultVersionMatchesReleaseVersion(t *testing.T) {
	content, err := os.ReadFile("../../VERSION")
	if err != nil {
		t.Fatal(err)
	}
	if expected := strings.TrimSpace(string(content)); Version != expected {
		t.Fatalf("default Version = %q; VERSION = %q", Version, expected)
	}
	if Commit != "unknown" || Date != "unknown" {
		t.Fatalf("unlinked build claims metadata: commit=%q date=%q", Commit, Date)
	}
}

func TestStringIncludesInjectedMetadata(t *testing.T) {
	originalVersion, originalCommit, originalDate := Version, Commit, Date
	t.Cleanup(func() {
		Version, Commit, Date = originalVersion, originalCommit, originalDate
	})
	Version, Commit, Date = "0.0.1", "0123456789abcdef", "2026-11-16T00:00:00Z"
	expected := "0.0.1 (commit 0123456789abcdef, built 2026-11-16T00:00:00Z)"
	if actual := String(); actual != expected {
		t.Fatalf("String() = %q; want %q", actual, expected)
	}
}

func TestLinkerMetadata(t *testing.T) {
	const commit = "0123456789abcdef"
	const date = "2026-11-16T00:00:00Z"
	if os.Getenv("LINKPORT_TEST_LINKED_METADATA") == "1" {
		if Version != "0.0.2" || Commit != commit || Date != date {
			t.Fatalf("linker metadata = %q %q %q", Version, Commit, Date)
		}
		return
	}
	binary := filepath.Join(t.TempDir(), "version.test.exe")
	flags := "-X github.com/Sid9017/linkport/internal/version.Version=0.0.2" +
		" -X github.com/Sid9017/linkport/internal/version.Commit=" + commit +
		" -X github.com/Sid9017/linkport/internal/version.Date=" + date
	build := exec.Command("go", "test", "-mod=readonly", "-c", "-ldflags", flags, "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("compile linked test: %v\n%s", err, output)
	}
	check := exec.Command(binary, "-test.run=^TestLinkerMetadata$")
	check.Env = append(os.Environ(), "LINKPORT_TEST_LINKED_METADATA=1")
	if output, err := check.CombinedOutput(); err != nil {
		t.Fatalf("run linked test: %v\n%s", err, output)
	}
}
