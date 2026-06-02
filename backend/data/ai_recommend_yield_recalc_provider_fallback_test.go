package data

import (
	"fmt"
	"go-stock/backend/db"
	appconfig "go-stock/internal/config"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFetchMinuteBarsFromProviders_PublicHistoricalWindowReturnsGuidanceError(t *testing.T) {
	appconfig.ResetRuntimeOverride()
	oldDB := db.Dao
	oldNow := timeNow
	db.Dao = nil
	t.Cleanup(func() {
		appconfig.ResetRuntimeOverride()
		db.Dao = oldDB
		timeNow = oldNow
	})
	t.Setenv("GO_STOCK_MINUTE_PROVIDER", "public")
	now := time.Date(2026, 4, 8, 10, 0, 0, 0, cnLocation())
	timeNow = func() time.Time { return now }

	start := time.Date(2026, 3, 10, 14, 30, 0, 0, cnLocation())
	end := time.Date(2026, 3, 20, 15, 0, 0, 0, cnLocation())

	bars, source, err := fetchMinuteBarsFromProviders("603019.SH", start, end, time.Second)
	if err == nil {
		t.Fatalf("expected guidance error for public historical window, got nil")
	}
	if !strings.Contains(err.Error(), "长历史分钟线") {
		t.Fatalf("unexpected error: %v", err)
	}
	if source != "" || len(bars) != 0 {
		t.Fatalf("expected empty result on public historical window, got source=%q bars=%d", source, len(bars))
	}
}

func TestFetchMinuteBarsFromProviders_PublicHistoricalWindowFallsBackToDiemengWhenPrivateConfigured(t *testing.T) {
	appconfig.ResetRuntimeOverride()
	db.Init(filepath.Join(t.TempDir(), "yield-public-historical-private-fallback.db"))
	if err := db.Dao.AutoMigrate(&Settings{}, &AIConfig{}); err != nil {
		t.Fatalf("auto migrate settings failed: %v", err)
	}
	t.Cleanup(func() {
		appconfig.ResetRuntimeOverride()
	})
	t.Setenv("GO_STOCK_MINUTE_PROVIDER", "public")

	oldNow := timeNow
	oldDiemeng := fetchMinuteBarsWithDiemengFn
	oldAkshare := fetchMinuteBarsWithAkShareFn
	oldTencent := fetchMinuteBarsWithTencentFn
	oldSina := fetchMinuteBarsWithSinaFn
	defer func() {
		timeNow = oldNow
		fetchMinuteBarsWithDiemengFn = oldDiemeng
		fetchMinuteBarsWithAkShareFn = oldAkshare
		fetchMinuteBarsWithTencentFn = oldTencent
		fetchMinuteBarsWithSinaFn = oldSina
	}()

	now := time.Date(2026, 4, 8, 10, 0, 0, 0, cnLocation())
	timeNow = func() time.Time { return now }

	row := &Settings{
		MinuteProviderMode:   "public",
		PrivateMinuteEnabled: true,
		PrivateMinuteBaseURL: "https://example.com/custom-api/",
		PrivateMinuteAPIKey:  "secret-key",
	}
	if err := db.Dao.Create(row).Error; err != nil {
		t.Fatalf("create settings failed: %v", err)
	}

	start := time.Date(2026, 3, 10, 14, 30, 0, 0, cnLocation())
	end := time.Date(2026, 3, 20, 15, 0, 0, 0, cnLocation())
	wantBars := []minuteBar{
		{TradeTime: start, Close: 11.2},
		{TradeTime: end, Close: 11.9},
	}

	var diemengCalls atomic.Int32
	var publicCalls atomic.Int32
	fetchMinuteBarsWithDiemengFn = func(tsCode string, gotStart, gotEnd time.Time) ([]minuteBar, string, error) {
		diemengCalls.Add(1)
		if tsCode != "603019.SH" {
			t.Fatalf("unexpected tsCode: %s", tsCode)
		}
		if !gotStart.Equal(start) || !gotEnd.Equal(end) {
			t.Fatalf("unexpected diemeng window: %v ~ %v", gotStart, gotEnd)
		}
		return wantBars, "diemeng", nil
	}
	fetchMinuteBarsWithAkShareFn = func(tsCode string, gotStart, gotEnd time.Time) ([]minuteBar, string, error) {
		publicCalls.Add(1)
		return nil, "akshare", fmt.Errorf("should not hit akshare when private fallback is ready")
	}
	fetchMinuteBarsWithTencentFn = func(tsCode string, gotStart, gotEnd time.Time) ([]minuteBar, string, error) {
		publicCalls.Add(1)
		return nil, "tencent", fmt.Errorf("should not hit tencent when private fallback is ready")
	}
	fetchMinuteBarsWithSinaFn = func(tsCode string, gotStart, gotEnd time.Time) ([]minuteBar, string, error) {
		publicCalls.Add(1)
		return nil, "sina", fmt.Errorf("should not hit sina when private fallback is ready")
	}

	bars, source, err := fetchMinuteBarsFromProviders("603019.SH", start, end, time.Second)
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if source != "diemeng" {
		t.Fatalf("expected diemeng source, got %s", source)
	}
	if diemengCalls.Load() != 1 {
		t.Fatalf("expected diemeng to be called once, got %d", diemengCalls.Load())
	}
	if publicCalls.Load() != 0 {
		t.Fatalf("expected no public provider fallback calls, got %d", publicCalls.Load())
	}
	if len(bars) != len(wantBars) {
		t.Fatalf("expected %d bars, got %d", len(wantBars), len(bars))
	}
}

func TestFetchMinuteBarsFromProviders_RecentFallsBackToAkshareAfterHedgedProvidersFail(t *testing.T) {
	appconfig.ResetRuntimeOverride()
	oldDB := db.Dao
	db.Dao = nil
	t.Cleanup(func() {
		appconfig.ResetRuntimeOverride()
		db.Dao = oldDB
	})
	t.Setenv("GO_STOCK_MINUTE_PROVIDER", "diemeng")
	t.Setenv("GO_STOCK_YIELD_HEDGE_DELAY_TENCENT_MS", "0")
	t.Setenv("GO_STOCK_YIELD_HEDGE_DELAY_DIEMENG_MS", "0")
	t.Setenv("GO_STOCK_YIELD_AKSHARE_FALLBACK", "true")

	oldNow := timeNow
	oldTencent := fetchMinuteBarsWithTencentFn
	oldAkshare := fetchMinuteBarsWithAkShareFn
	oldDiemeng := fetchMinuteBarsWithDiemengFn
	oldSina := fetchMinuteBarsWithSinaFn
	defer func() {
		timeNow = oldNow
		fetchMinuteBarsWithTencentFn = oldTencent
		fetchMinuteBarsWithAkShareFn = oldAkshare
		fetchMinuteBarsWithDiemengFn = oldDiemeng
		fetchMinuteBarsWithSinaFn = oldSina
	}()

	now := time.Date(2026, 4, 2, 10, 0, 0, 0, cnLocation())
	timeNow = func() time.Time { return now }
	start := time.Date(2026, 4, 1, 14, 30, 0, 0, cnLocation())
	end := time.Date(2026, 4, 1, 15, 0, 0, 0, cnLocation())
	wantBars := []minuteBar{
		{TradeTime: start, Close: 12.1},
		{TradeTime: end, Close: 12.8},
	}

	var akshareCalls atomic.Int32
	fetchMinuteBarsWithTencentFn = func(tsCode string, gotStart, gotEnd time.Time) ([]minuteBar, string, error) {
		return nil, "tencent", fmt.Errorf("tencent unavailable")
	}
	fetchMinuteBarsWithDiemengFn = func(tsCode string, gotStart, gotEnd time.Time) ([]minuteBar, string, error) {
		return nil, "diemeng", fmt.Errorf("diemeng unavailable")
	}
	fetchMinuteBarsWithSinaFn = func(tsCode string, gotStart, gotEnd time.Time) ([]minuteBar, string, error) {
		return nil, "sina", fmt.Errorf("sina minute provider only enabled for today")
	}
	fetchMinuteBarsWithAkShareFn = func(tsCode string, gotStart, gotEnd time.Time) ([]minuteBar, string, error) {
		akshareCalls.Add(1)
		if tsCode != "603019.SH" {
			t.Fatalf("unexpected tsCode: %s", tsCode)
		}
		if !gotStart.Equal(start) || !gotEnd.Equal(end) {
			t.Fatalf("unexpected akshare window: %v ~ %v", gotStart, gotEnd)
		}
		return wantBars, "akshare", nil
	}

	bars, source, err := fetchMinuteBarsFromProviders("603019.SH", start, end, time.Second)
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if source != "akshare" {
		t.Fatalf("expected akshare source, got %s", source)
	}
	if akshareCalls.Load() != 1 {
		t.Fatalf("expected akshare fallback to be called once, got %d", akshareCalls.Load())
	}
	if len(bars) != len(wantBars) {
		t.Fatalf("expected %d bars, got %d", len(wantBars), len(bars))
	}
}

func TestFetchMinuteBarsFromProviders_TodayIntradayPrefersSina(t *testing.T) {
	appconfig.ResetRuntimeOverride()
	oldDB := db.Dao
	db.Dao = nil
	t.Cleanup(func() {
		appconfig.ResetRuntimeOverride()
		db.Dao = oldDB
	})
	t.Setenv("GO_STOCK_MINUTE_PROVIDER", "diemeng")
	t.Setenv("GO_STOCK_YIELD_HEDGE_DELAY_TENCENT_MS", "100")
	t.Setenv("GO_STOCK_YIELD_HEDGE_DELAY_DIEMENG_MS", "200")

	oldNow := timeNow
	oldTencent := fetchMinuteBarsWithTencentFn
	oldDiemeng := fetchMinuteBarsWithDiemengFn
	oldSina := fetchMinuteBarsWithSinaFn
	defer func() {
		timeNow = oldNow
		fetchMinuteBarsWithTencentFn = oldTencent
		fetchMinuteBarsWithDiemengFn = oldDiemeng
		fetchMinuteBarsWithSinaFn = oldSina
	}()

	now := time.Date(2026, 4, 2, 10, 30, 0, 0, cnLocation())
	timeNow = func() time.Time { return now }
	start := time.Date(2026, 4, 2, 9, 31, 0, 0, cnLocation())
	end := time.Date(2026, 4, 2, 10, 30, 0, 0, cnLocation())
	wantBars := []minuteBar{
		{TradeTime: start, Close: 9.9},
		{TradeTime: end, Close: 10.2},
	}

	fetchMinuteBarsWithSinaFn = func(tsCode string, gotStart, gotEnd time.Time) ([]minuteBar, string, error) {
		return wantBars, "sina", nil
	}
	fetchMinuteBarsWithTencentFn = func(tsCode string, gotStart, gotEnd time.Time) ([]minuteBar, string, error) {
		time.Sleep(250 * time.Millisecond)
		return nil, "tencent", fmt.Errorf("should not win race")
	}
	fetchMinuteBarsWithDiemengFn = func(tsCode string, gotStart, gotEnd time.Time) ([]minuteBar, string, error) {
		time.Sleep(250 * time.Millisecond)
		return nil, "diemeng", fmt.Errorf("should not win race")
	}

	bars, source, err := fetchMinuteBarsFromProviders("603019.SH", start, end, time.Second)
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if source != "sina" {
		t.Fatalf("expected sina source, got %s", source)
	}
	if len(bars) != len(wantBars) {
		t.Fatalf("expected %d bars, got %d", len(wantBars), len(bars))
	}
}

func TestExecuteMinuteProviderPlan_ReturnsPartialBarsWithoutWaitingForSlowHedge(t *testing.T) {
	oldSina := fetchMinuteBarsWithSinaFn
	oldDiemeng := fetchMinuteBarsWithDiemengFn
	defer func() {
		fetchMinuteBarsWithSinaFn = oldSina
		fetchMinuteBarsWithDiemengFn = oldDiemeng
	}()

	start := time.Date(2026, 4, 2, 9, 31, 0, 0, cnLocation())
	end := time.Date(2026, 4, 2, 10, 30, 0, 0, cnLocation())
	wantBars := []minuteBar{{TradeTime: start, Close: 9.9}}
	fetchMinuteBarsWithSinaFn = func(tsCode string, gotStart, gotEnd time.Time) ([]minuteBar, string, error) {
		return wantBars, "sina", nil
	}
	fetchMinuteBarsWithDiemengFn = func(tsCode string, gotStart, gotEnd time.Time) ([]minuteBar, string, error) {
		time.Sleep(500 * time.Millisecond)
		return nil, "diemeng", fmt.Errorf("slow provider should not block partial usable data")
	}

	started := time.Now()
	bars, source, err := executeMinuteProviderPlan(
		"603019.SH",
		start,
		end,
		[]minuteProviderAttempt{{Provider: "sina"}, {Provider: "diemeng"}},
		nil,
		100*time.Millisecond,
	)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if source != "sina" {
		t.Fatalf("expected sina source, got %s", source)
	}
	if len(bars) != len(wantBars) {
		t.Fatalf("expected %d bars, got %d", len(wantBars), len(bars))
	}
	if elapsed >= 250*time.Millisecond {
		t.Fatalf("provider plan waited for slow hedge: elapsed=%s", elapsed)
	}
}

func TestExecuteMinuteProviderPlanStrict_WaitsForCompleteHedge(t *testing.T) {
	oldSina := fetchMinuteBarsWithSinaFn
	oldDiemeng := fetchMinuteBarsWithDiemengFn
	defer func() {
		fetchMinuteBarsWithSinaFn = oldSina
		fetchMinuteBarsWithDiemengFn = oldDiemeng
	}()

	loc := cnLocation()
	start := time.Date(2026, 4, 2, 9, 31, 0, 0, loc)
	end := time.Date(2026, 4, 2, 9, 33, 0, 0, loc)
	partialBars := []minuteBar{{TradeTime: start, Close: 9.9}}
	completeBars := []minuteBar{
		{TradeTime: start, Close: 9.9},
		{TradeTime: start.Add(time.Minute), Close: 10.0},
		{TradeTime: end, Close: 10.1},
	}
	fetchMinuteBarsWithSinaFn = func(tsCode string, gotStart, gotEnd time.Time) ([]minuteBar, string, error) {
		return partialBars, "sina", nil
	}
	fetchMinuteBarsWithDiemengFn = func(tsCode string, gotStart, gotEnd time.Time) ([]minuteBar, string, error) {
		time.Sleep(120 * time.Millisecond)
		return completeBars, "diemeng", nil
	}

	started := time.Now()
	bars, source, err := executeMinuteProviderPlanStrict(
		"603019.SH",
		start,
		end,
		[]minuteProviderAttempt{{Provider: "sina"}, {Provider: "diemeng"}},
		nil,
		time.Second,
	)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if source != "diemeng" {
		t.Fatalf("expected diemeng source, got %s", source)
	}
	if len(bars) != len(completeBars) {
		t.Fatalf("expected %d bars, got %d", len(completeBars), len(bars))
	}
	if elapsed < 100*time.Millisecond {
		t.Fatalf("strict provider plan returned before complete hedge: elapsed=%s", elapsed)
	}
}

func TestExecuteMinuteProviderPlanStrict_ReturnsPartialWithIncompleteError(t *testing.T) {
	oldSina := fetchMinuteBarsWithSinaFn
	defer func() {
		fetchMinuteBarsWithSinaFn = oldSina
	}()

	start := time.Date(2026, 4, 2, 9, 31, 0, 0, cnLocation())
	end := time.Date(2026, 4, 2, 10, 30, 0, 0, cnLocation())
	wantBars := []minuteBar{{TradeTime: start, Close: 9.9}}
	fetchMinuteBarsWithSinaFn = func(tsCode string, gotStart, gotEnd time.Time) ([]minuteBar, string, error) {
		return wantBars, "sina", nil
	}

	bars, source, err := executeMinuteProviderPlanStrict(
		"603019.SH",
		start,
		end,
		[]minuteProviderAttempt{{Provider: "sina"}},
		nil,
		time.Second,
	)
	if err == nil || !strings.Contains(err.Error(), "未完整覆盖") {
		t.Fatalf("expected incomplete coverage error, got %v", err)
	}
	if source != "sina" {
		t.Fatalf("expected sina source, got %s", source)
	}
	if len(bars) != len(wantBars) {
		t.Fatalf("expected %d partial bars, got %d", len(wantBars), len(bars))
	}
}

func TestExecuteMinuteProviderPlan_TimesOutSlowFallback(t *testing.T) {
	oldSina := fetchMinuteBarsWithSinaFn
	oldDiemeng := fetchMinuteBarsWithDiemengFn
	defer func() {
		fetchMinuteBarsWithSinaFn = oldSina
		fetchMinuteBarsWithDiemengFn = oldDiemeng
	}()

	start := time.Date(2026, 4, 2, 9, 31, 0, 0, cnLocation())
	end := time.Date(2026, 4, 2, 10, 30, 0, 0, cnLocation())
	fetchMinuteBarsWithSinaFn = func(tsCode string, gotStart, gotEnd time.Time) ([]minuteBar, string, error) {
		return nil, "sina", fmt.Errorf("sina unavailable")
	}
	fetchMinuteBarsWithDiemengFn = func(tsCode string, gotStart, gotEnd time.Time) ([]minuteBar, string, error) {
		time.Sleep(500 * time.Millisecond)
		return nil, "diemeng", nil
	}

	started := time.Now()
	bars, source, err := executeMinuteProviderPlan(
		"603019.SH",
		start,
		end,
		[]minuteProviderAttempt{{Provider: "sina"}},
		[]string{"diemeng"},
		100*time.Millisecond,
	)
	elapsed := time.Since(started)
	if err == nil || !strings.Contains(err.Error(), "响应超时") {
		t.Fatalf("expected timeout error, got %v", err)
	}
	if source != "diemeng" {
		t.Fatalf("expected diemeng timeout source, got %s", source)
	}
	if len(bars) != 0 {
		t.Fatalf("expected no bars, got %d", len(bars))
	}
	if elapsed >= 250*time.Millisecond {
		t.Fatalf("fallback timeout did not cap slow provider: elapsed=%s", elapsed)
	}
}
