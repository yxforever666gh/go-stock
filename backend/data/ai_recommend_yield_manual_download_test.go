package data

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
)

func TestEnsureYieldMetaSchema_AddsManualAuditColumnsToLegacyTable(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "yield-meta-legacy.db"))
	if err := db.Dao.AutoMigrate(&models.AiRecommendStocks{}); err != nil {
		t.Fatalf("auto migrate recommend table failed: %v", err)
	}
	if err := db.Dao.Exec(`CREATE TABLE ai_recommend_yield_meta (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at datetime,
		updated_at datetime,
		last_full_recalc_at datetime,
		last_yield_email_sent_at datetime,
		last_yield_email_sent_reason TEXT,
		last_query_recalc_at datetime,
		query_cooldown_until datetime,
		last_manual_download_at datetime,
		manual_cooldown_until datetime,
		recalc_in_progress numeric,
		recalc_total INTEGER,
		recalc_done INTEGER,
		recalc_progress INTEGER,
		last_error TEXT,
		current_trade_date TEXT,
		akshare_ready numeric,
		akshare_checked_at datetime,
		akshare_install_error TEXT,
		frozen_sell_price_fix_version TEXT,
		download_in_progress numeric,
		download_total INTEGER,
		download_done INTEGER,
		download_progress INTEGER,
		last_download_error TEXT
	)`).Error; err != nil {
		t.Fatalf("create legacy meta table failed: %v", err)
	}

	if err := ensureYieldMetaSchema(); err != nil {
		t.Fatalf("ensureYieldMetaSchema failed: %v", err)
	}

	type pragmaRow struct {
		Name string
	}
	rows := make([]pragmaRow, 0, 32)
	if err := db.Dao.Raw("PRAGMA table_info(ai_recommend_yield_meta)").Scan(&rows).Error; err != nil {
		t.Fatalf("load table info failed: %v", err)
	}
	names := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		names[row.Name] = struct{}{}
	}
	for _, col := range []string{
		"last_manual_finished_at",
		"last_manual_scope_count",
		"last_manual_prefetch_ms",
		"last_manual_recalc_ms",
		"last_manual_total_ms",
		"last_manual_sqlite_busy_count",
		"last_manual_provider_summary",
	} {
		if _, ok := names[col]; !ok {
			t.Fatalf("expected column %s to be added", col)
		}
	}
}

func TestPersistManualYieldAudit_WritesFinishedSummary(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "yield-manual-audit-persist.db"))
	if err := db.Dao.AutoMigrate(&models.AiRecommendStocks{}); err != nil {
		t.Fatalf("auto migrate recommend table failed: %v", err)
	}
	if err := ensureYieldMetaSchema(); err != nil {
		t.Fatalf("ensureYieldMetaSchema failed: %v", err)
	}

	meta := models.AiRecommendYieldMeta{}
	if err := db.Dao.Create(&meta).Error; err != nil {
		t.Fatalf("create meta failed: %v", err)
	}

	started := time.Date(2026, 4, 15, 8, 25, 2, 0, cnLocation())
	audit := newAiRecommendYieldManualAudit(started, 12)
	audit.markPrefetchStart(started)
	audit.markPrefetchDone(started.Add(45 * time.Second))
	audit.markRecalcStart(started.Add(45 * time.Second))
	audit.markRecalcDone(started.Add(2*time.Minute + 15*time.Second))
	audit.incrementSQLiteBusy()
	audit.recordProvider("tencent")
	audit.recordProvider("diemeng")
	audit.markFinished(started.Add(2*time.Minute + 20*time.Second))

	if err := persistManualYieldAudit(meta.ID, audit); err != nil {
		t.Fatalf("persistManualYieldAudit failed: %v", err)
	}

	var got models.AiRecommendYieldMeta
	if err := db.Dao.First(&got, meta.ID).Error; err != nil {
		t.Fatalf("reload meta failed: %v", err)
	}
	if got.LastManualFinishedAt == nil || !got.LastManualFinishedAt.Equal(started.Add(2*time.Minute+20*time.Second)) {
		t.Fatalf("unexpected LastManualFinishedAt: %v", got.LastManualFinishedAt)
	}
	if got.LastManualScopeCount != 12 {
		t.Fatalf("LastManualScopeCount=%d, want 12", got.LastManualScopeCount)
	}
	if got.LastManualPrefetchMs != int64(45*time.Second/time.Millisecond) {
		t.Fatalf("LastManualPrefetchMs=%d", got.LastManualPrefetchMs)
	}
	if got.LastManualRecalcMs != int64(90*time.Second/time.Millisecond) {
		t.Fatalf("LastManualRecalcMs=%d", got.LastManualRecalcMs)
	}
	if got.LastManualTotalMs != int64((2*time.Minute+20*time.Second)/time.Millisecond) {
		t.Fatalf("LastManualTotalMs=%d", got.LastManualTotalMs)
	}
	if got.LastManualSqliteBusyCount != 1 {
		t.Fatalf("LastManualSqliteBusyCount=%d, want 1", got.LastManualSqliteBusyCount)
	}
	if got.LastManualProviderSummary != "diemeng:1, tencent:1" {
		t.Fatalf("LastManualProviderSummary=%q", got.LastManualProviderSummary)
	}
}

