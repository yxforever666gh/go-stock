package data

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"go-stock/backend/models"
	"go-stock/backend/persistence"
	"go-stock/backend/portfolio"
	"go-stock/backend/strategy/v150"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestCompatibilityCurrentRecommendationReaderKeepsFrozenRuleWithoutProjection(t *testing.T) {
	database := openCompatibilityCurrentRecommendationTestDB(t)
	fixture := seedCompatibilityCurrentRecommendationHolding(t, database)

	got, err := NewCompatibilityCurrentRecommendationReader(database).List(context.Background(), fixture.query(fixture.afterFill))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("recommendations = %d, want 1", len(got))
	}
	if got[0].Display != nil {
		t.Fatalf("display = %+v, want nil", got[0].Display)
	}
	if got[0].Frozen.RunID != fixture.bundle.Run.RunID || got[0].Frozen.RuleID != fixture.bundle.Rules[0].RuleID ||
		got[0].Frozen.CandidateID != fixture.bundle.Candidates[0].CandidateID || got[0].Frozen.Symbol != fixture.bundle.Rules[0].Symbol {
		t.Fatalf("frozen identity = %+v", got[0].Frozen)
	}
	if got[0].Lifecycle.Status != portfolio.RecommendationHolding || got[0].Lifecycle.EntryPrice != fixture.fillPrice ||
		got[0].Lifecycle.EntryQuantity != float64(fixture.fillQuantity) {
		t.Fatalf("lifecycle = %+v, want sealed holding", got[0].Lifecycle)
	}
}

func TestCompatibilityCurrentRecommendationReaderIgnoresForgedProjectionIdentity(t *testing.T) {
	database := openCompatibilityCurrentRecommendationTestDB(t)
	fixture := seedCompatibilityCurrentRecommendationHolding(t, database)
	createCompatibilityProjection(t, database, fixture, func(row *models.AiRecommendStocks) {
		row.StockCode = "600000.SH"
		row.ActivationStatus = "activated"
		row.ExecutionState = "holding"
	})

	got, err := NewCompatibilityCurrentRecommendationReader(database).List(context.Background(), fixture.query(fixture.afterFill))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Display != nil {
		t.Fatalf("recommendations = %+v, want one frozen row without forged display", got)
	}
	if got[0].Frozen.Symbol != fixture.bundle.Rules[0].Symbol || got[0].Lifecycle.Status != portfolio.RecommendationHolding {
		t.Fatalf("forged projection changed identity/lifecycle: %+v", got[0])
	}
}

func TestCompatibilityCurrentRecommendationReaderOmitsAmbiguousDuplicateProjection(t *testing.T) {
	database := openCompatibilityCurrentRecommendationTestDB(t)
	fixture := seedCompatibilityCurrentRecommendationHolding(t, database)
	createCompatibilityProjection(t, database, fixture, nil)
	createCompatibilityProjection(t, database, fixture, func(row *models.AiRecommendStocks) {
		row.ProviderName = "duplicate-provider"
	})

	got, err := NewCompatibilityCurrentRecommendationReader(database).List(context.Background(), fixture.query(fixture.afterFill))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("recommendations = %d, want 1", len(got))
	}
	if got[0].Display != nil {
		t.Fatalf("ambiguous display = %+v, want nil", got[0].Display)
	}
}

func TestCompatibilityCurrentRecommendationReaderProjectionStateCannotOverrideFill(t *testing.T) {
	database := openCompatibilityCurrentRecommendationTestDB(t)
	fixture := seedCompatibilityCurrentRecommendationHolding(t, database)
	createCompatibilityProjection(t, database, fixture, func(row *models.AiRecommendStocks) {
		row.ActivationStatus = "pending"
		row.ExecutionState = "unactivated"
		row.RecommendStatus = "analysis_only"
	})

	got, err := NewCompatibilityCurrentRecommendationReader(database).List(context.Background(), fixture.query(fixture.afterFill))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Display == nil || got[0].Display.Provider != "projection-provider" {
		t.Fatalf("display = %+v, want the unique matching optional projection", got)
	}
	if got[0].Lifecycle.Status != portfolio.RecommendationHolding || got[0].Lifecycle.EntryAt == nil {
		t.Fatalf("projection state overrode ledger fill: %+v", got[0].Lifecycle)
	}
}

