package config_parser

import (
	"context"
	"encoding/json"
	"meshvianex/core/plugin"
	"meshvianex/core/resources"
	"strings"
)

const (
	HTTPOutbound   = "io.meshvianex.outbound.http"
	SOCKSOutbound  = "io.meshvianex.outbound.socks5"
	DirectOutbound = "io.meshvianex.direct"
	XrayOutbound   = "io.meshvianex.outbound.xray"
)

type Factory struct{}

func (Factory) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: "io.meshvianex.config.parser.basic", Name: "Basic Proxy Config Parser", Version: "0.1.0", PluginTypes: []string{"config_parser"}, RequiresCapabilities: []string{"config.read"}, Provides: []string{"config_parser.uri", "config_parser.base64", "config_parser.xray"}, ABI: 1, ConfigSchema: &plugin.ConfigSchema{Title: "Basic Proxy Config Parser"}}
}

func (Factory) Create(context.Context, plugin.InstanceContext, json.RawMessage) (plugin.Instance, error) {
	return Instance{}, nil
}

type Instance struct{}

func (Instance) Start(context.Context) error { return nil }
func (Instance) Stop(context.Context) error  { return nil }

func (Instance) Parse(ctx context.Context, content string) (resources.ParseResult, error) {
	if err := ctx.Err(); err != nil {
		return resources.ParseResult{}, err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return resources.ParseResult{Format: "empty", Diagnostics: []resources.Diagnostic{{Message: "subscription content is empty"}}}, nil
	}
	if strings.HasPrefix(content, "{") || strings.HasPrefix(content, "[") {
		result, ok := parseJSON(content)
		if ok {
			deduplicate(&result)
			return result, nil
		}
	}
	if decoded, ok := decodeBase64(content); ok {
		result, err := Instance{}.Parse(ctx, decoded)
		if err != nil {
			return resources.ParseResult{}, err
		}
		result.Format = "base64"
		deduplicate(&result)
		return result, nil
	}
	lines := strings.Split(content, "\n")
	result := resources.ParseResult{Format: "single_uri"}
	if len(lines) > 1 {
		result.Format = "uri_list"
	}
	for lineNumber, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		config, err := parseURI(line)
		if err != nil {
			result.Diagnostics = append(result.Diagnostics, resources.Diagnostic{Line: lineNumber + 1, Message: err.Error()})
			continue
		}
		result.Configs = append(result.Configs, config)
	}
	deduplicate(&result)
	if len(result.Configs) == 0 && len(result.Diagnostics) == 0 {
		result.Diagnostics = append(result.Diagnostics, resources.Diagnostic{Message: "no supported configuration entries found"})
	}
	return result, nil
}
