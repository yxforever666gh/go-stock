package data

import (
	"encoding/json"
	"go-stock/backend/db"
	"go-stock/backend/models"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParsePriceAverageFromText(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected float64
		ok       bool
	}{
		{name: "range-hyphen", input: "12-14", expected: 13, ok: true},
		{name: "range-tilde", input: "12~14元", expected: 13, ok: true},
		{name: "single", input: "9.88", expected: 9.88, ok: true},
		{name: "text-range", input: "止损区间10.2至10.8", expected: 10.5, ok: true},
		{name: "invalid", input: "无", expected: 0, ok: false},
	}

	for _, tt := range tests {
		got, ok := parsePriceAverageFromText(tt.input)
		if ok != tt.ok {
			t.Fatalf("%s: expected ok=%v, got %v", tt.name, tt.ok, ok)
		}
		if tt.ok && round2(got) != round2(tt.expected) {
			t.Fatalf("%s: expected %.2f, got %.2f", tt.name, tt.expected, got)
		}
	}
}

func TestParseStopProfitPrice(t *testing.T) {
	tests := []struct {
		name     string
		input    models.AiRecommendStocks
		expected float64
		ok       bool
	}{
		{
			name: "min-max-priority",
			input: models.AiRecommendStocks{
				RecommendStopProfitPriceMin: 12,
				RecommendStopProfitPriceMax: 14,
				RecommendStopProfitPrice:    "18-20",
			},
			expected: 12,
			ok:       true,
		},
		{
			name: "single-min",
			input: models.AiRecommendStocks{
				RecommendStopProfitPriceMin: 15,
			},
			expected: 15,
			ok:       true,
		},
		{
			name: "fallback-string",
			input: models.AiRecommendStocks{
				RecommendStopProfitPrice: "11.5-12.5",
			},
			expected: 11.5,
			ok:       true,
		},
		{
			name: "no-value",
			input: models.AiRecommendStocks{
				RecommendStopProfitPrice: "无",
			},
			expected: 0,
			ok:       false,
		},
	}

	for _, tt := range tests {
		got, ok := parseStopProfitPrice(tt.input)
		if ok != tt.ok {
			t.Fatalf("%s: expected ok=%v, got %v", tt.name, tt.ok, ok)
		}
		if tt.ok && round2(got) != round2(tt.expected) {
			t.Fatalf("%s: expected %.2f, got %.2f", tt.name, tt.expected, got)
		}
	}
}

func TestParsePriceMinMaxFromText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		min   float64
		max   float64
		ok    bool
		okMin bool
		okMax bool
	}{
		{name: "range-hyphen", input: "12-14", min: 12, max: 14, ok: true, okMin: true, okMax: true},
		{name: "range-reversed", input: "14-12", min: 12, max: 14, ok: true, okMin: true, okMax: true},
		{name: "single", input: "9.88", min: 9.88, max: 9.88, ok: true, okMin: true, okMax: true},
		{name: "text-range", input: "止损区间10.2至10.8", min: 10.2, max: 10.8, ok: true, okMin: true, okMax: true},
		{name: "invalid", input: "无", min: 0, max: 0, ok: false, okMin: false, okMax: false},
	}

	for _, tt := range tests {
		gotMin, okMin := parsePriceMinFromText(tt.input)
		gotMax, okMax := parsePriceMaxFromText(tt.input)
		if okMin != tt.okMin || okMax != tt.okMax {
			t.Fatalf("%s: expected okMin=%v okMax=%v, got %v %v", tt.name, tt.okMin, tt.okMax, okMin, okMax)
		}
		if tt.ok {
			if round2(gotMin) != round2(tt.min) {
				t.Fatalf("%s: expected min %.2f, got %.2f", tt.name, tt.min, gotMin)
			}
			if round2(gotMax) != round2(tt.max) {
				t.Fatalf("%s: expected max %.2f, got %.2f", tt.name, tt.max, gotMax)
			}
		}
	}
}

func TestNormalizeAiRecommendStockForSave(t *testing.T) {
	dataTime := time.Date(2026, 3, 7, 9, 45, 0, 0, time.Local)
	item := &models.AiRecommendStocks{
		DataTime:                 &dataTime,
		ModelName:                "test-model",
		StockCode:                "sz300308",
		StockName:                "中际旭创",
		BkName:                   "AI算力",
		RecommendReason:          "核心逻辑：光模块景气持续，龙头辨识度高",
		RecommendBuyPrice:        "165-169",
		RecommendStopProfitPrice: "178-185",
		RecommendStopLossPrice:   "159",
		RiskRemarks:              "高位波动较大，需防板块退潮",
	}
	if err := normalizeAiRecommendStockForSave(item); err != nil {
		t.Fatalf("normalizeAiRecommendStockForSave returned error: %v", err)
	}
	if item.StockCode != "300308.SZ" {
		t.Fatalf("unexpected stock code: %s", item.StockCode)
	}
	if item.Remarks == "" {
		t.Fatal("expected remarks to be auto-filled")
	}
	if item.StockPrice == "" || item.StockCurrentPrice == "" || item.StockClosePrice == "" {
		t.Fatalf("expected stock prices to be normalized, got %+v", item)
	}
}

func TestNormalizeAiRecommendStockForSaveRequiresRiskAndRanges(t *testing.T) {
	item := &models.AiRecommendStocks{
		StockCode:       "300308.SZ",
		StockName:       "中际旭创",
		BkName:          "AI算力",
		RecommendReason: "核心逻辑：光模块景气持续，龙头辨识度高",
	}
	if err := normalizeAiRecommendStockForSave(item); err == nil {
		t.Fatal("expected validation error when ranges and risk are missing")
	}
}

func TestParseStopLossPrice(t *testing.T) {
	tests := []struct {
		name     string
		input    models.AiRecommendStocks
		expected float64
		ok       bool
	}{
		{
			name: "text-range",
			input: models.AiRecommendStocks{
				RecommendStopLossPrice: "止损区间10.2至10.8",
			},
			expected: 10.8,
			ok:       true,
		},
		{
			name: "range-reversed",
			input: models.AiRecommendStocks{
				RecommendStopLossPrice: "10.8-10.2",
			},
			expected: 10.8,
			ok:       true,
		},
		{
			name: "no-value",
			input: models.AiRecommendStocks{
				RecommendStopLossPrice: "无",
			},
			expected: 0,
			ok:       false,
		},
	}

	for _, tt := range tests {
		got, ok := parseStopLossPrice(tt.input)
		if ok != tt.ok {
			t.Fatalf("%s: expected ok=%v, got %v", tt.name, tt.ok, ok)
		}
		if tt.ok && round2(got) != round2(tt.expected) {
			t.Fatalf("%s: expected %.2f, got %.2f", tt.name, tt.expected, got)
		}
	}
}

