# Historical migration fixtures

These fixtures contain synthetic data only. No production database, credentials,
provider response, user report, or downloaded market dataset was used.

## Provenance

The reference Go checkout is `a6a7d98` (the 5.2.5 migration implementation with
Python project scaffolding). Published source tags cover every local release
from `1.2.4` through `5.2.5`; their exact mapping is checked against
`storage/historical/published_versions.json`.

`published_databases.json` contains 56 source-schema groups covering all 98
published tags. Tags share a group only when their persistence-model and
migration source trees are identical. Each representative was extracted through
`git ls-tree`/`git cat-file` into an isolated directory, and its own Go migration
or declared startup AutoMigrate model list created a disposable SQLite database.
The schema objects and synthetic initial rows were exported as JSON. Repeated
schema objects are stored once by SHA-256. Blob values are tagged Base64.

The original 1.2.4–1.2.6 full applications do not compile because their tagged
sources refer to missing symbols. For those three versions, the exact original
persistence structs, embedded structs, scalar aliases, tags and TableName
methods were extracted into a model-only Go program; GORM generated their
original database layout without compiling or running the application.

The generator used the locally cached Go 1.25 toolchain, `GOPROXY=off`, and
isolated runtime/database paths. No application startup, scheduler or provider
operation was invoked. Generated source files and binaries are temporary.

## Financial parity

The `fixed_capital`, `freeze`, and `capital` before/after fixtures use the
original Go tests' nonempty inputs and the original migration implementation:

- `TestSchema12RebasesFixedCapitalAndRestoresApprovedHistoricalBuy`
- `TestSchema29FreezesAndLiquidatesResearch1WithoutChangingResearch2`
- `TestSchema31RebuildsSlotCapitalAndSameDayFixedAllocation`

Fixtures are captured immediately before and after one migration. The tests
compare persisted financial fields, original deterministic IDs, nanosecond
timestamps and ownership. Wall-clock creation/update times and random IDs are
excluded only where the original operation deliberately generates them.

`main_empty_golden.json` is the result of applying the original Go main
migrations 1–35 to an empty disposable database. It checks configuration
projection, 24-account balances, capital events and ledger snapshots.

## Production data boundary

The tests load fixture rows into pytest temporary databases. The Python
migration runner never invokes Go. Historical checksums, DDL and model defaults
are frozen data under `storage/historical`; later prediction changes must not
alter them. Newly added migrations receive new IDs instead of changing the
published 1–35/main or 1–3/minute records.

Run this scope with the project Python environment and an external temporary
directory: `python -m pytest tests/storage --basetemp <disposable-directory>`.
