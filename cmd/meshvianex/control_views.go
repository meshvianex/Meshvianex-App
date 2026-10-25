package main

import (
	"meshvianex/core/realm"
	"meshvianex/core/resources"
	"sort"
)

func (c *controlPlane) realmRowsLocked() []map[string]any {
	ids := make([]string, 0, len(c.realms))
	realmNames := make(map[string]string, len(c.realms))
	for id, rt := range c.realms {
		ids = append(ids, id)
		realmNames[id] = rt.Name()
	}
	sort.Strings(ids)
	graphCfg := c.graph.Config()
	rows := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		cfg := c.realms[id].Config()
		redactedCfg := redactedRealmConfig(cfg)
		components := componentRows(cfg)
		execution := executionTopologyForRealm(cfg, graphCfg, realmNames)
		rows = append(rows, map[string]any{"id": cfg.ID, "name": cfg.Name, "state": string(c.realms[id].State()), "running": c.realms[id].State() == realm.Running || c.realms[id].State() == realm.Degraded, "revision": cfg.Revision, "default_outbound": cfg.DefaultOutbound, "plugin_count": len(components), "components": components, "inputs": cfg.Inputs, "outputs": cfg.Outputs, "config": redactedCfg, "execution_graph": execution})
	}
	return rows
}

func (c *controlPlane) pluginRowsLocked() []map[string]any {
	used := map[string]int{}
	for _, rt := range c.realms {
		for _, component := range componentRows(rt.Config()) {
			used[component["plugin"].(string)]++
		}
	}
	rows := make([]map[string]any, 0)
	for _, manifest := range c.reg.List() {
		row := map[string]any{"id": manifest.ID, "name": manifest.Name, "version": manifest.Version, "plugin_types": manifest.PluginTypes, "enabled": c.reg.Enabled(manifest.ID), "used_by": used[manifest.ID]}
		if schema, ok := c.reg.ConfigSchema(manifest.ID); ok {
			row["config_schema"] = schema
		}
		rows = append(rows, row)
	}
	return rows
}

func (c *controlPlane) connectionRowsLocked() []map[string]any {
	cfg := c.graph.Config()
	names := make(map[string]string, len(cfg.Nodes))
	realmIDs := make(map[string]string, len(cfg.Nodes))
	for _, node := range cfg.Nodes {
		switch node.Type {
		case "source":
			names[node.ID] = "Local source"
		case "sink":
			names[node.ID] = "Internet"
		default:
			realmIDs[node.ID] = node.RealmID
			name := node.RealmID
			if rt := c.realms[node.RealmID]; rt != nil {
				name = rt.Name()
			}
			names[node.ID] = name
		}
	}
	rows := make([]map[string]any, 0, len(cfg.Edges))
	for _, edge := range cfg.Edges {
		realmID := realmIDs[edge.To.Node]
		if realmID == "" {
			realmID = realmIDs[edge.From.Node]
		}
		healthState := c.health.Get(realmID)
		status := "unknown"
		if healthState.Status == "healthy" {
			status = "active"
		} else if healthState.Status == "failed" {
			status = "failed"
		} else if healthState.Status == "degraded" || healthState.Status == "unstable" {
			status = "degraded"
		} else if healthState.Status == "stale" {
			status = "stale"
		}
		if rt := c.realms[realmID]; rt != nil {
			switch rt.State() {
			case realm.Running:
				if edge.Type != "control" && status != "failed" && status != "stale" {
					status = "active"
				}
			case realm.Degraded:
				status = "degraded"
			case realm.Failed, realm.Stopped:
				status = "failed"
			default:
				status = "unknown"
			}
		}
		capability := "unsupported"
		if edge.Type != "control" && status == "active" {
			capability = "active"
		}
		rows = append(rows, map[string]any{
			"id": edge.ID, "from": names[edge.From.Node], "to": names[edge.To.Node], "type": edge.Type,
			"status": status, "score": healthState.Score, "latency_ms": healthState.LatencyMS,
			"loss_percent": healthState.LossPercent, "throughput_kbps": healthState.ThroughputKbps,
			"capabilities": map[string]string{"tcp_forwarding": capability, "udp_forwarding": "unsupported", "tun_forwarding": "unsupported"},
		})
	}
	return rows
}