func TestNormalizeAiRecommendStockForSaveStructuredFields(t *testing.T) {
	item := &models.AiRecommendStocks{
		StockCode:                "300308.SZ",
		StockName:                "中际旭创",
		BkName:                   "AI算力",
		RecommendCategory:        "right_confirm",
		CoreCatalyst:             "光模块景气持续",
		KeyEvidence:              "[市场资讯] 板块成交额持续放大\n[财报/财务] 2025年前三季度盈利能力继续改善\n[技术/资金/形态] 放量突破前高",
		EvidenceSources:          `[{"type":"市场资讯","summary":"板块成交额持续放大"},{"type":"财报/财务","summary":"2025年前三季度盈利能力继续改善","sourceName":"东方财富财务数据","sourceType":"数据接口","trustLevel":"high","latencyLevel":"periodic"},{"type":"技术/资金/形态","summary":"放量突破前高"}]`,
		InvalidCondition:         "板块成交额明显走弱",
		ObservePrice:             "168.5",
		FocusPrice:               "165-169",
		RecommendBuyPrice:        "165-169",
		RecommendStopProfitPrice: "178-185",
		RecommendStopLossPrice:   "159",
		ExpectedCycle:            "1-2周",
		EventStrength:            88,
		CapitalConfirmation:      82,
		FundamentalFit:           76,
		TechnicalFit:             90,
		ActivationRuleJSON:       `{"signalType":"price_range_with_volume","evaluationWindow":"5m","baseline":"avg_amount_5x5m","operator":">=","thresholdValue":165,"thresholdMax":169,"volumeRatio":1.2,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5}`,
		RiskRemarks:              "高位波动较大，需防板块退潮",
	}
	if err := normalizeAiRecommendStockForSave(item); err != nil {
		t.Fatalf("normalizeAiRecommendStockForSave returned error: %v", err)
	}
	if item.RecommendReason == "" || !containsAll(item.RecommendReason, []string{"核心催化：光模块景气持续", "关键证据：[市场资讯] 板块成交额持续放大", "失效条件：板块成交额明显走弱"}) {
		t.Fatalf("unexpected recommend reason: %s", item.RecommendReason)
	}
	if item.RecommendCategory != recommendExecutionConditional {
		t.Fatalf("unexpected category: %s", item.RecommendCategory)
	}
	if item.EvidenceSources == "" {
		t.Fatal("expected evidence sources json to be generated")
	}
	if item.ActivationRuleVersion != activationRuleVersionV1 {
		t.Fatalf("expected activation rule version set, got %s", item.ActivationRuleVersion)
	}
}

func TestNormalizeAiRecommendStockForSaveExtractsExecutionTextIntoRemarks(t *testing.T) {
	item := &models.AiRecommendStocks{
		StockCode:                "002230.SZ",
		StockName:                "科大讯飞",
		BkName:                   "AI应用",
		RecommendCategory:        "right_confirm",
		CoreCatalyst:             "开发者大会催化",
		KeyEvidence:              "[市场资讯] 大会临近\n[财报/财务] 主业稳定\n[技术/资金/形态] 量能活跃",
		InvalidCondition:         "跌破关键支撑",
		ObservePrice:             "46.61",
		FocusPrice:               "右侧确认区46.80-47.20；回踩承接区46.10-46.50",
		RecommendBuyPrice:        "右侧确认区46.80-47.20；回踩承接区46.10-46.50",
		RecommendBuyPriceMin:     46.8,
		RecommendBuyPriceMax:     47.2,
		RecommendStopProfitPrice: "49.00-50.20",
		RecommendStopLossPrice:   "45.90",
		ExpectedCycle:            "1-3天",
		EventStrength:            70,
		CapitalConfirmation:      72,
		FundamentalFit:           68,
		TechnicalFit:             76,
		RiskRemarks:              "冲高回落风险",
	}
	if err := normalizeAiRecommendStockForSave(item); err != nil {
		t.Fatalf("normalizeAiRecommendStockForSave returned error: %v", err)
	}
	if item.FocusPrice != "46.8-47.2" && item.FocusPrice != "46.80-47.20" {
		t.Fatalf("expected normalized focus price, got %q", item.FocusPrice)
	}
	if item.RecommendBuyPrice != "46.8-47.2" && item.RecommendBuyPrice != "46.80-47.20" {
		t.Fatalf("expected normalized buy price, got %q", item.RecommendBuyPrice)
	}
	if !strings.Contains(item.Remarks, "右侧确认区46.80-47.20；回踩承接区46.10-46.50") {
		t.Fatalf("expected original execution text moved into remarks, got %q", item.Remarks)
	}
}

func TestNormalizeAiRecommendStockForSaveDowngradesInsufficientEvidence(t *testing.T) {
	item := &models.AiRecommendStocks{
		StockCode:                "300308.SZ",
		StockName:                "中际旭创",
		BkName:                   "AI算力",
		RecommendCategory:        "right_confirm",
		CoreCatalyst:             "光模块景气持续",
		KeyEvidence:              "[市场资讯] 板块热度提升",
		InvalidCondition:         "板块成交额明显走弱",
		ObservePrice:             "168.5",
		FocusPrice:               "165-169",
		RecommendBuyPrice:        "165-169",
		RecommendStopProfitPrice: "178-185",
		RecommendStopLossPrice:   "159",
		ExpectedCycle:            "1-2周",
		EventStrength:            88,
		CapitalConfirmation:      82,
		FundamentalFit:           76,
		TechnicalFit:             90,
		RiskRemarks:              "高位波动较大，需防板块退潮",
	}
	if err := normalizeAiRecommendStockForSave(item); err != nil {
		t.Fatalf("normalizeAiRecommendStockForSave returned error: %v", err)
	}
	if item.RecommendCategory != recommendExecutionConditional {
		t.Fatalf("expected category conditional, got %s", item.RecommendCategory)
	}
	if item.RecommendStatus != "insufficient_evidence" {
		t.Fatalf("expected insufficient_evidence status, got %s", item.RecommendStatus)
	}
	if !strings.Contains(item.Remarks, "证据不足") {
		t.Fatalf("expected remarks to mention downgrade, got %s", item.Remarks)
	}
}

