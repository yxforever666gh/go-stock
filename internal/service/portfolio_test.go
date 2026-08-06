package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"go-stock/backend/marketdata"
	"go-stock/backend/portfolio"
)

type recordingPortfolioReader struct {
	events       []portfolio.LedgerEvent
	eventsCalls  int
	accountCalls int
	marks        map[string]portfolio.ValuationMark
	initialCash  float64
	account      portfolio.AccountSnapshot
	err          error
}

func (r *recordingPortfolioReader) Events(context.Context, portfolio.LedgerQuery) ([]portfolio.LedgerEvent, portfolio.LedgerSeal, error) {
	r.eventsCalls++
	return append([]portfolio.LedgerEvent(nil), r.events...), portfolio.LedgerSeal{LedgerHash: "sealed"}, r.err
}

func (r *recordingPortfolioReader) Account(_ context.Context, _ portfolio.LedgerQuery, initialCash float64, marks map[string]portfolio.ValuationMark) (portfolio.AccountSnapshot, error) {
	r.accountCalls++
	r.marks = marks
	r.initialCash = initialCash
	return r.account, r.err
}

type recordingQuoteReader struct {
	quotes map[string]marketdata.Quote
	errors map[string]error
	calls  []string
}

func (r *recordingQuoteReader) Quote(_ context.Context, symbol string, _ time.Time) (marketdata.Quote, error) {
	r.calls = append(r.calls, symbol)
	if err := r.errors[symbol]; err != nil {
		return marketdata.Quote{}, err
	}
	return r.quotes[symbol], nil
}

func TestPortfolioServiceReturnsUnavailableWithoutPartialNAVWhenAnyOpenQuoteIsMissing(t *testing.T) {
	asOf := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	reader := &recordingPortfolioReader{events: []portfolio.LedgerEvent{
		{RuleID: "rule-a", Symbol: "000001.SZ", EventType: "fill", Quantity: 100},
		{RuleID: "rule-b", Symbol: "600000.SH", EventType: "fill", Quantity: 200},
	}}
	quotes := &recordingQuoteReader{
		quotes: map[string]marketdata.Quote{
			"000001.SZ": freshServiceQuote("000001.SZ", asOf),
		},
		errors: map[string]error{"600000.SH": marketdata.ErrObservationUnavailable},
	}
	service := NewPortfolioService(reader, quotes, "1.5.0", 100000, time.Hour)

	view, err := service.Current(context.Background(), validCurrentPortfolioQuery(asOf))
	if err != nil {
		t.Fatal(err)
	}
	if view.Availability != PortfolioUnavailable || view.Account != nil || view.Reason == "" {
		t.Fatalf("view = %+v, want wholly unavailable", view)
	}
	if reader.accountCalls != 0 {
		t.Fatalf("account derivation calls = %d, want 0 before every mark is valid", reader.accountCalls)
	}
}

func TestPortfolioServiceReturnsUnavailableWhenAnyOpenQuoteIsStale(t *testing.T) {
	asOf := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	reader := &recordingPortfolioReader{events: []portfolio.LedgerEvent{
		{RuleID: "rule-a", Symbol: "000001.SZ", EventType: "fill", Quantity: 100},
		{RuleID: "rule-b", Symbol: "600000.SH", EventType: "fill", Quantity: 200},
	}}
	stale := freshServiceQuote("600000.SH", asOf)
	stale.ObservedAt = asOf.Add(-2 * time.Hour)
	stale.SourceAt = stale.ObservedAt
	quotes := &recordingQuoteReader{quotes: map[string]marketdata.Quote{
		"000001.SZ": freshServiceQuote("000001.SZ", asOf),
		"600000.SH": stale,
	}}
	service := NewPortfolioService(reader, quotes, "1.5.0", 100000, time.Hour)

	view, err := service.Current(context.Background(), validCurrentPortfolioQuery(asOf))
	if err != nil {
		t.Fatal(err)
	}
	if view.Availability != PortfolioUnavailable || view.Account != nil || view.Reason != "stale quote for 600000.SH" {
		t.Fatalf("view = %+v, want stale and wholly unavailable", view)
	}
	if reader.accountCalls != 0 {
		t.Fatalf("account derivation calls = %d, want 0", reader.accountCalls)
	}
}

