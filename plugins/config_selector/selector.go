package config_selector

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"meshvianex/core/netx"
	"meshvianex/core/plugin"
)

const pluginID = "io.meshvianex.config.selector.basic"

const (
	RoundRobin       = "round_robin"
	ConsistentHash   = "consistent_hash"
	StickySession    = "sticky_session"
	defaultStickyTTL = 30 * time.Minute
	defaultStickyMax = 1024
)

type Config struct {
	Strategy         string `json:"strategy"`
	StickyTTLSeconds int    `json:"sticky_ttl_seconds,omitempty"`
	StickyMaxEntries int    `json:"sticky_max_entries,omitempty"`
}

type Factory struct{}

func (Factory) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:          pluginID,
		Name:        "Traffic Config Selector",
		Version:     "0.1.0",
		PluginTypes: []string{"config_selector"},
		Provides:    []string{"config_selector.round_robin", "config_selector.consistent_hash", "config_selector.sticky_session"},
		ABI:         1,
		ConfigSchema: &plugin.ConfigSchema{
			Title:       "Traffic selection",
			Description: "Selects an active Config resource without reading the resource store.",
			Fields: []plugin.ConfigField{
				{ID: "strategy", Type: "select", Label: "Strategy", Required: true, Default: RoundRobin, Options: []plugin.ConfigOption{
					{Value: RoundRobin, Label: "Round Robin"},
					{Value: ConsistentHash, Label: "Consistent Hash"},
					{Value: StickySession, Label: "Sticky Session"},
				}},
				{ID: "sticky_ttl_seconds", Type: "number", Label: "Sticky TTL (seconds)", Default: int(defaultStickyTTL / time.Second)},
				{ID: "sticky_max_entries", Type: "number", Label: "Sticky session limit", Default: defaultStickyMax},
			},
		},
	}
}

func (Factory) Create(_ context.Context, _ plugin.InstanceContext, raw json.RawMessage) (plugin.Instance, error) {
	var cfg Config
	if len(raw) != 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("config selector: %w", err)
		}
	}
	if cfg.Strategy == "" {
		cfg.Strategy = RoundRobin
	}
	var selector netx.ConfigSelector
	switch cfg.Strategy {
	case RoundRobin:
		selector = &roundRobinSelector{}
	case ConsistentHash:
		selector = consistentHashSelector{}
	case StickySession:
		ttl := defaultStickyTTL
		if cfg.StickyTTLSeconds > 0 {
			ttl = time.Duration(cfg.StickyTTLSeconds) * time.Second
		}
		maxEntries := cfg.StickyMaxEntries
		if maxEntries <= 0 {
			maxEntries = defaultStickyMax
		}
		selector = newStickySelector(time.Now, ttl, maxEntries)
	default:
		return nil, fmt.Errorf("config selector: unsupported strategy %q", cfg.Strategy)
	}
	return &Instance{selector: selector}, nil
}

type Instance struct {
	selector netx.ConfigSelector
}

func (i *Instance) Start(context.Context) error { return nil }
func (i *Instance) Stop(context.Context) error  { return nil }
func (i *Instance) Select(ctx context.Context, flow netx.Flow, candidates []string) (string, error) {
	return i.selector.Select(ctx, flow, candidates)
}

type roundRobinSelector struct {
	mu   sync.Mutex
	next uint64
}

func (s *roundRobinSelector) Select(ctx context.Context, _ netx.Flow, candidates []string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	ordered := sortedCandidates(candidates)
	if len(ordered) == 0 {
		return "", fmt.Errorf("config selector: no active candidates")
	}
	s.mu.Lock()
	selected := ordered[s.next%uint64(len(ordered))]
	s.next++
	s.mu.Unlock()
	return selected, nil
}

type consistentHashSelector struct{}

func (consistentHashSelector) Select(ctx context.Context, flow netx.Flow, candidates []string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	ordered := sortedCandidates(candidates)
	if len(ordered) == 0 {
		return "", fmt.Errorf("config selector: no active candidates")
	}
	key := StableFlowKey(flow)
	selected := ordered[0]
	var best [32]byte
	for index, candidate := range ordered {
		score := selectionScore(key, candidate)
		if index == 0 || bytes.Compare(score[:], best[:]) > 0 {
			selected, best = candidate, score
		}
	}
	return selected, nil
}

type stickyEntry struct {
	candidate string
	seenAt    time.Time
}