func TestNormalizeAiRecommendStockForSaveDowngradesImmediateOutsideTradingSession(t *testing.T) {
	loc := cnLocation()
	recordTime := time.Date(2026, 4, 2, 2, 15, 0, 0, loc)
	item := &models.AiRecommendStocks{
		DataTime:                 &recordTime,
		StockCode:                "300308.SZ",
		StockName:                "中际旭创",
		BkName:                   "AI算力",
		RecommendCategory:        recommendExecutionImmediate,
		ExecutionState:           recommendExecutionImmediate,
		CoreCatalyst:             "光模块景气持续",
		KeyEvidence:              "[市场资讯] 板块成交额持续放大\n[财报/财务] 2025年前三季度盈利能力继续改善\n[技术/资金/形态] 放量突破前高",
		EvidenceSources:          `[{"type":"市场资讯","summary":"板块成交额持续放大"},{"type":"财报/财务","summary":"2025年前三季度盈利能力继续改善","sourceName":"东方财富财务数据","sourceType":"数据接口","trustLevel":"high","latencyLevel":"periodic"},{"type":"技术/资金/形态","summary":"放量突破前高"}]`,
		InvalidCondition:         "板块成交额明显走弱",
		ObservePrice:             "168.5",
		FocusPrice:               "165-169",
		RecommendBuyPrice:        "165-169",
		RecommendStopProfitPrice: "178-185",
		RecommendStopLossPrice:   "159",
		ExpectedCycle:            "1-2周",
		EventStrength:            88,
		CapitalConfirmation:      82,
		FundamentalFit:           76,
		TechnicalFit:             90,
		RiskRemarks:              "高位波动较大，需防板块退潮",
		BuySignal:                "价格触发：未来3个交易日内股价进入165-169主买入区；量能触发：5分钟成交额不低于近5个5分钟均额的1.2倍；逻辑触发：光模块景气主线未证伪且板块资金净流入延续",
		SellSignal:               "触及178-185止盈区间卖出",
		InvalidSignal:            "时间失效：未来5个交易日内仍未触发主买入区；价格失效：任一5分钟收盘价跌破159；逻辑失效：景气验证被证伪或板块联动明显转弱",
	}
	if err := normalizeAiRecommendStockForSave(item); err != nil {
		t.Fatalf("normalizeAiRecommendStockForSave returned error: %v", err)
	}
	if item.ExecutionState != recommendExecutionConditional {
		t.Fatalf("expected execution state conditional, got %s", item.ExecutionState)
	}
	if item.RecommendCategory != recommendExecutionConditional {
		t.Fatalf("expected recommend category conditional, got %s", item.RecommendCategory)
	}
	if !strings.Contains(item.Remarks, "等待激活") {
		t.Fatalf("expected remarks to mention timing downgrade, got %s", item.Remarks)
	}
}

func TestValidateSignalDrivenRecommendRequiresStructuredWaitingActivationPlan(t *testing.T) {
	item := &models.AiRecommendStocks{
		ExecutionState: recommendExecutionConditional,
		BuySignal:      "价格触发：未来3个交易日内股价进入10.00-10.50主买入区；量能触发：5分钟成交额不低于近5个5分钟均额的1.2倍；逻辑触发：核心催化未证伪且板块未转弱",
		SellSignal:     "触及11.50-12.00止盈区间卖出",
		InvalidSignal:  "时间失效：未来5个交易日内仍未触发主买入区；价格失效：任一5分钟收盘价跌破9.80；逻辑失效：核心催化被证伪或板块联动明显转弱",
	}
	if err := validateSignalDrivenRecommend(item); err != nil {
		t.Fatalf("expected structured waiting-activation plan to pass, got err=%v", err)
	}
}

func TestValidateSignalDrivenRecommendAllowsInvalidSignalToReferenceActivationRule(t *testing.T) {
	item := &models.AiRecommendStocks{
		ExecutionState:       recommendExecutionConditional,
		BuySignal:            "价格触发：未来5个交易日内，回踩路径为5分钟收盘价进入133.30-136.20；突破路径为5分钟收盘价>=139.20；量能触发：在133.30-136.20区间或139.20突破价附近，5分钟成交额>=近5个5分钟均额的1.20倍，确认1根5分钟K线",
		SellSignal:           "触及145.80-151.50止盈区间卖出",
		InvalidSignal:        "时间失效：未来5个交易日内未同时触发价格与量能条件则失效；价格失效：触发前或触发后5分钟收盘价<131.80则失效",
		ActivationRuleJSON:   `{"version":"v2","mode":"any_of","paths":[{"name":"pullback","signalType":"price_range_with_volume","evaluationWindow":"5m","baseline":"avg_amount_5x5m","operator":">=","thresholdValue":133.3,"thresholdMax":136.2,"volumeRatio":1.2,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5},{"name":"breakout","signalType":"price_breakout_with_volume","evaluationWindow":"5m","baseline":"avg_amount_5x5m","operator":">=","thresholdValue":139.2,"volumeRatio":1.2,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5}]}`,
		ActivationRuleSource: "market_summary",
	}
	if err := validateSignalDrivenRecommend(item); err != nil {
		t.Fatalf("expected invalid signal to reuse activation rule volume context, got err=%v", err)
	}
}

func TestValidateSignalDrivenRecommendRejectsAmbiguousTriggerPhrases(t *testing.T) {
	item := &models.AiRecommendStocks{
		ExecutionState: recommendExecutionConditional,
		BuySignal:      "价格触发：股价进入9.42-9.56主买入区；量能触发：在9.42以上观察5分钟量能，相对近5个5分钟均量至少1.2倍，不能是缩量拉升；逻辑触发：电网建设主线未失真",
		SellSignal:     "触及10.00-10.30止盈区间卖出",
		InvalidSignal:  "时间失效：未来5个交易日内未触发买点；价格失效：任一5分钟收盘价跌破8.98；逻辑失效：冲击9.89/9.90失败并在5分钟级别放量回落至9.20下方",
	}
	err := validateSignalDrivenRecommend(item)
	if err == nil {
		t.Fatalf("expected ambiguous trigger phrases to be rejected")
	}
	if !strings.Contains(err.Error(), "未量化表述") && !strings.Contains(err.Error(), "触发阈值") {
		t.Fatalf("expected ambiguous-phrase error, got %v", err)
	}
}

func TestNormalizeAiRecommendStockForSaveIgnoresAmbiguousRemarksWhenPlanIsStructured(t *testing.T) {
	item := &models.AiRecommendStocks{
		StockCode:                "300308.SZ",
		StockName:                "中际旭创",
		BkName:                   "AI算力/光模块",
		RecommendReason:          "核心催化：AI算力大会临近；关键证据：[市场资讯] AI算力产业大会将于4月9日至11日召开；[财报/财务] 高速光模块景气延续；价格锚点：170；买入区间：168-172；止盈区间：180-188；止损位：163；买入依据：价格触发：未来3个交易日内股价回到168-172元区间后，连续2根15分钟K线收于170元上方；量能触发：对应15分钟成交额≥近5个15分钟均额的1.3倍，且量比≥1.5；逻辑触发：光模块景气逻辑未被证伪；失效条件：时间失效：未来3个交易日内未触发；价格失效：有效跌破163元；逻辑失效：行业景气显著走弱",
		RecommendBuyPrice:        "168-172",
		RecommendStopProfitPrice: "180-188",
		RecommendStopLossPrice:   "163",
		ExecutionState:           recommendExecutionConditional,
		BuySignal:                "价格触发：未来3个交易日内股价回到168-172元区间后，连续2根15分钟K线收于170元上方；量能触发：对应15分钟成交额≥近5个15分钟均额的1.3倍，且量比≥1.5；逻辑触发：光模块景气逻辑未被证伪",
		SellSignal:               "触及180-188止盈区间卖出",
		InvalidSignal:            "时间失效：未来3个交易日内未触发；价格失效：有效跌破163元；逻辑失效：行业景气显著走弱",
		ExpectedCycle:            "3-5个交易日",
		EventStrength:            85,
		CapitalConfirmation:      70,
		FundamentalFit:           82,
		TechnicalFit:             74,
		RiskRemarks:              "海外算力资本开支波动可能带来高位回撤风险",
		Remarks:                  "等待激活；若先直接上冲至止盈区间附近而未满足买点，不追价执行",
	}
	if err := normalizeAiRecommendStockForSave(item); err != nil {
		t.Fatalf("normalizeAiRecommendStockForSave returned error: %v", err)
	}
	if strings.Contains(item.BuySignalDetail, "不追") {
		t.Fatalf("expected ambiguous remark to be filtered from buy signal detail, got %q", item.BuySignalDetail)
	}
}

