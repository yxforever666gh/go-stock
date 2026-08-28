package data

import (
	"path/filepath"
	"testing"

	"go-stock/backend/db"
	"go-stock/backend/models"
)

func TestETFWatchlistIsIndependentAndIdempotent(t *testing.T) {
	initDatabaseForTest(t, filepath.Join(t.TempDir(), "stock.db"))
	api := NewFundApi()

	var followedFundsBefore, researchTradesBefore, research2TradesBefore int64
	if err := db.Dao.Table("followed_fund").Count(&followedFundsBefore).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Dao.Table("research_v160_simulated_trades").Count(&researchTradesBefore).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Dao.Table("research2_trades").Count(&research2TradesBefore).Error; err != nil {
		t.Fatal(err)
	}

	if err := api.FollowETF(models.ETFWatchlistItem{Code: " 510300 ", Name: "沪深300ETF", Market: "sh", Category: "broad"}); err != nil {
		t.Fatal(err)
	}
	if err := api.FollowETF(models.ETFWatchlistItem{Code: "510300", Name: "沪深300ETF（更新）", Market: "SH", Category: "broad"}); err != nil {
		t.Fatalf("idempotent follow: %v", err)
	}
	items, err := api.GetFollowedETFs()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Code != "sh510300" || items[0].Name != "沪深300ETF（更新）" {
		t.Fatalf("watchlist items=%+v", items)
	}

	var followedFundsAfter, researchTradesAfter, research2TradesAfter int64
	_ = db.Dao.Table("followed_fund").Count(&followedFundsAfter).Error
	_ = db.Dao.Table("research_v160_simulated_trades").Count(&researchTradesAfter).Error
	_ = db.Dao.Table("research2_trades").Count(&research2TradesAfter).Error
	if followedFundsAfter != followedFundsBefore || researchTradesAfter != researchTradesBefore || research2TradesAfter != research2TradesBefore {
		t.Fatalf("ETF watchlist leaked into fund/research trades: fund %d->%d r1 %d->%d r2 %d->%d", followedFundsBefore, followedFundsAfter, researchTradesBefore, researchTradesAfter, research2TradesBefore, research2TradesAfter)
	}

	removed, err := api.UnFollowETF(" 510300 ")
	if err != nil || !removed {
		t.Fatalf("unfollow removed=%v err=%v", removed, err)
	}
	removed, err = api.UnFollowETF("510300")
	if err != nil || removed {
		t.Fatalf("second unfollow removed=%v err=%v", removed, err)
	}
}
