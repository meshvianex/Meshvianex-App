package realm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"meshvianex/core/netx"
	"meshvianex/core/plugin"
	"meshvianex/plugins/direct"
	"meshvianex/plugins/http_inbound"
	"meshvianex/plugins/http_upstream"
	"meshvianex/plugins/raw_protocol"
	"meshvianex/plugins/socks5_inbound"
	"meshvianex/plugins/socks5_outbound"
	"meshvianex/plugins/tcp_transport"
)

type testFactory struct{}
type testInstance struct{}
type transformFactory struct{}
type transformInstance struct{ marker string }

var transformStageCalls chan netx.TransformStage

func (testFactory) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: "test.outbound", Name: "test", Version: "1.0.0", ABI: 1, PluginTypes: []string{"outbound"}, RequiresCapabilities: []string{"socket.tcp"}}
}
func (testFactory) Create(context.Context, plugin.InstanceContext, json.RawMessage) (plugin.Instance, error) {
	return testInstance{}, nil
}
func (testInstance) Start(context.Context) error { return nil }
func (testInstance) Stop(context.Context) error  { return nil }
func (testInstance) DialContext(context.Context, netx.Flow) (net.Conn, error) {
	a, b := net.Pipe()
	_ = b.Close()
	return a, nil
}

func (transformFactory) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: "test.transform", Name: "transform", Version: "1.0.0", ABI: 1, PluginTypes: []string{"outbound", "stream_transform"}, RequiresCapabilities: []string{"socket.tcp"}}
}
func (transformFactory) Create(_ context.Context, _ plugin.InstanceContext, raw json.RawMessage) (plugin.Instance, error) {
	var cfg struct {
		Marker string `json:"marker"`
	}
	_ = json.Unmarshal(raw, &cfg)
	return transformInstance{marker: cfg.Marker}, nil
}
func (transformInstance) Start(context.Context) error { return nil }
func (transformInstance) Stop(context.Context) error  { return nil }
func (transformInstance) DialContext(context.Context, netx.Flow) (net.Conn, error) {
	a, b := net.Pipe()
	_ = b.Close()
	return a, nil
}
func (i transformInstance) Transform(_ context.Context, stage netx.TransformStage, packet netx.Packet, _ netx.Flow) (netx.Packet, netx.TransformResult, error) {
	if transformStageCalls != nil {
		select {
		case transformStageCalls <- stage:
		default:
		}
	}
	packet.Metadata["order"] += i.marker
	if stage == netx.StreamData {
		packet.Payload = append(packet.Payload, []byte(i.marker)...)
	}
	return packet, netx.Continue, nil
}