func TestShouldTrackRecommendInYield(t *testing.T) {
	tests := []struct {
		name  string
		input models.AiRecommendStocks
		want  bool
	}{
		{
			name:  "legacy-empty-category-without-plan",
			input: models.AiRecommendStocks{RecommendCategory: "", RecommendStatus: ""},
			want:  false,
		},
		{
			name:  "conditional-valid",
			input: models.AiRecommendStocks{RecommendCategory: recommendExecutionConditional, RecommendStatus: "valid"},
			want:  false,
		},
		{
			name: "legacy-immediate-valid",
			input: models.AiRecommendStocks{
				RecommendCategory:        recommendExecutionImmediate,
				RecommendStatus:          "valid",
				RecommendBuyPrice:        "10-10.5",
				RecommendStopProfitPrice: "11-12",
				RecommendStopLossPrice:   "9.6",
			},
			want: true,
		},
		{
			name:  "activated-valid-without-signals",
			input: models.AiRecommendStocks{RecommendCategory: recommendExecutionConditional, RecommendStatus: "valid"},
			want:  false,
		},
		{
			name: "legacy-empty-category-with-full-plan",
			input: models.AiRecommendStocks{
				RecommendCategory:        "",
				RecommendStatus:          "",
				RecommendBuyPrice:        "10-10.5",
				RecommendStopProfitPrice: "11-12",
				RecommendStopLossPrice:   "9.6",
			},
			want: true,
		},
		{
			name: "structured-activated-buy",
			input: models.AiRecommendStocks{
				RecommendCategory:        recommendExecutionConditional,
				RecommendStatus:          "valid",
				ExecutionState:           recommendExecutionConditional,
				BuySignal:                "价格触发：股价进入10.00-10.50主买入区；量能触发：5分钟成交额不低于近5个5分钟均额的1.1倍；逻辑触发：核心催化未证伪且板块未转弱",
				SellSignal:               "触及 11.50 止盈",
				InvalidSignal:            "时间失效：未来5个交易日内仍未触发主买入区；价格失效：任一5分钟收盘价跌破9.80；逻辑失效：核心催化被证伪或板块联动明显转弱",
				ActivationRuleJSON:       `{"signalType":"price_range_with_volume","evaluationWindow":"5m","baseline":"avg_amount_5x5m","operator":">=","thresholdValue":10,"thresholdMax":10.5,"volumeRatio":1.1,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5}`,
				RecommendBuyPrice:        "10-10.5",
				RecommendStopProfitPrice: "11-12",
				RecommendStopLossPrice:   "9.6",
			},
			want: true,
		},
		{
			name:  "conditional-insufficient-evidence",
			input: models.AiRecommendStocks{RecommendCategory: recommendExecutionConditional, RecommendStatus: "insufficient_evidence"},
			want:  false,
		},
		{
			name:  "avoid-category",
			input: models.AiRecommendStocks{RecommendCategory: "avoid", RecommendStatus: "valid"},
			want:  false,
		},
		{
			name:  "unknown-category",
			input: models.AiRecommendStocks{RecommendCategory: "speculative", RecommendStatus: "valid"},
			want:  false,
		},
	}

	for _, tt := range tests {
		got := shouldTrackRecommendInYield(&tt.input)
		if got != tt.want {
			t.Fatalf("%s: expected %v, got %v", tt.name, tt.want, got)
		}
	}
}

