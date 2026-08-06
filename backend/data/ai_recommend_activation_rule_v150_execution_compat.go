package data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/execution"
	"go-stock/backend/models"
	"go-stock/backend/persistence"
	"go-stock/backend/strategy/v150"
)

type marketSummaryV150OrderEventStore = execution.ImmutableOrderEventStore[models.OrderEvent]

type marketSummaryV150OrderEventSink struct {
	ctx   context.Context
	store marketSummaryV150OrderEventStore
}

func newMarketSummaryV150OrderEventSink(ctx context.Context, store marketSummaryV150OrderEventStore) marketSummaryV150OrderEventSink {
	if ctx == nil {
		ctx = context.Background()
	}
	return marketSummaryV150OrderEventSink{ctx: ctx, store: store}
}

func (sink marketSummaryV150OrderEventSink) injected() bool {
	return sink.store != nil
}

func appendMarketSummaryV150OrderEventsWithSink(
	sink marketSummaryV150OrderEventSink,
	rec models.AiRecommendStocks,
	run models.StrategyRunSnapshot,
	source []v150.OrderEvent,
	accounting marketSummaryV150EventAccounting,
) error {
	if sink.injected() {
		return appendMarketSummaryV150OrderEventsWithStore(sink.ctx, sink.store, rec, run, source, accounting)
	}
	// TODO(app-1.5.3): yield recalculation still enters through the legacy
	// compatibility wrapper. Remove this fallback when that producer receives
	// the composition-root store in the next migration slice.
	return appendMarketSummaryV150OrderEvents(rec, run, source, accounting)
}

func (ctx yieldBuildContext) appendMarketSummaryV150OrderEvents(
	rec models.AiRecommendStocks,
	run models.StrategyRunSnapshot,
	source []v150.OrderEvent,
	accounting marketSummaryV150EventAccounting,
) error {
	return appendMarketSummaryV150OrderEventsWithSink(ctx.V150OrderEventSink, rec, run, source, accounting)
}

func appendMarketSummaryV150OrderEventsWithStore(
	ctx context.Context,
	store execution.ImmutableOrderEventStore[models.OrderEvent],
	rec models.AiRecommendStocks,
	run models.StrategyRunSnapshot,
	source []v150.OrderEvent,
	accounting marketSummaryV150EventAccounting,
) error {
	if len(source) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := requireStrategyProductionLive(ctx, db.Dao); err != nil {
		return err
	}
	if db.Dao == nil {
		return errors.New("strategy database is unavailable")
	}
	if store == nil {
		return errors.New("strategy order event store is unavailable")
	}
	runID := strings.TrimSpace(rec.StrategyRunID)
	ruleID := strings.TrimSpace(rec.StrategyRuleID)
	if runID == "" || ruleID == "" || run.RunID != runID {
		return errors.New("strategy event identity is incomplete")
	}

	ordered := append([]v150.OrderEvent(nil), source...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if !ordered[i].At.Equal(ordered[j].At) {
			return ordered[i].At.Before(ordered[j].At)
		}
		return marketSummaryV150EventTypeOrder(ordered[i].Type) < marketSummaryV150EventTypeOrder(ordered[j].Type)
	})
	var existing []models.OrderEvent
	if err := db.Dao.Where("run_id = ? AND rule_id = ?", runID, ruleID).Order("sequence ASC, event_id ASC").Find(&existing).Error; err != nil {
		return err
	}
	lastSequence := 0
	var lastAt time.Time
	for _, row := range existing {
		if row.RuleID != ruleID {
			continue
		}
		if row.Sequence > lastSequence || (row.Sequence == lastSequence && row.EventAt.After(lastAt)) {
			lastSequence = row.Sequence
			lastAt = row.EventAt
		}
	}
	remaining := make([]v150.OrderEvent, 0, len(ordered))
	for _, event := range ordered {
		matched := false
		for _, row := range existing {
			if row.RuleID == ruleID && row.Symbol == normalizeRecommendStockCode(rec.StockCode) && strings.EqualFold(row.EventType, string(event.Type)) && row.EventAt.Equal(event.At) {
				if math.Abs(row.Price-event.Price) > 1e-8 || math.Abs(row.Quantity-float64(event.Quantity)) > 1e-8 ||
					math.Abs(row.CashAmount-event.CashAmount) > 1e-8 || math.Abs(row.AdjustmentFactor-event.AdjustmentFactor) > 1e-8 ||
					strings.TrimSpace(row.Reason) != strings.TrimSpace(event.Reason) {
					return fmt.Errorf("immutable event %s already exists with different content", row.EventID)
				}
				matched = true
				break
			}
		}
		if !matched {
			remaining = append(remaining, event)
		}
	}
	if len(remaining) == 0 {
		return nil
	}
	if !lastAt.IsZero() && remaining[0].At.Before(lastAt) {
		return fmt.Errorf("event time %s precedes ledger tail %s", remaining[0].At.Format(time.RFC3339Nano), lastAt.Format(time.RFC3339Nano))
	}

	frozenAt := time.Now()
	for _, event := range remaining {
		if event.At.IsZero() {
			return errors.New("strategy lifecycle event time is missing")
		}
		// A completed-bar replay must never manufacture knowledge from the
		// future. In particular, do not move FrozenAt forward to make a future
		// event pass the immutable-ledger causality checks.
		if event.At.After(frozenAt) {
			return fmt.Errorf("strategy lifecycle event %s is in the future (event=%s observed=%s)", event.Type, event.At.Format(time.RFC3339Nano), frozenAt.Format(time.RFC3339Nano))
		}
	}
	rows := make([]models.OrderEvent, 0, len(remaining))
	for _, event := range remaining {
		lastSequence++
		fees := 0.0
		if event.Type == v150.EventFill && accounting.Entry != nil {
			fees = accounting.Entry.Commission + accounting.Entry.TransferFee + accounting.Entry.StampDuty
		}
		if event.Type == v150.EventExitFill && accounting.Exit != nil {
			fees = accounting.Exit.Commission + accounting.Exit.TransferFee + accounting.Exit.StampDuty
		}
		payload, err := json.Marshal(struct {
			RuleID        string          `json:"ruleId"`
			Event         v150.OrderEvent `json:"event"`
			Fees          float64         `json:"fees"`
			Scenario      string          `json:"scenario"`
			TradeDayIndex int             `json:"tradeDayIndex"`
		}{RuleID: ruleID, Event: event, Fees: fees, Scenario: "base_10bp", TradeDayIndex: marketSummaryV150TradeDayIndex(event.At)})
		if err != nil {
			return err
		}
		rows = append(rows, models.OrderEvent{
			EventID:          marketSummaryV150LifecycleEventID(runID, ruleID, event),
			RunID:            runID,
			RuleID:           ruleID,
			StrategyVersion:  v150.StrategyVersion,
			TradeDate:        run.TradeDate,
			Symbol:           normalizeRecommendStockCode(rec.StockCode),
			EventType:        string(event.Type),
			Sequence:         lastSequence,
			EventAt:          event.At,
			Price:            event.Price,
			Quantity:         float64(event.Quantity),
			CashAmount:       event.CashAmount,
			AdjustmentFactor: event.AdjustmentFactor,
			Fees:             fees,
			Reason:           strings.TrimSpace(event.Reason),
			PayloadJSON:      string(payload),
			FrozenAt:         &frozenAt,
		})
	}
	if err := persistence.SealStrategyOrderEvents(rows); err != nil {
		return err
	}
	return store.AppendOrderEvents(ctx, runID, rows)
}
