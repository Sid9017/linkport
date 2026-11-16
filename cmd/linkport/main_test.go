package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sid9017/linkport/internal/config"
	"github.com/Sid9017/linkport/internal/serialport"
	"github.com/Sid9017/linkport/internal/share"
	"github.com/Sid9017/linkport/internal/version"
)

func TestVersionAndHelp(t *testing.T) {
	for _, command := range []string{"version", "help"} {
		var output bytes.Buffer
		if err := run(context.Background(), []string{command}, &output, &output); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), version.Version) {
			t.Fatalf("%s did not report version: %s", command, output.String())
		}
	}
}

func TestDevelopmentListenersRejectPublicBinding(t *testing.T) {
	for _, address := range []string{"0.0.0.0:7700", ":7700", "[::]:7700", "100.65.23.13:7700", "localhost:7700"} {
		err := serve(context.Background(), address, http.NotFoundHandler(), &bytes.Buffer{}, "test")
		if err == nil || !strings.Contains(err.Error(), "loopback") {
			t.Fatalf("address %q should be rejected, got %v", address, err)
		}
	}
}

func TestLoopbackMiddlewareUsesActualPeerAddress(t *testing.T) {
	handler := loopbackOnly(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	for _, test := range []struct {
		address string
		want    int
	}{
		{"127.0.0.1:10000", http.StatusNoContent},
		{"[::1]:10000", http.StatusNoContent},
		{"100.100.10.2:10000", http.StatusForbidden},
		{"192.168.0.2:10000", http.StatusForbidden},
		{"invalid", http.StatusForbidden},
	} {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.RemoteAddr = test.address
		request.Header.Set("X-Forwarded-For", "127.0.0.1")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.want {
			t.Errorf("%s: got %d, want %d", test.address, response.Code, test.want)
		}
	}
}

func TestResolveOfflineSharedPort(t *testing.T) {
	ctx := context.Background()
	store := config.NewFileStore(filepath.Join(t.TempDir(), "shares.json"))
	port := serialport.Normalize(serialport.Port{Path: "/dev/example", IsUSB: true, VID: "10c4", PID: "ea60", SerialNumber: "board-A"})
	if err := store.Save(ctx, config.State{Version: config.Version, Shares: []config.Choice{{Port: port}}}); err != nil {
		t.Fatal(err)
	}
	manager, err := share.NewManager(ctx, serialport.EnumerateFunc(func(context.Context) ([]serialport.Port, error) {
		return nil, nil
	}), store, share.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{port.Path, port.ID} {
		identity, err := resolveIdentity(ctx, manager, store, value)
		if err != nil || identity != port.ID {
			t.Fatalf("resolve %s: %s, %v", value, identity, err)
		}
	}
	if _, err := resolveIdentity(ctx, manager, store, "/dev/not-shared"); err == nil {
		t.Fatal("resolved an unknown device")
	}
}