func TestMinuteCoverageContinuityIssueDetectsIntradayGap(t *testing.T) {
	loc := cnLocation()
	day := time.Date(2026, 5, 12, 0, 0, 0, 0, loc)
	sessions := buildMinuteCoverageSessions(
		time.Date(day.Year(), day.Month(), day.Day(), 9, 31, 0, 0, loc),
		time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, loc),
	)
	bars := []minuteBar{
		{TradeTime: time.Date(day.Year(), day.Month(), day.Day(), 14, 0, 0, 0, loc)},
		{TradeTime: time.Date(day.Year(), day.Month(), day.Day(), 14, 1, 0, 0, loc)},
		{TradeTime: time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, loc)},
	}

	reason := minuteCoverageContinuityIssue(bars, sessions)
	if reason == "" {
		t.Fatalf("expected intraday minute gap to be detected")
	}
	if !strings.Contains(reason, "09:31") {
		t.Fatalf("expected morning gap reason, got %q", reason)
	}
}

func TestMinuteCoverageContinuityIssueReturnsMissingWindow(t *testing.T) {
	loc := cnLocation()
	day := time.Date(2026, 5, 12, 0, 0, 0, 0, loc)
	sessions := buildMinuteCoverageSessions(
		time.Date(day.Year(), day.Month(), day.Day(), 9, 31, 0, 0, loc),
		time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, loc),
	)
	bars := []minuteBar{
		{TradeTime: time.Date(day.Year(), day.Month(), day.Day(), 11, 0, 0, 0, loc)},
		{TradeTime: time.Date(day.Year(), day.Month(), day.Day(), 11, 1, 0, 0, loc)},
		{TradeTime: time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, loc)},
	}

	issue := computeMinuteCoverageContinuityIssue(bars, sessions)
	if issue.Kind != "session_late_first" {
		t.Fatalf("kind=%q, want session_late_first; reason=%q", issue.Kind, issue.Reason)
	}
	wantStart := time.Date(day.Year(), day.Month(), day.Day(), 9, 31, 0, 0, loc)
	wantEnd := time.Date(day.Year(), day.Month(), day.Day(), 10, 59, 0, 0, loc)
	if !issue.MissingStart.Equal(wantStart) || !issue.MissingEnd.Equal(wantEnd) {
		t.Fatalf("missing window=%v~%v, want %v~%v", issue.MissingStart, issue.MissingEnd, wantStart, wantEnd)
	}
}

