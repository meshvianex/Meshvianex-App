package acquisition

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"meshvianex/core/packages"
)

func TestAcquireResumesAndStoresContent(t *testing.T) {
	data := make([]byte, 1<<20)
	for i := range data {
		data[i] = byte(i % 251)
	}
	sourcePath := filepath.Join(t.TempDir(), "source.bin")
	if err := os.WriteFile(sourcePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	store := packages.Store{Root: t.TempDir()}
	d := Downloader{Store: store}
	got, err := d.Acquire(context.Background(), Options{ObjectHash: hex.EncodeToString(sum[:]), ChunkSize: 64 << 10, Concurrency: 4, StateDir: t.TempDir(), Sources: []Source{FileSource{Name: "local", Path: sourcePath}}})
	if err != nil {
		t.Fatal(err)
	}
	if !store.HasObject(hex.EncodeToString(sum[:])) {
		t.Fatalf("stored object missing: %s", got)
	}
	stored, err := store.GetObject(hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) != string(data) {
		t.Fatal("stored object differs from source")
	}
}