func TestResolveRecommendYieldSkipInfo(t *testing.T) {
	tests := []struct {
		name           string
		input          models.AiRecommendStocks
		wantDisplay    bool
		wantTrack      bool
		wantSkip       bool
		wantStatus     string
		wantPosition   string
		wantDataStatus string
		wantReasonHas  string
	}{
		{
			name: "avoid-status",
			input: models.AiRecommendStocks{
				RecommendCategory: recommendExecutionConditional,
				RecommendStatus:   "avoid",
				InvalidCondition:  "跌破支撑",
			},
			wantDisplay:    true,
			wantTrack:      false,
			wantSkip:       true,
			wantStatus:     "skipped",
			wantPosition:   "已放弃",
			wantDataStatus: "已跳过",
			wantReasonHas:  "回避",
		},
		{
			name: "controversial-status",
			input: models.AiRecommendStocks{
				RecommendCategory: recommendExecutionConditional,
				RecommendStatus:   "controversial",
			},
			wantDisplay: true,
			wantTrack:   false,
			wantSkip:    false,
		},
		{
			name: "avoid-category",
			input: models.AiRecommendStocks{
				RecommendCategory: "回避标的（高位分歧）",
				RecommendStatus:   "valid",
			},
			wantDisplay:    true,
			wantTrack:      false,
			wantSkip:       true,
			wantStatus:     "skipped",
			wantPosition:   "已放弃",
			wantDataStatus: "已跳过",
			wantReasonHas:  "回避标的",
		},
		{
			name: "tracked-valid",
			input: models.AiRecommendStocks{
				RecommendCategory: recommendExecutionConditional,
				RecommendStatus:   "valid",
			},
			wantDisplay: true,
			wantTrack:   false,
			wantSkip:    false,
		},
		{
			name: "reason-only-observation-kept",
			input: models.AiRecommendStocks{
				RecommendCategory:        "observe",
				RecommendStatus:          "valid",
				RecommendReason:          "买入依据：仅观察 15.20 是否有效突破并站稳，不建议当前直接买入",
				RecommendBuyPrice:        "15.20-15.40",
				RecommendStopProfitPrice: "15.60-16.20",
				RecommendStopLossPrice:   "14.20",
			},
			wantDisplay: true,
			wantTrack:   true,
			wantSkip:    false,
		},
		{
			name: "observation-style-in-buy-price-skip",
			input: models.AiRecommendStocks{
				RecommendCategory:        "observe",
				RecommendStatus:          "valid",
				RecommendBuyPrice:        "仅观察49.80-50.80是否稳住；若直接冲至52上方不建议追",
				RecommendStopProfitPrice: "54.00-56.00",
				RecommendStopLossPrice:   "49.20",
				RecommendReason:          "核心催化：3月27日天工AI大模型生态专场发布会",
				InvalidCondition:         "跌破49.20；或发布会内容低于预期、冲高回落且无持续资金承接",
			},
			wantDisplay:    true,
			wantTrack:      false,
			wantSkip:       true,
			wantStatus:     "skipped",
			wantPosition:   "已放弃",
			wantDataStatus: "已跳过",
			wantReasonHas:  "买入依据仍含观察/保守口径",
		},
		{
			name: "observation-style-in-buy-signal-detail-skip",
			input: models.AiRecommendStocks{
				RecommendCategory:        recommendExecutionConditional,
				RecommendStatus:          "insufficient_evidence",
				ExecutionState:           recommendExecutionConditional,
				RecommendBuyPrice:        "64.0-67.5",
				RecommendStopProfitPrice: "72-75",
				RecommendStopLossPrice:   "61.8",
				BuySignal:                "价格触发：未来3-5个交易日内股价进入64.0-67.5主买入区；量能触发：5分钟成交额不低于近5个5分钟均额的1.0倍；逻辑触发：核心催化未证伪且板块未转弱",
				BuySignalDetail:          "仅作观察标的，等待正式公告后再决定是否升级。",
				SellSignal:               "触及72-75止盈区间卖出；若跌破61.8止损位立即止损",
				InvalidSignal:            "时间失效：未来5个交易日内仍未触发主买入区；价格失效：任一5分钟收盘价跌破61.8；逻辑失效：核心催化被证伪或板块联动明显转弱",
			},
			wantDisplay:    true,
			wantTrack:      false,
			wantSkip:       true,
			wantStatus:     "skipped",
			wantPosition:   "已放弃",
			wantDataStatus: "已跳过",
			wantReasonHas:  "证据不足",
		},
		{
			name: "before-cutoff-insufficient-evidence-without-observation-kept",
			input: models.AiRecommendStocks{
				RecommendCategory:        "observe",
				RecommendStatus:          "insufficient_evidence",
				RecommendBuyPrice:        "25.50-26.00放量站稳再看主买入区",
				RecommendStopProfitPrice: "27.50-28.20",
				RecommendStopLossPrice:   "24.30",
				BuySignal:                "价格触发：未来3-5个交易日内股价进入25.5-26.0放量站稳再看主买入区；量能触发：5分钟成交额不低于近5个5分钟均额的1.0倍",
				InvalidCondition:         "大会前后板块不联动；股价跌破24.30元前收附近且放量走弱；龙虎榜次日承接明显不足",
				DataTime: func() *time.Time {
					t := time.Date(2026, 3, 8, 4, 21, 29, 0, cnLocation())
					return &t
				}(),
			},
			wantDisplay: true,
			wantTrack:   true,
			wantSkip:    false,
		},
		{
			name: "before-cutoff-observation-word-still-skipped",
			input: models.AiRecommendStocks{
				RecommendCategory:        "observe",
				RecommendStatus:          "insufficient_evidence",
				RecommendBuyPrice:        "59.50-60.00",
				RecommendStopProfitPrice: "63.00-65.00",
				RecommendStopLossPrice:   "57.20",
				BuySignal:                "价格触发：未来3-5个交易日内股价进入仅观察59.50-60.00能否重新站稳；未站稳前不建议主动追买主买入区；量能触发：5分钟成交额不低于近5个5分钟均额的1.0倍",
				InvalidCondition:         "无法重新站回59.50-60.00区间且继续放量走弱；或跌破57.20后无承接；或板块整体转弱",
				DataTime: func() *time.Time {
					t := time.Date(2026, 3, 24, 11, 30, 0, 0, cnLocation())
					return &t
				}(),
			},
			wantDisplay:    true,
			wantTrack:      false,
			wantSkip:       true,
			wantStatus:     "skipped",
			wantPosition:   "已放弃",
			wantDataStatus: "已跳过",
			wantReasonHas:  "买入依据含“观察”",
		},
		{
			name: "before-cutoff-avoid-still-skipped",
			input: models.AiRecommendStocks{
				RecommendCategory: "observe",
				RecommendStatus:   "avoid",
				InvalidCondition:  "跌破支撑位",
				DataTime: func() *time.Time {
					t := time.Date(2026, 3, 20, 11, 30, 0, 0, cnLocation())
					return &t
				}(),
			},
			wantDisplay:    true,
			wantTrack:      false,
			wantSkip:       true,
			wantStatus:     "skipped",
			wantPosition:   "已放弃",
			wantDataStatus: "已跳过",
			wantReasonHas:  "回避",
		},
		{
			name: "cutoff-day-still-uses-current-insufficient-evidence-skip",
			input: models.AiRecommendStocks{
				RecommendCategory:        "observe",
				RecommendStatus:          "insufficient_evidence",
				RecommendBuyPrice:        "25.50-26.00放量站稳再看主买入区",
				RecommendStopProfitPrice: "27.50-28.20",
				RecommendStopLossPrice:   "24.30",
				BuySignal:                "价格触发：未来3-5个交易日内股价进入25.5-26.0放量站稳再看主买入区；量能触发：5分钟成交额不低于近5个5分钟均额的1.0倍",
				InvalidCondition:         "大会前后板块不联动；股价跌破24.30元前收附近且放量走弱；龙虎榜次日承接明显不足",
				DataTime: func() *time.Time {
					t := time.Date(2026, 4, 6, 0, 0, 0, 0, cnLocation())
					return &t
				}(),
			},
			wantDisplay:    true,
			wantTrack:      false,
			wantSkip:       true,
			wantStatus:     "skipped",
			wantPosition:   "已放弃",
			wantDataStatus: "已跳过",
			wantReasonHas:  "证据不足",
		},
		{
			name: "observation-word-but-explicit-plan-kept",
			input: models.AiRecommendStocks{
				RecommendCategory:        recommendExecutionConditional,
				RecommendStatus:          "valid",
				ExecutionState:           recommendExecutionConditional,
				RecommendBuyPrice:        "9.42-9.56",
				RecommendStopProfitPrice: "10.00-10.30",
				RecommendStopLossPrice:   "8.98",
				BuySignal:                "价格位置：以9.36为锚点；量能确认：在9.42以上观察5分钟量能，相对近5个5分钟均量至少1.2倍，且不能低于上一交易日同价位活跃度；催化仍成立：龙虎榜净流入且主线未证伪",
				BuySignalDetail:          "若未来5个交易日内重新进入9.42-9.56主买入区并满足上面的量能要求，则触发买入",
				SellSignal:               "触及10.00-10.30止盈区间卖出",
				InvalidSignal:            "时间失效：未来5个交易日内未触发买点；价格失效：任一5分钟收盘价跌破8.98；逻辑失效：主线催化被证伪",
				ActivationRuleJSON:       `{"signalType":"price_range_with_volume","evaluationWindow":"5m","baseline":"prev_day_same_slot_amount","operator":">=","thresholdValue":9.42,"thresholdMax":9.56,"volumeRatio":1.2,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5}`,
			},
			wantDisplay: true,
			wantTrack:   true,
			wantSkip:    false,
		},
	}

	for _, tt := range tests {
		activationStatus, positionStatus, dataStatus, reason, skip := resolveRecommendYieldSkipInfo(&tt.input)
		if got := shouldDisplayRecommendInYield(&tt.input); got != tt.wantDisplay {
			t.Fatalf("%s: expected display=%v, got %v", tt.name, tt.wantDisplay, got)
		}
		if got := shouldTrackRecommendInYield(&tt.input); got != tt.wantTrack {
			t.Fatalf("%s: expected track=%v, got %v", tt.name, tt.wantTrack, got)
		}
		if skip != tt.wantSkip {
			t.Fatalf("%s: expected skip=%v, got %v", tt.name, tt.wantSkip, skip)
		}
		if activationStatus != tt.wantStatus {
			t.Fatalf("%s: expected activation status %q, got %q", tt.name, tt.wantStatus, activationStatus)
		}
		if positionStatus != tt.wantPosition {
			t.Fatalf("%s: expected position %q, got %q", tt.name, tt.wantPosition, positionStatus)
		}
		if dataStatus != tt.wantDataStatus {
			t.Fatalf("%s: expected data status %q, got %q", tt.name, tt.wantDataStatus, dataStatus)
		}
		if tt.wantReasonHas != "" && !strings.Contains(reason, tt.wantReasonHas) {
			t.Fatalf("%s: expected reason to contain %q, got %q", tt.name, tt.wantReasonHas, reason)
		}
	}
}

