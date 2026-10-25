# PLAN.md

# Resilient Modular Networking Platform

## 1. Project Vision

Build a cross-platform, plugin-first, offline-capable networking platform designed for unreliable, filtered, intermittent, or partially disconnected networks.

The product should not be treated as a conventional VPN client. Its core purpose is to provide a stable runtime in which networking capabilities can be composed, extended, transferred between devices, tested, updated independently, and operated even when access to the global Internet is degraded or unavailable.

The long-term architectural goal is:

> Keep the Core small and stable. Move features, protocols, transports, policies, UI extensions, transfer methods, testing logic, and network behavior into independently installable plugins wherever safely possible.

The system should remain useful under the following conditions:

- Normal Internet access
- Heavy packet loss and high latency
- Highly unstable mobile networks
- DNS interference
- Partial protocol blocking
- Selective filtering
- Different behavior across ISPs or networks
- Temporary loss of international connectivity
- LAN-only or local-network-only connectivity
- Completely offline plugin/package transfer between devices

---

## 2. Core Design Principles

### 2.1 Stable Core, Evolving Ecosystem

The Core should rarely require updates for new features.

Normal feature evolution should happen through:

- Plugins
- Realm configurations
- Graph definitions
- Repository metadata
- Transfer plugins
- Test plugins
- UI schemas
- Rulesets

Core updates remain possible for:

- Critical security fixes
- Plugin runtime vulnerabilities
- Sandbox failures
- OS API changes
- ABI/API evolution that cannot be handled through compatibility layers
- Bootstrapping or recovery defects

The goal is not "Core never updates".

The goal is:

> Adding ordinary capabilities should not require updating the Core.

---

## 3. Fundamental Concepts

The architecture is built around six first-class concepts:

1. **Plugin**
2. **Realm**
3. **Graph**
4. **Content Acquisition**
5. **Trust**
6. **Testing**

These concepts should remain more fundamental than any specific protocol such as WireGuard, Xray, OpenVPN, VLESS, Shadowsocks, DoH, or any particular transport.

---

# 4. Core Architecture

## 4.1 Minimal Core Responsibilities

The Core should contain only functionality that must be trusted, portable, and always available.

Suggested Core responsibilities:

- Plugin loader/runtime
- Plugin ABI/API
- Capability and permission enforcement
- Plugin lifecycle management
- Realm manager
- Graph engine
- Event bus
- Flow abstraction
- Packet/stream/datagram abstraction
- Configuration engine
- Package verification
- Signature verification
- Content-addressed cache
- Resource limits
- Crash isolation
- Persistent state
- Recovery mode
- Rollback support
- Bootstrap networking
- Bootstrap file import/export
- Core test invariants
- Platform abstraction layer

The Core should not contain protocol-specific behavior unless absolutely necessary for bootstrapping.

---

# 5. Plugin System

## 5.1 Plugin Categories

The system should support extensibility in at least the following domains:

### Networking

- Inbound
- Outbound
- VPN protocol
- Proxy protocol
- Transport
- Routing
- Rules
- DNS
- Packet transformation
- Stream transformation
- Traffic shaping
- Congestion strategy
- Network discovery

### Data / Configuration

- Subscription management
- Subscription parser
- Config parser
- Import/export format
- Ruleset provider
- Geo database
- Repository source provider
- Package metadata provider

### Distribution

- Download transport
- Peer transfer
- LAN transfer
- Wi-Fi Direct
- Bluetooth
- Torrent/swarming
- Relay
- QR/manifest exchange
- Custom content source

### User Experience

- UI schema provider
- Settings panel
- Dashboard widgets
- Diagnostics panel
- Theme extensions

### Operations

- Logging
- Metrics
- Diagnostics
- Testing
- Network probes
- Health checks
- Automation

---

## 5.2 Capability-Based Plugin Security

Plugins should declare capabilities in their manifest.

Example:

```yaml
id: org.example.packet-transform
version: 1.2.0
type: packet_transform

requires:
  - packet.read
  - packet.modify
  - flow.metadata

provides:
  - packet.transform
```

Possible capabilities:

