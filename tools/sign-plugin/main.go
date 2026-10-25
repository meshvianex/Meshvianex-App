package main

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	corepkg "meshvianex/core/packages"
)

func main() {
	in := flag.String("in", "", "directory to package")
	out := flag.String("out", "plugin.mvplug", "output package")
	id := flag.String("id", "io.example.plugin", "plugin id")
	version := flag.String("version", "0.1.0", "plugin version")
	keyID := flag.String("key-id", "local-dev", "publisher key id")
	flag.Parse()
	if *in == "" {
		fmt.Fprintln(os.Stderr, "-in is required")
		os.Exit(2)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	must(err)
	files := map[string]string{}
	data := map[string][]byte{}
	must(filepath.Walk(*in, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(*in, path)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		sum := sha256.Sum256(b)
		files[rel] = hex.EncodeToString(sum[:])
		data[rel] = b
		return nil
	}))
	m := corepkg.PackageManifest{ID: *id, Version: *version, PublisherKeyID: *keyID, Files: files}
	mb, err := json.MarshalIndent(m, "", "  ")
	must(err)
	sig := ed25519.Sign(priv, mb)
	f, err := os.Create(*out)
	must(err)
	zw := zip.NewWriter(f)
	write := func(name string, b []byte) { w, err := zw.Create(name); must(err); _, err = w.Write(b); must(err) }
	write("manifest.json", mb)
	write("signature.ed25519", []byte(base64.StdEncoding.EncodeToString(sig)))
	names := make([]string, 0, len(data))
	for n := range data {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		write(n, data[n])
	}
	must(zw.Close())
	must(f.Close())
	fmt.Printf("wrote %s\npublisher_key_id=%s\npublic_key_base64=%s\n", *out, *keyID, base64.StdEncoding.EncodeToString(pub))
	_ = io.EOF
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
