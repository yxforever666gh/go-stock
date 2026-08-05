package data

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"go-stock/backend/marketdata"
	"go-stock/backend/models"

	"gorm.io/gorm"
)

// CompatibilityMarketDataReader exposes existing caches through the new
// provider-neutral read contracts. It never fetches remote data and it never
// presents a row as visible before the row's actual persistence timestamp.
type CompatibilityMarketDataReader struct {
	mainDB   *gorm.DB
	minuteDB *gorm.DB
}

func NewCompatibilityMarketDataReader(mainDB, minuteDB *gorm.DB) CompatibilityMarketDataReader {
	return CompatibilityMarketDataReader{mainDB: mainDB, minuteDB: minuteDB}
}

func (r CompatibilityMarketDataReader) DailyBars(ctx context.Context, request marketdata.DailyBarsRequest) ([]marketdata.DailyBar, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateCompatibilityRange(request.Symbol, request.Start, request.End, request.AsOf); err != nil {
		return nil, err
	}
	if r.mainDB == nil {
		return nil, fmt.Errorf("%w: primary cache is not configured", marketdata.ErrObservationUnavailable)
	}

	code := normalizeRecommendStockCode(request.Symbol)
	rows := make([]models.AiRecommendDailyBar, 0)
	err := r.mainDB.WithContext(ctx).Model(&models.AiRecommendDailyBar{}).
		Where("stock_code = ? AND trade_date >= ? AND trade_date <= ?", code, normalizeDailyTradeDate(request.Start), normalizeDailyTradeDate(request.End)).
		Order("trade_date ASC, id ASC").Find(&rows).Error
	if err != nil {
		return nil, err
	}

	result := make([]marketdata.DailyBar, 0, len(rows))
	for _, row := range rows {
		tradeDate := normalizeDailyTradeDate(row.TradeDate)
		sourceAt := tradeDate.Add(15 * time.Hour)
		availableAt := compatibilityPersistedAt(row.CreatedAt, row.UpdatedAt)
		if marketdata.ValidateTimeline(sourceAt, availableAt, request.AsOf) != nil {
			continue
		}
		result = append(result, marketdata.DailyBar{
			Symbol: code, TradeDate: tradeDate, Open: row.Open, High: row.High,
			Low: row.Low, Close: row.Close, Volume: row.Volume, Amount: row.Amount,
			Adjustment: compatibilityAdjustment(row.Source), Completed: true,
			Source: strings.TrimSpace(row.Source), SourceAt: sourceAt, AvailableAt: availableAt,
		})
	}
	return result, nil
}

func (r CompatibilityMarketDataReader) MinuteBars(ctx context.Context, request marketdata.MinuteBarsRequest) ([]marketdata.MinuteBar, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateCompatibilityRange(request.Symbol, request.Start, request.End, request.AsOf); err != nil {
		return nil, err
	}
	if r.mainDB == nil && r.minuteDB == nil {
		return nil, fmt.Errorf("%w: minute caches are not configured", marketdata.ErrObservationUnavailable)
	}

	code := normalizeRecommendStockCode(request.Symbol)
	byMinute := make(map[int64]marketdata.MinuteBar)
	if r.mainDB != nil && r.mainDB.Migrator().HasTable(&models.AiRecommendMinuteBar{}) {
		rows := make([]models.AiRecommendMinuteBar, 0)
		if err := r.mainDB.WithContext(ctx).Model(&models.AiRecommendMinuteBar{}).
			Where("stock_code = ? AND trade_time >= ? AND trade_time <= ?", code, request.Start, request.End).
			Order("trade_time ASC, id ASC").Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			availableAt := compatibilityPersistedAt(row.CreatedAt, row.UpdatedAt)
			if bar, ok := compatibilityMinuteBar(code, row.TradeTime, row.Open, row.High, row.Low, row.Close, row.Volume, row.Amount, row.Source, availableAt, request.AsOf); ok {
				byMinute[minuteTimeMillis(bar.Start)] = bar
			}
		}
	}
	if r.minuteDB != nil && r.minuteDB.Migrator().HasTable(&minuteCacheDBBar{}) {
		rows := make([]minuteCacheDBBar, 0)
		if err := r.minuteDB.WithContext(ctx).Model(&minuteCacheDBBar{}).
			Where("stock_code = ? AND trade_time >= ? AND trade_time <= ?", code, minuteTimeMillis(request.Start), minuteTimeMillis(request.End)).
			Order("trade_time ASC").Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			availableAt := time.Time{}
			if row.UpdatedAt > 0 {
				availableAt = time.UnixMilli(row.UpdatedAt).In(cnLocation())
			}
			if bar, ok := compatibilityMinuteBar(code, minuteTimeFromMillis(row.TradeTime), row.Open, row.High, row.Low, row.Close, row.Volume, row.Amount, row.Source, availableAt, request.AsOf); ok {
				// The dedicated minute database is authoritative when both caches
				// contain the same completed bar.
				byMinute[minuteTimeMillis(bar.Start)] = bar
			}
		}
	}

	result := make([]marketdata.MinuteBar, 0, len(byMinute))
	for _, bar := range byMinute {
		result = append(result, bar)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Start.Before(result[j].Start) })
	dayIndexes := make(map[string]int)
	for index := range result {
		day := result[index].Start.In(cnLocation()).Format(time.DateOnly)
		dayIndex, exists := dayIndexes[day]
		if !exists {
			dayIndex = len(dayIndexes)
			dayIndexes[day] = dayIndex
		}
		result[index].Index = index
		result[index].TradeDayIndex = dayIndex
	}
	return result, nil
}

