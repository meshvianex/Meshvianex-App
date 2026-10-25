# ARCHITECTURE.md

# Resilient Modular Networking Platform Architecture

## 1. Purpose

This document defines the high-level software architecture for the platform described in `PLAN.md`.

The system is a cross-platform, plugin-first, offline-capable networking runtime intended for unreliable, filtered, intermittent, or partially disconnected networks.

The architecture is built around these first-class concepts:

- Core
- Plugin
- Realm
- Graph
- Content Acquisition
- Trust
- Testing
- Platform Adapter

The central architectural principle is:

> Keep the trusted Core small, stable, portable, and policy-oriented while allowing most networking behavior and product evolution to happen through plugins and data-driven configuration.

---

## 2. Goals

The architecture MUST support:

- Cross-platform execution
- Stable Core with infrequent updates
- Independently updateable plugins
- Offline plugin installation
- Device-to-device plugin transfer
- Multiple repositories and mirrors
- Multiple package download transports
- Realm-based routing
- Realm-to-Realm composition
- Multi-source resumable downloads
- Health-aware failover
- Graceful degradation
- Strong package authenticity verification
- Sandboxed plugin execution
- Reproducible Realm bundles
- Runtime testing and diagnostics
- Last-known-good rollback
- Operation without a mandatory central store

The architecture does NOT assume that every OS exposes identical networking capabilities.

---

## 3. System Context

```text
┌──────────────────────────────────────────────┐
│               Application UI                 │
└───────────────────────┬──────────────────────┘
                        │
┌───────────────────────▼──────────────────────┐
│                    Core                      │
│                                              │
│ Plugin Runtime      Realm Manager            │
│ Graph Engine        Trust Engine             │
│ Acquisition Engine  Test Engine              │
│ Config Engine       Package Manager          │
│ Event Bus           Health Engine            │
└───────────────────────┬──────────────────────┘
                        │
             Platform Abstraction Layer
                        │
      ┌─────────────────┼─────────────────┐
      ▼                 ▼                 ▼
   Windows            Linux            Android
      │                                   │
      └─────────────────┬─────────────────┘
                        ▼
                      macOS
```

Plugins attach only to Core-defined extension points.

Realms instantiate plugins.

Graphs connect Realm ports.

The Acquisition Engine obtains packages and other content from local, peer, repository, or Internet sources.

The Trust Engine verifies what has been obtained.

The Test Engine evaluates health and compatibility.

---

## 4. Core Boundary

### 4.1 Core responsibilities

Core owns:

- Plugin loading/unloading
- Plugin sandbox/runtime
- Capability enforcement
- Plugin lifecycle
- ABI/API negotiation
- Realm lifecycle
- Graph lifecycle
- Flow identity
- Packet/stream/datagram abstractions
- Package verification
- Signature verification
- Content-addressed storage
- Dependency resolution
- Persistent state
- Rollback/recovery primitives
- Test execution environment
- Network condition collection
- Event routing
- Platform capability discovery
- Bootstrap import/install/networking

### 4.2 Outside Core by default

The following should normally be plugins:

- VPN/proxy protocols
- Advanced DNS implementations
- Rule engines
- Geo databases
- Subscription formats
- Packet/stream transforms
- Obfuscation transports
- Download transports
- Repository providers
- Peer transfer protocols
- Optional UI modules
- Diagnostics modules
- Test scenarios

Core MUST NOT know protocol-specific concepts such as `VLESS`, `WireGuard`, or `Xray` unless a bootstrap requirement absolutely forces it.

---

## 5. Core Modules

Suggested layout:

```text
core/
├── runtime/
├── plugin/
├── abi/
├── realm/
├── graph/
├── flow/
├── packet/
├── stream/
├── dns/
├── trust/
├── package/
├── acquisition/
├── transfer/
├── health/
├── testing/
├── config/
├── storage/
├── event/
├── platform/
└── bootstrap/
```

Avoid hidden global state. Cross-module interactions should be explicit and typed.

---

## 6. Plugin Runtime

### 6.1 Preferred execution model

- WASM for portable plugins
- Native plugins only where necessary
- Process isolation for high-risk native plugins when practical

The runtime MUST provide:

- Memory isolation
- Capability-gated host APIs
- CPU/time limits
- Memory limits
- Cancellation
- Trap/crash isolation
- Structured logging
- Lifecycle callbacks
- Interface negotiation

### 6.2 Plugin identity

Every plugin has an immutable globally unique ID, for example:

```text
org.example.xray
```

Display names may change; IDs must not.

---

## 7. Plugin Instance Model

Installed plugin code is global; runtime state is per instance.

```text
Plugin Registry
└── org.example.dns
    ├── Realm A / instance 1
    ├── Realm B / instance 2
    └── Test / instance 3
```

A plugin MUST NOT assume one global configuration.

---

## 8. Realm Model

A Realm is an isolated logical networking context.

A Realm MAY contain:

- Inbounds
- DNS
- Rules/routing
- Packet transforms
- Stream transforms
- Protocols
- Transports
- Outbounds
- Health probes
- Data-plane policy
- Control-plane policy
- Resource limits

Example:

```text
Realm: Work
├── Inbound: TUN
├── DNS: DoH
├── Rules: Work Rules
├── Transform: Transform X
├── Protocol: WireGuard
└── Outbound: Default
```

Plugin code is shared globally; each Realm owns separate plugin instances/configuration.

---

## 9. Realm Lifecycle

Suggested states:

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

A Realm MUST NOT enter `Running` until required dependencies are available and graph validation succeeds.

---

## 10. Realm Ports

Realms expose typed named ports.

```yaml
inputs:
  - local
  - upstream

outputs:
  - default
  - secure
  - direct
  - blocked
```

Possible port types:

- packet
- stream
- datagram
- flow
- control
- metadata

Graph validation MUST reject incompatible port connections.

---

## 11. Graph Engine

The Graph Engine connects Realm ports and platform endpoints.

```text
Platform TUN
    ↓
Realm A.local
    ↓
Realm A.secure
    ↓
Realm B.upstream
    ↓
Internet
```

The graph supports:

- Directed edges
- Branching
- Chaining
- Multiple outputs
- Conditional edges
- Health-based selection
- Failover
- Weighted selection
- Explicit re-entry
- Cycle detection
- Dependency validation

### 11.1 Safety

The engine MUST detect:

- Infinite routing loops
- Bootstrap dependency loops
- Invalid port types
- Missing endpoints
- Capability incompatibility
- Dead-end routes
- Control-plane recursion

Cycles are rejected unless a future bounded re-entry primitive explicitly permits them.

---

## 12. Flow Model

A Flow is a logical unit of traffic.

```yaml
flow_id: ...
protocol: tcp
source: ...
destination: ...
purpose: user_traffic
realm: ...
priority: normal
```

Internal traffic uses explicit purpose values such as:

- `plugin_download`
- `repository_metadata`
- `subscription_update`
- `health_check`
- `peer_discovery`
- `realm_sync`
- `authentication`

Realm rules may route based on purpose.

---

## 13. Packet, Stream, and Datagram Abstractions

Core should expose typed abstractions rather than raw bytes everywhere:

```text
Packet
Stream
Datagram
Flow
Endpoint
Route
Resolver
Transport
```

Raw access requires stronger capabilities.

---

## 14. Transform Pipeline

Packet-oriented stages:

```text
pre_route
post_route
pre_protocol
post_protocol
pre_transport
post_transport
pre_socket
```

Stream-oriented stages:

```text
stream_open
handshake
stream_data
stream_close
```

A transform plugin declares which stages it supports.

---

## 15. DNS Architecture

Core exposes a Resolver abstraction.

DNS plugins may provide:

- System DNS
- DoH
- DoT
- Split DNS
- Fake-IP
- Custom resolvers

A Realm can select one or more resolvers.

DNS control traffic itself can be routed through another Realm.

---

## 16. Routing and Rules

Rule engines produce standardized actions rather than directly controlling unrelated plugin internals.

Possible actions:

```text
RouteTo(port)
Drop
Reject
ResolveVia(resolver)
UseRealm(realm_id)
Tag(metadata)
Continue
```

---

## 17. Data Plane vs Control Plane

Each Realm distinguishes:

### Data Plane

User/network payload traffic.

### Control Plane

Internal runtime traffic such as:

- Endpoint resolution
- Plugin download
- Authentication
- Health probes
- Repository fetch

Example:

