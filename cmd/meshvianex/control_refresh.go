package main

import (
	"context"
	"encoding/json"
	"fmt"
	"meshvianex/core/plugin"
	"meshvianex/core/resources"
	"strings"
	"sync"
	"time"
)

func (c *controlPlane) refreshLockFor(id string) *sync.Mutex {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	if lock := c.refreshLocks[id]; lock != nil {
		return lock
	}
	lock := &sync.Mutex{}
	c.refreshLocks[id] = lock
	return lock
}

func (c *controlPlane) refreshSubscription(ctx context.Context, id string, persist bool) (any, error) {
	lock := c.refreshLockFor(id)
	lock.Lock()
	defer lock.Unlock()
	return c.refreshSubscriptionLocked(ctx, id, persist)
}

func (c *controlPlane) refreshSubscriptionLocked(ctx context.Context, id string, persist bool) (any, error) {
	sub, err := c.resources.GetSubscription(id)
	if err != nil {
		return nil, err
	}
	configsBefore, err := c.resources.ListConfigs()
	if err != nil {
		return nil, err
	}
	realmSnapshots := c.snapshotRealmConfigs()
	restoreRefreshState := func() {
		_ = c.resources.RestoreConfigs(configsBefore)
		_ = c.restoreRealmConfigs(ctx, realmSnapshots)
	}
	parserID := sub.ParserPlugin
	if parserID == "" {
		parserID = "io.meshvianex.config.parser.basic"
	}
	parserInstance, err := c.reg.Create(ctx, parserID, plugin.InstanceContext{InstanceID: "parser-" + id, Purpose: "subscription_update"}, json.RawMessage(`{}`))
	if err != nil {
		return nil, fmt.Errorf("subscription parser %s: %w", parserID, err)
	}
	defer parserInstance.Stop(context.Background())
	if err := parserInstance.Start(ctx); err != nil {
		return nil, fmt.Errorf("subscription parser %s start: %w", parserID, err)
	}
	parser, ok := parserInstance.(resources.Parser)
	if !ok {
		return nil, fmt.Errorf("plugin %s does not implement config parser", parserID)
	}
	sourceInstance, err := c.reg.Create(ctx, "io.meshvianex.subscription.http", plugin.InstanceContext{InstanceID: "source-" + id, Purpose: "subscription_update"}, json.RawMessage(`{}`))
	if err != nil {
		return nil, fmt.Errorf("subscription source: %w", err)
	}
	defer sourceInstance.Stop(context.Background())
	if err := sourceInstance.Start(ctx); err != nil {
		return nil, fmt.Errorf("subscription source start: %w", err)
	}
	fetcher, ok := sourceInstance.(resources.SubscriptionFetcher)
	if !ok {
		return nil, fmt.Errorf("subscription source plugin does not implement fetch")
	}
	content, err := fetcher.Fetch(ctx, sub)
	if err != nil {
		if persist {
			_ = c.resources.RecordRefresh(id, "failed", err.Error(), time.Now().UTC())
		}
		return nil, err
	}
	parsed, err := parser.Parse(ctx, content)
	if err != nil {
		if persist {
			_ = c.resources.RecordRefresh(id, "failed", err.Error(), time.Now().UTC())
		}
		return nil, err
	}
	valid := make([]resources.NormalizedConfig, 0, len(parsed.Configs))
	for _, item := range parsed.Configs {
		manifest, exists := c.reg.Manifest(item.PluginID)
		if !exists {
			parsed.Diagnostics = append(parsed.Diagnostics, resources.Diagnostic{Message: "parsed config references unknown plugin " + item.PluginID})
			continue
		}
		provided := false
		for _, typ := range manifest.PluginTypes {
			if typ == item.PluginType {
				provided = true
				break
			}
		}
		if !provided {
			parsed.Diagnostics = append(parsed.Diagnostics, resources.Diagnostic{Message: "parsed config plugin " + item.PluginID + " does not provide type " + item.PluginType})
			continue
		}
		valid = append(valid, item)
	}
	if !persist {
		return map[string]any{"subscription": resources.RedactSubscription(sub), "format": parsed.Format, "configs": redactNormalized(valid), "diagnostics": parsed.Diagnostics}, nil
	}
	if len(valid) == 0 {
		err = fmt.Errorf("subscription produced no valid configs")
		_ = c.resources.RecordRefresh(id, "failed", err.Error(), time.Now().UTC())
		return map[string]any{"subscription": resources.RedactSubscription(sub), "format": parsed.Format, "diagnostics": parsed.Diagnostics}, err
	}
	if _, err = c.resources.ReconcileSubscription(id, valid, parsed.Format, time.Now().UTC()); err != nil {
		_ = c.resources.RecordRefresh(id, "failed", err.Error(), time.Now().UTC())
		return nil, err
	}
	if err = c.syncSubscriptionBindings(ctx, id); err != nil {
		restoreRefreshState()
		_ = c.resources.RecordRefresh(id, "failed", err.Error(), time.Now().UTC())
		return nil, err
	}
	if err = c.persistState(); err != nil {
		restoreRefreshState()
		_ = c.resources.RecordRefresh(id, "failed", err.Error(), time.Now().UTC())
		return nil, err
	}
	status := "success"
	lastError := ""
	if len(parsed.Diagnostics) > 0 {
		status, lastError = "partial", parsed.Diagnostics[0].Message
	}
	if err = c.resources.RecordRefresh(id, status, lastError, time.Now().UTC()); err != nil {
		return nil, err
	}
	c.record("Refreshed subscription "+sub.Name, fmt.Sprintf("%d configs; %s", len(valid), status), "ok")
	return map[string]any{"subscription": resources.RedactSubscription(sub), "format": parsed.Format, "configs": c.configRows(), "diagnostics": parsed.Diagnostics}, nil
}

