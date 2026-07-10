package data

import "testing"

func TestCollectMarketSummaryRecommendStocksForSaveWithLimitsCapsTwentyDraftsAndPreservesOrder(t *testing.T) {
	drafts := make([]*marketSummaryRecommendDraft, 0, 20)
	for idx := 0; idx < 20; idx++ {
		drafts = append(drafts, buildMarketSummaryProductionDraftForTest(idx))
	}

	got := collectMarketSummaryRecommendStocksForSaveWithLimits(drafts, nil, 12, 4)
	if len(got) != 12 {
		t.Fatalf("collected draft count = %d, want 12", len(got))
	}

	productionCount := 0
	analysisOnlyCount := 0
	for idx, draft := range got {
		if draft != drafts[idx] {
			t.Fatalf("draft at index %d = %q, want original ordered draft %q", idx, draft.StockCode, drafts[idx].StockCode)
		}
		if normalizeRecommendExecutionState(draft.ExecutionState) == recommendExecutionAnalysisOnly {
			analysisOnlyCount++
			continue
		}
		productionCount++
	}

	if productionCount != 4 {
		t.Fatalf("production count = %d, want 4", productionCount)
	}
	if analysisOnlyCount != 8 {
		t.Fatalf("analysis-only count = %d, want 8", analysisOnlyCount)
	}
}

func TestCollectMarketSummaryRecommendStocksForSaveWithLimitsHonorsCustomThreeStockPolicy(t *testing.T) {
	policy := ResolveMarketSummaryRecommendationCountPolicy("推荐3只股票")
	if policy.MaximumOutput != 3 || policy.ProductionTarget != 3 {
		t.Fatalf("custom three-stock policy = output %d, production %d; want 3 and 3", policy.MaximumOutput, policy.ProductionTarget)
	}

	drafts := make([]*marketSummaryRecommendDraft, 0, 6)
	for idx := 0; idx < 6; idx++ {
		drafts = append(drafts, buildMarketSummaryProductionDraftForTest(idx))
	}

	got := collectMarketSummaryRecommendStocksForSaveWithLimits(
		drafts,
		nil,
		policy.MaximumOutput,
		policy.ProductionTarget,
	)
	if len(got) != 3 {
		t.Fatalf("collected draft count = %d, want 3", len(got))
	}
	for idx, draft := range got {
		if draft != drafts[idx] {
			t.Fatalf("draft at index %d = %q, want original ordered draft %q", idx, draft.StockCode, drafts[idx].StockCode)
		}
		if normalizeRecommendExecutionState(draft.ExecutionState) == recommendExecutionAnalysisOnly {
			t.Fatalf("draft at index %d was downgraded, want all three custom-policy rows production eligible", idx)
		}
	}
}

func TestResolveMarketSummaryRecommendationCountPolicyDistinguishesDirectionsFromCandidateStocks(t *testing.T) {
	directions := ResolveMarketSummaryRecommendationCountPolicy("仅2个候选方向")
	if directions.Custom {
		t.Fatalf("candidate directions unexpectedly produced a custom stock-count policy: %+v", directions)
	}
	if directions.MinimumOutput != defaultMarketSummaryRecommendationMinimum || directions.MaximumOutput != defaultMarketSummaryRecommendationMaximum {
		t.Fatalf(
			"candidate-direction policy = %d-%d, want default %d-%d",
			directions.MinimumOutput,
			directions.MaximumOutput,
			defaultMarketSummaryRecommendationMinimum,
			defaultMarketSummaryRecommendationMaximum,
		)
	}

	stocks := ResolveMarketSummaryRecommendationCountPolicy("推荐8个候选股票")
	if !stocks.Custom {
		t.Fatalf("candidate-stock request did not produce a custom policy: %+v", stocks)
	}
	if stocks.MinimumOutput != 8 || stocks.MaximumOutput != 8 || stocks.ProductionTarget != 4 {
		t.Fatalf(
			"candidate-stock policy = output %d-%d, production %d; want 8-8 and 4",
			stocks.MinimumOutput,
			stocks.MaximumOutput,
			stocks.ProductionTarget,
		)
	}
}
