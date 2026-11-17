package vport

import (
	"strings"
	"testing"
)

func TestWindowsRequiresBothCom0comEndpoints(test *testing.T) {
	for _, options := range []Options{{}, {Path: "COM121"}, {PairDevice: "COM122"}} {
		if port, err := Open(options); err == nil {
			port.Close()
			test.Fatalf("Open(%+v) accepted incomplete pair", options)
		}
	}
}

func TestWindowsRejectsInvalidCom0comDeviceName(test *testing.T) {
	if port, err := Open(Options{Path: "COM121", PairDevice: "invalid\x00device"}); err == nil {
		port.Close()
		test.Fatal("Open accepted a device name containing NUL")
	}
}

func TestWindowsReportsMissingCom0comDevice(test *testing.T) {
	port, err := Open(Options{Path: "COM121", PairDevice: "LINKPORT_NONEXISTENT_CI_DEVICE"})
	if err == nil {
		port.Close()
		test.Fatal("Open unexpectedly found a reserved nonexistent test device")
	}
	if !strings.Contains(err.Error(), "com0com") {
		test.Fatalf("missing driver guidance: %v", err)
	}
}
