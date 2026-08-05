package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
	"go-stock/backend/persistence"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupStrategyBacktestTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:strategy-cli-%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "-"))
	database, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := persistence.MigrateStrategyPersistence(database); err != nil {
		t.Fatal(err)
	}
	db.Dao = database
	return database
}

func appendStrategyBacktestFixture(t *testing.T, database *gorm.DB) {
	t.Helper()
	cn := time.FixedZone("Asia/Shanghai", 8*60*60)
	frozenAt := time.Date(2026, 8, 6, 16, 0, 0, 0, cn)
	startedAt := time.Date(2026, 8, 4, 9, 0, 0, 0, cn)
	asOf := time.Date(2026, 8, 4, 9, 10, 0, 0, cn)
	decisionAt := time.Date(2026, 8, 4, 9, 15, 0, 0, cn)
	generatedAt := time.Date(2026, 8, 4, 9, 16, 0, 0, cn)
	validFromAt := time.Date(2026, 8, 4, 9, 30, 0, 0, cn)
	bundle := persistence.StrategySnapshotBundle{
		Run: models.StrategyRunSnapshot{
			RunID:           "run-cli-1",
			StrategyVersion: "1.5.0",
			TradeDate:       "2026-08-04",
			RunSlot:         "close",
			StartedAt:       startedAt,
			AsOf:            asOf,
			DataCutoffAt:    asOf,
			DecisionAt:      decisionAt,
			GeneratedAt:     generatedAt,
			ValidFromAt:     &validFromAt,
			PayloadJSON:     `{}`,
			FrozenAt:        &frozenAt,
		},
		Candidates: []models.CandidateSnapshot{{
			CandidateID:     "candidate-cli-1",
			RunID:           "run-cli-1",
			StrategyVersion: "1.5.0",
			TradeDate:       "2026-08-04",
			Symbol:          "000001.SZ",
			Decision:        "selected",
			Eligible:        true,
			Rank:            1,
			PayloadJSON:     `{}`,
			FrozenAt:        &frozenAt,
		}},
		Rules: []models.RuleSnapshot{{
			RuleID:          "rule-cli-1",
			RunID:           "run-cli-1",
			CandidateID:     "candidate-cli-1",
			StrategyVersion: "1.5.0",
			TradeDate:       "2026-08-04",
			Symbol:          "000001.SZ",
			RuleType:        "entry",
			Path:            "pullback",
			ValidFromAt:     validFromAt,
			PayloadJSON:     `{}`,
			FrozenAt:        &frozenAt,
		}},
		OrderEvents: []models.OrderEvent{{
			EventID:         "event-cli-rule-1",
			RunID:           "run-cli-1",
			RuleID:          "rule-cli-1",
			StrategyVersion: "1.5.0",
			TradeDate:       "2026-08-04",
			Symbol:          "000001.SZ",
			EventType:       "rule_issued",
			Sequence:        1,
			EventAt:         validFromAt,
			PayloadJSON:     `{}`,
			FrozenAt:        &frozenAt,
		}, {
			EventID:         "event-cli-signal-1",
			RunID:           "run-cli-1",
			RuleID:          "rule-cli-1",
			StrategyVersion: "1.5.0",
			TradeDate:       "2026-08-04",
			Symbol:          "000001.SZ",
			EventType:       "signal",
			Sequence:        2,
			EventAt:         time.Date(2026, 8, 4, 9, 45, 0, 0, cn),
			PayloadJSON:     `{}`,
			FrozenAt:        &frozenAt,
		}, {
			EventID:         "event-cli-order-1",
			RunID:           "run-cli-1",
			RuleID:          "rule-cli-1",
			StrategyVersion: "1.5.0",
			TradeDate:       "2026-08-04",
			Symbol:          "000001.SZ",
			EventType:       "order",
			Sequence:        3,
			EventAt:         time.Date(2026, 8, 4, 10, 0, 0, 0, cn),
			Price:           10,
			Quantity:        1000,
			PayloadJSON:     `{}`,
			FrozenAt:        &frozenAt,
		}, {
			EventID:         "event-cli-fill-1",
			RunID:           "run-cli-1",
			RuleID:          "rule-cli-1",
			StrategyVersion: "1.5.0",
			TradeDate:       "2026-08-04",
			Symbol:          "000001.SZ",
			EventType:       "fill",
			Sequence:        4,
			EventAt:         time.Date(2026, 8, 4, 10, 0, 0, 0, cn),
			Price:           10,
			Quantity:        1000,
			Fees:            5.1,
			PayloadJSON:     `{}`,
			FrozenAt:        &frozenAt,
		}, {
			EventID:         "event-cli-exit-signal-1",
			RunID:           "run-cli-1",
			RuleID:          "rule-cli-1",
			StrategyVersion: "1.5.0",
			TradeDate:       "2026-08-04",
			Symbol:          "000001.SZ",
			EventType:       "exit_signal",
			Sequence:        5,
			EventAt:         time.Date(2026, 8, 5, 14, 30, 0, 0, cn),
			Price:           10.5,
			Quantity:        1000,
			PayloadJSON:     `{}`,
			FrozenAt:        &frozenAt,
		}, {
			EventID:         "event-cli-exit-1",
			RunID:           "run-cli-1",
			RuleID:          "rule-cli-1",
			StrategyVersion: "1.5.0",
			TradeDate:       "2026-08-04",
			Symbol:          "000001.SZ",
			EventType:       "exit_fill",
			Sequence:        6,
			EventAt:         time.Date(2026, 8, 5, 14, 45, 0, 0, cn),
			Price:           10.5,
			Quantity:        1000,
			Fees:            10.355,
			Reason:          "target",
			PayloadJSON:     `{}`,
			FrozenAt:        &frozenAt,
		}},
	}
	if err := persistence.SealStrategySnapshotBundle(&bundle); err != nil {
		t.Fatal(err)
	}
	if err := persistence.AppendStrategySnapshotBundle(context.Background(), database, bundle); err != nil {
		t.Fatal(err)
	}
}

