package graph

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"

	"meshvianex/core/event"
	"meshvianex/core/netx"
)

const (
	forwardVisitedTag = "__meshvianex_forward_visited"
	defaultMaxHops    = 16
)

// Forwarder turns validated Realm-to-Realm graph edges into net.Conn dial
// delegation. It knows only the normalized Flow/Dialer contracts; protocol
// details remain in the Realm's plugins.
type Forwarder struct {
	manager   *Manager
	bus       *event.Bus
	mu        sync.RWMutex
	realms    map[string]netx.Dialer
	resolvers map[string]netx.Resolver
	maxHop    int
}

func NewForwarder(manager *Manager, bus *event.Bus) *Forwarder {
	return &Forwarder{manager: manager, bus: bus, realms: map[string]netx.Dialer{}, resolvers: map[string]netx.Resolver{}, maxHop: defaultMaxHops}
}

func (f *Forwarder) SetMaxHops(max int) {
	if max <= 0 {
		max = defaultMaxHops
	}
	f.mu.Lock()
	f.maxHop = max
	f.mu.Unlock()
}

func (f *Forwarder) RegisterRealm(id string, dialer netx.Dialer) error {
	if strings.TrimSpace(id) == "" || dialer == nil {
		return fmt.Errorf("realm forwarder requires a realm id and dialer")
	}
	f.mu.Lock()
	f.realms[id] = dialer
	if resolver, ok := dialer.(netx.Resolver); ok {
		f.resolvers[id] = resolver
	}
	f.mu.Unlock()
	return nil
}

func (f *Forwarder) UnregisterRealm(id string) {
	f.mu.Lock()
	delete(f.realms, id)
	delete(f.resolvers, id)
	f.mu.Unlock()
}

func (f *Forwarder) Forward(ctx context.Context, flow netx.Flow, local netx.Dialer) (net.Conn, error) {
	if local == nil {
		return nil, fmt.Errorf("realm %s has no terminal dialer", flow.RealmID)
	}
	if f == nil || f.manager == nil || flow.RealmID == "" {
		return local.DialContext(ctx, cleanForwardFlow(flow))
	}
	visited := parseVisited(flow.Tags[forwardVisitedTag])
	f.mu.RLock()
	maxHop := f.maxHop
	f.mu.RUnlock()
	if visited[flow.RealmID] {
		return nil, fmt.Errorf("realm %s forwarding cycle detected", flow.RealmID)
	}
	if len(visited) >= maxHop {
		return nil, fmt.Errorf("realm %s forwarding exceeded max hops %d", flow.RealmID, maxHop)
	}
	visited[flow.RealmID] = true

	engine := f.manager.Engine()
	if engine == nil || !engine.HasRealm(flow.RealmID) {
		return local.DialContext(ctx, cleanForwardFlow(flow))
	}
	next, trace, err := engine.NextRealm(flow)
	if err != nil {
		return nil, fmt.Errorf("realm %s graph forwarding: %w", flow.RealmID, err)
	}
	f.publish(flow, next, trace)
	if next == "" {
		return local.DialContext(ctx, cleanForwardFlow(flow))
	}
	f.mu.RLock()
	nextDialer := f.realms[next]
	f.mu.RUnlock()
	if nextDialer == nil {
		return nil, fmt.Errorf("realm %s forwarding to unregistered realm %s", flow.RealmID, next)
	}
	flow.Tags = copyTags(flow.Tags)
	flow.Tags[forwardVisitedTag] = joinVisited(visited)
	for _, hop := range trace {
		node := engine.nodes[hop.To]
		if node.Type == "realm" && node.RealmID == next {
			flow.InputPort = hop.ToPort
			break
		}
	}
	flow.OutputPort = ""
	flow.RealmID = next
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nextDialer.DialContext(ctx, flow)
}

func (f *Forwarder) publish(flow netx.Flow, next string, trace []TraceHop) {
	if f.bus == nil {
		return
	}
	f.bus.Publish(event.Event{Type: "realm.forward", Data: map[string]any{
		"realm_id":   flow.RealmID,
		"next_realm": next,
		"flow_id":    flow.ID,
		"address":    flow.Address,
		"hops":       trace,
	}})
}

func parseVisited(value string) map[string]bool {
	visited := map[string]bool{}
	for _, id := range strings.Split(value, ",") {
		if id = strings.TrimSpace(id); id != "" {
			visited[id] = true
		}
	}
	return visited
}

