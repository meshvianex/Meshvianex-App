package packages

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type Ticket struct {
	Plugin    string   `json:"plugin"`
	Version   string   `json:"version"`
	Hash      string   `json:"hash"`
	Publisher string   `json:"publisher,omitempty"`
	Sources   []string `json:"sources,omitempty"`
}

func EncodeTicket(t Ticket) ([]byte, error) {
	if t.Plugin == "" || t.Version == "" || t.Hash == "" {
		return nil, fmt.Errorf("ticket requires plugin, version and hash")
	}
	return json.MarshalIndent(t, "", "  ")
}
func DecodeTicket(b []byte) (Ticket, error) {
	var t Ticket
	if err := json.Unmarshal(b, &t); err != nil {
		return t, err
	}
	if t.Plugin == "" || t.Version == "" || t.Hash == "" {
		return t, fmt.Errorf("ticket requires plugin, version and hash")
	}
	return t, nil
}

type Repository struct {
	Root    string
	mu      sync.RWMutex
	Tickets map[string]Ticket
}

func NewRepository(root string) *Repository {
	return &Repository{Root: root, Tickets: map[string]Ticket{}}
}
func (r *Repository) AddTicket(t Ticket) error {
	if _, err := EncodeTicket(t); err != nil {
		return err
	}
	r.mu.Lock()
	r.Tickets[t.Hash] = t
	r.mu.Unlock()
	return nil
}
func (r *Repository) Missing(hashes []string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0)
	for _, h := range hashes {
		if _, ok := r.Tickets[h]; !ok {
			out = append(out, h)
		}
	}
	return out
}
func (r *Repository) Save() error {
	if r.Root == "" {
		return fmt.Errorf("repository root is required")
	}
	r.mu.RLock()
	b, err := json.MarshalIndent(r.Tickets, "", "  ")
	r.mu.RUnlock()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(r.Root, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(r.Root, "index.json.tmp")
	if err = os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(r.Root, "index.json"))
}
func (r *Repository) Load() error {
	b, err := os.ReadFile(filepath.Join(r.Root, "index.json"))
	if err != nil {
		return err
	}
	var t map[string]Ticket
	if err = json.Unmarshal(b, &t); err != nil {
		return err
	}
	r.mu.Lock()
	r.Tickets = t
	r.mu.Unlock()
	return nil
}
