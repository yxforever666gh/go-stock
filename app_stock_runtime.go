package main

import (
	"go-stock/backend/data"
	"go-stock/internal/service"
	"strings"
	"time"

	"github.com/duke-git/lancet/v2/convertor"
	"github.com/duke-git/lancet/v2/mathutil"
	"github.com/duke-git/lancet/v2/slice"
	"github.com/duke-git/lancet/v2/strutil"
)

type monitoredStockSnapshot struct {
	ChangedInfos []data.StockInfo
	Total        float64
}

func shouldTrackRealtimeStock(stockCode string, now time.Time) bool {
	if strutil.HasPrefixAny(stockCode, []string{"SZ", "SH", "sh", "sz"}) {
		return isTradingTime(now)
	}
	if strutil.HasPrefixAny(stockCode, []string{"hk", "HK"}) {
		return IsHKTradingTime(now)
	}
	if strutil.HasPrefixAny(stockCode, []string{"us", "US", "gb_"}) {
		return IsUSTradingTime(now)
	}
	return true
}

func (a *App) collectMonitoredStockSnapshot() monitoredStockSnapshot {
	follows := a.services.Stock.GetAllFollowedStocks()
	stockInfos := collectStockInfos(a.services.Stock, follows...)
	snapshot := monitoredStockSnapshot{
		ChangedInfos: make([]data.StockInfo, 0, len(stockInfos)),
	}
	for _, stockInfo := range stockInfos {
		snapshot.Total += stockInfo.ProfitAmountToday
		price, _ := convertor.ToFloat(stockInfo.Price)
		if stockInfo.PrePrice != price {
			snapshot.ChangedInfos = append(snapshot.ChangedInfos, stockInfo)
		}
	}
	return snapshot
}

func GetStockInfos(follows ...data.FollowedStock) *[]data.StockInfo {
	stockInfos := collectStockInfos(service.NewStockService(), follows...)
	return &stockInfos
}

func collectStockInfos(stockService service.StockService, follows ...data.FollowedStock) []data.StockInfo {
	stockInfos := make([]data.StockInfo, 0)
	stockCodes := make([]string, 0, len(follows))
	now := time.Now()
	for _, follow := range follows {
		if !shouldTrackRealtimeStock(follow.StockCode, now) {
			continue
		}
		stockCodes = append(stockCodes, follow.StockCode)
	}
	if len(stockCodes) == 0 {
		return stockInfos
	}
	stockData, err := stockService.GetStockCodeRealTimeData(stockCodes...)
	if err != nil || stockData == nil {
		return stockInfos
	}
	for _, info := range *stockData {
		v, ok := slice.FindBy(follows, func(idx int, follow data.FollowedStock) bool {
			if strutil.HasPrefixAny(follow.StockCode, []string{"US", "us"}) {
				return strings.ToLower(strings.Replace(follow.StockCode, "us", "gb_", 1)) == info.Code
			}
			return follow.StockCode == info.Code
		})
		if ok {
			enrichFollowedStockData(stockService, v, &info)
			stockInfos = append(stockInfos, info)
		}
	}
	return stockInfos
}

func getStockInfo(follow data.FollowedStock) *data.StockInfo {
	return getStockInfoWithService(service.NewStockService(), follow)
}

func getStockInfoWithService(stockService service.StockService, follow data.FollowedStock) *data.StockInfo {
	stockDatas, err := stockService.GetStockCodeRealTimeData(follow.StockCode)
	if err != nil || stockDatas == nil || len(*stockDatas) == 0 {
		return &data.StockInfo{}
	}
	stockData := (*stockDatas)[0]
	enrichFollowedStockData(stockService, follow, &stockData)
	return &stockData
}

func enrichFollowedStockData(stockService service.StockService, follow data.FollowedStock, stockData *data.StockInfo) {
	stockData.PrePrice = follow.Price
	stockData.Sort = follow.Sort
	stockData.CostPrice = follow.CostPrice
	stockData.CostVolume = follow.Volume
	stockData.AlarmChangePercent = follow.AlarmChangePercent
	stockData.AlarmPrice = follow.AlarmPrice
	stockData.Groups = follow.Groups

	price, _ := convertor.ToFloat(stockData.Price)
	if price == 0 {
		price, _ = convertor.ToFloat(stockData.A1P)
	}
	if price == 0 {
		price, _ = convertor.ToFloat(stockData.B1P)
	}

	preClosePrice, _ := convertor.ToFloat(stockData.PreClose)
	if price == 0 {
		price = preClosePrice
	}

	highPrice, _ := convertor.ToFloat(stockData.High)
	if highPrice == 0 {
		highPrice, _ = convertor.ToFloat(stockData.Open)
	}

	lowPrice, _ := convertor.ToFloat(stockData.Low)
	if lowPrice == 0 {
		lowPrice, _ = convertor.ToFloat(stockData.Open)
	}

	if price > 0 && preClosePrice > 0 {
		stockData.ChangePrice = mathutil.RoundToFloat(price-preClosePrice, 2)
		stockData.ChangePercent = mathutil.RoundToFloat(mathutil.Div(price-preClosePrice, preClosePrice)*100, 3)
	}
	if highPrice > 0 && preClosePrice > 0 {
		stockData.HighRate = mathutil.RoundToFloat(mathutil.Div(highPrice-preClosePrice, preClosePrice)*100, 3)
	}
	if lowPrice > 0 && preClosePrice > 0 {
		stockData.LowRate = mathutil.RoundToFloat(mathutil.Div(lowPrice-preClosePrice, preClosePrice)*100, 3)
	}
	if follow.CostPrice > 0 && follow.Volume > 0 {
		if price > 0 {
			stockData.Profit = mathutil.RoundToFloat(mathutil.Div(price-follow.CostPrice, follow.CostPrice)*100, 3)
			stockData.ProfitAmount = mathutil.RoundToFloat((price-follow.CostPrice)*float64(follow.Volume), 2)
			stockData.ProfitAmountToday = mathutil.RoundToFloat((price-preClosePrice)*float64(follow.Volume), 2)
		} else {
			stockData.Profit = mathutil.RoundToFloat(mathutil.Div(preClosePrice-follow.CostPrice, follow.CostPrice)*100, 3)
			stockData.ProfitAmount = mathutil.RoundToFloat((preClosePrice-follow.CostPrice)*float64(follow.Volume), 2)
			stockData.ProfitAmountToday = 0
		}
	}

	if follow.Price != price && price > 0 {
		stockService.UpdateFollowPrice(follow.StockCode, price)
	}
}
