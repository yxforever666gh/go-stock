package data

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
)

const testActivationRuleJSON = `{"signalType":"price_range_with_volume","evaluationWindow":"5m","baseline":"avg_amount_5x5m","operator":">=","thresholdValue":168,"thresholdMax":169,"volumeRatio":1.2,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5}`

func TestHumanizeMarketSummaryReportRemovesActivationRuleJSON(t *testing.T) {
	raw := `# 推荐股票池
| 股票（代码） | 操作备注 |
| --- | --- |
| 中际旭创(300308.SZ) | 仅在量价共振时右侧跟随 activationRuleJson: ` + testActivationRuleJSON + ` |
`

	cleaned := HumanizeMarketSummaryReport(raw)
	if strings.Contains(cleaned, "activationRuleJson") || strings.Contains(cleaned, `"signalType"`) {
		t.Fatalf("expected report content to hide machine json, got %s", cleaned)
	}
	if !strings.Contains(cleaned, "激活条件：价格进入168-169区间") {
		t.Fatalf("expected report content to contain human summary, got %s", cleaned)
	}
}

func TestHumanizeMarketSummaryReportSupportsFullwidthColonAndBackticks(t *testing.T) {
	raw := "需落库时，应同步保存 `activationRuleJson`；下表“操作备注”已给出对应机器可读规则草案。\n操作备注：等待激活。activationRuleJson：`" + testActivationRuleJSON + "`"
	cleaned := HumanizeMarketSummaryReport(raw)
	if strings.Contains(cleaned, "activationRuleJson") || strings.Contains(cleaned, `"signalType"`) || strings.Contains(cleaned, "机器可读规则草案") {
		t.Fatalf("expected legacy machine wording removed, got %s", cleaned)
	}
	if !strings.Contains(cleaned, "激活条件：价格进入168-169区间") {
		t.Fatalf("expected fullwidth legacy format to be humanized, got %s", cleaned)
	}
}

