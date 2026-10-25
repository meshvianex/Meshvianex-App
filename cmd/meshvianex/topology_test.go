package main

import (
	"testing"

	"meshvianex/core/graph"
	"meshvianex/core/realm"
)

func TestExecutionTopologyUsesGlobalGraphAsRuntimeHandoffBoundary(t *testing.T) {
	a := realm.Config{ID: "a", Name: "A", Outbounds: []realm.ComponentRef{{InstanceID: "a-out", Plugin: "out", Enabled: true}}, DefaultOutbound: "a-out"}
	b := realm.Config{ID: "b", Name: "B", Outbounds: []realm.ComponentRef{{InstanceID: "b-out", Plugin: "out", Enabled: true}}, DefaultOutbound: "b-out"}
	cfg := chainGraphForRealms([]string{"a", "b"}, map[string]realm.Config{"a": a, "b": b}, "chain", 1)
	names := map[string]string{"a": "A", "b": "B"}

	aTopology := executionTopologyForRealm(a, cfg, names)
	if !executionHasEdge(aTopology, "output:out", "graph:downstream:realm-b") {
		t.Fatalf("Realm A must hand off to Realm B: %#v", aTopology.Edges)
	}
	if executionHasEdge(aTopology, "output:out", "terminal:egress") {
		t.Fatalf("Realm A incorrectly shows local terminal egress while Graph forwards to B: %#v", aTopology.Edges)
	}
	if executionHasEdge(aTopology, "graph:upstream:source", "input:in") {
		t.Fatalf("graph source edge is declarative and must not be presented as a runtime ingress path: %#v", aTopology.Edges)
	}

	bTopology := executionTopologyForRealm(b, cfg, names)
	if !executionHasEdge(bTopology, "graph:upstream:realm-a", "input:in") {
		t.Fatalf("Realm B must show Realm A as its graph input: %#v", bTopology.Edges)
	}
	if !executionHasEdge(bTopology, "output:out", "terminal:egress") {
		t.Fatalf("Realm B must show terminal egress when its Graph edge reaches the sink: %#v", bTopology.Edges)
	}
}

func TestExecutionTopologyIndependentRealmFallsThroughToLocalEgress(t *testing.T) {
	cfg := realm.Config{ID: "independent", Name: "Independent", Outbounds: []realm.ComponentRef{{InstanceID: "direct", Plugin: "out", Enabled: true}}, DefaultOutbound: "direct"}
	graphCfg := graph.Config{GraphID: "main", Name: "empty", SchemaVersion: 1, Revision: 1}
	topology := executionTopologyForRealm(cfg, graphCfg, map[string]string{"independent": "Independent"})
	if !executionHasEdge(topology, "output:out", "terminal:egress") {
		t.Fatalf("independent Realm must use local egress: %#v", topology.Edges)
	}
}

func TestExecutionTopologyDoesNotIncludeApplicationLocalSource(t *testing.T) {
	root := realm.Config{ID: "root", Name: "Root", Outbounds: []realm.ComponentRef{{InstanceID: "root-out", Plugin: "out", Enabled: true}}, DefaultOutbound: "root-out"}
	cfg := chainGraphForRealms([]string{"root"}, map[string]realm.Config{"root": root}, "chain", 1)
	topology := executionTopologyForRealm(root, cfg, map[string]string{"root": "Root"})
	for _, node := range topology.Nodes {
		if node.ID == "source:local-http-proxy" || node.Kind == "source" {
			t.Fatalf("Realm Graph must not contain the application Local source: %#v", topology.Nodes)
		}
	}
}

func executionHasEdge(topology realm.ExecutionTopology, from, to string) bool {
	for _, edge := range topology.Edges {
		if edge.From == from && edge.To == to {
			return true
		}
	}
	return false
}

func TestEnsureDNSGraphRoutesMigratesLegacyRealmNodes(t *testing.T) {
	legacy := graph.Config{GraphID: "legacy", Name: "legacy", SchemaVersion: 1, Revision: 1,
		Nodes: []graph.Node{
			{ID: "realm-a", Type: "realm", RealmID: "a", Ports: []graph.Port{{Name: "in", Type: "flow", Direction: "input"}, {Name: "out", Type: "flow", Direction: "output"}}},
			{ID: "internet", Type: "sink", Ports: []graph.Port{{Name: "in", Type: "flow", Direction: "input"}}},
		},
		Edges: []graph.Edge{{ID: "a-internet", From: graph.Endpoint{Node: "realm-a", Port: "out"}, To: graph.Endpoint{Node: "internet", Port: "in"}, Type: "direct"}},
	}
	migrated := ensureDNSGraphRoutes(legacy, map[string]realm.Config{"a": {ID: "a", Name: "A"}})
	var hasPort, hasRoute bool
	for _, node := range migrated.Nodes {
		if node.ID != "realm-a" {
			continue
		}
		for _, port := range node.Ports {
			if port.Name == "dns" && port.Direction == "output" {
				hasPort = true
			}
		}
	}
	for _, edge := range migrated.Edges {
		if edge.From.Node == "realm-a" && edge.From.Port == "dns" && edge.To.Node == "internet" && edge.When.Purpose == "dns" {
			hasRoute = true
		}
	}
	if !hasPort || !hasRoute {
		t.Fatalf("legacy graph was not upgraded with DNS egress: ports=%v route=%v cfg=%#v", hasPort, hasRoute, migrated)
	}
}

func TestExecutionTopologyShowsDNSHandoffAndLocalFallback(t *testing.T) {
	a := realm.Config{ID: "a", Name: "A", DNS: []realm.ComponentRef{{InstanceID: "a-dns", Plugin: "dns", Enabled: true}}, Outbounds: []realm.ComponentRef{{InstanceID: "a-out", Plugin: "out", Enabled: true}}, DefaultOutbound: "a-out"}
	b := realm.Config{ID: "b", Name: "B", DNS: []realm.ComponentRef{{InstanceID: "b-dns", Plugin: "dns", Enabled: true}}, Outbounds: []realm.ComponentRef{{InstanceID: "b-out", Plugin: "out", Enabled: true}}, DefaultOutbound: "b-out"}
	cfg := chainGraphForRealms([]string{"a", "b"}, map[string]realm.Config{"a": a, "b": b}, "chain", 1)
	for index := range cfg.Edges {
		if cfg.Edges[index].From.Node == "realm-a" && cfg.Edges[index].From.Port == "dns" {
			cfg.Edges[index].To = graph.Endpoint{Node: "realm-b", Port: "in"}
		}
	}
	names := map[string]string{"a": "A", "b": "B"}
	aTopology := executionTopologyForRealm(a, cfg, names)
	if !executionHasEdge(aTopology, "output:dns", "graph:dns-downstream:realm-b") {
		t.Fatalf("Realm A DNS route must hand off to Realm B: %#v", aTopology.Edges)
	}
	fallback := false
	for _, edge := range aTopology.Edges {
		if edge.From == "output:dns" && edge.To == "component:dns:a-dns" && edge.Kind == "dns_fallback" {
			fallback = true
		}
	}
	if !fallback {
		t.Fatalf("Realm A DNS component must remain visible as explicit local fallback: %#v", aTopology.Edges)
	}
	bTopology := executionTopologyForRealm(b, cfg, names)
	if !executionHasEdge(bTopology, "graph:dns-upstream:realm-a", "dns:request") {
		t.Fatalf("Realm B must show DNS handoff entering its DNS request branch: %#v", bTopology.Edges)
	}
}
