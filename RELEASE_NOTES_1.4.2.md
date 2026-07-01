# go-stock 1.4.2 Release Notes

## Summary

1.4.2 focuses on the bottleneck found after 1.4.1: the candidate pool can be non-empty, but AI-generated trade plans often fail the production hard gates and are downgraded to `analysis_only`.

This release does not relax risk controls. It moves trade-plan feasibility earlier in the market-summary recommendation flow, asks AI to self-check the plan it outputs, and records production downgrade reasons for diagnostics.

## Highlights

- Added strategy cohort `V1.4.2`; `current` now maps to `summary_version = 1.4.2`.
- Kept existing `conditional / analysis_only / activation_status` semantics, activation gates, same-day dedupe, and the maximum of 4 production candidates.
- Added precomputed `pullback` and `breakout` feasible plans for verified candidates.
- Marked a path as production-feasible only when `rewardRisk >= 0.80` and `downsidePct <= 5.00%`.
- Added `tradePlanFeasibility` to candidate scoring and `scoreBreakdown`.
- Added `feasiblePlans` to the AI input payload and required AI to use only `passHardGate=true` paths for production candidates.
- Added AI self-check fields in the prompt contract: `hardGateSelfCheck`, `worstEntry`, `rewardRisk`, and `downsidePct`.
- Upgraded second-round supplement into supplement plus one-time constrained plan repair for near-threshold failures.
- Added production downgrade diagnostics through `production_downgrade_reason_top`.
- Added `V1.4.2` to the strategy filters on recommendation records, stock yield list, and yield statistics.
- Kept the Web SPA no-cache fix and stock-yield V1.4.1 copy fix as compatibility fixes.

## Diagnostics

The market-summary diagnostic panel now separates:

- Save/block failures before a record can be persisted.
- Production downgrades where a record is saved but becomes `analysis_only`.

This makes it easier to distinguish an empty run from a run where AI produced plans that failed reward-risk or downside constraints.

## Compatibility

Historical records for `V1.4.1`, `V1.4.0`, `V1.3.6`, `V1.3.2`, and `V1.3.1` remain queryable from the existing filters.

No recommendation status model or activation-status semantics were changed.
