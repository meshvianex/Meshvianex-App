package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// CanonicalID maps pre-release plugin identifiers to their current public IDs.
// Keeping this compatibility shim at the registry boundary lets persisted
// development state survive identifier cleanups without exposing duplicate
// legacy plugins in the UI.
func CanonicalID(id string) string {
	switch id {
	case "io.meshvianex.protocol.raw":
		return "io.meshvianex.protocol.passthrough"
	case "io.meshvianex.transport.tcp":
		return "io.meshvianex.transport.raw"
	default:
		return id
	}
}

// CanonicalType maps pre-release plugin type names to their current public
// terminology.
func CanonicalType(pluginType string) string {
	if pluginType == "rule_engine" {
		return "routing"
	}
	return pluginType
}

var (
	ErrCapabilityDenied      = fmt.Errorf("ERR_CAPABILITY_DENIED")
	ErrCapabilityUnavailable = fmt.Errorf("ERR_CAPABILITY_UNAVAILABLE")
	ErrInvalidState          = fmt.Errorf("ERR_INVALID_STATE")
	ErrResourceLimit         = fmt.Errorf("ERR_RESOURCE_LIMIT")
	ErrDependencyCycle       = fmt.Errorf("ERR_DEPENDENCY_CYCLE")
)

type Manifest struct {
	ID                   string            `json:"id"`
	Name                 string            `json:"name"`
	Version              string            `json:"version"`
	PluginTypes          []string          `json:"plugin_types"`
	RequiresCapabilities []string          `json:"requires_capabilities,omitempty"`
	OptionalCapabilities []string          `json:"optional_capabilities,omitempty"`
	Provides             []string          `json:"provides,omitempty"`
	ABI                  int               `json:"abi"`
	RequiresAPI          map[string]string `json:"requires_api,omitempty"`
	OptionalAPI          map[string]string `json:"optional_api,omitempty"`
	Dependencies         []Dependency      `json:"dependencies,omitempty"`
	Runtime              RuntimeInfo       `json:"runtime,omitempty"`
	ConfigSchema         *ConfigSchema     `json:"config_schema,omitempty"`
}

type Dependency struct {
	Plugin   string `json:"plugin,omitempty"`
	Version  string `json:"version,omitempty"`
	Optional bool   `json:"optional,omitempty"`
}

type RuntimeInfo struct {
	Type  string `json:"type,omitempty"`
	Entry string `json:"entry,omitempty"`
}

type CapabilitySet map[string]bool

func (c CapabilitySet) Allows(capability string) bool { return c[capability] }

func (c CapabilitySet) Require(capability string) error {
	if !c.Allows(capability) {
		return fmt.Errorf("%w: %s", ErrCapabilityDenied, capability)
	}
	return nil
}

type InstanceContext struct {
	InstanceID   string
	RealmID      string
	Purpose      string
	Capabilities CapabilitySet
	Services     map[string]any
	Host         HostInfo
	Limits       ResourceLimits
	Budget       *Budget
}

type HostInfo struct {
	OS           string         `json:"os,omitempty"`
	Architecture string         `json:"arch,omitempty"`
	APIs         map[string]int `json:"apis,omitempty"`
}

type ResourceLimits struct {
	MemoryBytes  int64 `json:"memory_bytes,omitempty"`
	StorageBytes int64 `json:"storage_bytes,omitempty"`
	Sockets      int   `json:"sockets,omitempty"`
	Tasks        int   `json:"tasks,omitempty"`
}

type Budget struct {
	sockets chan struct{}
	tasks   chan struct{}
}

func NewBudget(l ResourceLimits) *Budget {
	b := &Budget{}
	if l.Sockets > 0 {
		b.sockets = make(chan struct{}, l.Sockets)
	}
	if l.Tasks > 0 {
		b.tasks = make(chan struct{}, l.Tasks)
	}
	return b
}
func (b *Budget) AcquireSocket(ctx context.Context) error {
	if b == nil || b.sockets == nil {
		return nil
	}
	select {
	case b.sockets <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("%w: socket budget", ErrResourceLimit)
	}
}
func (b *Budget) ReleaseSocket() {
	if b != nil && b.sockets != nil {
		select {
		case <-b.sockets:
		default:
		}
	}
}
func (b *Budget) AcquireTask(ctx context.Context) error {
	if b == nil || b.tasks == nil {
		return nil
	}
	select {
	case b.tasks <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("%w: task budget", ErrResourceLimit)
	}
}
func (b *Budget) ReleaseTask() {
	if b != nil && b.tasks != nil {
		select {
		case <-b.tasks:
		default:
		}
	}
}

