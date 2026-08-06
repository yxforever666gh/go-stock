package data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"go-stock/backend/marketintel"
	"go-stock/backend/recommendation"
	"go-stock/backend/strategy/v150"
)

var errMarketSummaryV150RunnerCompatInput = errors.New("invalid V1.5 runner compatibility input")

// marketSummaryV150RunnerCompatPipeline is the one temporary bridge from the
// typed recommendation runner to the existing immutable V1.5 decision. It is
// deliberately build-only: publication remains exclusively owned by Runner's
// DecisionPublisher and this type has no database or global runtime access.
//
// startedAt/runSlot are command metadata captured before the phased discovery
// begins. Every market-dependent value in the returned decision is obtained
// from PipelinePorts during Build.
type marketSummaryV150RunnerCompatPipeline struct {
	startedAt  time.Time
	runSlot    string
	aiProtocol string
}

func newMarketSummaryV150RunnerCompatPipeline(
	startedAt time.Time,
	runSlot string,
	aiProtocol string,
) *marketSummaryV150RunnerCompatPipeline {
	return &marketSummaryV150RunnerCompatPipeline{
		startedAt: startedAt, runSlot: strings.TrimSpace(runSlot), aiProtocol: NormalizeAIAPIProtocol(aiProtocol),
	}
}

