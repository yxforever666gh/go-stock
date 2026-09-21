package research2

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	CapitalEventInitial            = "initial_external"
	CapitalEventTopUp              = "top_up_external"
	CapitalEventLegacyPoolTransfer = "legacy_pool_transfer"
	CapitalValuationBasisLegacy    = "legacy_baseline"
	CapitalValuationBasisLedger    = "capital_ledger_v1"
)

type capitalSummary struct {
	initial  float64
	topUp    float64
	external float64
	transfer float64
	ledger   bool
}

func (summary capitalSummary) profit(nav float64) float64 {
	return nav - summary.external - summary.transfer
}

func (summary capitalSummary) rate(nav float64) float64 {
	if summary.external <= 0 {
		return 0
	}
	return summary.profit(nav) / summary.external
}

func research2CapitalLedgerAvailable(database *gorm.DB) bool {
	return database != nil && database.Migrator().HasTable(&AccountCapitalEvent{})
}

func research2CapitalSummary(ctx context.Context, database *gorm.DB, slot string, fallback float64) (capitalSummary, error) {
	if !research2CapitalLedgerAvailable(database) {
		return capitalSummary{external: fallback}, nil
	}
	var rows []AccountCapitalEvent
	if err := database.WithContext(ctx).Where("slot = ?", slot).Order("effective_at ASC, id ASC").Find(&rows).Error; err != nil {
		return capitalSummary{}, err
	}
	summary := capitalSummary{ledger: len(rows) != 0}
	for _, item := range rows {
		if item.External {
			summary.external += item.Amount
			switch strings.TrimSpace(item.EventType) {
			case CapitalEventInitial:
				summary.initial += item.Amount
			case CapitalEventTopUp:
				summary.topUp += item.Amount
			}
			continue
		}
		summary.transfer += item.Amount
	}
	if !summary.ledger || summary.external <= 0 || math.IsNaN(summary.external) || math.IsInf(summary.external, 0) {
		return capitalSummary{external: fallback}, nil
	}
	return summary, nil
}

func newAccountLedgerSnapshot(slot, kind string, at time.Time, overview AccountOverview) AccountLedgerSnapshot {
	local := at.In(shanghai())
	return AccountLedgerSnapshot{
		SnapshotID:                "",
		Slot:                      slot,
		ValuedAt:                  at,
		TradingDate:               local.Format("2006-01-02"),
		SnapshotType:              kind,
		Cash:                      overview.Cash,
		PositionValue:             overview.PositionValue,
		NetAssetValue:             overview.NetAssetValue,
		CumulativeExternalCapital: overview.CumulativeExternalCapital,
		NetInternalTransfer:       overview.NetInternalTransfer,
		NetProfit:                 overview.NetProfit,
		CumulativeCapitalReturn:   overview.CumulativeCapitalReturn,
		ValuationBasis:            overview.ValuationBasis,
	}
}

func accountLedgerSnapshotFromRaw(raw AccountSnapshot) AccountLedgerSnapshot {
	return AccountLedgerSnapshot{
		SnapshotID:              raw.SnapshotID,
		Slot:                    raw.Slot,
		ValuedAt:                raw.ValuedAt,
		TradingDate:             raw.TradingDate,
		SnapshotType:            raw.SnapshotType,
		Cash:                    raw.Cash,
		PositionValue:           raw.PositionValue,
		NetAssetValue:           raw.NetAssetValue,
		NetProfit:               raw.NetProfit,
		CumulativeCapitalReturn: raw.ReturnRate,
		ValuationBasis:          CapitalValuationBasisLegacy,
	}
}

func validateCapitalSummary(summary capitalSummary) error {
	if summary.external <= 0 || math.IsNaN(summary.external) || math.IsInf(summary.external, 0) {
		return errors.New("research2 external capital is invalid")
	}
	return nil
}
