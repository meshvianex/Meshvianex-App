# Implementation status

This repository is a Go reference implementation of the specifications in
`resilient-networking-docs/`. The items below describe the current boundary;
they are deliberately based on executable code rather than document examples.

## Implemented

- capability-gated plugin registry, dependency-cycle checks, ABI metadata,
  structured capability errors, instance contexts, and per-instance resource
  budgets;
- Realm-local plugin instances with type-aware component validation, lifecycle
  states, separate data/control outbound selection, normalized default Realm
  ports, revision updates, and rollback;
- typed graph validation, purpose/health/tag/network-state conditions,
  weighted selection, reachability checks, atomic revision transactions, route
  tracing, aggregate plus per-object persistent graph/Realm config stores, and bounded live TCP
  cross-Realm forwarding; Realm status snapshots also expose a backend-derived
  `execution_graph` that merges the Realm-local pipeline with the active global
  Graph so the UI renders the runtime handoff/terminal path instead of a
  duplicated hard-coded topology; the same model also exposes a dedicated DNS
  execution branch and drives the Realm-components inspector list; Realm graph
  nodes can be manually arranged with pointer drag or keyboard arrows, persist
  per Realm in the local UI profile, and can be restored with Auto layout;
- normalized Flow, Packet, Stream, Datagram, Endpoint, and transform-stage
  contracts;
- Realm component families for inbound, DNS, routing, transforms, protocols,
  stream transports, outbounds, and probes, with typed reference validation,
  lifecycle ordering, and plugin-owned service interfaces; selected outbounds
  own their optional protocol/transport binding, so routing cannot be bypassed
  by a plane-level stack override;
- real TCP SOCKS5/SOCKS5h outbound and loopback-first SOCKS5 inbound plugins,
  including username/password authentication and Realm-routed inbound flows;
- persistent subscription/config resources with atomic writes, redacted
  snapshots, parser/source plugins, stable refresh reconciliation, stale
  removal handling, registry-selected parsers, URI import, Realm assignment,
  automatic interval refresh, and explicit connectivity tests;
- startup loads the atomic application aggregate before the initial config
  file's mutable Realm/Graph sections, while resource files remain in the same
  state directory; URL userinfo, sensitive query values, and diagnostic text
  are redacted only at presentation boundaries;
- a strict compiled-in `protocol` runtime contract for `netx.Protocol`
  instances; HTTP/SOCKS proxy outbounds and inbounds are not advertised as
  Realm protocols, the neutral protocol is named `passthrough`, the baseline
  Xray-style stream transport is named `raw`, and the accepted `handshake`
  transform stage executes once before the first application byte;
- resource assignment never selects or replaces a Realm's default outbound;
  replacing it is an explicit control-plane action;
- ordered Realm DNS resolver fallback plus a Graph-routable DNS egress: DNS can
  terminate at Internet through the local resolver chain or delegate to another
  Realm's resolver context, with local fallback when that Realm route is
  unavailable; DNS/TCP/TLS health probes expose structured results and a
  non-fabricated unknown health state;
- health metrics, network-state classification, adaptive acquisition
  concurrency, and event versions/type-filtered subscriptions;
- signed package import with path safety, manifest/payload hashing, a
  content-addressed object store, plugin tickets, offline repository index,
  and last-known-good activation state;
- chunked resumable acquisition from local files or HTTP range sources, with
  persistent resume metadata, per-chunk hashes, source failover, and final
  object verification;
- privacy-preserving full/manifest-only/smart transfer planning;
- namespaced persistent storage, opaque secret handles, ABI buffers/errors,
  platform capability discovery, and a bounded Core test engine.

## Still requires a platform/runtime implementation

These items cannot be honestly represented by the compiled-in Go reference
plugins alone and remain explicit boundaries:

- WASM execution and native process isolation for third-party plugins;
- TUN/transparent OS adapters, UDP/QUIC transports, Bluetooth/Wi-Fi Direct,
  and authenticated peer transport;
- UDP/TUN live forwarding and peer/remote transport composition; the web UI
  reports these capabilities as unsupported rather than active;
- OS keystore-backed secret integration, CPU/memory metering, crash
  supervision, and production key rotation/transparency policy;
- repository network discovery/replication, QR/UI flows, and a production
  update canary pipeline.

The existing compiled-in plugins provide working HTTP and TCP SOCKS5 proxy
paths. SOCKS5 UDP ASSOCIATE, transparent interception, TUN, dynamic WASM
execution, and unsupported platform capabilities are not silently treated as
available.
