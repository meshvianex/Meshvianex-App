package graph

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"meshvianex/core/event"
	"meshvianex/core/health"
	"meshvianex/core/netx"
	"meshvianex/core/plugin"
	"meshvianex/core/realm"
	"meshvianex/plugins/direct"
)

func TestForwarderDelegatesPayloadAcrossTwoRealms(t *testing.T) {
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
		payload := make([]byte, len("ping"))
		if _, readErr := io.ReadFull(conn, payload); readErr == nil {
			received <- string(payload)
			_, _ = conn.Write([]byte("pong"))
		}
	}()

	registry := plugin.NewRegistry(plugin.CapabilitySet{"socket.tcp": true})
	if err := registry.Register(direct.Factory{}); err != nil {
		t.Fatal(err)
	}
	newRealm := func(id string) *realm.Runtime {
		return realm.NewRuntime(realm.Config{ID: id, Name: id, SchemaVersion: 1,
			Outbounds:       []realm.ComponentRef{{InstanceID: "direct", Plugin: "io.meshvianex.direct", Enabled: true}},
			DefaultOutbound: "direct"}, registry, nil)
	}
	a, b := newRealm("a"), newRealm("b")
	for _, rt := range []*realm.Runtime{a, b} {
		if err := rt.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer rt.Stop(context.Background())
	}
	bus := event.NewBus()
	manager, err := NewManager(Config{GraphID: "chain", Name: "chain", SchemaVersion: 1, Revision: 1,
		Nodes: []Node{
			{ID: "source", Type: "source", Ports: []Port{{Name: "out", Type: "flow", Direction: "output"}}},
			{ID: "realm-a", Type: "realm", RealmID: "a", Ports: []Port{{Name: "in", Type: "flow", Direction: "input"}, {Name: "out", Type: "flow", Direction: "output"}}},
			{ID: "realm-b", Type: "realm", RealmID: "b", Ports: []Port{{Name: "in", Type: "flow", Direction: "input"}, {Name: "out", Type: "flow", Direction: "output"}}},
			{ID: "sink", Type: "sink", Ports: []Port{{Name: "in", Type: "flow", Direction: "input"}}},
		}, Edges: []Edge{
			{ID: "source-a", From: Endpoint{Node: "source", Port: "out"}, To: Endpoint{Node: "realm-a", Port: "in"}, Type: "direct"},
			{ID: "a-b", From: Endpoint{Node: "realm-a", Port: "out"}, To: Endpoint{Node: "realm-b", Port: "in"}, Type: "direct"},
			{ID: "b-sink", From: Endpoint{Node: "realm-b", Port: "out"}, To: Endpoint{Node: "sink", Port: "in"}, Type: "direct"},
		}}, health.NewStore(), bus)
	if err != nil {
		t.Fatal(err)
	}
	forwarder := NewForwarder(manager, bus)
	if err := forwarder.RegisterRealm("a", a); err != nil {
		t.Fatal(err)
	}
	if err := forwarder.RegisterRealm("b", b); err != nil {
		t.Fatal(err)
	}
	a.SetForwarder(forwarder)
	b.SetForwarder(forwarder)
	events, unsubscribe := bus.Subscribe(16)
	defer unsubscribe()

	conn, err := a.DialContext(context.Background(), netx.NewFlow("a", "tcp", listener.Addr().String(), "user_traffic"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, len("pong"))
	if _, err = io.ReadFull(conn, response); err != nil {
		t.Fatal(err)
	}
	if string(response) != "pong" {
		t.Fatalf("response=%q", response)
	}
	select {
	case payload := <-received:
		if payload != "ping" {
			t.Fatalf("server payload=%q", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("local server did not receive payload")
	}
	forwardEvents := 0
	deadline := time.After(time.Second)
	for forwardEvents < 2 {
		select {
		case event := <-events:
			if event.Type == "realm.forward" {
				forwardEvents++
			}
		case <-deadline:
			t.Fatalf("expected forwarding events for both realms, got %d", forwardEvents)
		}
	}
}

func TestForwarderRejectsVisitedRealm(t *testing.T) {
	registry := plugin.NewRegistry(plugin.CapabilitySet{"socket.tcp": true})
	_ = registry.Register(direct.Factory{})
	manager, err := NewManager(Config{GraphID: "single", Name: "single", SchemaVersion: 1,
		Nodes: []Node{
			{ID: "realm-a", Type: "realm", RealmID: "a", Ports: []Port{{Name: "in", Type: "flow", Direction: "input"}, {Name: "out", Type: "flow", Direction: "output"}}},
			{ID: "sink", Type: "sink", Ports: []Port{{Name: "in", Type: "flow", Direction: "input"}}},
		}, Edges: []Edge{{ID: "a-sink", From: Endpoint{Node: "realm-a", Port: "out"}, To: Endpoint{Node: "sink", Port: "in"}, Type: "direct"}}}, health.NewStore(), nil)
	if err != nil {
		t.Fatal(err)
	}
	forwarder := NewForwarder(manager, nil)
	local := netx.DialerFunc(func(context.Context, netx.Flow) (net.Conn, error) { return nil, io.ErrClosedPipe })
	flow := netx.NewFlow("a", "tcp", "127.0.0.1:1", "user_traffic")
	flow.Tags["__meshvianex_forward_visited"] = "a"
	if _, err = forwarder.Forward(context.Background(), flow, local); err == nil {
		t.Fatal("expected visited realm cycle error")
	}
}

func TestForwarderHonorsHopLimitAndCancellation(t *testing.T) {
	manager, err := NewManager(Config{GraphID: "chain", Name: "chain", SchemaVersion: 1,
		Nodes: []Node{
			{ID: "realm-a", Type: "realm", RealmID: "a", Ports: []Port{{Name: "in", Type: "flow", Direction: "input"}, {Name: "out", Type: "flow", Direction: "output"}}},
			{ID: "realm-b", Type: "realm", RealmID: "b", Ports: []Port{{Name: "in", Type: "flow", Direction: "input"}, {Name: "out", Type: "flow", Direction: "output"}}},
			{ID: "sink", Type: "sink", Ports: []Port{{Name: "in", Type: "flow", Direction: "input"}}},
		}, Edges: []Edge{
			{ID: "a-b", From: Endpoint{Node: "realm-a", Port: "out"}, To: Endpoint{Node: "realm-b", Port: "in"}, Type: "direct"},
			{ID: "b-sink", From: Endpoint{Node: "realm-b", Port: "out"}, To: Endpoint{Node: "sink", Port: "in"}, Type: "direct"},
		}}, health.NewStore(), nil)
	if err != nil {
		t.Fatal(err)
	}
	forwarder := NewForwarder(manager, nil)
	local := netx.DialerFunc(func(context.Context, netx.Flow) (net.Conn, error) { return nil, io.ErrClosedPipe })
	forwarder.SetMaxHops(1)
	forwarder.RegisterRealm("b", netx.DialerFunc(func(ctx context.Context, flow netx.Flow) (net.Conn, error) {
		return forwarder.Forward(ctx, flow, local)
	}))
	if _, err := forwarder.Forward(context.Background(), netx.NewFlow("a", "tcp", "127.0.0.1:1", "user_traffic"), local); err == nil {
		t.Fatal("expected hop-limit error")
	}
	forwarder.SetMaxHops(defaultMaxHops)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := forwarder.Forward(ctx, netx.NewFlow("a", "tcp", "127.0.0.1:1", "user_traffic"), local); err != context.Canceled {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

type dnsResolverEndpoint struct {
	resolver netx.Resolver
}

func (e dnsResolverEndpoint) DialContext(context.Context, netx.Flow) (net.Conn, error) {
	return nil, io.ErrClosedPipe
}

func (e dnsResolverEndpoint) LookupHost(ctx context.Context, host string) ([]string, error) {
	return e.resolver.LookupHost(ctx, host)
}

func TestForwarderRoutesDNSToAnotherRealmResolver(t *testing.T) {
	manager, err := NewManager(Config{GraphID: "dns-chain", Name: "dns-chain", SchemaVersion: 1,
		Nodes: []Node{
			{ID: "realm-a", Type: "realm", RealmID: "a", Ports: []Port{{Name: "in", Type: "flow", Direction: "input"}, {Name: "out", Type: "flow", Direction: "output"}, {Name: netx.DNSOutputPort, Type: "flow", Direction: "output"}}},
			{ID: "realm-b", Type: "realm", RealmID: "b", Ports: []Port{{Name: "in", Type: "flow", Direction: "input"}, {Name: "out", Type: "flow", Direction: "output"}, {Name: netx.DNSOutputPort, Type: "flow", Direction: "output"}}},
			{ID: "internet", Type: "sink", Ports: []Port{{Name: "in", Type: "flow", Direction: "input"}}},
		}, Edges: []Edge{
			{ID: "a-dns-b", From: Endpoint{Node: "realm-a", Port: netx.DNSOutputPort}, To: Endpoint{Node: "realm-b", Port: "in"}, Type: "control", When: Condition{Purpose: netx.DNSPurpose}},
			{ID: "b-dns-internet", From: Endpoint{Node: "realm-b", Port: netx.DNSOutputPort}, To: Endpoint{Node: "internet", Port: "in"}, Type: "control", When: Condition{Purpose: netx.DNSPurpose}},
		}}, health.NewStore(), nil)
	if err != nil {
		t.Fatal(err)
	}
	forwarder := NewForwarder(manager, nil)
	localA := netx.ResolverFunc(func(context.Context, string) ([]string, error) {
		return []string{"192.0.2.10"}, nil
	})
	localB := netx.ResolverFunc(func(context.Context, string) ([]string, error) {
		return []string{"203.0.113.20"}, nil
	})
	b := dnsResolverEndpoint{resolver: netx.ResolverFunc(func(ctx context.Context, host string) ([]string, error) {
		return forwarder.ResolveHost(ctx, netx.DNSQuery{RealmID: "b", Host: host, OutputPort: netx.DNSOutputPort}, localB)
	})}
	if err := forwarder.RegisterRealm("b", b); err != nil {
		t.Fatal(err)
	}
	values, err := forwarder.ResolveHost(context.Background(), netx.DNSQuery{RealmID: "a", Host: "example.test", OutputPort: netx.DNSOutputPort}, localA)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0] != "203.0.113.20" {
		t.Fatalf("DNS lookup did not use Realm B resolver: %#v", values)
	}
}

func TestForwarderDNSInternetUsesLocalResolver(t *testing.T) {
	manager, err := NewManager(Config{GraphID: "dns-local", Name: "dns-local", SchemaVersion: 1,
		Nodes: []Node{
			{ID: "realm-a", Type: "realm", RealmID: "a", Ports: []Port{{Name: "in", Type: "flow", Direction: "input"}, {Name: netx.DNSOutputPort, Type: "flow", Direction: "output"}}},
			{ID: "internet", Type: "sink", Ports: []Port{{Name: "in", Type: "flow", Direction: "input"}}},
		}, Edges: []Edge{{ID: "a-dns-internet", From: Endpoint{Node: "realm-a", Port: netx.DNSOutputPort}, To: Endpoint{Node: "internet", Port: "in"}, Type: "control", When: Condition{Purpose: netx.DNSPurpose}}}}, health.NewStore(), nil)
	if err != nil {
		t.Fatal(err)
	}
	forwarder := NewForwarder(manager, nil)
	local := netx.ResolverFunc(func(context.Context, string) ([]string, error) {
		return []string{"198.51.100.7"}, nil
	})
	values, err := forwarder.ResolveHost(context.Background(), netx.DNSQuery{RealmID: "a", Host: "example.test"}, local)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0] != "198.51.100.7" {
		t.Fatalf("DNS Internet route did not terminate in local resolver: %#v", values)
	}
}

func TestForwarderDNSRealmUnavailableFallsBackToLocalResolver(t *testing.T) {
	manager, err := NewManager(Config{GraphID: "dns-fallback", Name: "dns-fallback", SchemaVersion: 1,
		Nodes: []Node{
			{ID: "realm-a", Type: "realm", RealmID: "a", Ports: []Port{{Name: "in", Type: "flow", Direction: "input"}, {Name: netx.DNSOutputPort, Type: "flow", Direction: "output"}}},
			{ID: "realm-b", Type: "realm", RealmID: "b", Ports: []Port{{Name: "in", Type: "flow", Direction: "input"}, {Name: netx.DNSOutputPort, Type: "flow", Direction: "output"}}},
		}, Edges: []Edge{{ID: "a-dns-b", From: Endpoint{Node: "realm-a", Port: netx.DNSOutputPort}, To: Endpoint{Node: "realm-b", Port: "in"}, Type: "control", When: Condition{Purpose: netx.DNSPurpose}}}}, health.NewStore(), nil)
	if err != nil {
		t.Fatal(err)
	}
	forwarder := NewForwarder(manager, nil)
	local := netx.ResolverFunc(func(context.Context, string) ([]string, error) {
		return []string{"192.0.2.55"}, nil
	})
	values, err := forwarder.ResolveHost(context.Background(), netx.DNSQuery{RealmID: "a", Host: "example.test"}, local)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0] != "192.0.2.55" {
		t.Fatalf("DNS Realm fallback did not use local resolver: %#v", values)
	}
}
