package transfer

import (
	"fmt"
	"sort"
)

type Mode string

const (
	Full         Mode = "full"
	ManifestOnly Mode = "manifest_only"
	Smart        Mode = "smart"
)

type Manifest struct {
	Plugin  string   `json:"plugin,omitempty"`
	Version string   `json:"version,omitempty"`
	Objects []string `json:"objects"`
	Sources []string `json:"sources,omitempty"`
}
type Peer interface{ HasObject(string) bool }
type Plan struct {
	Mode     Mode
	Manifest Manifest
	Missing  []string
	Transfer []string
}

// BuildPlan reveals only hashes requested by the caller. It does not exchange
// a peer's complete inventory, which keeps discovery need-based.
func BuildPlan(mode Mode, manifest Manifest, peer Peer) (Plan, error) {
	if mode != Full && mode != ManifestOnly && mode != Smart {
		return Plan{}, fmt.Errorf("unsupported transfer mode %q", mode)
	}
	if manifest.Plugin == "" && mode != ManifestOnly {
		return Plan{}, fmt.Errorf("manifest plugin is required")
	}
	objects := append([]string(nil), manifest.Objects...)
	sort.Strings(objects)
	manifest.Objects = objects
	p := Plan{Mode: mode, Manifest: manifest}
	if mode == ManifestOnly {
		return p, nil
	}
	for _, object := range objects {
		if mode == Full || peer == nil || !peer.HasObject(object) {
			p.Transfer = append(p.Transfer, object)
		}
	}
	p.Missing = append([]string(nil), p.Transfer...)
	return p, nil
}

type Inventory struct{ objects map[string]bool }

func NewInventory(objects []string) *Inventory {
	i := &Inventory{objects: map[string]bool{}}
	for _, h := range objects {
		i.objects[h] = true
	}
	return i
}
func (i *Inventory) HasObject(hash string) bool { return i != nil && i.objects[hash] }
