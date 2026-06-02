package data

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
)

func TestBuildManualMinuteGapCoverageTasks_IncludesRangeStartGap(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "manual-gap-range-start.db"))
	if err := db.Dao.AutoMigrate(
		&models.AiRecommendStocks{},
		&models.AiRecommendYieldMeta{},
		&models.AiRecommendMinuteBar{},
		&models.AiRecommendYieldState{},
		&models.AiRecommendYieldRecordState{},
		&models.AiRecommendYieldOverride{},
		&Settings{},
	); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	oldNow := timeNow
	t.Cleanup(func() { timeNow = oldNow })
	loc := cnLocation()
	now := time.Date(2026, 5, 29, 15, 30, 0, 0, loc)
	timeNow = func() time.Time { return now }
	if err := db.Dao.Create(&models.AiRecommendYieldMeta{CurrentTradeDate: "2026-05-29"}).Error; err != nil {
		t.Fatalf("create meta failed: %v", err)
	}

	recordTime := time.Date(2026, 5, 28, 14, 30, 0, 0, loc)
	rec := models.AiRecommendStocks{
		DataTime:                    &recordTime,
		StockCode:                   "301293.SZ",
		StockName:                   "三博脑科",
		RecommendBuyPrice:           "10-10.5",
		RecommendStopProfitPrice:    "11-12",
		RecommendStopLossPrice:      "9.6",
		RecommendStatus:             "valid",
		RecommendCategory:           recommendExecutionImmediate,
		ActivationStatus:            "pending",
		RecommendBuyPriceMin:        10,
		RecommendBuyPriceMax:        10.5,
		RecommendStopProfitPriceMin: 11,
		RecommendStopProfitPriceMax: 12,
	}
	if err := db.Dao.Create(&rec).Error; err != nil {
		t.Fatalf("create recommend failed: %v", err)
	}
	for _, tradeTime := range []time.Time{
		time.Date(2026, 5, 29, 11, 0, 0, 0, loc),
		time.Date(2026, 5, 29, 15, 0, 0, 0, loc),
	} {
		if err := db.Dao.Create(&models.AiRecommendMinuteBar{
			StockCode: "301293.SZ",
			TradeTime: tradeTime,
			Open:      10,
			High:      10,
			Low:       10,
			Close:     10,
			Source:    "test",
		}).Error; err != nil {
			t.Fatalf("create minute bar failed: %v", err)
		}
	}

	tasks := buildManualMinuteGapCoverageTasks(map[string]struct{}{"301293.SZ": struct{}{}})
	if len(tasks) == 0 {
		t.Fatal("expected forced gap task for range start gap")
	}
	got := tasks[0]
	if got.StockCode != "301293.SZ" || !got.Forced {
		t.Fatalf("unexpected task: %#v", got)
	}
	if !got.PreferHistorical {
		t.Fatalf("expected range-start gap task to prefer historical providers: %#v", got)
	}
	wantStart := time.Date(2026, 5, 27, 9, 31, 0, 0, loc)
	if !got.Start.Equal(wantStart) {
		t.Fatalf("task start=%v, want %v", got.Start, wantStart)
	}
	wantEnd := time.Date(2026, 5, 27, 11, 30, 0, 0, loc)
	if !got.End.Equal(wantEnd) {
		t.Fatalf("task end=%v, want %v", got.End, wantEnd)
	}
	if len(tasks) != 5 {
		t.Fatalf("task len=%d, want 5 split trading sessions: %#v", len(tasks), tasks)
	}
	last := tasks[len(tasks)-1]
	wantLastStart := time.Date(2026, 5, 29, 9, 31, 0, 0, loc)
	wantLastEnd := time.Date(2026, 5, 29, 10, 59, 0, 0, loc)
	if !last.Start.Equal(wantLastStart) || !last.End.Equal(wantLastEnd) {
		t.Fatalf("last task=%v~%v, want %v~%v", last.Start, last.End, wantLastStart, wantLastEnd)
	}
	warning := buildManualDownloadCoverageWarning(&models.AiRecommendYieldMeta{CurrentTradeDate: "2026-05-29"}, 5)
	if !strings.Contains(warning, "仍有待覆盖 1 条") || !strings.Contains(warning, "301293.SZ") {
		t.Fatalf("unexpected warning: %q", warning)
	}
}

