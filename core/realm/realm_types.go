package realm

import (
	"encoding/json"
	"meshvianex/core/event"
	"meshvianex/core/netx"
	"meshvianex/core/plugin"
	"sync"
)

type ComponentRef struct {
	InstanceID string          `json:"instance_id"`
	Plugin     string          `json:"plugin"`
	Enabled    bool            `json:"enabled"`
	ConfigRef  string          `json:"config_ref,omitempty"`
	Protocol   string          `json:"protocol,omitempty"`
	Transport  string          `json:"transport,omitempty"`
	Stage      string          `json:"stage,omitempty"`
	Config     json.RawMessage `json:"config,omitempty"`
}

// ConfigSelection is the first-class egress selection component. Source
// discovery is owned by the persistent resource store; the configured plugin
// only selects among the stable IDs supplied by Runtime.
type ConfigSelection struct {
	InstanceID string          `json:"instance_id"`
	Plugin     string          `json:"plugin"`
	Enabled    bool            `json:"enabled"`
	SourceType string          `json:"source_type"`
	SourceRef  string          `json:"source_ref"`
	SourceRefs []string        `json:"source_refs,omitempty"`
	Config     json.RawMessage `json:"config,omitempty"`
}
type Port struct {
	Name string `json:"name"`
	Type string `json:"type"`
}
type PlaneConfig struct {
	Input    string `json:"input,omitempty"`
	Output   string `json:"output,omitempty"`
	Outbound string `json:"outbound,omitempty"`
}

type ReferenceResolver interface {
	ResolveConfig(string) (json.RawMessage, bool)
	HasSubscription(string) bool
}

// ConfigMetadataResolver is an optional extension implemented by the
// persistent resource store. The base resolver remains intentionally small so
// embedders can provide raw config references without mirroring resource
// metadata, while the runtime can reject a reference for the wrong plugin.
type ConfigMetadataResolver interface {
	ResolveConfigMetadata(string) (pluginID, pluginType string, active, ok bool)
}

// ConfigCandidateResolver exposes only the active stable IDs for a Realm
// source. It deliberately does not expose resource storage to selector code.
type ConfigCandidateResolver interface {
	ConfigCandidates(sourceType, sourceRef string) ([]string, error)
}
type State string

const (
	Created               State = "Created"
	Configured            State = "Configured"
	ResolvingDependencies State = "ResolvingDependencies"
	Ready                 State = "Ready"
	Starting              State = "Starting"
	Running               State = "Running"
	Degraded              State = "Degraded"
	Stopping              State = "Stopping"
	Stopped               State = "Stopped"
	Failed                State = "Failed"
)

type Config struct {
	ID               string           `json:"id"`
	Name             string           `json:"name"`
	SchemaVersion    int              `json:"schema_version,omitempty"`
	Version          int              `json:"version,omitempty"`
	Revision         int              `json:"revision,omitempty"`
	Inputs           []Port           `json:"inputs,omitempty"`
	Outputs          []Port           `json:"outputs,omitempty"`
	Routing          *ComponentRef    `json:"routing,omitempty"`
	Config           *ConfigSelection `json:"config,omitempty"`
	DNS              []ComponentRef   `json:"dns,omitempty"`
	Transforms       []ComponentRef   `json:"transforms,omitempty"`
	Protocols        []ComponentRef   `json:"protocols,omitempty"`
	Transports       []ComponentRef   `json:"transports,omitempty"`
	Outbounds        []ComponentRef   `json:"outbounds"`
	Inbounds         []ComponentRef   `json:"inbounds,omitempty"`
	Probes           []ComponentRef   `json:"probes,omitempty"`
	DefaultOutbound  string           `json:"default_outbound"`
	DefaultInput     string           `json:"default_input,omitempty"`
	DefaultOutput    string           `json:"default_output,omitempty"`
	DataPlane        *PlaneConfig     `json:"data_plane,omitempty"`
	ControlPlane     *PlaneConfig     `json:"control_plane,omitempty"`
	SubscriptionRefs []string         `json:"subscription_refs,omitempty"`
	ConfigRefs       []string         `json:"config_refs,omitempty"`
}

// UnmarshalJSON accepts the pre-release rule_engine field long enough to
// migrate persisted development state to the current routing terminology.
// Plugin identifiers are canonicalized at the same boundary so old state is
// never re-exposed to the runtime or UI after it has been loaded.
func (c *Config) UnmarshalJSON(data []byte) error {
	type configAlias Config
	var wire struct {
		configAlias
		RuleEngine *ComponentRef `json:"rule_engine,omitempty"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*c = Config(wire.configAlias)
	if c.Routing == nil && wire.RuleEngine != nil {
		c.Routing = wire.RuleEngine
	}
	*c = migrateConfig(*c)
	return nil
}

type Runtime struct {
	cfg                Config
	registry           *plugin.Registry
	bus                *event.Bus
	mu                 sync.RWMutex
	rule               netx.RuleEngine
	resolvers          []namedResolver
	outbounds          map[string]netx.Dialer
	transports         map[string]netx.Transport
	protocols          map[string]netx.Protocol
	protocolTransports map[string]string
	transforms         []transformBinding
	probes             []netx.HealthProbe
	instances          []plugin.Instance
	running            bool
	state              State
	revisions          []Config
	lastGood           Config
	refs               ReferenceResolver
	forwarder          netx.RealmForwarder
	selector           netx.ConfigSelector
	configOutbounds    map[string]netx.Dialer
}

type namedResolver struct {
	id    string
	value netx.Resolver
}

type transformBinding struct {
	id    string
	stage netx.TransformStage
	value netx.Transformer
}
