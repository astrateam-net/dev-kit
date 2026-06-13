# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **coder-i18n** — build-time factory for Russian localization of the Coder UI (Node/TS module with
  its own `mise.toml`, isolated from the Go toolchain). A ts-morph codemod wraps the frontend's
  string literals into Tolgee `<T>`/`t()` shapes (type-aware — skips components whose `children` is
  typed `string`), `tolgee sync` + an OpenAI batch (the atlassian-i18n-toolkit method: project
  glossary → bound prompt → one all-keys batch) translate them, and the baked `dist/{en,ru}.json`
  catalogs plus a thin `<I18nProvider>` are injected into the frontend. English remains the source
  of truth and runtime fallback; Russian is the staff default, switchable via `localStorage`. Full
  pipeline: `mise run i18n:build` (source → wrap → sync → translate → pull → bake). The Coder fork
  consumes only the baked catalogs + provider; no wrapped sources are committed.

### Changed

- **Tooling** — quality gates are now language-scoped for the polyglot repo. `mise run check`
  splits into `check:go` (vet, golangci-lint, gofumpt, shellcheck) and `check:i18n` (biome + tsc),
  and the `hk` pre-commit hook runs only the gate whose files changed — a Go-only commit never
  spins up Node, and vice versa. `gofumpt` now formats **tracked** Go files only (`git ls-files`),
  so it no longer descends into gitignored build checkouts such as `coder-i18n/.upstream`.

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