func TestMinuteProviderResultCompletesOvernightNonTradingWindow(t *testing.T) {
	loc := cnLocation()
	start := time.Date(2026, 6, 1, 9, 31, 0, 0, loc)
	end := time.Date(2026, 6, 2, 9, 29, 0, 0, loc)
	bars := minuteBarsForSessions(start, end)
	if len(bars) != 240 {
		t.Fatalf("bars=%d, want 240", len(bars))
	}

	result := buildMinuteProviderResult("diemeng", bars, "diemeng", nil, start, end)
	if !result.Complete {
		t.Fatalf("expected trading-session coverage to be complete for overnight non-trading tail")
	}
	if minuteBarsCoverRange(bars, start, end) {
		t.Fatalf("natural range coverage should remain false for this regression scenario")
	}
}

func TestMinuteProviderResultRejectsTradingSessionGap(t *testing.T) {
	loc := cnLocation()
	start := time.Date(2026, 6, 1, 9, 31, 0, 0, loc)
	end := time.Date(2026, 6, 2, 9, 29, 0, 0, loc)
	bars := minuteBarsForSessions(start, end)
	bars = bars[30:]

	result := buildMinuteProviderResult("diemeng", bars, "diemeng", nil, start, end)
	if result.Complete {
		t.Fatalf("expected missing trading-session head to be incomplete")
	}
}

func TestMinuteCoverageContinuityReportsUnsuspendedEmptySession(t *testing.T) {
	oldFetchSuspensions := fetchDiemengSuspensionsFn
	clearDiemengSuspensionCache()
	t.Cleanup(func() {
		fetchDiemengSuspensionsFn = oldFetchSuspensions
		clearDiemengSuspensionCache()
	})
	fetchDiemengSuspensionsFn = func(stockCode string, tradeDate time.Time) ([]diemengSuspensionItem, error) {
		return nil, nil
	}

	loc := cnLocation()
	day := time.Date(2026, 5, 29, 0, 0, 0, 0, loc)
	sessions := buildMinuteCoverageSessions(
		time.Date(day.Year(), day.Month(), day.Day(), 9, 31, 0, 0, loc),
		time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, loc),
	)
	bars := minuteBarsForSessions(
		time.Date(day.Year(), day.Month(), day.Day(), 9, 31, 0, 0, loc),
		time.Date(day.Year(), day.Month(), day.Day(), 11, 30, 0, 0, loc),
	)

	issue := computeMinuteCoverageContinuityIssueForStock("301293.SZ", bars, sessions)
	if !strings.Contains(issue.Reason, "13:01~15:00 无数据") {
		t.Fatalf("expected afternoon empty-session issue, got %#v", issue)
	}
}

func TestMinuteCoverageContinuityIgnoresSuspendedEmptySession(t *testing.T) {
	oldFetchSuspensions := fetchDiemengSuspensionsFn
	clearDiemengSuspensionCache()
	t.Cleanup(func() {
		fetchDiemengSuspensionsFn = oldFetchSuspensions
		clearDiemengSuspensionCache()
	})
	fetchDiemengSuspensionsFn = func(stockCode string, tradeDate time.Time) ([]diemengSuspensionItem, error) {
		start := "13:00:00"
		end := "15:00:00"
		return []diemengSuspensionItem{
			{
				StockCode:        "301293.SZ",
				SuspendDate:      tradeDate.In(cnLocation()).Format("2006-01-02"),
				SuspendStartTime: &start,
				SuspendEndTime:   &end,
			},
		}, nil
	}

	loc := cnLocation()
	day := time.Date(2026, 5, 29, 0, 0, 0, 0, loc)
	sessions := buildMinuteCoverageSessions(
		time.Date(day.Year(), day.Month(), day.Day(), 9, 31, 0, 0, loc),
		time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, loc),
	)
	bars := minuteBarsForSessions(
		time.Date(day.Year(), day.Month(), day.Day(), 9, 31, 0, 0, loc),
		time.Date(day.Year(), day.Month(), day.Day(), 11, 30, 0, 0, loc),
	)

	issue := computeMinuteCoverageContinuityIssueForStockWithSuspensionFetch("301293.SZ", bars, sessions, true)
	if strings.TrimSpace(issue.Reason) != "" {
		t.Fatalf("expected suspended afternoon to be ignored, got %#v", issue)
	}
}

