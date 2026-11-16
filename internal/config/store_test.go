package config

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/Sid9017/linkport/internal/serialport"
)

func TestFileStoreRoundTripAndPrivatePermissions(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "private", "shares.json")
	store := NewFileStore(path)
	empty, err := store.Load(ctx)
	if err != nil || empty.Version != Version || len(empty.Shares) != 0 {
		t.Fatalf("initial load: %#v, %v", empty, err)
	}
	state := State{Version: Version, Shares: []Choice{{
		Port: serialport.Normalize(serialport.Port{
			Path: "/dev/ttyUSB0", IsUSB: true, VID: "0403", PID: "6001", SerialNumber: "unique",
		}),
		NeedsConfirmation: true,
	}}}
	if err := store.Save(ctx, state); err != nil {
		t.Fatal(err)
	}
	got, err := NewFileStore(path).Load(ctx)
	if err != nil || !reflect.DeepEqual(got, state) {
		t.Fatalf("round trip: %#v, %v", got, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("config permissions: %o", info.Mode().Perm())
		}
	}
	state.Shares = []Choice{}
	if err := store.Save(ctx, state); err != nil {
		t.Fatal(err)
	}
	got, err = store.Load(ctx)
	if err != nil || len(got.Shares) != 0 {
		t.Fatalf("unshare round trip: %#v, %v", got, err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v, %v", entries, err)
	}
}

func TestInvalidConfigDoesNotSilentlyResetChoices(t *testing.T) {
	cases := []string{
		`{`,
		`{"version":2,"shares":[]}`,
		`{"version":1,"shares":[{"port":{"id":"wrong","path":"/dev/tty0"}}]}`,
		`{"version":1,"shares":[]} {}`,
		`{"version":1,"shares":[{"port":{"id":"path:tty0","path":"tty0"}},{"port":{"id":"path:tty0","path":"tty0"}}]}`,
	}
	for _, contents := range cases {
		t.Run(contents, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "shares.json")
			if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := NewFileStore(path).Load(context.Background()); err == nil {
				t.Fatal("invalid config accepted")
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != contents {
				t.Fatalf("load changed file: %q, %v", got, err)
			}
		})
	}
}

func TestFailedSavePreservesPreviousFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shares.json")
	store := NewFileStore(path)
	initial := State{Version: Version, Shares: []Choice{}}
	if err := store.Save(context.Background(), initial); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), State{Version: 999}); err == nil {
		t.Fatal("invalid state saved")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Save(ctx, initial); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled save: %v", err)
	}
	got, err := store.Load(context.Background())
	if err != nil || !reflect.DeepEqual(got, initial) {
		t.Fatalf("previous file changed: %#v, %v", got, err)
	}
}

func TestSaveWhilePreviousConfigIsStillOpen(t *testing.T) {
	for _, test := range []struct {
		name string
		path string
	}{
		{"unicode", filepath.Join("共享设置", "shares.json")},
		{"long-path", filepath.Join(
			strings.Repeat("long-config-path-", 6),
			strings.Repeat("long-config-path-", 6),
			strings.Repeat("long-config-path-", 6), "shares.json")},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), test.path)
			initial := State{Version: Version, Shares: []Choice{{
				Port: serialport.Normalize(serialport.Port{Path: "COM7"}),
			}}}
			if err := NewFileStore(path).Save(context.Background(), initial); err != nil {
				t.Fatal(err)
			}
			reader, err := openConfigFile(path)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()

			next := State{Version: Version, Shares: []Choice{}}
			if err := NewFileStore(path).Save(context.Background(), next); err != nil {
				t.Fatalf("replace config while the watcher holds its read handle: %v", err)
			}
			var previous State
			if err := json.NewDecoder(reader).Decode(&previous); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(previous, initial) {
				t.Fatalf("open reader lost its previous snapshot: %#v", previous)
			}
			current, err := NewFileStore(path).Load(context.Background())
			if err != nil || !reflect.DeepEqual(current, next) {
				t.Fatalf("new reader did not observe replacement: %#v, %v", current, err)
			}
		})
	}
}

func TestConcurrentFileStoreReadsAndReplacements(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shares.json")
	writer := NewFileStore(path)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	empty := State{Version: Version, Shares: []Choice{}}
	shared := State{Version: Version, Shares: []Choice{{
		Port: serialport.Normalize(serialport.Port{Path: "COM7"}),
	}}}
	if err := writer.Save(ctx, empty); err != nil {
		t.Fatal(err)
	}

	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			store := NewFileStore(path)
			for ctx.Err() == nil {
				state, err := store.Load(ctx)
				if ctx.Err() != nil {
					return
				}
				if err != nil {
					t.Errorf("concurrent reader: %v", err)
					return
				}
				if !reflect.DeepEqual(state, empty) && !reflect.DeepEqual(state, shared) {
					t.Errorf("reader observed a partial config: %#v", state)
					return
				}
			}
		})
	}
	defer func() {
		cancel()
		readers.Wait()
	}()
	for i := range 100 {
		state := empty
		if i%2 == 0 {
			state = shared
		}
		if err := writer.Save(ctx, state); err != nil {
			t.Fatalf("concurrent config replacement %d: %v", i, err)
		}
	}
}
