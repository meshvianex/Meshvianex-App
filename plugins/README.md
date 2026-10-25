# Built-in plugins

- `direct`: direct TCP outbound
- `dns_system`: system DNS resolver
- `rules_basic`: deterministic metadata routing rules
- `http_inbound`: local HTTP/1.1 + CONNECT proxy
- `socks5_inbound`: loopback-first SOCKS5 TCP CONNECT proxy
- `http_upstream`: HTTP/HTTPS CONNECT upstream proxy connector
- `socks5_outbound`: SOCKS5/SOCKS5h TCP CONNECT upstream connector
- `xray_outbound`: embedded Xray-core TCP outbound through a loopback-only SOCKS bridge
- `subscription_basic`: bounded local/HTTP subscription source
- `config_parser`: normalized JSON/base64/proxy-URI config parser
- `health_probe`: DNS/TCP/TLS connectivity probe
- `tcp_transport`: context-aware TCP transport baseline
- `raw_protocol`: pass-through protocol baseline over a selected transport
- `transform_basic`: ordered metadata/stream transform test component
- `ui_web`: loopback web UI provider and declarative schema endpoint

These plugins are compiled into the MVP host. Their manifests are available through the runtime registry/API snapshot.

`xray_outbound` embeds Xray-core v26.3.27 in an isolated Meshvianex worker process
for each plugin instance, so it does not need a separate Xray executable. Set
`config_path` or `inline_config` to the Xray JSON outbound settings. Meshvianex
rejects user-provided Xray inbounds and creates one private `127.0.0.1` SOCKS
inbound with UDP disabled. The adapter supports TCP outbound flows; Xray's UDP,
TUN, and transparent interception are outside the Meshvianex contract. Each
Realm can run its own Xray worker concurrently.
