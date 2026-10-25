package graph

import "testing"

func TestManagerCommitAndRollback(t *testing.T) {
	cfg := Config{GraphID: "g", SchemaVersion: 1, Revision: 1, Nodes: []Node{{ID: "a", Ports: []Port{p("out", "output")}}, {ID: "sink", Type: "sink", Ports: []Port{p("in", "input")}}}, Edges: []Edge{{ID: "e", From: Endpoint{"a", "out"}, To: Endpoint{"sink", "in"}}}}
	m, err := NewManager(cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	tx := m.Begin()
	tx.SetRevision(2)
	tx.AddNode(Node{ID: "unused", Type: "sink", Ports: []Port{p("in", "input")}})
	if err = m.Commit(tx); err != nil {
		t.Fatal(err)
	}
	if m.Config().Revision != 2 || m.State() != GraphActive {
		t.Fatalf("unexpected manager state: %#v %s", m.Config(), m.State())
	}
	if err = m.Rollback(); err != nil {
		t.Fatal(err)
	}
	if m.Config().Revision != 1 {
		t.Fatalf("rollback failed: %d", m.Config().Revision)
	}
}
