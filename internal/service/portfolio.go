package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"go-stock/backend/marketdata"
	"go-stock/backend/portfolio"
)

const (
	PortfolioAvailable   = "available"
	PortfolioUnavailable = "unavailable"
)

var ErrInvalidPortfolioQuery = errors.New("invalid portfolio query")

// PortfolioAccountReader exposes only immutable-ledger reads and account
// derivation. Mutable yield projections cannot satisfy this boundary.
type PortfolioAccountReader interface {
	Events(context.Context, portfolio.LedgerQuery) ([]portfolio.LedgerEvent, portfolio.LedgerSeal, error)
	Account(context.Context, portfolio.LedgerQuery, float64, map[string]portfolio.ValuationMark) (portfolio.AccountSnapshot, error)
}

type CurrentPortfolioQuery struct {
	Cohort          StrategyCohort
	StrategyVersion string
	AsOf            time.Time
}

type CurrentPortfolioView struct {
	Availability string                     `json:"availability"`
	Reason       string                     `json:"reason,omitempty"`
	Account      *portfolio.AccountSnapshot `json:"account,omitempty"`
}

type PortfolioService struct {
	reader                 PortfolioAccountReader
	quotes                 marketdata.QuoteReader
	currentStrategyVersion string
	initialCash            float64
	maxQuoteAge            time.Duration
}

func NewPortfolioService(
	reader PortfolioAccountReader,
	quotes marketdata.QuoteReader,
	currentStrategyVersion string,
	initialCash float64,
	maxQuoteAge time.Duration,
) PortfolioService {
	return PortfolioService{
		reader:                 reader,
		quotes:                 quotes,
		currentStrategyVersion: strings.TrimSpace(currentStrategyVersion),
		initialCash:            initialCash,
		maxQuoteAge:            maxQuoteAge,
	}
}

// Current returns an all-or-nothing valuation of the complete current Strategy
// cohort. Account cash, quote freshness and ledger scope are service policy;
// callers cannot override them. A missing, invalid or stale mark makes the
// entire view unavailable, so a partial NAV is never returned.
func (s PortfolioService) Current(ctx context.Context, query CurrentPortfolioQuery) (CurrentPortfolioView, error) {
	if err := s.validateCurrentQuery(query); err != nil {
		return CurrentPortfolioView{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ledgerQuery := portfolio.LedgerQuery{
		StrategyVersion: strings.TrimSpace(query.StrategyVersion),
		AsOf:            query.AsOf,
	}
	events, _, err := s.reader.Events(ctx, ledgerQuery)
	if err != nil {
		return CurrentPortfolioView{}, err
	}

	marks := make(map[string]portfolio.ValuationMark)
	for _, symbol := range openPortfolioSymbols(events) {
		quote, quoteErr := s.quotes.Quote(ctx, symbol, query.AsOf)
		if quoteErr != nil {
			return unavailablePortfolioView(fmt.Sprintf("quote unavailable for %s: %v", symbol, quoteErr)), nil
		}
		if reason := invalidPortfolioQuoteReason(quote, symbol, query.AsOf, s.maxQuoteAge); reason != "" {
			return unavailablePortfolioView(reason), nil
		}
		marks[normalizePortfolioSymbol(symbol)] = portfolio.ValuationMark{
			Symbol: quote.Symbol, Price: quote.Price, ObservedAt: quote.ObservedAt,
			AvailableAt: quote.AvailableAt, Source: quote.Source,
		}
	}

	account, err := s.reader.Account(ctx, ledgerQuery, s.initialCash, marks)
	if errors.Is(err, portfolio.ErrInvalidValuation) {
		return unavailablePortfolioView(err.Error()), nil
	}
	if err != nil {
		return CurrentPortfolioView{}, err
	}
	return CurrentPortfolioView{Availability: PortfolioAvailable, Account: &account}, nil
}

func (s PortfolioService) validateCurrentQuery(query CurrentPortfolioQuery) error {
	if err := requireStrategyCohort(query.Cohort, StrategyCohortCurrent); err != nil {
		return err
	}
	version := strings.TrimSpace(query.StrategyVersion)
	if s.reader == nil || s.quotes == nil || s.currentStrategyVersion == "" ||
		s.initialCash <= 0 || math.IsNaN(s.initialCash) || math.IsInf(s.initialCash, 0) || s.maxQuoteAge <= 0 {
		return fmt.Errorf("%w: portfolio read dependencies are unavailable", ErrInvalidPortfolioQuery)
	}
	if version == "" || version != s.currentStrategyVersion {
		return fmt.Errorf("%w: strategy version %q does not match current %q", ErrInvalidPortfolioQuery, version, s.currentStrategyVersion)
	}
	if query.AsOf.IsZero() {
		return fmt.Errorf("%w: asOf is required", ErrInvalidPortfolioQuery)
	}
	return nil
}

func unavailablePortfolioView(reason string) CurrentPortfolioView {
	return CurrentPortfolioView{Availability: PortfolioUnavailable, Reason: strings.TrimSpace(reason)}
}

func invalidPortfolioQuoteReason(quote marketdata.Quote, symbol string, asOf time.Time, maxAge time.Duration) string {
	if !strings.EqualFold(normalizePortfolioSymbol(quote.Symbol), normalizePortfolioSymbol(symbol)) ||
		quote.Price <= 0 || math.IsNaN(quote.Price) || math.IsInf(quote.Price, 0) || strings.TrimSpace(quote.Source) == "" {
		return fmt.Sprintf("invalid quote for %s", symbol)
	}
	if err := marketdata.ValidateTimeline(quote.SourceAt, quote.AvailableAt, asOf); err != nil ||
		quote.ObservedAt.IsZero() || quote.ObservedAt.After(quote.AvailableAt) {
		return fmt.Sprintf("invalid quote timeline for %s", symbol)
	}
	if asOf.Sub(quote.ObservedAt) > maxAge {
		return fmt.Sprintf("stale quote for %s", symbol)
	}
	return ""
}

func openPortfolioSymbols(events []portfolio.LedgerEvent) []string {
	type openPosition struct {
		symbol   string
		quantity float64
	}
	positions := make(map[string]openPosition)
	for _, event := range events {
		switch strings.ToLower(strings.TrimSpace(event.EventType)) {
		case "fill":
			positions[event.RuleID] = openPosition{symbol: event.Symbol, quantity: event.Quantity}
		case "corporate_action":
			position, exists := positions[event.RuleID]
			if exists {
				position.quantity = event.Quantity
				positions[event.RuleID] = position
			}
		case "exit_fill":
			position, exists := positions[event.RuleID]
			if !exists {
				continue
			}
			position.quantity -= event.Quantity
			if position.quantity <= 1e-8 {
				delete(positions, event.RuleID)
			} else {
				positions[event.RuleID] = position
			}
		}
	}
	set := make(map[string]struct{}, len(positions))
	for _, position := range positions {
		if symbol := normalizePortfolioSymbol(position.symbol); symbol != "" {
			set[symbol] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for symbol := range set {
		result = append(result, symbol)
	}
	sort.Strings(result)
	return result
}

func normalizePortfolioSymbol(symbol string) string {
	return strings.ToUpper(strings.TrimSpace(symbol))
}
