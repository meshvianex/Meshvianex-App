# Running Meshvianex

## Requirements

- Go 1.26 or newer
- Linux, macOS, or Windows for the current userspace proxy MVP

The embedded Xray-core runtime and its Go modules are downloaded on the first
build. Pre-populate the Go module cache when building offline.

## 1. Test and build

From the repository root:

```bash
go test ./...
mkdir -p bin
go build -o bin/meshvianex ./cmd/meshvianex
```

On Windows, use `bin/meshvianex.exe` as the output path.

## 2. Run the direct local HTTP proxy

```bash
./bin/meshvianex -config examples/direct.json
```

Persistent subscriptions and normalized configs are stored atomically under
the platform config directory by default. Override this location with
`-state-dir path` or `MESHVIANEX_STATE_DIR` when running multiple instances or
when an explicit backup location is required.

The same directory contains the application aggregate state. After the first
successful save, persisted Realm and Graph state takes precedence over mutable
Realm/Graph sections in the JSON configuration file. `save_changes` performs
the durable write and reports failures; resource files and the aggregate use
temporary-file plus atomic-rename writes.

Defaults in that example:

- local HTTP proxy: `127.0.0.1:8080`
- web UI: `127.0.0.1:8787`
- outbound: direct TCP

Quick test:

```bash
curl --noproxy '' -x http://127.0.0.1:8080 https://example.com/
```

Meshvianex also starts an application-level proxy (logged as `Meshvianex HTTP
proxy: http://127.0.0.1:<port>`). Unless overridden with
`MESHVIANEX_HTTP_PROXY_LISTEN`, its port is chosen automatically. This listener
routes each new HTTP request or HTTPS CONNECT through the **current** Local
Source (`source`) edge of the active Graph, then through the selected Realm's
outbound and any downstream Realm connections. Changing or restoring the Graph
in the dashboard takes effect without restarting the proxy. If Local Source
has no usable route to a Realm, the proxy returns `502 Bad Gateway` instead
of bypassing the Graph. The listener remains available if no Realm exists at
startup so it can adopt one created later.

On Windows, Meshvianex opens the UI automatically in a separate app window using
an installed Edge, Chrome, Brave, or Chromium runtime. On Linux and macOS, open
`http://127.0.0.1:8787` in a browser. If no compatible runtime is available on
Windows, the UI server remains available at the same address for manual access.
The Sun/Moon control in the dashboard header switches between Dark and Light mode
and remembers the user's choice.

## 3. Route through an upstream HTTP proxy

Copy the example and change the proxy URL:

```bash
cp examples/upstream-http-proxy.json my-proxy.json
```

Edit:

```json
{"url":"http://127.0.0.1:10808","timeout_seconds":15}
```

You may embed credentials in the URL (`http://user:pass@host:port`) or use the `username` and `password` fields. Then run:

```bash
./bin/meshvianex -config my-proxy.json
```

The local client still connects to `127.0.0.1:8080`; Meshvianex selects the `proxy-main` outbound and issues CONNECT to the configured upstream proxy. The checked-in example uses the local HTTP proxy at `127.0.0.1:10808`; change it when using a different upstream.

HTTPS upstream proxies are supported with `https://...`. `insecure_skip_verify` exists only for controlled development environments and should remain false in normal use.

## 4. Route through an upstream SOCKS5 proxy

Use the included example as a starting point:

```bash
./bin/meshvianex -config examples/upstream-socks5.json
```

Change the `url` in the `io.meshvianex.outbound.socks5` component to the real
upstream (`socks5://host:port`, `socks5h://host:port`, or a URL with
`user:password@`). `socks5h` sends the destination hostname to the proxy for
resolution; the outbound supports TCP CONNECT only.

## 5. Validate configuration without starting listeners

```bash
./bin/meshvianex -config examples/direct.json -validate
```

If a Graph is present in the configuration, it is validated too.

## 6. Trace a Graph

A configuration containing a `graph` object can be traced with:

```bash
./bin/meshvianex -config path/to/config.json -trace source-node -purpose user_traffic
```

The command prints the selected edge path as JSON. Failed health nodes are excluded and lower-priority healthy paths are used as fallback.

## 7. Resource library and health probes

Open the dashboard and choose `Subscriptions & Configs` to create a raw or URL
subscription, refresh/test it, review normalized configs, and assign resources
to Realms. Config and subscription snapshots redact URL credentials and
credential-like fields. Manual resource edits intentionally expose only safe
metadata; replace a secret-bearing source/config when changing credentials.
Subscriptions with a positive `refresh_interval` refresh automatically while
enabled; a failed refresh keeps the last-known-good normalized configs and
records the failure in the status fields. Assigning a subscription or config
does not select a default outbound. Use the explicit `Set default` action when
that routing choice is intended.

Realm configurations can include `probes` with
`io.meshvianex.diagnostic.connectivity`; invoke the `probe_realm` control action
to record DNS/TCP/TLS results in the health store. A Realm with no probe result
is reported as `unknown`, not healthy.

## 8. Security defaults

The HTTP inbound refuses to bind a non-loopback address unless its configuration explicitly sets `allow_remote: true`. This is deliberate: accidentally exposing an unauthenticated forward proxy to a LAN or the Internet is dangerous.

The HTTP inbound is HTTP/1.1 and CONNECT oriented. The SOCKS5 inbound and
outbound implement TCP CONNECT and username/password authentication; UDP
ASSOCIATE, HTTP/2 proxy semantics, transparent interception, TLS MITM, and
certificate injection are not implemented.

## 9. Stop

Press Ctrl+C or send SIGTERM. Realm components and the UI server are stopped in reverse lifecycle order.
