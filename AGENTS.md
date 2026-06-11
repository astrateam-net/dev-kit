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
- Work on branches — direct commits to `main`/`master` are blocked by the pre-commit hook.
- Hooks self-install via mise `postinstall` (`hk install --mise`); bypass once with
  `HK=0 git commit …` if you must.
- Never commit real credentials or keys (`*.pem`, `*.key`, `cmd/test-authority/.env` are gitignored;
  `hk` also blocks private keys).
