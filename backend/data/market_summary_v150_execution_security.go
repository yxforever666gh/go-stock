package data

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
	"go-stock/backend/persistence"
	"go-stock/backend/strategy/v150"

	"gorm.io/gorm"
)

const (
	marketSummaryV150ExecutionSecurityObservationMode = persistence.StrategyRunModeExecutionSecurityObservation
	marketSummaryV150ExecutionSecurityFetchTimeout    = 5 * time.Second
)

// marketSummaryV150ExecutionSecurityFact is the normalized point-in-time fact
// appended before an online execution evaluation. It is intentionally not an
// update to the candidate run's security row: every refresh gets its own
// immutable observation run and security_master_history row.
type marketSummaryV150ExecutionSecurityFact struct {
	Symbol      string
	Name        string
	Market      string
	Exchange    string
	Board       string
	Sector      string
	Industry    string
	Currency    string
	Status      string
	ListStatus  string
	IsST        bool
	IsSuspended bool
	ListedAt    *time.Time
	DelistedAt  *time.Time
	Source      string
	SourceAt    time.Time
	Quote       *marketSummaryV150ExecutionSecurityQuote
}

type marketSummaryV150ExecutionSecurityQuote struct {
	Code       string `json:"code"`
	Name       string `json:"name,omitempty"`
	Date       string `json:"date"`
	Time       string `json:"time"`
	Price      string `json:"price,omitempty"`
	Open       string `json:"open,omitempty"`
	PreClose   string `json:"preClose,omitempty"`
	Volume     string `json:"volume,omitempty"`
	Amount     string `json:"amount,omitempty"`
	ObservedAt string `json:"observedAt"`
}

type marketSummaryV150ExecutionSecurityObservationPayload struct {
	Kind        string                                   `json:"kind"`
	OriginRunID string                                   `json:"originRunId"`
	Symbol      string                                   `json:"symbol"`
	Security    MarketSummaryV150SecuritySource          `json:"security"`
	Quote       *marketSummaryV150ExecutionSecurityQuote `json:"quote,omitempty"`
}

var marketSummaryV150ExecutionSecurityNow = time.Now

var fetchMarketSummaryV150ExecutionSecurityFactFn = fetchMarketSummaryV150ExecutionSecurityFact

var runMarketSummaryV150ExecutionRealtimeWithTimeoutFn = runStockRealtimeWithTimeout

// refreshMarketSummaryV150ExecutionSecurityObservation appends one auditable
// observation only when online refresh is explicitly allowed. Cache-only
// callers invoke this with allowRefresh=false, which performs no provider call
// and no database write.
func refreshMarketSummaryV150ExecutionSecurityObservation(originRunID, symbol string, allowRefresh bool) (string, error) {
	if !allowRefresh {
		return "", nil
	}
	originRunID = strings.TrimSpace(originRunID)
	symbol = normalizeRecommendStockCode(symbol)
	if originRunID == "" || symbol == "" {
		return "", errors.New("origin run id and symbol are required for execution security refresh")
	}
	startedAt := marketSummaryV150ExecutionSecurityNow().In(cnLocation())
	if startedAt.IsZero() {
		return "", errors.New("execution security observation start time is unavailable")
	}
	fact, err := fetchMarketSummaryV150ExecutionSecurityFactFn(symbol, startedAt)
	if err != nil {
		return "", err
	}
	availableAt := marketSummaryV150ExecutionSecurityNow().In(cnLocation())
	if availableAt.Before(startedAt) {
		availableAt = startedAt
	}
	return appendMarketSummaryV150ExecutionSecurityObservation(originRunID, fact, startedAt, availableAt)
}

