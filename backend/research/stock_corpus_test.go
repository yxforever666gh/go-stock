package research

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestStockSourceCorpusUsesPerCandidateBudgetAndCompleteJSON(t *testing.T) {
	now := time.Date(2026, 9, 3, 14, 20, 0, 0, shanghaiLocation)
	sources := []SourceDocument{
		{SourceID: "S001", SourceName: "Sina/Tencent实时行情 sh600000", Category: "stock", CollectedAt: now, PromptContent: `{"price":10,"asOf":"2026-09-03T14:10:00+08:00"}`},
		{SourceID: "S002", SourceName: "Sina日K sh600000", Category: "stock", CollectedAt: now, PromptContent: `{"order":"newest_first","asOf":"2026-09-03","bars":[["2026-09-03",10]]}`},
		{SourceID: "S003", SourceName: "Tencent分钟K sh600000", Category: "stock", CollectedAt: now, PromptContent: `{"order":"newest_first","asOf":"14:20","bars":[["14:20",10]]}`},
		{SourceID: "S004", SourceName: "东方财富公告 sh600000", Category: "stock", CollectedAt: now, PromptContent: `[{"title":"` + strings.Repeat("x", 8000) + `"}]`},
	}
	content := stockSourceCorpus(sources, []StockCandidate{{Code: "sh600000", Name: "浦发银行"}}, 64*1024, 6*1024)
	if !json.Valid([]byte(content)) || len(content) > 6*1024+512 {
		t.Fatalf("invalid or oversized corpus (%d): %s", len(content), content)
	}
	for _, required := range []string{"S001", "S002", "S003", "newest_first", `"asOf":"2026-09-03T14:10:00+08:00"`} {
		if !strings.Contains(content, required) {
			t.Fatalf("corpus omitted mandatory value %q: %s", required, content)
		}
	}
}

func TestStockSourceCorpusShrinksMandatoryPayloadWithoutDroppingIt(t *testing.T) {
	now := time.Date(2026, 9, 3, 14, 20, 0, 0, shanghaiLocation)
	largeBars := `{"order":"newest_first","asOf":"2026-09-03","bars":[` + strings.Repeat(`["2026-09-03",10,11,9,10,"`+strings.Repeat("x", 200)+`"],`, 80) + `["2026-09-02",10]]}`
	sources := []SourceDocument{{SourceID: "S001", SourceName: "Sina日K sh600000", Category: "stock", CollectedAt: now, PromptContent: largeBars}}
	content := stockSourceCorpus(sources, []StockCandidate{{Code: "sh600000", Name: "Alpha"}}, 64*1024, 6*1024)
	if !json.Valid([]byte(content)) || !strings.Contains(content, `"sourceId":"S001"`) || !strings.Contains(content, `"content"`) || !strings.Contains(content, "2026-09-03") {
		t.Fatalf("mandatory source was dropped while shrinking: %s", content)
	}
}
