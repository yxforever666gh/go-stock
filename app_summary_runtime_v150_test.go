package main

import (
	"context"
	"errors"
	"testing"

	"go-stock/backend/data"
	"go-stock/backend/strategy/v150"
	"go-stock/internal/service"
)

type blockingSummaryRecommendOperations struct {
	service.RecommendOperations
	version string
	err     error
}

func (o *blockingSummaryRecommendOperations) RequireStrategyLive(_ context.Context, version string) error {
	o.version = version
	return o.err
}

func TestV150SummaryResultRequiresFrozenBackendDecision(t *testing.T) {
	if !marketSummaryRequiresV150Backend() {
		t.Fatalf("current summary version = %s, want 1.5.0 backend enforcement", data.MarketSummaryCurrentVersion())
	}
	plain := summaryRunResult{text: "legacy markdown"}
	if usableMarketSummaryRunResult(plain) {
		t.Fatal("plain model text bypassed the V1.5 backend decision")
	}
	rejected := rejectMissingV150BackendResult(plain)
	if len(rejected.errs) != 1 || rejected.errs[0] != marketSummaryV150BackendMissingReason {
		t.Fatalf("missing-backend errors = %v", rejected.errs)
	}

	valid := summaryRunResult{
		text: "presentation",
		v150Run: &data.MarketSummaryV150RunSnapshot{RunContext: v150.RunContext{
			RunID:           "v150-run",
			StrategyVersion: v150.StrategyVersion,
		}},
	}
	if !usableMarketSummaryRunResult(valid) {
		t.Fatal("frozen V1.5 backend decision was rejected")
	}
	valid.text = ""
	if !usableMarketSummaryRunResult(valid) {
		t.Fatal("structured V1.5 no_trade run was rejected because presentation text was empty")
	}

	wrongVersion := valid
	wrongVersion.v150Run = &data.MarketSummaryV150RunSnapshot{RunContext: v150.RunContext{
		RunID:           "wrong-version",
		StrategyVersion: "1.4.2",
	}}
	if usableMarketSummaryRunResult(wrongVersion) {
		t.Fatal("wrong-version backend decision was accepted")
	}
}

func TestV150SummaryFailoverDoesNotStopAtPlainText(t *testing.T) {
	if !shouldSummaryFailover(summaryRunResult{text: "plain markdown"}) {
		t.Fatal("V1.5 failover stopped at a plain-text result without a frozen run")
	}
	complete := summaryRunResult{
		text: "presentation",
		v150Run: &data.MarketSummaryV150RunSnapshot{RunContext: v150.RunContext{
			RunID:           "v150-run",
			StrategyVersion: v150.StrategyVersion,
		}},
	}
	if shouldSummaryFailover(complete) {
		t.Fatal("complete V1.5 result unexpectedly requested failover")
	}
}

func TestSummaryProductionUsesInjectedStrategyRuntimeGate(t *testing.T) {
	gateErr := errors.New("strategy paused by test")
	operations := &blockingSummaryRecommendOperations{err: gateErr}
	app := &App{
		ctx: context.Background(),
		services: service.AppServices{
			Recommend: service.NewRecommendService(operations),
		},
	}

	result := app.runSummaryStockNewsTask("summary", 1, nil, true, false)
	if operations.version != v150.StrategyVersion {
		t.Fatalf("gate version = %q, want %q", operations.version, v150.StrategyVersion)
	}
	if len(result.errs) != 1 || result.errs[0] != gateErr.Error() {
		t.Fatalf("blocked result errors = %v", result.errs)
	}
}
