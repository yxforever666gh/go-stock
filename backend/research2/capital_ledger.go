package research2

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
		return capitalSummary{}, errors.New("research2 slot has an incomplete capital ledger")
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
		ReturnRate:                overview.CumulativeCapitalReturn,
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
		ReturnRate:              raw.ReturnRate,
		ValuationBasis:          CapitalValuationBasisLegacy,
	}
}

func validateCapitalSummary(summary capitalSummary) error {
	if summary.external <= 0 || math.IsNaN(summary.external) || math.IsInf(summary.external, 0) {
		return errors.New("research2 external capital is invalid")
	}
	return nil
}

// Write with the trade/account transaction so a crash cannot omit a cash-flow
// event from the performance curve. Old schema fixtures keep their raw path.
func saveCapitalTradeSnapshot(tx *gorm.DB, trade Trade) error {
	if !research2CapitalLedgerAvailable(tx) {
		return nil
	}
	overview, err := NewRepository(tx).WithSlot(trade.Slot).Overview(tx.Statement.Context)
	if err != nil {
		return err
	}
	point := newAccountLedgerSnapshot(trade.Slot, "trade", trade.TradedAt, overview)
	point.SnapshotID = "trade-" + trade.TradeID
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&point).Error
}

// Unitize only for drawdown: deposits/transfers change assets but not trading
// performance. The displayed account return remains profit/external capital.
func capitalEventDrawdown(curve []AccountLedgerSnapshot) float64 {
	wealth, peak, drawdown := 1.0, 1.0, 0.0
	previousNAV, previousFunding := 0.0, 0.0
	for _, point := range curve {
		funding := point.CumulativeExternalCapital + point.NetInternalTransfer
		if previousNAV > 0 {
			wealth *= math.Max(0, (point.NetAssetValue-(funding-previousFunding))/previousNAV)
		}
		peak = math.Max(peak, wealth)
		if peak > 0 {
			drawdown = math.Max(drawdown, (peak-wealth)/peak)
		}
		previousNAV, previousFunding = point.NetAssetValue, funding
	}
	return drawdown
}
