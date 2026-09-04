package data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-stock/backend/marketdata"
	"go-stock/backend/models"
	"go-stock/backend/research2"
	"go-stock/internal/researchevidence"
	"go-stock/internal/trading"

	"gorm.io/gorm"
)

const (
	research2EvidenceProfileV7 = "research2-trailing5-v7"
	research2MinimumCoverage   = 0.95
	research2MinimumMinuteBars = 4
	research2QuoteFreshness    = 3 * time.Minute
	research2QuoteFutureSkew   = 5 * time.Second
)

type research2FullMarketSnapshot struct {
	Rows        []research2MarketRow
	Reported    int
	SourceID    string
	SourceName  string
	SourceRef   string
	AsOf        time.Time
	CollectedAt time.Time
}

type research2MinuteWindowProvider interface {
	Window(context.Context, string, time.Time, time.Time) ([]minuteBar, string, error)
}

type research2DefaultMinuteWindowProvider struct {
	stocks *StockDataApi
	cache  *ResearchChartProvider
}

func newResearch2MinuteWindowProvider(stocks *StockDataApi, minuteDB *gorm.DB) research2MinuteWindowProvider {
	quotes := NewResearchQuoteProviderWithStockData(stocks)
	return &research2DefaultMinuteWindowProvider{stocks: stocks, cache: NewResearchChartProviderWithStorage(quotes, minuteDB)}
}

func (p *research2DefaultMinuteWindowProvider) Window(ctx context.Context, code string, start, end time.Time) ([]minuteBar, string, error) {
	var failures []error
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if bars, source, err := fetchMinuteBarsWithTencentContext(ctx, code, start, end); err == nil && len(bars) > 0 {
		clean := sanitizeResearch2MinuteBars(bars, source, start, end)
		if len(clean) >= research2MinimumMinuteBars {
			return clean, source, nil
		}
		failures = append(failures, fmt.Errorf("tencent: only %d valid minute bars", len(clean)))
	} else if err != nil {
		failures = append(failures, fmt.Errorf("tencent: %w", err))
	} else {
		failures = append(failures, errors.New("tencent: empty minute response"))
	}
	if bars, source, err := fetchResearch2EastmoneyMinutes(ctx, p.stocks, code, start, end, 2); err == nil && len(bars) > 0 {
		clean := sanitizeResearch2MinuteBars(bars, source, start, end)
		if len(clean) >= research2MinimumMinuteBars {
			return clean, source, nil
		}
		failures = append(failures, fmt.Errorf("eastmoney: only %d valid minute bars", len(clean)))
	} else if err != nil {
		failures = append(failures, fmt.Errorf("eastmoney: %w", err))
	} else {
		failures = append(failures, errors.New("eastmoney: empty minute response"))
	}
	if p.cache != nil {
		snapshot, err := p.cache.LoadCached(ctx, code, start, end)
		if err == nil && len(snapshot.Bars) > 0 {
			bars := make([]minuteBar, 0, len(snapshot.Bars))
			for _, bar := range snapshot.Bars {
				bars = append(bars, minuteBar{TradeTime: bar.At, Open: bar.Open, High: bar.High, Low: bar.Low, Close: bar.Close, Volume: bar.Volume, Amount: bar.Amount, Source: bar.Source})
			}
			clean := sanitizeResearch2MinuteBars(bars, "local-minute-cache", start, end)
			if len(clean) >= research2MinimumMinuteBars {
				return clean, "local-minute-cache", nil
			}
			failures = append(failures, fmt.Errorf("local-minute-cache: only %d valid minute bars", len(clean)))
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("local-minute-cache: %w", err))
		} else {
			failures = append(failures, errors.New("local-minute-cache: empty minute response"))
		}
	}
	return nil, "", errors.Join(failures...)
}

func sanitizeResearch2MinuteBars(bars []minuteBar, source string, start, end time.Time) []minuteBar {
	result := make([]minuteBar, 0, len(bars))
	seen := make(map[int64]struct{}, len(bars))
	for _, bar := range bars {
		bar.TradeTime = normalizeMinuteTime(bar.TradeTime.In(shanghaiDataLocation()))
		// A minute timestamp is the bucket start. Keep the end exclusive so the
		// bucket containing the evidence cutoff is never partially observed.
		if bar.TradeTime.Before(start) || !bar.TradeTime.Before(end) || bar.Open <= 0 || bar.High <= 0 || bar.Low <= 0 || bar.Close <= 0 || bar.High < bar.Low {
			continue
		}
		key := bar.TradeTime.Unix()
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		if strings.TrimSpace(bar.Source) == "" {
			bar.Source = source
		}
		result = append(result, bar)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].TradeTime.Before(result[j].TradeTime) })
	return result
}

type research2CompactSource struct {
	SourceID    string     `json:"sourceId"`
	SourceName  string     `json:"sourceName"`
	Category    string     `json:"category"`
	EntityID    string     `json:"entityId,omitempty"`
	Status      string     `json:"status"`
	AvailableAt *time.Time `json:"availableAt,omitempty"`
	SourceRef   string     `json:"sourceRef,omitempty"`
	Summary     string     `json:"summary,omitempty"`
	EmptyKind   string     `json:"emptyKind,omitempty"`
}

type research2CompactMarket struct {
	SourceID     string        `json:"sourceId"`
	Observed     int           `json:"observed"`
	Reported     int           `json:"reported"`
	CoveragePct  float64       `json:"coveragePct"`
	Advances     int           `json:"advances"`
	Declines     int           `json:"declines"`
	Flat         int           `json:"flat"`
	LimitUps     int           `json:"limitUps"`
	LimitDowns   int           `json:"limitDowns"`
	MedianChange float64       `json:"medianChangePct"`
	SectorFlows  []FundFlowRow `json:"sectorFlows"`
	ConceptFlows []FundFlowRow `json:"conceptFlows"`
}

type research2CompactQuote struct {
	At            time.Time `json:"at"`
	Price         float64   `json:"price"`
	Open          float64   `json:"open"`
	PreviousClose float64   `json:"previousClose"`
	High          float64   `json:"high"`
	Low           float64   `json:"low"`
	TurnoverPct   float64   `json:"turnoverPct"`
	MainFlow      float64   `json:"mainFlow"`
	DayVolume     float64   `json:"dayVolume"`
	DayAmount     float64   `json:"dayAmount"`
}

type research2CompactMetrics struct {
	ReturnPct              *float64 `json:"returnPct"`
	VWAP                   *float64 `json:"vwap"`
	DistanceFromHighPct    *float64 `json:"distanceFromHighPct"`
	MaxDrawdownPct         *float64 `json:"maxDrawdownPct"`
	RecoveryPct            *float64 `json:"recoveryPct"`
	WindowVolume           float64  `json:"windowVolume"`
	WindowAmount           float64  `json:"windowAmount"`
	VolumeAcceleration     *float64 `json:"volumeAcceleration"`
	VWAPMethod             string   `json:"vwapMethod,omitempty"`
	DayReturnPct           *float64 `json:"dayReturnPct"`
	DayOpenReturnPct       *float64 `json:"dayOpenReturnPct"`
	DayDistanceFromHighPct *float64 `json:"dayDistanceFromHighPct"`
	AmountVs5DaySameTime   *float64 `json:"amountVs5DaySameTime,omitempty"`
	VolumeVs5DaySameTime   *float64 `json:"volumeVs5DaySameTime,omitempty"`
	HistoricalBaseline     string   `json:"historicalBaseline"`
}

