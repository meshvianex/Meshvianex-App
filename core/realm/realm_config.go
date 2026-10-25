package realm

import (
	"meshvianex/core/netx"
	"meshvianex/core/plugin"
)

func migrateConfig(cfg Config) Config {
	canonicalize := func(ref *ComponentRef) {
		if ref != nil {
			ref.Plugin = plugin.CanonicalID(ref.Plugin)
		}
	}
	canonicalize(cfg.Routing)
	if cfg.Config != nil {
		cfg.Config.Plugin = plugin.CanonicalID(cfg.Config.Plugin)
	}
	groups := [][]ComponentRef{cfg.DNS, cfg.Transforms, cfg.Protocols, cfg.Transports, cfg.Outbounds, cfg.Inbounds, cfg.Probes}
	for _, group := range groups {
		for i := range group {
			canonicalize(&group[i])
		}
	}
	return cfg
}

func normalizeConfig(cfg Config) Config {
	cfg = migrateConfig(cfg)
	if len(cfg.Inputs) == 0 {
		cfg.Inputs = []Port{{Name: "in", Type: "flow"}}
	}
	if len(cfg.Outputs) == 0 {
		cfg.Outputs = []Port{{Name: "out", Type: "flow"}}
	}
	if cfg.DefaultInput == "" && len(cfg.Inputs) > 0 {
		cfg.DefaultInput = cfg.Inputs[0].Name
	}
	if cfg.DefaultOutput == "" && len(cfg.Outputs) > 0 {
		cfg.DefaultOutput = cfg.Outputs[0].Name
	}
	return cfg
}

func validTransformStage(stage netx.TransformStage) bool {
	switch stage {
	case netx.PreRoute, netx.PostRoute, netx.PreProtocol, netx.PostProtocol, netx.PreTransport, netx.PostTransport, netx.PreSocket, netx.StreamOpen, netx.Handshake, netx.StreamData, netx.StreamClose:
		return true
	default:
		return false
	}
}

func containsPluginType(types []string, wanted string) bool {
	for _, actual := range types {
		if actual == wanted {
			return true
		}
	}
	return false
}

func (r *Runtime) hasTransforms(stages ...netx.TransformStage) bool {
	wanted := make(map[netx.TransformStage]bool, len(stages))
	for _, stage := range stages {
		wanted[stage] = true
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, binding := range r.transforms {
		if wanted[binding.stage] {
			return true
		}
	}
	return false
}

// ApplyTransforms executes enabled transforms in the order in which they are
// declared. Realm configuration order is the deterministic tie-breaker.
