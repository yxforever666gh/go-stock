package data

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"go-stock/backend/models"
	"go-stock/backend/persistence"
	"go-stock/backend/strategy/v150"

	"gorm.io/gorm"
)

// The desktop runtime is a single process, but manual and scheduled summary
// runs may overlap. Serialize the final quota recheck plus legacy-view inserts
// so two runs cannot both consume the same last daily/portfolio slot.
var persistMarketSummaryV150RecommendationsMu sync.Mutex

type marketSummaryV150RunPayload struct {
	Run *MarketSummaryV150RunSnapshot `json:"run"`
}

type marketSummaryV150CandidatePayload struct {
	RunID     string                             `json:"runId"`
	Candidate MarketSummaryV150CandidateSnapshot `json:"candidate"`
}

type marketSummaryV150RulePayload struct {
	RunID      string                              `json:"runId"`
	Production MarketSummaryV150ProductionSnapshot `json:"production"`
}

type marketSummaryV150OrderPayload struct {
	RunID      string `json:"runId"`
	RuleID     string `json:"ruleId,omitempty"`
	Symbol     string `json:"symbol"`
	EventType  string `json:"eventType"`
	Reason     string `json:"reason,omitempty"`
	OccurredAt string `json:"occurredAt"`
}

type marketSummaryV150SecurityPayload struct {
	RunID    string                          `json:"runId"`
	Symbol   string                          `json:"symbol"`
	Security MarketSummaryV150SecuritySource `json:"security"`
}

