package service

import (
	"context"

	"go-stock/backend/models"
)

type StockService struct {
	operations StockOperations
}

func NewStockService(operations StockOperations) StockService {
	return StockService{operations: operations}
}

func (s StockService) ReplaceStockBaseInfo(
	ctx context.Context,
	domestic []models.StockBasic,
	hongKong []models.StockInfoHK,
	unitedStates []models.StockInfoUS,
) error {
	return s.operations.ReplaceStockBaseInfo(ctx, domestic, hongKong, unitedStates)
}

func (s StockService) Follow(stockCode string) string {
	return s.operations.Follow(stockCode)
}

func (s StockService) UnFollow(stockCode string) string {
	return s.operations.UnFollow(stockCode)
}

func (s StockService) GetFollowList(groupID int) *[]models.FollowedStock {
	return s.operations.GetFollowList(groupID)
}

func (s StockService) GetStockList(key string) []models.StockBasic {
	return s.operations.GetStockList(key)
}

func (s StockService) SetCostPriceAndVolume(stockCode string, price float64, volume int64) string {
	return s.operations.SetCostPriceAndVolume(stockCode, price, volume)
}

func (s StockService) SetAlarmChangePercent(val, alarmPrice float64, stockCode string) string {
	return s.operations.SetAlarmChangePercent(val, alarmPrice, stockCode)
}

func (s StockService) SetStockSort(sort int64, stockCode string) {
	s.operations.SetStockSort(sort, stockCode)
}

func (s StockService) SetStockAICron(cronText, stockCode string) {
	s.operations.SetStockAICron(cronText, stockCode)
}

func (s StockService) GetFollowedStockByStockCode(stockCode string) *models.FollowedStock {
	return s.operations.GetFollowedStockByStockCode(stockCode)
}

func (s StockService) GetAllFollowedStocks() []models.FollowedStock {
	return s.operations.GetAllFollowedStocks()
}

func (s StockService) GetFollowedStockDetail(stockCode string) *models.FollowedStock {
	return s.operations.GetFollowedStockDetail(stockCode)
}

func (s StockService) UpdateFollowPrice(stockCode string, price float64) {
	go s.operations.UpdateFollowPrice(stockCode, price)
}

func (s StockService) GetStoredStockInfo(stockCode string) *models.StockInfo {
	return s.operations.GetStoredStockInfo(stockCode)
}

func (s StockService) GetStockKLine(stockCode string, days int64) *[]models.KLineData {
	return s.operations.GetStockKLine(stockCode, days)
}

func (s StockService) GetStockCommonKLine(stockCode string, days int64) *[]models.KLineData {
	return s.operations.GetStockCommonKLine(stockCode, days)
}

func (s StockService) GetStockMinutePriceLineData(stockCode, stockName string) map[string]any {
	return s.operations.GetStockMinutePriceLineData(stockCode, stockName)
}

func (s StockService) SearchStock(words string) map[string]any {
	return s.operations.SearchStock(words)
}

func (s StockService) SearchStockWithFingerprint(words, fingerprint string, pageSize int) map[string]any {
	return s.operations.SearchStockWithFingerprint(words, fingerprint, pageSize)
}

func (s StockService) GetHotStrategy() map[string]any {
	return s.operations.GetHotStrategy()
}

func (s StockService) GetStockCodeRealTimeData(stockCodes ...string) (*[]models.StockInfo, error) {
	return s.operations.GetStockCodeRealTimeData(stockCodes...)
}
