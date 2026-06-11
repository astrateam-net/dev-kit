# DevKit

Host-side **extension modules for [Coder](https://coder.com)** — small, portable Go packages that
add capabilities to a Coder deployment behind clean ports & adapters.

> **Unofficial, community project.** Not affiliated with or endorsed by Coder Technologies.

Each module is a self-contained package: a portable core that depends only on a few interfaces, with
the host-specific wiring (the Coder fork's adapters + routes) kept outside this repository. That
keeps every module testable on its own and portable if the host platform changes.

## Modules

| Module | What it adds |
| --- | --- |
| [`jetbroker`](jetbroker/README.md) | Browser-RDP authority: one-click, fully in-browser RDP into a Coder workspace through a Devolutions Gateway, with the real password never reaching the browser. |

_More modules will land here over time._

## Layout

```
jetbroker/            # module: browser-RDP authority (Devolutions Gateway)
cmd/test-authority/   # dev harness for jetbroker (env-driven, no Coder needed)
.mise/tasks/          # mise task scripts (lint, fmt, test, tree)
```

## Develop

[`mise`](https://mise.jdx.dev) manages the toolchain and tasks; [`hk`](https://hk.jdx.dev) runs the
git hooks. Entering the repo installs the tools and self-installs the hooks.

```sh
mise run build        # go build ./...
mise run test         # go test ./...
mise run check        # all quality gates: vet, golangci-lint, gofumpt --check, shellcheck
mise run ci           # check + build + test
mise tasks            # list every task
```

Without mise, the plain Go commands work too: `go build ./...`, `go vet ./...`, `go test ./...`.

## Conventions

- **Conventional Commits**, enforced by the `hk` commit-msg hook.
- Work on branches — direct commits to `main`/`master` are blocked by the pre-commit hook.
- See [AGENTS.md](AGENTS.md) for the contributor/agent quick reference and
  [CHANGELOG.md](CHANGELOG.md) for history.

## Module path

The Go module is `github.com/astrateam-net/dev-kit`; `jetbroker` imports as
`github.com/astrateam-net/dev-kit/jetbroker`. If this is published under a differently-named
repository, align the module path with the repo URL so `go get` resolves.

## License

[Apache-2.0](LICENSE).
