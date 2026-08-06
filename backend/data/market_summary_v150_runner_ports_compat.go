package data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"go-stock/backend/marketintel"
	"go-stock/backend/recommendation"
	"go-stock/backend/strategy/v150"

	"gorm.io/gorm"
)

var errMarketSummaryV150RunnerReadSet = errors.New("invalid V1.5 runner read set")

// MarketSummaryV150RunnerReadSet is the run-scoped, pre-decision output of the
// existing phased discovery. Prepared must be captured immediately after
// prepareMarketSummaryV150ForPhase; it must not contain event-model, final
// quote, portfolio, decision or production output. Verified is the factual
// result of the existing top-set evidence read.
type MarketSummaryV150RunnerReadSet struct {
	Prepared        *MarketSummaryV150RunSnapshot
	EvidenceStatus  recommendation.EvidenceStatus
	EvidenceWarning string
	Verified        []MarketSummaryVerifiedCandidateSnapshot
	AIProtocol      string
}

// MarketSummaryV150RunnerComponents is the narrow compatibility export for a
// composition root. EventVerifier and Clock are intentionally absent: callers
// must inject both explicitly into recommendation.RunnerDependencies.
type MarketSummaryV150RunnerComponents struct {
	Pipeline    recommendation.Pipeline
	Market      recommendation.MarketPort
	Candidates  recommendation.CandidatesPort
	Evidence    recommendation.EvidencePort
	FinalQuotes recommendation.FinalQuotePort
	Portfolio   recommendation.PortfolioPort
}

// NewMarketSummaryV150RunnerComponents splits one frozen discovery read set
// into typed, read-only ports. Final quotes remain a live read because the old
// chain refreshes them after event verification; portfolio state is read from
// the explicitly supplied database at the pipeline's point-in-time request.
func NewMarketSummaryV150RunnerComponents(
	readSet MarketSummaryV150RunnerReadSet,
	database *gorm.DB,
) (MarketSummaryV150RunnerComponents, error) {
	if database == nil {
		return MarketSummaryV150RunnerComponents{}, fmt.Errorf("%w: main database is required", errMarketSummaryV150RunnerReadSet)
	}
	prepared, err := cloneMarketSummaryV150RunnerPrepared(readSet.Prepared)
	if err != nil {
		return MarketSummaryV150RunnerComponents{}, err
	}
	if err := validateMarketSummaryV150RunnerReadSet(prepared, readSet); err != nil {
		return MarketSummaryV150RunnerComponents{}, err
	}
	market, err := newMarketSummaryV150RunnerMarketPort(prepared)
	if err != nil {
		return MarketSummaryV150RunnerComponents{}, err
	}
	candidates, err := newMarketSummaryV150RunnerCandidatesPort(prepared)
	if err != nil {
		return MarketSummaryV150RunnerComponents{}, err
	}
	evidence, err := newMarketSummaryV150RunnerEvidencePort(prepared, readSet)
	if err != nil {
		return MarketSummaryV150RunnerComponents{}, err
	}
	return MarketSummaryV150RunnerComponents{
		Pipeline:    newMarketSummaryV150RunnerCompatPipeline(prepared.RunContext.StartedAt, prepared.RunSlot, readSet.AIProtocol),
		Market:      market,
		Candidates:  candidates,
		Evidence:    evidence,
		FinalQuotes: marketSummaryV150RunnerProductionFinalQuotePort{},
		Portfolio:   marketSummaryV150RunnerProductionPortfolioPort{database: database},
	}, nil
}

func cloneMarketSummaryV150RunnerPrepared(source *MarketSummaryV150RunSnapshot) (*MarketSummaryV150RunSnapshot, error) {
	if source == nil {
		return nil, fmt.Errorf("%w: prepared run is required", errMarketSummaryV150RunnerReadSet)
	}
	payload, err := json.Marshal(source)
	if err != nil {
		return nil, fmt.Errorf("%w: encode prepared run: %v", errMarketSummaryV150RunnerReadSet, err)
	}
	var result MarketSummaryV150RunSnapshot
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, fmt.Errorf("%w: decode prepared run: %v", errMarketSummaryV150RunnerReadSet, err)
	}
	return &result, nil
}