func (p *marketSummaryV150RunnerCompatPipeline) Build(
	ctx context.Context,
	request recommendation.BuildRequest,
	ports recommendation.PipelinePorts,
) (recommendation.ProducedDecision, error) {
	if err := p.validate(request); err != nil {
		return recommendation.ProducedDecision{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return recommendation.ProducedDecision{}, err
	}

	marketAt, err := marketSummaryV150RunnerClockAt(ports.Clock, "market snapshot", p.startedAt)
	if err != nil {
		return recommendation.ProducedDecision{}, err
	}
	market, err := ports.Market.MarketSnapshot(ctx, recommendation.MarketRequest{
		AsOf: marketAt, StrategyVersion: request.StrategyVersion, ConfigHash: request.ConfigHash,
	})
	if err != nil {
		return recommendation.ProducedDecision{}, fmt.Errorf("load V1.5 market snapshot: %w", err)
	}
	benchmarkSource, err := decodeMarketSummaryV150RunnerBenchmarkSource(market)
	if err != nil {
		return recommendation.ProducedDecision{}, err
	}

	preliminaryContext := v150.RunContext{
		RunID: buildMarketSummaryV150RunID(p.startedAt), StartedAt: p.startedAt,
		AsOf: marketAt, DataCutoffAt: marketAt, StrategyVersion: request.StrategyVersion,
		ConfigHash: request.ConfigHash, Mode: "production",
	}
	batch, err := ports.Candidates.Candidates(ctx, recommendation.CandidateRequest{
		RunContext: preliminaryContext, Benchmark: market.Benchmark,
	})
	if err != nil {
		return recommendation.ProducedDecision{}, fmt.Errorf("load V1.5 candidates: %w", err)
	}
	initialCutoff, err := marketSummaryV150RunnerClockAt(ports.Clock, "candidate cutoff", marketAt)
	if err != nil {
		return recommendation.ProducedDecision{}, err
	}
	if err := validateMarketSummaryV150RunnerEvidence(market.Evidence, initialCutoff, "benchmark"); err != nil {
		return recommendation.ProducedDecision{}, err
	}
	candidates, sources, err := decodeMarketSummaryV150RunnerCandidates(batch, initialCutoff)
	if err != nil {
		return recommendation.ProducedDecision{}, err
	}

	run, err := newMarketSummaryV150Run(p.startedAt, initialCutoff, p.runSlot, market.Benchmark, candidates, sources)
	if err != nil {
		return recommendation.ProducedDecision{}, err
	}
	run.BenchmarkSource = benchmarkSource
	if warning := strings.TrimSpace(run.Regime.Warning); warning != "" {
		run.Warnings = append(run.Warnings, "benchmark:"+warning)
	}

	verificationCandidates := marketSummaryV150RunnerVerificationCandidates(run)
	statusOnly := run.Regime.NoTrade || len(verificationCandidates) == 0
	evidenceCandidates := verificationCandidates
	if statusOnly {
		evidenceCandidates = nil
	}
	evidence, evidenceErr := ports.Evidence.Evidence(ctx, recommendation.EvidenceRequest{
		RunContext: run.RunContext,
		Candidates: evidenceCandidates,
		StatusOnly: statusOnly,
	})
	if evidenceErr != nil {
		evidence.Status = recommendation.EvidenceStatusFailed
		evidence.Warning = evidenceErr.Error()
		evidence.Candidates = nil
	}
	if err := validateMarketSummaryV150RunnerEvidenceStatus(evidence.Status); err != nil {
		return recommendation.ProducedDecision{}, err
	}
	if statusOnly && len(evidence.Candidates) > 0 {
		return recommendation.ProducedDecision{}, fmt.Errorf("%w: status-only evidence response contains verified candidates", errMarketSummaryV150RunnerCompatInput)
	}
	applyMarketSummaryV150RunnerEvidenceWarnings(run, evidence.Warning, evidence.Status)
	applyMarketSummaryV150RunnerInputWarnings(run, batch.Warnings)

	verified := []marketSummaryVerifiedCandidate(nil)
	lastObservedAt := initialCutoff
	if run.Regime.NoTrade {
		_, _ = applyMarketSummaryV150NewsEventGate(run, NewsWindowStatus(evidence.Status))
	} else {
		eventAt, clockErr := marketSummaryV150RunnerClockAt(ports.Clock, "event verification", lastObservedAt)
		if clockErr != nil {
			return recommendation.ProducedDecision{}, clockErr
		}
		lastObservedAt = eventAt
		verified, err = decodeMarketSummaryV150RunnerVerifiedCandidates(run, evidence, eventAt)
		if err != nil {
			return recommendation.ProducedDecision{}, err
		}
		if runEventModel, _ := applyMarketSummaryV150NewsEventGate(run, NewsWindowStatus(evidence.Status)); runEventModel {
			if err := verifyMarketSummaryV150RunnerEvents(ctx, ports.EventVerifier, run, verified, request, p.aiProtocol, eventAt); err != nil {
				degradeMarketSummaryV150EventCandidates(run, run.VerificationSymbols, "model_batch_failed:"+strings.TrimSpace(err.Error()))
			}
		}
		if len(run.VerificationSymbols) > 0 {
			quoteSnapshot, _ := ports.FinalQuotes.FinalQuotes(ctx, recommendation.FinalQuoteRequest{
				RunContext: run.RunContext, AsOf: eventAt, Symbols: append([]string(nil), run.VerificationSymbols...),
			})
			quoteAvailableAt, clockErr := marketSummaryV150RunnerClockAt(ports.Clock, "final quote availability", lastObservedAt)
			if clockErr != nil {
				return recommendation.ProducedDecision{}, clockErr
			}
			lastObservedAt = quoteAvailableAt
			if err := applyMarketSummaryV150RunnerFinalQuotes(run, quoteSnapshot, quoteAvailableAt); err != nil {
				return recommendation.ProducedDecision{}, err
			}
		}
	}

	portfolioAt, err := marketSummaryV150RunnerClockAt(ports.Clock, "portfolio snapshot", lastObservedAt)
	if err != nil {
		return recommendation.ProducedDecision{}, err
	}
	portfolioContext := run.RunContext
	portfolioContext.DataCutoffAt = portfolioAt
	portfolioSnapshot, portfolioErr := ports.Portfolio.PortfolioSnapshot(ctx, recommendation.PortfolioRequest{RunContext: portfolioContext})
	portfolio := cloneV150PortfolioState(portfolioSnapshot.State)
	if portfolioErr != nil {
		warning := "portfolio_state_degraded:" + strings.TrimSpace(portfolioErr.Error())
		run.Warnings = append(run.Warnings, warning)
		run.PortfolioStateStatus = "failed"
		portfolio = cloneV150PortfolioState(v150.PortfolioState{})
	} else {
		run.PortfolioStateStatus = "ok"
	}
	finalCutoff, err := marketSummaryV150RunnerClockAt(ports.Clock, "data cutoff", portfolioAt)
	if err != nil {
		return recommendation.ProducedDecision{}, err
	}
	run.RunContext.DataCutoffAt = finalCutoff
	run.PortfolioBefore = cloneV150PortfolioState(portfolio)
	decisionAt, err := marketSummaryV150RunnerClockAt(ports.Clock, "decision", finalCutoff)
	if err != nil {
		return recommendation.ProducedDecision{}, err
	}
	if err := finalizeMarketSummaryV150Run(run, verified, portfolio, decisionAt); err != nil {
		return recommendation.ProducedDecision{}, err
	}
	if err := refreshMarketSummaryV150DataHash(run); err != nil {
		return recommendation.ProducedDecision{}, err
	}
	run.Warnings = dedupeNonEmptyStrings(run.Warnings, 256)

	return recommendation.ProducedDecision{
		Decision: run, StrategyVersion: request.StrategyVersion, ConfigHash: request.ConfigHash,
	}, nil
}

func (p *marketSummaryV150RunnerCompatPipeline) validate(request recommendation.BuildRequest) error {
	if p == nil || p.startedAt.IsZero() {
		return fmt.Errorf("%w: startedAt is required", errMarketSummaryV150RunnerCompatInput)
	}
	if request.StrategyVersion != v150.StrategyVersion || request.ConfigHash != v150.FixedStrategyV150ConfigHash() {
		return fmt.Errorf("%w: strategy identity is not frozen V1.5.0", errMarketSummaryV150RunnerCompatInput)
	}
	return nil
}

func marketSummaryV150RunnerClockAt(clock recommendation.Clock, stage string, notBefore time.Time) (time.Time, error) {
	if clock == nil {
		return time.Time{}, fmt.Errorf("%w: clock is unavailable", errMarketSummaryV150RunnerCompatInput)
	}
	at := clock.Now()
	if at.IsZero() || (!notBefore.IsZero() && at.Before(notBefore)) {
		return time.Time{}, fmt.Errorf("%w: %s time is zero or non-monotonic", errMarketSummaryV150RunnerCompatInput, stage)
	}
	return at, nil
}

func decodeMarketSummaryV150RunnerBenchmarkSource(snapshot recommendation.MarketSnapshot) (MarketSummaryV150BenchmarkSource, error) {
	var source MarketSummaryV150BenchmarkSource
	if len(snapshot.CompatibilityProjection) == 0 || !json.Valid(snapshot.CompatibilityProjection) {
		return source, fmt.Errorf("%w: benchmark compatibility projection is required", errMarketSummaryV150RunnerCompatInput)
	}
	if err := json.Unmarshal(snapshot.CompatibilityProjection, &source); err != nil {
		return source, fmt.Errorf("%w: decode benchmark provenance: %v", errMarketSummaryV150RunnerCompatInput, err)
	}
	if strings.TrimSpace(source.Timing.EvidenceID) == "" {
		return source, fmt.Errorf("%w: benchmark evidence id is required", errMarketSummaryV150RunnerCompatInput)
	}
	if !marketSummaryV150RunnerEvidenceContainsTiming(snapshot.Evidence, source.Timing) {
		return source, fmt.Errorf("%w: benchmark projection does not match normalized evidence", errMarketSummaryV150RunnerCompatInput)
	}
	if len(snapshot.Evidence) != 1 {
		return source, fmt.Errorf("%w: benchmark normalized evidence does not exactly match its projection", errMarketSummaryV150RunnerCompatInput)
	}
	return source, nil
}

func decodeMarketSummaryV150RunnerCandidates(
	batch recommendation.CandidateBatch,
	cutoff time.Time,
) ([]v150.Candidate, map[string]MarketSummaryV150SourceCandidate, error) {
	candidates := make([]v150.Candidate, 0, len(batch.Items))
	sources := make(map[string]MarketSummaryV150SourceCandidate, len(batch.Items))
	seen := make(map[string]bool, len(batch.Items))
	for index, item := range batch.Items {
		symbol := normalizeRecommendStockCode(item.Candidate.Symbol)
		if symbol == "" || seen[symbol] {
			return nil, nil, fmt.Errorf("%w: candidate %d has an empty or duplicate symbol", errMarketSummaryV150RunnerCompatInput, index)
		}
		seen[symbol] = true
		if err := validateMarketSummaryV150RunnerEvidence(item.Evidence, cutoff, symbol); err != nil {
			return nil, nil, err
		}
		if len(item.CompatibilityProjection) == 0 || !json.Valid(item.CompatibilityProjection) {
			return nil, nil, fmt.Errorf("%w: candidate %s compatibility projection is required", errMarketSummaryV150RunnerCompatInput, symbol)
		}
		var source MarketSummaryV150SourceCandidate
		if err := json.Unmarshal(item.CompatibilityProjection, &source); err != nil {
			return nil, nil, fmt.Errorf("%w: decode candidate %s provenance: %v", errMarketSummaryV150RunnerCompatInput, symbol, err)
		}
		if normalizeRecommendStockCode(source.StockCode) != symbol {
			return nil, nil, fmt.Errorf("%w: candidate %s provenance identity mismatch", errMarketSummaryV150RunnerCompatInput, symbol)
		}
		projectedTimings := marketSummaryV150RunnerSourceTimings(source, symbol)
		for _, timing := range projectedTimings {
			if !marketSummaryV150RunnerEvidenceContainsTiming(item.Evidence, timing) {
				return nil, nil, fmt.Errorf("%w: candidate %s provenance evidence %s is not normalized", errMarketSummaryV150RunnerCompatInput, symbol, timing.EvidenceID)
			}
		}
		if len(item.Evidence) != len(projectedTimings) {
			return nil, nil, fmt.Errorf("%w: candidate %s normalized evidence does not exactly match its projection", errMarketSummaryV150RunnerCompatInput, symbol)
		}
		candidate := item.Candidate
		candidate.Symbol = symbol
		candidates = append(candidates, candidate)
		sources[symbol] = source
	}
	return candidates, sources, nil
}

func marketSummaryV150RunnerSourceTimings(source MarketSummaryV150SourceCandidate, symbol string) []MarketSummaryV150EvidenceTiming {
	result := append([]MarketSummaryV150EvidenceTiming(nil), source.EventEvidence...)
	result = append(result, source.IndicatorEvidence...)
	if source.QuoteEvidence != nil {
		result = append(result, *source.QuoteEvidence)
	}
	if !source.DailyData.SourceAt.IsZero() || !source.DailyData.AvailableAt.IsZero() {
		result = append(result, MarketSummaryV150EvidenceTiming{
			EvidenceID:   "daily-qfq:" + symbol + ":" + source.DailyData.LatestTradeDate,
			EvidenceType: "adjusted_daily_bar", SourceAt: source.DailyData.SourceAt, AvailableAt: source.DailyData.AvailableAt,
		})
	}
	securitySourceAt, _ := parseMarketSummaryV150EvidenceTime(source.Security.SourceAt)
	securityAvailableAt, _ := parseMarketSummaryV150EvidenceTime(source.Security.AvailableAt)
	if !securitySourceAt.IsZero() || !securityAvailableAt.IsZero() {
		result = append(result, MarketSummaryV150EvidenceTiming{
			EvidenceID: "security-master:" + symbol, EvidenceType: "security_master",
			SourceAt: securitySourceAt, AvailableAt: securityAvailableAt,
		})
	}
	return result
}

func marketSummaryV150RunnerEvidenceContainsTiming(items []marketintel.Evidence, timing MarketSummaryV150EvidenceTiming) bool {
	for _, item := range items {
		if strings.TrimSpace(item.ID) == strings.TrimSpace(timing.EvidenceID) &&
			string(item.Type) == strings.TrimSpace(timing.EvidenceType) &&
			item.SourceAt.Equal(timing.SourceAt) && item.AvailableAt.Equal(timing.AvailableAt) {
			return true
		}
	}
	return false
}

func validateMarketSummaryV150RunnerEvidence(items []marketintel.Evidence, cutoff time.Time, owner string) error {
	seen := make(map[string]bool, len(items))
	for index, item := range items {
		id := strings.TrimSpace(item.ID)
		if id == "" || seen[id] || strings.TrimSpace(string(item.Type)) == "" || item.SourceAt.IsZero() || item.AvailableAt.IsZero() ||
			item.SourceAt.After(item.AvailableAt) || item.AvailableAt.After(cutoff) {
			return fmt.Errorf("%w: %s evidence %d is incomplete, duplicate or non-causal", errMarketSummaryV150RunnerCompatInput, owner, index)
		}
		seen[id] = true
	}
	return nil
}

func marketSummaryV150RunnerVerificationCandidates(run *MarketSummaryV150RunSnapshot) []v150.ScoredCandidate {
	if run == nil {
		return nil
	}
	selected := make(map[string]bool, len(run.VerificationSymbols))
	for _, symbol := range run.VerificationSymbols {
		selected[normalizeRecommendStockCode(symbol)] = true
	}
	result := make([]v150.ScoredCandidate, 0, len(selected))
	for _, row := range run.Candidates {
		if !selected[normalizeRecommendStockCode(row.Candidate.Symbol)] {
			continue
		}
		result = append(result, v150.ScoredCandidate{
			Candidate: row.Candidate, Score: row.Score, Eligibility: cloneV150Eligibility(row.Eligibility), Rank: row.Rank,
		})
	}
	return result
}

// applyMarketSummaryV150RunnerFinalQuotes is the typed equivalent of
// refreshMarketSummaryV150VerificationQuotes. Membership is fixed before this
// stage; quotes may only update executable price/security fields and scores.
func applyMarketSummaryV150RunnerFinalQuotes(
	run *MarketSummaryV150RunSnapshot,
	snapshot recommendation.FinalQuoteSnapshot,
	availableAt time.Time,
) error {
	if run == nil || availableAt.IsZero() {
		return fmt.Errorf("%w: final quote run/time is required", errMarketSummaryV150RunnerCompatInput)
	}
	selected := make(map[string]bool, len(run.VerificationSymbols))
	for _, raw := range run.VerificationSymbols {
		selected[normalizeRecommendStockCode(raw)] = true
	}
	quotes := make(map[string]recommendation.FinalQuote, len(snapshot.Quotes))
	for index, quote := range snapshot.Quotes {
		symbol := normalizeRecommendStockCode(quote.Symbol)
		if symbol == "" || !selected[symbol] || quotes[symbol].Symbol != "" {
			return fmt.Errorf("%w: final quote %d has an empty, duplicate or unrequested symbol", errMarketSummaryV150RunnerCompatInput, index)
		}
		quote.Symbol = symbol
		quotes[symbol] = quote
	}
	run.Warnings = append(run.Warnings, snapshot.Warnings...)
	for index := range run.Candidates {
		row := &run.Candidates[index]
		symbol := normalizeRecommendStockCode(row.Candidate.Symbol)
		if !selected[symbol] {
			continue
		}
		quote := quotes[symbol]
		row.Source.QuoteEvidence = &MarketSummaryV150EvidenceTiming{
			EvidenceID:   "realtime-quote:" + symbol + ":" + quote.SourceAt.Format(time.RFC3339Nano),
			EvidenceType: "realtime_quote", SourceAt: quote.SourceAt, AvailableAt: availableAt,
		}
		if currentName := strings.TrimSpace(quote.Name); currentName != "" {
			row.Candidate.Name = currentName
			row.Source.StockName = currentName
			row.Source.Security.Name = currentName
		}
		if marketSummaryV150SecurityNameIsST(quote.Name) {
			row.Candidate.ST = true
			row.Source.InputWarnings = append(row.Source.InputWarnings, "final_security_st")
			run.Warnings = append(run.Warnings, symbol+":final_security_st")
		}
		fresh := quote.HasPrice && quote.HasPreviousClose && quote.HasOpen &&
			marketSummaryV150RunnerFinite(quote.Price) && marketSummaryV150RunnerFinite(quote.PreviousClose) && marketSummaryV150RunnerFinite(quote.Open) &&
			quote.Price > 0 && quote.PreviousClose > 0 && quote.Open > 0 &&
			marketSummaryV150QuoteTimestampIsFresh(quote.SourceAt, availableAt)
		row.Candidate.HasCurrentData = fresh
		if !fresh {
			row.Candidate.Price = 0
			row.Candidate.PreviousClose = 0
			row.Candidate.DayChangeRatio = 0
			row.Candidate.GapRatio = 0
			row.Source.InputWarnings = append(row.Source.InputWarnings, "final_current_quote_missing_or_stale")
			run.Warnings = append(run.Warnings, symbol+":final_current_quote_missing_or_stale")
			continue
		}
		row.Candidate.Price = quote.Price
		row.Candidate.PreviousClose = quote.PreviousClose
		row.Candidate.DayChangeRatio = quote.Price/quote.PreviousClose - 1
		row.Candidate.GapRatio = quote.Open/quote.PreviousClose - 1
		if !quote.HasAmount || !marketSummaryV150RunnerFinite(quote.Amount) || quote.Amount <= 0 || !quote.HasVolume {
			row.Candidate.Suspended = true
		}
		indicator := marketSummaryIndicatorCandidate{
			StockCode: symbol, StockName: row.Source.StockName, BkName: row.Source.BkName,
			Direction: row.Source.Direction, Metrics: row.Source.Metrics,
		}
		row.Candidate.Signals.SetupQuality = marketSummaryV150SetupSignal(row.Candidate, indicator)
		row.Candidate.Signals.LiquidityRiskQuality = marketSummaryV150LiquiditySignal(row.Candidate)
	}
	return nil
}

func marketSummaryV150RunnerFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func validateMarketSummaryV150RunnerEvidenceStatus(status recommendation.EvidenceStatus) error {
	switch status {
	case recommendation.EvidenceStatusOK, recommendation.EvidenceStatusEmpty, recommendation.EvidenceStatusFailed, recommendation.EvidenceStatusStale:
		return nil
	default:
		return fmt.Errorf("%w: evidence status %q is unknown", errMarketSummaryV150RunnerCompatInput, status)
	}
}

func decodeMarketSummaryV150RunnerVerifiedCandidates(
	run *MarketSummaryV150RunSnapshot,
	snapshot recommendation.EvidenceSnapshot,
	cutoff time.Time,
) ([]marketSummaryVerifiedCandidate, error) {
	allowed := make(map[string]bool, len(run.VerificationSymbols))
	for _, symbol := range run.VerificationSymbols {
		allowed[normalizeRecommendStockCode(symbol)] = true
	}
	seen := make(map[string]bool, len(snapshot.Candidates))
	result := make([]marketSummaryVerifiedCandidate, 0, len(snapshot.Candidates))
	for index, candidateEvidence := range snapshot.Candidates {
		symbol := normalizeRecommendStockCode(candidateEvidence.Symbol)
		if symbol == "" || seen[symbol] || !allowed[symbol] || candidateEvidence.VerifiedAt.IsZero() || candidateEvidence.VerifiedAt.After(cutoff) {
			return nil, fmt.Errorf("%w: verified candidate %d is not in the frozen top set or has invalid time", errMarketSummaryV150RunnerCompatInput, index)
		}
		seen[symbol] = true
		if err := validateMarketSummaryV150RunnerEvidence(candidateEvidence.Items, cutoff, symbol+" verification"); err != nil {
			return nil, err
		}
		verified, err := decodeMarketSummaryV150RunnerVerifiedProjection(candidateEvidence)
		if err != nil {
			return nil, err
		}
		result = append(result, verified)
	}
	sort.SliceStable(result, func(i, j int) bool {
		left, right := marketSummaryV150RunnerVerificationIndex(run, result[i].StockCode), marketSummaryV150RunnerVerificationIndex(run, result[j].StockCode)
		return left < right
	})
	return result, nil
}

func decodeMarketSummaryV150RunnerVerifiedProjection(candidate recommendation.CandidateEvidence) (marketSummaryVerifiedCandidate, error) {
	symbol := normalizeRecommendStockCode(candidate.Symbol)
	verified := marketSummaryVerifiedCandidate{StockCode: symbol, VerifiedAt: candidate.VerifiedAt}
	if len(candidate.CompatibilityProjection) > 0 {
		if !json.Valid(candidate.CompatibilityProjection) {
			return verified, fmt.Errorf("%w: verified candidate %s projection is invalid JSON", errMarketSummaryV150RunnerCompatInput, symbol)
		}
		if err := json.Unmarshal(candidate.CompatibilityProjection, &verified); err != nil {
			return verified, fmt.Errorf("%w: decode verified candidate %s projection: %v", errMarketSummaryV150RunnerCompatInput, symbol, err)
		}
		if normalizeRecommendStockCode(verified.StockCode) != symbol || !verified.VerifiedAt.Equal(candidate.VerifiedAt) {
			return verified, fmt.Errorf("%w: verified candidate %s projection identity mismatch", errMarketSummaryV150RunnerCompatInput, symbol)
		}
	} else {
		verified.EvidenceSources = marketSummaryV150RunnerEvidenceReferences(candidate.Items)
	}
	projectedEvidence := make(map[string]aiEvidenceReference, len(verified.EvidenceSources))
	for index, ref := range verified.EvidenceSources {
		id := firstNonEmptyText(strings.TrimSpace(ref.RawHash), strings.TrimSpace(ref.DedupeKey))
		publishedAt, published := parseMarketSummaryV150EvidenceTime(ref.PublishedAt)
		if !published {
			continue
		}
		if id == "" {
			id = fmt.Sprintf("verified:%s:%d:%s", symbol, index, marketSummaryV150StableHash(ref.SourceName+"|"+ref.Title+"|"+ref.PublishedAt))
		}
		if _, duplicate := projectedEvidence[id]; duplicate {
			return verified, fmt.Errorf("%w: verified candidate %s projection has duplicate evidence %s", errMarketSummaryV150RunnerCompatInput, symbol, id)
		}
		ref.PublishedAt = publishedAt.Format(time.RFC3339Nano)
		projectedEvidence[id] = ref
	}
	for _, item := range candidate.Items {
		ref, ok := projectedEvidence[strings.TrimSpace(item.ID)]
		if !ok {
			return verified, fmt.Errorf("%w: verified candidate %s evidence %s is absent from its projection", errMarketSummaryV150RunnerCompatInput, symbol, item.ID)
		}
		publishedAt, ok := parseMarketSummaryV150EvidenceTime(ref.PublishedAt)
		if !ok || !publishedAt.Equal(item.SourceAt) || !candidate.VerifiedAt.Equal(item.AvailableAt) {
			return verified, fmt.Errorf("%w: verified candidate %s evidence %s timing mismatch", errMarketSummaryV150RunnerCompatInput, symbol, item.ID)
		}
	}
	if len(candidate.Items) != len(projectedEvidence) {
		return verified, fmt.Errorf("%w: verified candidate %s normalized evidence does not exactly match its projection", errMarketSummaryV150RunnerCompatInput, symbol)
	}
	return verified, nil
}

func marketSummaryV150RunnerEvidenceReferences(items []marketintel.Evidence) []aiEvidenceReference {
	result := make([]aiEvidenceReference, 0, len(items))
	for _, item := range items {
		result = append(result, aiEvidenceReference{
			Type: string(item.Type), Title: item.Title, Summary: string(item.Payload), SourceName: item.Source,
			PublishedAt: item.SourceAt.Format(time.RFC3339Nano), RawHash: item.ID, EntityCode: normalizeRecommendStockCode(item.Symbol),
		})
	}
	return result
}

func marketSummaryV150RunnerVerificationIndex(run *MarketSummaryV150RunSnapshot, symbol string) int {
	normalized := normalizeRecommendStockCode(symbol)
	for index, item := range run.VerificationSymbols {
		if normalizeRecommendStockCode(item) == normalized {
			return index
		}
	}
	return len(run.VerificationSymbols)
}

func applyMarketSummaryV150RunnerInputWarnings(run *MarketSummaryV150RunSnapshot, batchWarnings []string) {
	if run == nil {
		return
	}
	run.Warnings = append(run.Warnings, batchWarnings...)
	sources := marketSummaryV150RunnerSources(run)
	for _, symbol := range sortedMarketSummaryV150SourceSymbols(sources) {
		for _, warning := range sources[symbol].InputWarnings {
			if warning = strings.TrimSpace(warning); warning != "" {
				run.Warnings = append(run.Warnings, symbol+":"+warning)
			}
		}
	}
	run.Warnings = dedupeNonEmptyStrings(run.Warnings, 256)
}

func marketSummaryV150RunnerSources(run *MarketSummaryV150RunSnapshot) map[string]MarketSummaryV150SourceCandidate {
	result := make(map[string]MarketSummaryV150SourceCandidate)
	if run == nil {
		return result
	}
	for _, candidate := range run.Candidates {
		result[normalizeRecommendStockCode(candidate.Candidate.Symbol)] = candidate.Source
	}
	return result
}

func applyMarketSummaryV150RunnerEvidenceWarnings(run *MarketSummaryV150RunSnapshot, warning string, status recommendation.EvidenceStatus) {
	if run == nil {
		return
	}
	if warning = strings.TrimSpace(warning); warning != "" {
		run.Warnings = append(run.Warnings, "news:"+warning)
	}
	if status == recommendation.EvidenceStatusOK {
		return
	}
	run.Warnings = append(run.Warnings,
		"news_status:"+string(status),
		"event_evidence_degraded:news_"+string(status),
	)
}

func verifyMarketSummaryV150RunnerEvents(
	ctx context.Context,
	verifier recommendation.EventVerifier,
	run *MarketSummaryV150RunSnapshot,
	verified []marketSummaryVerifiedCandidate,
	request recommendation.BuildRequest,
	aiProtocol string,
	cutoff time.Time,
) error {
	modelRequest := buildMarketSummaryV150RunnerEventRequest(run, verified, cutoff)
	if len(modelRequest.Candidates) == 0 {
		degradeMarketSummaryV150EventCandidates(run, run.VerificationSymbols, "model_evidence_empty")
		return nil
	}
	payload, err := json.Marshal(modelRequest)
	if err != nil {
		return fmt.Errorf("encode event verification request: %w", err)
	}
	messages := []map[string]any{
		{"role": "system", "content": marketSummaryV150EventModelSystemPrompt},
		{"role": "user", "content": marketSummaryV150EventModelSchemaPrompt + "\nInput JSON:\n" + string(payload)},
	}
	completion, verifyErr := verifier.Verify(ctx, recommendation.EventVerificationCall{Messages: messages, Think: false})
	run.PromptHash = marketSummaryV150StableHash(marketSummaryV150EventModelSystemPrompt + "\n" + marketSummaryV150EventModelSchemaPrompt)
	modelIdentity := strings.Join([]string{
		strings.TrimSpace(request.ProviderName), strings.TrimSpace(request.ModelName), strings.TrimSpace(completion.Model), NormalizeAIAPIProtocol(aiProtocol),
	}, "|")
	run.ModelHash = marketSummaryV150StableHash(modelIdentity)
	if verifyErr != nil {
		return verifyErr
	}
	return applyMarketSummaryV150EventModelResponse(run, modelRequest, completion.Content, firstNonEmptyText(strings.TrimSpace(completion.Model), strings.TrimSpace(request.ModelName), "event_model"))
}

func buildMarketSummaryV150RunnerEventRequest(
	run *MarketSummaryV150RunSnapshot,
	verified []marketSummaryVerifiedCandidate,
	cutoff time.Time,
) marketSummaryV150EventModelRequest {
	request := marketSummaryV150EventModelRequest{AsOf: cutoff.Format(time.RFC3339Nano), Candidates: []marketSummaryV150EventModelCandidate{}}
	if run == nil || cutoff.IsZero() {
		return request
	}
	verifiedBySymbol := make(map[string]marketSummaryVerifiedCandidate, len(verified))
	for _, item := range verified {
		if symbol := normalizeRecommendStockCode(item.StockCode); symbol != "" {
			verifiedBySymbol[symbol] = item
		}
	}
	for _, rawSymbol := range run.VerificationSymbols {
		symbol := normalizeRecommendStockCode(rawSymbol)
		if symbol == "" {
			continue
		}
		candidate := marketSummaryV150EventModelCandidate{Symbol: symbol, Evidence: []marketSummaryV150EventModelEvidence{}, SourceAt: map[string]MarketSummaryV150EvidenceTiming{}}
		item := verifiedBySymbol[symbol]
		for index, ref := range item.EvidenceSources {
			sourceAt, ok := parseMarketSummaryV150EvidenceTime(ref.PublishedAt)
			if !ok || sourceAt.After(cutoff) || cutoff.Sub(sourceAt) > v150.FixedStrategyV150Config().EventFreshness {
				continue
			}
			evidenceID := firstNonEmptyText(strings.TrimSpace(ref.RawHash), strings.TrimSpace(ref.DedupeKey))
			if evidenceID == "" {
				evidenceID = fmt.Sprintf("verified:%s:%d:%s", symbol, index, marketSummaryV150StableHash(ref.SourceName+"|"+ref.Title+"|"+ref.PublishedAt))
			}
			if _, duplicate := candidate.SourceAt[evidenceID]; duplicate {
				continue
			}
			candidate.Evidence = append(candidate.Evidence, marketSummaryV150EventModelEvidence{
				EvidenceID: evidenceID, Title: strings.TrimSpace(ref.Title), Summary: strings.TrimSpace(ref.Summary),
				Source: firstNonEmptyText(strings.TrimSpace(ref.SourceName), strings.TrimSpace(ref.SourceType)), PublishedAt: sourceAt.Format(time.RFC3339Nano),
			})
			candidate.SourceAt[evidenceID] = MarketSummaryV150EvidenceTiming{
				EvidenceID: evidenceID, EvidenceType: firstNonEmptyText(ref.Type, ref.SourceType, "verified_evidence"), SourceAt: sourceAt, AvailableAt: cutoff,
			}
		}
		sort.SliceStable(candidate.Evidence, func(i, j int) bool { return candidate.Evidence[i].EvidenceID < candidate.Evidence[j].EvidenceID })
		request.Candidates = append(request.Candidates, candidate)
	}
	return request
}

var _ recommendation.Pipeline = (*marketSummaryV150RunnerCompatPipeline)(nil)
