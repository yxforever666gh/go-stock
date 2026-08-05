package persistence

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"go-stock/backend/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func openStrategyPersistenceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:strategy-persistence-%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "-"))
	database, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := MigrateStrategyPersistence(database); err != nil {
		t.Fatal(err)
	}
	return database
}

func openUnmigratedStrategyPersistenceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:strategy-persistence-unmigrated-%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "-"))
	database, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(models.StrategyPersistenceModels()...); err != nil {
		t.Fatal(err)
	}
	return database
}

func frozenStrategyBundle() StrategySnapshotBundle {
	cn := time.FixedZone("Asia/Shanghai", 8*60*60)
	frozenAt := time.Date(2026, 8, 4, 9, 17, 0, 0, cn)
	startedAt := time.Date(2026, 8, 4, 9, 0, 0, 0, cn)
	asOf := time.Date(2026, 8, 4, 9, 10, 0, 0, cn)
	decisionAt := time.Date(2026, 8, 4, 9, 15, 0, 0, cn)
	generatedAt := time.Date(2026, 8, 4, 9, 16, 0, 0, cn)
	validFromAt := time.Date(2026, 8, 4, 9, 30, 0, 0, cn)
	announcedAt := time.Date(2026, 8, 4, 8, 0, 0, 0, cn)
	bundle := StrategySnapshotBundle{
		Run: models.StrategyRunSnapshot{
			RunID:           "run-20260804-close",
			StrategyVersion: "1.5.0",
			TradeDate:       "2026-08-04",
			RunSlot:         "close",
			StartedAt:       startedAt,
			AsOf:            asOf,
			DataCutoffAt:    asOf,
			DecisionAt:      decisionAt,
			GeneratedAt:     generatedAt,
			ValidFromAt:     &validFromAt,
			ConfigHash:      "config-hash",
			InputHash:       "input-hash",
			PayloadJSON:     `{"mode":"backtest"}`,
			FrozenAt:        &frozenAt,
		},
		Candidates: []models.CandidateSnapshot{{
			CandidateID:     "candidate-000001",
			RunID:           "run-20260804-close",
			StrategyVersion: "1.5.0",
			TradeDate:       "2026-08-04",
			Symbol:          "000001.SZ",
			Name:            "平安银行",
			Rank:            1,
			Decision:        "selected",
			Score:           88,
			Eligible:        true,
			PayloadJSON:     `{"price":10.5}`,
			FrozenAt:        &frozenAt,
		}},
		Rules: []models.RuleSnapshot{{
			RuleID:          "rule-000001-pullback",
			RunID:           "run-20260804-close",
			CandidateID:     "candidate-000001",
			StrategyVersion: "1.5.0",
			TradeDate:       "2026-08-04",
			Symbol:          "000001.SZ",
			RuleVersion:     "1",
			RuleType:        "entry",
			Path:            "pullback",
			ValidFromAt:     validFromAt,
			PayloadJSON:     `{"entryMin":10.2,"entryMax":10.4}`,
			FrozenAt:        &frozenAt,
		}},
		OrderEvents: []models.OrderEvent{{
			EventID:         "order-event-1",
			RunID:           "run-20260804-close",
			RuleID:          "rule-000001-pullback",
			StrategyVersion: "1.5.0",
			TradeDate:       "2026-08-04",
			Symbol:          "000001.SZ",
			EventType:       "rule_issued",
			Sequence:        1,
			EventAt:         decisionAt,
			PayloadJSON:     `{"reason":"frozen plan"}`,
			FrozenAt:        &frozenAt,
		}},
		SecurityMaster: []models.SecurityMasterHistory{{
			RecordID:        "security-000001-2026",
			RunID:           "run-20260804-close",
			SnapshotVersion: "1.5.0",
			Symbol:          "000001.SZ",
			Name:            "平安银行",
			Market:          "CN",
			Exchange:        "SZSE",
			Status:          "listed",
			EffectiveFrom:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			PayloadJSON:     `{"listed":true}`,
			FrozenAt:        &frozenAt,
		}},
		CorporateActions: []models.CorporateActionEvent{{
			EventID:         "action-000001-20260804",
			RunID:           "run-20260804-close",
			SnapshotVersion: "1.5.0",
			Symbol:          "000001.SZ",
			ActionType:      "cash_dividend",
			AnnouncedAt:     &announcedAt,
			ExDate:          time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC),
			CashDividend:    0.1,
			Currency:        "CNY",
			PayloadJSON:     `{"cashDividend":0.1}`,
			FrozenAt:        &frozenAt,
		}},
	}
	if err := SealStrategySnapshotBundle(&bundle); err != nil {
		panic(err)
	}
	return bundle
}

func appendedOrderEvent(bundle StrategySnapshotBundle, id, eventType string, sequence int, at time.Time) models.OrderEvent {
	frozenAt := at.Add(time.Minute)
	event := models.OrderEvent{
		EventID:         id,
		RunID:           bundle.Run.RunID,
		RuleID:          bundle.Rules[0].RuleID,
		StrategyVersion: bundle.Run.StrategyVersion,
		TradeDate:       bundle.Run.TradeDate,
		Symbol:          bundle.Rules[0].Symbol,
		EventType:       eventType,
		Sequence:        sequence,
		EventAt:         at,
		PayloadJSON:     `{}`,
		FrozenAt:        &frozenAt,
	}
	switch eventType {
	case "order", "fill", "exit_signal", "exit_order", "exit_fill":
		event.Price = 10.5
		event.Quantity = 900
	}
	if eventType == "fill" {
		event.Fees = 5 + event.Price*event.Quantity*0.00001
	}
	if eventType == "exit_fill" {
		event.Fees = 5 + event.Price*event.Quantity*(0.00001+0.0005)
	}
	sealed := []models.OrderEvent{event}
	if err := SealStrategyOrderEvents(sealed); err != nil {
		panic(err)
	}
	return sealed[0]
}

