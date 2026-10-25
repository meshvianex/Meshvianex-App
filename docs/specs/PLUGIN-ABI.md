# PLUGIN-ABI.md

# Plugin ABI Specification

## 1. Purpose

This document defines the contract between the Core runtime and plugins.

The ABI must remain:

- Stable
- Versioned
- Capability-based
- Cross-platform
- Sandbox-friendly
- Explicit about ownership
- Safe under partial failure
- Backward-compatible where practical

The preferred portable plugin target is WebAssembly.

Native plugin support may exist as a secondary path for OS-specific or performance-sensitive functionality.

---

## 2. Design Goals

The Plugin ABI MUST:

- Let plugins evolve independently from Core
- Avoid direct plugin-to-plugin coupling
- Support many plugin categories
- Support per-Realm instances
- Support asynchronous operations
- Support capability negotiation
- Support host-controlled resource limits
- Avoid exposing raw OS handles by default
- Support structured configuration and errors
- Support self-tests
- Support schema-driven UI
- Allow interface extension without redesigning Core

---

## 3. ABI Layers

```text
Plugin Base ABI
├── Lifecycle API
├── Capability API
├── Config API
├── Logging API
├── Event API
├── Flow API
├── Packet API
├── Stream API
├── Datagram API
├── DNS API
├── Socket API
├── Storage API
├── Package API
├── Repository API
├── Transfer API
├── UI Schema API
├── Health API
└── Test API
```

Plugins import only APIs permitted by their granted capabilities.

---

## 4. Interface Versioning

Each public interface has its own major version.

Example:

```yaml
abi:
  plugin: 1
  lifecycle: 1
  config: 2
  packet: 3
  stream: 1
  dns: 2
```

Avoid a single `requires_core >= X` rule where possible.

Prefer:

```yaml
requires_api:
  plugin: ">=1,<2"
  packet: ">=3,<4"

optional_api:
  flow_metadata: ">=2,<4"
```

A breaking interface change increments that interface's major ABI version.

---

## 5. Plugin Manifest

Every plugin package MUST contain a signed manifest.

Example:

```yaml
id: org.example.xray
name: Xray
version: 5.0.0

runtime:
  type: wasm
  entry: modules/runtime.wasm

plugin_types:
  - vpn_protocol
  - outbound

abi:
  plugin: 1
  config: 2
  stream: 1
  socket: 2

requires_capabilities:
  - socket.tcp
  - socket.udp
  - timer.create
  - config.read

optional_capabilities:
  - network.observe

provides:
  - protocol.xray
  - outbound.proxy

entrypoints:
  init: plugin_init
  create_instance: plugin_create_instance
  destroy_instance: plugin_destroy_instance
```

The package signature should cover the manifest, module hashes, resources, dependency declarations, ABI requirements, and capability declarations.

---

## 6. Standard Plugin Types

Initial plugin types:

```text
inbound
outbound
protocol
vpn_protocol
proxy_protocol
transport
dns
routing
packet_transform
stream_transform
subscription
config_parser
repository_source
download_transport
peer_discovery
peer_transport
transfer_strategy
ui_provider
diagnostic
test_provider
metrics
storage_provider
```

`protocol` and `vpn_protocol` are Realm component contracts and require the
corresponding typed runtime interface. `proxy_protocol` is a capability or
package category, not an alias for a Realm protocol: a proxy inbound/outbound
plugin must advertise `inbound` or `outbound` and cannot be selected for a
Realm protocol slot unless it also implements the actual protocol contract.
The compiled-in HTTP and SOCKS proxy plugins intentionally advertise only
their inbound/outbound type.

A plugin MAY implement multiple types.

---

## 7. Lifecycle ABI

Required lifecycle entrypoints:

```text
plugin_init()
plugin_shutdown()

plugin_create_instance(context)
plugin_start_instance(instance)
plugin_stop_instance(instance)
plugin_destroy_instance(instance)
```

Optional lifecycle entrypoints:

```text
plugin_suspend_instance(instance)
plugin_resume_instance(instance)
plugin_reload_config(instance)
```

### `plugin_init`

- Called once per loaded module
- Must not perform unbounded work
- Receives immutable host information
- Must not assume a Realm exists

