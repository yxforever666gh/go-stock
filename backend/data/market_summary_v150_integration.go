package data

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"go-stock/backend/strategy/v150"
)

const (
	marketSummaryV150NoLegalCandidate = "no_legal_candidate"
	marketSummaryV150DailyCapReached  = "daily_entry_cap_reached"
	marketSummaryV150ScoreBelowFloor  = "score_below_70"
	marketSummaryV150CausalityReject  = "evidence_causality_invalid"
	marketSummaryV150PortfolioUnknown = "portfolio_state_unavailable"
)

func marketSummaryV150RiskOnDailyCap() int {
	cap := v150.FixedStrategyV150Config().RiskOnDailyCap
	if cap < 0 {
		return 0
	}
	return cap
}

// MarketSummaryV150SourceCandidate is the frozen, provider-facing row that
// introduced a symbol into the deterministic universe. It is deliberately
// separate from v150.Candidate: the latter contains only normalized strategy
// inputs, while this value preserves source provenance for replay/audit.
type MarketSummaryV150SourceCandidate struct {
	StockName         string                            `json:"stockName"`
	StockCode         string                            `json:"stockCode"`
	Direction         string                            `json:"direction,omitempty"`
	BkName            string                            `json:"bkName,omitempty"`
	Source            string                            `json:"source,omitempty"`
	Reason            string                            `json:"reason,omitempty"`
	Metrics           map[string]string                 `json:"metrics,omitempty"`
	SourceNames       []string                          `json:"sourceNames,omitempty"`
	IndicatorEvidence []MarketSummaryV150EvidenceTiming `json:"indicatorEvidence,omitempty"`
	InputWarnings     []string                          `json:"inputWarnings,omitempty"`
	Security          MarketSummaryV150SecuritySource   `json:"security"`
	DailyData         MarketSummaryV150DailyDataSource  `json:"dailyData"`
	EventEvidence     []MarketSummaryV150EvidenceTiming `json:"eventEvidence,omitempty"`
	QuoteEvidence     *MarketSummaryV150EvidenceTiming  `json:"quoteEvidence,omitempty"`
	EventAssessment   MarketSummaryV150EventAssessment  `json:"eventAssessment"`
}

type MarketSummaryV150SecuritySource struct {
	Name        string `json:"name,omitempty"`
	Market      string `json:"market,omitempty"`
	Exchange    string `json:"exchange,omitempty"`
	Board       string `json:"board,omitempty"`
	Industry    string `json:"industry,omitempty"`
	Currency    string `json:"currency,omitempty"`
	ListStatus  string `json:"listStatus,omitempty"`
	ListDate    string `json:"listDate,omitempty"`
	DelistDate  string `json:"delistDate,omitempty"`
	ObservedAt  string `json:"observedAt,omitempty"`
	SourceAt    string `json:"sourceAt,omitempty"`
	AvailableAt string `json:"availableAt,omitempty"`
	Source      string `json:"source"`
}

type MarketSummaryV150DailyDataSource struct {
	AdjustmentSource string    `json:"adjustmentSource"`
	LatestTradeDate  string    `json:"latestTradeDate,omitempty"`
	AdjustmentFactor float64   `json:"adjustmentFactor"`
	Complete         bool      `json:"complete"`
	SourceAt         time.Time `json:"sourceAt"`
	AvailableAt      time.Time `json:"availableAt"`
}

type MarketSummaryV150EvidenceTiming struct {
	EvidenceID   string    `json:"evidenceId"`
	EvidenceType string    `json:"evidenceType"`
	SourceAt     time.Time `json:"sourceAt"`
	AvailableAt  time.Time `json:"availableAt"`
}

type MarketSummaryV150EventAssessment struct {
	Direction   string   `json:"direction"`
	Relevance   float64  `json:"relevance"`
	Importance  float64  `json:"importance"`
	Confidence  float64  `json:"confidence"`
	EvidenceIDs []string `json:"evidenceIds,omitempty"`
	Verifier    string   `json:"verifier"`
}

