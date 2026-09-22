package migrations

import (
	"testing"
	"time"

	"go-stock/backend/research2"
)

func TestSchema33AddsDerivedPerformanceWithoutRewritingLegacyMetrics(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := database.AutoMigrate(&research2.Recommendation{}); err != nil {
		t.Fatal(err)
	}
	legacyTrue, legacyFalse := true, false
	now := time.Date(2026, 9, 22, 10, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*3600))
	item := research2.Recommendation{RecommendationID: "legacy", AnalysisRunID: "run", Slot: "09:50", StockCode: "sh600001", StockName: "legacy", SignalAt: now, TargetBuyAt: now, Status: "closed", HitFiveBeforeSell: &legacyTrue, HitLimitUpFullDay: &legacyFalse, HitMinusThree: &legacyTrue, MetricsFinalized: true}
	if err := database.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"BuyDayLimitOutcome", "BuyDayLimitStatus", "BuyDayLimitEvaluatedAt", "BuyDayLimitAttemptCount", "BuyDayLimitSourceJSON", "BuyDayLimitFailureReason"} {
		if err := database.Migrator().DropColumn(&research2.Recommendation{}, column); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Transaction(applyResearch2PortfolioPerformance); err != nil {
		t.Fatal(err)
	}
	if err := verifyMainSchema33Runtime(database); err != nil {
		t.Fatal(err)
	}
	var stored research2.Recommendation
	if err := database.Where("recommendation_id = ?", item.RecommendationID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if !stored.MetricsFinalized || stored.HitFiveBeforeSell == nil || !*stored.HitFiveBeforeSell || stored.HitLimitUpFullDay == nil || *stored.HitLimitUpFullDay || stored.HitMinusThree == nil || !*stored.HitMinusThree || stored.BuyDayLimitStatus != research2.LimitOutcomePending {
		t.Fatalf("legacy audit data changed: %+v", stored)
	}
}