func TestAppendAndLoadFrozenStrategySnapshotBundle(t *testing.T) {
	database := openStrategyPersistenceTestDB(t)
	bundle := frozenStrategyBundle()
	if err := AppendStrategySnapshotBundle(context.Background(), database, bundle); err != nil {
		t.Fatalf("append failed: %v", err)
	}
	if err := AppendStrategySnapshotBundle(context.Background(), database, bundle); err == nil {
		t.Fatal("expected duplicate immutable bundle to fail")
	}

	start := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)
	inputs, err := LoadFrozenStrategyInputs(context.Background(), database, "1.5.0", start, start)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if len(inputs.Runs) != 1 || len(inputs.Candidates) != 1 || len(inputs.Rules) != 1 || len(inputs.OrderEvents) != 1 || len(inputs.SecurityMaster) != 1 || len(inputs.CorporateActions) != 1 {
		t.Fatalf("unexpected input counts: %+v", inputs)
	}
	hashA := FrozenStrategyInputHash(inputs)
	inputs.Candidates = append([]models.CandidateSnapshot(nil), inputs.Candidates...)
	hashB := FrozenStrategyInputHash(inputs)
	if hashA == "" || hashA != hashB {
		t.Fatalf("input hash is not deterministic: %q != %q", hashA, hashB)
	}
	changedAccounting := inputs
	changedAccounting.OrderEvents = append([]models.OrderEvent(nil), inputs.OrderEvents...)
	changedAccounting.OrderEvents[0].Fees++
	if got := FrozenStrategyInputHash(changedAccounting); got == hashA {
		t.Fatal("input hash did not include persisted order-event fees")
	}
	changedFrozenAt := inputs
	changedFrozenAt.Runs = append([]models.StrategyRunSnapshot(nil), inputs.Runs...)
	later := inputs.Runs[0].FrozenAt.Add(time.Second)
	changedFrozenAt.Runs[0].FrozenAt = &later
	if got := FrozenStrategyInputHash(changedFrozenAt); got == hashA {
		t.Fatal("input hash did not include deterministic frozen time")
	}
	changedCandidate := inputs
	changedCandidate.Candidates = append([]models.CandidateSnapshot(nil), inputs.Candidates...)
	changedCandidate.Candidates[0].Score++
	if got := FrozenStrategyInputHash(changedCandidate); got == hashA {
		t.Fatal("input hash did not include candidate business fields")
	}
	changedSecurity := inputs
	changedSecurity.SecurityMaster = append([]models.SecurityMasterHistory(nil), inputs.SecurityMaster...)
	changedSecurity.SecurityMaster[0].Status = "suspended"
	if got := FrozenStrategyInputHash(changedSecurity); got == hashA {
		t.Fatal("input hash did not include security-master business fields")
	}
	changedAction := inputs
	changedAction.CorporateActions = append([]models.CorporateActionEvent(nil), inputs.CorporateActions...)
	changedAction.CorporateActions[0].CashDividend++
	if got := FrozenStrategyInputHash(changedAction); got == hashA {
		t.Fatal("input hash did not include corporate-action business fields")
	}

	var runCount int64
	if err := database.Model(&models.StrategyRunSnapshot{}).Count(&runCount).Error; err != nil {
		t.Fatal(err)
	}
	if runCount != 1 {
		t.Fatalf("duplicate append changed run count: %d", runCount)
	}
}

func TestAppendStrategySnapshotBundleRejectsUnfrozenChild(t *testing.T) {
	database := openStrategyPersistenceTestDB(t)
	bundle := frozenStrategyBundle()
	bundle.Candidates[0].FrozenAt = nil
	err := AppendStrategySnapshotBundle(context.Background(), database, bundle)
	if !errors.Is(err, ErrInvalidImmutableRecord) {
		t.Fatalf("error = %v, want ErrInvalidImmutableRecord", err)
	}
}

func TestAppendStrategySnapshotBundleRequiresExplicitReasonedNoTrade(t *testing.T) {
	database := openStrategyPersistenceTestDB(t)
	bundle := frozenStrategyBundle()
	bundle.Rules = nil
	bundle.OrderEvents = nil
	if err := SealStrategySnapshotBundle(&bundle); err != nil {
		t.Fatal(err)
	}
	if err := AppendStrategySnapshotBundle(context.Background(), database, bundle); !errors.Is(err, ErrInvalidImmutableRecord) {
		t.Fatalf("implicit empty run error = %v", err)
	}

	noTrade := frozenStrategyBundle()
	noTrade.Rules = nil
	event := noTrade.OrderEvents[0]
	event.EventID = "no-trade-explicit"
	event.RuleID = ""
	event.Symbol = "510300.SH"
	event.EventType = "no_trade"
	event.Reason = "risk_off"
	event.Price, event.Quantity, event.Fees = 0, 0, 0
	noTrade.OrderEvents = []models.OrderEvent{event}
	if err := SealStrategySnapshotBundle(&noTrade); err != nil {
		t.Fatal(err)
	}
	if err := AppendStrategySnapshotBundle(context.Background(), database, noTrade); err != nil {
		t.Fatalf("explicit no_trade append failed: %v", err)
	}
}

func TestAppendStrategySnapshotBundleAllowsSecurityObservationWithoutFakeNoTrade(t *testing.T) {
	database := openStrategyPersistenceTestDB(t)
	bundle := frozenStrategyBundle()
	bundle.Run.RunID = "security-observation-run"
	bundle.Run.RunSlot = StrategyRunModeExecutionSecurityObservation
	bundle.Run.Mode = StrategyRunModeExecutionSecurityObservation
	bundle.Run.ValidFromAt = nil
	bundle.Candidates = nil
	bundle.Rules = nil
	bundle.OrderEvents = nil
	bundle.CorporateActions = nil
	security := bundle.SecurityMaster[0]
	security.RecordID = "security-observation-row"
	security.RunID = bundle.Run.RunID
	bundle.SecurityMaster = []models.SecurityMasterHistory{security}
	if err := SealStrategySnapshotBundle(&bundle); err != nil {
		t.Fatal(err)
	}
	if err := AppendStrategySnapshotBundle(context.Background(), database, bundle); err != nil {
		t.Fatalf("append security observation: %v", err)
	}

	invalid := bundle
	invalid.Run.RunID = "security-observation-with-candidate"
	invalid.SecurityMaster = append([]models.SecurityMasterHistory(nil), bundle.SecurityMaster...)
	invalid.SecurityMaster[0].RecordID = "security-observation-row-invalid"
	invalid.SecurityMaster[0].RunID = invalid.Run.RunID
	candidate := frozenStrategyBundle().Candidates[0]
	candidate.CandidateID = "security-observation-candidate"
	candidate.RunID = invalid.Run.RunID
	invalid.Candidates = []models.CandidateSnapshot{candidate}
	if err := SealStrategySnapshotBundle(&invalid); err != nil {
		t.Fatal(err)
	}
	if err := AppendStrategySnapshotBundle(context.Background(), database, invalid); !errors.Is(err, ErrInvalidImmutableRecord) {
		t.Fatalf("mixed observation error = %v, want ErrInvalidImmutableRecord", err)
	}
}