func TestCompatibilityCurrentRecommendationReaderAppliesSnapshotAndEventAsOf(t *testing.T) {
	database := openCompatibilityCurrentRecommendationTestDB(t)
	fixture := seedCompatibilityCurrentRecommendationHolding(t, database)
	reader := NewCompatibilityCurrentRecommendationReader(database)

	beforeSnapshot, err := reader.List(context.Background(), fixture.query(fixture.bundle.Run.FrozenAt.Add(-time.Nanosecond)))
	if err != nil {
		t.Fatal(err)
	}
	if len(beforeSnapshot) != 0 {
		t.Fatalf("snapshot visible before frozenAt: %+v", beforeSnapshot)
	}

	beforeFill, err := reader.List(context.Background(), fixture.query(fixture.fillFrozenAt.Add(-time.Nanosecond)))
	if err != nil {
		t.Fatal(err)
	}
	if len(beforeFill) != 1 || beforeFill[0].Lifecycle.Status != portfolio.RecommendationOrdered || beforeFill[0].Lifecycle.EntryAt != nil {
		t.Fatalf("fill visible before its frozenAt: %+v", beforeFill)
	}

	afterFill, err := reader.List(context.Background(), fixture.query(fixture.afterFill))
	if err != nil {
		t.Fatal(err)
	}
	if len(afterFill) != 1 || afterFill[0].Lifecycle.Status != portfolio.RecommendationHolding {
		t.Fatalf("sealed fill missing after frozenAt: %+v", afterFill)
	}
}

