package data

import (
	"strings"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type minuteBar struct {
	TradeTime time.Time
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    float64
	Amount    float64
}

func normalizeMinuteTime(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return t.Truncate(time.Minute)
}

func listMinuteBarsFromCache(stockCode string, start, end time.Time) ([]minuteBar, error) {
	code := strings.ToUpper(strings.TrimSpace(stockCode))
	if code == "" {
		return []minuteBar{}, nil
	}
	if !start.Before(end) {
		return []minuteBar{}, nil
	}

	rows := make([]models.AiRecommendMinuteBar, 0)
	err := db.Dao.Model(&models.AiRecommendMinuteBar{}).
		Where("stock_code = ? AND trade_time >= ? AND trade_time <= ?", code, start, end).
		Order("trade_time ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}

	bars := make([]minuteBar, 0, len(rows))
	for _, row := range rows {
		bars = append(bars, minuteBar{
			TradeTime: row.TradeTime,
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

func upsertMinuteBarsToCache(stockCode string, bars []minuteBar, source string) (int, error) {
	code := strings.ToUpper(strings.TrimSpace(stockCode))
	if code == "" || len(bars) == 0 {
		return 0, nil
	}

	rows := make([]models.AiRecommendMinuteBar, 0, len(bars))
	for _, bar := range bars {
		if bar.TradeTime.IsZero() {
			continue
		}
		rows = append(rows, models.AiRecommendMinuteBar{
			StockCode: code,
			TradeTime: normalizeMinuteTime(bar.TradeTime),
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
		Columns: []clause.Column{{Name: "stock_code"}, {Name: "trade_time"}},
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
	}).CreateInBatches(rows, 800).Error
	if err != nil {
		return 0, err
	}

	return len(rows), nil
}

func getMinuteCacheRange(stockCode string) (*time.Time, *time.Time, error) {
	code := strings.ToUpper(strings.TrimSpace(stockCode))
	if code == "" {
		return nil, nil, nil
	}

	type scopeRow struct {
		Start string `gorm:"column:start_time"`
		End   string `gorm:"column:end_time"`
	}
	row := scopeRow{}
	err := db.Dao.Model(&models.AiRecommendMinuteBar{}).
		Select("MIN(trade_time) AS start_time, MAX(trade_time) AS end_time").
		Where("stock_code = ?", code).
		Scan(&row).Error
	if err != nil {
		return nil, nil, err
	}
	start, okStart := parseSQLiteDateTimeText(strings.TrimSpace(row.Start))
	end, okEnd := parseSQLiteDateTimeText(strings.TrimSpace(row.End))
	if !okStart || !okEnd {
		return nil, nil, nil
	}
	return &start, &end, nil
}

func parseSQLiteDateTimeText(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	loc := cnLocation()
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		"2006/01/02 15:04:05",
	}
	for _, layout := range layouts {
		var t time.Time
		var err error
		if strings.Contains(layout, "-07:00") || strings.Contains(layout, "Z07:00") {
			t, err = time.Parse(layout, raw)
		} else {
			t, err = time.ParseInLocation(layout, raw, loc)
		}
		if err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

type minuteCacheRange struct {
	Start *time.Time
	End   *time.Time
}

func loadMinuteCacheRangeMap() (map[string]minuteCacheRange, error) {
	return loadMinuteCacheRangeMapByCodes(nil)
}

func loadMinuteCacheRangeMapByCodes(codes []string) (map[string]minuteCacheRange, error) {
	type row struct {
		StockCode string `gorm:"column:stock_code"`
		Start     string `gorm:"column:start_time"`
		End       string `gorm:"column:end_time"`
	}
	normalizedCodes := make([]string, 0, len(codes))
	seen := make(map[string]struct{}, len(codes))
	for _, code := range codes {
		normalized := strings.ToUpper(strings.TrimSpace(code))
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		normalizedCodes = append(normalizedCodes, normalized)
	}
	rows := make([]row, 0)
	q := db.Dao.Model(&models.AiRecommendMinuteBar{})
	if len(normalizedCodes) > 0 {
		q = q.Where("stock_code IN ?", normalizedCodes)
	}
	err := q.
		Select("stock_code, MIN(trade_time) AS start_time, MAX(trade_time) AS end_time").
		Group("stock_code").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]minuteCacheRange, len(rows))
	for _, r := range rows {
		code := strings.ToUpper(strings.TrimSpace(r.StockCode))
		if code == "" {
			continue
		}
		start, okStart := parseSQLiteDateTimeText(strings.TrimSpace(r.Start))
		end, okEnd := parseSQLiteDateTimeText(strings.TrimSpace(r.End))
		if !okStart || !okEnd {
			continue
		}
		s := start
		e := end
		out[code] = minuteCacheRange{Start: &s, End: &e}
	}
	return out, nil
}

func deleteMinuteBarsCache(stockCode string) error {
	code := strings.ToUpper(strings.TrimSpace(stockCode))
	if code == "" {
		return nil
	}
	return db.Dao.Where("stock_code = ?", code).Delete(&models.AiRecommendMinuteBar{}).Error
}

func cleanMinuteCacheForTrackedCodes(codes []string) error {
	if len(codes) == 0 {
		return db.Dao.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&models.AiRecommendMinuteBar{}).Error
	}
	normalized := make([]string, 0, len(codes))
	for _, code := range codes {
		c := strings.ToUpper(strings.TrimSpace(code))
		if c == "" {
			continue
		}
		normalized = append(normalized, c)
	}
	if len(normalized) == 0 {
		return db.Dao.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&models.AiRecommendMinuteBar{}).Error
	}
	return db.Dao.Where("stock_code NOT IN ?", normalized).Delete(&models.AiRecommendMinuteBar{}).Error
}