func TestSnapshotSealCoversBusinessFieldsAndCanonicalPayload(t *testing.T) {
	database := openStrategyPersistenceTestDB(t)
	bundle := frozenStrategyBundle()
	if len(bundle.Run.SnapshotHash) != 64 || len(bundle.Candidates[0].SnapshotHash) != 64 {
		t.Fatalf("seal is not SHA-256: run=%q candidate=%q", bundle.Run.SnapshotHash, bundle.Candidates[0].SnapshotHash)
	}
	tamperedField := bundle
	tamperedField.Candidates = append([]models.CandidateSnapshot(nil), bundle.Candidates...)
	tamperedField.Candidates[0].Score++
	if err := AppendStrategySnapshotBundle(context.Background(), database, tamperedField); !errors.Is(err, ErrInvalidImmutableRecord) {
		t.Fatalf("business-field tamper error = %v", err)
	}
	tamperedPayload := bundle
	tamperedPayload.Candidates = append([]models.CandidateSnapshot(nil), bundle.Candidates...)
	tamperedPayload.Candidates[0].PayloadJSON = `{"price":10.6}`
	if err := AppendStrategySnapshotBundle(context.Background(), database, tamperedPayload); !errors.Is(err, ErrInvalidImmutableRecord) {
		t.Fatalf("payload tamper error = %v", err)
	}

	canonical := frozenStrategyBundle()
	canonical.Candidates[0].Name = "  TEST NAME  "
	canonical.Candidates[0].PayloadJSON = " { \"z\" : 1, \"a\" : true } "
	if err := SealStrategySnapshotBundle(&canonical); err != nil {
		t.Fatal(err)
	}
	if canonical.Candidates[0].Name != "TEST NAME" || canonical.Candidates[0].PayloadJSON != `{"a":true,"z":1}` {
		t.Fatalf("seal did not normalize immutable business data: %+v", canonical.Candidates[0])
	}
}

func TestAppendStrategySnapshotBundleRejectsTimeCausalityViolations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*StrategySnapshotBundle)
	}{
		{name: "missing started", mutate: func(bundle *StrategySnapshotBundle) { bundle.Run.StartedAt = time.Time{} }},
		{name: "cutoff before started", mutate: func(bundle *StrategySnapshotBundle) { bundle.Run.DataCutoffAt = bundle.Run.StartedAt.Add(-time.Second) }},
		{name: "decision before cutoff", mutate: func(bundle *StrategySnapshotBundle) {
			bundle.Run.DecisionAt = bundle.Run.DataCutoffAt.Add(-time.Second)
		}},
		{name: "valid from not after decision", mutate: func(bundle *StrategySnapshotBundle) { at := bundle.Run.DecisionAt; bundle.Run.ValidFromAt = &at }},
		{name: "rule before run valid from", mutate: func(bundle *StrategySnapshotBundle) {
			bundle.Rules[0].ValidFromAt = bundle.Run.ValidFromAt.Add(-time.Second)
		}},
		{name: "order before valid from", mutate: func(bundle *StrategySnapshotBundle) {
			bundle.OrderEvents[0].EventAt = bundle.Run.ValidFromAt.Add(-time.Second)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			database := openStrategyPersistenceTestDB(t)
			bundle := frozenStrategyBundle()
			tt.mutate(&bundle)
			err := AppendStrategySnapshotBundle(context.Background(), database, bundle)
			if !errors.Is(err, ErrInvalidImmutableRecord) {
				t.Fatalf("error = %v, want ErrInvalidImmutableRecord", err)
			}
		})
	}
}

func TestSnapshotReferenceDataRejectsFutureKnowledge(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*StrategySnapshotBundle)
	}{
		{name: "security effective after cutoff", mutate: func(bundle *StrategySnapshotBundle) {
			bundle.SecurityMaster[0].EffectiveFrom = bundle.Run.DataCutoffAt.Add(time.Minute)
		}},
		{name: "action announced after cutoff", mutate: func(bundle *StrategySnapshotBundle) {
			at := bundle.Run.DataCutoffAt.Add(time.Minute)
			bundle.CorporateActions[0].AnnouncedAt = &at
		}},
		{name: "security belongs to another run", mutate: func(bundle *StrategySnapshotBundle) {
			bundle.SecurityMaster[0].RunID = "foreign-run"
		}},
		{name: "action belongs to another run", mutate: func(bundle *StrategySnapshotBundle) {
			bundle.CorporateActions[0].RunID = "foreign-run"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			database := openStrategyPersistenceTestDB(t)
			bundle := frozenStrategyBundle()
			tt.mutate(&bundle)
			if err := SealStrategySnapshotBundle(&bundle); err != nil {
				t.Fatal(err)
			}
			if err := AppendStrategySnapshotBundle(context.Background(), database, bundle); !errors.Is(err, ErrInvalidImmutableRecord) {
				t.Fatalf("future/foreign reference data error = %v", err)
			}
		})
	}
}

func TestLoadFrozenStrategyInputsReturnsCacheMiss(t *testing.T) {
	database := openStrategyPersistenceTestDB(t)
	day := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)
	_, err := LoadFrozenStrategyInputs(context.Background(), database, "1.5.0", day, day)
	if !errors.Is(err, ErrNoFrozenSnapshots) {
		t.Fatalf("error = %v, want ErrNoFrozenSnapshots", err)
	}
}

