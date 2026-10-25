# GRAPH-SPEC.md

# Graph Engine Specification

## 1. Purpose

This document defines the topology model used to connect platform endpoints, Realm ports, routing decisions, transfer paths, and network services.

The Graph Engine is the composition layer of the platform.

It answers:

> What is connected to what, under which conditions, and what happens when a path fails?

---

# 2. Graph Model

The Graph is a directed typed graph.

It contains:

- Nodes
- Ports
- Edges
- Conditions
- Policies
- Health references
- Failover groups
- Metadata

---

# 3. Node Types

Initial node types:

```text
realm
platform_inbound
platform_outbound
service
sink
source
virtual
```

Most user-facing topology should use Realm nodes.

---

# 4. Realm Node

Realm node references an existing Realm.

Example:

```yaml
node:
  id: realm-work
  type: realm
  realm_id: work
```

Its Graph ports map to Realm ports.

---

# 5. Platform Nodes

Platform nodes represent OS integration.

Examples:

```text
TUN
Local Proxy
System Network
LAN
```

Example:

```yaml
node:
  id: system-tun
  type: platform_inbound
  adapter: tun
```

---

# 6. Source Nodes

Source nodes originate traffic or control objects.

Examples:

- Platform TUN
- Local application
- Plugin downloader
- Repository resolver
- Test runner

---

# 7. Sink Nodes

Sink nodes terminate flows.

Examples:

```text
Internet
Drop
Reject
Local Service
Package Store
```

---

# 8. Ports

Every edge connects ports.

Port fields:

```yaml
name: secure
type: flow
direction: output
```

Graph Engine MUST validate type and direction compatibility.

---

# 9. Edge Definition

Example:

```yaml
edge:
  id: edge-a-b
  from:
    node: realm-a
    port: secure
  to:
    node: realm-b
    port: upstream
```

---

# 10. Edge Types

Initial edge behaviors:

```text
direct
conditional
failover
weighted
mirror
control
```

The edge type defines selection behavior, not payload type.

---

# 11. Direct Edge

Always forwards when source emits.

Example:

```text
Realm A.secure → Realm B.upstream
```

---

# 12. Conditional Edge

Edge activates only if expression evaluates true.

Example:

```yaml
when:
  purpose: plugin_download
```

Possible condition inputs:

- Flow metadata
- Realm health
- Network health
- Time
- Tags
- Platform state
- User policy

---

# 13. Failover Edge Group

Ordered fallback:

```text
Primary
  ↓ fail
Backup
  ↓ fail
Emergency
```

Example:

```yaml
group:
  type: failover
  members:
    - edge-primary
    - edge-backup
    - edge-local
```

---

# 14. Weighted Edge Group

Traffic may be distributed by weight.

Example:

```yaml
weights:
  realm-a: 70
  realm-b: 30
```

Weights MAY adapt based on health.

---

# 15. Mirror Edge

Duplicates supported traffic/events to multiple destinations.

Mirror MUST NOT be allowed for secret-bearing or user traffic by default.

Requires explicit capability/policy.

Useful for:

- Testing
- Metrics
- Synthetic comparison

---

# 16. Graph Identity

Every Graph MUST have:

```yaml
graph_id: ...
name: ...
schema_version: 1
revision: ...
```

---

# 17. Graph Lifecycle

Suggested states:

```text
Created
Validated
Ready
Active
Degraded
Stopped
Failed
```

---

# 18. Graph Validation

Static validation MUST verify:

- Node IDs unique
- Edge IDs unique
- Node references valid
- Ports exist
- Direction valid
- Port types compatible
- No forbidden cycles
- Required sinks reachable
- Failover groups valid
- Conditions parse successfully
- Realm dependencies valid
- Platform capabilities available

---

# 19. Reachability Validation

Core SHOULD verify that required paths have a terminal destination.

Example error:

```text
realm-a.secure has no reachable sink
```

---

# 20. Cycle Detection

Default rule:

```text
Cycles are forbidden.
```

Explicit bounded cycles MAY be supported later.

Example forbidden:

```text
A → B → C → A
```

---

# 21. Bounded Re-Entry

If re-entry is implemented, it MUST declare:

```yaml
max_hops: 2
```

or:

```yaml
loop_token_limit: 1
```