type MarketSummaryV150CandidateSnapshot struct {
	Source               MarketSummaryV150SourceCandidate  `json:"source"`
	Candidate            v150.Candidate                    `json:"candidate"`
	Score                v150.ScoreBreakdown               `json:"score"`
	Eligibility          v150.EligibilityResult            `json:"eligibility"`
	PortfolioEligibility v150.EligibilityResult            `json:"portfolioEligibility"`
	Rank                 int                               `json:"rank"`
	PreVerificationRank  int                               `json:"preVerificationRank"`
	FinalRank            int                               `json:"finalRank"`
	VerificationSelected bool                              `json:"verificationSelected"`
	Verified             bool                              `json:"verified"`
	ProductionSelected   bool                              `json:"productionSelected"`
	SelectionReasons     []string                          `json:"selectionReasons,omitempty"`
	EvidenceTimeline     []MarketSummaryV150EvidenceTiming `json:"evidenceTimeline,omitempty"`
}

type MarketSummaryV150ProductionSnapshot struct {
	Symbol   string                                 `json:"symbol"`
	Rank     int                                    `json:"rank"`
	Score    v150.ScoreBreakdown                    `json:"score"`
	Plan     v150.TradePlan                         `json:"plan"`
	Evidence MarketSummaryVerifiedCandidateSnapshot `json:"evidence"`
}

type MarketSummaryV150BenchmarkSource struct {
	Timing           MarketSummaryV150EvidenceTiming `json:"timing"`
	AdjustmentSource string                          `json:"adjustmentSource"`
	LatestTradeDate  string                          `json:"latestTradeDate,omitempty"`
	Complete         bool                            `json:"complete"`
}

// MarketSummaryV150RunSnapshot is passed from phase-3 to the app runtime. The
// runtime persists this exact value; it does not reconstruct features from the
// final model prose.
type MarketSummaryV150RunSnapshot struct {
	RunContext           v150.RunContext                       `json:"runContext"`
	RunSlot              string                                `json:"runSlot"`
	DataHash             string                                `json:"dataHash"`
	ModelHash            string                                `json:"modelHash"`
	PromptHash           string                                `json:"promptHash"`
	Warnings             []string                              `json:"warnings,omitempty"`
	PortfolioBefore      v150.PortfolioState                   `json:"portfolioBefore"`
	PortfolioStateStatus string                                `json:"portfolioStateStatus"`
	Benchmark            v150.BenchmarkSnapshot                `json:"benchmark"`
	BenchmarkSource      MarketSummaryV150BenchmarkSource      `json:"benchmarkSource"`
	Regime               v150.RegimeDecision                   `json:"regime"`
	Candidates           []MarketSummaryV150CandidateSnapshot  `json:"candidates"`
	VerificationSymbols  []string                              `json:"verificationSymbols"`
	Production           []MarketSummaryV150ProductionSnapshot `json:"production"`
	NoTradeReason        string                                `json:"noTradeReason,omitempty"`
}

const (
	marketSummaryV150LocalModelSpec  = "strategy-v150-local-evidence-verifier-v1"
	marketSummaryV150LocalPromptSpec = "event-fields-only:direction,relevance,importance,confidence,evidence_ids;no-ranking,no-target,no-state"
)

func marketSummaryV150SourceFromIndicator(item marketSummaryIndicatorCandidate) MarketSummaryV150SourceCandidate {
	metrics := make(map[string]string, len(item.Metrics))
	for key, value := range item.Metrics {
		metrics[key] = value
	}
	return MarketSummaryV150SourceCandidate{
		StockName:         strings.TrimSpace(item.StockName),
		StockCode:         normalizeRecommendStockCode(item.StockCode),
		Direction:         strings.TrimSpace(item.Direction),
		BkName:            strings.TrimSpace(item.BkName),
		Source:            strings.TrimSpace(item.Source),
		Reason:            strings.TrimSpace(item.Reason),
		Metrics:           metrics,
		SourceNames:       append([]string(nil), item.SourceNames...),
		IndicatorEvidence: append([]MarketSummaryV150EvidenceTiming(nil), item.IndicatorEvidence...),
	}
}

