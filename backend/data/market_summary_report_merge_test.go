package data

import (
	"reflect"
	"strings"
	"testing"
)

func TestMergeMarketSummarySupplementReportReplacesAppendsAndRejectsUnconfirmedRows(t *testing.T) {
	base := `# 市场结论
维持震荡判断。

# 推荐股票池
| 股票（代码） | 所属方向 | 操作备注 |
| --- | --- | --- |
| 平安银行(000001.SZ) | 银行 | 首轮旧计划 |
| 浦发银行(600000.SH) | 银行 | 保留原计划 |

# 风险提示
保留基础报告风险章节。`
	supplement := `# 推荐股票池
| 股票（代码） | 所属方向 | 操作备注 |
| --- | --- | --- |
| 平安银行(000001.SZ) | 银行 | 二轮修正计划 |
| 东方财富(300059.SZ) | 券商 | 二轮新增计划 |
| 宁德时代(300750.SZ) | 电池 | 模型声称成功但后端拒绝 |

# 补位说明
模型声称三只股票均已补位成功。`

	merged, stats := MergeMarketSummarySupplementReport(
		base,
		supplement,
		[]string{"000001.SZ", "300059.SZ"},
		12,
	)

	oldIndex := strings.Index(merged, "二轮修正计划")
	retainedIndex := strings.Index(merged, "保留原计划")
	appendedIndex := strings.Index(merged, "二轮新增计划")
	if oldIndex < 0 || retainedIndex < 0 || appendedIndex < 0 || !(oldIndex < retainedIndex && retainedIndex < appendedIndex) {
		t.Fatalf("expected replacement in place and new row appended, got:\n%s", merged)
	}
	if strings.Contains(merged, "首轮旧计划") {
		t.Fatalf("expected old same-code row to be replaced, got:\n%s", merged)
	}
	if strings.Contains(merged, "宁德时代") || strings.Contains(merged, "模型声称三只股票均已补位成功") {
		t.Fatalf("expected unconfirmed row and AI supplement note to be ignored, got:\n%s", merged)
	}
	if !strings.Contains(merged, "保留基础报告风险章节") {
		t.Fatalf("expected non-table base sections to remain, got:\n%s", merged)
	}

	if !stats.BaseTableFound || !stats.SupplementTableFound {
		t.Fatalf("expected both recommendation tables to be found: %+v", stats)
	}
	if stats.BaseRecommendationRows != 2 || stats.SupplementRecommendationRows != 3 {
		t.Fatalf("unexpected row counts: %+v", stats)
	}
	if !reflect.DeepEqual(stats.ReplacedCodes, []string{"000001.SZ"}) {
		t.Fatalf("replaced codes = %v", stats.ReplacedCodes)
	}
	if !reflect.DeepEqual(stats.AppendedCodes, []string{"300059.SZ"}) {
		t.Fatalf("appended codes = %v", stats.AppendedCodes)
	}
	if !reflect.DeepEqual(stats.VisibleCodes, []string{"000001.SZ", "600000.SH", "300059.SZ"}) {
		t.Fatalf("visible codes = %v", stats.VisibleCodes)
	}
	if stats.UnconfirmedRowsOmitted != 1 || !reflect.DeepEqual(stats.UnconfirmedCodes, []string{"300750.SZ"}) {
		t.Fatalf("unexpected unconfirmed stats: %+v", stats)
	}
}

func TestMergeMarketSummarySupplementReportDeduplicatesBeforeStableLimit(t *testing.T) {
	base := `# 推荐股票池
| 股票（代码） | 备注 |
| --- | --- |
| 甲(000001.SZ) | 甲第一行 |
| 甲重复(000001.SZ) | 甲重复行 |
| 乙(600000.SH) | 乙原行 |`
	supplement := `# 推荐股票池
| 股票（代码） | 备注 |
| --- | --- |
| 丙(300001.SZ) | 丙第一行 |
| 丙重复(300001.SZ) | 丙重复行 |
| 丁(300002.SZ) | 丁第一行 |`

	merged, stats := MergeMarketSummarySupplementReport(
		base,
		supplement,
		[]string{"300001.SZ", "300002.SZ"},
		3,
	)

	if strings.Contains(merged, "甲重复行") || strings.Contains(merged, "丙重复行") {
		t.Fatalf("expected duplicate rows to be omitted, got:\n%s", merged)
	}
	if !strings.Contains(merged, "丙第一行") || strings.Contains(merged, "丁第一行") {
		t.Fatalf("expected stable truncation after appending, got:\n%s", merged)
	}
	if stats.DuplicateRowsOmitted != 2 {
		t.Fatalf("duplicate rows omitted = %d, want 2", stats.DuplicateRowsOmitted)
	}
	if stats.OutputRowsOmitted != 1 || !reflect.DeepEqual(stats.OmittedByLimitCodes, []string{"300002.SZ"}) {
		t.Fatalf("unexpected limit stats: %+v", stats)
	}
	if !reflect.DeepEqual(stats.VisibleCodes, []string{"000001.SZ", "600000.SH", "300001.SZ"}) {
		t.Fatalf("visible codes = %v", stats.VisibleCodes)
	}
}

