package data

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
)

func TestBuildActivationRuleFromRecommend_MarketSummaryBuildsDualPath(t *testing.T) {
	rec := &models.AiRecommendStocks{
		StockCode:                   "300308.SZ",
		StockName:                   "中际旭创",
		StockPrice:                  "170",
		StockCurrentPrice:           "170",
		ActivationRuleSource:        "market_summary",
		RecommendBuyPrice:           "168-172",
		RecommendBuyPriceMin:        168,
		RecommendBuyPriceMax:        172,
		RecommendStopProfitPrice:    "180-188",
		RecommendStopProfitPriceMin: 180,
		RecommendStopProfitPriceMax: 188,
		RecommendStopLossPrice:      "163",
		BuySignal:                   "价格触发：未来3个交易日内股价回到168-172元区间后，连续2根15分钟K线收于170元上方；量能触发：对应15分钟成交额≥近5个15分钟均额的1.3倍，且量比≥1.5；逻辑触发：AI算力主线未证伪",
	}

	rule, err := buildActivationRuleFromRecommend(rec)
	if err != nil {
		t.Fatalf("buildActivationRuleFromRecommend failed: %v", err)
	}
	if rule.Version != activationRuleVersionV3 {
		t.Fatalf("version = %s, want %s", rule.Version, activationRuleVersionV3)
	}
	if rule.Mode != activationRuleModeAnyOf {
		t.Fatalf("mode = %s, want %s", rule.Mode, activationRuleModeAnyOf)
	}
	if len(rule.Paths) != 2 {
		t.Fatalf("paths len = %d, want 2", len(rule.Paths))
	}
	if rule.Paths[0].Name != "pullback" || rule.Paths[0].SignalType != "price_range_with_volume" {
		t.Fatalf("unexpected pullback path: %+v", rule.Paths[0])
	}
	if rule.Paths[0].EvaluationWindow != "15m" {
		t.Fatalf("pullback window = %s, want 15m", rule.Paths[0].EvaluationWindow)
	}
	if rule.Paths[0].ConfirmBars != 1 {
		t.Fatalf("pullback confirmBars = %d, want 1", rule.Paths[0].ConfirmBars)
	}
	if rule.Paths[0].VolumeRatio != 1.15 {
		t.Fatalf("pullback volumeRatio = %.2f, want 1.15", rule.Paths[0].VolumeRatio)
	}
	if rule.Paths[0].OpeningPolicy == nil || rule.Paths[0].OpeningPolicy.MorningBufferUntil != openingReviewPhase0940 {
		t.Fatalf("expected pullback opening policy, got %+v", rule.Paths[0].OpeningPolicy)
	}
	if rule.Paths[1].Name != "breakout" || rule.Paths[1].SignalType != "price_breakout_with_volume" {
		t.Fatalf("unexpected breakout path: %+v", rule.Paths[1])
	}
	if rule.Paths[1].ConfirmBars != 1 {
		t.Fatalf("breakout confirmBars = %d, want 1", rule.Paths[1].ConfirmBars)
	}
	if rule.Paths[1].VolumeRatio != 1.2 {
		t.Fatalf("breakout volumeRatio = %.2f, want 1.2", rule.Paths[1].VolumeRatio)
	}
	if rule.Paths[1].OpeningPolicy == nil || rule.Paths[1].OpeningPolicy.MorningBufferUntil != openingReviewPhase0940 {
		t.Fatalf("expected breakout opening policy, got %+v", rule.Paths[1].OpeningPolicy)
	}
}

func TestExtractActivationBreakoutThreshold_IgnoresIndicatorNumbers(t *testing.T) {
	value, ok := extractActivationBreakoutThreshold("价格触发：回踩路径为未来5个交易日内任一5分钟K线价格进入398.80-401.20且收盘不低于398.80，或突破路径为任一5分钟K线收盘≥404.80并重新站稳MA20上方")
	if !ok {
		t.Fatal("expected breakout threshold to be parsed")
	}
	if value != 404.8 {
		t.Fatalf("breakout threshold = %.2f, want 404.80", value)
	}
	if _, bad := extractActivationBreakoutThreshold("重新站稳MA20上方后再观察是否转强"); bad {
		t.Fatal("expected MA20 indicator text to be ignored")
	}
	if _, bad := extractActivationBreakoutThreshold("有效站上5日线上方再看"); bad {
		t.Fatal("expected day-line indicator text to be ignored")
	}
}

func TestBuildActivationRuleFromRecommend_MarketSummaryKeepsPullbackRangeAndExplicitBreakout(t *testing.T) {
	rec := &models.AiRecommendStocks{
		StockCode:                   "300750.SZ",
		StockName:                   "宁德时代",
		StockPrice:                  "402.95",
		StockCurrentPrice:           "402.95",
		ActivationRuleSource:        "market_summary",
		RecommendBuyPrice:           "398.80-401.20",
		RecommendBuyPriceMin:        398.8,
		RecommendBuyPriceMax:        401.2,
		RecommendStopProfitPrice:    "430-445",
		RecommendStopProfitPriceMin: 430,
		RecommendStopProfitPriceMax: 445,
		RecommendStopLossPrice:      "390",
		BuySignal:                   "价格触发：回踩路径为未来5个交易日内任一5分钟K线价格进入398.80-401.20且收盘不低于398.80，或突破路径为任一5分钟K线收盘≥404.80并重新站稳MA20上方；量能触发：对应5分钟成交额≥近5个5分钟均额的1.15倍（回踩）/1.20倍（突破），且连续1根确认",
	}

	rule, err := buildActivationRuleFromRecommend(rec)
	if err != nil {
		t.Fatalf("buildActivationRuleFromRecommend failed: %v", err)
	}
	if len(rule.Paths) != 2 {
		t.Fatalf("paths len = %d, want 2", len(rule.Paths))
	}
	if rule.Paths[0].SignalType != "price_range_with_volume" {
		t.Fatalf("pullback signalType = %s, want price_range_with_volume", rule.Paths[0].SignalType)
	}
	if rule.Paths[0].ThresholdValue != 398.8 || rule.Paths[0].ThresholdMax != 401.2 {
		t.Fatalf("pullback thresholds = %.2f-%.2f, want 398.80-401.20", rule.Paths[0].ThresholdValue, rule.Paths[0].ThresholdMax)
	}
	if rule.Paths[1].SignalType != "price_breakout_with_volume" {
		t.Fatalf("breakout signalType = %s, want price_breakout_with_volume", rule.Paths[1].SignalType)
	}
	if rule.Paths[1].ThresholdValue != 404.8 {
		t.Fatalf("breakout threshold = %.2f, want 404.80", rule.Paths[1].ThresholdValue)
	}
}

