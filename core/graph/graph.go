package graph

import (
	"fmt"
	"hash/fnv"
	"sort"

	"meshvianex/core/health"
	"meshvianex/core/netx"
)

type Port struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Direction string `json:"direction"`
}
type Node struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	RealmID string `json:"realm_id,omitempty"`
	Ports   []Port `json:"ports"`
}
type Endpoint struct {
	Node string `json:"node"`
	Port string `json:"port"`
}
type Condition struct {
	Purpose      string            `json:"purpose,omitempty"`
	MinHealth    int               `json:"min_health,omitempty"`
	NetworkState string            `json:"network_state,omitempty"`
	Tags         map[string]string `json:"tags,omitempty"`
}
type Edge struct {
	ID       string    `json:"id"`
	From     Endpoint  `json:"from"`
	To       Endpoint  `json:"to"`
	Type     string    `json:"type"`
	Group    string    `json:"group,omitempty"`
	Priority int       `json:"priority,omitempty"`
	Weight   int       `json:"weight,omitempty"`
	When     Condition `json:"when,omitempty"`
}
type Config struct {
	GraphID       string `json:"graph_id"`
	Name          string `json:"name"`
	SchemaVersion int    `json:"schema_version"`
	Revision      int    `json:"revision"`
	Nodes         []Node `json:"nodes"`
	Edges         []Edge `json:"edges"`
}
type TraceHop struct {
	EdgeID   string `json:"edge_id"`
	From     string `json:"from"`
	FromPort string `json:"from_port"`
	To       string `json:"to"`
	ToPort   string `json:"to_port"`
}

type Engine struct {
	cfg       Config
	health    *health.Store
	nodes     map[string]Node
	edgesFrom map[string][]Edge
}

func (e *Engine) Config() Config { return e.cfg }

func New(cfg Config, hs *health.Store) *Engine {
	if hs == nil {
		hs = health.NewStore()
	}
	return &Engine{cfg: cfg, health: hs}
}

func (e *Engine) Validate() error {
	if e.cfg.GraphID == "" {
		return fmt.Errorf("graph_id is required")
	}
	if e.cfg.SchemaVersion != 1 {
		return fmt.Errorf("unsupported graph schema_version %d", e.cfg.SchemaVersion)
	}
	e.nodes = map[string]Node{}
	e.edgesFrom = map[string][]Edge{}
	for _, n := range e.cfg.Nodes {
		if n.ID == "" {
			return fmt.Errorf("node id required")
		}
		if _, ok := e.nodes[n.ID]; ok {
			return fmt.Errorf("duplicate node %s", n.ID)
		}
		e.nodes[n.ID] = n
	}
	ids := map[string]bool{}
	for _, ed := range e.cfg.Edges {
		if ed.ID == "" {
			return fmt.Errorf("edge id required")
		}
		if ed.Type == "" {
			ed.Type = "direct"
		}
		if ed.Type != "direct" && ed.Type != "conditional" && ed.Type != "failover" && ed.Type != "weighted" && ed.Type != "mirror" && ed.Type != "control" {
			return fmt.Errorf("edge %s has unsupported type %s", ed.ID, ed.Type)
		}
		if ids[ed.ID] {
			return fmt.Errorf("duplicate edge %s", ed.ID)
		}
		ids[ed.ID] = true
		fn, fok := e.nodes[ed.From.Node]
		tn, tok := e.nodes[ed.To.Node]
		if !fok || !tok {
			return fmt.Errorf("edge %s references unknown node", ed.ID)
		}
		fp, ok := findPort(fn, ed.From.Port)
		if !ok {
			return fmt.Errorf("edge %s source port missing", ed.ID)
		}
		tp, ok := findPort(tn, ed.To.Port)
		if !ok {
			return fmt.Errorf("edge %s target port missing", ed.ID)
		}
		if fp.Direction != "output" {
			return fmt.Errorf("edge %s source port must be output", ed.ID)
		}
		if tp.Direction != "input" {
			return fmt.Errorf("edge %s target port must be input", ed.ID)
		}
		if fp.Type != tp.Type {
			return fmt.Errorf("edge %s incompatible port types %s -> %s", ed.ID, fp.Type, tp.Type)
		}
		e.edgesFrom[ed.From.Node] = append(e.edgesFrom[ed.From.Node], ed)
	}
	// DNS egress is a separate routing domain from ordinary traffic. A
	// user-traffic edge A -> B and a DNS edge B -> A are not a runtime loop:
	// each traversal starts from a different Realm output. Validate cycles
	// independently so cross-domain topologies remain legal while true loops
	// inside either domain are still rejected.
	if err := validateAcyclicDomain(e.nodes, e.edgesFrom, false); err != nil {
		return err
	}
	if err := validateAcyclicDomain(e.nodes, e.edgesFrom, true); err != nil {
		return err
	}
	return nil
}