Unbounded loops are never allowed.

---

# 22. Control-Plane Cycle Detection

Control-plane cycles MUST be checked separately from data-plane cycles.

Example:

```text
Realm A needs DNS through B
Realm B downloads plugin through A
```

may create hidden recursion even when traffic edges appear valid.

---

# 23. Graph Compilation

Runtime SHOULD compile Graph config into an efficient execution plan.

Conceptual:

```text
Graph Config
   ↓
Validate
   ↓
Resolve Realms
   ↓
Resolve Ports
   ↓
Compile Conditions
   ↓
Build Routing Tables
   ↓
Activate
```

---

# 24. Runtime Routing

For each Flow:

```text
Input Node
   ↓
Evaluate applicable edges
   ↓
Apply conditions
   ↓
Apply policy group
   ↓
Select destination
   ↓
Forward
```

---

# 25. Flow Affinity

Existing Flow SHOULD remain on its selected path unless:

- Path fails
- Policy allows migration
- Protocol supports migration

New flows may use new health decisions immediately.

---

# 26. Session Affinity

Optional affinity key:

```text
application
destination
user
realm
subscription
```

Affinity behavior must be explicit.

---

# 27. Health-Aware Routing

Edges and nodes expose health.

Example:

```text
Realm A: Healthy
Realm B: Degraded
Realm C: Offline
```

Failover chooses healthiest eligible route according to policy.

---

# 28. Health Threshold Conditions

Example:

```yaml
when:
  realm_health:
    realm: realm-a
    minimum_score: 60
```

---

# 29. Network-State Conditions

Example:

```yaml
when:
  network_state:
    equals: local_only
```

Can route to:

```text
LAN Realm
```

---

# 30. Purpose-Aware Graph Routing

Example:

```text
plugin_download → Download Realm
user_traffic    → User Realm
health_check    → Direct Realm
```

Purpose is Core-defined metadata.

---

## 30.1 Dedicated DNS Egress

A Realm publishes a logical `dns` output for name-resolution requests. DNS
routing is separate from the user-traffic output so a Realm can choose its DNS
path independently without pretending that resolver components are serial
packet-path nodes.

The two baseline destinations are:

```text
DNS egress -> Internet
DNS egress -> input of another Realm
```

`DNS -> Internet` terminates in the source Realm's local resolver chain.
`DNS -> Realm` delegates the lookup to the target Realm's resolver context; the
target Realm may in turn apply its own DNS Graph route. Implementations MUST
bound recursive Realm DNS forwarding and reject cycles. They SHOULD fall back
to the local resolver chain when a selected target Realm is unavailable.

A DNS Graph edge uses the `dns` source port and `purpose: dns`. The UI SHOULD
render this as a distinct DNS connection rather than conflating it with the
Realm's normal Outbound edge.

# 31. Data Plane Graph

User traffic Graph may be distinct from control traffic Graph.

Example:

```text
TUN
 ↓
User Router
 ├── Secure Realm
 └── Direct Realm
```

---

# 32. Control Plane Graph

Example:

```text
Plugin Downloader
 ↓
Control Router
 ├── Download Realm
 ├── LAN Realm
 └── Bootstrap
```

---

# 33. Nested Graphs

A Realm may internally use a subgraph.

Top-level Graph treats Realm as a node.

This preserves abstraction.

---

# 34. Graph Encapsulation

External Graph SHOULD see only published Realm ports.

Internal Realm topology remains private.

This prevents coupling.

---

# 35. Graph Policies

Policy objects SHOULD be reusable.

Examples:

```text
ordered_failover
lowest_latency
highest_health
lowest_cost
manual_only
weighted_balance
```

---

# 36. Ordered Failover

Example:

```yaml
policy:
  type: ordered_failover
  targets:
    - realm-a
    - realm-b
    - realm-c
```

---

# 37. Lowest-Latency Policy

Select among healthy paths.

Requires health metrics.

Tie-breaking must be deterministic.

---

# 38. Lowest-Cost Policy

Cost metadata may include:

```text
metered
roaming
battery
bandwidth_budget
user_priority
```

---

# 39. Manual Policy

User selects active path.

Automatic failover may be disabled.

---

# 40. Policy Stability

Policies SHOULD avoid rapid oscillation.

Use:

- Hysteresis
- Minimum dwell time
- Health smoothing
- Cooldown

---

# 41. Edge State

Possible edge states:

```text
Enabled
Disabled
Healthy
Degraded
Unavailable
BlockedByPolicy
Invalid
```

---

# 42. Node State

Possible node states:

```text
Ready
Running
Degraded
Unavailable
Failed
```

---

# 43. Dynamic Graph Updates

Graph MAY support runtime updates.

Safe update flow:

```text
Begin Transaction
   ↓
Apply Changes
   ↓
Validate
   ↓
Compile
   ↓
Commit
```

If invalid:

```text
Rollback
```

---

# 44. Atomic Graph Revision

Graph activation should be atomic.

Flows SHOULD NOT observe half-applied topology.

---

# 45. Graph Revision

Every committed change increments revision.

Example:

```yaml
revision: 23
```

---

# 46. Last-Known-Good Graph

Core SHOULD store:

```text
Current
Previous
Last Known Good
```

Invalid activation can roll back.

---

# 47. Graph Events

Suggested events:

```text
graph.validated
graph.activated
graph.changed
graph.degraded
graph.failed
edge.health_changed
node.health_changed
route.changed
failover.triggered
```

---

# 48. Diagnostics

Graph diagnostics should answer:

```text
Why did Flow X use Realm B?
```

Example:

```text
Primary edge rejected:
  Realm A health = 32 < minimum 50

Selected:
  Realm B
```

---

# 49. Route Trace

Optional trace:

```text
TUN.local
  ↓
Realm Router.secure
  ↓
Realm Proxy.upstream
  ↓
Internet
```

Trace MUST redact sensitive destinations by default.

---

# 50. Graph Testing

Graph Test API SHOULD support:

- Validate only
- Dry-run routing
- Synthetic flow
- Synthetic health state
- Failover simulation
- Cycle detection test
- Port compatibility test

---

# 51. Synthetic Flow

Example:

```yaml
flow:
  purpose: plugin_download
  destination: example.org
  protocol: tcp
```

Core returns predicted route without opening network connection.

---

# 52. Synthetic Health

Example:

```yaml
assume:
  realm-a: offline
  realm-b: healthy
```

Used to verify failover.

---

# 53. Graph Permissions

Graph config may reference only:

- Existing Realms
- Allowed platform endpoints
- Approved services

Plugins cannot mutate global Graph directly unless granted explicit capability.

---

# 54. Plugin-Requested Routing

Plugin may request:

```text
Route this Flow through output X
```

Core validates request against Realm and Graph policy.

Plugin does not directly rewrite Graph.

---

# 55. Graph Storage

Graph configuration is persisted separately from runtime state.

Suggested:

```text
graphs/<graph_id>/config
graphs/<graph_id>/revisions
graphs/<graph_id>/lkg
```

---

# 56. Graph Export

Graph export modes:

## Config only

References external Realms.

## Portable topology bundle

May include:

- Graph config
- Referenced Realm configs
- Realm lockfiles
- Required plugin tickets

---

# 57. Graph Import

Import MUST validate:

- Realm references
- Port references
- Schema
- Cycles
- Policies
- Capability compatibility
- Platform endpoints

---

# 58. Graph Template

Graph templates may describe reusable topologies.

Example:

```text
Primary VPN + Backup VPN + Direct fallback
```

Template instantiation binds Realm IDs.

---

# 59. Example Graph

```yaml
schema_version: 1

graph:
  id: main
  name: Main Network

  nodes:
    - id: tun
      type: platform_inbound
      adapter: tun

    - id: secure
      type: realm
      realm_id: secure-realm

    - id: backup
      type: realm
      realm_id: backup-realm

    - id: internet
      type: sink
      sink: internet

  edges:
    - id: tun-secure
      from:
        node: tun
        port: output
      to:
        node: secure
        port: local

    - id: secure-internet
      from:
        node: secure
        port: default
      to:
        node: internet
        port: input

    - id: secure-backup
      from:
        node: secure
        port: fallback
      to:
        node: backup
        port: upstream
```

---

# 60. Failover Example

```text
                ┌→ Realm A ─→ Internet
TUN → Router ───┤
                ├→ Realm B ─→ Internet
                └→ Direct  ─→ Internet
```

