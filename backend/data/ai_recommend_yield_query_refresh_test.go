package data

import (
	"path/filepath"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"

	"gorm.io/gorm"
)

func setTestCNTradeCalendar(startDay, endDay time.Time, openDays map[string]bool) func() {
	cache := globalCNTradeCalCache
	cache.mu.Lock()
	prevStartDay := cache.startDay
	prevEndDay := cache.endDay
	prevOpenDays := cache.openDays
	prevLoadedAt := cache.loadedAt
	prevLastError := cache.lastError

	copied := make(map[string]bool, len(openDays))
	for key, val := range openDays {
		copied[key] = val
	}
	cache.startDay = startDay
	cache.endDay = endDay
	cache.openDays = copied
	cache.loadedAt = time.Now()
	cache.lastError = ""
	cache.mu.Unlock()

	return func() {
		cache.mu.Lock()
		cache.startDay = prevStartDay
		cache.endDay = prevEndDay
		cache.openDays = prevOpenDays
		cache.loadedAt = prevLoadedAt
		cache.lastError = prevLastError
		cache.mu.Unlock()
	}
}

func TestResolveExpectedYieldTradeDate_UsesCurrentTradeDayOrPreviousOpenDay(t *testing.T) {
	loc := cnLocation()
	restoreCal := setTestCNTradeCalendar(
		time.Date(2026, 4, 1, 0, 0, 0, 0, loc),
		time.Date(2026, 4, 30, 0, 0, 0, 0, loc),
		map[string]bool{
			"2026-04-09": true,
			"2026-04-10": true,
			"2026-04-11": false,
			"2026-04-12": false,
		},
	)
	defer restoreCal()

	gotOpenDay := resolveExpectedYieldTradeDate(time.Date(2026, 4, 10, 14, 30, 0, 0, loc))
	wantOpenDay := time.Date(2026, 4, 10, 0, 0, 0, 0, loc)
	if !gotOpenDay.Equal(wantOpenDay) {
		t.Fatalf("expected open day %s, got %s", wantOpenDay.Format("2006-01-02"), gotOpenDay.Format("2006-01-02"))
	}

	gotWeekend := resolveExpectedYieldTradeDate(time.Date(2026, 4, 11, 10, 0, 0, 0, loc))
	wantWeekend := time.Date(2026, 4, 10, 0, 0, 0, 0, loc)
	if !gotWeekend.Equal(wantWeekend) {
		t.Fatalf("expected previous trade day %s, got %s", wantWeekend.Format("2006-01-02"), gotWeekend.Format("2006-01-02"))
	}
}

func TestShouldTriggerYieldQueryRecalc_RespectsTradeDateAndCooldown(t *testing.T) {
	loc := cnLocation()
	now := time.Date(2026, 4, 10, 16, 5, 0, 0, loc)
	expectedTradeDate := time.Date(2026, 4, 10, 0, 0, 0, 0, loc)
	cooldownUntil := now.Add(30 * time.Second)

	if !shouldTriggerYieldQueryRecalc(&models.AiRecommendYieldMeta{
		ID:               1,
		CurrentTradeDate: "2026-04-08",
	}, expectedTradeDate, now) {
		t.Fatal("expected stale trade date to trigger query recalc")
	}

	if shouldTriggerYieldQueryRecalc(&models.AiRecommendYieldMeta{
		ID:                 1,
		CurrentTradeDate:   "2026-04-10",
		QueryCooldownUntil: &cooldownUntil,
	}, expectedTradeDate, now) {
		t.Fatal("expected active cooldown to block query recalc")
	}

	if shouldTriggerYieldQueryRecalc(&models.AiRecommendYieldMeta{
		ID:               1,
		CurrentTradeDate: "2026-04-10",
	}, expectedTradeDate, now) {
		t.Fatal("expected current trade date to skip query recalc")
	}
}