// newMarketSummaryV150Run calls the strategy package's real rank/top
// functions. Callers must pass the complete candidate universe; truncation is
// only performed after RankCandidates has produced a fixed total order.
func newMarketSummaryV150Run(
	startedAt, dataCutoffAt time.Time,
	runSlot string,
	benchmark v150.BenchmarkSnapshot,
	candidates []v150.Candidate,
	sources map[string]MarketSummaryV150SourceCandidate,
) (*MarketSummaryV150RunSnapshot, error) {
	if startedAt.IsZero() || dataCutoffAt.IsZero() || dataCutoffAt.Before(startedAt) {
		return nil, errors.New("v1.5 run requires startedAt <= dataCutoffAt")
	}
	cfg := v150.FixedStrategyV150Config()
	ctx := v150.RunContext{
		RunID:           buildMarketSummaryV150RunID(startedAt),
		StartedAt:       startedAt,
		AsOf:            dataCutoffAt,
		DataCutoffAt:    dataCutoffAt,
		StrategyVersion: v150.StrategyVersion,
		ConfigHash:      cfg.Hash(),
		Mode:            "production",
	}
	regime := v150.ClassifyMarketRegime(benchmark, cfg)
	ranked := v150.RankCandidates(ctx, append([]v150.Candidate(nil), candidates...), regime, cfg)
	top := v150.TopForVerification(ranked, cfg)
	topIndex := make(map[string]bool, len(top))
	verificationSymbols := make([]string, 0, len(top))
	for _, row := range top {
		symbol := normalizeRecommendStockCode(row.Candidate.Symbol)
		topIndex[symbol] = true
		verificationSymbols = append(verificationSymbols, symbol)
	}

	result := &MarketSummaryV150RunSnapshot{
		RunContext:          ctx,
		RunSlot:             strings.TrimSpace(runSlot),
		ModelHash:           marketSummaryV150StableHash(marketSummaryV150LocalModelSpec),
		PromptHash:          marketSummaryV150StableHash(marketSummaryV150LocalPromptSpec),
		Benchmark:           benchmark,
		Regime:              regime,
		Candidates:          make([]MarketSummaryV150CandidateSnapshot, 0, len(ranked)),
		VerificationSymbols: verificationSymbols,
	}
	for _, row := range ranked {
		symbol := normalizeRecommendStockCode(row.Candidate.Symbol)
		source := sources[symbol]
		if source.StockCode == "" {
			source.StockCode = symbol
			source.StockName = strings.TrimSpace(row.Candidate.Name)
			source.BkName = strings.TrimSpace(row.Candidate.Sector)
		}
		evidenceTimeline := append([]MarketSummaryV150EvidenceTiming(nil), source.EventEvidence...)
		evidenceTimeline = append(evidenceTimeline, source.IndicatorEvidence...)
		if source.QuoteEvidence != nil {
			evidenceTimeline = append(evidenceTimeline, *source.QuoteEvidence)
		}
		if !source.DailyData.SourceAt.IsZero() || !source.DailyData.AvailableAt.IsZero() {
			evidenceTimeline = append(evidenceTimeline, MarketSummaryV150EvidenceTiming{
				EvidenceID:   "daily-qfq:" + symbol + ":" + source.DailyData.LatestTradeDate,
				EvidenceType: "adjusted_daily_bar",
				SourceAt:     source.DailyData.SourceAt,
				AvailableAt:  source.DailyData.AvailableAt,
			})
		}
		securitySourceAt, _ := parseMarketSummaryV150EvidenceTime(source.Security.SourceAt)
		securityAvailableAt, _ := parseMarketSummaryV150EvidenceTime(source.Security.AvailableAt)
		if !securitySourceAt.IsZero() || !securityAvailableAt.IsZero() {
			evidenceTimeline = append(evidenceTimeline, MarketSummaryV150EvidenceTiming{
				EvidenceID:   "security-master:" + symbol,
				EvidenceType: "security_master",
				SourceAt:     securitySourceAt,
				AvailableAt:  securityAvailableAt,
			})
		}
		result.Candidates = append(result.Candidates, MarketSummaryV150CandidateSnapshot{
			Source:               source,
			Candidate:            row.Candidate,
			Score:                row.Score,
			Eligibility:          cloneV150Eligibility(row.Eligibility),
			Rank:                 row.Rank,
			PreVerificationRank:  row.Rank,
			FinalRank:            row.Rank,
			VerificationSelected: topIndex[symbol],
			EvidenceTimeline:     evidenceTimeline,
		})
	}
	if regime.NoTrade {
		result.NoTradeReason = v150.RejectRiskOff
	}
	candidatesForHash := append([]v150.Candidate(nil), candidates...)
	sort.SliceStable(candidatesForHash, func(i, j int) bool {
		return normalizeRecommendStockCode(candidatesForHash[i].Symbol) < normalizeRecommendStockCode(candidatesForHash[j].Symbol)
	})
	dataEnvelope := struct {
		Benchmark  v150.BenchmarkSnapshot                      `json:"benchmark"`
		Candidates []v150.Candidate                            `json:"candidates"`
		Sources    map[string]MarketSummaryV150SourceCandidate `json:"sources"`
	}{Benchmark: benchmark, Candidates: candidatesForHash, Sources: sources}
	dataPayload, err := json.Marshal(dataEnvelope)
	if err != nil {
		return nil, fmt.Errorf("marshal v1.5 data hash input: %w", err)
	}
	result.DataHash = marketSummaryV150StableHash(string(dataPayload))
	return result, nil
}

