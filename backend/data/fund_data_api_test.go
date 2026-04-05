package data

import (
	"go-stock/backend/db"
	"testing"
)

func TestCrawlFundBasic(t *testing.T) {
	requireIntegration(t)
	db.Init("../../data/stock.db")
	db.Dao.AutoMigrate(&FundBasic{})
	api := NewFundApi()

	//api.CrawlFundBasic("510630")
	//api.CrawlFundBasic("159688")
	//
	api.AllFund()
}

func TestCrawlFundNetUnitValue(t *testing.T) {
	requireIntegration(t)
	db.Init("../../data/stock.db")
	api := NewFundApi()
	api.CrawlFundNetUnitValue("016533")
}
