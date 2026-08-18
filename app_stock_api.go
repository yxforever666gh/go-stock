package main

import (
	"strings"

	"go-stock/backend/models"
)

func (a *App) stockSnapshot(stockCode string) *models.StockInfo {
	follow := a.services.Stock.GetFollowedStockDetail(stockCode)
	if follow == nil {
		return nil
	}
	stockInfo := getStockInfoWithService(a.services.Stock, *follow)
	return stockInfo
}

func (a *App) setStockAICron(cronText, stockCode string) {
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
	id, err := a.cron.AddFunc(cronText, a.addCronTask(*follow))
	if err != nil {
		a.recordSchedulerRegistrationError("FollowAnalysis:"+stockCode, cronText, err)
		return
	}
	a.setCronEntry(stockCode, id)
}
