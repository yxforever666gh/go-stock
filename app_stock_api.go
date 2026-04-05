package main

import (
	"strings"

	"go-stock/backend/data"
)

func (a *App) Greet(stockCode string) *data.StockInfo {
	follow := a.services.Stock.GetFollowedStockDetail(stockCode)
	stockInfo := getStockInfoWithService(a.services.Stock, *follow)
	return stockInfo
}

func (a *App) Follow(stockCode string) string {
	return a.services.Stock.Follow(stockCode)
}

func (a *App) UnFollow(stockCode string) string {
	return a.services.Stock.UnFollow(stockCode)
}

func (a *App) GetFollowList(groupId int) *[]data.FollowedStock {
	return a.services.Stock.GetFollowList(groupId)
}

func (a *App) GetStockList(key string) []data.StockBasic {
	return a.services.Stock.GetStockList(key)
}

func (a *App) SetCostPriceAndVolume(stockCode string, price float64, volume int64) string {
	return a.services.Stock.SetCostPriceAndVolume(stockCode, price, volume)
}

func (a *App) SetAlarmChangePercent(val, alarmPrice float64, stockCode string) string {
	return a.services.Stock.SetAlarmChangePercent(val, alarmPrice, stockCode)
}

func (a *App) SetStockSort(sort int64, stockCode string) {
	a.services.Stock.SetStockSort(sort, stockCode)
}

func (a *App) SetStockAICron(cronText, stockCode string) {
	a.services.Stock.SetStockAICron(cronText, stockCode)
	if strings.HasPrefix(stockCode, "gb_") {
		stockCode = strings.ToUpper(stockCode)
		stockCode = strings.Replace(stockCode, "gb_", "us", 1)
		stockCode = strings.Replace(stockCode, "GB_", "us", 1)
	}
	if entryID, exists := a.getCronEntry(stockCode); exists {
		a.cron.Remove(entryID)
	}
	follow := a.services.Stock.GetFollowedStockByStockCode(stockCode)
	if follow == nil {
		return
	}
	id, _ := a.cron.AddFunc(cronText, a.AddCronTask(*follow))
	a.setCronEntry(stockCode, id)
}

func (a *App) GetStockKLine(stockCode, stockName string, days int64) *[]data.KLineData {
	return a.services.Stock.GetStockKLine(stockCode, days)
}

func (a *App) GetStockMinutePriceLineData(stockCode, stockName string) map[string]any {
	return a.services.Stock.GetStockMinutePriceLineData(stockCode, stockName)
}

func (a *App) GetStockCommonKLine(stockCode, stockName string, days int64) *[]data.KLineData {
	return a.services.Stock.GetStockCommonKLine(stockCode, days)
}
