package realm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"meshvianex/core/netx"
	"sort"
)

func (r *Runtime) LookupHost(ctx context.Context, host string) ([]string, error) {
	r.mu.RLock()
	realmID := r.cfg.ID
	forwarder := r.forwarder
	r.mu.RUnlock()
	local := netx.ResolverFunc(r.lookupHostLocal)
	if dnsForwarder, ok := forwarder.(netx.DNSForwarder); ok {
		return dnsForwarder.ResolveHost(ctx, netx.DNSQuery{RealmID: realmID, Host: host, OutputPort: netx.DNSOutputPort}, local)
	}
	return local.LookupHost(ctx, host)
}

func (r *Runtime) lookupHostLocal(ctx context.Context, host string) ([]string, error) {
	r.mu.RLock()
	resolvers := append([]namedResolver(nil), r.resolvers...)
	r.mu.RUnlock()
	var errs []error
	for _, resolver := range resolvers {
		values, err := resolver.value.LookupHost(ctx, host)
		if err == nil && len(values) > 0 {
			return values, nil
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("resolver %s: %w", resolver.id, err))
		}
	}
	values, err := net.DefaultResolver.LookupHost(ctx, host)
	if err == nil {
		return values, nil
	}
	errs = append(errs, fmt.Errorf("system resolver: %w", err))
	return nil, errors.Join(errs...)
}

func (r *Runtime) RunProbes(ctx context.Context, flow netx.Flow) []netx.ProbeResult {
	r.mu.RLock()
	probes := append([]netx.HealthProbe(nil), r.probes...)
	r.mu.RUnlock()
	results := make([]netx.ProbeResult, 0, len(probes))
	for _, probe := range probes {
		results = append(results, probe.Probe(ctx, flow))
	}
	return results
}
func (r *Runtime) Status() map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	outs := make([]string, 0, len(r.outbounds))
	for k := range r.outbounds {
		outs = append(outs, k)
	}
	sort.Strings(outs)
	return map[string]any{"id": r.cfg.ID, "name": r.cfg.Name, "state": r.state, "running": r.running, "default_outbound": r.cfg.DefaultOutbound, "outbounds": outs, "revision": r.cfg.Revision}
}

func (r *Runtime) Persist(store Store) error {
	r.mu.RLock()
	cfg, good := r.cfg, r.lastGood
	r.mu.RUnlock()
	if err := store.Save(cfg); err != nil {
		return err
	}
	if good.ID != "" {
		return store.SaveLastKnownGood(good)
	}
	return nil
}
