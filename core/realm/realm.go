package realm

import (
	"fmt"
	"meshvianex/core/event"
	"meshvianex/core/netx"
	"meshvianex/core/plugin"
)

func NewRuntime(cfg Config, registry *plugin.Registry, bus *event.Bus) *Runtime {
	cfg = normalizeConfig(cfg)
	state := Configured
	if cfg.ID == "" || cfg.Name == "" {
		state = Created
	}
	return &Runtime{cfg: cfg, registry: registry, bus: bus, outbounds: map[string]netx.Dialer{}, configOutbounds: map[string]netx.Dialer{}, transports: map[string]netx.Transport{}, protocols: map[string]netx.Protocol{}, protocolTransports: map[string]string{}, state: state}
}

func (r *Runtime) SetReferenceResolver(refs ReferenceResolver) {
	r.mu.Lock()
	r.refs = refs
	r.mu.Unlock()
}
func (r *Runtime) SetForwarder(forwarder netx.RealmForwarder) {
	r.mu.Lock()
	r.forwarder = forwarder
	r.mu.Unlock()
}
func (r *Runtime) ID() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cfg.ID
}
func (r *Runtime) Name() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cfg.Name
}
func (r *Runtime) Config() Config { r.mu.RLock(); defer r.mu.RUnlock(); return r.cfg }
func (r *Runtime) State() State   { r.mu.RLock(); defer r.mu.RUnlock(); return r.state }

func CanTransition(from, to State) bool {
	allowed := map[State][]State{
		Created: {Configured}, Configured: {ResolvingDependencies}, ResolvingDependencies: {Ready, Failed}, Ready: {Starting, Failed}, Starting: {Running, Degraded, Failed, Stopping}, Running: {Degraded, Stopping, Failed}, Degraded: {Running, Stopping, Failed}, Stopping: {Stopped, Failed}, Stopped: {ResolvingDependencies, Starting}, Failed: {ResolvingDependencies, Stopping},
	}
	for _, candidate := range allowed[from] {
		if candidate == to {
			return true
		}
	}
	return from == to
}

func (r *Runtime) Transition(to State) error {
	r.mu.Lock()
	if !CanTransition(r.state, to) {
		from := r.state
		r.mu.Unlock()
		return fmt.Errorf("%w: realm transition %s -> %s", plugin.ErrInvalidState, from, to)
	}
	r.state, r.running = to, to == Running || to == Degraded
	id := r.cfg.ID
	r.mu.Unlock()
	if r.bus != nil {
		r.bus.Publish(event.Event{Type: "realm." + string(to), Data: map[string]any{"realm_id": id, "state": to}})
	}
	return nil
}
func (r *Runtime) setState(state State) {
	r.mu.Lock()
	r.state = state
	r.running = state == Running || state == Degraded
	id := r.cfg.ID
	r.mu.Unlock()
	if r.bus != nil {
		r.bus.Publish(event.Event{Type: "realm." + string(state), Data: map[string]any{"realm_id": id, "state": state}})
	}
}
