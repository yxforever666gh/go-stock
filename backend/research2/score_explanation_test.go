package research2

import (
	"context"
	"encoding/json"
	"fmt"
	"go-stock/backend/researchaudit"
	"go-stock/internal/researchevidence"
	"strings"
	"testing"
	"time"
)

func TestResearch2ScorePromptSeparatesSnapshotAndFreeze(t *testing.T) {
	t0 := time.Date(2026, 9, 8, 10, 0, 2, 0, shanghai())
	freeze := time.Date(2026, 9, 8, 10, 0, 21, 455695000, shanghai())
	prompt := buildPrompt(prepareEvidence(Evidence{CutoffAt: t0, FreezeAt: freeze, Prompt: `{}`}, time.Time{}), t0)
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

func scoreFixtureRefs(code string) []string {
	return []string{"market", "quote-" + code, "概念 " + code, "公告 " + code}
}

// Tests that exercise execution or score arithmetic still supply the same
// minimum provenance that a real collector must expose.
func scoreFixtureEvidence(at time.Time, candidates ...researchevidence.StockCandidate) Evidence {
	evidence := Evidence{Prompt: `{}`, CutoffAt: at, SourceStatusJSON: `[]`, Candidates: candidates,
		Documents: []researchevidence.SourceDocument{scoreAuditDocument("market", "market", `{"advances":4000}`, at)}}
	for _, candidate := range candidates {
		code := candidate.Code
		evidence.Documents = append(evidence.Documents,
			scoreAuditDocument("quote-"+code, "quote", fmt.Sprintf(`{"entityId":"stock:%s","price":10}`, code), at),
			scoreAuditDocument("概念 "+code, "stock", fmt.Sprintf(`{"BOARD_NAME":"fixture","SECURITY_CODE":"%s","BOARD_YIELD":1.5}`, code), at),
			scoreAuditDocument("公告 "+code, "stock", fmt.Sprintf(`{"title":"新订单","publishedAt":%q}`, at.Add(-time.Minute).Format(time.RFC3339)), at))
	}
	return evidence
}

func TestRunnerPreparedScoresSurviveRepairAndKnowledgeWithoutChangingFrozenEvidence(t *testing.T) {
	at := time.Date(2026, 9, 8, 10, 0, 0, 0, shanghai())
	since := time.Date(2026, 9, 7, 15, 0, 0, 0, shanghai())
	for _, knowledgeText := range []string{"", "知识库引用 [kb:example]，只作背景"} {
		t.Run(fmt.Sprintf("knowledge=%t", knowledgeText != ""), func(t *testing.T) {
			repository := research2TestRepository(t)
			if err := repository.DB().AutoMigrate(&researchaudit.PromptVersion{}, &researchaudit.Payload{}, &researchaudit.RunState{}); err != nil {
				t.Fatal(err)
			}
			evidence := scoreFixtureEvidence(at, researchevidence.StockCandidate{Code: "sh600343"})
			evidence.Candidates = append(evidence.Candidates, researchevidence.StockCandidate{Code: "600343"})
			evidence.Prompt = `{"candidates":[{"code":"sh600343"}],"frozen":"keep exact bytes"}`
			originalDocuments, _ := json.Marshal(evidence.Documents)
			ai := &sequenceAI{responses: []string{"invalid JSON", `{"tradingDay":true,"conclusion":"推荐","recommendations":[{"code":"sh600343","marketScore":15,"sectorScore":15,"stockScore":25,"catalystScore":5,"finalScore":60,"referencePrice":10,"sourceRefs":["market","quote-sh600343","概念 sh600343","公告 sh600343"]}]}`}}
			audit := researchaudit.NewRecorder(researchaudit.NewRepository(repository.DB()))
			runner := NewRunner(repository, ai, fixedEvidence{value: evidence}, testCalendar{})
			runner.ConfigureKnowledge(&fixtureKnowledgeRetriever{prompt: knowledgeText})
			runner.ConfigureAudit(audit)
			runner.ConfigureReplayClock(func() time.Time { return at }, nil)
			run, err := runner.Run(context.Background(), at)
			if err != nil || run.RecommendationCount != 1 || ai.calls != 2 {
				t.Fatalf("run=%+v calls=%d err=%v", run, ai.calls, err)
			}
			view, err := audit.Audit(context.Background(), researchaudit.OwnerResearch2, run.RunID)
			if err != nil || len(view.Payloads) != 2 {
				t.Fatalf("audit=%+v err=%v", view, err)
			}
			if view.Payloads[0].EvidenceSHA256 != view.Payloads[1].EvidenceSHA256 {
				t.Fatal("repair changed the scoring basis")
			}
			for index, payload := range view.Payloads {
				var recorded struct {
					ScoreEvidence         map[string]CandidateScoreEvidence `json:"scoreEvidence"`
					CatalystWindowStartAt time.Time                         `json:"catalystWindowStartAt"`
					Documents             []researchevidence.SourceDocument `json:"documents"`
				}
				if err := json.Unmarshal([]byte(payload.Evidence), &recorded); err != nil {
					t.Fatal(err)
				}
				if len(recorded.ScoreEvidence) != 1 || recorded.ScoreEvidence["sh600343"].CatalystState != "fresh_available" || !recorded.CatalystWindowStartAt.Equal(since) {
					t.Fatalf("incorrect or duplicate prepared scores: %+v", recorded)
				}
				scores, _ := json.Marshal(recorded.ScoreEvidence)
				if !strings.Contains(ai.requests[index].Prompt, evidence.Prompt) || !strings.Contains(ai.requests[index].Prompt, string(scores)) || !strings.Contains(ai.requests[index].Prompt, "# 知识库参考（不是市场评分来源）\n"+knowledgeText) {
					t.Fatal("model input differs from audited scores or corrupted the original snapshot")
				}
				recordedDocuments, _ := json.Marshal(recorded.Documents)
				if string(recordedDocuments) != string(originalDocuments) {
					t.Fatal("auditing rewrote frozen source documents")
				}
			}
			if !strings.Contains(run.ReportMarkdown, "存在带可核验时间的新催化材料") || !strings.Contains(run.ReportMarkdown, "公告 sh600343") {
				t.Fatal("report did not use the prepared catalyst evidence")
			}
			after, _ := json.Marshal(evidence.Documents)
			if string(after) != string(originalDocuments) {
				t.Fatal("run mutated the collector's frozen documents")
			}
		})
	}
}

func TestRecommendationsCannotBypassEvidenceWithNilDocuments(t *testing.T) {
	at := time.Date(2026, 9, 8, 10, 0, 0, 0, shanghai())
	evidence := prepareEvidence(Evidence{CutoffAt: at, Candidates: []researchevidence.StockCandidate{{Code: "sh600343"}}}, time.Time{})
	value := modelRecommendation{Code: "sh600343", MarketScore: 20, StockScore: 40, FinalScore: 60, ReferencePrice: 10, SourceRefs: []string{"missing"}}
	items, warnings := validateRecommendations("run", at, evidence, []modelRecommendation{value})
	if len(items) != 0 || len(warnings) == 0 || len(validateModelSourceRefs([]modelRecommendation{value}, evidence)) == 0 {
		t.Fatalf("nil documents bypassed production validation: %+v %v", items, warnings)
	}
}

func TestResearch2ScoreEvidenceBindsExactBoardsAndThemeConstituents(t *testing.T) {
	t0 := time.Date(2026, 9, 8, 10, 0, 2, 0, shanghai())
	freeze := t0.Add(20 * time.Second)
	docs := []researchevidence.SourceDocument{
		scoreAuditDocument("概念 sh600343", "stock", `{"message":"ok","result":{"data":[{"SECURITY_CODE":"600343","BOARD_NAME":"军工","NEW_BOARD_CODE":"BK0490"}]}}`, t0.Add(12*time.Second)),
		scoreAuditDocument("sectors", "sector", `{"data":[{"bd_code":"BK0490","bd_name":"军工","bd_zdf":"1.5"},{"bd_name":"军工装备","bd_zdf":"8.2"}]}`, t0.Add(13*time.Second)),
		scoreAuditDocument("theme-army", "theme", `{"themeId":"army","snapshot":{"heatScore":90},"stockConstituents":[{"assetType":"stock","code":"600343","market":"SH"}]}`, t0),
	}
	proof := buildCandidateScoreEvidence("sh600343", docs, t0, freeze, t0.Add(-19*time.Hour))
	if proof.SectorState != "available" || len(proof.Sector) != 3 {
		t.Fatalf("valid auxiliary evidence omitted: %+v", proof)
	}
	encoded, _ := json.Marshal(proof)
	if !strings.Contains(string(encoded), `"bd_zdf":"1.5"`) || strings.Contains(string(encoded), `"8.2"`) {
		t.Fatalf("inexact board match: %s", encoded)
	}
	other := buildCandidateScoreEvidence("sh600391", docs, t0, freeze, t0.Add(-19*time.Hour))
	if len(other.Sector) != 0 || other.SectorState != "membership_unverified" {
		t.Fatalf("unproved stock membership leaked: %+v", other)
	}
	docs[1].AvailableAt = &[]time.Time{freeze.Add(time.Second)}[0]
	docs = docs[:2]
	proof = buildCandidateScoreEvidence("sh600343", docs, t0, freeze, time.Time{})
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
			proof := buildCandidateScoreEvidence("sh600343", []researchevidence.SourceDocument{doc}, t0, freeze, since)
			if proof.CatalystState != sample.want {
				t.Fatalf("proof=%+v", proof)
			}
		})
	}
	theme := scoreAuditDocument("theme", "theme", `{"themeId":"army","stockConstituents":[{"assetType":"stock","code":"600343"}]}`, t0)
	catalyst := scoreAuditDocument("theme-event", "catalyst", `{"themeId":"army","event":{"eventAt":"2026-08-27T10:00:00+08:00","title":"旧事件"},"claim":{"publishedAt":"2026-09-08T09:30:00+08:00"}}`, t0)
	proof := buildCandidateScoreEvidence("sh600343", []researchevidence.SourceDocument{theme, catalyst}, t0, freeze, since)
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
	proof := buildCandidateScoreEvidence("sh600343", []researchevidence.SourceDocument{doc}, t0, t0, since)
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
	for _, sample := range []struct {
		question string
		reply    any
		want     string
	}{
		{"2026-08-27 10:00:00", "2026-09-08 09:30:00", "fresh_available"},
		{"2026-09-08 09:30:00", "2026-08-27 10:00:00", "old_background"},
		{"2026-09-08 09:30:00", "", "time_unverified"},
		{"2026-09-08 09:30:00", "1781080143000", "old_background"},
		{"2026-09-08 09:30:00", int64(1781080143000), "old_background"},
		{"2026-09-08 09:30:00", int64(1781080143), "old_background"},
		{"2026-09-08 09:30:00", "1781080143", "old_background"},
		{"2026-09-08 09:30:00", since.UnixMilli(), "fresh_available"},
		{"2026-09-08 09:30:00", fmt.Sprint(since.Unix()), "fresh_available"},
		{"2026-09-08 09:30:00", t0.Unix(), "fresh_available"},
		{"2026-09-08 09:30:00", fmt.Sprint(t0.UnixMilli()), "fresh_available"},
		{"2026-09-08 09:30:00", t0.Add(time.Second).Unix(), "after_snapshot"},
		{"2026-09-08 09:30:00", fmt.Sprint(t0.Add(time.Millisecond).UnixMilli()), "after_snapshot"},
		{"2026-09-08 09:30:00", nil, "time_unverified"},
		{"2026-09-08 09:30:00", "invalid", "time_unverified"},
		{"2026-09-08 09:30:00", -1781080143000, "time_unverified"},
		{"2026-09-08 09:30:00", 1781080143.5, "time_unverified"},
		{"2026-09-08 09:30:00", "17810801430", "time_unverified"},
		{"2026-09-08 09:30:00", "178108014300", "time_unverified"},
	} {
		payload, _ := json.Marshal(map[string]any{"results": []any{map[string]any{"stockCode": "600343", "mainContent": "投资者问题", "attachedContent": "公司答复", "pubDate": sample.question, "attachedPubDate": sample.reply}}})
		doc := scoreAuditDocument("互动易 sh600343", "stock", string(payload), t0)
		proof := buildCandidateScoreEvidence("sh600343", []researchevidence.SourceDocument{doc}, t0, t0, since)
		if proof.CatalystState != sample.want {
			t.Fatalf("question was treated as company reply: %+v", proof)
		}
	}
}