func TestCompatibilityCurrentRecommendationReaderNoFrozenSnapshotsIsEmpty(t *testing.T) {
	database := openCompatibilityCurrentRecommendationTestDB(t)
	zone := time.FixedZone("Asia/Shanghai", 8*60*60)
	day := time.Date(2026, 8, 6, 0, 0, 0, 0, zone)
	got, err := NewCompatibilityCurrentRecommendationReader(database).List(context.Background(), portfolio.RecommendationQuery{
		StrategyVersion: v150.StrategyVersion,
		Start:           day,
		End:             day,
		AsOf:            day.Add(23 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("recommendations = %#v, want non-nil empty list", got)
	}
}

func TestCompatibilityCurrentRecommendationReaderEnumeratesEntryRulesOnly(t *testing.T) {
	database := openCompatibilityCurrentRecommendationTestDB(t)
	bundle := compatibilityCurrentRecommendationBundle(t)
	nonEntry := bundle.Rules[0]
	nonEntry.RuleID = "current-rule-exit"
	nonEntry.RuleType = "exit"
	nonEntry.Path = "time_exit"
	nonEntry.SnapshotHash = ""
	bundle.Rules = append(bundle.Rules, nonEntry)
	nonEntryIssued := bundle.OrderEvents[0]
	nonEntryIssued.EventID = "current-event-exit-issued"
	nonEntryIssued.RuleID = nonEntry.RuleID
	nonEntryIssued.SnapshotHash = ""
	bundle.OrderEvents = append(bundle.OrderEvents, nonEntryIssued)
	sealAndAppendCompatibilityBundle(t, database, &bundle)

	got, err := NewCompatibilityCurrentRecommendationReader(database).List(
		context.Background(),
		compatibilityQueryForBundle(bundle, bundle.Run.FrozenAt.Add(time.Hour)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Frozen.RuleID != bundle.Rules[0].RuleID {
		t.Fatalf("recommendations = %+v, want only frozen entry rule", got)
	}
}

func TestCompatibilityCurrentRecommendationReaderRejectsConfigAndFrozenIdentityMismatch(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*persistence.StrategySnapshotBundle)
	}{
		{name: "config hash", mutate: func(bundle *persistence.StrategySnapshotBundle) {
			bundle.Run.ConfigHash = "forged-config"
		}},
		{name: "candidate identity", mutate: func(bundle *persistence.StrategySnapshotBundle) {
			bundle.Rules[0].CandidateID = "missing-candidate"
		}},
		{name: "candidate symbol", mutate: func(bundle *persistence.StrategySnapshotBundle) {
			bundle.Candidates[0].Symbol = "600000.SH"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database := openCompatibilityCurrentRecommendationTestDB(t)
			bundle := compatibilityCurrentRecommendationBundle(t)
			test.mutate(&bundle)
			sealAndAppendCompatibilityBundle(t, database, &bundle)

			_, err := NewCompatibilityCurrentRecommendationReader(database).List(context.Background(), compatibilityQueryForBundle(bundle, bundle.Run.FrozenAt.Add(time.Hour)))
			if !errors.Is(err, portfolio.ErrInvalidFrozenRecommendation) {
				t.Fatalf("error = %v, want ErrInvalidFrozenRecommendation", err)
			}
		})
	}
}

func TestCompatibilityCurrentRecommendationReaderFailsClosedOnBrokenSealsAndSequence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *gorm.DB, compatibilityCurrentRecommendationFixture)
		want   error
	}{
		{name: "candidate seal", mutate: func(t *testing.T, database *gorm.DB, fixture compatibilityCurrentRecommendationFixture) {
			if err := database.Model(&models.CandidateSnapshot{}).
				Where("candidate_id = ?", fixture.bundle.Candidates[0].CandidateID).
				UpdateColumn("name", "tampered-name").Error; err != nil {
				t.Fatal(err)
			}
		}, want: persistence.ErrIncompleteSnapshots},
		{name: "event seal", mutate: func(t *testing.T, database *gorm.DB, fixture compatibilityCurrentRecommendationFixture) {
			if err := database.Model(&models.OrderEvent{}).
				Where("event_id = ?", fixture.fillEventID).
				UpdateColumn("reason", "tampered-reason").Error; err != nil {
				t.Fatal(err)
			}
		}, want: persistence.ErrIncompleteSnapshots},
		{name: "sequence gap", mutate: func(t *testing.T, database *gorm.DB, fixture compatibilityCurrentRecommendationFixture) {
			var row models.OrderEvent
			if err := database.Where("event_id = ?", fixture.fillEventID).First(&row).Error; err != nil {
				t.Fatal(err)
			}
			row.Sequence = 5
			row.SnapshotHash = ""
			sealed := []models.OrderEvent{row}
			if err := persistence.SealStrategyOrderEvents(sealed); err != nil {
				t.Fatal(err)
			}
			if err := database.Model(&models.OrderEvent{}).
				Where("id = ?", row.ID).
				Updates(map[string]any{"sequence": sealed[0].Sequence, "snapshot_hash": sealed[0].SnapshotHash}).Error; err != nil {
				t.Fatal(err)
			}
		}, want: portfolio.ErrInvalidRecommendationLedger},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database := openCompatibilityCurrentRecommendationTestDB(t)
			fixture := seedCompatibilityCurrentRecommendationHolding(t, database)
			test.mutate(t, database, fixture)

			_, err := NewCompatibilityCurrentRecommendationReader(database).List(context.Background(), fixture.query(fixture.afterFill))
			if err == nil {
				t.Fatal("expected fail-closed error")
			}
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestCompatibilityCurrentRecommendationReaderReadsNoMutableYieldTables(t *testing.T) {
	database := openCompatibilityCurrentRecommendationTestDB(t)
	fixture := seedCompatibilityCurrentRecommendationHolding(t, database)
	createCompatibilityProjection(t, database, fixture, nil)

	var mu sync.Mutex
	reads := make(map[string]int)
	callbackName := "test:current-recommendation-read-tables"
	if err := database.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		table := tx.Statement.Table
		if table == "" && tx.Statement.Schema != nil {
			table = tx.Statement.Schema.Table
		}
		mu.Lock()
		reads[table]++
		mu.Unlock()
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Callback().Query().Remove(callbackName) })

	got, err := NewCompatibilityCurrentRecommendationReader(database).List(context.Background(), fixture.query(fixture.afterFill))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("recommendations = %d, want 1", len(got))
	}
	mu.Lock()
	defer mu.Unlock()
	for _, table := range []string{
		"ai_recommend_yield_record_state",
		"ai_recommend_yield_state",
		"ai_recommend_yield_override",
	} {
		if reads[table] != 0 {
			t.Fatalf("mutable projection table %s read %d times", table, reads[table])
		}
	}
	for _, table := range []string{"strategy_run_snapshot", "strategy_rule_snapshot", "strategy_order_event", "ai_recommend_stocks"} {
		if reads[table] == 0 {
			t.Fatalf("query callback did not observe required source table %s; reads=%v", table, reads)
		}
	}
}

func TestCompatibilityCurrentRecommendationReaderRequiresExactV150Query(t *testing.T) {
	database := openCompatibilityCurrentRecommendationTestDB(t)
	zone := time.FixedZone("Asia/Shanghai", 8*60*60)
	day := time.Date(2026, 8, 7, 0, 0, 0, 0, zone)
	for _, version := range []string{"", "current", "1.4.2", " 1.5.0 "} {
		_, err := NewCompatibilityCurrentRecommendationReader(database).List(context.Background(), portfolio.RecommendationQuery{
			StrategyVersion: version,
			Start:           day,
			End:             day,
			AsOf:            day.Add(12 * time.Hour),
		})
		if !errors.Is(err, portfolio.ErrInvalidRecommendationQuery) {
			t.Fatalf("version %q error = %v, want ErrInvalidRecommendationQuery", version, err)
		}
	}
}