```yaml
data_plane:
  output: secure

control_plane:
  output: direct
```

This separation helps prevent dependency loops.

---

## 18. Content Acquisition Architecture

The Acquisition Engine retrieves verified content objects.

Core requests content by identity:

```text
Acquire(hash)
```

Content may include:

- Plugin package
- Plugin chunk
- Realm bundle
- Ruleset
- Repository metadata
- UI resource
- Test resource

The acquisition path is source-agnostic.

---

## 19. Acquisition Pipeline

```text
Resolve Object
    ↓
Discover Sources
    ↓
Rank Sources
    ↓
Choose Transport
    ↓
Choose Realm
    ↓
Download Chunks
    ↓
Verify Chunks
    ↓
Assemble Object
    ↓
Verify Object
    ↓
Store
```

---

## 20. Adaptive Download Scheduler

The scheduler chooses:

```text
chunk × source × transport × realm
```

Inputs include:

- Network health
- Realm health
- Source speed
- Source reliability
- Data cost
- Battery policy
- User policy
- Transport support
- Package priority

It must support dynamic reassignment when sources or Realms fail.

---

## 21. Persistent Resume

Download state MUST survive:

- App restart
- Process crash
- Device reboot
- Network switch
- Realm switch
- Source loss

Suggested state:

```yaml
object_hash: ...
total_size: ...
chunk_size: ...
completed_chunks: ...
known_sources: ...
retry_state: ...
```

---

## 22. Transfer Architecture

Transfer has three separable layers:

```text
Discovery
Transport
Strategy
```

### Discovery

Find peers.

### Transport

Move bytes.

### Strategy

Decide how to use available peers/transports.

This lets Bluetooth discover a peer while Wi-Fi Direct transfers the payload, for example.

---

## 23. Offline Sharing

Supported modes:

### Full

Transfer package and dependencies.

### Manifest Only

Transfer identity/version/hash/source information.

### Smart

Negotiate missing hashes and transfer only missing objects.

```text
Sender: Do you have hash A?
Receiver: Yes.
Sender: Skip A.
Receiver: Need B and C.
```

---

## 24. Offline Repository

Any device may host a portable repository:

```text
Offline Repository
├── Plugins
├── Rules
├── Geo data
├── Realm templates
├── Source metadata
├── Trusted publisher keys
└── Package manifests
```

Repository replication should operate by content hashes and support partial synchronization.

---

## 25. Delay-Tolerant Distribution

The system should support store-and-forward exchange:

```text
Internet
   ↓
Alice
   ↓ hours later
Bob
   ↓ days later
Carol
```

Sender, receiver, and Internet access do not need to exist simultaneously.

---

## 26. Trust Engine

Trust chain:

```text
Publisher Key
    ↓
Signed Manifest
    ↓
Package Hashes
    ↓
Content Objects
```

Principles:

> Transport does not imply trust.

> Repository does not imply trust.

> Peer identity does not imply package authenticity.

Plugins cannot silently alter the trust store or disable verification.

---

## 27. Package Manager

Responsibilities:

- Parse manifest
- Verify format
- Verify signature
- Resolve dependencies
- Check platform compatibility
- Install atomically
- Run self-tests
- Activate
- Roll back
- Remove
- Export/import

Suggested states:

```text
Discovered
Downloading
Downloaded
Verified
Installed
Testing
Active
Degraded
Disabled
RollbackAvailable
Removed
```

---

## 28. Last-Known-Good

Critical plugins and Realms should retain a previous working state.

```text
New Version
    ↓
Verify
    ↓
Self-Test
    ↓
Canary
    ↓
Activate
```

Failure:

```text
Rollback
```

Rollback MUST work offline.

---

## 29. Test Architecture

Core contains the trusted Test Engine.

Plugins may provide:

- Network probes
- DNS tests
- TCP/UDP tests
- Protocol tests
- Realm tests
- Repository tests
- Download tests
- Peer transfer tests
- Chaos scenarios

Core controls:

- Isolation
- Capabilities
- Timeouts
- Resource limits
- Result format
- Cancellation

Plugins cannot redefine Core security invariants.

---

## 30. Network Condition Engine

Observe:

- RTT
- Jitter
- Loss
- DNS success
- TCP success
- UDP success
- TLS success
- Throughput
- Stalls
- Realm reachability
- Local-only state
- Internet reachability

