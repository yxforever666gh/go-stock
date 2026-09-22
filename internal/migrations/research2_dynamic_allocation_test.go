package migrations

import (
	"testing"
	"time"

	"go-stock/backend/research2"
)

func TestSchema34AddsReplayAuditWithoutRewritingExecutionHistory(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := database.AutoMigrate(&research2.ExecutionChain{}, &research2.Recommendation{}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 22, 10, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*3600))
	base := 20000.0
	chain := research2.ExecutionChain{ChainID: "legacy-chain", Slot: "10:00", TradingDate: "2026-09-22", ScheduledFor: now, Status: "completed", TargetSlots: 5, FilledSlots: 4, StartedAt: now, AllocationBaseCash: &base}
	item := research2.Recommendation{RecommendationID: "legacy-rec", AnalysisRunID: "run", Slot: "10:00", StockCode: "sh600001", StockName: "legacy", SignalAt: now, TargetBuyAt: now, Status: "closed", Quantity: 100, NetPnL: 88}
	if err := database.Create(&chain).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Migrator().DropColumn(&research2.ExecutionChain{}, "AllocationPolicy"); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"HistoricalReplayID", "HistoricalSellBlocked"} {
		if err := database.Migrator().DropColumn(&research2.Recommendation{}, column); err != nil {
			t.Fatal(err)
		}
	}
	for iteration := 0; iteration < 2; iteration++ {
		if err := database.Transaction(applyResearch2DynamicAllocation); err != nil {
			t.Fatal(err)
		}
	}
	if err := verifyMainSchema34Runtime(database); err != nil {
		t.Fatal(err)
	}
	var storedChain research2.ExecutionChain
	if err := database.Where("chain_id = ?", chain.ChainID).First(&storedChain).Error; err != nil {
		t.Fatal(err)
	}
	var storedItem research2.Recommendation
	if err := database.Where("recommendation_id = ?", item.RecommendationID).First(&storedItem).Error; err != nil {
		t.Fatal(err)
	}
	if storedChain.AllocationPolicy != research2.AllocationPolicyLegacyRecorded || storedChain.TargetSlots != 5 || storedChain.FilledSlots != 4 || storedChain.AllocationBaseCash == nil || *storedChain.AllocationBaseCash != base {
		t.Fatalf("legacy chain changed: %+v", storedChain)
	}
	if storedItem.Status != "closed" || storedItem.Quantity != 100 || storedItem.NetPnL != 88 || storedItem.HistoricalReplayID != "" || storedItem.HistoricalSellBlocked {
		t.Fatalf("legacy recommendation changed: %+v", storedItem)
	}
}
