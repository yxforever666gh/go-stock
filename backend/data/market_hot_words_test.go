package data

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/marketdata"
	"go-stock/backend/models"
)

func TestHotWordsWindowUsesEventTimeAndLegacyCreatedAtFallback(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "hot-words-window.db"))
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Dao.AutoMigrate(&models.Telegraph{}, &models.TelegraphTags{}, &models.Tags{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	now := time.Date(2026, 8, 29, 12, 0, 0, 0, cnLocation())
	current := now.Add(-time.Hour)
	stale := now.Add(-48 * time.Hour)
	zero := time.Time{}
	rows := []models.Telegraph{
		{DataTime: &current, Content: "current event", Source: "source-a"},
		{DataTime: &stale, Content: "old event imported now", Source: "source-a"},
		{DataTime: nil, Content: "legacy nil event", Source: "source-b"},
		{DataTime: &zero, Content: "legacy zero event", Source: "source-b"},
	}
	rows[0].CreatedAt = now.Add(-72 * time.Hour)
	rows[1].CreatedAt = now.Add(-time.Minute)
	rows[2].CreatedAt = now.Add(-30 * time.Minute)
	rows[3].CreatedAt = now.Add(-20 * time.Minute)
	for index := range rows {
		if err := db.Dao.Create(&rows[index]).Error; err != nil {
			t.Fatalf("seed row %d: %v", index, err)
		}
	}

	service := NewMarketHotWordsServiceWithDB(db.Dao, func() time.Time { return now })
	loaded, truncated, err := service.loadWindow(context.Background(), now.Add(-24*time.Hour), now, true)
	if err != nil || truncated {
		t.Fatalf("loadWindow error=%v truncated=%v", err, truncated)
	}
	if len(loaded) != 3 {
		t.Fatalf("loaded %d rows, want 3", len(loaded))
	}
	for _, item := range loaded {
		if item.Content == "old event imported now" {
			t.Fatal("old event time must not be replaced by fresh ingestion time")
		}
	}
	empty := service.compute(context.Background(), HotWordsQuery{}.Normalize(), now.Add(30*24*time.Hour))
	if empty.Status != marketdata.StatusEmpty || empty.Data.CurrentDocumentCount != 0 || len(empty.Data.Items) != 0 {
		t.Fatalf("empty window result = %#v", empty)
	}
}

func TestDedupeHotWordNewsExactAndNearDuplicates(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, cnLocation())
	base := strings.Repeat("人工智能算力产业快速增长", 12)
	near := strings.TrimSuffix(base, "长") + "涨"
	rows := []*models.Telegraph{
		hotWordTelegraph(1, base, "source-a", now),
		hotWordTelegraph(2, " 人工智能，算力产业快速增长 "+strings.Repeat("人工智能算力产业快速增长", 11), "source-b", now.Add(time.Minute)),
		hotWordTelegraph(3, near, "source-c", now.Add(2*time.Minute)),
		hotWordTelegraph(4, "黄金价格显著下跌", "source-a", now.Add(3*time.Minute)),
	}
	documents := dedupeHotWordNews(rows)
	if len(documents) != 2 {
		t.Fatalf("deduplicated documents = %d, want 2", len(documents))
	}
	var merged *hotWordDocument
	for index := range documents {
		if strings.Contains(documents[index].normalized, "人工智能") {
			merged = &documents[index]
			break
		}
	}
	if merged == nil || len(merged.sources) != 3 {
		t.Fatalf("near-duplicate sources were not merged: %#v", merged)
	}
}