### `plugin_create_instance`

- Creates isolated state
- Receives an instance context
- Returns an opaque instance handle

### `plugin_start_instance`

- Starts active behavior
- May request host resources

### `plugin_stop_instance`

- Cancels or completes active work

### `plugin_destroy_instance`

- Releases instance state

---

## 8. Instance Context

Conceptual structure:

```yaml
instance_id: ...
plugin_id: org.example.xray
realm_id: optional
purpose: realm_component
platform:
  os: linux
  arch: x86_64
granted_capabilities:
  - socket.tcp
  - socket.udp
```

Plugins MUST NOT infer identity or configuration from global state.

---

## 9. Capability Model

Capabilities gate imported host functions.

Initial examples:

```text
packet.read
packet.write
packet.modify
packet.raw

stream.read
stream.write
stream.metadata

dns.resolve
dns.register

socket.tcp
socket.udp
socket.listen

storage.read
storage.write
storage.secret

config.read
config.propose_write

ui.schema
ui.events

network.observe

transfer.discover
transfer.connect
transfer.listen

package.read
package.emit

test.register

event.publish
event.subscribe

timer.create
crypto.random
```

Capabilities should be fine-grained enough that a UI plugin cannot automatically read packet contents, for example.

---

## 10. Capability Failure Semantics

If permission is not granted:

```text
ERR_CAPABILITY_DENIED
```

If the OS/platform cannot implement a granted capability:

```text
ERR_CAPABILITY_UNAVAILABLE
```

These cases are distinct.

---

## 11. Host Resource Handles

Plugins use opaque handles rather than host pointers.

Examples:

```text
InstanceHandle
FlowHandle
PacketHandle
StreamHandle
DatagramHandle
SocketHandle
ResolverHandle
TimerHandle
StorageHandle
PackageHandle
TransferHandle
TestHandle
SecretHandle
```

Plugins MUST NOT depend on the host's internal representation.

---

## 12. Ownership Semantics

Every handle or buffer has explicit ownership.

Possible modes:

```text
borrowed
owned
shared
```

Examples:

- Borrowed packet: valid only during callback
- Owned timer: plugin must close/cancel it
- Shared config snapshot: retained until release

Official SDK wrappers should enforce ownership rules where the language permits.

---

## 13. Error Model

All fallible operations return structured errors.

Generic codes:

```text
OK
ERR_INVALID_ARGUMENT
ERR_NOT_SUPPORTED
ERR_CAPABILITY_DENIED
ERR_CAPABILITY_UNAVAILABLE
ERR_TIMEOUT
ERR_CANCELLED
ERR_RESOURCE_LIMIT
ERR_NETWORK
ERR_PROTOCOL
ERR_INVALID_STATE
ERR_NOT_FOUND
ERR_ALREADY_EXISTS
ERR_IO
ERR_PARSE
ERR_INTEGRITY
ERR_TRUST
ERR_DEPENDENCY_CYCLE
ERR_INTERNAL
```

Errors may include redaction-safe diagnostic metadata.

---

## 14. String and Buffer Encoding

All strings are UTF-8.

Portable ABI strings and buffers use explicit length.

Conceptually:

```text
Buffer {
  ptr
  len
}
```

Null-terminated assumptions are forbidden in the portable ABI.

Large objects should use handles/streaming to avoid excessive copies.

---

## 15. Async Model

Network and long-running APIs are asynchronous.

Conceptual request model:

```text
request_id = host_call(...)
```

Completion:

```text
on_completion(request_id, result)
```

An implementation may use WASM async/component-model primitives later, but the semantic contract remains cancellable asynchronous work.

---

## 16. Cancellation

Every long-running host operation SHOULD be cancellable.

```text
cancel(request_handle)
```

Core may cancel work on:

- Realm shutdown
- Plugin unload
- Timeout
- Resource exhaustion
- User action
- Graph reconfiguration

Plugins MUST tolerate cancellation without corrupting state.

---

## 17. Config ABI

Core provides immutable configuration snapshots.

Conceptual APIs:

```text
config_get_snapshot(instance)
config_get(path)
config_watch(path)
```

Plugins may expose configuration schemas.

Example:

