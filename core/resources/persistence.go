package resources

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func (s *Store) path(name string) string {
	if s.Root == "" {
		return ""
	}
	return filepath.Join(s.Root, name)
}

func readSubscriptions(path string) ([]Subscription, error) {
	var out []Subscription
	if err := readJSON(path, &out); err != nil {
		return nil, err
	}
	return out, nil
}
func readConfigs(path string) ([]Config, error) {
	var out []Config
	if err := readJSON(path, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func readJSON(path string, target any) error {
	if path == "" {
		return fmt.Errorf("resource store root is required")
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, target)
}

func writeJSON(path string, value any) error {
	if path == "" {
		return fmt.Errorf("resource store root is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".meshvianex-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err = tmp.Chmod(0o600); err == nil {
		_, err = tmp.Write(b)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