type research2CompactCandidate struct {
	EntityID       string                  `json:"entityId"`
	Code           string                  `json:"code"`
	Name           string                  `json:"name"`
	CoreEligible   bool                    `json:"coreEligible"`
	Quote          *research2CompactQuote  `json:"quote,omitempty"`
	MinuteBarCount int                     `json:"minuteBarCount"`
	MinuteSource   string                  `json:"minuteSource,omitempty"`
	Metrics        research2CompactMetrics `json:"metrics"`
	SourceIDs      []string                `json:"sourceIds"`
	Missing        []string                `json:"missing,omitempty"`
}

type research2CompactSnapshot struct {
	Version         string                      `json:"version"`
	WindowStartAt   time.Time                   `json:"windowStartAt"`
	WindowEndAt     time.Time                   `json:"windowEndAt"`
	CutoffAt        time.Time                   `json:"cutoffAt"`
	FreezeAt        time.Time                   `json:"freezeAt"`
	Market          research2CompactMarket      `json:"market"`
	Candidates      []research2CompactCandidate `json:"candidates"`
	Sources         []research2CompactSource    `json:"sources"`
	Degraded        bool                        `json:"degraded"`
	DegradedReasons []string                    `json:"degradedReasons,omitempty"`
}

func research2ClosedWindowEnd(startedAt, snapshotAt time.Time) time.Time {
	location := shanghaiDataLocation()
	startedAt = startedAt.In(location)
	snapshotAt = snapshotAt.In(location)
	morningClose := time.Date(startedAt.Year(), startedAt.Month(), startedAt.Day(), 11, 30, 0, 0, location)
	afternoonOpen := time.Date(startedAt.Year(), startedAt.Month(), startedAt.Day(), 13, 0, 0, 0, location)
	startedDuringLunch := !startedAt.Before(morningClose) && startedAt.Before(afternoonOpen)
	snapshotDuringLunch := !snapshotAt.Before(morningClose) && snapshotAt.Before(afternoonOpen)
	if startedDuringLunch || snapshotDuringLunch {
		return morningClose
	}
	return snapshotAt.Truncate(time.Minute)
}

type research2CandidateWindow struct {
	Candidate researchevidence.StockCandidate
	Row       research2MarketRow
	Bars      []minuteBar
	Source    string
	Error     error
}

func collectResearch2CandidateWindows(ctx context.Context, provider research2MinuteWindowProvider, candidates []researchevidence.StockCandidate, rows []research2MarketRow, start, cutoff time.Time) []research2CandidateWindow {
	byCode := make(map[string]research2MarketRow, len(rows))
	for _, row := range rows {
		byCode[research2CanonicalCode(row.Code)] = row
	}
	result := make([]research2CandidateWindow, len(candidates))
	type indexedWindow struct {
		index int
		item  research2CandidateWindow
	}
	results := make(chan indexedWindow, len(candidates))
	for index, candidate := range candidates {
		index, candidate := index, candidate
		go func() {
			item := research2CandidateWindow{Candidate: candidate, Row: byCode[research2CanonicalCode(candidate.Code)]}
			if provider == nil {
				item.Error = errors.New("minute window provider is unavailable")
			} else {
				item.Bars, item.Source, item.Error = provider.Window(ctx, candidate.Code, start, cutoff)
				item.Bars = sanitizeResearch2MinuteBars(item.Bars, item.Source, start, cutoff)
				if item.Error == nil && len(item.Bars) < research2MinimumMinuteBars {
					item.Error = fmt.Errorf("only %d closed minute bars", len(item.Bars))
				}
			}
			results <- indexedWindow{index: index, item: item}
		}()
	}
	for range candidates {
		select {
		case value := <-results:
			result[value.index] = value.item
		case <-ctx.Done():
			for index := range result {
				if result[index].Candidate.Code == "" {
					result[index] = research2CandidateWindow{Candidate: candidates[index], Row: byCode[research2CanonicalCode(candidates[index].Code)], Error: ctx.Err()}
				}
			}
			return result
		}
	}
	return result
}

func buildResearch2CompactCandidate(item research2CandidateWindow, cutoff time.Time) research2CompactCandidate {
	code := research2CanonicalCode(item.Candidate.Code)
	result := research2CompactCandidate{EntityID: "stock:" + code, Code: code, Name: item.Candidate.Name,
		MinuteBarCount: len(item.Bars), MinuteSource: item.Source, SourceIDs: []string{"research2:quote:" + code, "research2:minutes:" + code}}
	quoteAt := time.Time{}
	if item.Row.Timestamp > 0 {
		quoteAt = time.Unix(item.Row.Timestamp, 0).In(shanghaiDataLocation())
	}
	quoteValid := item.Row.Price > 0 && !quoteAt.IsZero() && !quoteAt.After(cutoff) && cutoff.Sub(quoteAt) <= research2QuoteFreshness
	if quoteValid {
		result.Quote = &research2CompactQuote{At: quoteAt, Price: item.Row.Price, Open: item.Row.Open, PreviousClose: item.Row.PreClose,
			High: item.Row.High, Low: item.Row.Low, TurnoverPct: item.Row.Turnover, MainFlow: item.Row.MainFlow,
			DayVolume: item.Row.Volume, DayAmount: item.Row.Amount}
	} else {
		result.Missing = append(result.Missing, "fresh_quote")
	}
	if item.Error != nil {
		result.Missing = append(result.Missing, "minute_window")
	}
	if len(item.Bars) < research2MinimumMinuteBars {
		result.Missing = append(result.Missing, "minimum_4_minute_bars")
	}
	result.Metrics = calculateResearch2CompactMetrics(item.Row, item.Bars)
	result.Missing = append(result.Missing, "historical_5day_same_time_baseline")
	result.CoreEligible = quoteValid && len(item.Bars) >= research2MinimumMinuteBars
	return result
}