func (r CompatibilityMarketDataReader) Quote(ctx context.Context, symbol string, asOf time.Time) (marketdata.Quote, error) {
	if err := ctx.Err(); err != nil {
		return marketdata.Quote{}, err
	}
	if err := marketdata.ValidateSymbol(symbol); err != nil || asOf.IsZero() {
		return marketdata.Quote{}, fmt.Errorf("%w: symbol and asOf are required", marketdata.ErrInvalidObservation)
	}
	if r.mainDB == nil || !r.mainDB.Migrator().HasTable(&StockInfo{}) {
		return marketdata.Quote{}, fmt.Errorf("%w: quote cache is not configured", marketdata.ErrObservationUnavailable)
	}

	variants := compatibilitySymbolVariants(symbol)
	rows := make([]StockInfo, 0, len(variants))
	if err := r.mainDB.WithContext(ctx).Model(&StockInfo{}).
		Where("UPPER(code) IN ?", variants).
		Order("updated_at DESC, created_at DESC, id DESC").Find(&rows).Error; err != nil {
		return marketdata.Quote{}, err
	}
	for _, row := range rows {
		sourceAt, ok := compatibilityQuoteTime(row.Date, row.Time)
		availableAt := compatibilityPersistedAt(row.CreatedAt, row.UpdatedAt)
		if !ok || marketdata.ValidateTimeline(sourceAt, availableAt, asOf) != nil {
			continue
		}
		price, priceOK := compatibilityFloat(row.Price)
		if !priceOK || price <= 0 {
			continue
		}
		open, _ := compatibilityFloat(row.Open)
		previousClose, _ := compatibilityFloat(row.PreClose)
		high, _ := compatibilityFloat(row.High)
		low, _ := compatibilityFloat(row.Low)
		volume, _ := compatibilityFloat(row.Volume)
		amount, _ := compatibilityFloat(row.Amount)
		return marketdata.Quote{
			Symbol: normalizeRecommendStockCode(symbol), Name: strings.TrimSpace(row.Name),
			Price: price, Open: open, PreviousClose: previousClose, High: high, Low: low,
			Volume: volume, Amount: amount, ObservedAt: sourceAt,
			Source: "legacy_stock_info_cache", SourceAt: sourceAt, AvailableAt: availableAt,
		}, nil
	}
	return marketdata.Quote{}, fmt.Errorf("%w: quote for %s at %s", marketdata.ErrObservationUnavailable, symbol, asOf.Format(time.RFC3339))
}

