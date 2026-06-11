# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.2.0] - 2026-06-11

### Added

- **jetbroker** — `Gateway.BrowserURL`: an optional browser-facing gateway address. When set, it
  backs the descriptor URLs the browser opens (the player `gateway_url`, the launch page, and the
  KDC-proxy URL), while `BaseURL` keeps serving the server-side legs (`/jet/heartbeat` selection and
  `/jet/preflight` injection). This lets a host send the browser through a front proxy (for example
  a reverse-proxied subdomain) to a gateway it cannot reach directly. Backward compatible: an empty
  `BrowserURL` falls back to `BaseURL`, so existing single-address farms are unaffected.

## [0.1.0] - 2026-06-11

### Added

- **jetbroker** module — browser-RDP authority for Coder, driving a Devolutions Gateway:
  - gateway-farm selection (heartbeat-based liveness + least-loaded-by-weight pick, no failover);
  - provisioner-signed Jet token minting (RS256): `ASSOCIATION`, `SCOPE`, `KDC`, `WEBAPP`;
  - server-to-server credential injection via `/jet/preflight` — the real password never reaches
    the browser;
  - WEBAPP login token so the gateway-webapp session survives its periodic expiration check;
  - KDC-proxy URL for domain (Kerberos) targets.
  - Ports & adapters design — `IdentityResolver`, `TargetResolver`, `GatewayResolver`, `SecretStore`.
- `cmd/test-authority` dev harness — env-driven, exercises the full chain without Coder.
- Developer tooling: `mise` tasks, `hk` git hooks, `golangci-lint` + `gofumpt` config,
  `.editorconfig`.