func calculateResearch2CompactMetrics(row research2MarketRow, bars []minuteBar) research2CompactMetrics {
	result := research2CompactMetrics{HistoricalBaseline: "unavailable"}
	if row.PreClose > 0 && row.Price > 0 {
		result.DayReturnPct = research2FloatPointer((row.Price/row.PreClose - 1) * 100)
	}
	if row.PreClose > 0 && row.Open > 0 {
		result.DayOpenReturnPct = research2FloatPointer((row.Open/row.PreClose - 1) * 100)
	}
	if row.High > 0 && row.Price > 0 {
		result.DayDistanceFromHighPct = research2FloatPointer((row.Price/row.High - 1) * 100)
	}
	if len(bars) == 0 {
		return result
	}
	first, last := bars[0], bars[len(bars)-1]
	if first.Open > 0 && last.Close > 0 {
		result.ReturnPct = research2FloatPointer((last.Close/first.Open - 1) * 100)
	}
	windowHigh, peak, troughAfterPeak := 0.0, 0.0, math.MaxFloat64
	maxDrawdown := 0.0
	firstHalfVolume, secondHalfVolume := 0.0, 0.0
	for index, bar := range bars {
		result.WindowVolume += bar.Volume
		result.WindowAmount += bar.Amount
		if index < len(bars)/2 {
			firstHalfVolume += bar.Volume
		} else {
			secondHalfVolume += bar.Volume
		}
		if bar.High > windowHigh {
			windowHigh = bar.High
		}
		if bar.High > peak {
			peak, troughAfterPeak = bar.High, bar.Low
		} else if bar.Low < troughAfterPeak {
			troughAfterPeak = bar.Low
		}
		if peak > 0 {
			drawdown := (bar.Low/peak - 1) * 100
			if drawdown < maxDrawdown {
				maxDrawdown = drawdown
			}
		}
	}
	if result.WindowAmount > 0 && result.WindowVolume > 0 {
		value := result.WindowAmount / result.WindowVolume
		low, high := bars[0].Low, bars[0].High
		for _, bar := range bars[1:] {
			low, high = math.Min(low, bar.Low), math.Max(high, bar.High)
		}
		switch {
		case value >= low*0.8 && value <= high*1.2:
			result.VWAP = research2FloatPointer(value)
			result.VWAPMethod = "amount_divided_by_share_volume"
		case value/100 >= low*0.8 && value/100 <= high*1.2:
			result.VWAP = research2FloatPointer(value / 100)
			result.VWAPMethod = "amount_divided_by_lot_volume_times_100"
		}
	}
	if result.VWAP == nil {
		// Providers do not agree on volume units and Tencent omits amount. A
		// volume-weighted close is the safe provider-independent fallback.
		weighted := 0.0
		for _, bar := range bars {
			weighted += bar.Close * bar.Volume
		}
		if result.WindowVolume > 0 {
			result.VWAP = research2FloatPointer(weighted / result.WindowVolume)
			result.VWAPMethod = "volume_weighted_minute_close_proxy"
		}
	}
	if windowHigh > 0 && last.Close > 0 {
		result.DistanceFromHighPct = research2FloatPointer((last.Close/windowHigh - 1) * 100)
	}
	result.MaxDrawdownPct = research2FloatPointer(maxDrawdown)
	if peak > 0 && troughAfterPeak < math.MaxFloat64 && peak > troughAfterPeak {
		result.RecoveryPct = research2FloatPointer((last.Close - troughAfterPeak) / (peak - troughAfterPeak) * 100)
	}
	if firstHalfVolume > 0 {
		leftCount := math.Max(1, float64(len(bars)/2))
		rightCount := math.Max(1, float64(len(bars)-len(bars)/2))
		result.VolumeAcceleration = research2FloatPointer((secondHalfVolume / rightCount) / (firstHalfVolume / leftCount))
	}
	return result
}

func research2FloatPointer(value float64) *float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	value = math.Round(value*10000) / 10000
	return &value
}

func research2CanonicalCode(value string) string {
	if normalized, ok := trading.NormalizeMainlandCode(value); ok {
		return normalized
	}
	digits := strings.TrimSpace(value)
	if len(digits) == 6 {
		if strings.HasPrefix(digits, "6") {
			return "sh" + digits
		}
		return "sz" + digits
	}
	return strings.ToLower(strings.TrimSpace(value))
}

func research2MarketBreadth(rows []research2MarketRow) BreadthData {
	observations := make([]breadthObservation, 0, len(rows))
	for _, row := range rows {
		observations = append(observations, breadthObservation{code: row.Code, name: row.Name, current: row.Price, currentOK: row.Price > 0,
			changePct: row.ChangeRate, changeOK: row.ChangeValid, high: row.High, highOK: row.High > 0, low: row.Low, lowOK: row.Low > 0})
	}
	data, _, _ := calculateBreadth(observations)
	return data
}

func research2EnvelopeDocument[T any](name, sourceID, category string, envelope marketdata.DataEnvelope[T]) researchevidence.SourceDocument {
	document := marketEnvelopeDocument(name, sourceID, envelope)
	document.Category = category
	if envelope.Status == marketdata.StatusEmpty {
		if body, err := json.Marshal(envelope); err == nil {
			document.Content = string(body)
			document.Error = ""
		}
	}
	// The cutoff is about the market observation, not HTTP completion. Preserve
	// CollectedAt for audit, but use the provider's verifiable as-of timestamp
	// when deciding whether the payload can enter the scoring snapshot.
	if !envelope.AsOf.IsZero() {
		available := envelope.AsOf
		document.AvailableAt = &available
	}
	return document
}

func research2EnvelopeAvailableAt[T any](envelope marketdata.DataEnvelope[T]) *time.Time {
	for _, source := range envelope.Sources {
		if source.Provider == envelope.Source && source.AvailableAt != nil {
			value := *source.AvailableAt
			return &value
		}
	}
	return nil
}

func research2SourceEntity(document researchevidence.SourceDocument) string {
	text := strings.ToLower(strings.Join([]string{document.SourceID, document.SourceName}, " "))
	for _, prefix := range []string{"sh", "sz"} {
		for index := 0; index+8 <= len(text); index++ {
			value := text[index : index+8]
			if strings.HasPrefix(value, prefix) {
				valid := true
				for _, digit := range value[2:] {
					if digit < '0' || digit > '9' {
						valid = false
						break
					}
				}
				if valid {
					return "stock:" + value
				}
			}
		}
	}
	return ""
}

func stabilizeResearch2Document(document researchevidence.SourceDocument) researchevidence.SourceDocument {
	if strings.TrimSpace(document.SourceID) == "" {
		base := strings.ToLower(strings.TrimSpace(document.Category + ":" + document.SourceName))
		base = strings.NewReplacer(" ", "-", "/", "-", "\\", "-", ":", "-").Replace(base)
		document.SourceID = "research2:aux:" + base
	}
	return document
}

func research2DocumentIsEmpty(document researchevidence.SourceDocument) bool {
	if strings.TrimSpace(document.Error) != "" {
		return false
	}
	content := strings.TrimSpace(document.Content)
	if content == "" || content == "null" || content == "[]" || content == "{}" {
		return true
	}
	var value any
	if json.Unmarshal([]byte(content), &value) != nil {
		return false
	}
	return research2JSONValueEmpty(value)
}

func research2DocumentEmptyKind(document researchevidence.SourceDocument) string {
	if strings.TrimSpace(document.Error) != "" {
		return ""
	}
	content := strings.TrimSpace(document.Content)
	switch content {
	case "", "null":
		return "null"
	case "[]":
		return "empty_array"
	case "{}":
		return "empty_object"
	}
	if research2DocumentIsEmpty(document) {
		return "nested_empty"
	}
	return ""
}