func TestMergeMarketSummarySupplementReportNormalizesCodeAndRemovesPlaceholder(t *testing.T) {
	base := `# 推荐股票池
| 股票（代码） | 备注 |
| --- | --- |
| 暂无新增高质量候选标的 | 同日已无新增正式推荐 |`
	supplement := `# 推荐股票池
| 股票（代码） | 备注 |
| --- | --- |
| 平安银行（000001） | 二轮新增 |`

	merged, stats := MergeMarketSummarySupplementReport(base, supplement, []string{"sz000001"}, 12)

	if strings.Contains(merged, "暂无新增高质量候选标的") || !strings.Contains(merged, "平安银行（000001）") {
		t.Fatalf("expected confirmed row to replace placeholder, got:\n%s", merged)
	}
	if !reflect.DeepEqual(stats.AppendedCodes, []string{"000001.SZ"}) || !reflect.DeepEqual(stats.VisibleCodes, []string{"000001.SZ"}) {
		t.Fatalf("unexpected normalized code stats: %+v", stats)
	}
}

func TestMergeMarketSummarySupplementReportKeepsPlaceholderWhenNothingIsConfirmed(t *testing.T) {
	base := `# 推荐股票池
| 股票（代码） | 备注 |
| --- | --- |
| 暂无新增高质量候选标的 | 严格核验后无候选 |`
	supplement := `# 推荐股票池
| 股票（代码） | 备注 |
| --- | --- |
| 平安银行(000001.SZ) | 未确认行 |

# 补位说明
已补位成功。`

	merged, stats := MergeMarketSummarySupplementReport(base, supplement, nil, 12)

	if merged != base {
		t.Fatalf("expected base report to remain unchanged, got:\n%s", merged)
	}
	if stats.UnconfirmedRowsOmitted != 1 || !reflect.DeepEqual(stats.UnconfirmedCodes, []string{"000001.SZ"}) {
		t.Fatalf("unexpected unconfirmed stats: %+v", stats)
	}
}

func TestMergeMarketSummarySupplementReportReportsMissingAcceptedCode(t *testing.T) {
	base := `# 推荐股票池
| 股票（代码） | 备注 |
| --- | --- |
| 平安银行(000001.SZ) | 原计划 |`
	supplement := `# 推荐股票池
| 股票（代码） | 备注 |
| --- | --- |
| 浦发银行(600000.SH) | 其他行 |`

	merged, stats := MergeMarketSummarySupplementReport(base, supplement, []string{"300059.SZ"}, 12)

	if !strings.Contains(merged, "平安银行") || strings.Contains(merged, "浦发银行") {
		t.Fatalf("expected original row only, got:\n%s", merged)
	}
	if !reflect.DeepEqual(stats.MissingAcceptedCodes, []string{"300059.SZ"}) {
		t.Fatalf("missing accepted codes = %v", stats.MissingAcceptedCodes)
	}
}

func TestMergeMarketSummarySupplementReportUsesFirstTableInsideRecommendationSection(t *testing.T) {
	base := `# 盘面数据
| 股票（代码） | 备注 |
| --- | --- |
| 盘面样本(600000.SH) | 不属于推荐池 |

# 推荐股票池
| 股票（代码） | 备注 |
| --- | --- |
| 平安银行(000001.SZ) | 推荐池原计划 |

| 股票（代码） | 备注 |
| --- | --- |
| 第二张表(300750.SZ) | 不应被替换 |

# 风险提示
原风险提示。`
	supplement := `# 推荐股票池
| 股票（代码） | 备注 |
| --- | --- |
| 平安银行(000001.SZ) | 推荐池修正计划 |

| 股票（代码） | 备注 |
| --- | --- |
| 第二张补位表(300750.SZ) | 不应采信 |`

	merged, stats := MergeMarketSummarySupplementReport(base, supplement, []string{"000001.SZ", "300750.SZ"}, 12)

	if !strings.Contains(merged, "盘面样本(600000.SH) | 不属于推荐池") {
		t.Fatalf("expected table outside recommendation section to remain untouched, got:\n%s", merged)
	}
	if !strings.Contains(merged, "平安银行(000001.SZ) | 推荐池修正计划") || strings.Contains(merged, "推荐池原计划") {
		t.Fatalf("expected first recommendation table to be updated, got:\n%s", merged)
	}
	if !strings.Contains(merged, "第二张表(300750.SZ) | 不应被替换") || strings.Contains(merged, "第二张补位表") {
		t.Fatalf("expected later tables and supplement tables to be ignored, got:\n%s", merged)
	}
	if !reflect.DeepEqual(stats.MissingAcceptedCodes, []string{"300750.SZ"}) {
		t.Fatalf("expected accepted code from ignored second supplement table to be reported missing: %+v", stats)
	}
}