func TestMergeMinuteCoverageTasks_MergesAdjacentForcedWindows(t *testing.T) {
	loc := cnLocation()
	tasks := []aiRecommendMinuteCoverageTask{
		{
			StockCode: "002747.SZ",
			Start:     time.Date(2026, 5, 12, 9, 31, 0, 0, loc),
			End:       time.Date(2026, 5, 12, 10, 0, 0, 0, loc),
			Forced:    true,
		},
		{
			StockCode: "002747.SZ",
			Start:     time.Date(2026, 5, 12, 10, 1, 0, 0, loc),
			End:       time.Date(2026, 5, 12, 11, 30, 0, 0, loc),
			Forced:    true,
		},
		{
			StockCode: "688017.SH",
			Start:     time.Date(2026, 5, 12, 9, 31, 0, 0, loc),
			End:       time.Date(2026, 5, 12, 11, 30, 0, 0, loc),
			Forced:    true,
		},
	}

	merged := mergeMinuteCoverageTasks(tasks)
	if len(merged) != 2 {
		t.Fatalf("merged len=%d, want 2: %#v", len(merged), merged)
	}
	if merged[0].StockCode != "002747.SZ" || !merged[0].End.Equal(time.Date(2026, 5, 12, 11, 30, 0, 0, loc)) {
		t.Fatalf("first merged task unexpected: %#v", merged[0])
	}
	if !merged[0].Forced {
		t.Fatal("expected merged task to remain forced")
	}
}

func TestBuildAiRecommendMinuteCoverageTasks_ManualDownloadExtendsStartForPrevDayActivity(t *testing.T) {
	loc := cnLocation()
	now := time.Date(2026, 3, 10, 15, 10, 0, 0, loc)
	recordTime := time.Date(2026, 3, 10, 9, 30, 0, 0, loc)

	targets := &aiRecommendYieldTargets{
		aggrMap: map[string]*aiRecommendYieldAggregate{
			"300274.SZ": {
				StockCode:                    "300274.SZ",
				SignalTime:                   recordTime,
				RequirePrevDayActivityFilter: true,
			},
		},
		targetCodes: []string{"300274.SZ"},
		targetRecords: []models.AiRecommendStocks{
			{
				StockCode: "300274.SZ",
				DataTime:  &recordTime,
				BuySignal: "5分钟成交额不能低于上一交易日同一时刻活跃度",
				ModelName: "gpt-5.4",
				StockName: "阳光电源",
				BkName:    "光伏设备",
			},
		},
	}
	runtime := &aiRecommendYieldRecalcRuntime{
		now:        now,
		inTrading:  false,
		latestDate: time.Date(2026, 3, 10, 0, 0, 0, 0, loc),
		ctx: yieldBuildContext{
			Reason:           "manual_minute_download",
			Now:              now,
			InTradingSession: false,
			LatestTradeDate:  time.Date(2026, 3, 10, 0, 0, 0, 0, loc),
		},
	}

	tasks := buildAiRecommendMinuteCoverageTasks(runtime, targets)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	gotStart := tasks[0].Start
	wantStart := time.Date(2026, 3, 9, 9, 31, 0, 0, loc)
	if !gotStart.Equal(wantStart) {
		t.Fatalf("expected start=%v, got %v", wantStart, gotStart)
	}
}

func TestBuildAiRecommendMinuteCoverageTasks_ManualDownloadExtendsStartForNormalRecommend(t *testing.T) {
	loc := cnLocation()
	now := time.Date(2026, 3, 10, 15, 10, 0, 0, loc)
	recordTime := time.Date(2026, 3, 10, 9, 30, 0, 0, loc)

	targets := &aiRecommendYieldTargets{
		aggrMap: map[string]*aiRecommendYieldAggregate{
			"300274.SZ": {
				StockCode:  "300274.SZ",
				SignalTime: recordTime,
			},
		},
		targetCodes: []string{"300274.SZ"},
		targetRecords: []models.AiRecommendStocks{
			{
				StockCode: "300274.SZ",
				DataTime:  &recordTime,
				BuySignal: "回到10.00-10.08主买入区即可",
			},
		},
	}
	runtime := &aiRecommendYieldRecalcRuntime{
		now:        now,
		inTrading:  false,
		latestDate: time.Date(2026, 3, 10, 0, 0, 0, 0, loc),
		ctx: yieldBuildContext{
			Reason:           "manual_minute_download",
			Now:              now,
			InTradingSession: false,
			LatestTradeDate:  time.Date(2026, 3, 10, 0, 0, 0, 0, loc),
		},
	}

	tasks := buildAiRecommendMinuteCoverageTasks(runtime, targets)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	gotStart := tasks[0].Start
	wantStart := time.Date(2026, 3, 9, 9, 31, 0, 0, loc)
	if !gotStart.Equal(wantStart) {
		t.Fatalf("expected start=%v, got %v", wantStart, gotStart)
	}
}

