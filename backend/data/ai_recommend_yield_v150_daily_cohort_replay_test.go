package data

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
	"go-stock/backend/persistence"
	"go-stock/backend/strategy/v150"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestLoadV150DailyLedgerAcceptsCompleteValidCohort(t *testing.T) {
	openV150DailyCohortReplayTestDB(t)
	fillAt := time.Date(2026, 8, 3, 10, 0, 0, 0, cnLocation())
	request := appendV150DailyCohortReplayFixture(t, "valid", "000001.SZ", "bank", fillAt)

	ledgers, warnings := loadV150YieldDailyOrderLedgersForRequests(
		[]v150YieldDailyLedgerRequest{request},
		fillAt.Add(5*time.Hour),
	)
	if len(warnings) != 0 || len(ledgers) != 1 {
		t.Fatalf("valid cohort ledger=%+v warnings=%v", ledgers, warnings)
	}
	if ledger := ledgers[request.Key]; ledger.RuleID != request.RuleID || len(ledger.Events) != 4 {
		t.Fatalf("valid cohort returned the wrong immutable ledger: %+v", ledger)
	}
}

func TestLoadV150DailyLedgerIgnoresAuxiliaryObservationPolicyPayload(t *testing.T) {
	openV150DailyCohortReplayTestDB(t)
	fillAt := time.Date(2026, 8, 3, 10, 0, 0, 0, cnLocation())
	request := appendV150DailyCohortReplayFixture(t, "valid-with-observation", "000001.SZ", "bank", fillAt)
	appendV150DailyCohortReplayObservationFixture(t, fillAt.Add(30*time.Minute))

	ledgers, warnings := loadV150YieldDailyOrderLedgersForRequests(
		[]v150YieldDailyLedgerRequest{request},
		fillAt.Add(5*time.Hour),
	)
	if len(warnings) != 0 || len(ledgers) != 1 {
		t.Fatalf("auxiliary observation invalidated decision cohort: ledger=%+v warnings=%v", ledgers, warnings)
	}
}

func TestLoadV150DailyLedgerAsOfExcludesLaterSameDayRun(t *testing.T) {
	openV150DailyCohortReplayTestDB(t)
	loc := cnLocation()
	visibleFillAt := time.Date(2026, 8, 3, 10, 0, 0, 0, loc)
	visible := appendV150DailyCohortReplayFixture(t, "asof-visible", "000001.SZ", "bank", visibleFillAt)
	appendV150DailyCohortReplayFixture(t, "asof-future", "000002.SZ", "software", visibleFillAt.Add(4*time.Hour))

	ledgers, warnings := loadV150YieldDailyOrderLedgersForRequests(
		[]v150YieldDailyLedgerRequest{visible},
		visibleFillAt.Add(5*time.Minute),
	)
	if len(warnings) != 0 || len(ledgers) != 1 {
		t.Fatalf("later same-day run leaked into asOf cohort: ledgers=%+v warnings=%v", ledgers, warnings)
	}
}

func TestLoadV150DailyLedgerRejectsIncompletePolicyMetadata(t *testing.T) {
	openV150DailyCohortReplayTestDB(t)
	fillAt := time.Date(2026, 8, 3, 10, 0, 0, 0, cnLocation())
	request := appendV150DailyCohortReplayFixtureWithRunPayload(
		t,
		"missing-policy",
		"000001.SZ",
		"bank",
		fillAt,
		`{}`,
	)

	assertV150DailyCohortReplayFailsClosed(t, request, fillAt.Add(5*time.Hour))
}

func TestLoadV150DailyLedgerRejectsHiddenDuplicateOpenSymbol(t *testing.T) {
	openV150DailyCohortReplayTestDB(t)
	loc := cnLocation()
	firstDay := time.Date(2026, 8, 3, 10, 0, 0, 0, loc)
	visible := appendV150DailyCohortReplayFixture(t, "duplicate-visible", "000001.SZ", "bank", firstDay)
	appendV150DailyCohortReplayFixture(t, "duplicate-hidden", "000001.SZ", "finance", firstDay.AddDate(0, 0, 1))

	assertV150DailyCohortReplayFailsClosed(t, visible, firstDay.AddDate(0, 0, 1).Add(5*time.Hour))
}

