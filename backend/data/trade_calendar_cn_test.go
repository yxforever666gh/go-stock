package data

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-stock/backend/db"
)

func initTradeCalendarTestDB(t *testing.T) {
	t.Helper()
	db.Init(filepath.Join(t.TempDir(), "trade-calendar-test.db"))
}

func TestIsCNOpenTradeDayStrictWeekend(t *testing.T) {
	initTradeCalendarTestDB(t)

	loc := cnLocation()
	day := time.Date(2026, 3, 7, 10, 0, 0, 0, loc)

	open, err := IsCNOpenTradeDayStrict(day)
	if err != nil {
		t.Fatalf("weekend should not require calendar, err=%v", err)
	}
	if open {
		t.Fatalf("weekend should not be open")
	}
}

func TestIsCNOpenTradeDayStrictCalendarUnavailable(t *testing.T) {
	initTradeCalendarTestDB(t)

	backup := globalCNTradeCalCache
	globalCNTradeCalCache = &cnTradeCalCache{
		lastError: "tushare token is empty",
	}
	defer func() {
		globalCNTradeCalCache = backup
	}()

	loc := cnLocation()
	day := time.Date(2026, 3, 4, 10, 0, 0, 0, loc)

	open, err := IsCNOpenTradeDayStrict(day)
	if err == nil {
		t.Fatalf("expected error when trade calendar is unavailable, open=%v", open)
	}
	if open {
		t.Fatalf("calendar unavailable should not be treated as open")
	}
	if !strings.Contains(err.Error(), "trade calendar unavailable") {
		t.Fatalf("unexpected error: %v", err)
	}
}