func TestBuildRecalcTargets_ForceKeepsExplicitScope(t *testing.T) {
	scope := normalizeScopeCodes([]string{"002297.SZ"})
	codes := buildRecalcTargetCodes([]string{"002297.SZ", "300274.SZ"}, scope, true)
	if len(codes) != 1 || codes[0] != "002297.SZ" {
		t.Fatalf("target codes = %#v, want scoped 002297.SZ", codes)
	}

	now := time.Date(2026, 4, 29, 9, 40, 0, 0, cnLocation())
	records := buildRecalcTargetRecords([]models.AiRecommendStocks{
		{StockCode: "002297.SZ", DataTime: &now},
		{StockCode: "300274.SZ", DataTime: &now},
	}, scope, true)
	if len(records) != 1 || records[0].StockCode != "002297.SZ" {
		t.Fatalf("target records = %#v, want scoped 002297.SZ", records)
	}
}

func TestIsFullAiRecommendYieldRecalc_ForceWithScopeIsNotFull(t *testing.T) {
	scope := normalizeScopeCodes([]string{"002297.SZ"})
	targets := &aiRecommendYieldTargets{
		allCodes:      []string{"002297.SZ", "300274.SZ"},
		targetCodes:   []string{"002297.SZ"},
		records:       []models.AiRecommendStocks{{StockCode: "002297.SZ"}, {StockCode: "300274.SZ"}},
		targetRecords: []models.AiRecommendStocks{{StockCode: "002297.SZ"}},
	}
	if isFullAiRecommendYieldRecalc(true, scope, targets) {
		t.Fatal("force scoped manual recalc should not be treated as full recalc")
	}
	if !isFullAiRecommendYieldRecalc(true, nil, targets) {
		t.Fatal("force without scope should be treated as full recalc")
	}
}

func TestRecalcManagerPendingForceKeepsScopeAndReason(t *testing.T) {
	manager := &aiRecommendYieldRecalcManager{running: true}
	scope := normalizeScopeCodes([]string{"002297.SZ"})

	manager.Request(true, "manual_minute_download", scope)

	if !manager.pending {
		t.Fatal("expected pending request")
	}
	if !manager.pendingForce {
		t.Fatal("expected pending force")
	}
	if manager.pendingReason != "manual_minute_download" {
		t.Fatalf("pending reason = %s, want manual_minute_download", manager.pendingReason)
	}
	if len(manager.pendingScope) != 1 {
		t.Fatalf("pending scope = %#v, want one code", manager.pendingScope)
	}
	if _, ok := manager.pendingScope["002297.SZ"]; !ok {
		t.Fatalf("pending scope = %#v, want 002297.SZ", manager.pendingScope)
	}
}

