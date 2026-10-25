package packages

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestImportSignedPackage(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("demo payload")
	sum := sha256.Sum256(payload)
	manifest := PackageManifest{ID: "io.test.demo", Version: "0.1.0", PublisherKeyID: "test", Files: map[string]string{"payload.bin": hex.EncodeToString(sum[:])}}
	mb, _ := json.Marshal(manifest)
	pkg := filepath.Join(t.TempDir(), "demo.mvplug")
	f, err := os.Create(pkg)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	write := func(name string, b []byte) {
		w, e := zw.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write(b); e != nil {
			t.Fatal(e)
		}
	}
	write("manifest.json", mb)
	write("signature.ed25519", []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, mb))))
	write("payload.bin", payload)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	dst, err := (Store{Root: t.TempDir(), Trust: TrustStore{"test": pub}}).Import(pkg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatal(err)
	}
}
