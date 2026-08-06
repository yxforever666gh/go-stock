package data

import (
	"errors"
	"os"
	"sort"
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
	// Source must survive cache reads so V1.5 valuation can distinguish
	// executable unadjusted minutes from qfq/hfq observations.
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

// minuteBarSourceProvesUnadjusted is intentionally allowlist-based. V1.5
// execution and NAV accounting apply corporate actions from the sealed ledger;
// consuming a qfq/hfq minute as well would double-adjust price and quantity.
// Historical akshare:sina rows are ambiguous because the old source label did
// not record GO_STOCK_AKSHARE_MINUTE_ADJUST, whereas the EM minute endpoint has
// always forced adjust="" and is therefore safe to recognize explicitly.
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

	bars, err := listMinuteBarsFromMinuteDB(code, start, end)
	if err != nil {
		return nil, err
	}
	if len(bars) > 0 && minuteBarsCoverTradingSessions(bars, start, end) {
		return bars, nil
	}
	legacyBars, err := listMinuteBarsFromLegacyCache(code, start, end)
	if err != nil {
		return nil, err
	}
	return mergeMinuteBars(bars, legacyBars), nil
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

func listMinuteBarsFromLegacyCache(code string, start, end time.Time) ([]minuteBar, error) {
	if !legacyMinuteBarTableAvailable() {
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
			Source:    strings.TrimSpace(row.Source),
		})
	}
	return bars, nil
}

func mergeMinuteBars(primary, legacy []minuteBar) []minuteBar {
	if len(primary) == 0 {
		return append([]minuteBar(nil), legacy...)
	}
	if len(legacy) == 0 {
		return append([]minuteBar(nil), primary...)
	}
	byMinute := make(map[int64]minuteBar, len(primary)+len(legacy))
	for _, bar := range legacy {
		if bar.TradeTime.IsZero() {
			continue
		}
		bar.TradeTime = normalizeMinuteTime(bar.TradeTime.In(cnLocation()))
		byMinute[minuteTimeMillis(bar.TradeTime)] = bar
	}
	for _, bar := range primary {
		if bar.TradeTime.IsZero() {
			continue
		}
		bar.TradeTime = normalizeMinuteTime(bar.TradeTime.In(cnLocation()))
		byMinute[minuteTimeMillis(bar.TradeTime)] = bar
	}
	keys := make([]int64, 0, len(byMinute))
	for key := range byMinute {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i] < keys[j]
	})
	out := make([]minuteBar, 0, len(keys))
	for _, key := range keys {
		out = append(out, byMinute[key])
	}
	return out
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
		rowSource := strings.TrimSpace(bar.Source)
		if rowSource == "" {
			rowSource = strings.TrimSpace(source)
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
			Source:    rowSource,
		})
	}
	if len(rows) == 0 {
		return 0, nil
	}

	if err := upsertMinuteBarsToMinuteDB(rows); err != nil {
		return 0, err
	}
	if !minuteCacheDualWriteEnabled() {
		clearMinuteCoverageStatsCache()
		return len(rows), nil
	}
	if !legacyMinuteBarTableAvailable() {
		return 0, errors.New("legacy minute cache schema is unavailable; run numbered migrations")
	}
	if err := upsertMinuteBarsToLegacyCache(rows); err != nil {
		return 0, err
	}
	clearMinuteCoverageStatsCache()
	return len(rows), nil
}

