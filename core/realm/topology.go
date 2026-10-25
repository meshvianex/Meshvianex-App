package realm

import (
	"fmt"
	"sort"
	"strings"

	"meshvianex/core/netx"
)

// ExecutionTopology is a declarative view of the Realm data path. It is built
// from the same canonical Config consumed by Runtime.DialContext, so UIs and
// diagnostics do not need to recreate (and eventually drift from) runtime
// composition rules.
type ExecutionTopology struct {
	SchemaVersion int                `json:"schema_version"`
	Nodes         []ExecutionNode    `json:"nodes"`
	Edges         []ExecutionEdge    `json:"edges"`
	Services      []ExecutionService `json:"services,omitempty"`
}

type ExecutionNode struct {
	ID            string   `json:"id"`
	Kind          string   `json:"kind"`
	Label         string   `json:"label"`
	Detail        string   `json:"detail,omitempty"`
	Lane          string   `json:"lane,omitempty"`
	ComponentType string   `json:"component_type,omitempty"`
	InstanceID    string   `json:"instance_id,omitempty"`
	Plugin        string   `json:"plugin,omitempty"`
	Stage         string   `json:"stage,omitempty"`
	Port          string   `json:"port,omitempty"`
	SourceType    string   `json:"source_type,omitempty"`
	SourceRef     string   `json:"source_ref,omitempty"`
	SourceRefs    []string `json:"source_refs,omitempty"`
	Enabled       bool     `json:"enabled"`
}

type ExecutionEdge struct {
	ID    string `json:"id"`
	From  string `json:"from"`
	To    string `json:"to"`
	Kind  string `json:"kind,omitempty"`
	Label string `json:"label,omitempty"`
}

type ExecutionService struct {
	Kind       string `json:"kind"`
	InstanceID string `json:"instance_id"`
	Plugin     string `json:"plugin"`
	Detail     string `json:"detail,omitempty"`
	Enabled    bool   `json:"enabled"`
}

