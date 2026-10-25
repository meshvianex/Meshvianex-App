# Meshvianex source organization

This document maps the source tree to the control API, Realm/Graph execution
rules, plugin contracts, proxy behavior, and stored data formats.

## Control plane (`cmd/meshvianex`)

- `control.go`: control-plane state, construction, registration, snapshots.
- `control_actions.go`: public `Control` action dispatcher.
- `control_subscription_actions.go`, `control_config_actions.go`: persistent
  resource actions.
- `control_realm_actions.go`, `control_graph_actions.go`,
  `control_status_actions.go`: lifecycle, topology, status, plugin actions.
- `control_resources.go`, `control_refresh.go`, `control_bindings.go`:
  resource validation, subscription scheduling, and Realm bindings.
- `control_diagnostics.go`, `control_views.go`, `control_topology.go`,
  `control_helpers.go`: probes, snapshot rows, Graph helpers, JSON helpers.
- Control-plane tests are organized by behavior across `control_test.go`,
  `control_resources_test.go`, `control_persistence_test.go`, and
  `control_bindings_test.go`.

## Realm runtime (`core/realm`)

`realm_types.go` defines schema and runtime types. Runtime construction and
state changes remain in `realm.go`. Validation, lifecycle, outbound selection
and dialing, stream transforms, service methods, configuration migration, and
packet transforms live in `realm_validation.go`, `realm_lifecycle.go`,
`realm_dial.go`, `realm_stream.go`, `realm_services.go`, `realm_config.go`, and
`realm_transforms.go` respectively.

## Resources and config parser

The resource store's types, subscriptions, normalized configurations,
redaction, and persistence are in `core/resources/resources.go`,
`subscriptions.go`, `configs.go`, `redaction.go`, and `persistence.go`.

The basic parser's plugin entry point, JSON, URI, transport settings, and
format helpers are in `plugins/config_parser/config_parser.go`, `json.go`,
`uri.go`, `stream.go`, and `helpers.go` respectively.

## Embedded Web UI

`plugins/ui_web/index.html` contains markup only. Styles live in
`plugins/ui_web/assets/layout.css` and `dialogs.css`; 15 smaller JavaScript
files in that directory divide application state, topology, resources,
component editing, execution graphs, and import dialogs.

Classic `<script src>` elements load **in order** (no `async`, `defer`, or
`type="module"`); this is important because existing scripts share global
bindings and some later scripts extend earlier functions. These assets are
embedded and served from the existing `/assets/` endpoint by `ui_web.go`.
When adding scripts, preserve dependency order or first convert the UI to a
module-based architecture with an explicit bundling step.

## Verification guidance

Use Go 1.26 or a compatible later release with project dependencies available:

```bash
go test ./...
go test -race ./cmd/meshvianex ./core/realm ./core/resources ./plugins/config_parser ./plugins/ui_web
```

When Node.js is available, check each JavaScript asset with `node --check`.
These are recommended validation commands, not a claim that they have passed
on every platform.
