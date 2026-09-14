package research

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"go-stock/internal/researchevidence"
)

func TestStockSourceCorpusUsesPerCandidateBudgetAndCompleteJSON(t *testing.T) {
	now := time.Date(2026, 9, 3, 14, 20, 0, 0, shanghaiLocation)
	sources := []researchevidence.SourceDocument{
		{SourceID: "S001", SourceName: "Sina/Tencent实时行情 sh600000", Category: "stock", CollectedAt: now, PromptContent: `{"price":10,"asOf":"2026-09-03T14:10:00+08:00"}`},
		{SourceID: "S002", SourceName: "Sina日K sh600000", Category: "stock", CollectedAt: now, PromptContent: `{"order":"newest_first","asOf":"2026-09-03","bars":[["2026-09-03",10]]}`},
		{SourceID: "S003", SourceName: "Tencent分钟K sh600000", Category: "stock", CollectedAt: now, PromptContent: `{"order":"newest_first","asOf":"14:20","bars":[["14:20",10]]}`},
		{SourceID: "S004", SourceName: "东方财富公告 sh600000", Category: "stock", CollectedAt: now, PromptContent: `[{"title":"` + strings.Repeat("x", 8000) + `"}]`},
	}
	content := stockSourceCorpus(sources, []researchevidence.StockCandidate{{Code: "sh600000", Name: "浦发银行"}}, 64*1024, 6*1024)
	if !json.Valid([]byte(content)) || len(content) > 6*1024+512 {
		t.Fatalf("invalid or oversized corpus (%d): %s", len(content), content)
	}
	for _, required := range []string{"S001", "S002", "S003", "newest_first", `"asOf":"2026-09-03T14:10:00+08:00"`} {
		if !strings.Contains(content, required) {
			t.Fatalf("corpus omitted mandatory value %q: %s", required, content)
		}
	}
}

func TestStockSourceCorpusReservesFinancialAndNoticeFacts(t *testing.T) {
	now := time.Date(2026, 9, 14, 13, 35, 43, 0, shanghaiLocation)
	var sources []researchevidence.SourceDocument
	var candidates []researchevidence.StockCandidate
	for c := 0; c < 10; c++ {
		code := fmt.Sprintf("sh600%03d", c)
		candidates = append(candidates, researchevidence.StockCandidate{Code: code, Name: "合成样例"})
		finance := `{"result":{"data":[{"REPORT_DATE":"2026-06-30","NOTICE_DATE":"2026-08-31","TOTAL_OPERATE_INCOME":37929268927.18,"NETPROFIT":15033525962.4,"ROE":6.55,"DEBT_ASSET_RATIO":59.36,"NETCASH_OPERATE":12345,"unused":"` + strings.Repeat("x", 8000) + `"},{"REPORT_DATE":"2026-03-31","NETPROFIT":0}]}}`
		notices := []map[string]any{}
		for n := 1; n <= 7; n++ {
			notices = append(notices, map[string]any{"notice_date": fmt.Sprintf("2026-09-%02d", n), "title": fmt.Sprintf("公告%d：", n) + strings.Repeat("重要事项", 60), "art_code": fmt.Sprint(n), "unused": strings.Repeat("x", 4000)})
		}
		noticeJSON, _ := json.Marshal(notices)
		payloads := []string{
			`{"asOf":"2026-09-14T13:35:40+08:00","order":"newest_first","quotes":[{"code":"sh600000","name":"合成样例","date":"2026-09-14","time":"13:35:40","price":28.54,"previousClose":28.45,"volume":71912300,"amount":2057224882,"open":28.42,"high":28.84,"low":28.38,"bid1":28.53,"ask1":28.54,"market":"A"}]}`,
			`{"asOf":"2026-09-11","returns":{"5d":0.001,"20d":0.012,"60d":0.067},"bars":[` + strings.Repeat(`["2026-09-11",28,29,27,28,100000],`, 19) + `["2026-09-10",28,29,27,28,100000]]}`,
			`{"asOf":"2026-09-14T13:35:00+08:00","windows":[{"minutes":15,"returnRate":0.01},{"minutes":30,"returnRate":0.02},{"minutes":60,"returnRate":0.03}],"bars":[["13:35",28,100,2800]]}`,
			finance, `[{"opendate":"2026-09-11","netamount":"661154999.72","r0_net":"831874065.79"}]`, string(noticeJSON), `{"status":"no_match","message":"本次未匹配到相关新闻，不代表公司没有风险"}`, `[{"title":"研报"}]`, `[{"name":"电力"}]`, `[{"content":"互动"}]`,
		}
		names := []string{"Sina/Tencent实时行情", "Sina日K", "Tencent分钟K", "东方财富财务", "Sina资金流", "东方财富公告", "相关市场新闻", "东方财富研报", "东方财富概念", "巨潮互动易"}
		for n, name := range names {
			sources = append(sources, researchevidence.SourceDocument{SourceID: fmt.Sprintf("S%03d", c*10+n+1), SourceName: name + " " + code, CollectedAt: now, PromptContent: payloads[n], Content: payloads[n]})
		}
	}
	corpus := stockSourceCorpus(sources, candidates, 64*1024, 6*1024)
	var batch compactStockPromptBatch
	if json.Unmarshal([]byte(corpus), &batch) != nil || len(corpus) > 64*1024 || len(batch.Candidates) != 10 {
		t.Fatalf("invalid batch bytes=%d candidates=%d", len(corpus), len(batch.Candidates))
	}
	for _, raw := range batch.Candidates {
		if len(raw) > 6*1024 {
			t.Fatalf("candidate exceeds budget: %d", len(raw))
		}
		var candidate compactStockPromptCandidate
		_ = json.Unmarshal(raw, &candidate)
		for _, source := range candidate.Sources {
			if stockSourcePriority(source.SourceName) <= 5 && len(source.Content) == 0 {
				t.Fatalf("key source dropped: %s (%d bytes)", source.SourceName, len(raw))
			}
			if strings.Contains(source.SourceName, "财务") {
				for _, fact := range []string{"37929268927.18", `"operatingCashFlow":12345`, `"netProfit":0`, `"roe":null`, "2026-03-31"} {
					if !strings.Contains(string(source.Content), fact) {
						t.Fatalf("lost financial fact %s: %s", fact, source.Content)
					}
				}
			}
			if strings.Contains(source.SourceName, "公告") {
				var list struct {
					Kind  string
					Items []map[string]any
				}
				_ = json.Unmarshal(source.Content, &list)
				if list.Kind != "announcement_list" || len(list.Items) != 5 || list.Items[0]["ref"] != "7" || list.Items[4]["ref"] != "3" {
					t.Fatalf("notice list lost: %s", source.Content)
				}
			}
		}
	}
	for _, source := range sources {
		if source.InputStatus == "" {
			t.Fatal("audit state not populated")
		}
	}
}