func TestResolveRecommendBacktestEligibility(t *testing.T) {
	tests := []struct {
		name       string
		input      models.AiRecommendStocks
		want       string
		wantReason string
	}{
		{
			name: "legacy-conditional-category-eligible",
			input: models.AiRecommendStocks{
				RecommendCategory:        recommendExecutionConditional,
				RecommendStatus:          "valid",
				RecommendBuyPrice:        "10-10.5",
				RecommendStopProfitPrice: "11-12",
				RecommendStopLossPrice:   "9.6",
			},
			want: recommendBacktestEligible,
		},
		{
			name: "phase3-activated-buy-ineligible-without-signals",
			input: models.AiRecommendStocks{
				RecommendCategory:        recommendExecutionConditional,
				RecommendStatus:          "valid",
				SummaryVersion:           marketSummaryPhase3Version,
				RecommendBuyPrice:        "10-10.5",
				RecommendStopProfitPrice: "11-12",
				RecommendStopLossPrice:   "9.6",
			},
			want:       recommendBacktestIneligible,
			wantReason: "缺少结构化激活规则",
		},
		{
			name: "phase3-before-cutoff-falls-back-to-legacy-direct-activation",
			input: models.AiRecommendStocks{
				RecommendCategory:        recommendExecutionConditional,
				RecommendStatus:          "valid",
				SummaryVersion:           marketSummaryPhase3Version,
				RecommendBuyPrice:        "10-10.5",
				RecommendStopProfitPrice: "11-12",
				RecommendStopLossPrice:   "9.6",
				DataTime: func() *time.Time {
					t := time.Date(2026, 4, 5, 10, 0, 0, 0, cnLocation())
					return &t
				}(),
			},
			want: recommendBacktestEligible,
		},
		{
			name: "phase3-v2-activated-buy-still-legacy-compatible",
			input: models.AiRecommendStocks{
				RecommendCategory:        recommendExecutionConditional,
				RecommendStatus:          "valid",
				SummaryVersion:           "phase3-v2",
				RecommendBuyPrice:        "10-10.5",
				RecommendStopProfitPrice: "11-12",
				RecommendStopLossPrice:   "9.6",
			},
			want: recommendBacktestEligible,
		},
		{
			name: "phase3-immediate-buy-eligible-with-full-plan",
			input: models.AiRecommendStocks{
				RecommendCategory:        recommendExecutionImmediate,
				RecommendStatus:          "valid",
				SummaryVersion:           marketSummaryPhase3Version,
				RecommendBuyPrice:        "10-10.5",
				RecommendStopProfitPrice: "11-12",
				RecommendStopLossPrice:   "9.6",
			},
			want: recommendBacktestEligible,
		},
		{
			name: "structured-activated-buy-eligible",
			input: models.AiRecommendStocks{
				RecommendCategory:  recommendExecutionConditional,
				RecommendStatus:    "valid",
				ExecutionState:     recommendExecutionConditional,
				BuySignal:          "价格触发：股价进入10.00-10.50主买入区；量能触发：5分钟成交额不低于近5个5分钟均额的1.1倍；逻辑触发：核心催化未证伪且板块未转弱",
				SellSignal:         "触及 11.50 止盈",
				InvalidSignal:      "时间失效：未来5个交易日内仍未触发主买入区；价格失效：任一5分钟收盘价跌破9.80；逻辑失效：核心催化被证伪或板块联动明显转弱",
				ActivationRuleJSON: `{"signalType":"price_range_with_volume","evaluationWindow":"5m","baseline":"avg_amount_5x5m","operator":">=","thresholdValue":10,"thresholdMax":10.5,"volumeRatio":1.1,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5}`,
			},
			want: recommendBacktestEligible,
		},
		{
			name: "reason-only-observation-plan-eligible",
			input: models.AiRecommendStocks{
				RecommendCategory:        "observe",
				RecommendStatus:          "valid",
				RecommendReason:          "买入依据：仅观察59.50-60.00能否重新站稳；未站稳前不建议主动追买",
				RecommendBuyPrice:        "59.50-60.00",
				RecommendStopProfitPrice: "63.00-65.00",
				RecommendStopLossPrice:   "58.20",
			},
			want: recommendBacktestEligible,
		},
		{
			name: "legacy-plan-eligible",
			input: models.AiRecommendStocks{
				RecommendBuyPrice:        "10-10.5",
				RecommendStopProfitPrice: "11-12",
				RecommendStopLossPrice:   "9.6",
			},
			want: recommendBacktestEligible,
		},
		{
			name: "skip-status",
			input: models.AiRecommendStocks{
				RecommendCategory: recommendExecutionConditional,
				RecommendStatus:   "controversial",
			},
			want:       recommendBacktestIneligible,
			wantReason: "缺少可解析买入区间",
		},
		{
			name: "skip-only-when-buy-basis-conservative",
			input: models.AiRecommendStocks{
				RecommendCategory:        recommendExecutionConditional,
				RecommendStatus:          "insufficient_evidence",
				ExecutionState:           recommendExecutionConditional,
				RecommendBuyPrice:        "59.5-60.0",
				RecommendStopProfitPrice: "63-65",
				RecommendStopLossPrice:   "58.2",
				BuySignal:                "价格触发：未来3-5个交易日内股价进入59.5-60.0主买入区；量能触发：5分钟成交额不低于近5个5分钟均额的1.0倍；逻辑触发：核心催化未证伪且板块未转弱",
				BuySignalDetail:          "以观察为主，未站稳前不建议主动追买。",
				SellSignal:               "触及63-65止盈区间卖出；若跌破58.2止损位立即止损",
				InvalidSignal:            "时间失效：未来5个交易日内仍未触发主买入区；价格失效：任一5分钟收盘价跌破58.2；逻辑失效：核心催化被证伪或板块联动明显转弱",
			},
			want:       recommendBacktestSkipped,
			wantReason: "证据不足",
		},
	}

	for _, tt := range tests {
		got, reason := resolveRecommendBacktestEligibility(&tt.input)
		if got != tt.want {
			t.Fatalf("%s: expected eligibility=%s, got %s", tt.name, tt.want, got)
		}
		if tt.wantReason != "" && !strings.Contains(reason, tt.wantReason) {
			t.Fatalf("%s: expected reason to contain %q, got %q", tt.name, tt.wantReason, reason)
		}
	}
}

