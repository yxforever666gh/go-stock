package data

import "testing"

func TestExtractSearchStockRowsAcceptsNumericSuccessCode(t *testing.T) {
	rows := extractSearchStockRows(map[string]any{
		"code": float64(100),
		"data": map[string]any{
			"result": map[string]any{
				"dataList": []any{
					map[string]any{"SECURITY_CODE": "300308"},
				},
			},
		},
	})
	if len(rows) != 1 {
		t.Fatalf("rows len = %d, want 1", len(rows))
	}
}

func TestBuildMarketSummaryIndicatorCandidateParsesAndScores(t *testing.T) {
	candidate := buildMarketSummaryIndicatorCandidate(map[string]any{
		"SECURITY_CODE":      "300308",
		"SECURITY_NAME_ABBR": "中际旭创",
		"INDUSTRY_NAME":      "光通信",
		"CHANGE_RATE":        "3.2%",
		"VOLUME_RATIO":       "2.1",
		"TURNOVERRATE":       "7.5%",
		"AMOUNT":             "12.5亿",
		"NEW_PRICE":          "68.30",
	}, marketSummaryIndicatorTemplate{Name: "unit-template", Weight: 20})

	if candidate.StockCode != "300308.SZ" {
		t.Fatalf("StockCode = %q, want 300308.SZ", candidate.StockCode)
	}
	if candidate.StockName != "中际旭创" {
		t.Fatalf("StockName = %q, want 中际旭创", candidate.StockName)
	}
	if candidate.Score <= 20 {
		t.Fatalf("Score = %d, want boosted score above template weight", candidate.Score)
	}
	if candidate.Metrics["volumeRatio"] != "2.1" {
		t.Fatalf("volumeRatio metric = %q", candidate.Metrics["volumeRatio"])
	}
	if candidate.Reason == "" {
		t.Fatalf("expected non-empty reason")
	}
}

func TestScoreIndicatorCandidateMetricsRanksTradableSetupHigher(t *testing.T) {
	strong := scoreIndicatorCandidateMetrics(map[string]string{
		"changePct":   "3.5%",
		"volumeRatio": "2.2",
		"turnover":    "8.0%",
		"amount":      "6.8亿",
		"price":       "26.5",
	})
	weak := scoreIndicatorCandidateMetrics(map[string]string{
		"changePct":   "-1.2%",
		"volumeRatio": "0.7",
		"turnover":    "33%",
		"price":       "168",
	})
	if strong <= weak {
		t.Fatalf("strong score = %d, weak score = %d; want strong > weak", strong, weak)
	}
}

func TestMergeMarketSummaryDiscoveryCandidatesAppendsIndicatorBackfill(t *testing.T) {
	discovery := &marketSummaryDiscoveryResult{CandidateStocks: []marketSummaryRouteCandidate{
		{StockName: "中际旭创", StockCode: "300308.SZ", Direction: "AI算力"},
	}}
	indicators := []marketSummaryIndicatorCandidate{
		{StockName: "中际旭创", StockCode: "300308.SZ", SourceNames: []string{"duplicate"}},
		{StockName: "北方华创", StockCode: "002371.SZ", SourceNames: []string{"strong-breakout"}, Score: 80},
		{StockName: "贵州茅台", StockCode: "600519.SH", SourceNames: []string{"volume-confirm"}, Score: 70},
	}

	mergeMarketSummaryDiscoveryCandidates(discovery, indicators, 3)

	if len(discovery.CandidateStocks) != 3 {
		t.Fatalf("merged len = %d, want 3", len(discovery.CandidateStocks))
	}
	if discovery.CandidateStocks[0].StockCode != "300308.SZ" {
		t.Fatalf("first candidate changed: %+v", discovery.CandidateStocks[0])
	}
	if discovery.CandidateStocks[1].StockCode != "002371.SZ" {
		t.Fatalf("second candidate = %q, want indicator backfill 002371.SZ", discovery.CandidateStocks[1].StockCode)
	}
	if discovery.CandidateStocks[2].StockCode != "600519.SH" {
		t.Fatalf("third candidate = %q, want indicator backfill 600519.SH", discovery.CandidateStocks[2].StockCode)
	}
}
