package data

import (
	log "go-stock/backend/logger"
	"testing"
)

func TestGetTopNewsList(t *testing.T) {
	requireIntegration(t)
	news := GetTopNewsList(30)
	t.Log(news)
}

func TestSearchGuShiTongStockInfo(t *testing.T) {
	requireIntegration(t)
	initDatabaseForTest(t, "../../data/stock.db")
	//SearchGuShiTongStockInfo("hk01810", 60)
	msgs := SearchGuShiTongStockInfo("sh600745", 60)
	for _, msg := range *msgs {
		log.SugaredLogger.Infof("%s", msg)
	}
	//SearchGuShiTongStockInfo("gb_goog", 60)

}

func TestGetZSInfo(t *testing.T) {
	requireIntegration(t)
	initDatabaseForTest(t, "../../data/stock.db")
	GetZSInfo("中证银行", "sz399986", 5)
	GetZSInfo("上海贝岭", "sh600171", 5)
}
