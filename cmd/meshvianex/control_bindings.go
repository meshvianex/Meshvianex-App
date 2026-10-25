package main

import (
	"context"
	"encoding/json"
	"fmt"
	"meshvianex/core/realm"
	"meshvianex/core/resources"
)

func (c *controlPlane) assignResourceRef(ctx context.Context, resourceID string, payload json.RawMessage, subscription, add bool) (any, error) {
	var request struct {
		RealmID string `json:"realm_id"`
	}
	if err := decodePayload(payload, &request); err != nil {
		return nil, err
	}
	if request.RealmID == "" {
		return nil, fmt.Errorf("realm_id is required")
	}
	if subscription {
		if !c.resources.HasSubscription(resourceID) {
			return nil, fmt.Errorf("subscription not found: %s", resourceID)
		}
	} else if !c.resources.HasConfig(resourceID) {
		return nil, fmt.Errorf("config not found: %s", resourceID)
	}
	rt, err := c.realm(request.RealmID)
	if err != nil {
		return nil, err
	}
	snapshots := c.snapshotRealmConfigs()
	previous := rt.Config()
	next := previous
	if subscription {
		next.SubscriptionRefs = updateRefList(next.SubscriptionRefs, resourceID, add)
		if !add {
			configs, listErr := c.resources.ListConfigs()
			if listErr != nil {
				return nil, listErr
			}
			for _, item := range configs {
				if item.SourceSubscriptionID != resourceID {
					continue
				}
				next.ConfigRefs = updateRefList(next.ConfigRefs, item.ID, false)
				removeConfigComponent(&next, item.ID)
			}
		}
	} else {
		next.ConfigRefs = updateRefList(next.ConfigRefs, resourceID, add)
		item, getErr := c.resources.GetConfig(resourceID)
		if getErr != nil {
			return nil, getErr
		}
		bindConfigComponent(&next, item, add)
	}
	if err = rt.Update(ctx, next); err != nil {
		return nil, err
	}
	if subscription && add {
		if err = c.syncSubscriptionBindings(ctx, resourceID); err != nil {
			_ = c.restoreRealmConfigs(ctx, snapshots)
			return nil, err
		}
	}
	if err = c.persistState(); err != nil {
		_ = c.restoreRealmConfigs(ctx, snapshots)
		return nil, err
	}
	c.record("Updated resource assignment", resourceID+" → "+request.RealmID, "ok")
	return rt.Status(), nil
}

func (c *controlPlane) syncSubscriptionBindings(ctx context.Context, subscriptionID string) error {
	subscription, err := c.resources.GetSubscription(subscriptionID)
	if err != nil {
		return err
	}
	configs, err := c.resources.ListConfigs()
	if err != nil {
		return err
	}
	c.mu.RLock()
	realms := make([]*realm.Runtime, 0, len(c.realms))
	for _, rt := range c.realms {
		realms = append(realms, rt)
	}
	c.mu.RUnlock()
	for _, rt := range realms {
		next := rt.Config()
		if next.Config != nil {
			selection := *next.Config
			next.Config = &selection
		}
		if next.Config != nil && next.Config.SourceType == "subscription" && next.Config.SourceRef == subscriptionID {
			candidateIDs, candidateErr := c.resources.ConfigCandidates("subscription", subscriptionID)
			next.Config.Enabled = subscription.Enabled && candidateErr == nil && len(candidateIDs) > 0
			// Refresh may change candidates without changing the persisted
			// component fields. Update rebuilds the runtime candidate dialers.
			if err := rt.Update(ctx, next); err != nil {
				return fmt.Errorf("apply config selector %s to realm %s: %w", subscriptionID, rt.ID(), err)
			}
		}
		if !containsString(next.SubscriptionRefs, subscriptionID) {
			continue
		}
		changed := false
		for _, item := range configs {
			if item.SourceSubscriptionID != subscriptionID {
				continue
			}
			if !subscription.Enabled || !item.Enabled || item.ValidationStatus == "stale" || item.ValidationStatus == "invalid" {
				refs := updateRefList(next.ConfigRefs, item.ID, false)
				if len(refs) != len(next.ConfigRefs) {
					next.ConfigRefs = refs
					changed = true
				}
				if removeConfigComponent(&next, item.ID) {
					changed = true
				}
				continue
			}
			refs := updateRefList(next.ConfigRefs, item.ID, true)
			if len(refs) != len(next.ConfigRefs) {
				next.ConfigRefs = refs
				changed = true
			}
			if bindConfigComponent(&next, item, true) {
				changed = true
			}
		}
		if changed {
			if err := rt.Update(ctx, next); err != nil {
				return fmt.Errorf("apply subscription %s to realm %s: %w", subscriptionID, rt.ID(), err)
			}
		}
	}
	return nil
}