func TestLoadFrozenStrategyInputsRejectsIncompleteCache(t *testing.T) {
	database := openStrategyPersistenceTestDB(t)
	bundle := frozenStrategyBundle()
	if err := AppendStrategySnapshotBundle(context.Background(), database, bundle); err != nil {
		t.Fatal(err)
	}
	if err := database.Exec("DROP TRIGGER immutable_strategy_candidate_snapshot_delete").Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Where("candidate_id = ?", bundle.Candidates[0].CandidateID).Delete(&models.CandidateSnapshot{}).Error; err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)
	_, err := LoadFrozenStrategyInputs(context.Background(), database, "1.5.0", day, day)
	if !errors.Is(err, ErrIncompleteSnapshots) {
		t.Fatalf("error = %v, want ErrIncompleteSnapshots", err)
	}
}

func TestLoadFrozenStrategyInputsRejectsTamperedCausalTimeline(t *testing.T) {
	database := openStrategyPersistenceTestDB(t)
	bundle := frozenStrategyBundle()
	if err := AppendStrategySnapshotBundle(context.Background(), database, bundle); err != nil {
		t.Fatal(err)
	}
	if err := database.Exec("DROP TRIGGER immutable_strategy_run_snapshot_update").Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Model(&models.StrategyRunSnapshot{}).Where("run_id = ?", bundle.Run.RunID).UpdateColumn("decision_at", bundle.Run.DataCutoffAt.Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)
	_, err := LoadFrozenStrategyInputs(context.Background(), database, "1.5.0", day, day)
	if !errors.Is(err, ErrIncompleteSnapshots) {
		t.Fatalf("error = %v, want ErrIncompleteSnapshots", err)
	}
}

func TestLoadFrozenStrategyInputsRevalidatesSnapshotSHA256(t *testing.T) {
	database := openStrategyPersistenceTestDB(t)
	bundle := frozenStrategyBundle()
	if err := AppendStrategySnapshotBundle(context.Background(), database, bundle); err != nil {
		t.Fatal(err)
	}
	if err := database.Exec("DROP TRIGGER immutable_strategy_candidate_snapshot_update").Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Model(&models.CandidateSnapshot{}).Where("candidate_id = ?", bundle.Candidates[0].CandidateID).UpdateColumn("score", bundle.Candidates[0].Score+1).Error; err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)
	_, err := LoadFrozenStrategyInputs(context.Background(), database, "1.5.0", day, day)
	if !errors.Is(err, ErrIncompleteSnapshots) {
		t.Fatalf("read-time SHA-256 revalidation error = %v", err)
	}
}

func TestImmutableInputTablesRejectUpdateAndDelete(t *testing.T) {
	database := openStrategyPersistenceTestDB(t)
	bundle := frozenStrategyBundle()
	if err := AppendStrategySnapshotBundle(context.Background(), database, bundle); err != nil {
		t.Fatal(err)
	}
	tables := []string{
		"strategy_run_snapshot",
		"strategy_candidate_snapshot",
		"strategy_rule_snapshot",
		"strategy_order_event",
		"security_master_history",
		"corporate_action_event",
	}
	for _, table := range tables {
		t.Run(table, func(t *testing.T) {
			if err := database.Exec("UPDATE " + table + " SET payload_json = payload_json WHERE id = 1").Error; err == nil {
				t.Fatal("immutable UPDATE unexpectedly succeeded")
			}
			if err := database.Exec("DELETE FROM " + table + " WHERE id = 1").Error; err == nil {
				t.Fatal("immutable DELETE unexpectedly succeeded")
			}
		})
	}
}

func TestMigrateStrategyPersistenceReplacesLegacyRunGlobalEventIndex(t *testing.T) {
	// Start from a fully migrated database so the retry also proves that
	// immutable UPDATE/DELETE triggers do not require rewriting existing rows.
	database := openStrategyPersistenceTestDB(t)
	if err := database.Migrator().DropIndex(&models.OrderEvent{}, orderEventRuleSequenceIndex); err != nil {
		t.Fatal(err)
	}
	if err := database.Exec("CREATE UNIQUE INDEX " + legacyOrderEventSequenceIndex + " ON strategy_order_event (run_id, sequence)").Error; err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 4, 9, 30, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	frozenAt := at.Add(time.Minute)
	legacy := models.OrderEvent{
		EventID: "legacy-rule-a-1", RunID: "legacy-run", RuleID: "legacy-rule-a", StrategyVersion: "1.5.0",
		TradeDate: "2026-08-04", Symbol: "000001.SZ", EventType: "rule_issued", Sequence: 1,
		EventAt: at, SnapshotHash: "legacy-preview-hash", PayloadJSON: `{}`, FrozenAt: &frozenAt,
	}
	if err := database.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateStrategyPersistence(database); err != nil {
		t.Fatal(err)
	}
	if database.Migrator().HasIndex(&models.OrderEvent{}, legacyOrderEventSequenceIndex) {
		t.Fatal("legacy run-global sequence index still exists")
	}
	if !database.Migrator().HasIndex(&models.OrderEvent{}, orderEventRuleSequenceIndex) || !database.Migrator().HasIndex(&models.OrderEvent{}, orderEventNoTradeRunIndex) {
		t.Fatal("replacement per-rule/no-trade indexes were not installed")
	}

	otherRule := legacy
	otherRule.ID, otherRule.CreatedAt = 0, time.Time{}
	otherRule.EventID, otherRule.RuleID, otherRule.Symbol = "legacy-rule-b-1", "legacy-rule-b", "600000.SH"
	if err := database.Create(&otherRule).Error; err != nil {
		t.Fatalf("same run sequence for a different rule should be allowed: %v", err)
	}
	duplicateRuleSequence := otherRule
	duplicateRuleSequence.ID, duplicateRuleSequence.CreatedAt = 0, time.Time{}
	duplicateRuleSequence.EventID = "legacy-rule-b-duplicate"
	if err := database.Create(&duplicateRuleSequence).Error; err == nil {
		t.Fatal("duplicate sequence in one rule unexpectedly succeeded")
	}
	var count int64
	if err := database.Model(&models.OrderEvent{}).Where("run_id = ?", legacy.RunID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("migration rewrote or lost immutable rows: count=%d", count)
	}
}

