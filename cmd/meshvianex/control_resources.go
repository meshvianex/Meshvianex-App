package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"meshvianex/core/plugin"
	"meshvianex/core/resources"
	"strings"
	"time"
)

func (c *controlPlane) configRows() []resources.Config {
	items, err := c.resources.ListConfigs()
	if err != nil {
		return []resources.Config{}
	}
	for i := range items {
		items[i] = resources.RedactConfig(items[i])
	}
	return items
}

func (c *controlPlane) validateResourceConfig(item resources.Config) error {
	if item.ID == "" || item.Name == "" || item.PluginID == "" {
		return fmt.Errorf("config id, name, and plugin_id are required")
	}
	manifest, ok := c.reg.Manifest(item.PluginID)
	if !ok {
		return fmt.Errorf("unknown config plugin: %s", item.PluginID)
	}
	if !c.reg.Enabled(item.PluginID) {
		return fmt.Errorf("config plugin is disabled: %s", item.PluginID)
	}
	if item.PluginType == "" {
		return fmt.Errorf("config %s plugin_type is required", item.ID)
	}
	provided := false
	for _, typ := range manifest.PluginTypes {
		if typ == item.PluginType {
			provided = true
			break
		}
	}
	if !provided {
		return fmt.Errorf("config plugin %s does not provide type %s", item.PluginID, item.PluginType)
	}
	if !json.Valid(item.Normalized) {
		return fmt.Errorf("config %s normalized value must be valid JSON", item.ID)
	}
	// Outbound factories own their protocol-specific validation. Inbound
	// factories can require a Realm dialer service and are validated when the
	// Realm instantiates them instead.
	if item.PluginType != "inbound" {
		instance, err := c.reg.Create(context.Background(), item.PluginID, plugin.InstanceContext{InstanceID: "validate-" + item.ID, Purpose: "config_validation"}, item.Normalized)
		if err != nil {
			return fmt.Errorf("config %s: %w", item.ID, err)
		}
		if err := instance.Start(context.Background()); err != nil {
			_ = instance.Stop(context.Background())
			return fmt.Errorf("config %s start: %w", item.ID, err)
		}
		if err := instance.Stop(context.Background()); err != nil {
			return fmt.Errorf("config %s stop: %w", item.ID, err)
		}
	}
	return nil
}

func (c *controlPlane) validateParserPlugin(id string) error {
	manifest, ok := c.reg.Manifest(id)
	if !ok {
		return fmt.Errorf("unknown parser plugin: %s", id)
	}
	provided := false
	for _, typ := range manifest.PluginTypes {
		if typ == "config_parser" {
			provided = true
			break
		}
	}
	if !provided {
		return fmt.Errorf("plugin %s does not provide type config_parser", id)
	}
	if !c.reg.Enabled(id) {
		return fmt.Errorf("parser plugin is disabled: %s", id)
	}
	instance, err := c.reg.Create(context.Background(), id, plugin.InstanceContext{InstanceID: "validate-parser", Purpose: "config_validation"}, json.RawMessage(`{}`))
	if err != nil {
		return fmt.Errorf("parser plugin %s: %w", id, err)
	}
	if err := instance.Start(context.Background()); err != nil {
		_ = instance.Stop(context.Background())
		return fmt.Errorf("parser plugin %s start: %w", id, err)
	}
	if err := instance.Stop(context.Background()); err != nil {
		return fmt.Errorf("parser plugin %s stop: %w", id, err)
	}
	return nil
}

