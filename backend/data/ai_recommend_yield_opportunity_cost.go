package data

import (
	"fmt"
	"strings"
	"time"

	"go-stock/backend/models"
)

// calculateOpportunityCost 计算机会成本（被跳过推荐的假设收益）
func calculateOpportunityCost(skippedRecommends []models.AiRecommendStocks, lookbackDays int) (float64, string) {
	if len(skippedRecommends) == 0 {
		return 0, "--"
	}

	totalCost := 0.0
	validCount := 0

	for _, rec := range skippedRecommends {
		// 获取跳过时的价格
		skipPrice := resolveSkipPrice(rec)
		if skipPrice <= 0 {
			continue
		}

		// 获取跳过后N天内的最高价
		maxPrice := getMaxPriceAfterSkip(rec, lookbackDays)
		if maxPrice <= 0 {
			continue
		}

		// 计算假设收益
		hypotheticalYield := (maxPrice - skipPrice) / skipPrice * 100
		if hypotheticalYield > 0 {
			totalCost += hypotheticalYield
			validCount++
		}
	}

	if validCount == 0 {
		return 0, "--"
	}

	avgCost := totalCost / float64(validCount)
	return round2(avgCost), formatSignedPercent(avgCost)
}

// resolveSkipPrice 获取跳过时的价格
func resolveSkipPrice(rec models.AiRecommendStocks) float64 {
	// 优先使用推荐时的价格
	if rec.StockPrice != "" {
		if price := parseFloatSafe(rec.StockPrice); price > 0 {
			return price
		}
	}

	// 其次使用收盘价
	if rec.StockClosePrice != "" {
		if price := parseFloatSafe(rec.StockClosePrice); price > 0 {
			return price
		}
	}

	// 最后使用当前价格
	if rec.StockCurrentPrice != "" {
		if price := parseFloatSafe(rec.StockCurrentPrice); price > 0 {
			return price
		}
	}

	return 0
}

// getMaxPriceAfterSkip 获取跳过后N天内的最高价
func getMaxPriceAfterSkip(rec models.AiRecommendStocks, lookbackDays int) float64 {
	stockCode := normalizeRecommendStockCode(rec.StockCode)
	if stockCode == "" {
		return 0
	}

	// 获取推荐时间
	var startTime time.Time
	if rec.DataTime != nil {
		startTime = *rec.DataTime
	} else {
		startTime = rec.CreatedAt
	}

	if startTime.IsZero() {
		return 0
	}

	// 计算结束时间（N个交易日后）
	endTime := startTime.AddDate(0, 0, lookbackDays*2) // 粗略估计，实际交易日会少一些

	// 获取日线数据
	dailyPrices, err := queryStockDailyPrices(stockCode, startTime, endTime)
	if err != nil || len(dailyPrices) == 0 {
		return 0
	}

	// 找出最高价
	maxPrice := 0.0
	tradeDayCount := 0
	for _, price := range dailyPrices {
		if price.High > maxPrice {
			maxPrice = price.High
		}
		tradeDayCount++
		if tradeDayCount >= lookbackDays {
			break
		}
	}

	return maxPrice
}

// getMinPriceAfterSkip 获取跳过后N天内的最低价
func getMinPriceAfterSkip(rec models.AiRecommendStocks, lookbackDays int) float64 {
	stockCode := normalizeRecommendStockCode(rec.StockCode)
	if stockCode == "" {
		return 0
	}

	// 获取推荐时间
	var startTime time.Time
	if rec.DataTime != nil {
		startTime = *rec.DataTime
	} else {
		startTime = rec.CreatedAt
	}

	if startTime.IsZero() {
		return 0
	}

	// 计算结束时间
	endTime := startTime.AddDate(0, 0, lookbackDays*2)

	// 获取日线数据
	dailyPrices, err := queryStockDailyPrices(stockCode, startTime, endTime)
	if err != nil || len(dailyPrices) == 0 {
		return 0
	}

	// 找出最低价
	minPrice := 999999.0
	tradeDayCount := 0
	for _, price := range dailyPrices {
		if price.Low < minPrice && price.Low > 0 {
			minPrice = price.Low
		}
		tradeDayCount++
		if tradeDayCount >= lookbackDays {
			break
		}
	}

	if minPrice == 999999.0 {
		return 0
	}

	return minPrice
}

// queryStockDailyPrices 查询股票日线数据
func queryStockDailyPrices(stockCode string, startTime, endTime time.Time) ([]stockDailyPrice, error) {
	// 简化实现：从数据库查询
	var prices []stockDailyPrice

	// 这里需要实际的数据库查询逻辑
	// 暂时返回空，后续补充
	return prices, nil
}

// stockDailyPrice 日线价格数据
type stockDailyPrice struct {
	TradeDate string
	Open      float64
	High      float64
	Low       float64
	Close     float64
}

// parseFloatSafe 安全解析浮点数
func parseFloatSafe(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}

	var f float64
	if _, err := fmt.Sscanf(s, "%f", &f); err != nil {
		return 0
	}
	return f
}