// PersistMarketSummaryV150Snapshot freezes every ranked candidate, rejection,
// selected rule and initial strategy event in one immutable transaction.
func PersistMarketSummaryV150Snapshot(ctx context.Context, database *gorm.DB, run *MarketSummaryV150RunSnapshot) error {
	if run == nil {
		return errors.New("v1.5 snapshot run is nil")
	}
	if database == nil {
		return errors.New("v1.5 snapshot database is nil")
	}
	if !run.RunContext.ValidTimeline() {
		return errors.New("v1.5 snapshot has an invalid causal timeline")
	}
	if run.BenchmarkSource.Complete {
		if !marketSummaryV150EvidenceTimelineValid([]MarketSummaryV150EvidenceTiming{run.BenchmarkSource.Timing}, run.RunContext.DataCutoffAt) {
			return errors.New("v1.5 benchmark source violates source<=available<=cutoff")
		}
	} else if !run.Benchmark.Stale && run.Benchmark.DataPresent {
		return errors.New("v1.5 benchmark without complete provenance must be marked stale and treated as neutral")
	}
	generatedAt := run.RunContext.GeneratedAt
	if generatedAt.IsZero() {
		generatedAt = run.RunContext.DecisionAt
	}
	frozenAt := time.Now()
	if frozenAt.Before(generatedAt) {
		frozenAt = generatedAt
	}
	tradeDate := run.RunContext.DecisionAt.In(cnLocation()).Format(time.DateOnly)
	validFrom := run.RunContext.ValidFromAt
	decisionAt := run.RunContext.DecisionAt

	runPayload, runHash, err := marshalMarketSummaryV150FrozenPayload(marketSummaryV150RunPayload{Run: run})
	if err != nil {
		return err
	}
	inputPayload := struct {
		Benchmark            v150.BenchmarkSnapshot               `json:"benchmark"`
		BenchmarkSource      MarketSummaryV150BenchmarkSource     `json:"benchmarkSource"`
		Candidates           []MarketSummaryV150CandidateSnapshot `json:"candidates"`
		PortfolioBefore      v150.PortfolioState                  `json:"portfolioBefore"`
		PortfolioStateStatus string                               `json:"portfolioStateStatus"`
	}{
		Benchmark: run.Benchmark, BenchmarkSource: run.BenchmarkSource, Candidates: run.Candidates,
		PortfolioBefore: run.PortfolioBefore, PortfolioStateStatus: run.PortfolioStateStatus,
	}
	_, inputHash, err := marshalMarketSummaryV150FrozenPayload(inputPayload)
	if err != nil {
		return err
	}
	bundle := persistence.StrategySnapshotBundle{
		Run: models.StrategyRunSnapshot{
			RunID:           run.RunContext.RunID,
			StrategyVersion: v150.StrategyVersion,
			TradeDate:       tradeDate,
			RunSlot:         strings.TrimSpace(run.RunSlot),
			StartedAt:       run.RunContext.StartedAt,
			AsOf:            run.RunContext.AsOf,
			DataCutoffAt:    run.RunContext.DataCutoffAt,
			DecisionAt:      run.RunContext.DecisionAt,
			GeneratedAt:     generatedAt,
			ValidFromAt:     &validFrom,
			Mode:            firstNonEmptyText(run.RunContext.Mode, "production"),
			ConfigHash:      run.RunContext.ConfigHash,
			InputHash:       inputHash,
			SnapshotHash:    runHash,
			PayloadJSON:     runPayload,
			FrozenAt:        &frozenAt,
		},
	}

	productionBySymbol := make(map[string]MarketSummaryV150ProductionSnapshot, len(run.Production))
	for _, row := range run.Production {
		productionBySymbol[normalizeRecommendStockCode(row.Symbol)] = row
	}
	for _, row := range run.Candidates {
		symbol := normalizeRecommendStockCode(row.Candidate.Symbol)
		decision, rejection := marketSummaryV150CandidateDecision(row)
		payload, hash, marshalErr := marshalMarketSummaryV150FrozenPayload(marketSummaryV150CandidatePayload{
			RunID: run.RunContext.RunID, Candidate: row,
		})
		if marshalErr != nil {
			return marshalErr
		}
		bundle.Candidates = append(bundle.Candidates, models.CandidateSnapshot{
			CandidateID:     marketSummaryV150CandidateID(run.RunContext.RunID, symbol),
			RunID:           run.RunContext.RunID,
			StrategyVersion: v150.StrategyVersion,
			TradeDate:       tradeDate,
			Symbol:          symbol,
			Name:            firstNonEmptyText(row.Candidate.Name, row.Source.StockName),
			Sector:          firstNonEmptyText(row.Candidate.Sector, row.Source.BkName),
			Market:          string(row.Candidate.Market),
			Rank:            row.Rank,
			PreVerifyRank:   row.PreVerificationRank,
			FinalRank:       row.FinalRank,
			Decision:        decision,
			Score:           float64(row.Score.Total),
			Eligible:        row.Eligibility.Eligible,
			RejectionReason: rejection,
			SnapshotHash:    hash,
			PayloadJSON:     payload,
			FrozenAt:        &frozenAt,
		})

		securityPayload, securityHash, marshalErr := marshalMarketSummaryV150FrozenPayload(marketSummaryV150SecurityPayload{
			RunID: run.RunContext.RunID, Symbol: symbol, Security: row.Source.Security,
		})
		if marshalErr != nil {
			return marshalErr
		}
		listedAt, _ := parseMarketSummaryV150ListDate(row.Source.Security.ListDate)
		var listedAtPointer *time.Time
		if !listedAt.IsZero() {
			listedAtPointer = &listedAt
		}
		delistedAt, _ := parseMarketSummaryV150ListDate(row.Source.Security.DelistDate)
		var delistedAtPointer *time.Time
		if !delistedAt.IsZero() {
			delistedAtPointer = &delistedAt
		}
		status := firstNonEmptyText(strings.TrimSpace(row.Source.Security.ListStatus), "data_missing")
		bundle.SecurityMaster = append(bundle.SecurityMaster, models.SecurityMasterHistory{
			RecordID:        run.RunContext.RunID + "|security|" + symbol,
			RunID:           run.RunContext.RunID,
			SnapshotVersion: v150.StrategyVersion,
			Symbol:          symbol,
			Name:            firstNonEmptyText(row.Source.Security.Name, row.Candidate.Name),
			Market:          firstNonEmptyText(row.Source.Security.Market, string(row.Candidate.Market)),
			Exchange:        strings.TrimSpace(row.Source.Security.Exchange),
			Board:           strings.TrimSpace(row.Source.Security.Board),
			Sector:          firstNonEmptyText(row.Candidate.Sector, row.Source.BkName),
			Industry:        strings.TrimSpace(row.Source.Security.Industry),
			Currency:        firstNonEmptyText(row.Source.Security.Currency, "CNY"),
			Status:          status,
			IsST:            row.Candidate.ST,
			IsSuspended:     row.Candidate.Suspended,
			ListedAt:        listedAtPointer,
			DelistedAt:      delistedAtPointer,
			EffectiveFrom:   run.RunContext.DataCutoffAt,
			Source:          firstNonEmptyText(row.Source.Security.Source, "data_missing"),
			SnapshotHash:    securityHash,
			PayloadJSON:     securityPayload,
			FrozenAt:        &frozenAt,
		})

	}

	eventOrdinal := 0
	for _, production := range run.Production {
		symbol := normalizeRecommendStockCode(production.Symbol)
		ruleID := marketSummaryV150RuleID(run.RunContext.RunID, symbol, production.Plan.Path)
		payload, hash, marshalErr := marshalMarketSummaryV150FrozenPayload(marketSummaryV150RulePayload{
			RunID: run.RunContext.RunID, Production: production,
		})
		if marshalErr != nil {
			return marshalErr
		}
		expiresAt := marketSummaryV150PlanExpiresAt(validFrom, production.Plan.ValidTradeDays)
		bundle.Rules = append(bundle.Rules, models.RuleSnapshot{
			RuleID:          ruleID,
			RunID:           run.RunContext.RunID,
			CandidateID:     marketSummaryV150CandidateID(run.RunContext.RunID, symbol),
			StrategyVersion: v150.StrategyVersion,
			TradeDate:       tradeDate,
			Symbol:          symbol,
			RuleVersion:     v150.StrategyVersion,
			RuleType:        "entry",
			Path:            string(production.Plan.Path),
			ValidFromAt:     production.Plan.ValidFromAt,
			ExpiresAt:       &expiresAt,
			SnapshotHash:    hash,
			PayloadJSON:     payload,
			FrozenAt:        &frozenAt,
		})
		eventOrdinal++
		eventPayload, eventHash, marshalErr := marshalMarketSummaryV150FrozenPayload(marketSummaryV150OrderPayload{
			RunID: run.RunContext.RunID, RuleID: ruleID, Symbol: symbol, EventType: "rule_issued", OccurredAt: decisionAt.Format(time.RFC3339Nano),
		})
		if marshalErr != nil {
			return marshalErr
		}
		bundle.OrderEvents = append(bundle.OrderEvents, models.OrderEvent{
			EventID:         marketSummaryV150EventID(run.RunContext.RunID, eventOrdinal, "rule_issued", symbol),
			RunID:           run.RunContext.RunID,
			RuleID:          ruleID,
			StrategyVersion: v150.StrategyVersion,
			TradeDate:       tradeDate,
			Symbol:          symbol,
			EventType:       "rule_issued",
			Sequence:        1,
			EventAt:         decisionAt,
			Reason:          "backend_v150_trade_plan_frozen",
			SnapshotHash:    eventHash,
			PayloadJSON:     eventPayload,
			FrozenAt:        &frozenAt,
		})
	}
	_ = productionBySymbol
	if len(run.Production) == 0 {
		eventOrdinal++
		reason := firstNonEmptyText(run.NoTradeReason, marketSummaryV150NoLegalCandidate)
		eventPayload, eventHash, marshalErr := marshalMarketSummaryV150FrozenPayload(marketSummaryV150OrderPayload{
			RunID: run.RunContext.RunID, Symbol: v150.BenchmarkCode, EventType: "no_trade", Reason: reason, OccurredAt: decisionAt.Format(time.RFC3339Nano),
		})
		if marshalErr != nil {
			return marshalErr
		}
		bundle.OrderEvents = append(bundle.OrderEvents, models.OrderEvent{
			EventID:         marketSummaryV150EventID(run.RunContext.RunID, eventOrdinal, "no_trade", v150.BenchmarkCode),
			RunID:           run.RunContext.RunID,
			StrategyVersion: v150.StrategyVersion,
			TradeDate:       tradeDate,
			Symbol:          v150.BenchmarkCode,
			EventType:       "no_trade",
			Sequence:        1,
			EventAt:         decisionAt,
			Reason:          reason,
			SnapshotHash:    eventHash,
			PayloadJSON:     eventPayload,
			FrozenAt:        &frozenAt,
		})
	}
	if err := persistence.SealStrategySnapshotBundle(&bundle); err != nil {
		return fmt.Errorf("seal v1.5 immutable snapshot: %w", err)
	}
	return persistence.AppendStrategySnapshotBundle(ctx, database, bundle)
}