func TestResolveRecommendSkipReasonKeepsSpecificAnalysisOnlyCause(t *testing.T) {
	recordTime := time.Date(2026, 6, 8, 9, 40, 0, 0, cnLocation())
	rec := models.AiRecommendStocks{
		DataTime:                &recordTime,
		StockCode:               "300433.SZ",
		StockName:               "蓝思科技",
		RecommendStatus:         "missing_market_data",
		ExecutionState:          recommendExecutionAnalysisOnly,
		ActivationStatus:        "skipped",
		ActivationInvalidReason: "V1.3.6源头质量门槛未通过：最差成交价盈亏比 0.71 低于 0.80",
		InvalidCondition:        marketSummaryAnalysisOnlySkipReason,
	}

	activationStatus, positionStatus, dataStatus, reason, skip := resolveRecommendYieldSkipInfo(&rec)
	if !skip {
		t.Fatal("expected analysis_only recommend to be skipped")
	}
	if activationStatus != "skipped" || positionStatus != "仅分析" || dataStatus != "已跳过" {
		t.Fatalf("unexpected skip state: activation=%s position=%s data=%s", activationStatus, positionStatus, dataStatus)
	}
	if !strings.Contains(reason, "仅分析（analysis_only）：V1.3.6源头质量门槛未通过") || !strings.Contains(reason, "盈亏比 0.71 低于 0.80") {
		t.Fatalf("expected specific analysis_only reason, got %q", reason)
	}

	eligibility, eligibilityReason := resolveRecommendBacktestEligibility(&rec)
	if eligibility != recommendBacktestSkipped {
		t.Fatalf("eligibility = %s, want skipped", eligibility)
	}
	if eligibilityReason != reason {
		t.Fatalf("backtest reason = %q, want yield reason %q", eligibilityReason, reason)
	}
}

func TestResolveRecommendSkipReasonDescribesPendingMinuteWindow(t *testing.T) {
	recordTime := time.Date(2026, 6, 8, 11, 30, 0, 0, cnLocation())
	rec := models.AiRecommendStocks{
		DataTime:           &recordTime,
		StockCode:          "600629.SH",
		StockName:          "华建集团",
		RecommendStatus:    recommendStatusPendingMarketData,
		ExecutionState:     recommendExecutionConditional,
		ActivationStatus:   recommendActivationPendingData,
		ActivationRuleJSON: `{"trigger":"x"}`,
	}

	_, _, _, reason, skip := resolveRecommendYieldSkipInfo(&rec)
	if !skip {
		t.Fatal("expected pending market data recommend to be skipped")
	}
	for _, want := range []string{"待补分钟线：600629.SH", "2026-06-08 11:00:00 至 2026-06-08 13:30:00", "缺少连续分钟线"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("expected pending reason to contain %q, got %q", want, reason)
		}
	}
}

func TestParseEvidenceSourcesFromTextSupportsMultipleTaggedSegments(t *testing.T) {
	refs := parseEvidenceSourcesFromText("[市场资讯] 板块强度高；[财报/财务] 盈利改善；[技术/资金/形态] 放量突破前高")
	if len(refs) != 3 {
		t.Fatalf("expected 3 refs, got %d: %+v", len(refs), refs)
	}
	if refs[1].Type != "财报/财务" {
		t.Fatalf("unexpected second ref type: %+v", refs[1])
	}
}

func TestNormalizeEvidenceSourcesTextUpgradesToV2(t *testing.T) {
	raw := `[{"type":"财报/财务","summary":"2025年前三季度盈利改善"},{"type":"资金结构","summary":"北向资金连续净流入"}]`
	normalized := normalizeEvidenceSourcesText(raw)
	if normalized == "" {
		t.Fatal("expected normalized evidence sources")
	}
	var refs []aiEvidenceReference
	if err := json.Unmarshal([]byte(normalized), &refs); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d", len(refs))
	}
	if refs[0].TrustLevel == "" || refs[0].SourceType == "" || refs[0].LatencyLevel == "" {
		t.Fatalf("expected governance fields to be filled: %+v", refs[0])
	}
}

func TestNormalizeAiRecommendStockForSaveMarksControversialOnConflict(t *testing.T) {
	item := &models.AiRecommendStocks{
		StockCode:                "300308.SZ",
		StockName:                "中际旭创",
		BkName:                   "AI算力",
		RecommendCategory:        "right_confirm",
		CoreCatalyst:             "光模块景气持续",
		KeyEvidence:              "[一级披露] 公司公告称订单增长",
		EvidenceSources:          `[{"type":"一级披露","title":"订单公告","summary":"公司公告称订单增长","sourceType":"原始披露","trustLevel":"high","latencyLevel":"realtime","dedupeKey":"same-event-high"},{"type":"个股新闻","title":"订单公告","summary":"媒体称订单不及预期","sourceType":"聚合媒体","trustLevel":"medium","latencyLevel":"realtime","dedupeKey":"same-event-media"}]`,
		InvalidCondition:         "板块成交额明显走弱",
		ObservePrice:             "168.5",
		FocusPrice:               "165-169",
		RecommendBuyPrice:        "165-169",
		RecommendStopProfitPrice: "178-185",
		RecommendStopLossPrice:   "159",
		ExpectedCycle:            "1-2周",
		EventStrength:            88,
		CapitalConfirmation:      82,
		FundamentalFit:           76,
		TechnicalFit:             90,
		RiskRemarks:              "高位波动较大，需防板块退潮",
	}
	if err := normalizeAiRecommendStockForSave(item); err != nil {
		t.Fatalf("normalizeAiRecommendStockForSave returned error: %v", err)
	}
	if item.RecommendCategory != recommendExecutionConditional {
		t.Fatalf("expected category conditional, got %s", item.RecommendCategory)
	}
	if item.RecommendStatus != "controversial" {
		t.Fatalf("expected controversial status, got %s", item.RecommendStatus)
	}
}

