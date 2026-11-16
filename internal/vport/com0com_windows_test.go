package vport

import (
	"strings"
	"testing"
)

func TestWindowsRequiresBothCom0comEndpoints(t *testing.T) {
	for _, options := range []Options{{}, {Path: "COM121"}, {PairDevice: "COM122"}} {
		if port, err := Open(options); err == nil {
			port.Close()
			t.Fatalf("Open(%+v) accepted incomplete pair", options)
		}
	}
}

func TestWindowsRejectsInvalidCom0comDeviceName(t *testing.T) {
	if port, err := Open(Options{Path: "COM121", PairDevice: "invalid\x00device"}); err == nil {
		port.Close()
		t.Fatal("Open accepted a device name containing NUL")
	}
}

func TestWindowsReportsMissingCom0comDevice(t *testing.T) {
	port, err := Open(Options{Path: "COM121", PairDevice: "LINKPORT_NONEXISTENT_CI_DEVICE"})
	if err == nil {
		port.Close()
		t.Fatal("Open unexpectedly found a reserved nonexistent test device")
	}
	if !strings.Contains(err.Error(), "com0com") {
		t.Fatalf("missing driver guidance: %v", err)
	}
}
