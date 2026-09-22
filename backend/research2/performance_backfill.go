package research2

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"time"

	"go-stock/internal/trading"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type PerformanceBackfillResult struct {
	OutcomeCandidates     int `json:"outcomeCandidates"`
	OutcomesCompleted     int `json:"outcomesCompleted"`
	OutcomesUnavailable   int `json:"outcomesUnavailable"`
	ValuationsCompleted   int `json:"valuationsCompleted"`
	ValuationsUnavailable int `json:"valuationsUnavailable"`
}

type PerformanceBackfillService struct {
	repository *Repository
	provider   PerformanceHistoryProvider
	calendar   Calendar
	now        func() time.Time
}

func NewPerformanceBackfillService(repository *Repository, provider PerformanceHistoryProvider, calendar Calendar) *PerformanceBackfillService {
	return &PerformanceBackfillService{repository: repository, provider: provider, calendar: calendar, now: time.Now}
}

func (service *PerformanceBackfillService) BackfillAll(ctx context.Context) (PerformanceBackfillResult, error) {
	if service == nil || service.repository == nil || service.provider == nil || service.calendar == nil {
		return PerformanceBackfillResult{}, errors.New("research2 performance backfill is unavailable")
	}
	result, outcomeErr := service.backfillOutcomes(ctx, "")
	valuationResult, valuationErr := service.backfillDailyValuations(ctx)
	result.ValuationsCompleted = valuationResult.ValuationsCompleted
	result.ValuationsUnavailable = valuationResult.ValuationsUnavailable
	return result, errors.Join(outcomeErr, valuationErr)
}

func (service *PerformanceBackfillService) FinalizeDate(ctx context.Context, at time.Time) (PerformanceBackfillResult, error) {
	local := at.In(shanghai())
	date := local.Format("2006-01-02")
	result, outcomeErr := service.backfillOutcomes(ctx, date)
	valuationResult, valuationErr := service.backfillDailyValuationsBetween(ctx, local, local)
	result.ValuationsCompleted = valuationResult.ValuationsCompleted
	result.ValuationsUnavailable = valuationResult.ValuationsUnavailable
	return result, errors.Join(outcomeErr, valuationErr)
}

func (service *PerformanceBackfillService) FinalizeBuyDayOutcomes(ctx context.Context, tradingDate string) (PerformanceBackfillResult, error) {
	if service == nil || service.repository == nil || service.provider == nil {
		return PerformanceBackfillResult{}, errors.New("research2 outcome finalizer is unavailable")
	}
	return service.backfillOutcomes(ctx, tradingDate)
}

func (service *PerformanceBackfillService) backfillOutcomes(ctx context.Context, tradingDate string) (PerformanceBackfillResult, error) {
	result := PerformanceBackfillResult{}
	query := service.repository.db.WithContext(ctx).Where("buy_at IS NOT NULL")
	if tradingDate != "" {
		day, err := time.ParseInLocation("2006-01-02", tradingDate, shanghai())
		if err != nil {
			return result, err
		}
		query = query.Where("buy_at >= ? AND buy_at < ?", day, day.AddDate(0, 0, 1))
	}
	var items []Recommendation
	if err := query.Order("buy_at ASC, id ASC").Find(&items).Error; err != nil {
		return result, err
	}
	now := service.now().In(shanghai())
	errorsOut := make([]error, 0)
	for _, item := range items {
		if item.BuyAt == nil || item.BuyDayLimitStatus == LimitOutcomeComplete {
			continue
		}
		closeAt := time.Date(item.BuyAt.In(shanghai()).Year(), item.BuyAt.In(shanghai()).Month(), item.BuyAt.In(shanghai()).Day(), 15, 0, 0, 0, shanghai())
		if now.Before(closeAt) {
			continue
		}
		result.OutcomeCandidates++
		data, err := service.provider.BuyDayData(ctx, item)
		if err == nil {
			var evaluation BuyDayOutcomeEvaluation
			evaluation, err = ClassifyBuyDayLimitOutcome(item, data)
			if err == nil {
				if saveErr := service.repository.saveBuyDayOutcome(ctx, item.RecommendationID, LimitOutcomeComplete, evaluation.Outcome, evaluation.SourceStatusJSON, "", now); saveErr != nil {
					err = saveErr
				} else {
					result.OutcomesCompleted++
				}
			}
		}
		if err != nil {
			source := data.SourceStatusJSON
			if saveErr := service.repository.saveBuyDayOutcome(ctx, item.RecommendationID, LimitOutcomeUnavailable, "", source, err.Error(), now); saveErr != nil {
				errorsOut = append(errorsOut, saveErr)
			} else {
				result.OutcomesUnavailable++
			}
		}
	}
	return result, errors.Join(errorsOut...)
}

