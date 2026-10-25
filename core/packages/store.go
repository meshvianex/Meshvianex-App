package packages

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type PackageManifest struct {
	ID             string            `json:"id"`
	Version        string            `json:"version"`
	PublisherKeyID string            `json:"publisher_key_id"`
	Files          map[string]string `json:"files"`
}
type TrustStore map[string]ed25519.PublicKey
type Store struct {
	Root  string
	Trust TrustStore
}

func (s Store) Import(path string) (string, error) {
	if s.Root == "" {
		return "", fmt.Errorf("package store root is required")
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", err
	}
	defer zr.Close()
	var manifestBytes, sigBytes []byte
	fileData := map[string][]byte{}
	var totalRead int64
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if f.FileInfo().Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symlink entries are not allowed")
		}
		if _, exists := fileData[f.Name]; exists || f.Name == "manifest.json" && len(manifestBytes) > 0 || f.Name == "signature.ed25519" && len(sigBytes) > 0 {
			return "", fmt.Errorf("duplicate package entry %q", f.Name)
		}
		clean := filepath.Clean(f.Name)
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			return "", fmt.Errorf("unsafe package path %q", f.Name)
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		b, err := io.ReadAll(io.LimitReader(rc, 64<<20))
		rc.Close()
		if err != nil {
			return "", err
		}
		totalRead += int64(len(b))
		if totalRead > 512<<20 {
			return "", fmt.Errorf("package exceeds size limit")
		}
		switch f.Name {
		case "manifest.json":
			manifestBytes = b
		case "signature.ed25519":
			sigBytes = b
		default:
			fileData[f.Name] = b
		}
	}
	if len(manifestBytes) == 0 || len(sigBytes) == 0 {
		return "", fmt.Errorf("package requires manifest.json and signature.ed25519")
	}
	var m PackageManifest
	if err := json.Unmarshal(manifestBytes, &m); err != nil {
		return "", err
	}
	if m.ID == "" || m.Version == "" || m.PublisherKeyID == "" {
		return "", fmt.Errorf("package manifest requires id, version and publisher_key_id")
	}
	pub, ok := s.Trust[m.PublisherKeyID]
	if !ok {
		return "", fmt.Errorf("untrusted publisher key %q", m.PublisherKeyID)
	}
	if len(pub) != ed25519.PublicKeySize {
		return "", fmt.Errorf("invalid trusted publisher key")
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigBytes)))
	if err != nil {
		return "", fmt.Errorf("invalid signature encoding: %w", err)
	}
	if !ed25519.Verify(pub, manifestBytes, sig) {
		return "", fmt.Errorf("manifest signature verification failed")
	}
	for name, want := range m.Files {
		b, ok := fileData[name]
		if !ok {
			return "", fmt.Errorf("missing file %s", name)
		}
		sum := sha256.Sum256(b)
		got := hex.EncodeToString(sum[:])
		if !strings.EqualFold(got, want) {
			return "", fmt.Errorf("hash mismatch for %s", name)
		}
	}
	for name := range fileData {
		if _, declared := m.Files[name]; !declared {
			return "", fmt.Errorf("package contains undeclared file %s", name)
		}
	}
	for name, want := range m.Files {
		if _, err := s.PutObject(want, bytes.NewReader(fileData[name])); err != nil {
			return "", err
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	id := hex.EncodeToString(sum[:])
	dir := filepath.Join(s.Root, "sha256", id[:2])
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, id+".mvplug")
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", err
	}
	return dst, nil
}
