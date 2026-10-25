package realm

import (
	"context"
	"encoding/json"
	"fmt"
	"meshvianex/core/netx"
)

func (r *Runtime) ApplyTransforms(ctx context.Context, stage netx.TransformStage, packet netx.Packet, flow netx.Flow) (netx.Packet, netx.TransformResult, error) {
	if packet.Metadata == nil {
		packet.Metadata = map[string]string{}
	}
	r.mu.RLock()
	bindings := make([]transformBinding, 0, len(r.transforms))
	for _, binding := range r.transforms {
		if binding.stage == stage {
			bindings = append(bindings, binding)
		}
	}
	r.mu.RUnlock()
	if len(bindings) == 0 {
		return packet, netx.Continue, nil
	}
	current := packet.Clone()
	for _, binding := range bindings {
		next, result, err := binding.value.Transform(ctx, stage, current, flow)
		if err != nil {
			return current, netx.Error, fmt.Errorf("transform %s at %s: %w", binding.id, stage, err)
		}
		switch result {
		case netx.Drop:
			return next, netx.Drop, nil
		case netx.Error:
			return current, netx.Error, fmt.Errorf("transform %s at %s returned ERROR", binding.id, stage)
		case netx.Replace, netx.Continue, netx.Defer:
			current = next
		default:
			return current, netx.Error, fmt.Errorf("transform %s returned unknown result %q", binding.id, result)
		}
	}
	return current, netx.Continue, nil
}

func mergeJSONObjects(base, override json.RawMessage) (json.RawMessage, error) {
	merged := map[string]json.RawMessage{}
	if len(base) > 0 && string(base) != "null" {
		if err := json.Unmarshal(base, &merged); err != nil {
			return nil, fmt.Errorf("referenced config must be a JSON object: %w", err)
		}
	}
	if len(override) > 0 && string(override) != "null" {
		var extra map[string]json.RawMessage
		if err := json.Unmarshal(override, &extra); err != nil {
			return nil, fmt.Errorf("inline config override must be a JSON object: %w", err)
		}
		for key, value := range extra {
			merged[key] = value
		}
	}
	return json.Marshal(merged)
}
