package serialport

import (
	"context"
	"errors"
	"testing"
)

func TestUSBIdentitySurvivesPathChange(t *testing.T) {
	first := Normalize(Port{Path: "/dev/ttyUSB0", IsUSB: true, VID: "0x403", PID: "6001", SerialNumber: "A: /B"})
	second := Normalize(Port{Path: "/dev/ttyUSB8", IsUSB: true, VID: "0403", PID: "0x6001", SerialNumber: "A: /B"})
	if first.ID != second.ID || first.VID != "0403" {
		t.Fatalf("USB identity changed: %#v %#v", first, second)
	}
	second.SerialNumber = "different"
	if Identity(second) == first.ID {
		t.Fatal("different serial numbers have the same identity")
	}
	second.SerialNumber = first.SerialNumber
	second.PID = "6002"
	if Identity(second) == first.ID {
		t.Fatal("different USB products have the same identity")
	}
}

func TestIncompleteUSBIdentityFallsBackToPath(t *testing.T) {
	cases := []Port{
		{Path: "/dev/tty0"},
		{Path: "/dev/tty0", IsUSB: true, VID: "0403", PID: "6001"},
		{Path: "/dev/tty0", IsUSB: true, VID: "invalid", PID: "6001", SerialNumber: "A"},
		{Path: "/dev/tty0", IsUSB: true, VID: "0403", SerialNumber: "A"},
	}
	for _, port := range cases {
		first := Identity(port)
		port.Path = "/dev/tty1"
		if Identity(port) == first {
			t.Fatalf("fallback identity ignored path: %#v", port)
		}
	}
}

func TestSystemEnumerationHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewEnumerator().Enumerate(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context canceled", err)
	}
}