func TestTriggerYieldQueryRecalcIfStale_UpdatesCooldownAndInvokesRecalc(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "yield-query-refresh.db"))
	if err := db.Dao.AutoMigrate(&models.AiRecommendYieldMeta{}); err != nil {
		t.Fatalf("auto migrate meta failed: %v", err)
	}

	loc := cnLocation()
	now := time.Date(2026, 4, 10, 16, 10, 0, 0, loc)
	expectedTradeDate := time.Date(2026, 4, 10, 0, 0, 0, 0, loc)
	meta := models.AiRecommendYieldMeta{
		CurrentTradeDate: "2026-04-08",
	}
	if err := db.Dao.Create(&meta).Error; err != nil {
		t.Fatalf("create meta failed: %v", err)
	}

	prevRequestFn := requestAiRecommendYieldRecalcForQueryFn
	triggered := false
	triggerReason := ""
	triggerForce := false
	requestAiRecommendYieldRecalcForQueryFn = func(force bool, reason string) {
		triggered = true
		triggerForce = force
		triggerReason = reason
	}
	defer func() {
		requestAiRecommendYieldRecalcForQueryFn = prevRequestFn
	}()

	if !triggerYieldQueryRecalcIfStale(&meta, expectedTradeDate, now) {
		t.Fatal("expected stale query recalc trigger to succeed")
	}
	if !triggered {
		t.Fatal("expected query recalc callback to be invoked")
	}
	if !triggerForce || triggerReason != "query_stale_trade_date" {
		t.Fatalf("unexpected recalc request: force=%v reason=%s", triggerForce, triggerReason)
	}

	var reloaded models.AiRecommendYieldMeta
	if err := db.Dao.First(&reloaded, meta.ID).Error; err != nil {
		t.Fatalf("reload meta failed: %v", err)
	}
	if reloaded.LastQueryRecalcAt == nil || !reloaded.LastQueryRecalcAt.Equal(now) {
		t.Fatalf("expected last_query_recalc_at=%v, got %+v", now, reloaded.LastQueryRecalcAt)
	}
	if reloaded.QueryCooldownUntil == nil {
		t.Fatal("expected query cooldown to be persisted")
	}
	if reloaded.QueryCooldownUntil.Sub(now) != aiRecommendQueryRecalcCooldown {
		t.Fatalf("expected cooldown %v, got %v", aiRecommendQueryRecalcCooldown, reloaded.QueryCooldownUntil.Sub(now))
	}
}

func TestCollectYieldPendingIntradayRecalcScope_FindsStalePendingOrMissingState(t *testing.T) {
	loc := cnLocation()
	now := time.Date(2026, 4, 10, 10, 30, 0, 0, loc)
	latestTradeDate := time.Date(2026, 4, 10, 0, 0, 0, 0, loc)
	staleAt := now.Add(-16 * time.Minute)
	freshAt := now.Add(-5 * time.Minute)

	records := []models.AiRecommendStocks{
		{Model: gorm.Model{ID: 1}, StockCode: "000001.SZ", DataTime: testTimePtr(time.Date(2026, 4, 10, 9, 30, 0, 0, loc))},
		{Model: gorm.Model{ID: 2}, StockCode: "000002.SZ", DataTime: testTimePtr(time.Date(2026, 4, 10, 9, 30, 0, 0, loc))},
		{Model: gorm.Model{ID: 3}, StockCode: "000003.SZ", DataTime: testTimePtr(time.Date(2026, 4, 10, 9, 30, 0, 0, loc))},
		{Model: gorm.Model{ID: 4}, StockCode: "000004.SZ", DataTime: testTimePtr(time.Date(2026, 4, 10, 9, 30, 0, 0, loc))},
	}
	stateMap := map[uint]models.AiRecommendYieldRecordState{
		1: {RecommendID: 1, ActivationStatus: "pending", LastRecalcAt: &staleAt},
		2: {RecommendID: 2, ActivationStatus: "pending", LastRecalcAt: &freshAt},
		3: {RecommendID: 3, ActivationStatus: "activated", LastRecalcAt: &staleAt},
	}

	got := collectYieldPendingIntradayRecalcScope(now, latestTradeDate, records, stateMap)
	if len(got) != 2 {
		t.Fatalf("expected 2 scoped codes, got %v", got)
	}
	if got[0] != "000001.SZ" || got[1] != "000004.SZ" {
		t.Fatalf("unexpected scope codes: %v", got)
	}
}