```text
packet.read
packet.write
packet.modify
stream.read
stream.write
dns.resolve
socket.tcp
socket.udp
storage.read
storage.write
config.read
config.write
ui.panel
ui.schema
transfer.connect
transfer.listen
transfer.discover
package.read
package.write_temp
flow.metadata
timer.create
network.observe
```

Plugins should not receive direct filesystem or unrestricted OS access unless explicitly granted.

---

## 5.3 Portable Plugin Runtime

Prefer a portable runtime for most plugins.

Primary candidate:

- WebAssembly / WASM

Benefits:

- Cross-platform execution
- Stronger sandboxing
- Easier package portability
- Reduced platform-specific binaries
- Explicit host capability API

Native plugins may still be supported for performance-sensitive or OS-specific functionality, but must declare platform and architecture compatibility.

Example variants:

```text
windows-x64
windows-arm64
linux-x64
linux-arm64
android-arm64
macos-arm64
```

---

# 6. Realm Architecture

## 6.1 Realm Definition

A Realm is an isolated networking context.

Each Realm may contain its own:

- Inbounds
- DNS
- Rules
- Routing
- Packet transformations
- Stream transformations
- Protocols
- Transports
- Outbounds
- Subscription sources
- Health policy
- Failover policy
- Control-plane routing
- Data-plane routing
- Resource limits
- Plugin instances

Example:

```text
Realm: Work

Inbound:
  TUN

DNS:
  DoH Plugin

Rules:
  Work Rules

Transform:
  Packet Transformer

Protocol:
  WireGuard

Outbound:
  Default
```

---

## 6.2 Plugins Are Shared, Instances Are Realm-Specific

Installed plugin code is global.

Plugin instances and configuration are Realm-local.

```text
Plugin Registry
└── WireGuard Plugin
    ├── Realm A instance
    ├── Realm B instance
    └── Realm C instance
```

This avoids duplicated plugin binaries while preserving configuration isolation.

---

## 6.3 Realm-to-Realm Communication

Realms should communicate through standardized ports and graph edges.

Do not allow arbitrary direct plugin-to-plugin calls across Realms.

Use:

```text
Realm A Output Port
        ↓
       Edge
        ↓
Realm B Input Port
```

Possible named ports:

```text
inputs:
  vpn
  local_apps
  upstream

outputs:
  default
  secure
  direct
  blocked
```

---

# 7. Graph Engine

The application should represent network topology as a directed graph.

Example:

```text
TUN
 ↓
Realm Router
 ├── Browser → Realm Proxy
 ├── Gaming  → Realm WireGuard
 ├── Banking → Realm Direct
 └── Blocked → Drop
```

The graph should support:

- Branching
- Chaining
- Fallback
- Failover
- Load balancing
- Realm-to-Realm routing
- Conditional routing
- Health-aware routing
- Purpose-aware routing
- Multiple outputs
- Re-entry when explicitly allowed

The Core must detect cycles that would produce dependency or network loops.

---

# 8. Data Plane vs Control Plane

The architecture must explicitly separate:

## Data Plane

User traffic:

- Application packets
- Streams
- Datagram traffic
- Tunnel traffic

## Control Plane

Internal system traffic:

- Plugin downloads
- Repository index updates
- Endpoint resolution
- Authentication
- Health checks
- Subscription updates
- Peer discovery
- Plugin metadata lookup
- Package verification metadata

Each Realm must be able to specify independent routes for both planes.

Example:

```yaml
realm:
  data_route: realm_proxy
  control_route: direct
```

This avoids circular dependencies.

---

# 9. Packet / Traffic Transformation Pipeline

Avoid one generic "packet editor" stage.

Provide explicit stages:

```text
pre_route
post_route

pre_protocol
post_protocol

pre_transport
post_transport

pre_socket
```

Stream lifecycle stages:

```text
stream_open
handshake
stream_data
stream_close
```

Core abstractions should include:

- Packet
- Flow
- Stream
- Datagram
- Endpoint
- Route
- Resolver
- Transport
- Handshake metadata

Transformation plugins must declare the stage in which they operate.

---

