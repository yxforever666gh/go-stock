package main

import "go-stock/backend/models"

func (a *App) stockSnapshot(stockCode string) *models.StockInfo {
	follow := a.services.Stock.GetFollowedStockDetail(stockCode)
	if follow == nil {
		return nil
	}
	stockInfo := getStockInfoWithService(a.services.Stock, *follow)
	return stockInfo
}
