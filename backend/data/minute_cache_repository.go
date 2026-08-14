package data

import (
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

func listMinuteBarsFromCache(stockCode string, start, end time.Time) ([]minuteBar, error) {
	code := strings.ToUpper(strings.TrimSpace(stockCode))
	if code == "" {
		return []minuteBar{}, nil
	}
	if start.After(end) {
		return []minuteBar{}, nil
	}

	return listMinuteBarsFromMinuteDB(code, start, end)
}

func listMinuteBarsFromMinuteDB(code string, start, end time.Time) ([]minuteBar, error) {
	if db.MinuteDao == nil {
		return []minuteBar{}, nil
	}
	rows := make([]minuteCacheDBBar, 0)
	err := db.MinuteDao.Model(&minuteCacheDBBar{}).
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

func getMinuteCacheRange(stockCode string) (*time.Time, *time.Time, error) {
	code := strings.ToUpper(strings.TrimSpace(stockCode))
	if code == "" {
		return nil, nil, nil
	}

	minuteStart, minuteEnd, found, err := getMinuteCacheRangeFromMinuteDB(code)
	if err != nil {
		return nil, nil, err
	}
	if !found {
		return nil, nil, nil
	}
	return minuteStart, minuteEnd, nil
}

func getMinuteCacheRangeFromMinuteDB(code string) (*time.Time, *time.Time, bool, error) {
	if db.MinuteDao == nil {
		return nil, nil, false, nil
	}
	type scopeRow struct {
		Start *int64 `gorm:"column:start_time"`
		End   *int64 `gorm:"column:end_time"`
	}
	row := scopeRow{}
	err := db.MinuteDao.Model(&minuteCacheDBBar{}).
		Select("MIN(trade_time) AS start_time, MAX(trade_time) AS end_time").
		Where("stock_code = ?", code).
		Scan(&row).Error
	if err != nil {
		return nil, nil, false, err
	}
	if row.Start == nil || row.End == nil {
		return nil, nil, false, nil
	}
	start := minuteTimeFromMillis(*row.Start)
	end := minuteTimeFromMillis(*row.End)
	return &start, &end, true, nil
}

type minuteCacheRange struct {
	Start *time.Time
	End   *time.Time
}

func loadMinuteCacheRangeMap() (map[string]minuteCacheRange, error) {
	return loadMinuteCacheRangeMapByCodes(nil)
}

func loadMinuteCacheRangeMapByCodes(codes []string) (map[string]minuteCacheRange, error) {
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

	return loadMinuteCacheRangeMapFromMinuteDB(normalizedCodes)
}

func loadMinuteCacheRangeMapFromMinuteDB(normalizedCodes []string) (map[string]minuteCacheRange, error) {
	type row struct {
		StockCode string `gorm:"column:stock_code"`
		Start     *int64 `gorm:"column:start_time"`
		End       *int64 `gorm:"column:end_time"`
	}
	out := make(map[string]minuteCacheRange)
	if db.MinuteDao == nil {
		return out, nil
	}
	rows := make([]row, 0)
	q := db.MinuteDao.Model(&minuteCacheDBBar{})
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
	for _, r := range rows {
		code := strings.ToUpper(strings.TrimSpace(r.StockCode))
		if code == "" || r.Start == nil || r.End == nil {
			continue
		}
		start := minuteTimeFromMillis(*r.Start)
		end := minuteTimeFromMillis(*r.End)
		if start.IsZero() || end.IsZero() {
			continue
		}
		out[code] = minuteCacheRange{Start: &start, End: &end}
	}
	return out, nil
}

func missingMinuteCacheRangeCodes(normalizedCodes []string, existing map[string]minuteCacheRange) []string {
	if len(normalizedCodes) == 0 {
		return nil
	}
	missing := make([]string, 0, len(normalizedCodes))
	for _, code := range normalizedCodes {
		if _, ok := existing[code]; ok {
			continue
		}
		missing = append(missing, code)
	}
	return missing
}

func deleteMinuteBarsCache(stockCode string) error {
	code := strings.ToUpper(strings.TrimSpace(stockCode))
	if code == "" {
		return nil
	}
	if db.MinuteDao != nil {
		if err := db.MinuteDao.Where("stock_code = ?", code).Delete(&minuteCacheDBBar{}).Error; err != nil {
			return err
		}
	}
	clearMinuteCoverageStatsCache()
	return nil
}

func cleanMinuteCacheForTrackedCodes(codes []string) error {
	if len(codes) == 0 {
		if db.MinuteDao != nil {
			if err := db.MinuteDao.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&minuteCacheDBBar{}).Error; err != nil {
				return err
			}
		}
		clearMinuteCoverageStatsCache()
		return nil
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
		if db.MinuteDao != nil {
			if err := db.MinuteDao.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&minuteCacheDBBar{}).Error; err != nil {
				return err
			}
		}
		clearMinuteCoverageStatsCache()
		return nil
	}
	if db.MinuteDao != nil {
		if err := db.MinuteDao.Where("stock_code NOT IN ?", normalized).Delete(&minuteCacheDBBar{}).Error; err != nil {
			return err
		}
	}
	clearMinuteCoverageStatsCache()
	return nil
}
