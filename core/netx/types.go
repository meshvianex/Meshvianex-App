package netx

import (
	"context"
	"net"
	"strconv"
	"strings"
)

type Flow struct {
	ID         string            `json:"id"`
	RealmID    string            `json:"realm_id,omitempty"`
	Network    string            `json:"network"`
	Address    string            `json:"address"`
	Host       string            `json:"host"`
	Port       int               `json:"port"`
	Purpose    string            `json:"purpose,omitempty"`
	InputPort  string            `json:"input_port,omitempty"`
	OutputPort string            `json:"output_port,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
}

func NewFlow(realmID, network, address, purpose string) Flow {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		host = address
		portText = "0"
	}
	port, _ := strconv.Atoi(portText)
	return Flow{ID: NewFlowID(), RealmID: realmID, Network: network, Address: address, Host: strings.Trim(host, "[]"), Port: port, Purpose: purpose, Tags: map[string]string{}}
}

type RouteDecision struct {
	Outbound string `json:"outbound"`
	Reason   string `json:"reason,omitempty"`
}

type Dialer interface {
	DialContext(context.Context, Flow) (net.Conn, error)
}

type DialerFunc func(context.Context, Flow) (net.Conn, error)

func (f DialerFunc) DialContext(ctx context.Context, flow Flow) (net.Conn, error) {
	return f(ctx, flow)
}

// RealmForwarder is the protocol-agnostic seam used when a Realm edge points
// at another Realm. The local dialer is supplied by the current Realm and is
// used only when the graph reaches a terminal endpoint.
type RealmForwarder interface {
	Forward(context.Context, Flow, Dialer) (net.Conn, error)
}

type RuleEngine interface {
	Select(context.Context, Flow) (RouteDecision, error)
}

// ConfigSelector chooses one resource-backed outbound candidate for a flow.
// The Realm owns candidate discovery and lifecycle; selectors only receive
// stable candidate IDs and traffic metadata.
type ConfigSelector interface {
	Select(context.Context, Flow, []string) (string, error)
}

const (
	DNSPurpose    = "dns"
	DNSOutputPort = "dns"
)

type Resolver interface {
	LookupHost(context.Context, string) ([]string, error)
}

type ResolverFunc func(context.Context, string) ([]string, error)

func (f ResolverFunc) LookupHost(ctx context.Context, host string) ([]string, error) {
	return f(ctx, host)
}

// DNSQuery identifies the Realm DNS-egress boundary used by the graph. A DNS
// edge can terminate locally (the Realm's resolver chain) or hand the lookup
// to another Realm through its inbound boundary.
type DNSQuery struct {
	RealmID    string `json:"realm_id"`
	Host       string `json:"host"`
	OutputPort string `json:"output_port,omitempty"`
}

// DNSForwarder is the DNS analogue of RealmForwarder. It deliberately deals
// in resolver requests rather than raw DNS packets so resolver plugins remain
// protocol-agnostic while the Realm graph still owns inter-Realm DNS routing.
type DNSForwarder interface {
	ResolveHost(context.Context, DNSQuery, Resolver) ([]string, error)
}

// Transport is the Core contract for a lower-layer connection mechanic. A
// transport owns framing/connection setup but does not choose a route.
type Transport interface {
	OpenStream(context.Context, Flow) (Stream, error)
}

// Protocol composes a protocol session over a Core transport. Protocol
// implementations stay in plugins; Core only validates this small contract.
type Protocol interface {
	OpenSession(context.Context, Flow, StreamOpener) (Stream, error)
}

type ProbeResult struct {
	Status       string  `json:"status"`
	LatencyMS    float64 `json:"latency_ms,omitempty"`
	ResolvedHost string  `json:"resolved_host,omitempty"`
	Error        string  `json:"error,omitempty"`
}

// HealthProbe is the portable contract for a bounded connectivity diagnostic.
type HealthProbe interface {
	Probe(context.Context, Flow) ProbeResult
}
