package main

import (
	"fmt"
	"meshvianex/core/graph"
	"meshvianex/core/netx"
	"meshvianex/core/realm"
	"meshvianex/core/resources"
	"time"
)

func (c *controlPlane) chainIDs() []string {
	cfg := c.graph.Config()
	ids := make([]string, 0)
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, node := range cfg.Nodes {
		if node.Type == "realm" && node.RealmID != "" {
			if _, ok := c.realms[node.RealmID]; !ok {
				continue
			}
			ids = append(ids, node.RealmID)
		}
	}
	return ids
}

func (c *controlPlane) setChain(ids []string) error {
	c.mu.RLock()
	configs := make(map[string]realm.Config, len(ids))
	for _, id := range ids {
		rt, ok := c.realms[id]
		if !ok {
			c.mu.RUnlock()
			return fmt.Errorf("unknown realm in chain: %s", id)
		}
		configs[id] = rt.Config()
	}
	c.mu.RUnlock()
	current := c.graph.Config()
	tx := c.graph.Begin()
	tx.SetConfig(cloneGraphConfig(current))
	existing := append([]graph.Node(nil), tx.Config().Nodes...)
	for _, node := range existing {
		tx.RemoveNode(node.ID)
	}
	next := chainGraphForRealms(ids, configs, c.graph.Config().Name, c.graph.Config().Revision+1)
	for _, node := range next.Nodes {
		tx.AddNode(node)
	}
	for _, edge := range next.Edges {
		tx.AddEdge(edge)
	}
	if err := c.graph.Commit(tx); err != nil {
		return err
	}
	if err := c.persistState(); err != nil {
		_ = c.graph.Rollback()
		return err
	}
	return nil
}

func (c *controlPlane) validateGraphRealms(cfg graph.Config) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, node := range cfg.Nodes {
		if node.Type != "realm" || node.RealmID == "" {
			continue
		}
		if _, ok := c.realms[node.RealmID]; !ok {
			return fmt.Errorf("unknown realm in graph: %s", node.RealmID)
		}
	}
	return nil
}

func (c *controlPlane) record(message, detail, kind string) {
	message = resources.RedactString(message)
	detail = resources.RedactString(detail)
	c.mu.Lock()
	c.activity = append([]map[string]any{{"message": message, "detail": detail, "kind": kind, "time": time.Now().Format("15:04:05")}}, c.activity...)
	if len(c.activity) > 8 {
		c.activity = c.activity[:8]
	}
	c.mu.Unlock()
}

func chainGraphForRealms(ids []string, configs map[string]realm.Config, name string, revision int) graph.Config {
	flowIn := graph.Port{Name: "in", Type: "flow", Direction: "input"}
	flowOut := graph.Port{Name: "out", Type: "flow", Direction: "output"}
	nodes := []graph.Node{{ID: "source", Type: "source", Ports: []graph.Port{flowOut}}}
	inputPort, outputPort := map[string]string{}, map[string]string{}
	for _, id := range ids {
		cfg := configs[id]
		inputs, outputs := cfg.Inputs, cfg.Outputs
		if len(inputs) == 0 {
			inputs = []realm.Port{{Name: "in", Type: "flow"}}
		}
		if len(outputs) == 0 {
			outputs = []realm.Port{{Name: "out", Type: "flow"}}
		}
		in := cfg.DefaultInput
		if in == "" {
			in = inputs[0].Name
		}
		out := cfg.DefaultOutput
		if out == "" {
			out = outputs[0].Name
		}
		inputPort[id], outputPort[id] = in, out
		ports := make([]graph.Port, 0, len(inputs)+len(outputs)+1)
		for _, port := range inputs {
			ports = append(ports, graph.Port{Name: port.Name, Type: port.Type, Direction: "input"})
		}
		for _, port := range outputs {
			ports = append(ports, graph.Port{Name: port.Name, Type: port.Type, Direction: "output"})
		}
		ports = append(ports, graph.Port{Name: netx.DNSOutputPort, Type: "flow", Direction: "output"})
		nodes = append(nodes, graph.Node{ID: "realm-" + id, Type: "realm", RealmID: id, Ports: ports})
	}
	nodes = append(nodes, graph.Node{ID: "internet", Type: "sink", Ports: []graph.Port{flowIn}})
	edges := []graph.Edge{}
	fromNode, fromPort := "source", "out"
	for _, id := range ids {
		toNode, toPort := "realm-"+id, inputPort[id]
		edges = append(edges, graph.Edge{ID: "edge-" + fromNode + "-" + toNode, From: graph.Endpoint{Node: fromNode, Port: fromPort}, To: graph.Endpoint{Node: toNode, Port: toPort}, Type: "direct"})
		fromNode, fromPort = toNode, outputPort[id]
	}
	edges = append(edges, graph.Edge{ID: "edge-" + fromNode + "-internet", From: graph.Endpoint{Node: fromNode, Port: fromPort}, To: graph.Endpoint{Node: "internet", Port: "in"}, Type: "direct"})
	for _, id := range ids {
		edges = append(edges, graph.Edge{ID: "edge-realm-" + id + "-dns-internet", From: graph.Endpoint{Node: "realm-" + id, Port: netx.DNSOutputPort}, To: graph.Endpoint{Node: "internet", Port: "in"}, Type: "control", When: graph.Condition{Purpose: netx.DNSPurpose}})
	}
	return graph.Config{GraphID: "main", Name: name, SchemaVersion: 1, Revision: revision, Nodes: nodes, Edges: edges}
}

func chainGraph(ids []string, name string, revision int) graph.Config {
	flowIn := graph.Port{Name: "in", Type: "flow", Direction: "input"}
	flowOut := graph.Port{Name: "out", Type: "flow", Direction: "output"}
	nodes := []graph.Node{{ID: "source", Type: "source", Ports: []graph.Port{flowOut}}}
	for _, id := range ids {
		nodes = append(nodes, graph.Node{ID: "realm-" + id, Type: "realm", RealmID: id, Ports: []graph.Port{flowIn, flowOut, {Name: netx.DNSOutputPort, Type: "flow", Direction: "output"}}})
	}
	nodes = append(nodes, graph.Node{ID: "internet", Type: "sink", Ports: []graph.Port{flowIn}})
	edges := []graph.Edge{}
	from := "source"
	for _, id := range ids {
		to := "realm-" + id
		edges = append(edges, graph.Edge{ID: "edge-" + from + "-" + to, From: graph.Endpoint{Node: from, Port: "out"}, To: graph.Endpoint{Node: to, Port: "in"}, Type: "direct"})
		from = to
	}
	edges = append(edges, graph.Edge{ID: "edge-" + from + "-internet", From: graph.Endpoint{Node: from, Port: "out"}, To: graph.Endpoint{Node: "internet", Port: "in"}, Type: "direct"})
	for _, id := range ids {
		edges = append(edges, graph.Edge{ID: "edge-realm-" + id + "-dns-internet", From: graph.Endpoint{Node: "realm-" + id, Port: netx.DNSOutputPort}, To: graph.Endpoint{Node: "internet", Port: "in"}, Type: "control", When: graph.Condition{Purpose: netx.DNSPurpose}})
	}
	return graph.Config{GraphID: "main", Name: name, SchemaVersion: 1, Revision: revision, Nodes: nodes, Edges: edges}
}
