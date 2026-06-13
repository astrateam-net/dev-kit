# coder-i18n

Build-time **i18n factory** for the Coder fork. It takes a pristine Coder
checkout, wraps every user-facing string so [Tolgee](https://tolgee.io) can
harvest it, machine-translates the catalog with OpenAI, and bakes the result
back into the frontend as static data — **without committing wrapped sources or
a fork of Coder**. The running app never contacts a translation server.

This is the external "weight" of the russification: the fork
([`containers/apps/dev`](../../../containers/apps/dev)) stays near-stock and
only consumes this module's output (baked catalogs + a thin provider).

> Self-contained Node/TS module inside the Go `dev-kit` repo. Its own
> `mise.toml` toolchain (node + pnpm + biome + ts-morph) is isolated from the
> root Go build — `go build ./...` never sees this subtree.

## Pipeline

```
mise run i18n:build        # runs all six steps in order
```

| Step | Task | What it does |
|------|------|--------------|
| 1 | `i18n:source` | Checkout pristine Coder at the pinned tag into `.upstream/coder` (gitignored). |
| 2 | `i18n:wrap` | `codemod/wrap.ts` (ts-morph) wraps JSX text, mixed text+`<strong>` (tags-interpolation), and user-facing attributes into Tolgee's `<T keyName defaultValue>` / `t()` shapes. Type-aware: skips components whose `children` is typed `string`. Adds `@tolgee/react` deps, formats with Coder's biome. |
| 3 | `i18n:sync` | `tolgee sync` — the native extractor harvests every wrapped key into the Tolgee project. |
| 4 | `i18n:translate` | `tolgee/translate.mjs` — sets the project glossary/style from `tolgee/ai-context.coder.json`, binds an OpenAI prompt, batch-translates all keys (mirrors the atlassian-i18n-toolkit runbook). |
| 5 | `i18n:pull` | `tolgee pull` → `dist/{en,ru}.json` — the committed artifact. |
| 6 | `i18n:bake` | `codemod/inject-runtime.ts` injects `runtime/tolgee.tsx` + the baked catalogs into `site/src/i18n/` and wraps `AppProviders` in `<I18nProvider>`. |

Steps 2 and 6 are pure source transforms (re-runnable, reset with
`git -C .upstream/coder checkout`). Steps 3–5 talk to Tolgee.

## English stays the source of truth

Every wrapped string keeps its English text as `defaultValue`. That is both the
translation source pushed to Tolgee **and** the runtime fallback: any key
without a Russian translation renders English automatically. Russian is the
default for staff; English is one `localStorage` flip away (`setLanguage`).

## Configuration

`.tolgeerc` — project id, API URL, extractor patterns (point at the wrapped
checkout). `tolgee/ai-context.coder.json` — glossary, tone, brand do-not-translate
list, length budget fed to the LLM as the project description.

Environment (`.env`, gitignored):

| Var | Default | Used by |
|-----|---------|---------|
| `TOLGEE_API_KEY` | — | CLI `sync`/`pull` (PAT) |
| `TOLGEE_API_URL` | `http://localhost:8085` | translate.mjs |
| `TOLGEE_PROJECT_ID` | `8` | translate.mjs |
| `TOLGEE_ADMIN_USER`/`PASSWORD` | `admin`/`admin` | translate.mjs (JWT for description/prompt/MT settings) |
| `TOLGEE_LLM_PROVIDER` | `openai-gpt-5.4-mini` | translate.mjs |
| `CODER_VERSION` | `v2.34.1` | source checkout tag |

Tolgee is currently the shared instance from `atlassian-i18n-toolkit`
(localhost:8085); it migrates to the production stack later — only `TOLGEE_API_URL`
changes.

## Consuming the output in `apps/dev`

The fork's Docker build runs this factory in the site stage (after fetching the
pinned tag, before `pnpm build`) and ships only the thin glue. Nothing
generated is committed to the fork beyond the baked `dist/` catalogs and the
provider.

## Honest limitations

- **ICU plurals** are not auto-generated — `n === 1 ? "x" : "y"` patterns are
  skipped and reported; the handful of real plurals are authored by hand.
- The codemod reports everything it could not wrap (dynamic strings, string-children
  components, non-block components) rather than dropping them silently. Tolgee's
  `extract check` independently lists dynamic-default warnings.