func TestBuildActivationRuleFromRecommend_AddsBreakoutMaxEntryBelowStopProfit(t *testing.T) {
	loc := cnLocation()
	recordTime := time.Date(2026, 4, 29, 9, 40, 0, 0, loc)
	rec := &models.AiRecommendStocks{
		DataTime:                    &recordTime,
		StockCode:                   "002297.SZ",
		StockName:                   "博云新材",
		StockPrice:                  "21.86",
		StockCurrentPrice:           "21.86",
		RecommendBuyPrice:           "21.30-21.90",
		RecommendBuyPriceMin:        21.3,
		RecommendBuyPriceMax:        21.9,
		RecommendStopProfitPrice:    "23.30-24.20",
		RecommendStopProfitPriceMin: 23.3,
		RecommendStopProfitPriceMax: 24.2,
		RecommendStopLossPrice:      "20.80",
		ActivationRuleSource:        "market_summary",
		BuySignal:                   "价格触发：未来5个交易日内，回踩路径为5分钟收盘价进入21.30-21.90区间；突破路径为5分钟收盘价≥22.65；量能触发：5分钟成交额≥近5个5分钟均额的1.15倍",
	}

	rule, err := buildActivationRuleFromRecommend(rec)
	if err != nil {
		t.Fatalf("buildActivationRuleFromRecommend failed: %v", err)
	}
	var breakout *activationRule
	for i := range rule.Paths {
		if rule.Paths[i].Name == "breakout" {
			breakout = &rule.Paths[i]
			break
		}
	}
	if breakout == nil {
		t.Fatal("expected breakout path")
	}
	if round2(breakout.ThresholdValue) != 22.65 {
		t.Fatalf("breakout threshold = %.2f, want 22.65", breakout.ThresholdValue)
	}
	if breakout.ThresholdMax <= breakout.ThresholdValue {
		t.Fatalf("expected thresholdMax above thresholdValue, got %.2f <= %.2f", breakout.ThresholdMax, breakout.ThresholdValue)
	}
	if breakout.ThresholdMax >= 23.30 {
		t.Fatalf("expected thresholdMax below stop profit lower bound, got %.2f", breakout.ThresholdMax)
	}
}

func TestResolveActivationRuleScan_TriggersBreakoutPath(t *testing.T) {
	rec := models.AiRecommendStocks{
		StockCode:         "300308.SZ",
		StockName:         "中际旭创",
		RecommendBuyPrice: "9.80-10.00",
		ActivationRuleJSON: `{"version":"v2","mode":"any_of","paths":[` +
			`{"name":"pullback","signalType":"price_range_with_volume","evaluationWindow":"1m","baseline":"avg_amount_5x5m","operator":">=","thresholdValue":9.8,"thresholdMax":10.0,"volumeRatio":1.15,"confirmBars":1,"volumeWindow":2,"volumeMetric":"amount","expireTradeDays":5},` +
			`{"name":"breakout","signalType":"price_breakout_with_volume","evaluationWindow":"1m","baseline":"avg_amount_5x5m","operator":">=","thresholdValue":10.2,"thresholdMax":10.35,"volumeRatio":1.2,"confirmBars":1,"volumeWindow":2,"volumeMetric":"amount","expireTradeDays":5}` +
			`]}`,
	}
	loc := cnLocation()
	bars := []minuteBar{
		{TradeTime: time.Date(2026, 4, 9, 10, 0, 0, 0, loc), Open: 10.01, High: 10.05, Low: 10.00, Close: 10.03, Amount: 1000, Volume: 100},
		{TradeTime: time.Date(2026, 4, 9, 10, 1, 0, 0, loc), Open: 10.03, High: 10.10, Low: 10.02, Close: 10.08, Amount: 1100, Volume: 110},
		{TradeTime: time.Date(2026, 4, 9, 10, 2, 0, 0, loc), Open: 10.08, High: 10.25, Low: 10.07, Close: 10.22, Amount: 2400, Volume: 260},
	}

	scan := resolveActivationRuleScan(rec, bars)
	if !scan.Triggered {
		t.Fatalf("expected triggered scan, got %+v", scan)
	}
	if !scan.Time.Equal(time.Date(2026, 4, 9, 10, 2, 0, 0, loc)) {
		t.Fatalf("scan time = %v, want 2026-04-09 10:02:00", scan.Time)
	}
	if round2(scan.Price) != 10.22 {
		t.Fatalf("scan price = %.2f, want 10.22", scan.Price)
	}
}