# 10. Network Condition Engine

The application should continuously understand network quality and accessibility.

Suggested observations:

- DNS reachability
- TCP reachability
- UDP reachability
- TLS connection success
- Packet loss
- RTT
- Jitter
- Throughput
- Connection stall rate
- International route availability
- Local network availability
- Per-network history
- Per-Realm health

Possible states:

```text
Healthy
Degraded
Unstable
Restricted
Local-only
Offline
```

The system should expose this information to Realm policies and the download scheduler.

---

# 11. Realm Health and Failover

Every Realm should expose health metrics.

Example:

```text
Realm A: Healthy
Realm B: Degraded
Realm C: Offline
LAN Realm: Healthy
```

Health may be calculated from:

- DNS success
- Connection success
- RTT
- Loss
- Throughput
- Timeouts
- Transport availability
- Protocol success

Graph policies may automatically select alternatives.

Example:

```text
Primary Realm
   ↓ unhealthy
Backup Realm
   ↓ unavailable
Local Peer Realm
```

---

# 12. Plugin Packaging

## 12.1 Plugin Capsule

Define a portable package format, e.g.:

```text
*.vplug
```

Possible structure:

```text
xray.vplug
├── manifest.json
├── signatures/
├── modules/
│   ├── runtime.wasm
│   └── parser.wasm
├── native/
│   ├── windows-x64/
│   └── android-arm64/
├── resources/
├── ui/
│   └── schema.json
├── tests/
├── licenses/
└── hashes.json
```

The exact same package format should be used by:

- Online repositories
- Local files
- Device-to-device transfer
- USB
- Offline repositories
- Peer repositories

---

## 12.2 Plugin Ticket / Manifest

Support lightweight plugin references.

Possible format:

```text
*.vticket
```

Contains:

```yaml
plugin: org.example.xray
version: 5.0.0
hash: sha256:...
publisher: ...
sources:
  - official
  - mirror-a
```

This can be:

- Sent as a tiny file
- Encoded as QR
- Shared over Bluetooth
- Stored for later download

---

# 13. Content-Addressed Storage

All package objects should be referenced by cryptographic hash.

Example:

```text
Plugin X
├── hash-A → runtime.wasm
├── hash-B → ui.schema
├── hash-C → geo.dat
└── hash-D → tests.wasm
```

Benefits:

- Deduplication
- Partial transfers
- Multi-source download
- Chunk verification
- Offline repository replication
- Resume
- Peer exchange
- Integrity checking

---

# 14. Plugin Discovery

Plugin discovery must not depend on one official store.

Define Source Provider plugins.

Possible providers:

- Official repository
- HTTP repository
- Static JSON index
- Git repository
- GitHub-style release source
- Community repository
- LAN repository
- Peer repository
- Torrent index
- Local directory
- Custom provider

Source Provider responsibilities:

```text
search_plugin()
get_versions()
resolve_package()
get_metadata()
```

Source Providers should return package locators, not necessarily download the package.

---

# 15. Downloader / Content Acquisition Engine

The downloader should be generalized as a Content Acquisition Engine.

The Core requests:

> Acquire object with hash X.

Possible sources:

- HTTPS
- HTTP
- LAN
- Local file
- Wi-Fi Direct
- Bluetooth
- Peer
- Torrent/swarm
- Custom transfer plugin

The Core should not care where the object originated.

---

## 15.1 Adaptive Resumable Downloading

Required features:

- Chunked download
- Per-chunk hash
- Persistent resume state
- Adaptive concurrency
- Adaptive chunk size
- Source failover
- Multi-source download
- Retry strategy
- Dynamic timeout
- Bandwidth policy
- Battery-aware policy
- Metered-network policy
- Background-safe persistent state

Example:

```text
Chunk 001 ← Mirror A
Chunk 002 ← Peer B
Chunk 003 ← Realm C / HTTP
Chunk 004 ← LAN device
```

---

## 15.2 Adaptive Concurrency

Concurrency should be dynamic.

Example behavior:

```text
Stable broadband:
  8 workers

Weak mobile network:
  2–3 workers

High loss:
  1–2 workers

Improving network:
  increase workers
```

