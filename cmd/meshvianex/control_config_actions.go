package main

import (
	"context"
	"encoding/json"
	"fmt"
	"meshvianex/core/resources"
	"strings"
)

func (c *controlPlane) controlConfigAction(ctx context.Context, action, id string, payload json.RawMessage) (any, error) {
	switch action {
	case "list_configs":
		return c.configRows(), nil
	case "create_config", "update_config":
		var item resources.Config
		var previous resources.Config
		if err := decodePayload(payload, &item); err != nil {
			return nil, err
		}
		if action == "update_config" {
			if id == "" || item.ID != id {
				return nil, fmt.Errorf("config id cannot be changed")
			}
			var getErr error
			previous, getErr = c.resources.GetConfig(id)
			if getErr != nil {
				return nil, getErr
			}
		} else if item.ID == "" {
			item.ID = newResourceID("cfg")
		}
		item.Normalized = normalizeRawJSON(item.Normalized)
		if action == "update_config" {
			item.Normalized = resources.MergeRedactedJSON(previous.Normalized, item.Normalized)
		}
		if err := c.validateResourceConfig(item); err != nil {
			return nil, err
		}
		if err := c.resources.SaveConfig(item); err != nil {
			return nil, err
		}
		if stored, getErr := c.resources.GetConfig(item.ID); getErr == nil {
			item = stored
		}
		if action == "update_config" {
			if err := c.syncConfigResource(ctx, item.ID); err != nil {
				_ = c.resources.SaveConfig(previous)
				_ = c.syncConfigResource(ctx, previous.ID)
				return nil, err
			}
			if err := c.persistState(); err != nil {
				_ = c.resources.SaveConfig(previous)
				_ = c.syncConfigResource(ctx, previous.ID)
				return nil, err
			}
		}
		c.record("Saved config "+item.Name, "Persistent plugin config updated", "ok")
		return resources.RedactConfig(item), nil
	case "rename_config":
		item, err := c.resources.GetConfig(id)
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
			return nil, fmt.Errorf("config name is required")
		}
		item.Name = strings.TrimSpace(request.Name)
		if err = c.resources.SaveConfig(item); err != nil {
			return nil, err
		}
		c.record("Renamed config", item.Name, "ok")
		return resources.RedactConfig(item), nil
	case "delete_config":
		previous, err := c.resources.GetConfig(id)
		if err != nil {
			return nil, err
		}
		if err := c.resources.DeleteConfig(id); err != nil {
			return nil, err
		}
		if err := c.removeResourceRef(ctx, id, false); err != nil {
			_ = c.resources.SaveConfig(previous)
			return nil, err
		}
		c.record("Deleted config", id, "ok")
		return map[string]any{"deleted": id}, nil
	case "validate_config":
		item, err := c.resources.GetConfig(id)
		if err != nil {
			return nil, err
		}
		previous := item
		if err = c.validateResourceConfig(item); err != nil {
			item.ValidationStatus, item.ValidationError = "invalid", err.Error()
			if saveErr := c.resources.SaveConfig(item); saveErr != nil {
				return nil, saveErr
			}
			if syncErr := c.syncConfigResource(ctx, id); syncErr != nil {
				_ = c.resources.SaveConfig(previous)
				_ = c.syncConfigResource(ctx, id)
				return nil, syncErr
			}
			if persistErr := c.persistState(); persistErr != nil {
				_ = c.resources.SaveConfig(previous)
				_ = c.syncConfigResource(ctx, id)
				return nil, persistErr
			}
			return resources.RedactConfig(item), err
		}
		item.ValidationStatus, item.ValidationError = "valid", ""
		if err = c.resources.SaveConfig(item); err != nil {
			return nil, err
		}
		if err = c.syncConfigResource(ctx, id); err != nil {
			_ = c.resources.SaveConfig(previous)
			_ = c.syncConfigResource(ctx, id)
			return nil, err
		}
		if err = c.persistState(); err != nil {
			_ = c.resources.SaveConfig(previous)
			_ = c.syncConfigResource(ctx, id)
			return nil, err
		}
		return resources.RedactConfig(item), nil
	case "test_config":
		return c.testConfig(ctx, id, payload)
	case "import_config":
		return c.importConfig(ctx, payload)
	case "set_config_enabled":
		return c.setConfigEnabled(ctx, id, payload)
	case "assign_config_to_realm":
		return c.assignResourceRef(ctx, id, payload, false, true)
	case "unassign_config_from_realm":
		return c.assignResourceRef(ctx, id, payload, false, false)
	case "set_default_outbound":
		return c.setDefaultOutbound(ctx, id, payload)
	default:
		return nil, fmt.Errorf("unsupported control action: %s", action)
	}
}
