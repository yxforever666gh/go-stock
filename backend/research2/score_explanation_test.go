package research2

import (
	"encoding/json"
	"go-stock/internal/researchevidence"
	"strings"
	"testing"
	"time"
)

func TestResearch2ScorePromptSeparatesSnapshotAndFreeze(t *testing.T) {
	t0 := time.Date(2026, 9, 8, 10, 0, 2, 0, shanghai())
	freeze := time.Date(2026, 9, 8, 10, 0, 21, 455695000, shanghai())
	prompt := buildPrompt(Evidence{FreezeAt: freeze, Prompt: `{}`}, t0)
	if !strings.Contains(prompt, "辅助来源") || !strings.Contains(prompt, "证据冻结时间") || strings.Contains(prompt, "所有指标只使用证据截止时间") {
		t.Fatal("prompt incorrectly applies the quote cutoff to auxiliary collection")
	}
	if !strings.Contains(prompt, "不得为了") {
		t.Fatal("missing threshold anchoring instruction")
	}
}

func scoreAuditDocument(id, category, content string, at time.Time) researchevidence.SourceDocument {
	return researchevidence.SourceDocument{SourceID: id, SourceName: id, Category: category, Content: content, AvailableAt: &at, CollectedAt: at}
}

func TestResearch2ScoreEvidenceBindsExactBoardsAndThemeConstituents(t *testing.T) {
	t0 := time.Date(2026, 9, 8, 10, 0, 2, 0, shanghai())
	freeze := t0.Add(20 * time.Second)
	docs := []researchevidence.SourceDocument{
		scoreAuditDocument("概念 sh600343", "stock", `{"message":"ok","result":{"data":[{"SECURITY_CODE":"600343","BOARD_NAME":"军工","NEW_BOARD_CODE":"BK0490"}]}}`, t0.Add(12*time.Second)),
		scoreAuditDocument("sectors", "sector", `{"data":[{"bd_code":"BK0490","bd_name":"军工","bd_zdf":"1.5"},{"bd_name":"军工装备","bd_zdf":"8.2"}]}`, t0.Add(13*time.Second)),
		scoreAuditDocument("theme-army", "theme", `{"themeId":"army","snapshot":{"heatScore":90},"stockConstituents":[{"assetType":"stock","code":"600343","market":"SH"}]}`, t0),
	}
	proof := BuildCandidateScoreEvidence("sh600343", docs, t0, freeze, t0.Add(-19*time.Hour))
	if proof.SectorState != "available" || len(proof.Sector) != 3 {
		t.Fatalf("valid auxiliary evidence omitted: %+v", proof)
	}
	encoded, _ := json.Marshal(proof)
	if !strings.Contains(string(encoded), `"bd_zdf":"1.5"`) || strings.Contains(string(encoded), `"8.2"`) {
		t.Fatalf("inexact board match: %s", encoded)
	}
	other := BuildCandidateScoreEvidence("sh600391", docs, t0, freeze, t0.Add(-19*time.Hour))
	if len(other.Sector) != 0 || other.SectorState != "membership_unverified" {
		t.Fatalf("unproved stock membership leaked: %+v", other)
	}
	docs[1].AvailableAt = &[]time.Time{freeze.Add(time.Second)}[0]
	docs = docs[:2]
	proof = BuildCandidateScoreEvidence("sh600343", docs, t0, freeze, time.Time{})
	if proof.SectorState != "membership_only" {
		t.Fatalf("after-freeze source accepted: %+v", proof)
	}
}

func TestResearch2ScoreCatalystDistinguishesOldEmptyFailureAndFresh(t *testing.T) {
	t0 := time.Date(2026, 9, 8, 10, 0, 2, 0, shanghai())
	freeze := t0.Add(20 * time.Second)
	since := time.Date(2026, 9, 7, 15, 0, 0, 0, shanghai())
	for _, sample := range []struct {
		name, payload, want string
		failed              bool
	}{
		{"old notice", `[{"title":"半年报","notice_date":"2026-08-27"}]`, "old_background", false},
		{"empty source", `{"items":[],"status":"ok"}`, "no_fresh_catalyst", false},
		{"provider failure", `[]`, "source_unavailable", true},
		{"new prior evening", `[{"title":"新订单","publishedAt":"2026-09-07T18:00:00+08:00"}]`, "fresh_available", false},
		{"unverified date", `[{"title":"未提供时间"}]`, "time_unverified", false},
		{"after snapshot", `[{"title":"快照后事件","publishedAt":"2026-09-08T10:00:03+08:00"}]`, "after_snapshot", false},
	} {
		t.Run(sample.name, func(t *testing.T) {
			doc := scoreAuditDocument("公告 sh600343", "stock", sample.payload, t0.Add(time.Second))
			if sample.failed {
				doc.Error = "HTTP 503"
			}
			proof := BuildCandidateScoreEvidence("sh600343", []researchevidence.SourceDocument{doc}, t0, freeze, since)
			if proof.CatalystState != sample.want {
				t.Fatalf("proof=%+v", proof)
			}
		})
	}
	theme := scoreAuditDocument("theme", "theme", `{"themeId":"army","stockConstituents":[{"assetType":"stock","code":"600343"}]}`, t0)
	catalyst := scoreAuditDocument("theme-event", "catalyst", `{"themeId":"army","event":{"eventAt":"2026-08-27T10:00:00+08:00","title":"旧事件"},"claim":{"publishedAt":"2026-09-08T09:30:00+08:00"}}`, t0)
	proof := BuildCandidateScoreEvidence("sh600343", []researchevidence.SourceDocument{theme, catalyst}, t0, freeze, since)
	if proof.CatalystState != "old_background" {
		t.Fatalf("new claim relabelled old event: %+v", proof)
	}
	if scoreScopedDocumentApplies(catalyst, "sh600391", nil, []researchevidence.SourceDocument{theme, catalyst}, freeze) {
		t.Fatal("theme catalyst applied to an unlisted stock")
	}
}

