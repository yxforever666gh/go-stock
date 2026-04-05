package service

import (
	"go-stock/backend/data"
	"go-stock/backend/db"
)

type StockService struct{}

func NewStockService() StockService {
	return StockService{}
}

func (s StockService) Follow(stockCode string) string {
	return data.NewStockDataApi().Follow(stockCode)
}

func (s StockService) UnFollow(stockCode string) string {
	return data.NewStockDataApi().UnFollow(stockCode)
}

func (s StockService) GetFollowList(groupID int) *[]data.FollowedStock {
	return data.NewStockDataApi().GetFollowList(groupID)
}

func (s StockService) GetStockList(key string) []data.StockBasic {
	return data.NewStockDataApi().GetStockList(key)
}

func (s StockService) SetCostPriceAndVolume(stockCode string, price float64, volume int64) string {
	return data.NewStockDataApi().SetCostPriceAndVolume(price, volume, stockCode)
}

func (s StockService) SetAlarmChangePercent(val, alarmPrice float64, stockCode string) string {
	return data.NewStockDataApi().SetAlarmChangePercent(val, alarmPrice, stockCode)
}

func (s StockService) SetStockSort(sort int64, stockCode string) {
	data.NewStockDataApi().SetStockSort(sort, stockCode)
}

func (s StockService) SetStockAICron(cronText, stockCode string) {
	data.NewStockDataApi().SetStockAICron(cronText, stockCode)
}

func (s StockService) GetFollowedStockByStockCode(stockCode string) *data.FollowedStock {
	followed := data.NewStockDataApi().GetFollowedStockByStockCode(stockCode)
	return &followed
}

func (s StockService) GetAllFollowedStocks() []data.FollowedStock {
	dest := make([]data.FollowedStock, 0)
	db.Dao.Model(&data.FollowedStock{}).Find(&dest)
	return dest
}

func (s StockService) GetFollowedStockDetail(stockCode string) *data.FollowedStock {
	follow := &data.FollowedStock{
		StockCode: stockCode,
	}
	db.Dao.Model(follow).Where("stock_code = ?", stockCode).Preload("Groups").Preload("Groups.GroupInfo").First(follow)
	return follow
}

func (s StockService) UpdateFollowPrice(stockCode string, price float64) {
	go db.Dao.Model(&data.FollowedStock{}).Where("stock_code = ?", stockCode).Updates(map[string]any{
		"price": price,
	})
}

func (s StockService) GetStoredStockInfo(stockCode string) *data.StockInfo {
	stockInfo := &data.StockInfo{}
	db.Dao.Model(stockInfo).Where("code = ?", stockCode).First(stockInfo)
	return stockInfo
}

func (s StockService) GetStockKLine(stockCode string, days int64) *[]data.KLineData {
	return data.NewStockDataApi().GetHK_KLineData(stockCode, "day", days)
}

func (s StockService) GetStockCommonKLine(stockCode string, days int64) *[]data.KLineData {
	return data.NewStockDataApi().GetCommonKLineData(stockCode, "day", days)
}

func (s StockService) GetStockMinutePriceLineData(stockCode, stockName string) map[string]any {
	res := make(map[string]any, 4)
	priceData, date := data.NewStockDataApi().GetStockMinutePriceData(stockCode)
	res["priceData"] = priceData
	res["date"] = date
	res["stockName"] = stockName
	res["stockCode"] = stockCode
	return res
}

func (s StockService) SearchStock(words string) map[string]any {
	return data.NewSearchStockApi(words).SearchStock(5000)
}

func (s StockService) GetHotStrategy() map[string]any {
	return data.NewSearchStockApi("").HotStrategy()
}

func (s StockService) GetStockCodeRealTimeData(stockCodes ...string) (*[]data.StockInfo, error) {
	return data.NewStockDataApi().GetStockCodeRealTimeData(stockCodes...)
}
