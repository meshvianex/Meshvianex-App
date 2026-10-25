package main

import (
	"context"
	"encoding/json"
	"fmt"
	"meshvianex/core/resources"
	"strings"
)

func (c *controlPlane) controlSubscriptionAction(ctx context.Context, action, id string, payload json.RawMessage) (any, error) {
	switch action {
	case "list_subscriptions":
		items, err := c.resources.ListSubscriptions()
		for i := range items {
			items[i] = resources.RedactSubscription(items[i])
		}
		return items, err
	case "create_subscription", "update_subscription":
		var item resources.Subscription
		var previous resources.Subscription
		if err := decodePayload(payload, &item); err != nil {
			return nil, err
		}
		if action == "update_subscription" {
			if id == "" || item.ID != id {
				return nil, fmt.Errorf("subscription id cannot be changed")
			}
			var getErr error
			previous, getErr = c.resources.GetSubscription(id)
			if getErr != nil {
				return nil, getErr
			}
			item.Source = resources.MergeRedactedString(previous.Source, item.Source)
		} else if item.ID == "" {
			item.ID = newResourceID("sub")
		}
		if item.ParserPlugin == "" {
			item.ParserPlugin = "io.meshvianex.config.parser.basic"
		}
		if err := c.validateParserPlugin(item.ParserPlugin); err != nil {
			return nil, err
		}
		if item.SourceType == "" {
			item.SourceType = "raw"
			lowerSource := strings.ToLower(strings.TrimSpace(item.Source))
			if !strings.ContainsAny(item.Source, "\r\n") && (strings.HasPrefix(lowerSource, "http://") || strings.HasPrefix(lowerSource, "https://")) {
				item.SourceType = "url"
			}
		}
		if item.SourceType != "raw" && item.SourceType != "url" {
			return nil, fmt.Errorf("unsupported subscription source_type %q", item.SourceType)
		}
		if _, err := parseRefreshInterval(item.RefreshInterval); err != nil {
			return nil, err
		}
		if err := c.resources.SaveSubscription(item); err != nil {
			return nil, err
		}
		c.record("Saved subscription "+item.Name, "Persistent source updated", "ok")
		return resources.RedactSubscription(item), nil
	case "patch_subscription":
		return c.patchSubscription(id, payload)
	case "rename_subscription":
		item, err := c.resources.GetSubscription(id)
		if err != nil {
			return nil, err
		}
		var request struct {
			Name string `json:"name"`
		}
		if err = decodePayload(payload, &request); err != nil {
			return nil, err
		}
		if strings.TrimSpace(request.Name) == "" {
			return nil, fmt.Errorf("subscription name is required")
		}
		item.Name = strings.TrimSpace(request.Name)
		if err = c.resources.SaveSubscription(item); err != nil {
			return nil, err
		}
		c.record("Renamed subscription", item.Name, "ok")
		return resources.RedactSubscription(item), nil
	case "delete_subscription":
		previous, err := c.resources.GetSubscription(id)
		if err != nil {
			return nil, err
		}
		if err := c.resources.DeleteSubscription(id); err != nil {
			return nil, err
		}
		if err := c.removeResourceRef(ctx, id, true); err != nil {
			_ = c.resources.SaveSubscription(previous)
			return nil, err
		}
		c.record("Deleted subscription", id, "ok")
		return map[string]any{"deleted": id}, nil
	case "refresh_subscription":
		return c.refreshSubscription(ctx, id, true)
	case "test_subscription":
		return c.testSubscription(ctx, id)
	case "set_subscription_enabled":
		return c.setSubscriptionEnabled(ctx, id, payload)
	case "assign_subscription_to_realm":
		return c.assignResourceRef(ctx, id, payload, true, true)
	case "unassign_subscription_from_realm":
		return c.assignResourceRef(ctx, id, payload, true, false)
	default:
		return nil, fmt.Errorf("unsupported control action: %s", action)
	}
}
