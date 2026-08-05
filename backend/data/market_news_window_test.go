package data

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
)

func isolateMarketNewsFetchMeta(t *testing.T) {
	t.Helper()
	marketNewsFetchMetaMu.Lock()
	previous := make(map[string]marketNewsFetchMeta, len(marketNewsFetchMetaBySource))
	for key, meta := range marketNewsFetchMetaBySource {
		previous[key] = meta
	}
	previousSequence := marketNewsFetchSequence
	marketNewsFetchMetaBySource = map[string]marketNewsFetchMeta{}
	marketNewsFetchSequence = 0
	marketNewsFetchMetaMu.Unlock()
	t.Cleanup(func() {
		marketNewsFetchMetaMu.Lock()
		marketNewsFetchMetaBySource = previous
		marketNewsFetchSequence = previousSequence
		marketNewsFetchMetaMu.Unlock()
	})
}

func TestGetNewsWindowUsesEventTimeAndLegacyCreatedAtFallback(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "news-window.db"))
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Dao.AutoMigrate(&models.Telegraph{}, &models.TelegraphTags{}, &models.Tags{}); err != nil {
		t.Fatalf("auto migrate news tables: %v", err)
	}

	loc := cnLocation()
	from := time.Date(2026, 8, 4, 10, 0, 0, 0, loc)
	to := from.Add(2 * time.Hour)
	inWindow := from.Add(time.Hour)
	outsideWindow := from.Add(-time.Minute)
	zero := time.Time{}
	rows := []models.Telegraph{
		{DataTime: &inWindow, Content: "event-time row", Source: "source-a"},
		{DataTime: &outsideWindow, Content: "old event with fresh ingestion", Source: "source-a"},
		{DataTime: &outsideWindow, Content: "stale-only source row", Source: "source-c"},
		{DataTime: nil, Content: "legacy nil event time", Source: "source-b"},
		{DataTime: &zero, Content: "legacy zero event time", Source: "source-b"},
	}
	rows[0].CreatedAt = from.Add(-24 * time.Hour)
	rows[1].CreatedAt = from.Add(30 * time.Minute)
	rows[2].CreatedAt = from.Add(35 * time.Minute)
	rows[3].CreatedAt = from.Add(40 * time.Minute)
	rows[4].CreatedAt = from.Add(50 * time.Minute)
	for idx := range rows {
		if err := db.Dao.Create(&rows[idx]).Error; err != nil {
			t.Fatalf("seed news row %d: %v", idx, err)
		}
	}

	result, err := NewMarketNewsApi().GetNewsWindow(nil, from, to)
	if err != nil {
		t.Fatalf("GetNewsWindow: %v", err)
	}
	if result.Status != NewsWindowStatusOK {
		t.Fatalf("status = %q, want ok (warning=%q)", result.Status, result.Warning)
	}
	if len(result.Items) != 3 {
		t.Fatalf("items = %d, want 3", len(result.Items))
	}
	for _, item := range result.Items {
		if item.Content == "old event with fresh ingestion" {
			t.Fatal("non-zero old DataTime must not fall back to fresh CreatedAt")
		}
	}

	filtered, err := NewMarketNewsApi().GetNewsWindow([]string{" source-a ", "source-a"}, from, to)
	if err != nil {
		t.Fatalf("filtered GetNewsWindow: %v", err)
	}
	if len(filtered.Sources) != 1 || filtered.Sources[0] != "source-a" {
		t.Fatalf("normalized sources = %#v", filtered.Sources)
	}
	if len(filtered.Items) != 1 || filtered.Items[0].Content != "event-time row" {
		t.Fatalf("filtered items = %#v", filtered.Items)
	}

	stale, err := NewMarketNewsApi().GetNewsWindow([]string{"source-c"}, from, to)
	if err != nil {
		t.Fatalf("stale GetNewsWindow: %v", err)
	}
	if stale.Status != NewsWindowStatusStale || len(stale.Items) != 1 || stale.Items[0].Content != "stale-only source row" {
		t.Fatalf("stale result = status=%q items=%#v", stale.Status, stale.Items)
	}

	empty, err := NewMarketNewsApi().GetNewsWindow([]string{"latest-24h-display-label"}, from, to)
	if err != nil {
		t.Fatalf("empty GetNewsWindow: %v", err)
	}
	if empty.Status != NewsWindowStatusEmpty || len(empty.Items) != 0 {
		t.Fatalf("fake display source should be empty, got status=%q items=%d", empty.Status, len(empty.Items))
	}
}

func TestNewsWindowItemsAreStale(t *testing.T) {
	from := time.Date(2026, 8, 4, 10, 0, 0, 0, cnLocation())
	old := from.Add(-time.Minute)
	item := &models.Telegraph{DataTime: &old}
	if !newsWindowItemsAreStale([]*models.Telegraph{item}, from) {
		t.Fatal("expected an all-old result to be stale")
	}
	current := from
	item.DataTime = &current
	if newsWindowItemsAreStale([]*models.Telegraph{item}, from) {
		t.Fatal("event at the lower bound is not stale")
	}
}

