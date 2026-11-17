package engine

import (
	"context"
	"github.com/Sid9017/linkport/internal/serialport"
	"github.com/Sid9017/linkport/internal/vport"
	"testing"
)

func testEngine(t *testing.T) *Engine {
	t.Helper()
	engine, err := New(context.Background(), Options{
		Directory: t.TempDir(), DeviceDirectory: t.TempDir(), DisableNetworkListener: true,
		Enumerator: serialport.EnumerateFunc(func(context.Context) ([]serialport.Port, error) {
			return []serialport.Port{{Path: "/dev/real-enumerated-port"}}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(engine.Close)
	return engine
}

func TestLocalInventoryRequiresExplicitPersistentSharing(t *testing.T) {
	engine := testEngine(t)
	if err := engine.manager.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := engine.State()
	if len(state.LocalPorts) != 1 || state.LocalPorts[0].Shared || len(state.SharedPorts) != 0 || len(state.Sessions) != 0 {
		t.Fatalf("unexpected initial state: %+v", state)
	}
	identity := state.LocalPorts[0].ID
	if err := engine.SetShared(context.Background(), identity, true); err != nil {
		t.Fatal(err)
	}
	if state := engine.State(); !state.LocalPorts[0].Shared || len(state.SharedPorts) != 1 {
		t.Fatalf("opt-in not applied: %+v", state)
	}
	if err := engine.SetShared(context.Background(), identity, false); err != nil {
		t.Fatal(err)
	}
	if len(engine.State().SharedPorts) != 0 {
		t.Fatal("unshare did not remove advertised port")
	}
}

func TestConnectRejectsDeviceOutsidePairedCatalog(t *testing.T) {
	engine := testEngine(t)
	if _, err := engine.Connect(context.Background(), "injected-host", "port", 115200, vport.Options{}); err == nil {
		t.Fatal("accepted target outside paired devices")
	}
}
