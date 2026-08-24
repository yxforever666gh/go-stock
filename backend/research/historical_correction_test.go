package research

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func historicalCorrectionTestService(t *testing.T) (*Service, *gorm.DB, HistoricalMissedCashCorrectionRequest) {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&Recommendation{}, &LifecycleMessage{}, &DecisionEvent{}, &SimulatedAccount{},
		&SimulatedTrade{}, &Position{}, &AccountCashFlow{}, &FundingPlan{}, &AccountValuationSnapshot{}); err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 8, 1, 9, 0, 0, 0, shanghaiLocation)
	if err := database.Create(&SimulatedAccount{ID: 1, InitialCash: LegacyInitialCash, Cash: 1924.6591, CreatedAt: created}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&AccountCashFlow{FlowID: "initial", Sequence: 0, Type: "initial_deposit", Amount: LegacyInitialCash,
		EffectiveAt: created, TradingDate: "2026-08-01", NetAssetValueAfter: LegacyInitialCash, UnitValueBefore: 1, UnitsIssued: LegacyInitialCash}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&FundingPlan{ID: 1, InitialContribution: LegacyInitialCash, TargetContribution: TargetContribution,
		DepositAmount: ScheduledDepositAmount, PlannedDeposits: ScheduledDepositCount, StartAfterTradingDate: "2026-08-19"}).Error; err != nil {
		t.Fatal(err)
	}
	signalA := time.Date(2026, 8, 17, 15, 43, 28, 143526400, shanghaiLocation)
	signalB := signalA.Add(28 * time.Millisecond)
	recommendations := []Recommendation{
		{RecommendationID: "c49ade23-12f4-4aa0-8203-b985bfd9d7e4", AnalysisRunID: "run", StockCode: "sz300308", StockName: "中际旭创", SignalAt: signalA, Status: "missed_cash"},
		{RecommendationID: "699640bc-861e-4330-8023-4182173b3e9e", AnalysisRunID: "run", StockCode: "sh688012", StockName: "中微公司", SignalAt: signalB, Status: "missed_cash"},
	}
	if err := database.Create(&recommendations).Error; err != nil {
		t.Fatal(err)
	}
	entryAt := time.Date(2026, 8, 18, 9, 31, 0, 0, shanghaiLocation)
	markAt := time.Date(2026, 8, 19, 15, 0, 0, 0, shanghaiLocation)
	request := HistoricalMissedCashCorrectionRequest{
		FundingEffectiveAt: time.Date(2026, 8, 18, 9, 20, 0, 0, shanghaiLocation), BuyTradingDate: "2026-08-18",
		FirstSellCheckAt: time.Date(2026, 8, 20, 9, 50, 0, 0, shanghaiLocation), AppliedAt: time.Date(2026, 8, 19, 18, 0, 0, 0, shanghaiLocation),
		Buys: []HistoricalBuyEvidence{
			{RecommendationID: recommendations[1].RecommendationID, EntrySource: "tencent-unadjusted-1m",
				EntryQuote: Quote{Code: "sh688012", Name: "中微公司", Market: "SH", Price: 200, At: entryAt},
				MarkQuote:  &Quote{Code: "sh688012", Name: "中微公司", Market: "SH", Price: 205, At: markAt}},
			{RecommendationID: recommendations[0].RecommendationID, EntrySource: "akshare-eastmoney-unadjusted-1m",
				EntryQuote: Quote{Code: "sz300308", Name: "中际旭创", Market: "SZ", Price: 510, At: entryAt},
				MarkQuote:  &Quote{Code: "sz300308", Name: "中际旭创", Market: "SZ", Price: 512, At: markAt}},
		},
	}
	return NewService(NewRepository(database), nil, nil, WeekdayCalendar{}), database, request
}

