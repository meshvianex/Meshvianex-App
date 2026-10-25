# Meshvianex
<img width="1916" height="1006" alt="meshvianex-images" src="https://github.com/user-attachments/assets/2851c0af-fcc4-41f8-b2b5-1b6fffadf8de" />

Meshvianex is an architecture-validating MVP of a resilient, modular networking runtime for unreliable or constrained networks. The name brings together **Mesh**, **Via**, and **Nex** to reflect interconnected paths, routing, and modular Realm/Graph connections.

This repository was implemented from the specifications under `docs/specs/`.

## What is implemented

- Small protocol-agnostic Core primitives
  - plugin registry and ABI-v1 metadata
  - capability enforcement
  - Realm lifecycle/runtime and Realm-local plugin instances
  - typed Graph validation, cycle detection and route tracing
  - health-aware graph selection
  - event bus
  - signed package import using Ed25519 + SHA-256 content verification
- Networking plugins
  - `io.meshvianex.inbound.http`: local HTTP/1.1 proxy with CONNECT tunneling
  - `io.meshvianex.inbound.socks5`: loopback-first SOCKS5 CONNECT inbound
  - `io.meshvianex.outbound.http`: outbound HTTP/HTTPS CONNECT proxy connector with optional Basic authentication
  - `io.meshvianex.outbound.socks5`: SOCKS5/SOCKS5h outbound with username/password authentication
  - `io.meshvianex.outbound.xray`: in-process Xray-core TCP outbound through a loopback-only SOCKS bridge
  - `io.meshvianex.direct`: direct TCP outbound
  - `io.meshvianex.dns.system`: system DNS resolver
  - `io.meshvianex.rules.basic`: simple domain/CIDR/port/purpose rules
  - `io.meshvianex.subscription.http`: bounded local/HTTP subscription source
  - `io.meshvianex.config.parser.basic`: normalized JSON, base64, and proxy URI parser
  - `io.meshvianex.diagnostic.connectivity`: DNS/TCP/TLS health probe
- Persistent resource library
  - atomic subscription/config storage under the configured state directory
  - stable refresh reconciliation that preserves config IDs and marks removed entries stale
  - redacted control-plane snapshots (including URL userinfo and sensitive
    query values), registry-selected parser/URI import,
    automatic refresh intervals, and explicit Realm assignment/default actions
- UI plugin
  - `io.meshvianex.ui.web`: loopback control dashboard for Realm lifecycle,
    chain ordering, Plugin inventory, resource management, and declarative `/api/schema`
  - Windows opens the dashboard in a separate app window when Edge, Chrome,
    Brave, or Chromium is installed
- Baseline TCP protocol/transport composition, ordered packet/stream transforms
  (including a real one-shot handshake stage),
  health-driven routing, and Graph-mediated live TCP Realm forwarding
- Examples for direct and upstream-proxy Realms
- Unit tests and a package-signing development tool
- Resumable, multi-source content acquisition and offline transfer planning
- Realm/Graph revision stores, transactional updates, last-known-good state, and
  restart-loaded aggregate application state

## Build and run

```bash
go test ./...
go build -o bin/meshvianex ./cmd/meshvianex
./bin/meshvianex -config examples/direct.json
```

Then configure an application to use `http://127.0.0.1:8080` as its HTTP/HTTPS proxy. The dashboard is served at `http://127.0.0.1:8787`; on Windows it opens automatically in an app window. Use the Sun/Moon control in the dashboard header to switch between Dark and Light mode; the choice is saved for the next visit.

The startup `Meshvianex HTTP proxy` is a separate application-level listener;
it routes new connections through the active Graph's Local Source, including
subsequent Realm and Graph edits, and rejects disconnected routes with HTTP 502.
See `RUNNING.md` for its listen-address setting and usage.

For full instructions see `RUNNING.md`; for code architecture and extension guidance see `DEVELOPMENT.md`.

## Current MVP boundaries

This is a working prototype, not a production anonymity or censorship-resistance product. Packaged third-party plugins can be verified and stored, but dynamic WASM execution is intentionally not included; the current executable plugin set is compiled-in Go. The SOCKS5 and Xray outbound bridges support TCP CONNECT only. TUN/transparent OS adapters, UDP/QUIC transports, peer transports, OS keystore integration, and production sandboxing remain platform/runtime follow-on work. TCP Graph-mediated cross-Realm forwarding is implemented as a bounded baseline; it does not imply UDP, TUN, peer, or production-isolated execution. See `docs/IMPLEMENTATION-STATUS.md` for the exact boundary.
