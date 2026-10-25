package main

import (
	"context"
	"encoding/json"
	"fmt"
	"meshvianex/core/netx"
	"meshvianex/core/plugin"
	"meshvianex/core/resources"
	"time"
)

func (c *controlPlane) setDefaultOutbound(ctx context.Context, id string, payload json.RawMessage) (any, error) {
	var request struct {
		RealmID  string `json:"realm_id"`
		Outbound string `json:"outbound"`
	}
	if err := decodePayload(payload, &request); err != nil {
		return nil, err
	}
	if request.RealmID == "" {
		request.RealmID = id
	}
	if request.RealmID == "" || request.Outbound == "" {
		return nil, fmt.Errorf("realm_id and outbound are required")
	}
	rt, err := c.realm(request.RealmID)
	if err != nil {
		return nil, err
	}
	previous := rt.Config()
	next := previous
	if !hasEnabledOutbound(&next, request.Outbound) {
		return nil, fmt.Errorf("outbound %q is not enabled in Realm %q", request.Outbound, request.RealmID)
	}
	next.DefaultOutbound = request.Outbound
	if err := rt.Update(ctx, next); err != nil {
		return nil, err
	}
	if err := c.persistState(); err != nil {
		_ = rt.Update(ctx, previous)
		return nil, err
	}
	return rt.Status(), nil
}

func (c *controlPlane) testRealm(ctx context.Context, id string, payload json.RawMessage) (any, error) {
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
	started := time.Now()
	conn, dialErr := rt.DialContext(ctx, netx.NewFlow(id, "tcp", request.Target, "user_traffic"))
	result := map[string]any{
		"success": dialErr == nil, "stage": "connect", "latency_ms": float64(time.Since(started).Microseconds()) / 1000,
		"realm": id, "outbound": rt.Config().DefaultOutbound, "proxy_type": "realm", "destination": request.Target,
		"timestamp": time.Now().UTC(),
	}
	if dialErr != nil {
		result["error"] = resources.RedactString(dialErr.Error())
		return result, nil
	}
	_ = conn.Close()
	return result, nil
}

func (c *controlPlane) testConfig(ctx context.Context, id string, payload json.RawMessage) (any, error) {
	item, err := c.resources.GetConfig(id)
	if err != nil {
		return nil, err
	}
	if err = c.validateResourceConfig(item); err != nil {
		return nil, err
	}
	if item.PluginType != "outbound" {
		return map[string]any{"success": false, "stage": "validation", "realm": "standalone", "outbound": item.ID, "proxy_type": item.PluginID, "destination": "", "error": "connectivity test requires an outbound config", "timestamp": time.Now().UTC()}, nil
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
	instance, err := c.reg.Create(ctx, item.PluginID, plugin.InstanceContext{InstanceID: "test-" + id, Purpose: "health_check"}, item.Normalized)
	if err != nil {
		return nil, err
	}
	defer instance.Stop(context.Background())
	if err := instance.Start(ctx); err != nil {
		return nil, fmt.Errorf("config test start: %w", err)
	}
	dialer, ok := instance.(netx.Dialer)
	if !ok {
		return map[string]any{"success": false, "stage": "validation", "error": "plugin does not implement outbound dialer", "timestamp": time.Now().UTC()}, nil
	}
	started := time.Now()
	conn, err := dialer.DialContext(ctx, netx.NewFlow("", "tcp", request.Target, "health_check"))
	result := map[string]any{"success": err == nil, "stage": "connect", "latency_ms": float64(time.Since(started).Microseconds()) / 1000, "realm": "standalone", "outbound": item.ID, "selected_outbound": item.ID, "proxy_type": item.PluginID, "destination": request.Target, "timestamp": time.Now().UTC()}
	if err != nil {
		result["error"] = resources.RedactString(err.Error())
		return result, nil
	}
	_ = conn.Close()
	return result, nil
}

func (c *controlPlane) testSubscription(ctx context.Context, id string) (any, error) {
	started := time.Now()
	result, err := c.refreshSubscription(ctx, id, false)
	base := map[string]any{"success": err == nil, "stage": "fetch_parse", "latency_ms": float64(time.Since(started).Microseconds()) / 1000, "realm": "standalone", "outbound": "", "proxy_type": "subscription", "destination": "subscription:" + id, "timestamp": time.Now().UTC()}
	if err != nil {
		base["error"] = resources.RedactString(err.Error())
		return base, nil
	}
	if values, ok := result.(map[string]any); ok {
		for key, value := range values {
			base[key] = value
		}
		if configs, ok := values["configs"].([]resources.NormalizedConfig); ok && len(configs) == 0 {
			base["success"] = false
			base["error"] = "subscription produced no valid configs"
		}
	}
	return base, nil
}