func upsertMinuteBarsToMinuteDB(rows []models.AiRecommendMinuteBar) error {
	if db.MinuteDao == nil {
		return nil
	}
	now := time.Now().UnixMilli()
	dbRows := make([]minuteCacheDBBar, 0, len(rows))
	for _, row := range rows {
		if row.TradeTime.IsZero() {
			continue
		}
		dbRows = append(dbRows, minuteCacheDBBar{
			StockCode: row.StockCode,
			TradeTime: minuteTimeMillis(row.TradeTime),
			Open:      row.Open,
			High:      row.High,
			Low:       row.Low,
			Close:     row.Close,
			Volume:    row.Volume,
			Amount:    row.Amount,
			Source:    strings.TrimSpace(row.Source),
			UpdatedAt: now,
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

func upsertMinuteBarsToLegacyCache(rows []models.AiRecommendMinuteBar) error {
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
	return err
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
	rng := minuteCacheRange{}
	if found {
		rng = mergeMinuteCacheRange(rng, minuteCacheRange{Start: minuteStart, End: minuteEnd})
	}
	legacyStart, legacyEnd, err := getMinuteCacheRangeFromLegacyCache(code)
	if err != nil {
		return nil, nil, err
	}
	rng = mergeMinuteCacheRange(rng, minuteCacheRange{Start: legacyStart, End: legacyEnd})
	if rng.Start == nil || rng.End == nil {
		return nil, nil, nil
	}
	return rng.Start, rng.End, nil
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

func getMinuteCacheRangeFromLegacyCache(code string) (*time.Time, *time.Time, error) {
	if !legacyMinuteBarTableAvailable() {
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

func mergeMinuteCacheRange(base, next minuteCacheRange) minuteCacheRange {
	if next.Start != nil {
		start := normalizeMinuteTime(next.Start.In(cnLocation()))
		if !start.IsZero() && (base.Start == nil || start.Before(*base.Start)) {
			base.Start = &start
		}
	}
	if next.End != nil {
		end := normalizeMinuteTime(next.End.In(cnLocation()))
		if !end.IsZero() && (base.End == nil || end.After(*base.End)) {
			base.End = &end
		}
	}
	return base
}

func mergeMinuteCacheRangeMaps(base, next map[string]minuteCacheRange) map[string]minuteCacheRange {
	if base == nil {
		base = make(map[string]minuteCacheRange, len(next))
	}
	for code, rng := range next {
		code = strings.ToUpper(strings.TrimSpace(code))
		if code == "" {
			continue
		}
		base[code] = mergeMinuteCacheRange(base[code], rng)
	}
	return base
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

	out, err := loadMinuteCacheRangeMapFromMinuteDB(normalizedCodes)
	if err != nil {
		return nil, err
	}
	legacyOut, err := loadMinuteCacheRangeMapFromLegacyCache(normalizedCodes)
	if err != nil {
		return nil, err
	}
	return mergeMinuteCacheRangeMaps(out, legacyOut), nil
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

func loadMinuteCacheRangeMapFromLegacyCache(normalizedCodes []string) (map[string]minuteCacheRange, error) {
	type row struct {
		StockCode string `gorm:"column:stock_code"`
		Start     string `gorm:"column:start_time"`
		End       string `gorm:"column:end_time"`
	}
	out := make(map[string]minuteCacheRange)
	if !legacyMinuteBarTableAvailable() {
		return out, nil
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
	if legacyMinuteBarTableAvailable() {
		if err := db.Dao.Where("stock_code = ?", code).Delete(&models.AiRecommendMinuteBar{}).Error; err != nil {
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
		if legacyMinuteBarTableAvailable() {
			if err := db.Dao.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&models.AiRecommendMinuteBar{}).Error; err != nil {
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
		if legacyMinuteBarTableAvailable() {
			if err := db.Dao.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&models.AiRecommendMinuteBar{}).Error; err != nil {
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
	if legacyMinuteBarTableAvailable() {
		if err := db.Dao.Where("stock_code NOT IN ?", normalized).Delete(&models.AiRecommendMinuteBar{}).Error; err != nil {
			return err
		}
	}
	clearMinuteCoverageStatsCache()
	return nil
}

func minuteCacheDualWriteEnabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("GO_STOCK_MINUTE_DUAL_WRITE")))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

func legacyMinuteBarTableAvailable() bool {
	if db.Dao == nil {
		return false
	}
	return db.Dao.Migrator().HasTable(&models.AiRecommendMinuteBar{})
}
