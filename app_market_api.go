package main

import (
	"go-stock/backend/models"

	"github.com/duke-git/lancet/v2/slice"
)

func (a *App) GetTelegraphList(source string) *[]*models.Telegraph {
	return a.services.Market.GetTelegraphList(source)
}

func (a *App) ReFleshTelegraphList(source string) *[]*models.Telegraph {
	return a.services.Market.RefreshTelegraphList(source)
}

func (a *App) GlobalStockIndexes() map[string]any {
	return a.services.Market.GlobalStockIndexes()
}

func (a *App) GetIndustryRank(sort string, cnt int) []any {
	return a.services.Market.GetIndustryRank(sort, cnt)
}

func (a *App) GetIndustryMoneyRankSina(fenlei, sort string) []map[string]any {
	return a.services.Market.GetIndustryMoneyRankSina(fenlei, sort)
}

func (a *App) GetMoneyRankSina(sort string) []map[string]any {
	return a.services.Market.GetMoneyRankSina(sort)
}

func (a *App) GetStockMoneyTrendByDay(stockCode string, days int) []map[string]any {
	res := a.services.Market.GetStockMoneyTrendByDay(stockCode, days)
	slice.Reverse(res)
	return res
}