```json
{
  "type": "object",
  "properties": {
    "endpoint": {"type": "string"},
    "mtu": {"type": "integer"}
  }
}
```

Plugins should not directly mutate arbitrary global configuration.

Preferred write path:

```text
Plugin proposes change
      ↓
Core validates
      ↓
Policy/user approval if required
      ↓
Core commits
```

---

## 18. Event ABI

Plugins may subscribe to typed, versioned events.

Examples:

```text
realm.started
realm.stopped
realm.degraded
network.changed
download.completed
package.installed
config.changed
```

Subscriptions require `event.subscribe`.

Publishing requires `event.publish`.

---

## 19. Logging ABI

Conceptual API:

```text
log(level, event_name, fields)
```

Levels:

```text
trace
debug
info
warn
error
```

Plugins MUST NOT log secrets, private keys, credentials, or packet payloads by default.

---

## 20. Metrics ABI

Optional metric types:

```text
counter
gauge
histogram
```

Examples:

```text
plugin_connections_total
plugin_handshake_latency_ms
plugin_failures_total
```

High-cardinality sensitive labels should be rejected or discouraged.

---

## 21. Flow ABI

Flow metadata is exposed through controlled host functions.

Conceptual APIs:

```text
flow_get_id()
flow_get_protocol()
flow_get_destination()
flow_get_purpose()
flow_get_tag()
flow_set_tag()
```

Application identity and sensitive destination metadata may require separate capabilities.

---

## 22. Packet ABI

Read-only operations:

```text
packet_len(handle)
packet_read(handle, offset, len)
packet_metadata(handle)
```

Mutation requires `packet.modify`.

Possible operations:

```text
packet_write
packet_resize
packet_replace
packet_drop
packet_clone
```

Raw link-layer access is a stronger separate capability.

---

## 23. Packet Transform Contract

A packet transform declares stages:

```yaml
stages:
  - pre_route
  - post_protocol
```

Core invokes:

```text
on_packet(stage, packet_handle, flow_handle)
```

Possible results:

```text
CONTINUE
DROP
REPLACE
DEFER
ERROR
```

A plugin cannot attach itself to an undeclared stage.

---

## 24. Stream ABI

Stream lifecycle stages:

```text
stream_open
handshake
stream_data
stream_close
```

Host APIs:

```text
stream_read
stream_write
stream_close
stream_metadata
```

Plugins MUST support partial reads/writes.

---

## 25. Datagram ABI

Conceptual APIs:

```text
datagram_recv
datagram_send
datagram_peer
```

Datagram boundaries are preserved where the platform and transport permit it.

---

## 26. DNS ABI

Resolver plugin entrypoint:

```text
resolve(query)
```

Example query:

```yaml
name: example.org
record_type: A
context:
  realm_id: ...
  purpose: ...
```

Response:

```yaml
records:
  - ...
ttl: ...
status: ...
```

Core enforces limits and cancellation.

---

## 27. Socket ABI

Plugins should not open OS sockets directly in the portable runtime.

Host APIs:

```text
tcp_connect
tcp_listen
udp_bind
udp_connect
socket_send
socket_recv
socket_close
```

Requests may include:

```yaml
realm_binding: current
purpose: plugin_internal
timeout_ms: ...
priority: normal
```

This makes plugin networking Realm-aware.

---

## 28. Realm-Bound Networking

A plugin may request networking through:

```yaml
realm: current
```

or a permitted explicit Realm:

```yaml
realm: realm-id
```

Core evaluates graph/dependency recursion before opening the connection.

Example invalid cycle:

```text
Plugin X is missing
↓
Download X through Realm A
↓
Realm A requires Plugin X
```

Core returns:

```text
ERR_DEPENDENCY_CYCLE
```

---

## 29. Storage ABI

Three logical storage classes:

```text
ephemeral
persistent
secret
```

Conceptual APIs:

```text
storage_open(namespace)
storage_get
storage_put
storage_delete
storage_list
```

Secret storage requires `storage.secret`.

A plugin cannot access another plugin's namespace.

---

## 30. Secret Handles

Where feasible, secrets are referenced via opaque `SecretHandle` objects instead of raw bytes.

A protocol plugin may request cryptographic use of a secret without receiving its raw representation, depending on platform support.

---

## 31. Package ABI

