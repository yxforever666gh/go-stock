package data

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"go-stock/backend/db"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type minuteBar struct {
	TradeTime  time.Time
	Open       float64
	High       float64
	Low        float64
	Close      float64
	Volume     float64
	Amount     float64
	Source     string
	SlotIndex  int
	SlotWindow int
}

type minuteCacheDBBar struct {
	StockCode string  `gorm:"column:stock_code;primaryKey"`
	TradeTime int64   `gorm:"column:trade_time;primaryKey"`
	Open      float64 `gorm:"column:open"`
	High      float64 `gorm:"column:high"`
	Low       float64 `gorm:"column:low"`
	Close     float64 `gorm:"column:close"`
	Volume    float64 `gorm:"column:volume"`
	Amount    float64 `gorm:"column:amount"`
	Source    string  `gorm:"column:source"`
	UpdatedAt int64   `gorm:"column:updated_at"`
}

func (minuteCacheDBBar) TableName() string {
	return "minute_bar"
}

func normalizeMinuteTime(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return t.Truncate(time.Minute)
}

func toFloatAny(row []any, idx int) float64 {
	if idx < 0 || idx >= len(row) {
		return 0
	}
	switch value := row[idx].(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int64:
		return float64(value)
	case json.Number:
		parsed, _ := value.Float64()
		return parsed
	case string:
		parsed, _ := strconv.ParseFloat(strings.TrimSpace(value), 64)
		return parsed
	default:
		return 0
	}
}

// minuteBarSourceProvesUnadjusted is intentionally allowlist-based so cached
// raw execution prices cannot be confused with qfq/hfq observations.
func minuteBarSourceProvesUnadjusted(source string) bool {
	source = strings.ToLower(strings.TrimSpace(source))
	if source == "" {
		return false
	}
	for _, adjusted := range []string{"qfq", "hfq", "adjustment=forward", "adjustment=backward"} {
		if strings.Contains(source, adjusted) {
			return false
		}
	}
	if strings.Contains(source, "adjustment=none") || strings.Contains(source, "unadjusted") ||
		source == "raw" || strings.HasSuffix(source, ":raw") || strings.Contains(source, "_raw") {
		return true
	}
	switch source {
	case "sina", "tencent", "diemeng", "diemeng_dump", "akshare:em":
		return true
	}
	// Test fixtures construct raw OHLC directly. Keep those explicit labels out
	// of the production-provider allowlist while still exercising provenance.
	return source == "test" || strings.HasPrefix(source, "test:") || strings.HasSuffix(source, "-test")
}

func minuteTimeMillis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return normalizeMinuteTime(t).UnixMilli()
}

func minuteTimeFromMillis(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).In(cnLocation())
}

func listMinuteBarsFromMinuteDatabase(database *gorm.DB, code string, start, end time.Time) ([]minuteBar, error) {
	if database == nil {
		return []minuteBar{}, nil
	}
	rows := make([]minuteCacheDBBar, 0)
	err := database.Model(&minuteCacheDBBar{}).
		Where("stock_code = ? AND trade_time >= ? AND trade_time <= ?", code, minuteTimeMillis(start), minuteTimeMillis(end)).
		Order("trade_time ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	bars := make([]minuteBar, 0, len(rows))
	for _, row := range rows {
		bars = append(bars, minuteBar{
			TradeTime: minuteTimeFromMillis(row.TradeTime),
			Open:      row.Open,
			High:      row.High,
			Low:       row.Low,
			Close:     row.Close,
			Volume:    row.Volume,
			Amount:    row.Amount,
			Source:    strings.TrimSpace(row.Source),
		})
	}
	return bars, nil
}

func upsertMinuteBarsToCache(stockCode string, bars []minuteBar, source string) (int, error) {
	code := strings.ToUpper(strings.TrimSpace(stockCode))
	if code == "" || len(bars) == 0 {
		return 0, nil
	}

	rows := make([]minuteCacheDBBar, 0, len(bars))
	now := time.Now().UnixMilli()
	for _, bar := range bars {
		if bar.TradeTime.IsZero() {
			continue
		}
		rowSource := strings.TrimSpace(bar.Source)
		if rowSource == "" {
			rowSource = strings.TrimSpace(source)
		}
		rows = append(rows, minuteCacheDBBar{
			StockCode: code,
			TradeTime: minuteTimeMillis(bar.TradeTime),
			Open:      round2(bar.Open),
			High:      round2(bar.High),
			Low:       round2(bar.Low),
			Close:     round2(bar.Close),
			Volume:    bar.Volume,
			Amount:    bar.Amount,
			Source:    rowSource,
			UpdatedAt: now,
		})
	}
	if len(rows) == 0 {
		return 0, nil
	}

	if err := upsertMinuteBarsToMinuteDB(rows); err != nil {
		return 0, err
	}
	clearMinuteCoverageStatsCache()
	return len(rows), nil
}

func upsertMinuteBarsToMinuteDB(rows []minuteCacheDBBar) error {
	if db.MinuteDao == nil {
		return nil
	}
	dbRows := make([]minuteCacheDBBar, 0, len(rows))
	for _, row := range rows {
		if row.TradeTime <= 0 {
			continue
		}
		updatedAt := row.UpdatedAt
		if updatedAt <= 0 {
			updatedAt = time.Now().UnixMilli()
		}
		dbRows = append(dbRows, minuteCacheDBBar{
			StockCode: row.StockCode,
			TradeTime: row.TradeTime,
			Open:      row.Open,
			High:      row.High,
			Low:       row.Low,
			Close:     row.Close,
			Volume:    row.Volume,
			Amount:    row.Amount,
			Source:    strings.TrimSpace(row.Source),
			UpdatedAt: updatedAt,
		})
	}
	if len(dbRows) == 0 {
		return nil
	}
	return db.MinuteDao.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "stock_code"}, {Name: "trade_time"}},
		DoUpdates: clause.Assignments(map[string]any{
			"open":       gorm.Expr("excluded.open"),
			"high":       gorm.Expr("excluded.high"),
			"low":        gorm.Expr("excluded.low"),
			"close":      gorm.Expr("excluded.close"),
			"volume":     gorm.Expr("excluded.volume"),
			"amount":     gorm.Expr("excluded.amount"),
			"source":     gorm.Expr("excluded.source"),
			"updated_at": gorm.Expr("excluded.updated_at"),
		}),
	}).CreateInBatches(dbRows, 800).Error
}
