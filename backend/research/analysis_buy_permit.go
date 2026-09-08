package research

import (
	"context"
	"sync/atomic"

	"go-stock/internal/trading"
)

// AnalysisBuyPermit belongs to exactly one analysis task. Closing the strategy
// revokes that task permanently; enabling it creates a fresh permit next run.
type AnalysisBuyPermit struct{ disabled atomic.Bool }

func (permit *AnalysisBuyPermit) Disable() { permit.disabled.Store(true) }

type analysisBuyPermitKey struct{}

func checkAnalysisBuyPermit(ctx context.Context) error {
	permit, _ := ctx.Value(analysisBuyPermitKey{}).(*AnalysisBuyPermit)
	if permit != nil && permit.disabled.Load() {
		return trading.ErrNewPositionsDisabled
	}
	return nil
}