func TestResearch2ScoreAvailableSubEvidenceStillSupportsPositiveScore(t *testing.T) {
	at := time.Date(2026, 9, 9, 10, 0, 0, 0, shanghai())
	evidence := prepareEvidence(scoreFixtureEvidence(at, researchevidence.StockCandidate{Code: "sh600343"}), at.Add(-19*time.Hour))
	value := modelRecommendation{Code: "sh600343", MarketScore: 10, SectorScore: 10, StockScore: 35, FinalScore: 55, ReferencePrice: 10, SourceRefs: scoreFixtureRefs("sh600343")}
	// The fixture has breadth and a verified board yield, but no historical
	// baseline, failed-limit-up ratio, or within-board breadth/ranking.
	items, warnings := validateRecommendations("fixture", at, evidence, []modelRecommendation{value})
	if len(items) != 1 || items[0].FinalScore != 55 || len(warnings) != 0 {
		t.Fatalf("available evidence rejected: %v %v", items, warnings)
	}
	for _, sample := range []struct {
		name string
		edit func(*researchevidence.SourceDocument)
	}{
		{"membership_only", func(d *researchevidence.SourceDocument) {
			d.Content = `{"BOARD_NAME":"fixture","SECURITY_CODE":"sh600343"}`
		}},
		{"other_stock", func(d *researchevidence.SourceDocument) {
			d.SourceID, d.SourceName = "概念 sh600000", "概念 sh600000"
			d.Content = `{"BOARD_NAME":"fixture","SECURITY_CODE":"sh600000","BOARD_YIELD":1.5}`
		}},
		{"failed", func(d *researchevidence.SourceDocument) { d.Error = "source failure" }},
		{"stale", func(d *researchevidence.SourceDocument) {
			d.Content = `{"status":"stale","BOARD_NAME":"fixture","SECURITY_CODE":"sh600343","BOARD_YIELD":1.5}`
		}},
		{"after_freeze", func(d *researchevidence.SourceDocument) { future := at.Add(time.Second); d.AvailableAt = &future }},
	} {
		t.Run(sample.name, func(t *testing.T) {
			raw := scoreFixtureEvidence(at, researchevidence.StockCandidate{Code: "sh600343"})
			sample.edit(&raw.Documents[2])
			proof := prepareEvidence(raw, at.Add(-19*time.Hour))
			candidate := value
			candidate.SourceRefs = append([]string(nil), value.SourceRefs...)
			candidate.SourceRefs[2] = raw.Documents[2].SourceID
			items, warnings := validateRecommendations("fixture", at, proof, []modelRecommendation{candidate})
			if sample.name == "membership_only" {
				if len(items) != 1 || len(warnings) != 0 {
					t.Fatalf("sector-specific gate remains: %v", warnings)
				}
			} else if len(items) != 0 || len(warnings) == 0 {
				t.Fatal("invalid general source reference accepted")
			}
		})
	}
}

