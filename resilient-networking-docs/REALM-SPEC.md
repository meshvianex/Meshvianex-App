# REALM-SPEC.md

# Realm Specification

## 1. Purpose

This document defines the runtime model, configuration schema, lifecycle, isolation rules, dependency behavior, health model, and portability requirements for Realms.

A Realm is a first-class, isolated networking context that composes installed plugins into a reusable networking environment.

A Realm is not a protocol.

A Realm is not a process.

A Realm is not an OSI layer.

A Realm is a logical network execution context.

---

# 2. Core Definition

A Realm MAY contain:

- Inbound plugin instances
- DNS resolver instances
- Rule/routing engine instances
- Packet transform instances
- Stream transform instances
- Protocol instances
- Transport instances
- Outbound instances
- Health probes
- Control-plane routing policy
- Data-plane routing policy
- Resource limits
- Realm-local state
- Named input ports
- Named output ports

A Realm MUST NOT directly own installed plugin code.

Installed plugin code belongs to the global Plugin Registry.

A Realm owns plugin instances and their configuration.

---

# 3. Realm Identity

Every Realm MUST have:

```yaml
realm_id: uuid-or-stable-id
name: Human Readable Name
version: 1
```

Recommended metadata:

```yaml
description: ...
created_at: ...
updated_at: ...
tags:
  - work
  - mobile
```

`realm_id` must remain stable across export/import unless explicitly cloned.

---

# 4. Realm Schema

Conceptual structure:

```yaml
realm:
  id: ...
  name: ...

  inputs: ...
  outputs: ...

  components:
    inbounds: ...
    dns: ...
    rules: ...
    transforms: ...
    protocols: ...
    transports: ...
    outbounds: ...
    probes: ...

  data_plane: ...
  control_plane: ...

  health: ...
  failover: ...
  resources: ...
  dependencies: ...
  policies: ...
```

---

# 5. Realm Lifecycle

Required lifecycle states:

```text
Created
Configured
ResolvingDependencies
Ready
Starting
Running
Degraded
Stopping
Stopped
Failed
```

Optional additional states:

```text
Suspended
Recovering
WaitingForNetwork
WaitingForDependency
```

---

# 6. Lifecycle Transitions

Recommended transition model:

```text
Created
   ↓
Configured
   ↓
ResolvingDependencies
   ↓
Ready
   ↓
Starting
   ↓
Running
   ├──→ Degraded
   ├──→ Stopping
   └──→ Failed

Degraded
   ├──→ Running
   ├──→ Stopping
   └──→ Failed

Stopping
   ↓
Stopped
```

Invalid state transitions MUST be rejected.

---

# 7. Dependency Resolution

Before `Ready`, Core MUST resolve:

- Required plugins
- Plugin ABI compatibility
- Required plugin capabilities
- Required data packages
- Platform capabilities
- Required Graph ports
- Realm references
- Optional dependencies

Missing required dependency:

```text
WaitingForDependency
```

or:

```text
Failed
```

depending on policy.

---

# 8. Plugin Instance Binding

Realm components refer to installed plugins.

Example:

```yaml
components:
  dns:
    - instance_id: dns-main
      plugin: org.example.doh
      config:
        endpoint: ...
```

The plugin package is global.

The instance is Realm-local.

---

# 9. Instance Isolation

Each Realm plugin instance MUST have separate:

- Configuration
- Runtime state
- Resource accounting
- Event scope
- Storage namespace
- Health state

A plugin MAY share immutable code and read-only resources between instances.

Mutable Realm state MUST NOT be shared implicitly.

---

# 10. Realm Ports

Realms expose typed named ports.

Example:

```yaml
inputs:
  - name: local
    type: flow

  - name: upstream
    type: stream

outputs:
  - name: default
    type: flow

  - name: secure
    type: flow

  - name: blocked
    type: control
```

---

# 11. Standard Port Types

Initial standard port types:

```text
flow
packet
stream
datagram
control
metadata
```

Custom port types MAY exist only through namespaced extension schemas.

Graph validation MUST reject incompatible connections.

---

# 12. Default Ports

A Realm MAY define:

```yaml
default_input: local
default_output: default
```

Default ports are convenience only.

Explicit port references are preferred in reusable Graph definitions.

---

# 13. Inbound Components

Inbound plugins inject traffic into the Realm.

Examples:

- TUN
- SOCKS
- HTTP proxy
- Transparent proxy
- Local process bridge
- Realm-to-Realm input

Inbound output MUST produce a normalized Core traffic abstraction.

---

# 14. DNS Components

A Realm MAY contain multiple DNS resolvers.

Example:

```yaml
dns:
  resolvers:
    primary: doh-main
    backup: system-dns

  policy:
    default: primary
```

Resolvers may use different control-plane Realms.

---

### DNS Graph egress