func TestLoadAiRecommendYieldTargets_ManualDownloadSkipsRealtimePriceFetch(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "manual-targets-skip-realtime.db"))
	if err := db.Dao.AutoMigrate(
		&models.AiRecommendStocks{},
		&models.AiRecommendYieldState{},
		&models.AiRecommendYieldRecordState{},
		&models.AiRecommendMinuteBar{},
	); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	loc := cnLocation()
	now := time.Date(2026, 4, 30, 15, 30, 0, 0, loc)
	recordTime := time.Date(2026, 4, 29, 9, 40, 0, 0, loc)
	rec := models.AiRecommendStocks{
		DataTime:                    &recordTime,
		StockCode:                   "002297.SZ",
		StockName:                   "博云新材",
		StockPrice:                  "21.86",
		RecommendBuyPrice:           "21.30-21.90",
		RecommendBuyPriceMin:        21.3,
		RecommendBuyPriceMax:        21.9,
		RecommendStopProfitPrice:    "23.30-24.20",
		RecommendStopProfitPriceMin: 23.3,
		RecommendStopProfitPriceMax: 24.2,
		RecommendStopLossPrice:      "20.80",
		RecommendStatus:             "valid",
		ExecutionState:              recommendExecutionConditional,
		ActivationRuleSource:        "market_summary",
		ActivationRuleJSON:          `{"version":"v3","mode":"any_of","paths":[{"name":"pullback","signalType":"price_range_with_volume","thresholdValue":21.3,"thresholdMax":21.9,"volumeRatio":1.15,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount"}]}`,
		ActivationStatus:            "pending",
	}
	if err := db.Dao.Create(&rec).Error; err != nil {
		t.Fatalf("create record failed: %v", err)
	}

	oldFetch := fetchCurrentPriceMapFn
	called := false
	fetchCurrentPriceMapFn = func(aggrMap map[string]*aiRecommendYieldAggregate) (map[string]float64, map[string]string) {
		called = true
		return map[string]float64{}, map[string]string{}
	}
	t.Cleanup(func() {
		fetchCurrentPriceMapFn = oldFetch
	})

	runtime := &aiRecommendYieldRecalcRuntime{
		meta:       &models.AiRecommendYieldMeta{ID: 1},
		now:        now,
		inTrading:  false,
		latestDate: time.Date(2026, 4, 30, 0, 0, 0, 0, loc),
		ctx: yieldBuildContext{
			Reason:             "manual_minute_download",
			Now:                now,
			InTradingSession:   false,
			LatestTradeDate:    time.Date(2026, 4, 30, 0, 0, 0, 0, loc),
			DisableMinuteFetch: true,
		},
	}
	targets, err := loadAiRecommendYieldTargets(runtime, normalizeScopeCodes([]string{"002297.SZ"}), true)
	if err != nil {
		t.Fatalf("load targets failed: %v", err)
	}
	if len(targets.targetCodes) != 1 || targets.targetCodes[0] != "002297.SZ" {
		t.Fatalf("target codes = %#v, want 002297.SZ", targets.targetCodes)
	}
	if called {
		t.Fatal("manual minute download should not fetch realtime prices while loading targets")
	}
}