type compatibilityCurrentRecommendationFixture struct {
	bundle       persistence.StrategySnapshotBundle
	fillPrice    float64
	fillQuantity int
	fillFrozenAt time.Time
	fillEventID  string
	afterFill    time.Time
}

func (fixture compatibilityCurrentRecommendationFixture) query(asOf time.Time) portfolio.RecommendationQuery {
	return compatibilityQueryForBundle(fixture.bundle, asOf)
}

func compatibilityQueryForBundle(bundle persistence.StrategySnapshotBundle, asOf time.Time) portfolio.RecommendationQuery {
	day, err := time.ParseInLocation(time.DateOnly, bundle.Run.TradeDate, bundle.Run.DecisionAt.Location())
	if err != nil {
		panic(err)
	}
	return portfolio.RecommendationQuery{
		StrategyVersion: v150.StrategyVersion,
		Start:           day,
		End:             day,
		AsOf:            asOf,
	}
}

func openCompatibilityCurrentRecommendationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:current-recommendation-%s?mode=memory&cache=shared", strings.NewReplacer("/", "-", " ", "-").Replace(t.Name()))
	database, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	allModels := append(models.StrategyPersistenceModels(),
		&models.AiRecommendStocks{},
		&models.AiRecommendYieldRecordState{},
		&models.AiRecommendYieldState{},
		&models.AiRecommendYieldOverride{},
	)
	if err := database.AutoMigrate(allModels...); err != nil {
		t.Fatal(err)
	}
	return database
}

func compatibilityCurrentRecommendationBundle(t *testing.T) persistence.StrategySnapshotBundle {
	t.Helper()
	zone := time.FixedZone("Asia/Shanghai", 8*60*60)
	startedAt := time.Date(2026, 8, 7, 9, 0, 0, 0, zone)
	dataCutoffAt := time.Date(2026, 8, 7, 9, 10, 0, 0, zone)
	decisionAt := time.Date(2026, 8, 7, 9, 15, 0, 0, zone)
	generatedAt := time.Date(2026, 8, 7, 9, 16, 0, 0, zone)
	frozenAt := time.Date(2026, 8, 7, 9, 17, 0, 0, zone)
	validFromAt := time.Date(2026, 8, 7, 9, 30, 0, 0, zone)
	bundle := persistence.StrategySnapshotBundle{
		Run: models.StrategyRunSnapshot{
			RunID: "current-run-1", StrategyVersion: v150.StrategyVersion, TradeDate: "2026-08-07", RunSlot: "open",
			StartedAt: startedAt, AsOf: dataCutoffAt, DataCutoffAt: dataCutoffAt, DecisionAt: decisionAt,
			GeneratedAt: generatedAt, ValidFromAt: &validFromAt, Mode: "neutral",
			ConfigHash: v150.FixedStrategyV150ConfigHash(), InputHash: "input-hash", PayloadJSON: `{}`, FrozenAt: &frozenAt,
		},
		Candidates: []models.CandidateSnapshot{{
			CandidateID: "current-candidate-1", RunID: "current-run-1", StrategyVersion: v150.StrategyVersion,
			TradeDate: "2026-08-07", Symbol: "000001.SZ", Name: "sample", Sector: "bank",
			Rank: 1, FinalRank: 1, Decision: "selected", Score: 88, Eligible: true, PayloadJSON: `{}`, FrozenAt: &frozenAt,
		}},
		Rules: []models.RuleSnapshot{{
			RuleID: "current-rule-1", RunID: "current-run-1", CandidateID: "current-candidate-1",
			StrategyVersion: v150.StrategyVersion, TradeDate: "2026-08-07", Symbol: "000001.SZ",
			RuleVersion: v150.StrategyVersion, RuleType: "entry", Path: "pullback", ValidFromAt: validFromAt,
			PayloadJSON: `{}`, FrozenAt: &frozenAt,
		}},
		OrderEvents: []models.OrderEvent{{
			EventID: "current-event-issued", RunID: "current-run-1", RuleID: "current-rule-1",
			StrategyVersion: v150.StrategyVersion, TradeDate: "2026-08-07", Symbol: "000001.SZ",
			EventType: "rule_issued", Sequence: 1, EventAt: decisionAt, Reason: "published", PayloadJSON: `{}`, FrozenAt: &frozenAt,
		}},
	}
	return bundle
}