func research2DocumentEmbeddedStatus(document researchevidence.SourceDocument) string {
	var payload map[string]any
	if json.Unmarshal([]byte(document.Content), &payload) != nil {
		return ""
	}
	status, _ := payload["status"].(string)
	switch strings.ToLower(strings.TrimSpace(status)) {
	case marketdata.StatusStale:
		return marketdata.StatusStale
	case marketdata.StatusPartial:
		return marketdata.StatusPartial
	case marketdata.StatusEmpty:
		return marketdata.StatusEmpty
	case marketdata.StatusFailed, marketdata.StatusUnavailable, marketdata.StatusAfterCutoff:
		return marketdata.StatusFailed
	default:
		for _, key := range []string{"code", "rc"} {
			if raw, exists := payload[key]; exists {
				code := strings.TrimSpace(fmt.Sprint(raw))
				if code != "" && code != "0" && code != "0.0" && code != "200" {
					return marketdata.StatusFailed
				}
			}
		}
		if success, exists := payload["success"].(bool); exists && !success {
			return marketdata.StatusFailed
		}
		return ""
	}
}

func research2JSONValueEmpty(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	case []any:
		if len(typed) == 0 {
			return true
		}
		for _, item := range typed {
			if !research2JSONValueEmpty(item) {
				return false
			}
		}
		return true
	case map[string]any:
		if len(typed) == 0 {
			return true
		}
		if data, exists := typed["data"]; exists && research2JSONValueEmpty(data) {
			return true
		}
		for key, item := range typed {
			switch strings.ToLower(strings.TrimSpace(key)) {
			case "code", "rc", "status", "success", "message", "warning", "total", "count", "page", "pagesize":
				continue
			}
			if !research2JSONValueEmpty(item) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func normalizeResearch2DocumentAvailability(document researchevidence.SourceDocument, cutoff time.Time) researchevidence.SourceDocument {
	if strings.TrimSpace(document.Content) == "" || (document.AvailableAt != nil && !document.AvailableAt.After(cutoff)) {
		return document
	}
	var payload any
	if json.Unmarshal([]byte(document.Content), &payload) != nil {
		return document
	}
	filtered, asOf, proved, keep := filterResearch2JSONAtCutoff(payload, cutoff)
	if !proved {
		return document
	}
	if !keep {
		document.Content = "[]"
		document.AvailableAt = &cutoff
		return document
	}
	encoded, err := json.Marshal(filtered)
	if err != nil {
		return document
	}
	document.Content = string(encoded)
	document.AvailableAt = &asOf
	return document
}

func filterResearch2JSONAtCutoff(value any, cutoff time.Time) (any, time.Time, bool, bool) {
	return filterResearch2JSONAtCutoffInherited(value, cutoff, false)
}

func filterResearch2JSONAtCutoffInherited(value any, cutoff time.Time, inheritedTimestamp bool) (any, time.Time, bool, bool) {
	switch typed := value.(type) {
	case []any:
		result := make([]any, 0, len(typed))
		var newest time.Time
		proved := false
		for _, item := range typed {
			filtered, at, itemProved, keep := filterResearch2JSONAtCutoffInherited(item, cutoff, inheritedTimestamp)
			proved = proved || itemProved
			// An array fetched after cutoff may only contribute independently
			// timestamped entries unless its parent timestamp covers the array.
			if keep && (inheritedTimestamp || itemProved) {
				result = append(result, filtered)
				if at.After(newest) {
					newest = at
				}
			}
		}
		return result, newest, proved, inheritedTimestamp || len(result) > 0
	case map[string]any:
		directTimes := research2DirectObjectTimes(typed)
		for _, at := range directTimes {
			if at.After(cutoff) {
				return nil, time.Time{}, true, false
			}
		}
		result := make(map[string]any, len(typed))
		directProved := len(directTimes) > 0
		newest, proved := time.Time{}, directProved
		for _, at := range directTimes {
			if at.After(newest) {
				newest = at
			}
		}
		for key, item := range typed {
			filtered, at, itemProved, keep := filterResearch2JSONAtCutoffInherited(item, cutoff, inheritedTimestamp || directProved)
			proved = proved || itemProved
			// A historical timestamp in one child must not make unrelated,
			// untimestamped siblings point-in-time safe. Primitive/object fields
			// inherit time only from their own object or an already dated parent.
			if keep && (inheritedTimestamp || directProved || itemProved) {
				result[key] = filtered
				if at.After(newest) {
					newest = at
				}
			}
		}
		return result, newest, proved, inheritedTimestamp || directProved || len(result) > 0
	default:
		return value, time.Time{}, false, inheritedTimestamp
	}
}

func research2DirectObjectTimes(value map[string]any) []time.Time {
	values := make([]time.Time, 0, 2)
	dateText, timeText := "", ""
	for key, raw := range value {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if normalized == "date" || normalized == "日期" || normalized == "tradedate" || normalized == "trade_date" {
			dateText = fmt.Sprint(raw)
		}
		if normalized == "time" || normalized == "时间" {
			timeText = fmt.Sprint(raw)
		}
		if !research2TimestampKey(normalized) {
			continue
		}
		if parsed, ok := parseResearch2EvidenceTime(raw); ok {
			values = append(values, parsed)
		}
	}
	if dateText != "" && timeText != "" {
		if parsed, ok := parseResearch2EvidenceTime(strings.TrimSpace(dateText) + " " + strings.TrimSpace(timeText)); ok {
			values = append(values, parsed)
		}
	}
	return values
}

func research2TimestampKey(key string) bool {
	for _, token := range []string{"publish", "published", "eventat", "event_at", "availableat", "available_at", "notice_date", "datetime", "date_time", "timestamp", "trade_time", "tradedate", "trade_date", "日期"} {
		if strings.Contains(key, token) {
			return true
		}
	}
	return false
}

func parseResearch2EvidenceTime(value any) (time.Time, bool) {
	switch typed := value.(type) {
	case float64:
		seconds := int64(typed)
		if seconds > 1e12 {
			return time.UnixMilli(seconds).In(shanghaiDataLocation()), true
		}
		if seconds > 1e9 {
			return time.Unix(seconds, 0).In(shanghaiDataLocation()), true
		}
	case json.Number:
		if number, err := typed.Int64(); err == nil {
			return parseResearch2EvidenceTime(float64(number))
		}
	case string:
		text := strings.TrimSpace(typed)
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02 15:04", "2006/01/02 15:04:05", "2006/01/02 15:04", "20060102 15:04:05", "20060102", "2006-01-02"} {
			if parsed, err := time.ParseInLocation(layout, text, shanghaiDataLocation()); err == nil {
				return parsed.In(shanghaiDataLocation()), true
			}
		}
	}
	return time.Time{}, false
}

func research2CompactDocumentSummary(document researchevidence.SourceDocument) string {
	if strings.TrimSpace(document.Error) != "" || research2DocumentIsEmpty(document) {
		return ""
	}
	var payload any
	if json.Unmarshal([]byte(document.Content), &payload) != nil {
		return limitResearch2Text(document.Content, 360)
	}
	parts := make([]string, 0, 8)
	collectResearch2SummaryStrings(payload, &parts)
	if len(parts) == 0 {
		return limitResearch2Text(document.Content, 360)
	}
	return limitResearch2Text(strings.Join(uniqueBreadthStrings(parts), "；"), 480)
}

func collectResearch2SummaryStrings(value any, output *[]string) {
	if len(*output) >= 8 {
		return
	}
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			collectResearch2SummaryStrings(item, output)
			if len(*output) >= 8 {
				return
			}
		}
	case map[string]any:
		for _, key := range []string{"title", "Title", "name", "Name", "summary", "Summary", "content", "Content", "message", "Message", "公告标题", "标题"} {
			if text, ok := typed[key].(string); ok && strings.TrimSpace(text) != "" {
				*output = append(*output, limitResearch2Text(text, 120))
				if len(*output) >= 8 {
					return
				}
			}
		}
		for _, item := range typed {
			collectResearch2SummaryStrings(item, output)
			if len(*output) >= 8 {
				return
			}
		}
	}
}