Transfer/repository plugins may use package handles.

Possible APIs:

```text
package_open(hash)
package_read_chunk
package_get_manifest
package_emit_locator
```

No package plugin can bypass Core trust verification.

---

## 32. Repository Source ABI

A repository source plugin may implement:

```text
search(query)
list_versions(plugin_id)
get_manifest(plugin_id, version)
resolve_locations(object_hash)
```

A Source Provider returns metadata and locators.

It does NOT declare a package trusted.

---

## 33. Download Transport ABI

A download transport handles retrieval from a locator scheme.

Conceptual interface:

```text
supports(locator_scheme)
open(locator)
read_range(offset, length)
close()
```

Potential schemes:

```text
https
http
magnet
peer
lan
custom
```

Core remains responsible for:

- Chunk scheduling
- Hash checking
- Resume state
- Multi-source coordination
- Package assembly

---

## 34. Peer Discovery ABI

Discovery plugins report minimal peer information.

Example:

```yaml
peer_id: ...
capabilities:
  - package-transfer
addresses:
  - ...
```

Do not advertise full installed plugin inventory.

---

## 35. Peer Transport ABI

Conceptual operations:

```text
connect(peer)
offer(object_hash)
request(object_hash)
read_chunk
write_chunk
close
```

Package trust remains separate from peer transport.

---

## 36. Transfer Strategy ABI

Optional strategy plugins receive an abstract scheduling view:

```text
sources
peer health
network health
realm health
remaining chunks
cost policy
```

They return preferences, not absolute authority.

Core may override a strategy for safety or resource limits.

---

## 37. UI Schema ABI

Portable plugin UI is declarative.

Example:

```json
{
  "page": "settings",
  "title": "Example Plugin",
  "fields": [
    {
      "id": "endpoint",
      "type": "host_port",
      "label": "Endpoint"
    }
  ]
}
```

Portable ABI does not permit arbitrary native UI execution.

---

## 38. UI Event ABI

UI actions arrive as structured events.

Example:

```yaml
event: field_changed
field_id: endpoint
value: ...
```

Plugin may return:

- Validation result
- Derived state
- Suggested action

Core/UI retains authority over final configuration commit.

---

## 39. Test Provider ABI

Test plugins register tests.

Example:

```yaml
tests:
  - id: connectivity
    name: Connectivity Test
    requires:
      - socket.tcp
```

Runner invokes:

```text
test_start(context)
test_cancel(handle)
```

Test results are structured.

---

## 40. Plugin Self-Test ABI

A plugin may export:

```text
self_test()
```

Self-test runs:

- In sandbox
- With bounded resources
- With limited capabilities
- Before activation when policy requires

A failed mandatory self-test prevents activation.

---

## 41. Health Probe ABI

A plugin may expose:

```text
probe_health(instance)
```

Example result:

```yaml
status: healthy
metrics:
  latency_ms: 80
message: ok
```

Core aggregates plugin health into Realm health.

---

## 42. Rule Engine ABI

Rule engine input is normalized flow context.

Example:

```yaml
protocol: tcp
destination: ...
purpose: ...
tags: ...
realm: ...
```

Possible outputs:

```text
route_to
drop
reject
tag
continue
use_resolver
use_realm
```

Rule engines cannot directly mutate unrelated plugin state.

---

## 43. Inbound ABI

Inbound plugin responsibilities:

- Accept traffic
- Create/attach a Flow
- Attach permitted metadata
- Submit to a Realm input port

Conceptual host operations:

```text
flow_create
flow_submit
stream_create
datagram_create
```

---

## 44. Outbound ABI

Outbound plugins receive a Flow and a traffic abstraction.

They emit traffic using host socket APIs or a Graph-mediated endpoint.

They should not bypass Realm routing policy unless explicitly authorized.

---

## 45. Protocol Plugin ABI

Protocol plugins may expose conceptual operations such as:

```text
create_client_session
create_server_session
handshake
encode
decode
close
```

Core must not hard-code protocol-specific fields.

Protocol-specific configuration lives in plugin-defined schema.

---

## 46. Transport Plugin ABI

Transport plugins operate below protocol plugins.

Typical interface:

```text
connect
accept
send
recv
close
```

