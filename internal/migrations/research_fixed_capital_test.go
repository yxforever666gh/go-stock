package migrations

import (
	"math"
	"strings"
	"testing"
	"time"

	"go-stock/backend/models"
	"go-stock/backend/research"

	"gorm.io/gorm"
)

func prepareFixedCapitalSchema(t *testing.T) *gorm.DB {
	t.Helper()
	database := openMigrationTestDB(t)
	if err := database.AutoMigrate(&research.AnalysisRun{}, &research.Recommendation{}, &research.LifecycleMessage{},
		&research.DecisionEvent{}, &research.SimulatedAccount{}, &research.SimulatedTrade{}, &research.Position{},
		&research.AccountCashFlow{}, &research.FundingPlan{}, &research.AccountValuationSnapshot{}, &models.Settings{}); err != nil {
		t.Fatal(err)
	}
	return database
}

func TestSchema12RebasesFixedCapitalAndRestoresApprovedHistoricalBuy(t *testing.T) {
	database := prepareFixedCapitalSchema(t)
	createdAt := time.Date(2026, 8, 14, 9, 30, 0, 0, fixedCapitalTradeAt.Location())
	if err := database.Create(&research.SimulatedAccount{ID: 1, InitialCash: research.LegacyInitialCash, Cash: 146049.00376762002, CreatedAt: createdAt}).Error; err != nil {
		t.Fatal(err)
	}
	flows := []research.AccountCashFlow{
		{FlowID: "initial", Sequence: 0, Type: "initial_deposit", Amount: 100000, EffectiveAt: createdAt, TradingDate: "2026-08-14", NetAssetValueAfter: 100000, UnitValueBefore: 1, UnitsIssued: 100000},
		{FlowID: "scheduled", Sequence: 1, Type: "scheduled_deposit", Amount: 100000, EffectiveAt: time.Date(2026, 8, 20, 9, 20, 0, 0, fixedCapitalTradeAt.Location()), TradingDate: "2026-08-20", NetAssetValueBefore: 100000, NetAssetValueAfter: 200000, UnitValueBefore: 1, UnitsIssued: 100000},
	}
	if err := database.Create(&flows).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&research.FundingPlan{ID: 1, InitialContribution: 100000, TargetContribution: 500000, DepositAmount: 100000, PlannedDeposits: 4, CompletedDeposits: 1, StartAfterTradingDate: "2026-08-19", LastDepositTradingDate: "2026-08-20"}).Error; err != nil {
		t.Fatal(err)
	}
	snapshots := []research.AccountValuationSnapshot{
		{SnapshotID: "initial-deposit-baseline", SnapshotType: "initial_deposit", TradingDate: "2026-08-14", ValuedAt: createdAt, Cash: 100000, NetAssetValue: 100000, CumulativeNetContribution: 100000, UnitValue: 1, ValuationStatus: "baseline"},
		{SnapshotID: "pre-deposit-2026-08-20", SnapshotType: "pre_deposit", TradingDate: "2026-08-20", ValuedAt: flows[1].EffectiveAt, Cash: 100000, NetAssetValue: 100000, CumulativeNetContribution: 100000, UnitValue: 1, ValuationStatus: "complete"},
		{SnapshotID: "post-deposit-2026-08-20", SnapshotType: "post_deposit", TradingDate: "2026-08-20", ValuedAt: flows[1].EffectiveAt, Cash: 200000, NetAssetValue: 200000, CumulativeNetContribution: 200000, UnitValue: 1, ValuationStatus: "complete"},
		{SnapshotID: "daily-close-2026-08-24", SnapshotType: "daily_close", TradingDate: "2026-08-24", ValuedAt: fixedCapitalCloseAt, Cash: 146049.00376762002, PositionValue: 100000, NetAssetValue: 246049.00376762002, CumulativeNetContribution: 200000, UnitValue: 1.2302450188381, TimeWeightedReturn: 0.2302450188381, ValuationStatus: "complete"},
	}
	if err := database.Create(&snapshots).Error; err != nil {
		t.Fatal(err)
	}
	markAt := time.Date(2026, 8, 24, 16, 14, 36, 0, fixedCapitalTradeAt.Location())
	if err := database.Create(&research.Position{RecommendationID: "existing-ping-an", StockCode: fixedCapitalStockCode, StockName: fixedCapitalStockName, Market: "SH", Quantity: 900, EntryAt: time.Date(2026, 8, 21, 11, 22, 17, 0, fixedCapitalTradeAt.Location()), EntryPrice: 53.5535, CurrentPrice: 54.92, CurrentPriceAt: &markAt, Status: "open"}).Error; err != nil {
		t.Fatal(err)
	}
	finalReport := "综合判断推荐中国平安。\n\n| 股票名称 | 股票代码 | AI分析摘要 | 主要风险 | 来源编号 |\n|---|---|---|---|---|"
	if err := database.Create(&research.AnalysisRun{RunID: fixedCapitalAnalysisRunID, ScheduledFor: time.Date(2026, 8, 24, 12, 3, 20, 874997100, fixedCapitalTradeAt.Location()), StartedAt: time.Date(2026, 8, 24, 12, 3, 20, 874997100, fixedCapitalTradeAt.Location()), CompletedAt: &fixedCapitalSignalAt, Status: "no_recommendation", ModelName: "gpt-5.6-sol", FinalReport: finalReport, ModelAttemptLogJSON: "[]"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&models.Settings{AIReviewStartTime: "09:50", AIReviewIntervalMinutes: 15}).Error; err != nil {
		t.Fatal(err)
	}
	if err := applyResearchFixedCapitalAndHistoricalBuy(database); err != nil {
		t.Fatal(err)
	}
	if err := applyResearchFixedCapitalAndHistoricalBuy(database); err != nil {
		t.Fatalf("schema 12 must be idempotent: %v", err)
	}
	var account research.SimulatedAccount
	if err := database.First(&account, 1).Error; err != nil {
		t.Fatal(err)
	}
	if math.Abs(account.Cash-390806.71396492003) > 1e-6 || account.InitialCash != 500000 {
		t.Fatalf("account=%+v", account)
	}
	var openPingAn, scheduledFlows, obsoleteSnapshots int64
	_ = database.Model(&research.Position{}).Where("stock_code = ? AND status = ?", fixedCapitalStockCode, "open").Count(&openPingAn).Error
	_ = database.Model(&research.AccountCashFlow{}).Where("type = ?", "scheduled_deposit").Count(&scheduledFlows).Error
	_ = database.Model(&research.AccountValuationSnapshot{}).Where("snapshot_type IN ?", []string{"pre_deposit", "post_deposit"}).Count(&obsoleteSnapshots).Error
	if openPingAn != 2 || scheduledFlows != 0 || obsoleteSnapshots != 0 {
		t.Fatalf("openPingAn=%d scheduledFlows=%d obsoleteSnapshots=%d", openPingAn, scheduledFlows, obsoleteSnapshots)
	}
	var recommendation research.Recommendation
	if err := database.Where("recommendation_id = ?", fixedCapitalID("china-ping-an-recommendation")).First(&recommendation).Error; err != nil {
		t.Fatal(err)
	}
	if recommendation.NextCheckAt == nil || recommendation.NextCheckAt.Format("2006-01-02 15:04") != "2026-08-25 09:50" {
		t.Fatalf("next check=%v", recommendation.NextCheckAt)
	}
	var run research.AnalysisRun
	if err := database.Where("run_id = ?", fixedCapitalAnalysisRunID).First(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != "success" || run.RecommendationCount != 1 || !strings.Contains(run.FinalReport, "| 中国平安 | sh601318 |") {
		t.Fatalf("run=%+v", run)
	}
}

func TestSchema12FreshAccountWithoutHistoricalRunOnlyFixesCapital(t *testing.T) {
	database := prepareFixedCapitalSchema(t)
	createdAt := time.Date(2026, 8, 24, 9, 0, 0, 0, fixedCapitalTradeAt.Location())
	if err := database.Create(&research.SimulatedAccount{ID: 1, InitialCash: research.LegacyInitialCash, Cash: research.LegacyInitialCash, CreatedAt: createdAt}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&research.AccountCashFlow{FlowID: "initial", Sequence: 0, Type: "initial_deposit", Amount: research.LegacyInitialCash, EffectiveAt: createdAt, TradingDate: "2026-08-24", NetAssetValueAfter: research.LegacyInitialCash, UnitValueBefore: 1, UnitsIssued: research.LegacyInitialCash}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&research.FundingPlan{ID: 1, InitialContribution: research.LegacyInitialCash, TargetContribution: research.InitialCash, DepositAmount: 100000, PlannedDeposits: 4}).Error; err != nil {
		t.Fatal(err)
	}
	if err := applyResearchFixedCapitalAndHistoricalBuy(database); err != nil {
		t.Fatal(err)
	}
	var account research.SimulatedAccount
	_ = database.First(&account, 1).Error
	if account.InitialCash != research.InitialCash || account.Cash != research.InitialCash {
		t.Fatalf("account=%+v", account)
	}
	var recommendations int64
	_ = database.Model(&research.Recommendation{}).Count(&recommendations).Error
	if recommendations != 0 {
		t.Fatalf("unexpected recommendations=%d", recommendations)
	}
}
