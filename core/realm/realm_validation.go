package realm

import (
	"encoding/json"
	"fmt"
	"meshvianex/core/netx"
	"strings"
)

func (r *Runtime) ValidateConfig(cfg Config) error {
	if cfg.ID == "" || cfg.Name == "" {
		return fmt.Errorf("realm id and name are required")
	}
	if cfg.SchemaVersion != 0 && cfg.SchemaVersion != 1 {
		return fmt.Errorf("unsupported realm schema_version %d", cfg.SchemaVersion)
	}
	if r.registry == nil {
		return fmt.Errorf("plugin registry is required")
	}
	r.mu.RLock()
	refs := r.refs
	r.mu.RUnlock()

	seen := map[string]bool{}
	enabledByType := map[string]map[string]bool{}
	markEnabled := func(kind, id string) {
		if enabledByType[kind] == nil {
			enabledByType[kind] = map[string]bool{}
		}
		enabledByType[kind][id] = true
	}
	check := func(c ComponentRef, kind string, expected ...string) error {
		if c.InstanceID == "" {
			if !c.Enabled {
				return nil
			}
			return fmt.Errorf("enabled %s instance_id is required", kind)
		}
		if seen[c.InstanceID] {
			return fmt.Errorf("duplicate component instance: %s", c.InstanceID)
		}
		seen[c.InstanceID] = true
		if !c.Enabled {
			return nil
		}
		manifest, ok := r.registry.Manifest(c.Plugin)
		if !ok {
			return fmt.Errorf("unknown plugin: %s", c.Plugin)
		}
		if _, err := r.registry.ResolveDependencies(c.Plugin); err != nil {
			return err
		}
		matches := false
		for _, actual := range manifest.PluginTypes {
			for _, want := range expected {
				if actual == want {
					matches = true
					break
				}
			}
		}
		if !matches {
			return fmt.Errorf("plugin %s does not provide %s type %v", c.Plugin, kind, expected)
		}
		markEnabled(kind, c.InstanceID)
		return nil
	}

	if cfg.Routing != nil {
		if err := check(*cfg.Routing, "routing", "routing"); err != nil {
			return err
		}
	}
	for _, c := range cfg.DNS {
		if err := check(c, "dns", "dns"); err != nil {
			return err
		}
	}
	for _, c := range cfg.Transforms {
		if err := check(c, "transform", "packet_transform", "stream_transform"); err != nil {
			return err
		}
		if c.Enabled && !validTransformStage(netx.TransformStage(c.Stage)) {
			return fmt.Errorf("transform %s has invalid stage %q", c.InstanceID, c.Stage)
		}
	}
	for _, c := range cfg.Transports {
		if err := check(c, "transport", "transport"); err != nil {
			return err
		}
	}
	for _, c := range cfg.Protocols {
		if err := check(c, "protocol", "protocol", "vpn_protocol"); err != nil {
			return err
		}
	}
	for _, c := range cfg.Outbounds {
		if err := check(c, "outbound", "outbound"); err != nil {
			return err
		}
	}
	for _, c := range cfg.Inbounds {
		if err := check(c, "inbound", "inbound"); err != nil {
			return err
		}
	}
	for _, c := range cfg.Probes {
		if err := check(c, "probe", "diagnostic", "test_provider"); err != nil {
			return err
		}
	}
	if cfg.Config != nil {
		selection := cfg.Config
		if selection.InstanceID == "" {
			if selection.Enabled {
				return fmt.Errorf("enabled config instance_id is required")
			}
		} else if seen[selection.InstanceID] {
			return fmt.Errorf("duplicate component instance: %s", selection.InstanceID)
		}
		if selection.Enabled {
			manifest, ok := r.registry.Manifest(selection.Plugin)
			if !ok {
				return fmt.Errorf("unknown config selector plugin: %s", selection.Plugin)
			}
			if !containsPluginType(manifest.PluginTypes, "config_selector") {
				return fmt.Errorf("plugin %s does not provide config_selector type", selection.Plugin)
			}
			if _, err := r.registry.ResolveDependencies(selection.Plugin); err != nil {
				return err
			}
			sourceType := strings.ToLower(strings.TrimSpace(selection.SourceType))
			if sourceType != "config" && sourceType != "subscription" {
				return fmt.Errorf("config source_type must be config or subscription")
			}
			sourceRefs := selection.SourceRefs
			if len(sourceRefs) == 0 && strings.TrimSpace(selection.SourceRef) != "" {
				sourceRefs = []string{selection.SourceRef}
			}
			if len(sourceRefs) == 0 {
				return fmt.Errorf("config source_ref is required")
			}
			if len(selection.Config) > 0 && !json.Valid(selection.Config) {
				return fmt.Errorf("config selector configuration must be valid JSON")
			}
			resolver, ok := refs.(ConfigCandidateResolver)
			if !ok {
				return fmt.Errorf("config selector requires a resource store")
			}
			seenSources := make(map[string]bool, len(sourceRefs))
			for _, sourceRef := range sourceRefs {
				sourceRef = strings.TrimSpace(sourceRef)
				if sourceRef == "" || seenSources[sourceRef] {
					continue
				}
				seenSources[sourceRef] = true
				if _, err := resolver.ConfigCandidates(sourceType, sourceRef); err != nil {
					return err
				}
			}
			if len(seenSources) == 0 {
				return fmt.Errorf("config source_ref is required")
			}
		}
	}

	for _, c := range cfg.Protocols {
		if c.Enabled && c.Transport != "" && !enabledByType["transport"][c.Transport] {
			return fmt.Errorf("protocol %s references unavailable transport %q", c.InstanceID, c.Transport)
		}
	}
	for _, c := range cfg.Outbounds {
		if !c.Enabled {
			continue
		}
		if c.Protocol != "" && !enabledByType["protocol"][c.Protocol] {
			return fmt.Errorf("outbound %s references unavailable protocol %q", c.InstanceID, c.Protocol)
		}
		if c.Transport != "" && !enabledByType["transport"][c.Transport] {
			return fmt.Errorf("outbound %s references unavailable transport %q", c.InstanceID, c.Transport)
		}
		if c.Protocol != "" {
			resolvedTransport := c.Transport
			for _, protocol := range cfg.Protocols {
				if protocol.InstanceID != c.Protocol {
					continue
				}
				if resolvedTransport == "" {
					resolvedTransport = protocol.Transport
				} else if protocol.Transport != "" && protocol.Transport != resolvedTransport {
					return fmt.Errorf("outbound %s selects transport %q but protocol %s requires %q", c.InstanceID, c.Transport, c.Protocol, protocol.Transport)
				}
				break
			}
			if resolvedTransport == "" {
				return fmt.Errorf("outbound %s protocol %s requires an enabled transport binding", c.InstanceID, c.Protocol)
			}
		}
	}
	if cfg.DefaultOutbound == "" || !enabledByType["outbound"][cfg.DefaultOutbound] {
		return fmt.Errorf("default outbound %q is not enabled", cfg.DefaultOutbound)
	}

	validTypes := map[string]bool{"flow": true, "packet": true, "stream": true, "datagram": true, "control": true, "metadata": true}
	inputPorts, outputPorts := map[string]bool{}, map[string]bool{}
	validatePorts := func(ports []Port, target map[string]bool, direction string) error {
		for _, port := range ports {
			if port.Name == "" || !validTypes[port.Type] {
				return fmt.Errorf("realm %s ports require a name and valid type", direction)
			}
			if target[port.Name] {
				return fmt.Errorf("duplicate realm %s port %s", direction, port.Name)
			}
			target[port.Name] = true
		}
		return nil
	}
	if err := validatePorts(cfg.Inputs, inputPorts, "input"); err != nil {
		return err
	}
	if err := validatePorts(cfg.Outputs, outputPorts, "output"); err != nil {
		return err
	}
	if cfg.DefaultInput != "" && !inputPorts[cfg.DefaultInput] {
		return fmt.Errorf("default input %q is not declared", cfg.DefaultInput)
	}
	if cfg.DefaultOutput != "" && !outputPorts[cfg.DefaultOutput] {
		return fmt.Errorf("default output %q is not declared", cfg.DefaultOutput)
	}
	for name, plane := range map[string]*PlaneConfig{"data_plane": cfg.DataPlane, "control_plane": cfg.ControlPlane} {
		if plane == nil {
			continue
		}
		if plane.Outbound != "" && !enabledByType["outbound"][plane.Outbound] {
			return fmt.Errorf("%s outbound %q is not enabled", name, plane.Outbound)
		}
		if plane.Input != "" && !inputPorts[plane.Input] {
			return fmt.Errorf("%s input %q is not declared", name, plane.Input)
		}
		if plane.Output != "" && !outputPorts[plane.Output] {
			return fmt.Errorf("%s output %q is not declared", name, plane.Output)
		}
	}

	for _, ref := range cfg.SubscriptionRefs {
		if ref == "" {
			return fmt.Errorf("realm contains an empty subscription reference")
		}
		if refs != nil && !refs.HasSubscription(ref) {
			return fmt.Errorf("realm %s references missing subscription %q", cfg.ID, ref)
		}
	}
	for _, ref := range cfg.ConfigRefs {
		if ref == "" {
			return fmt.Errorf("realm contains an empty config reference")
		}
		if refs != nil {
			if _, ok := refs.ResolveConfig(ref); !ok {
				return fmt.Errorf("realm %s references missing config %q", cfg.ID, ref)
			}
		}
	}
	componentRefs := []ComponentRef{}
	if cfg.Routing != nil {
		componentRefs = append(componentRefs, *cfg.Routing)
	}
	componentRefs = append(componentRefs, cfg.DNS...)
	componentRefs = append(componentRefs, cfg.Outbounds...)
	componentRefs = append(componentRefs, cfg.Inbounds...)
	componentRefs = append(componentRefs, cfg.Transforms...)
	componentRefs = append(componentRefs, cfg.Protocols...)
	componentRefs = append(componentRefs, cfg.Transports...)
	componentRefs = append(componentRefs, cfg.Probes...)
	for _, c := range componentRefs {
		if !c.Enabled || c.ConfigRef == "" {
			continue
		}
		if refs == nil {
			return fmt.Errorf("component %s references config %q but no resource store is configured", c.InstanceID, c.ConfigRef)
		}
		if _, ok := refs.ResolveConfig(c.ConfigRef); !ok {
			return fmt.Errorf("component %s references missing config %q", c.InstanceID, c.ConfigRef)
		}
		if metadata, ok := refs.(ConfigMetadataResolver); ok {
			pluginID, _, active, found := metadata.ResolveConfigMetadata(c.ConfigRef)
			if !found || !active {
				return fmt.Errorf("component %s references inactive config %q", c.InstanceID, c.ConfigRef)
			}
			if pluginID != "" && pluginID != c.Plugin {
				return fmt.Errorf("component %s references config %q for plugin %q, not %q", c.InstanceID, c.ConfigRef, pluginID, c.Plugin)
			}
		}
	}
	return nil
}