func research2TencentRows(ctx context.Context, service *MarketEvidenceService) (research2FullMarketSnapshot, error) {
	if service == nil {
		return research2FullMarketSnapshot{}, errors.New("market evidence service is unavailable")
	}
	universe, err := service.tencentBreadthUniverse(ctx)
	if err != nil {
		return research2FullMarketSnapshot{}, err
	}
	type result struct {
		rows []breadthObservation
		err  error
	}
	batches := make([][]string, 0, (len(universe)+breadthTencentBatch-1)/breadthTencentBatch)
	for start := 0; start < len(universe); start += breadthTencentBatch {
		end := start + breadthTencentBatch
		if end > len(universe) {
			end = len(universe)
		}
		batches = append(batches, append([]string(nil), universe[start:end]...))
	}
	results := make(chan result, len(batches))
	jobs := make(chan []string)
	var wait sync.WaitGroup
	workerCount := breadthTencentWorkers
	if len(batches) < workerCount {
		workerCount = len(batches)
	}
	for worker := 0; worker < workerCount; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for batch := range jobs {
				rows, fetchErr := service.fetchTencentBreadthBatch(ctx, batch, 2)
				results <- result{rows: rows, err: fetchErr}
			}
		}()
	}
	go func() {
		for _, batch := range batches {
			jobs <- batch
		}
		close(jobs)
		wait.Wait()
		close(results)
	}()
	observations := make([]breadthObservation, 0, len(universe))
	for item := range results {
		if item.err == nil {
			observations = append(observations, item.rows...)
		}
	}
	observations = dedupeBreadthObservations([][]breadthObservation{observations})
	if len(universe) == 0 || float64(len(observations))/float64(len(universe)) < research2MinimumCoverage {
		return research2FullMarketSnapshot{}, fmt.Errorf("腾讯全市场覆盖率 %.2f%% 低于95%%", float64(len(observations))/math.Max(1, float64(len(universe)))*100)
	}
	listingDates := research2ListingDates(ctx, service.mainDB)
	rows := make([]research2MarketRow, 0, len(observations))
	var asOf time.Time
	for _, observation := range observations {
		code := strings.TrimPrefix(strings.TrimPrefix(observation.code, "sh"), "sz")
		row := research2MarketRow{Code: code, Name: observation.name, Price: observation.current, ChangeRate: observation.changePct, ChangeValid: observation.changeOK,
			High: observation.high, Low: observation.low, Open: observation.open, PreClose: observation.previous,
			Volume: observation.volume, Amount: observation.amount, ListingDate: listingDates[code]}
		if observation.quoteAt.After(asOf) {
			asOf = observation.quoteAt
		}
		row.Timestamp = observation.quoteAt.Unix()
		if row.PreClose <= 0 && observation.changeOK && observation.current > 0 {
			row.PreClose = observation.current / (1 + observation.changePct/100)
		}
		rows = append(rows, row)
	}
	return research2FullMarketSnapshot{Rows: rows, Reported: len(universe), SourceID: "research2:market:tencent", SourceName: "腾讯全市场降级快照",
		SourceRef: service.urls.breadthTencent, AsOf: asOf, CollectedAt: service.now()}, nil
}

func research2ListingDates(ctx context.Context, database *gorm.DB) map[string]int64 {
	result := make(map[string]int64)
	if database == nil {
		return result
	}
	var stocks []models.StockBasic
	if database.WithContext(ctx).Select("symbol", "ts_code", "list_date").Where("list_status = ?", "L").Find(&stocks).Error != nil {
		return result
	}
	for _, stock := range stocks {
		code := strings.TrimSpace(stock.Symbol)
		if len(code) != 6 && len(stock.TsCode) >= 6 {
			code = stock.TsCode[:6]
		}
		date, _ := strconv.ParseInt(strings.TrimSpace(stock.ListDate), 10, 64)
		if len(code) == 6 && date > 0 {
			result[code] = date
		}
	}
	return result
}

func (c *research2EvidenceCollector) collectStructuredEvidence(ctx context.Context, startedAt time.Time) (research2.Evidence, error) {
	return c.collectStructuredEvidenceWithExclusions(ctx, startedAt, nil)
}

