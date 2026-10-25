package packages

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// PutObject stores a verified content object under its SHA-256 identity. The
// caller may provide the expected hash; the store never trusts a filename.
func (s Store) PutObject(expected string, r io.Reader) (string, error) {
	if s.Root == "" {
		return "", fmt.Errorf("package store root is required")
	}
	b, err := io.ReadAll(io.LimitReader(r, 512<<20))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	actual := hex.EncodeToString(sum[:])
	if expected != "" && !strings.EqualFold(strings.TrimPrefix(expected, "sha256:"), actual) {
		return "", fmt.Errorf("object hash mismatch: got %s", actual)
	}
	dir := filepath.Join(s.Root, "objects", actual[:2])
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, actual)
	if _, err := os.Stat(dst); err == nil {
		return dst, nil
	}
	tmp, err := os.CreateTemp(dir, ".object-")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err = tmp.Write(b); err != nil {
		tmp.Close()
		return "", err
	}
	if err = tmp.Close(); err != nil {
		return "", err
	}
	if err = os.Rename(tmpName, dst); err != nil {
		return "", err
	}
	return dst, nil
}

func (s Store) ObjectPath(hash string) (string, error) {
	hash = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(hash)), "sha256:")
	if len(hash) != 64 {
		return "", fmt.Errorf("invalid sha256 object hash")
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return "", fmt.Errorf("invalid sha256 object hash: %w", err)
	}
	return filepath.Join(s.Root, "objects", hash[:2], hash), nil
}
func (s Store) HasObject(hash string) bool {
	p, err := s.ObjectPath(hash)
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}
func (s Store) GetObject(hash string) ([]byte, error) {
	p, err := s.ObjectPath(hash)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}