func appendMarketSummaryV150ExecutionSecurityObservation(
	originRunID string,
	fact marketSummaryV150ExecutionSecurityFact,
	startedAt, availableAt time.Time,
) (string, error) {
	if db.Dao == nil || !db.Dao.Migrator().HasTable(&models.StrategyRunSnapshot{}) || !db.Dao.Migrator().HasTable(&models.SecurityMasterHistory{}) {
		return "", errors.New("immutable strategy security tables are unavailable")
	}
	originRunID = strings.TrimSpace(originRunID)
	fact.Symbol = normalizeRecommendStockCode(fact.Symbol)
	startedAt = startedAt.In(cnLocation())
	availableAt = availableAt.In(cnLocation())
	if originRunID == "" || fact.Symbol == "" || startedAt.IsZero() || availableAt.IsZero() || availableAt.Before(startedAt) {
		return "", errors.New("execution security observation identity or timeline is invalid")
	}
	if err := validateMarketSummaryV150ExecutionSecurityFact(fact, availableAt); err != nil {
		return "", err
	}

	securitySource := MarketSummaryV150SecuritySource{
		Name:        strings.TrimSpace(fact.Name),
		Market:      strings.TrimSpace(fact.Market),
		Exchange:    strings.TrimSpace(fact.Exchange),
		Board:       strings.TrimSpace(fact.Board),
		Industry:    strings.TrimSpace(fact.Industry),
		Currency:    firstNonEmptyText(strings.TrimSpace(fact.Currency), "CNY"),
		ListStatus:  strings.TrimSpace(fact.ListStatus),
		ObservedAt:  availableAt.Format(time.RFC3339Nano),
		SourceAt:    fact.SourceAt.Format(time.RFC3339Nano),
		AvailableAt: availableAt.Format(time.RFC3339Nano),
		Source:      strings.TrimSpace(fact.Source),
	}
	if fact.ListedAt != nil && !fact.ListedAt.IsZero() {
		securitySource.ListDate = fact.ListedAt.In(cnLocation()).Format("20060102")
	}
	if fact.DelistedAt != nil && !fact.DelistedAt.IsZero() {
		securitySource.DelistDate = fact.DelistedAt.In(cnLocation()).Format("20060102")
	}
	payload := marketSummaryV150ExecutionSecurityObservationPayload{
		Kind:        marketSummaryV150ExecutionSecurityObservationMode,
		OriginRunID: originRunID,
		Symbol:      fact.Symbol,
		Security:    securitySource,
		Quote:       fact.Quote,
	}
	payloadJSON, inputHash, err := marshalMarketSummaryV150FrozenPayload(payload)
	if err != nil {
		return "", fmt.Errorf("encode execution security observation: %w", err)
	}
	runID := marketSummaryV150ExecutionSecurityObservationMode + "|" + fact.Symbol + "|" + inputHash[:24]
	runPayload, _, err := marshalMarketSummaryV150FrozenPayload(struct {
		Observation marketSummaryV150ExecutionSecurityObservationPayload `json:"observation"`
	}{Observation: payload})
	if err != nil {
		return "", fmt.Errorf("encode execution security observation run: %w", err)
	}
	frozenAt := availableAt
	bundle := persistence.StrategySnapshotBundle{
		Run: models.StrategyRunSnapshot{
			RunID:           runID,
			StrategyVersion: v150.StrategyVersion,
			TradeDate:       availableAt.Format(time.DateOnly),
			RunSlot:         marketSummaryV150ExecutionSecurityObservationMode,
			StartedAt:       startedAt,
			AsOf:            startedAt,
			DataCutoffAt:    availableAt,
			DecisionAt:      availableAt,
			GeneratedAt:     availableAt,
			Mode:            marketSummaryV150ExecutionSecurityObservationMode,
			ConfigHash:      v150.FixedStrategyV150ConfigHash(),
			InputHash:       inputHash,
			PayloadJSON:     runPayload,
			FrozenAt:        &frozenAt,
		},
		SecurityMaster: []models.SecurityMasterHistory{{
			RecordID:        runID + "|security",
			RunID:           runID,
			SnapshotVersion: v150.StrategyVersion,
			Symbol:          fact.Symbol,
			Name:            strings.TrimSpace(fact.Name),
			Market:          strings.TrimSpace(fact.Market),
			Exchange:        strings.TrimSpace(fact.Exchange),
			Board:           strings.TrimSpace(fact.Board),
			Sector:          strings.TrimSpace(fact.Sector),
			Industry:        strings.TrimSpace(fact.Industry),
			Currency:        firstNonEmptyText(strings.TrimSpace(fact.Currency), "CNY"),
			Status:          strings.ToUpper(strings.TrimSpace(fact.Status)),
			IsST:            fact.IsST,
			IsSuspended:     fact.IsSuspended,
			ListedAt:        cloneMarketSummaryV150TimePointer(fact.ListedAt),
			DelistedAt:      cloneMarketSummaryV150TimePointer(fact.DelistedAt),
			EffectiveFrom:   availableAt,
			Source:          strings.TrimSpace(fact.Source),
			PayloadJSON:     payloadJSON,
			FrozenAt:        &frozenAt,
		}},
	}
	if err := persistence.SealStrategySnapshotBundle(&bundle); err != nil {
		return "", fmt.Errorf("seal execution security observation: %w", err)
	}
	if err := persistence.AppendStrategySnapshotBundle(context.Background(), db.Dao, bundle); err != nil {
		// A fixed test clock or two truly simultaneous callers can produce the
		// same content-derived identity. Treat the already-frozen identical run
		// as an idempotent observation, never as permission to update it.
		var existing models.StrategyRunSnapshot
		lookupErr := db.Dao.Where("run_id = ? AND strategy_version = ? AND input_hash = ? AND frozen_at IS NOT NULL", runID, v150.StrategyVersion, inputHash).First(&existing).Error
		if lookupErr == nil {
			return runID, nil
		}
		return "", fmt.Errorf("append execution security observation: %w", err)
	}
	return runID, nil
}

