// relay-catalog signs a public directory with the publisher's offline key.
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Sid9017/linkport/internal/relaycatalog"
)

func main() {
	keyFile := flag.String("key", "", "publisher private key file, kept outside the repository")
	version := flag.Uint64("version", 0, "monotonically increasing directory version")
	urls := flag.String("relays", "", "comma-separated HTTPS IP relay URLs")
	output := flag.String("output", "", "public signed directory output file")
	flag.Parse()
	if *keyFile == "" || *output == "" || *urls == "" {
		fmt.Fprintln(os.Stderr, "key, version, relays and output are required")
		os.Exit(1)
	}
	secret, err := os.ReadFile(*keyFile)
	if err == nil {
		secret, err = base64.StdEncoding.DecodeString(strings.TrimSpace(string(secret)))
	}
	if err != nil || len(secret) != ed25519.PrivateKeySize {
		fmt.Fprintln(os.Stderr, "cannot read publisher signing key")
		os.Exit(1)
	}
	now := time.Now().UTC().Truncate(time.Second)
	m := relaycatalog.Manifest{Format: 1, Version: *version, PublishedAt: now, ExpiresAt: now.Add(89 * 24 * time.Hour), Relays: strings.Split(*urls, ",")}
	data, err := relaycatalog.Sign(m, ed25519.PrivateKey(secret))
	if err == nil {
		err = os.WriteFile(*output, append(data, '\n'), 0644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("Signed public relay directory:", *output)
}