func TestMinuteBarsCoverTradingSessionsAllowsSuspendedEmptySession(t *testing.T) {
	oldFetchSuspensions := fetchDiemengSuspensionsFn
	clearDiemengSuspensionCache()
	t.Cleanup(func() {
		fetchDiemengSuspensionsFn = oldFetchSuspensions
		clearDiemengSuspensionCache()
	})
	fetchDiemengSuspensionsFn = func(stockCode string, tradeDate time.Time) ([]diemengSuspensionItem, error) {
		start := "13:00:00"
		end := "15:00:00"
		return []diemengSuspensionItem{
			{
				StockCode:        extractAShareSymbol(stockCode),
				SuspendDate:      tradeDate.In(cnLocation()).Format("2006-01-02"),
				SuspendStartTime: &start,
				SuspendEndTime:   &end,
			},
		}, nil
	}

	loc := cnLocation()
	day := time.Date(2026, 5, 29, 0, 0, 0, 0, loc)
	bars := minuteBarsForSessions(
		time.Date(day.Year(), day.Month(), day.Day(), 9, 31, 0, 0, loc),
		time.Date(day.Year(), day.Month(), day.Day(), 11, 30, 0, 0, loc),
	)
	if minuteBarsCoverTradingSessions(bars, time.Date(day.Year(), day.Month(), day.Day(), 9, 31, 0, 0, loc), time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, loc)) {
		t.Fatal("stock-agnostic coverage should still reject the missing afternoon")
	}
	if !minuteBarsCoverTradingSessionsForStockWithSuspensionFetch("301293.SZ", bars, time.Date(day.Year(), day.Month(), day.Day(), 9, 31, 0, 0, loc), time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, loc), true) {
		t.Fatal("expected stock-aware coverage to allow suspended afternoon")
	}
}

func TestCloseManualMinuteCoverageGaps_RetriesUntilRealGapCovered(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "manual-gap-close-retry.db"))
	if err := db.Dao.AutoMigrate(
		&models.AiRecommendStocks{},
		&models.AiRecommendYieldMeta{},
		&models.AiRecommendMinuteBar{},
		&models.AiRecommendYieldState{},
		&models.AiRecommendYieldRecordState{},
		&models.AiRecommendYieldOverride{},
		&Settings{},
	); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	oldNow := timeNow
	oldManualNow := manualMinuteCoverageNow
	oldManualSleep := manualMinuteCoverageSleep
	oldBackoffs := manualMinuteCoverageRetryBackoffs
	oldBudget := manualMinuteCoverageRetryBudget
	oldMaxRetryRounds := manualMinuteCoverageMaxRetryRounds
	oldTencent := fetchMinuteBarsWithTencentFn
	oldAkshare := fetchMinuteBarsWithAkShareFn
	oldSina := fetchMinuteBarsWithSinaFn
	oldDiemeng := fetchMinuteBarsWithDiemengFn
	t.Cleanup(func() {
		timeNow = oldNow
		manualMinuteCoverageNow = oldManualNow
		manualMinuteCoverageSleep = oldManualSleep
		manualMinuteCoverageRetryBackoffs = oldBackoffs
		manualMinuteCoverageRetryBudget = oldBudget
		manualMinuteCoverageMaxRetryRounds = oldMaxRetryRounds
		fetchMinuteBarsWithTencentFn = oldTencent
		fetchMinuteBarsWithAkShareFn = oldAkshare
		fetchMinuteBarsWithSinaFn = oldSina
		fetchMinuteBarsWithDiemengFn = oldDiemeng
	})

	loc := cnLocation()
	now := time.Date(2026, 5, 29, 15, 30, 0, 0, loc)
	timeNow = func() time.Time { return now }
	manualMinuteCoverageNow = func() time.Time { return now }
	manualMinuteCoverageSleep = func(time.Duration) {}
	manualMinuteCoverageRetryBackoffs = []time.Duration{0}
	manualMinuteCoverageRetryBudget = time.Minute
	manualMinuteCoverageMaxRetryRounds = 6

	meta := models.AiRecommendYieldMeta{CurrentTradeDate: "2026-05-29"}
	if err := db.Dao.Create(&meta).Error; err != nil {
		t.Fatalf("create meta failed: %v", err)
	}
	recordTime := time.Date(2026, 5, 29, 9, 40, 0, 0, loc)
	rec := models.AiRecommendStocks{
		DataTime:                    &recordTime,
		StockCode:                   "301293.SZ",
		StockName:                   "三博脑科",
		RecommendBuyPrice:           "10-10.5",
		RecommendStopProfitPrice:    "11-12",
		RecommendStopLossPrice:      "9.6",
		RecommendStatus:             "valid",
		RecommendCategory:           recommendExecutionImmediate,
		ActivationStatus:            "pending",
		RecommendBuyPriceMin:        10,
		RecommendBuyPriceMax:        10.5,
		RecommendStopProfitPriceMin: 11,
		RecommendStopProfitPriceMax: 12,
	}
	if err := db.Dao.Create(&rec).Error; err != nil {
		t.Fatalf("create recommend failed: %v", err)
	}
	for _, bar := range minuteBarsForSessions(
		time.Date(2026, 5, 28, 10, 30, 0, 0, loc),
		time.Date(2026, 5, 29, 15, 0, 0, 0, loc),
	) {
		if bar.TradeTime.Before(time.Date(2026, 5, 29, 10, 30, 0, 0, loc)) {
			continue
		}
		if err := db.Dao.Create(&models.AiRecommendMinuteBar{
			StockCode: "301293.SZ",
			TradeTime: bar.TradeTime,
			Open:      bar.Open,
			High:      bar.High,
			Low:       bar.Low,
			Close:     bar.Close,
			Volume:    bar.Volume,
			Amount:    bar.Amount,
			Source:    "seed",
		}).Error; err != nil {
			t.Fatalf("create seed minute bar failed: %v", err)
		}
	}

	providerCalls := 0
	fetch := func(tsCode string, start, end time.Time) ([]minuteBar, string, error) {
		providerCalls++
		if providerCalls <= 4 {
			return []minuteBar{}, "test", nil
		}
		return minuteBarsForSessions(start, end), "test", nil
	}
	fetchMinuteBarsWithTencentFn = fetch
	fetchMinuteBarsWithAkShareFn = fetch
	fetchMinuteBarsWithSinaFn = fetch
	fetchMinuteBarsWithDiemengFn = fetch

	stats, _ := computeMinuteDownloadCoverageStatsWithIssues(&meta, -1)
	if stats.Pending == 0 {
		t.Fatal("expected pending gap before retry closure")
	}
	err := closeManualMinuteCoverageGaps(&aiRecommendYieldRecalcRuntime{meta: &meta}, map[string]struct{}{"301293.SZ": struct{}{}})
	if err != nil {
		t.Fatalf("closeManualMinuteCoverageGaps failed: %v", err)
	}
	stats, _ = computeMinuteDownloadCoverageStatsWithIssues(&meta, -1)
	if stats.Pending != 0 || stats.Uncoverable != 0 || stats.Done != stats.Total {
		t.Fatalf("coverage stats after retry = %#v, want full coverage", stats)
	}
	if providerCalls <= 4 {
		t.Fatalf("expected retry after incomplete provider responses, calls=%d", providerCalls)
	}
}

