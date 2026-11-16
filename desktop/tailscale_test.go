package main

import "testing"

func TestTrustedLoginURL(t *testing.T) {
	for _, test := range []struct {
		address string
		trusted bool
	}{
		{"https://login.tailscale.com/a/example", true},
		{"https://tailscale.com/a/example", true},
		{"http://login.tailscale.com/a/example", false},
		{"https://login.tailscale.com.attacker.test/a/example", false},
		{"https://attacker.test/a/example", false},
		{"https://user:pass@login.tailscale.com/a/example", false},
		{"https://login.tailscale.com/download", false},
	} {
		if got := trustedLoginURL(test.address); got != test.trusted {
			t.Errorf("%q: trusted=%v, want %v", test.address, got, test.trusted)
		}
	}
}

func TestLoginOutputOnlyOpensCompleteTrustedURLOnce(t *testing.T) {
	var opened []string
	output := &loginOutput{openURL: func(address string) error {
		opened = append(opened, address)
		return nil
	}}
	output.Write([]byte("To authenticate, visit:\nhttps://login.tail"))
	if len(opened) != 0 {
		t.Fatal("opened an incomplete URL")
	}
	output.Write([]byte("scale.com/a/example-token\nhttps://login.tailscale.com/a/example-token\n"))
	output.flush()
	if len(opened) != 1 || opened[0] != "https://login.tailscale.com/a/example-token" {
		t.Fatalf("unexpected URLs: %v", opened)
	}
}
