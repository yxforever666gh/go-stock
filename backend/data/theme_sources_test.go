package data

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go-stock/backend/marketdata"
	"go-stock/backend/models"
)

func TestThemeSourceAggregatorIsolatesTimeoutAndSourceError(t *testing.T) {
	observedAt := time.Date(2026, 8, 28, 9, 30, 0, 0, time.FixedZone("CST", 8*60*60))
	slowFinished := make(chan struct{})
	slow := SourceAdapterFunc{
		SourceName: "ignores-context",
		CollectFunc: func(context.Context, time.Time) ([]RawThemeSignal, error) {
			time.Sleep(120 * time.Millisecond)
			close(slowFinished)
			return []RawThemeSignal{{ThemeName: "迟到题材", Title: "迟到信号"}}, nil
		},
	}
	failed := SourceAdapterFunc{
		SourceName: "failed-source",
		CollectFunc: func(context.Context, time.Time) ([]RawThemeSignal, error) {
			return nil, errors.New("fixture unavailable")
		},
	}
	fast := SourceAdapterFunc{
		SourceName: "fast-source",
		CollectFunc: func(context.Context, time.Time) ([]RawThemeSignal, error) {
			return []RawThemeSignal{{ThemeName: "算力", Kind: ThemeSignalNews, EventType: ThemeSignalNews, Title: "订单落地"}}, nil
		},
	}

	started := time.Now()
	batch := NewThemeSourceAggregator(20*time.Millisecond, slow, failed, fast).Collect(context.Background(), observedAt)
	if elapsed := time.Since(started); elapsed >= 100*time.Millisecond {
		t.Fatalf("one source blocked the batch for %s", elapsed)
	}
	if batch.Status != marketdata.StatusPartial || len(batch.Signals) != 1 {
		t.Fatalf("unexpected degraded batch: status=%s signals=%+v", batch.Status, batch.Signals)
	}
	if got := batch.Signals[0].SourceName; got != "fast-source" {
		t.Fatalf("source provenance lost: %q", got)
	}
	wantCodes := map[string]bool{"source_timeout": false, "source_error": false}
	for _, item := range batch.Errors {
		if _, ok := wantCodes[item.Code]; ok {
			wantCodes[item.Code] = true
		}
	}
	for code, found := range wantCodes {
		if !found {
			t.Fatalf("missing isolated error %q in %+v", code, batch.Errors)
		}
	}
	select {
	case <-slowFinished:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("legacy source fixture never finished")
	}
}

func TestThemeSourceAggregatorDegradesEmptyAndDuplicateSources(t *testing.T) {
	observedAt := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	empty := SourceAdapterFunc{SourceName: "empty", CollectFunc: func(context.Context, time.Time) ([]RawThemeSignal, error) {
		return []RawThemeSignal{}, nil
	}}
	duplicate := RawThemeSignal{
		ThemeName: "低空经济", Kind: ThemeSignalHotTopic, EventType: ThemeSignalHotTopic,
		Title: "低空经济升温", Summary: "产业政策发布", SourceName: "东方财富热门话题",
		SourceRef: "https://example.test/topic/1", Stance: ThemeSignalSupports,
	}
	repeated := SourceAdapterFunc{SourceName: "duplicates", CollectFunc: func(context.Context, time.Time) ([]RawThemeSignal, error) {
		return []RawThemeSignal{duplicate, duplicate}, nil
	}}

	batch := NewThemeSourceAggregator(time.Second, empty, repeated).Collect(context.Background(), observedAt)
	if batch.Status != marketdata.StatusPartial {
		t.Fatalf("empty/duplicate sources must degrade a usable batch, got %s", batch.Status)
	}
	if len(batch.Signals) != 1 {
		t.Fatalf("expected one exact signal after dedupe, got %+v", batch.Signals)
	}
	if batch.Sources[0].Status != marketdata.StatusEmpty {
		t.Fatalf("empty source status=%s", batch.Sources[0].Status)
	}
	if batch.Sources[1].Status != marketdata.StatusPartial || batch.Sources[1].DuplicateCount != 1 {
		t.Fatalf("duplicate degradation missing: %+v", batch.Sources[1])
	}
}

func TestThemeSourceAggregatorKeepsConflictingClaimsSideBySide(t *testing.T) {
	observedAt := time.Date(2026, 8, 28, 10, 5, 0, 0, time.UTC)
	eventAt := observedAt.Add(-time.Hour)
	base := RawThemeSignal{
		ThemeName: "机器人", Kind: ThemeSignalNews, EventType: "policy", Title: "行业政策调整",
		Summary: "同一事件存在方向分歧", EventAt: eventAt, SourceName: "来源甲",
		SourceRef: "https://example.test/policy", Stance: ThemeSignalSupports,
	}
	contradicts := base
	contradicts.SourceName = "来源乙"
	contradicts.Stance = ThemeSignalContradicts
	adapter := SourceAdapterFunc{SourceName: "conflict-fixture", CollectFunc: func(context.Context, time.Time) ([]RawThemeSignal, error) {
		return []RawThemeSignal{base, contradicts}, nil
	}}

	batch := NewThemeSourceAggregator(time.Second, adapter).Collect(context.Background(), observedAt)
	if batch.Status != marketdata.StatusOK || len(batch.Signals) != 2 {
		t.Fatalf("conflicting claims must not be deduplicated: %+v", batch)
	}
	stances := map[string]string{}
	for _, signal := range batch.Signals {
		stances[signal.SourceName] = signal.Stance
	}
	if stances["来源甲"] != ThemeSignalSupports || stances["来源乙"] != ThemeSignalContradicts {
		t.Fatalf("conflict provenance/stance lost: %+v", stances)
	}
}

