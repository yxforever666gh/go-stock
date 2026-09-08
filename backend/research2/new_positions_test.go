package research2

import (
	"context"
	"errors"
	"testing"
	"time"

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

func TestNewPositionsPermissionGuardsBuyTransaction(t *testing.T) {
	ctx := context.Background()
	repo := research2TestRepository(t)
	at := time.Date(2026, 9, 9, 10, 0, 0, 0, shanghai())
	_, run := createChainRun(t, repo, at)
	item := Recommendation{RecommendationID: "permission-buy", AnalysisRunID: run.RunID, StockCode: "sh600000", Status: "buy_pending", SignalAt: at, TargetBuyAt: at}
	if err := repo.CreateRecommendations(ctx, []Recommendation{item}); err != nil {
		t.Fatal(err)
	}
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
	err := repo.RecordBuy(ctx, item.RecommendationID, Trade{TradeID: "disabled-buy", RecommendationID: item.RecommendationID, Side: "buy", TradedAt: at, NetCashFlow: -1005, Quantity: 100}, at.AddDate(0, 0, 1))
	if !errors.Is(err, trading.ErrNewPositionsDisabled) {
		t.Fatalf("buy err=%v", err)
	}
	var account Account
	if err := repo.DB().First(&account, 1).Error; err != nil {
		t.Fatal(err)
	}
	var trades int64
	if err := repo.DB().Model(&Trade{}).Count(&trades).Error; err != nil {
		t.Fatal(err)
	}
	if calls != 1 || account.Cash != InitialCash || trades != 0 {
		t.Fatalf("disabled transaction leaked or retried: calls=%d cash=%f trades=%d", calls, account.Cash, trades)
	}
}