type Instance interface {
	Start(context.Context) error
	Stop(context.Context) error
}

type Factory interface {
	Manifest() Manifest
	Create(context.Context, InstanceContext, json.RawMessage) (Instance, error)
}

type Registry struct {
	mu          sync.RWMutex
	factories   map[string]Factory
	enabled     map[string]bool
	grants      CapabilitySet
	unavailable CapabilitySet
	host        HostInfo
	limits      ResourceLimits
}

func NewRegistry(grants CapabilitySet) *Registry {
	cp := CapabilitySet{}
	for k, v := range grants {
		cp[k] = v
	}
	return &Registry{factories: map[string]Factory{}, enabled: map[string]bool{}, grants: cp}
}

func NewRegistryWithHost(grants CapabilitySet, host HostInfo, limits ResourceLimits) *Registry {
	r := NewRegistry(grants)
	r.host, r.limits = host, limits
	return r
}

func (r *Registry) SetUnavailable(capabilities CapabilitySet) {
	r.mu.Lock()
	r.unavailable = capabilities
	r.mu.Unlock()
}

func (r *Registry) ResolveDependencies(pluginID string) ([]Manifest, error) {
	pluginID = CanonicalID(pluginID)
	r.mu.RLock()
	defer r.mu.RUnlock()
	visiting, visited := map[string]bool{}, map[string]bool{}
	var resolve func(string) ([]Manifest, error)
	resolve = func(id string) ([]Manifest, error) {
		if visiting[id] {
			return nil, fmt.Errorf("%w: %s", ErrDependencyCycle, id)
		}
		if visited[id] {
			return nil, nil
		}
		f, ok := r.factories[id]
		if !ok {
			return nil, fmt.Errorf("unknown plugin dependency: %s", id)
		}
		if !r.enabled[id] {
			return nil, fmt.Errorf("plugin is disabled: %s", id)
		}
		visiting[id] = true
		out := []Manifest{}
		for _, dep := range f.Manifest().Dependencies {
			depFactory, exists := r.factories[dep.Plugin]
			if !exists {
				if dep.Optional {
					continue
				}
				return nil, fmt.Errorf("missing plugin dependency: %s", dep.Plugin)
			}
			if dep.Version != "" && !versionSatisfies(depFactory.Manifest().Version, dep.Version) {
				if dep.Optional {
					continue
				}
				return nil, fmt.Errorf("plugin %s does not satisfy dependency %s %s", dep.Plugin, depFactory.Manifest().Version, dep.Version)
			}
			nested, err := resolve(dep.Plugin)
			if err != nil {
				return nil, err
			}
			out = append(out, nested...)
			out = append(out, depFactory.Manifest())
		}
		delete(visiting, id)
		visited[id] = true
		return out, nil
	}
	return resolve(pluginID)
}

