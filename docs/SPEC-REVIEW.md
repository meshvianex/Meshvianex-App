# Specification Review and MVP Mapping

The five source documents describe a plugin-first networking platform whose trusted Core stays small and protocol-agnostic. The most important cross-document invariants are:

1. Installed plugin code is global, while mutable plugin instances are Realm-local.
2. Protocol behavior such as HTTP proxying belongs in plugins, not in the Core.
3. Realm ports and Graph edges are the composition boundary; direct cross-Realm plugin calls are forbidden.
4. Plugin host access is capability-gated.
5. Data-plane and control-plane concerns should remain distinguishable.
6. Graphs must validate typed ports, references, reachability, conditions, and forbidden cycles.
7. Health should affect failover without moving existing flows unless policy/protocol permits it.
8. Offline package installation, signature verification, content hashing, and rollback are architectural requirements.
9. Portable plugin UI should be declarative rather than arbitrary native UI code.
10. Platform-specific networking belongs behind adapters; Linux is the recommended first platform.

## What this repository validates now

- capability-gated plugin creation;
- Realm-local component instances and ordered lifecycle;
- protocol-agnostic Flow/Dialer/RuleEngine/Resolver interfaces;
- typed Graph validation, cycle rejection, purpose conditions, and health-aware route tracing;
- local HTTP proxy inbound as a plugin;
- upstream HTTP/HTTPS CONNECT proxy as a plugin;
- direct outbound, system DNS, and simple routing rules as plugins;
- declarative UI schema exposed by a UI provider plugin;
- Ed25519-signed, SHA-256-verified offline package import into a content-addressed store;
- loopback-safe defaults for local proxy and UI listeners.

## Deferred from the full specification

The source specifications are substantially broader than a single MVP. This repository intentionally does not claim completion of:

- WASM sandbox execution and dynamic third-party plugin activation;
- TUN/transparent proxy platform adapters;
- UDP/QUIC transports;
- full Realm lifecycle state machine and persistent revision history;
- live cross-Realm Graph data-plane chaining;
- transactional Graph mutation API;
- persistent last-known-good rollback;
- acquisition engine, multi-source resumable downloader, offline repository replication, and peer transfer;
- secret-handle integration with OS keystores;
- resource budgets, plugin CPU/memory metering, and crash isolation;
- full trust-root/key-rotation policy;
- production diagnostics/metrics privacy model.

These are listed in `DEVELOPMENT.md` as the hardening and next-phase backlog.