func marketSummaryV150StableHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func refreshMarketSummaryV150DataHash(run *MarketSummaryV150RunSnapshot) error {
	if run == nil {
		return errors.New("v1.5 run is nil")
	}
	payload, err := json.Marshal(struct {
		AsOf                 time.Time                            `json:"asOf"`
		DataCutoffAt         time.Time                            `json:"dataCutoffAt"`
		Benchmark            v150.BenchmarkSnapshot               `json:"benchmark"`
		BenchmarkSource      MarketSummaryV150BenchmarkSource     `json:"benchmarkSource"`
		Candidates           []MarketSummaryV150CandidateSnapshot `json:"candidates"`
		PortfolioBefore      v150.PortfolioState                  `json:"portfolioBefore"`
		PortfolioStateStatus string                               `json:"portfolioStateStatus"`
	}{run.RunContext.AsOf, run.RunContext.DataCutoffAt, run.Benchmark, run.BenchmarkSource, run.Candidates, run.PortfolioBefore, run.PortfolioStateStatus})
	if err != nil {
		return err
	}
	run.DataHash = marketSummaryV150StableHash(string(payload))
	return nil
}

func buildMarketSummaryV150VerificationRoutes(run *MarketSummaryV150RunSnapshot) []marketSummaryRouteCandidate {
	if run == nil || len(run.VerificationSymbols) == 0 {
		return nil
	}
	bySymbol := make(map[string]MarketSummaryV150CandidateSnapshot, len(run.Candidates))
	for _, row := range run.Candidates {
		bySymbol[normalizeRecommendStockCode(row.Candidate.Symbol)] = row
	}
	result := make([]marketSummaryRouteCandidate, 0, len(run.VerificationSymbols))
	for _, symbol := range run.VerificationSymbols {
		row, ok := bySymbol[normalizeRecommendStockCode(symbol)]
		if !ok {
			continue
		}
		result = append(result, marketSummaryRouteCandidate{
			StockName:  firstNonEmptyText(row.Source.StockName, row.Candidate.Name),
			StockCode:  normalizeRecommendStockCode(row.Candidate.Symbol),
			Direction:  firstNonEmptyText(row.Source.Direction, row.Candidate.Sector),
			BkName:     firstNonEmptyText(row.Source.BkName, row.Candidate.Sector),
			Reason:     firstNonEmptyText(row.Source.Reason, fmt.Sprintf("v1.5 deterministic rank=%d score=%d", row.Rank, row.Score.Total)),
			SourceHint: "strategy_v150_ranked",
		})
	}
	return result
}

