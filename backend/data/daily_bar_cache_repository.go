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
	// Some read-only overview callers legitimately run before the database has
	// been initialised (notably isolated tests and early UI startup). Treat that
	// exactly like an empty cache; callers already surface the missing-series
	// warning and must never attempt a remote fallback in cache-only mode.
	if db.Dao == nil {
		return []dailyBar{}, nil
	}
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
	observedAt := time.Now().In(cnLocation())
	if db.Dao == nil {
		return cacheableCompletedDailyBars(fetchDailyBarsFromRemote(quoteCode, startDay, endDay, klineDays), observedAt), nil
	}

	cached, err := listDailyBarsFromCache(modelCode, startDay, endDay)
	if err != nil {
		return nil, err
	}
	if dailyBarsCoverTradingWindowAt(cached, startDay, endDay, observedAt) {
		return cached, nil
	}

	fetched := cacheableCompletedDailyBars(fetchDailyBarsFromRemote(quoteCode, startDay, endDay, klineDays), observedAt)
	if len(fetched) == 0 {
		return cached, nil
	}
	// StockDataApi.GetKLineData requests Tencent's qfq series. Persist the
	// adjustment provenance; treating it as an unadjusted Sina close would make
	// later freshness/corporate-action audits impossible.
	if _, upsertErr := upsertDailyBarsToCache(modelCode, fetched, "tencent_qfq"); upsertErr != nil {
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

// dailyBarsCoverTradingWindowAt deliberately treats a request through the
// current China trading day as refreshable. A provider can expose today's
// still-forming 240-minute bar during the session; considering that date a
// complete cache hit would prevent the final close from replacing it later.
func dailyBarsCoverTradingWindowAt(bars []dailyBar, startDay, endDay, observedAt time.Time) bool {
	if !dailyBarsCoverTradingWindow(bars, startDay, endDay) {
		return false
	}
	observedDay := normalizeDailyTradeDate(observedAt)
	requestedEnd := normalizeDailyTradeDate(endDay)
	return observedDay.IsZero() || requestedEnd.IsZero() || !requestedEnd.Equal(observedDay)
}

// cacheableCompletedDailyBars keeps a forming current-day bar out of the
// durable daily cache. Historical bars and the current bar after the 15:00
// close remain cacheable. Combined with dailyBarsCoverTradingWindowAt, this
// also refreshes a legacy partial row after the close instead of accepting its
// date alone as proof of completeness.
func cacheableCompletedDailyBars(bars []dailyBar, observedAt time.Time) []dailyBar {
	if len(bars) == 0 || observedAt.IsZero() {
		return bars
	}
	local := observedAt.In(cnLocation())
	observedDay := normalizeDailyTradeDate(local)
	marketClose := time.Date(local.Year(), local.Month(), local.Day(), 15, 0, 0, 0, local.Location())
	if !local.Before(marketClose) {
		return bars
	}
	result := make([]dailyBar, 0, len(bars))
	for _, bar := range bars {
		if normalizeDailyTradeDate(bar.TradeDate).Equal(observedDay) {
			continue
		}
		result = append(result, bar)
	}
	return result
}