func (r *Repository) saveBuyDayOutcome(ctx context.Context, recommendationID, status, outcome, sources, reason string, at time.Time) error {
	return research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		return tx.Model(&Recommendation{}).Where("recommendation_id = ? AND buy_at IS NOT NULL", recommendationID).Updates(map[string]any{
			"buy_day_limit_status":         status,
			"buy_day_limit_outcome":        outcome,
			"buy_day_limit_evaluated_at":   at,
			"buy_day_limit_attempt_count":  gorm.Expr("buy_day_limit_attempt_count + 1"),
			"buy_day_limit_source_json":    defaultJSONList(sources),
			"buy_day_limit_failure_reason": reason,
		}).Error
	})
}

func (service *PerformanceBackfillService) backfillDailyValuations(ctx context.Context) (PerformanceBackfillResult, error) {
	result := PerformanceBackfillResult{}
	var recommendations []Recommendation
	if err := service.repository.db.WithContext(ctx).Where("buy_at IS NOT NULL").Order("buy_at ASC, id ASC").Find(&recommendations).Error; err != nil {
		return result, err
	}
	if len(recommendations) == 0 {
		return result, nil
	}
	first := recommendations[0].BuyAt.In(shanghai())
	end := service.now().In(shanghai())
	closeToday := time.Date(end.Year(), end.Month(), end.Day(), 15, 0, 0, 0, shanghai())
	if end.Before(closeToday) {
		end = end.AddDate(0, 0, -1)
	}
	return service.backfillDailyValuationsBetween(ctx, first, end)
}

func (service *PerformanceBackfillService) backfillDailyValuationsBetween(ctx context.Context, first, end time.Time) (PerformanceBackfillResult, error) {
	result := PerformanceBackfillResult{}
	var recommendations []Recommendation
	if err := service.repository.db.WithContext(ctx).Where("buy_at IS NOT NULL").Order("buy_at ASC, id ASC").Find(&recommendations).Error; err != nil {
		return result, err
	}
	if len(recommendations) == 0 {
		return result, nil
	}
	dates, err := service.tradingDates(ctx, first, end)
	if err != nil {
		return result, err
	}
	if len(dates) == 0 {
		return result, nil
	}
	activeRecommendations := make([]Recommendation, 0, len(recommendations))
	fromStart := time.Date(dates[0].Year(), dates[0].Month(), dates[0].Day(), 0, 0, 0, 0, shanghai())
	toEnd := time.Date(dates[len(dates)-1].Year(), dates[len(dates)-1].Month(), dates[len(dates)-1].Day(), 23, 59, 59, 0, shanghai())
	for _, item := range recommendations {
		if item.BuyAt != nil && !item.BuyAt.After(toEnd) && (item.SellAt == nil || !item.SellAt.Before(fromStart)) {
			activeRecommendations = append(activeRecommendations, item)
		}
	}
	closes := service.loadDailyCloses(ctx, activeRecommendations, dates[0], dates[len(dates)-1])
	errorsOut := make([]error, 0)
	for _, slot := range Slots() {
		completed, unavailable, rebuildErr := service.rebuildSlotValuations(ctx, slot, dates, recommendations, closes)
		result.ValuationsCompleted += completed
		result.ValuationsUnavailable += unavailable
		if rebuildErr != nil {
			errorsOut = append(errorsOut, rebuildErr)
		}
	}
	return result, errors.Join(errorsOut...)
}

func (service *PerformanceBackfillService) tradingDates(ctx context.Context, from, to time.Time) ([]time.Time, error) {
	result := make([]time.Time, 0)
	for day := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, shanghai()); !day.After(to); day = day.AddDate(0, 0, 1) {
		ok, err := service.calendar.IsTradingDay(ctx, day)
		if err != nil {
			return nil, err
		}
		if ok {
			result = append(result, day)
		}
	}
	return result, nil
}

func (service *PerformanceBackfillService) loadDailyCloses(ctx context.Context, recommendations []Recommendation, from, to time.Time) map[string]map[string]DailyClose {
	codes := map[string]struct{}{}
	for _, item := range recommendations {
		codes[item.StockCode] = struct{}{}
	}
	result := make(map[string]map[string]DailyClose, len(codes))
	ordered := make([]string, 0, len(codes))
	for code := range codes {
		ordered = append(ordered, code)
	}
	sort.Strings(ordered)
	for _, code := range ordered {
		rows, _, err := service.provider.DailyCloses(ctx, code, from, to)
		_ = err // Missing provider coverage is persisted per valuation, not treated as a command crash.
		result[code] = make(map[string]DailyClose, len(rows))
		for _, row := range rows {
			if row.Close > 0 {
				result[code][row.TradingDate] = row
			}
		}
	}
	return result
}

