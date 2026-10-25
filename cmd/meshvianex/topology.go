package main

import (
	"fmt"
	"sort"
	"strings"

	"meshvianex/core/graph"
	"meshvianex/core/netx"
	"meshvianex/core/realm"
)

// executionTopologyForRealm combines the Realm-local execution topology with
// the current global Graph bindings. Keeping this composition on the backend
// makes the UI a renderer of runtime truth instead of a second architecture
// implementation.
func executionTopologyForRealm(cfg realm.Config, graphCfg graph.Config, realmNames map[string]string) realm.ExecutionTopology {
	topology := realm.BuildExecutionTopology(cfg)
	nodeIDs := make(map[string]bool, len(topology.Nodes))
	for _, node := range topology.Nodes {
		nodeIDs[node.ID] = true
	}
	edgeIDs := make(map[string]bool, len(topology.Edges))
	for _, edge := range topology.Edges {
		edgeIDs[edge.ID] = true
	}
	addNode := func(node realm.ExecutionNode) string {
		if node.ID == "" {
			return ""
		}
		if nodeIDs[node.ID] {
			return node.ID
		}
		nodeIDs[node.ID] = true
		topology.Nodes = append(topology.Nodes, node)
		return node.ID
	}
	addEdge := func(id, from, to, kind, label string) {
		if from == "" || to == "" || from == to {
			return
		}
		base := id
		if base == "" {
			base = "graph-" + sanitizeExecutionID(from) + "-" + sanitizeExecutionID(to)
		}
		id = base
		for suffix := 2; edgeIDs[id]; suffix++ {
			id = fmt.Sprintf("%s-%d", base, suffix)
		}
		edgeIDs[id] = true
		topology.Edges = append(topology.Edges, realm.ExecutionEdge{ID: id, From: from, To: to, Kind: kind, Label: label})
	}

	nodes := make(map[string]graph.Node, len(graphCfg.Nodes))
	var realmNode graph.Node
	for _, node := range graphCfg.Nodes {
		nodes[node.ID] = node
		if node.Type == "realm" && node.RealmID == cfg.ID {
			realmNode = node
		}
	}
	if realmNode.ID == "" {
		// A Realm not present in the active Graph is independent. Its configured
		// output ports terminate locally, which is exactly how Runtime behaves
		// when no graph forwarding context can resolve another Realm.
		for _, port := range normalizedExecutionOutputs(cfg) {
			addEdge("", "output:"+port.Name, "terminal:egress", "terminal", "independent Realm · local egress")
		}
		return topology
	}

	for _, edge := range graphCfg.Edges {
		if edge.To.Node != realmNode.ID {
			continue
		}
		source, ok := nodes[edge.From.Node]
		if !ok {
			continue
		}
		if source.Type == "source" {
			continue
		}
		if source.Type != "realm" {
			continue
		}
		isDNS := edge.From.Port == netx.DNSOutputPort || edge.When.Purpose == netx.DNSPurpose
		externalID := "graph:upstream:" + source.ID
		label, detail, kind := executionExternalNode(source, realmNames, true)
		if isDNS {
			externalID = "graph:dns-upstream:" + source.ID
			detail = "DNS handoff via input " + edge.To.Port + " · " + detail
			addNode(realm.ExecutionNode{ID: externalID, Kind: kind, Label: label, Detail: detail, Lane: "dns", Port: edge.To.Port})
			addEdge("graph-dns-in-"+edge.ID, externalID, "dns:request", "dns_handoff", graphEdgeExecutionLabel(edge))
			continue
		}
		addNode(realm.ExecutionNode{ID: externalID, Kind: kind, Label: label, Detail: detail, Lane: "external", Port: edge.From.Port})
		addEdge("graph-in-"+edge.ID, externalID, "input:"+edge.To.Port, "handoff", graphEdgeExecutionLabel(edge))
	}

	outgoingPorts := map[string]bool{}
	dnsHasGraphRoute := false
	dnsHandsOff := false
	for _, edge := range graphCfg.Edges {
		if edge.From.Node != realmNode.ID {
			continue
		}
		outgoingPorts[edge.From.Port] = true
		target, ok := nodes[edge.To.Node]
		if !ok {
			continue
		}
		label := graphEdgeExecutionLabel(edge)
		if edge.From.Port == netx.DNSOutputPort {
			dnsHasGraphRoute = true
			if target.Type == "sink" {
				markDNSLocalRoute(&topology, "dns_local", appendExecutionLabel(label, "Internet · local resolver"))
				continue
			}
			dnsHandsOff = true
			externalID := "graph:dns-downstream:" + target.ID
			nodeLabel, detail, kind := executionExternalNode(target, realmNames, false)
			addNode(realm.ExecutionNode{ID: externalID, Kind: kind, Label: nodeLabel, Detail: "DNS handoff · " + detail, Lane: "dns", Port: edge.To.Port})
			addEdge("graph-dns-out-"+edge.ID, "output:"+netx.DNSOutputPort, externalID, "dns_handoff", label)
			continue
		}
		if target.Type == "sink" {
			sinkName, _, _ := executionExternalNode(target, realmNames, false)
			if sinkName != "" {
				label = appendExecutionLabel(label, "terminal: "+sinkName)
			}
			addEdge("graph-out-"+edge.ID, "output:"+edge.From.Port, "terminal:egress", "terminal", label)
			continue
		}
		externalID := "graph:downstream:" + target.ID
		nodeLabel, detail, kind := executionExternalNode(target, realmNames, false)
		addNode(realm.ExecutionNode{ID: externalID, Kind: kind, Label: nodeLabel, Detail: detail, Lane: "external", Port: edge.To.Port})
		addEdge("graph-out-"+edge.ID, "output:"+edge.From.Port, externalID, "handoff", label)
	}
	if dnsHandsOff {
		markDNSLocalRoute(&topology, "dns_fallback", "local fallback if DNS handoff is unavailable")
	} else if !dnsHasGraphRoute {
		markDNSLocalRoute(&topology, "dns_local", "no DNS graph edge · local resolver")
	}
	for _, port := range normalizedExecutionOutputs(cfg) {
		if !outgoingPorts[port.Name] {
			addEdge("", "output:"+port.Name, "terminal:egress", "terminal", "no graph edge · local egress")
		}
	}
	return topology
}

