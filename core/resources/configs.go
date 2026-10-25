package resources

import (
	"encoding/json"
	"fmt"
	"meshvianex/core/plugin"
	"sort"
	"strings"
	"time"
)

func (s *Store) ListConfigs() ([]Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listConfigsLocked()
}

func (s *Store) listConfigsLocked() ([]Config, error) {
	var out []Config
	if err := readJSON(s.path("configs.json"), &out); err != nil {
		return nil, err
	}
	for i := range out {
		out[i] = canonicalizeConfig(out[i])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *Store) GetConfig(id string) (Config, error) {
	items, err := s.ListConfigs()
	if err != nil {
		return Config{}, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	return Config{}, fmt.Errorf("config not found: %s", id)
}

func (s *Store) HasConfig(id string) bool {
	_, err := s.GetConfig(id)
	return err == nil
}

func (s *Store) HasSubscription(id string) bool {
	_, err := s.GetSubscription(id)
	return err == nil
}

// ResolveConfig adapts the store to Realm's reference resolver without
// exposing the resource metadata to the runtime.
func (s *Store) ResolveConfig(id string) (json.RawMessage, bool) {
	item, err := s.GetConfig(id)
	if err != nil || !item.Enabled || item.ValidationStatus == "stale" || item.ValidationStatus == "invalid" {
		return nil, false
	}
	return append(json.RawMessage(nil), item.Normalized...), true
}

func (s *Store) ResolveConfigMetadata(id string) (pluginID, pluginType string, active, ok bool) {
	item, err := s.GetConfig(id)
	if err != nil {
		return "", "", false, false
	}
	active = item.Enabled && item.ValidationStatus != "stale" && item.ValidationStatus != "invalid"
	return item.PluginID, item.PluginType, active, true
}

// ConfigCandidates returns stable resource IDs that a Realm may hand to a
// ConfigSelector. Resource filtering stays in the store so selectors never
// need access to persistence or subscription lifecycle state.
func (s *Store) ConfigCandidates(sourceType, sourceID string) ([]string, error) {
	switch strings.ToLower(strings.TrimSpace(sourceType)) {
	case "config":
		item, err := s.GetConfig(sourceID)
		if err != nil {
			return nil, err
		}
		if !item.Enabled || item.ValidationStatus == "stale" || item.ValidationStatus == "invalid" {
			return nil, fmt.Errorf("config %s is inactive", sourceID)
		}
		return []string{item.ID}, nil
	case "subscription":
		subscription, err := s.GetSubscription(sourceID)
		if err != nil {
			return nil, err
		}
		if !subscription.Enabled {
			return nil, fmt.Errorf("subscription %s is disabled", sourceID)
		}
		items, err := s.ListConfigs()
		if err != nil {
			return nil, err
		}
		candidates := make([]string, 0)
		for _, item := range items {
			if item.SourceSubscriptionID != sourceID || !item.Enabled || item.ValidationStatus == "stale" || item.ValidationStatus == "invalid" {
				continue
			}
			candidates = append(candidates, item.ID)
		}
		if len(candidates) == 0 {
			return nil, fmt.Errorf("subscription %s has no active configs", sourceID)
		}
		return candidates, nil
	default:
		return nil, fmt.Errorf("unsupported config source_type %q", sourceType)
	}
}

func (s *Store) SaveConfig(item Config) error {
	item = canonicalizeConfig(item)
	if item.ID == "" || item.Name == "" || item.PluginID == "" || len(item.Normalized) == 0 {
		return fmt.Errorf("config id, name, plugin_id, and normalized config are required")
	}
	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	if item.ValidationStatus == "" {
		item.ValidationStatus = "valid"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.listConfigsLocked()
	if err != nil {
		return err
	}
	updated := false
	for i := range items {
		if items[i].ID == item.ID {
			item.CreatedAt = items[i].CreatedAt
			items[i] = item
			updated = true
			break
		}
	}
	if !updated {
		items = append(items, item)
	}
	return writeJSON(s.path("configs.json"), items)
}

func (s *Store) DeleteConfig(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := readConfigs(s.path("configs.json"))
	if err != nil {
		return err
	}
	filtered := items[:0]
	found := false
	for _, item := range items {
		if item.ID == id {
			found = true
			continue
		}
		filtered = append(filtered, item)
	}
	if !found {
		return fmt.Errorf("config not found: %s", id)
	}
	return writeJSON(s.path("configs.json"), filtered)
}

// ReconcileSubscription replaces only the configs owned by a successful
// refresh. Stable keys preserve IDs across refreshes; removed entries remain
// disabled and marked stale instead of being silently destroyed.
func (s *Store) ReconcileSubscription(subscriptionID string, parsed []NormalizedConfig, format string, now time.Time) ([]Config, error) {
	if subscriptionID == "" {
		return nil, fmt.Errorf("subscription id is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.listConfigsLocked()
	if err != nil {
		return nil, err
	}
	byKey := map[string]int{}
	owned := map[string]bool{}
	for i := range items {
		if items[i].SourceSubscriptionID == subscriptionID {
			owned[items[i].StableKey] = true
			byKey[items[i].StableKey] = i
		}
	}
	seen := map[string]bool{}
	for _, normalized := range parsed {
		normalized.PluginID = plugin.CanonicalID(normalized.PluginID)
		normalized.PluginType = plugin.CanonicalType(normalized.PluginType)
		if normalized.PluginID == "" || normalized.PluginType == "" || len(normalized.Config) == 0 {
			continue
		}
		key := StableKey(normalized)
		seen[key] = true
		index, ok := byKey[key]
		if !ok {
			name := strings.TrimSpace(normalized.Name)
			if name == "" {
				name = normalized.PluginID + " config"
			}
			items = append(items, Config{ID: "cfg-" + key[:16], Name: name, SourceSubscriptionID: subscriptionID, Format: format, PluginID: normalized.PluginID, PluginType: normalized.PluginType, Normalized: append(json.RawMessage(nil), normalized.Config...), Enabled: true, CreatedAt: now, UpdatedAt: now, ValidationStatus: "valid", StableKey: key})
			continue
		}
		item := &items[index]
		item.Format, item.PluginID, item.PluginType = format, normalized.PluginID, normalized.PluginType
		item.Normalized = append(json.RawMessage(nil), normalized.Config...)
		if name := strings.TrimSpace(normalized.Name); name != "" {
			item.Name = name
		}
		item.Enabled, item.ValidationStatus, item.ValidationError, item.UpdatedAt = true, "valid", "", now
	}
	for i := range items {
		if items[i].SourceSubscriptionID == subscriptionID && owned[items[i].StableKey] && !seen[items[i].StableKey] {
			items[i].Enabled = false
			items[i].ValidationStatus = "stale"
			items[i].ValidationError = "entry was not present in the last successful refresh"
			items[i].UpdatedAt = now
		}
	}
	if err := writeJSON(s.path("configs.json"), items); err != nil {
		return nil, err
	}
	return items, nil
}

// RestoreConfigs restores a previously captured config snapshot using the
// same atomic writer as normal resource updates. It is used to keep Realm
// bindings and the resource library consistent when a later application-state
// commit fails.
func (s *Store) RestoreConfigs(items []Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeJSON(s.path("configs.json"), items)
}

func (s *Store) RecordRefresh(id, status, lastError string, at time.Time) error {
	item, err := s.GetSubscription(id)
	if err != nil {
		return err
	}
	item.LastRefreshAt, item.LastRefreshStatus, item.LastError = at.UTC(), status, lastError
	return s.SaveSubscription(item)
}