// PersistMarketSummaryV150Recommendations writes only backend-selected plans.
// It does not parse the model's four scores, price targets, or execution state.
func PersistMarketSummaryV150Recommendations(database *gorm.DB, run *MarketSummaryV150RunSnapshot, providerName, modelName string) (*models.MarketSummaryRecommendSaveResult, error) {
	persistMarketSummaryV150RecommendationsMu.Lock()
	defer persistMarketSummaryV150RecommendationsMu.Unlock()
	result, err := persistMarketSummaryV150RecommendationsLocked(database, run, providerName, modelName)
	if err == nil {
		notifyMarketSummaryV150Recommendations(result)
	}
	return result, err
}

// PersistMarketSummaryV150Decision is the production write boundary. The
// final shared quota read, immutable snapshot/rule/event append and legacy
// recommendation projection are serialized and committed in one database
// transaction. A stale overlapping run is reclassified before it can publish
// a rule_issued event, so quota losers remain candidate diagnostics/no_trade
// instead of becoming orphan executable rules.
func PersistMarketSummaryV150Decision(
	ctx context.Context,
	database *gorm.DB,
	run *MarketSummaryV150RunSnapshot,
	providerName, modelName string,
) (*models.MarketSummaryRecommendSaveResult, error) {
	if run == nil {
		return nil, errors.New("v1.5 recommendation run is nil")
	}
	if database == nil {
		return nil, errors.New("v1.5 recommendation database is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	persistMarketSummaryV150RecommendationsMu.Lock()
	defer persistMarketSummaryV150RecommendationsMu.Unlock()

	var result *models.MarketSummaryRecommendSaveResult
	err := database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		portfolio, err := loadMarketSummaryV150PublicationPortfolioState(tx, marketSummaryV150FinalQuotaAsOf(run.RunContext.DecisionAt), run.RunContext.RunID)
		if err != nil {
			return fmt.Errorf("reload v1.5 portfolio quota before release: %w", err)
		}
		reconcileMarketSummaryV150Production(run, portfolio)
		if err := refreshMarketSummaryV150DataHash(run); err != nil {
			return fmt.Errorf("refresh final v1.5 data hash: %w", err)
		}
		if err := PersistMarketSummaryV150Snapshot(ctx, tx, run); err != nil {
			return err
		}
		result, err = persistMarketSummaryV150RecommendationsLocked(tx, run, providerName, modelName)
		if err != nil {
			return err
		}
		if len(run.Production) > 0 && result != nil && result.BlockedCount > 0 {
			return fmt.Errorf("v1.5 atomic release rejected %d frozen production plan(s)", result.BlockedCount)
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	notifyMarketSummaryV150Recommendations(result)
	return result, nil
}

func persistMarketSummaryV150RecommendationsLocked(database *gorm.DB, run *MarketSummaryV150RunSnapshot, providerName, modelName string) (*models.MarketSummaryRecommendSaveResult, error) {
	result := &models.MarketSummaryRecommendSaveResult{
		BlockedReasons:              []models.MarketSummaryBlockedReasonItem{},
		ProductionDowngradeReasons:  []models.MarketSummaryBlockedReasonItem{},
		RepairableTradePlanFailures: []models.MarketSummaryTradePlanRepairCandidate{},
		UsedStockCodes:              []string{},
		SavedStockCodes:             []string{},
		RemainingCandidateStocks:    []string{},
	}
	if run == nil {
		return result, errors.New("v1.5 recommendation run is nil")
	}
	if database == nil {
		return result, errors.New("v1.5 recommendation database is nil")
	}

	portfolio, err := loadMarketSummaryV150PublicationPortfolioState(database, marketSummaryV150FinalQuotaAsOf(run.RunContext.DecisionAt), run.RunContext.RunID)
	if err != nil {
		return result, fmt.Errorf("reload v1.5 portfolio quota before insert: %w", err)
	}
	cfg := v150.FixedStrategyV150Config()
	result.AIOutputCount = len(run.Production)
	if len(run.Production) == 0 {
		reason := firstNonEmptyText(run.NoTradeReason, marketSummaryV150NoLegalCandidate)
		result.BlockedCount = 1
		result.BlockedReasons = []models.MarketSummaryBlockedReasonItem{{Reason: reason, Count: 1}}
		return result, nil
	}
	for _, production := range run.Production {
		if portfolio.TodayEntries >= run.Regime.DailyCap {
			result.BlockedCount++
			result.BlockedReasons = append(result.BlockedReasons, models.MarketSummaryBlockedReasonItem{Reason: marketSummaryV150DailyCapReached, Count: 1})
			continue
		}
		candidateSnapshot, exists := marketSummaryV150CandidateBySymbol(run, production.Symbol)
		if !exists {
			result.BlockedCount++
			result.BlockedReasons = append(result.BlockedReasons, models.MarketSummaryBlockedReasonItem{Reason: "candidate_missing_from_frozen_run", Count: 1})
			continue
		}
		portfolioEligibility := v150.EvaluatePortfolioEligibility(candidateSnapshot.Candidate, portfolio, cfg)
		if !portfolioEligibility.Eligible {
			result.BlockedCount++
			result.BlockedReasons = append(result.BlockedReasons, models.MarketSummaryBlockedReasonItem{Reason: strings.Join(portfolioEligibility.Reasons, ";"), Count: 1})
			continue
		}
		item, err := buildMarketSummaryV150RecommendStock(run, production, providerName, modelName)
		if err != nil {
			result.BlockedCount++
			result.BlockedReasons = append(result.BlockedReasons, models.MarketSummaryBlockedReasonItem{Reason: err.Error(), Count: 1})
			continue
		}
		if err := insertMarketSummaryV150Recommendation(database, run, production, item); err != nil {
			result.BlockedCount++
			result.BlockedReasons = append(result.BlockedReasons, models.MarketSummaryBlockedReasonItem{Reason: err.Error(), Count: 1})
			continue
		}
		code := normalizeRecommendStockCode(item.StockCode)
		result.SavedCount++
		result.ProductionCount++
		result.SavedStockCodes = append(result.SavedStockCodes, code)
		result.UsedStockCodes = append(result.UsedStockCodes, code)
		portfolio.PendingSymbols[code] = true
		portfolio.TodayEntries++
		if sector := strings.TrimSpace(candidateSnapshot.Candidate.Sector); sector != "" {
			portfolio.TodaySectorEntries[sector]++
		}
	}
	result.BlockedReasons = aggregateMarketSummaryBlockedReasonItems(result.BlockedReasons)
	if result.SavedCount == 0 && result.BlockedCount > 0 {
		return result, errors.New("all backend v1.5 recommendations failed validation")
	}
	return result, nil
}

func notifyMarketSummaryV150Recommendations(result *models.MarketSummaryRecommendSaveResult) {
	if result == nil || len(result.SavedStockCodes) == 0 {
		return
	}
	_ = markAiRecommendYieldDirtyCodesForMutationFn(
		result.SavedStockCodes,
		"V1.5 immutable recommendation created; awaiting event-ledger execution replay",
		aiRecommendYieldModeStrict,
	)
	requestAiRecommendYieldScopedRecalcForMutationFn(false, "v150_recommendations_created", result.SavedStockCodes)
	// A rule can be published between two regular 15-minute monitor slots (for
	// example the 09:40 strategy run). Wake the independent executor now so its
	// security observation is available before the rule's first valid bar.
	wakeMarketSummaryV150ExecutionMonitor()
}

// Final publication is an ingestion-time quota decision, not a historical
// point-in-time replay. Overlapping runs can finish and acquire the process
// lock in the opposite order from their DecisionAt timestamps, so the final
// check must include every already-persisted fact for that trade date. For a
// live run we stop at the current instant; historical fixtures/backfills use
// the conservative end of their decision date.
func marketSummaryV150FinalQuotaAsOf(decisionAt time.Time) time.Time {
	if decisionAt.IsZero() {
		return decisionAt
	}
	loc := cnLocation()
	decision := decisionAt.In(loc)
	now := time.Now().In(loc)
	if decision.Year() == now.Year() && decision.YearDay() == now.YearDay() && now.After(decision) {
		return now
	}
	return time.Date(decision.Year(), decision.Month(), decision.Day(), 23, 59, 59, int(time.Second-time.Nanosecond), loc)
}

func reconcileMarketSummaryV150Production(run *MarketSummaryV150RunSnapshot, portfolio v150.PortfolioState) {
	if run == nil {
		return
	}
	for index := range run.Candidates {
		run.Candidates[index].ProductionSelected = false
	}
	if run.Regime.NoTrade || run.Regime.DailyCap <= 0 || len(run.Production) == 0 {
		if run.Regime.NoTrade {
			run.NoTradeReason = v150.RejectRiskOff
		}
		return
	}

	cfg := v150.FixedStrategyV150Config()
	state := cloneV150PortfolioState(portfolio)
	ordered := append([]MarketSummaryV150ProductionSnapshot(nil), run.Production...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Rank != ordered[j].Rank {
			return ordered[i].Rank < ordered[j].Rank
		}
		return normalizeRecommendStockCode(ordered[i].Symbol) < normalizeRecommendStockCode(ordered[j].Symbol)
	})
	kept := make([]MarketSummaryV150ProductionSnapshot, 0, len(ordered))
	for _, production := range ordered {
		symbol := normalizeRecommendStockCode(production.Symbol)
		candidate, ok := marketSummaryV150CandidateBySymbol(run, symbol)
		if !ok {
			continue
		}
		reasons := make([]string, 0, 4)
		if state.TodayEntries >= run.Regime.DailyCap {
			reasons = append(reasons, marketSummaryV150DailyCapReached)
		} else {
			eligibility := v150.EvaluatePortfolioEligibility(candidate.Candidate, state, cfg)
			if !eligibility.Eligible {
				reasons = append(reasons, eligibility.Reasons...)
			}
		}
		if len(reasons) > 0 {
			for index := range run.Candidates {
				if normalizeRecommendStockCode(run.Candidates[index].Candidate.Symbol) != symbol {
					continue
				}
				run.Candidates[index].PortfolioEligibility = v150.EligibilityResult{Eligible: false, Reasons: append([]string(nil), reasons...)}
				run.Candidates[index].SelectionReasons = dedupeNonEmptyStrings(append(run.Candidates[index].SelectionReasons, reasons...), 32)
				break
			}
			continue
		}
		kept = append(kept, production)
		state.PendingSymbols[symbol] = true
		state.TodayEntries++
		if sector := strings.TrimSpace(candidate.Candidate.Sector); sector != "" {
			state.TodaySectorEntries[sector]++
		}
		for index := range run.Candidates {
			if normalizeRecommendStockCode(run.Candidates[index].Candidate.Symbol) == symbol {
				run.Candidates[index].ProductionSelected = true
				break
			}
		}
	}
	run.Production = kept
	if len(kept) == 0 {
		if portfolio.TodayEntries >= run.Regime.DailyCap {
			run.NoTradeReason = marketSummaryV150DailyCapReached
		} else {
			run.NoTradeReason = marketSummaryV150NoLegalCandidate
		}
	} else {
		run.NoTradeReason = ""
	}
}

func insertMarketSummaryV150Recommendation(database *gorm.DB, run *MarketSummaryV150RunSnapshot, production MarketSummaryV150ProductionSnapshot, item *models.AiRecommendStocks) error {
	if database == nil || run == nil || item == nil {
		return errors.New("v1.5 immutable recommendation insert requires database, run and item")
	}
	plan := production.Plan
	entryMin, entryMax := marketSummaryV150PlanEntryRange(plan)
	expectedRuleID := marketSummaryV150RuleID(run.RunContext.RunID, production.Symbol, plan.Path)
	if item.SummaryVersion != v150.StrategyVersion || item.StrategyRunID != run.RunContext.RunID || item.StrategyRuleID != expectedRuleID ||
		item.ExecutionState != recommendExecutionConditional || item.RecommendCategory != recommendExecutionConditional || item.ActivationStatus != "pending" ||
		item.DataTime == nil || !item.DataTime.Equal(run.RunContext.DecisionAt) {
		return errors.New("v1.5 recommendation identity/state differs from frozen backend decision")
	}
	stopLoss, stopLossOK := parseStopLossPrice(*item)
	if !stopLossOK || math.Abs(item.RecommendBuyPriceMin-entryMin) > 1e-9 || math.Abs(item.RecommendBuyPriceMax-entryMax) > 1e-9 ||
		math.Abs(stopLoss-plan.Stop) > 0.0050001 || math.Abs(item.RecommendStopProfitPriceMin-plan.Target) > 1e-9 || math.Abs(item.RecommendStopProfitPriceMax-plan.Target) > 1e-9 {
		return errors.New("v1.5 recommendation prices differ from frozen backend plan")
	}
	rule, err := parseActivationRuleJSON(item.ActivationRuleJSON)
	if err != nil {
		return fmt.Errorf("parse v1.5 backend rule: %w", err)
	}
	if err := validateActivationRuleTimelineForPaths(rule, *item); err != nil {
		return fmt.Errorf("validate v1.5 backend rule timeline: %w", err)
	}
	// Deliberately bypass normalizeMarketSummaryExecutionDataForSave: that
	// legacy routine fetches minute data and may rewrite state/targets. V1.5 has
	// already been fully normalized and causality-checked above.
	return database.Transaction(func(tx *gorm.DB) error {
		if err := validateRecommendDailyUniqueness(tx, []*models.AiRecommendStocks{item}); err != nil {
			return err
		}
		return tx.Create(item).Error
	})
}

func buildMarketSummaryV150RecommendStock(run *MarketSummaryV150RunSnapshot, production MarketSummaryV150ProductionSnapshot, providerName, modelName string) (*models.AiRecommendStocks, error) {
	plan := production.Plan
	entryMin, entryMax := marketSummaryV150PlanEntryRange(plan)
	if plan.Symbol == "" || entryMin <= 0 || entryMax <= 0 || plan.Stop <= 0 || plan.Target <= 0 {
		return nil, fmt.Errorf("%s backend v1.5 plan is incomplete", production.Symbol)
	}
	symbol := normalizeRecommendStockCode(production.Symbol)
	candidate, ok := marketSummaryV150CandidateBySymbol(run, symbol)
	if !ok {
		return nil, fmt.Errorf("%s is missing from frozen v1.5 candidates", symbol)
	}
	ruleID := marketSummaryV150RuleID(run.RunContext.RunID, symbol, plan.Path)
	ruleJSON, err := buildMarketSummaryV150ActivationRuleJSON(run, production, ruleID)
	if err != nil {
		return nil, err
	}
	decisionAt := run.RunContext.DecisionAt
	priceText := formatMarketSummaryPlanPrice(candidate.Candidate.Price)
	currentPriceTime := ""
	if quote := candidate.Source.QuoteEvidence; quote != nil &&
		marketSummaryV150EvidenceTimelineValid([]MarketSummaryV150EvidenceTiming{*quote}, run.RunContext.DataCutoffAt) {
		currentPriceTime = quote.SourceAt.In(cnLocation()).Format(time.DateTime)
	}
	buyText := fmt.Sprintf("%.2f-%.2f", entryMin, entryMax)
	stopText := formatMarketSummaryPlanPrice(plan.Stop)
	targetText := formatMarketSummaryPlanPrice(plan.Target)
	buySignal := fmt.Sprintf("未来%d个交易日内，完整15分钟K线触碰%.2f-%.2f后收盘恢复至支撑%.2f及以上；信号后下一根K线开盘成交", plan.ValidTradeDays, entryMin, entryMax, plan.Support)
	if plan.Path == v150.PathBreakout {
		buySignal = fmt.Sprintf("前一根完整15分钟K线不高于%.2f，当前完整K线真实上穿且同时间段量比不低于%.2f；14:00后不激活，下一根K线开盘成交", plan.Trigger, plan.MinimumVolumeRatio)
	}
	evidenceJSON := "[]"
	if len(production.Evidence.EvidenceSources) > 0 {
		if payload, marshalErr := json.Marshal(production.Evidence.EvidenceSources); marshalErr == nil {
			evidenceJSON = string(payload)
		}
	}
	return &models.AiRecommendStocks{
		DataTime:                    &decisionAt,
		ProviderName:                strings.TrimSpace(providerName),
		ModelName:                   strings.TrimSpace(modelName),
		StockCode:                   symbol,
		StockName:                   firstNonEmptyText(candidate.Candidate.Name, candidate.Source.StockName),
		BkName:                      firstNonEmptyText(candidate.Candidate.Sector, candidate.Source.BkName),
		StockPrice:                  priceText,
		StockCurrentPrice:           priceText,
		StockCurrentPriceTime:       currentPriceTime,
		StockClosePrice:             priceText,
		StockPrePrice:               formatMarketSummaryPlanPrice(candidate.Candidate.PreviousClose),
		RecommendReason:             fmt.Sprintf("v1.5后端确定性排名=%d，总分=%d；%s", production.Rank, production.Score.Total, firstNonEmptyText(candidate.Source.Reason, "通过硬门槛、验证与组合约束")),
		RecommendBuyPrice:           buyText,
		RecommendBuyPriceMin:        entryMin,
		RecommendBuyPriceMax:        entryMax,
		RecommendStopProfitPrice:    targetText,
		RecommendStopProfitPriceMin: plan.Target,
		RecommendStopProfitPriceMax: plan.Target,
		RecommendStopLossPrice:      stopText,
		RecommendCategory:           recommendExecutionConditional,
		ExecutionState:              recommendExecutionConditional,
		BuySignal:                   buySignal,
		BuySignalDetail:             fmt.Sprintf("strategyRunId=%s; strategyRuleId=%s; rank=%d; score=%d; path=%s", run.RunContext.RunID, ruleID, production.Rank, production.Score.Total, plan.Path),
		SellSignal:                  fmt.Sprintf("止盈%.2f；初始止损%.2f；达到1R后按1.5ATR跟踪；第%d个交易日14:45退出", plan.Target, plan.Stop, plan.MaxHoldTradeDays),
		SellSignalDetail:            fmt.Sprintf("riskPerShare=%.4f; rewardRisk=%.4f", plan.RiskPerShare, plan.RewardRisk),
		InvalidSignal:               fmt.Sprintf("未来%d个交易日未触发，或触发前价格结构失效", plan.ValidTradeDays),
		CoreCatalyst:                firstNonEmptyText(production.Evidence.Reason, candidate.Source.Reason, "后端量价筛选"),
		KeyEvidence:                 strings.Join(append(append([]string(nil), production.Evidence.PositiveSignals...), production.Evidence.VerdictHints...), "；"),
		EvidenceSources:             evidenceJSON,
		InvalidCondition:            fmt.Sprintf("完整15分钟恢复条件未成立；计划止损位%.2f", plan.Stop),
		ObservePrice:                formatMarketSummaryPlanPrice(plan.Support),
		FocusPrice:                  buyText,
		ExpectedCycle:               fmt.Sprintf("%d个交易日内触发，最多持有%d个交易日", plan.ValidTradeDays, plan.MaxHoldTradeDays),
		EventStrength:               marketSummaryV150ScorePercent(production.Score.Event, v150.FixedStrategyV150Config().EventWeight),
		CapitalConfirmation:         marketSummaryV150ScorePercent(production.Score.LiquidityRisk, v150.FixedStrategyV150Config().LiquidityRiskWeight),
		FundamentalFit:              marketSummaryV150ScorePercent(production.Score.Sector, v150.FixedStrategyV150Config().SectorWeight),
		TechnicalFit:                marketSummaryV150ScorePercent(production.Score.TrendRelative+production.Score.Setup, v150.FixedStrategyV150Config().TrendRelativeWeight+v150.FixedStrategyV150Config().SetupWeight),
		ActivationRuleJSON:          ruleJSON,
		ActivationRuleVersion:       activationRuleVersionV3,
		ActivationRuleSource:        "strategy_v150_backend",
		ActivationStatus:            "pending",
		RecommendStatus:             "valid",
		SummaryVersion:              v150.StrategyVersion,
		RiskRemarks:                 fmt.Sprintf("单笔目标资金10000元；止损%.2f；盈亏比%.2f；禁止追价", plan.Stop, plan.RewardRisk),
		Remarks:                     fmt.Sprintf("immutable strategy run=%s rule=%s config=%s", run.RunContext.RunID, ruleID, run.RunContext.ConfigHash),
		StrategyRunID:               run.RunContext.RunID,
		StrategyRuleID:              ruleID,
	}, nil
}

func marketSummaryV150PlanEntryRange(plan v150.TradePlan) (float64, float64) {
	if plan.Path == v150.PathBreakout {
		return plan.Trigger, plan.Trigger
	}
	return plan.EntryMin, plan.EntryMax
}

func buildMarketSummaryV150ActivationRuleJSON(run *MarketSummaryV150RunSnapshot, production MarketSummaryV150ProductionSnapshot, ruleID string) (string, error) {
	plan := production.Plan
	entryMin, entryMax := marketSummaryV150PlanEntryRange(plan)
	signalType := "price_range_with_volume"
	volumeRatio := 1.0
	if plan.Path == v150.PathBreakout {
		signalType = "price_breakout_with_volume"
		volumeRatio = plan.MinimumVolumeRatio
	}
	path := activationRule{
		Name:                       string(plan.Path),
		SignalType:                 signalType,
		EvaluationWindow:           "15m",
		Baseline:                   "manual_amount",
		Operator:                   ">=",
		ThresholdValue:             entryMin,
		ThresholdMax:               entryMax,
		Support:                    plan.Support,
		VolumeRatio:                volumeRatio,
		ConfirmBars:                1,
		VolumeWindow:               1,
		VolumeMetric:               "amount",
		ExpireTradeDays:            plan.ValidTradeDays,
		GeneratedAt:                run.RunContext.DecisionAt,
		ValidFrom:                  run.RunContext.ValidFromAt,
		DataCutoffTime:             run.RunContext.DataCutoffAt,
		StrategyRunID:              run.RunContext.RunID,
		StrategyRuleID:             ruleID,
		DecisionTradeDayIndex:      plan.DecisionTradeDayIndex,
		ValidFromTradeDayIndex:     plan.ValidFromTradeDayIndex,
		ReferenceEntry:             plan.ReferenceEntry,
		Stop:                       plan.Stop,
		Target:                     plan.Target,
		ATR14:                      plan.ATR14,
		RiskPerShare:               plan.RiskPerShare,
		RewardRisk:                 plan.RewardRisk,
		NegativeOvernightGapRisk60: plan.NegativeOvernightGapRisk60,
		ValidTradeDays:             plan.ValidTradeDays,
		MaxHoldTradeDays:           plan.MaxHoldTradeDays,
		TrailingActivationR:        plan.TrailingActivationR,
		TrailingATRMultiple:        plan.TrailingATRMultiple,
	}
	root := activationRule{
		Version:        activationRuleVersionV3,
		Mode:           activationRuleModeAnyOf,
		Paths:          []activationRule{path},
		GeneratedAt:    run.RunContext.DecisionAt,
		ValidFrom:      run.RunContext.ValidFromAt,
		DataCutoffTime: run.RunContext.DataCutoffAt,
		StrategyRunID:  run.RunContext.RunID,
		StrategyRuleID: ruleID,
	}
	payload, err := json.Marshal(root)
	if err != nil {
		return "", err
	}
	parsed, err := parseActivationRuleJSON(string(payload))
	if err != nil {
		return "", err
	}
	if err := validateActivationRuleTimelineForPaths(parsed, models.AiRecommendStocks{DataTime: &run.RunContext.DecisionAt}); err != nil {
		return "", err
	}
	return string(payload), nil
}

func marketSummaryV150CandidateDecision(row MarketSummaryV150CandidateSnapshot) (string, string) {
	decision := "rejected"
	switch {
	case row.ProductionSelected:
		decision = "production"
	case row.VerificationSelected && row.Verified:
		decision = "verified_not_selected"
	case row.VerificationSelected:
		decision = "verification_failed"
	case row.Eligibility.Eligible:
		decision = "ranked_below_top18"
	}
	reasons := append([]string(nil), row.Eligibility.Reasons...)
	reasons = append(reasons, row.SelectionReasons...)
	if decision == "verification_failed" {
		reasons = append(reasons, "verification_failed")
	}
	return decision, strings.Join(dedupeNonEmptyStrings(reasons, 32), ";")
}

func marketSummaryV150CandidateBySymbol(run *MarketSummaryV150RunSnapshot, symbol string) (MarketSummaryV150CandidateSnapshot, bool) {
	if run == nil {
		return MarketSummaryV150CandidateSnapshot{}, false
	}
	symbol = normalizeRecommendStockCode(symbol)
	for _, row := range run.Candidates {
		if normalizeRecommendStockCode(row.Candidate.Symbol) == symbol {
			return row, true
		}
	}
	return MarketSummaryV150CandidateSnapshot{}, false
}

func marketSummaryV150PlanExpiresAt(validFrom time.Time, validTradeDays int) time.Time {
	if validTradeDays <= 0 {
		validTradeDays = v150.FixedStrategyV150Config().ActivationValidTradeDays
	}
	day := normalizeDailyTradeDate(validFrom)
	remaining := validTradeDays - 1
	for remaining > 0 {
		day = shiftToNextCNOpenTradeDaySafe(day.AddDate(0, 0, 1))
		remaining--
	}
	return time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, cnLocation())
}

func marketSummaryV150CandidateID(runID, symbol string) string {
	return runID + "|candidate|" + normalizeRecommendStockCode(symbol)
}

func marketSummaryV150RuleID(runID, symbol string, path v150.TradePath) string {
	return runID + "|rule|" + normalizeRecommendStockCode(symbol) + "|" + string(path)
}

func marketSummaryV150EventID(runID string, sequence int, eventType, symbol string) string {
	return fmt.Sprintf("%s|event|%04d|%s|%s", runID, sequence, strings.TrimSpace(eventType), normalizeRecommendStockCode(symbol))
}

func marshalMarketSummaryV150FrozenPayload(value any) (string, string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", "", err
	}
	digest := sha256.Sum256(payload)
	return string(payload), hex.EncodeToString(digest[:]), nil
}

func marketSummaryV150ScorePercent(points, maximum int) int {
	if maximum <= 0 || points <= 0 {
		return 0
	}
	value := int(mathRound(float64(points) * 100 / float64(maximum)))
	if value > 100 {
		return 100
	}
	return value
}

func mathRound(value float64) float64 {
	if value < 0 {
		return float64(int64(value - 0.5))
	}
	return float64(int64(value + 0.5))
}

func aggregateMarketSummaryBlockedReasonItems(items []models.MarketSummaryBlockedReasonItem) []models.MarketSummaryBlockedReasonItem {
	counts := map[string]int{}
	order := make([]string, 0, len(items))
	for _, item := range items {
		reason := strings.TrimSpace(item.Reason)
		if reason == "" {
			continue
		}
		if _, ok := counts[reason]; !ok {
			order = append(order, reason)
		}
		count := item.Count
		if count <= 0 {
			count = 1
		}
		counts[reason] += count
	}
	result := make([]models.MarketSummaryBlockedReasonItem, 0, len(order))
	for _, reason := range order {
		result = append(result, models.MarketSummaryBlockedReasonItem{Reason: reason, Count: counts[reason]})
	}
	return result
}
