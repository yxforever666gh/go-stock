package data

import (
	"strconv"
	"strings"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type dailyBar struct {
	TradeDate time.Time
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    float64
	Amount    float64
}

func normalizeDailyTradeDate(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	loc := cnLocation()
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

func listDailyBarsFromCache(stockCode string, startDay, endDay time.Time) ([]dailyBar, error) {
	code := strings.ToUpper(strings.TrimSpace(stockCode))
	if code == "" {
		return []dailyBar{}, nil
	}
	startDay = normalizeDailyTradeDate(startDay)
	endDay = normalizeDailyTradeDate(endDay)
	if startDay.IsZero() || endDay.IsZero() || endDay.Before(startDay) {
		return []dailyBar{}, nil
	}

	rows := make([]models.AiRecommendDailyBar, 0)
	err := db.Dao.Model(&models.AiRecommendDailyBar{}).
		Where("stock_code = ? AND trade_date >= ? AND trade_date <= ?", code, startDay, endDay).
		Order("trade_date ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}

	bars := make([]dailyBar, 0, len(rows))
	for _, row := range rows {
		bars = append(bars, dailyBar{
			TradeDate: normalizeDailyTradeDate(row.TradeDate),
			Open:      row.Open,
			High:      row.High,
			Low:       row.Low,
			Close:     row.Close,
			Volume:    row.Volume,
			Amount:    row.Amount,
		})
	}
	return bars, nil
}

func upsertDailyBarsToCache(stockCode string, bars []dailyBar, source string) (int, error) {
	code := strings.ToUpper(strings.TrimSpace(stockCode))
	if code == "" || len(bars) == 0 {
		return 0, nil
	}

	rows := make([]models.AiRecommendDailyBar, 0, len(bars))
	for _, bar := range bars {
		tradeDate := normalizeDailyTradeDate(bar.TradeDate)
		if tradeDate.IsZero() || bar.Close <= 0 {
			continue
		}
		rows = append(rows, models.AiRecommendDailyBar{
			StockCode: code,
			TradeDate: tradeDate,
			Open:      round2(bar.Open),
			High:      round2(bar.High),
			Low:       round2(bar.Low),
			Close:     round2(bar.Close),
			Volume:    bar.Volume,
			Amount:    bar.Amount,
			Source:    strings.TrimSpace(source),
		})
	}
	if len(rows) == 0 {
		return 0, nil
	}

	err := db.Dao.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "stock_code"}, {Name: "trade_date"}},
		DoUpdates: clause.Assignments(map[string]any{
			"open":       gorm.Expr("excluded.open"),
			"high":       gorm.Expr("excluded.high"),
			"low":        gorm.Expr("excluded.low"),
			"close":      gorm.Expr("excluded.close"),
			"volume":     gorm.Expr("excluded.volume"),
			"amount":     gorm.Expr("excluded.amount"),
			"source":     gorm.Expr("excluded.source"),
			"updated_at": time.Now(),
		}),
	}).CreateInBatches(rows, 500).Error
	if err != nil {
		return 0, err
	}
	return len(rows), nil
}

func kLineDataToDailyBars(kLines []KLineData, startDay, endDay time.Time) []dailyBar {
	startDay = normalizeDailyTradeDate(startDay)
	endDay = normalizeDailyTradeDate(endDay)
	bars := make([]dailyBar, 0, len(kLines))
	for _, line := range kLines {
		day, ok := parseYieldOverviewTradeDay(line.Day)
		if !ok {
			continue
		}
		day = normalizeDailyTradeDate(day)
		if day.Before(startDay) || day.After(endDay) {
			continue
		}
		closePrice, errClose := strconv.ParseFloat(strings.TrimSpace(line.Close), 64)
		if errClose != nil || closePrice <= 0 {
			continue
		}
		openPrice, _ := strconv.ParseFloat(strings.TrimSpace(line.Open), 64)
		highPrice, _ := strconv.ParseFloat(strings.TrimSpace(line.High), 64)
		lowPrice, _ := strconv.ParseFloat(strings.TrimSpace(line.Low), 64)
		volume, _ := strconv.ParseFloat(strings.TrimSpace(line.Volume), 64)
		bars = append(bars, dailyBar{
			TradeDate: day,
			Open:      openPrice,
			High:      highPrice,
			Low:       lowPrice,
			Close:     closePrice,
			Volume:    volume,
		})
	}
	return bars
}

func loadDailyBarsWithCache(modelCode, quoteCode string, startDay, endDay time.Time, klineDays int64) ([]dailyBar, error) {
	modelCode = strings.ToUpper(strings.TrimSpace(modelCode))
	quoteCode = strings.TrimSpace(quoteCode)
	if modelCode == "" || quoteCode == "" {
		return []dailyBar{}, nil
	}
	startDay = normalizeDailyTradeDate(startDay)
	endDay = normalizeDailyTradeDate(endDay)
	if startDay.IsZero() || endDay.IsZero() || endDay.Before(startDay) {
		return []dailyBar{}, nil
	}
	if db.Dao == nil {
		return fetchDailyBarsFromRemote(quoteCode, startDay, endDay, klineDays), nil
	}

	cached, err := listDailyBarsFromCache(modelCode, startDay, endDay)
	if err != nil {
		return nil, err
	}
	if dailyBarsCoverTradingWindow(cached, startDay, endDay) {
		return cached, nil
	}

	fetched := fetchDailyBarsFromRemote(quoteCode, startDay, endDay, klineDays)
	if len(fetched) == 0 {
		return cached, nil
	}
	if _, upsertErr := upsertDailyBarsToCache(modelCode, fetched, "sina"); upsertErr != nil {
		return cached, upsertErr
	}
	return listDailyBarsFromCache(modelCode, startDay, endDay)
}

func fetchDailyBarsFromRemote(quoteCode string, startDay, endDay time.Time, klineDays int64) []dailyBar {
	kLines := NewStockDataApi().GetKLineData(quoteCode, "240", klineDays)
	if kLines == nil || len(*kLines) == 0 {
		return []dailyBar{}
	}
	return kLineDataToDailyBars(*kLines, startDay, endDay)
}

func dailyBarsCoverTradingWindow(bars []dailyBar, startDay, endDay time.Time) bool {
	if len(bars) == 0 {
		return false
	}
	startDay = normalizeDailyTradeDate(startDay)
	endDay = normalizeDailyTradeDate(endDay)
	first := normalizeDailyTradeDate(bars[0].TradeDate)
	last := normalizeDailyTradeDate(bars[len(bars)-1].TradeDate)
	return !first.After(startDay) && !last.Before(endDay)
}