// finalizeMarketSummaryV150Run applies verification, score floor, executable
// plan and portfolio gates before calling SelectProductionCandidates. Model
// scores, targets and execution-state prose never enter this function.
func finalizeMarketSummaryV150Run(
	run *MarketSummaryV150RunSnapshot,
	verified []marketSummaryVerifiedCandidate,
	portfolio v150.PortfolioState,
	decisionAt time.Time,
) error {
	if run == nil {
		return errors.New("v1.5 run is nil")
	}
	if decisionAt.IsZero() || decisionAt.Before(run.RunContext.DataCutoffAt) {
		return errors.New("v1.5 decisionAt must be >= dataCutoffAt")
	}
	validFrom := nextMarketSummary15MinuteBarStart(decisionAt)
	if validFrom.IsZero() || !validFrom.After(decisionAt) {
		return errors.New("v1.5 validFrom must be strictly after decisionAt")
	}
	run.RunContext.DecisionAt = decisionAt
	run.RunContext.GeneratedAt = decisionAt
	run.RunContext.ValidFromAt = validFrom
	run.RunContext.TradeDayIndex = marketSummaryV150TradeDayIndex(decisionAt)
	run.RunContext.ValidFromTradeDayIndex = marketSummaryV150TradeDayIndex(validFrom)
	if run.RunContext.TradeDayIndex <= 0 || run.RunContext.ValidFromTradeDayIndex < run.RunContext.TradeDayIndex {
		return errors.New("v1.5 run has invalid decision/validFrom trading-day indexes")
	}

	verifiedBySymbol := make(map[string]marketSummaryVerifiedCandidate, len(verified))
	verifiedTimelines := make(map[string][]MarketSummaryV150EvidenceTiming, len(verified))
	verifiedCausalityInvalid := make(map[string]bool, len(verified))
	verifiedSymbols := make(map[string]bool, len(verified))
	for _, item := range verified {
		symbol := normalizeRecommendStockCode(item.StockCode)
		if symbol == "" {
			continue
		}
		sanitized, timeline, invalid := sanitizeMarketSummaryV150VerifiedEvidence(item, run.RunContext.DataCutoffAt)
		verifiedBySymbol[symbol] = sanitized
		verifiedTimelines[symbol] = timeline
		verifiedCausalityInvalid[symbol] = invalid
		verifiedSymbols[symbol] = !invalid
	}

	cfg := v150.FixedStrategyV150Config()
	for index := range run.Candidates {
		row := &run.Candidates[index]
		quoteFreshAtDecision := row.Source.QuoteEvidence != nil &&
			marketSummaryV150QuoteTimestampIsFresh(row.Source.QuoteEvidence.SourceAt, decisionAt)
		if !quoteFreshAtDecision {
			row.Candidate.HasCurrentData = false
			row.Candidate.Price = 0
			run.Warnings = append(run.Warnings, normalizeRecommendStockCode(row.Candidate.Symbol)+":current_quote_stale_at_decision")
		}
		row.Eligibility = v150.EvaluateEligibility(run.RunContext, row.Candidate, run.Regime, cfg)
		row.Score = v150.ScoreCandidate(run.RunContext, row.Candidate, cfg)
	}
	rankMarketSummaryV150CandidatesAfterVerification(run.Candidates)
	ranked := make([]v150.ScoredCandidate, len(run.Candidates))
	acceptedPlan := make(map[string]v150.TradePlan, len(run.Candidates))
	for index := range run.Candidates {
		row := &run.Candidates[index]
		symbol := normalizeRecommendStockCode(row.Candidate.Symbol)
		row.EvidenceTimeline = append(row.EvidenceTimeline, verifiedTimelines[symbol]...)
		if !marketSummaryV150EvidenceTimelineValid(row.EvidenceTimeline, run.RunContext.DataCutoffAt) || verifiedCausalityInvalid[symbol] {
			row.Eligibility.Eligible = false
			row.Eligibility.Reasons = append(row.Eligibility.Reasons, marketSummaryV150CausalityReject)
			verifiedSymbols[symbol] = false
			run.Warnings = append(run.Warnings, symbol+":"+marketSummaryV150CausalityReject)
		}
		row.Verified = verifiedSymbols[symbol]
		row.SelectionReasons = nil
		if row.Score.Total < cfg.ProductionScoreFloor {
			row.SelectionReasons = append(row.SelectionReasons, marketSummaryV150ScoreBelowFloor)
		}
		if row.Eligibility.Eligible && row.Verified && row.Score.Total >= cfg.ProductionScoreFloor {
			planResults := v150.BuildTradePlans(run.RunContext, row.Candidate, run.Regime, cfg)
			plan, ok := selectMarketSummaryV150Plan(planResults)
			if ok {
				acceptedPlan[symbol] = plan
			} else {
				reasons := marketSummaryV150PlanRejectionReasons(planResults)
				row.SelectionReasons = append(row.SelectionReasons, reasons...)
				row.Eligibility.Eligible = false
				row.Eligibility.Reasons = append(row.Eligibility.Reasons, reasons...)
			}
		}
		ranked[index] = v150.ScoredCandidate{
			Candidate:   row.Candidate,
			Score:       row.Score,
			Eligibility: cloneV150Eligibility(row.Eligibility),
			Rank:        row.Rank,
			Verified:    row.Verified,
		}
	}

	// Reserve capacity/sector slots in the already-fixed rank order, so two
	// candidates from one sector cannot both pass a one-entry daily limit.
	state := cloneV150PortfolioState(portfolio)
	remainingDailyCap := run.Regime.DailyCap - state.TodayEntries
	if remainingDailyCap < 0 {
		remainingDailyCap = 0
	}
	reserved := 0
	for index := range ranked {
		row := &run.Candidates[index]
		symbol := normalizeRecommendStockCode(row.Candidate.Symbol)
		if strings.EqualFold(strings.TrimSpace(run.PortfolioStateStatus), "failed") {
			row.PortfolioEligibility = v150.EligibilityResult{Eligible: false, Reasons: []string{marketSummaryV150PortfolioUnknown}}
			row.SelectionReasons = append(row.SelectionReasons, marketSummaryV150PortfolioUnknown)
			ranked[index].Eligibility.Eligible = false
			ranked[index].Eligibility.Reasons = append(ranked[index].Eligibility.Reasons, marketSummaryV150PortfolioUnknown)
			row.Eligibility = cloneV150Eligibility(ranked[index].Eligibility)
			continue
		}
		if !ranked[index].Eligibility.Eligible || !verifiedSymbols[symbol] || row.Score.Total < cfg.ProductionScoreFloor {
			continue
		}
		if _, ok := acceptedPlan[symbol]; !ok {
			continue
		}
		portfolioResult := v150.EvaluatePortfolioEligibility(row.Candidate, state, cfg)
		row.PortfolioEligibility = cloneV150Eligibility(portfolioResult)
		if !portfolioResult.Eligible {
			row.SelectionReasons = append(row.SelectionReasons, portfolioResult.Reasons...)
			ranked[index].Eligibility.Eligible = false
			ranked[index].Eligibility.Reasons = append(ranked[index].Eligibility.Reasons, portfolioResult.Reasons...)
			row.Eligibility = cloneV150Eligibility(ranked[index].Eligibility)
			continue
		}
		if reserved >= remainingDailyCap {
			continue
		}
		state.PendingSymbols[symbol] = true
		if sector := strings.TrimSpace(row.Candidate.Sector); sector != "" {
			state.TodaySectorEntries[sector]++
		}
		reserved++
	}

	selectionRegime := run.Regime
	selectionRegime.DailyCap = remainingDailyCap
	selected := v150.SelectProductionCandidates(ranked, verifiedSymbols, selectionRegime, cfg)
	selectedIndex := make(map[string]v150.ScoredCandidate, len(selected))
	for _, row := range selected {
		selectedIndex[normalizeRecommendStockCode(row.Candidate.Symbol)] = row
	}
	run.Production = nil
	for index := range run.Candidates {
		row := &run.Candidates[index]
		symbol := normalizeRecommendStockCode(row.Candidate.Symbol)
		selectedRow, ok := selectedIndex[symbol]
		if !ok {
			continue
		}
		plan, ok := acceptedPlan[symbol]
		if !ok {
			return fmt.Errorf("v1.5 selected candidate %s has no accepted backend plan", symbol)
		}
		row.ProductionSelected = true
		run.Production = append(run.Production, MarketSummaryV150ProductionSnapshot{
			Symbol:   symbol,
			Rank:     selectedRow.Rank,
			Score:    selectedRow.Score,
			Plan:     plan,
			Evidence: verifiedBySymbol[symbol],
		})
	}
	if len(run.Production) == 0 {
		if run.Regime.NoTrade {
			run.NoTradeReason = v150.RejectRiskOff
		} else if strings.EqualFold(strings.TrimSpace(run.PortfolioStateStatus), "failed") {
			run.NoTradeReason = marketSummaryV150PortfolioUnknown
		} else if remainingDailyCap == 0 {
			run.NoTradeReason = marketSummaryV150DailyCapReached
		} else {
			run.NoTradeReason = marketSummaryV150NoLegalCandidate
		}
	} else {
		run.NoTradeReason = ""
	}
	return nil
}