func parseRefreshInterval(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("refresh_interval must be a positive duration: %q", value)
	}
	return duration, nil
}

func (c *controlPlane) startRefreshScheduler(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	c.refreshMu.Lock()
	if c.refreshCancel != nil {
		c.refreshMu.Unlock()
		return
	}
	schedulerCtx, cancel := context.WithCancel(ctx)
	c.refreshCancel = cancel
	c.refreshMu.Unlock()
	c.refreshWG.Add(1)
	go func() {
		defer c.refreshWG.Done()
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-schedulerCtx.Done():
				return
			case now := <-ticker.C:
				c.scheduleDueRefreshes(schedulerCtx, now)
			}
		}
	}()
}

func (c *controlPlane) stopRefreshScheduler() {
	c.refreshMu.Lock()
	cancel := c.refreshCancel
	c.refreshCancel = nil
	c.refreshMu.Unlock()
	if cancel != nil {
		cancel()
		c.refreshWG.Wait()
	}
}

func (c *controlPlane) scheduleDueRefreshes(ctx context.Context, now time.Time) int {
	subs, err := c.resources.ListSubscriptions()
	if err != nil {
		return 0
	}
	started := 0
	for _, sub := range subs {
		if !sub.Enabled {
			continue
		}
		interval, err := parseRefreshInterval(sub.RefreshInterval)
		if err != nil || interval == 0 || (!sub.LastRefreshAt.IsZero() && now.Sub(sub.LastRefreshAt) < interval) {
			continue
		}
		lock := c.refreshLockFor(sub.ID)
		if !lock.TryLock() {
			continue
		}
		started++
		c.refreshWG.Add(1)
		go func(id string, lock *sync.Mutex) {
			defer c.refreshWG.Done()
			defer lock.Unlock()
			if _, err := c.refreshSubscriptionLocked(ctx, id, true); err != nil {
				c.record("Automatic refresh failed", id+": "+err.Error(), "warn")
			}
		}(sub.ID, lock)
	}
	return started
}

func redactNormalized(items []resources.NormalizedConfig) []resources.NormalizedConfig {
	out := make([]resources.NormalizedConfig, len(items))
	for i, item := range items {
		out[i] = item
		out[i].Config = resources.RedactJSON(item.Config)
	}
	return out
}