type stickySelector struct {
	mu         sync.Mutex
	now        func() time.Time
	ttl        time.Duration
	maxEntries int
	entries    map[string]stickyEntry
}

func newStickySelector(now func() time.Time, ttl time.Duration, maxEntries int) *stickySelector {
	if now == nil {
		now = time.Now
	}
	if ttl <= 0 {
		ttl = defaultStickyTTL
	}
	if maxEntries <= 0 {
		maxEntries = defaultStickyMax
	}
	return &stickySelector{now: now, ttl: ttl, maxEntries: maxEntries, entries: map[string]stickyEntry{}}
}

func (s *stickySelector) Select(ctx context.Context, flow netx.Flow, candidates []string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	ordered := sortedCandidates(candidates)
	if len(ordered) == 0 {
		return "", fmt.Errorf("config selector: no active candidates")
	}
	key := StableFlowKey(flow)
	if key == "" {
		return consistentHashSelection("", ordered), nil
	}
	now := s.now()
	membership := make(map[string]bool, len(ordered))
	for _, candidate := range ordered {
		membership[candidate] = true
	}
	s.mu.Lock()
	for entryKey, entry := range s.entries {
		if now.Sub(entry.seenAt) >= s.ttl {
			delete(s.entries, entryKey)
		}
	}
	if entry, ok := s.entries[key]; ok && membership[entry.candidate] {
		entry.seenAt = now
		s.entries[key] = entry
		s.mu.Unlock()
		return entry.candidate, nil
	}
	selected := consistentHashSelection(key, ordered)
	if len(s.entries) >= s.maxEntries {
		s.removeOldest()
	}
	s.entries[key] = stickyEntry{candidate: selected, seenAt: now}
	s.mu.Unlock()
	return selected, nil
}

func (s *stickySelector) removeOldest() {
	var oldestKey string
	var oldest time.Time
	for key, entry := range s.entries {
		if oldestKey == "" || entry.seenAt.Before(oldest) {
			oldestKey, oldest = key, entry.seenAt
		}
	}
	if oldestKey != "" {
		delete(s.entries, oldestKey)
	}
}

func consistentHashSelection(key string, candidates []string) string {
	selected := candidates[0]
	var best [32]byte
	for index, candidate := range candidates {
		score := selectionScore(key, candidate)
		if index == 0 || bytes.Compare(score[:], best[:]) > 0 {
			selected, best = candidate, score
		}
	}
	return selected
}

func sortedCandidates(candidates []string) []string {
	ordered := make([]string, 0, len(candidates))
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		ordered = append(ordered, candidate)
	}
	sort.Strings(ordered)
	return ordered
}

func selectionScore(key, candidate string) [32]byte {
	var encoded [binary.MaxVarintLen64 * 2]byte
	keyLength := binary.PutUvarint(encoded[:], uint64(len(key)))
	value := append(encoded[:keyLength], key...)
	var candidateLength [binary.MaxVarintLen64]byte
	length := binary.PutUvarint(candidateLength[:], uint64(len(candidate)))
	value = append(value, candidateLength[:length]...)
	value = append(value, candidate...)
	return sha256.Sum256(value)
}

// StableFlowKey prefers explicit session/hash tags. The fallback intentionally
// excludes Flow.ID because it is generated per flow; host, port, purpose and
// network keep equivalent traffic on a stable key.
func StableFlowKey(flow netx.Flow) string {
	for _, name := range []string{"session_key", "hash_key", "traffic_key", "sticky_key", "session"} {
		if value := strings.TrimSpace(flow.Tags[name]); value != "" {
			return "tag:" + name + "=" + value
		}
	}
	network := strings.TrimSpace(flow.Network)
	purpose := strings.TrimSpace(flow.Purpose)
	host := strings.TrimSpace(flow.Host)
	port := flow.Port
	if host == "" {
		address := strings.TrimSpace(flow.Address)
		if parsedHost, parsedPort, err := net.SplitHostPort(address); err == nil {
			host = parsedHost
			if port == 0 {
				port, _ = strconv.Atoi(parsedPort)
			}
		} else {
			host = address
		}
	}
	host = strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
	if network == "" && host == "" && port == 0 && purpose == "" {
		return ""
	}
	return fmt.Sprintf("flow:%s|%d|%s|%s", strings.ToLower(network), port, host, purpose)
}

var _ netx.ConfigSelector = (*Instance)(nil)
var _ plugin.Instance = (*Instance)(nil)