func (c *research2EvidenceCollector) collectStructuredEvidenceWithExclusions(ctx context.Context, startedAt time.Time, excludedCodes map[string]struct{}) (research2.Evidence, error) {
	if c == nil || c.sources == nil || c.stocks == nil || c.minuteWindows == nil ||
		(c.market == nil && (c.collectBreadth == nil || c.collectFlows == nil)) {
		return research2.Evidence{CutoffAt: startedAt}, errors.New("research2 evidence collector is unavailable")
	}
	startedAt = startedAt.In(shanghaiDataLocation())
	collectionCtx := ctx
	fetch := c.fetchSnapshot
	if fetch == nil {
		fetch = c.fetchResearch2FullMarketSnapshot
	}
	marketCtx, cancelMarket := context.WithTimeout(collectionCtx, 25*time.Second)
	marketSnapshot, marketErr := fetch(marketCtx, startedAt)
	cancelMarket()
	if marketErr != nil {
		document := researchevidence.SourceDocument{SourceID: "research2:market:full", SourceName: "全市场候选快照", Category: "market",
			CollectedAt: c.research2Now(), Error: marketErr.Error()}
		return research2.Evidence{CutoffAt: startedAt, WindowStartAt: startedAt.Truncate(time.Minute).Add(-5 * time.Minute), Documents: []researchevidence.SourceDocument{document}, Degraded: true,
			DegradedReasons: []string{"full_market_unavailable"}}, fmt.Errorf("全市场列表不可用: %w", marketErr)
	}
	sourceReported := marketSnapshot.Reported
	marketSnapshot.Rows, marketSnapshot.Reported = research2EligibleCoverageRows(marketSnapshot.Rows, marketSnapshot.CollectedAt)
	marketSnapshot.AsOf = research2RowsAsOf(marketSnapshot.Rows)
	observed := len(marketSnapshot.Rows)
	coverage := 0.0
	if marketSnapshot.Reported > 0 {
		coverage = float64(observed) / float64(marketSnapshot.Reported)
	}
	if coverage < research2MinimumCoverage && marketSnapshot.SourceID != "research2:market:tencent" {
		fallbackCtx, cancelFallback := context.WithTimeout(collectionCtx, 25*time.Second)
		fetchFallback := c.fetchFallback
		if fetchFallback == nil {
			fetchFallback = func(ctx context.Context) (research2FullMarketSnapshot, error) {
				return research2TencentRows(ctx, c.market)
			}
		}
		fallback, fallbackErr := fetchFallback(fallbackCtx)
		cancelFallback()
		if fallbackErr == nil {
			fallbackSourceReported := fallback.Reported
			fallback.Rows, fallback.Reported = research2EligibleCoverageRows(fallback.Rows, fallback.CollectedAt)
			fallback.AsOf = research2RowsAsOf(fallback.Rows)
			fallbackObserved := len(fallback.Rows)
			fallbackCoverage := 0.0
			if fallback.Reported > 0 {
				fallbackCoverage = float64(fallbackObserved) / float64(fallback.Reported)
			}
			if fallbackCoverage >= research2MinimumCoverage {
				marketSnapshot, observed, coverage = fallback, fallbackObserved, fallbackCoverage
				sourceReported = fallbackSourceReported
			}
		}
	}
	cutoff := marketSnapshot.AsOf
	windowEnd := research2ClosedWindowEnd(startedAt, cutoff)
	windowStart := windowEnd.Add(-5 * time.Minute)
	availableAt := cutoff
	if availableAt.IsZero() {
		detail := "全市场快照缺少可验证的截止时点"
		if marketSnapshot.Reported <= 0 {
			detail += "，且来源未提供可信总数"
		}
		document := researchevidence.SourceDocument{SourceID: "research2:market:full", SourceName: marketSnapshot.SourceName, SourceRef: marketSnapshot.SourceRef,
			Category: "market", CollectedAt: marketSnapshot.CollectedAt, Error: detail}
		return research2.Evidence{CutoffAt: startedAt, WindowStartAt: startedAt.Truncate(time.Minute).Add(-5 * time.Minute), CoveragePct: coverage * 100, Documents: []researchevidence.SourceDocument{document},
			Degraded: true, DegradedReasons: []string{"full_market_time_unverifiable"}}, errors.New(detail)
	}
	fullMarketPayload, _ := json.Marshal(map[string]any{
		"sourceId": marketSnapshot.SourceID, "source": marketSnapshot.SourceName, "asOf": marketSnapshot.AsOf,
		"sourceReported": sourceReported, "eligibleReported": marketSnapshot.Reported, "observed": observed, "coveragePct": coverage * 100, "rows": marketSnapshot.Rows,
	})
	fullMarketDocument := researchevidence.SourceDocument{SourceID: "research2:market:full", SourceName: marketSnapshot.SourceName, SourceRef: marketSnapshot.SourceRef,
		Category: "market", CollectedAt: marketSnapshot.CollectedAt, AvailableAt: &availableAt, Content: string(fullMarketPayload)}
	if coverage < research2MinimumCoverage {
		fullMarketDocument.Error = fmt.Sprintf("全市场覆盖率 %.2f%% 低于95%%（%d/%d）", coverage*100, observed, marketSnapshot.Reported)
		return research2.Evidence{CutoffAt: cutoff, WindowStartAt: windowStart, CoveragePct: coverage * 100, Documents: []researchevidence.SourceDocument{fullMarketDocument},
			Degraded: true, DegradedReasons: []string{"market_coverage_below_95pct"}}, errors.New(fullMarketDocument.Error)
	}

	// Candidate selection happens exactly once from the T0 snapshot.  Rows with
	// no verifiable fresh quote are never admitted to the model's candidate set.
	candidateRows := make([]research2MarketRow, 0, len(marketSnapshot.Rows))
	for _, row := range marketSnapshot.Rows {
		quoteAt := time.Time{}
		if row.Timestamp > 0 {
			quoteAt = time.Unix(row.Timestamp, 0).In(shanghaiDataLocation())
		}
		if quoteAt.IsZero() || quoteAt.After(cutoff) || cutoff.Sub(quoteAt) > research2QuoteFreshness {
			continue
		}
		candidateRows = append(candidateRows, row)
	}
	selected := selectResearch2CandidatesWithExclusions(candidateRows, 12, cutoff, excludedCodes)
	windows := collectResearch2CandidateWindows(collectionCtx, c.minuteWindows, selected, marketSnapshot.Rows, windowStart, windowEnd)

	type documentsResult struct {
		kind      string
		documents []researchevidence.SourceDocument
		err       error
	}
	documentResults := make(chan documentsResult, 6)
	go func() {
		docs, err := c.sources.CollectMarket(collectionCtx, cutoff)
		documentResults <- documentsResult{kind: "market", documents: docs, err: err}
	}()
	go func() {
		docs, err := c.sources.CollectSectors(collectionCtx, cutoff)
		documentResults <- documentsResult{kind: "sector", documents: docs, err: err}
	}()
	go func() {
		docs, err := c.sources.CollectStocks(collectionCtx, cutoff, selected)
		documentResults <- documentsResult{kind: "stock", documents: docs, err: err}
	}()
	type marketEnvelopeResult struct {
		kind    string
		breadth marketdata.DataEnvelope[BreadthData]
		flows   marketdata.DataEnvelope[[]FundFlowRow]
	}
	envelopeResults := make(chan marketEnvelopeResult, 3)
	collectBreadth := c.collectBreadth
	if collectBreadth == nil {
		collectBreadth = c.market.Breadth
	}
	collectFlows := c.collectFlows
	if collectFlows == nil {
		collectFlows = c.market.FundFlows
	}
	go func() {
		envelopeResults <- marketEnvelopeResult{kind: "breadth", breadth: collectBreadth(collectionCtx)}
	}()
	go func() {
		envelopeResults <- marketEnvelopeResult{kind: "sector", flows: collectFlows(collectionCtx, marketdata.ProviderRequest{Scope: "sector", Sort: "netamount", Limit: 20, CutoffAt: cutoff})}
	}()
	go func() {
		envelopeResults <- marketEnvelopeResult{kind: "concept", flows: collectFlows(collectionCtx, marketdata.ProviderRequest{Scope: "concept", Sort: "netamount", Limit: 20, CutoffAt: cutoff})}
	}()

	documents := []researchevidence.SourceDocument{fullMarketDocument}
	degradedReasons := make([]string, 0)
	for range 3 {
		var result documentsResult
		select {
		case result = <-documentResults:
		case <-collectionCtx.Done():
			return research2.Evidence{CutoffAt: cutoff, WindowStartAt: windowStart, CoveragePct: coverage * 100, Documents: documents, Degraded: true,
				DegradedReasons: []string{"evidence_collection_deadline"}}, collectionCtx.Err()
		}
		if result.err != nil {
			degradedReasons = append(degradedReasons, result.kind+"_auxiliary_collection_failed")
			documents = append(documents, researchevidence.SourceDocument{SourceID: "research2:aux:" + result.kind, SourceName: result.kind + "辅助资料",
				Category: result.kind, CollectedAt: c.research2Now(), Error: result.err.Error()})
		}
		documents = append(documents, result.documents...)
	}
	compactMarket := research2CompactMarket{SourceID: fullMarketDocument.SourceID, Observed: observed, Reported: marketSnapshot.Reported,
		CoveragePct: math.Round(coverage*10000) / 100, SectorFlows: []FundFlowRow{}, ConceptFlows: []FundFlowRow{}}
	calculatedBreadth := research2MarketBreadth(marketSnapshot.Rows)
	compactMarket.Advances, compactMarket.Declines, compactMarket.Flat = calculatedBreadth.Advances, calculatedBreadth.Declines, calculatedBreadth.Flat
	compactMarket.LimitUps, compactMarket.LimitDowns, compactMarket.MedianChange = calculatedBreadth.LimitUps, calculatedBreadth.LimitDowns, calculatedBreadth.MedianChangePct
	for range 3 {
		var result marketEnvelopeResult
		select {
		case result = <-envelopeResults:
		case <-collectionCtx.Done():
			return research2.Evidence{CutoffAt: cutoff, WindowStartAt: windowStart, CoveragePct: coverage * 100, Documents: documents, Degraded: true,
				DegradedReasons: []string{"market_evidence_deadline"}}, collectionCtx.Err()
		}
		typedCutoff := c.research2Now()
		switch result.kind {
		case "breadth":
			documents = append(documents, research2EnvelopeDocument("结构化市场宽度", "research2:market:breadth", "market", result.breadth))
			if !research2EnvelopeUsableAt(result.breadth.Status, result.breadth.AsOf, typedCutoff) {
				degradedReasons = append(degradedReasons, "typed_breadth_unavailable")
			}
		case "sector":
			documents = append(documents, research2EnvelopeDocument("结构化行业资金流", "research2:sector:fund-flow", "sector", result.flows))
			if research2EnvelopeUsableAt(result.flows.Status, result.flows.AsOf, typedCutoff) && len(result.flows.Data) > 0 {
				compactMarket.SectorFlows = append([]FundFlowRow(nil), result.flows.Data...)
			} else {
				degradedReasons = append(degradedReasons, "sector_fund_flow_unavailable")
			}
		case "concept":
			documents = append(documents, research2EnvelopeDocument("结构化概念资金流", "research2:concept:fund-flow", "sector", result.flows))
			if research2EnvelopeUsableAt(result.flows.Status, result.flows.AsOf, typedCutoff) && len(result.flows.Data) > 0 {
				compactMarket.ConceptFlows = append([]FundFlowRow(nil), result.flows.Data...)
			} else {
				degradedReasons = append(degradedReasons, "concept_fund_flow_unavailable")
			}
		}
	}
	if c.themes != nil {
		documents = append(documents, themeResearchEvidenceDocuments(c.themes.ResearchEvidence(collectionCtx, cutoff), cutoff)...)
	}

	compactCandidates := make([]research2CompactCandidate, 0, len(windows))
	eligibleCandidates := make([]researchevidence.StockCandidate, 0, len(windows))
	referencePrices := make(map[string]float64, len(windows))
	for _, window := range windows {
		compact := buildResearch2CompactCandidate(window, cutoff)
		compactCandidates = append(compactCandidates, compact)
		quoteAt := cutoff
		if compact.Quote != nil {
			quoteAt = compact.Quote.At
		}
		quotePayload, _ := json.Marshal(map[string]any{"entityId": compact.EntityID, "quote": compact.Quote, "missing": compact.Missing})
		quoteDocument := researchevidence.SourceDocument{SourceID: "research2:quote:" + compact.Code, SourceName: "截止点行情 " + compact.Code,
			SourceRef: marketSnapshot.SourceRef, Category: "stock", CollectedAt: c.research2Now(), AvailableAt: &quoteAt, Content: string(quotePayload)}
		if compact.Quote == nil {
			quoteDocument.Error = "截止点有效行情不可用"
		}
		minuteAvailableAt := cutoff
		if len(window.Bars) > 0 {
			minuteAvailableAt = window.Bars[len(window.Bars)-1].TradeTime
		}
		minutePayload, _ := json.Marshal(map[string]any{"entityId": compact.EntityID, "windowStartAt": windowStart, "cutoffAt": cutoff,
			"source": window.Source, "bars": window.Bars, "metrics": compact.Metrics})
		minuteDocument := researchevidence.SourceDocument{SourceID: "research2:minutes:" + compact.Code, SourceName: "五分钟未复权行情 " + compact.Code,
			SourceRef: window.Source, Category: "stock", CollectedAt: c.research2Now(), AvailableAt: &minuteAvailableAt, Content: string(minutePayload)}
		if window.Error != nil {
			minuteDocument.Error = window.Error.Error()
		} else if len(window.Bars) < research2MinimumMinuteBars {
			minuteDocument.Error = fmt.Sprintf("五分钟窗口仅有%d根有效一分钟线，至少需要4根", len(window.Bars))
		}
		documents = append(documents, quoteDocument, minuteDocument)
		if compact.CoreEligible {
			eligibleCandidates = append(eligibleCandidates, window.Candidate)
			referencePrices[compact.Code] = compact.Quote.Price
			degradedReasons = append(degradedReasons, "historical_5day_same_time_baseline_unavailable:"+compact.Code)
		} else {
			degradedReasons = append(degradedReasons, "candidate_core_evidence_incomplete:"+compact.Code)
		}
	}

	freezeAt := c.research2Now()
	if freezeAt.Before(cutoff) {
		freezeAt = cutoff
	}
	frozenDocuments := make([]researchevidence.SourceDocument, 0, len(documents)+1)
	statuses := make([]map[string]any, 0, len(documents)+1)
	compactSources := make([]research2CompactSource, 0, len(documents))
	usedIDs := make(map[string]int)
	for index, document := range documents {
		document = stabilizeResearch2Document(document)
		document.SourceID = uniqueResearchEvidenceSourceID(document, index, usedIDs)
		document = normalizeResearch2DocumentAvailability(document, cutoff)
		document = research2DocumentAtCutoff(document, freezeAt, true)
		status := research2DocumentStatus(document, freezeAt, true)
		entityID := research2SourceEntity(document)
		frozenDocuments = append(frozenDocuments, document)
		compactSources = append(compactSources, research2CompactSource{SourceID: document.SourceID, SourceName: document.SourceName,
			Category: document.Category, EntityID: entityID, Status: status, AvailableAt: document.AvailableAt,
			SourceRef: limitResearch2Text(document.SourceRef, 240), Summary: research2CompactDocumentSummary(document),
			EmptyKind: research2DocumentEmptyKind(document)})
		statuses = append(statuses, map[string]any{"sourceId": document.SourceID, "sourceName": document.SourceName, "category": document.Category,
			"entityId": entityID, "availableAt": document.AvailableAt, "collectedAt": document.CollectedAt, "status": status, "error": document.Error})
		if status != marketdata.StatusOK && status != marketdata.StatusPartial {
			degradedReasons = append(degradedReasons, "source_"+status+":"+document.SourceID)
		}
	}
	degradedReasons = summarizeResearch2DegradedReasons(uniqueBreadthStrings(degradedReasons))
	for candidateIndex := range compactCandidates {
		entityID := compactCandidates[candidateIndex].EntityID
		for _, source := range compactSources {
			if source.EntityID == entityID {
				compactCandidates[candidateIndex].SourceIDs = append(compactCandidates[candidateIndex].SourceIDs, source.SourceID)
			}
		}
		compactCandidates[candidateIndex].SourceIDs = uniqueBreadthStrings(compactCandidates[candidateIndex].SourceIDs)
	}
	compactSnapshot := research2CompactSnapshot{Version: research2EvidenceProfileV7, WindowStartAt: windowStart, WindowEndAt: windowEnd, CutoffAt: cutoff, FreezeAt: freezeAt,
		Market: compactMarket, Candidates: compactCandidates, Sources: compactSources, Degraded: len(degradedReasons) > 0, DegradedReasons: degradedReasons}
	compactPayload, marshalErr := json.Marshal(compactSnapshot)
	if marshalErr != nil {
		return research2.Evidence{CutoffAt: cutoff, WindowStartAt: windowStart, CoveragePct: coverage * 100, Documents: frozenDocuments, Degraded: true,
			DegradedReasons: append(degradedReasons, "compact_snapshot_marshal_failed")}, marshalErr
	}
	compactDocument := researchevidence.SourceDocument{SourceID: "research2:compact:v4", SourceName: "研究中心2紧凑结构化快照", Category: "snapshot",
		CollectedAt: freezeAt, AvailableAt: &freezeAt, Content: string(compactPayload)}
	frozenDocuments = append([]researchevidence.SourceDocument{compactDocument}, frozenDocuments...)
	statuses = append([]map[string]any{{"sourceId": compactDocument.SourceID, "sourceName": compactDocument.SourceName, "category": compactDocument.Category,
		"availableAt": freezeAt, "collectedAt": compactDocument.CollectedAt, "status": marketdata.StatusOK}}, statuses...)
	statusJSON, _ := json.Marshal(statuses)
	return research2.Evidence{Prompt: string(compactPayload), SourceStatusJSON: string(statusJSON), Candidates: eligibleCandidates,
		Documents: frozenDocuments, EvidenceProfileVersion: research2EvidenceProfileV7, CutoffAt: cutoff, FreezeAt: freezeAt, WindowStartAt: windowStart, WindowEndAt: windowEnd,
		CoveragePct: compactMarket.CoveragePct, Degraded: compactSnapshot.Degraded, DegradedReasons: degradedReasons,
		CandidateReferencePrices: referencePrices}, nil
}