func TestNormalizeAiRecommendStockForSave_DowngradesMarketSummaryPriceMismatchToAnalysisOnly(t *testing.T) {
	withStubbedMinuteProviders(t)
	db.Init(filepath.Join(t.TempDir(), "market-summary-mismatch-save.db"))
	if err := db.Dao.AutoMigrate(&StockBasic{}, &Settings{}, &models.AiRecommendMinuteBar{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	loc := cnLocation()
	recordTime := time.Date(2026, 4, 7, 11, 32, 0, 0, loc)
	seedMinuteBars(t, "300308.SZ", []minuteBar{
		{TradeTime: recordTime.Add(-time.Minute), Open: 600, High: 603, Low: 598, Close: 600.5, Volume: 1000, Amount: 600500},
		{TradeTime: recordTime, Open: 600.5, High: 602, Low: 599.5, Close: 601.2, Volume: 1100, Amount: 661320},
	})

	rec := &models.AiRecommendStocks{
		ModelName:                "test-model",
		StockCode:                "300308.SZ",
		StockName:                "中际旭创",
		BkName:                   "AI算力",
		DataTime:                 &recordTime,
		StockPrice:               "170",
		StockCurrentPrice:        "170",
		StockCurrentPriceTime:    recordTime.Format(time.DateTime),
		StockClosePrice:          "170",
		RecommendBuyPrice:        "168-172",
		RecommendBuyPriceMin:     168,
		RecommendBuyPriceMax:     172,
		RecommendStopProfitPrice: "180-188",
		RecommendStopLossPrice:   "163",
		ExecutionState:           recommendExecutionConditional,
		BuySignal:                "价格触发：未来3个交易日内股价回到168-172元区间后，连续2根15分钟K线收于170元上方；量能触发：对应15分钟成交额≥近5个15分钟均额的1.3倍，且量比≥1.5；逻辑触发：AI算力主线未证伪",
		InvalidCondition:         "时间失效：未来3个交易日内未触发；价格失效：跌破163",
		RecommendStatus:          "valid",
		SummaryVersion:           marketSummaryPhase3Version,
		ActivationRuleSource:     "market_summary",
		RiskRemarks:              "高位波动较大，需防板块分歧",
	}

	err := normalizeAiRecommendStockForSave(rec)
	if err != nil {
		t.Fatalf("normalizeAiRecommendStockForSave returned error: %v", err)
	}
	if rec.RecommendStatus != "missing_market_data" {
		t.Fatalf("recommend status = %s, want missing_market_data", rec.RecommendStatus)
	}
	if rec.ExecutionState != recommendExecutionAnalysisOnly {
		t.Fatalf("execution state = %s, want analysis_only", rec.ExecutionState)
	}
	if rec.RecommendBuyPrice != "" || rec.RecommendStopProfitPrice != "" || rec.RecommendStopLossPrice != "" {
		t.Fatalf("expected trade fields cleared after downgrade: %+v", rec)
	}
	if !strings.Contains(rec.ActivationInvalidReason, "偏离过大") {
		t.Fatalf("unexpected activation invalid reason: %s", rec.ActivationInvalidReason)
	}
}

func TestNormalizeAiRecommendStockForSave_MissingMinuteKeepsRecoverableMarketSummaryPending(t *testing.T) {
	withStubbedMinuteProviders(t)
	db.Init(filepath.Join(t.TempDir(), "market-summary-missing-minute-pending.db"))
	if err := db.Dao.AutoMigrate(&StockBasic{}, &Settings{}, &models.AiRecommendMinuteBar{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	loc := cnLocation()
	recordTime := time.Date(2026, 4, 29, 9, 40, 0, 0, loc)
	rec := &models.AiRecommendStocks{
		ModelName:                "test-model",
		StockCode:                "002297.SZ",
		StockName:                "博云新材",
		BkName:                   "低空经济",
		DataTime:                 &recordTime,
		StockPrice:               "21.86",
		StockCurrentPrice:        "21.86",
		StockCurrentPriceTime:    recordTime.Format(time.DateTime),
		StockClosePrice:          "21.86",
		RecommendBuyPrice:        "21.30-21.90",
		RecommendBuyPriceMin:     21.3,
		RecommendBuyPriceMax:     21.9,
		RecommendStopProfitPrice: "23.30-24.20",
		RecommendStopLossPrice:   "20.80",
		ExecutionState:           recommendExecutionConditional,
		BuySignal:                "等待激活；激活条件：pullback：价格进入21.3-21.9区间，5分钟成交额不低于近5个5分钟平均成交额的1.15倍，1根5分钟K线确认，5个交易日内有效；或 breakout：价格站上22.65，5分钟成交额不低于近5个5分钟平均成交额的1.2倍，1根5分钟K线确认，5个交易日内有效",
		InvalidCondition:         "时间失效：5个交易日内未触发",
		RecommendStatus:          "valid",
		SummaryVersion:           marketSummaryPhase3Version,
		ActivationRuleSource:     "market_summary",
		ActivationRuleJSON:       `{"version":"v3","mode":"any_of","paths":[{"name":"pullback","signalType":"price_range_with_volume","evaluationWindow":"5m","baseline":"avg_amount_5x5m","operator":">=","thresholdValue":21.3,"thresholdMax":21.9,"volumeRatio":1,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5},{"name":"breakout","signalType":"price_breakout_with_volume","evaluationWindow":"5m","baseline":"avg_amount_5x5m","operator":">=","thresholdValue":22.65,"volumeRatio":1.05,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5}]}`,
		RiskRemarks:              "若板块退潮或跌破止损位，需要严格执行退出",
	}

	if err := normalizeAiRecommendStockForSave(rec); err != nil {
		t.Fatalf("normalizeAiRecommendStockForSave returned error: %v", err)
	}
	if rec.RecommendStatus != recommendStatusPendingMarketData {
		t.Fatalf("recommend status = %s, want %s", rec.RecommendStatus, recommendStatusPendingMarketData)
	}
	if rec.ExecutionState != recommendExecutionConditional {
		t.Fatalf("execution state = %s, want conditional", rec.ExecutionState)
	}
	if rec.ActivationStatus != recommendActivationPendingData {
		t.Fatalf("activation status = %s, want pending_data", rec.ActivationStatus)
	}
	if rec.RecommendBuyPrice == "" || rec.RecommendStopProfitPrice == "" || rec.RecommendStopLossPrice == "" {
		t.Fatalf("expected trade plan preserved: %+v", rec)
	}
}

func TestRepairHistoricalMarketSummaryActivationIssues_InvalidatesMismatchAndUpgradesRules(t *testing.T) {
	withStubbedMinuteProviders(t)
	db.Init(filepath.Join(t.TempDir(), "market-summary-activation-repair.db"))
	if err := db.Dao.AutoMigrate(&models.AiRecommendStocks{}, &StockBasic{}, &Settings{}, &models.AiRecommendMinuteBar{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	loc := cnLocation()
	recordTime := time.Date(2026, 4, 7, 11, 32, 0, 0, loc)
	seedMinuteBars(t, "300308.SZ", []minuteBar{
		{TradeTime: recordTime, Open: 600, High: 602, Low: 598, Close: 601.2, Volume: 1000, Amount: 601200},
	})
	seedMinuteBars(t, "002371.SZ", []minuteBar{
		{TradeTime: recordTime, Open: 360, High: 362, Low: 358, Close: 360.8, Volume: 900, Amount: 324720},
	})

	bad := models.AiRecommendStocks{
		DataTime:                 &recordTime,
		ModelName:                "test-model",
		StockCode:                "300308.SZ",
		StockName:                "中际旭创",
		BkName:                   "AI算力",
		StockPrice:               "170",
		StockCurrentPrice:        "170",
		StockCurrentPriceTime:    recordTime.Format(time.DateTime),
		StockClosePrice:          "170",
		RecommendBuyPrice:        "168-172",
		RecommendBuyPriceMin:     168,
		RecommendBuyPriceMax:     172,
		RecommendStopProfitPrice: "180-188",
		RecommendStopLossPrice:   "163",
		ExecutionState:           recommendExecutionConditional,
		BuySignal:                "价格触发：未来3个交易日内股价回到168-172元区间后，连续2根15分钟K线收于170元上方；量能触发：对应15分钟成交额≥近5个15分钟均额的1.3倍，且量比≥1.5；逻辑触发：AI算力主线未证伪",
		InvalidCondition:         "时间失效：未来3个交易日内未触发；价格失效：跌破163",
		RecommendStatus:          "valid",
		SummaryVersion:           marketSummaryPhase3Version,
		ActivationRuleSource:     "market_summary",
		ActivationRuleVersion:    activationRuleVersionV1,
		ActivationRuleJSON:       `{"signalType":"price_range_with_volume","evaluationWindow":"5m","baseline":"avg_amount_5x5m","operator":">=","thresholdValue":168,"thresholdMax":172,"volumeRatio":1.5,"confirmBars":2,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":3}`,
		ActivationStatus:         "pending",
	}
	good := models.AiRecommendStocks{
		DataTime:                 &recordTime,
		ModelName:                "test-model",
		StockCode:                "002371.SZ",
		StockName:                "北方华创",
		BkName:                   "半导体设备",
		StockPrice:               "360",
		StockCurrentPrice:        "360",
		StockCurrentPriceTime:    recordTime.Format(time.DateTime),
		StockClosePrice:          "360",
		RecommendBuyPrice:        "355-365",
		RecommendBuyPriceMin:     355,
		RecommendBuyPriceMax:     365,
		RecommendStopProfitPrice: "382-398",
		RecommendStopLossPrice:   "346",
		ExecutionState:           recommendExecutionConditional,
		BuySignal:                "价格触发：未来5个交易日内股价进入355-365元区间，并连续2根30分钟K线站上360元；量能触发：30分钟成交额≥近5个30分钟均额的1.2倍，且不低于上一交易日同一时段成交额的1.1倍；逻辑触发：展会催化未证伪",
		InvalidCondition:         "时间失效：未来5个交易日内未触发；价格失效：跌破346",
		RecommendStatus:          "valid",
		SummaryVersion:           marketSummaryPhase3Version,
		ActivationRuleSource:     "market_summary",
		ActivationRuleVersion:    activationRuleVersionV1,
		ActivationRuleJSON:       `{"signalType":"price_range_with_volume","evaluationWindow":"5m","baseline":"prev_day_same_slot_amount","operator":">=","thresholdValue":355,"thresholdMax":365,"volumeRatio":1.2,"confirmBars":2,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5}`,
		ActivationStatus:         "pending",
	}
	if err := db.Dao.Create(&bad).Error; err != nil {
		t.Fatalf("create bad row failed: %v", err)
	}
	if err := db.Dao.Create(&good).Error; err != nil {
		t.Fatalf("create good row failed: %v", err)
	}

	globalAiRecommendYieldRecalcManager.mu.Lock()
	prevRunning := globalAiRecommendYieldRecalcManager.running
	prevPending := globalAiRecommendYieldRecalcManager.pending
	prevPendingForce := globalAiRecommendYieldRecalcManager.pendingForce
	prevPendingScope := globalAiRecommendYieldRecalcManager.pendingScope
	globalAiRecommendYieldRecalcManager.running = true
	globalAiRecommendYieldRecalcManager.pending = false
	globalAiRecommendYieldRecalcManager.pendingForce = false
	globalAiRecommendYieldRecalcManager.pendingScope = nil
	globalAiRecommendYieldRecalcManager.mu.Unlock()
	t.Cleanup(func() {
		globalAiRecommendYieldRecalcManager.mu.Lock()
		globalAiRecommendYieldRecalcManager.running = prevRunning
		globalAiRecommendYieldRecalcManager.pending = prevPending
		globalAiRecommendYieldRecalcManager.pendingForce = prevPendingForce
		globalAiRecommendYieldRecalcManager.pendingScope = prevPendingScope
		globalAiRecommendYieldRecalcManager.mu.Unlock()
	})

	stats, err := RepairHistoricalMarketSummaryActivationIssues(time.Now())
	if err != nil {
		t.Fatalf("RepairHistoricalMarketSummaryActivationIssues failed: %v", err)
	}
	if stats.Downgraded != 1 || stats.RuleUpgraded != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	var gotBad models.AiRecommendStocks
	if err := db.Dao.First(&gotBad, bad.ID).Error; err != nil {
		t.Fatalf("load bad row failed: %v", err)
	}
	if gotBad.ActivationStatus != "invalid" {
		t.Fatalf("bad activation status = %s, want invalid", gotBad.ActivationStatus)
	}
	if gotBad.RecommendStatus != "missing_market_data" {
		t.Fatalf("bad recommend status = %s, want missing_market_data", gotBad.RecommendStatus)
	}
	if gotBad.ExecutionState != recommendExecutionAnalysisOnly {
		t.Fatalf("bad execution state = %s, want analysis_only", gotBad.ExecutionState)
	}
	if gotBad.RecommendBuyPrice != "" || gotBad.RecommendStopProfitPrice != "" || gotBad.RecommendStopLossPrice != "" {
		t.Fatalf("expected downgraded row trade fields cleared: %+v", gotBad)
	}
	if !strings.Contains(gotBad.ActivationInvalidReason, "偏离过大") {
		t.Fatalf("unexpected bad invalid reason: %s", gotBad.ActivationInvalidReason)
	}

	var gotGood models.AiRecommendStocks
	if err := db.Dao.First(&gotGood, good.ID).Error; err != nil {
		t.Fatalf("load good row failed: %v", err)
	}
	if gotGood.ActivationRuleVersion != activationRuleVersionV3 {
		t.Fatalf("good activation rule version = %s, want v3", gotGood.ActivationRuleVersion)
	}
	if !strings.Contains(gotGood.ActivationRuleJSON, `"paths"`) {
		t.Fatalf("expected upgraded rule paths, got %s", gotGood.ActivationRuleJSON)
	}
	if !strings.Contains(gotGood.ActivationRuleJSON, `"openingPolicy"`) {
		t.Fatalf("expected upgraded rule openingPolicy, got %s", gotGood.ActivationRuleJSON)
	}
}

func TestRepairHistoricalMarketSummaryActivationIssues_RecoversCorruptedDualPathRule(t *testing.T) {
	withStubbedMinuteProviders(t)
	db.Init(filepath.Join(t.TempDir(), "market-summary-activation-repair-recover.db"))
	if err := db.Dao.AutoMigrate(&models.AiRecommendStocks{}, &StockBasic{}, &Settings{}, &models.AiRecommendMinuteBar{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	loc := cnLocation()
	recordTime := time.Date(2026, 4, 10, 11, 30, 0, 0, loc)
	seedMinuteBars(t, "300750.SZ", []minuteBar{
		{TradeTime: recordTime, Open: 402.8, High: 403.2, Low: 402.5, Close: 402.95, Volume: 1000, Amount: 402950},
	})

	row := models.AiRecommendStocks{
		DataTime:                 &recordTime,
		ModelName:                "test-model",
		StockCode:                "300750.SZ",
		StockName:                "宁德时代",
		BkName:                   "动力电池",
		StockPrice:               "402.95",
		StockCurrentPrice:        "402.95",
		StockCurrentPriceTime:    recordTime.Format(time.DateTime),
		StockClosePrice:          "402.95",
		RecommendBuyPrice:        "",
		RecommendBuyPriceMin:     0,
		RecommendBuyPriceMax:     0,
		RecommendStopProfitPrice: "",
		RecommendStopLossPrice:   "",
		ExecutionState:           recommendExecutionAnalysisOnly,
		BuySignal:                "缺少可信实时价格/量能数据，本次仅保留逻辑分析，不生成交易计划",
		InvalidCondition:         marketSummaryAnalysisOnlySkipReason,
		RecommendStatus:          "missing_market_data",
		SummaryVersion:           marketSummaryPhase3Version,
		ActivationRuleSource:     "market_summary",
		ActivationRuleVersion:    activationRuleVersionV1,
		ActivationRuleJSON:       `{"version":"v3","mode":"any_of","paths":[{"name":"pullback","signalType":"price_breakout_with_volume","evaluationWindow":"5m","baseline":"avg_amount_5x5m","operator":">=","thresholdValue":20,"volumeRatio":1,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5},{"name":"breakout","signalType":"price_breakout_with_volume","evaluationWindow":"5m","baseline":"avg_amount_5x5m","operator":">=","thresholdValue":407,"volumeRatio":1.05,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5}]}`,
		ActivationStatus:         "skipped",
		ActivationInvalidReason:  "突破价20.00与参考价402.95(minute_bar)偏离过大，已降级为仅分析并跳过激活",
		Remarks:                  "等待激活；激活条件：pullback：价格进入398.8-401.2区间，5分钟成交额不低于近5个5分钟平均成交额的1.15倍，1根5分钟K线确认，5个交易日内有效；或 breakout：价格站上404.8，5分钟成交额不低于近5个5分钟平均成交额的1.2倍，1根5分钟K线确认，5个交易日内有效\n止盈区间430-445；止损位390\n突破价20.00与参考价402.95(minute_bar)偏离过大，已降级为仅分析并跳过激活",
		RiskRemarks:              "高位波动较大，需防板块分歧",
	}
	if err := db.Dao.Create(&row).Error; err != nil {
		t.Fatalf("create row failed: %v", err)
	}

	globalAiRecommendYieldRecalcManager.mu.Lock()
	prevRunning := globalAiRecommendYieldRecalcManager.running
	prevPending := globalAiRecommendYieldRecalcManager.pending
	prevPendingForce := globalAiRecommendYieldRecalcManager.pendingForce
	prevPendingScope := globalAiRecommendYieldRecalcManager.pendingScope
	globalAiRecommendYieldRecalcManager.running = true
	globalAiRecommendYieldRecalcManager.pending = false
	globalAiRecommendYieldRecalcManager.pendingForce = false
	globalAiRecommendYieldRecalcManager.pendingScope = nil
	globalAiRecommendYieldRecalcManager.mu.Unlock()
	t.Cleanup(func() {
		globalAiRecommendYieldRecalcManager.mu.Lock()
		globalAiRecommendYieldRecalcManager.running = prevRunning
		globalAiRecommendYieldRecalcManager.pending = prevPending
		globalAiRecommendYieldRecalcManager.pendingForce = prevPendingForce
		globalAiRecommendYieldRecalcManager.pendingScope = prevPendingScope
		globalAiRecommendYieldRecalcManager.mu.Unlock()
	})

	stats, err := RepairHistoricalMarketSummaryActivationIssues(time.Now())
	if err != nil {
		t.Fatalf("RepairHistoricalMarketSummaryActivationIssues failed: %v", err)
	}
	if stats.RuleUpgraded != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	var got models.AiRecommendStocks
	if err := db.Dao.First(&got, row.ID).Error; err != nil {
		t.Fatalf("load row failed: %v", err)
	}
	if got.RecommendStatus != "valid" {
		t.Fatalf("recommend status = %s, want valid", got.RecommendStatus)
	}
	if got.ExecutionState != recommendExecutionConditional {
		t.Fatalf("execution state = %s, want conditional", got.ExecutionState)
	}
	if got.ActivationStatus != "pending" {
		t.Fatalf("activation status = %s, want pending", got.ActivationStatus)
	}
	if got.RecommendBuyPrice != "398.8-401.2" && got.RecommendBuyPrice != "398.80-401.20" {
		t.Fatalf("recommend buy price = %s, want 398.8-401.2", got.RecommendBuyPrice)
	}
	if got.ActivationInvalidReason != "" {
		t.Fatalf("activation invalid reason = %s, want empty", got.ActivationInvalidReason)
	}
	rule, err := parseActivationRuleJSON(got.ActivationRuleJSON)
	if err != nil {
		t.Fatalf("parse repaired rule failed: %v", err)
	}
	if len(rule.Paths) != 2 {
		t.Fatalf("paths len = %d, want 2", len(rule.Paths))
	}
	if rule.Paths[0].Name != "pullback" || rule.Paths[0].SignalType != "price_range_with_volume" {
		t.Fatalf("unexpected pullback path: %+v", rule.Paths[0])
	}
	if rule.Paths[0].ThresholdValue != 398.8 || rule.Paths[0].ThresholdMax != 401.2 {
		t.Fatalf("unexpected pullback thresholds: %+v", rule.Paths[0])
	}
	if rule.Paths[1].Name != "breakout" || rule.Paths[1].SignalType != "price_breakout_with_volume" {
		t.Fatalf("unexpected breakout path: %+v", rule.Paths[1])
	}
	if rule.Paths[1].ThresholdValue != 404.8 {
		t.Fatalf("breakout threshold = %.2f, want 404.8", rule.Paths[1].ThresholdValue)
	}
	if rule.Paths[1].ThresholdMax <= rule.Paths[1].ThresholdValue {
		t.Fatalf("breakout thresholdMax = %.2f, want above %.2f", rule.Paths[1].ThresholdMax, rule.Paths[1].ThresholdValue)
	}
	if rule.Paths[1].ThresholdMax >= 430 {
		t.Fatalf("breakout thresholdMax = %.2f, want below stop profit", rule.Paths[1].ThresholdMax)
	}
}

func TestRecoverPendingMarketSummaryRecommendationsForScope_RecoversMissingMarketDataAfterMinuteArrives(t *testing.T) {
	withStubbedMinuteProviders(t)
	db.Init(filepath.Join(t.TempDir(), "market-summary-recover-pending-minute.db"))
	if err := db.Dao.AutoMigrate(
		&models.AiRecommendStocks{},
		&models.AiRecommendYieldDirtyCode{},
		&StockBasic{},
		&Settings{},
		&models.AiRecommendMinuteBar{},
	); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	loc := cnLocation()
	recordTime := time.Date(2026, 4, 29, 9, 40, 0, 0, loc)
	seedMinuteBars(t, "002297.SZ", []minuteBar{
		{TradeTime: recordTime, Open: 21.82, High: 21.90, Low: 21.72, Close: 21.86, Volume: 1200, Amount: 2623200},
	})
	row := models.AiRecommendStocks{
		DataTime:                 &recordTime,
		ModelName:                "test-model",
		StockCode:                "002297.SZ",
		StockName:                "博云新材",
		BkName:                   "低空经济",
		StockPrice:               "21.86",
		StockCurrentPrice:        "21.86",
		StockCurrentPriceTime:    recordTime.Format(time.DateTime),
		StockClosePrice:          "21.86",
		RecommendBuyPrice:        "",
		RecommendBuyPriceMin:     0,
		RecommendBuyPriceMax:     0,
		RecommendStopProfitPrice: "",
		RecommendStopLossPrice:   "",
		ExecutionState:           recommendExecutionAnalysisOnly,
		BuySignal:                "缺少可信实时价格/量能数据，本次仅保留逻辑分析，不生成交易计划",
		InvalidCondition:         marketSummaryAnalysisOnlySkipReason,
		RecommendStatus:          "missing_market_data",
		SummaryVersion:           marketSummaryPhase3Version,
		ActivationRuleSource:     "market_summary",
		ActivationRuleJSON:       `{"version":"v3","mode":"any_of","paths":[{"name":"pullback","signalType":"price_range_with_volume","evaluationWindow":"5m","baseline":"avg_amount_5x5m","operator":">=","thresholdValue":21.3,"thresholdMax":21.9,"volumeRatio":1,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5},{"name":"breakout","signalType":"price_breakout_with_volume","evaluationWindow":"5m","baseline":"avg_amount_5x5m","operator":">=","thresholdValue":22.65,"volumeRatio":1.05,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5}]}`,
		ActivationStatus:         "skipped",
		ActivationInvalidReason:  marketSummaryAnalysisOnlySkipReason,
		Remarks:                  "价格锚点：21.86，来源：2026-04-29 09:41 minute_bar；买入区间：21.30-21.90；止盈区间：23.30-24.20；止损位：20.80；等待激活；激活条件：pullback：价格进入21.3-21.9区间，5分钟成交额不低于近5个5分钟平均成交额的1.15倍，1根5分钟K线确认，5个交易日内有效；或 breakout：价格站上22.65，5分钟成交额不低于近5个5分钟平均成交额的1.2倍，1根5分钟K线确认，5个交易日内有效",
	}
	if err := db.Dao.Create(&row).Error; err != nil {
		t.Fatalf("create row failed: %v", err)
	}

	changed, err := recoverPendingMarketSummaryRecommendationsForScope(normalizeScopeCodes([]string{"002297.SZ"}))
	if err != nil {
		t.Fatalf("recoverPendingMarketSummaryRecommendationsForScope failed: %v", err)
	}
	if len(changed) != 1 || changed[0] != "002297.SZ" {
		t.Fatalf("changed = %#v, want 002297.SZ", changed)
	}

	var got models.AiRecommendStocks
	if err := db.Dao.First(&got, row.ID).Error; err != nil {
		t.Fatalf("load row failed: %v", err)
	}
	if got.RecommendStatus != "valid" {
		t.Fatalf("recommend status = %s, want valid", got.RecommendStatus)
	}
	if got.ExecutionState != recommendExecutionConditional {
		t.Fatalf("execution state = %s, want conditional", got.ExecutionState)
	}
	if got.ActivationStatus != "pending" {
		t.Fatalf("activation status = %s, want pending", got.ActivationStatus)
	}
	if got.RecommendBuyPrice != "21.30-21.90" {
		t.Fatalf("recommend buy price = %s, want 21.30-21.90", got.RecommendBuyPrice)
	}
	if got.RecommendStopProfitPrice != "23.30-24.20" {
		t.Fatalf("stop profit = %s, want 23.30-24.20", got.RecommendStopProfitPrice)
	}
	if got.RecommendStopLossPrice != "20.80" {
		t.Fatalf("stop loss = %s, want 20.80", got.RecommendStopLossPrice)
	}

	var dirty models.AiRecommendYieldDirtyCode
	if err := db.Dao.Where("stock_code = ?", "002297.SZ").First(&dirty).Error; err != nil {
		t.Fatalf("expected dirty code after recovery: %v", err)
	}
}

func TestRecoverPendingMarketSummaryRecommendationsForScope_RefillsExitPlanFromStoredReport(t *testing.T) {
	withStubbedMinuteProviders(t)
	db.Init(filepath.Join(t.TempDir(), "market-summary-recover-stored-report.db"))
	if err := db.Dao.AutoMigrate(
		&models.AiRecommendStocks{},
		&models.AiRecommendYieldDirtyCode{},
		&models.AIResponseResult{},
		&StockBasic{},
		&Settings{},
		&models.AiRecommendMinuteBar{},
	); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	loc := cnLocation()
	recordTime := time.Date(2026, 4, 29, 9, 40, 0, 0, loc)
	seedMinuteBars(t, "002297.SZ", []minuteBar{
		{TradeTime: recordTime, Open: 21.82, High: 21.90, Low: 21.72, Close: 21.86, Volume: 1200, Amount: 2623200},
	})
	report := models.AIResponseResult{
		StockCode: "市场资讯",
		StockName: "市场资讯",
		Question:  "总结和分析股票市场新闻中的投资机会，并推荐2个A股，并给出关键价位与交易计划",
		Content: `# 推荐股票池

| 股票（代码） | 所属方向 | 核心催化 | 关键证据 | 价格锚点 | 买入区间 | 止盈区间 | 止损位 | 买入依据 | 失效条件 | 风险点 | 预期周期 | 事件强度 | 资金确认度 | 基本面匹配度 | 技术面匹配度 | 操作备注 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 博云新材(002297.SZ) | 高换手龙虎榜新材料 | 龙虎榜资金净流入 | minutePrice=21.86，MA5=21.59，近 5 日高点=22.58 | 21.86，来源：2026-04-29 09:41 minute_bar | 21.30-21.90 | 23.30-24.20 | 20.80 | 价格触发：未来 5 个交易日内，回踩路径为 5 分钟收盘价进入 21.30-21.90 区间；突破路径为 5 分钟收盘价 ≥22.65；量能触发：5 分钟成交额 ≥近 5 个 5 分钟成交额均值的 1.15 倍 | 时间失效：未来 5 个交易日内未同时触发价格与量能条件则失效；价格失效：触发前或触发后 5 分钟收盘价 <20.80 则失效 | 高换手后资金兑现风险 | 3-5 个交易日 | 中高 | 中高 | 中 | 中高 | 等待激活；激活条件：pullback：价格进入21.3-21.9区间，5分钟成交额不低于近5个5分钟平均成交额的1.15倍，1根5分钟K线确认，5个交易日内有效；或 breakout：价格站上22.65，5分钟成交额不低于近5个5分钟平均成交额的1.15倍，1根5分钟K线确认，5个交易日内有效 |`,
	}
	report.CreatedAt = recordTime
	if err := db.Dao.Create(&report).Error; err != nil {
		t.Fatalf("create report failed: %v", err)
	}

	row := models.AiRecommendStocks{
		DataTime:                 &recordTime,
		ModelName:                "test-model",
		StockCode:                "002297.SZ",
		StockName:                "博云新材",
		StockPrice:               "21.86",
		StockCurrentPrice:        "21.86",
		StockCurrentPriceTime:    recordTime.Format(time.DateTime),
		StockClosePrice:          "21.86",
		RecommendBuyPrice:        "21.3-21.9",
		RecommendBuyPriceMin:     21.3,
		RecommendBuyPriceMax:     21.9,
		RecommendStopProfitPrice: "",
		RecommendStopLossPrice:   "",
		ExecutionState:           recommendExecutionConditional,
		RecommendStatus:          "valid",
		SummaryVersion:           marketSummaryPhase3Version,
		ActivationRuleSource:     "market_summary",
		ActivationRuleJSON:       `{"version":"v3","mode":"any_of","paths":[{"name":"pullback","signalType":"price_range_with_volume","thresholdValue":21.3,"thresholdMax":21.9,"volumeRatio":1,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount"},{"name":"breakout","signalType":"price_breakout_with_volume","thresholdValue":22.65,"volumeRatio":1.05,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount"}]}`,
		ActivationStatus:         "pending",
		InvalidCondition:         marketSummaryAnalysisOnlySkipReason,
		Remarks:                  "等待激活；激活条件：pullback：价格进入21.3-21.9区间，5分钟成交额不低于近5个5分钟平均成交额的1.15倍，1根5分钟K线确认，5个交易日内有效；或 breakout：价格站上22.65，5分钟成交额不低于近5个5分钟平均成交额的1.15倍，1根5分钟K线确认，5个交易日内有效",
	}
	if err := db.Dao.Create(&row).Error; err != nil {
		t.Fatalf("create row failed: %v", err)
	}

	changed, err := recoverPendingMarketSummaryRecommendationsForScope(normalizeScopeCodes([]string{"002297.SZ"}))
	if err != nil {
		t.Fatalf("recoverPendingMarketSummaryRecommendationsForScope failed: %v", err)
	}
	if len(changed) != 1 || changed[0] != "002297.SZ" {
		t.Fatalf("changed = %#v, want 002297.SZ", changed)
	}

	var got models.AiRecommendStocks
	if err := db.Dao.First(&got, row.ID).Error; err != nil {
		t.Fatalf("load row failed: %v", err)
	}
	if got.RecommendStopProfitPrice != "23.30-24.20" {
		t.Fatalf("stop profit = %s, want 23.30-24.20", got.RecommendStopProfitPrice)
	}
	if got.RecommendStopLossPrice != "20.80" {
		t.Fatalf("stop loss = %s, want 20.80", got.RecommendStopLossPrice)
	}
	if got.ActivationStatus != "pending" || got.RecommendStatus != "valid" {
		t.Fatalf("unexpected recovered status: recommend=%s activation=%s", got.RecommendStatus, got.ActivationStatus)
	}
	if strings.Contains(got.InvalidCondition, "缺少真实价格/量能数据") {
		t.Fatalf("invalid condition still has stale missing-data reason: %s", got.InvalidCondition)
	}
	if !strings.Contains(got.InvalidCondition, "未来 5 个交易日内未同时触发价格与量能条件") {
		t.Fatalf("invalid condition = %s, want stored report invalid condition", got.InvalidCondition)
	}
}

func TestRecoverPendingMarketSummaryRecommendationsForScope_ReplacesStaleMissingDataInvalidCondition(t *testing.T) {
	withStubbedMinuteProviders(t)
	db.Init(filepath.Join(t.TempDir(), "market-summary-recover-stale-invalid-condition.db"))
	if err := db.Dao.AutoMigrate(
		&models.AiRecommendStocks{},
		&models.AiRecommendYieldDirtyCode{},
		&models.AIResponseResult{},
		&StockBasic{},
		&Settings{},
		&models.AiRecommendMinuteBar{},
	); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	loc := cnLocation()
	recordTime := time.Date(2026, 4, 29, 9, 40, 0, 0, loc)
	seedMinuteBars(t, "002297.SZ", []minuteBar{
		{TradeTime: recordTime, Open: 21.82, High: 21.90, Low: 21.72, Close: 21.86, Volume: 1200, Amount: 2623200},
	})
	report := models.AIResponseResult{
		StockCode: "市场资讯",
		StockName: "市场资讯",
		Question:  "总结和分析股票市场新闻中的投资机会，并推荐2个A股，并给出关键价位与交易计划",
		Content: `# 推荐股票池

| 股票（代码） | 所属方向 | 核心催化 | 关键证据 | 价格锚点 | 买入区间 | 止盈区间 | 止损位 | 买入依据 | 失效条件 | 风险点 | 预期周期 | 事件强度 | 资金确认度 | 基本面匹配度 | 技术面匹配度 | 操作备注 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 博云新材(002297.SZ) | 高换手龙虎榜新材料 | 龙虎榜资金净流入 | minutePrice=21.86，MA5=21.59，近 5 日高点=22.58 | 21.86，来源：2026-04-29 09:41 minute_bar | 21.30-21.90 | 23.30-24.20 | 20.80 | 价格触发：未来 5 个交易日内，回踩路径为 5 分钟收盘价进入 21.30-21.90 区间；突破路径为 5 分钟收盘价 ≥22.65；量能触发：5 分钟成交额 ≥近 5 个 5 分钟成交额均值的 1.15 倍 | 时间失效：未来 5 个交易日内未同时触发价格与量能条件则失效；价格失效：触发前或触发后 5 分钟收盘价 <20.80 则失效 | 高换手后资金兑现风险 | 3-5 个交易日 | 中高 | 中高 | 中 | 中高 | 等待激活；激活条件：pullback：价格进入21.3-21.9区间，5分钟成交额不低于近5个5分钟平均成交额的1.15倍，1根5分钟K线确认，5个交易日内有效；或 breakout：价格站上22.65，5分钟成交额不低于近5个5分钟平均成交额的1.15倍，1根5分钟K线确认，5个交易日内有效 |`,
	}
	report.CreatedAt = recordTime
	if err := db.Dao.Create(&report).Error; err != nil {
		t.Fatalf("create report failed: %v", err)
	}

	row := models.AiRecommendStocks{
		DataTime:                    &recordTime,
		ModelName:                   "test-model",
		StockCode:                   "002297.SZ",
		StockName:                   "博云新材",
		StockPrice:                  "21.86",
		StockCurrentPrice:           "21.86",
		StockCurrentPriceTime:       recordTime.Format(time.DateTime),
		StockClosePrice:             "21.86",
		RecommendBuyPrice:           "21.3-21.9",
		RecommendBuyPriceMin:        21.3,
		RecommendBuyPriceMax:        21.9,
		RecommendStopProfitPrice:    "23.30-24.20",
		RecommendStopProfitPriceMin: 23.3,
		RecommendStopProfitPriceMax: 24.2,
		RecommendStopLossPrice:      "20.80",
		ExecutionState:              recommendExecutionConditional,
		RecommendStatus:             "valid",
		SummaryVersion:              marketSummaryPhase3Version,
		ActivationRuleSource:        "market_summary",
		ActivationRuleJSON:          `{"version":"v3","mode":"any_of","paths":[{"name":"pullback","signalType":"price_range_with_volume","thresholdValue":21.3,"thresholdMax":21.9,"volumeRatio":1.15,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount"},{"name":"breakout","signalType":"price_breakout_with_volume","thresholdValue":22.65,"volumeRatio":1.15,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount"}]}`,
		ActivationStatus:            "activated",
		ActivationInvalidReason:     "",
		InvalidCondition:            marketSummaryAnalysisOnlySkipReason,
		Remarks:                     "等待激活；激活条件：pullback：价格进入21.3-21.9区间，5分钟成交额不低于近5个5分钟平均成交额的1.15倍，1根5分钟K线确认，5个交易日内有效；或 breakout：价格站上22.65，5分钟成交额不低于近5个5分钟平均成交额的1.15倍，1根5分钟K线确认，5个交易日内有效",
	}
	if err := db.Dao.Create(&row).Error; err != nil {
		t.Fatalf("create row failed: %v", err)
	}

	changed, err := recoverPendingMarketSummaryRecommendationsForScope(normalizeScopeCodes([]string{"002297.SZ"}))
	if err != nil {
		t.Fatalf("recoverPendingMarketSummaryRecommendationsForScope failed: %v", err)
	}
	if len(changed) != 1 || changed[0] != "002297.SZ" {
		t.Fatalf("changed = %#v, want 002297.SZ", changed)
	}

	var got models.AiRecommendStocks
	if err := db.Dao.First(&got, row.ID).Error; err != nil {
		t.Fatalf("load row failed: %v", err)
	}
	if strings.Contains(got.InvalidCondition, "缺少真实价格/量能数据") {
		t.Fatalf("invalid condition still has stale missing-data reason: %s", got.InvalidCondition)
	}
	if !strings.Contains(got.InvalidCondition, "未来 5 个交易日内未同时触发价格与量能条件") {
		t.Fatalf("invalid condition = %s, want stored report invalid condition", got.InvalidCondition)
	}
	if got.RecommendStopProfitPrice != "23.30-24.20" || got.RecommendStopLossPrice != "20.80" {
		t.Fatalf("exit plan changed unexpectedly: stopProfit=%s stopLoss=%s", got.RecommendStopProfitPrice, got.RecommendStopLossPrice)
	}
}

func TestRecoverPendingMarketSummaryRecommendationsForScope_UsesCacheOnly(t *testing.T) {
	withStubbedMinuteProviders(t)
	db.Init(filepath.Join(t.TempDir(), "market-summary-recover-cache-only.db"))
	if err := db.Dao.AutoMigrate(
		&models.AiRecommendStocks{},
		&models.AiRecommendYieldDirtyCode{},
		&StockBasic{},
		&Settings{},
		&models.AiRecommendMinuteBar{},
	); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	loc := cnLocation()
	recordTime := time.Date(2026, 4, 29, 9, 40, 0, 0, loc)
	providerCalled := false
	oldDiemeng := fetchMinuteBarsWithDiemengFn
	fetchMinuteBarsWithDiemengFn = func(tsCode string, start, end time.Time) ([]minuteBar, string, error) {
		providerCalled = true
		return []minuteBar{
			{TradeTime: recordTime, Open: 21.82, High: 21.90, Low: 21.72, Close: 21.86, Volume: 1200, Amount: 2623200},
		}, "test", nil
	}
	t.Cleanup(func() {
		fetchMinuteBarsWithDiemengFn = oldDiemeng
	})

	row := models.AiRecommendStocks{
		DataTime:                 &recordTime,
		ModelName:                "test-model",
		StockCode:                "002297.SZ",
		StockName:                "博云新材",
		StockPrice:               "21.86",
		StockCurrentPrice:        "21.86",
		StockCurrentPriceTime:    recordTime.Format(time.DateTime),
		StockClosePrice:          "21.86",
		RecommendBuyPrice:        "",
		RecommendBuyPriceMin:     0,
		RecommendBuyPriceMax:     0,
		RecommendStopProfitPrice: "",
		RecommendStopLossPrice:   "",
		ExecutionState:           recommendExecutionAnalysisOnly,
		BuySignal:                "缺少可信实时价格/量能数据，本次仅保留逻辑分析，不生成交易计划",
		InvalidCondition:         marketSummaryAnalysisOnlySkipReason,
		RecommendStatus:          "missing_market_data",
		SummaryVersion:           marketSummaryPhase3Version,
		ActivationRuleSource:     "market_summary",
		ActivationRuleJSON:       `{"version":"v3","mode":"any_of","paths":[{"name":"pullback","signalType":"price_range_with_volume","evaluationWindow":"5m","baseline":"avg_amount_5x5m","operator":">=","thresholdValue":21.3,"thresholdMax":21.9,"volumeRatio":1,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5}]}`,
		ActivationStatus:         "skipped",
		ActivationInvalidReason:  marketSummaryAnalysisOnlySkipReason,
		Remarks:                  "价格锚点：21.86，来源：2026-04-29 09:41 minute_bar；买入区间：21.30-21.90；止盈区间：23.30-24.20；止损位：20.80；等待激活",
	}
	if err := db.Dao.Create(&row).Error; err != nil {
		t.Fatalf("create row failed: %v", err)
	}

	changed, err := recoverPendingMarketSummaryRecommendationsForScope(normalizeScopeCodes([]string{"002297.SZ"}))
	if err != nil {
		t.Fatalf("recoverPendingMarketSummaryRecommendationsForScope failed: %v", err)
	}
	if len(changed) != 0 {
		t.Fatalf("changed = %#v, want none without cached minute bars", changed)
	}
	if providerCalled {
		t.Fatalf("recover should not fetch minute bars from providers")
	}

	var got models.AiRecommendStocks
	if err := db.Dao.First(&got, row.ID).Error; err != nil {
		t.Fatalf("load row failed: %v", err)
	}
	if got.RecommendStatus != "missing_market_data" || got.ActivationStatus != "skipped" {
		t.Fatalf("unexpected status after cache-only recovery: recommend=%s activation=%s", got.RecommendStatus, got.ActivationStatus)
	}
}

func TestRecoverPendingMarketSummaryRecommendationsForScope_SkipsAnalysisOnlyRemarksSignal(t *testing.T) {
	withStubbedMinuteProviders(t)
	db.Init(filepath.Join(t.TempDir(), "market-summary-skip-remarks-only.db"))
	if err := db.Dao.AutoMigrate(
		&models.AiRecommendStocks{},
		&models.AiRecommendYieldDirtyCode{},
		&StockBasic{},
		&Settings{},
		&models.AiRecommendMinuteBar{},
	); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	loc := cnLocation()
	recordTime := time.Date(2026, 4, 8, 9, 40, 0, 0, loc)
	row := models.AiRecommendStocks{
		DataTime:                &recordTime,
		ModelName:               "test-model",
		StockCode:               "002960.SZ",
		StockName:               "青鸟消防",
		ExecutionState:          recommendExecutionAnalysisOnly,
		InvalidCondition:        marketSummaryAnalysisOnlySkipReason,
		RecommendStatus:         "missing_market_data",
		SummaryVersion:          marketSummaryPhase3Version,
		ActivationRuleSource:    "market_summary",
		ActivationStatus:        "invalid",
		ActivationInvalidReason: marketSummaryAnalysisOnlySkipReason,
		Remarks:                 "等待激活；优先看11.98附近是否形成有效突破。；激活条件：价格进入11.82-11.98区间，5分钟成交额不低于近5个5分钟平均成交额的1.3倍，1根5分钟K线确认，5个交易日内有效",
	}
	if err := db.Dao.Create(&row).Error; err != nil {
		t.Fatalf("create row failed: %v", err)
	}

	if marketSummaryRecommendMissingExitPlan(row) {
		t.Fatal("remarks-only analysis record should not be treated as missing exit plan")
	}

	changed, err := recoverPendingMarketSummaryRecommendationsForScope(normalizeScopeCodes([]string{"002960.SZ"}))
	if err != nil {
		t.Fatalf("recoverPendingMarketSummaryRecommendationsForScope failed: %v", err)
	}
	if len(changed) != 0 {
		t.Fatalf("changed = %#v, want none", changed)
	}

	var dirtyCount int64
	if err := db.Dao.Model(&models.AiRecommendYieldDirtyCode{}).Where("stock_code = ?", "002960.SZ").Count(&dirtyCount).Error; err != nil {
		t.Fatalf("count dirty failed: %v", err)
	}
	if dirtyCount != 0 {
		t.Fatalf("dirty count = %d, want 0", dirtyCount)
	}
}
