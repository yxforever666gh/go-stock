package data

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"go-stock/backend/marketdata"
	"go-stock/backend/research2"
	"go-stock/backend/research2app"
	"go-stock/backend/themes"
	"go-stock/internal/researchevidence"
)

type themeResearch2EvidenceProvider struct {
	evidence research2.Evidence
}

func (provider themeResearch2EvidenceProvider) Collect(context.Context, time.Time) (research2.Evidence, error) {
	return provider.evidence, nil
}

func (provider themeResearch2EvidenceProvider) CollectWithExclusions(context.Context, time.Time, map[string]struct{}) (research2.Evidence, error) {
	return provider.evidence, nil
}

type themeEvidenceReaderFixture struct {
	calls    int
	cutoffs  []time.Time
	envelope marketdata.DataEnvelope[themes.ResearchEvidence]
}

func (f *themeEvidenceReaderFixture) ResearchEvidence(_ context.Context, cutoff time.Time) marketdata.DataEnvelope[themes.ResearchEvidence] {
	f.calls++
	f.cutoffs = append(f.cutoffs, cutoff)
	return f.envelope
}

func TestNonExperimentalResearch2CutoffKeepsLegacyRule(t *testing.T) {
	at := time.Date(2026, 8, 28, 9, 55, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	late := researchevidence.SourceDocument{CollectedAt: at.Add(2 * time.Minute), Content: "late"}
	legacyFrozen := research2DocumentAtCutoff(late, at, false)
	if legacyFrozen.Content != "" || legacyFrozen.Error == "" || research2DocumentStatus(legacyFrozen, at, false) != "failed" {
		t.Fatalf("disabled Research2 cutoff bytes/status changed: %+v status=%s", legacyFrozen, research2DocumentStatus(legacyFrozen, at, false))
	}
}

func TestThemeResearchEvidenceUsesStrictInclusiveCutoffAndIndependentConflictClaims(t *testing.T) {
	cutoff := time.Date(2026, 8, 28, 9, 55, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	before, after := cutoff.Add(-time.Minute), cutoff.Add(time.Nanosecond)
	eventEqual, eventFuture := cutoff, after
	claimEqual, claimFuture := cutoff, after
	envelope := marketdata.DataEnvelope[themes.ResearchEvidence]{
		Status: marketdata.StatusOK, FetchedAt: cutoff.Add(2 * time.Minute),
		Data: themes.ResearchEvidence{CutoffAt: cutoff, Themes: []themes.ResearchTheme{
			{
				ID: "theme-1", Name: "人工智能",
				Snapshot: themes.DailySnapshot{ID: "snapshot-equal", ThemeID: "theme-1", TradeDate: "2026-08-28", LifecycleStage: themes.StageAccelerate, FrozenAt: cutoff, ContentHash: "snapshot-hash", ConstituentCount: 3, CatalystCount: 2},
				Constituents: []themes.SnapshotConstituent{
					{ID: "stock-1", AssetType: "stock", Market: "SH", Code: "sh600000", Name: "浦发银行"},
					{ID: "etf-1", AssetType: "etf", Market: "SH", Code: "sh512000", Name: "券商ETF"},
					{ID: "fund-1", AssetType: "fund", Market: "SZ", Code: "sz160000", Name: "测试基金"},
				},
				BackgroundOnlyAssetTypes: []string{"etf", "fund"},
				Catalysts: []themes.CatalystEvent{
					{ID: "event-equal", ThemeID: "theme-1", Title: "政策催化", EventAt: before, FirstAvailableAt: &eventEqual, CredibilityScore: 80, Status: "active", Claims: []themes.SourceClaim{
						{ID: "claim-support", SourceName: "来源A", SourceRef: "https://a.test/1", Stance: "supports", Summary: "支持", AvailableAt: &claimEqual, CollectedAt: before},
						{ID: "claim-contradict", SourceName: "来源B", SourceRef: "https://b.test/1", Stance: "contradicts", Summary: "反驳", AvailableAt: &claimFuture, CollectedAt: before.Add(-time.Minute)},
						{ID: "claim-null", SourceName: "来源C", SourceRef: "https://c.test/1", Stance: "neutral", Summary: "未知", AvailableAt: nil, CollectedAt: before},
					}},
					{ID: "event-future", ThemeID: "theme-1", Title: "未来事件", EventAt: before, FirstAvailableAt: &eventFuture, Status: "active", Claims: []themes.SourceClaim{
						{ID: "claim-event-future", SourceName: "来源D", SourceRef: "https://d.test/1", Stance: "supports", Summary: "未来", AvailableAt: &claimEqual, CollectedAt: before},
					}},
					{ID: "event-null", ThemeID: "theme-1", Title: "未知事件", EventAt: before, FirstAvailableAt: nil, Status: "active", Claims: []themes.SourceClaim{
						{ID: "claim-event-null", SourceName: "来源E", SourceRef: "https://e.test/1", Stance: "supports", Summary: "未知", AvailableAt: &claimEqual, CollectedAt: before},
					}},
				},
			},
			{ID: "theme-2", Name: "未来题材", Snapshot: themes.DailySnapshot{ID: "snapshot-future", ThemeID: "theme-2", TradeDate: "2026-08-28", LifecycleStage: themes.StageObserve, FrozenAt: after}},
		}},
	}
	documents := themeResearchEvidenceDocuments(envelope, cutoff)
	byID := make(map[string]researchevidence.SourceDocument, len(documents))
	for _, document := range documents {
		byID[document.SourceID] = document
	}
	for _, sourceID := range []string{
		"theme-snapshot:snapshot-equal", "theme-snapshot:snapshot-future",
		"theme-catalyst:claim-support", "theme-catalyst:claim-contradict", "theme-catalyst:claim-null",
		"theme-catalyst:claim-event-future", "theme-catalyst:claim-event-null",
	} {
		if _, ok := byID[sourceID]; !ok {
			t.Fatalf("missing independent theme evidence document %s: %+v", sourceID, documents)
		}
	}
	if byID["theme-catalyst:claim-support"].AvailableAt == nil || !byID["theme-catalyst:claim-support"].AvailableAt.Equal(cutoff) {
		t.Fatalf("cutoff-equal claim was not usable: %+v", byID["theme-catalyst:claim-support"])
	}
	if byID["theme-catalyst:claim-contradict"].AvailableAt == nil || !byID["theme-catalyst:claim-contradict"].AvailableAt.After(cutoff) {
		t.Fatalf("collected-early but available-late claim did not remain after cutoff: %+v", byID["theme-catalyst:claim-contradict"])
	}
	if byID["theme-catalyst:claim-null"].AvailableAt != nil || byID["theme-catalyst:claim-event-null"].AvailableAt != nil {
		t.Fatal("nil claim/event availability became usable")
	}
	if byID["theme-catalyst:claim-event-future"].AvailableAt == nil || !byID["theme-catalyst:claim-event-future"].AvailableAt.After(cutoff) {
		t.Fatal("future event availability did not dominate its earlier claim availability")
	}
	if byID["theme-snapshot:snapshot-future"].AvailableAt == nil || !byID["theme-snapshot:snapshot-future"].AvailableAt.After(cutoff) {
		t.Fatal("future snapshot did not retain its after-cutoff availability")
	}
	snapshotContent := byID["theme-snapshot:snapshot-equal"].Content
	if !strings.Contains(snapshotContent, "sh600000") || strings.Contains(snapshotContent, "sh512000") || strings.Contains(snapshotContent, "券商ETF") || strings.Contains(snapshotContent, "sz160000") || strings.Contains(snapshotContent, "测试基金") {
		t.Fatalf("ETF/fund escaped the background-only boundary: %s", snapshotContent)
	}
	if !strings.Contains(snapshotContent, `"backgroundOnlyAssetTypes":["etf","fund"]`) {
		t.Fatalf("background asset types were not retained: %s", snapshotContent)
	}
}

func TestResearch2ThemeEvidencePersistsAfterCutoffWithoutContentAndCannotRewriteFrozenBatch(t *testing.T) {
	repository := research2EvidenceTestRepository(t)
	cutoff := time.Date(2026, 8, 28, 9, 55, 0, 0, shanghaiDataLocation())
	equal, future := cutoff, cutoff.Add(time.Nanosecond)
	documents := []researchevidence.SourceDocument{
		{SourceID: "theme-snapshot:equal", SourceName: "equal", Category: "theme", CollectedAt: cutoff.Add(time.Minute), AvailableAt: &equal, Content: "equal-content"},
		{SourceID: "theme-catalyst:future", SourceName: "future", Category: "catalyst", CollectedAt: cutoff.Add(-time.Hour), AvailableAt: &future, Content: "future-secret"},
		{SourceID: "theme-catalyst:null", SourceName: "null", Category: "catalyst", CollectedAt: cutoff.Add(-time.Hour), AvailableAt: nil, Content: "null-secret"},
	}
	for index := range documents {
		documents[index] = research2DocumentAtCutoff(documents[index], cutoff, true)
	}
	collector := research2app.NewDurableEvidenceCollector(
		themeResearch2EvidenceProvider{evidence: research2.Evidence{Prompt: "fixture", SourceStatusJSON: "[]", Documents: documents}},
		repository, researchThemeEvidenceProfile, buildResearch2EvidenceItem,
	)
	evidence, err := collector.CollectForRun(context.Background(), "theme-r2-run", cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.EvidenceProfileVersion != "market-evidence-v2" || evidence.EvidenceSetID == "" {
		t.Fatalf("research2 theme profile/link missing: %+v", evidence)
	}
	batch, err := repository.Batch(context.Background(), evidence.EvidenceSetID)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Status != marketdata.StatusFrozen || batch.FrozenAt == nil {
		t.Fatalf("theme evidence batch was not frozen: %+v", batch)
	}
	items, err := repository.Items(context.Background(), evidence.EvidenceSetID)
	if err != nil {
		t.Fatal(err)
	}
	statusByID := map[string]string{}
	for _, item := range items {
		statusByID[item.SourceID] = item.Status
		if item.Status == marketdata.StatusAfterCutoff || item.Status == marketdata.StatusUnavailable {
			var payload map[string]any
			if unmarshalErr := json.Unmarshal(item.Payload, &payload); unmarshalErr != nil {
				t.Fatal(unmarshalErr)
			}
			if payload["content"] != "" {
				t.Fatalf("excluded theme content persisted for %s: %s", item.SourceID, item.Payload)
			}
		}
	}
	if statusByID["theme-snapshot:equal"] != marketdata.StatusOK || statusByID["theme-catalyst:future"] != marketdata.StatusAfterCutoff || statusByID["theme-catalyst:null"] != marketdata.StatusUnavailable {
		t.Fatalf("unexpected persisted statuses: %#v", statusByID)
	}
	before, _ := json.Marshal(items)
	appendErr := repository.AppendItems(context.Background(), evidence.EvidenceSetID, []marketdata.EvidenceItem{{SourceID: "theme-catalyst:late", SourceName: "late", Category: "catalyst"}})
	if !errors.Is(appendErr, marketdata.ErrEvidenceBatchFrozen) {
		t.Fatalf("frozen theme evidence accepted a late write: %v", appendErr)
	}
	afterItems, err := repository.Items(context.Background(), evidence.EvidenceSetID)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(afterItems)
	if !bytes.Equal(before, after) {
		t.Fatal("research2 frozen theme evidence changed after the late-write attempt")
	}
}

func TestResearch2ThemeBackgroundETFAndFundNeverBecomeCandidates(t *testing.T) {
	asOf := time.Date(2026, 8, 28, 10, 0, 0, 0, shanghaiDataLocation())
	rows := []research2MarketRow{
		{Code: "600000", Name: "浦发银行", Price: 10, PreClose: 10, ChangeRate: 1, Volume: 100, Amount: 10000, ListingDate: 19991110},
		{Code: "512000", Name: "券商ETF", Price: 1, PreClose: 1, ChangeRate: 1, Volume: 100, Amount: 10000, ListingDate: 20100101},
		{Code: "159001", Name: "测试基金", Price: 1, PreClose: 1, ChangeRate: 1, Volume: 100, Amount: 10000, ListingDate: 20100101},
	}
	candidates := selectResearch2Candidates(rows, 12, asOf)
	if len(candidates) != 1 || candidates[0].Code != "sh600000" {
		t.Fatalf("ETF/fund entered Research2 candidates: %+v", candidates)
	}
}
