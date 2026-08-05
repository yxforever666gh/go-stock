package data

import (
	"strings"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
	"gorm.io/gorm"
)

func TestOrderMarketSummaryV150ScheduledStepsUsesEventTimeThenRankRule(t *testing.T) {
	loc := cnLocation()
	earlier := time.Date(2026, 8, 4, 10, 0, 0, 0, loc)
	later := earlier.Add(15 * time.Minute)

	rankTwoInverseSymbol := &marketSummaryV150ScheduledRecord{
		record: models.AiRecommendStocks{Model: gorm.Model{ID: 2}, StockCode: "000001.SZ"},
		rank:   2,
		ruleID: "rule-b",
	}
	rankOneHigherSymbol := &marketSummaryV150ScheduledRecord{
		record: models.AiRecommendStocks{Model: gorm.Model{ID: 1}, StockCode: "600000.SH"},
		rank:   1,
		ruleID: "rule-z",
	}
	laterRecord := &marketSummaryV150ScheduledRecord{
		record: models.AiRecommendStocks{Model: gorm.Model{ID: 3}, StockCode: "000002.SZ"},
		rank:   1,
		ruleID: "rule-a",
	}

	// Deliberately provide both event times and same-bar symbols in reverse
	// order. Symbol lexical order must never outrank event time or frozen rank.
	steps := orderMarketSummaryV150ScheduledSteps(
		[]time.Time{later, earlier},
		map[int64][]*marketSummaryV150ScheduledRecord{
			earlier.UnixNano(): {rankTwoInverseSymbol, rankOneHigherSymbol},
			later.UnixNano():   {laterRecord},
		},
	)
	if len(steps) != 3 {
		t.Fatalf("steps=%d, want 3", len(steps))
	}
	if !steps[0].at.Equal(earlier) || steps[0].record != rankOneHigherSymbol {
		t.Fatalf("first step=%+v, want earlier event rank 1", steps[0])
	}
	if !steps[1].at.Equal(earlier) || steps[1].record != rankTwoInverseSymbol {
		t.Fatalf("second step=%+v, want earlier event rank 2", steps[1])
	}
	if !steps[2].at.Equal(later) || steps[2].record != laterRecord {
		t.Fatalf("third step=%+v, want later event regardless of symbol", steps[2])
	}
}

func TestSplitAiRecommendYieldCalcTasksKeepsLegacyParallelPathSeparate(t *testing.T) {
	v150Record := models.AiRecommendStocks{Model: gorm.Model{ID: 15}, StockCode: "600000.SH", SummaryVersion: marketSummaryVersion150, StrategyRuleID: "v150-rule"}
	legacyRecord := models.AiRecommendStocks{Model: gorm.Model{ID: 14}, StockCode: "000001.SZ", SummaryVersion: marketSummaryVersion142}
	task := aiRecommendYieldCalcTask{
		StockCode:      "mixed",
		Records:        []models.AiRecommendStocks{v150Record, legacyRecord},
		ExistingRecord: map[uint]*models.AiRecommendYieldRecordState{},
		Aggregate:      &aiRecommendYieldAggregate{StockCode: "mixed"},
	}

	legacy, scheduled := splitAiRecommendYieldCalcTasksByVersion([]aiRecommendYieldCalcTask{task})
	if len(legacy) != 1 || len(legacy[0].Records) != 1 || legacy[0].Records[0].ID != legacyRecord.ID {
		t.Fatalf("legacy worker path changed: %+v", legacy)
	}
	if len(scheduled) != 1 || scheduled[0].record.ID != v150Record.ID || scheduled[0].ruleID != v150Record.StrategyRuleID {
		t.Fatalf("V1.5 record was not isolated for serial replay: %+v", scheduled)
	}
}