func TestLoadV150DailyLedgerRejectsHiddenThirdDailyFill(t *testing.T) {
	openV150DailyCohortReplayTestDB(t)
	loc := cnLocation()
	day := time.Date(2026, 8, 3, 10, 0, 0, 0, loc)
	visible := appendV150DailyCohortReplayFixture(t, "daily-cap-visible", "000001.SZ", "bank", day)
	appendV150DailyCohortReplayFixture(t, "daily-cap-hidden-2", "000002.SZ", "software", day.Add(15*time.Minute))
	appendV150DailyCohortReplayFixture(t, "daily-cap-hidden-3", "000003.SZ", "medicine", day.Add(30*time.Minute))

	assertV150DailyCohortReplayFailsClosed(t, visible, day.Add(5*time.Hour))
}

func TestLoadV150DailyLedgerRejectsHiddenSecondSectorFill(t *testing.T) {
	openV150DailyCohortReplayTestDB(t)
	loc := cnLocation()
	day := time.Date(2026, 8, 3, 10, 0, 0, 0, loc)
	visible := appendV150DailyCohortReplayFixture(t, "sector-visible", "000001.SZ", "bank", day)
	appendV150DailyCohortReplayFixture(t, "sector-hidden", "000002.SZ", "bank", day.Add(15*time.Minute))

	assertV150DailyCohortReplayFailsClosed(t, visible, day.Add(5*time.Hour))
}

func TestLoadV150DailyLedgerRejectsHiddenStopCooldownReentry(t *testing.T) {
	openV150DailyCohortReplayTestDB(t)
	loc := cnLocation()
	entryAt := time.Date(2026, 8, 3, 10, 0, 0, 0, loc)
	visible := appendV150DailyCohortReplayFixture(t, "cooldown-visible", "000001.SZ", "bank", entryAt)
	appendV150DailyCohortReplayStopExit(t, visible, entryAt.AddDate(0, 0, 1))
	appendV150DailyCohortReplayFixture(t, "cooldown-hidden", "000001.SZ", "bank", entryAt.AddDate(0, 0, 2))

	assertV150DailyCohortReplayFailsClosed(t, visible, entryAt.AddDate(0, 0, 2).Add(5*time.Hour))
}

func TestLoadV150DailyLedgerRejectsHiddenSixthConcurrentPosition(t *testing.T) {
	openV150DailyCohortReplayTestDB(t)
	loc := cnLocation()
	days := []time.Time{
		time.Date(2026, 8, 3, 10, 0, 0, 0, loc),
		time.Date(2026, 8, 4, 10, 0, 0, 0, loc),
		time.Date(2026, 8, 5, 10, 0, 0, 0, loc),
		time.Date(2026, 8, 6, 10, 0, 0, 0, loc),
		time.Date(2026, 8, 7, 10, 0, 0, 0, loc),
		time.Date(2026, 8, 10, 10, 0, 0, 0, loc),
	}
	visible := v150YieldDailyLedgerRequest{}
	for index, fillAt := range days {
		request := appendV150DailyCohortReplayFixture(
			t,
			fmt.Sprintf("max-open-%d", index+1),
			fmt.Sprintf("00000%d.SZ", index+1),
			fmt.Sprintf("sector-%d", index+1),
			fillAt,
		)
		if index == 0 {
			visible = request
		}
	}

	assertV150DailyCohortReplayFailsClosed(t, visible, days[len(days)-1].Add(5*time.Hour))
}

