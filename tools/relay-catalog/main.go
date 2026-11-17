// relay-catalog signs a public directory with the publisher's offline key.
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Sid9017/linkport/internal/relaycatalog"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("relay-catalog", flag.ContinueOnError)
	flags.SetOutput(output)
	keyFile := flags.String("key", "", "publisher private key file, kept outside the repository")
	version := flags.Uint64("version", 0, "monotonically increasing directory version")
	urls := flags.String("relays", "", "comma-separated HTTPS IP relay URLs")
	destination := flags.String("output", "", "public signed directory output file")
	verify := flags.String("verify", "", "verify a signed directory with the embedded operator public key")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	if *verify != "" {
		if *keyFile != "" || *version != 0 || *urls != "" || *destination != "" {
			return errors.New("verify cannot be combined with signing options")
		}
		file, err := os.Open(*verify)
		if err != nil {
			return err
		}
		defer file.Close()
		raw, err := io.ReadAll(io.LimitReader(file, relaycatalog.MaxSize+1))
		if err != nil {
			return err
		}
		now := time.Now()
		directory, err := relaycatalog.Verify(raw, relaycatalog.PublicKey, now)
		if err != nil {
			return err
		}
		if !directory.Fresh(now) {
			return errors.New("relay directory expired")
		}
		fmt.Fprintf(output, "Verified relay directory: version=%d relays=%d expires=%s\n",
			directory.Version(), len(directory.URLs()), directory.ExpiresAt().Format(time.RFC3339))
		return nil
	}
	if *keyFile == "" || *destination == "" || *urls == "" || *version == 0 {
		return errors.New("key, positive version, relays and output are required")
	}
	secret, err := os.ReadFile(*keyFile)
	if err == nil {
		secret, err = base64.StdEncoding.DecodeString(strings.TrimSpace(string(secret)))
	}
	if err != nil || len(secret) != ed25519.PrivateKeySize {
		return errors.New("cannot read publisher signing key")
	}
	now := time.Now().UTC().Truncate(time.Second)
	m := relaycatalog.Manifest{Format: 1, Version: *version, PublishedAt: now, ExpiresAt: now.Add(89 * 24 * time.Hour), Relays: strings.Split(*urls, ",")}
	data, err := relaycatalog.Sign(m, ed25519.PrivateKey(secret))
	if err == nil {
		err = os.WriteFile(*destination, append(data, '\n'), 0644)
	}
	if err != nil {
		return err
	}
	fmt.Fprintln(output, "Signed public relay directory:", *destination)
	return nil
}
