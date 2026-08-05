package data

import (
	"fmt"
	"testing"
)

func TestFinalizeMarketSummaryV150CandidatePoolKeepsEntireRawUniverse(t *testing.T) {
	index := make(map[string]*marketSummaryIndicatorCandidate, 135)
	for value := 134; value >= 0; value-- {
		code := fmt.Sprintf("%06d.SZ", value)
		index[code] = &marketSummaryIndicatorCandidate{
			StockCode: code,
			BkName:    "bank",
			Score:     10_000 - value,
			ScoreBreakdown: map[string]int{
				"recentFailurePenalty": -24,
				"tradePlanFeasibility": 99,
				"dataCompleteness":     -12,
			},
		}
	}
	items := finalizeMarketSummaryIndicatorCandidatePool(index, 0, true, map[string]int{"bank": 14})
	if len(items) != len(index) {
		t.Fatalf("V1.5 raw universe was truncated: got=%d want=%d", len(items), len(index))
	}
	for position, item := range items {
		want := fmt.Sprintf("%06d.SZ", position)
		if item.StockCode != want {
			t.Fatalf("V1.5 pool order[%d]=%s, want deterministic symbol %s", position, item.StockCode, want)
		}
		if item.ScoreBreakdown["sectorStrength"] != 14 {
			t.Fatalf("sector feature not retained: %+v", item.ScoreBreakdown)
		}
		for _, forbidden := range []string{"recentFailurePenalty", "tradePlanFeasibility", "dataCompleteness"} {
			if _, exists := item.ScoreBreakdown[forbidden]; exists {
				t.Fatalf("legacy feature %s leaked into V1.5 pool: %+v", forbidden, item.ScoreBreakdown)
			}
		}
	}
}