func (c *research2EvidenceCollector) research2Now() time.Time {
	if c != nil && c.now != nil {
		return c.now().In(shanghaiDataLocation())
	}
	return time.Now().In(shanghaiDataLocation())
}

func (c *research2EvidenceCollector) fetchResearch2FullMarketSnapshot(ctx context.Context, _ time.Time) (research2FullMarketSnapshot, error) {
	rows, reported, err := c.fetchFullMarketWithReported(ctx)
	if err == nil && len(rows) > 0 {
		collectedAt := c.research2Now()
		return research2FullMarketSnapshot{Rows: rows, Reported: reported, SourceID: "research2:market:eastmoney", SourceName: "东方财富全市场快照",
			SourceRef: "eastmoney:full-market", CollectedAt: collectedAt}, nil
	}
	fallback, fallbackErr := research2TencentRows(ctx, c.market)
	if fallbackErr == nil {
		return fallback, nil
	}
	return research2FullMarketSnapshot{}, errors.Join(err, fmt.Errorf("tencent fallback: %w", fallbackErr))
}

func research2RowsTrustedAtCollection(rows []research2MarketRow, collectedAt time.Time) []research2MarketRow {
	trusted, _ := research2EligibleCoverageRows(rows, collectedAt)
	return trusted
}

func research2EligibleCoverageRows(rows []research2MarketRow, collectedAt time.Time) ([]research2MarketRow, int) {
	if collectedAt.IsZero() {
		return nil, 0
	}
	latestTrusted := collectedAt.In(shanghaiDataLocation()).Add(research2QuoteFutureSkew)
	eligible := make(map[string]struct{}, len(rows))
	best := make(map[string]research2MarketRow, len(rows))
	for _, row := range rows {
		code := research2CanonicalCode(row.Code)
		name := strings.ToUpper(strings.TrimSpace(row.Name))
		if code == "" || !(strings.HasPrefix(code, "sh60") || strings.HasPrefix(code, "sz00")) || strings.Contains(name, "ST") || strings.Contains(name, "退") || row.ListingDate <= 0 {
			continue
		}
		eligible[code] = struct{}{}
		if row.Price <= 0 || row.Timestamp <= 0 || time.Unix(row.Timestamp, 0).In(shanghaiDataLocation()).After(latestTrusted) {
			continue
		}
		if existing, exists := best[code]; !exists || row.Timestamp > existing.Timestamp || (row.Timestamp == existing.Timestamp && row.Amount > existing.Amount) {
			best[code] = row
		}
	}
	keys := make([]string, 0, len(best))
	for code := range best {
		keys = append(keys, code)
	}
	sort.Strings(keys)
	result := make([]research2MarketRow, 0, len(keys))
	for _, code := range keys {
		result = append(result, best[code])
	}
	return result, len(eligible)
}

func research2RowsAsOf(rows []research2MarketRow) time.Time {
	var newest time.Time
	for _, row := range rows {
		if row.Timestamp <= 0 {
			continue
		}
		at := time.Unix(row.Timestamp, 0).In(shanghaiDataLocation())
		if at.After(newest) {
			newest = at
		}
	}
	return newest
}

func summarizeResearch2DegradedReasons(values []string) []string {
	result := make([]string, 0, len(values))
	positions := make(map[string]int)
	counts := make(map[string]int)
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := value
		if index := strings.IndexByte(value, ':'); index > 0 {
			key = value[:index]
		}
		if position, exists := positions[key]; exists {
			counts[key]++
			result[position] = fmt.Sprintf("%s(%d)", key, counts[key])
			continue
		}
		positions[key] = len(result)
		counts[key] = 1
		result = append(result, value)
	}
	return result
}

func research2EnvelopeUsableAt(status string, asOf, cutoff time.Time) bool {
	return (status == marketdata.StatusOK || status == marketdata.StatusPartial) && !asOf.IsZero() && !asOf.After(cutoff)
}