func validateMarketSummaryV150RunnerReadSet(prepared *MarketSummaryV150RunSnapshot, readSet MarketSummaryV150RunnerReadSet) error {
	if prepared == nil || prepared.RunContext.StartedAt.IsZero() || prepared.RunContext.AsOf.IsZero() ||
		prepared.RunContext.StrategyVersion != v150.StrategyVersion || prepared.RunContext.ConfigHash != v150.FixedStrategyV150ConfigHash() {
		return fmt.Errorf("%w: prepared run identity is not frozen V1.5.0", errMarketSummaryV150RunnerReadSet)
	}
	if !prepared.RunContext.DecisionAt.IsZero() || !prepared.RunContext.ValidFromAt.IsZero() || len(prepared.Production) != 0 ||
		strings.TrimSpace(prepared.PortfolioStateStatus) != "" {
		return fmt.Errorf("%w: prepared run already contains decision output", errMarketSummaryV150RunnerReadSet)
	}
	if err := validateMarketSummaryV150RunnerEvidenceStatus(readSet.EvidenceStatus); err != nil {
		return fmt.Errorf("%w: %v", errMarketSummaryV150RunnerReadSet, err)
	}
	if prepared.Regime.NoTrade && len(readSet.Verified) > 0 {
		return fmt.Errorf("%w: risk-off read set contains candidate verification", errMarketSummaryV150RunnerReadSet)
	}
	return nil
}

type marketSummaryV150RunnerFrozenMarketPort struct {
	snapshot recommendation.MarketSnapshot
}

func newMarketSummaryV150RunnerMarketPort(prepared *MarketSummaryV150RunSnapshot) (*marketSummaryV150RunnerFrozenMarketPort, error) {
	projection, err := json.Marshal(prepared.BenchmarkSource)
	if err != nil {
		return nil, err
	}
	timing := prepared.BenchmarkSource.Timing
	return &marketSummaryV150RunnerFrozenMarketPort{snapshot: recommendation.MarketSnapshot{
		Benchmark: prepared.Benchmark,
		Evidence: []marketintel.Evidence{{
			ID: timing.EvidenceID, Type: marketintel.EvidenceType(timing.EvidenceType), Symbol: v150.BenchmarkCode,
			Source: prepared.BenchmarkSource.AdjustmentSource, SourceAt: timing.SourceAt, AvailableAt: timing.AvailableAt,
		}},
		CompatibilityProjection: projection,
	}}, nil
}

func (p *marketSummaryV150RunnerFrozenMarketPort) MarketSnapshot(ctx context.Context, request recommendation.MarketRequest) (recommendation.MarketSnapshot, error) {
	if err := marketSummaryV150RunnerPortContext(ctx); err != nil {
		return recommendation.MarketSnapshot{}, err
	}
	if request.StrategyVersion != v150.StrategyVersion || request.ConfigHash != v150.FixedStrategyV150ConfigHash() {
		return recommendation.MarketSnapshot{}, fmt.Errorf("%w: market request identity mismatch", errMarketSummaryV150RunnerReadSet)
	}
	return cloneMarketSummaryV150RunnerValue(p.snapshot)
}

type marketSummaryV150RunnerFrozenCandidatesPort struct {
	batch recommendation.CandidateBatch
}

func newMarketSummaryV150RunnerCandidatesPort(prepared *MarketSummaryV150RunSnapshot) (*marketSummaryV150RunnerFrozenCandidatesPort, error) {
	batch := recommendation.CandidateBatch{Items: make([]recommendation.CandidateInput, 0, len(prepared.Candidates))}
	for _, row := range prepared.Candidates {
		symbol := normalizeRecommendStockCode(row.Candidate.Symbol)
		projection, err := json.Marshal(row.Source)
		if err != nil {
			return nil, err
		}
		evidence := make([]marketintel.Evidence, 0)
		for _, timing := range marketSummaryV150RunnerSourceTimings(row.Source, symbol) {
			evidence = append(evidence, marketintel.Evidence{
				ID: timing.EvidenceID, Type: marketintel.EvidenceType(timing.EvidenceType), Symbol: symbol,
				Source: "frozen_discovery", SourceAt: timing.SourceAt, AvailableAt: timing.AvailableAt,
			})
		}
		batch.Items = append(batch.Items, recommendation.CandidateInput{
			Candidate: row.Candidate, Evidence: evidence, CompatibilityProjection: projection,
		})
	}
	return &marketSummaryV150RunnerFrozenCandidatesPort{batch: batch}, nil
}