User override may support:

```text
Automatic
1
2
4
8
16
```

---

## 15.3 Multi-Source Scheduling

The scheduler chooses:

```text
Which chunk?
Which source?
Which transport?
Which Realm?
```

This gives a four-dimensional scheduling problem:

```text
Chunk × Source × Transport × Realm
```

The scheduler should optimize for:

- Reliability
- Throughput
- Loss
- Cost
- User preference
- Battery
- Data limits
- Local availability

---

# 16. Realm-Aware Downloads

All ordinary network access from internal subsystems should be Realm-aware.

Example:

```text
Plugin Resolver
      ↓
Download Planner
      ↓
HTTP / Torrent
      ↓
Selected Realm
      ↓
DNS / Rules / Protocol / Transform / Outbound
      ↓
Network
```

Different sources may use different Realms:

```text
Official Repo → Realm A
Mirror B      → Realm B
Torrent       → Realm C
LAN           → Local Realm
```

Metadata resolution and package downloading may use separate Realms.

---

# 17. Bootstrap Networking

Avoid dependency deadlocks.

Example deadlock:

```text
Need Xray plugin
      ↓
Download through Realm A
      ↓
Realm A requires Xray
```

Therefore Core must include a minimal Bootstrap Network path.

Bootstrap capabilities:

- Import local file
- Export local file
- Scan/import manifest
- QR manifest
- Direct basic network access
- Minimal DNS
- Basic HTTPS download where possible
- LAN package discovery where feasible
- Signature verification
- Package install

Bootstrap networking must remain intentionally small.

---

# 18. Transfer Engine

Device-to-device transfer should be a first-class subsystem.

Separate:

## Discovery

Possible plugins:

- LAN discovery
- mDNS-style discovery
- Bluetooth discovery
- QR pairing
- Manual address
- Wi-Fi Direct discovery

## Transport

Possible plugins:

- TCP
- QUIC
- Bluetooth
- Wi-Fi Direct
- LAN
- Torrent/swarming
- Relay
- Custom P2P transport

## Strategy

Optional strategy plugins may decide:

- Source priority
- Chunk distribution
- Multi-peer scheduling
- Transport fallback
- Bandwidth limits

---

# 19. Offline Sharing Modes

Support at least three modes.

## Full

Send plugin and dependencies.

## Manifest Only

Send plugin identity/version/hash/source metadata.

## Smart

Peers negotiate missing objects and only transfer necessary content.

Example:

```text
Sender:
  Do you have hash A?

Receiver:
  Yes.

Sender:
  Skip hash A.

Receiver:
  Need hash B and C.
```

---

# 20. Offline Repository

Any device should be able to operate as a portable repository.

Example:

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

A laptop or phone can share this repository over:

- USB
- LAN
- Local file
- Wi-Fi
- Peer transfer

---

# 21. Repository Replication

Repositories should support partial replication using content hashes.

Example:

```text
Device A has:
A, B, C

Device B has:
B, D

Sync:
A → B
C → B
D → A
```

Replication should work without global Internet access.

---

# 22. Delay-Tolerant Distribution

Support store-and-forward operation.

Example:

```text
Internet
   ↓
Alice
   ↓ hours later
Bob
   ↓ days later
Carol
```

Metadata and packages should remain useful even when sender, receiver, and Internet connectivity are never simultaneously available.

---

# 23. Realm Bundles

Realm configurations must be portable.

Possible formats:

```text
*.realm
*.realm-bundle
```

Configuration-only Realm:

```text
realm.json
plugin-lock.json
```

Portable Realm bundle:

```text
restricted.realm-bundle
├── realm.json
├── plugin-lock.json
├── signatures/
└── packages/
```

Import process should indicate:

```text
Plugin A: already installed
Plugin B: install from bundle
Plugin C: unavailable
```

---

# 24. Plugin Lockfile

Realms should support reproducible plugin environments.

Example:

```yaml
plugins:
  org.example.xray:
    version: 5.1.0
    hash: sha256:...

  org.example.dns:
    version: 2.4.0
    hash: sha256:...
```

