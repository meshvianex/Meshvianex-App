package realm

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"meshvianex/core/event"
	"meshvianex/core/netx"
	"sort"
	"strings"
)

func (r *Runtime) DialContext(ctx context.Context, flow netx.Flow) (net.Conn, error) {
	r.mu.RLock()
	cfgSnapshot := r.cfg
	realmID := cfgSnapshot.ID
	controlPlane := cfgSnapshot.ControlPlane
	r.mu.RUnlock()
	if flow.RealmID == "" {
		flow.RealmID = realmID
	}
	plane := cfgSnapshot.DataPlane
	if flow.Purpose != "" && flow.Purpose != "user_traffic" && cfgSnapshot.ControlPlane != nil {
		plane = cfgSnapshot.ControlPlane
	}
	if flow.InputPort == "" {
		flow.InputPort = cfgSnapshot.DefaultInput
		if plane != nil && plane.Input != "" {
			flow.InputPort = plane.Input
		}
	}
	if flow.OutputPort == "" {
		flow.OutputPort = cfgSnapshot.DefaultOutput
		if plane != nil && plane.Output != "" {
			flow.OutputPort = plane.Output
		}
	}
	packet, result, err := r.ApplyTransforms(ctx, netx.PreRoute, netx.Packet{Metadata: map[string]string{}}, flow)
	if err != nil {
		return nil, err
	} else if result == netx.Drop {
		return nil, fmt.Errorf("flow %s dropped by pre_route transform", flow.ID)
	}
	defaultOutbound := r.selectOutbound(flow)
	choice := netx.RouteDecision{Outbound: defaultOutbound, Reason: "realm default"}
	controlLocked := flow.Purpose != "" && flow.Purpose != "user_traffic" && controlPlane != nil && controlPlane.Outbound != ""
	routingOverride := false
	r.mu.RLock()
	rule := r.rule
	r.mu.RUnlock()
	if rule != nil && !controlLocked {
		d, err := rule.Select(ctx, flow)
		if err != nil {
			return nil, err
		}
		if d.Outbound != "" {
			choice = d
			routingOverride = d.Outbound != defaultOutbound
		}
	}
	selectedConfigID := ""
	if !routingOverride {
		selectedConfigID, err = r.selectConfig(ctx, flow)
		if err != nil {
			return nil, err
		}
	}
	var selectedConfigDialer netx.Dialer
	if selectedConfigID != "" && !routingOverride {
		r.mu.RLock()
		selectedConfigDialer = r.configOutbounds[selectedConfigID]
		r.mu.RUnlock()
		if selectedConfigDialer == nil {
			return nil, fmt.Errorf("config selector returned unknown candidate %q", selectedConfigID)
		}
		choice = netx.RouteDecision{Outbound: selectedConfigID, Reason: "config selector"}
	}
	r.mu.RLock()
	dialer, ok := r.outbounds[choice.Outbound]
	r.mu.RUnlock()
	if !ok && selectedConfigDialer == nil {
		return nil, fmt.Errorf("route selected unknown outbound %q", choice.Outbound)
	}
	for _, stage := range []netx.TransformStage{netx.PostRoute, netx.PreProtocol, netx.PreTransport, netx.PreSocket} {
		var next netx.Packet
		next, result, err = r.ApplyTransforms(ctx, stage, packet, flow)
		if err != nil {
			return nil, err
		}
		if result == netx.Drop {
			return nil, fmt.Errorf("flow %s dropped by %s transform", flow.ID, stage)
		}
		packet = next
	}
	r.mu.RLock()
	cfg := r.cfg
	forwarder := r.forwarder
	r.mu.RUnlock()
	if r.bus != nil {
		r.bus.Publish(event.Event{Type: "flow.route", Data: map[string]any{"flow_id": flow.ID, "realm_id": cfg.ID, "address": flow.Address, "input_port": flow.InputPort, "output_port": flow.OutputPort, "outbound": choice.Outbound, "reason": choice.Reason}})
	}
	localDialer := netx.DialerFunc(func(openCtx context.Context, openFlow netx.Flow) (net.Conn, error) {
		if selectedConfigDialer != nil {
			return selectedConfigDialer.DialContext(openCtx, openFlow)
		}
		return r.openOutboundStream(openCtx, cfg, openFlow, choice.Outbound, dialer)
	})
	var conn net.Conn
	if forwarder != nil {
		conn, err = forwarder.Forward(ctx, flow, localDialer)
	} else {
		conn, err = localDialer.DialContext(ctx, flow)
	}
	if err != nil {
		return nil, err
	}
	for _, stage := range []netx.TransformStage{netx.PostProtocol, netx.PostTransport} {
		var next netx.Packet
		next, result, err = r.ApplyTransforms(ctx, stage, packet, flow)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		if result == netx.Drop {
			_ = conn.Close()
			return nil, fmt.Errorf("flow %s dropped by %s transform", flow.ID, stage)
		}
		packet = next
	}
	conn, err = r.wrapStream(ctx, flow, conn)
	if err != nil {
		return nil, err
	}
	return conn, nil
}
func (r *Runtime) selectOutbound(flow netx.Flow) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if flow.Purpose != "" && flow.Purpose != "user_traffic" && r.cfg.ControlPlane != nil && r.cfg.ControlPlane.Outbound != "" {
		return r.cfg.ControlPlane.Outbound
	}
	if (flow.Purpose == "" || flow.Purpose == "user_traffic") && r.cfg.DataPlane != nil && r.cfg.DataPlane.Outbound != "" {
		return r.cfg.DataPlane.Outbound
	}
	return r.cfg.DefaultOutbound
}