func isDNSEdge(edge Edge) bool {
	return edge.From.Port == netx.DNSOutputPort || edge.When.Purpose == netx.DNSPurpose
}

func validateAcyclicDomain(nodes map[string]Node, edgesFrom map[string][]Edge, dns bool) error {
	seen := map[string]int{}
	var visit func(string) error
	visit = func(nodeID string) error {
		if seen[nodeID] == 1 {
			domain := "traffic"
			if dns {
				domain = "DNS"
			}
			return fmt.Errorf("forbidden %s cycle detected at %s", domain, nodeID)
		}
		if seen[nodeID] == 2 {
			return nil
		}
		seen[nodeID] = 1
		for _, edge := range edgesFrom[nodeID] {
			if isDNSEdge(edge) != dns {
				continue
			}
			if err := visit(edge.To.Node); err != nil {
				return err
			}
		}
		seen[nodeID] = 2
		return nil
	}
	for id := range nodes {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

// ValidateReachability checks that graph sinks are not stranded. Graphs may
// intentionally contain reusable branches, so this is separate from the
// structural Validate call.
func (e *Engine) ValidateReachability() error {
	if e.nodes == nil {
		if err := e.Validate(); err != nil {
			return err
		}
	}
	visiting, reachable := map[string]bool{}, map[string]bool{}
	var reachesSink func(string) bool
	reachesSink = func(id string) bool {
		if e.nodes[id].Type == "sink" {
			return true
		}
		if reachable[id] {
			return true
		}
		if visiting[id] {
			return false
		}
		visiting[id] = true
		for _, edge := range e.edgesFrom[id] {
			if reachesSink(edge.To.Node) {
				delete(visiting, id)
				reachable[id] = true
				return true
			}
		}
		delete(visiting, id)
		return false
	}
	for id, node := range e.nodes {
		if node.Type == "sink" {
			continue
		}
		if len(e.edgesFrom[id]) == 0 || !reachesSink(id) {
			return fmt.Errorf("node %s has no reachable sink", id)
		}
	}
	return nil
}

func findPort(n Node, name string) (Port, bool) {
	for _, p := range n.Ports {
		if p.Name == name {
			return p, true
		}
	}
	return Port{}, false
}

func (e *Engine) Trace(start, purpose string) ([]TraceHop, error) {
	return e.trace(start, purpose, nil, "")
}

func (e *Engine) TraceFlow(flow netx.Flow) ([]TraceHop, error) {
	return e.trace(e.resolveStartNode(flow.RealmID), flow.Purpose, flow.Tags, flow.OutputPort)
}

// NextRealm returns the first Realm node selected after the current Realm.
// A blank result means the graph reached a terminal platform endpoint and the
// caller should use its local outbound dialer.
func (e *Engine) NextRealm(flow netx.Flow) (string, []TraceHop, error) {
	if e.nodes == nil {
		if err := e.Validate(); err != nil {
			return "", nil, err
		}
	}
	hops, err := e.TraceFlow(flow)
	if err != nil {
		return "", nil, err
	}
	for _, hop := range hops {
		node := e.nodes[hop.To]
		if node.Type == "realm" {
			if node.RealmID == "" {
				return "", hops, fmt.Errorf("graph node %s has no realm_id", node.ID)
			}
			return node.RealmID, hops, nil
		}
	}
	return "", hops, nil
}

// HasRealm reports whether the active graph contains a Realm node for the
// supplied runtime Realm ID. Independent Realms deliberately fall back to
// their local data/DNS egress instead of failing graph traversal.
func (e *Engine) HasRealm(realmID string) bool {
	if e.nodes == nil {
		if err := e.Validate(); err != nil {
			return false
		}
	}
	if node, ok := e.nodes[realmID]; ok && node.Type == "realm" {
		return node.RealmID == "" || node.RealmID == realmID
	}
	for _, node := range e.nodes {
		if node.Type == "realm" && node.RealmID == realmID {
			return true
		}
	}
	return false
}

func (e *Engine) resolveStartNode(realmID string) string {
	if _, ok := e.nodes[realmID]; ok {
		return realmID
	}
	for id, node := range e.nodes {
		if node.Type == "realm" && node.RealmID == realmID {
			return id
		}
	}
	return realmID
}

func (e *Engine) trace(start, purpose string, tags map[string]string, startPort string) ([]TraceHop, error) {
	if e.nodes == nil {
		if err := e.Validate(); err != nil {
			return nil, err
		}
	}
	if _, ok := e.nodes[start]; !ok {
		return nil, fmt.Errorf("unknown start node %s", start)
	}
	cur := start
	hops := []TraceHop{}
	visited := map[string]bool{}
	for {
		if visited[cur] {
			return nil, fmt.Errorf("runtime cycle at %s", cur)
		}
		visited[cur] = true
		candidates := append([]Edge(nil), e.edgesFrom[cur]...)
		if len(candidates) == 0 {
			return hops, nil
		}
		filtered := candidates[:0]
		for _, ed := range candidates {
			if cur == start && startPort != "" && ed.From.Port != startPort {
				continue
			}
			if ed.When.Purpose != "" && ed.When.Purpose != purpose {
				continue
			}
			if ed.When.MinHealth > 0 && e.health.Get(ed.To.Node).Score < ed.When.MinHealth {
				continue
			}
			if ed.When.NetworkState != "" && e.health.Get(ed.To.Node).Status != ed.When.NetworkState {
				continue
			}
			matchedTags := true
			for key, value := range ed.When.Tags {
				if tags == nil || tags[key] != value {
					matchedTags = false
					break
				}
			}
			if !matchedTags {
				continue
			}
			if status := e.health.Get(ed.To.Node).Status; status == "failed" || status == "stale" {
				continue
			}
			filtered = append(filtered, ed)
		}
		if len(filtered) == 0 {
			return nil, fmt.Errorf("no healthy route from %s", cur)
		}
		if selected, ok := weightedEdge(filtered, purpose); ok {
			filtered = []Edge{selected}
		}
		sort.SliceStable(filtered, func(i, j int) bool {
			if filtered[i].Priority == filtered[j].Priority {
				return e.health.Get(filtered[i].To.Node).Score > e.health.Get(filtered[j].To.Node).Score
			}
			return filtered[i].Priority < filtered[j].Priority
		})
		ed := filtered[0]
		hops = append(hops, TraceHop{EdgeID: ed.ID, From: ed.From.Node, FromPort: ed.From.Port, To: ed.To.Node, ToPort: ed.To.Port})
		cur = ed.To.Node
	}
}

func weightedEdge(edges []Edge, key string) (Edge, bool) {
	weighted := make([]Edge, 0)
	total := 0
	for _, edge := range edges {
		if edge.Type == "weighted" && edge.Weight > 0 {
			weighted = append(weighted, edge)
			total += edge.Weight
		}
	}
	if len(weighted) == 0 || total == 0 {
		return Edge{}, false
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	pick := int(h.Sum32() % uint32(total))
	for _, edge := range weighted {
		if pick < edge.Weight {
			return edge, true
		}
		pick -= edge.Weight
	}
	return weighted[len(weighted)-1], true
}
