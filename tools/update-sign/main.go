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
	directory := flag.String("sign-dir", "", "sign Linkport CLI and desktop archives in this directory")
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
	archives, err := filepath.Glob(filepath.Join(directory, "linkport*.zip"))
	if err != nil {
		return err
	}
	tarballs, err := filepath.Glob(filepath.Join(directory, "linkport_*.tar.gz"))
	if err != nil {
		return err
	}
	archives = append(archives, tarballs...)
	if len(archives) == 0 {
		return fmt.Errorf("no Linkport archives to sign")
	}
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
		signature := ed25519.Sign(private, digest.Sum(nil))
		if err := os.WriteFile(archive+".sig", []byte(base64.StdEncoding.EncodeToString(signature)+"\n"), 0o644); err != nil {
			return err
		}
		fmt.Println(filepath.Base(archive) + ".sig")
	}
	return nil
}