Possible state model:

```text
Healthy
Degraded
Unstable
Restricted
Local-only
Offline
```

State changes emit events consumed by Realm and acquisition policies.

---

## 31. Event Bus

Use typed, versioned events.

Examples:

- Plugin events
- Realm events
- Graph events
- Download events
- Health events
- Package events
- Test events
- Platform events

Avoid public free-form string events as the primary API.

---

## 32. Storage Architecture

Logical stores:

```text
Config Store
State Store
Secret Store
Content Store
Cache
Logs
```

Secrets should use platform secure storage where possible.

Plugins should receive scoped handles rather than unrestricted filesystem access.

---

## 33. Content-Addressed Store

Objects are keyed by cryptographic hash.

Benefits:

- Deduplication
- Resume
- Verification
- Multi-source acquisition
- Peer exchange
- Repository replication
- Efficient rollback

Garbage collection should use reference tracking.

---

## 34. Platform Abstraction Layer

Core should not directly call OS APIs outside platform adapters.

Suggested host abstractions:

```text
tun.create
route.add
route.remove
dns.apply
socket.tcp
socket.udp
network.observe
secure_store.get
secure_store.put
power.state
metered.state
notification.show
```

### Windows

Potential integrations:

- Virtual adapter
- WFP
- Native route APIs
- Service integration

### Linux

Potential integrations:

- TUN/TAP
- Netlink
- Route tables
- Optional namespaces

### Android

Potential integrations:

- VpnService
- ConnectivityManager
- Foreground service
- Keystore

### macOS

Potential integrations:

- Network Extension
- Packet Tunnel Provider
- Keychain

---

## 35. Platform Capability Discovery

Each adapter reports capabilities, for example:

```yaml
packet_l3: true
packet_l2: false
raw_socket: limited
background_networking: true
secure_store: true
```

Plugin loading and Realm activation use capability negotiation.

---

## 36. UI Architecture

UI is outside Core trust logic.

```text
UI Shell
   ↓
Application Service API
   ↓
Core
```

Plugin UI should preferably be declarative/schema-driven so it remains portable.

If custom UI is unavailable, the application should render a generic configuration UI.

---

## 37. Observability and Privacy

Diagnostics should identify the failing stage:

```text
Platform TUN     OK
Realm            OK
DNS              OK
Rule Engine      OK
Protocol         OK
Transport        FAIL
Remote Endpoint  UNKNOWN
```

Default logs MUST NOT contain:

- Private keys
- Credentials
- Secret tokens
- Packet payloads
- Full destination history

Peer inventory must remain private by default.

---

## 38. Bootstrap Architecture

Bootstrap remains intentionally small and independent of optional plugins.

Core must always be able to:

- Import local package
- Verify package
- Install package
- Import/export lightweight tickets
- Export installed package where policy permits
- Use minimal direct networking where supported
- Perform minimal repository fetch where possible
- Access local/offline package sources

This prevents dependency deadlocks.

---

## 39. Dependency Resolution

Dependencies may refer to:

- Plugin IDs
- API versions
- Capabilities
- Platform capabilities
- Data packages
- Realm availability

Resolver detects:

- Missing dependency
- Circular dependency
- Version conflict
- Platform mismatch
- Bootstrap recursion

Prefer capability/API requirements over a monolithic minimum Core version.

---

## 40. Failure Isolation

A plugin crash must not crash the whole application.

```text
Plugin traps
    ↓
Instance failed
    ↓
Realm degraded
    ↓
Fallback / rollback
```

Native plugins should be isolated out-of-process when practical.

---

## 41. Resource Limits

Per-plugin limits may include:

- Memory
- CPU time
- Execution duration
- Socket count
- Concurrent tasks
- Storage
- Download bandwidth
- Log volume

Limits are capability and policy aware.

---

## 42. Concurrency Model

Prefer async execution with bounded work queues rather than one thread per flow.

Suggested model:

- Async runtime
- Bounded task pools
- Dedicated blocking pool
- Cancellation propagation

---

## 43. Security Boundaries

Primary boundaries:

```text
User
  ↓
UI
  ↓
Core
  ↓
Plugin Sandbox
  ↓
External Network
```

