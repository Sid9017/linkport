package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sid9017/linkport/docs/design"
	"github.com/Sid9017/linkport/internal/engine"
	"github.com/Sid9017/linkport/internal/serialport"
)

func TestNativeStateReportsActualEnumerationWithoutDemoSessions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	live, err := engine.New(ctx, engine.Options{
		Directory: t.TempDir(), DeviceDirectory: t.TempDir(),
		DisableNetworkListener: true,
		Enumerator: serialport.EnumerateFunc(func(context.Context) ([]serialport.Port, error) {
			return []serialport.Port{{Path: "/dev/test-enumeration-only"}}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	go live.Run()
	deadline := time.After(4 * time.Second)
	for live.State().UpdatedAt.IsZero() || len(live.State().LocalPorts) == 0 {
		select {
		case <-deadline:
			t.Fatal("native live source did not initialize")
		case <-time.After(10 * time.Millisecond):
		}
	}
	api := &nativeAPI{token: "test-token", engine: live}
	request := httptest.NewRequest(http.MethodGet, "/native/v1/state", nil)
	request.Header.Set("X-Linkport-Token", "test-token")
	response := httptest.NewRecorder()
	api.handler().ServeHTTP(response, request)
	var state engine.State
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || len(state.LocalPorts) != 1 || state.LocalPorts[0].Path != "/dev/test-enumeration-only" ||
		len(state.Sessions) != 0 || len(state.Devices) != 0 || state.Network.State != "Disabled" {
		t.Fatalf("native state is not backed by actual providers: %+v", state)
	}
}

func TestLiveDesktopBrowser(t *testing.T) {
	script := os.Getenv("LINKPORT_UI_CHECK")
	if script == "" {
		t.Skip("set LINKPORT_UI_CHECK to a browser verification script")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	options := engine.Options{
		Directory: t.TempDir(), DeviceDirectory: t.TempDir(), DisableNetworkListener: true,
	}
	if os.Getenv("LINKPORT_UI_FIXTURES") == "1" {
		options.Enumerator = serialport.EnumerateFunc(func(context.Context) ([]serialport.Port, error) {
			return []serialport.Port{{Path: "/dev/frontend-fixture", Product: "Frontend fixture"}}, nil
		})
	}
	live, err := engine.New(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	go live.Run()
	deadline := time.After(15 * time.Second)
	for live.State().UpdatedAt.IsZero() {
		select {
		case <-deadline:
			t.Fatal("real local data source did not initialize")
		case <-time.After(10 * time.Millisecond):
		}
	}
	preferences, err := newPreferenceStore(filepath.Join(t.TempDir(), "desktop.json"))
	if err != nil {
		t.Fatal(err)
	}
	api := &nativeAPI{
		ctx: ctx, token: "browser-test-only", engine: live, preferences: preferences,
	}
	mux := http.NewServeMux()
	mux.Handle("/native/v1/", api.handler())
	mux.Handle("/", design.DesktopHandlerWithToken(api.token))
	server := httptest.NewServer(mux)
	defer server.Close()
	// Stop the driver before the outer Go timeout so partial browser diagnostics
	// remain available, even when a renderer or a protocol operation stalls.
	driverContext, stopDriver := context.WithTimeout(ctx, 4*time.Minute)
	defer stopDriver()
	command := exec.CommandContext(driverContext, "node", script, server.URL)
	command.WaitDelay = 5 * time.Second
	output, err := command.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatalf("browser verification: %v", err)
	}
}