func TestFilterManualDownloadScopeCodes_SkipsTerminalAndAnalysisOnlyRecords(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "manual-scope-filter.db"))
	if err := db.Dao.AutoMigrate(&models.AiRecommendStocks{}, &models.AiRecommendYieldRecordState{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	now := time.Date(2026, 4, 15, 9, 45, 0, 0, cnLocation())
	ruleJSON := `{"signalType":"price_range_with_volume","evaluationWindow":"5m","baseline":"manual_amount","operator":">=","thresholdValue":10,"thresholdMax":10.5,"volumeRatio":1,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5}`
	rows := []models.AiRecommendStocks{
		{
			StockCode:                "300001.SZ",
			StockName:                "特锐德",
			RecommendCategory:        "conditional",
			ExecutionState:           recommendExecutionAnalysisOnly,
			RecommendBuyPrice:        "18.90-19.10",
			BuySignal:                "价格触发：回到18.90-19.10主买入区；量能触发：5分钟成交额不低于100万",
			SellSignal:               "触及20.50止盈区间卖出；若跌破18.80止损位立即止损",
			InvalidSignal:            "时间失效：未来5个交易日内仍未触发主买入区；价格失效：任一5分钟收盘价跌破18.80",
			ActivationRuleJSON:       ruleJSON,
			RecommendStopLossPrice:   "18.80",
			RecommendStopProfitPrice: "20.50",
			ActivationStatus:         "pending",
			DataTime:                 &now,
		},
		{
			StockCode:                "300002.SZ",
			StockName:                "神州泰岳",
			RecommendCategory:        "conditional",
			ExecutionState:           recommendExecutionConditional,
			RecommendBuyPrice:        "9.90-10.10",
			BuySignal:                "价格触发：回到9.90-10.10主买入区；量能触发：5分钟成交额不低于100万",
			SellSignal:               "触及10.80止盈区间卖出；若跌破9.80止损位立即止损",
			InvalidSignal:            "时间失效：未来5个交易日内仍未触发主买入区；价格失效：任一5分钟收盘价跌破9.80",
			ActivationRuleJSON:       ruleJSON,
			RecommendStopLossPrice:   "9.80",
			RecommendStopProfitPrice: "10.80",
			ActivationStatus:         "skipped",
			DataTime:                 &now,
		},
		{
			StockCode:                "300003.SZ",
			StockName:                "乐普医疗",
			RecommendCategory:        "conditional",
			ExecutionState:           recommendExecutionConditional,
			RecommendBuyPrice:        "15.10-15.30",
			BuySignal:                "价格触发：回到15.10-15.30主买入区；量能触发：5分钟成交额不低于100万",
			SellSignal:               "触及16.80止盈区间卖出；若跌破14.80止损位立即止损",
			InvalidSignal:            "时间失效：未来5个交易日内仍未触发主买入区；价格失效：任一5分钟收盘价跌破14.80",
			ActivationRuleJSON:       ruleJSON,
			RecommendStopLossPrice:   "14.80",
			RecommendStopProfitPrice: "16.80",
			ActivationStatus:         "pending",
			DataTime:                 &now,
		},
		{
			StockCode:                "300004.SZ",
			StockName:                "南风股份",
			RecommendCategory:        "conditional",
			RecommendStatus:          "missing_market_data",
			ExecutionState:           recommendExecutionAnalysisOnly,
			RecommendBuyPrice:        "",
			BuySignal:                "缺少可信实时价格/量能数据，本次仅保留逻辑分析，不生成交易计划",
			ActivationRuleJSON:       ruleJSON,
			ActivationRuleSource:     "market_summary",
			RecommendStopLossPrice:   "",
			RecommendStopProfitPrice: "",
			ActivationStatus:         "skipped",
			Remarks:                  "等待激活；激活条件：pullback：价格进入10.00-10.50区间，5分钟成交额不低于近5个5分钟平均成交额的1.15倍；止盈区间：11.20-11.80；止损位：9.60",
			DataTime:                 &now,
		},
		{
			StockCode:                "300005.SZ",
			StockName:                "探路者",
			SummaryVersion:           marketSummaryVersionV132,
			RecommendCategory:        "conditional",
			ExecutionState:           recommendExecutionConditional,
			RecommendBuyPrice:        "20.00-20.20",
			BuySignal:                "价格触发：回到20.00-20.20主买入区；量能触发：5分钟成交额不低于100万",
			SellSignal:               "触及22.50止盈区间卖出；若跌破19.50止损位立即止损",
			InvalidSignal:            "时间失效：未来5个交易日内仍未触发主买入区",
			ActivationRuleJSON:       ruleJSON,
			RecommendStopLossPrice:   "19.50",
			RecommendStopProfitPrice: "22.50",
			ActivationStatus:         "skipped",
			DataTime:                 &now,
		},
	}
	for _, row := range rows {
		if err := db.Dao.Create(&row).Error; err != nil {
			t.Fatalf("create recommend failed: %v", err)
		}
	}
	if err := db.Dao.Create(&models.AiRecommendYieldRecordState{
		RecommendID:      2,
		StockCode:        "300002.SZ",
		ActivationStatus: "skipped",
		DataStatus:       "已跳过",
		DataStatusReason: "已终态",
	}).Error; err != nil {
		t.Fatalf("create skipped record state failed: %v", err)
	}
	if err := db.Dao.Create(&models.AiRecommendYieldRecordState{
		RecommendID:      5,
		StockCode:        "300005.SZ",
		ActivationStatus: "skipped",
		DataStatus:       "已跳过",
		DataStatusReason: "V1.3.2强弱过滤未通过：激活价 20.10 低于 VWAP 2010.00",
	}).Error; err != nil {
		t.Fatalf("create v132 skipped record state failed: %v", err)
	}

	got, err := filterManualDownloadScopeCodes([]string{"300001.SZ", "300002.SZ", "300003.SZ", "300004.SZ", "300005.SZ"})
	if err != nil {
		t.Fatalf("filterManualDownloadScopeCodes failed: %v", err)
	}
	if len(got) != 3 || got[0] != "300003.SZ" || got[1] != "300004.SZ" || got[2] != "300005.SZ" {
		t.Fatalf("unexpected filtered scope: %#v", got)
	}
}

func TestLoadManualDownloadScopeCodesByRecoverablePlans_IncludesMissingExitPlan(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "manual-download-recoverable-plan.db"))
	if err := db.Dao.AutoMigrate(&models.AiRecommendStocks{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}
	loc := cnLocation()
	dataTime := time.Date(2026, 4, 29, 9, 40, 0, 0, loc)
	row := models.AiRecommendStocks{
		DataTime:                 &dataTime,
		StockCode:                "002297.SZ",
		StockName:                "博云新材",
		RecommendStatus:          "valid",
		ExecutionState:           recommendExecutionConditional,
		ActivationStatus:         "pending",
		ActivationRuleSource:     "market_summary",
		ActivationRuleJSON:       `{"version":"v3","mode":"any_of","paths":[{"name":"pullback","signalType":"price_range_with_volume","thresholdValue":21.3,"thresholdMax":21.9},{"name":"breakout","signalType":"price_breakout_with_volume","thresholdValue":22.65}]}`,
		RecommendBuyPrice:        "21.3-21.9",
		RecommendBuyPriceMin:     21.3,
		RecommendBuyPriceMax:     21.9,
		RecommendStopProfitPrice: "",
		RecommendStopLossPrice:   "",
	}
	if err := db.Dao.Create(&row).Error; err != nil {
		t.Fatalf("create row failed: %v", err)
	}

	got, err := loadManualDownloadScopeCodesByRecoverablePlans()
	if err != nil {
		t.Fatalf("loadManualDownloadScopeCodesByRecoverablePlans failed: %v", err)
	}
	if len(got) != 1 || got[0] != "002297.SZ" {
		t.Fatalf("scope codes = %#v, want 002297.SZ", got)
	}
}