Additional boundaries:

- Repository
- Peer
- Package publisher
- Platform privileged service
- Native plugin process

Every boundary crossing requires explicit validation.

---

## 44. Update Architecture

Three update classes:

```text
Plugin Update
Runtime Component Update
Core Update
```

All updates require:

- Signature verification
- Atomic deployment
- Rollback
- Version history
- Interrupted-update recovery

Ordinary feature evolution should primarily use Plugin Updates.

---

## 45. Offline-First Requirement

The following MUST work without Internet:

- Start installed Realm
- Switch Realm
- Export/import Realm
- Export/import plugin
- Roll back plugin
- Run local tests
- View diagnostics
- Use local repository
- Transfer packages locally

---

## 46. Reference User Traffic Flow

```text
OS App
  ↓
Platform Adapter
  ↓
Inbound
  ↓
Realm A
  ↓
Rules
  ↓
Transform
  ↓
Protocol
  ↓
Transport
  ↓
Realm B
  ↓
Outbound
  ↓
Network
```

---

## 47. Reference Plugin Download Flow

```text
Plugin Manager
   ↓
Dependency Resolver
   ↓
Source Provider
   ↓
Acquisition Scheduler
   ↓
Transport
   ↓
Selected Realm
   ↓
Network
   ↓
Chunks
   ↓
Hash Verification
   ↓
Package Verification
   ↓
Self-Test
   ↓
Install / Activate
```

---

## 48. Suggested Implementation Stack

Tentative choices:

```text
Core:             Rust
Portable plugins: WASM
Persistence:      SQLite or similar transactional embedded DB
Content store:    Filesystem + metadata DB
Schemas:          Stable typed schemas / JSON Schema where appropriate
UI:               Flutter or platform-native shell after prototype evaluation
Crypto:           Mature audited libraries only
```

These choices require prototype validation before being frozen.

---

## 49. Architecture Decision Records

Record major irreversible decisions in `docs/adr/`.

Examples:

```text
0001-rust-core.md
0002-wasm-plugin-runtime.md
0003-realm-graph-model.md
0004-content-addressed-packages.md
0005-trust-model.md
```

---

## 50. Architectural Invariants

1. Core security policy is not redefinable by plugins.
2. Transport does not imply trust.
3. Repository does not imply publisher authenticity.
4. Offline import remains possible.
5. Realm communication uses Graph edges.
6. Cross-Realm direct plugin calls are prohibited.
7. Plugin inventory is private by default.
8. Critical activation paths support rollback.
9. Optional failures degrade rather than collapse unrelated functionality.
10. Platform-specific behavior lives behind platform interfaces.
11. Plugin compatibility is capability/API driven.
12. Bootstrap does not depend on optional plugins.
13. Internal network operations are Realm-aware where practical.
14. No mandatory central store exists.
15. Data-plane and control-plane traffic remain distinguishable.

---

## 51. MVP Architecture

The architecture-validating MVP should contain:

```text
Core
├── Plugin Runtime
├── Capability Engine
├── Realm Manager
├── Graph Engine
├── Package Manager
├── Trust Engine
├── Basic Acquisition Engine
└── Basic Test Engine

Platform
└── Linux first

Plugins
├── Direct outbound
├── Basic DNS
├── Simple rule engine
└── One protocol demo
```

The MVP exists to validate architectural boundaries, not feature breadth.

---

## 52. Success Criteria

The architecture is viable when:

- A plugin can be installed offline
- Plugin authenticity is verified
- Plugin runs in isolation
- Capabilities are enforced
- Two Realms can be chained
- Graph routing can react to health
- Package download can use a Realm
- Download survives restart
- Package can come from multiple sources
- Realm can be exported/reproduced
- Failed plugin update can roll back offline
- Core remains protocol-agnostic

---

## 53. Next Specifications

This document is complemented by:

- `PLUGIN-ABI.md`
- `REALM-SPEC.md`
- `GRAPH-SPEC.md`
- `PACKAGE-SPEC.md`
- `TRUST-MODEL.md`
- `ACQUISITION-SPEC.md`
- `TRANSFER-SPEC.md`
- `TESTING-SPEC.md`
- `SECURITY.md`
- `THREAT-MODEL.md`
- `PLATFORM-API.md`
