package data

import (
	"strings"
	"testing"
)

func TestRenderMarketSummaryTemplateAddsStructuredInstruction(t *testing.T) {
	prompt := RenderMarketSummaryTemplate("你是一名专业A股分析师，请基于公开信息输出结论。")
	mustContain := []string{
		"# 市场主线",
		"# 候选方向",
		"# 风险提示",
		"# 推荐结论",
		"# 交易计划说明",
		"# 推荐股票池",
		"| 股票（代码） | 所属方向 | 核心催化 | 关键证据 | 价格锚点 | 买入区间 | 止盈区间 | 止损位 | 买入依据 | 失效条件 | 风险点 | 预期周期 | 事件强度 | 资金确认度 | 基本面匹配度 | 技术面匹配度 | 操作备注 |",
		"[市场资讯]",
		"非交易时段",
		"未来3-5个交易日",
		"“推荐股票池”的输出数量与生产候选上限",
	}
	for _, item := range mustContain {
		if !strings.Contains(prompt, item) {
			t.Fatalf("expected prompt to contain %q", item)
		}
	}
	if strings.Contains(prompt, "# 推荐分层") {
		t.Fatalf("expected prompt to drop legacy recommend tier heading")
	}
	if strings.Contains(prompt, "| 分类 |") {
		t.Fatalf("expected prompt to drop legacy category column")
	}
	if strings.Contains(prompt, "执行状态") {
		t.Fatalf("expected prompt to drop execution-state wording")
	}
	if strings.Contains(prompt, "推荐股票池最多输出 2 只股票") {
		t.Fatalf("expected prompt to drop legacy fixed two-stock limit")
	}
	if strings.Contains(prompt, "本次“推荐股票池”目标输出") {
		t.Fatalf("expected reusable template to remain count-neutral: %s", prompt)
	}
}

func TestNormalizeMarketSummaryQuestion(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty", input: "", want: DefaultMarketSummaryQuestion},
		{name: "legacy", input: "总结和分析股票市场新闻中的投资机会", want: DefaultMarketSummaryQuestion},
		{name: "with instruction", input: "自定义问题\n\n【市场资讯AI总结输出规范】\n# 市场主线", want: "自定义问题"},
		{name: "placeholder template", input: "{{stockName}}分析和总结", want: DefaultMarketSummaryQuestion},
		{name: "generic market info", input: "市场资讯分析和总结", want: DefaultMarketSummaryQuestion},
		{name: "legacy official two stocks", input: "总结和分析股票市场新闻中的投资机会，并推荐2个A股，并给出关键价位与交易计划", want: DefaultMarketSummaryQuestion},
		{name: "legacy official three stocks with ge and A share", input: "总结和分析股票市场新闻中的投资机会，并推荐3个A股", want: DefaultMarketSummaryQuestion},
		{name: "legacy official three stocks with zhi and A share", input: "总结和分析股票市场新闻中的投资机会，并推荐3只A股", want: DefaultMarketSummaryQuestion},
		{name: "legacy official three stocks with ge and stock", input: "总结和分析股票市场新闻中的投资机会，并推荐3个A股股票", want: DefaultMarketSummaryQuestion},
		{name: "legacy official three stocks with zhi and stock", input: "总结和分析股票市场新闻中的投资机会，并推荐3只A股股票", want: DefaultMarketSummaryQuestion},
		{name: "custom", input: "从最近市场资讯里提炼可交易主线，并筛选3只A股", want: "从最近市场资讯里提炼可交易主线，并筛选3只A股"},
		{name: "current time prefix with custom count", input: "请根据当前时间，总结和分析股票市场新闻中的投资机会，并推荐5只A股", want: "请根据当前时间，总结和分析股票市场新闻中的投资机会，并推荐5只A股"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeMarketSummaryQuestion(tc.input); got != tc.want {
				t.Fatalf("NormalizeMarketSummaryQuestion() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildMarketSummaryExecutionQuestion(t *testing.T) {
	tests := []struct {
		name              string
		question          string
		wantQuestion      string
		wantCountPolicy   string
		dontWantCountRule string
	}{
		{
			name:              "default count policy",
			question:          "自定义市场问题",
			wantQuestion:      "自定义市场问题",
			wantCountPolicy:   "目标输出 8 到 12 只股票，其中最多 4 只可作为可交易生产候选",
			dontWantCountRule: "目标输出 3 只股票",
		},
		{
			name:              "explicit three stocks",
			question:          "从最近市场资讯里提炼可交易主线，并推荐3只A股",
			wantQuestion:      "从最近市场资讯里提炼可交易主线，并推荐3只A股",
			wantCountPolicy:   "目标输出 3 只股票，其中最多 3 只可作为可交易生产候选",
			dontWantCountRule: "目标输出 8 到 12 只股票",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executionQuestion := BuildMarketSummaryExecutionQuestion(tt.question)
			if !strings.Contains(executionQuestion, tt.wantQuestion) {
				t.Fatalf("expected execution question to contain original question %q", tt.wantQuestion)
			}
			if !strings.Contains(executionQuestion, "【市场资讯AI总结输出规范】") {
				t.Fatalf("expected execution question to contain output instruction")
			}
			if strings.Count(executionQuestion, "【市场资讯AI总结输出规范】") != 1 {
				t.Fatalf("expected execution question to contain output instruction once")
			}
			if strings.Count(executionQuestion, "本次“推荐股票池”目标输出") != 1 {
				t.Fatalf("expected one authoritative recommendation count rule, got: %s", executionQuestion)
			}
			if !strings.Contains(executionQuestion, tt.wantCountPolicy) {
				t.Fatalf("expected execution question to contain %q, got: %s", tt.wantCountPolicy, executionQuestion)
			}
			if strings.Contains(executionQuestion, tt.dontWantCountRule) {
				t.Fatalf("expected execution question not to contain conflicting rule %q, got: %s", tt.dontWantCountRule, executionQuestion)
			}
			if strings.Contains(executionQuestion, "推荐股票池最多输出 2 只股票") || strings.Contains(executionQuestion, "最多输出 2 只") {
				t.Fatalf("expected execution question to drop legacy fixed two-stock limit: %s", executionQuestion)
			}
			if !strings.Contains(executionQuestion, "甚至可以为 0 只") || !strings.Contains(executionQuestion, "不得编造股票") {
				t.Fatalf("expected execution question to allow zero after strict verification without padding: %s", executionQuestion)
			}
		})
	}
}
