# Go-Stock Working Rules

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

## Change budget

- Explain necessity and long-term cost in the final response when a task adds
  more than 200 production lines, changes more than 10 source files, or adds a
  public interface, configuration option, schema object, or background job.
- Treat tests, documentation, and generated files separately from production
  code when reporting size. These measurements explain the change; they are not
  mechanical pass/fail gates.

## Verification

- Diagnosis and read-only audits do not run tests by default.
- Routine implementation uses `scripts/verify.ps1 -Tier fast` with the affected
  Go package/test or frontend test file. Cross-boundary work may use one
  matching `domain` verification.
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