func (r *Registry) Register(f Factory) error {
	m := f.Manifest()
	if m.ID == "" {
		return fmt.Errorf("plugin id is required")
	}
	if m.ABI != 1 {
		return fmt.Errorf("plugin %s requires unsupported ABI %d", m.ID, m.ABI)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.enabled == nil {
		r.enabled = map[string]bool{}
	}
	if _, exists := r.factories[m.ID]; exists {
		return fmt.Errorf("plugin already registered: %s", m.ID)
	}
	r.factories[m.ID] = f
	r.enabled[m.ID] = true
	return nil
}

func (r *Registry) SetEnabled(id string, enabled bool) error {
	id = CanonicalID(id)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.factories[id]; !ok {
		return fmt.Errorf("unknown plugin: %s", id)
	}
	r.enabled[id] = enabled
	return nil
}

func (r *Registry) Enabled(id string) bool {
	id = CanonicalID(id)
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.enabled[id]
}

func (r *Registry) Manifest(id string) (Manifest, bool) {
	id = CanonicalID(id)
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.factories[id]
	if !ok {
		return Manifest{}, false
	}
	return f.Manifest(), true
}

func (r *Registry) ConfigSchema(id string) (ConfigSchema, bool) {
	id = CanonicalID(id)
	r.mu.RLock()
	f, ok := r.factories[id]
	r.mu.RUnlock()
	if !ok {
		return ConfigSchema{}, false
	}
	m := f.Manifest()
	if m.ConfigSchema != nil {
		return *m.ConfigSchema, true
	}
	provider, ok := f.(ConfigSchemaProvider)
	if !ok {
		return ConfigSchema{}, false
	}
	return provider.ConfigSchema(), true
}

func (r *Registry) List() []Manifest {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Manifest, 0, len(r.factories))
	for _, f := range r.factories {
		out = append(out, f.Manifest())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *Registry) Create(ctx context.Context, pluginID string, ictx InstanceContext, cfg json.RawMessage) (Instance, error) {
	pluginID = CanonicalID(pluginID)
	r.mu.RLock()
	f, ok := r.factories[pluginID]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown plugin: %s", pluginID)
	}
	if !r.Enabled(pluginID) {
		return nil, fmt.Errorf("plugin is disabled: %s", pluginID)
	}
	m := f.Manifest()
	if _, err := r.ResolveDependencies(pluginID); err != nil {
		return nil, err
	}
	for name, requirement := range m.RequiresAPI {
		supported := 1
		r.mu.RLock()
		if v, ok := r.host.APIs[name]; ok {
			supported = v
		}
		r.mu.RUnlock()
		if !apiRequirementSatisfied(requirement, supported) {
			return nil, fmt.Errorf("plugin %s: unsupported API %s %s", pluginID, name, requirement)
		}
	}
	granted := CapabilitySet{}
	for _, cap := range m.RequiresCapabilities {
		if r.unavailable.Allows(cap) {
			return nil, fmt.Errorf("plugin %s: %w: %s", pluginID, ErrCapabilityUnavailable, cap)
		}
		if !r.grants.Allows(cap) {
			return nil, fmt.Errorf("plugin %s: %w: %s", pluginID, ErrCapabilityDenied, cap)
		}
		granted[cap] = true
	}
	if ictx.Host.OS == "" {
		ictx.Host = r.host
	}
	if ictx.Limits == (ResourceLimits{}) {
		ictx.Limits = r.limits
	}
	if ictx.Budget == nil {
		ictx.Budget = NewBudget(ictx.Limits)
	}
	for _, cap := range m.OptionalCapabilities {
		if r.grants.Allows(cap) {
			granted[cap] = true
		}
	}
	ictx.Capabilities = granted
	if ictx.Services == nil {
		ictx.Services = map[string]any{}
	}
	return f.Create(ctx, ictx, cfg)
}

func versionSatisfies(actual, requirement string) bool {
	if requirement == "" {
		return true
	}
	actualParts := strings.Split(strings.TrimPrefix(actual, "v"), ".")
	parse := func(s string) int { n, _ := strconv.Atoi(strings.TrimSpace(s)); return n }
	av := [3]int{}
	for i := 0; i < len(actualParts) && i < 3; i++ {
		av[i] = parse(actualParts[i])
	}
	for _, term := range strings.Split(requirement, ",") {
		term = strings.TrimSpace(term)
		op := "="
		value := term
		for _, candidate := range []string{">=", "<=", ">", "<", "="} {
			if strings.HasPrefix(term, candidate) {
				op, value = candidate, strings.TrimSpace(strings.TrimPrefix(term, candidate))
				break
			}
		}
		parts := strings.Split(strings.TrimPrefix(value, "v"), ".")
		want := [3]int{}
		for i := 0; i < len(parts) && i < 3; i++ {
			want[i] = parse(parts[i])
		}
		cmp := 0
		for i := 0; i < 3; i++ {
			if av[i] < want[i] {
				cmp = -1
				break
			}
			if av[i] > want[i] {
				cmp = 1
				break
			}
		}
		if (op == "=" && cmp != 0) || (op == ">=" && cmp < 0) || (op == "<=" && cmp > 0) || (op == ">" && cmp <= 0) || (op == "<" && cmp >= 0) {
			return false
		}
	}
	return true
}

func apiRequirementSatisfied(requirement string, version int) bool {
	for _, term := range strings.Split(requirement, ",") {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		value := strings.TrimLeft(term, "<>= ")
		n, err := strconv.Atoi(value)
		if err != nil {
			return false
		}
		switch {
		case strings.HasPrefix(term, ">="):
			if version < n {
				return false
			}
		case strings.HasPrefix(term, ">"):
			if version <= n {
				return false
			}
		case strings.HasPrefix(term, "<="):
			if version > n {
				return false
			}
		case strings.HasPrefix(term, "<"):
			if version >= n {
				return false
			}
		default:
			if version != n {
				return false
			}
		}
	}
	return true
}