func TestMigrateStrategyPersistenceAuditsDuplicateRuleSequencesBeforeIndexCreation(t *testing.T) {
	database := openUnmigratedStrategyPersistenceTestDB(t)
	if err := database.Migrator().DropIndex(&models.OrderEvent{}, orderEventRuleSequenceIndex); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 4, 9, 30, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	frozenAt := at.Add(time.Minute)
	rows := []models.OrderEvent{
		{EventID: "duplicate-a", RunID: "duplicate-run", RuleID: "duplicate-rule", StrategyVersion: "1.5.0", TradeDate: "2026-08-04", Symbol: "000001.SZ", EventType: "rule_issued", Sequence: 1, EventAt: at, SnapshotHash: "legacy-a", PayloadJSON: `{}`, FrozenAt: &frozenAt},
		{EventID: "duplicate-b", RunID: "duplicate-run", RuleID: "duplicate-rule", StrategyVersion: "1.5.0", TradeDate: "2026-08-04", Symbol: "000001.SZ", EventType: "signal", Sequence: 1, EventAt: at, SnapshotHash: "legacy-b", PayloadJSON: `{}`, FrozenAt: &frozenAt},
	}
	if err := database.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateStrategyPersistence(database); !errors.Is(err, ErrImmutableConflict) {
		t.Fatalf("migration audit error = %v, want ErrImmutableConflict", err)
	}
	if database.Migrator().HasIndex(&models.OrderEvent{}, orderEventRuleSequenceIndex) {
		t.Fatal("unsafe per-rule index was created despite audit failure")
	}
}

func TestAppendStrategyOrderEventsExtendsFrozenRunWithoutUpdatingIt(t *testing.T) {
	database := openStrategyPersistenceTestDB(t)
	bundle := frozenStrategyBundle()
	if err := AppendStrategySnapshotBundle(context.Background(), database, bundle); err != nil {
		t.Fatal(err)
	}
	signalAt := bundle.OrderEvents[0].EventAt.Add(15 * time.Minute)
	orderAt := signalAt.Add(15 * time.Minute)
	signal := appendedOrderEvent(bundle, "future-signal", "signal", 2, signalAt)
	order := appendedOrderEvent(bundle, "future-order", "order", 3, orderAt)
	fill := appendedOrderEvent(bundle, "future-fill", "fill", 4, orderAt)
	exitSignal := appendedOrderEvent(bundle, "future-exit-signal", "exit_signal", 5, orderAt.Add(24*time.Hour))
	exit := appendedOrderEvent(bundle, "future-exit", "exit_fill", 6, orderAt.Add(24*time.Hour))
	exit.Price = 11
	exit.Fees = 5 + exit.Price*exit.Quantity*(0.00001+0.0005)
	exit.Reason = "target"
	sealedExit := []models.OrderEvent{exit}
	if err := SealStrategyOrderEvents(sealedExit); err != nil {
		t.Fatal(err)
	}
	exit = sealedExit[0]
	// Deliberately pass the batch out of order; sequence defines the ledger.
	if err := AppendStrategyOrderEvents(context.Background(), database, bundle.Run.RunID, []models.OrderEvent{exit, signal, fill, order, exitSignal}); err != nil {
		t.Fatal(err)
	}

	day := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)
	inputs, err := LoadFrozenStrategyInputs(context.Background(), database, "1.5.0", day, day)
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs.OrderEvents) != 6 {
		t.Fatalf("loaded order events = %d, want complete lifecycle", len(inputs.OrderEvents))
	}
	var run models.StrategyRunSnapshot
	if err := database.Where("run_id = ?", bundle.Run.RunID).First(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.OrderEventCount != 1 {
		t.Fatalf("frozen run was updated: orderEventCount=%d", run.OrderEventCount)
	}
}

