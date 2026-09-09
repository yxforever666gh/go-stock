package data

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"go-stock/internal/researchevidence"
)

type research2IndexFixture struct {
	*research2StructuredSourceFixture
	indexes string
}

func (f research2IndexFixture) CollectMarket(context.Context, time.Time) ([]researchevidence.SourceDocument, error) {
	available := f.cutoff.Add(time.Second)
	return []researchevidence.SourceDocument{{SourceName: "全球与国内指数", Category: "market", Content: f.indexes, AvailableAt: &available, CollectedAt: available}}, nil
}

func TestResearch2ScoreIndexNumbersSurviveCollectorSummaryCap(t *testing.T) {
	at := time.Date(2026, 9, 9, 10, 32, 0, 0, shanghaiDataLocation())
	overseas := make([]map[string]any, 9)
	for index := range overseas {
		overseas[index] = map[string]any{"code": fmt.Sprint(index), "qtcode": fmt.Sprint("us", index), "name": "海外", "zxj": "100", "zdf": "-1", "state": "close"}
	}
	domestic := []map[string]any{
		{"code": "000001", "qtcode": "sh000001", "name": "上证指数", "zxj": "4000.12", "zdf": "-0.32", "state": "open"},
		{"code": "399001", "qtcode": "sz399001", "name": "深证成指", "zxj": "12000.34", "zdf": "0.27", "state": "open"},
		{"code": "399006", "qtcode": "sz399006", "name": "创业板指", "zxj": "2600.56", "zdf": "0.52", "state": "open"},
	}
	encoded, _ := json.Marshal(map[string]any{"america": overseas, "asia": domestic, "common": domestic})
	collector := newResearch2StructuredCollector(t, at, research2StructuredRows(at, 20), 20, 5)
	collector.sources = research2IndexFixture{&research2StructuredSourceFixture{cutoff: at}, string(encoded)}
	evidence, err := collector.Collect(context.Background(), at)
	if err != nil {
		t.Fatal(err)
	}
	var compact research2CompactSnapshot
	if err = json.Unmarshal([]byte(evidence.Prompt), &compact); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, source := range compact.Sources {
		if source.SourceName != "全球与国内指数" {
			continue
		}
		found = true
		var summary struct {
			Rows    []map[string]any `json:"rows"`
			Omitted int              `json:"omittedRows"`
		}
		if err := json.Unmarshal([]byte(source.Summary), &summary); err != nil {
			t.Fatal(err)
		}
		if len(summary.Rows) != 8 || summary.Omitted != 4 || source.Status != "ok" {
			t.Fatalf("summary=%+v status=%s", summary, source.Status)
		}
		for index, want := range domestic {
			for key, value := range want {
				if summary.Rows[index][key] != value {
					t.Fatalf("index field %s lost: %+v", key, summary.Rows[index])
				}
			}
		}
	}
	if !found {
		t.Fatal("index source missing from model evidence")
	}
	for _, document := range evidence.Documents {
		if document.SourceName == "全球与国内指数" && document.Content != string(encoded) {
			t.Fatal("raw audit source changed")
		}
	}
}

// These shapes are reduced from the saved September 8 research audit. They
// contain no runtime database or provider credentials.
func TestResearch2ScoreSummaryKeepsMembershipAndSectorNumbers(t *testing.T) {
	for _, sample := range []struct {
		name, category, payload string
		want                    []string
	}{
		{"东方财富概念 sh600343", "stock", `{"code":0,"message":"ok","success":true,"result":{"data":[{"BOARD_NAME":"军工","NEW_BOARD_CODE":"BK0490","SECURITY_CODE":"600343","SECUCODE":"600343.SH","BOARD_YIELD":1.5}]}}`, []string{"军工", "BK0490", "600343", "1.5"}},
		{"Sina行业资金", "sector", `[{"name":"化工行业","category":"new_hghy","netamount":"1425376793.8700","avg_changeratio":"0.0063319"}]`, []string{"化工行业", "1425376793.8700", "0.0063319"}},
		{"腾讯行业排名", "sector", `{"code":0,"data":[{"bd_code":"pt01801016","bd_name":"种植业","bd_zdf":"5.15","bd_zdf5":"7.05","bd_zdf20":"32.97"}]}`, []string{"种植业", "pt01801016", "5.15", "7.05", "32.97"}},
		{"结构化行业资金", "sector", `{"status":"ok","data":[{"code":"BK0490","name":"军工","netAmount":12345.6,"changePct":1.2}]}`, []string{"军工", "BK0490", "12345.6", "1.2"}},
		{"公告 sh600343", "stock", `[{"title":"旧订单公告","notice_date":"2026-08-27"},{"title":"诉讼公告","notice_date":"2026-09-08"}]`, []string{"旧订单公告", "2026-08-27", "诉讼公告", "2026-09-08"}},
	} {
		t.Run(sample.name, func(t *testing.T) {
			summary := research2CompactDocumentSummary(researchevidence.SourceDocument{SourceName: sample.name, Category: sample.category, Content: sample.payload})
			for _, word := range sample.want {
				if !strings.Contains(summary, word) {
					t.Fatalf("score fact %q lost: %s", word, summary)
				}
			}
		})
	}
}
