package graph

import (
	"meshvianex/core/health"
	"testing"
)

func p(name, dir string) Port { return Port{Name: name, Type: "flow", Direction: dir} }
func TestValidateRejectsCycle(t *testing.T) {
	cfg := Config{GraphID: "g", SchemaVersion: 1, Nodes: []Node{{ID: "a", Ports: []Port{p("in", "input"), p("out", "output")}}, {ID: "b", Ports: []Port{p("in", "input"), p("out", "output")}}}, Edges: []Edge{{ID: "ab", From: Endpoint{"a", "out"}, To: Endpoint{"b", "in"}}, {ID: "ba", From: Endpoint{"b", "out"}, To: Endpoint{"a", "in"}}}}
	if err := New(cfg, nil).Validate(); err == nil {
		t.Fatal("expected cycle validation error")
	}
}
func TestTracePrefersHealthyRoute(t *testing.T) {
	hs := health.NewStore()
	hs.Set("primary", 20)
	hs.Set("backup", 90)
	cfg := Config{GraphID: "g", SchemaVersion: 1, Nodes: []Node{{ID: "start", Ports: []Port{p("out", "output")}}, {ID: "primary", Ports: []Port{p("in", "input")}}, {ID: "backup", Ports: []Port{p("in", "input")}}}, Edges: []Edge{{ID: "p", From: Endpoint{"start", "out"}, To: Endpoint{"primary", "in"}, Priority: 0}, {ID: "b", From: Endpoint{"start", "out"}, To: Endpoint{"backup", "in"}, Priority: 1}}}
	e := New(cfg, hs)
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
	hops, err := e.Trace("start", "user_traffic")
	if err != nil {
		t.Fatal(err)
	}
	if len(hops) != 1 || hops[0].To != "backup" {
		t.Fatalf("unexpected trace: %#v", hops)
	}
}

func TestValidateAllowsCrossDomainTrafficDNSReversePath(t *testing.T) {
	cfg := Config{GraphID: "g", SchemaVersion: 1,
		Nodes: []Node{
			{ID: "a", Ports: []Port{p("in", "input"), p("out", "output"), p("dns", "output")}},
			{ID: "b", Ports: []Port{p("in", "input"), p("out", "output"), p("dns", "output")}},
			{ID: "internet", Ports: []Port{p("in", "input")}},
		},
		Edges: []Edge{
			{ID: "ab", From: Endpoint{"a", "out"}, To: Endpoint{"b", "in"}},
			{ID: "b-internet", From: Endpoint{"b", "out"}, To: Endpoint{"internet", "in"}},
			{ID: "b-dns-a", From: Endpoint{"b", "dns"}, To: Endpoint{"a", "in"}, Type: "control", When: Condition{Purpose: "dns"}},
		},
	}
	if err := New(cfg, nil).Validate(); err != nil {
		t.Fatalf("cross-domain traffic/DNS reverse path must be valid: %v", err)
	}
}

func TestValidateRejectsDNSCycle(t *testing.T) {
	cfg := Config{GraphID: "g", SchemaVersion: 1,
		Nodes: []Node{
			{ID: "a", Ports: []Port{p("in", "input"), p("dns", "output")}},
			{ID: "b", Ports: []Port{p("in", "input"), p("dns", "output")}},
		},
		Edges: []Edge{
			{ID: "a-dns-b", From: Endpoint{"a", "dns"}, To: Endpoint{"b", "in"}, Type: "control", When: Condition{Purpose: "dns"}},
			{ID: "b-dns-a", From: Endpoint{"b", "dns"}, To: Endpoint{"a", "in"}, Type: "control", When: Condition{Purpose: "dns"}},
		},
	}
	if err := New(cfg, nil).Validate(); err == nil {
		t.Fatal("expected DNS cycle validation error")
	}
}