func (r CompatibilityMarketDataReader) SecurityState(ctx context.Context, symbol string, asOf time.Time) (marketdata.SecurityState, error) {
	if err := ctx.Err(); err != nil {
		return marketdata.SecurityState{}, err
	}
	if err := marketdata.ValidateSymbol(symbol); err != nil || asOf.IsZero() {
		return marketdata.SecurityState{}, fmt.Errorf("%w: symbol and asOf are required", marketdata.ErrInvalidObservation)
	}
	if r.mainDB == nil {
		return marketdata.SecurityState{}, fmt.Errorf("%w: security cache is not configured", marketdata.ErrObservationUnavailable)
	}
	code := normalizeRecommendStockCode(symbol)
	if r.mainDB.Migrator().HasTable(&models.SecurityMasterHistory{}) {
		rows := make([]models.SecurityMasterHistory, 0)
		err := r.mainDB.WithContext(ctx).Model(&models.SecurityMasterHistory{}).
			Where("symbol = ?", code).
			Order("effective_from DESC, frozen_at DESC, id DESC").Find(&rows).Error
		if err != nil {
			return marketdata.SecurityState{}, err
		}
		for _, row := range rows {
			if row.FrozenAt == nil || row.FrozenAt.IsZero() || row.FrozenAt.After(asOf) || row.EffectiveFrom.After(asOf) ||
				(row.EffectiveTo != nil && !row.EffectiveTo.After(asOf)) || strings.TrimSpace(row.SnapshotHash) == "" {
				continue
			}
			return compatibilityFrozenSecurityState(row), nil
		}
	}
	if !r.mainDB.Migrator().HasTable(&StockBasic{}) {
		return marketdata.SecurityState{}, fmt.Errorf("%w: security state for %s", marketdata.ErrObservationUnavailable, code)
	}
	var basic StockBasic
	if err := r.mainDB.WithContext(ctx).Model(&StockBasic{}).
		Where("UPPER(ts_code) = ?", strings.ToUpper(code)).
		Order("updated_at DESC, created_at DESC, id DESC").First(&basic).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return marketdata.SecurityState{}, fmt.Errorf("%w: security state for %s", marketdata.ErrObservationUnavailable, code)
		}
		return marketdata.SecurityState{}, err
	}
	availableAt := compatibilityPersistedAt(basic.CreatedAt, basic.UpdatedAt)
	if marketdata.ValidateTimeline(availableAt, availableAt, asOf) != nil {
		return marketdata.SecurityState{}, fmt.Errorf("%w: security state for %s was not visible at %s", marketdata.ErrObservationUnavailable, code, asOf.Format(time.RFC3339))
	}
	return compatibilityBasicSecurityState(basic, availableAt), nil
}

func validateCompatibilityRange(symbol string, start, end, asOf time.Time) error {
	if err := marketdata.ValidateSymbol(symbol); err != nil {
		return err
	}
	if start.IsZero() || end.IsZero() || asOf.IsZero() || end.Before(start) || end.After(asOf) {
		return fmt.Errorf("%w: require start <= end <= asOf", marketdata.ErrInvalidObservation)
	}
	return nil
}

func compatibilityMinuteBar(symbol string, start time.Time, open, high, low, close, volume, amount float64, source string, availableAt, asOf time.Time) (marketdata.MinuteBar, bool) {
	start = normalizeMinuteTime(start).In(cnLocation())
	end := start.Add(time.Minute)
	if start.IsZero() || end.After(asOf) || marketdata.ValidateTimeline(end, availableAt, asOf) != nil {
		return marketdata.MinuteBar{}, false
	}
	return marketdata.MinuteBar{
		Symbol: symbol, IntervalMinutes: 1, Start: start, End: end,
		Open: open, High: high, Low: low, Close: close, Volume: volume, Amount: amount,
		Completed: true, Source: strings.TrimSpace(source), SourceAt: end, AvailableAt: availableAt,
	}, true
}

func compatibilityPersistedAt(createdAt, updatedAt time.Time) time.Time {
	if !updatedAt.IsZero() {
		return updatedAt
	}
	return createdAt
}

func compatibilityAdjustment(source string) marketdata.Adjustment {
	source = strings.ToLower(strings.TrimSpace(source))
	switch {
	case strings.Contains(source, "qfq"), strings.Contains(source, "forward"):
		return marketdata.AdjustmentForward
	case strings.Contains(source, "unadjusted"), strings.Contains(source, "none"):
		return marketdata.AdjustmentNone
	default:
		return marketdata.AdjustmentUnknown
	}
}

