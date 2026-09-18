package migrations

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"go-stock/backend/research"
	"go-stock/backend/research2"
	"go-stock/backend/researchconfig"
	"go-stock/internal/trading"
)

func TestSchema29FreezesAndLiquidatesResearch1WithoutChangingResearch2(t *testing.T) {
	db := openMigrationTestDB(t)
	if err := migrate(db, "main", mainMigrations[:28], 28); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"frozen", "frozen_at", "frozen_reason"} {
		if err := db.Exec("ALTER TABLE research_v160_simulated_accounts DROP COLUMN " + field).Error; err != nil {
			t.Fatal(err)
		}
	}
	at := time.Date(2026, 9, 18, 10, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*3600))
	if err := db.Create(&research.AnalysisRun{RunID: "before-freeze", Status: "success", StartedAt: at, ScheduledFor: at}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&research.Recommendation{RecommendationID: "liquidate", AnalysisRunID: "before-freeze", StockCode: "sh600000", StockName: "fixture", SignalAt: at, Status: "active", ActivatedAt: &at, ActivationPrice: 10, Quantity: 100}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&research.Position{RecommendationID: "liquidate", StockCode: "sh600000", StockName: "fixture", Status: "open", EntryAt: at, EntryPrice: 10, Quantity: 100, CurrentPrice: 11}).Error; err != nil {
		t.Fatal(err)
	}
	var beforeAccount research.SimulatedAccount
	if err := db.First(&beforeAccount, 1).Error; err != nil {
		t.Fatal(err)
	}
	var otherBefore, otherAfter []research2.Account
	if err := db.Order("id").Find(&otherBefore).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(applyResearch1Freeze); err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(applyResearch1Freeze); err != nil {
		t.Fatal(err)
	}
	var account research.SimulatedAccount
	if err := db.First(&account, 1).Error; err != nil {
		t.Fatal(err)
	}
	if !account.Frozen || account.FrozenAt == nil || math.Abs(account.Cash-beforeAccount.Cash-trading.CalculateSellCost(11, 100).NetCashFlow) > 1e-6 {
		t.Fatal(account)
	}
	var sells int64
	db.Model(&research.SimulatedTrade{}).Where("side = ?", "sell").Count(&sells)
	if sells != 1 {
		t.Fatal(sells)
	}
	snapshot, err := researchconfig.New(db).Load(context.Background(), researchconfig.Research1)
	if err != nil || snapshot.Settings.AICapitalDeploymentEnabled {
		t.Fatal(err)
	}
	if err := db.Order("id").Find(&otherAfter).Error; err != nil {
		t.Fatal(err)
	}
	beforeJSON, _ := json.Marshal(otherBefore)
	afterJSON, _ := json.Marshal(otherAfter)
	if string(beforeJSON) != string(afterJSON) {
		t.Fatal("research2 accounts changed during research1 freeze")
	}
}
