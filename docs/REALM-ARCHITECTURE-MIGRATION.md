# Realm architecture migration

This source tree uses the revised pre-release Realm model.

## Public model changes

- `rule_engine` is now `routing`; the plugin type is also `routing`.
- Routing selects an outbound and nothing later in the plane may replace that
  selection with an unrelated protocol/transport path.
- An outbound may bind `protocol` and `transport` instance IDs. This binding is
  the outbound's egress stack.
- `data_plane` and `control_plane` contain only `input`, `output`, and
  `outbound` selectors.
- Empty Realm port lists are normalized to `in: flow` and `out: flow`, and
  these become the default ports.
- `Flow` carries `input_port` and `output_port`. Graph forwarding consumes the
  selected output port and records the target Realm input port.
- The neutral protocol plugin is `io.meshvianex.protocol.passthrough`.
- The baseline Xray-style stream transport plugin is
  `io.meshvianex.transport.raw`.

## Runtime behavior changes

The effective path is now deterministic and the Realm Graph is an actual runtime
boundary rather than a UI-only relationship:

```text
Ingress -> Input port -> transforms -> Routing -> Output port -> Realm Graph
                                                      |
                                                      +-> next Realm (handoff)
                                                      |
                                                      +-> terminal egress
                                                            -> selected Outbound
                                                            -> optional Protocol
                                                            -> optional Stream Transport
                                                            -> Socket
```

A Realm's local outbound stack is opened only when graph forwarding terminates
locally. If the selected output port hands the flow to another Realm, the local
outbound/protocol/transport stack is not executed. Monolithic outbounds such as
Direct, HTTP, and SOCKS continue to dial directly when no protocol/transport
binding is present.

Realm DNS components are tried in declaration order and the system resolver is
used only after configured resolvers fail or return no addresses. DNS resolution
also has a dedicated logical `dns` egress in the active Graph. `DNS -> Internet`
terminates in the Realm's local resolver chain; `DNS -> Realm` delegates the
lookup to the target Realm's resolver context. If a selected DNS Realm is
unavailable, the source Realm falls back to its local resolver chain.

Stream transformation wrappers are skipped when no stream-stage transform is
configured, and packet cloning is avoided when a transform stage has no
bindings.

## UI behavior

The Realm inspector calls the routing component "Routing", exposes the
Outbound egress-stack Protocol and Stream Transport bindings, and uses the
Realm's declared/default ports for independent topology nodes and connection
handles.

The **Realm graph** is no longer assembled from a hard-coded UI diagram. The
backend derives an `execution_graph` from the same canonical Realm configuration
and active global Graph used by the runtime. The UI only lays out and renders
that model. As a result:

- an output that hands off to another Realm shows that Realm as the next hop and
  hides the unused local egress branch;
- a terminal/sink output shows the selected local egress possibilities
  (`Outbound -> Protocol -> Stream Transport`) instead;
- DNS is rendered as its own execution branch (`DNS request -> DNS egress ->
  local resolver/Internet` or a handoff to another Realm), while probes remain
  runtime services rather than fake serial packet-path nodes;
- the **Realm components** list is derived from the same visible
  `execution_graph` component nodes, so the list and diagram cannot describe
  different component sets;
- the global Realms Topology exposes separate **Outbound** and **DNS** handles;
  the DNS handle can connect to Internet or to another Realm's input; and
- graph source declarations are not presented as Realm ingress unless a runtime
  adapter actually dispatches traffic through them.

## Pre-release state compatibility

Meshvianex automatically canonicalizes persisted development state that still
uses the identifiers from before this refactor. In particular:

- `io.meshvianex.protocol.raw` is loaded as `io.meshvianex.protocol.passthrough`.
- `io.meshvianex.transport.tcp` is loaded as `io.meshvianex.transport.raw`.
- the legacy `rule_engine` Realm field/plugin type is loaded as `routing`.

The compatibility aliases are accepted on input only. Runtime snapshots and UI
state use the canonical names, so users do not need to delete their existing
state directory after upgrading a development build.
