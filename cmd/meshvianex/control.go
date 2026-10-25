package main

import (
	"context"
	"fmt"
	"meshvianex/core/event"
	"meshvianex/core/graph"
	"meshvianex/core/health"
	"meshvianex/core/plugin"
	"meshvianex/core/realm"
	"meshvianex/core/resources"
	"sync"
)

type controlPlane struct {
	mu            sync.RWMutex
	reg           *plugin.Registry
	health        *health.Store
	bus           *event.Bus
	realms        map[string]*realm.Runtime
	graph         *graph.Manager
	forwarder     *graph.Forwarder
	resources     *resources.Store
	stateRoot     string
	refreshMu     sync.Mutex
	refreshLocks  map[string]*sync.Mutex
	refreshCancel context.CancelFunc
	refreshWG     sync.WaitGroup
	activity      []map[string]any
}

func newControlPlane(reg *plugin.Registry, hs *health.Store, bus *event.Bus, cfg *graph.Config, initial realm.Config) (*controlPlane, error) {
	return newControlPlaneWithStore(reg, hs, bus, cfg, initial, resources.NewStore(".meshvianex-state"))
}

func newControlPlaneWithStore(reg *plugin.Registry, hs *health.Store, bus *event.Bus, cfg *graph.Config, initial realm.Config, store *resources.Store) (*controlPlane, error) {
	var graphCfg graph.Config
	if cfg != nil {
		graphCfg = *cfg
	} else {
		graphCfg = chainGraphForRealms([]string{initial.ID}, map[string]realm.Config{initial.ID: initial}, "Meshvianex chain", 1)
	}
	gm, err := graph.NewManager(graphCfg, hs, bus)
	if err != nil {
		return nil, err
	}
	if store == nil {
		store = resources.NewStore(".meshvianex-state")
	}
	return &controlPlane{reg: reg, health: hs, bus: bus, realms: map[string]*realm.Runtime{}, graph: gm, forwarder: gm.NewForwarder(bus), resources: store, stateRoot: store.Root, refreshLocks: map[string]*sync.Mutex{}, activity: []map[string]any{{"message": "Control plane ready", "detail": "Runtime is ready for changes", "time": "now"}}}, nil
}

func (c *controlPlane) register(rt *realm.Runtime) {
	c.mu.Lock()
	c.realms[rt.ID()] = rt
	rt.SetReferenceResolver(c.resources)
	rt.SetForwarder(c.forwarder)
	_ = c.forwarder.RegisterRealm(rt.ID(), rt)
	c.mu.Unlock()
}

func (c *controlPlane) Snapshot() any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	subs, subErr := c.resources.ListSubscriptions()
	for i := range subs {
		subs[i] = resources.RedactSubscription(subs[i])
	}
	configs, configErr := c.resources.ListConfigs()
	redacted := make([]resources.Config, 0, len(configs))
	for _, item := range configs {
		redacted = append(redacted, resources.RedactConfig(item))
	}
	snapshot := map[string]any{"project": "Meshvianex", "status": "running", "realms": c.realmRowsLocked(), "plugins": c.pluginRowsLocked(), "graph": c.graph.Config(), "connections": c.connectionRowsLocked(), "connection_stats": c.connectionStatsLocked(), "health": c.health.Snapshot(), "subscriptions": subs, "configs": redacted, "activity": append([]map[string]any(nil), c.activity...)}
	if subErr != nil {
		snapshot["subscriptions_error"] = resources.RedactString(subErr.Error())
	}
	if configErr != nil {
		snapshot["configs_error"] = resources.RedactString(configErr.Error())
	}
	return snapshot
}

func (c *controlPlane) realm(id string) (*realm.Runtime, error) {
	c.mu.RLock()
	rt := c.realms[id]
	c.mu.RUnlock()
	if rt == nil {
		return nil, fmt.Errorf("unknown realm: %s", id)
	}
	return rt, nil
}