func TestGetNewsWindowUsesStableIDTieBreaker(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "news-window-order.db"))
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Dao.AutoMigrate(&models.Telegraph{}, &models.TelegraphTags{}, &models.Tags{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	loc := cnLocation()
	from := time.Date(2026, 8, 4, 10, 0, 0, 0, loc)
	eventAt := from.Add(time.Hour)
	createdAt := from.Add(30 * time.Minute)
	first := models.Telegraph{DataTime: &eventAt, Content: "first", Source: "source-a"}
	first.CreatedAt = createdAt
	second := models.Telegraph{DataTime: &eventAt, Content: "second", Source: "source-a"}
	second.CreatedAt = createdAt
	if err := db.Dao.Create(&first).Error; err != nil {
		t.Fatalf("seed first: %v", err)
	}
	if err := db.Dao.Create(&second).Error; err != nil {
		t.Fatalf("seed second: %v", err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		result, err := NewMarketNewsApi().GetNewsWindow(nil, from, from.Add(2*time.Hour))
		if err != nil {
			t.Fatalf("query attempt %d: %v", attempt, err)
		}
		if len(result.Items) != 2 || result.Items[0].ID != second.ID || result.Items[1].ID != first.ID {
			t.Fatalf("unstable tie order attempt %d: %#v", attempt, result.Items)
		}
	}
}

func TestGetNewsWindowDistinguishesFetchFailureFromSuccessfulEmpty(t *testing.T) {
	isolateMarketNewsFetchMeta(t)
	db.Init(filepath.Join(t.TempDir(), "news-window-fetch-state.db"))
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Dao.AutoMigrate(&models.Telegraph{}, &models.TelegraphTags{}, &models.Tags{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	from := time.Now().Add(-time.Minute)
	to := time.Now().Add(time.Minute)
	failedSequence := marketNewsBeginFetch(marketNewsFetchKeyCLSTelegraphAPI, marketNewsSourceCLSTelegraph)
	marketNewsFinishFetch(marketNewsFetchKeyCLSTelegraphAPI, failedSequence, "direct", false, errors.New("upstream unavailable"))

	failed, err := NewMarketNewsApi().GetNewsWindow(nil, from, to)
	if err == nil {
		t.Fatal("failed upstream refresh must return an error")
	}
	if failed.Status != NewsWindowStatusFailed || len(failed.Items) != 0 {
		t.Fatalf("failed result = status=%q items=%d warning=%q", failed.Status, len(failed.Items), failed.Warning)
	}

	// A failure for one real source must not poison an explicitly different
	// source filter.
	filtered, err := NewMarketNewsApi().GetNewsWindow([]string{marketNewsSourceSina}, from, to)
	if err != nil || filtered.Status != NewsWindowStatusEmpty {
		t.Fatalf("filtered result = status=%q err=%v warning=%q", filtered.Status, err, filtered.Warning)
	}

	// A later successful fallback fetch with a valid but empty feed supersedes
	// the failed API endpoint for the same real source.
	successSequence := marketNewsBeginFetch(marketNewsFetchKeyCLSTelegraphWeb, marketNewsSourceCLSTelegraph)
	marketNewsFinishFetch(marketNewsFetchKeyCLSTelegraphWeb, successSequence, "proxy", true, nil)
	empty, err := NewMarketNewsApi().GetNewsWindow(nil, from, to)
	if err != nil {
		t.Fatalf("successful empty fetch returned error: %v", err)
	}
	if empty.Status != NewsWindowStatusEmpty || len(empty.Items) != 0 {
		t.Fatalf("successful empty result = status=%q items=%d warning=%q", empty.Status, len(empty.Items), empty.Warning)
	}
}

func TestMarketNewsFetchStateIgnoresOlderCompletion(t *testing.T) {
	isolateMarketNewsFetchMeta(t)
	older := marketNewsBeginFetch(marketNewsFetchKeySinaLive, marketNewsSourceSina)
	newer := marketNewsBeginFetch(marketNewsFetchKeySinaLive, marketNewsSourceSina)
	marketNewsFinishFetch(marketNewsFetchKeySinaLive, newer, "proxy", true, nil)
	marketNewsFinishFetch(marketNewsFetchKeySinaLive, older, "direct", false, errors.New("late failure"))

	meta := GetMarketNewsFetchMeta(marketNewsFetchKeySinaLive)
	if meta["status"] != "success" || meta["networkPath"] != "proxy" || meta["fallbackUsed"] != true {
		t.Fatalf("newer successful observation was overwritten: %#v", meta)
	}
}
