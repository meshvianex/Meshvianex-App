package main

import (
	"context"
	"encoding/json"
	"fmt"
	"meshvianex/core/graph"
	"meshvianex/core/realm"
)

func (c *controlPlane) controlGraphAction(ctx context.Context, action, id string, payload json.RawMessage) (any, error) {
	switch action {
	case "get_graph":
		return c.graph.Config(), nil
	case "set_chain":
		var req struct {
			RealmIDs []string `json:"realm_ids"`
		}
		if err := decodePayload(payload, &req); err != nil {
			return nil, err
		}
		if err := c.setChain(req.RealmIDs); err != nil {
			return nil, err
		}
		c.record("Updated Realm chain", fmt.Sprintf("%d Realm nodes", len(req.RealmIDs)), "ok")
		return c.graph.Config(), nil
	case "set_graph":
		var cfg graph.Config
		if err := decodePayload(payload, &cfg); err != nil {
			return nil, err
		}
		c.mu.RLock()
		configs := make(map[string]realm.Config, len(c.realms))
		for realmID, rt := range c.realms {
			configs[realmID] = rt.Config()
		}
		c.mu.RUnlock()
		cfg = ensureDNSGraphRoutes(cfg, configs)
		if err := c.validateGraphRealms(cfg); err != nil {
			return nil, err
		}
		tx := c.graph.Begin()
		tx.SetConfig(cfg)
		if err := c.graph.Commit(tx); err != nil {
			return nil, err
		}
		if err := c.persistState(); err != nil {
			_ = c.graph.Rollback()
			return nil, err
		}
		c.record("Updated Realm topology", fmt.Sprintf("%d edges", len(cfg.Edges)), "ok")
		return c.graph.Config(), nil
	case "rollback_graph":
		previous := c.graph.Config()
		if err := c.graph.Rollback(); err != nil {
			return nil, err
		}
		if err := c.persistState(); err != nil {
			tx := c.graph.Begin()
			tx.SetConfig(previous)
			_ = c.graph.Commit(tx)
			return nil, err
		}
		c.record("Rolled back graph", "Last revision restored", "ok")
		return c.graph.Config(), nil
	case "save_changes":
		if err := c.persistState(); err != nil {
			return nil, err
		}
		c.record("Applied changes", "Runtime configuration committed", "ok")
		return map[string]any{"saved": true}, nil
	default:
		return nil, fmt.Errorf("unsupported control action: %s", action)
	}
}