Do not silently substitute unrelated newer versions unless policy explicitly allows it.

---

# 25. Survival / Offline Kit

Provide a user-facing feature:

```text
Create Offline Kit
```

Bundle may contain:

- Realm configuration
- Required plugins
- Required dependencies
- DNS components
- Rules
- Repository metadata
- Trusted publisher keys
- Recovery metadata
- Last-known-good versions

This acts as disaster recovery for the networking environment.

---

# 26. Trust Architecture

Transport and repository location must never imply trust.

Principle:

> Transport != Trust

A package from:

- USB
- Bluetooth
- Torrent
- LAN peer
- Random mirror

may still be trusted if its publisher signature and content hash validate.

Trust should be based on:

- Publisher identity
- Cryptographic signature
- Package hash
- Manifest integrity
- Optional transparency metadata

---

## 26.1 Trusted Publishers

Maintain a trust store.

Example:

```text
Trusted Publishers
├── Publisher A
├── Publisher B
└── Organization C
```

Repositories only distribute packages.

Publishers establish authenticity.

---

# 27. Privacy of Peer Sharing

Do not broadcast full plugin inventories by default.

Prefer need-based discovery.

Good:

```text
Do you have hash abc123?
```

Avoid:

```text
Here is every privacy/networking plugin I use.
```

Peer metadata should reveal as little as possible.

---

# 28. Partial Functionality

The system should degrade gracefully.

Examples:

```text
Geo database missing
→ Basic routing still works

UI plugin missing
→ Generic schema UI

Subscription plugin missing
→ Manual config remains available

Optional resources missing
→ Plugin runs in reduced mode
```

A missing optional component should not break unrelated functionality.

---

# 29. Last-Known-Good and Rollback

Maintain previous working versions.

Example:

```text
Xray
├── 5.2 Current
└── 5.1 Last Known Good
```

Support:

- Plugin rollback
- Realm rollback
- Repository metadata rollback
- Config rollback
- Failed activation recovery

Rollback must work offline.

---

# 30. Test Architecture

Testing should be partly plugin-based.

Core contains the trusted Test Engine.

Test plugins provide test logic.

Possible test categories:

- DNS
- TCP
- UDP
- TLS
- Routing
- Realm health
- Protocol
- Transport
- Packet pipeline
- Repository
- Download
- Peer transfer
- Performance
- Reconnect
- Failover
- Compatibility

---

## 30.1 Core Test Engine

Core responsibilities:

- Test runner
- Timeouts
- Isolation
- Permissions
- Result schema
- Resource limits
- Report generation
- Test lifecycle

---

## 30.2 Plugin Self-Tests

Plugins may ship tests.

Example:

```text
Xray Plugin
├── Runtime
├── UI
├── Config parser
└── Tests
    ├── config validation
    ├── connection test
    ├── reconnect test
    └── DNS-through-tunnel
```

Install flow:

```text
Download
 ↓
Signature verification
 ↓
Static validation
 ↓
Sandbox load
 ↓
Self-test
 ↓
Compatibility test
 ↓
Canary activation
 ↓
Full activation
```

---

# 31. Test Profiles

Support reusable test profiles.

Examples:

```text
Quick Test
Full Network Test
Plugin Certification
Realm Diagnostics
Weak Network Test
```

Profiles should be importable and shareable.

---

# 32. Synthetic Network Testing

Provide a network simulator for development and diagnostics.

Simulated conditions:

- Packet loss
- High latency
- Jitter
- Bandwidth limit
- Random disconnect
- DNS failure
- UDP blocked
- TCP stalls
- Intermittent connectivity

Example:

```text
Realm
 ↓
Network Simulator
 ↓
Synthetic bad network
```

---

# 33. Chaos Testing

Support fault injection.

Possible faults:

- Kill connection
- Delay DNS
- Fail a source
- Remove a peer
- Corrupt a chunk
- Disable a Realm
- Restart a plugin
- Trigger timeout
- Simulate partial repository

Verify:

- Recovery
- Resume
- Rollback
- Failover
- Data integrity
- State consistency

---

# 34. Non-Plugin Core Security Tests

