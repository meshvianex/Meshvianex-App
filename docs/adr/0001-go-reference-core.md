# ADR 0001: Go for the executable reference Core

Status: Accepted for MVP prototype

## Context

The architecture proposes Rust for the Core and WASM for portable plugins,
while treating those choices as tentative pending prototype validation. A Go
reference implementation can validate the runtime contracts and networking
behavior without making the long-term language decision irreversible.

## Decision

Implement the executable MVP in Go. Preserve architecture boundaries through
stable plugin IDs, explicit capabilities, Realm-local instances, typed service
interfaces, JSON configuration, and protocol-specific plugins outside the Core.

## Consequences

Positive:

- Go provides straightforward concurrency and cross-platform tooling;
- userspace TCP proxying can be tested through standard Go interfaces;
- interfaces can serve as executable reference semantics.

Negative / deferred:

- compiled-in Go plugins are not a sandbox;
- dynamically loaded WASM plugins are not yet executable;
- a later Rust/WASM implementation may coexist or replace the Go host while
  retaining schemas and semantics.