func TestCloseManualMinuteCoverageGaps_MarksUncoverableAfterRetryExhausted(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "manual-gap-uncoverable.db"))
	if err := db.Dao.AutoMigrate(
		&models.AiRecommendStocks{},
		&models.AiRecommendYieldMeta{},
		&models.AiRecommendMinuteBar{},
		&models.AiRecommendYieldState{},
		&models.AiRecommendYieldRecordState{},
		&models.AiRecommendYieldOverride{},
		&Settings{},
	); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	oldNow := timeNow
	oldManualNow := manualMinuteCoverageNow
	oldManualSleep := manualMinuteCoverageSleep
	oldBackoffs := manualMinuteCoverageRetryBackoffs
	oldBudget := manualMinuteCoverageRetryBudget
	oldMaxRetryRounds := manualMinuteCoverageMaxRetryRounds
	oldTencent := fetchMinuteBarsWithTencentFn
	oldAkshare := fetchMinuteBarsWithAkShareFn
	oldSina := fetchMinuteBarsWithSinaFn
	oldDiemeng := fetchMinuteBarsWithDiemengFn
	t.Cleanup(func() {
		timeNow = oldNow
		manualMinuteCoverageNow = oldManualNow
		manualMinuteCoverageSleep = oldManualSleep
		manualMinuteCoverageRetryBackoffs = oldBackoffs
		manualMinuteCoverageRetryBudget = oldBudget
		manualMinuteCoverageMaxRetryRounds = oldMaxRetryRounds
		fetchMinuteBarsWithTencentFn = oldTencent
		fetchMinuteBarsWithAkShareFn = oldAkshare
		fetchMinuteBarsWithSinaFn = oldSina
		fetchMinuteBarsWithDiemengFn = oldDiemeng
	})

	loc := cnLocation()
	now := time.Date(2026, 5, 29, 15, 30, 0, 0, loc)
	timeNow = func() time.Time { return now }
	manualMinuteCoverageNow = func() time.Time { return now }
	manualMinuteCoverageSleep = func(time.Duration) {}
	manualMinuteCoverageRetryBackoffs = []time.Duration{0}
	manualMinuteCoverageRetryBudget = time.Minute
	manualMinuteCoverageMaxRetryRounds = 2

	meta := models.AiRecommendYieldMeta{CurrentTradeDate: "2026-05-29"}
	if err := db.Dao.Create(&meta).Error; err != nil {
		t.Fatalf("create meta failed: %v", err)
	}
	recordTime := time.Date(2026, 5, 29, 9, 40, 0, 0, loc)
	rec := models.AiRecommendStocks{
		DataTime:                    &recordTime,
		StockCode:                   "301293.SZ",
		StockName:                   "三博脑科",
		RecommendBuyPrice:           "10-10.5",
		RecommendStopProfitPrice:    "11-12",
		RecommendStopLossPrice:      "9.6",
		RecommendStatus:             "valid",
		RecommendCategory:           recommendExecutionImmediate,
		ActivationStatus:            "pending",
		RecommendBuyPriceMin:        10,
		RecommendBuyPriceMax:        10.5,
		RecommendStopProfitPriceMin: 11,
		RecommendStopProfitPriceMax: 12,
	}
	if err := db.Dao.Create(&rec).Error; err != nil {
		t.Fatalf("create recommend failed: %v", err)
	}

	fetch := func(tsCode string, start, end time.Time) ([]minuteBar, string, error) {
		return []minuteBar{}, "empty-test", nil
	}
	fetchMinuteBarsWithTencentFn = fetch
	fetchMinuteBarsWithAkShareFn = fetch
	fetchMinuteBarsWithSinaFn = fetch
	fetchMinuteBarsWithDiemengFn = fetch

	err := closeManualMinuteCoverageGaps(&aiRecommendYieldRecalcRuntime{meta: &meta}, map[string]struct{}{"301293.SZ": {}})
	if err != nil {
		t.Fatalf("closeManualMinuteCoverageGaps failed: %v", err)
	}
	stats, issues := computeMinuteDownloadCoverageStatsWithIssues(&meta, -1)
	if stats.Pending != 0 || stats.Uncoverable != 1 {
		t.Fatalf("coverage stats after exhausted retry = %#v issues=%#v, want pending=0 uncoverable=1", stats, issues)
	}
	if len(issues) != 1 || !strings.Contains(issues[0].RawReason, "连续2轮重试") {
		t.Fatalf("unexpected issues after exhausted retry: %#v", issues)
	}
}

