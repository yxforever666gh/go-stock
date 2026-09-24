# Stock God Working Rules

These instructions apply to the entire repository. Keep routine work local, make
complexity visible, and reserve full-system proof for an explicit release.

## Scope and complexity

- Prefer, in order: delete obsolete code, simplify existing code, merge real
  duplication, extend a clear existing boundary, then add new implementation.
- Optimize total complexity rather than raw line count. Correctness,
  readability, data safety, and meaningful tests outrank a smaller diff.
- Do not add abstractions, configuration, states, or extension points for
  hypothetical future needs. Extract a shared abstraction after a third real
  use, or earlier only when it creates a necessary dependency boundary.
- A routine task should normally stay within one domain. If it would touch more
  than 10 source files or more than two domains, split the work unless the user
  explicitly requested a cross-domain change.
- When new behavior replaces old behavior, remove the old entry point, branch,
  configuration, and obsolete tests in the same task. If compatibility must
  remain, document its exact removal condition.
- Do not add new business responsibilities to `backend/data`. Existing code may
  be fixed there; new domain logic belongs in a focused package.
- Refactor incrementally: extract at most one complete responsibility while
  delivering a feature or fix. Do not start a repository-wide cleanup unless
  the user explicitly requests it.

## Product and dependency boundaries

- The active product is 股票预测 plus headless market-data APIs. Market,
  stock/fund watchlist, Research 1, and knowledge UI/runtime features are retired.
  Preserve their historical database rows; do not recreate their entry points.
- Preserve market-data API and MCP contracts independently of UI removal.
  Prediction APIs use `/api/v1/prediction`; old prediction routes have no aliases.
- Prediction rules belong to `stock_god.prediction`, provider I/O to
  `stock_god.market` and `stock_god.ai`, and SQLite access to `stock_god.storage`.
  Historical migration algorithms stay frozen inside migrations and are never
  imported by current prediction logic.
- Persisted research2 table names, owner values, and deterministic ID namespaces
  are data contracts. Branding changes must not rewrite them.
- Python is the sole backend runtime after the 6.0 migration. Go is a temporary
  development oracle and is removed after parity tests pass; archived binaries
  remain available only for rollback.
- Capture a deep configuration snapshot at each task entry. Provider retries,
  fallbacks, and evidence collection use that snapshot; never temporarily replace
  global settings. A center settings save only notifies that center.
- Changes to AI, quotes, evidence, chart, SQLite or configuration boundaries
  require the matching domain checks and affected frontend behavior tests.

## Change budget

- Explain necessity and long-term cost in the final response when a task adds
  more than 200 production lines, changes more than 10 source files, or adds a
  public interface, configuration option, schema object, or background job.
- Treat tests, documentation, and generated files separately from production
  code when reporting size. These measurements explain the change; they are not
  mechanical pass/fail gates. Count physical lines including blank lines; report
  runtime prompts, tests, documentation, and generated files separately.

## Verification

- Diagnosis and read-only audits do not run tests by default.
- Routine implementation uses `scripts/verify.ps1 -Tier fast` with the affected
  Go package/test or frontend test file. Cross-boundary work may use one
  matching `domain` verification.
- During the 6.0 migration, new Python scopes use the locked project environment
  and targeted pytest files. Keep old-language checks only while they provide
  the behavior oracle; the final verifier must not require Go.
- Use `scripts/verify.ps1 -Tier release` only for an explicit release or when
  the user explicitly asks for the full local gate.
- Do not automatically run `go test ./...`, `go vet ./...`, `npm run ci`, live
  network checks, production database checks, workspace cleanup, version bumps,
  release builds, deployments, tags, or pushes.
- Do not repeat a passing check unless relevant code changed. After two failures
  with the same cause, stop rerunning and diagnose or report the cause.
- An unrelated or pre-existing failure is reported separately; it does not
  silently expand the task.
- Target budgets are three minutes for `fast`, eight minutes for `domain`, and
  10-20 minutes total for a routine fix. Release verification is separate.

## Tests and data safety

- Unit and repository tests use `t.TempDir()` or another disposable fixture.
  They must not migrate or write `data/*.db` or runtime databases.
- Live network, browser, email, and provider probes require a dedicated
  integration build tag and an explicit opt-in command. They are never part of
  `fast`, `domain`, or `release` verification.
- Ordinary repository tests do not rebuild the complete historical migration
  chain. Full migrations and SQLite integrity checks belong to migration or
  release-specific tests.

## Versioning and release

- Development completion is not release completion. Routine fixes do not
  update versions, release notes, tags, or artifacts.
- Batch compatible development changes into one explicit release. A release
  runs the full local gate once; after a failure, repair and rerun the failed
  scope before repeating the full gate.
- Do not create or use GitHub CI for local validation.

## Workspace and completion

- Only one task may write to a checkout at a time. Parallel writing tasks use
  separate Git worktrees. Preserve unrelated user changes in a dirty worktree.
- Temporary validation output belongs under `H:\Download` when available and
  must be removed before completion.
- A routine task is complete when the requested behavior is implemented, the
  relevant targeted checks pass, required boundary contracts pass,
  `git diff --check` passes for the task changes, and no unrelated changes were
  introduced. Stop when these conditions are satisfied.
- Final responses include a short `Complexity change` note: production lines
  added/removed, source files touched, execution paths added/removed, public
  interfaces/configuration/schema added, and any old path that could not yet be
  removed.

## 6.0.0 release acceptance

- The approved release includes full Python migration, all published legacy
  database upgrades, in-database preservation before destructive historical
  transformations, Stock God naming, and deployment/restart.
- Before tag/push, validate the final candidate twice across complete offline
  chains and run one live market/AI prediction in disposable database copies.
  Live calls are opt-in; test email is delivered to a local fixture.
- Validation binds commit, dependency lock and artifact hashes. A changed final
  candidate needs fresh final validation. Never move an already published tag.
- Temporary scripts, test databases and raw outputs stay under `H:\Download`
  and are not committed. Durable regression tests and sanitized release receipts
  are retained in their respective project locations.