func markDNSLocalRoute(topology *realm.ExecutionTopology, kind, label string) {
	if topology == nil {
		return
	}
	for index := range topology.Edges {
		edge := &topology.Edges[index]
		if edge.From == "output:"+netx.DNSOutputPort && (edge.Kind == "dns_local" || edge.Kind == "dns_fallback") {
			edge.Kind = kind
			edge.Label = label
			return
		}
	}
}

// ensureDNSGraphRoutes upgrades pre-release graphs to the dedicated DNS-egress
// model. Every Realm node exposes one logical DNS output and, unless the user
// selected another Realm, DNS terminates at the Internet sink. Edges sourced
// from the DNS port are canonical control edges so runtime and UI can identify
// them without relying on labels.
func ensureDNSGraphRoutes(cfg graph.Config, configs map[string]realm.Config) graph.Config {
	cfg = cloneGraphConfig(cfg)
	internetIndex := -1
	for index := range cfg.Nodes {
		if cfg.Nodes[index].ID == "internet" && cfg.Nodes[index].Type == "sink" {
			internetIndex = index
			break
		}
	}
	if internetIndex < 0 {
		cfg.Nodes = append(cfg.Nodes, graph.Node{ID: "internet", Type: "sink", Ports: []graph.Port{{Name: "in", Type: "flow", Direction: "input"}}})
		internetIndex = len(cfg.Nodes) - 1
	}
	hasInternetInput := false
	for _, port := range cfg.Nodes[internetIndex].Ports {
		if port.Name == "in" && port.Direction == "input" && port.Type == "flow" {
			hasInternetInput = true
			break
		}
	}
	if !hasInternetInput {
		cfg.Nodes[internetIndex].Ports = append(cfg.Nodes[internetIndex].Ports, graph.Port{Name: "in", Type: "flow", Direction: "input"})
	}

	edgeIDs := map[string]bool{}
	for index := range cfg.Edges {
		edgeIDs[cfg.Edges[index].ID] = true
		if cfg.Edges[index].From.Port == netx.DNSOutputPort {
			cfg.Edges[index].Type = "control"
			cfg.Edges[index].When.Purpose = netx.DNSPurpose
		}
	}
	for nodeIndex := range cfg.Nodes {
		node := &cfg.Nodes[nodeIndex]
		if node.Type != "realm" {
			continue
		}
		if node.RealmID != "" && configs != nil {
			if _, known := configs[node.RealmID]; !known {
				continue
			}
		}
		hasDNSPort := false
		for _, port := range node.Ports {
			if port.Name == netx.DNSOutputPort && port.Direction == "output" {
				hasDNSPort = true
				break
			}
		}
		if !hasDNSPort {
			node.Ports = append(node.Ports, graph.Port{Name: netx.DNSOutputPort, Type: "flow", Direction: "output"})
		}
		hasDNSRoute := false
		for _, edge := range cfg.Edges {
			if edge.From.Node == node.ID && edge.From.Port == netx.DNSOutputPort {
				hasDNSRoute = true
				break
			}
		}
		if hasDNSRoute {
			continue
		}
		base := "edge-" + node.ID + "-dns-internet"
		id := base
		for suffix := 2; edgeIDs[id]; suffix++ {
			id = fmt.Sprintf("%s-%d", base, suffix)
		}
		edgeIDs[id] = true
		cfg.Edges = append(cfg.Edges, graph.Edge{ID: id, From: graph.Endpoint{Node: node.ID, Port: netx.DNSOutputPort}, To: graph.Endpoint{Node: "internet", Port: "in"}, Type: "control", When: graph.Condition{Purpose: netx.DNSPurpose}})
	}
	return cfg
}