func (p *marketSummaryV150RunnerFrozenCandidatesPort) Candidates(ctx context.Context, request recommendation.CandidateRequest) (recommendation.CandidateBatch, error) {
	if err := marketSummaryV150RunnerPortContext(ctx); err != nil {
		return recommendation.CandidateBatch{}, err
	}
	if request.RunContext.StrategyVersion != v150.StrategyVersion || request.RunContext.ConfigHash != v150.FixedStrategyV150ConfigHash() {
		return recommendation.CandidateBatch{}, fmt.Errorf("%w: candidate request identity mismatch", errMarketSummaryV150RunnerReadSet)
	}
	return cloneMarketSummaryV150RunnerValue(p.batch)
}

type marketSummaryV150RunnerFrozenEvidencePort struct {
	snapshot recommendation.EvidenceSnapshot
}

func newMarketSummaryV150RunnerEvidencePort(
	prepared *MarketSummaryV150RunSnapshot,
	readSet MarketSummaryV150RunnerReadSet,
) (*marketSummaryV150RunnerFrozenEvidencePort, error) {
	allowed := make(map[string]bool, len(prepared.VerificationSymbols))
	for _, symbol := range prepared.VerificationSymbols {
		allowed[normalizeRecommendStockCode(symbol)] = true
	}
	snapshot := recommendation.EvidenceSnapshot{Status: readSet.EvidenceStatus, Warning: strings.TrimSpace(readSet.EvidenceWarning)}
	for _, verified := range readSet.Verified {
		symbol := normalizeRecommendStockCode(verified.StockCode)
		if symbol == "" || !allowed[symbol] || verified.VerifiedAt.IsZero() {
			return nil, fmt.Errorf("%w: verified candidate %s is outside the frozen top set", errMarketSummaryV150RunnerReadSet, symbol)
		}
		projection, err := json.Marshal(verified)
		if err != nil {
			return nil, err
		}
		items, err := marketSummaryV150RunnerVerifiedEvidenceItems(verified)
		if err != nil {
			return nil, err
		}
		snapshot.Candidates = append(snapshot.Candidates, recommendation.CandidateEvidence{
			Symbol: symbol, VerifiedAt: verified.VerifiedAt, Items: items, CompatibilityProjection: projection,
		})
	}
	return &marketSummaryV150RunnerFrozenEvidencePort{snapshot: snapshot}, nil
}

func (p *marketSummaryV150RunnerFrozenEvidencePort) Evidence(ctx context.Context, request recommendation.EvidenceRequest) (recommendation.EvidenceSnapshot, error) {
	if err := marketSummaryV150RunnerPortContext(ctx); err != nil {
		return recommendation.EvidenceSnapshot{}, err
	}
	result, err := cloneMarketSummaryV150RunnerValue(p.snapshot)
	if err != nil {
		return recommendation.EvidenceSnapshot{}, err
	}
	if request.StatusOnly {
		result.Candidates = nil
		return result, nil
	}
	requested := make(map[string]bool, len(request.Candidates))
	for _, candidate := range request.Candidates {
		requested[normalizeRecommendStockCode(candidate.Candidate.Symbol)] = true
	}
	filtered := result.Candidates[:0]
	for _, candidate := range result.Candidates {
		if requested[normalizeRecommendStockCode(candidate.Symbol)] {
			filtered = append(filtered, candidate)
		}
	}
	result.Candidates = filtered
	return result, nil
}

func marketSummaryV150RunnerVerifiedEvidenceItems(verified MarketSummaryVerifiedCandidateSnapshot) ([]marketintel.Evidence, error) {
	result := make([]marketintel.Evidence, 0, len(verified.EvidenceSources))
	seen := make(map[string]bool, len(verified.EvidenceSources))
	symbol := normalizeRecommendStockCode(verified.StockCode)
	for index, ref := range verified.EvidenceSources {
		sourceAt, ok := parseMarketSummaryV150EvidenceTime(ref.PublishedAt)
		if !ok {
			continue
		}
		id := firstNonEmptyText(strings.TrimSpace(ref.RawHash), strings.TrimSpace(ref.DedupeKey))
		if id == "" {
			id = fmt.Sprintf("verified:%s:%d:%s", symbol, index, marketSummaryV150StableHash(ref.SourceName+"|"+ref.Title+"|"+ref.PublishedAt))
		}
		if seen[id] {
			return nil, fmt.Errorf("%w: duplicate verified evidence %s", errMarketSummaryV150RunnerReadSet, id)
		}
		seen[id] = true
		result = append(result, marketintel.Evidence{
			ID: id, Type: marketintel.EvidenceType(firstNonEmptyText(ref.Type, ref.SourceType, "verified_evidence")),
			Symbol: symbol, Title: ref.Title, Source: firstNonEmptyText(ref.SourceName, ref.SourceType, "frozen_discovery"),
			SourceAt: sourceAt, AvailableAt: verified.VerifiedAt,
		})
	}
	return result, nil
}