func TestAppendStrategyOrderEventsAllowsReverseWriteOrderAcrossRulesAndReplaysDeterministically(t *testing.T) {
	database := openStrategyPersistenceTestDB(t)
	bundle := frozenStrategyBundle()
	secondCandidate := bundle.Candidates[0]
	secondCandidate.CandidateID = "candidate-600000"
	secondCandidate.Symbol = "600000.SH"
	secondCandidate.Name = "浦发银行"
	secondCandidate.Rank = 2
	secondRule := bundle.Rules[0]
	secondRule.RuleID = "rule-600000-pullback"
	secondRule.CandidateID = secondCandidate.CandidateID
	secondRule.Symbol = secondCandidate.Symbol
	secondIssued := bundle.OrderEvents[0]
	secondIssued.EventID = "order-event-rule-b-1"
	secondIssued.RuleID = secondRule.RuleID
	secondIssued.Symbol = secondRule.Symbol
	// Per-rule ledgers intentionally start from the same sequence number.
	secondIssued.Sequence = 1
	secondSecurity := bundle.SecurityMaster[0]
	secondSecurity.RecordID = "security-600000-2026"
	secondSecurity.Symbol = secondCandidate.Symbol
	secondSecurity.Name = secondCandidate.Name
	secondSecurity.Exchange = "SSE"
	bundle.Candidates = append(bundle.Candidates, secondCandidate)
	bundle.Rules = append(bundle.Rules, secondRule)
	bundle.OrderEvents = append(bundle.OrderEvents, secondIssued)
	bundle.SecurityMaster = append(bundle.SecurityMaster, secondSecurity)
	if err := SealStrategySnapshotBundle(&bundle); err != nil {
		t.Fatal(err)
	}
	if err := AppendStrategySnapshotBundle(context.Background(), database, bundle); err != nil {
		t.Fatal(err)
	}

	makeEvent := func(ruleIndex int, id, eventType string, sequence int, at time.Time) models.OrderEvent {
		event := appendedOrderEvent(bundle, id, eventType, sequence, at)
		event.RuleID = bundle.Rules[ruleIndex].RuleID
		event.Symbol = bundle.Rules[ruleIndex].Symbol
		sealed := []models.OrderEvent{event}
		if err := SealStrategyOrderEvents(sealed); err != nil {
			t.Fatal(err)
		}
		return sealed[0]
	}
	cn := time.FixedZone("Asia/Shanghai", 8*60*60)
	aSignalAt := time.Date(2026, 8, 4, 10, 15, 0, 0, cn)
	aOrderAt := aSignalAt.Add(15 * time.Minute)
	aExitSignalAt := time.Date(2026, 8, 5, 10, 15, 0, 0, cn)
	aExitAt := aExitSignalAt.Add(15 * time.Minute)
	ruleAEvents := []models.OrderEvent{
		makeEvent(0, "rule-a-signal", "signal", 2, aSignalAt),
		makeEvent(0, "rule-a-order", "order", 3, aOrderAt),
		makeEvent(0, "rule-a-fill", "fill", 4, aOrderAt),
		makeEvent(0, "rule-a-exit-signal", "exit_signal", 5, aExitSignalAt),
		makeEvent(0, "rule-a-exit-fill", "exit_fill", 6, aExitAt),
	}
	// Write the later rule-A facts first.
	if err := AppendStrategyOrderEvents(context.Background(), database, bundle.Run.RunID, ruleAEvents); err != nil {
		t.Fatal(err)
	}

	bSignalAt := time.Date(2026, 8, 4, 9, 45, 0, 0, cn)
	bOrderAt := bSignalAt.Add(15 * time.Minute)
	bExitSignalAt := time.Date(2026, 8, 5, 9, 45, 0, 0, cn)
	bExitAt := bExitSignalAt.Add(15 * time.Minute)
	ruleBEvents := []models.OrderEvent{
		makeEvent(1, "rule-b-signal", "signal", 2, bSignalAt),
		makeEvent(1, "rule-b-order", "order", 3, bOrderAt),
		makeEvent(1, "rule-b-fill", "fill", 4, bOrderAt),
		makeEvent(1, "rule-b-exit-signal", "exit_signal", 5, bExitSignalAt),
		makeEvent(1, "rule-b-exit-fill", "exit_fill", 6, bExitAt),
	}
	// A late writer may append an earlier fact timeline for another rule.
	if err := AppendStrategyOrderEvents(context.Background(), database, bundle.Run.RunID, ruleBEvents); err != nil {
		t.Fatal(err)
	}

	var sharedSequenceCount int64
	if err := database.Model(&models.OrderEvent{}).
		Where("run_id = ? AND sequence = ?", bundle.Run.RunID, 2).
		Count(&sharedSequenceCount).Error; err != nil {
		t.Fatal(err)
	}
	if sharedSequenceCount != 2 {
		t.Fatalf("shared sequence count = %d, want one per rule", sharedSequenceCount)
	}
	duplicate := makeEvent(1, "rule-b-duplicate-sequence", "signal", 2, bSignalAt.Add(time.Minute))
	if err := AppendStrategyOrderEvents(context.Background(), database, bundle.Run.RunID, []models.OrderEvent{duplicate}); !errors.Is(err, ErrImmutableConflict) {
		t.Fatalf("same-rule duplicate sequence error = %v", err)
	}

	day := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)
	inputs, err := LoadFrozenStrategyInputs(context.Background(), database, "1.5.0", day, day)
	if err != nil {
		t.Fatal(err)
	}
	replayFrozenAt := time.Date(2026, 8, 5, 16, 0, 0, 0, cn)
	trades, stats, resultHash, err := ReplayFrozenStrategyInputs("bt-reverse-rule-write", "1.5.0", inputs, replayFrozenAt)
	if err != nil {
		t.Fatal(err)
	}
	reversed := inputs
	reversed.OrderEvents = append([]models.OrderEvent(nil), inputs.OrderEvents...)
	for left, right := 0, len(reversed.OrderEvents)-1; left < right; left, right = left+1, right-1 {
		reversed.OrderEvents[left], reversed.OrderEvents[right] = reversed.OrderEvents[right], reversed.OrderEvents[left]
	}
	againTrades, againStats, againHash, err := ReplayFrozenStrategyInputs("bt-reverse-rule-write", "1.5.0", reversed, replayFrozenAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 2 || trades[0].Symbol != secondRule.Symbol {
		t.Fatalf("replay did not follow fact time: %+v", trades)
	}
	if resultHash == "" || resultHash != againHash || !reflect.DeepEqual(trades, againTrades) || !reflect.DeepEqual(stats, againStats) {
		t.Fatalf("reverse-write replay is not deterministic: first=%s second=%s", resultHash, againHash)
	}
}

func TestAppendStrategyOrderEventsRejectsDuplicateIdentityAndSequence(t *testing.T) {
	database := openStrategyPersistenceTestDB(t)
	bundle := frozenStrategyBundle()
	if err := AppendStrategySnapshotBundle(context.Background(), database, bundle); err != nil {
		t.Fatal(err)
	}
	at := bundle.OrderEvents[0].EventAt.Add(15 * time.Minute)
	first := appendedOrderEvent(bundle, "append-unique", "signal", 2, at)
	if err := AppendStrategyOrderEvents(context.Background(), database, bundle.Run.RunID, []models.OrderEvent{first}); err != nil {
		t.Fatal(err)
	}

	if err := AppendStrategyOrderEvents(context.Background(), database, bundle.Run.RunID, []models.OrderEvent{first}); !errors.Is(err, ErrImmutableConflict) {
		t.Fatalf("duplicate event error = %v, want ErrImmutableConflict", err)
	}
	sameSequence := appendedOrderEvent(bundle, "append-other-id", "signal", 2, at.Add(time.Minute))
	if err := AppendStrategyOrderEvents(context.Background(), database, bundle.Run.RunID, []models.OrderEvent{sameSequence}); !errors.Is(err, ErrImmutableConflict) {
		t.Fatalf("duplicate sequence error = %v, want ErrImmutableConflict", err)
	}
	sameID := appendedOrderEvent(bundle, first.EventID, "order", 3, at.Add(time.Minute))
	if err := AppendStrategyOrderEvents(context.Background(), database, bundle.Run.RunID, []models.OrderEvent{sameID}); !errors.Is(err, ErrImmutableConflict) {
		t.Fatalf("duplicate identity error = %v, want ErrImmutableConflict", err)
	}
	var count int64
	if err := database.Model(&models.OrderEvent{}).Where("run_id = ?", bundle.Run.RunID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("conflicting appends changed row count to %d", count)
	}
}

