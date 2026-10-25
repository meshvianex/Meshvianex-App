package realm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Store struct{ Root string }

func (s Store) path(id, name string) string { return filepath.Join(s.Root, "realms", id, name+".json") }
func (s Store) Save(cfg Config) error {
	if s.Root == "" || cfg.ID == "" {
		return fmt.Errorf("realm store root and config id are required")
	}
	return writeJSON(s.path(cfg.ID, "current"), cfg)
}
func (s Store) SaveLastKnownGood(cfg Config) error {
	if s.Root == "" || cfg.ID == "" {
		return fmt.Errorf("realm store root and config id are required")
	}
	return writeJSON(s.path(cfg.ID, "lkg"), cfg)
}
func (s Store) Load(id string) (Config, error) {
	var cfg Config
	if id == "" {
		return cfg, fmt.Errorf("realm id is required")
	}
	b, err := os.ReadFile(s.path(id, "current"))
	if err != nil {
		return cfg, err
	}
	err = json.Unmarshal(b, &cfg)
	return cfg, err
}
func (s Store) LoadLastKnownGood(id string) (Config, error) {
	var cfg Config
	b, err := os.ReadFile(s.path(id, "lkg"))
	if err != nil {
		return cfg, err
	}
	err = json.Unmarshal(b, &cfg)
	return cfg, err
}
func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err = os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
