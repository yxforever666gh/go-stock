package data

import (
	"context"
	"testing"
	"time"

	"go-stock/backend/marketintel"
	"go-stock/backend/news"
)

type compatibilityNewsStub struct{ result news.NewsWindowResult }

func (s compatibilityNewsStub) GetNewsWindow(context.Context, news.Query) (news.NewsWindowResult, error) {
	return s.result, nil
}

func TestCompatibilityMarketIntelReaderProjectsVisibleNews(t *testing.T) {
	asOf := time.Date(2026, 8, 6, 15, 0, 0, 0, time.UTC)
	reader := NewCompatibilityMarketIntelReader(compatibilityNewsStub{result: news.NewsWindowResult{
		Status: news.WindowStatusOK,
		Items:  []news.Item{{ID: "n-1", Title: "event", Source: "cache", SourceAt: asOf.Add(-time.Hour), AvailableAt: asOf.Add(-30 * time.Minute)}},
	}})
	evidence, err := reader.Evidence(context.Background(), marketintel.Query{
		Symbols: []string{"600000.SH"}, Types: []marketintel.EvidenceType{marketintel.EvidenceEvent}, Start: asOf.Add(-24 * time.Hour), End: asOf, AsOf: asOf,
	})
	if err != nil || len(evidence) != 1 || evidence[0].ID != "news:n-1:600000.SH" || evidence[0].Symbol != "600000.SH" {
		t.Fatalf("evidence=%+v err=%v", evidence, err)
	}
}

func TestCompatibilityMarketIntelReaderKeepsPerSymbolEvidenceIdentityAndWindow(t *testing.T) {
	asOf := time.Date(2026, 8, 6, 15, 0, 0, 0, time.UTC)
	reader := NewCompatibilityMarketIntelReader(compatibilityNewsStub{result: news.NewsWindowResult{
		Status: news.WindowStatusOK,
		Items: []news.Item{
			{ID: "shared", Source: "cache", SourceAt: asOf.Add(-time.Hour), AvailableAt: asOf.Add(-30 * time.Minute)},
			{ID: "too-old", Source: "cache", SourceAt: asOf.Add(-25 * time.Hour), AvailableAt: asOf.Add(-24 * time.Hour)},
		},
	}})
	evidence, err := reader.Evidence(context.Background(), marketintel.Query{
		Symbols: []string{"600000.sh", "000001.SZ", "600000.SH"}, Types: []marketintel.EvidenceType{marketintel.EvidenceEvent},
		Start: asOf.Add(-24 * time.Hour), End: asOf, AsOf: asOf,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 2 {
		t.Fatalf("evidence=%+v, want one visible event for each unique symbol", evidence)
	}
	if evidence[0].ID != "news:shared:000001.SZ" || evidence[1].ID != "news:shared:600000.SH" {
		t.Fatalf("unexpected evidence identities: %+v", evidence)
	}
}