func (c *controlPlane) patchSubscription(id string, payload json.RawMessage) (any, error) {
	if id == "" {
		return nil, fmt.Errorf("subscription id is required")
	}
	item, err := c.resources.GetSubscription(id)
	if err != nil {
		return nil, err
	}
	var patch map[string]json.RawMessage
	if err := decodePayload(payload, &patch); err != nil {
		return nil, err
	}
	decodeString := func(key string, destination *string) error {
		if raw, ok := patch[key]; ok {
			if err := json.Unmarshal(raw, destination); err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
		}
		return nil
	}
	if err := decodeString("name", &item.Name); err != nil {
		return nil, err
	}
	if err := decodeString("source", &item.Source); err != nil {
		return nil, err
	}
	if raw, ok := patch["source"]; ok {
		var presented string
		if err := json.Unmarshal(raw, &presented); err != nil {
			return nil, fmt.Errorf("source: %w", err)
		}
		item.Source = resources.MergeRedactedString(item.Source, presented)
	}
	if err := decodeString("source_type", &item.SourceType); err != nil {
		return nil, err
	}
	if err := decodeString("parser_plugin", &item.ParserPlugin); err != nil {
		return nil, err
	}
	if raw, ok := patch["enabled"]; ok {
		if err := json.Unmarshal(raw, &item.Enabled); err != nil {
			return nil, fmt.Errorf("enabled: %w", err)
		}
	}
	if raw, ok := patch["refresh_interval"]; ok {
		if err := json.Unmarshal(raw, &item.RefreshInterval); err != nil {
			return nil, fmt.Errorf("refresh_interval: %w", err)
		}
	}
	if raw, ok := patch["tags"]; ok {
		if err := json.Unmarshal(raw, &item.Tags); err != nil {
			return nil, fmt.Errorf("tags: %w", err)
		}
	}
	if strings.TrimSpace(item.Name) == "" || strings.TrimSpace(item.Source) == "" {
		return nil, fmt.Errorf("subscription name and source are required")
	}
	if item.ParserPlugin == "" {
		item.ParserPlugin = "io.meshvianex.config.parser.basic"
	}
	if err := c.validateParserPlugin(item.ParserPlugin); err != nil {
		return nil, err
	}
	if item.SourceType == "" {
		item.SourceType = "raw"
	}
	if item.SourceType != "raw" && item.SourceType != "url" {
		return nil, fmt.Errorf("unsupported subscription source_type %q", item.SourceType)
	}
	if _, err := parseRefreshInterval(item.RefreshInterval); err != nil {
		return nil, err
	}
	if err := c.resources.SaveSubscription(item); err != nil {
		return nil, err
	}
	c.record("Updated subscription "+item.Name, "Subscription settings saved", "ok")
	return resources.RedactSubscription(item), nil
}

