package config_parser

import (
	"encoding/json"
	"meshvianex/core/resources"
)

func parseJSON(content string) (resources.ParseResult, bool) {
	var document json.RawMessage
	if err := json.Unmarshal([]byte(content), &document); err != nil {
		return resources.ParseResult{Format: "meshvianex_json", Diagnostics: []resources.Diagnostic{{Message: "invalid JSON: " + err.Error()}}}, true
	}
	result := resources.ParseResult{Format: "meshvianex_json"}
	if len(document) > 0 && document[0] == '{' {
		var envelope struct {
			Configs []json.RawMessage `json:"configs"`
		}
		if err := json.Unmarshal(document, &envelope); err == nil && envelope.Configs != nil {
			for index, raw := range envelope.Configs {
				appendJSONConfig(&result, raw, index+1)
			}
			return result, true
		}
		appendJSONConfig(&result, document, 1)
		return result, true
	}
	var many []json.RawMessage
	if err := json.Unmarshal(document, &many); err == nil {
		for index, raw := range many {
			appendJSONConfig(&result, raw, index+1)
		}
		return result, true
	}
	result.Diagnostics = append(result.Diagnostics, resources.Diagnostic{Message: "JSON document must be a normalized config, Xray/V2Ray config, or array"})
	return result, true
}

func appendJSONConfig(result *resources.ParseResult, raw json.RawMessage, line int) {
	var config resources.NormalizedConfig
	if err := json.Unmarshal(raw, &config); err == nil && config.PluginID != "" {
		if config.PluginType == "" || len(config.Config) == 0 || !json.Valid(config.Config) || string(config.Config) == "null" {
			result.Diagnostics = append(result.Diagnostics, resources.Diagnostic{Line: line, Message: "normalized config requires plugin_id, plugin_type, and valid config JSON"})
			return
		}
		if config.Name == "" {
			config.Name = jsonDisplayName(config.Config)
		}
		result.Configs = append(result.Configs, config)
		return
	}

	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		result.Diagnostics = append(result.Diagnostics, resources.Diagnostic{Line: line, Message: "invalid JSON config: " + err.Error()})
		return
	}
	if _, ok := document["outbounds"]; !ok {
		result.Diagnostics = append(result.Diagnostics, resources.Diagnostic{Line: line, Message: "JSON document must be normalized or contain Xray/V2Ray outbounds"})
		return
	}
	configJSON, name, diagnostic := xrayDocument(document)
	if diagnostic != "" {
		result.Diagnostics = append(result.Diagnostics, resources.Diagnostic{Line: line, Message: diagnostic})
		return
	}
	result.Configs = append(result.Configs, resources.NormalizedConfig{Name: name, PluginID: XrayOutbound, PluginType: "outbound", Config: configJSON})
}

func xrayDocument(document map[string]json.RawMessage) (json.RawMessage, string, string) {
	if inbounds, ok := document["inbounds"]; ok && string(inbounds) != "null" && string(inbounds) != "[]" {
		delete(document, "inbounds")
	}
	raw, ok := document["outbounds"]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return nil, "", "Xray/V2Ray JSON contains no outbounds"
	}
	var outbounds []json.RawMessage
	if err := json.Unmarshal(raw, &outbounds); err != nil || len(outbounds) == 0 {
		return nil, "", "Xray/V2Ray JSON outbounds must be a non-empty array"
	}
	encoded, err := json.Marshal(map[string]any{"inline_config": document})
	if err != nil {
		return nil, "", "could not normalize Xray/V2Ray JSON"
	}
	return encoded, jsonDisplayName(rawMessage(document)), ""
}

func deduplicate(result *resources.ParseResult) {
	seen := make(map[string]bool, len(result.Configs))
	unique := result.Configs[:0]
	for _, config := range result.Configs {
		key := resources.StableKey(config)
		if seen[key] {
			result.Diagnostics = append(result.Diagnostics, resources.Diagnostic{Message: "duplicate configuration entry ignored"})
			continue
		}
		seen[key] = true
		unique = append(unique, config)
	}
	result.Configs = unique
}