The following must not be replaceable by ordinary plugins:

- Signature validation
- Package hash verification
- Capability enforcement
- Sandbox boundary tests
- Plugin ABI validation
- Core resource isolation
- Core update integrity
- Rollback integrity
- Trust store validation

Plugins must never be able to redefine what "trusted" means.

---

# 35. Cross-Platform Strategy

Target platforms:

- Windows
- Android
- Linux
- macOS

Possible future targets:

- iOS
- BSD
- Embedded Linux
- Router platforms

---

## 35.1 Portable Core

Suggested implementation language:

- Rust

Primary shared components:

- Realm engine
- Graph engine
- Plugin runtime
- Content acquisition engine
- Package manager
- Trust engine
- Test engine
- Configuration
- Scheduler
- Network condition engine

---

## 35.2 Platform Adapter Layer

### Windows

Potential platform integrations:

- Virtual network adapter
- WFP
- Native routing APIs
- OS service integration

### Android

Potential integrations:

- VpnService
- Android networking APIs
- Foreground service
- Keystore

### Linux

Potential integrations:

- TUN/TAP
- Netlink
- Routing tables
- Namespaces where appropriate

### macOS

Potential integrations:

- Network Extension
- Packet Tunnel Provider
- Keychain
- System network APIs

---

# 36. Platform Capability Negotiation

Do not assume every capability exists on every OS.

Plugins should declare requirements:

```yaml
requires:
  - packet.raw
  - socket.udp

optional:
  - packet.raw_l2
```

Core evaluates:

```text
Supported
Unsupported
Available with limitation
Permission required
```

A Realm should report why it cannot run on a platform.

---

# 37. UI Architecture

The application shell may be cross-platform.

Potential options:

- Flutter
- Native UI per platform with shared logic
- Other cross-platform toolkit after prototype validation

Plugin UI should preferably be schema-driven instead of arbitrary native UI code.

Example:

```json
{
  "title": "WireGuard",
  "fields": [
    {
      "name": "endpoint",
      "type": "host_port"
    },
    {
      "name": "private_key",
      "type": "secret"
    },
    {
      "name": "mtu",
      "type": "number"
    }
  ]
}
```

Each platform renders the same schema using native/cross-platform components.

---

# 38. Observability

Provide actionable diagnostics.

Example:

```text
DNS          OK
Realm A      OK
Route        OK
Protocol     OK
Transport    FAILED
Source       REACHABLE
```

Avoid generic:

```text
Connection failed
```

---

## 38.1 Privacy-Safe Logging

Default logs must never contain:

- Credentials
- Private keys
- Packet payloads
- Sensitive tokens
- Full destination history unless explicitly enabled

Destination names should be redacted or minimized by default.

---

# 39. Purpose-Aware Internal Traffic

Internal connections should carry metadata.

Example:

```yaml
purpose: plugin_download
plugin: org.example.xray
source: official
priority: critical
```

Possible purposes:

- plugin_download
- plugin_update
- repository_metadata
- subscription_update
- health_check
- realm_sync
- peer_discovery
- telemetry
- authentication

Realm rules may route based on purpose.

---

# 40. Dependency Graph

Before activating or downloading components, resolve dependency requirements.

Example:

```text
Realm A requires:
├── Plugin X
├── Plugin Y
└── Plugin Z
```

The dependency engine must detect:

- Missing dependencies
- Version conflicts
- Circular dependencies
- Realm bootstrap loops
- Platform incompatibility
- Capability incompatibility

---

# 41. Bootstrappability Score

Optional but useful:

Every Realm may expose whether it can start in current conditions.

Example:

```text
Realm "Restricted"
Status: Cannot bootstrap

Reason:
Xray plugin missing
Selected download Realm requires Xray
```

Alternative:

```text
Realm "Emergency"
Status: Fully bootstrappable offline
```

---

# 42. Update Philosophy

Updates are divided into:

## Plugin Update

Normal feature evolution.

## Runtime Component Update

Occasional internal runtime improvements.

## Core Update

Rare, security-critical, OS-compatibility, or architectural fixes.

