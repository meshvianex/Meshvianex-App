# Meshvianex Development Guide

## Architecture mapping

The original design documents are preserved under `docs/specs/`. This MVP maps their concepts as follows:

| Specification concept | Implementation |
|---|---|
| Core / Plugin Registry | `core/plugin` |
| Capability Engine | `core/plugin.CapabilitySet` and registry grants |
| Realm Manager / instances | `core/realm` |
| Graph Engine | `core/graph` |
| Flow abstraction | `core/netx` |
| Event Bus | `core/event` |
| Health state | `core/health` |
| Trust + package verification | `core/packages` |
| Content acquisition | `core/acquisition` |
| Offline transfer planning | `core/transfer` |
| Test engine | `core/testing` |
| Namespaced storage and secret handles | `core/storage` |
| ABI errors, buffers, and negotiation | `core/abi` |
| Platform capability discovery | `core/platform` |
| Built-in plugins | `plugins/*` |
| Persistent resources | `core/resources`, `plugins/subscription_basic`, `plugins/config_parser` |
| Health probes | `plugins/health_probe`, `core/health` |
| Application shell | `cmd/meshvianex` |

The Core does not contain HTTP-proxy-specific routing logic. HTTP semantics live in plugins, while the Core provides Flow, Dialer, RuleEngine, lifecycle, capability, Realm, and Graph contracts.

For the file-by-file source organization, see
[`REFACTORING.md`](REFACTORING.md).

## Why Go in this prototype

`ARCHITECTURE.md` describes Rust/WASM as a tentative stack that should be validated before being frozen. The reference implementation uses Go. The embedded Xray-core v26.3.27 dependency requires Go 1.26.

The ABI boundary is intentionally represented with small interfaces and JSON manifests. A future Rust Core/WASM runtime can preserve the same IDs, plugin types, capabilities, Realm schema, Graph schema, and lifecycle semantics.

See `docs/adr/0001-go-reference-core.md`.

## Plugin contract

A compiled-in plugin implements:

```go
type Factory interface {
    Manifest() Manifest
    Create(context.Context, InstanceContext, json.RawMessage) (Instance, error)
}

type Instance interface {
    Start(context.Context) error
    Stop(context.Context) error
}
```

Capability grants are checked before `Create` runs. A networking component then implements one or more typed service interfaces such as `netx.Dialer`, `netx.RuleEngine`, or `netx.Resolver`.

### Add a new outbound

1. Create `plugins/<name>/`.
2. Implement a Factory whose manifest contains plugin type `outbound`.
3. Implement `netx.Dialer` on the instance.
4. Declare only the capabilities it actually needs.
5. Register the Factory in `cmd/meshvianex/main.go`.
6. Add a Realm `outbounds` component with a unique `instance_id`.
7. Add tests for capability failures and routing behavior.

### Add the Xray outbound

`io.meshvianex.outbound.xray` embeds Xray-core v26.3.27 in an isolated Meshvianex
worker process for each plugin instance. It accepts an Xray `config_path` or
`inline_config`; no separate Xray executable is needed. The plugin builds a
derived config with a Meshvianex-owned SOCKS inbound bound to `127.0.0.1` and UDP
disabled, then uses the existing TCP SOCKS5 client to provide `netx.Dialer`.
User-provided non-empty `inbounds` arrays are rejected. Each worker receives
only a small host-environment allowlist, its Xray config, and its asset-directory
setting, so multiple Realms can run Xray concurrently. The plugin declares the
`process.spawn` capability for this worker boundary. Stopping the plugin closes
its worker's control pipe and shuts down that process.

The upstream Xray-core source is licensed under MPL-2.0; its license is kept in
`third_party/xray-core-26.3.27/LICENSE`. See `examples/upstream-xray.json`
and `examples/xray-freedom.json` for TCP examples. Xray-specific protocol
configuration remains inside the plugin rather than the Core.

### Add a new rule engine

Implement `netx.RuleEngine.Select`. It receives normalized Flow metadata and returns an outbound instance ID. It must not directly open sockets.

### Add a protocol, transport, transform, or probe

Use the typed contracts in `core/netx` (`Protocol`, `Transport`, and
`HealthProbe`) or `Transformer` for packet/stream transforms. Declare the
corresponding plugin type and capabilities in the manifest, register the
factory in `cmd/meshvianex/main.go`, and add the component to a Realm with a
unique `instance_id`. Protocols may reference an enabled transport; inbound
plugins receive the typed `realm.dialer` service so accepted flows continue
through Realm routing.

### Add a subscription parser

