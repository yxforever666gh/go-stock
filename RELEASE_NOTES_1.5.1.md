# Go-Stock App 1.5.1 Release Notes

Release date: 2026-08-06

## Scope

App 1.5.1 establishes the release and runtime governance foundation. The active
strategy identity remains immutable at Strategy 1.5.0 with the same fixed config
hash and replay behavior.

Strategy production is deliberately paused during the refactor. Historical
recommendations remain available for read-only queries; no paused-period
recommendations will be backfilled.

## Runtime governance

- Added a persisted `paused/live` strategy runtime control, defaulting to
  `paused`. Only CLI commands may change it; HTTP and UI are read-only.
- Added fail-closed gates around recommendation publication, activation,
  simulated order events, corporate actions, and yield writes.
- Froze legacy cohorts against update, delete, repair, activation, exit, and
  performance recalculation.
- Removed startup repair/backfill side effects.

## Release identity and health

- Added one embedded release manifest for App, Strategy, and both schema
  versions.
- Build metadata now includes Git commit, build time, dirty status, and the
  running executable SHA256.
- Added `/livez`, `/readyz`, `/api/v1/system/version`, and the read-only
  `/api/v1/strategy/runtime` endpoint.
- Restricted the Web server to loopback listen addresses, loopback peers,
  accepted local Host values, and same-origin requests.

## Database and deployment

- Replaced asynchronous startup migration with synchronous, numbered,
  checksummed migrations for both SQLite databases.
- Added `db status|backup|migrate|verify`, `strategy status|pause|resume`, and
  `release inspect` CLI commands.
- Added the local Windows pipeline for one-time candidate builds, snapshot
  migration rehearsal, two-database backup, exact readiness checks, deployment
  receipts, and automatic rollback.
- Added a manifest-verified frozen replay bundle gate. The two SQLite files are
  hashed before they are opened, then row counts, cache `AsOf`, `quick_check`,
  the 226-rule corpus, and the fixed replay hash are verified read-only.

## Architecture transition

- Added provider-neutral market data, news, and evidence contracts.
- Added explicit recommendation, execution, portfolio, and legacy boundaries.
- Marked `backend/data` deprecated and introduced dependency-direction tests.
- Added the first versioned OpenAPI surface while retaining an explicit
  allowlisted compatibility bridge for the staged Web migration.

These architecture contracts are preparatory. They do not change Strategy
1.5.0 candidate, plan, execution, fee, or return semantics.