Name resolution has a dedicated logical `dns` output in the active Graph. A DNS
request may terminate at Internet through this Realm's resolver chain or be
delegated to another Realm's input boundary, where that Realm's resolver
context continues resolution. This DNS egress is independent from the normal
user-traffic outbound selection.

# 15. Rule Engine

A Realm MAY use one or more rule engines.

Rules operate on normalized metadata.

Possible inputs:

- Source
- Destination
- Protocol
- Application metadata
- Purpose
- Tags
- Realm health
- Network health

Possible actions:

```text
RouteTo
UseRealm
UseResolver
Drop
Reject
Tag
Continue
```

---

# 16. Packet Transform Stages

Realm configuration may attach transforms to:

```text
pre_route
post_route
pre_protocol
post_protocol
pre_transport
post_transport
pre_socket
```

Example:

```yaml
transforms:
  pre_transport:
    - fragment-main
```

---

# 17. Stream Transform Stages

Supported stream stages:

```text
stream_open
handshake
stream_data
stream_close
```

Ordering MUST be deterministic.

---

# 18. Protocol Components

Protocol plugins operate above transports.

A protocol instance MUST declare required lower-layer services.

Example:

```yaml
protocol:
  instance_id: protocol-main
  plugin: org.example.protocol
  transport: transport-main
```

Core SHOULD validate protocol/transport compatibility before activation.

---

# 19. Transport Components

Transport components implement connection mechanics.

Examples:

- TCP
- UDP
- QUIC
- WebSocket
- Custom framed transports

Transport is distinct from routing.

---

# 20a. Config Selection

Config selection is a Realm-local component boundary after terminal egress and
before the concrete outbound. It may select one or more Config resources, or
the normalized active members of one Subscription:

```yaml
config:
  instance_id: config-main
  plugin: io.meshvianex.config.selector.basic
  enabled: true
  source_type: config         # config or subscription
  source_refs: [office, backup]
  config:
    strategy: round_robin
```

`source_refs` contains persisted resource identifiers; the legacy singular
`source_ref` remains supported. A Config source may contain one or more Config
IDs. A Subscription source contains one Subscription ID whose active members
are the candidates. Core resolves only enabled, valid, non-stale candidates and
passes their stable Config IDs to the selector.
The selector does not access the resource store or instantiate an outbound.
With multiple candidates, the selector applies its configured load-balancing
strategy (Round Robin, Consistent Hash, or Sticky Session).
When disabled, normal outbound routing remains in effect. An enabled selector
with a missing, invalid, or empty source cannot start until the resource state
is repaired; routing remains a separate decision and must not silently replace
an active selection contract.

The effective user-traffic path is:

```text
Routing -> Output port -> Terminal egress -> Config selection -> Outbound -> Protocol -> Stream Transport -> Socket
```

---

# 20. Outbound Components

Outbounds are the only routing targets. An outbound may be monolithic (for
example Direct, SOCKS, or HTTP) or may bind a protocol and stream transport.
The selected outbound owns that binding; Data Plane and Control Plane MUST NOT
override protocol or transport after routing.

Conceptual shape:

```yaml
outbounds:
  - instance_id: secure-main
    plugin: org.example.outbound.stack
    protocol: vless-main
    transport: raw-main
```

This keeps the effective path deterministic:

```text
Routing -> Outbound -> Protocol -> Stream Transport -> Socket
```

---

# 21. Data Plane

Data Plane carries user/network payload traffic. It selects Realm ports and,
optionally, an outbound override. It does not select protocol or transport.

Example:

```yaml
data_plane:
  input: local
  output: internet
  outbound: secure-main
```

---

# 22. Control Plane

Control Plane carries internal system traffic such as DNS resolution, plugin
downloads, repository metadata, subscription updates, and health checks. It
uses the same outbound model as the Data Plane and may select a distinct
outbound and Realm ports.

Example:

```yaml
control_plane:
  input: control-in
  output: control-out
  outbound: bootstrap-direct
```

---

# 23. Bootstrap Safety

A Realm MUST declare whether it is bootstrappable without itself.

Example:

```yaml
bootstrap:
  self_dependency_allowed: false
```

Core MUST detect dependency cycles.

Forbidden:

```text
Need Plugin X
  ↓
Download through Realm A
  ↓
Realm A requires Plugin X
```

---

# 24. Realm Dependencies

Realm dependencies MAY include:

```yaml
dependencies:
  plugins:
    - id: org.example.xray
      version: ">=5,<6"

  capabilities:
    - socket.tcp

  data:
    - id: geo.example
      version: ">=1"
```

---

# 25. Optional Dependencies

Optional dependency:

```yaml
optional_dependencies:
  - plugin: org.example.metrics
```

Realm MUST remain operational without optional dependencies unless explicitly configured otherwise.

---

# 26. Health Model

Realm health states:

```text
Healthy
Degraded
Unhealthy
Offline
Unknown
```

Health is independent from lifecycle.

Example:

```text
Lifecycle: Running
Health: Degraded
```

---

# 27. Health Inputs

Realm health may include:

- DNS success rate
- Connection success rate
- RTT
- Packet loss
- Jitter
- Throughput
- Handshake success
- Transport health
- Endpoint reachability
- Plugin self-health
- Graph edge health

---

# 28. Health Score

Optional normalized score:

```text
0..100
```

Example:

```yaml
health:
  status: degraded
  score: 61
```

Status thresholds should be configurable.

---

# 29. Health Probes

Health probes MAY be plugin-provided.

Example:

```yaml
health:
  probes:
    - dns-test
    - tcp-test
    - endpoint-test
```

Health probes run with bounded resources.

---

# 30. Failover Policy

Realm MAY define fallback outputs or alternate Realms.

Example:

```yaml
failover:
  strategy: ordered
  targets:
    - realm-primary
    - realm-backup
    - realm-local
```

---

# 31. Failover Triggers

Possible triggers:

- Health threshold
- Timeout count
- Packet loss threshold
- Endpoint failure
- DNS failure
- Transport unavailable
- Manual user selection
- Network change

---

# 32. Failback

Failover and failback are separate policies.

Example:

```yaml
failback:
  enabled: true
  stable_for: 30s
```

This prevents oscillation.

---

# 33. Hysteresis

Health switching SHOULD use hysteresis.

Example:

```text
Failover when score < 40
Failback when score > 70 for 30s
```

---

# 34. Resource Policy

Realm may define:

```yaml
resources:
  memory_mb: 128
  max_flows: 5000
  max_sockets: 500
  cpu_weight: normal
```

Core may enforce stricter platform limits.

---

# 35. Network Cost Policy

Realm MAY specify:

```yaml
network_policy:
  allow_metered: true
  allow_roaming: false
  prefer_wifi: false
```

This affects acquisition and control-plane behavior.

---

# 36. Realm Storage

Realm-local plugin storage SHOULD be namespaced:

```text
realm/<realm_id>/plugin/<plugin_id>/<instance_id>/
```

Plugins cannot read other Realm namespaces without explicit capability.

---

# 37. Secret Storage

Secrets SHOULD be referenced, not embedded directly in portable Realm config.

Example:

```yaml
private_key:
  secret_ref: secret://realm/work/wg/private_key
```

Export may optionally include encrypted secret material.

---

# 38. Realm Export

Realm export modes:

## Configuration Only

Contains:

- Realm config
- Plugin lockfile
- Dependency metadata

## Portable Bundle

Contains:

- Realm config
- Plugin lockfile
- Required packages
- Optional data assets
- Signatures

---

# 39. Realm Import

Import validation MUST check:

- Schema version
- Signature if bundle is signed
- Required plugin presence
- Plugin package validity
- Platform compatibility
- Capability compatibility
- Graph references
- Secret references
- Dependency cycles

---

# 40. Realm Clone

Cloning creates a new `realm_id`.

Plugin instances receive new instance IDs.

Secrets MAY be:

- Reused by reference
- Duplicated
- Omitted

depending on user policy.

---

# 41. Realm Inheritance

Optional configuration inheritance MAY exist.

Example:

```text
Base Realm
   ↑
Work Realm
```

Inheritance applies to configuration only.

Runtime state MUST NOT be inherited.

---

# 42. Inheritance Rules

Possible merge behavior:

```text
replace
append
merge
remove
```

Inheritance must remain deterministic.

Multiple inheritance is discouraged for v1.

---

# 43. Realm Templates

Template is not a running Realm.

Template may contain:

- Default plugins
- Config schema
- Port definitions
- Suggested policies

Instantiation produces a Realm.

---

# 44. Platform Portability

Realm config SHOULD be platform-neutral.

Platform-specific requirements are expressed as capabilities.

Example:

```yaml
requires_capabilities:
  - packet.l3

optional_capabilities:
  - packet.l2
```

---

# 45. Platform Compatibility State

Possible result:

```text
Compatible
CompatibleWithLimitations
RequiresPermission
Unsupported
```

The app MUST explain why.

---

# 46. Purpose-Aware Routing

Realm rules may inspect internal purpose metadata.

Example:

```yaml
if:
  purpose: plugin_download
then:
  use_output: download
```

Possible purposes:

```text
user_traffic
plugin_download
repository_metadata
subscription_update
health_check
peer_discovery
realm_sync
authentication
```

---

# 47. Realm Events

Suggested events:

```text
realm.created
realm.ready
realm.started
realm.health_changed
realm.degraded
realm.failed
realm.stopping
realm.stopped
realm.config_changed
realm.dependency_missing
```

Events MUST be typed and versioned.

---

