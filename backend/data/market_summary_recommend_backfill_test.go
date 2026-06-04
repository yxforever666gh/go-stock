package data

import (
	"go-stock/backend/models"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"go-stock/backend/db"
)

func setupMarketSummaryRecommendBackfillRealtimeEnv(t *testing.T, dbName string) {
	t.Helper()
	withStubbedMinuteProviders(t)
	db.Init(filepath.Join(t.TempDir(), dbName))
	if err := db.Dao.AutoMigrate(&models.AiRecommendStocks{}, &StockBasic{}, &Settings{}, &models.AiRecommendMinuteBar{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}
}

func TestMarketSummaryDraftProductionRejectionReason_V136RejectsWeakWorstEntryRewardRisk(t *testing.T) {
	draft := &marketSummaryRecommendDraft{
		StockCode:                   "300001.SZ",
		StockName:                   "测试股份",
		StockCurrentPrice:           "10.1",
		StockPrice:                  "10.1",
		RecommendBuyPrice:           "10.00-10.20",
		RecommendBuyPriceMin:        10,
		RecommendBuyPriceMax:        10.2,
		RecommendStopProfitPrice:    "10.25",
		RecommendStopProfitPriceMin: 10.25,
		RecommendStopProfitPriceMax: 10.25,
		RecommendStopLossPrice:      "10.00",
		ExecutionState:              recommendExecutionConditional,
		EventStrength:               80,
		CapitalConfirmation:         80,
		FundamentalFit:              80,
		TechnicalFit:                80,
		ActivationRuleJSON:          `{"signalType":"price_range_with_volume","evaluationWindow":"5m","baseline":"avg_amount_5x5m","operator":">=","thresholdValue":10.0,"thresholdMax":10.2,"volumeRatio":1.2,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5}`,
		ActivationRuleSource:        "market_summary",
		SummaryVersion:              marketSummaryVersion136,
	}

	reason := marketSummaryDraftProductionRejectionReason(draft)
	if !strings.Contains(reason, "最差成交价盈亏比") {
		t.Fatalf("reason = %q, want worst-entry reward/risk rejection", reason)
	}
}

func TestParseMarketSummaryRecommendStocksStructuredTable(t *testing.T) {
	setupMarketSummaryRecommendBackfillRealtimeEnv(t, "market-summary-structured-table.db")
	loc := cnLocation()
	dataTime := time.Date(2026, 3, 7, 10, 0, 0, 0, loc)
	seedMinuteBars(t, "300308.SZ", []minuteBar{
		{TradeTime: dataTime.Add(-time.Minute), Open: 168.2, High: 168.6, Low: 168.1, Close: 168.4, Volume: 1200, Amount: 202080},
		{TradeTime: dataTime, Open: 168.4, High: 168.8, Low: 168.3, Close: 168.5, Volume: 1500, Amount: 252750},
	})

	summary := `# 市场主线
- AI 算力仍是主线

# 候选方向
- 光模块与服务器链条继续强化

# 风险提示
- 高位分歧放大

# 推荐结论
- 当前只接受量价共振后的交易信号

# 交易计划说明
- 只输出能够形成完整买卖计划的股票

# 推荐股票池
| 股票（代码） | 所属方向 | 核心催化 | 关键证据 | 价格锚点 | 买入区间 | 止盈区间 | 止损位 | 买入依据 | 失效条件 | 风险点 | 预期周期 | 事件强度 | 资金确认度 | 基本面匹配度 | 技术面匹配度 | 操作备注 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 中际旭创(300308.SZ) | AI算力 | 光模块景气持续 | [市场资讯] 板块强度高；[技术/资金/形态] 光模块方向5分钟放量突破前高 | 168.5 | 168-169 | 184-192 | 164 | 股价回到168-169区间并在前高附近5分钟放量站稳后分批买入 | 跌破164或AI算力板块5分钟成交额较前一日同时段明显走弱则本次交易逻辑失效 | 高位波动大 | 1-2周 | 90 | 85 | 78 | 88 | 仅在量价共振时右侧跟随 |
`
	items := parseMarketSummaryRecommendStocks(summary, "TestProvider", "test-model", dataTime)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	item := items[0]
	if item.StockCode != "300308.SZ" {
		t.Fatalf("unexpected stock code: %s", item.StockCode)
	}
	if item.BkName != "AI算力" {
		t.Fatalf("unexpected bkName: %s", item.BkName)
	}
	if item.ProviderName != "TestProvider" {
		t.Fatalf("unexpected provider name: %s", item.ProviderName)
	}
	if item.RecommendCategory != "" {
		t.Fatalf("expected signal-driven item to clear legacy category, got: %s", item.RecommendCategory)
	}
	if item.ExecutionState != recommendExecutionConditional {
		t.Fatalf("unexpected execution state: %s", item.ExecutionState)
	}
	if item.BuySignal == "" || !strings.Contains(item.BuySignal, "168-169") {
		t.Fatalf("unexpected buy signal: %s", item.BuySignal)
	}
	if item.BuySignalDetail != "" {
		t.Fatalf("expected no extra buy detail in compact trade-plan row, got: %s", item.BuySignalDetail)
	}
	if item.SellSignal == "" || !strings.Contains(item.SellSignal, "184-192") {
		t.Fatalf("unexpected sell signal: %s", item.SellSignal)
	}
	if item.InvalidSignal == "" || !strings.Contains(item.InvalidSignal, "164") {
		t.Fatalf("unexpected invalid signal: %s", item.InvalidSignal)
	}
	if item.CoreCatalyst != "光模块景气持续" {
		t.Fatalf("unexpected core catalyst: %s", item.CoreCatalyst)
	}
	if item.RiskRemarks != "高位波动大" {
		t.Fatalf("unexpected risk remarks: %s", item.RiskRemarks)
	}
	if item.InvalidCondition == "" || !strings.Contains(item.InvalidCondition, "跌破164") {
		t.Fatalf("unexpected invalid condition: %s", item.InvalidCondition)
	}
	if item.FocusPrice != "" {
		t.Fatalf("expected signal-driven item to clear focus price, got: %s", item.FocusPrice)
	}
	if item.RecommendBuyPrice != "168-169" {
		t.Fatalf("unexpected recommend buy price: %s", item.RecommendBuyPrice)
	}
	if !strings.Contains(item.ActivationRuleJSON, "\"signalType\"") {
		t.Fatalf("expected activation rule json, got %s", item.ActivationRuleJSON)
	}
	if item.ExpectedCycle != "1-2周" {
		t.Fatalf("unexpected expected cycle: %s", item.ExpectedCycle)
	}
	if item.EventStrength != 90 || item.CapitalConfirmation != 85 || item.FundamentalFit != 78 || item.TechnicalFit != 88 {
		t.Fatalf("unexpected confidence values: %+v", item)
	}
	if item.Remarks != "仅在量价共振时右侧跟随" {
		t.Fatalf("unexpected remarks: %s", item.Remarks)
	}
	if item.RecommendReason == "" || !containsAll(item.RecommendReason, []string{"核心催化：光模块景气持续", "买入区间：168-169", "买入依据：股价回到168-169区间并在前高附近5分钟放量站稳后分批买入", "失效条件：跌破164或AI算力板块5分钟成交额较前一日同时段明显走弱则本次交易逻辑失效"}) {
		t.Fatalf("unexpected recommend reason: %s", item.RecommendReason)
	}
	if !strings.Contains(item.EvidenceSources, "市场资讯") || !strings.Contains(item.EvidenceSources, "技术/资金/形态") {
		t.Fatalf("unexpected evidence sources: %s", item.EvidenceSources)
	}
}

func TestParseMarketSummaryRecommendStockDraftsAndToRecommendStock(t *testing.T) {
	setupMarketSummaryRecommendBackfillRealtimeEnv(t, "market-summary-drafts-table.db")
	loc := cnLocation()
	dataTime := time.Date(2026, 3, 7, 10, 0, 0, 0, loc)
	seedMinuteBars(t, "300308.SZ", []minuteBar{
		{TradeTime: dataTime.Add(-time.Minute), Open: 168.1, High: 168.5, Low: 168.0, Close: 168.3, Volume: 1100, Amount: 185130},
		{TradeTime: dataTime, Open: 168.3, High: 168.7, Low: 168.2, Close: 168.5, Volume: 1400, Amount: 235900},
	})

	summary := `# 推荐股票池
| 股票（代码） | 所属方向 | 核心催化 | 关键证据 | 价格锚点 | 买入区间 | 止盈区间 | 止损位 | 买入依据 | 失效条件 | 风险点 | 预期周期 | 事件强度 | 资金确认度 | 基本面匹配度 | 技术面匹配度 | 操作备注 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 中际旭创(300308.SZ) | AI算力 | 光模块景气持续 | [市场资讯] 板块强度高；[技术/资金/形态] 光模块方向5分钟放量突破前高 | 168.5 | 168-169 | 184-192 | 164 | 股价回到168-169区间并在前高附近5分钟放量站稳后分批买入 | 跌破164或AI算力板块5分钟成交额较前一日同时段明显走弱则本次交易逻辑失效 | 高位波动大 | 1-2周 | 90 | 85 | 78 | 88 | 仅在量价共振时右侧跟随 |
`
	drafts := parseMarketSummaryRecommendStockDrafts(summary, "TestProvider", "test-model", dataTime)
	if len(drafts) != 1 {
		t.Fatalf("expected 1 draft, got %d", len(drafts))
	}
	if drafts[0].StockCode != "300308.SZ" {
		t.Fatalf("unexpected draft stock code: %s", drafts[0].StockCode)
	}
	if drafts[0].ProviderName != "TestProvider" {
		t.Fatalf("unexpected draft provider name: %s", drafts[0].ProviderName)
	}
	item, err := drafts[0].toRecommendStock()
	if err != nil {
		t.Fatalf("toRecommendStock failed: %v", err)
	}
	if item.StockCode != "300308.SZ" {
		t.Fatalf("unexpected stock code: %s", item.StockCode)
	}
	if item.RecommendReason == "" || !containsAll(item.RecommendReason, []string{"核心催化：光模块景气持续", "买入区间：168-169"}) {
		t.Fatalf("unexpected recommend reason: %s", item.RecommendReason)
	}
}

func TestParseMarketSummaryRecommendStockDraftsOnlyUsesRecommendSection(t *testing.T) {
	setupMarketSummaryRecommendBackfillRealtimeEnv(t, "market-summary-recommend-section-only.db")
	dataTime := time.Date(2026, 4, 29, 14, 30, 0, 0, cnLocation())

	summary := `# 推荐股票池
| 股票（代码） | 所属方向 | 核心催化 | 关键证据 | 价格锚点 | 买入区间 | 止盈区间 | 止损位 | 买入依据 | 失效条件 | 风险点 | 预期周期 | 事件强度 | 资金确认度 | 基本面匹配度 | 技术面匹配度 | 操作备注 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 阳光电源(300274.SZ) | 风光储氢充绿电设备 | 绿电会议催化 | [市场资讯] 会议召开；[一级披露] 一季报披露 | 138.77 | 133.30-136.20 | 145.80-151.50 | 131.80 | 价格触发：未来5个交易日内，回踩路径为5分钟收盘价进入133.30-136.20；突破路径为5分钟收盘价>=139.20；量能触发：在133.30-136.20区间或139.20突破价附近，5分钟成交额>=近5个5分钟均额的1.20倍，确认1根5分钟K线 | 时间失效：未来5个交易日内未同时触发价格与量能条件则失效；价格失效：触发前或触发后5分钟收盘价<131.80则失效 | 会议兑现后可能回落 | 3-5个交易日 | 80 | 80 | 80 | 80 | 等待激活 |

# 跳过复审
| 原记录ID | 股票（代码） | 复审结论 | 买入区间 | 止盈区间 | 止损位 | 买入依据 | 失效条件 | 跳过/复审说明 |
|---|---|---|---|---|---|---|---|---|
| 327 | 中大力德（002896.SZ） | 继续跳过 | - | - | - | - | - | 继续跳过 |
`

	drafts := parseMarketSummaryRecommendStockDrafts(summary, "TestProvider", "test-model", dataTime)
	if len(drafts) != 1 {
		t.Fatalf("expected only recommend-section row, got %d", len(drafts))
	}
	if drafts[0].StockCode != "300274.SZ" {
		t.Fatalf("unexpected stock code: %s", drafts[0].StockCode)
	}
}

func TestParseMarketSummaryBuyRange_PrefersPullbackSegment(t *testing.T) {
	text, min, max := parseMarketSummaryBuyRange("398.80-401.20（回踩激活）；404.80-407.00上破确认（突破激活）")
	if text != "398.80-401.20" {
		t.Fatalf("buy range text = %q, want 398.80-401.20", text)
	}
	if min != 398.8 || max != 401.2 {
		t.Fatalf("buy range min/max = %.2f/%.2f, want 398.80/401.20", min, max)
	}
}

func containsAll(text string, subs []string) bool {
	for _, sub := range subs {
		if !strings.Contains(text, sub) {
			return false
		}
	}
	return true
}

func TestCollectMarketSummaryRecommendStocksForSaveSkipsAvoid(t *testing.T) {
	parsed := []*marketSummaryRecommendDraft{
		{StockCode: "002747.SZ", StockName: "埃斯顿", RecommendCategory: "right_confirm"},
		{StockCode: "002896.SZ", StockName: "中大力德", RecommendCategory: "avoid"},
	}

	missing := collectMarketSummaryRecommendStocksForSave(parsed, nil)
	if len(missing) != 1 {
		t.Fatalf("expected 1 savable item, got %d", len(missing))
	}
	if missing[0].StockCode != "002747.SZ" {
		t.Fatalf("unexpected stock code: %s", missing[0].StockCode)
	}
}

func TestCollectMarketSummaryRecommendStocksForSaveSkipsSameDayExistingStock(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "market-summary-backfill-test.db"))
	if err := db.Dao.AutoMigrate(&models.AiRecommendStocks{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	startedAt := time.Date(2026, 3, 16, 9, 35, 0, 0, time.Local)
	existingTime := startedAt.Add(-3 * time.Hour)
	existing := &models.AiRecommendStocks{
		DataTime:                    &existingTime,
		ModelName:                   "market-summary-auto-backfill",
		StockCode:                   "300308.SZ",
		StockName:                   "中际旭创",
		BkName:                      "AI算力",
		StockPrice:                  "168.5",
		StockCurrentPrice:           "168.5",
		StockCurrentPriceTime:       existingTime.Format(time.DateTime),
		StockClosePrice:             "168.5",
		StockPrePrice:               "168.5",
		RecommendReason:             "核心催化：光模块景气持续\n关键证据：板块强度高",
		RecommendBuyPrice:           "165-169",
		RecommendBuyPriceMin:        165,
		RecommendBuyPriceMax:        169,
		RecommendStopProfitPrice:    "178-185",
		RecommendStopProfitPriceMin: 178,
		RecommendStopProfitPriceMax: 185,
		RecommendStopLossPrice:      "159",
		RecommendCategory:           "right_confirm",
		CoreCatalyst:                "光模块景气持续",
		KeyEvidence:                 "板块强度高",
		InvalidCondition:            "板块成交额明显走弱",
		ObservePrice:                "168.5",
		FocusPrice:                  "165-169",
		ExpectedCycle:               "1-2周",
		RecommendStatus:             "valid",
		SummaryVersion:              marketSummaryPhase3Version,
		RiskRemarks:                 "高位波动大",
		Remarks:                     "auto-backfill-market-summary",
	}
	if err := db.Dao.Create(existing).Error; err != nil {
		t.Fatalf("seed existing record failed: %v", err)
	}

	summary := `# 推荐股票池
| 股票（代码） | 所属方向 | 核心催化 | 关键证据 | 价格锚点 | 买入区间 | 止盈区间 | 止损位 | 买入依据 | 失效条件 | 风险点 | 预期周期 | 事件强度 | 资金确认度 | 基本面匹配度 | 技术面匹配度 | 操作备注 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 中际旭创(300308.SZ) | AI算力 | 光模块景气持续 | [市场资讯] 板块强度高；[技术/资金/形态] 光模块方向5分钟放量突破前高 | 168.5 | 168-169 | 178-185 | 159 | 股价回到168-169区间并在前高附近5分钟放量站稳后分批买入 | 跌破159或AI算力板块5分钟成交额较前一日同时段明显走弱则本次交易逻辑失效 | 高位波动风险较大 | 1-2周 | 90 | 85 | 78 | 88 | 仅在量价共振时右侧跟随 |
| 新易盛(300502.SZ) | AI算力 | 光模块景气扩散 | [市场资讯] 资金持续流入；[技术/资金/形态] 量能持续放大 | 92.3 | 91-93 | 98-102 | 88 | 股价回到91-93区间并在前高附近5分钟放量站稳后分批买入 | 跌破88或板块转弱则本次交易逻辑失效 | 短线波动风险较大 | 1-2周 | 82 | 80 | 76 | 84 | 放量站稳后执行右侧突破计划 |
`

	saved, err := EnsureMarketSummaryRecommendStocksSaved(summary, "TestProvider", "test-model", startedAt)
	if err != nil {
		t.Fatalf("EnsureMarketSummaryRecommendStocksSaved failed: %v", err)
	}
	if saved != 1 {
		t.Fatalf("expected save count 1, got %d", saved)
	}

	var total int64
	dayStart, dayEnd := marketSummaryDayBounds(startedAt)
	if err := db.Dao.Model(&models.AiRecommendStocks{}).
		Where("data_time >= ? AND data_time < ?", dayStart, dayEnd).
		Count(&total).Error; err != nil {
		t.Fatalf("count records failed: %v", err)
	}
	if total != 2 {
		t.Fatalf("expected total 2 records for same day, got %d", total)
	}
}

func TestEnsureMarketSummaryRecommendStocksSavedSkipsObservationalRows(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "market-summary-observe-skip.db"))
	if err := db.Dao.AutoMigrate(&models.AiRecommendStocks{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	startedAt := time.Date(2026, 4, 1, 9, 35, 0, 0, time.Local)
	summary := `# 推荐股票池
| 股票（代码） | 所属方向 | 核心催化 | 关键证据 | 价格锚点 | 买入区间 | 止盈区间 | 止损位 | 买入依据 | 失效条件 | 风险点 | 预期周期 | 事件强度 | 资金确认度 | 基本面匹配度 | 技术面匹配度 | 操作备注 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 三环集团(300408.SZ) | 被动元件 | 景气修复 | [市场资讯] 海外提价；[一级披露] 董事会公告 | 52.85 | 52.00-53.20观察，只有重新站回54.20上方并放量时才考虑转入右侧跟踪 | 56.50-58.50 | 51.80 | 只有重新站回54.20上方并放量时才考虑转入右侧跟踪 | 跌破51.80且无资金回流 | 活跃度高但分歧大 | 1-4周 | 75 | 65 | 70 | 68 | 仅观察，不建议先手 |
`

	saved, err := EnsureMarketSummaryRecommendStocksSaved(summary, "TestProvider", "test-model", startedAt)
	if err != nil {
		t.Fatalf("EnsureMarketSummaryRecommendStocksSaved failed: %v", err)
	}
	if saved != 0 {
		t.Fatalf("expected save count 0, got %d", saved)
	}

	var total int64
	if err := db.Dao.Model(&models.AiRecommendStocks{}).Count(&total).Error; err != nil {
		t.Fatalf("count records failed: %v", err)
	}
	if total != 0 {
		t.Fatalf("expected no saved records, got %d", total)
	}
}

func TestEnsureMarketSummaryRecommendStocksSavedFromRuntimeReport20260407_1130(t *testing.T) {
	setupMarketSummaryRecommendBackfillRealtimeEnv(t, "market-summary-runtime-20260407.db")
	loc := cnLocation()
	startedAt := time.Date(2026, 4, 7, 11, 30, 0, 0, loc)
	seedMinuteBars(t, "002371.SZ", []minuteBar{
		{TradeTime: startedAt.Add(-time.Minute), Open: 360.1, High: 361.2, Low: 359.8, Close: 360.6, Volume: 900, Amount: 324540},
		{TradeTime: startedAt, Open: 360.6, High: 361.5, Low: 360.2, Close: 360.8, Volume: 980, Amount: 353584},
	})
	seedMinuteBars(t, "300124.SZ", []minuteBar{
		{TradeTime: startedAt.Add(-time.Minute), Open: 167.1, High: 167.8, Low: 166.8, Close: 167.4, Volume: 1300, Amount: 217620},
		{TradeTime: startedAt, Open: 167.4, High: 168.1, Low: 167.2, Close: 167.6, Volume: 1500, Amount: 251400},
	})
	seedMinuteBars(t, "300308.SZ", []minuteBar{
		{TradeTime: startedAt.Add(-time.Minute), Open: 169.2, High: 169.8, Low: 168.9, Close: 169.4, Volume: 1200, Amount: 203280},
		{TradeTime: startedAt, Open: 169.4, High: 170.0, Low: 169.1, Close: 169.6, Volume: 1450, Amount: 245920},
	})

	summary := `# 推荐股票池
| 股票（代码） | 所属方向 | 核心催化 | 关键证据 | 价格锚点 | 买入区间 | 止盈区间 | 止损位 | 买入依据 | 失效条件 | 风险点 | 预期周期 | 事件强度 | 资金确认度 | 基本面匹配度 | 技术面匹配度 | 操作备注 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 北方华创(002371.SZ) | 半导体设备 | 设备链景气修复 | [市场资讯] 订单预期回暖；[技术/资金/形态] 30分钟量价共振 | 360.8 | 355-365 | 382-398 | 346 | 股价进入355-365区间后，30分钟成交额不低于近5个30分钟均额的1.2倍再跟随 | 跌破346，或半导体设备板块30分钟成交额低于前一交易日同一时段的0.9倍则逻辑失效 | 高位震荡较大，需防设备链分歧 | 1-2周 | 86 | 82 | 80 | 84 | 等待量价确认后执行 |
| 汇川技术(300124.SZ) | 工业自动化 | 自动化景气改善 | [市场资讯] 下游开工改善；[技术/资金/形态] 15分钟放量修复 | 167.6 | 166-169 | 176-182 | 161 | 股价回到166-169区间并在15分钟维度放量站稳后分批买入 | 跌破161，或工业自动化板块15分钟成交额低于前一交易日同一时段的0.9倍则逻辑失效 | 波动放大明显，需防冲高回落 | 1-2周 | 80 | 78 | 82 | 79 | 仅在量价共振时右侧跟随 |
| 中际旭创(300308.SZ) | AI算力 | 光模块景气持续 | [市场资讯] 板块强度高；[技术/资金/形态] 15分钟放量突破前高 | 169.6 | 168-172 | 180-188 | 163 | 股价回到168-172区间并在15分钟维度放量站稳后分批买入 | 跌破163，或AI算力板块15分钟成交额低于前一交易日同一时段的0.9倍则逻辑失效 | 高位波动较大 | 1-2周 | 90 | 85 | 78 | 88 | 仅在量价共振时右侧跟随 |
`

	saved, err := EnsureMarketSummaryRecommendStocksSaved(summary, "TestProvider", "runtime-test-model", startedAt)
	if err != nil {
		t.Fatalf("EnsureMarketSummaryRecommendStocksSaved failed: %v", err)
	}
	if saved != 3 {
		t.Fatalf("expected save count 3, got %d", saved)
	}

	var rows []models.AiRecommendStocks
	if err := db.Dao.Order("stock_code asc").Find(&rows).Error; err != nil {
		t.Fatalf("query saved rows failed: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 saved rows, got %d", len(rows))
	}

	wantCodes := []string{"002371.SZ", "300124.SZ", "300308.SZ"}
	for idx, row := range rows {
		if row.StockCode != wantCodes[idx] {
			t.Fatalf("unexpected stock code at %d: got=%s want=%s", idx, row.StockCode, wantCodes[idx])
		}
		if row.SummaryVersion != marketSummaryCurrentVersion {
			t.Fatalf("unexpected summary version for %s: %s", row.StockCode, row.SummaryVersion)
		}
		if row.ExecutionState != recommendExecutionAnalysisOnly {
			t.Fatalf("expected analysis_only execution state for %s, got %s", row.StockCode, row.ExecutionState)
		}
		if row.ActivationRuleJSON != "" {
			t.Fatalf("expected downgraded record to clear activation rule for %s", row.StockCode)
		}
		if row.RecommendBuyPrice != "" || row.RecommendStopProfitPrice != "" || row.RecommendStopLossPrice != "" {
			t.Fatalf("expected downgraded record to clear trade plan for %s", row.StockCode)
		}
		if !strings.Contains(row.InvalidCondition, "V1.3.6源头质量门槛未通过") &&
			!strings.Contains(row.InvalidCondition, "缺少真实价格/量能数据") &&
			!strings.Contains(row.InvalidCondition, "超出当次市场总结最多2只生产候选上限") {
			t.Fatalf("expected downgrade reason recorded for %s, got %s", row.StockCode, row.InvalidCondition)
		}
	}
}

func TestEnsureMarketSummaryRecommendStocksSavedDowngradesPriceMismatchToAnalysisOnly(t *testing.T) {
	withStubbedMinuteProviders(t)
	db.Init(filepath.Join(t.TempDir(), "market-summary-price-mismatch.db"))
	if err := db.Dao.AutoMigrate(&models.AiRecommendStocks{}, &StockBasic{}, &Settings{}, &models.AiRecommendMinuteBar{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	loc := cnLocation()
	startedAt := time.Date(2026, 4, 7, 11, 30, 0, 0, loc)
	seedMinuteBars(t, "300308.SZ", []minuteBar{
		{TradeTime: startedAt.Add(-time.Minute), Open: 600, High: 603, Low: 598, Close: 600.5, Volume: 1000, Amount: 600500},
		{TradeTime: startedAt, Open: 600.5, High: 602, Low: 599.5, Close: 601.2, Volume: 1100, Amount: 661320},
	})

	summary := `# 推荐股票池
| 股票（代码） | 所属方向 | 核心催化 | 关键证据 | 价格锚点 | 买入区间 | 止盈区间 | 止损位 | 买入依据 | 失效条件 | 风险点 | 预期周期 | 事件强度 | 资金确认度 | 基本面匹配度 | 技术面匹配度 | 操作备注 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 中际旭创(300308.SZ) | AI算力 | 光模块景气持续 | [市场资讯] 板块强度高；[技术/资金/形态] 光模块方向15分钟放量突破前高 | 170 | 168-172 | 180-188 | 163 | 股价回到168-172区间并在前高附近15分钟放量站稳后分批买入 | 跌破163或AI算力板块15分钟成交额较前一日同时段明显走弱则本次交易逻辑失效 | 高位波动风险较大 | 1-2周 | 90 | 85 | 78 | 88 | 仅在量价共振时右侧跟随 |
`

	saved, err := EnsureMarketSummaryRecommendStocksSaved(summary, "TestProvider", "test-model", startedAt)
	if err != nil {
		t.Fatalf("EnsureMarketSummaryRecommendStocksSaved failed: %v", err)
	}
	if saved != 1 {
		t.Fatalf("expected save count 1, got %d", saved)
	}

	var row models.AiRecommendStocks
	if err := db.Dao.First(&row).Error; err != nil {
		t.Fatalf("query saved record failed: %v", err)
	}
	if row.RecommendStatus != "missing_market_data" {
		t.Fatalf("recommend status = %s, want missing_market_data", row.RecommendStatus)
	}
	if row.ExecutionState != recommendExecutionAnalysisOnly {
		t.Fatalf("execution state = %s, want analysis_only", row.ExecutionState)
	}
	if row.RecommendBuyPrice != "" || row.RecommendStopProfitPrice != "" || row.RecommendStopLossPrice != "" {
		t.Fatalf("expected trade fields cleared after downgrade: %+v", row)
	}
}

func TestPrepareMarketSummaryReportForPersistence_DropsDuplicatesAndHumanizesAnalysisOnlyRows(t *testing.T) {
	withStubbedMinuteProviders(t)
	db.Init(filepath.Join(t.TempDir(), "market-summary-prepare-report.db"))
	if err := db.Dao.AutoMigrate(&models.AiRecommendStocks{}, &StockBasic{}, &Settings{}, &models.AiRecommendMinuteBar{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	loc := cnLocation()
	startedAt := time.Date(2026, 4, 7, 11, 30, 0, 0, loc)
	existingTime := startedAt.Add(-2 * time.Hour)
	existing := &models.AiRecommendStocks{
		DataTime:        &existingTime,
		StockCode:       "300308.SZ",
		StockName:       "中际旭创",
		BkName:          "AI算力",
		RecommendReason: "seed",
		RiskRemarks:     "seed risk",
		SummaryVersion:  marketSummaryPhase3Version,
	}
	if err := db.Dao.Create(existing).Error; err != nil {
		t.Fatalf("seed existing record failed: %v", err)
	}
	seedMinuteBars(t, "002371.SZ", []minuteBar{
		{TradeTime: startedAt, Open: 360, High: 362, Low: 358, Close: 360.8, Volume: 900, Amount: 324720},
	})

	summary := `# 推荐股票池
| 股票（代码） | 所属方向 | 核心催化 | 关键证据 | 价格锚点 | 买入区间 | 止盈区间 | 止损位 | 买入依据 | 失效条件 | 风险点 | 预期周期 | 事件强度 | 资金确认度 | 基本面匹配度 | 技术面匹配度 | 操作备注 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 中际旭创(300308.SZ) | AI算力 | 光模块景气持续 | [市场资讯] 板块强度高 | 170 | 168-172 | 180-188 | 163 | 股价回到168-172区间并放量站稳后分批买入 | 跌破163则失效 | 高位波动大 | 1-2周 | 90 | 85 | 78 | 88 | 已在早盘推荐 |
| 北方华创(002371.SZ) | 半导体设备 | 展会催化强化 | [市场资讯] 设备链走强 | 170 | 168-172 | 180-188 | 163 | 股价回到168-172区间并放量站稳后分批买入 | 跌破163则失效 | 波动较大 | 1-2周 | 82 | 79 | 76 | 81 | 原始锚点待核对 |
`

	prepared, stats, err := PrepareMarketSummaryReportForPersistence(summary, startedAt)
	if err != nil {
		t.Fatalf("PrepareMarketSummaryReportForPersistence failed: %v", err)
	}
	if stats.DuplicateRowsOmit != 1 {
		t.Fatalf("duplicate omit = %d, want 1", stats.DuplicateRowsOmit)
	}
	if stats.AnalysisOnlyRows != 1 {
		t.Fatalf("analysis only rows = %d, want 1", stats.AnalysisOnlyRows)
	}
	if strings.Contains(prepared, "中际旭创(300308.SZ)") {
		t.Fatalf("expected duplicate stock to be removed from prepared report: %s", prepared)
	}
	if !strings.Contains(prepared, "北方华创(002371.SZ)") {
		t.Fatalf("expected remaining stock row in prepared report: %s", prepared)
	}
	if !strings.Contains(prepared, "仅保留逻辑分析") {
		t.Fatalf("expected analysis-only explanation in prepared report: %s", prepared)
	}
	if !strings.Contains(prepared, "360.8") {
		t.Fatalf("expected prepared report to anchor to real reference price: %s", prepared)
	}
}

func TestEnsureMarketSummaryYieldOverridesSaved(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "market-summary-yield-override.db"))
	if err := db.Dao.AutoMigrate(&models.AiRecommendStocks{}, &models.AiRecommendYieldOverride{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	loc := time.Local
	recordTime := time.Date(2026, 4, 2, 9, 35, 0, 0, loc)
	startedAt := time.Date(2026, 4, 4, 18, 0, 0, 0, loc)

	skipped := &models.AiRecommendStocks{
		DataTime:          &recordTime,
		ModelName:         "test-model",
		StockCode:         "002281.SZ",
		StockName:         "光迅科技",
		BkName:            "CPO",
		RecommendBuyPrice: "51.2-52.0",
		BuySignal:         "价格触发：站回51.2-52.0；量能触发：5分钟量比≥1.3；逻辑触发：CPO分支继续加强",
		InvalidSignal:     "时间失效：3个交易日内未触发；价格失效：跌破50.2；逻辑失效：板块转弱",
		InvalidCondition:  "时间失效：3个交易日内未触发；价格失效：跌破50.2；逻辑失效：板块转弱",
		RecommendCategory: "observe",
		RecommendStatus:   "insufficient_evidence",
		ExecutionState:    recommendExecutionConditional,
		SummaryVersion:    marketSummaryPhase3Version,
		RiskRemarks:       "波动较大",
		Remarks:           "seed skipped",
	}
	reactivate := &models.AiRecommendStocks{
		DataTime:                    &recordTime,
		ModelName:                   "test-model",
		StockCode:                   "002463.SZ",
		StockName:                   "沪电股份",
		BkName:                      "PCB",
		RecommendBuyPrice:           "33.0-33.8",
		RecommendStopProfitPrice:    "35.5-36.8",
		RecommendStopProfitPriceMin: 35.5,
		RecommendStopProfitPriceMax: 36.8,
		RecommendStopLossPrice:      "32.2",
		BuySignal:                   "价格触发：回踩33.0-33.8企稳；量能触发：5分钟量比≥1.2；逻辑触发：高阶 PCB 订单预期强化",
		InvalidSignal:               "时间失效：4个交易日内未触发；价格失效：跌破32.2；逻辑失效：板块分歧扩大",
		InvalidCondition:            "时间失效：4个交易日内未触发；价格失效：跌破32.2；逻辑失效：板块分歧扩大",
		RecommendCategory:           "observe",
		RecommendStatus:             "insufficient_evidence",
		ExecutionState:              recommendExecutionConditional,
		SummaryVersion:              marketSummaryPhase3Version,
		RiskRemarks:                 "追高风险",
		Remarks:                     "seed reactivate",
	}
	if err := db.Dao.Create(skipped).Error; err != nil {
		t.Fatalf("create skipped seed failed: %v", err)
	}
	if err := db.Dao.Create(reactivate).Error; err != nil {
		t.Fatalf("create reactivate seed failed: %v", err)
	}

	summary := `# 市场主线
- 算力硬件分支轮动

# 候选方向
- 只保留具备完整计划的股票

# 风险提示
- 高位波动大

# 推荐结论
- 当日推荐和复审分开处理

# 交易计划说明
- 已跳过股票需要单独复审

# 推荐股票池
| 股票（代码） | 所属方向 | 核心催化 | 关键证据 | 价格锚点 | 买入区间 | 止盈区间 | 止损位 | 买入依据 | 失效条件 | 风险点 | 预期周期 | 事件强度 | 资金确认度 | 基本面匹配度 | 技术面匹配度 | 操作备注 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 暂无高质量候选标的 | - | - | - | - | - | - | - | - | - | - | - | - | - | - | - | - |

# 跳过复审
| 原记录ID | 股票（代码） | 复审结论 | 买入区间 | 止盈区间 | 止损位 | 买入依据 | 失效条件 | 跳过/复审说明 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| ` + strconv.FormatUint(uint64(skipped.ID), 10) + ` | 光迅科技(002281.SZ) | 继续跳过 | - | - | - | - | 时间失效：旧逻辑已过期；价格失效：无；逻辑失效：缺少新增催化 | 仍未形成未来3-5个交易日内可验证的买点，继续跳过 |
| ` + strconv.FormatUint(uint64(reactivate.ID), 10) + ` | 沪电股份(002463.SZ) | 重新纳入 | 34.5-35.2 | 37.2-38.6 | 33.8 | 价格触发：34.5-35.2 区间回踩不破；量能触发：5分钟成交额≥近5根5分钟均额的1.2倍；逻辑触发：高阶 PCB 业绩预期继续强化 | 时间失效：4个交易日内未触发；价格失效：跌破33.8；逻辑失效：PCB 方向转弱 | 旧的跳过依据已被新催化修正，改判恢复收益率跟踪 |
`

	saved, err := EnsureMarketSummaryYieldOverridesSaved(summary, startedAt)
	if err != nil {
		t.Fatalf("EnsureMarketSummaryYieldOverridesSaved failed: %v", err)
	}
	if saved != 2 {
		t.Fatalf("expected save count 2, got %d", saved)
	}

	rows := make([]models.AiRecommendYieldOverride, 0, 2)
	if err := db.Dao.Order("recommend_id asc").Find(&rows).Error; err != nil {
		t.Fatalf("query overrides failed: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 override rows, got %d", len(rows))
	}

	if rows[0].RecommendID != skipped.ID {
		t.Fatalf("unexpected first recommend id: %d", rows[0].RecommendID)
	}
	if rows[0].ActivationStatusOverride != "skipped" {
		t.Fatalf("expected skipped override, got %s", rows[0].ActivationStatusOverride)
	}
	if !strings.Contains(rows[0].DataStatusReason, "继续跳过") && !strings.Contains(rows[0].DataStatusReason, "仍未形成") {
		t.Fatalf("unexpected skipped reason: %s", rows[0].DataStatusReason)
	}

	if rows[1].RecommendID != reactivate.ID {
		t.Fatalf("unexpected second recommend id: %d", rows[1].RecommendID)
	}
	if rows[1].ActivationStatusOverride != "pending" {
		t.Fatalf("expected pending override, got %s", rows[1].ActivationStatusOverride)
	}
	if rows[1].RecommendBuyPrice != "34.5-35.2" {
		t.Fatalf("unexpected buy range: %s", rows[1].RecommendBuyPrice)
	}
	if rows[1].RecommendStopProfitPrice != "37.2-38.6" {
		t.Fatalf("unexpected stop profit range: %s", rows[1].RecommendStopProfitPrice)
	}
	if rows[1].RecommendStopLossPrice != "33.8" {
		t.Fatalf("unexpected stop loss: %s", rows[1].RecommendStopLossPrice)
	}
	if !strings.Contains(rows[1].BuySignal, "价格触发") {
		t.Fatalf("unexpected buy signal: %s", rows[1].BuySignal)
	}
	if !strings.Contains(rows[1].InvalidCondition, "时间失效") {
		t.Fatalf("unexpected invalid condition: %s", rows[1].InvalidCondition)
	}
	if !strings.Contains(rows[1].DataStatusReason, "改判恢复收益率跟踪") {
		t.Fatalf("unexpected review reason: %s", rows[1].DataStatusReason)
	}
}