func (c *controlPlane) setSubscriptionEnabled(ctx context.Context, id string, payload json.RawMessage) (any, error) {
	item, err := c.resources.GetSubscription(id)
	if err != nil {
		return nil, err
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodePayload(payload, &req); err != nil {
		return nil, err
	}
	previous := item
	item.Enabled = req.Enabled
	if err := c.resources.SaveSubscription(item); err != nil {
		return nil, err
	}
	if err := c.syncSubscriptionBindings(ctx, id); err != nil {
		_ = c.resources.SaveSubscription(previous)
		_ = c.syncSubscriptionBindings(ctx, id)
		return nil, err
	}
	if err := c.persistState(); err != nil {
		_ = c.resources.SaveSubscription(previous)
		_ = c.syncSubscriptionBindings(ctx, id)
		return nil, err
	}
	return resources.RedactSubscription(item), nil
}

func (c *controlPlane) setConfigEnabled(ctx context.Context, id string, payload json.RawMessage) (any, error) {
	item, err := c.resources.GetConfig(id)
	if err != nil {
		return nil, err
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodePayload(payload, &req); err != nil {
		return nil, err
	}
	previous := item
	if req.Enabled {
		if err := c.validateResourceConfig(item); err != nil {
			return nil, err
		}
	}
	item.Enabled = req.Enabled
	if err := c.resources.SaveConfig(item); err != nil {
		return nil, err
	}
	if err := c.syncConfigResource(ctx, id); err != nil {
		_ = c.resources.SaveConfig(previous)
		_ = c.syncConfigResource(ctx, id)
		return nil, err
	}
	if err := c.persistState(); err != nil {
		_ = c.resources.SaveConfig(previous)
		_ = c.syncConfigResource(ctx, id)
		return nil, err
	}
	return resources.RedactConfig(item), nil
}

func (c *controlPlane) importConfig(ctx context.Context, payload json.RawMessage) (any, error) {
	var req struct {
		Name         string   `json:"name"`
		Names        []string `json:"names"`
		URI          string   `json:"uri"`
		ParserPlugin string   `json:"parser_plugin"`
		Enabled      *bool    `json:"enabled"`
		Tags         []string `json:"tags"`
	}
	if err := decodePayload(payload, &req); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.URI) == "" {
		return nil, fmt.Errorf("config URI is required")
	}
	if req.ParserPlugin == "" {
		req.ParserPlugin = "io.meshvianex.config.parser.basic"
	}
	if err := c.validateParserPlugin(req.ParserPlugin); err != nil {
		return nil, err
	}
	instance, err := c.reg.Create(ctx, req.ParserPlugin, plugin.InstanceContext{InstanceID: "import-parser", Purpose: "config_import"}, json.RawMessage(`{}`))
	if err != nil {
		return nil, err
	}
	defer instance.Stop(context.Background())
	if err := instance.Start(ctx); err != nil {
		return nil, fmt.Errorf("config import parser start: %w", err)
	}
	parser, ok := instance.(resources.Parser)
	if !ok {
		return nil, fmt.Errorf("plugin %s does not implement config parser", req.ParserPlugin)
	}
	parsed, err := parser.Parse(ctx, req.URI)
	if err != nil {
		return nil, err
	}
	valid := make([]resources.Config, 0, len(parsed.Configs))
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	for index, normalized := range parsed.Configs {
		manifest, exists := c.reg.Manifest(normalized.PluginID)
		if !exists || !containsString(manifest.PluginTypes, normalized.PluginType) {
			parsed.Diagnostics = append(parsed.Diagnostics, resources.Diagnostic{Message: "imported config references an incompatible plugin " + normalized.PluginID})
			continue
		}
		item := resources.Config{ID: newResourceID("cfg"), Name: strings.TrimSpace(normalized.Name), Format: parsed.Format, PluginID: normalized.PluginID, PluginType: normalized.PluginType, Normalized: normalizeRawJSON(normalized.Config), Enabled: enabled, Tags: append([]string(nil), req.Tags...), ValidationStatus: "valid"}
		if strings.TrimSpace(req.Name) != "" {
			item.Name = strings.TrimSpace(req.Name)
		}
		if index < len(req.Names) && strings.TrimSpace(req.Names[index]) != "" {
			item.Name = strings.TrimSpace(req.Names[index])
		}
		if item.Name == "" {
			parsed.Diagnostics = append(parsed.Diagnostics, resources.Diagnostic{Line: index + 1, Message: "imported config requires a name"})
			continue
		}
		if len(valid) > 0 && req.Name != "" && len(req.Names) == 0 {
			item.Name = fmt.Sprintf("%s (%d)", req.Name, len(valid)+1)
		}
		if err := c.validateResourceConfig(item); err != nil {
			parsed.Diagnostics = append(parsed.Diagnostics, resources.Diagnostic{Message: err.Error()})
			continue
		}
		if err := c.resources.SaveConfig(item); err != nil {
			return nil, err
		}
		stored, _ := c.resources.GetConfig(item.ID)
		valid = append(valid, stored)
	}
	if len(valid) == 0 {
		return map[string]any{"configs": []resources.Config{}, "format": parsed.Format, "diagnostics": parsed.Diagnostics}, fmt.Errorf("config URI produced no valid configs")
	}
	out := make([]resources.Config, len(valid))
	for i := range valid {
		out[i] = resources.RedactConfig(valid[i])
	}
	return map[string]any{"configs": out, "format": parsed.Format, "diagnostics": parsed.Diagnostics}, nil
}

func newResourceID(prefix string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s-%x", prefix, b[:])
}