func openV150DailyCohortReplayTestDB(t *testing.T) {
	t.Helper()
	database, err := gorm.Open(
		sqlite.Open(filepath.Join(t.TempDir(), "v150-daily-cohort.db")),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	if err != nil {
		t.Fatal(err)
	}
	previous := db.Dao
	db.Dao = database
	t.Cleanup(func() {
		if sqlDB, sqlErr := database.DB(); sqlErr == nil {
			_ = sqlDB.Close()
		}
		if db.Dao == database {
			db.Dao = previous
		}
	})
	if err := persistence.MigrateStrategyPersistence(database); err != nil {
		t.Fatal(err)
	}
}

func appendV150DailyCohortReplayFixture(
	t *testing.T,
	suffix, symbol, sector string,
	fillAt time.Time,
) v150YieldDailyLedgerRequest {
	t.Helper()
	return appendV150DailyCohortReplayFixtureWithRunPayload(
		t,
		suffix,
		symbol,
		sector,
		fillAt,
		`{"run":{"regime":{"dailyCap":2}}}`,
	)
}

func appendV150DailyCohortReplayFixtureWithRunPayload(
	t *testing.T,
	suffix, symbol, sector string,
	fillAt time.Time,
	runPayload string,
) v150YieldDailyLedgerRequest {
	t.Helper()
	day := normalizeYieldOverviewTradeDay(fillAt)
	tradeDate := day.Format(time.DateOnly)
	runID := "daily-cohort-run-" + suffix
	ruleID := "daily-cohort-rule-" + suffix
	candidateID := "daily-cohort-candidate-" + suffix
	decisionAt := day.Add(9 * time.Hour)
	validFromAt := day.Add(9*time.Hour + 30*time.Minute)
	frozenAt := fillAt.Add(time.Minute)

	cfg := v150.FixedStrategyV150Config()
	rawPrice := 10.0
	unitCost := v150.CalculateTradeCost(
		v150.SideBuy,
		v150.ResolveMarket(symbol),
		rawPrice,
		cfg.RoundLotSize,
		cfg.SlippageScenarios()[0],
		cfg,
	)
	size := v150.SizeRoundLot(unitCost.EffectivePrice, cfg.TargetCashPerPosition, cfg)
	if size.Rejected {
		t.Fatalf("fixture position rejected: %+v", size)
	}
	entryCost := v150.CalculateTradeCost(
		v150.SideBuy,
		v150.ResolveMarket(symbol),
		rawPrice,
		size.Quantity,
		cfg.SlippageScenarios()[0],
		cfg,
	)

	bundle := persistence.StrategySnapshotBundle{
		Run: models.StrategyRunSnapshot{
			RunID: runID, StrategyVersion: v150.StrategyVersion, TradeDate: tradeDate, RunSlot: "cohort-test",
			StartedAt: day.Add(8*time.Hour + 45*time.Minute), AsOf: day.Add(8*time.Hour + 55*time.Minute),
			DataCutoffAt: day.Add(8*time.Hour + 55*time.Minute), DecisionAt: decisionAt,
			GeneratedAt: decisionAt.Add(time.Minute), ValidFromAt: &validFromAt, Mode: "risk_on",
			ConfigHash: v150.FixedStrategyV150ConfigHash(), InputHash: "input-" + suffix,
			PayloadJSON: runPayload, FrozenAt: &frozenAt,
		},
		Candidates: []models.CandidateSnapshot{{
			CandidateID: candidateID, RunID: runID, StrategyVersion: v150.StrategyVersion, TradeDate: tradeDate,
			Symbol: symbol, Sector: sector, Rank: 1, FinalRank: 1, Decision: "production", Eligible: true,
			PayloadJSON: `{}`, FrozenAt: &frozenAt,
		}},
		Rules: []models.RuleSnapshot{{
			RuleID: ruleID, RunID: runID, CandidateID: candidateID, StrategyVersion: v150.StrategyVersion,
			TradeDate: tradeDate, Symbol: symbol, RuleVersion: v150.StrategyVersion, RuleType: "entry",
			Path: string(v150.PathPullback), ValidFromAt: validFromAt, PayloadJSON: `{}`, FrozenAt: &frozenAt,
		}},
		OrderEvents: []models.OrderEvent{
			{
				EventID: runID + "|issued", RunID: runID, RuleID: ruleID, StrategyVersion: v150.StrategyVersion,
				TradeDate: tradeDate, Symbol: symbol, EventType: "rule_issued", Sequence: 1,
				EventAt: decisionAt, Reason: "cohort_test", PayloadJSON: `{}`, FrozenAt: &frozenAt,
			},
			{
				EventID: runID + "|signal", RunID: runID, RuleID: ruleID, StrategyVersion: v150.StrategyVersion,
				TradeDate: tradeDate, Symbol: symbol, EventType: string(v150.EventSignal), Sequence: 2,
				EventAt: fillAt.Add(-15 * time.Minute), Reason: string(v150.PathPullback), PayloadJSON: `{}`, FrozenAt: &frozenAt,
			},
			{
				EventID: runID + "|order", RunID: runID, RuleID: ruleID, StrategyVersion: v150.StrategyVersion,
				TradeDate: tradeDate, Symbol: symbol, EventType: string(v150.EventOrder), Sequence: 3,
				EventAt: fillAt, Reason: "next_bar_market_order", PayloadJSON: `{}`, FrozenAt: &frozenAt,
			},
			{
				EventID: runID + "|fill", RunID: runID, RuleID: ruleID, StrategyVersion: v150.StrategyVersion,
				TradeDate: tradeDate, Symbol: symbol, EventType: string(v150.EventFill), Sequence: 4,
				EventAt: fillAt, Price: entryCost.EffectivePrice, Quantity: float64(size.Quantity),
				Fees:   entryCost.Commission + entryCost.TransferFee + entryCost.StampDuty,
				Reason: "cohort_test", PayloadJSON: `{}`, FrozenAt: &frozenAt,
			},
		},
	}
	if err := persistence.SealStrategySnapshotBundle(&bundle); err != nil {
		t.Fatal(err)
	}
	if err := persistence.AppendStrategySnapshotBundle(context.Background(), db.Dao, bundle); err != nil {
		t.Fatal(err)
	}
	return v150YieldDailyLedgerRequest{
		Key: runID, RunID: runID, RuleID: ruleID, Symbol: symbol,
		WarningKey: ruleID, RequireFill: true,
	}
}

func appendV150DailyCohortReplayObservationFixture(t *testing.T, observedAt time.Time) {
	t.Helper()
	tradeDate := observedAt.In(cnLocation()).Format(time.DateOnly)
	runID := "daily-cohort-security-observation"
	frozenAt := observedAt
	bundle := persistence.StrategySnapshotBundle{
		Run: models.StrategyRunSnapshot{
			RunID: runID, StrategyVersion: v150.StrategyVersion, TradeDate: tradeDate,
			RunSlot:   persistence.StrategyRunModeExecutionSecurityObservation,
			StartedAt: observedAt.Add(-time.Minute), AsOf: observedAt.Add(-time.Minute),
			DataCutoffAt: observedAt, DecisionAt: observedAt, GeneratedAt: observedAt,
			Mode:       persistence.StrategyRunModeExecutionSecurityObservation,
			ConfigHash: v150.FixedStrategyV150ConfigHash(), InputHash: "observation-input",
			PayloadJSON: `{"observation":{"kind":"security"}}`, FrozenAt: &frozenAt,
		},
		SecurityMaster: []models.SecurityMasterHistory{{
			RecordID: runID + "|security", RunID: runID, SnapshotVersion: v150.StrategyVersion,
			Symbol: "000001.SZ", Market: "CN", Exchange: "SZSE", Status: "listed",
			EffectiveFrom: observedAt, Source: "test", PayloadJSON: `{}`, FrozenAt: &frozenAt,
		}},
	}
	if err := persistence.SealStrategySnapshotBundle(&bundle); err != nil {
		t.Fatal(err)
	}
	if err := persistence.AppendStrategySnapshotBundle(context.Background(), db.Dao, bundle); err != nil {
		t.Fatal(err)
	}
}

func appendV150DailyCohortReplayStopExit(
	t *testing.T,
	request v150YieldDailyLedgerRequest,
	exitAt time.Time,
) {
	t.Helper()
	var fill models.OrderEvent
	if err := db.Dao.Where(
		"run_id = ? AND rule_id = ? AND event_type = ?",
		request.RunID,
		request.RuleID,
		string(v150.EventFill),
	).Take(&fill).Error; err != nil {
		t.Fatal(err)
	}
	cfg := v150.FixedStrategyV150Config()
	exitCost := v150.CalculateTradeCost(
		v150.SideSell,
		v150.ResolveMarket(request.Symbol),
		9,
		int(fill.Quantity),
		cfg.SlippageScenarios()[0],
		cfg,
	)
	signalAt := exitAt.Add(-30 * time.Minute)
	orderAt := exitAt.Add(-15 * time.Minute)
	signalFrozenAt := signalAt.Add(time.Minute)
	orderFrozenAt := orderAt.Add(time.Minute)
	fillFrozenAt := exitAt.Add(time.Minute)
	events := []models.OrderEvent{
		{
			EventID: request.RunID + "|exit-signal", RunID: request.RunID, RuleID: request.RuleID,
			StrategyVersion: v150.StrategyVersion, TradeDate: fill.TradeDate, Symbol: request.Symbol,
			EventType: string(v150.EventExitSignal), Sequence: 5, EventAt: signalAt,
			Price: exitCost.EffectivePrice, Quantity: fill.Quantity, Reason: string(v150.ExitStop),
			PayloadJSON: `{}`, FrozenAt: &signalFrozenAt,
		},
		{
			EventID: request.RunID + "|exit-order", RunID: request.RunID, RuleID: request.RuleID,
			StrategyVersion: v150.StrategyVersion, TradeDate: fill.TradeDate, Symbol: request.Symbol,
			EventType: "exit_order", Sequence: 6, EventAt: orderAt,
			Quantity: fill.Quantity, Reason: string(v150.ExitStop), PayloadJSON: `{}`, FrozenAt: &orderFrozenAt,
		},
		{
			EventID: request.RunID + "|exit-fill", RunID: request.RunID, RuleID: request.RuleID,
			StrategyVersion: v150.StrategyVersion, TradeDate: fill.TradeDate, Symbol: request.Symbol,
			EventType: string(v150.EventExitFill), Sequence: 7, EventAt: exitAt,
			Price: exitCost.EffectivePrice, Quantity: fill.Quantity,
			Fees:   exitCost.Commission + exitCost.TransferFee + exitCost.StampDuty,
			Reason: string(v150.ExitStop), PayloadJSON: `{}`, FrozenAt: &fillFrozenAt,
		},
	}
	if err := persistence.SealStrategyOrderEvents(events); err != nil {
		t.Fatal(err)
	}
	if err := persistence.AppendStrategyOrderEvents(context.Background(), db.Dao, request.RunID, events); err != nil {
		t.Fatal(err)
	}
}

func appendV150DailyCohortReplayCorporateActionFixture(
	t *testing.T,
	suffix, symbol, sector string,
	entryDay, actionDay time.Time,
	adjustedQuantity, cashAmount, adjustmentFactor float64,
	actionFrozenAt time.Time,
) v150YieldDailyLedgerRequest {
	t.Helper()
	request := appendV150DailyCohortReplayFixture(
		t,
		suffix,
		symbol,
		sector,
		normalizeYieldOverviewTradeDay(entryDay).Add(10*time.Hour),
	)
	event := models.OrderEvent{
		EventID: request.RunID + "|corporate-action", RunID: request.RunID, RuleID: request.RuleID,
		StrategyVersion: v150.StrategyVersion, TradeDate: normalizeYieldOverviewTradeDay(entryDay).Format(time.DateOnly),
		Symbol: request.Symbol, EventType: string(v150.EventCorporateAction), Sequence: 5,
		EventAt:  normalizeYieldOverviewTradeDay(actionDay).Add(9*time.Hour + 30*time.Minute),
		Quantity: adjustedQuantity, CashAmount: cashAmount, AdjustmentFactor: adjustmentFactor,
		Reason: "cohort_test", PayloadJSON: `{}`, FrozenAt: &actionFrozenAt,
	}
	events := []models.OrderEvent{event}
	if err := persistence.SealStrategyOrderEvents(events); err != nil {
		t.Fatal(err)
	}
	if err := persistence.AppendStrategyOrderEvents(context.Background(), db.Dao, request.RunID, events); err != nil {
		t.Fatal(err)
	}
	return request
}

func assertV150DailyCohortReplayFailsClosed(
	t *testing.T,
	request v150YieldDailyLedgerRequest,
	reportAsOf time.Time,
) {
	t.Helper()
	ledgers, warnings := loadV150YieldDailyOrderLedgersForRequests(
		[]v150YieldDailyLedgerRequest{request},
		reportAsOf,
	)
	if len(ledgers) != 0 {
		t.Fatalf("invalid hidden cohort published a partial ledger: %+v", ledgers)
	}
	wantWarning := request.WarningKey + ":" + v150YieldDailyLedgerMissingHealthCode
	if len(warnings) != 1 || warnings[0] != wantWarning {
		t.Fatalf("cohort warning=%v want %q", warnings, wantWarning)
	}
}