func TestCloseManualMinuteCoverageGaps_MarksContinuityGapUncoverableAfterRetryExhausted(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "manual-gap-continuity-uncoverable.db"))
	if err := db.Dao.AutoMigrate(
		&models.AiRecommendStocks{},
		&models.AiRecommendYieldMeta{},
		&models.AiRecommendMinuteBar{},
		&models.AiRecommendYieldState{},
		&models.AiRecommendYieldRecordState{},
		&models.AiRecommendYieldOverride{},
		&Settings{},
	); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	oldNow := timeNow
	oldManualNow := manualMinuteCoverageNow
	oldManualSleep := manualMinuteCoverageSleep
	oldBackoffs := manualMinuteCoverageRetryBackoffs
	oldBudget := manualMinuteCoverageRetryBudget
	oldMaxRetryRounds := manualMinuteCoverageMaxRetryRounds
	oldTencent := fetchMinuteBarsWithTencentFn
	oldAkshare := fetchMinuteBarsWithAkShareFn
	oldSina := fetchMinuteBarsWithSinaFn
	oldDiemeng := fetchMinuteBarsWithDiemengFn
	t.Cleanup(func() {
		timeNow = oldNow
		manualMinuteCoverageNow = oldManualNow
		manualMinuteCoverageSleep = oldManualSleep
		manualMinuteCoverageRetryBackoffs = oldBackoffs
		manualMinuteCoverageRetryBudget = oldBudget
		manualMinuteCoverageMaxRetryRounds = oldMaxRetryRounds
		fetchMinuteBarsWithTencentFn = oldTencent
		fetchMinuteBarsWithAkShareFn = oldAkshare
		fetchMinuteBarsWithSinaFn = oldSina
		fetchMinuteBarsWithDiemengFn = oldDiemeng
	})

	loc := cnLocation()
	now := time.Date(2026, 5, 29, 15, 30, 0, 0, loc)
	timeNow = func() time.Time { return now }
	manualMinuteCoverageNow = func() time.Time { return now }
	manualMinuteCoverageSleep = func(time.Duration) {}
	manualMinuteCoverageRetryBackoffs = []time.Duration{0}
	manualMinuteCoverageRetryBudget = time.Minute
	manualMinuteCoverageMaxRetryRounds = 2

	meta := models.AiRecommendYieldMeta{CurrentTradeDate: "2026-05-29"}
	if err := db.Dao.Create(&meta).Error; err != nil {
		t.Fatalf("create meta failed: %v", err)
	}
	recordTime := time.Date(2026, 5, 29, 9, 40, 0, 0, loc)
	rec := models.AiRecommendStocks{
		DataTime:                    &recordTime,
		StockCode:                   "301293.SZ",
		StockName:                   "三博脑科",
		RecommendBuyPrice:           "10-10.5",
		RecommendStopProfitPrice:    "11-12",
		RecommendStopLossPrice:      "9.6",
		RecommendStatus:             "valid",
		RecommendCategory:           recommendExecutionImmediate,
		ActivationStatus:            "pending",
		RecommendBuyPriceMin:        10,
		RecommendBuyPriceMax:        10.5,
		RecommendStopProfitPriceMin: 11,
		RecommendStopProfitPriceMax: 12,
	}
	if err := db.Dao.Create(&rec).Error; err != nil {
		t.Fatalf("create recommend failed: %v", err)
	}

	gapStart := time.Date(2026, 5, 29, 10, 0, 0, 0, loc)
	gapEnd := time.Date(2026, 5, 29, 10, 10, 0, 0, loc)
	for _, bar := range minuteBarsForSessions(
		time.Date(2026, 5, 28, 9, 31, 0, 0, loc),
		time.Date(2026, 5, 29, 15, 0, 0, 0, loc),
	) {
		if !bar.TradeTime.Before(gapStart) && !bar.TradeTime.After(gapEnd) {
			continue
		}
		if err := db.Dao.Create(&models.AiRecommendMinuteBar{
			StockCode: "301293.SZ",
			TradeTime: bar.TradeTime,
			Open:      bar.Open,
			High:      bar.High,
			Low:       bar.Low,
			Close:     bar.Close,
			Volume:    bar.Volume,
			Amount:    bar.Amount,
			Source:    "seed",
		}).Error; err != nil {
			t.Fatalf("create seed minute bar failed: %v", err)
		}
	}

	fetch := func(tsCode string, start, end time.Time) ([]minuteBar, string, error) {
		return []minuteBar{}, "empty-test", nil
	}
	fetchMinuteBarsWithTencentFn = fetch
	fetchMinuteBarsWithAkShareFn = fetch
	fetchMinuteBarsWithSinaFn = fetch
	fetchMinuteBarsWithDiemengFn = fetch

	stats, issues := computeMinuteDownloadCoverageStatsWithIssues(&meta, -1)
	if stats.Pending != 1 || stats.Uncoverable != 0 {
		t.Fatalf("coverage stats before exhausted retry = %#v issues=%#v, want pending=1 uncoverable=0", stats, issues)
	}
	err := closeManualMinuteCoverageGaps(&aiRecommendYieldRecalcRuntime{meta: &meta}, map[string]struct{}{"301293.SZ": {}})
	if err != nil {
		t.Fatalf("closeManualMinuteCoverageGaps failed: %v", err)
	}
	stats, issues = computeMinuteDownloadCoverageStatsWithIssues(&meta, -1)
	if stats.Pending != 0 || stats.Uncoverable != 1 {
		t.Fatalf("coverage stats after exhausted continuity retry = %#v issues=%#v, want pending=0 uncoverable=1", stats, issues)
	}
	if len(issues) != 1 || !strings.Contains(issues[0].RawReason, "连续2轮重试") {
		t.Fatalf("unexpected continuity issues after exhausted retry: %#v", issues)
	}
}

func minuteBarsForSessions(start, end time.Time) []minuteBar {
	sessions := buildMinuteCoverageSessions(start, end)
	bars := make([]minuteBar, 0, 256)
	for _, session := range sessions {
		for ts := session.Start; !ts.After(session.End); ts = ts.Add(time.Minute) {
			bars = append(bars, minuteBar{
				TradeTime: ts,
				Open:      10,
				High:      10,
				Low:       10,
				Close:     10,
				Volume:    100,
				Amount:    1000,
			})
		}
	}
	return bars
}
