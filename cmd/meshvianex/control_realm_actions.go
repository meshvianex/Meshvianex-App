package main

import (
	"context"
	"encoding/json"
	"fmt"
	"meshvianex/core/netx"
	"meshvianex/core/realm"
)

func (c *controlPlane) controlRealmAction(ctx context.Context, action, id string, payload json.RawMessage) (any, error) {
	switch action {
	case "start_realm":
		rt, err := c.realm(id)
		if err != nil {
			return nil, err
		}
		if err = rt.Start(ctx); err != nil {
			return nil, err
		}
		c.record("Started "+rt.Name(), "Realm is active", "ok")
		return rt.Status(), nil
	case "stop_realm":
		rt, err := c.realm(id)
		if err != nil {
			return nil, err
		}
		if err = rt.Stop(ctx); err != nil {
			return nil, err
		}
		c.record("Stopped "+rt.Name(), "Realm is inactive", "ok")
		return rt.Status(), nil
	case "create_realm":
		var cfg realm.Config
		if err := decodePayload(payload, &cfg); err != nil {
			return nil, err
		}
		if cfg.ID == "" || cfg.Name == "" {
			return nil, fmt.Errorf("realm id and name are required")
		}
		c.mu.RLock()
		_, exists := c.realms[cfg.ID]
		c.mu.RUnlock()
		if exists {
			return nil, fmt.Errorf("realm already exists: %s", cfg.ID)
		}
		rt := realm.NewRuntime(cfg, c.reg, c.bus)
		if err := rt.ValidateConfig(cfg); err != nil {
			return nil, err
		}
		c.register(rt)
		ids := c.chainIDs()
		ids = append(ids, cfg.ID)
		if err := c.setChain(ids); err != nil {
			c.mu.Lock()
			delete(c.realms, cfg.ID)
			c.mu.Unlock()
			return nil, err
		}
		c.record("Created "+cfg.Name, "Added to Realm chain", "ok")
		return rt.Status(), nil
	case "update_realm":
		rt, err := c.realm(id)
		if err != nil {
			return nil, err
		}
		var cfg realm.Config
		if err = decodePayload(payload, &cfg); err != nil {
			return nil, err
		}
		if cfg.ID != id {
			return nil, fmt.Errorf("realm id cannot be changed")
		}
		previous := rt.Config()
		if err = rt.Update(ctx, cfg); err != nil {
			return nil, err
		}
		if err = c.persistState(); err != nil {
			_ = rt.Update(ctx, previous)
			return nil, err
		}
		c.record("Updated "+rt.Name(), "Realm revision "+fmt.Sprint(cfg.Revision), "ok")
		return rt.Status(), nil
	case "delete_realm":
		rt, err := c.realm(id)
		if err != nil {
			return nil, err
		}
		if err = rt.Stop(ctx); err != nil {
			return nil, err
		}
		c.mu.Lock()
		delete(c.realms, id)
		c.mu.Unlock()
		c.forwarder.UnregisterRealm(id)
		ids := c.chainIDs()
		filtered := ids[:0]
		for _, value := range ids {
			if value != id {
				filtered = append(filtered, value)
			}
		}
		if err = c.setChain(filtered); err != nil {
			return nil, err
		}
		c.record("Deleted "+rt.Name(), "Removed from Realm chain", "ok")
		return map[string]any{"deleted": id}, nil
	case "probe_realm":
		rt, err := c.realm(id)
		if err != nil {
			return nil, err
		}
		var request struct {
			Target string `json:"target"`
		}
		if len(payload) > 0 {
			if err = decodePayload(payload, &request); err != nil {
				return nil, err
			}
		}
		if request.Target == "" {
			request.Target = "example.com:443"
		}
		results := rt.RunProbes(ctx, netx.NewFlow(id, "tcp", request.Target, "health_check"))
		status, latency, probeError := "healthy", 0.0, ""
		for _, result := range results {
			latency += result.LatencyMS
			if result.Status == "degraded" && status == "healthy" {
				status = "degraded"
			}
			if result.Status == "failed" || result.Status == "unreachable" || result.Error != "" {
				status = "failed"
				probeError = result.Error
			}
		}
		if len(results) == 0 {
			status = "unknown"
		}
		state := c.health.RecordProbe(id, status, latency, probeError)
		c.record("Probed "+rt.Name(), status, map[string]string{"failed": "warn", "degraded": "warn"}[status])
		return map[string]any{"realm_id": id, "target": request.Target, "results": results, "health": state}, nil
	case "test_realm":
		return c.testRealm(ctx, id, payload)
	default:
		return nil, fmt.Errorf("unsupported control action: %s", action)
	}
}