Policy:

```yaml
type: ordered_failover
order:
  - realm-a
  - realm-b
  - direct
```

---

# 61. Download Graph Example

```text
Plugin Downloader
      ↓
Control Router
      ├── LAN Realm
      ├── Proxy Realm
      └── Bootstrap
```

Each acquisition worker may use a different branch.

---

# 62. Multi-Source Acquisition Graph

```text
Chunk A → Mirror 1 → Realm X
Chunk B → Peer 1   → LAN Realm
Chunk C → Mirror 2 → Realm Y
```

Graph is route substrate.

Acquisition Scheduler chooses assignment.

---

# 63. Graph/Realm Boundary

Graph decides:

```text
Which Realm / endpoint receives traffic?
```

Realm decides:

```text
How does that Realm process traffic internally?
```

This distinction MUST remain strict.

---

# 64. Graph/Plugin Boundary

Graph MUST NOT directly reference arbitrary plugin internals.

It references:

- Realm ports
- Core services
- Platform endpoints

Plugin composition happens inside Realm.

---

# 65. Graph Failure Semantics

When destination fails:

Possible behaviors:

```text
retry
failover
drop
reject
buffer
surface error
```

Policy must define behavior.

---

# 66. Buffering

Buffering is dangerous under unstable networks.

Graph MAY buffer only with explicit limits:

```yaml
max_bytes: ...
max_duration: ...
```

Unbounded buffering is prohibited.

---

# 67. Backpressure

Edges MUST propagate backpressure where traffic type supports it.

Do not allow unbounded queue growth.

---

# 68. Queue Policy

Possible queue policy:

```text
block
drop_newest
drop_oldest
reject
```

Default depends on traffic type.

---

# 69. Priority

Flow priority may affect scheduling.

Example:

```text
critical
high
normal
low
background
```

Plugin updates may be lower priority than interactive user traffic.

---

# 70. Bandwidth Policy

Edge/group may define limits.

Example:

```yaml
bandwidth:
  max_kbps: 500
```

Acquisition Graph may obey user data budget.

---

# 71. Graph Security Rules

Graph MUST NOT:

- Bypass plugin capability enforcement
- Override package trust
- Expose private plugin storage
- Connect incompatible port types
- Create hidden unrestricted cycles
- Broadcast sensitive metadata without explicit policy

---

# 72. Graph API

Conceptual Core API:

```text
graph_create()
graph_load()
graph_validate()
graph_compile()
graph_activate()
graph_stop()
graph_update()
graph_trace()
graph_export()
graph_rollback()
```

---

# 73. Route Query API

Examples:

```text
graph_predict_route(flow)
graph_get_active_path(flow_id)
graph_get_failover_status()
graph_get_node_health()
graph_get_edge_health()
```

---

# 74. Graph Mutation API

Transactional:

```text
graph_begin_transaction()
graph_add_node()
graph_add_edge()
graph_remove_edge()
graph_update_policy()
graph_validate_transaction()
graph_commit()
graph_abort()
```

---

# 75. Graph Observability

Metrics may include:

```text
active_flows
flows_per_edge
failovers_total
route_changes_total
edge_errors_total
queue_depth
dropped_flows
```

Sensitive labels must be minimized.

---

# 76. Platform Awareness

Graph may reference platform capabilities, but should remain portable.

Example:

```yaml
requires:
  - platform.tun
```

If unavailable:

```text
CompatibleWithLimitations
```

or:

```text
Unsupported
```

---

# 77. Graph Reproducibility

Portable Graph bundle SHOULD lock Realm revisions.

Example:

```yaml
realms:
  secure:
    revision: 14
  backup:
    revision: 5
```

---

# 78. Graph Upgrade

Schema migrations must be deterministic.

Old Graph configs should migrate through versioned converters.

---

# 79. Graph Success Criteria

Graph design is successful when:

1. Realms can be chained without direct plugin coupling.
2. Invalid cycles are detected.
3. Health-aware failover works.
4. Control and data paths can differ.
5. Graph changes are atomic.
6. Route decisions are explainable.
7. Graph configs are portable.
8. Multi-source acquisition can use multiple Realm paths.
9. Flow affinity avoids unnecessary route churn.
10. Last-known-good Graph rollback works offline.
