package cli

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"go-stock/backend/persistence"
)

type recordingFrozenBacktestRepository struct {
	loadCalls int
	loadErr   error
}

func (r *recordingFrozenBacktestRepository) LoadFrozenStrategyInputs(context.Context, string, time.Time, time.Time) (persistence.FrozenStrategyInputs, error) {
	r.loadCalls++
	return persistence.FrozenStrategyInputs{}, r.loadErr
}

func (*recordingFrozenBacktestRepository) PersistBacktestResult(context.Context, persistence.BacktestResult) error {
	return nil
}

func TestStrategyBacktestUsesInjectedFrozenRepository(t *testing.T) {
	sentinel := errors.New("frozen repository sentinel")
	repository := &recordingFrozenBacktestRepository{loadErr: sentinel}
	err := runStrategyBacktestWithRepository(
		[]string{"--version", "1.5.0", "--from", "2026-08-04", "--to", "2026-08-04"},
		GlobalOptions{}, io.Discard, io.Discard, repository,
	)
	if !errors.Is(err, sentinel) || repository.loadCalls != 1 {
		t.Fatalf("error=%v loadCalls=%d", err, repository.loadCalls)
	}
}
