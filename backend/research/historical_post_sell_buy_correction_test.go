package research

import (
	"context"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func historicalPostSellBuyTestService(t *testing.T) (*Service, *gorm.DB, HistoricalPostSellBuyCorrectionRequest) {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "research.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.AutoMigrate(&Recommendation{}, &Position{}, &SimulatedTrade{}, &DecisionEvent{}, &LifecycleMessage{}, &SimulatedAccount{}, &AccountCashFlow{}, &AccountValuationSnapshot{}); err != nil {
		t.Fatal(err)
	}
	initialCash := 154814.32202159
	if err := database.Create(&SimulatedAccount{ID: 1, InitialCash: InitialCash, Cash: initialCash}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&AccountCashFlow{FlowID: "funding", Sequence: 1, Type: "scheduled_deposit", Amount: InitialCash, EffectiveAt: historicalPostSellBuySignalAt.Add(-24 * time.Hour), TradingDate: "2026-08-20", NetAssetValueBefore: 0, NetAssetValueAfter: InitialCash, UnitValueBefore: 1, UnitsIssued: InitialCash}).Error; err != nil {
		t.Fatal(err)
	}
	// The closed 紫金矿业 recommendation records the completed 1.7.5 repair.
	if err := database.Create(&Recommendation{RecommendationID: historicalPostSellBuySellRecommendation, AnalysisRunID: "run", StockCode: "sh601899", StockName: "紫金矿业", SignalAt: historicalPostSellBuySignalAt.Add(-time.Hour), Status: "closed"}).Error; err != nil {
		t.Fatal(err)
	}
	closedAt := historicalPostSellBuySignalAt.Add(-time.Hour)
	if err := database.Create(&Position{RecommendationID: historicalPostSellBuySellRecommendation, StockCode: "sh601899", StockName: "紫金矿业", Market: "SH", Quantity: 1500, EntryAt: closedAt.Add(-time.Hour), EntryPrice: 32, Status: "closed", ExitAt: &closedAt}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&SimulatedTrade{TradeID: "historic-sell", RecommendationID: historicalPostSellBuySellRecommendation, StockCode: "sh601899", Side: "sell", TradedAt: closedAt, Quantity: 1500}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&DecisionEvent{EventID: "historic-sell-fix", RecommendationID: historicalPostSellBuySellRecommendation, DecisionType: HistoricalSellCorrectionDecision, DecidedAt: closedAt, DataStatus: "complete"}).Error; err != nil {
		t.Fatal(err)
	}
	// An unrelated open holding verifies the correction preserves existing
	// portfolio valuation and admits the corrected second exposure.
	if err := database.Create(&Recommendation{RecommendationID: "other-open", AnalysisRunID: "run", StockCode: "sh601318", StockName: "中国平安", SignalAt: closedAt, Status: "active"}).Error; err != nil {
		t.Fatal(err)
	}
	markOtherAt := time.Date(2026, 8, 21, 14, 59, 0, 0, shanghaiLocation)
	if err := database.Create(&Position{RecommendationID: "other-open", StockCode: "sh601318", StockName: "中国平安", Market: "SH", Quantity: 100, EntryAt: closedAt, EntryPrice: 50, CurrentPrice: 51, CurrentPriceAt: &markOtherAt, Status: "open"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&Recommendation{RecommendationID: historicalPostSellBuyRecommendationID, AnalysisRunID: "run", StockCode: "sz300308", StockName: "中际旭创", SignalAt: historicalPostSellBuySignalAt, Status: "missed_cash"}).Error; err != nil {
		t.Fatal(err)
	}
	quoteAt := historicalPostSellBuyQuoteAt
	if err := database.Create(&DecisionEvent{EventID: historicalPostSellBuyFailureEventID, RecommendationID: historicalPostSellBuyRecommendationID, DecisionType: "错过—资金不足", DecidedAt: historicalPostSellBuySignalAt, Reason: "insufficient cash for minimum order unit", QuotePrice: 941.41, QuoteAt: &quoteAt, DataStatus: "complete"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&AccountValuationSnapshot{SnapshotID: "daily-close-2026-08-21", SnapshotType: "daily_close", TradingDate: "2026-08-21", ValuedAt: markOtherAt, Cash: initialCash, PositionValue: 0, NetAssetValue: initialCash, CumulativeNetContribution: InitialCash, UnitValue: 1, TimeWeightedReturn: 0, ValuationStatus: "complete"}).Error; err != nil {
		t.Fatal(err)
	}
	request := HistoricalPostSellBuyCorrectionRequest{MarkQuote: Quote{Code: "sz300308", Name: "中际旭创", Market: "SZ", Price: 950, At: markOtherAt}, AppliedAt: time.Date(2026, 8, 21, 15, 10, 0, 0, shanghaiLocation)}
	return NewService(NewRepository(database), nil, nil, WeekdayCalendar{}), database, request
}

func TestApplyHistoricalPostSellBuyCorrectionIsAtomicAndIdempotent(t *testing.T) {
	service, database, request := historicalPostSellBuyTestService(t)
	receipt, err := service.ApplyHistoricalPostSellBuyCorrection(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	expected := CalculateBuyCost(941.41, 100)
	if receipt.Status != "applied" || receipt.Quantity != 100 || !receipt.TradedAt.Equal(historicalPostSellBuySignalAt) || !receipt.QuoteAt.Equal(historicalPostSellBuyQuoteAt) || !receipt.MarkAt.Equal(request.MarkQuote.At) || math.Abs(receipt.ExecutionPrice-942.35141) > 1e-8 || math.Abs(receipt.NetCashFlow-expected.NetCashFlow) > 1e-6 || math.Abs(receipt.CashAfter-60549.96812788) > 1e-5 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	var recommendation Recommendation
	var position Position
	var trade SimulatedTrade
	var snapshot AccountValuationSnapshot
	if err := database.Where("recommendation_id = ?", historicalPostSellBuyRecommendationID).First(&recommendation).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Where("recommendation_id = ?", historicalPostSellBuyRecommendationID).First(&position).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Where("recommendation_id = ? AND side = ?", historicalPostSellBuyRecommendationID, "buy").First(&trade).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Where("snapshot_id = ?", "daily-close-2026-08-21").First(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	if recommendation.Status != "active" || recommendation.NextCheckAt == nil || !recommendation.NextCheckAt.Equal(historicalPostSellBuyNextSell) || position.Status != "open" || position.Quantity != 100 || !position.EntryAt.Equal(historicalPostSellBuySignalAt) || position.CurrentPrice != 950 || !trade.TradedAt.Equal(historicalPostSellBuySignalAt) {
		t.Fatalf("unexpected persisted correction recommendation=%+v position=%+v trade=%+v", recommendation, position, trade)
	}
	if math.Abs(snapshot.Cash-receipt.CashAfter) > 1e-6 || math.Abs(snapshot.NetAssetValue-(snapshot.Cash+snapshot.PositionValue)) > 1e-6 || snapshot.ValuationStatus != "complete" {
		t.Fatalf("snapshot was not rebuilt: %+v", snapshot)
	}
	duplicate, err := service.ApplyHistoricalPostSellBuyCorrection(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.Status != "already_applied" || duplicate.Quantity != 100 || math.Abs(duplicate.CashAfter-receipt.CashAfter) > 1e-6 {
		t.Fatalf("unexpected duplicate: %+v", duplicate)
	}
	var buys, positions, events, messages int64
	_ = database.Model(&SimulatedTrade{}).Where("recommendation_id = ? AND side = ?", historicalPostSellBuyRecommendationID, "buy").Count(&buys).Error
	_ = database.Model(&Position{}).Where("recommendation_id = ?", historicalPostSellBuyRecommendationID).Count(&positions).Error
	_ = database.Model(&DecisionEvent{}).Where("recommendation_id = ? AND decision_type = ?", historicalPostSellBuyRecommendationID, HistoricalPostSellBuyCorrectionDecision).Count(&events).Error
	_ = database.Model(&LifecycleMessage{}).Where("recommendation_id = ?", historicalPostSellBuyRecommendationID).Count(&messages).Error
	if buys != 1 || positions != 1 || events != 1 || messages != 1 {
		t.Fatalf("duplicate correction wrote rows buys=%d positions=%d events=%d messages=%d", buys, positions, events, messages)
	}
}

func TestApplyHistoricalPostSellBuyCorrectionRejectsInvalidMarkWithoutWrites(t *testing.T) {
	service, database, request := historicalPostSellBuyTestService(t)
	request.MarkQuote.Code = "sh600000"
	if _, err := service.ApplyHistoricalPostSellBuyCorrection(context.Background(), request); err == nil {
		t.Fatal("expected invalid mark to fail")
	}
	var buys, positions, events int64
	_ = database.Model(&SimulatedTrade{}).Where("recommendation_id = ?", historicalPostSellBuyRecommendationID).Count(&buys).Error
	_ = database.Model(&Position{}).Where("recommendation_id = ?", historicalPostSellBuyRecommendationID).Count(&positions).Error
	_ = database.Model(&DecisionEvent{}).Where("recommendation_id = ? AND decision_type = ?", historicalPostSellBuyRecommendationID, HistoricalPostSellBuyCorrectionDecision).Count(&events).Error
	if buys != 0 || positions != 0 || events != 0 {
		t.Fatalf("invalid correction wrote rows buys=%d positions=%d events=%d", buys, positions, events)
	}
}

func TestApplyHistoricalPostSellBuyCorrectionRequiresExistingCloseSnapshot(t *testing.T) {
	service, database, request := historicalPostSellBuyTestService(t)
	if err := database.Where("snapshot_id = ?", "daily-close-2026-08-21").Delete(&AccountValuationSnapshot{}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplyHistoricalPostSellBuyCorrection(context.Background(), request); err == nil {
		t.Fatal("expected missing snapshot to fail")
	}
	var buys int64
	_ = database.Model(&SimulatedTrade{}).Where("recommendation_id = ?", historicalPostSellBuyRecommendationID).Count(&buys).Error
	if buys != 0 {
		t.Fatalf("missing snapshot correction wrote %d buys", buys)
	}
}

func TestApplyHistoricalPostSellBuyCorrectionRequiresCompletedXDSellRepair(t *testing.T) {
	service, database, request := historicalPostSellBuyTestService(t)
	if err := database.Where("recommendation_id = ? AND decision_type = ?", historicalPostSellBuySellRecommendation, HistoricalSellCorrectionDecision).Delete(&DecisionEvent{}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplyHistoricalPostSellBuyCorrection(context.Background(), request); err == nil {
		t.Fatal("expected missing 1.7.5 prerequisite to fail")
	}
	var buys, positions int64
	_ = database.Model(&SimulatedTrade{}).Where("recommendation_id = ?", historicalPostSellBuyRecommendationID).Count(&buys).Error
	_ = database.Model(&Position{}).Where("recommendation_id = ?", historicalPostSellBuyRecommendationID).Count(&positions).Error
	if buys != 0 || positions != 0 {
		t.Fatalf("missing prerequisite wrote rows buys=%d positions=%d", buys, positions)
	}
}
