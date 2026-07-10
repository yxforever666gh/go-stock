package data

import (
	"strings"
	"testing"
)

func TestResolveMarketSummaryRecommendationCountPolicy(t *testing.T) {
	tests := []struct {
		name             string
		question         string
		minimumOutput    int
		maximumOutput    int
		productionTarget int
		requestedMinimum int
		requestedMaximum int
		custom           bool
		clamped          bool
	}{
		{name: "default", question: "总结市场机会", minimumOutput: 8, maximumOutput: 12, productionTarget: 4, requestedMinimum: 8, requestedMaximum: 12},
		{name: "explicit three stocks", question: "推荐3只A股", minimumOutput: 3, maximumOutput: 3, productionTarget: 3, requestedMinimum: 3, requestedMaximum: 3, custom: true},
		{name: "filter five stocks", question: "请筛选5个股票", minimumOutput: 5, maximumOutput: 5, productionTarget: 4, requestedMinimum: 5, requestedMaximum: 5, custom: true},
		{name: "hyphen range", question: "输出5-8只A股", minimumOutput: 5, maximumOutput: 8, productionTarget: 4, requestedMinimum: 5, requestedMaximum: 8, custom: true},
		{name: "chinese range", question: "推荐5至8只股票", minimumOutput: 5, maximumOutput: 8, productionTarget: 4, requestedMinimum: 5, requestedMaximum: 8, custom: true},
		{name: "clamp above output limit", question: "推荐20只A股", minimumOutput: 12, maximumOutput: 12, productionTarget: 4, requestedMinimum: 20, requestedMaximum: 20, custom: true, clamped: true},
		{name: "trading day range is not a stock count", question: "分析未来3-5个交易日的市场机会", minimumOutput: 8, maximumOutput: 12, productionTarget: 4, requestedMinimum: 8, requestedMaximum: 12},
		{name: "candidate directions are not stock count", question: "给出2个候选方向，再推荐8只股票", minimumOutput: 8, maximumOutput: 8, productionTarget: 4, requestedMinimum: 8, requestedMaximum: 8, custom: true},
		{name: "spaced A share unit", question: "推荐 3 个 A 股", minimumOutput: 3, maximumOutput: 3, productionTarget: 3, requestedMinimum: 3, requestedMaximum: 3, custom: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveMarketSummaryRecommendationCountPolicy(tt.question)
			if got.MinimumOutput != tt.minimumOutput || got.MaximumOutput != tt.maximumOutput || got.ProductionTarget != tt.productionTarget {
				t.Fatalf("ResolveMarketSummaryRecommendationCountPolicy(%q) = output %d-%d, production %d; want %d-%d, production %d", tt.question, got.MinimumOutput, got.MaximumOutput, got.ProductionTarget, tt.minimumOutput, tt.maximumOutput, tt.productionTarget)
			}
			if got.RequestedMinimum != tt.requestedMinimum || got.RequestedMaximum != tt.requestedMaximum {
				t.Fatalf("ResolveMarketSummaryRecommendationCountPolicy(%q) requested = %d-%d, want %d-%d", tt.question, got.RequestedMinimum, got.RequestedMaximum, tt.requestedMinimum, tt.requestedMaximum)
			}
			if got.Custom != tt.custom || got.Clamped != tt.clamped {
				t.Fatalf("ResolveMarketSummaryRecommendationCountPolicy(%q) custom/clamped = %v/%v, want %v/%v", tt.question, got.Custom, got.Clamped, tt.custom, tt.clamped)
			}
		})
	}
}

func TestMarketSummaryRecommendationCountPolicyInstructionExplainsClamp(t *testing.T) {
	policy := ResolveMarketSummaryRecommendationCountPolicy("推荐20只A股")
	instruction := policy.Instruction()

	for _, want := range []string{"目标输出 12 只股票", "最多 4 只可作为可交易生产候选", "原始请求为20 只", "上限为 12 只", "截断"} {
		if !strings.Contains(instruction, want) {
			t.Fatalf("expected clamped instruction to contain %q, got: %s", want, instruction)
		}
	}
}

func TestResolveMarketSummaryFinalCandidateLimit(t *testing.T) {
	tests := []struct {
		question string
		want     int
	}{
		{question: "总结市场机会", want: 12},
		{question: "推荐3只A股", want: 3},
		{question: "推荐5-8只A股", want: 8},
		{question: "推荐20只A股", want: 12},
	}
	for _, tt := range tests {
		if got := resolveMarketSummaryFinalCandidateLimit(tt.question); got != tt.want {
			t.Fatalf("resolveMarketSummaryFinalCandidateLimit(%q) = %d, want %d", tt.question, got, tt.want)
		}
	}
}