func TestThemeSourceAvailableAtUsesLaterReliableTime(t *testing.T) {
	observedAt := time.Date(2026, 8, 28, 9, 40, 0, 0, time.UTC)
	before := observedAt.Add(-10 * time.Minute)
	after := observedAt.Add(5 * time.Minute)
	adapter := SourceAdapterFunc{SourceName: "availability", CollectFunc: func(context.Context, time.Time) ([]RawThemeSignal, error) {
		return []RawThemeSignal{
			{ThemeName: "A", Title: "published before observation", PublishedAt: &before},
			{ThemeName: "B", Title: "published after observation", PublishedAt: &after},
			{ThemeName: "C", Title: "no reliable publication"},
		}, nil
	}}

	batch := NewThemeSourceAggregator(time.Second, adapter).Collect(context.Background(), observedAt)
	byTheme := make(map[string]RawThemeSignal)
	for _, signal := range batch.Signals {
		byTheme[signal.ThemeName] = signal
	}
	if !byTheme["A"].AvailableAt.Equal(observedAt) {
		t.Fatalf("old publication must not predate first observation: %s", byTheme["A"].AvailableAt)
	}
	if !byTheme["B"].AvailableAt.Equal(after) {
		t.Fatalf("later reliable publication must win: %s", byTheme["B"].AvailableAt)
	}
	if !byTheme["C"].AvailableAt.Equal(observedAt) {
		t.Fatalf("unknown publication must use first observation: %s", byTheme["C"].AvailableAt)
	}
}

func TestExistingThemeSourceConvertersPreserveShapeAndProvenance(t *testing.T) {
	observedAt := time.Date(2026, 8, 28, 11, 0, 0, 0, time.UTC)
	publishedAt := observedAt.Add(-time.Minute)

	hotTopics := AdaptHotTopics([]any{map[string]any{
		"TopicName": "商业航天", "TopicDesc": "发射计划", "hotValue": 88,
	}}, observedAt)
	if len(hotTopics) != 1 || hotTopics[0].Kind != ThemeSignalHotTopic || hotTopics[0].SourceName != "东方财富热门话题" {
		t.Fatalf("hot topic conversion failed: %+v", hotTopics)
	}

	hotEvents := AdaptXueqiuHotEvents([]models.HotEvent{{Tag: "固态电池", Content: "行业进展", Hot: 99}}, observedAt)
	if len(hotEvents) != 1 || hotEvents[0].SourceName != "雪球热点事件" || strings.Contains(hotEvents[0].SourceName, "东方财富") {
		t.Fatalf("Xueqiu event was mislabelled: %+v", hotEvents)
	}

	telegraphs := AdaptTelegraphs([]*models.Telegraph{{
		DataTime: &publishedAt, Title: "订单公告", Content: "订单确认", SubjectTags: []string{"算力"},
		StocksTags: []string{"sh600000"}, Source: "财联社电报", Url: "https://example.test/news/1",
	}}, observedAt)
	if len(telegraphs) != 1 || telegraphs[0].ThemeName != "算力" || telegraphs[0].SourceName != "财联社电报" || telegraphs[0].Securities[0].Market != "SH" {
		t.Fatalf("telegraph conversion failed: %+v", telegraphs)
	}

	announcements := AdaptAnnouncements([]any{map[string]any{
		"title": "重大合同公告", "themeName": "电网设备", "notice_date": publishedAt.Format(time.RFC3339),
		"columns": []any{map[string]any{"stock_code": "002001", "short_name": "测试公司"}},
	}}, observedAt)
	if len(announcements) != 1 || announcements[0].Kind != ThemeSignalAnnouncement || announcements[0].Securities[0].Code != "002001" || announcements[0].SourceCredibilityScore != 90 {
		t.Fatalf("announcement conversion failed: %+v", announcements)
	}

	concepts := AdaptConceptInfo([]models.StockConceptInfo{{
		SECURITYCODE: "600000", SECURITYNAMEABBR: "浦发银行", NEWBOARDCODE: "BK001",
		BOARDNAME: "金融科技", SELECTEDBOARDREASON: "入选理由", BOARDRANK: 2,
	}}, observedAt)
	if len(concepts) != 1 || concepts[0].ThemeName != "金融科技" || concepts[0].Rank != 2 {
		t.Fatalf("concept conversion failed: %+v", concepts)
	}

	flows := AdaptConceptFundFlows(ConceptFundFlowSnapshot{
		Rows: []FundFlowRow{{Code: "BK002", Name: "机器人", NetAmount: 123}}, SourceName: "sina",
		SourceRef: "https://example.test/fund-flow", AsOf: publishedAt,
	}, observedAt)
	if len(flows) != 1 || flows[0].Kind != ThemeSignalFundFlow || flows[0].SourceName != "新浪概念资金流" {
		t.Fatalf("fund-flow provenance failed: %+v", flows)
	}
}

func TestThemeSourceEmptyOnlyBatch(t *testing.T) {
	adapter := SourceAdapterFunc{SourceName: "empty", CollectFunc: func(context.Context, time.Time) ([]RawThemeSignal, error) {
		return nil, nil
	}}
	batch := NewThemeSourceAggregator(time.Second, adapter).Collect(context.Background(), time.Now())
	if batch.Status != marketdata.StatusEmpty || len(batch.Errors) != 0 {
		t.Fatalf("empty source is not an error: %+v", batch)
	}
}