func TestEnsureMarketSummaryRecommendStocksSavedHumanizesRemarks(t *testing.T) {
	setupMarketSummaryRecommendBackfillRealtimeEnv(t, "market-summary-humanize-save.db")
	startedAt := time.Date(2026, 4, 8, 10, 0, 0, 0, cnLocation())
	seedMinuteBars(t, "300308.SZ", []minuteBar{
		{TradeTime: startedAt.Add(-time.Minute), Open: 168.2, High: 168.6, Low: 168.1, Close: 168.4, Volume: 1200, Amount: 202080},
		{TradeTime: startedAt, Open: 168.4, High: 168.8, Low: 168.3, Close: 168.5, Volume: 1500, Amount: 252750},
	})

	summary := `# 推荐股票池
| 股票（代码） | 所属方向 | 核心催化 | 关键证据 | 价格锚点 | 买入区间 | 止盈区间 | 止损位 | 买入依据 | 失效条件 | 风险点 | 预期周期 | 事件强度 | 资金确认度 | 基本面匹配度 | 技术面匹配度 | 操作备注 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 中际旭创(300308.SZ) | AI算力 | 光模块景气持续 | [市场资讯] 板块强度高；[技术/资金/形态] 光模块方向5分钟放量突破前高 | 168.5 | 168-169 | 184-192 | 164 | 价格触发：股价进入168-169区间；量能触发：5分钟成交额不低于近5个5分钟均额的1.2倍 | 时间失效：未来5个交易日内未触发；价格失效：跌破164 | 高位波动风险较大 | 1-2周 | 90 | 85 | 78 | 88 | 仅在量价共振时右侧跟随 activationRuleJson: ` + testActivationRuleJSON + ` |
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
		t.Fatalf("query saved row failed: %v", err)
	}
	if strings.Contains(row.Remarks, "activationRuleJson") || strings.Contains(row.Remarks, `"signalType"`) {
		t.Fatalf("expected saved remarks to hide machine json, got %s", row.Remarks)
	}
	if !strings.Contains(row.Remarks, "激活条件：价格进入168-169区间") {
		t.Fatalf("expected saved remarks to contain human summary, got %s", row.Remarks)
	}
	if strings.TrimSpace(row.ActivationRuleJSON) == "" {
		t.Fatal("expected activation rule json to stay available for machine use")
	}
	if row.ProviderName != "TestProvider" {
		t.Fatalf("expected provider name saved, got %q", row.ProviderName)
	}
}

func TestAIResponseResultServiceGetListHumanizesMarketSummaryContent(t *testing.T) {
	initDatabaseForTest(t, filepath.Join(t.TempDir(), "market-summary-humanize-report.db"))
	if err := db.Dao.AutoMigrate(&models.AIResponseResult{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	report := &models.AIResponseResult{
		StockCode: "市场资讯",
		StockName: "市场资讯",
		Question:  "市场资讯分析",
		Content:   "操作备注 activationRuleJson: " + testActivationRuleJSON,
	}
	if err := db.Dao.Create(report).Error; err != nil {
		t.Fatalf("create report failed: %v", err)
	}

	service := NewAIResponseResultService()
	list, err := service.GetAIResponseResultList(models.AIResponseResultQuery{
		Page:     1,
		PageSize: 10,
	})
	if err != nil {
		t.Fatalf("GetAIResponseResultList failed: %v", err)
	}
	if len(list.List) != 1 {
		t.Fatalf("expected 1 report, got %d", len(list.List))
	}
	if strings.Contains(list.List[0].Content, "activationRuleJson") || strings.Contains(list.List[0].Content, `"signalType"`) {
		t.Fatalf("expected list content to hide machine json, got %s", list.List[0].Content)
	}
	if list.List[0].Question != DefaultMarketSummaryQuestion {
		t.Fatalf("expected normalized market summary question, got %s", list.List[0].Question)
	}
}

func TestRunMarketSummaryHumanizeCompatFixIsIdempotent(t *testing.T) {
	initDatabaseForTest(t, filepath.Join(t.TempDir(), "market-summary-humanize-compat.db"))
	if err := db.Dao.AutoMigrate(&models.AIResponseResult{}, &models.AiRecommendStocks{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	report := &models.AIResponseResult{
		StockCode: "市场资讯",
		StockName: "市场资讯",
		Question:  DefaultMarketSummaryQuestion,
		Content:   "操作备注 activationRuleJson: " + testActivationRuleJSON,
	}
	if err := db.Dao.Create(report).Error; err != nil {
		t.Fatalf("create report failed: %v", err)
	}

	dataTime := time.Date(2026, 4, 8, 10, 0, 0, 0, time.Local)
	recommend := &models.AiRecommendStocks{
		DataTime:                 &dataTime,
		StockCode:                "300308.SZ",
		StockName:                "中际旭创",
		ModelName:                "test-model",
		Remarks:                  "仅在量价共振时右侧跟随 activationRuleJson: " + testActivationRuleJSON,
		ActivationRuleJSON:       testActivationRuleJSON,
		RecommendBuyPrice:        "168-169",
		RecommendStatus:          "valid",
		SummaryVersion:           marketSummaryVersion150,
		ExecutionState:           recommendExecutionConditional,
		RecommendStopProfitPrice: "178-185",
		RecommendStopLossPrice:   "159",
	}
	if err := db.Dao.Create(recommend).Error; err != nil {
		t.Fatalf("create recommend failed: %v", err)
	}

	first, err := RunMarketSummaryHumanizeCompatFix()
	if err != nil {
		t.Fatalf("RunMarketSummaryHumanizeCompatFix first run failed: %v", err)
	}
	if first.ReportsScanned != 0 || first.ReportsUpdated != 0 || first.RemarksUpdated != 1 {
		t.Fatalf("unexpected first fix result: %+v", first)
	}

	second, err := RunMarketSummaryHumanizeCompatFix()
	if err != nil {
		t.Fatalf("RunMarketSummaryHumanizeCompatFix second run failed: %v", err)
	}
	if second.ReportsUpdated != 0 || second.RemarksUpdated != 0 {
		t.Fatalf("expected second run to be idempotent, got %+v", second)
	}

	var gotReport models.AIResponseResult
	if err := db.Dao.First(&gotReport, report.ID).Error; err != nil {
		t.Fatalf("reload report failed: %v", err)
	}
	if gotReport.Content != report.Content {
		t.Fatalf("versionless historical report was rewritten: before=%q after=%q", report.Content, gotReport.Content)
	}

	var gotRecommend models.AiRecommendStocks
	if err := db.Dao.First(&gotRecommend, recommend.ID).Error; err != nil {
		t.Fatalf("reload recommend failed: %v", err)
	}
	if strings.Contains(gotRecommend.Remarks, "activationRuleJson") || strings.Contains(gotRecommend.Remarks, `"signalType"`) {
		t.Fatalf("expected fixed remarks to hide machine json, got %s", gotRecommend.Remarks)
	}
}