func (c *controlPlane) syncConfigResource(ctx context.Context, configID string) error {
	item, err := c.resources.GetConfig(configID)
	if err != nil {
		return err
	}
	if item.SourceSubscriptionID != "" {
		return c.syncSubscriptionBindings(ctx, item.SourceSubscriptionID)
	}
	c.mu.RLock()
	realms := make([]*realm.Runtime, 0, len(c.realms))
	for _, rt := range c.realms {
		realms = append(realms, rt)
	}
	c.mu.RUnlock()
	for _, rt := range realms {
		next := rt.Config()
		if next.Config != nil {
			selection := *next.Config
			next.Config = &selection
		}
		assigned := containsString(next.ConfigRefs, configID) || hasConfigComponent(next, configID)
		if !assigned {
			continue
		}
		if next.Config != nil && next.Config.SourceType == "config" && next.Config.SourceRef == configID {
			next.Config.Enabled = item.Enabled && item.ValidationStatus != "stale" && item.ValidationStatus != "invalid"
		} else if item.Enabled && item.ValidationStatus != "stale" && item.ValidationStatus != "invalid" {
			next.ConfigRefs = updateRefList(next.ConfigRefs, configID, true)
			bindConfigComponent(&next, item, true)
		} else {
			next.ConfigRefs = updateRefList(next.ConfigRefs, configID, false)
			removeConfigComponent(&next, configID)
		}
		if err := rt.Update(ctx, next); err != nil {
			return fmt.Errorf("apply config %s to realm %s: %w", configID, rt.ID(), err)
		}
	}
	return nil
}

func hasConfigComponent(cfg realm.Config, configID string) bool {
	if cfg.Config != nil && cfg.Config.SourceType == "config" && cfg.Config.SourceRef == configID {
		return true
	}
	if cfg.Config != nil && cfg.Config.SourceType == "config" {
		for _, sourceRef := range cfg.Config.SourceRefs {
			if sourceRef == configID {
				return true
			}
		}
	}
	if cfg.Routing != nil && cfg.Routing.ConfigRef == configID {
		return true
	}
	for _, components := range [][]realm.ComponentRef{cfg.DNS, cfg.Transforms, cfg.Protocols, cfg.Transports, cfg.Outbounds, cfg.Inbounds, cfg.Probes} {
		for _, component := range components {
			if component.ConfigRef == configID {
				return true
			}
		}
	}
	return false
}

func bindConfigComponent(cfg *realm.Config, item resources.Config, add bool) bool {
	component, ok := resourceComponent(item)
	if !ok {
		return false
	}
	if !add {
		return removeConfigComponent(cfg, item.ID)
	}
	if item.PluginType == "routing" {
		if cfg.Routing == nil {
			cfg.Routing = &component
			return true
		}
		if cfg.Routing.ConfigRef == item.ID {
			*cfg.Routing = component
			return true
		}
		return false
	}
	var target *[]realm.ComponentRef
	switch item.PluginType {
	case "dns":
		target = &cfg.DNS
	case "packet_transform", "stream_transform":
		target = &cfg.Transforms
	case "protocol", "vpn_protocol":
		target = &cfg.Protocols
	case "transport":
		target = &cfg.Transports
	case "outbound":
		target = &cfg.Outbounds
	case "inbound":
		target = &cfg.Inbounds
	case "diagnostic", "test_provider":
		target = &cfg.Probes
	default:
		return false
	}
	for index := range *target {
		if (*target)[index].ConfigRef == item.ID {
			(*target)[index] = component
			return true
		}
	}
	*target = append(*target, component)
	return true
}

func hasEnabledOutbound(cfg *realm.Config, instanceID string) bool {
	if instanceID == "" {
		return false
	}
	for _, outbound := range cfg.Outbounds {
		if outbound.Enabled && outbound.InstanceID == instanceID {
			return true
		}
	}
	return false
}

