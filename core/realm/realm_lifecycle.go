package realm

import (
	"context"
	"encoding/json"
	"fmt"
	"meshvianex/core/event"
	"meshvianex/core/netx"
	"meshvianex/core/plugin"
)

func (r *Runtime) Start(ctx context.Context) error {
	r.mu.Lock()
	if r.state == Running || r.state == Starting || r.state == Degraded {
		r.mu.Unlock()
		return nil
	}
	r.state = ResolvingDependencies
	r.rule, r.selector, r.resolvers, r.outbounds, r.configOutbounds, r.transports, r.protocols, r.protocolTransports, r.transforms, r.probes, r.instances = nil, nil, nil, map[string]netx.Dialer{}, map[string]netx.Dialer{}, map[string]netx.Transport{}, map[string]netx.Protocol{}, map[string]string{}, nil, nil, nil
	cfg := r.cfg
	refs := r.refs
	r.mu.Unlock()
	if err := r.ValidateConfig(cfg); err != nil {
		r.setState(Failed)
		return err
	}
	r.setState(Ready)
	r.setState(Starting)
	services := map[string]any{"realm.dialer": netx.Dialer(r), "realm.resolver": netx.Resolver(r)}
	configFor := func(c ComponentRef) (json.RawMessage, error) {
		if c.ConfigRef == "" {
			return c.Config, nil
		}
		if refs == nil {
			return nil, fmt.Errorf("component %s references config %q but no resource store is configured", c.InstanceID, c.ConfigRef)
		}
		base, ok := refs.ResolveConfig(c.ConfigRef)
		if !ok {
			return nil, fmt.Errorf("component %s references missing config %q", c.InstanceID, c.ConfigRef)
		}
		return mergeJSONObjects(base, c.Config)
	}
	create := func(c ComponentRef) (plugin.Instance, error) {
		if !c.Enabled {
			return nil, nil
		}
		resolved, err := configFor(c)
		if err != nil {
			return nil, err
		}
		return r.registry.Create(ctx, c.Plugin, plugin.InstanceContext{InstanceID: c.InstanceID, RealmID: cfg.ID, Purpose: "realm_component", Services: services}, resolved)
	}
	fail := func(err error) error { r.stopStarted(ctx); r.setState(Failed); return err }
	if cfg.Config != nil && cfg.Config.Enabled {
		selectionConfig := cfg.Config.Config
		if len(selectionConfig) == 0 {
			selectionConfig = json.RawMessage(`{"strategy":"round_robin"}`)
		}
		selectorInstance, err := r.registry.Create(ctx, cfg.Config.Plugin, plugin.InstanceContext{InstanceID: cfg.Config.InstanceID, RealmID: cfg.ID, Purpose: "realm_config_selector", Services: services}, selectionConfig)
		if err != nil {
			return fail(err)
		}
		selector, ok := selectorInstance.(netx.ConfigSelector)
		if !ok {
			return fail(fmt.Errorf("plugin %s does not implement config selector", cfg.Config.Plugin))
		}
		r.selector = selector
		r.instances = append(r.instances, selectorInstance)
		candidates, err := r.resolveConfigCandidates(*cfg.Config)
		if err != nil {
			return fail(err)
		}
		for _, candidate := range candidates {
			instance, createErr := r.registry.Create(ctx, candidate.pluginID, plugin.InstanceContext{InstanceID: "resource-" + candidate.id, RealmID: cfg.ID, Purpose: "realm_config_candidate", Services: services}, candidate.config)
			if createErr != nil {
				return fail(fmt.Errorf("config candidate %s: %w", candidate.id, createErr))
			}
			dialer, ok := instance.(netx.Dialer)
			if !ok {
				return fail(fmt.Errorf("config candidate %s plugin %s does not implement outbound dialer", candidate.id, candidate.pluginID))
			}
			r.configOutbounds[candidate.id] = dialer
			r.instances = append(r.instances, instance)
		}
	}
	if cfg.Routing != nil && cfg.Routing.Enabled {
		inst, err := create(*cfg.Routing)
		if err != nil {
			return fail(err)
		}
		re, ok := inst.(netx.RuleEngine)
		if !ok {
			return fail(fmt.Errorf("plugin %s does not implement rule engine", cfg.Routing.Plugin))
		}
		r.rule = re
		r.instances = append(r.instances, inst)
	}
	for _, c := range cfg.DNS {
		inst, err := create(c)
		if err != nil {
			return fail(err)
		}
		if inst == nil {
			continue
		}
		if d, ok := inst.(netx.Resolver); ok {
			r.resolvers = append(r.resolvers, namedResolver{id: c.InstanceID, value: d})
		}
		r.instances = append(r.instances, inst)
	}
	for _, c := range cfg.Transforms {
		inst, err := create(c)
		if err != nil {
			return fail(err)
		}
		if inst == nil {
			continue
		}
		transformer, ok := inst.(netx.Transformer)
		if !ok {
			return fail(fmt.Errorf("plugin %s does not implement transformer", c.Plugin))
		}
		stage := netx.TransformStage(c.Stage)
		if !validTransformStage(stage) {
			return fail(fmt.Errorf("transform %s has invalid stage %q", c.InstanceID, c.Stage))
		}
		r.transforms = append(r.transforms, transformBinding{id: c.InstanceID, stage: stage, value: transformer})
		r.instances = append(r.instances, inst)
	}
	for _, c := range cfg.Transports {
		inst, err := create(c)
		if err != nil {
			return fail(err)
		}
		if inst == nil {
			continue
		}
		transport, ok := inst.(netx.Transport)
		if !ok {
			return fail(fmt.Errorf("plugin %s does not implement transport", c.Plugin))
		}
		r.transports[c.InstanceID] = transport
		services["transport."+c.InstanceID] = transport
		r.instances = append(r.instances, inst)
	}
	for _, c := range cfg.Protocols {
		inst, err := create(c)
		if err != nil {
			return fail(err)
		}
		if inst == nil {
			continue
		}
		protocol, ok := inst.(netx.Protocol)
		if !ok {
			return fail(fmt.Errorf("plugin %s does not implement protocol", c.Plugin))
		}
		r.protocols[c.InstanceID] = protocol
		r.protocolTransports[c.InstanceID] = c.Transport
		services["protocol."+c.InstanceID] = protocol
		r.instances = append(r.instances, inst)
	}
	for _, c := range cfg.Outbounds {
		inst, err := create(c)
		if err != nil {
			return fail(err)
		}
		if inst == nil {
			continue
		}
		d, ok := inst.(netx.Dialer)
		if !ok {
			return fail(fmt.Errorf("plugin %s does not implement outbound dialer", c.Plugin))
		}
		if _, exists := r.outbounds[c.InstanceID]; exists {
			return fail(fmt.Errorf("duplicate outbound instance: %s", c.InstanceID))
		}
		r.outbounds[c.InstanceID] = d
		r.instances = append(r.instances, inst)
	}
	if _, ok := r.outbounds[cfg.DefaultOutbound]; !ok {
		return fail(fmt.Errorf("default outbound %q is not enabled", cfg.DefaultOutbound))
	}
	for _, c := range cfg.Inbounds {
		inst, err := create(c)
		if err != nil {
			return fail(err)
		}
		if inst != nil {
			r.instances = append(r.instances, inst)
		}
	}
	for _, c := range cfg.Probes {
		inst, err := create(c)
		if err != nil {
			return fail(err)
		}
		if inst == nil {
			continue
		}
		probe, ok := inst.(netx.HealthProbe)
		if !ok {
			return fail(fmt.Errorf("plugin %s does not implement health probe", c.Plugin))
		}
		r.probes = append(r.probes, probe)
		r.instances = append(r.instances, inst)
	}
	for _, inst := range r.instances {
		if err := inst.Start(ctx); err != nil {
			return fail(err)
		}
	}
	r.mu.Lock()
	r.lastGood = cfg
	r.mu.Unlock()
	r.setState(Running)
	if r.bus != nil {
		r.bus.Publish(event.Event{Type: "realm.running", Data: map[string]any{"realm_id": cfg.ID}})
	}
	return nil
}
func (r *Runtime) stopStarted(ctx context.Context) {
	for i := len(r.instances) - 1; i >= 0; i-- {
		_ = r.instances[i].Stop(ctx)
	}
}
func (r *Runtime) Stop(ctx context.Context) error {
	r.mu.Lock()
	if r.state == Stopped || r.state == Created || r.state == Configured || r.state == Failed {
		r.mu.Unlock()
		return nil
	}
	r.state, r.running = Stopping, false
	instances := append([]plugin.Instance(nil), r.instances...)
	id := r.cfg.ID
	r.mu.Unlock()
	var first error
	for i := len(instances) - 1; i >= 0; i-- {
		if err := instances[i].Stop(ctx); err != nil && first == nil {
			first = err
		}
	}
	if r.bus != nil {
		r.bus.Publish(event.Event{Type: "realm.stopped", Data: map[string]any{"realm_id": id}})
	}
	r.setState(Stopped)
	return first
}