func TestResearch2ScoreCatalystKeepsEachEventDateAndCompanyReplyTime(t *testing.T) {
	t0 := time.Date(2026, 9, 8, 10, 0, 2, 0, shanghai())
	since := time.Date(2026, 9, 7, 15, 0, 0, 0, shanghai())
	doc := scoreAuditDocument("公告 sh600343", "stock", `[{"title":"诉讼公告","notice_date":"2026-09-08"},{"title":"大订单旧公告","notice_date":"2026-08-27"}]`, t0)
	proof := BuildCandidateScoreEvidence("sh600343", []researchevidence.SourceDocument{doc}, t0, t0, since)
	if len(proof.Catalyst) != 1 || len(proof.Catalyst[0].Facts) != 2 {
		t.Fatalf("missing event facts: %+v", proof)
	}
	byTitle := map[string]map[string]any{}
	for _, fact := range proof.Catalyst[0].Facts {
		byTitle[scoreText(fact["title"])] = fact
	}
	if byTitle["诉讼公告"]["relation"] != "fresh_available" || byTitle["大订单旧公告"]["relation"] != "old_background" || byTitle["大订单旧公告"]["eventTime"] != "2026-08-27" {
		t.Fatalf("source-level freshness leaked to old event: %+v", byTitle)
	}
	for _, sample := range []struct{ question, reply, want string }{
		{"2026-08-27 10:00:00", "2026-09-08 09:30:00", "fresh_available"},
		{"2026-09-08 09:30:00", "2026-08-27 10:00:00", "old_background"},
		{"2026-09-08 09:30:00", "", "time_unverified"},
	} {
		payload, _ := json.Marshal(map[string]any{"results": []any{map[string]any{"stockCode": "600343", "mainContent": "投资者问题", "attachedContent": "公司答复", "pubDate": sample.question, "attachedPubDate": sample.reply}}})
		doc := scoreAuditDocument("互动易 sh600343", "stock", string(payload), t0)
		proof := BuildCandidateScoreEvidence("sh600343", []researchevidence.SourceDocument{doc}, t0, t0, since)
		if proof.CatalystState != sample.want {
			t.Fatalf("question was treated as company reply: %+v", proof)
		}
	}
}

func TestResearch2ScoreTypedSectorFlowMatchesOutsideSummaryPreview(t *testing.T) {
	t0 := time.Date(2026, 9, 8, 10, 0, 2, 0, shanghai())
	docs := []researchevidence.SourceDocument{scoreAuditDocument("概念 sh600343", "stock", `{"result":{"data":[{"SECUCODE":"600343.SH","BOARD_NAME":"军工","NEW_BOARD_CODE":"BK0490"}]}}`, t0)}
	rows := make([]map[string]any, 0, 10)
	for index := 0; index < 9; index++ {
		rows = append(rows, map[string]any{"code": "other", "name": "other", "netAmount": index})
	}
	rows = append(rows, map[string]any{"code": "BK0490", "name": "军工", "netAmount": 12345.6, "changePct": 1.2})
	payload, _ := json.Marshal(map[string]any{"data": rows, "status": "ok"})
	docs = append(docs, scoreAuditDocument("typed-sector", "sector", string(payload), t0))
	proof := BuildCandidateScoreEvidence("sh600343", docs, t0, t0, time.Time{})
	if proof.SectorState != "available" || len(proof.Sector) != 2 || proof.Sector[1].Facts[0]["netAmount"] != 12345.6 {
		t.Fatalf("typed flow or later rows lost: %+v", proof)
	}
}