func TestProcessMarketSummaryV150RecordsObservesOnlyCurrentExecutionDay(t *testing.T) {
	t.Run("online current day is observed before checkpoints", func(t *testing.T) {
		loc := cnLocation()
		wallClockNow := time.Date(2026, 8, 5, 9, 20, 0, 0, loc)
		decision := time.Date(2026, 8, 5, 9, 0, 0, 0, loc)
		validFrom := time.Date(2026, 8, 5, 13, 0, 0, 0, loc)
		rec := seedMarketSummaryV150ExecutionFixture(t, decision, marketSummaryV150TestBreakoutPlan(validFrom))
		rec.Model = gorm.Model{ID: 1}

		previousNow := marketSummaryV150ExecutionSecurityNow
		previousFetch := fetchMarketSummaryV150ExecutionSecurityFactFn
		marketSummaryV150ExecutionSecurityNow = func() time.Time { return wallClockNow }
		fetchCalls := 0
		fetchMarketSummaryV150ExecutionSecurityFactFn = func(symbol string, observedAt time.Time) (marketSummaryV150ExecutionSecurityFact, error) {
			fetchCalls++
			return marketSummaryV150ExecutionSecurityFact{
				Symbol: symbol, Name: "test", Market: "SH", Board: "MAIN", Currency: "CNY", Status: "L", ListStatus: "L",
				Source: "test_realtime_quote", SourceAt: observedAt.Add(-time.Minute),
			}, nil
		}
		t.Cleanup(func() {
			marketSummaryV150ExecutionSecurityNow = previousNow
			fetchMarketSummaryV150ExecutionSecurityFactFn = previousFetch
		})

		records := []*marketSummaryV150ScheduledRecord{{record: rec, ruleID: rec.StrategyRuleID}}
		ctx := yieldBuildContext{Now: wallClockNow, LatestTradeDate: wallClockNow, InTradingSession: false}
		if err := processMarketSummaryV150RecordsInEventOrder(records, ctx, newAiRecommendYieldSnapshotWriter(0, 10)); err != nil {
			t.Fatal(err)
		}
		if fetchCalls != 1 {
			t.Fatalf("current online round fetched security %d times, want once before checkpoints", fetchCalls)
		}
		state, err := loadMarketSummaryV150ExecutionObservationState(rec.StrategyRunID, rec.StockCode, wallClockNow.Add(time.Second))
		if err != nil || !state.Tradable {
			t.Fatalf("current-day observation state=%+v err=%v", state, err)
		}
	})

	t.Run("historical checkpoint is never backdated", func(t *testing.T) {
		loc := cnLocation()
		wallClockNow := time.Date(2026, 8, 5, 9, 20, 0, 0, loc)
		historicalNow := time.Date(2026, 8, 4, 9, 20, 0, 0, loc)
		decision := time.Date(2026, 8, 4, 9, 0, 0, 0, loc)
		validFrom := time.Date(2026, 8, 4, 13, 0, 0, 0, loc)
		rec := seedMarketSummaryV150ExecutionFixture(t, decision, marketSummaryV150TestBreakoutPlan(validFrom))
		rec.Model = gorm.Model{ID: 1}

		previousNow := marketSummaryV150ExecutionSecurityNow
		previousFetch := fetchMarketSummaryV150ExecutionSecurityFactFn
		marketSummaryV150ExecutionSecurityNow = func() time.Time { return wallClockNow }
		fetchCalls := 0
		fetchMarketSummaryV150ExecutionSecurityFactFn = func(symbol string, observedAt time.Time) (marketSummaryV150ExecutionSecurityFact, error) {
			fetchCalls++
			return marketSummaryV150ExecutionSecurityFact{}, nil
		}
		t.Cleanup(func() {
			marketSummaryV150ExecutionSecurityNow = previousNow
			fetchMarketSummaryV150ExecutionSecurityFactFn = previousFetch
		})

		records := []*marketSummaryV150ScheduledRecord{{record: rec, ruleID: rec.StrategyRuleID}}
		ctx := yieldBuildContext{Now: historicalNow, LatestTradeDate: historicalNow, InTradingSession: false}
		if err := processMarketSummaryV150RecordsInEventOrder(records, ctx, newAiRecommendYieldSnapshotWriter(0, 10)); err != nil {
			t.Fatal(err)
		}
		if fetchCalls != 0 {
			t.Fatalf("historical replay fetched %d security observations", fetchCalls)
		}
		var observations int64
		if err := db.Dao.Model(&models.StrategyRunSnapshot{}).
			Where("mode = ?", marketSummaryV150ExecutionSecurityObservationMode).
			Count(&observations).Error; err != nil {
			t.Fatal(err)
		}
		if observations != 0 {
			t.Fatalf("historical replay backdated %d execution observations", observations)
		}
		if _, err := loadMarketSummaryV150ExecutionObservationState(rec.StrategyRunID, rec.StockCode, historicalNow); err == nil {
			t.Fatal("historical checkpoint without a frozen observation did not fail closed")
		}
	})
}