func TestFinancialSummaryHandlesBankMissingFieldsAndZeros(t *testing.T) {
	summary := string(stockSourceSummary("财务", json.RawMessage(`{"result":{"data":[{"REPORT_DATE":"2026-06-30","OPERATE_INCOME":123,"NETPROFIT":0},{"REPORT_DATE":"2026-03-31","NETPROFIT":22},{"REPORT_DATE":"2025-12-31","NETPROFIT":999}]}}`)))
	for _, fact := range []string{`"revenue":123`, `"netProfit":0`, `"operatingCashFlow":null`, `"debtAssetRatio":null`} {
		if !strings.Contains(summary, fact) {
			t.Fatalf("missing %s: %s", fact, summary)
		}
	}
	if strings.Contains(summary, "999") {
		t.Fatalf("old period included: %s", summary)
	}
}

func TestStockSourceAuditMatchesOmissionsAndFailures(t *testing.T) {
	large := map[string]int{}
	for n := 0; n < 200; n++ {
		large[fmt.Sprintf("provider_field_%03d", n)] = n
	}
	largeJSON, _ := json.Marshal(large)
	sources := []researchevidence.SourceDocument{
		{SourceID: "S001", SourceName: "实时行情 sh600000", Content: `{"price":10}`},
		{SourceID: "S002", SourceName: "分钟K sh600000", Content: `{"price":999}`, Error: "unavailable"},
		{SourceID: "S003", SourceName: "公告 sh600000", Content: `[{"title":"notice"}]`},
		{SourceID: "S004", SourceName: "研报 sh600000", Content: string(largeJSON)},
	}
	_ = stockSourceCorpus(sources, []researchevidence.StockCandidate{{Code: "sh600000"}}, 64000, 6000)
	if sources[0].InputStatus != "included" || sources[1].InputStatus != "unavailable" || sources[1].InputReason != "unavailable" || sources[2].InputStatus != "summarized" {
		t.Fatalf("audit=%+v", sources)
	}
	_ = stockSourceCorpus(sources, []researchevidence.StockCandidate{{Code: "sh600000"}}, 64000, 1500)
	if sources[3].InputStatus != "omitted" {
		t.Fatalf("available but oversized source mislabeled: %+v", sources[3])
	}
	_ = stockSourceCorpus(sources, []researchevidence.StockCandidate{{Code: "sh600000"}}, 100, 100)
	for _, source := range sources {
		if source.InputStatus != "omitted" {
			t.Fatalf("batch drop reported as included: %+v", source)
		}
	}
}

func TestStockSourceCorpusShrinksMandatoryPayloadWithoutDroppingIt(t *testing.T) {
	now := time.Date(2026, 9, 3, 14, 20, 0, 0, shanghaiLocation)
	largeBars := `{"order":"newest_first","asOf":"2026-09-03","bars":[` + strings.Repeat(`["2026-09-03",10,11,9,10,"`+strings.Repeat("x", 200)+`"],`, 80) + `["2026-09-02",10]]}`
	sources := []researchevidence.SourceDocument{{SourceID: "S001", SourceName: "Sina日K sh600000", Category: "stock", CollectedAt: now, PromptContent: largeBars}}
	content := stockSourceCorpus(sources, []researchevidence.StockCandidate{{Code: "sh600000", Name: "Alpha"}}, 64*1024, 6*1024)
	if !json.Valid([]byte(content)) || !strings.Contains(content, `"sourceId":"S001"`) || !strings.Contains(content, `"content"`) || !strings.Contains(content, "2026-09-03") {
		t.Fatalf("mandatory source was dropped while shrinking: %s", content)
	}
}