func rankMarketSummaryV150CandidatesAfterVerification(rows []MarketSummaryV150CandidateSnapshot) {
	for index := range rows {
		if rows[index].PreVerificationRank <= 0 {
			rows[index].PreVerificationRank = rows[index].Rank
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		left, right := rows[i], rows[j]
		if left.Score.Total != right.Score.Total {
			return left.Score.Total > right.Score.Total
		}
		if left.Score.TrendRelative != right.Score.TrendRelative {
			return left.Score.TrendRelative > right.Score.TrendRelative
		}
		if left.Score.Setup != right.Score.Setup {
			return left.Score.Setup > right.Score.Setup
		}
		return normalizeRecommendStockCode(left.Candidate.Symbol) < normalizeRecommendStockCode(right.Candidate.Symbol)
	})
	for index := range rows {
		rows[index].FinalRank = index + 1
		rows[index].Rank = index + 1
	}
}

func sanitizeMarketSummaryV150VerifiedEvidence(item marketSummaryVerifiedCandidate, cutoff time.Time) (marketSummaryVerifiedCandidate, []MarketSummaryV150EvidenceTiming, bool) {
	timeline := make([]MarketSummaryV150EvidenceTiming, 0, len(item.EvidenceSources))
	validRefs := make([]aiEvidenceReference, 0, len(item.EvidenceSources))
	invalid := false
	availableAt := item.VerifiedAt
	for index, ref := range item.EvidenceSources {
		sourceAt, ok := parseMarketSummaryV150EvidenceTime(ref.PublishedAt)
		if !ok {
			// Legacy sources without an actual source timestamp are not evidence in
			// V1.5. They are dropped before persistence and cannot influence output.
			continue
		}
		evidenceID := firstNonEmptyText(strings.TrimSpace(ref.RawHash), strings.TrimSpace(ref.DedupeKey))
		if evidenceID == "" {
			evidenceID = fmt.Sprintf("verified:%s:%d:%s", normalizeRecommendStockCode(item.StockCode), index, marketSummaryV150StableHash(ref.SourceName+"|"+ref.Title+"|"+ref.PublishedAt))
		}
		timing := MarketSummaryV150EvidenceTiming{
			EvidenceID: evidenceID, EvidenceType: firstNonEmptyText(ref.Type, ref.SourceType, "verified_evidence"),
			SourceAt: sourceAt, AvailableAt: availableAt,
		}
		timeline = append(timeline, timing)
		if !marketSummaryV150EvidenceTimelineValid([]MarketSummaryV150EvidenceTiming{timing}, cutoff) {
			invalid = true
			continue
		}
		validRefs = append(validRefs, ref)
	}
	item.EvidenceSources = validRefs
	item.FeasiblePlans = nil
	if len(validRefs) == 0 {
		item.PositiveSignals = nil
		item.VerdictHints = nil
	}
	return item, timeline, invalid
}