func TestTriggerYieldPendingIntradayRecalcIfStale_UpdatesCooldownAndInvokesScopedRecalc(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "yield-query-pending-refresh.db"))
	if err := db.Dao.AutoMigrate(&models.AiRecommendYieldMeta{}); err != nil {
		t.Fatalf("auto migrate meta failed: %v", err)
	}

	loc := cnLocation()
	now := time.Date(2026, 4, 10, 10, 30, 0, 0, loc)
	latestTradeDate := time.Date(2026, 4, 10, 0, 0, 0, 0, loc)
	staleAt := now.Add(-20 * time.Minute)
	meta := models.AiRecommendYieldMeta{
		CurrentTradeDate: "2026-04-10",
	}
	if err := db.Dao.Create(&meta).Error; err != nil {
		t.Fatalf("create meta failed: %v", err)
	}

	prevScopedRequestFn := requestAiRecommendYieldScopedRecalcForQueryFn
	triggered := false
	triggerForce := true
	triggerReason := ""
	triggerScope := []string(nil)
	requestAiRecommendYieldScopedRecalcForQueryFn = func(force bool, reason string, scopeCodes []string) {
		triggered = true
		triggerForce = force
		triggerReason = reason
		triggerScope = append([]string{}, scopeCodes...)
	}
	defer func() {
		requestAiRecommendYieldScopedRecalcForQueryFn = prevScopedRequestFn
	}()

	records := []models.AiRecommendStocks{
		{Model: gorm.Model{ID: 1}, StockCode: "000001.SZ", DataTime: testTimePtr(time.Date(2026, 4, 10, 9, 30, 0, 0, loc))},
	}
	stateMap := map[uint]models.AiRecommendYieldRecordState{
		1: {RecommendID: 1, ActivationStatus: "pending", LastRecalcAt: &staleAt},
	}

	if !triggerYieldPendingIntradayRecalcIfStale(&meta, now, latestTradeDate, records, stateMap) {
		t.Fatal("expected pending intraday recalc trigger to succeed")
	}
	if !triggered {
		t.Fatal("expected scoped recalc callback to be invoked")
	}
	if triggerForce {
		t.Fatal("expected pending intraday recalc to use non-force scoped refresh")
	}
	if triggerReason != "query_pending_intraday" {
		t.Fatalf("unexpected recalc reason: %s", triggerReason)
	}
	if len(triggerScope) != 1 || triggerScope[0] != "000001.SZ" {
		t.Fatalf("unexpected scoped codes: %v", triggerScope)
	}

	var reloaded models.AiRecommendYieldMeta
	if err := db.Dao.First(&reloaded, meta.ID).Error; err != nil {
		t.Fatalf("reload meta failed: %v", err)
	}
	if reloaded.LastQueryRecalcAt == nil || !reloaded.LastQueryRecalcAt.Equal(now) {
		t.Fatalf("expected last_query_recalc_at=%v, got %+v", now, reloaded.LastQueryRecalcAt)
	}
	if reloaded.QueryCooldownUntil == nil {
		t.Fatal("expected query cooldown to be persisted")
	}
	if reloaded.QueryCooldownUntil.Sub(now) != aiRecommendQueryRecalcCooldown {
		t.Fatalf("expected cooldown %v, got %v", aiRecommendQueryRecalcCooldown, reloaded.QueryCooldownUntil.Sub(now))
	}
}

func testTimePtr(t time.Time) *time.Time {
	return &t
}
