package research2

import (
	"context"
	"errors"
	"math"
	"sort"
	"time"

	"gorm.io/gorm"
)

func NormalizePortfolioQuery(query PortfolioQuery) (PortfolioQuery, error) {
	slots, err := normalizePortfolioSlots(query.Slots)
	if err != nil {
		return PortfolioQuery{}, err
	}
	from, err := parseOptionalTradingDate(query.From)
	if err != nil {
		return PortfolioQuery{}, err
	}
	to, err := parseOptionalTradingDate(query.To)
	if err != nil {
		return PortfolioQuery{}, err
	}
	if !from.IsZero() && !to.IsZero() && from.After(to) {
		return PortfolioQuery{}, errors.New("performance from date must not be after to date")
	}
	result := PortfolioQuery{Slots: slots}
	if !from.IsZero() {
		result.From = from.Format("2006-01-02")
	}
	if !to.IsZero() {
		result.To = to.Format("2006-01-02")
	}
	return result, nil
}

func parseOptionalTradingDate(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.ParseInLocation("2006-01-02", value, shanghai())
	if err != nil {
		return time.Time{}, errors.New("performance dates must use YYYY-MM-DD")
	}
	return parsed, nil
}

func (r *Repository) ListPerformanceRecommendations(ctx context.Context, query PerformanceRecommendationQuery) ([]Recommendation, error) {
	normalized, err := NormalizePortfolioQuery(query.PortfolioQuery)
	if err != nil {
		return nil, err
	}
	database := r.db.WithContext(ctx).Model(&Recommendation{}).Where("slot IN ?", normalized.Slots)
	if query.BoughtOnly {
		database = database.Where("buy_at IS NOT NULL")
	}
	database = applyRecommendationBuyDateRange(database, normalized.From, normalized.To)
	limit := query.Limit
	if limit <= 0 {
		limit = -1
	}
	var items []Recommendation
	if err = database.Order("buy_at DESC, id DESC").Limit(limit).Offset(max(0, query.Offset)).Find(&items).Error; err != nil {
		return nil, err
	}
	for index := range items {
		enrichLiveRecommendation(&items[index])
	}
	return items, nil
}

func applyRecommendationBuyDateRange(database *gorm.DB, from, to string) *gorm.DB {
	if from != "" {
		start, _ := time.ParseInLocation("2006-01-02", from, shanghai())
		database = database.Where("buy_at >= ?", start)
	}
	if to != "" {
		end, _ := time.ParseInLocation("2006-01-02", to, shanghai())
		database = database.Where("buy_at < ?", end.AddDate(0, 0, 1))
	}
	return database
}