func TestValidateStrategyBacktestArgs(t *testing.T) {
	loc := time.UTC
	tests := []struct {
		name          string
		version       string
		start         string
		end           string
		wantErrSubstr string
	}{
		{name: "valid", version: "1.5.0", start: "2026-08-01", end: "2026-08-04"},
		{name: "missing version", start: "2026-08-01", end: "2026-08-04", wantErrSubstr: "--version"},
		{name: "bad version", version: "latest", start: "2026-08-01", end: "2026-08-04", wantErrSubstr: "--version"},
		{name: "bad start", version: "1.5.0", start: "08/01/2026", end: "2026-08-04", wantErrSubstr: "--from"},
		{name: "reversed", version: "1.5.0", start: "2026-08-05", end: "2026-08-04", wantErrSubstr: "不能早于"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, err := validateStrategyBacktestArgs(tt.version, tt.start, tt.end, loc)
			if tt.wantErrSubstr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSubstr) {
				t.Fatalf("error = %v, want substring %q", err, tt.wantErrSubstr)
			}
		})
	}
}

func TestResolveStrategyBacktestDateAlias(t *testing.T) {
	if got, err := resolveStrategyBacktestDateAlias("--from", "", "--start", "2026-08-04"); err != nil || got != "2026-08-04" {
		t.Fatalf("alias resolution = %q, %v", got, err)
	}
	if _, err := resolveStrategyBacktestDateAlias("--from", "2026-08-04", "--start", "2026-08-05"); err == nil {
		t.Fatal("expected conflicting primary/alias dates to fail")
	}
}

