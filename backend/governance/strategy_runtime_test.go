package governance

import (
	"context"
	"errors"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestStrategyRuntimeDefaultsPausedAndRequiresExplicitResume(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := InitializeStrategyRuntimeControl(ctx, database, "1.5.0"); err != nil {
		t.Fatal(err)
	}
	status := GetStrategyRuntimeStatus(ctx, database, "1.5.0")
	if !status.Ready || status.Mode != StrategyModePaused {
		t.Fatalf("unexpected initial status: %+v", status)
	}
	if err := RequireStrategyLive(ctx, database, "1.5.0"); !errors.Is(err, ErrStrategyPaused) {
		t.Fatalf("RequireStrategyLive error = %v", err)
	}
	status, err = SetStrategyRuntimeMode(ctx, database, StrategyModeLive, "1.5.0", "engineering gates passed", "test")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Ready || status.Mode != StrategyModeLive {
		t.Fatalf("unexpected resumed status: %+v", status)
	}
	if err := RequireStrategyLive(ctx, database, "1.5.0"); err != nil {
		t.Fatalf("RequireStrategyLive after resume: %v", err)
	}
}

func TestStrategyRuntimeFailsClosedWithoutControlTable(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	status := GetStrategyRuntimeStatus(context.Background(), database, "1.5.0")
	if status.Ready || status.Mode != StrategyModePaused {
		t.Fatalf("unexpected fail-closed status: %+v", status)
	}
	if err := RequireStrategyLive(context.Background(), database, "1.5.0"); !errors.Is(err, ErrStrategyRuntimeUnavailable) {
		t.Fatalf("RequireStrategyLive error = %v", err)
	}
}