func (r *Repository) PortfolioPerformance(ctx context.Context, query PortfolioQuery) (PortfolioPerformance, error) {
	normalized, err := NormalizePortfolioQuery(query)
	if err != nil {
		return PortfolioPerformance{}, err
	}
	result := PortfolioPerformance{
		Slots: normalized.Slots, From: normalized.From, To: normalized.To,
		SelectedAccountCount: len(normalized.Slots), Curve: []PortfolioReturnPoint{},
	}
	if err = r.populatePortfolioTradeStatistics(ctx, normalized, &result); err != nil {
		return result, err
	}
	if err = r.populatePortfolioReturns(ctx, normalized, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (r *Repository) populatePortfolioTradeStatistics(ctx context.Context, query PortfolioQuery, result *PortfolioPerformance) error {
	base := r.db.WithContext(ctx).Model(&Recommendation{}).Where("slot IN ? AND buy_at IS NOT NULL", query.Slots)
	base = applyRecommendationBuyDateRange(base, query.From, query.To)
	if err := base.Session(&gorm.Session{}).Count(&result.BoughtTrades).Error; err != nil {
		return err
	}
	if err := base.Session(&gorm.Session{}).Where("status = ?", "closed").Count(&result.ClosedTrades).Error; err != nil {
		return err
	}
	if err := base.Session(&gorm.Session{}).Where("status = ? AND net_pn_l > 0", "closed").Count(&result.WinningTrades).Error; err != nil {
		return err
	}
	if result.ClosedTrades > 0 {
		value := float64(result.WinningTrades) / float64(result.ClosedTrades)
		result.WinRate = &value
	}
	counts := map[string]*OutcomeMetric{
		LimitOutcomeSealed: &result.Sealed, LimitOutcomeBroken: &result.Broken, LimitOutcomeUntouched: &result.Untouched,
	}
	for outcome, metric := range counts {
		if err := base.Session(&gorm.Session{}).Where("buy_day_limit_status = ? AND buy_day_limit_outcome = ?", LimitOutcomeComplete, outcome).Count(&metric.Count).Error; err != nil {
			return err
		}
		result.ClassifiedTrades += metric.Count
	}
	result.PendingOutcomeCount = max(int64(0), result.BoughtTrades-result.ClassifiedTrades)
	if result.ClassifiedTrades > 0 {
		for _, metric := range counts {
			value := float64(metric.Count) / float64(result.ClassifiedTrades)
			metric.Rate = &value
		}
	}
	return nil
}

type accountPeriodReturn struct {
	slot       string
	values     map[string]float64
	period     float64
	incomplete bool
	active     bool
}

func (r *Repository) populatePortfolioReturns(ctx context.Context, query PortfolioQuery, result *PortfolioPerformance) error {
	accounts := make([]accountPeriodReturn, 0, len(query.Slots))
	curveDates := map[string]struct{}{}
	for _, slot := range query.Slots {
		account, err := r.accountPeriodReturn(ctx, slot, query.From, query.To)
		if err != nil {
			return err
		}
		if !account.active {
			result.NoActivityAccountCount++
			continue
		}
		if account.incomplete {
			result.IncompleteAccountCount++
			continue
		}
		result.EffectiveAccountCount++
		accounts = append(accounts, account)
		for date := range account.values {
			curveDates[date] = struct{}{}
		}
	}
	if len(accounts) == 0 {
		return nil
	}
	total := 0.0
	for _, account := range accounts {
		total += account.period
	}
	period := total / float64(len(accounts))
	result.PeriodReturn = &period
	dates := make([]string, 0, len(curveDates))
	for date := range curveDates {
		dates = append(dates, date)
	}
	sort.Strings(dates)
	latest := make(map[string]float64, len(accounts))
	for _, date := range dates {
		sum, count := 0.0, 0
		for _, account := range accounts {
			if value, ok := account.values[date]; ok {
				latest[account.slot] = value
			}
			if value, ok := latest[account.slot]; ok {
				sum += value
				count++
			}
		}
		if count > 0 {
			result.Curve = append(result.Curve, PortfolioReturnPoint{TradingDate: date, ReturnRate: sum / float64(count), EffectiveAccountCount: count, IncompleteAccountCount: result.IncompleteAccountCount})
		}
	}
	return nil
}

func (r *Repository) accountPeriodReturn(ctx context.Context, slot, from, to string) (accountPeriodReturn, error) {
	result := accountPeriodReturn{slot: slot, values: map[string]float64{}}
	activity := r.db.WithContext(ctx).Model(&Recommendation{}).Where("slot = ? AND buy_at IS NOT NULL", slot)
	if to != "" {
		end, _ := time.ParseInLocation("2006-01-02", to, shanghai())
		activity = activity.Where("buy_at < ?", end.AddDate(0, 0, 1))
	}
	if from != "" {
		start, _ := time.ParseInLocation("2006-01-02", from, shanghai())
		activity = activity.Where("sell_at IS NULL OR sell_at >= ?", start)
	}
	var activeCount int64
	if err := activity.Count(&activeCount).Error; err != nil {
		return result, err
	}
	result.active = activeCount > 0
	if !result.active {
		return result, nil
	}
	query := r.db.WithContext(ctx).Where("slot = ?", slot)
	if from != "" {
		query = query.Where("trading_date >= ?", from)
	}
	if to != "" {
		query = query.Where("trading_date <= ?", to)
	}
	var rows []AccountDailyValuation
	if err := query.Order("trading_date ASC").Find(&rows).Error; err != nil {
		return result, err
	}
	if len(rows) == 0 {
		result.incomplete = true
		return result, nil
	}
	wealth := 1.0
	for _, row := range rows {
		if row.DataStatus != DailyValuationComplete || row.DailyReturn == nil || math.IsNaN(*row.DailyReturn) || math.IsInf(*row.DailyReturn, 0) {
			result.incomplete = true
			return result, nil
		}
		wealth *= math.Max(0, 1+*row.DailyReturn)
		result.values[row.TradingDate] = wealth - 1
	}
	result.period = wealth - 1
	return result, nil
}
