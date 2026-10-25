package graph

import (
	"fmt"
	"sync"

	"meshvianex/core/event"
	"meshvianex/core/health"
)

type State string

const (
	GraphCreated   State = "Created"
	GraphValidated State = "Validated"
	GraphReady     State = "Ready"
	GraphActive    State = "Active"
	GraphDegraded  State = "Degraded"
	GraphStopped   State = "Stopped"
	GraphFailed    State = "Failed"
)

// Manager owns revisions and only swaps a validated Engine into the active
// slot. Readers therefore never observe a partially updated topology.
type Manager struct {
	mu       sync.RWMutex
	health   *health.Store
	bus      *event.Bus
	cfg      Config
	engine   *Engine
	state    State
	previous *Config
	lkg      *Config
}

func NewManager(cfg Config, hs *health.Store, bus *event.Bus) (*Manager, error) {
	m := &Manager{health: hs, bus: bus, cfg: cfg, state: GraphCreated}
	if err := m.activate(cfg); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) State() State    { m.mu.RLock(); defer m.mu.RUnlock(); return m.state }
func (m *Manager) Config() Config  { m.mu.RLock(); defer m.mu.RUnlock(); return m.cfg }
func (m *Manager) Engine() *Engine { m.mu.RLock(); defer m.mu.RUnlock(); return m.engine }

func (m *Manager) NewForwarder(bus *event.Bus) *Forwarder { return NewForwarder(m, bus) }

func (m *Manager) activate(cfg Config) error {
	e := New(cfg, m.health)
	if err := e.Validate(); err != nil {
		m.mu.Lock()
		m.state = GraphFailed
		m.mu.Unlock()
		return err
	}
	m.mu.Lock()
	if m.engine != nil {
		old := m.cfg
		m.previous = &old
	}
	m.cfg, m.engine, m.state = cfg, e, GraphActive
	good := cfg
	m.lkg = &good
	m.mu.Unlock()
	if m.bus != nil {
		m.bus.Publish(event.Event{Type: "graph.activated", Data: map[string]any{"graph_id": cfg.GraphID, "revision": cfg.Revision}})
	}
	return nil
}

type Transaction struct{ cfg Config }

func (t *Transaction) Config() Config       { return t.cfg }
func (t *Transaction) SetConfig(cfg Config) { t.cfg = cfg }
func (t *Transaction) AddNode(n Node)       { t.cfg.Nodes = append(t.cfg.Nodes, n) }
func (t *Transaction) AddEdge(e Edge)       { t.cfg.Edges = append(t.cfg.Edges, e) }
func (t *Transaction) RemoveEdge(id string) {
	out := t.cfg.Edges[:0]
	for _, e := range t.cfg.Edges {
		if e.ID != id {
			out = append(out, e)
		}
	}
	t.cfg.Edges = out
}
func (t *Transaction) RemoveNode(id string) {
	nodes := t.cfg.Nodes[:0]
	for _, n := range t.cfg.Nodes {
		if n.ID != id {
			nodes = append(nodes, n)
		}
	}
	t.cfg.Nodes = nodes
	edges := t.cfg.Edges[:0]
	for _, e := range t.cfg.Edges {
		if e.From.Node != id && e.To.Node != id {
			edges = append(edges, e)
		}
	}
	t.cfg.Edges = edges
}
func (t *Transaction) SetRevision(revision int) { t.cfg.Revision = revision }

func (m *Manager) Begin() *Transaction {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return &Transaction{cfg: m.cfg}
}
func (m *Manager) Commit(tx *Transaction) error {
	if tx == nil {
		return fmt.Errorf("nil graph transaction")
	}
	m.mu.RLock()
	revision := m.cfg.Revision
	m.mu.RUnlock()
	if tx.cfg.Revision <= revision {
		tx.cfg.Revision = revision + 1
	}
	return m.activate(tx.cfg)
}
func (m *Manager) Trace(start, purpose string) ([]TraceHop, error) {
	m.mu.RLock()
	e := m.engine
	m.mu.RUnlock()
	if e == nil {
		return nil, fmt.Errorf("graph is not active")
	}
	return e.Trace(start, purpose)
}
func (m *Manager) Rollback() error {
	m.mu.RLock()
	if m.previous == nil {
		m.mu.RUnlock()
		return fmt.Errorf("no graph revision available")
	}
	cfg := *m.previous
	m.mu.RUnlock()
	return m.activate(cfg)
}

func (m *Manager) Persist(store Store) error {
	m.mu.RLock()
	cfg := m.cfg
	lkg := m.cfg
	if m.lkg != nil {
		lkg = *m.lkg
	}
	m.mu.RUnlock()
	if err := store.Save(cfg); err != nil {
		return err
	}
	return store.SaveLastKnownGood(lkg)
}
