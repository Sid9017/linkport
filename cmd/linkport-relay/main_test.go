package main

import (
	"context"
	"io"
	"testing"
)

func TestRejectUnsafeConfiguration(t *testing.T) {
	for _, args := range [][]string{{"--listen", ":3340"}, {"--listen", "0.0.0.0:3340"}, {"--listen", "localhost:3340"}, {"--max-connections", "0"}, {"--client-bytes-per-second", "0"}, {"--tls-cert", "missing"}, {"unexpected"}} {
		if err := run(context.Background(), args, io.Discard); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