# 48. Realm Diagnostics

Diagnostics should report component status.

Example:

```text
Realm: Work
Lifecycle: Running
Health: Degraded

Components:
  DNS          OK
  Rules        OK
  Protocol     OK
  Transport    FAILING
  Outbound     DEGRADED
```

---

# 49. Realm Validation

Static validation checks:

- Unique instance IDs
- Valid plugin IDs
- Valid plugin types
- Valid port names
- Valid transform stages
- No duplicate mandatory components
- Dependency resolution
- Platform compatibility
- Control-plane recursion
- Graph references

---

# 50. Runtime Validation

Runtime checks:

- Plugin started successfully
- Health probe success
- Required endpoint reachable
- Resource quotas respected
- No repeated fatal loops
- No dependency loss

---

# 51. Realm Recovery

Recovery options:

```text
Restart failed component
Restart Realm
Use fallback component
Use fallback Realm
Rollback plugin
Rollback Realm config
Enter degraded mode
```

---

# 52. Partial Operation

Realm MAY enter degraded mode when optional pieces fail.

Examples:

```text
Geo DB unavailable
→ basic routing continues

Metrics plugin failed
→ traffic unaffected

Backup DNS failed
→ primary DNS continues
```

Required component failure may stop the Realm.

---

# 53. Realm Versioning

Realm schema version is separate from Realm user version.

Example:

```yaml
schema_version: 1
revision: 17
```

`revision` tracks user/config changes.

---

# 54. Realm Revision History

Core SHOULD keep recent Realm revisions.

Supports:

- Undo
- Rollback
- Diff
- Last-known-good

---

# 55. Last-Known-Good Realm

A Realm may have:

```text
Current Revision
Previous Revision
Last Known Good Revision
```

Activation failure can restore LKG automatically.

---

# 56. Realm Lockfile

Example:

```yaml
realm_id: ...
plugins:
  org.example.xray:
    version: 5.1.0
    hash: sha256:...
```

Lockfile enables reproducibility.

---

# 57. Realm Security Rules

Realm config MUST NOT:

- Disable package signature checks
- Grant undeclared plugin capabilities
- Modify Core trust policy
- Access arbitrary filesystem paths
- Reference unavailable secrets without explicit handling
- Create hidden unrestricted cross-Realm calls

---

# 58. Realm API

Conceptual Core API:

```text
realm_create()
realm_load(config)
realm_validate()
realm_resolve_dependencies()
realm_start()
realm_stop()
realm_restart()
realm_health()
realm_export()
realm_clone()
realm_rollback()
```

---

# 59. Realm Query API

Read-only query examples:

```text
realm_list_components()
realm_list_ports()
realm_get_health()
realm_get_dependencies()
realm_get_capabilities()
realm_get_revision()
```

---

# 60. Realm Mutation API

Mutations should be transactional.

Example:

```text
begin_transaction
modify_config
validate
commit
```

If validation fails:

```text
rollback transaction
```

---

# 61. Hot Configuration Changes

Safe changes MAY apply live.

Examples:

- Rule updates
- Health thresholds
- Source priorities

Unsafe changes require restart.

Examples:

- Protocol replacement
- TUN ownership change
- ABI-incompatible instance changes

---

# 62. Realm Testing

Realm MUST support test execution in context.

Example:

```text
Run DNS Test through Realm A
Run TCP Test through Realm A
Run Repository Test through Realm A
```

Tests should not require permanent config mutation.

---

# 63. Realm Simulation

Development environment SHOULD support:

- Fake DNS failure
- Packet loss
- Latency
- Realm dependency failure
- Outbound failure
- Plugin crash

---

# 64. Example Realm

```yaml
schema_version: 1

realm:
  id: work
  name: Work

  inputs:
    - name: local
      type: flow

  outputs:
    - name: secure
      type: flow
    - name: direct
      type: flow

  components:
    dns:
      - instance_id: dns-main
        plugin: org.example.doh

    routing:
      instance_id: routing-main
      plugin: org.example.routing

    protocols:
      - instance_id: vpn-main
        plugin: org.example.wireguard

    outbounds:
      - instance_id: direct-main
        plugin: core.direct

  data_plane:
    input: in
    output: direct
    outbound: direct-main

  control_plane:
    outbound: direct-main

  health:
    probes:
      - dns
      - endpoint

  failover:
    strategy: ordered
```

---

# 65. Realm Success Criteria

Realm design is successful when:

1. Realm config is portable across supported OSes.
2. Installed plugin code is reused across Realm instances.
3. Realm state remains isolated.
4. Realm can be exported/imported.
5. Realm can operate in degraded mode.
6. Realm health drives Graph decisions.
7. Realm can be chained with another Realm.
8. Control-plane loops are detected.
9. Missing optional dependencies do not collapse the Realm.
10. Last-known-good rollback works offline.
