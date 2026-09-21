package migrations

import (
	"testing"
	"time"

	"go-stock/backend/research2"
)

func TestSchema30AddsNullableAllocationBaseWithoutRewritingChains(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := database.AutoMigrate(&research2.ExecutionChain{}); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrator().DropColumn(&research2.ExecutionChain{}, "AllocationBaseCash"); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 21, 10, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*3600))
	if err := database.Exec(`INSERT INTO research2_execution_chains
		(chain_id, slot, trading_date, scheduled_for, status, target_slots, filled_slots, started_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, "legacy-chain", "10:00", "2026-09-21", now, "completed", 5, 3, now, now, now).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := database.Transaction(applyResearch2AllocationBaseCash); err != nil {
			t.Fatal(err)
		}
	}
	if err := verifyMainSchema30Runtime(database); err != nil {
		t.Fatal(err)
	}
	var stored research2.ExecutionChain
	if err := database.Where("chain_id = ?", "legacy-chain").First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.AllocationBaseCash != nil || stored.Slot != "10:00" || stored.FilledSlots != 3 || !stored.CreatedAt.Equal(now) || !stored.UpdatedAt.Equal(now) {
		t.Fatalf("schema 30 rewrote legacy chain: %+v", stored)
	}
}