Protocol/transport composition is managed by Realm configuration and Core interfaces.

---

## 47. Subscription ABI

Subscription plugins may implement:

```text
parse
refresh
merge
deduplicate
validate
```

Fetch operations use host Realm-aware networking.

---

## 48. Config Parser ABI

Parsers translate external formats into normalized internal configuration.

```text
External Config
     ↓
Parser Plugin
     ↓
Normalized Realm/Plugin Model
```

Parsers may declare missing plugin dependencies but may not auto-trust or silently install unsigned code.

---

## 49. Native Plugin ABI

Native plugins are optional and stricter.

Requirements:

- Separate native ABI version
- Signed package
- Platform/architecture declaration
- Same capability model where possible
- Prefer out-of-process execution for third-party code
- No unrestricted host access by default

---

## 50. WASM Memory Rules

Portable plugin rules:

- Host and plugin memory are separate
- No persistent raw host pointers
- Large objects use handles
- Buffers have explicit lengths
- Owned allocations have explicit release semantics
- Host may revoke handles during shutdown/cancellation

---

## 51. Resource Accounting

Host may expose remaining quotas:

```text
memory_remaining
storage_remaining
socket_quota
task_quota
```

Plugins should degrade gracefully when limits are reached.

Core may terminate a misbehaving instance.

---

## 52. Time and Timers

Host APIs:

```text
timer_create
timer_cancel
clock_monotonic
clock_wall
```

Timeout logic should use monotonic time.

---

## 53. Randomness and Cryptography

Host may provide:

```text
crypto.random
```

Cryptographic implementations should use mature audited libraries.

Plugins never receive Core trust-store private keys.

---

## 54. State Migration

A new plugin version may expose:

```text
migrate_state(old_version, data)
```

Migration runs before activation.

Failure causes rollback.

---

## 55. Hot Reload

Optional interfaces:

```text
prepare_reload
serialize_state
restore_state
```

If unsupported, Core performs a normal stop/start transition.

---

## 56. Compatibility Rules

Compatible additions include:

- New optional host function
- New optional event
- New optional capability
- New enum value with unknown-safe semantics

Breaking changes include:

- Function signature change
- Ownership semantic change
- Required field removal
- Error semantic redefinition

Breaking changes require a new major version for the affected interface.

---

## 57. Unknown Field Handling

Structured formats should support forward-compatible parsing.

Plugins ignore unknown optional fields unless strict parsing is explicitly required.

Security-critical ambiguity may be rejected by Core.

---

## 58. Feature Negotiation

Conceptual API:

```text
host_supports_api("packet", 3)
host_supports_capability("flow.metadata")
```

Negotiation happens before activation.

---

## 59. Plugin Dependencies

Example:

```yaml
dependencies:
  plugins:
    - id: org.example.rules
      version: ">=2,<3"

  capabilities:
    - dns.resolve

  data:
    - id: geo.example
      version: ">=1"
```

Circular dependencies are rejected.

Optional dependencies must not prevent startup when absent.

---

## 60. Plugin Composition Rule

Plugins MUST NOT directly import another plugin implementation.

Preferred model:

```text
Plugin A
  ↓ Core service interface
Named capability/service
  ↓
Plugin B provider
```

Core mediates versioning, lifecycle, permissions, and failure.

---

## 61. Service Registry

Plugins may publish versioned named services.

Example:

```text
service: dns.resolver/example
version: 2
```

Consumers request services through Core.

Service calls are:

- Versioned
- Permission-checked
- Cancellable
- Memory-isolated
- Failure-aware

---

## 62. Realm Context Exposure

Only relevant Realm metadata is exposed:

```text
realm_id
instance_id
graph_port
purpose
```

Realm configuration remains namespace-scoped.

---

## 63. Plugin Update Safety

Before activating a new version:

```text
Verify Signature
    ↓
Check ABI
    ↓
Check Capabilities
    ↓
Resolve Dependencies
    ↓
Migrate State
    ↓
Run Self-Test
    ↓
Canary
    ↓
Activate
```

Failure triggers restoration of the previous known-good version.

---

## 64. ABI Security Invariants

Plugins can never:

