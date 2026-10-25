package main

import (
	"context"
	"encoding/json"
	"fmt"
)

// Control dispatches actions to domain-specific handlers. It keeps the
// plugin-facing API stable while separating lifecycle, resources and graph edits.
func (c *controlPlane) Control(ctx context.Context, action, id string, payload json.RawMessage) (any, error) {
	switch action {
	case "assign_subscription_to_realm", "create_subscription", "delete_subscription", "list_subscriptions", "patch_subscription", "refresh_subscription", "rename_subscription", "set_subscription_enabled", "test_subscription", "unassign_subscription_from_realm", "update_subscription":
		return c.controlSubscriptionAction(ctx, action, id, payload)
	case "assign_config_to_realm", "create_config", "delete_config", "import_config", "list_configs", "rename_config", "set_config_enabled", "set_default_outbound", "test_config", "unassign_config_from_realm", "update_config", "validate_config":
		return c.controlConfigAction(ctx, action, id, payload)
	case "create_realm", "delete_realm", "probe_realm", "start_realm", "stop_realm", "test_realm", "update_realm":
		return c.controlRealmAction(ctx, action, id, payload)
	case "get_graph", "rollback_graph", "save_changes", "set_chain", "set_graph":
		return c.controlGraphAction(ctx, action, id, payload)
	case "list_connections", "list_plugins", "list_realms", "toggle_plugin":
		return c.controlStatusAction(ctx, action, id, payload)
	default:
		return nil, fmt.Errorf("unsupported control action: %s", action)
	}
}
