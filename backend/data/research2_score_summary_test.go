package data

import (
	"strings"
	"testing"

	"go-stock/internal/researchevidence"
)

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