func TestStrategyBacktestAcceptsGlobalDatabasePath(t *testing.T) {
	opts, command, args, err := parseRootArgs([]string{
		"--db-path", `H:\cache\frozen.db`,
		"strategy-backtest", "--version", "1.5.0", "--from", "2026-08-04", "--to", "2026-08-04",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if opts.DBPath != `H:\cache\frozen.db` || command != "strategy-backtest" || len(args) != 6 {
		t.Fatalf("global DB option was not routed to strategy-backtest: opts=%+v command=%q args=%v", opts, command, args)
	}
}

func TestRunStrategyBacktestCacheOnlyPersistsDeterministicSummary(t *testing.T) {
	database := setupStrategyBacktestTestDB(t)
	appendStrategyBacktestFixture(t, database)
	args := []string{"--version", "1.5.0", "--from", "2026-08-04", "--to", "2026-08-04", "--json"}

	var first bytes.Buffer
	if err := runStrategyBacktest(args, GlobalOptions{JSON: true}, &first, &bytes.Buffer{}); err != nil {
		t.Fatalf("first run failed: %v", err)
	}
	var summary strategyBacktestSummary
	if err := json.Unmarshal(first.Bytes(), &summary); err != nil {
		t.Fatalf("decode output: %v\n%s", err, first.String())
	}
	if !summary.CacheOnly || !summary.Persisted || summary.RunSnapshots != 1 || summary.CandidateSnapshots != 1 || summary.EligibleCandidates != 1 || summary.RuleSnapshots != 1 || summary.OrderEvents != 6 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if summary.TradeCount != 1 || summary.BacktestID == "" || summary.InputHash == "" || summary.ResultHash == "" {
		t.Fatalf("missing deterministic replay fields: %+v", summary)
	}
	if summary.ReplayMetrics.NetPnL != 484.545 || summary.ReplayMetrics.EndingEquity != 100484.545 || summary.ReplayMetrics.PortfolioNetReturnPct <= 0 || summary.ReplayMetrics.Stress20EndingEquity >= summary.ReplayMetrics.EndingEquity || summary.ReplayMetrics.Stress50EndingEquity >= summary.ReplayMetrics.Stress20EndingEquity || summary.ReplayMetrics.ProfitFactorText != "+Inf" {
		t.Fatalf("unexpected real-cost replay metrics: %+v", summary.ReplayMetrics)
	}

	var second bytes.Buffer
	if err := runStrategyBacktest(args, GlobalOptions{JSON: true}, &second, &bytes.Buffer{}); err != nil {
		t.Fatalf("second run failed: %v", err)
	}
	if first.String() != second.String() {
		t.Fatalf("output is not deterministic:\nfirst=%s\nsecond=%s", first.String(), second.String())
	}
	var runCount, tradeCount, metricCount int64
	if err := database.Model(&models.BacktestRun{}).Count(&runCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Model(&models.Metric{}).Count(&metricCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Model(&models.Trade{}).Count(&tradeCount).Error; err != nil {
		t.Fatal(err)
	}
	if runCount != 1 || tradeCount != 1 || metricCount != 26 {
		t.Fatalf("idempotent persistence counts: runs=%d trades=%d metrics=%d", runCount, tradeCount, metricCount)
	}
	var trade models.Trade
	if err := database.First(&trade).Error; err != nil {
		t.Fatal(err)
	}
	if trade.Quantity != 1000 || trade.EntryPrice != 10 || trade.ExitPrice != 10.5 || trade.Fees != 15.455 || trade.NetPnL != 484.545 {
		t.Fatalf("persisted trade did not use frozen event accounting: %+v", trade)
	}
}

func TestRunStrategyBacktestDryRunDoesNotPersist(t *testing.T) {
	database := setupStrategyBacktestTestDB(t)
	appendStrategyBacktestFixture(t, database)
	var output bytes.Buffer
	err := runStrategyBacktest([]string{"--version", "1.5.0", "--start", "2026-08-04", "--end", "2026-08-04", "--dry-run", "--json"}, GlobalOptions{}, &output, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	var summary strategyBacktestSummary
	if err := json.Unmarshal(output.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Persisted {
		t.Fatal("dry run unexpectedly reported persistence")
	}
	var count int64
	if err := database.Model(&models.BacktestRun{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("dry run persisted %d backtest rows", count)
	}
}

func TestRunStrategyBacktestRefusesNetworkFallbackOnCacheMiss(t *testing.T) {
	setupStrategyBacktestTestDB(t)
	err := runStrategyBacktest([]string{"--version", "1.5.0", "--from", "2026-08-04", "--to", "2026-08-04"}, GlobalOptions{}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "拒绝联网补数") {
		t.Fatalf("error = %v, want explicit cache-only refusal", err)
	}
}