func TestManualYieldAuditSnapshot_FormatsDurationsAndProviders(t *testing.T) {
	started := time.Date(2026, 4, 15, 8, 25, 2, 0, cnLocation())
	audit := newAiRecommendYieldManualAudit(started, 99)
	audit.markPrefetchStart(started)
	audit.markPrefetchDone(started.Add(90 * time.Second))
	audit.markRecalcStart(started.Add(90 * time.Second))
	audit.markRecalcDone(started.Add(4 * time.Minute))
	audit.recordProvider("tencent")
	audit.recordProvider("diemeng")
	audit.recordProvider("tencent")
	audit.incrementSQLiteBusy()
	audit.markFinished(started.Add(4*time.Minute + 18*time.Second))

	snapshot := audit.snapshot()
	if snapshot.ScopeCount != 99 {
		t.Fatalf("scopeCount=%d, want 99", snapshot.ScopeCount)
	}
	if snapshot.PrefetchMs != int64(90*time.Second/time.Millisecond) {
		t.Fatalf("prefetchMs=%d", snapshot.PrefetchMs)
	}
	if snapshot.RecalcMs != int64(150*time.Second/time.Millisecond) {
		t.Fatalf("recalcMs=%d", snapshot.RecalcMs)
	}
	if snapshot.TotalMs != int64((4*time.Minute+18*time.Second)/time.Millisecond) {
		t.Fatalf("totalMs=%d", snapshot.TotalMs)
	}
	if snapshot.SQLiteBusyCount != 1 {
		t.Fatalf("sqliteBusyCount=%d, want 1", snapshot.SQLiteBusyCount)
	}
	if snapshot.ProviderSummary != "diemeng:1, tencent:2" {
		t.Fatalf("providerSummary=%q", snapshot.ProviderSummary)
	}
}