func joinVisited(visited map[string]bool) string {
	ids := make([]string, 0, len(visited))
	for id := range visited {
		ids = append(ids, id)
	}
	// The visited set is used for detection, not route selection. Sorting keeps
	// event/debug output deterministic while preserving the same set semantics.
	sortStrings(ids)
	return strings.Join(ids, ",")
}

func copyTags(tags map[string]string) map[string]string {
	out := make(map[string]string, len(tags)+1)
	for key, value := range tags {
		out[key] = value
	}
	return out
}

func cleanForwardFlow(flow netx.Flow) netx.Flow {
	if flow.Tags == nil {
		return flow
	}
	flow.Tags = copyTags(flow.Tags)
	delete(flow.Tags, forwardVisitedTag)
	return flow
}

// Local string sorting keeps this file independent of graph route ordering
// helpers and avoids exposing the engine's internal edge slices.
func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

type dnsVisitedContextKey struct{}

// ResolveHost applies the graph's dedicated DNS egress. A DNS edge to the
// Internet means "resolve locally in this Realm"; an edge to another Realm
// delegates the lookup to that Realm's resolver context. The visited set is
// kept in context so recursive Realm DNS chains cannot loop indefinitely.
func (f *Forwarder) ResolveHost(ctx context.Context, query netx.DNSQuery, local netx.Resolver) ([]string, error) {
	if local == nil {
		return nil, fmt.Errorf("realm %s has no local DNS resolver", query.RealmID)
	}
	if f == nil || f.manager == nil || query.RealmID == "" {
		return local.LookupHost(ctx, query.Host)
	}
	if query.OutputPort == "" {
		query.OutputPort = netx.DNSOutputPort
	}
	engine := f.manager.Engine()
	if engine == nil || !engine.HasRealm(query.RealmID) {
		return local.LookupHost(ctx, query.Host)
	}

	visited := map[string]bool{}
	if existing, ok := ctx.Value(dnsVisitedContextKey{}).(map[string]bool); ok {
		for id, value := range existing {
			visited[id] = value
		}
	}
	f.mu.RLock()
	maxHop := f.maxHop
	f.mu.RUnlock()
	if visited[query.RealmID] {
		return nil, fmt.Errorf("realm %s DNS forwarding cycle detected", query.RealmID)
	}
	if len(visited) >= maxHop {
		return nil, fmt.Errorf("realm %s DNS forwarding exceeded max hops %d", query.RealmID, maxHop)
	}
	visited[query.RealmID] = true

	flow := netx.NewFlow(query.RealmID, "dns", query.Host, netx.DNSPurpose)
	flow.OutputPort = query.OutputPort
	next, trace, err := engine.NextRealm(flow)
	if err != nil {
		f.publishDNSFallback(query, "graph route unavailable", err)
		return local.LookupHost(ctx, query.Host)
	}
	f.publishDNS(query, next, trace)
	if next == "" {
		return local.LookupHost(ctx, query.Host)
	}
	f.mu.RLock()
	resolver := f.resolvers[next]
	f.mu.RUnlock()
	if resolver == nil {
		err := fmt.Errorf("realm %s DNS forwarding to unregistered Realm %s", query.RealmID, next)
		f.publishDNSFallback(query, "target Realm unavailable", err)
		return local.LookupHost(ctx, query.Host)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return resolver.LookupHost(context.WithValue(ctx, dnsVisitedContextKey{}, visited), query.Host)
}

func (f *Forwarder) publishDNSFallback(query netx.DNSQuery, reason string, routeErr error) {
	if f.bus == nil {
		return
	}
	data := map[string]any{
		"realm_id":    query.RealmID,
		"host":        query.Host,
		"output_port": query.OutputPort,
		"fallback":    "local",
		"reason":      reason,
	}
	if routeErr != nil {
		data["error"] = routeErr.Error()
	}
	f.bus.Publish(event.Event{Type: "dns.fallback", Data: data})
}

func (f *Forwarder) publishDNS(query netx.DNSQuery, next string, trace []TraceHop) {
	if f.bus == nil {
		return
	}
	f.bus.Publish(event.Event{Type: "dns.forward", Data: map[string]any{
		"realm_id":    query.RealmID,
		"next_realm":  next,
		"host":        query.Host,
		"output_port": query.OutputPort,
		"hops":        trace,
	}})
}
