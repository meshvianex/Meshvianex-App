// Package transform_basic provides a small observable packet/stream
// transform. It is intentionally generic: it can annotate metadata, reject a
// flow, or fail explicitly while leaving protocol details to other plugins.
package transform_basic

import (
	"context"
	"encoding/json"
	"fmt"

	"meshvianex/core/netx"
	"meshvianex/core/plugin"
)

type Config struct {
	Set    map[string]string `json:"set,omitempty"`
	Append map[string]string `json:"append,omitempty"`
	Drop   bool              `json:"drop,omitempty"`
	Error  string            `json:"error,omitempty"`
}

type Factory struct{}

func (Factory) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID: "io.meshvianex.transform.metadata", Name: "Metadata Transform", Version: "0.1.0",
		PluginTypes: []string{"packet_transform", "stream_transform"}, RequiresCapabilities: []string{},
		Provides: []string{"transform.metadata", "transform.drop"}, ABI: 1,
		ConfigSchema: &plugin.ConfigSchema{Title: "Metadata Transform", Fields: []plugin.ConfigField{
			{ID: "set", Type: "json", Label: "Set metadata"},
			{ID: "append", Type: "json", Label: "Append metadata"},
			{ID: "drop", Type: "boolean", Label: "Drop traffic", Default: false},
			{ID: "error", Type: "text", Label: "Return error"},
		}},
	}
}

func (Factory) Create(_ context.Context, _ plugin.InstanceContext, raw json.RawMessage) (plugin.Instance, error) {
	var cfg Config
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("metadata transform config: %w", err)
		}
	}
	return &Instance{cfg: cfg}, nil
}

type Instance struct{ cfg Config }

func (*Instance) Start(context.Context) error { return nil }
func (*Instance) Stop(context.Context) error  { return nil }

func (i *Instance) Transform(ctx context.Context, _ netx.TransformStage, packet netx.Packet, _ netx.Flow) (netx.Packet, netx.TransformResult, error) {
	if err := ctx.Err(); err != nil {
		return packet, netx.Error, err
	}
	if packet.Metadata == nil {
		packet.Metadata = map[string]string{}
	}
	for key, value := range i.cfg.Set {
		packet.Metadata[key] = value
	}
	for key, value := range i.cfg.Append {
		packet.Metadata[key] += value
	}
	if i.cfg.Error != "" {
		return packet, netx.Error, fmt.Errorf("%s", i.cfg.Error)
	}
	if i.cfg.Drop {
		return packet, netx.Drop, nil
	}
	return packet, netx.Continue, nil
}