func TestAppendStrategyOrderEventsRejectsCausalViolations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*models.OrderEvent, StrategySnapshotBundle)
		want   error
	}{
		{name: "wrong version", mutate: func(event *models.OrderEvent, _ StrategySnapshotBundle) { event.StrategyVersion = "1.5.1" }, want: ErrInvalidImmutableRecord},
		{name: "non increasing sequence", mutate: func(event *models.OrderEvent, _ StrategySnapshotBundle) { event.Sequence = 1 }, want: ErrInvalidImmutableRecord},
		{name: "time before tail", mutate: func(event *models.OrderEvent, bundle StrategySnapshotBundle) {
			event.EventAt = bundle.OrderEvents[0].EventAt.Add(-time.Second)
		}, want: ErrInvalidImmutableRecord},
		{name: "frozen before event", mutate: func(event *models.OrderEvent, _ StrategySnapshotBundle) {
			at := event.EventAt.Add(-time.Second)
			event.FrozenAt = &at
		}, want: ErrInvalidImmutableRecord},
		{name: "invalid entry lot", mutate: func(event *models.OrderEvent, _ StrategySnapshotBundle) { event.Quantity = 99 }, want: ErrInvalidImmutableRecord},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			database := openStrategyPersistenceTestDB(t)
			bundle := frozenStrategyBundle()
			if err := AppendStrategySnapshotBundle(context.Background(), database, bundle); err != nil {
				t.Fatal(err)
			}
			event := appendedOrderEvent(bundle, "invalid-append", "signal", 2, bundle.OrderEvents[0].EventAt.Add(time.Minute))
			tt.mutate(&event, bundle)
			err := AppendStrategyOrderEvents(context.Background(), database, bundle.Run.RunID, []models.OrderEvent{event})
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestAppendStrategyOrderEventsEnforcesLifecycleStateMachine(t *testing.T) {
	tests := []struct {
		name  string
		build func(StrategySnapshotBundle) []models.OrderEvent
	}{
		{name: "orphan order", build: func(bundle StrategySnapshotBundle) []models.OrderEvent {
			return []models.OrderEvent{appendedOrderEvent(bundle, "orphan-order", "order", 2, bundle.OrderEvents[0].EventAt.Add(15*time.Minute))}
		}},
		{name: "fill without order", build: func(bundle StrategySnapshotBundle) []models.OrderEvent {
			at := bundle.OrderEvents[0].EventAt.Add(15 * time.Minute)
			return []models.OrderEvent{
				appendedOrderEvent(bundle, "signal-before-orphan-fill", "signal", 2, at),
				appendedOrderEvent(bundle, "orphan-fill", "fill", 3, at.Add(15*time.Minute)),
			}
		}},
		{name: "duplicate signal", build: func(bundle StrategySnapshotBundle) []models.OrderEvent {
			at := bundle.OrderEvents[0].EventAt.Add(15 * time.Minute)
			return []models.OrderEvent{
				appendedOrderEvent(bundle, "signal-a", "signal", 2, at),
				appendedOrderEvent(bundle, "signal-b", "signal", 3, at.Add(time.Minute)),
			}
		}},
		{name: "duplicate fill", build: func(bundle StrategySnapshotBundle) []models.OrderEvent {
			at := bundle.OrderEvents[0].EventAt.Add(15 * time.Minute)
			orderAt := at.Add(15 * time.Minute)
			return []models.OrderEvent{
				appendedOrderEvent(bundle, "signal-fill-twice", "signal", 2, at),
				appendedOrderEvent(bundle, "order-fill-twice", "order", 3, orderAt),
				appendedOrderEvent(bundle, "fill-first", "fill", 4, orderAt),
				appendedOrderEvent(bundle, "fill-second", "fill", 5, orderAt.Add(time.Minute)),
			}
		}},
		{name: "same day exit violates T+1", build: func(bundle StrategySnapshotBundle) []models.OrderEvent {
			at := bundle.OrderEvents[0].EventAt.Add(15 * time.Minute)
			orderAt := at.Add(15 * time.Minute)
			exitAt := time.Date(2026, 8, 4, 14, 30, 0, 0, at.Location())
			return []models.OrderEvent{
				appendedOrderEvent(bundle, "signal-t0", "signal", 2, at),
				appendedOrderEvent(bundle, "order-t0", "order", 3, orderAt),
				appendedOrderEvent(bundle, "fill-t0", "fill", 4, orderAt),
				appendedOrderEvent(bundle, "exit-signal-t0", "exit_signal", 5, exitAt),
				appendedOrderEvent(bundle, "exit-fill-t0", "exit_fill", 6, exitAt),
			}
		}},
		{name: "signal outside trading session", build: func(bundle StrategySnapshotBundle) []models.OrderEvent {
			at := time.Date(2026, 8, 4, 12, 0, 0, 0, bundle.OrderEvents[0].EventAt.Location())
			return []models.OrderEvent{appendedOrderEvent(bundle, "lunch-signal", "signal", 2, at)}
		}},
		{name: "foreign rule ownership", build: func(bundle StrategySnapshotBundle) []models.OrderEvent {
			event := appendedOrderEvent(bundle, "foreign-rule-signal", "signal", 2, bundle.OrderEvents[0].EventAt.Add(15*time.Minute))
			event.RuleID = "foreign-rule"
			sealed := []models.OrderEvent{event}
			if err := SealStrategyOrderEvents(sealed); err != nil {
				panic(err)
			}
			return sealed
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			database := openStrategyPersistenceTestDB(t)
			bundle := frozenStrategyBundle()
			if err := AppendStrategySnapshotBundle(context.Background(), database, bundle); err != nil {
				t.Fatal(err)
			}
			events := tt.build(bundle)
			if err := AppendStrategyOrderEvents(context.Background(), database, bundle.Run.RunID, events); !errors.Is(err, ErrInvalidImmutableRecord) {
				t.Fatalf("state-machine error = %v", err)
			}
		})
	}
}

func TestAppendStrategyOrderEventsConcurrentSameSequenceOnlyOneWins(t *testing.T) {
	database := openStrategyPersistenceTestDB(t)
	bundle := frozenStrategyBundle()
	if err := AppendStrategySnapshotBundle(context.Background(), database, bundle); err != nil {
		t.Fatal(err)
	}
	at := *bundle.Run.ValidFromAt
	events := []models.OrderEvent{
		appendedOrderEvent(bundle, "concurrent-a", "signal", 2, at),
		appendedOrderEvent(bundle, "concurrent-b", "signal", 2, at),
	}
	start := make(chan struct{})
	errs := make([]error, len(events))
	var wg sync.WaitGroup
	for i := range events {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			errs[index] = AppendStrategyOrderEvents(context.Background(), database, bundle.Run.RunID, []models.OrderEvent{events[index]})
		}(i)
	}
	close(start)
	wg.Wait()
	successes := 0
	conflicts := 0
	for _, err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrImmutableConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent append error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent results: successes=%d conflicts=%d errors=%v", successes, conflicts, errs)
	}
	var count int64
	if err := database.Model(&models.OrderEvent{}).Where("run_id = ?", bundle.Run.RunID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("event count = %d, want initial+one winner", count)
	}
}