func TestProcessMarketSummaryV150RecordsReplaysEarlierEventBeforeInverseSymbol(t *testing.T) {
	loc := cnLocation()
	decision := time.Date(2026, 8, 4, 9, 0, 0, 0, loc)
	validFrom := time.Date(2026, 8, 4, 9, 30, 0, 0, loc)
	initMarketSummaryV150ExecutionTestDB(t)

	earlierPlan := marketSummaryV150TestBreakoutPlan(validFrom)
	earlier := appendMarketSummaryV150ExecutionFixtureWithSecurity(t, decision, earlierPlan, true)
	earlier.Model = gorm.Model{ID: 2}
	seedMarketSummaryV150BreakoutBars(t, earlier, decision, validFrom)

	laterPlan := earlierPlan
	laterPlan.Symbol = "000001.SZ" // Lexically first, but its crossing is later.
	later := appendMarketSummaryV150ExecutionFixtureWithSecurity(t, decision, laterPlan, true)
	later.Model = gorm.Model{ID: 1}
	seedMarketSummaryV150DailyClose(t, later.StockCode, decision.AddDate(0, 0, -1), 10)
	prior := validFrom.AddDate(0, 0, -1)
	seedMarketSummaryV150Minutes(t, later.StockCode, marketSummaryV150TestMinuteBucket(prior.Add(15*time.Minute), 9.8, 9.9, 100, false))
	seedMarketSummaryV150Minutes(t, later.StockCode, marketSummaryV150TestMinuteBucket(prior.Add(30*time.Minute), 9.8, 9.9, 100, false))
	seedMarketSummaryV150Minutes(t, later.StockCode, marketSummaryV150TestMinuteBucket(validFrom, 9.9, 9.95, 100, false))
	seedMarketSummaryV150Minutes(t, later.StockCode, marketSummaryV150TestMinuteBucket(validFrom.Add(15*time.Minute), 9.95, 9.98, 100, false))
	seedMarketSummaryV150Minutes(t, later.StockCode, marketSummaryV150TestMinuteBucket(validFrom.Add(30*time.Minute), 9.98, 10.10, 150, false))
	seedMarketSummaryV150Minutes(t, later.StockCode, marketSummaryV150TestMinuteBucket(validFrom.Add(45*time.Minute), 10.05, 10.08, 100, false))

	records := []*marketSummaryV150ScheduledRecord{
		{record: later, rank: 1, ruleID: later.StrategyRuleID},
		{record: earlier, rank: 1, ruleID: earlier.StrategyRuleID},
	}
	ctx := yieldBuildContext{
		Now:                validFrom.Add(time.Hour),
		InTradingSession:   true,
		LatestTradeDate:    decision,
		DisableMinuteFetch: true,
		Force:              true,
	}
	writer := newAiRecommendYieldSnapshotWriter(0, 100)
	if err := processMarketSummaryV150RecordsInEventOrder(records, ctx, writer); err != nil {
		t.Fatal(err)
	}

	var fills []models.OrderEvent
	if err := db.Dao.Where("event_type = ?", "fill").Order("event_at ASC, rule_id ASC").Find(&fills).Error; err != nil {
		t.Fatal(err)
	}
	if len(fills) != 1 || fills[0].Symbol != earlier.StockCode {
		var allEvents []models.OrderEvent
		if err := db.Dao.Order("event_at ASC, rule_id ASC, sequence ASC").Find(&allEvents).Error; err != nil {
			t.Fatal(err)
		}
		t.Fatalf("fills=%+v, want earlier-event %s despite inverse symbol order; events=%+v; earlier_state=%+v later_state=%+v; earlier_checkpoints=%v later_checkpoints=%v",
			fills, earlier.StockCode, allEvents, records[1].runtimeState, records[0].runtimeState, records[1].checkpoints, records[0].checkpoints)
	}
	var laterEvents []models.OrderEvent
	if err := db.Dao.Where("rule_id = ?", later.StrategyRuleID).Order("sequence ASC").Find(&laterEvents).Error; err != nil {
		t.Fatal(err)
	}
	rejectedForSector := false
	for _, event := range laterEvents {
		if event.EventType == "reject" && strings.Contains(event.Reason, "sector_daily_limit") {
			rejectedForSector = true
		}
	}
	if !rejectedForSector {
		t.Fatalf("later lexical symbol was not rejected from the already-consumed sector: %+v", laterEvents)
	}
}
