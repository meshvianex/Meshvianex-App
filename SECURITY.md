# Security Notes

Meshvianex is an MVP and should not be treated as a production security boundary.

- Local HTTP proxy binding is loopback-only by default.
- Do not set `allow_remote: true` unless you add network-layer access controls and understand the risk of exposing an unauthenticated proxy.
- Upstream proxy passwords are currently configuration fields; production code should replace these with secret handles/OS keystores.
- Third-party executable plugin sandboxing is not implemented yet. Only compiled-in plugins execute.
- `.mvplug` import verifies trusted publisher signatures and payload hashes, but package dependency resolution and activation rollback are not complete.
- `insecure_skip_verify` for HTTPS upstream proxies is a development escape hatch and should be false.

Report security issues privately to the project maintainer rather than publishing exploit details before a fix is available.
