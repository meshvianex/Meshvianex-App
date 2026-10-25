package storage

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type Namespace struct{ Root, PluginID, InstanceID string }

func (n Namespace) file(key string) (string, error) {
	if n.Root == "" || !validSegment(n.PluginID) || !validSegment(n.InstanceID) {
		return "", fmt.Errorf("storage namespace is incomplete")
	}
	if !validSegment(key) {
		return "", fmt.Errorf("invalid storage key")
	}
	return filepath.Join(n.Root, n.PluginID, n.InstanceID, key), nil
}
func (n Namespace) Put(key string, value []byte) error {
	p, err := n.file(key)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err = os.WriteFile(tmp, value, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
func (n Namespace) Get(key string) ([]byte, error) {
	p, err := n.file(key)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}
func (n Namespace) Delete(key string) error {
	p, err := n.file(key)
	if err != nil {
		return err
	}
	return os.Remove(p)
}
func (n Namespace) List() ([]string, error) {
	if n.Root == "" || !validSegment(n.PluginID) || !validSegment(n.InstanceID) {
		return nil, fmt.Errorf("storage namespace is incomplete")
	}
	entries, err := os.ReadDir(filepath.Join(n.Root, n.PluginID, n.InstanceID))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Type().IsRegular() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

func validSegment(value string) bool {
	return value != "" && value != "." && value != ".." && filepath.Base(value) == value
}

type SecretHandle string
type Vault struct{ root string }

func NewVault(root string) *Vault { return &Vault{root: root} }
func (v *Vault) New(value []byte) (SecretHandle, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	h := SecretHandle(hex.EncodeToString(b[:]))
	if err := os.MkdirAll(v.root, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(v.root, string(h)), value, 0o600); err != nil {
		return "", err
	}
	return h, nil
}
func (v *Vault) Use(h SecretHandle, fn func([]byte) error) error {
	if h == "" || filepath.Base(string(h)) != string(h) {
		return fmt.Errorf("invalid secret handle")
	}
	b, err := os.ReadFile(filepath.Join(v.root, string(h)))
	if err != nil {
		return err
	}
	return fn(append([]byte(nil), b...))
}
func (v *Vault) Delete(h SecretHandle) error {
	if h == "" || filepath.Base(string(h)) != string(h) {
		return fmt.Errorf("invalid secret handle")
	}
	return os.Remove(filepath.Join(v.root, string(h)))
}