func TestLifecycleAndValidation(t *testing.T) {
	r := plugin.NewRegistry(plugin.CapabilitySet{"socket.tcp": true})
	if err := r.Register(testFactory{}); err != nil {
		t.Fatal(err)
	}
	rt := NewRuntime(Config{ID: "r", Name: "Realm", Outbounds: []ComponentRef{{InstanceID: "direct", Plugin: "test.outbound", Enabled: true}}, DefaultOutbound: "direct"}, r, nil)
	if err := rt.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rt.State() != Running {
		t.Fatalf("state=%s", rt.State())
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rt.State() != Stopped {
		t.Fatalf("state=%s", rt.State())
	}
	if CanTransition(Stopped, Running) {
		t.Fatal("direct transition should be rejected")
	}
}

func TestTransformsRunInConfiguredOrderAndSkipDisabled(t *testing.T) {
	r := plugin.NewRegistry(plugin.CapabilitySet{"socket.tcp": true})
	if err := r.Register(transformFactory{}); err != nil {
		t.Fatal(err)
	}
	rt := NewRuntime(Config{ID: "r", Name: "Realm", Outbounds: []ComponentRef{{InstanceID: "direct", Plugin: "test.transform", Enabled: true}}, Transforms: []ComponentRef{{InstanceID: "first", Plugin: "test.transform", Enabled: true, Stage: string(netx.PreRoute), Config: json.RawMessage(`{"marker":"A"}`)}, {InstanceID: "second", Plugin: "test.transform", Enabled: true, Stage: string(netx.PreRoute), Config: json.RawMessage(`{"marker":"B"}`)}, {InstanceID: "disabled", Plugin: "test.transform", Enabled: false, Stage: string(netx.PreRoute), Config: json.RawMessage(`{"marker":"X"}`)}}, DefaultOutbound: "direct"}, r, nil)
	if err := rt.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer rt.Stop(context.Background())
	packet, result, err := rt.ApplyTransforms(context.Background(), netx.PreRoute, netx.Packet{Metadata: map[string]string{}}, netx.NewFlow("r", "tcp", "example.com:443", "user_traffic"))
	if err != nil || result != netx.Continue || packet.Metadata["order"] != "AB" {
		t.Fatalf("packet=%#v result=%q err=%v", packet, result, err)
	}
}

func TestEveryAdvertisedTransformStageIsExecutable(t *testing.T) {
	stages := []netx.TransformStage{netx.PreRoute, netx.PostRoute, netx.PreProtocol, netx.PostProtocol, netx.PreTransport, netx.PostTransport, netx.PreSocket, netx.StreamOpen, netx.Handshake, netx.StreamData, netx.StreamClose}
	registry := plugin.NewRegistry(plugin.CapabilitySet{"socket.tcp": true})
	if err := registry.Register(testFactory{}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(transformFactory{}); err != nil {
		t.Fatal(err)
	}
	transforms := make([]ComponentRef, 0, len(stages))
	for index, stage := range stages {
		transforms = append(transforms, ComponentRef{InstanceID: fmt.Sprintf("stage-%d", index), Plugin: "test.transform", Enabled: true, Stage: string(stage), Config: json.RawMessage(`{"marker":"x"}`)})
	}
	rt := NewRuntime(Config{ID: "stages", Name: "Stages", Outbounds: []ComponentRef{{InstanceID: "out", Plugin: "test.outbound", Enabled: true}}, DefaultOutbound: "out", Transforms: transforms}, registry, nil)
	if err := rt.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer rt.Stop(context.Background())
	for _, stage := range stages {
		transformStageCalls = make(chan netx.TransformStage, 1)
		_, result, err := rt.ApplyTransforms(context.Background(), stage, netx.Packet{Metadata: map[string]string{}}, netx.NewFlow("stages", "tcp", "example.test:443", "user_traffic"))
		if err != nil || result != netx.Continue {
			t.Fatalf("stage %s result=%s err=%v", stage, result, err)
		}
		select {
		case got := <-transformStageCalls:
			if got != stage {
				t.Fatalf("stage %s invoked transformer for %s", stage, got)
			}
		case <-time.After(time.Second):
			t.Fatalf("stage %s was accepted but not invoked", stage)
		}
	}
	transformStageCalls = nil
}

func TestRuntimeDirectDialAndStreamTransformsCarryPayload(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	received := make(chan string, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		payload := make([]byte, len("ping-A"))
		if _, readErr := io.ReadFull(conn, payload); readErr == nil {
			received <- string(payload)
			_, _ = conn.Write([]byte("pong"))
		}
	}()

	registry := plugin.NewRegistry(plugin.CapabilitySet{"socket.tcp": true})
	if err := registry.Register(testFactory{}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(direct.Factory{}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(transformFactory{}); err != nil {
		t.Fatal(err)
	}
	rt := NewRuntime(Config{ID: "direct", Name: "Direct", SchemaVersion: 1,
		Outbounds: []ComponentRef{{InstanceID: "out", Plugin: "io.meshvianex.direct", Enabled: true}}, DefaultOutbound: "out",
		Transforms: []ComponentRef{
			{InstanceID: "open", Plugin: "test.transform", Enabled: true, Stage: string(netx.StreamOpen), Config: json.RawMessage(`{"marker":"open"}`)},
			{InstanceID: "handshake", Plugin: "test.transform", Enabled: true, Stage: string(netx.Handshake), Config: json.RawMessage(`{"marker":"handshake"}`)},
			{InstanceID: "data", Plugin: "test.transform", Enabled: true, Stage: string(netx.StreamData), Config: json.RawMessage(`{"marker":"-A"}`)},
			{InstanceID: "close", Plugin: "test.transform", Enabled: true, Stage: string(netx.StreamClose), Config: json.RawMessage(`{"marker":"close"}`)},
		}}, registry, nil)
	transformStageCalls = make(chan netx.TransformStage, 8)
	defer func() { transformStageCalls = nil }()
	if err := rt.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer rt.Stop(context.Background())
	conn, err := rt.DialContext(context.Background(), netx.NewFlow("direct", "tcp", listener.Addr().String(), "user_traffic"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, len("pong-A"))
	if _, err = io.ReadFull(conn, response); err != nil {
		t.Fatal(err)
	}
	if string(response) != "pong-A" {
		t.Fatalf("response=%q", response)
	}
	_ = conn.Close()
	select {
	case payload := <-received:
		if payload != "ping-A" {
			t.Fatalf("payload=%q", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not receive transformed payload")
	}
	seen := map[netx.TransformStage]bool{}
	ordered := []netx.TransformStage{}
	for !seen[netx.StreamClose] {
		select {
		case stage := <-transformStageCalls:
			seen[stage] = true
			ordered = append(ordered, stage)
		case <-time.After(time.Second):
			t.Fatalf("transform stages=%v", seen)
		}
	}
	if !seen[netx.StreamOpen] || !seen[netx.Handshake] || !seen[netx.StreamData] || !seen[netx.StreamClose] {
		t.Fatalf("missing stream lifecycle stages: %v", seen)
	}
	openIndex, handshakeIndex, dataIndex := -1, -1, -1
	for index, stage := range ordered {
		switch stage {
		case netx.StreamOpen:
			if openIndex < 0 {
				openIndex = index
			}
		case netx.Handshake:
			if handshakeIndex < 0 {
				handshakeIndex = index
			}
		case netx.StreamData:
			if dataIndex < 0 {
				dataIndex = index
			}
		}
	}
	if !(openIndex < handshakeIndex && handshakeIndex < dataIndex) {
		t.Fatalf("unexpected transform order: %v", ordered)
	}
}

func TestRuntimeComposesRawProtocolOverTCPTransport(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(conn, conn)
	}()
	registry := plugin.NewRegistry(plugin.CapabilitySet{"socket.tcp": true, "stream.read": true, "stream.write": true})
	for _, factory := range []plugin.Factory{direct.Factory{}, tcp_transport.Factory{}, raw_protocol.Factory{}} {
		if err := registry.Register(factory); err != nil {
			t.Fatal(err)
		}
	}
	rt := NewRuntime(Config{ID: "stack", Name: "Stack", SchemaVersion: 1,
		Outbounds: []ComponentRef{{InstanceID: "fallback", Plugin: "io.meshvianex.direct", Enabled: true, Protocol: "raw", Transport: "tcp"}}, DefaultOutbound: "fallback",
		Transports: []ComponentRef{{InstanceID: "tcp", Plugin: "io.meshvianex.transport.raw", Enabled: true, Config: json.RawMessage(`{"no_delay":true}`)}},
		Protocols:  []ComponentRef{{InstanceID: "raw", Plugin: "io.meshvianex.protocol.passthrough", Enabled: true, Transport: "tcp"}}}, registry, nil)
	if err := rt.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer rt.Stop(context.Background())
	conn, err := rt.DialContext(context.Background(), netx.NewFlow("stack", "tcp", listener.Addr().String(), "user_traffic"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.Write([]byte("stack")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, len("stack"))
	if _, err = io.ReadFull(conn, response); err != nil || string(response) != "stack" {
		t.Fatalf("response=%q err=%v", response, err)
	}
}

func TestProtocolManifestMatchesRuntimeContract(t *testing.T) {
	for _, factory := range []plugin.Factory{http_upstream.Factory{}, socks5_outbound.Factory{}, http_inbound.Factory{}, socks5_inbound.Factory{}} {
		manifest := factory.Manifest()
		for _, typ := range manifest.PluginTypes {
			if typ == "protocol" || typ == "proxy_protocol" {
				t.Fatalf("%s incorrectly advertises a Realm protocol type: %v", manifest.ID, manifest.PluginTypes)
			}
		}
	}
	manifest := raw_protocol.Factory{}.Manifest()
	if len(manifest.PluginTypes) != 1 || manifest.PluginTypes[0] != "protocol" {
		t.Fatalf("raw protocol manifest=%v", manifest.PluginTypes)
	}
}

func TestProxyOutboundsCannotBeRealmProtocols(t *testing.T) {
	for _, factory := range []plugin.Factory{http_upstream.Factory{}, socks5_outbound.Factory{}} {
		registry := plugin.NewRegistry(plugin.CapabilitySet{"socket.tcp": true, "config.read": true})
		for _, candidate := range []plugin.Factory{direct.Factory{}, factory} {
			if err := registry.Register(candidate); err != nil {
				t.Fatal(err)
			}
		}
		rt := NewRuntime(Config{ID: "proxy-as-protocol", Name: "Invalid", Outbounds: []ComponentRef{{InstanceID: "out", Plugin: "io.meshvianex.direct", Enabled: true}}, Protocols: []ComponentRef{{InstanceID: "proxy", Plugin: factory.Manifest().ID, Enabled: true}}, DefaultOutbound: "out"}, registry, nil)
		if err := rt.ValidateConfig(rt.Config()); err == nil {
			t.Fatalf("proxy outbound %s was accepted as a Realm protocol", factory.Manifest().ID)
		}
	}
}

func TestConfigUnmarshalMigratesPreReleaseRoutingAndPluginIDs(t *testing.T) {
	var cfg Config
	raw := []byte(`{
		"id":"legacy",
		"name":"Legacy",
		"rule_engine":{"instance_id":"rules","plugin":"io.meshvianex.rules.basic","enabled":true,"config":{"default":"direct","rules":[]}},
		"protocols":[{"instance_id":"proto","plugin":"io.meshvianex.protocol.raw","enabled":true,"transport":"carrier"}],
		"transports":[{"instance_id":"carrier","plugin":"io.meshvianex.transport.tcp","enabled":true}],
		"outbounds":[{"instance_id":"direct","plugin":"io.meshvianex.direct","enabled":true,"protocol":"proto","transport":"carrier"}],
		"default_outbound":"direct"
	}`)
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Routing == nil || cfg.Routing.InstanceID != "rules" {
		t.Fatalf("legacy rule_engine was not migrated: %#v", cfg.Routing)
	}
	if got := cfg.Protocols[0].Plugin; got != "io.meshvianex.protocol.passthrough" {
		t.Fatalf("protocol plugin=%q", got)
	}
	if got := cfg.Transports[0].Plugin; got != "io.meshvianex.transport.raw" {
		t.Fatalf("transport plugin=%q", got)
	}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if strings.Contains(text, "rule_engine") || strings.Contains(text, "io.meshvianex.protocol.raw") || strings.Contains(text, "io.meshvianex.transport.tcp") {
		t.Fatalf("legacy identifiers survived re-encoding: %s", text)
	}
}

func TestBuildExecutionTopologyMatchesRuntimeComposition(t *testing.T) {
	cfg := Config{
		ID:       "topology",
		Name:     "Topology",
		Inbounds: []ComponentRef{{InstanceID: "http", Plugin: "inbound.test", Enabled: true}},
		Routing:  &ComponentRef{InstanceID: "rules", Plugin: "routing.test", Enabled: true},
		Config:   &ConfigSelection{InstanceID: "config", Plugin: "selector.test", Enabled: true, SourceType: "subscription", SourceRef: "sub-office"},
		DNS:      []ComponentRef{{InstanceID: "dns", Plugin: "dns.test", Enabled: true}},
		Transforms: []ComponentRef{
			{InstanceID: "before", Plugin: "transform.test", Enabled: true, Stage: string(netx.PreRoute)},
			{InstanceID: "after", Plugin: "transform.test", Enabled: true, Stage: string(netx.PostTransport)},
		},
		Protocols:  []ComponentRef{{InstanceID: "proto", Plugin: "protocol.test", Enabled: true, Transport: "raw"}},
		Transports: []ComponentRef{{InstanceID: "raw", Plugin: "transport.test", Enabled: true}},
		Outbounds: []ComponentRef{
			{InstanceID: "direct", Plugin: "outbound.direct", Enabled: true},
			{InstanceID: "secure", Plugin: "outbound.stack", Enabled: true, Protocol: "proto"},
		},
		DefaultOutbound: "direct",
	}
	topology := BuildExecutionTopology(cfg)
	nodes := map[string]ExecutionNode{}
	for _, node := range topology.Nodes {
		nodes[node.ID] = node
	}
	for _, id := range []string{
		"component:inbound:http",
		"input:in",
		"component:transform:before",
		"component:routing:rules",
		"component:config:config",
		"output:out",
		"terminal:egress",
		"component:outbound:direct",
		"component:outbound:secure",
		"component:protocol:proto",
		"component:transport:raw",
		"component:transform:after",
		"destination:network",
		"dns:request",
		"output:dns",
		"component:dns:dns",
		"dns:internet",
	} {
		if _, ok := nodes[id]; !ok {
			t.Fatalf("execution topology omitted %s: %#v", id, topology.Nodes)
		}
	}
	wantEdges := [][2]string{
		{"component:inbound:http", "input:in"},
		{"input:in", "component:transform:before"},
		{"component:transform:before", "component:routing:rules"},
		{"terminal:egress", "component:config:config"},
		{"component:config:config", "component:outbound:direct"},
		{"component:config:config", "component:outbound:secure"},
		{"component:outbound:secure", "component:protocol:proto"},
		{"component:protocol:proto", "component:transport:raw"},
		{"component:transport:raw", "component:transform:after"},
		{"component:outbound:direct", "component:transform:after"},
		{"component:transform:after", "destination:network"},
		{"dns:request", "output:dns"},
		{"output:dns", "component:dns:dns"},
		{"component:dns:dns", "dns:internet"},
	}
	for _, pair := range wantEdges {
		found := false
		for _, edge := range topology.Edges {
			if edge.From == pair[0] && edge.To == pair[1] {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("execution topology missing edge %s -> %s: %#v", pair[0], pair[1], topology.Edges)
		}
	}
	if len(topology.Services) != 0 {
		t.Fatalf("DNS is now a first-class execution branch; unexpected services: %#v", topology.Services)
	}
}

func TestBuildExecutionTopologyIncludesDisabledComponents(t *testing.T) {
	cfg := Config{
		ID:       "disabled-topology",
		Name:     "Disabled topology",
		Inbounds: []ComponentRef{{InstanceID: "inbound", Plugin: "inbound.test", Enabled: false}},
		Routing:  &ComponentRef{InstanceID: "routing", Plugin: "routing.test", Enabled: false},
		Config:   &ConfigSelection{InstanceID: "config", Plugin: "selector.test", Enabled: false, SourceType: "config", SourceRef: "config-main"},
		DNS:      []ComponentRef{{InstanceID: "dns", Plugin: "dns.test", Enabled: false}},
		Transforms: []ComponentRef{{
			InstanceID: "transform", Plugin: "transform.test", Enabled: false, Stage: string(netx.PreRoute),
		}},
		Protocols:  []ComponentRef{{InstanceID: "protocol", Plugin: "protocol.test", Enabled: false}},
		Transports: []ComponentRef{{InstanceID: "transport", Plugin: "transport.test", Enabled: false}},
		Outbounds:  []ComponentRef{{InstanceID: "outbound", Plugin: "outbound.test", Enabled: false}},
		Probes:     []ComponentRef{{InstanceID: "probe", Plugin: "probe.test", Enabled: false}},
	}

	topology := BuildExecutionTopology(cfg)
	nodes := make(map[string]ExecutionNode, len(topology.Nodes))
	for _, node := range topology.Nodes {
		nodes[node.ID] = node
	}
	for _, id := range []string{
		"component:inbound:inbound",
		"component:routing:routing",
		"component:config:config",
		"component:dns:dns",
		"component:transform:transform",
		"component:protocol:protocol",
		"component:transport:transport",
		"component:outbound:outbound",
	} {
		node, ok := nodes[id]
		if !ok {
			t.Fatalf("execution topology omitted disabled component %s: %#v", id, topology.Nodes)
		}
		if node.Enabled {
			t.Errorf("disabled component %s is marked enabled", id)
		}
	}
	if len(topology.Services) != 1 || topology.Services[0].InstanceID != "probe" || topology.Services[0].Enabled {
		t.Fatalf("disabled probe service was not preserved: %#v", topology.Services)
	}
}

func TestValidateRejectsProtocolStackWithoutTransport(t *testing.T) {
	registry := plugin.NewRegistry(plugin.CapabilitySet{"socket.tcp": true})
	for _, factory := range []plugin.Factory{direct.Factory{}, raw_protocol.Factory{}} {
		if err := registry.Register(factory); err != nil {
			t.Fatal(err)
		}
	}
	cfg := Config{
		ID:              "missing-transport",
		Name:            "Missing transport",
		Protocols:       []ComponentRef{{InstanceID: "proto", Plugin: "io.meshvianex.protocol.passthrough", Enabled: true}},
		Outbounds:       []ComponentRef{{InstanceID: "direct", Plugin: "io.meshvianex.direct", Enabled: true, Protocol: "proto"}},
		DefaultOutbound: "direct",
	}
	rt := NewRuntime(cfg, registry, nil)
	if err := rt.ValidateConfig(rt.Config()); err == nil || !strings.Contains(err.Error(), "requires an enabled transport binding") {
		t.Fatalf("expected protocol/transport validation error, got %v", err)
	}
}