func TestSectorScoreWithoutSpecialProofKeepsOtherGuards(t *testing.T) {
	at := time.Date(2026, 9, 9, 12, 0, 0, 0, shanghai())
	evidence := prepareEvidence(scoreFixtureEvidence(at, researchevidence.StockCandidate{Code: "sh600343"}), at.Add(-19*time.Hour))
	base := modelRecommendation{Code: "sh600343", MarketScore: 10, SectorScore: 25, StockScore: 25, FinalScore: 60, ReferencePrice: 10, SourceRefs: []string{"market", "quote-sh600343"}}
	for _, sample := range []struct {
		name string
		edit func(*modelRecommendation)
		want bool
	}{
		{"no sector citation", func(v *modelRecommendation) {}, true},
		{"recalculate", func(v *modelRecommendation) { v.FinalScore = 49 }, true},
		{"sector above 30", func(v *modelRecommendation) { v.SectorScore = 31 }, false},
		{"sector negative", func(v *modelRecommendation) { v.SectorScore = -1 }, false},
		{"exactly 50", func(v *modelRecommendation) { v.SectorScore = 15; v.FinalScore = 80 }, false},
		{"invalid reference", func(v *modelRecommendation) { v.SourceRefs = []string{"missing"} }, false},
		{"market unsupported", func(v *modelRecommendation) { v.SourceRefs = []string{"quote-sh600343"} }, false},
		{"stock unsupported", func(v *modelRecommendation) { v.SourceRefs = []string{"market"} }, false},
		{"catalyst unsupported", func(v *modelRecommendation) { v.CatalystScore = 1 }, false},
	} {
		t.Run(sample.name, func(t *testing.T) {
			v := base
			sample.edit(&v)
			items, _ := validateRecommendations("fixture", at, evidence, []modelRecommendation{v})
			if (len(items) == 1) != sample.want {
				t.Fatalf("items=%+v", items)
			}
			if sample.want && items[0].FinalScore != 60 {
				t.Fatalf("sum not recalculated: %v", items[0].FinalScore)
			}
		})
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
	proof := buildCandidateScoreEvidence("sh600343", docs, t0, t0, time.Time{})
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
			value.SourceRefs = scoreFixtureRefs(sample.code)
			evidence := prepareEvidence(scoreFixtureEvidence(at, researchevidence.StockCandidate{Code: sample.code}), time.Time{})
			items, warnings := validateRecommendations("fixture", at, evidence, []modelRecommendation{value})
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
	evidence := prepareEvidence(Evidence{CutoffAt: t0, FreezeAt: freeze, Candidates: candidates, Documents: docs}, since)
	items, warnings := validateRecommendations("run", t0, evidence, []modelRecommendation{value})
	if len(items) != 1 || items[0].FinalScore != 52 || len(warnings) != 0 {
		t.Fatalf("saved scoring baseline changed: items=%+v warnings=%v", items, warnings)
	}
	lines := strings.Join(scoreReportLines(items[0], value, evidence), "\n")
	for _, text := range []string{"分项评分依据", "0分不能直接解释成接口失败", "只有旧背景", "概念 sh600343"} {
		if !strings.Contains(lines, text) {
			t.Fatalf("missing explanation %q: %s", text, lines)
		}
	}
	value.CatalystScore = 1
	if got := validateRecommendationScoreEvidence(value.Code, value, evidence); len(got) == 0 {
		t.Fatal("old notice awarded new catalyst credit")
	}
	value.CatalystScore = 0
	value.StockScore = 35
	value.FinalScore = 50
	if items, _ = validateRecommendations("run", t0, evidence, []modelRecommendation{value}); len(items) != 0 {
		t.Fatal("50-point threshold changed")
	}
}