- Disable signature validation
- Silently add trusted publisher keys
- Access arbitrary filesystem paths by default
- Read arbitrary process memory
- Open unrestricted sockets without permission
- Read other plugin storage
- Read unrelated secrets
- Directly mutate another Realm
- Mark their own package as trusted
- Override Core package hash verification
- Redefine Core security tests

---

## 65. ABI Conformance Testing

The official SDK should include tests for:

- Lifecycle correctness
- Handle ownership
- Capability denial
- Timeout behavior
- Cancellation
- Memory/resource limits
- Invalid input handling
- Unknown fields
- Version negotiation
- Crash/trap isolation
- State migration

---

## 66. Reference Plugin Skeleton

Conceptual structure:

```text
plugin_init(host_info)

plugin_create_instance(ctx):
    validate_config()
    return instance

plugin_start_instance(instance):
    register_handlers()
    return OK

on_flow(instance, flow):
    ...

plugin_stop_instance(instance):
    cancel_tasks()

plugin_destroy_instance(instance):
    free_state()

plugin_shutdown():
    ...
```

---

## 67. Example DNS Plugin

```yaml
id: org.example.doh
plugin_types:
  - dns

requires_capabilities:
  - socket.tcp
  - dns.register

provides:
  - dns.resolver
```

Flow:

```text
Core Resolver
   ↓
DoH Plugin
   ↓
Host TCP Socket
   ↓
Selected Realm
```

---

## 68. Example Packet Transform Plugin

```yaml
id: org.example.transform
plugin_types:
  - packet_transform

requires_capabilities:
  - packet.read
  - packet.modify

stages:
  - pre_transport
```

Core invokes it only at declared stages.

---

## 69. Example Repository Plugin

```yaml
id: org.example.community-repo
plugin_types:
  - repository_source

requires_capabilities:
  - socket.tcp
  - config.read

provides:
  - repository.source
```

It returns package locations and metadata, not trust decisions.

---

## 70. Example Transfer Plugin

```yaml
id: org.example.lan-transfer
plugin_types:
  - peer_discovery
  - peer_transport

requires_capabilities:
  - transfer.discover
  - transfer.listen
  - socket.udp
  - socket.tcp
```

---

## 71. Example Test Plugin

```yaml
id: org.example.network-tests
plugin_types:
  - test_provider

requires_capabilities:
  - socket.tcp
  - socket.udp
  - dns.resolve
  - network.observe
```

Possible tests:

```text
dns_test
tcp_test
udp_test
realm_health_test
```

---

## 72. SDK Requirements

The official SDK should provide:

- Manifest types
- ABI bindings
- Capability helpers
- Safe handle wrappers
- Async helpers
- Logging helpers
- Config helpers
- Test helpers
- UI schema helpers
- Conformance tests
- Package builder
- Local simulator

---

## 73. Developer Tooling

Recommended CLI surface:

```text
plugin init
plugin build
plugin test
plugin lint
plugin sign
plugin pack
plugin inspect
plugin run-sandbox
plugin compatibility
```

---

## 74. ABI Stability Policy

Suggested policy:

- Major ABI versions receive long support windows
- Deprecated functions remain available until a documented major transition
- Plugins may declare compatibility with multiple interface versions
- Core explicitly reports supported interfaces
- Security-sensitive behavior never changes silently

---

## 75. Minimal ABI v1

The first implementation should remain intentionally small.

Recommended v1 interfaces:

```text
Lifecycle
Config
Logging
Capability queries
Event subscription
Basic Flow
Basic Stream
Basic DNS
Basic TCP/UDP socket
Basic storage
Test registration
UI schema
```

Advanced packet mutation, service registry, peer transfer, and complex download APIs can be introduced after the base contract proves stable.

---

## 76. ABI Success Criteria

The ABI is successful if:

1. A plugin built for an older compatible Core still runs on a later Core.
2. Capability restrictions are actually enforceable.
3. Portable plugins can run across supported OSes without OS-specific source changes when using portable APIs.
4. A plugin crash does not crash Core.
5. A plugin cannot access undeclared resources.
6. Plugins can be independently updated.
7. Realm-specific instances remain isolated.
8. Self-tests run before activation when required.
9. Core remains protocol-agnostic.
10. New plugin categories can be added without redesigning the entire runtime.