type configCandidate struct {
	id       string
	pluginID string
	config   json.RawMessage
}

func (r *Runtime) resolveConfigCandidates(selection ConfigSelection) ([]configCandidate, error) {
	r.mu.RLock()
	refs := r.refs
	r.mu.RUnlock()
	resolver, ok := refs.(ConfigCandidateResolver)
	if !ok {
		return nil, fmt.Errorf("config selector requires a resource store")
	}
	sourceRefs := selection.SourceRefs
	if len(sourceRefs) == 0 && strings.TrimSpace(selection.SourceRef) != "" {
		sourceRefs = []string{selection.SourceRef}
	}
	metadata, ok := refs.(ConfigMetadataResolver)
	if !ok {
		return nil, fmt.Errorf("config selector requires resource metadata")
	}
	candidates := make([]configCandidate, 0)
	seenCandidates := make(map[string]bool)
	for _, sourceRef := range sourceRefs {
		ids, err := resolver.ConfigCandidates(strings.ToLower(strings.TrimSpace(selection.SourceType)), strings.TrimSpace(sourceRef))
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			if seenCandidates[id] {
				continue
			}
			seenCandidates[id] = true
			raw, found := refs.ResolveConfig(id)
			if !found {
				continue
			}
			pluginID, pluginType, active, found := metadata.ResolveConfigMetadata(id)
			if !found || !active || pluginType != "outbound" {
				continue
			}
			candidates = append(candidates, configCandidate{id: id, pluginID: pluginID, config: append(json.RawMessage(nil), raw...)})
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("config selector sources %s have no runnable outbound candidates", strings.Join(sourceRefs, ", "))
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].id < candidates[j].id })
	return candidates, nil
}

func (r *Runtime) selectConfig(ctx context.Context, flow netx.Flow) (string, error) {
	r.mu.RLock()
	selector := r.selector
	candidates := make([]string, 0, len(r.configOutbounds))
	for id := range r.configOutbounds {
		candidates = append(candidates, id)
	}
	r.mu.RUnlock()
	if selector == nil {
		return "", nil
	}
	return selector.Select(ctx, flow, candidates)
}

func (r *Runtime) openOutboundStream(ctx context.Context, cfg Config, flow netx.Flow, outboundID string, fallback netx.Dialer) (net.Conn, error) {
	var selected *ComponentRef
	for index := range cfg.Outbounds {
		if cfg.Outbounds[index].InstanceID == outboundID {
			selected = &cfg.Outbounds[index]
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("selected outbound %q is not configured", outboundID)
	}
	protocolID, transportID := selected.Protocol, selected.Transport
	if protocolID == "" && transportID == "" {
		return fallback.DialContext(ctx, flow)
	}

	r.mu.RLock()
	protocols, transports := r.protocols, r.transports
	protocolTransports := r.protocolTransports
	r.mu.RUnlock()
	if protocolID != "" {
		protocol, ok := protocols[protocolID]
		if !ok {
			return nil, fmt.Errorf("outbound %s selected unknown protocol %q", outboundID, protocolID)
		}
		if transportID == "" {
			transportID = protocolTransports[protocolID]
		}
		transport, ok := transports[transportID]
		if !ok {
			return nil, fmt.Errorf("outbound %s protocol %s selected unknown transport %q", outboundID, protocolID, transportID)
		}
		stream, err := protocol.OpenSession(ctx, flow, transport)
		if err != nil {
			return nil, fmt.Errorf("outbound %s protocol %s: %w", outboundID, protocolID, err)
		}
		conn, ok := stream.(net.Conn)
		if !ok {
			_ = stream.Close()
			return nil, fmt.Errorf("outbound %s protocol %s returned a non-network stream", outboundID, protocolID)
		}
		return conn, nil
	}
	transport, ok := transports[transportID]
	if !ok {
		return nil, fmt.Errorf("outbound %s selected unknown transport %q", outboundID, transportID)
	}
	stream, err := transport.OpenStream(ctx, flow)
	if err != nil {
		return nil, fmt.Errorf("outbound %s transport %s: %w", outboundID, transportID, err)
	}
	conn, ok := stream.(net.Conn)
	if !ok {
		_ = stream.Close()
		return nil, fmt.Errorf("outbound %s transport %s returned a non-network stream", outboundID, transportID)
	}
	return conn, nil
}

func (r *Runtime) wrapStream(ctx context.Context, flow netx.Flow, conn net.Conn) (net.Conn, error) {
	if !r.hasTransforms(netx.StreamOpen, netx.Handshake, netx.StreamData, netx.StreamClose) {
		return conn, nil
	}
	packet, result, err := r.ApplyTransforms(ctx, netx.StreamOpen, netx.Packet{Metadata: map[string]string{}}, flow)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if result == netx.Drop {
		_ = conn.Close()
		return nil, fmt.Errorf("flow %s dropped by stream_open transform", flow.ID)
	}
	return &transformedConn{Conn: conn, runtime: r, ctx: ctx, flow: flow, metadata: packet.Metadata}, nil
}
