package data

import (
	"go-stock/backend/db"
	"testing"
)

func TestCrawlFundBasic(t *testing.T) {
	requireIntegration(t)
	initDatabaseForTest(t, "../../data/stock.db")
	if err := db.Dao.AutoMigrate(&FundBasic{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}
	api := NewFundApi()

	//api.CrawlFundBasic("510630")
	//api.CrawlFundBasic("159688")
	//
	api.AllFund()
}

func TestCrawlFundNetUnitValue(t *testing.T) {
	requireIntegration(t)
	initDatabaseForTest(t, "../../data/stock.db")
	api := NewFundApi()
	api.CrawlFundNetUnitValue("016533")
}