func TestHotWordAnalyzerCountsDocumentsAndFiltersNoise(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, cnLocation())
	service := NewMarketHotWordsServiceWithDB(nil, func() time.Time { return now })
	documents := []hotWordDocument{
		hotWordDocumentFixture(1, "以色列人工智能人工智能人工智能带来利好上涨，公司披露12345。", "source-a", now.Add(-time.Hour), "量子芯片"),
		hotWordDocumentFixture(2, "以色列关注人工智能和量子芯片继续上涨。", "source-b", now.Add(-2*time.Hour), "量子芯片"),
	}
	stats, sentiment := service.analyzeDocuments(documents, now)
	ai := stats["人工智能"]
	if ai == nil || ai.documentCount != 2 || ai.occurrences <= ai.documentCount {
		t.Fatalf("AI stats = %#v, want two documents and repeated occurrences", ai)
	}
	if tag := stats["量子芯片"]; tag == nil || tag.documentCount != 2 {
		t.Fatalf("forced entity tag stats = %#v", tag)
	}
	if entity := stats["以色列"]; entity == nil || entity.documentCount != 2 {
		t.Fatalf("general dictionary entity stats = %#v", entity)
	}
	if _, exists := stats["公司"]; exists {
		t.Fatal("generic stop word 公司 must be filtered")
	}
	if sentiment.Score <= 0 || sentiment.Category != Positive {
		t.Fatalf("sentiment = %#v, want normalized positive score", sentiment)
	}
}

func TestRankHotWordsUsesBurstAndFallbackModes(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, cnLocation())
	documents := make([]hotWordDocument, 8)
	for index := range documents {
		documents[index] = hotWordDocumentFixture(uint(index+1), "人工智能", "source-a", now.Add(-time.Duration(index)*time.Hour))
	}
	current := map[string]*hotWordStats{
		"人工智能": {display: "人工智能", documentCount: 5, occurrences: 9, recencySum: 4, latestAt: now,
			sources: map[string]struct{}{"source-a": {}, "source-b": {}}, documentIDs: []int{0, 1, 2, 3, 4}},
		"一次提及": {display: "一次提及", documentCount: 1, occurrences: 1, recencySum: 1, latestAt: now,
			sources: map[string]struct{}{"source-a": {}}, documentIDs: []int{5}},
	}
	baseline := map[string]*hotWordStats{"人工智能": {documentCount: 0}}

	burst := rankHotWords(current, baseline, documents, 100, 1000, true, 30, now)
	if len(burst) != 1 || burst[0].BurstRatio == nil || *burst[0].BurstRatio <= 1 || burst[0].GrowthPct == nil {
		t.Fatalf("burst ranking = %#v", burst)
	}
	if burst[0].Confidence != "high" || len(burst[0].RepresentativeNews) != 3 {
		t.Fatalf("burst confidence/news = %q/%d", burst[0].Confidence, len(burst[0].RepresentativeNews))
	}

	fallback := rankHotWords(current, baseline, documents, 100, 0, false, 30, now)
	if len(fallback) != 1 || fallback[0].BurstRatio != nil || fallback[0].GrowthPct != nil || fallback[0].Confidence != "medium" {
		t.Fatalf("fallback ranking = %#v", fallback)
	}
}

func TestHotWordsServiceFallsBackWhenBaselineIsSparseAndCaches(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "hot-words-cache.db"))
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Dao.AutoMigrate(&models.Telegraph{}, &models.TelegraphTags{}, &models.Tags{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, cnLocation())
	contents := []string{
		"人工智能产业持续上涨，企业发布新一代模型",
		"人工智能推动芯片需求增长，算力订单明显增加",
		"多地建设人工智能算力中心，产业迎来利好",
	}
	for index := 0; index < 3; index++ {
		publishedAt := now.Add(-time.Duration(index+1) * time.Hour)
		row := models.Telegraph{DataTime: &publishedAt, Content: contents[index], Source: "source-a"}
		if err := db.Dao.Create(&row).Error; err != nil {
			t.Fatalf("seed current row: %v", err)
		}
	}
	clock := now
	service := NewMarketHotWordsServiceWithDB(db.Dao, func() time.Time { return clock })
	first := service.HotWords(context.Background(), HotWordsQuery{})
	if first.Status != marketdata.StatusPartial || first.Data.Baseline.Available || first.Data.Baseline.Mode != "coverage_fallback" {
		t.Fatalf("fallback envelope = status=%q baseline=%#v", first.Status, first.Data.Baseline)
	}
	if len(first.Data.Items) == 0 || first.Data.Items[0].BurstRatio != nil {
		t.Fatalf("fallback items = %#v", first.Data.Items)
	}

	newAt := now.Add(-10 * time.Minute)
	if err := db.Dao.Create(&models.Telegraph{DataTime: &newAt, Content: "黄金价格显著下跌", Source: "source-b"}).Error; err != nil {
		t.Fatalf("seed after cache: %v", err)
	}
	second := service.HotWords(context.Background(), HotWordsQuery{})
	if second.Data.CurrentDocumentCount != first.Data.CurrentDocumentCount || !second.FetchedAt.Equal(first.FetchedAt) {
		t.Fatal("result should remain cached for five minutes")
	}
	clock = clock.Add(hotWordsCacheTTL + time.Second)
	third := service.HotWords(context.Background(), HotWordsQuery{})
	if third.Data.CurrentDocumentCount != first.Data.CurrentDocumentCount+1 || !third.FetchedAt.After(first.FetchedAt) {
		t.Fatal("expired cache did not recompute the news window")
	}
}