func marketSummaryV150EvidenceTimelineValid(timeline []MarketSummaryV150EvidenceTiming, cutoff time.Time) bool {
	if cutoff.IsZero() {
		return false
	}
	for _, evidence := range timeline {
		if strings.TrimSpace(evidence.EvidenceID) == "" || evidence.SourceAt.IsZero() || evidence.AvailableAt.IsZero() ||
			evidence.SourceAt.After(evidence.AvailableAt) || evidence.AvailableAt.After(cutoff) {
			return false
		}
	}
	return true
}

func selectMarketSummaryV150Plan(results []v150.PlanResult) (v150.TradePlan, bool) {
	accepted := make([]v150.TradePlan, 0, len(results))
	for _, result := range results {
		if result.Accepted {
			accepted = append(accepted, result.Plan)
		}
	}
	if len(accepted) == 0 {
		return v150.TradePlan{}, false
	}
	sort.SliceStable(accepted, func(i, j int) bool {
		if accepted[i].RewardRisk != accepted[j].RewardRisk {
			return accepted[i].RewardRisk > accepted[j].RewardRisk
		}
		if accepted[i].Path != accepted[j].Path {
			return accepted[i].Path == v150.PathPullback
		}
		return accepted[i].Symbol < accepted[j].Symbol
	})
	return accepted[0], true
}

