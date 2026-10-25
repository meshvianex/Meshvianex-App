package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"flag"
	"fmt"
	"os"

	corepkg "meshvianex/core/packages"
)

func main() {
	in := flag.String("in", "", "path to .mvplug")
	store := flag.String("store", ".meshvianex/packages", "content-addressed package store")
	keyID := flag.String("key-id", "", "trusted publisher key id")
	publicKey := flag.String("public-key", "", "base64 Ed25519 public key")
	flag.Parse()
	if *in == "" || *keyID == "" || *publicKey == "" {
		fmt.Fprintln(os.Stderr, "-in, -key-id, and -public-key are required")
		os.Exit(2)
	}
	raw, err := base64.StdEncoding.DecodeString(*publicKey)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		fmt.Fprintln(os.Stderr, "invalid Ed25519 public key")
		os.Exit(2)
	}
	s := corepkg.Store{Root: *store, Trust: corepkg.TrustStore{*keyID: ed25519.PublicKey(raw)}}
	dst, err := s.Import(*in)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(dst)
}
