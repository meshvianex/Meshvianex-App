package packages

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type Activation struct {
	PackageID     string `json:"package_id"`
	Current       string `json:"current,omitempty"`
	LastKnownGood string `json:"last_known_good,omitempty"`
}
type Manager struct {
	Store Store
	mu    sync.Mutex
	state Activation
}

func NewManager(store Store) *Manager { return &Manager{Store: store} }
func (m *Manager) State() Activation  { m.mu.Lock(); defer m.mu.Unlock(); return m.state }
func (m *Manager) Activate(packageID string, selfTest func(string) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if packageID == "" {
		return fmt.Errorf("package id is required")
	}
	if m.state.Current != "" {
		m.state.LastKnownGood = m.state.Current
	}
	if selfTest != nil {
		if err := selfTest(packageID); err != nil {
			m.state.Current = m.state.LastKnownGood
			return fmt.Errorf("activation self-test failed: %w", err)
		}
	}
	m.state.Current = packageID
	return m.saveLocked()
}
func (m *Manager) Rollback() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.LastKnownGood == "" {
		return fmt.Errorf("no last-known-good package")
	}
	m.state.Current, m.state.LastKnownGood = m.state.LastKnownGood, m.state.Current
	return m.saveLocked()
}
func (m *Manager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, err := os.ReadFile(filepath.Join(m.Store.Root, "activation.json"))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, &m.state)
}
func (m *Manager) saveLocked() error {
	if m.Store.Root == "" {
		return fmt.Errorf("package store root is required")
	}
	if err := os.MkdirAll(m.Store.Root, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(m.Store.Root, "activation.json.tmp")
	if err = os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(m.Store.Root, "activation.json"))
}
