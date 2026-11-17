package p2p

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sid9017/linkport/internal/relaycatalog"

	"github.com/tmc/go-iroh/netaddr"
)

// TestPublicRelayDeployment is an explicit operator acceptance check. Ordinary
// test runs never contact a public relay or use existing device identities.
func TestPublicRelayDeployment(t *testing.T) {
	address := os.Getenv("LINKPORT_TEST_PUBLIC_RELAY")
	if address == "" {
		t.Skip("set LINKPORT_TEST_PUBLIC_RELAY to explicitly test an operator relay")
	}
	if !strings.HasPrefix(address, "https://") {
		t.Fatal("public acceptance requires verified HTTPS")
	}
	address = strings.TrimRight(address, "/")
	relayURL, err := netaddr.ParseRelayURL(address)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	targets := []struct{ name, url string }{
		{"health", address + "/healthz"},
		{"ping", address + "/ping"},
	}
	if relaycatalog.SourceURL != "" {
		targets = append(targets, struct{ name, url string }{"directory", relaycatalog.SourceURL})
	}
	for _, target := range targets {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.url, nil)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		response, err := client.Do(req)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, relaycatalog.MaxSize+1))
		response.Body.Close()
		cancel()
		if readErr != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("%s: status=%d error=%v", target.name, response.StatusCode, readErr)
		}
		if response.TLS == nil || len(response.TLS.VerifiedChains) == 0 {
			t.Fatal("HTTPS did not verify a trusted certificate chain")
		}
		if target.name == "health" && string(body) != "ok\n" {
			t.Fatalf("unexpected health response %q", body)
		}
		if target.name == "directory" {
			directory, err := relaycatalog.Verify(body, relaycatalog.PublicKey, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if !directory.Fresh(time.Now()) {
				t.Fatal("published relay directory expired")
			}
			found := false
			for _, candidate := range directory.URLs() {
				if candidate == address {
					found = true
				}
			}
			if !found {
				t.Fatal("published directory does not include tested relay")
			}
		}
	}
	runRelayPairingScenario(t, relayURL)
}