func (c *controlPlane) connectionStatsLocked() map[string]any {
	rows := c.connectionRowsLocked()
	stats := map[string]any{"total": len(rows), "active": 0, "degraded": 0, "failed": 0, "average_latency_ms": 0.0, "average_throughput_kbps": 0.0}
	var latency, throughput float64
	for _, row := range rows {
		switch row["status"] {
		case "active":
			stats["active"] = stats["active"].(int) + 1
		case "degraded":
			stats["degraded"] = stats["degraded"].(int) + 1
		case "failed":
			stats["failed"] = stats["failed"].(int) + 1
		}
		latency += row["latency_ms"].(float64)
		throughput += row["throughput_kbps"].(float64)
	}
	if len(rows) > 0 {
		stats["average_latency_ms"] = latency / float64(len(rows))
		stats["average_throughput_kbps"] = throughput / float64(len(rows))
	}
	return stats
}

func componentRows(cfg realm.Config) []map[string]any {
	rows := []map[string]any{}
	if cfg.Config != nil && cfg.Config.Plugin != "" {
		rows = append(rows, map[string]any{
			"type":        "config",
			"instance_id": cfg.Config.InstanceID,
			"plugin":      cfg.Config.Plugin,
			"enabled":     cfg.Config.Enabled,
			"source_type": cfg.Config.SourceType,
			"source_ref":  cfg.Config.SourceRef,
			"source_refs": cfg.Config.SourceRefs,
		})
	}
	add := func(kind string, c realm.ComponentRef) {
		if c.Plugin != "" {
			rows = append(rows, map[string]any{"type": kind, "instance_id": c.InstanceID, "plugin": c.Plugin, "enabled": c.Enabled, "config_ref": c.ConfigRef, "stage": c.Stage, "transport": c.Transport})
		}
	}
	if cfg.Routing != nil {
		add("routing", *cfg.Routing)
	}
	for _, item := range cfg.DNS {
		add("dns", item)
	}
	for _, item := range cfg.Transforms {
		add("transform", item)
	}
	for _, item := range cfg.Protocols {
		add("protocol", item)
	}
	for _, item := range cfg.Transports {
		add("transport", item)
	}
	for _, item := range cfg.Outbounds {
		add("outbound", item)
	}
	for _, item := range cfg.Inbounds {
		add("inbound", item)
	}
	for _, item := range cfg.Probes {
		add("probe", item)
	}
	return rows
}

func redactedRealmConfig(cfg realm.Config) realm.Config {
	redact := func(component realm.ComponentRef) realm.ComponentRef {
		component.Config = resources.RedactJSON(component.Config)
		return component
	}
	if cfg.Routing != nil {
		copy := redact(*cfg.Routing)
		cfg.Routing = &copy
	}
	if cfg.Config != nil {
		copy := *cfg.Config
		copy.Config = resources.RedactJSON(copy.Config)
		cfg.Config = &copy
	}
	for index := range cfg.DNS {
		cfg.DNS[index] = redact(cfg.DNS[index])
	}
	for index := range cfg.Transforms {
		cfg.Transforms[index] = redact(cfg.Transforms[index])
	}
	for index := range cfg.Protocols {
		cfg.Protocols[index] = redact(cfg.Protocols[index])
	}
	for index := range cfg.Transports {
		cfg.Transports[index] = redact(cfg.Transports[index])
	}
	for index := range cfg.Outbounds {
		cfg.Outbounds[index] = redact(cfg.Outbounds[index])
	}
	for index := range cfg.Inbounds {
		cfg.Inbounds[index] = redact(cfg.Inbounds[index])
	}
	for index := range cfg.Probes {
		cfg.Probes[index] = redact(cfg.Probes[index])
	}
	return cfg
}