func marketSummaryV150PlanRejectionReasons(results []v150.PlanResult) []string {
	reasons := make([]string, 0, len(results))
	for _, result := range results {
		if result.Accepted {
			continue
		}
		reason := strings.TrimSpace(result.Reason)
		if reason == "" {
			reason = v150.RejectPlanInputs
		}
		reasons = append(reasons, "trade_plan:"+reason)
	}
	return dedupeNonEmptyStrings(reasons, 8)
}

func cloneV150Eligibility(value v150.EligibilityResult) v150.EligibilityResult {
	return v150.EligibilityResult{Eligible: value.Eligible, Reasons: append([]string(nil), value.Reasons...)}
}

func cloneV150PortfolioState(value v150.PortfolioState) v150.PortfolioState {
	result := v150.PortfolioState{
		OpenSymbols:            map[string]bool{},
		PendingSymbols:         map[string]bool{},
		TodayEntries:           value.TodayEntries,
		TodaySectorEntries:     map[string]int{},
		TradeDaysSinceLastStop: map[string]int{},
		ExecutionDailyCap:      value.ExecutionDailyCap,
	}
	for key, item := range value.OpenSymbols {
		result.OpenSymbols[normalizeRecommendStockCode(key)] = item
	}
	for key, item := range value.PendingSymbols {
		result.PendingSymbols[normalizeRecommendStockCode(key)] = item
	}
	for key, item := range value.TodaySectorEntries {
		result.TodaySectorEntries[strings.TrimSpace(key)] = item
	}
	for key, item := range value.TradeDaysSinceLastStop {
		result.TradeDaysSinceLastStop[normalizeRecommendStockCode(key)] = item
	}
	return result
}

func buildMarketSummaryV150RunID(startedAt time.Time) string {
	return "market-summary-v150-" + startedAt.UTC().Format("20060102T150405.000000000Z")
}

func marketSummaryV150TradeDayIndex(at time.Time) int {
	if at.IsZero() {
		return 0
	}
	loc := cnLocation()
	target := time.Date(at.In(loc).Year(), at.In(loc).Month(), at.In(loc).Day(), 0, 0, 0, 0, loc)
	epoch := time.Date(2000, 1, 1, 0, 0, 0, 0, loc)
	if target.Before(epoch) {
		return 0
	}
	ordinal := 0
	for day := epoch; !day.After(target); day = day.AddDate(0, 0, 1) {
		if isCNOpenTradeDaySafe(day) {
			ordinal++
		}
	}
	return ordinal
}
