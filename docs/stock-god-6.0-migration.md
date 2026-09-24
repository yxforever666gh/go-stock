# Stock God 6.0.0 migration

Development baseline: `4838d135d2a1eac08ddb3ef892ad1f8c5bc47dee` (5.2.5, schema 35/3).

The release replaces the Go runtime with Python, removes market/watchlist displays,
Research 1 and knowledge features, and exposes the remaining research feature as
股票预测. Market data APIs, minute/MCP services and all historical data remain.

## Release acceptance

- [ ] Retained API and MCP inventory captured; prediction routes move to `/api/v1/prediction`.
- [ ] Python market, prediction, AI, storage, CLI and minute/tunnel services are complete.
- [ ] Published legacy schemas, including databases without a migration ledger, upgrade directly.
- [ ] Original rows affected by destructive historical migrations are archived in the same SQLite.
- [ ] Retired UI, APIs, tasks, Go runtime dependencies and knowledge injection are removed.
- [ ] Final directory/repository name is `stock-god`; archived release identities remain intact.
- [ ] Final candidate passes two independent full-chain runs and one isolated live prediction.
- [ ] Annotated `6.0.0` and main are pushed only after validation; remote SHAs are verified.
- [ ] Final candidate is deployed, restarted and checked against its runtime identity.

New runtime code must not import historical migration algorithms. Stored table
names, research2 owner identifiers and historical UUID namespaces are data contracts.
Temporary harnesses and database copies belong under `H:\Download`, never production databases.
