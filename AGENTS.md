# dev-kit

Go repository of extension modules for [Coder](https://coder.com). `mise` is the task runner and
tool manager; `hk` runs the git hooks.

**Start here:** `mise run tree` to see the layout; `mise tasks` to list every runnable task.

## Modules

- `jetbroker/` — browser-RDP authority driving a Devolutions Gateway. See
  [jetbroker/README.md](jetbroker/README.md).

## Commands (mise)

| Action | Command |
| --- | --- |
| List every runnable task | `mise tasks` |
| See repo structure (use instead of `find`/`ls`) | `mise run tree` |
| Build / vet / test | `mise run build` · `mise run vet` · `mise run test` |
| Lint Go / shell | `mise run lint:go` · `mise run lint:shell` |
| Format (write / verify) | `mise run fmt` · `mise run fmt --check` |
| All quality gates | `mise run check` |
| Full local CI | `mise run ci` |

## Conventions

- **Conventional Commits** enforced by `hk` (commit-msg hook): `type(scope): summary`.
- Work on branches — direct commits to `main` are blocked locally by the pre-commit hook AND
  remotely by branch protection. Land changes via PR.
- Hooks self-install via mise `postinstall` (`hk install --mise`); bypass once with
  `HK=0 git commit …` if you must.
- Never commit real credentials or keys (`*.pem`, `*.key`, `cmd/test-authority/.env` are gitignored;
  `hk` also blocks private keys).

## CI & branch protection

- **CI** (`.github/workflows/ci.yml`) runs `mise run ci` (the same gate as the hooks) on every PR
  and push to `main`. `main` is protected: PR required, the `ci` check must pass, linear history,
  no force-push/deletion.
- All GitHub Actions are **SHA-pinned** with a `# vX.Y.Z` comment; `dependabot` bumps them weekly.
  When adding or changing an action, pin the **latest** release by commit SHA.

## Releasing

1. Add a dated section to [CHANGELOG.md](CHANGELOG.md) under the new version (Keep a Changelog).
2. Tag the release commit: `git tag -a vX.Y.Z -m vX.Y.Z && git push origin vX.Y.Z` (Go module
   versions require the `v` prefix).
3. `.github/workflows/release.yml` verifies the tagged commit (`mise run ci`), extracts that
   version's CHANGELOG section as the release notes, and publishes a GitHub Release. The Go module
   is then served by `proxy.golang.org` on first `go get …@vX.Y.Z` — no separate publish step.