func (r *Runtime) Update(ctx context.Context, next Config) error {
	next = normalizeConfig(next)
	if err := r.ValidateConfig(next); err != nil {
		return err
	}
	r.mu.Lock()
	previous, wasRunning := r.cfg, r.running
	if next.Revision <= previous.Revision {
		next.Revision = previous.Revision + 1
	}
	r.revisions = append(r.revisions, previous)
	if len(r.revisions) > 10 {
		r.revisions = r.revisions[len(r.revisions)-10:]
	}
	r.cfg = next
	r.mu.Unlock()
	if !wasRunning {
		r.mu.Lock()
		r.lastGood = next
		r.mu.Unlock()
		r.setState(Configured)
		return nil
	}
	_ = r.Stop(ctx)
	if err := r.Start(ctx); err != nil {
		r.mu.Lock()
		r.cfg = previous
		r.mu.Unlock()
		if wasRunning {
			_ = r.Start(ctx)
		}
		return err
	}
	return nil
}
func (r *Runtime) Rollback(ctx context.Context) error {
	r.mu.Lock()
	if len(r.revisions) == 0 {
		r.mu.Unlock()
		return fmt.Errorf("no realm revision available")
	}
	previous := r.revisions[len(r.revisions)-1]
	r.revisions = r.revisions[:len(r.revisions)-1]
	r.mu.Unlock()
	return r.Update(ctx, previous)
}