All updates should support:

- Cryptographic verification
- Atomic install
- Rollback
- Version pinning
- Last-known-good
- Offline installation

---

# 43. MVP Scope

The first version should not attempt every feature.

## MVP Goal

Prove the architecture.

The MVP should demonstrate:

1. Cross-platform Core on at least Linux + Android or Linux + Windows
2. Plugin runtime
3. Capability-based plugin permissions
4. Basic Realm
5. Basic graph routing
6. One inbound
7. Two simple outbound types
8. One DNS plugin
9. One protocol plugin
10. One rules plugin
11. Plugin package format
12. Offline plugin import/export
13. Signed plugin verification
14. Resumable chunked download
15. Download through a Realm
16. Basic test plugin
17. Last-known-good rollback
18. Realm export/import

Do not begin with every protocol and every platform.

---

# 44. Suggested Development Phases

## Phase 0 — Architecture Specification

Deliverables:

- Terminology
- Threat model
- Plugin ABI draft
- Realm model
- Graph model
- Package format
- Trust model
- Capability model
- Platform abstraction
- Dependency model

Exit criteria:

- No feature implementation before interfaces are reviewed.

---

## Phase 1 — Core Skeleton

Implement:

- Rust workspace
- Event bus
- Config system
- Plugin manifest parser
- Capability registry
- Basic WASM runtime
- Package hashing
- Signature verification
- Local package install

Exit criteria:

```text
Install signed test plugin
Load sandbox
Request capability
Execute function
Unload safely
```

---

## Phase 2 — Realm + Graph

Implement:

- Realm lifecycle
- Realm-local plugin instances
- Input/output ports
- Graph edges
- Cycle detection
- Basic routing
- Realm health state

Exit criteria:

```text
Inbound → Realm A → Realm B → Outbound
```

works with a fully described graph.

---

## Phase 3 — Platform Networking

Start with one desktop platform.

Recommended:

- Linux first

Implement:

- TUN
- Route adapter
- DNS adapter
- Socket abstraction
- Platform capability discovery

Then port to:

- Windows
- Android
- macOS

---

## Phase 4 — Plugin Package Ecosystem

Implement:

- `.vplug`
- `.vticket`
- Dependency metadata
- Publisher trust
- Plugin lockfile
- Local repository
- Offline import/export
- Realm bundle

---

## Phase 5 — Content Acquisition

Implement:

- Chunking
- Resume
- Persistent state
- Adaptive workers
- Multi-source interfaces
- Source provider API
- Download transport API
- Realm-aware sockets

Exit criteria:

A package download must survive:

- app restart
- connection loss
- source failure
- Realm failover

---

## Phase 6 — Peer / Offline Distribution

Implement:

- Local package sharing
- LAN discovery
- Smart missing-chunk negotiation
- Offline repository
- Repository replication
- Plugin ticket sharing
- QR manifest

---

## Phase 7 — Testing Framework

Implement:

- Test Engine
- Test plugin API
- Realm health tests
- Network simulator
- Plugin self-test lifecycle
- Canary activation
- Rollback integration

---

## Phase 8 — UI

Implement:

- Application shell
- Realm editor
- Graph editor
- Plugin manager
- Download manager
- Repository manager
- Test dashboard
- Diagnostics
- Offline kit creator
- Plugin UI schema renderer

---

## Phase 9 — Hardening

Focus:

- Security audit
- Fuzzing
- Sandbox escape testing
- Dependency attacks
- Package tampering
- Signature downgrade
- Plugin permission abuse
- Resource exhaustion
- Crash recovery
- Corrupted state recovery
- Replay protection
- Peer metadata privacy

---

# 45. Proposed Repository Layout

