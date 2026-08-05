package data

import (
	"strings"
	"testing"

	"go-stock/backend/models"
)

func TestMarketSummaryV142TradePlanAllowsLargeDownside(t *testing.T) {
	draft := newMarketSummaryTradePlanGateTestDraft(marketSummaryVersion142, 100, 90, 110)

	if reason := marketSummaryDraftV136TradePlanRejectionReason(draft); reason != "" {
		t.Fatalf("v1.4.2 large downside should pass when reward/risk is valid, got %q", reason)
	}
}

func TestMarketSummaryV141TradePlanStillRejectsLargeDownside(t *testing.T) {
	draft := newMarketSummaryTradePlanGateTestDraft(marketSummaryVersion141, 100, 90, 110)

	if reason := marketSummaryDraftV136TradePlanRejectionReason(draft); reason == "" {
		t.Fatalf("v1.4.1 large downside should still be rejected")
	}
}

func TestMarketSummaryV142TradePlanStillRejectsWeakRewardRisk(t *testing.T) {
	draft := newMarketSummaryTradePlanGateTestDraft(marketSummaryVersion142, 100, 90, 103)

	reason := marketSummaryDraftV136TradePlanRejectionReason(draft)
	if reason == "" {
		t.Fatalf("v1.4.2 weak reward/risk should still be rejected")
	}
	if !strings.Contains(reason, "0.80") {
		t.Fatalf("rejection reason = %q, want reward/risk threshold", reason)
	}
}

func TestV136ActivationGateAllowsV142LargeDownside(t *testing.T) {
	rec := models.AiRecommendStocks{
		SummaryVersion:              marketSummaryVersion142,
		RecommendStopProfitPrice:    "110.00",
		RecommendStopProfitPriceMin: 110,
		RecommendStopLossPrice:      "90.00",
	}

	ok, reason := passesV136RewardRiskGate(rec, 100)
	if !ok {
		t.Fatalf("v1.4.2 activation gate should allow large downside when reward/risk is valid, got %q", reason)
	}
}

func TestFinalizeMarketSummaryFeasiblePlanAllowsLargeDownside(t *testing.T) {
	plan := finalizeMarketSummaryFeasiblePlan("pullback", 99, 100, 100, 90, 110)

	if !plan.PassHardGate {
		t.Fatalf("large downside feasible plan should pass v1.4.2 hard gate, reason=%q", plan.FailureReason)
	}
	if plan.RewardRisk < marketSummaryFeasibleRewardRisk {
		t.Fatalf("rewardRisk = %.2f, want >= %.2f", plan.RewardRisk, marketSummaryFeasibleRewardRisk)
	}
	if plan.DownsidePct <= marketSummaryFeasibleDownsidePct {
		t.Fatalf("downsidePct = %.2f, test setup should exceed old cap %.2f", plan.DownsidePct, marketSummaryFeasibleDownsidePct)
	}
}