func compatibilityQuoteTime(dateText, timeText string) (time.Time, bool) {
	raw := strings.TrimSpace(dateText) + " " + strings.TrimSpace(timeText)
	for _, layout := range []string{
		time.DateTime, "2006-01-02 15:04", "2006-01-02 150405",
		"2006/01/02 15:04:05", "2006/01/02 15:04", "20060102 150405", "20060102 15:04:05",
	} {
		if parsed, err := time.ParseInLocation(layout, strings.TrimSpace(raw), cnLocation()); err == nil && !parsed.IsZero() {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func compatibilityFloat(raw string) (float64, bool) {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	return value, err == nil
}

func compatibilitySymbolVariants(symbol string) []string {
	canonical := normalizeRecommendStockCode(symbol)
	digits := RemoveAllNonDigitChar(canonical)
	variants := []string{strings.ToUpper(strings.TrimSpace(symbol)), strings.ToUpper(canonical)}
	if len(digits) == 6 {
		variants = append(variants, digits)
		suffix := ""
		if parts := strings.Split(strings.ToUpper(canonical), "."); len(parts) == 2 {
			suffix = parts[1]
		}
		if suffix == "SH" || suffix == "SZ" || suffix == "BJ" {
			variants = append(variants, suffix+digits, digits+"."+suffix)
		}
	}
	seen := make(map[string]struct{}, len(variants))
	result := make([]string, 0, len(variants))
	for _, variant := range variants {
		variant = strings.ToUpper(strings.TrimSpace(variant))
		if variant == "" {
			continue
		}
		if _, exists := seen[variant]; exists {
			continue
		}
		seen[variant] = struct{}{}
		result = append(result, variant)
	}
	return result
}

func compatibilityFrozenSecurityState(row models.SecurityMasterHistory) marketdata.SecurityState {
	availableAt := *row.FrozenAt
	status := compatibilityTradingStatus(row.Status, row.IsSuspended)
	return marketdata.SecurityState{
		Symbol: row.Symbol, Name: row.Name, Market: row.Market, Exchange: row.Exchange,
		Board: row.Board, Sector: row.Sector, Industry: row.Industry, Currency: row.Currency,
		Status: status, ST: row.IsST, ListedAt: cloneCompatibilityTime(row.ListedAt),
		DelistedAt: cloneCompatibilityTime(row.DelistedAt), EffectiveFrom: row.EffectiveFrom,
		EffectiveTo: cloneCompatibilityTime(row.EffectiveTo), Source: row.Source,
		SourceAt: row.EffectiveFrom, AvailableAt: availableAt,
	}
}

func compatibilityBasicSecurityState(row StockBasic, availableAt time.Time) marketdata.SecurityState {
	listedAt, _ := parseMarketSummaryV150ListDate(row.ListDate)
	delistedAt, _ := parseMarketSummaryV150ListDate(row.DelistDate)
	var listedPointer, delistedPointer *time.Time
	if !listedAt.IsZero() {
		listedPointer = &listedAt
	}
	if !delistedAt.IsZero() {
		delistedPointer = &delistedAt
	}
	return marketdata.SecurityState{
		Symbol: normalizeRecommendStockCode(row.TsCode), Name: strings.TrimSpace(row.Name),
		Market: strings.TrimSpace(row.Market), Exchange: strings.TrimSpace(row.Exchange),
		Board: strings.TrimSpace(row.Market), Sector: strings.TrimSpace(row.BKName),
		Industry: strings.TrimSpace(row.Industry), Currency: strings.TrimSpace(row.CurrType),
		Status: compatibilityTradingStatus(row.ListStatus, false), ST: marketSummaryV150SecurityNameIsST(row.Name),
		ListedAt: listedPointer, DelistedAt: delistedPointer, EffectiveFrom: availableAt,
		Source: "legacy_stock_basic_cache", SourceAt: availableAt, AvailableAt: availableAt,
	}
}

func compatibilityTradingStatus(raw string, suspended bool) marketdata.TradingStatus {
	if suspended {
		return marketdata.TradingStatusSuspended
	}
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "L", "LISTED", "TRADABLE":
		return marketdata.TradingStatusTradable
	case "P", "S", "SUSPENDED":
		return marketdata.TradingStatusSuspended
	case "D", "DELISTED":
		return marketdata.TradingStatusDelisted
	default:
		return marketdata.TradingStatusUnknown
	}
}

func cloneCompatibilityTime(value *time.Time) *time.Time {
	if value == nil || value.IsZero() {
		return nil
	}
	cloned := *value
	return &cloned
}

var (
	_ marketdata.DailyBarReader      = CompatibilityMarketDataReader{}
	_ marketdata.MinuteBarReader     = CompatibilityMarketDataReader{}
	_ marketdata.QuoteReader         = CompatibilityMarketDataReader{}
	_ marketdata.SecurityStateReader = CompatibilityMarketDataReader{}
)