func TestCreateAiRecommendStocksRejectsSameDayDuplicateStock(t *testing.T) {
	prevMarkDirty := markAiRecommendYieldDirtyCodesForMutationFn
	prevRequestRecalc := requestAiRecommendYieldScopedRecalcForMutationFn
	markAiRecommendYieldDirtyCodesForMutationFn = func([]string, string, string) error { return nil }
	requestAiRecommendYieldScopedRecalcForMutationFn = func(bool, string, []string) {}
	t.Cleanup(func() {
		markAiRecommendYieldDirtyCodesForMutationFn = prevMarkDirty
		requestAiRecommendYieldScopedRecalcForMutationFn = prevRequestRecalc
	})

	initDatabaseForTest(t, filepath.Join(t.TempDir(), "ai-recommend-daily-duplicate.db"))
	if err := db.Dao.AutoMigrate(&models.AiRecommendStocks{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	service := NewAiRecommendStocksService()
	loc := cnLocation()
	firstTime := time.Date(2026, 4, 8, 9, 35, 0, 0, loc)
	secondTime := time.Date(2026, 4, 8, 14, 12, 0, 0, loc)
	nextDayTime := time.Date(2026, 4, 9, 9, 40, 0, 0, loc)

	first := buildValidAiRecommendForCreate(firstTime, "300308.SZ", "中际旭创")
	if err := service.CreateAiRecommendStocks(first); err != nil {
		t.Fatalf("expected first create success, got %v", err)
	}

	duplicate := buildValidAiRecommendForCreate(secondTime, "300308.SZ", "中际旭创")
	err := service.CreateAiRecommendStocks(duplicate)
	if err == nil {
		t.Fatalf("expected same-day duplicate to be rejected")
	}
	if !strings.Contains(err.Error(), "同一天不能同时买入同一只股票") {
		t.Fatalf("unexpected duplicate error: %v", err)
	}

	nextDay := buildValidAiRecommendForCreate(nextDayTime, "300308.SZ", "中际旭创")
	if err := service.CreateAiRecommendStocks(nextDay); err != nil {
		t.Fatalf("expected next-day create success, got %v", err)
	}

	var count int64
	if err := db.Dao.Model(&models.AiRecommendStocks{}).Count(&count).Error; err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 saved records, got %d", count)
	}
}

func TestBatchCreateAiRecommendStocksRejectsSameDayDuplicateStockInBatch(t *testing.T) {
	prevMarkDirty := markAiRecommendYieldDirtyCodesForMutationFn
	prevRequestRecalc := requestAiRecommendYieldScopedRecalcForMutationFn
	markAiRecommendYieldDirtyCodesForMutationFn = func([]string, string, string) error { return nil }
	requestAiRecommendYieldScopedRecalcForMutationFn = func(bool, string, []string) {}
	t.Cleanup(func() {
		markAiRecommendYieldDirtyCodesForMutationFn = prevMarkDirty
		requestAiRecommendYieldScopedRecalcForMutationFn = prevRequestRecalc
	})

	initDatabaseForTest(t, filepath.Join(t.TempDir(), "ai-recommend-batch-duplicate.db"))
	if err := db.Dao.AutoMigrate(&models.AiRecommendStocks{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	service := NewAiRecommendStocksService()
	loc := cnLocation()
	day := time.Date(2026, 4, 8, 10, 5, 0, 0, loc)
	later := time.Date(2026, 4, 8, 14, 55, 0, 0, loc)

	err := service.BatchCreateAiRecommendStocks([]*models.AiRecommendStocks{
		buildValidAiRecommendForCreate(day, "002371.SZ", "北方华创"),
		buildValidAiRecommendForCreate(later, "002371.SZ", "北方华创"),
	})
	if err == nil {
		t.Fatalf("expected same-day duplicate in batch to be rejected")
	}
	if !strings.Contains(err.Error(), "同一天不能同时买入同一只股票") {
		t.Fatalf("unexpected batch duplicate error: %v", err)
	}

	var count int64
	if err := db.Dao.Model(&models.AiRecommendStocks{}).Count(&count).Error; err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected batch rejection to keep db empty, got %d", count)
	}
}

func TestCollapseRecommendRecordsSameDayByCode_KeepsLatestPerDay(t *testing.T) {
	loc := cnLocation()
	records := []models.AiRecommendStocks{
		{
			ModelName: "GPT-5",
			StockCode: "001309.SZ",
			StockName: "德明利",
			DataTime:  timePtr(time.Date(2026, 3, 6, 7, 58, 54, 0, loc)),
		},
		{
			ModelName: "GPT-5",
			StockCode: "001309.SZ",
			StockName: "德明利",
			DataTime:  timePtr(time.Date(2026, 3, 6, 7, 45, 53, 0, loc)),
		},
		{
			ModelName: "AI助手",
			StockCode: "001309.SZ",
			StockName: "德明利",
			DataTime:  timePtr(time.Date(2026, 3, 6, 7, 38, 27, 0, loc)),
		},
		{
			ModelName: "GPT-5",
			StockCode: "001309.SZ",
			StockName: "德明利",
			DataTime:  timePtr(time.Date(2026, 3, 7, 8, 10, 0, 0, loc)),
		},
	}
	records[0].ID = 101
	records[1].ID = 98
	records[2].ID = 95
	records[3].ID = 110

	collapsed := collapseRecommendRecordsSameDayByCode(records)
	if len(collapsed) != 2 {
		t.Fatalf("expected 2 collapsed records, got %d", len(collapsed))
	}
	if collapsed[0].ID != 101 {
		t.Fatalf("expected same-day latest record id=101 kept, got %d", collapsed[0].ID)
	}
	if collapsed[1].ID != 110 {
		t.Fatalf("expected next-day record kept, got %d", collapsed[1].ID)
	}

	counts := countRecommendOccurrencesByCode(records)
	if counts["001309.SZ"] != 4 {
		t.Fatalf("expected raw repeat count=4, got %d", counts["001309.SZ"])
	}
}

func buildValidAiRecommendForCreate(dataTime time.Time, stockCode, stockName string) *models.AiRecommendStocks {
	return &models.AiRecommendStocks{
		DataTime:                 &dataTime,
		ModelName:                "gpt-5.4",
		StockCode:                stockCode,
		StockName:                stockName,
		BkName:                   "测试板块",
		RecommendReason:          "核心逻辑：主线催化清晰，量价结构具备跟踪价值",
		RecommendBuyPrice:        "10.00-10.50",
		RecommendStopProfitPrice: "11.20-11.80",
		RecommendStopLossPrice:   "9.60",
		RiskRemarks:              "若板块退潮或跌破止损位，需要严格执行退出",
	}
}

func timePtr(v time.Time) *time.Time {
	return &v
}
