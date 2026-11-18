package p2p

import (
	"testing"
	"time"

	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"
)

func TestDetectedRelayCountUsesObservedRelays(t *testing.T) {
	first, err := netaddr.ParseRelayURL("https://first.example")
	if err != nil {
		t.Fatal(err)
	}
	second, err := netaddr.ParseRelayURL("https://second.example")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		report iroh.NetReport
		home   string
		want   int
	}{
		{name: "no observations"},
		{name: "connected without probe support", home: first.String(), want: 1},
		{name: "probe only", report: iroh.NetReport{RelayLatencies: map[netaddr.RelayURL]time.Duration{first: time.Millisecond}}, want: 1},
		{name: "home is not counted twice", report: iroh.NetReport{RelayLatencies: map[netaddr.RelayURL]time.Duration{first: time.Millisecond}}, home: first.String(), want: 1},
		{name: "different connected home", report: iroh.NetReport{RelayLatencies: map[netaddr.RelayURL]time.Duration{first: time.Millisecond}}, home: second.String(), want: 2},
		{name: "multiple probes", report: iroh.NetReport{RelayLatencies: map[netaddr.RelayURL]time.Duration{first: time.Millisecond, second: time.Millisecond}}, want: 2},
		{name: "zero relay ignored", report: iroh.NetReport{RelayLatencies: map[netaddr.RelayURL]time.Duration{{}: time.Millisecond}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := detectedRelayCount(test.report, test.home); got != test.want {
				t.Fatalf("detected %d relays, want %d", got, test.want)
			}
		})
	}
}