func sealAndAppendCompatibilityBundle(t *testing.T, database *gorm.DB, bundle *persistence.StrategySnapshotBundle) {
	t.Helper()
	if err := persistence.SealStrategySnapshotBundle(bundle); err != nil {
		t.Fatal(err)
	}
	if err := persistence.AppendStrategySnapshotBundle(context.Background(), database, *bundle); err != nil {
		t.Fatal(err)
	}
}

func seedCompatibilityCurrentRecommendationHolding(t *testing.T, database *gorm.DB) compatibilityCurrentRecommendationFixture {
	t.Helper()
	bundle := compatibilityCurrentRecommendationBundle(t)
	sealAndAppendCompatibilityBundle(t, database, &bundle)

	zone := time.FixedZone("Asia/Shanghai", 8*60*60)
	signalAt := time.Date(2026, 8, 7, 9, 45, 0, 0, zone)
	orderAt := time.Date(2026, 8, 7, 10, 0, 0, 0, zone)
	fillAt := orderAt
	signalFrozenAt := signalAt.Add(30 * time.Second)
	orderFrozenAt := orderAt.Add(30 * time.Second)
	fillFrozenAt := fillAt.Add(90 * time.Second)
	fillPrice := 10.0
	cfg := v150.FixedStrategyV150Config()
	fillQuantity := v150.SizeRoundLot(fillPrice, cfg.TargetCashPerPosition, cfg).Quantity
	rawPrice := fillPrice / (1 + cfg.BaseSlippageBPS/10_000)
	cost := v150.CalculateTradeCost(v150.SideBuy, v150.MarketSZ, rawPrice, fillQuantity, cfg.SlippageScenarios()[0], cfg)
	fillFees := cost.Commission + cost.TransferFee + cost.StampDuty
	events := []models.OrderEvent{
		{
			EventID: "current-event-signal", RunID: bundle.Run.RunID, RuleID: bundle.Rules[0].RuleID,
			StrategyVersion: v150.StrategyVersion, TradeDate: bundle.Run.TradeDate, Symbol: bundle.Rules[0].Symbol,
			EventType: "signal", Sequence: 2, EventAt: signalAt, Price: fillPrice, Reason: "pullback", PayloadJSON: `{}`, FrozenAt: &signalFrozenAt,
		},
		{
			EventID: "current-event-order", RunID: bundle.Run.RunID, RuleID: bundle.Rules[0].RuleID,
			StrategyVersion: v150.StrategyVersion, TradeDate: bundle.Run.TradeDate, Symbol: bundle.Rules[0].Symbol,
			EventType: "order", Sequence: 3, EventAt: orderAt, Quantity: float64(fillQuantity), Reason: "next_bar", PayloadJSON: `{}`, FrozenAt: &orderFrozenAt,
		},
		{
			EventID: "current-event-fill", RunID: bundle.Run.RunID, RuleID: bundle.Rules[0].RuleID,
			StrategyVersion: v150.StrategyVersion, TradeDate: bundle.Run.TradeDate, Symbol: bundle.Rules[0].Symbol,
			EventType: "fill", Sequence: 4, EventAt: fillAt, Price: fillPrice, Quantity: float64(fillQuantity),
			Fees: fillFees, Reason: "filled", PayloadJSON: `{}`, FrozenAt: &fillFrozenAt,
		},
	}
	if err := persistence.SealStrategyOrderEvents(events); err != nil {
		t.Fatal(err)
	}
	if err := persistence.AppendStrategyOrderEvents(context.Background(), database, bundle.Run.RunID, events); err != nil {
		t.Fatal(err)
	}
	return compatibilityCurrentRecommendationFixture{
		bundle: bundle, fillPrice: fillPrice, fillQuantity: fillQuantity,
		fillFrozenAt: fillFrozenAt, fillEventID: events[2].EventID, afterFill: fillFrozenAt.Add(time.Hour),
	}
}

func createCompatibilityProjection(
	t *testing.T,
	database *gorm.DB,
	fixture compatibilityCurrentRecommendationFixture,
	mutate func(*models.AiRecommendStocks),
) {
	t.Helper()
	decisionAt := fixture.bundle.Run.DecisionAt
	row := models.AiRecommendStocks{
		DataTime: &decisionAt, ProviderName: "projection-provider", ModelName: "projection-model",
		StockCode: fixture.bundle.Rules[0].Symbol, StockName: "projection-name",
		SummaryVersion: v150.StrategyVersion, StrategyRunID: fixture.bundle.Run.RunID,
		StrategyRuleID: fixture.bundle.Rules[0].RuleID,
	}
	if mutate != nil {
		mutate(&row)
	}
	if err := database.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
}
