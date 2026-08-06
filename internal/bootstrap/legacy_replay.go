package bootstrap

import (
	"context"

	"go-stock/backend/data"
	"go-stock/backend/db"
)

// LegacyReplayOptions and LegacyReplayReport keep the read-only CLI contract
// independent from the deprecated data package. The database handle remains a
// bootstrap concern while the legacy replay is being strangled out.
type LegacyReplayOptions = data.LegacyStructuredRuleReplayOptions
type LegacyReplayReport = data.LegacyStructuredRuleReplayReport

func ReplayLegacyStructuredRulesCacheOnly(ctx context.Context, options LegacyReplayOptions) (LegacyReplayReport, error) {
	return data.ReplayLegacyStructuredRulesCacheOnly(ctx, db.Dao, options)
}