func resourceComponent(item resources.Config) (realm.ComponentRef, bool) {
	if item.ID == "" || item.PluginID == "" || item.PluginType == "" {
		return realm.ComponentRef{}, false
	}
	stage := ""
	if item.PluginType == "packet_transform" || item.PluginType == "stream_transform" {
		stage = "pre_route"
	}
	return realm.ComponentRef{InstanceID: "resource-" + item.ID, Plugin: item.PluginID, Enabled: item.Enabled && item.ValidationStatus != "stale", ConfigRef: item.ID, Stage: stage}, true
}

func removeConfigComponent(cfg *realm.Config, configID string) bool {
	changed := false
	if cfg.Config != nil && cfg.Config.SourceType == "config" {
		if cfg.Config.SourceRef == configID {
			cfg.Config.SourceRef = ""
			changed = true
		}
		filtered := cfg.Config.SourceRefs[:0]
		for _, sourceRef := range cfg.Config.SourceRefs {
			if sourceRef == configID {
				changed = true
				continue
			}
			filtered = append(filtered, sourceRef)
		}
		cfg.Config.SourceRefs = filtered
		if changed && cfg.Config.SourceRef == "" && len(cfg.Config.SourceRefs) == 1 {
			cfg.Config.SourceRef = cfg.Config.SourceRefs[0]
		}
		if changed && cfg.Config.SourceRef == "" && len(cfg.Config.SourceRefs) == 0 {
			cfg.Config = nil
		}
	}
	if cfg.Routing != nil && cfg.Routing.ConfigRef == configID {
		cfg.Routing = nil
		changed = true
	}
	remove := func(values []realm.ComponentRef) []realm.ComponentRef {
		filtered := values[:0]
		for _, value := range values {
			if value.ConfigRef == configID {
				changed = true
				continue
			}
			filtered = append(filtered, value)
		}
		return filtered
	}
	cfg.DNS = remove(cfg.DNS)
	cfg.Transforms = remove(cfg.Transforms)
	cfg.Protocols = remove(cfg.Protocols)
	cfg.Transports = remove(cfg.Transports)
	cfg.Outbounds = remove(cfg.Outbounds)
	cfg.Inbounds = remove(cfg.Inbounds)
	cfg.Probes = remove(cfg.Probes)
	if changed && cfg.DefaultOutbound == "resource-"+configID {
		// Removing the selected outbound deliberately leaves the Realm without
		// a default. An operator must choose the replacement explicitly; resource
		// assignment must never silently change routing policy.
		cfg.DefaultOutbound = ""
	}
	return changed
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func updateRefList(values []string, value string, add bool) []string {
	result := make([]string, 0, len(values)+1)
	found := false
	for _, item := range values {
		if item == value {
			found = true
			if !add {
				continue
			}
		}
		result = append(result, item)
	}
	if add && !found {
		result = append(result, value)
	}
	return result
}

func (c *controlPlane) removeResourceRef(ctx context.Context, id string, subscription bool) error {
	snapshots := c.snapshotRealmConfigs()
	var configs []resources.Config
	if subscription {
		var err error
		configs, err = c.resources.ListConfigs()
		if err != nil {
			return err
		}
	}
	c.mu.RLock()
	realms := make([]*realm.Runtime, 0, len(c.realms))
	for _, rt := range c.realms {
		realms = append(realms, rt)
	}
	c.mu.RUnlock()
	for _, rt := range realms {
		next := rt.Config()
		if next.Config != nil {
			selection := *next.Config
			next.Config = &selection
		}
		if subscription {
			next.SubscriptionRefs = updateRefList(next.SubscriptionRefs, id, false)
			if next.Config != nil && next.Config.SourceType == "subscription" && next.Config.SourceRef == id {
				next.Config = nil
			}
			for _, item := range configs {
				if item.SourceSubscriptionID == id {
					next.ConfigRefs = updateRefList(next.ConfigRefs, item.ID, false)
					removeConfigComponent(&next, item.ID)
				}
			}
		} else {
			next.ConfigRefs = updateRefList(next.ConfigRefs, id, false)
			removeConfigComponent(&next, id)
		}
		if err := rt.Update(ctx, next); err != nil {
			_ = c.restoreRealmConfigs(ctx, snapshots)
			return err
		}
	}
	if err := c.persistState(); err != nil {
		_ = c.restoreRealmConfigs(ctx, snapshots)
		return err
	}
	return nil
}