func (service *PerformanceBackfillService) rebuildSlotValuations(ctx context.Context, slot string, dates []time.Time, allRecommendations []Recommendation, closes map[string]map[string]DailyClose) (int, int, error) {
	items := make([]Recommendation, 0)
	byID := make(map[string]Recommendation)
	for _, item := range allRecommendations {
		if item.Slot == slot {
			items = append(items, item)
			byID[item.RecommendationID] = item
		}
	}
	if len(items) == 0 {
		return 0, 0, nil
	}
	var capital []AccountCapitalEvent
	if err := service.repository.db.WithContext(ctx).Where("slot = ?", slot).Order("effective_at ASC, event_id ASC").Find(&capital).Error; err != nil {
		return 0, 0, err
	}
	var trades []Trade
	if err := service.repository.db.WithContext(ctx).Where("slot = ?", slot).Order("traded_at ASC, trade_id ASC").Find(&trades).Error; err != nil {
		return 0, 0, err
	}
	cash, previousNAV := 0.0, 0.0
	positions := map[string]Trade{}
	capitalIndex, tradeIndex := 0, 0
	firstStart := time.Date(dates[0].Year(), dates[0].Month(), dates[0].Day(), 0, 0, 0, 0, shanghai())
	for capitalIndex < len(capital) && capital[capitalIndex].EffectiveAt.Before(firstStart) {
		cash += capital[capitalIndex].Amount
		capitalIndex++
	}
	for tradeIndex < len(trades) && trades[tradeIndex].TradedAt.Before(firstStart) {
		trade := trades[tradeIndex]
		cash += trade.NetCashFlow
		if trade.Side == "buy" {
			positions[trade.RecommendationID] = trade
		} else if trade.Side == "sell" {
			delete(positions, trade.RecommendationID)
		}
		tradeIndex++
	}
	previousNAV = cash
	chainReady := previousNAV > 0 && len(positions) == 0
	if len(positions) > 0 {
		var baseline AccountDailyValuation
		if err := service.repository.db.WithContext(ctx).Where("slot = ? AND trading_date < ? AND data_status = ?", slot, dates[0].Format("2006-01-02"), DailyValuationComplete).Order("trading_date DESC").First(&baseline).Error; err == nil && baseline.NetAssetValue > 0 {
			previousNAV, chainReady = baseline.NetAssetValue, true
		} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, 0, err
		}
	}
	completed, unavailable := 0, 0
	for _, day := range dates {
		dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, shanghai())
		dayClose := time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, shanghai())
		neutralFlow := 0.0
		for capitalIndex < len(capital) && !capital[capitalIndex].EffectiveAt.After(dayClose) {
			cash += capital[capitalIndex].Amount
			if !capital[capitalIndex].EffectiveAt.Before(dayStart) {
				neutralFlow += capital[capitalIndex].Amount
			}
			capitalIndex++
		}
		for tradeIndex < len(trades) && !trades[tradeIndex].TradedAt.After(dayClose) {
			trade := trades[tradeIndex]
			cash += trade.NetCashFlow
			if trade.Side == "buy" {
				positions[trade.RecommendationID] = trade
			} else if trade.Side == "sell" {
				delete(positions, trade.RecommendationID)
			}
			tradeIndex++
		}
		positionValue := 0.0
		usedSources := map[string]string{}
		missing := ""
		for recommendationID, holding := range positions {
			item := byID[recommendationID]
			close, ok := closes[item.StockCode][day.Format("2006-01-02")]
			if !ok || close.Close <= 0 {
				missing = "missing unadjusted daily close for " + item.StockCode
				break
			}
			positionValue += trading.CalculateSellCost(close.Close, holding.Quantity).NetCashFlow
			usedSources[item.StockCode] = close.Source
		}
		nav := cash + positionValue
		row := AccountDailyValuation{
			ValuationID: "daily-v2-" + slot + "-" + day.Format("20060102"), Slot: slot, TradingDate: day.Format("2006-01-02"),
			ValuedAt: dayClose, Cash: roundMoney(cash), PositionValue: roundMoney(positionValue), NetAssetValue: roundMoney(nav), NeutralFunding: roundMoney(neutralFlow),
			DataStatus: DailyValuationComplete, SourceStatusJSON: marshalSourceMap(usedSources),
		}
		if missing != "" {
			row.DataStatus, row.FailureReason = DailyValuationUnavailable, missing
			unavailable++
			chainReady = false
		} else if !chainReady || previousNAV <= 0 {
			row.DataStatus, row.FailureReason = DailyValuationUnavailable, "previous complete daily valuation is unavailable"
			unavailable++
			previousNAV = nav
			chainReady = nav > 0
		} else {
			value := (nav-neutralFlow)/previousNAV - 1
			if math.IsNaN(value) || math.IsInf(value, 0) {
				row.DataStatus, row.FailureReason = DailyValuationUnavailable, "daily return is not finite"
				unavailable++
				chainReady = false
			} else {
				row.DailyReturn = &value
				completed++
				previousNAV = nav
				chainReady = nav > 0
			}
		}
		if err := service.repository.upsertDailyValuation(ctx, row); err != nil {
			return completed, unavailable, err
		}
	}
	return completed, unavailable, nil
}

func marshalSourceMap(values map[string]string) string {
	if len(values) == 0 {
		return "[]"
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

func (r *Repository) upsertDailyValuation(ctx context.Context, row AccountDailyValuation) error {
	return research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "valuation_id"}}, DoUpdates: clause.AssignmentColumns([]string{
			"valued_at", "cash", "position_value", "net_asset_value", "neutral_funding", "daily_return", "data_status", "source_status_json", "failure_reason", "updated_at",
		})}).Create(&row).Error
	})
}
