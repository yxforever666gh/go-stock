package research

import (
	"context"
	"errors"
	"testing"
	"time"

	"go-stock/internal/marketquote"
	"go-stock/internal/trading"

	"gorm.io/gorm"
)

func configurePermissionFixture(t *testing.T, repo *Repository) func(bool) {
	t.Helper()
	if err := repo.DB().Exec("CREATE TABLE test_new_positions (enabled BOOLEAN NOT NULL)").Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.DB().Exec("INSERT INTO test_new_positions VALUES (true)").Error; err != nil {
		t.Fatal(err)
	}
	repo.ConfigureNewPositionsPermission(func(ctx context.Context, database *gorm.DB) error {
		var enabled bool
		if err := database.WithContext(ctx).Raw("SELECT enabled FROM test_new_positions").Scan(&enabled).Error; err != nil {
			return err
		}
		if !enabled {
			return trading.ErrNewPositionsDisabled
		}
		return nil
	})
	return func(enabled bool) {
		t.Helper()
		if err := repo.DB().Exec("UPDATE test_new_positions SET enabled = ?", enabled).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestNewPositionsPermissionGuardsAdmissionAndBuyTransaction(t *testing.T) {
	ctx := context.Background()
	repo := researchTestRepo(t)
	at := time.Date(2026, 9, 9, 10, 0, 0, 0, shanghaiLocation)
	pending := seedRecommendation(t, repo, "buy_pending", at, at, "")
	setEnabled := configurePermissionFixture(t, repo)
	if err := repo.CheckNewPositionsAllowed(ctx); err != nil {
		t.Fatal(err)
	}
	setEnabled(false)
	if !errors.Is(repo.CheckNewPositionsAllowed(ctx), trading.ErrNewPositionsDisabled) {
		t.Fatal("disabled setting not visible")
	}
	permission := repo.newPositionsPermission
	calls := 0
	repo.ConfigureNewPositionsPermission(func(ctx context.Context, tx *gorm.DB) error {
		calls++
		if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
			t.Error("permission did not receive buy transaction")
		}
		return permission(ctx, tx)
	})
	var before, after SimulatedAccount
	if err := repo.DB().First(&before, 1).Error; err != nil {
		t.Fatal(err)
	}
	quote := marketquote.Quote{Code: pending.StockCode, Name: pending.StockName, Price: 10, At: at, Market: "SH"}
	if err := repo.Buy(ctx, pending.RecommendationID, quote, at.AddDate(0, 0, 1), at); !errors.Is(err, trading.ErrNewPositionsDisabled) {
		t.Fatalf("buy err=%v", err)
	}
	newItem := Recommendation{RecommendationID: newID(), StockCode: "sz000001", Status: "buy_pending", ReservedCash: TargetCashPerTrade}
	if err := repo.CreateRecommendationWithinCapacity(ctx, &newItem, nil, nil); !errors.Is(err, trading.ErrNewPositionsDisabled) {
		t.Fatalf("admission err=%v", err)
	}
	if err := repo.DB().First(&after, 1).Error; err != nil {
		t.Fatal(err)
	}
	var trades, positions, recommendations int64
	repo.DB().Model(&SimulatedTrade{}).Count(&trades)
	repo.DB().Model(&Position{}).Count(&positions)
	repo.DB().Model(&Recommendation{}).Count(&recommendations)
	if calls != 2 || before.Cash != after.Cash || trades != 0 || positions != 0 || recommendations != 1 {
		t.Fatalf("disabled transaction leaked or retried: calls=%d cash=%f/%f trades=%d positions=%d recommendations=%d", calls, before.Cash, after.Cash, trades, positions, recommendations)
	}
}