func TestPortfolioServiceBuildsCurrentAccountOnlyAfterEveryOpenQuoteIsFresh(t *testing.T) {
	asOf := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	want := portfolio.AccountSnapshot{StrategyVersion: "1.5.0", AsOf: asOf, NAV: 100500}
	reader := &recordingPortfolioReader{events: []portfolio.LedgerEvent{
		{RuleID: "closed", Symbol: "300001.SZ", EventType: "fill", Quantity: 100},
		{RuleID: "closed", Symbol: "300001.SZ", EventType: "exit_fill", Quantity: 100},
		{RuleID: "open", Symbol: "000001.SZ", EventType: "fill", Quantity: 100},
	}, account: want}
	quotes := &recordingQuoteReader{quotes: map[string]marketdata.Quote{
		"000001.SZ": freshServiceQuote("000001.SZ", asOf),
	}}
	service := NewPortfolioService(reader, quotes, "1.5.0", 100000, time.Hour)

	view, err := service.Current(context.Background(), validCurrentPortfolioQuery(asOf))
	if err != nil {
		t.Fatal(err)
	}
	if view.Availability != PortfolioAvailable || view.Account == nil || view.Account.NAV != want.NAV || view.Reason != "" {
		t.Fatalf("view = %+v", view)
	}
	if reader.accountCalls != 1 || len(reader.marks) != 1 || reader.marks["000001.SZ"].Price != 10 {
		t.Fatalf("account calls=%d marks=%+v", reader.accountCalls, reader.marks)
	}
	if reader.initialCash != 100000 {
		t.Fatalf("initial cash=%v, want frozen 100000", reader.initialCash)
	}
	if len(quotes.calls) != 1 || quotes.calls[0] != "000001.SZ" {
		t.Fatalf("quote calls = %+v, closed position must not require a mark", quotes.calls)
	}
}

func TestPortfolioServiceRequiresExplicitCurrentCohortAndExactVersion(t *testing.T) {
	asOf := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	reader := &recordingPortfolioReader{}
	service := NewPortfolioService(reader, &recordingQuoteReader{}, "1.5.0", 100000, time.Hour)
	for _, cohort := range []StrategyCohort{"", "all", StrategyCohortLegacy} {
		query := validCurrentPortfolioQuery(asOf)
		query.Cohort = cohort
		if _, err := service.Current(context.Background(), query); !errors.Is(err, ErrInvalidStrategyCohort) {
			t.Fatalf("cohort %q error = %v, want ErrInvalidStrategyCohort", cohort, err)
		}
	}
	query := validCurrentPortfolioQuery(asOf)
	query.StrategyVersion = "1.4.2"
	if _, err := service.Current(context.Background(), query); !errors.Is(err, ErrInvalidPortfolioQuery) {
		t.Fatalf("version error = %v, want ErrInvalidPortfolioQuery", err)
	}
	if reader.eventsCalls != 0 {
		t.Fatalf("invalid cohort reached ledger %d times", reader.eventsCalls)
	}
}

func freshServiceQuote(symbol string, asOf time.Time) marketdata.Quote {
	observedAt := asOf.Add(-time.Minute)
	return marketdata.Quote{
		Symbol: symbol, Price: 10, ObservedAt: observedAt, SourceAt: observedAt,
		AvailableAt: observedAt.Add(10 * time.Second), Source: "cache",
	}
}

func validCurrentPortfolioQuery(asOf time.Time) CurrentPortfolioQuery {
	return CurrentPortfolioQuery{
		Cohort: StrategyCohortCurrent, StrategyVersion: "1.5.0", AsOf: asOf,
	}
}
