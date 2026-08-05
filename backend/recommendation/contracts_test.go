package recommendation

import (
	"errors"
	"testing"
	"time"

	"go-stock/backend/strategy/v150"
)

func TestPublicationRequiresSingleRunOwnership(t *testing.T) {
	started := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	decision := started.Add(time.Hour)
	validFrom := decision.Add(time.Minute)
	p := Publication{
		Run: RunSnapshot{Context: v150.RunContext{
			RunID: "run-1", StartedAt: started, AsOf: decision, DataCutoffAt: decision,
			DecisionAt: decision, ValidFromAt: validFrom, TradeDayIndex: 1, ValidFromTradeDayIndex: 1,
			StrategyVersion: v150.StrategyVersion,
		}, FrozenAt: decision},
		Candidates: []CandidateSnapshot{{CandidateID: "candidate-1", RunID: "other-run", FrozenAt: decision}},
	}
	if err := p.Validate(); !errors.Is(err, ErrInvalidPublication) {
		t.Fatalf("Validate error = %v, want ErrInvalidPublication", err)
	}
}

func TestPublicationAcceptsAtomicEmptyNoTradeRun(t *testing.T) {
	started := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	decision := started.Add(time.Hour)
	p := Publication{Run: RunSnapshot{Context: v150.RunContext{
		RunID: "run-1", StartedAt: started, AsOf: decision, DataCutoffAt: decision,
		DecisionAt: decision, ValidFromAt: decision.Add(time.Minute), TradeDayIndex: 1,
		ValidFromTradeDayIndex: 1, StrategyVersion: v150.StrategyVersion,
	}, FrozenAt: decision}}
	if err := p.Validate(); err != nil {
		t.Fatalf("Validate no-trade run: %v", err)
	}
}