type marketSummaryV150RunnerProductionFinalQuotePort struct{}

func (marketSummaryV150RunnerProductionFinalQuotePort) FinalQuotes(ctx context.Context, request recommendation.FinalQuoteRequest) (recommendation.FinalQuoteSnapshot, error) {
	if err := marketSummaryV150RunnerPortContext(ctx); err != nil {
		return recommendation.FinalQuoteSnapshot{}, err
	}
	indicators := make([]marketSummaryIndicatorCandidate, 0, len(request.Symbols))
	for _, raw := range request.Symbols {
		if symbol := normalizeRecommendStockCode(raw); symbol != "" {
			indicators = append(indicators, marketSummaryIndicatorCandidate{StockCode: symbol})
		}
	}
	rows := loadMarketSummaryV150RealtimeQuotesForRefresh(indicators)
	result := recommendation.FinalQuoteSnapshot{Quotes: make([]recommendation.FinalQuote, 0, len(rows))}
	for _, raw := range request.Symbols {
		symbol := normalizeRecommendStockCode(raw)
		row, ok := rows[symbol]
		if !ok {
			continue
		}
		price, hasPrice := parseLooseFloat(row.Price)
		previousClose, hasPreviousClose := parseLooseFloat(row.PreClose)
		openPrice, hasOpen := parseLooseFloat(row.Open)
		amount, hasAmount := parseLooseFloat(row.Amount)
		sourceAt, _ := parseMarketSummaryV150QuoteTimestamp(row)
		result.Quotes = append(result.Quotes, recommendation.FinalQuote{
			Symbol: symbol, Name: strings.TrimSpace(row.Name), Price: price, PreviousClose: previousClose,
			Open: openPrice, Amount: amount, HasPrice: hasPrice, HasPreviousClose: hasPreviousClose,
			HasOpen: hasOpen, HasAmount: hasAmount, HasVolume: strings.TrimSpace(row.Volume) != "", SourceAt: sourceAt,
		})
	}
	if err := marketSummaryV150RunnerPortContext(ctx); err != nil {
		return recommendation.FinalQuoteSnapshot{}, err
	}
	return result, nil
}

type marketSummaryV150RunnerProductionPortfolioPort struct {
	database *gorm.DB
}

func (p marketSummaryV150RunnerProductionPortfolioPort) PortfolioSnapshot(ctx context.Context, request recommendation.PortfolioRequest) (recommendation.PortfolioSnapshot, error) {
	if err := marketSummaryV150RunnerPortContext(ctx); err != nil {
		return recommendation.PortfolioSnapshot{}, err
	}
	if p.database == nil {
		return recommendation.PortfolioSnapshot{}, errors.New("main database is unavailable")
	}
	state, err := loadMarketSummaryV150PortfolioState(p.database.WithContext(ctx), request.RunContext.DataCutoffAt)
	return recommendation.PortfolioSnapshot{State: state}, err
}

func marketSummaryV150RunnerPortContext(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func cloneMarketSummaryV150RunnerValue[T any](source T) (T, error) {
	var result T
	payload, err := json.Marshal(source)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(payload, &result); err != nil {
		return result, err
	}
	return result, nil
}

var (
	_ recommendation.MarketPort     = (*marketSummaryV150RunnerFrozenMarketPort)(nil)
	_ recommendation.CandidatesPort = (*marketSummaryV150RunnerFrozenCandidatesPort)(nil)
	_ recommendation.EvidencePort   = (*marketSummaryV150RunnerFrozenEvidencePort)(nil)
	_ recommendation.FinalQuotePort = marketSummaryV150RunnerProductionFinalQuotePort{}
	_ recommendation.PortfolioPort  = marketSummaryV150RunnerProductionPortfolioPort{}
)