func TestPersistBacktestResultIsIdempotentAndRejectsConflict(t *testing.T) {
	database := openStrategyPersistenceTestDB(t)
	frozenAt := time.Date(2026, 8, 4, 15, 1, 0, 0, time.UTC)
	run := models.BacktestRun{
		BacktestID:      "bt-stable",
		StrategyVersion: "1.5.0",
		StartDate:       "2026-08-04",
		EndDate:         "2026-08-04",
		InputHash:       "input-hash",
		Status:          "completed",
		SummaryJSON:     `{"cacheOnly":true}`,
		StartedAt:       frozenAt,
		CompletedAt:     frozenAt,
		FrozenAt:        &frozenAt,
	}
	metric := models.Metric{
		MetricID:   "bt-stable:summary:trade_count",
		BacktestID: "bt-stable",
		Name:       "trade_count",
		Scope:      "summary",
		Value:      0,
		FrozenAt:   &frozenAt,
	}
	result := BacktestResult{Run: run, Metrics: []models.Metric{metric}}
	first, err := PersistBacktestResult(context.Background(), database, result)
	if err != nil {
		t.Fatal(err)
	}
	second, err := PersistBacktestResult(context.Background(), database, result)
	if err != nil {
		t.Fatalf("idempotent persist failed: %v", err)
	}
	if first.ID == 0 || first.ID != second.ID {
		t.Fatalf("idempotent IDs differ: %d != %d", first.ID, second.ID)
	}

	conflict := result
	conflict.Run.SummaryJSON = `{"cacheOnly":false}`
	_, err = PersistBacktestResult(context.Background(), database, conflict)
	if !errors.Is(err, ErrImmutableConflict) {
		t.Fatalf("error = %v, want ErrImmutableConflict", err)
	}

	var runCount, metricCount int64
	if err := database.Model(&models.BacktestRun{}).Count(&runCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Model(&models.Metric{}).Count(&metricCount).Error; err != nil {
		t.Fatal(err)
	}
	if runCount != 1 || metricCount != 1 {
		t.Fatalf("unexpected persisted counts: run=%d metric=%d", runCount, metricCount)
	}
}

func TestValidateBacktestTradeAccountingRejectsReconciliationError(t *testing.T) {
	entryAt := time.Date(2026, 8, 4, 7, 0, 0, 0, time.UTC)
	exitAt := entryAt.Add(24 * time.Hour)
	trade := models.Trade{
		Sequence:    1,
		EntryAt:     entryAt,
		ExitAt:      &exitAt,
		EntryPrice:  10,
		ExitPrice:   10.5,
		Quantity:    100,
		Fees:        11,
		GrossPnL:    50,
		NetPnL:      39,
		ReturnPct:   39.0 / 1005.0 * 100,
		PayloadJSON: `{"entryFees":5,"exitFees":6}`,
		FrozenAt:    &exitAt,
	}
	if err := validateBacktestTradeAccounting(trade); err != nil {
		t.Fatalf("valid accounting rejected: %v", err)
	}
	bad := trade
	bad.NetPnL++
	if err := validateBacktestTradeAccounting(bad); err == nil {
		t.Fatal("non-reconciling net PnL was accepted")
	}
	bad = trade
	bad.ReturnPct += 0.0101
	if err := validateBacktestTradeAccounting(bad); err == nil {
		t.Fatal("return error above 1bp was accepted")
	}
}

func TestValidateBacktestTradeAccountingReconcilesCorporateActionCashToOriginalCost(t *testing.T) {
	entryAt := time.Date(2026, 8, 4, 7, 0, 0, 0, time.UTC)
	exitAt := entryAt.Add(48 * time.Hour)
	trade := models.Trade{
		Sequence:    1,
		EntryAt:     entryAt,
		ExitAt:      &exitAt,
		EntryPrice:  5,
		ExitPrice:   5.5,
		Quantity:    200,
		Fees:        11,
		GrossPnL:    200,
		NetPnL:      189,
		ReturnPct:   189.0 / 1005.0 * 100,
		PayloadJSON: `{"status":"closed","entryFees":5,"exitFees":6,"entryCash":1005,"corporateActionCash":100,"adjustedEntryPrice":5}`,
		FrozenAt:    &exitAt,
	}
	if err := validateBacktestTradeAccounting(trade); err != nil {
		t.Fatalf("valid corporate-action accounting rejected: %v", err)
	}

	bad := trade
	bad.GrossPnL = 100 // old adjusted-price formula incorrectly ignored the dividend.
	bad.NetPnL = 89
	bad.ReturnPct = 89.0 / 1005.0 * 100
	if err := validateBacktestTradeAccounting(bad); err == nil {
		t.Fatal("corporate-action cash omission was accepted")
	}
}
