# Go-Stock Version Upgrade Policy

This document is the release contract for the Windows localhost Web product.
It applies to every change after App 1.5.1.

## Independent version lines

- `AppVersion` identifies product code, UI, packaging, operations, and refactors.
- `StrategyVersion` identifies any behavior that can change candidates, ranking,
  evidence semantics, timing, plans, execution, fees, or reported performance.
- Main and minute database schema versions identify numbered migrations.

An App release does not prove Strategy performance. Strategy validation remains
an independent forward process.

## Change classification

1. Bug fixes, refactors, UI changes, API changes, and release tooling change only
   `AppVersion` when they do not alter strategy output or accounting semantics.
2. Candidate selection, ranking, prompt meaning, model responsibility, data
   timing, plans, execution, fees, and performance accounting require a new
   immutable `StrategyVersion` and cohort.
3. Every schema change gets a new migration ID. An applied migration's name,
   checksum, and implementation are never edited.

When classification is uncertain, treat the change as a Strategy change.

## Main-only development

- Development and release happen only on `main`; no release branches are used.
- Each commit has one bounded purpose and must compile with its focused tests.
- Moving code and changing strategy behavior must be separate commits.
- `currentStrategyVersion` changes only in the final release commit for a new
  Strategy cohort.

## Candidate and deployment

1. Start from a clean `main` commit on the local Windows release host.
2. Run Go tests and vet, frontend lint/build, OpenAPI generation consistency,
   migration tests, frozen replay, and version consistency checks.
   Frozen replay must use the manifest-approved SQLite snapshot pair, never the
   mutable production databases. Both database SHA256 values are checked before
   SQLite is opened.
3. Build the Windows Web executable once into
   `runtime/releases/<appVersion>/<commit>/` and record its SHA256.
4. Rehearse both database migrations on consistent snapshots.
5. Stop the old process, back up both live databases, migrate, start the exact
   candidate artifact, and verify `/readyz` identity and readiness.
6. On failure, restore both databases and the previous binary pointer. Keep the
   failed `main` commit, but do not create or move a tag.
7. After successful deployment, create an annotated immutable App tag, push
   `main` and the tag, and publish the same artifact, manifest, and SHA256.

Production directories must never be build destinations.

## Strategy releases

- Released strategy code, configuration, config hash, and replay fixtures are
  immutable.
- A new strategy lives in a new version package and writes a new
  `summary_version` cohort.
- Candidate snapshots, rules, order events, and portfolio results never cross
  cohort boundaries.
- No recommendation is backfilled for a paused interval.
- A strategy remains "forward validation pending" until it has at least 60
  trading days, 100 closed trades, and 40 independent recommendation days and
  passes the fixed statistical thresholds for that Strategy version.

## Operational controls

- Strategy production defaults to `paused` and can change mode only through the
  CLI with an audit reason.
- UI and HTTP APIs expose the mode read-only.
- Historical Strategy 1.4.2 and earlier rows remain queryable but immutable.
- Market and news caches may continue while strategy production is paused.