// BuildExecutionTopology describes the local Realm execution path. Inter-Realm
// Graph edges are deliberately added by the control plane because a Realm is
// reusable and does not own the global graph. Output port nodes are therefore
// left as graph boundaries and the terminal-egress node is present as the
// local fallback destination when the global graph terminates.
func BuildExecutionTopology(input Config) ExecutionTopology {
	cfg := normalizeConfig(input)
	topology := ExecutionTopology{SchemaVersion: 1}
	nodeIDs := map[string]bool{}
	edgeIDs := map[string]int{}

	addNode := func(node ExecutionNode) string {
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
	addEdge := func(from, to, kind, label string) {
		if from == "" || to == "" || from == to {
			return
		}
		base := "exec-" + sanitizeTopologyID(from) + "-" + sanitizeTopologyID(to)
		edgeIDs[base]++
		id := base
		if edgeIDs[base] > 1 {
			id = fmt.Sprintf("%s-%d", base, edgeIDs[base])
		}
		topology.Edges = append(topology.Edges, ExecutionEdge{ID: id, From: from, To: to, Kind: kind, Label: label})
	}
	componentNode := func(kind string, ref ComponentRef, label, detail, lane string) string {
		return addNode(ExecutionNode{
			ID:            "component:" + kind + ":" + ref.InstanceID,
			Kind:          "component",
			Label:         label,
			Detail:        detail,
			Lane:          lane,
			ComponentType: kind,
			InstanceID:    ref.InstanceID,
			Plugin:        ref.Plugin,
			Stage:         ref.Stage,
			Enabled:       ref.Enabled,
		})
	}

	// Realm input ports are real runtime boundaries. Inbounds that originate
	// user traffic enter through the data-plane/default input; graph-forwarded
	// traffic is attached to the exact target port by the control plane.
	inputIDs := make(map[string]string, len(cfg.Inputs))
	for _, port := range cfg.Inputs {
		id := addNode(ExecutionNode{ID: "input:" + port.Name, Kind: "input", Label: "Input · " + port.Name, Detail: port.Type + " port", Lane: "flow", Port: port.Name})
		inputIDs[port.Name] = id
	}
	outputIDs := make(map[string]string, len(cfg.Outputs))
	for _, port := range cfg.Outputs {
		id := addNode(ExecutionNode{ID: "output:" + port.Name, Kind: "output", Label: "Output · " + port.Name, Detail: port.Type + " port", Lane: "flow", Port: port.Name})
		outputIDs[port.Name] = id
	}

	dataInput := cfg.DefaultInput
	if cfg.DataPlane != nil && cfg.DataPlane.Input != "" {
		dataInput = cfg.DataPlane.Input
	}
	for _, ref := range cfg.Inbounds {
		id := componentNode("inbound", ref, "Inbound", ref.InstanceID, "ingress")
		addEdge(id, inputIDs[dataInput], "flow", "user traffic")
	}
	if cfg.ControlPlane != nil {
		controlInput := cfg.DefaultInput
		if cfg.ControlPlane.Input != "" {
			controlInput = cfg.ControlPlane.Input
		}
		id := addNode(ExecutionNode{ID: "source:control", Kind: "source", Label: "Control-plane traffic", Detail: "DNS, updates, probes and system work", Lane: "ingress"})
		addEdge(id, inputIDs[controlInput], "flow", "system traffic")
	}

	// Every input enters the same transform/routing pipeline. Transform stages
	// are emitted in exactly the order Runtime.DialContext executes them.
	currentInputs := make([]string, 0, len(cfg.Inputs))
	for _, port := range cfg.Inputs {
		currentInputs = append(currentInputs, inputIDs[port.Name])
	}
	chainStage := func(stage netx.TransformStage, from []string) []string {
		current := append([]string(nil), from...)
		for _, ref := range cfg.Transforms {
			if netx.TransformStage(ref.Stage) != stage {
				continue
			}
			id := componentNode("transform", ref, "Transform · "+string(stage), ref.InstanceID, "flow")
			for _, source := range current {
				addEdge(source, id, "flow", "")
			}
			current = []string{id}
		}
		return current
	}

	currentInputs = chainStage(netx.PreRoute, currentInputs)
	routingID := "decision:routing"
	if cfg.Routing != nil {
		routingID = componentNode("routing", *cfg.Routing, "Routing", cfg.Routing.InstanceID, "flow")
	} else {
		routingID = addNode(ExecutionNode{ID: routingID, Kind: "decision", Label: "Default route", Detail: "No routing plugin; plane/default outbound is used", Lane: "flow"})
	}
	for _, source := range currentInputs {
		addEdge(source, routingID, "flow", "")
	}
	current := []string{routingID}
	for _, stage := range []netx.TransformStage{netx.PostRoute, netx.PreProtocol, netx.PreTransport, netx.PreSocket} {
		current = chainStage(stage, current)
	}

	// Output port is selected by the active data/control plane before the graph
	// forwarder decides whether the flow is handed to another Realm or falls
	// through to local terminal egress.
	outputLabels := map[string][]string{}
	dataOutput := cfg.DefaultOutput
	if cfg.DataPlane != nil && cfg.DataPlane.Output != "" {
		dataOutput = cfg.DataPlane.Output
	}
	outputLabels[dataOutput] = append(outputLabels[dataOutput], "data")
	if cfg.ControlPlane != nil {
		controlOutput := cfg.DefaultOutput
		if cfg.ControlPlane.Output != "" {
			controlOutput = cfg.ControlPlane.Output
		}
		outputLabels[controlOutput] = append(outputLabels[controlOutput], "control")
	}
	for _, port := range cfg.Outputs {
		labels := outputLabels[port.Name]
		if len(labels) == 0 {
			continue
		}
		label := joinTopologyLabels(labels)
		for _, source := range current {
			addEdge(source, outputIDs[port.Name], "flow", label)
		}
	}

	terminalID := addNode(ExecutionNode{ID: "terminal:egress", Kind: "terminal", Label: "Terminal egress", Detail: "Used only when the Realm Graph does not hand off to another Realm", Lane: "egress"})
	configSelectionID := ""
	if cfg.Config != nil {
		sourceRefs := cfg.Config.SourceRefs
		if len(sourceRefs) == 0 && cfg.Config.SourceRef != "" {
			sourceRefs = []string{cfg.Config.SourceRef}
		}
		configSelectionID = addNode(ExecutionNode{ID: "component:config:" + cfg.Config.InstanceID, Kind: "component", Label: "Config", Detail: cfg.Config.SourceType + " · " + strings.Join(sourceRefs, ", "), Lane: "egress", ComponentType: "config", InstanceID: cfg.Config.InstanceID, Plugin: cfg.Config.Plugin, SourceType: cfg.Config.SourceType, SourceRef: cfg.Config.SourceRef, SourceRefs: append([]string(nil), sourceRefs...), Enabled: cfg.Config.Enabled})
		addEdge(terminalID, configSelectionID, "select", cfg.Config.SourceType)
	}

	// The routing decision names an outbound, but the selected local egress is
	// opened only after the Graph reaches a terminal endpoint. Every enabled
	// outbound is therefore represented as a possible terminal route target.
	protocolByID := map[string]ComponentRef{}
	for _, ref := range cfg.Protocols {
		protocolByID[ref.InstanceID] = ref
	}
	transportByID := map[string]ComponentRef{}
	for _, ref := range cfg.Transports {
		transportByID[ref.InstanceID] = ref
	}

	branchEnds := []string{}
	for _, outbound := range cfg.Outbounds {
		outboundID := componentNode("outbound", outbound, "Outbound", outbound.InstanceID, "egress")
		role := outboundRole(cfg, outbound.InstanceID)
		if configSelectionID != "" {
			addEdge(configSelectionID, outboundID, "route", role)
		} else {
			addEdge(terminalID, outboundID, "route", role)
		}
		end := outboundID
		protocolID := outbound.Protocol
		transportID := outbound.Transport
		if protocolID != "" {
			if protocol, ok := protocolByID[protocolID]; ok {
				protocolNode := componentNode("protocol", protocol, "Protocol", protocol.InstanceID, "egress")
				addEdge(end, protocolNode, "binding", "protocol")
				end = protocolNode
				if transportID == "" {
					transportID = protocol.Transport
				}
			}
		}
		if transportID != "" {
			if transport, ok := transportByID[transportID]; ok {
				transportNode := componentNode("transport", transport, "Stream transport", transport.InstanceID, "egress")
				addEdge(end, transportNode, "binding", "transport")
				end = transportNode
			}
		}
		branchEnds = append(branchEnds, end)
	}
	for _, protocol := range cfg.Protocols {
		componentNode("protocol", protocol, "Protocol", protocol.InstanceID, "egress")
	}
	for _, transport := range cfg.Transports {
		componentNode("transport", transport, "Stream transport", transport.InstanceID, "egress")
	}

	// Post-open transform stages run after the selected outbound stack has
	// established a connection. Multiple egress branches converge here.
	post := uniqueTopologyIDs(branchEnds)
	for _, stage := range []netx.TransformStage{netx.PostProtocol, netx.PostTransport, netx.StreamOpen, netx.Handshake, netx.StreamData, netx.StreamClose} {
		post = chainStage(stage, post)
	}
	destinationID := addNode(ExecutionNode{ID: "destination:network", Kind: "destination", Label: "Destination", Detail: "Remote network endpoint", Lane: "egress"})
	for _, source := range post {
		addEdge(source, destinationID, "flow", "")
	}
	if len(post) == 0 {
		addEdge(terminalID, destinationID, "flow", "no enabled outbound")
	}

	// DNS is a separate execution branch. Runtime.LookupHost enters this branch
	// and the global Graph decides whether the request terminates in this
	// Realm's resolver chain or is handed to another Realm. The local branch is
	// always represented because it is the terminal/fallback resolver path.
	dnsRequestID := addNode(ExecutionNode{ID: "dns:request", Kind: "dns_request", Label: "DNS request", Detail: "Realm name-resolution request", Lane: "dns"})
	dnsOutputID := addNode(ExecutionNode{ID: "output:" + netx.DNSOutputPort, Kind: "dns_output", Label: "DNS egress", Detail: "Graph-controlled DNS route", Lane: "dns", Port: netx.DNSOutputPort})
	addEdge(dnsRequestID, dnsOutputID, "dns", "lookup")
	dnsCurrent := dnsOutputID
	for _, ref := range cfg.DNS {
		dnsID := componentNode("dns", ref, "DNS", ref.InstanceID, "dns")
		addEdge(dnsCurrent, dnsID, "dns_local", "local resolver")
		dnsCurrent = dnsID
	}
	dnsDestinationID := addNode(ExecutionNode{ID: "dns:internet", Kind: "dns_destination", Label: "Internet (DNS)", Detail: "Local resolver upstream", Lane: "dns"})
	if dnsCurrent == dnsOutputID {
		addEdge(dnsCurrent, dnsDestinationID, "dns_local", "system resolver")
	} else {
		addEdge(dnsCurrent, dnsDestinationID, "dns_local", "resolver upstream")
	}

	// Probes remain service capabilities rather than serial packet-path nodes.
	for _, ref := range cfg.Probes {
		topology.Services = append(topology.Services, ExecutionService{Kind: "probe", InstanceID: ref.InstanceID, Plugin: ref.Plugin, Detail: "health/diagnostic service", Enabled: ref.Enabled})
	}

	return topology
}

func outboundRole(cfg Config, id string) string {
	roles := []string{}
	if id == cfg.DefaultOutbound {
		roles = append(roles, "default")
	}
	if cfg.DataPlane != nil && cfg.DataPlane.Outbound == id {
		roles = append(roles, "data plane")
	}
	if cfg.ControlPlane != nil && cfg.ControlPlane.Outbound == id {
		roles = append(roles, "control plane")
	}
	if len(roles) == 0 {
		roles = append(roles, "routing target")
	}
	return joinTopologyLabels(roles)
}

func uniqueTopologyIDs(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func joinTopologyLabels(values []string) string {
	values = uniqueTopologyIDs(values)
	sort.Strings(values)
	out := ""
	for i, value := range values {
		if i > 0 {
			out += " / "
		}
		out += value
	}
	return out
}

func sanitizeTopologyID(value string) string {
	out := make([]byte, 0, len(value))
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			out = append(out, c)
		} else {
			out = append(out, '-')
		}
	}
	return string(out)
}