Implement `resources.Parser` in a plugin with type `config_parser`. Return
normalized `PluginID`, `PluginType`, and JSON config values plus diagnostics for
malformed or unsupported entries. The control plane validates parsed plugin
types against the registry, reconciles by a content-derived stable key, and
keeps removed configs as disabled/stale records.

### Add a UI provider

Portable UI remains declarative. `io.meshvianex.ui.web` exposes `/api/schema` and
publishes each plugin's optional `config_schema` through the control-plane
snapshot. The Realm inspector renders those fields and sends a structured
configuration object to the existing Realm update path. Plugins can implement
`plugin.ConfigSchemaProvider` when the schema is not embedded in their manifest;
arbitrary native UI execution is not permitted.

## Realm lifecycle

`core/realm.Runtime` creates routing-critical components first, then inbounds after the Realm Dialer service exists. Start is ordered; Stop is reverse ordered. Mutable component state is instance-local.

Realm state is represented by the documented lifecycle states. Configuration updates are validated before activation and the previous revision can be restored on activation failure.

Configuration references (`config_ref`) and Realm resource references are
resolved by the control plane's persistent resource store. Secrets remain in
the private store/configuration path and are redacted from API snapshots.

The application writes an atomic `application.json` aggregate containing the
current Graph and all registered Realm configurations. On startup that
aggregate takes precedence over mutable Realm/Graph sections in the initial
configuration file; when it is absent, the per-object revision stores are used
as a compatibility fallback. Subscription/config resource files are loaded
from the same state directory and are written atomically by each resource
operation. `save_changes` flushes the aggregate and returns an error if any
durability step fails.

The compiled-in transform pipeline executes every exposed stage in the
following order: `pre_route`, `post_route`, `pre_protocol`, `post_protocol`,
`pre_transport`, `post_transport`, `pre_socket`, `stream_open`, `handshake`,
`stream_data`, and `stream_close`. The handshake stage runs once, lazily before
the first application read or write; all stages are covered by Realm runtime
tests.

Realm `protocol` components are typed `netx.Protocol` implementations. Proxy
inbound/outbound plugins are separate `inbound`/`outbound` components and are
not selectable as Realm protocols. Assignment makes resources available but
never changes `default_outbound`; selecting a default is an explicit action.

## Graph engine

`core/graph` validates:

- graph/schema identity
- unique nodes and edges
- node references
- source/target port existence
- direction compatibility
- port type compatibility
- forbidden cycles

Tracing evaluates purpose, health, tags, network state, and weighted candidates. Graph revisions can be committed transactionally. The runtime also provides bounded live TCP Realm-to-Realm forwarding with context cancellation, loop detection, and hop limits; UDP/TUN and peer transports remain platform boundaries.

## Package verification

`core/packages.Store.Import` expects a ZIP-compatible `.mvplug` containing:

```text
manifest.json
signature.ed25519
...payload files...
```

The manifest contains SHA-256 hashes of payload files and a `publisher_key_id`. `signature.ed25519` is base64-encoded Ed25519 signature bytes over the exact `manifest.json` bytes.

A development helper is included:

```bash
go run ./tools/sign-plugin -in ./some-plugin-payload -out demo.mvplug -id io.example.demo
```

It prints the generated public key. Production publisher key management must use persistent protected private keys; the helper intentionally generates an ephemeral key and is for format testing only.

## Tests

Run:

```bash
go test ./...
```

Important future test additions:

- HTTP CONNECT end-to-end tests under packet loss and connection resets
- upstream proxy authentication failures
- SOCKS5/SOCKS5h handshake, authentication, and inbound Realm-routing tests
- subscription refresh stability, stale removal, parser diagnostics, and
  snapshot redaction
- DNS/TCP/TLS probe success, failure, and unknown-state behavior
- fuzzing of HTTP request parsing and package ZIP input
- package rollback/LKG tests
- Realm state-machine transition tests
- Graph transactional update tests
- race tests (`go test -race ./...`)

## Production-hardening backlog

Before production use, implement at least:

- WASM sandbox or process isolation for untrusted plugins
 - database-level transactions and crash recovery beyond the current atomic JSON files
- explicit secret handles instead of JSON plaintext credentials
- resource accounting, cancellation budgets, and connection limits
- package dependency/ABI resolution and dynamic activation
- signed trust-root management and key rotation
- TUN/platform adapters
- structured metrics and privacy-safe logging
- UDP/TUN and peer/remote Graph-driven composition beyond the bounded TCP baseline
- networked repository discovery/replication and peer transports (the resumable acquisition core and offline index are implemented)
- authenticated remote proxy mode, if remote listening is ever enabled
