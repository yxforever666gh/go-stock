# Frozen replay bundle

App 1.5.1 release acceptance does not replay against the mutable production
cache. It uses an immutable pair of SQLite snapshots described by
`release/replay_bundle_manifest.json`.

The database files are intentionally not committed to Git. Provision the
approved pair out of band at the default local path:

```text
runtime/backups/pre-refactor-20260806-001309/
  stock.db
  minute.db
```

The small committed manifest fixes:

- both database file names and SHA256 values;
- the legacy-rule and minute-bar row counts;
- the minute-cache `AsOf` value;
- the recommendation cutoff and expected 226-rule corpus;
- the deterministic replay result hash.

Verify the bundle directly:

```powershell
go run ./cmd/go-stock-cli release verify-replay-bundle `
  --bundle runtime/backups/pre-refactor-20260806-001309 `
  --manifest release/replay_bundle_manifest.json
```

The verifier hashes both files before opening SQLite. It then opens both files
read-only, checks their row counts, `AsOf`, and `PRAGMA quick_check`, and only
then runs the 226-rule replay twice. Any mismatch fails the command.

`scripts/release.ps1` invokes this verifier during build preflight. Deploying an
already-built candidate invokes it again so an existing artifact cannot bypass
the replay gate. Override the local bundle only when pointing to a byte-for-byte
copy of the manifest-approved files:

```powershell
powershell -ExecutionPolicy Bypass -File scripts/release.ps1 build `
  -ReplayBundle D:\approved\go-stock-replay-1.5.0
```

Read-only SQLite mode prevents the verifier from writing. It does not make a
live database immutable, which is why production `data/stock.db` and
`data/minute.db` must never be used for this acceptance hash.
