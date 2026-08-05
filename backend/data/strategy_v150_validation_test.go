package data

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestCalculateV150ForwardValidationRemainsPendingWithoutSamples(t *testing.T) {
	result := calculateV150ForwardValidation(nil)
	if result.Status != "forward_validation" || result.Label != "前向验证中" {
		t.Fatalf("unexpected empty validation status: %+v", result)
	}
	if len(result.UnmetConditions) != 8 {
		t.Fatalf("expected every validation gate to remain unmet, got %+v", result.UnmetConditions)
	}
}

func TestCalculateV150ForwardValidationUsesAllFrozenThresholds(t *testing.T) {
	original := db.Dao
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "v150-forward-validation.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Dao = original
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close validation database: %v", err)
		}
	})
	db.Dao = database
	if err := database.AutoMigrate(&models.AiRecommendDailyBar{}); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 1, 5, 0, 0, 0, 0, cnLocation())
	bars := make([]models.AiRecommendDailyBar, 0, 60)
	for day := 0; day < 60; day++ {
		bars = append(bars, models.AiRecommendDailyBar{StockCode: "510300.SH", TradeDate: base.AddDate(0, 0, day), Close: 4})
	}
	if err := database.Create(&bars).Error; err != nil {
		t.Fatal(err)
	}

	items := make([]models.AiRecommendStocksYieldItem, 0, 100)
	for index := 0; index < 100; index++ {
		dayOffset := (index % 40) * 59 / 39
		sell := 10.2
		items = append(items, models.AiRecommendStocksYieldItem{
			SummaryVersion:            "1.5.0",
			StockCode:                 "000001.SZ",
			ExecutionState:            "conditional",
			BacktestEligibility:       recommendBacktestEligible,
			RecommendTime:             base.AddDate(0, 0, dayOffset).Add(10 * time.Hour).Format("2006-01-02 15:04:05"),
			ActivationStatus:          "activated",
			BuyAmount:                 10,
			SellAmount:                &sell,
			ExcessYieldRate:           0.60,
			BenchmarkYieldRateText:    "+1.00%",
			ExcessYieldRateText:       "+0.60%",
			YieldRate:                 1.00,
			YieldRateText:             "+1.00%",
			V150LedgerAccountingReady: true,
			V150LedgerClosed:          true,
			V150LedgerEntryCash:       9000,
			V150LedgerNetPnL:          90,
		})
	}
	result := calculateV150ForwardValidation(items)
	if result.Status != "validated" || len(result.UnmetConditions) != 0 {
		t.Fatalf("expected all frozen validation thresholds to pass: %+v", result)
	}
	if result.TradingDayCount != 60 || result.ClosedTradeCount != 100 || result.ComparableTradeCount != 100 || result.RecommendationDayCount != 40 {
		t.Fatalf("sample counts changed: %+v", result)
	}
	if result.NetMeanPct < 0.75 || result.NetExcessMeanPct < 0.50 || result.ProfitFactor < 1.25 || result.DailyLowerBound90Pct <= 0 {
		t.Fatalf("metric thresholds changed: %+v", result)
	}
}

func TestCalculateV150ForwardValidationExcludesUncomparableBenchmarkTrades(t *testing.T) {
	sell := 10.2
	items := []models.AiRecommendStocksYieldItem{
		{
			SummaryVersion: "1.5.0", ExecutionState: "conditional", BacktestEligibility: recommendBacktestEligible,
			RecommendTime: "2026-08-04 10:00:00", ActivationStatus: "activated", BuyAmount: 10, SellAmount: &sell,
			ExcessYieldRate: 99, YieldRate: 1, YieldRateText: "+1.00%",
			V150LedgerAccountingReady: true, V150LedgerClosed: true, V150LedgerEntryCash: 9000, V150LedgerNetPnL: 90,
		},
		{
			SummaryVersion: "1.5.0", ExecutionState: "conditional", BacktestEligibility: recommendBacktestEligible,
			RecommendTime: "2026-08-05 10:00:00", ActivationStatus: "activated", BuyAmount: 10, SellAmount: &sell,
			BenchmarkYieldRateText: "+0.10%", ExcessYieldRate: 0.6, ExcessYieldRateText: "+0.60%",
			YieldRate: 1, YieldRateText: "+1.00%",
			V150LedgerAccountingReady: true, V150LedgerClosed: true, V150LedgerEntryCash: 9000, V150LedgerNetPnL: 90,
		},
	}

	result := calculateV150ForwardValidation(items)
	if result.ClosedTradeCount != 2 || result.ComparableTradeCount != 1 {
		t.Fatalf("benchmark comparability counts = closed:%d comparable:%d", result.ClosedTradeCount, result.ComparableTradeCount)
	}
	if result.NetExcessMeanPct != 0.6 {
		t.Fatalf("uncomparable default excess entered validation mean: %.4f", result.NetExcessMeanPct)
	}
	if !strings.Contains(strings.Join(result.UnmetConditions, ";"), "基准可比平仓 1/100") {
		t.Fatalf("missing comparable-sample gate: %+v", result.UnmetConditions)
	}
}

func TestCalculateV150ForwardValidationProfitFactorUsesLedgerNetPnL(t *testing.T) {
	items := []models.AiRecommendStocksYieldItem{
		{SummaryVersion: "1.5.0", ExecutionState: "conditional", BacktestEligibility: recommendBacktestEligible,
			RecommendTime: "2026-08-04 10:00:00", ActivationStatus: "activated", YieldRate: 2,
			V150LedgerAccountingReady: true, V150LedgerClosed: true, V150LedgerEntryCash: 9000, V150LedgerNetPnL: 200},
		{SummaryVersion: "1.5.0", ExecutionState: "conditional", BacktestEligibility: recommendBacktestEligible,
			RecommendTime: "2026-08-05 10:00:00", ActivationStatus: "activated", YieldRate: -1,
			V150LedgerAccountingReady: true, V150LedgerClosed: true, V150LedgerEntryCash: 9900, V150LedgerNetPnL: -100},
	}
	result := calculateV150ForwardValidation(items)
	if result.ClosedTradeCount != 2 || result.ProfitFactor != 2 || result.NetMeanPct != .5 {
		t.Fatalf("validation did not use ledger return/PnL: %+v", result)
	}
}

func TestOneSidedLowerBound90RequiresIndependentDays(t *testing.T) {
	if got := oneSidedLowerBound90([]float64{5}); got != 0 {
		t.Fatalf("single day must not claim a positive confidence bound: %.4f", got)
	}
	if got := oneSidedLowerBound90([]float64{1, 1, 1, 1}); got <= 0 {
		t.Fatalf("stable positive daily groups should have positive lower bound: %.4f", got)
	}
}
