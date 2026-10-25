package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"meshvianex/core/graph"
	"meshvianex/core/realm"
)

const applicationStateVersion = 1

type applicationState struct {
	Version int            `json:"version"`
	Graph   graph.Config   `json:"graph"`
	Realms  []realm.Config `json:"realms"`
}

func loadApplicationState(root string, initial realm.Config, initialGraph *graph.Config) ([]realm.Config, graph.Config, error) {
	graphCfg := chainGraphForRealms([]string{initial.ID}, map[string]realm.Config{initial.ID: initial}, "Meshvianex chain", 1)
	if initialGraph != nil {
		graphCfg = *initialGraph
	}
	path := filepath.Join(root, "application.json")
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		// The aggregate is the normal startup path. These per-object stores are
		// also a compatibility fallback for state written before the aggregate
		// file existed.
		if initial.ID != "" {
			if persisted, loadErr := (realm.Store{Root: root}).Load(initial.ID); loadErr == nil {
				initial = persisted
			} else if !errors.Is(loadErr, os.ErrNotExist) {
				return nil, graph.Config{}, fmt.Errorf("load Realm %s: %w", initial.ID, loadErr)
			}
		}
		if initialGraph != nil && initialGraph.GraphID != "" {
			if persisted, loadErr := (graph.Store{Root: root}).Load(initialGraph.GraphID); loadErr == nil {
				graphCfg = persisted
			} else if !errors.Is(loadErr, os.ErrNotExist) {
				return nil, graph.Config{}, fmt.Errorf("load graph %s: %w", initialGraph.GraphID, loadErr)
			}
		}
		realms := []realm.Config{initial}
		return realms, ensureDNSGraphRoutes(graphCfg, realmConfigMap(realms)), nil
	}
	if err != nil {
		return nil, graph.Config{}, fmt.Errorf("read application state: %w", err)
	}
	var state applicationState
	if err := json.Unmarshal(b, &state); err != nil {
		return nil, graph.Config{}, fmt.Errorf("decode application state: %w", err)
	}
	if state.Version != applicationStateVersion {
		return nil, graph.Config{}, fmt.Errorf("unsupported application state version %d", state.Version)
	}
	if state.Graph.GraphID == "" {
		return nil, graph.Config{}, fmt.Errorf("application state graph id is required")
	}
	for _, cfg := range state.Realms {
		if cfg.ID == "" || cfg.Name == "" {
			return nil, graph.Config{}, fmt.Errorf("application state contains an invalid Realm")
		}
	}
	return state.Realms, ensureDNSGraphRoutes(state.Graph, realmConfigMap(state.Realms)), nil
}

func realmConfigMap(configs []realm.Config) map[string]realm.Config {
	out := make(map[string]realm.Config, len(configs))
	for _, cfg := range configs {
		if cfg.ID != "" {
			out[cfg.ID] = cfg
		}
	}
	return out
}

func (c *controlPlane) persistState() error {
	c.mu.RLock()
	realms := make([]*realm.Runtime, 0, len(c.realms))
	for _, rt := range c.realms {
		realms = append(realms, rt)
	}
	c.mu.RUnlock()
	sort.Slice(realms, func(i, j int) bool { return realms[i].ID() < realms[j].ID() })
	configs := make([]realm.Config, 0, len(realms))
	for _, rt := range realms {
		configs = append(configs, rt.Config())
	}
	graphCfg := c.graph.Config()
	if c.stateRoot == "" {
		return fmt.Errorf("application state root is required")
	}
	if err := (graph.Store{Root: c.stateRoot}).Save(graphCfg); err != nil {
		return fmt.Errorf("persist graph: %w", err)
	}
	if err := (graph.Store{Root: c.stateRoot}).SaveLastKnownGood(graphCfg); err != nil {
		return fmt.Errorf("persist graph last-known-good: %w", err)
	}
	for _, cfg := range configs {
		store := realm.Store{Root: c.stateRoot}
		if err := store.Save(cfg); err != nil {
			return fmt.Errorf("persist Realm %s: %w", cfg.ID, err)
		}
		if err := store.SaveLastKnownGood(cfg); err != nil {
			return fmt.Errorf("persist Realm %s last-known-good: %w", cfg.ID, err)
		}
	}
	state := applicationState{Version: applicationStateVersion, Graph: graphCfg, Realms: configs}
	if err := writeApplicationJSON(filepath.Join(c.stateRoot, "application.json"), state); err != nil {
		return fmt.Errorf("persist application state: %w", err)
	}
	return nil
}

func (c *controlPlane) snapshotRealmConfigs() map[string]realm.Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	snapshots := make(map[string]realm.Config, len(c.realms))
	for id, rt := range c.realms {
		snapshots[id] = rt.Config()
	}
	return snapshots
}

func cloneGraphConfig(cfg graph.Config) graph.Config {
	copy := cfg
	copy.Nodes = append([]graph.Node(nil), cfg.Nodes...)
	for index := range copy.Nodes {
		copy.Nodes[index].Ports = append([]graph.Port(nil), cfg.Nodes[index].Ports...)
	}
	copy.Edges = append([]graph.Edge(nil), cfg.Edges...)
	for index := range copy.Edges {
		if cfg.Edges[index].When.Tags != nil {
			copy.Edges[index].When.Tags = make(map[string]string, len(cfg.Edges[index].When.Tags))
			for key, value := range cfg.Edges[index].When.Tags {
				copy.Edges[index].When.Tags[key] = value
			}
		}
	}
	return copy
}

// restoreRealmConfigs is used when the durable commit fails after one or more
// Runtime.Update calls. It keeps the in-memory state aligned with the last
// known-good snapshot; each update is itself lifecycle-safe.
func (c *controlPlane) restoreRealmConfigs(ctx context.Context, snapshots map[string]realm.Config) error {
	c.mu.RLock()
	realms := make(map[string]*realm.Runtime, len(c.realms))
	for id, rt := range c.realms {
		realms[id] = rt
	}
	c.mu.RUnlock()
	var first error
	for id, cfg := range snapshots {
		rt := realms[id]
		if rt == nil || reflect.DeepEqual(rt.Config(), cfg) {
			continue
		}
		if err := rt.Update(ctx, cfg); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func writeApplicationJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".meshvianex-state-*")
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