func TestApplyMarketSummaryFeasiblePlanFallbackDoesNotPromoteAnalysisOnly(t *testing.T) {
	draft := &marketSummaryRecommendDraft{
		StockCode:               "000001.SZ",
		StockName:               "Ping An Bank",
		SummaryVersion:          marketSummaryVersion150,
		ExecutionState:          recommendExecutionAnalysisOnly,
		RecommendStatus:         "invalid",
		EventStrength:           80,
		CapitalConfirmation:     80,
		FundamentalFit:          80,
		TechnicalFit:            80,
		InvalidCondition:        "missing structured trade plan",
		ActivationInvalidReason: "missing structured trade plan",
	}
	candidate := MarketSummaryVerifiedCandidateSnapshot{
		StockCode:     "000001.SZ",
		StockName:     "Ping An Bank",
		AuctionPrice:  "101.00",
		MinutePrice:   "100.80",
		CurrentPrice:  "100.50",
		AuctionAmount: "10000000",
		FeasiblePlans: []marketSummaryFeasiblePlan{{
			Path:         "pullback",
			EntryRange:   "100.00-102.00",
			WorstEntry:   102,
			StopLoss:     90,
			TakeProfit:   112,
			RewardRisk:   0.83,
			DownsidePct:  11.76,
			PassHardGate: true,
		}},
	}

	applyMarketSummaryFeasiblePlanFallback([]*marketSummaryRecommendDraft{draft}, []MarketSummaryVerifiedCandidateSnapshot{candidate})

	if got := normalizeRecommendExecutionState(draft.ExecutionState); got != recommendExecutionAnalysisOnly {
		t.Fatalf("execution state = %s, want analysis_only", got)
	}
	if draft.RecommendBuyPriceMin != 0 || draft.RecommendBuyPriceMax != 0 {
		t.Fatalf("analysis-only buy range was manufactured: min=%.2f max=%.2f", draft.RecommendBuyPriceMin, draft.RecommendBuyPriceMax)
	}
	if draft.RecommendStopProfitPrice != "" || draft.RecommendStopLossPrice != "" {
		t.Fatalf("analysis-only stop prices were manufactured: profit=%q loss=%q", draft.RecommendStopProfitPrice, draft.RecommendStopLossPrice)
	}
	if strings.TrimSpace(draft.ActivationRuleJSON) != "" {
		t.Fatalf("analysis-only activation rule should remain empty")
	}
}

func TestApplyMarketSummaryFeasiblePlanFallbackIgnoresNoPassPlan(t *testing.T) {
	draft := &marketSummaryRecommendDraft{
		StockCode:      "000001.SZ",
		SummaryVersion: marketSummaryVersion142,
		ExecutionState: recommendExecutionAnalysisOnly,
	}
	candidate := MarketSummaryVerifiedCandidateSnapshot{
		StockCode: "000001.SZ",
		FeasiblePlans: []marketSummaryFeasiblePlan{{
			Path:         "pullback",
			EntryRange:   "100.00-102.00",
			WorstEntry:   102,
			StopLoss:     96,
			TakeProfit:   103,
			RewardRisk:   0.25,
			DownsidePct:  5.88,
			PassHardGate: false,
		}},
	}

	applyMarketSummaryFeasiblePlanFallback([]*marketSummaryRecommendDraft{draft}, []MarketSummaryVerifiedCandidateSnapshot{candidate})

	if got := normalizeRecommendExecutionState(draft.ExecutionState); got != recommendExecutionAnalysisOnly {
		t.Fatalf("execution state = %s, want analysis_only", got)
	}
	if strings.TrimSpace(draft.ActivationRuleJSON) != "" {
		t.Fatalf("activation rule should remain empty when no passHardGate plan exists")
	}
}

func newMarketSummaryTradePlanGateTestDraft(version string, worstEntry, stopLoss, takeProfit float64) *marketSummaryRecommendDraft {
	return &marketSummaryRecommendDraft{
		StockCode:                   "000001.SZ",
		StockName:                   "Ping An Bank",
		SummaryVersion:              version,
		StockCurrentPrice:           formatMarketSummaryPlanPrice(worstEntry),
		ObservePrice:                formatMarketSummaryPlanPrice(worstEntry),
		StockPrice:                  formatMarketSummaryPlanPrice(worstEntry),
		RecommendBuyPrice:           formatMarketSummaryPlanPrice(worstEntry),
		RecommendBuyPriceMin:        worstEntry,
		RecommendBuyPriceMax:        worstEntry,
		RecommendStopProfitPrice:    formatMarketSummaryPlanPrice(takeProfit),
		RecommendStopProfitPriceMin: takeProfit,
		RecommendStopProfitPriceMax: takeProfit,
		RecommendStopLossPrice:      formatMarketSummaryPlanPrice(stopLoss),
		ExecutionState:              recommendExecutionConditional,
		RecommendStatus:             "valid",
		ActivationRuleSource:        "market_summary",
		EventStrength:               80,
		CapitalConfirmation:         80,
		FundamentalFit:              80,
		TechnicalFit:                80,
	}
}
