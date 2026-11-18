package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	generate := flag.String("generate", "", "write a NEW private signing key to this file")
	directory := flag.String("sign-dir", "", "write signed desktop checksum metadata in this directory")
	flag.Parse()
	if err := run(*generate, *directory); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(generate, directory string) error {
	if (generate == "") == (directory == "") {
		return fmt.Errorf("choose exactly one of -generate or -sign-dir")
	}
	if generate != "" {
		public, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		file, err := os.OpenFile(generate, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		_, writeErr := file.WriteString(base64.StdEncoding.EncodeToString(private) + "\n")
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
		fmt.Println(base64.StdEncoding.EncodeToString(public))
		return nil
	}
	private, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("LINKPORT_UPDATE_PRIVATE_KEY")))
	if err != nil {
		return fmt.Errorf("invalid signing key")
	}
	if len(private) == ed25519.SeedSize {
		private = ed25519.NewKeyFromSeed(private)
	}
	if len(private) != ed25519.PrivateKeySize || !bytes.Equal(private, ed25519.NewKeyFromSeed(private[:ed25519.SeedSize])) {
		return fmt.Errorf("invalid signing key")
	}
	public := ed25519.PrivateKey(private).Public().(ed25519.PublicKey)
	expected := strings.TrimSpace(os.Getenv("LINKPORT_UPDATE_PUBLIC_KEY"))
	if expected != "" && expected != base64.StdEncoding.EncodeToString(public) {
		return fmt.Errorf("signing private key does not match the embedded public key")
	}
	archives, err := filepath.Glob(filepath.Join(directory, "linkport-desktop_*.zip"))
	if err != nil {
		return err
	}
	if len(archives) == 0 {
		return fmt.Errorf("no Linkport desktop archives to sign")
	}
	var metadata strings.Builder
	for _, archive := range archives {
		file, err := os.Open(archive)
		if err != nil {
			return err
		}
		digest := sha256.New()
		_, readErr := io.Copy(digest, file)
		closeErr := file.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		sum := digest.Sum(nil)
		signature := ed25519.Sign(private, sum)
		name := filepath.Base(archive)
		fmt.Fprintf(&metadata, "%x  %s\n# ed25519 %s %s\n",
			sum, name, base64.StdEncoding.EncodeToString(signature), name)
	}
	if err := os.WriteFile(filepath.Join(directory, "desktop-checksums.txt"), []byte(metadata.String()), 0o644); err != nil {
		return err
	}
	fmt.Println("desktop-checksums.txt")
	return nil
}