func TestNormalizedHotWordsSentimentBoundsAndNeutral(t *testing.T) {
	if got := normalizedHotWordsSentiment(0, 0); got.Score != 0 || got.Category != Neutral {
		t.Fatalf("neutral sentiment = %#v", got)
	}
	positive := normalizedHotWordsSentiment(10, 0)
	negative := normalizedHotWordsSentiment(0, 10)
	if math.Abs(positive.Score-100) > 1e-9 || math.Abs(negative.Score+100) > 1e-9 {
		t.Fatalf("sentiment bounds = positive %v negative %v", positive.Score, negative.Score)
	}
}

func TestHotWordBaselineAvailabilityAndRowLimit(t *testing.T) {
	start := time.Date(2026, 8, 20, 12, 0, 0, 0, cnLocation())
	documents := make([]hotWordDocument, 500)
	for index := range documents {
		day := index % 3
		documents[index] = hotWordDocumentFixture(uint(index+1), "不同的基线新闻", "source-a", start.Add(time.Duration(day)*24*time.Hour))
	}
	if !hotWordBaselineAvailable(documents, false, nil) {
		t.Fatal("500 documents across three qualifying days should form a valid baseline")
	}
	if hotWordBaselineAvailable(documents, true, nil) || hotWordBaselineAvailable(documents[:499], false, nil) {
		t.Fatal("truncated or undersized history must not form a valid baseline")
	}

	rows := make([]*models.Telegraph, hotWordsMaximumRows+1)
	limited, truncated := limitHotWordRows(rows)
	if !truncated || len(limited) != hotWordsMaximumRows {
		t.Fatalf("limited rows=%d truncated=%v", len(limited), truncated)
	}
}

func TestHotWordsServiceUnavailableWithoutDatabase(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, cnLocation())
	result := NewMarketHotWordsServiceWithDB(nil, func() time.Time { return now }).HotWords(context.Background(), HotWordsQuery{})
	if result.Status != marketdata.StatusUnavailable || len(result.Errors) != 1 || result.Errors[0].Code != "database_unavailable" {
		t.Fatalf("unavailable result = %#v", result)
	}
}

func hotWordTelegraph(id uint, content, source string, eventAt time.Time) *models.Telegraph {
	item := &models.Telegraph{DataTime: &eventAt, Content: content, Source: source}
	item.ID = id
	return item
}

func hotWordDocumentFixture(id uint, content, source string, eventAt time.Time, tags ...string) hotWordDocument {
	item := hotWordTelegraph(id, content, source, eventAt)
	tagSet := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		tagSet[tag] = struct{}{}
	}
	return hotWordDocument{news: item, eventAt: eventAt, normalized: normalizeHotWordText(content), simhash: hotWordSimHash(normalizeHotWordText(content)),
		sources: map[string]struct{}{source: {}}, tags: tagSet}
}