func executionExternalNode(node graph.Node, realmNames map[string]string, upstream bool) (label, detail, kind string) {
	switch node.Type {
	case "source":
		return "Local source", "Graph source · " + node.ID, "external_source"
	case "sink":
		if node.ID == "internet" {
			return "Internet", "Graph sink · internet", "external_sink"
		}
		return node.ID, "Graph sink", "external_sink"
	case "realm":
		name := realmNames[node.RealmID]
		if name == "" {
			name = node.RealmID
		}
		if upstream {
			return name, "Upstream Realm · " + node.RealmID, "external_realm"
		}
		return name, "Next Realm · " + node.RealmID, "external_realm"
	default:
		return node.ID, "Graph " + node.Type + " node", "external_graph"
	}
}

func graphEdgeExecutionLabel(edge graph.Edge) string {
	parts := []string{}
	if edge.Type != "" && edge.Type != "direct" {
		parts = append(parts, edge.Type)
	}
	if edge.When.Purpose != "" {
		parts = append(parts, "purpose="+edge.When.Purpose)
	}
	if edge.When.MinHealth > 0 {
		parts = append(parts, fmt.Sprintf("health>=%d", edge.When.MinHealth))
	}
	if edge.When.NetworkState != "" {
		parts = append(parts, "network="+edge.When.NetworkState)
	}
	if len(edge.When.Tags) > 0 {
		keys := make([]string, 0, len(edge.When.Tags))
		for key := range edge.When.Tags {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			parts = append(parts, "tag:"+key+"="+edge.When.Tags[key])
		}
	}
	if edge.Group != "" {
		parts = append(parts, "group="+edge.Group)
	}
	if edge.Priority != 0 {
		parts = append(parts, fmt.Sprintf("priority=%d", edge.Priority))
	}
	if edge.Weight != 0 {
		parts = append(parts, fmt.Sprintf("weight=%d", edge.Weight))
	}
	if len(parts) == 0 {
		return "graph"
	}
	return strings.Join(parts, " · ")
}

func appendExecutionLabel(current, value string) string {
	if current == "" {
		return value
	}
	if value == "" {
		return current
	}
	return current + " · " + value
}

func normalizedExecutionOutputs(cfg realm.Config) []realm.Port {
	if len(cfg.Outputs) > 0 {
		return cfg.Outputs
	}
	return []realm.Port{{Name: "out", Type: "flow"}}
}

func sanitizeExecutionID(value string) string {
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}