func TestResearch2ScoreSavedSeptemberSamplesRemainUnchanged(t *testing.T) {
	// Persisted recommendation components, September 4, 7 and 8. These are
	// selected samples, not a claim about the unobserved candidate population.
	for _, sample := range []struct {
		day, code         string
		stock, risk, want float64
	}{
		{"2026-09-08", "sh600343", 37, 3, 52}, {"2026-09-08", "sh600722", 37, 4, 51},
		{"2026-09-08", "sh605177", 36, 3, 51}, {"2026-09-08", "sh605300", 36, 3, 51},
		{"2026-09-08", "sz000560", 36, 3, 51}, {"2026-09-08", "sz002909", 38, 5, 51},
		{"2026-09-08", "sh600391", 39, 4, 53}, {"2026-09-08", "sh601595", 37, 4, 51},
		{"2026-09-07", "sh600103", 38, 5, 51}, {"2026-09-07", "sh601118", 37, 4, 51},
		{"2026-09-07", "sz000833", 37, 4, 51}, {"2026-09-04", "sh603613", 37, 2, 53},
		{"2026-09-04", "sh600857", 36, 3, 51}, {"2026-09-04", "sz002124", 35, 2, 51},
	} {
		t.Run(sample.day+sample.code, func(t *testing.T) {
			at, _ := time.ParseInLocation("2006-01-02", sample.day, shanghai())
			value := modelRecommendation{Code: sample.code, MarketScore: 18, StockScore: sample.stock, RiskDeduction: sample.risk, FinalScore: sample.want, ReferencePrice: 10}
			items, warnings := validateRecommendations("fixture", at, at, nil, []modelRecommendation{value})
			if len(items) != 1 || items[0].FinalScore != sample.want || len(warnings) > 0 {
				t.Fatalf("historical component formula changed: %+v %v", items, warnings)
			}
		})
	}
}

func TestResearch2ScoreSourceValidationKeepsThresholdAndExplainsZero(t *testing.T) {
	t0 := time.Date(2026, 9, 8, 10, 0, 2, 0, shanghai())
	freeze := t0.Add(20 * time.Second)
	since := t0.Add(-19 * time.Hour)
	docs := []researchevidence.SourceDocument{
		scoreAuditDocument("market", "market", `{"advances":4000}`, t0),
		scoreAuditDocument("stock-sh600343", "stock", `{"entityId":"stock:sh600343","price":19.61}`, t0),
		scoreAuditDocument("概念 sh600343", "stock", `{"result":{"data":[{"SECURITY_CODE":"600343","BOARD_NAME":"军工","NEW_BOARD_CODE":"BK0490","BOARD_YIELD":1.5}]}}`, t0.Add(12*time.Second)),
		scoreAuditDocument("公告 sh600343", "stock", `[{"notice_date":"2026-08-29","title":"旧报告"}]`, t0),
	}
	candidates := []researchevidence.StockCandidate{{Code: "sh600343", Name: "航天动力"}}
	value := modelRecommendation{Code: "sh600343", MarketScore: 18, StockScore: 37, RiskDeduction: 3, FinalScore: 52, ReferencePrice: 19.61, SourceRefs: []string{"market", "stock-sh600343", "概念 sh600343", "公告 sh600343"}, ScoreReasons: map[string]string{"sector": "板块资料可用，本轮未奖励该项"}}
	window := scoreEvidenceWindow{t0, since}
	items, warnings := validateRecommendationsWithEvidence("run", t0, t0, freeze, candidates, docs, nil, []modelRecommendation{value}, window)
	if len(items) != 1 || items[0].FinalScore != 52 || len(warnings) != 0 {
		t.Fatalf("saved scoring baseline changed: items=%+v warnings=%v", items, warnings)
	}
	lines := strings.Join(scoreReportLines(items[0], value, Evidence{CutoffAt: t0, FreezeAt: freeze, Documents: docs, Candidates: candidates, CatalystWindowStartAt: since}), "\n")
	for _, text := range []string{"分项评分依据", "0分不能直接解释成接口失败", "只有旧背景", "概念 sh600343"} {
		if !strings.Contains(lines, text) {
			t.Fatalf("missing explanation %q: %s", text, lines)
		}
	}
	value.CatalystScore = 1
	if got := validateRecommendationScoreEvidence(value.Code, value, docs, freeze, candidates, window); len(got) == 0 {
		t.Fatal("old notice awarded new catalyst credit")
	}
	value.CatalystScore = 0
	value.StockScore = 35
	value.FinalScore = 50
	if items, _ = validateRecommendationsWithEvidence("run", t0, t0, freeze, candidates, docs, nil, []modelRecommendation{value}, window); len(items) != 0 {
		t.Fatal("50-point threshold changed")
	}
}