```text
/
├── PLAN.md
├── ARCHITECTURE.md
├── SECURITY.md
├── PROTOCOL.md
├── Cargo.toml
│
├── core/
│   ├── runtime/
│   ├── plugin/
│   ├── realm/
│   ├── graph/
│   ├── flow/
│   ├── trust/
│   ├── package/
│   ├── acquisition/
│   ├── testing/
│   ├── config/
│   └── platform/
│
├── platform/
│   ├── linux/
│   ├── windows/
│   ├── android/
│   └── macos/
│
├── sdk/
│   ├── rust/
│   ├── wasm/
│   └── schema/
│
├── plugins/
│   ├── examples/
│   └── builtin-bootstrap/
│
├── schemas/
│   ├── plugin-manifest/
│   ├── realm/
│   ├── graph/
│   ├── ui/
│   └── test/
│
├── tests/
│   ├── integration/
│   ├── chaos/
│   ├── security/
│   └── network-simulation/
│
└── tools/
    ├── packager/
    ├── signer/
    ├── repo-builder/
    └── realm-validator/
```

---

# 46. Documents to Create Next

Recommended technical documents:

1. `ARCHITECTURE.md`
2. `PLUGIN-ABI.md`
3. `REALM-SPEC.md`
4. `GRAPH-SPEC.md`
5. `PACKAGE-SPEC.md`
6. `TRUST-MODEL.md`
7. `TRANSFER-SPEC.md`
8. `ACQUISITION-SPEC.md`
9. `TESTING-SPEC.md`
10. `SECURITY.md`
11. `THREAT-MODEL.md`
12. `PLATFORM-API.md`

---

# 47. Important Architectural Rules

The following should be treated as project invariants.

### Rule 1

Core must not know individual protocols unless required for bootstrap.

### Rule 2

Plugins must interact through stable abstractions, not direct cross-plugin coupling.

### Rule 3

Realm-to-Realm communication must go through graph ports/edges.

### Rule 4

Transport does not imply trust.

### Rule 5

Repositories distribute packages; publishers establish authenticity.

### Rule 6

Internal network operations should use Realm-aware networking whenever possible.

### Rule 7

Bootstrap networking must not depend on optional plugins.

### Rule 8

Offline package installation must always remain possible.

### Rule 9

Missing optional components must degrade functionality instead of crashing the system.

### Rule 10

Every important update must support rollback.

### Rule 11

Plugin inventory must not be unnecessarily exposed to peers.

### Rule 12

Tests may be extensible, but Core security invariants must not be redefinable by plugins.

### Rule 13

Platform-specific functionality must live behind capability APIs.

### Rule 14

Portable Realm configurations should be OS-independent wherever possible.

### Rule 15

No single store, repository, download protocol, or server may become a mandatory distribution dependency.

---

# 48. Definition of Success

The architecture should eventually satisfy the following scenario:

1. A user installs the application.
2. The application contains only the minimal Core and bootstrap capabilities.
3. The user receives a signed plugin package from another device with no Internet access.
4. The application verifies and installs it.
5. The user imports a Realm bundle.
6. Missing dependencies are discovered.
7. Some dependencies are obtained from LAN peers.
8. Others are queued as plugin tickets.
9. When intermittent Internet becomes available, missing chunks are downloaded from multiple sources through a selected Realm.
10. The connection drops repeatedly.
11. Download resumes without losing completed data.
12. One source disappears.
13. Scheduler moves missing chunks to another source.
14. One Realm becomes unhealthy.
15. Traffic fails over to another Realm.
16. The new plugin runs self-tests.
17. A failed version is automatically prevented from replacing the last-known-good version.
18. The complete working Realm is exported to another offline device.
19. The receiving device can reproduce the environment using the same locked plugin versions.
20. None of these operations require a mandatory central store.

If this scenario works reliably, the project has achieved its core architectural purpose.

---

# 49. Long-Term Direction

The system may eventually become more than a VPN platform.

Potential long-term identity:

> A resilient, programmable, user-controlled networking runtime for hostile, unreliable, fragmented, or intermittently connected networks.

Possible future capabilities:

- Distributed configuration exchange
- LAN-first application services
- Delay-tolerant content distribution
- Portable networking environments
- Enterprise network policy packages
- Router deployment
- Headless server mode
- Embedded gateway mode
- Remote Realm orchestration
- Peer-assisted package ecosystems

These should remain future extensions.

The initial project must stay focused on validating the Core + Plugin + Realm + Graph + Acquisition + Trust + Testing architecture.
