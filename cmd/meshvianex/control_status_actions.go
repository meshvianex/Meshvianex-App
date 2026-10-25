package main

import (
	"context"
	"encoding/json"
	"fmt"
)

func (c *controlPlane) controlStatusAction(ctx context.Context, action, id string, payload json.RawMessage) (any, error) {
	switch action {
	case "list_realms":
		c.mu.RLock()
		defer c.mu.RUnlock()
		return c.realmRowsLocked(), nil
	case "list_plugins":
		c.mu.RLock()
		defer c.mu.RUnlock()
		return c.pluginRowsLocked(), nil
	case "list_connections":
		c.mu.RLock()
		defer c.mu.RUnlock()
		return c.connectionRowsLocked(), nil
	case "toggle_plugin":
		var req struct {
			Enabled bool `json:"enabled"`
		}
		if err := decodePayload(payload, &req); err != nil {
			return nil, err
		}
		if err := c.reg.SetEnabled(id, req.Enabled); err != nil {
			return nil, err
		}
		c.record("Plugin "+id, map[bool]string{true: "enabled", false: "disabled"}[req.Enabled], "ok")
		return map[string]any{"id": id, "enabled": req.Enabled}, nil
	default:
		return nil, fmt.Errorf("unsupported control action: %s", action)
	}
}
