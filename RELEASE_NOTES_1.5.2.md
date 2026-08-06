# Go-Stock App 1.5.2 Release Notes

Release date: 2026-08-06

## Scope

App 1.5.2 establishes dependency boundaries for the staged migration away
from the legacy `backend/data` package. Strategy 1.5.0 remains immutable: its
fixed configuration, cohort identity, replay behavior, and validation hash do
not change. Both database schema versions remain at 2.

Strategy production remains `paused`. This release does not generate or
backfill recommendations for the paused interval and does not modify or
recalculate Strategy 1.4.2 or earlier records.

## Composition and use cases

- Made `internal/bootstrap` the application composition root for both SQLite
  handles, Clock, provider adapters, services, and execution monitoring.
- Routed Web, Cron, CLI, and Agent production entry points through
  `internal/service` use cases and consumer-owned ports instead of direct
  `backend/data` imports.
- Added explicit compatibility adapters for market data, news, market
  intelligence, portfolio ledger, legacy reads, AI-facing operations, and
  execution monitoring while the old package is retired.
- Moved the large sensitive-word list from Go source into an embedded resource.

## Enforced boundaries

- Kept `backend/strategy/v150` on a standard-library-only import allowlist.
- Added architecture checks against global database access, runtime clocks,
  version routing, replaceable package-level function dependencies, and new
  direct imports of the deprecated package.
- Removed direct `backend/data` imports from production delivery and use-case
  code. Remaining imports are explicitly listed bootstrap compatibility
  adapters and can only shrink during later stages.

## Runtime and release hardening

- Disabled historical recommendation repair and backfill capabilities and
  rejected persistent backtests while Strategy production is paused.
- Delayed scheduler start and all immediate startup work until task assembly
  succeeds and readiness can be established.
- Embedded IANA timezone data in the Windows Web binary so scheduler readiness
  does not depend on host timezone files. Each release also carries a
  versioned, hash-verified timezone sidecar for explicit startup and rollback,
  including recovery of the previous 1.5.1 binary; deploy and rollback never
  discover timezone data from an installed Go toolchain.
- Strengthened automatic and manual rollback to verify the previous binary's
  App, commit, SHA256, schema, Strategy, config hash, paused mode, scheduler,
  and complete `/readyz` state before accepting recovery.
- Changed the release branch gate to query actual origin heads and added a
  cross-file App-version consistency test.

## Next stage

App 1.5.3 will migrate the recommendation publication transaction, structured
AI event verifier, execution orchestration, and portfolio truth source into
their dedicated modules. This 1.5.2 release does not rewire those strategy
behaviors and therefore does not alter Strategy 1.5.0 outputs.
