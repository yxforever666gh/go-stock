package research2

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	LimitOutcomePending     = "pending"
	LimitOutcomeComplete    = "complete"
	LimitOutcomeUnavailable = "unavailable"

	LimitOutcomeSealed    = "sealed"
	LimitOutcomeBroken    = "broken"
	LimitOutcomeUntouched = "untouched"

	DailyValuationComplete    = "complete"
	DailyValuationUnavailable = "unavailable"
)

type PerformanceBar struct {
	At     time.Time
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Source string
}

type BuyDayMarketData struct {
	PreviousClose    float64
	LimitRate        float64
	NoLimitReason    string
	Bars             []PerformanceBar
	SourceStatusJSON string
}

type DailyClose struct {
	TradingDate string
	Close       float64
	Source      string
}

type PerformanceHistoryProvider interface {
	BuyDayData(context.Context, Recommendation) (BuyDayMarketData, error)
	DailyCloses(context.Context, string, time.Time, time.Time) ([]DailyClose, string, error)
}

type PortfolioQuery struct {
	Slots []string
	From  string
	To    string
}

type PerformanceRecommendationQuery struct {
	PortfolioQuery
	BoughtOnly bool
	Limit      int
	Offset     int
}

type BuyDayOutcomeEvaluation struct {
	Outcome          string
	SourceStatusJSON string
}

func UpperLimitPrice(previousClose, rate float64) float64 {
	if previousClose <= 0 || rate <= 0 || math.IsNaN(previousClose) || math.IsInf(previousClose, 0) {
		return 0
	}
	previousCloseCents := int64(math.Floor(previousClose*100 + 0.5))
	rateBasisPoints := int64(math.Floor(rate*10000 + 0.5))
	limitCents := (previousCloseCents*(10000+rateBasisPoints) + 5000) / 10000
	return float64(limitCents) / 100
}

// MainlandLimitRate covers the boards currently accepted by the strategy.
// listingTradingDays is one-based; the first five sessions have no limit.
func MainlandLimitRate(code, name string, listingTradingDays int) (float64, bool) {
	if listingTradingDays > 0 && listingTradingDays <= 5 {
		return 0, false
	}
	normalized := strings.ToLower(strings.TrimSpace(code))
	upperName := strings.ToUpper(strings.TrimSpace(name))
	if strings.HasPrefix(upperName, "ST") || strings.HasPrefix(upperName, "*ST") {
		return 0.05, true
	}
	digits := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(normalized, "sh"), "sz"), "bj")
	switch {
	case strings.HasPrefix(digits, "300"), strings.HasPrefix(digits, "301"), strings.HasPrefix(digits, "688"), strings.HasPrefix(digits, "689"):
		return 0.20, true
	case strings.HasPrefix(digits, "4"), strings.HasPrefix(digits, "8"):
		return 0.30, true
	default:
		return 0.10, true
	}
}

func ClassifyBuyDayLimitOutcome(item Recommendation, data BuyDayMarketData) (BuyDayOutcomeEvaluation, error) {
	if item.BuyAt == nil || item.BuyAt.IsZero() {
		return BuyDayOutcomeEvaluation{}, errors.New("actual buy time is unavailable")
	}
	if strings.TrimSpace(data.NoLimitReason) != "" {
		return BuyDayOutcomeEvaluation{}, errors.New(data.NoLimitReason)
	}
	limitPrice := UpperLimitPrice(data.PreviousClose, data.LimitRate)
	if limitPrice <= 0 {
		return BuyDayOutcomeEvaluation{}, errors.New("verified buy-day upper limit is unavailable")
	}
	localBuy := item.BuyAt.In(shanghai())
	firstEnd := localBuy.Truncate(time.Minute).Add(time.Minute)
	if !localBuy.Equal(localBuy.Truncate(time.Minute)) {
		firstEnd = firstEnd.Add(time.Minute)
	}
	closeAt := time.Date(localBuy.Year(), localBuy.Month(), localBuy.Day(), 15, 0, 0, 0, shanghai())
	if !firstEnd.Before(closeAt) && !firstEnd.Equal(closeAt) {
		return BuyDayOutcomeEvaluation{}, errors.New("buy occurred after the final complete minute")
	}
	byMinute := make(map[int64]PerformanceBar, len(data.Bars))
	for _, bar := range data.Bars {
		at := bar.At.In(shanghai()).Truncate(time.Minute)
		if at.Format("2006-01-02") != localBuy.Format("2006-01-02") || at.Before(firstEnd) || at.After(closeAt) || !validPerformanceBar(bar) {
			continue
		}
		byMinute[at.Unix()] = bar
	}
	expected := expectedBuyDayMinuteEnds(firstEnd, closeAt)
	for _, at := range expected {
		if _, ok := byMinute[at.Unix()]; !ok {
			return BuyDayOutcomeEvaluation{}, fmt.Errorf("buy-day minute coverage is incomplete at %s", at.Format("15:04"))
		}
	}
	last, ok := byMinute[closeAt.Unix()]
	if !ok {
		return BuyDayOutcomeEvaluation{}, errors.New("buy-day closing minute is unavailable")
	}
	touched := false
	for _, at := range expected {
		if byMinute[at.Unix()].High >= limitPrice-0.001 {
			touched = true
			break
		}
	}
	outcome := LimitOutcomeUntouched
	if touched && last.Close >= limitPrice-0.001 {
		outcome = LimitOutcomeSealed
	} else if touched {
		outcome = LimitOutcomeBroken
	}
	return BuyDayOutcomeEvaluation{Outcome: outcome, SourceStatusJSON: defaultJSONList(data.SourceStatusJSON)}, nil
}

func expectedBuyDayMinuteEnds(first, last time.Time) []time.Time {
	result := make([]time.Time, 0, 240)
	for at := first; !at.After(last); at = at.Add(time.Minute) {
		minute := at.Hour()*60 + at.Minute()
		if minute > 11*60+30 && minute < 13*60+1 {
			continue
		}
		result = append(result, at)
	}
	return result
}

func validPerformanceBar(bar PerformanceBar) bool {
	return !bar.At.IsZero() && bar.Open > 0 && bar.High > 0 && bar.Low > 0 && bar.Close > 0 && bar.High >= bar.Low
}

func defaultJSONList(value string) string {
	if strings.TrimSpace(value) == "" {
		return "[]"
	}
	return value
}

func normalizePortfolioSlots(values []string) ([]string, error) {
	selected := make(map[string]struct{}, len(values))
	for _, raw := range values {
		slot := strings.TrimSpace(raw)
		if !ValidSlot(slot) {
			return nil, fmt.Errorf("invalid research2 slot: %s", raw)
		}
		selected[slot] = struct{}{}
	}
	if len(selected) == 0 {
		return nil, errors.New("at least one research2 slot is required")
	}
	result := make([]string, 0, len(selected))
	for _, slot := range Slots() {
		if _, ok := selected[slot]; ok {
			result = append(result, slot)
		}
	}
	return result, nil
}

func sortPerformanceBars(values []PerformanceBar) {
	sort.SliceStable(values, func(i, j int) bool { return values[i].At.Before(values[j].At) })
}