func validateMarketSummaryV150ExecutionSecurityFact(fact marketSummaryV150ExecutionSecurityFact, availableAt time.Time) error {
	if normalizeRecommendStockCode(fact.Symbol) == "" || strings.TrimSpace(fact.Source) == "" {
		return errors.New("execution security observation is missing symbol or source")
	}
	if fact.SourceAt.IsZero() || fact.SourceAt.After(availableAt) {
		return errors.New("execution security source timestamp is missing or not causal")
	}
	switch strings.ToUpper(strings.TrimSpace(fact.Status)) {
	case "L", "LISTED", "ACTIVE", "TRADING", "NORMAL":
		if fact.IsSuspended {
			return errors.New("listed execution security observation contradicts suspended flag")
		}
	case "P", "SUSPENDED", "HALTED", "D", "DELISTED":
		if !fact.IsSuspended {
			return errors.New("non-tradable execution security observation lacks suspended flag")
		}
	default:
		return fmt.Errorf("unknown execution security status %q", fact.Status)
	}
	return nil
}

func fetchMarketSummaryV150ExecutionSecurityFact(symbol string, observedAt time.Time) (marketSummaryV150ExecutionSecurityFact, error) {
	var fact marketSummaryV150ExecutionSecurityFact
	if db.Dao == nil || !db.Dao.Migrator().HasTable(&StockBasic{}) {
		return fact, errors.New("local security master is unavailable")
	}
	symbol = normalizeRecommendStockCode(symbol)
	if symbol == "" {
		return fact, errors.New("execution security symbol is empty")
	}
	var basic StockBasic
	err := db.Dao.Model(&StockBasic{}).
		Where("upper(ts_code) = ?", symbol).
		Order("updated_at DESC, id DESC").
		First(&basic).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fact, fmt.Errorf("security master status is missing for %s", symbol)
		}
		return fact, fmt.Errorf("load security master status for %s: %w", symbol, err)
	}
	fact = marketSummaryV150ExecutionSecurityFact{
		Symbol:     symbol,
		Name:       strings.TrimSpace(basic.Name),
		Market:     string(v150.ResolveMarket(symbol)),
		Exchange:   strings.TrimSpace(basic.Exchange),
		Board:      strings.TrimSpace(basic.Market),
		Sector:     strings.TrimSpace(basic.Industry),
		Industry:   strings.TrimSpace(basic.Industry),
		Currency:   firstNonEmptyText(strings.TrimSpace(basic.CurrType), "CNY"),
		ListStatus: strings.ToUpper(strings.TrimSpace(basic.ListStatus)),
		Source:     "tushare_stock_basic",
		IsST:       marketSummaryV150SecurityNameIsST(basic.Name),
	}
	if listedAt, ok := parseMarketSummaryV150ListDate(basic.ListDate); ok {
		fact.ListedAt = &listedAt
	}
	if delistedAt, ok := parseMarketSummaryV150ListDate(basic.DelistDate); ok {
		fact.DelistedAt = &delistedAt
	}
	basicSourceAt := basic.UpdatedAt
	if basicSourceAt.IsZero() {
		basicSourceAt = basic.CreatedAt
	}
	if basicSourceAt.IsZero() {
		basicSourceAt = observedAt
	}
	fact.SourceAt = basicSourceAt

	switch fact.ListStatus {
	case "D", "DELISTED":
		fact.Status = "D"
		fact.IsSuspended = true
		return fact, nil
	case "P", "SUSPENDED", "HALTED":
		fact.Status = "P"
		fact.IsSuspended = true
		return fact, nil
	case "L", "LISTED", "ACTIVE", "TRADING", "NORMAL":
		// A current-day quote is the execution observation for listed stocks.
	default:
		return fact, fmt.Errorf("unknown local security status %q for %s", basic.ListStatus, symbol)
	}

	rows, quoteErr := runMarketSummaryV150ExecutionRealtimeWithTimeoutFn(toQuoteCode(symbol), marketSummaryV150ExecutionSecurityFetchTimeout)
	if quoteErr != nil {
		return fact, fmt.Errorf("refresh execution quote for %s: %w", symbol, quoteErr)
	}
	if rows == nil || len(*rows) == 0 {
		return fact, fmt.Errorf("current-day execution quote is missing for %s", symbol)
	}
	var quote StockInfo
	found := false
	for _, candidate := range *rows {
		if normalizeRecommendStockCode(candidate.Code) == symbol {
			quote = candidate
			found = true
			break
		}
	}
	if !found {
		return fact, fmt.Errorf("execution quote symbol mismatch for %s", symbol)
	}
	quoteAt, ok := parseMarketSummaryV150ExecutionQuoteTime(quote)
	if !ok || !normalizeDailyTradeDate(quoteAt).Equal(normalizeDailyTradeDate(observedAt)) {
		return fact, fmt.Errorf("current-day execution quote timestamp is missing or stale for %s", symbol)
	}
	fact.SourceAt = quoteAt
	fact.Source = "tushare_stock_basic+realtime_quote"
	fact.Name = firstNonEmptyText(strings.TrimSpace(quote.Name), fact.Name)
	fact.IsST = fact.IsST || marketSummaryV150SecurityNameIsST(quote.Name)
	fact.Quote = &marketSummaryV150ExecutionSecurityQuote{
		Code:       normalizeRecommendStockCode(quote.Code),
		Name:       strings.TrimSpace(quote.Name),
		Date:       strings.TrimSpace(quote.Date),
		Time:       strings.TrimSpace(quote.Time),
		Price:      strings.TrimSpace(quote.Price),
		Open:       strings.TrimSpace(quote.Open),
		PreClose:   strings.TrimSpace(quote.PreClose),
		Volume:     strings.TrimSpace(quote.Volume),
		Amount:     strings.TrimSpace(quote.Amount),
		ObservedAt: observedAt.Format(time.RFC3339Nano),
	}
	price, priceOK := parseLooseFloat(quote.Price)
	openPrice, openOK := parseLooseFloat(quote.Open)
	previousClose, previousCloseOK := parseLooseFloat(quote.PreClose)
	volume, volumeOK := parseLooseFloat(quote.Volume)
	amount, amountOK := parseLooseFloat(quote.Amount)
	if priceOK && openOK && previousCloseOK && price > 0 && openPrice > 0 && previousClose > 0 {
		fact.Status = "L"
		return fact, nil
	}
	local := observedAt.In(cnLocation())
	afterContinuousTradingStart := local.Hour() > 9 || (local.Hour() == 9 && local.Minute() >= 30)
	if afterContinuousTradingStart && priceOK && openOK && previousCloseOK && volumeOK && amountOK &&
		price >= 0 && openPrice <= 0 && previousClose > 0 && volume <= 0 && amount <= 0 {
		fact.Status = "SUSPENDED"
		fact.IsSuspended = true
		return fact, nil
	}
	return fact, fmt.Errorf("execution quote has an unknown tradability state for %s", symbol)
}

func parseMarketSummaryV150ExecutionQuoteTime(quote StockInfo) (time.Time, bool) {
	dateText := strings.TrimSpace(quote.Date)
	timeText := strings.TrimSpace(quote.Time)
	if dateText == "" || timeText == "" {
		return time.Time{}, false
	}
	raw := dateText + " " + timeText
	for _, layout := range []string{
		time.DateTime,
		"2006-01-02 15:04",
		"2006/01/02 15:04:05",
		"2006/01/02 15:04",
		"20060102 150405",
		"20060102 15:04:05",
	} {
		if parsed, err := time.ParseInLocation(layout, raw, cnLocation()); err == nil && !parsed.IsZero() {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func marketSummaryV150SecurityNameIsST(raw string) bool {
	name := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(raw), " ", ""))
	return strings.HasPrefix(name, "ST") || strings.HasPrefix(name, "*ST") || strings.HasPrefix(name, "S*ST")
}

func cloneMarketSummaryV150TimePointer(value *time.Time) *time.Time {
	if value == nil || value.IsZero() {
		return nil
	}
	cloned := *value
	return &cloned
}