func TestApplyHistoricalMissedCashCorrectionIsAtomicAndIdempotent(t *testing.T) {
	service, database, request := historicalCorrectionTestService(t)
	receipt, err := service.ApplyHistoricalMissedCashCorrection(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "applied" || receipt.CompletedDeposits != 2 || receipt.RemainingDeposits != 2 || len(receipt.Buys) != 2 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	if receipt.Buys[0].StockCode != "sz300308" || receipt.Buys[1].StockCode != "sh688012" {
		t.Fatalf("buys were not ordered by signal time: %+v", receipt.Buys)
	}
	if receipt.Buys[0].Quantity%100 != 0 || receipt.Buys[1].Quantity%200 != 0 {
		t.Fatalf("market lot rules were not used: %+v", receipt.Buys)
	}
	if !receipt.Buys[0].BudgetException || receipt.Buys[0].Quantity != 100 || receipt.Buys[1].BudgetException {
		t.Fatalf("minimum-lot exception was not narrowly applied: %+v", receipt.Buys)
	}
	var flow AccountCashFlow
	if err := database.Where("sequence = ?", 1).First(&flow).Error; err != nil {
		t.Fatal(err)
	}
	if flow.TradingDate != "2026-08-18" || math.Abs(flow.UnitValueBefore-1) > 1e-9 || math.Abs(flow.UnitsIssued-100000) > 1e-6 {
		t.Fatalf("unexpected cash flow: %+v", flow)
	}
	var active, positions, trades, correctionEvents, messages int64
	_ = database.Model(&Recommendation{}).Where("status = ?", "active").Count(&active).Error
	_ = database.Model(&Position{}).Count(&positions).Error
	_ = database.Model(&SimulatedTrade{}).Count(&trades).Error
	_ = database.Model(&DecisionEvent{}).Where("decision_type = ?", historicalCorrectionDecision).Count(&correctionEvents).Error
	_ = database.Model(&LifecycleMessage{}).Where("phase = ?", "holding").Count(&messages).Error
	if active != 2 || positions != 2 || trades != 2 || correctionEvents != 2 || messages != 2 {
		t.Fatalf("correction rows missing: active=%d positions=%d trades=%d events=%d messages=%d", active, positions, trades, correctionEvents, messages)
	}
	var recommendation Recommendation
	if err := database.Where("recommendation_id = ?", request.Buys[0].RecommendationID).First(&recommendation).Error; err != nil {
		t.Fatal(err)
	}
	if recommendation.PreviousResponseID != "" || recommendation.NextCheckAt == nil || !recommendation.NextCheckAt.Equal(request.FirstSellCheckAt) {
		t.Fatalf("recommendation lifecycle was not re-anchored: %+v", recommendation)
	}
	var closeSnapshot AccountValuationSnapshot
	if err := database.Where("snapshot_id = ?", "daily-close-2026-08-19").First(&closeSnapshot).Error; err != nil {
		t.Fatal(err)
	}
	if closeSnapshot.CumulativeNetContribution != 300000 || closeSnapshot.UnitValue <= 0 {
		t.Fatalf("unexpected corrected close snapshot: %+v", closeSnapshot)
	}

	duplicate, err := service.ApplyHistoricalMissedCashCorrection(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.Status != "already_applied" || duplicate.CumulativeNetContribution != 300000 || len(duplicate.Buys) != 2 {
		t.Fatalf("unexpected idempotent receipt: %+v", duplicate)
	}
	var flowCount, tradeCount int64
	_ = database.Model(&AccountCashFlow{}).Count(&flowCount).Error
	_ = database.Model(&SimulatedTrade{}).Count(&tradeCount).Error
	if flowCount != 3 || tradeCount != 2 {
		t.Fatalf("idempotent call duplicated rows: flows=%d trades=%d", flowCount, tradeCount)
	}
}

func TestApplyHistoricalMissedCashCorrectionRollsBackAllRows(t *testing.T) {
	service, database, request := historicalCorrectionTestService(t)
	request.Buys[1].EntryQuote.Name = "错误股票"
	if _, err := service.ApplyHistoricalMissedCashCorrection(context.Background(), request); err == nil {
		t.Fatal("expected mismatched quote to fail")
	}
	var flows, positions, trades int64
	_ = database.Model(&AccountCashFlow{}).Count(&flows).Error
	_ = database.Model(&Position{}).Count(&positions).Error
	_ = database.Model(&SimulatedTrade{}).Count(&trades).Error
	if flows != 1 || positions != 0 || trades != 0 {
		t.Fatalf("transaction did not roll back: flows=%d positions=%d trades=%d", flows, positions, trades)
	}
	var account SimulatedAccount
	_ = database.First(&account, 1).Error
	if math.Abs(account.Cash-1924.6591) > 1e-6 {
		t.Fatalf("cash changed after rollback: %.6f", account.Cash)
	}
}
