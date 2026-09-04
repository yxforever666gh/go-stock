package data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"go-stock/backend/marketdata"
	"go-stock/backend/research"
	"go-stock/backend/research2"
)

type research2StructuredSourceFixture struct {
	cutoff     time.Time
	candidates []research.StockCandidate
}

func (f *research2StructuredSourceFixture) CollectMarket(context.Context, time.Time) ([]research.SourceDocument, error) {
	available := f.cutoff
	return []research.SourceDocument{{SourceName: "空市场响应", Category: "market", CollectedAt: f.cutoff.Add(time.Second), AvailableAt: &available,
		Content: `{"code":0,"data":{"total":0,"items":[]}}`}}, nil
}

func (f *research2StructuredSourceFixture) CollectSectors(context.Context, time.Time) ([]research.SourceDocument, error) {
	available := f.cutoff.Add(time.Second)
	return []research.SourceDocument{{SourceName: "无法证明时间的板块资料", Category: "sector", CollectedAt: available, AvailableAt: &available,
		Content: `{"items":[{"name":"未来内容但没有时间"}]}`}}, nil
}

func (f *research2StructuredSourceFixture) CollectStocks(_ context.Context, _ time.Time, candidates []research.StockCandidate) ([]research.SourceDocument, error) {
	f.candidates = append([]research.StockCandidate(nil), candidates...)
	available := f.cutoff.Add(2 * time.Second)
	documents := make([]research.SourceDocument, 0, len(candidates))
	for _, candidate := range candidates {
		content := fmt.Sprintf(`[{"title":"截止前公告-%s","publishedAt":"%s"},{"title":"截止后公告-%s","publishedAt":"%s"}]`,
			candidate.Code, f.cutoff.Add(-time.Hour).Format(time.RFC3339), candidate.Code, f.cutoff.Add(time.Hour).Format(time.RFC3339))
		documents = append(documents, research.SourceDocument{SourceName: "公告 " + candidate.Code, Category: "stock", CollectedAt: available,
			AvailableAt: &available, SourceRef: "https://example.invalid/" + candidate.Code, Content: content})
	}
	return documents, nil
}

type research2MinuteFixture struct{ count int }

func (f research2MinuteFixture) Window(_ context.Context, code string, start, end time.Time) ([]minuteBar, string, error) {
	result := make([]minuteBar, 0, f.count)
	for index := 0; index < f.count; index++ {
		at := start.Add(time.Duration(index) * time.Minute)
		if !at.Before(end) {
			break
		}
		price := 10 + float64(index)*0.1
		result = append(result, minuteBar{TradeTime: at, Open: price, High: price + 0.05, Low: price - 0.05, Close: price + 0.02,
			Volume: 100 + float64(index)*10, Amount: (100 + float64(index)*10) * (price + 0.02), Source: "tencent"})
	}
	return result, "tencent", nil
}

type research2LateAuxiliaryFixture struct {
	snapshotAt time.Time
	freezeAt   time.Time
}

func (f research2LateAuxiliaryFixture) CollectMarket(context.Context, time.Time) ([]research.SourceDocument, error) {
	collectedAt := f.freezeAt.Add(-time.Second)
	availableAt := collectedAt
	return []research.SourceDocument{
		{SourceID: "research2:test:late-aux", SourceName: "快照后完成的辅助资料", Category: "market", CollectedAt: collectedAt, AvailableAt: &availableAt, Content: `{"items":[{"name":"辅助信息"}]}`},
		{SourceID: "research2:test:future-event", SourceName: "真正晚于行情锚点的事件", Category: "market", CollectedAt: collectedAt, AvailableAt: &availableAt,
			Content: fmt.Sprintf(`[{"title":"未来事件","publishedAt":"%s"}]`, f.snapshotAt.Add(time.Second).Format(time.RFC3339))},
	}, nil
}

func (research2LateAuxiliaryFixture) CollectSectors(context.Context, time.Time) ([]research.SourceDocument, error) {
	return nil, nil
}

func (research2LateAuxiliaryFixture) CollectStocks(context.Context, time.Time, []research.StockCandidate) ([]research.SourceDocument, error) {
	return nil, nil
}

func research2StructuredRows(cutoff time.Time, count int) []research2MarketRow {
	rows := make([]research2MarketRow, 0, count)
	for index := 0; index < count; index++ {
		price := 10 + float64(index)/10
		preClose := price / 1.05
		rows = append(rows, research2MarketRow{Code: fmt.Sprintf("60%04d", index+1), Name: fmt.Sprintf("样本%d", index+1), Price: price,
			ChangeRate: 5 - float64(index)/100, ChangeValid: true, Volume: 100000, Amount: 10000000, Turnover: 3, High: price + 0.5, Low: preClose - 0.2, Open: preClose + 0.1,
			PreClose: preClose, ListingDate: 20200101, Timestamp: cutoff.Unix()})
	}
	return rows
}

func research2InvalidatePrices(rows []research2MarketRow, count int) []research2MarketRow {
	result := append([]research2MarketRow(nil), rows...)
	for index := 0; index < count && index < len(result); index++ {
		result[len(result)-1-index].Price = 0
	}
	return result
}

func research2StructuredEnvelope[T any](cutoff time.Time, data T) marketdata.DataEnvelope[T] {
	available := cutoff
	return marketdata.DataEnvelope[T]{Data: data, Source: "fixture", AsOf: cutoff, FetchedAt: cutoff.Add(time.Second), Status: marketdata.StatusOK,
		Sources: []marketdata.SourceState{{Provider: "fixture", Status: marketdata.StatusOK, AsOf: cutoff, AvailableAt: &available, SourceRef: "fixture"}}}
}

func newResearch2StructuredCollector(t *testing.T, cutoff time.Time, rows []research2MarketRow, reported, minuteCount int) *Research2EvidenceCollector {
	t.Helper()
	sources := &research2StructuredSourceFixture{cutoff: cutoff}
	return &Research2EvidenceCollector{
		sources: sources, stocks: &StockDataApi{}, minuteWindows: research2MinuteFixture{count: minuteCount},
		evidence: research2EvidenceTestRepository(t), evidenceProfile: research2EvidenceProfileV7,
		now: func() time.Time { return cutoff.Add(5 * time.Second) },
		fetchSnapshot: func(context.Context, time.Time) (research2FullMarketSnapshot, error) {
			return research2FullMarketSnapshot{Rows: rows, Reported: reported, SourceID: "fixture-market", SourceName: "测试全市场",
				SourceRef: "fixture-market", AsOf: cutoff.Add(-30 * time.Second), CollectedAt: cutoff.Add(time.Second)}, nil
		},
		collectBreadth: func(context.Context) marketdata.DataEnvelope[BreadthData] {
			return research2StructuredEnvelope(cutoff, BreadthData{Total: reported, Advances: reported})
		},
		collectFlows: func(_ context.Context, request marketdata.ProviderRequest) marketdata.DataEnvelope[[]FundFlowRow] {
			return research2StructuredEnvelope(cutoff, []FundFlowRow{{Code: request.Scope + "-1", Name: request.Scope, NetAmount: 100}})
		},
	}
}

func TestResearch2StructuredEvidenceUsesTrailingWindowAndCompactPrompt(t *testing.T) {
	cutoff := time.Date(2026, 9, 3, 10, 14, 0, 0, shanghaiDataLocation())
	collector := newResearch2StructuredCollector(t, cutoff, research2StructuredRows(cutoff, 20), 20, 5)
	evidence, err := collector.Collect(context.Background(), cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if !evidence.WindowStartAt.Equal(cutoff.Add(-5 * time.Minute)) {
		t.Fatalf("window start=%s want %s", evidence.WindowStartAt, cutoff.Add(-5*time.Minute))
	}
	if evidence.CoveragePct != 100 || len(evidence.Candidates) != 12 || len(evidence.CandidateReferencePrices) != 12 {
		t.Fatalf("unexpected core evidence: coverage=%v candidates=%d prices=%d", evidence.CoveragePct, len(evidence.Candidates), len(evidence.CandidateReferencePrices))
	}
	var compact research2CompactSnapshot
	if err := json.Unmarshal([]byte(evidence.Prompt), &compact); err != nil {
		t.Fatalf("compact prompt is not JSON: %v", err)
	}
	if len(compact.Candidates) != 12 {
		t.Fatalf("compact prompt lost candidates: %d", len(compact.Candidates))
	}
	for _, candidate := range compact.Candidates {
		if !candidate.CoreEligible || candidate.MinuteBarCount != 5 || candidate.Metrics.VWAP == nil || candidate.Metrics.ReturnPct == nil {
			t.Fatalf("candidate metrics incomplete: %+v", candidate)
		}
		if len(candidate.SourceIDs) < 3 {
			t.Fatalf("candidate lacks associated auxiliary source IDs: %+v", candidate)
		}
	}
	if strings.Contains(evidence.Prompt, "截止后公告") || strings.Contains(evidence.Prompt, `"rows":[{"f2"`) {
		t.Fatal("compact prompt leaked future or full raw evidence")
	}
	if !strings.Contains(evidence.Prompt, "截止前公告") {
		t.Fatal("compact prompt omitted bounded pre-cutoff catalyst summary")
	}
	foundArchived := false
	for _, document := range evidence.Documents {
		if strings.HasPrefix(document.SourceName, "公告 ") && strings.Contains(document.Content, "截止前公告") {
			foundArchived = true
			if strings.Contains(document.Content, "截止后公告") {
				t.Fatal("after-cutoff news remained in archived scoring document")
			}
		}
	}
	if !foundArchived {
		t.Fatal("full evidence archive omitted filtered stock documents")
	}
}

func TestResearch2StructuredEvidenceExcludesTodayNearLimitCandidate(t *testing.T) {
	cutoff := time.Date(2026, 9, 4, 10, 14, 0, 0, shanghaiDataLocation())
	rows := research2StructuredRows(cutoff, 20)
	rows[0].PreClose = 10.03
	limitPrice := research2.MainBoardLimitPrice(rows[0].PreClose)
	rows[0].Price = limitPrice * 0.99
	rows[0].ChangeRate = (rows[0].Price/rows[0].PreClose - 1) * 100
	rows[0].High = limitPrice
	collector := newResearch2StructuredCollector(t, cutoff, rows, len(rows), 5)
	evidence, err := collector.Collect(context.Background(), cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.EvidenceProfileVersion != research2EvidenceProfileV7 {
		t.Fatalf("evidence profile=%q want %q", evidence.EvidenceProfileVersion, research2EvidenceProfileV7)
	}
	assertResearch2CandidateAbsent(t, evidence, "sh600001")
}

func TestResearch2CollectForRunWithExclusionsRemovesCodeBeforePromptConstruction(t *testing.T) {
	cutoff := time.Date(2026, 9, 4, 10, 14, 0, 0, shanghaiDataLocation())
	collector := newResearch2StructuredCollector(t, cutoff, research2StructuredRows(cutoff, 20), 20, 5)
	evidence, err := collector.CollectForRunWithExclusions(context.Background(), "run-with-exclusions", cutoff, map[string]struct{}{"SH600001": {}})
	if err != nil {
		t.Fatal(err)
	}
	assertResearch2CandidateAbsent(t, evidence, "sh600001")
	fixture, ok := collector.sources.(*research2StructuredSourceFixture)
	if !ok {
		t.Fatal("unexpected source fixture type")
	}
	for _, candidate := range fixture.candidates {
		if candidate.Code == "sh600001" {
			t.Fatalf("excluded code reached per-stock source collection: %+v", fixture.candidates)
		}
	}
}

func assertResearch2CandidateAbsent(t *testing.T, evidence research2.Evidence, excludedCode string) {
	t.Helper()
	for _, candidate := range evidence.Candidates {
		if candidate.Code == excludedCode {
			t.Fatalf("excluded candidate remained recommendable: %+v", evidence.Candidates)
		}
	}
	var compact research2CompactSnapshot
	if err := json.Unmarshal([]byte(evidence.Prompt), &compact); err != nil {
		t.Fatalf("compact prompt is not JSON: %v", err)
	}
	for _, candidate := range compact.Candidates {
		if candidate.Code == excludedCode {
			t.Fatalf("excluded candidate remained in compact prompt: %+v", compact.Candidates)
		}
	}
}

func TestResearch2StructuredEvidenceUsesLastFiveMorningMinutesDuringLunch(t *testing.T) {
	startedAt := time.Date(2026, 9, 3, 12, 28, 0, 0, shanghaiDataLocation())
	snapshotAt := time.Date(2026, 9, 3, 11, 30, 0, 0, shanghaiDataLocation())
	rows := research2StructuredRows(snapshotAt, 20)
	collector := newResearch2StructuredCollector(t, snapshotAt, rows, 20, 5)
	collector.now = func() time.Time { return startedAt.Add(5 * time.Second) }
	collector.fetchSnapshot = func(context.Context, time.Time) (research2FullMarketSnapshot, error) {
		return research2FullMarketSnapshot{Rows: rows, Reported: 20, SourceID: "fixture-market", SourceName: "午休全市场",
			SourceRef: "fixture-market", AsOf: snapshotAt, CollectedAt: startedAt.Add(time.Second)}, nil
	}

	evidence, err := collector.Collect(context.Background(), startedAt)
	if err != nil {
		t.Fatal(err)
	}
	wantStart := time.Date(2026, 9, 3, 11, 25, 0, 0, shanghaiDataLocation())
	if !evidence.WindowStartAt.Equal(wantStart) || len(evidence.Candidates) != 12 {
		t.Fatalf("lunch evidence window/candidates mismatch: start=%s candidates=%d", evidence.WindowStartAt, len(evidence.Candidates))
	}
	var compact research2CompactSnapshot
	if err = json.Unmarshal([]byte(evidence.Prompt), &compact); err != nil {
		t.Fatal(err)
	}
	if len(compact.Candidates) != 12 {
		t.Fatalf("compact lunch candidates=%d", len(compact.Candidates))
	}
	wantEnd := time.Date(2026, 9, 3, 11, 30, 0, 0, shanghaiDataLocation())
	if !compact.WindowStartAt.Equal(wantStart) || !compact.WindowEndAt.Equal(wantEnd) {
		t.Fatalf("compact lunch window=%s..%s", compact.WindowStartAt, compact.WindowEndAt)
	}
	for _, candidate := range compact.Candidates {
		if !candidate.CoreEligible || candidate.MinuteBarCount < 4 || candidate.MinuteBarCount > 5 {
			t.Fatalf("lunch candidate lacks eligible 4/5-minute window: %+v", candidate)
		}
	}
}

func TestResearch2StructuredEvidenceSeparatesFreezeTimeFromSnapshotAsOf(t *testing.T) {
	snapshotAt := time.Date(2026, 9, 3, 10, 14, 0, 0, shanghaiDataLocation())
	freezeAt := snapshotAt.Add(5 * time.Second)
	collector := newResearch2StructuredCollector(t, snapshotAt, research2StructuredRows(snapshotAt, 20), 20, 5)
	collector.sources = research2LateAuxiliaryFixture{snapshotAt: snapshotAt, freezeAt: freezeAt}
	collector.now = func() time.Time { return freezeAt }

	evidence, err := collector.Collect(context.Background(), snapshotAt)
	if err != nil {
		t.Fatal(err)
	}
	var statuses []struct {
		SourceID string `json:"sourceId"`
		Status   string `json:"status"`
	}
	if err = json.Unmarshal([]byte(evidence.SourceStatusJSON), &statuses); err != nil {
		t.Fatal(err)
	}
	var compact research2CompactSnapshot
	if err = json.Unmarshal([]byte(evidence.Prompt), &compact); err != nil {
		t.Fatal(err)
	}
	if !compact.CutoffAt.Equal(snapshotAt) || !compact.FreezeAt.Equal(freezeAt) {
		t.Fatalf("snapshot/freeze anchors collapsed: snapshot=%s freeze=%s", compact.CutoffAt, compact.FreezeAt)
	}
	statusByID := make(map[string]string, len(statuses))
	for _, status := range statuses {
		statusByID[status.SourceID] = status.Status
	}
	if got := statusByID["research2:test:late-aux"]; got == "" || got == marketdata.StatusAfterCutoff || got == marketdata.StatusUnavailable || got == marketdata.StatusFailed {
		t.Fatalf("late auxiliary collection was not usable before freeze: status=%q statuses=%s", got, evidence.SourceStatusJSON)
	}
	for _, document := range evidence.Documents {
		switch document.SourceID {
		case "research2:test:late-aux":
			if !strings.Contains(document.Content, "辅助信息") || document.Error != "" {
				t.Fatalf("usable post-snapshot auxiliary document was removed: %+v", document)
			}
		case "research2:test:future-event":
			if strings.Contains(document.Content, "未来事件") {
				t.Fatalf("event after snapshot anchor entered evidence: %+v", document)
			}
		}
	}
}

func TestResearch2StructuredEvidenceMayFinishAfterMorningWindow(t *testing.T) {
	startedAt := time.Date(2026, 9, 3, 11, 29, 0, 0, shanghaiDataLocation())
	collector := newResearch2StructuredCollector(t, startedAt, research2StructuredRows(startedAt, 20), 20, 5)
	collector.now = func() time.Time { return time.Date(2026, 9, 3, 11, 31, 0, 0, shanghaiDataLocation()) }
	evidence, err := collector.Collect(context.Background(), startedAt)
	if err != nil {
		t.Fatalf("morning-started collection was rejected after 11:30: %v", err)
	}
	if evidence.CoveragePct != 100 || len(evidence.Candidates) != 12 {
		t.Fatalf("unexpected cross-noon evidence: coverage=%v candidates=%d", evidence.CoveragePct, len(evidence.Candidates))
	}
}

func TestResearch2StructuredEvidenceRejectsCoverageBelow95Percent(t *testing.T) {
	cutoff := time.Date(2026, 9, 3, 9, 50, 0, 0, shanghaiDataLocation())
	collector := newResearch2StructuredCollector(t, cutoff, research2InvalidatePrices(research2StructuredRows(cutoff, 100), 6), 100, 5)
	evidence, err := collector.Collect(context.Background(), cutoff)
	if err == nil || !strings.Contains(err.Error(), "低于95") {
		t.Fatalf("94%% coverage accepted: evidence=%+v err=%v", evidence, err)
	}
	if evidence.CoveragePct != 94 || len(evidence.Documents) != 1 {
		t.Fatalf("failed coverage did not retain auditable snapshot: %+v", evidence)
	}
}

func TestResearch2StructuredEvidenceFallsBackOnlyBelow95Percent(t *testing.T) {
	startedAt := time.Date(2026, 9, 3, 9, 50, 0, 0, shanghaiDataLocation())
	t.Run("exactly 95 percent keeps primary", func(t *testing.T) {
		collector := newResearch2StructuredCollector(t, startedAt, research2InvalidatePrices(research2StructuredRows(startedAt, 100), 5), 100, 5)
		fallbackCalls := 0
		collector.fetchFallback = func(context.Context) (research2FullMarketSnapshot, error) {
			fallbackCalls++
			return research2FullMarketSnapshot{}, errors.New("must not be called")
		}
		evidence, err := collector.Collect(context.Background(), startedAt)
		if err != nil || evidence.CoveragePct != 95 || fallbackCalls != 0 {
			t.Fatalf("95%% primary triggered fallback: coverage=%v calls=%d err=%v", evidence.CoveragePct, fallbackCalls, err)
		}
	})

	t.Run("below 95 percent uses trusted fallback", func(t *testing.T) {
		primaryRows := research2InvalidatePrices(research2StructuredRows(startedAt, 100), 6)
		fallbackCutoff := startedAt.Add(3 * time.Second)
		fallbackRows := research2StructuredRows(fallbackCutoff, 100)
		collector := newResearch2StructuredCollector(t, startedAt, primaryRows, 100, 5)
		fallbackCalls := 0
		collector.fetchFallback = func(context.Context) (research2FullMarketSnapshot, error) {
			fallbackCalls++
			return research2FullMarketSnapshot{Rows: fallbackRows, Reported: 100, SourceID: "research2:market:tencent",
				SourceName: "腾讯全市场降级快照", SourceRef: "tencent", CollectedAt: fallbackCutoff.Add(time.Second)}, nil
		}
		evidence, err := collector.Collect(context.Background(), startedAt)
		if err != nil || evidence.CoveragePct != 100 || fallbackCalls != 1 || !evidence.CutoffAt.Equal(fallbackCutoff) {
			t.Fatalf("trusted fallback not selected: coverage=%v cutoff=%s calls=%d err=%v", evidence.CoveragePct, evidence.CutoffAt, fallbackCalls, err)
		}
	})
}

func TestResearch2StructuredEvidenceExcludesCandidateWithFewerThanFourBars(t *testing.T) {
	cutoff := time.Date(2026, 9, 3, 9, 50, 0, 0, shanghaiDataLocation())
	collector := newResearch2StructuredCollector(t, cutoff, research2StructuredRows(cutoff, 1), 1, 3)
	evidence, err := collector.Collect(context.Background(), cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence.Candidates) != 0 || len(evidence.CandidateReferencePrices) != 0 || !evidence.Degraded {
		t.Fatalf("candidate without four bars remained recommendable: %+v", evidence)
	}
	var compact research2CompactSnapshot
	if err := json.Unmarshal([]byte(evidence.Prompt), &compact); err != nil || len(compact.Candidates) != 1 || compact.Candidates[0].CoreEligible {
		t.Fatalf("ineligible candidate was not retained for audit: compact=%+v err=%v", compact, err)
	}
}

func TestResearch2DocumentStatusDistinguishesNestedEmptyAndAfterCutoff(t *testing.T) {
	cutoff := time.Date(2026, 9, 3, 9, 50, 0, 0, shanghaiDataLocation())
	available, after := cutoff, cutoff.Add(time.Second)
	empty := research.SourceDocument{Content: `{"code":0,"data":{"result":{"items":[]}}}`, AvailableAt: &available}
	late := research.SourceDocument{Content: `[{"name":"value"}]`, AvailableAt: &after}
	failed := research.SourceDocument{Content: `{"error":true}`, Error: "provider failed", AvailableAt: &available}
	if status := research2DocumentStatus(empty, cutoff, true); status != marketdata.StatusEmpty {
		t.Fatalf("nested empty status=%s", status)
	}
	if status := research2DocumentStatus(late, cutoff, true); status != marketdata.StatusAfterCutoff {
		t.Fatalf("after-cutoff status=%s", status)
	}
	if status := research2DocumentStatus(failed, cutoff, true); status != marketdata.StatusFailed {
		t.Fatalf("failed status=%s", status)
	}
}

func TestSanitizeResearch2MinuteBarsUsesFiveClosedBuckets(t *testing.T) {
	cutoff := time.Date(2026, 9, 3, 10, 14, 23, 0, shanghaiDataLocation())
	end := cutoff.Truncate(time.Minute)
	start := end.Add(-5 * time.Minute)
	bars := make([]minuteBar, 0, 7)
	for index := -1; index <= 5; index++ {
		at := start.Add(time.Duration(index) * time.Minute)
		bars = append(bars, minuteBar{TradeTime: at, Open: 10, High: 10.1, Low: 9.9, Close: 10, Volume: 100})
	}
	clean := sanitizeResearch2MinuteBars(bars, "fixture", start, end)
	if len(clean) != 5 || !clean[0].TradeTime.Equal(start) || !clean[4].TradeTime.Equal(end.Add(-time.Minute)) {
		t.Fatalf("closed window mismatch: %+v", clean)
	}
	for _, bar := range clean {
		if !bar.TradeTime.Before(end) {
			t.Fatalf("cutoff bucket leaked into evidence: %s", bar.TradeTime)
		}
	}
}

func TestResearch2VWAPNormalizesLotVolumeAndRejectsImplausibleRatio(t *testing.T) {
	bars := []minuteBar{
		{Open: 10, High: 10.2, Low: 9.9, Close: 10.1, Volume: 100, Amount: 101000},
		{Open: 10.1, High: 10.3, Low: 10, Close: 10.2, Volume: 100, Amount: 102000},
	}
	metrics := calculateResearch2CompactMetrics(research2MarketRow{}, bars)
	if metrics.VWAP == nil || math.Abs(*metrics.VWAP-10.15) > 0.001 || metrics.VWAPMethod != "amount_divided_by_lot_volume_times_100" {
		t.Fatalf("lot-volume VWAP not normalized: %+v", metrics)
	}
	for index := range bars {
		bars[index].Amount = 1
	}
	metrics = calculateResearch2CompactMetrics(research2MarketRow{}, bars)
	if metrics.VWAP == nil || *metrics.VWAP < 9.9 || *metrics.VWAP > 10.3 || metrics.VWAPMethod != "volume_weighted_minute_close_proxy" {
		t.Fatalf("implausible amount ratio was trusted: %+v", metrics)
	}
}

func TestFilterResearch2JSONDoesNotLaunderUntimestampedSibling(t *testing.T) {
	cutoff := time.Date(2026, 9, 3, 10, 14, 0, 0, shanghaiDataLocation())
	payload := map[string]any{
		"currentUntimestamped": float64(999),
		"history":              []any{map[string]any{"publishedAt": cutoff.Add(-time.Hour).Format(time.RFC3339), "title": "old"}},
	}
	filtered, _, proved, keep := filterResearch2JSONAtCutoff(payload, cutoff)
	result, ok := filtered.(map[string]any)
	if !ok || !proved || !keep {
		t.Fatalf("timestamped history was unexpectedly discarded: %#v", filtered)
	}
	if _, exists := result["currentUntimestamped"]; exists {
		t.Fatalf("old nested timestamp laundered an unproven sibling: %#v", result)
	}
	if _, exists := result["history"]; !exists {
		t.Fatalf("pre-cutoff history missing: %#v", result)
	}
}

func TestResearch2RowsTrustedAtCollectionRequiresTimestampPriceAndSaneTime(t *testing.T) {
	collectedAt := time.Date(2026, 9, 3, 9, 50, 4, 0, shanghaiDataLocation())
	rows := []research2MarketRow{
		{Code: "600001", Name: "测试1", ListingDate: 20200101, Price: 10, Timestamp: collectedAt.Add(-time.Second).Unix()},
		{Code: "600002", Name: "测试2", ListingDate: 20200101, Price: 10},
		{Code: "600003", Name: "测试3", ListingDate: 20200101, Price: 0, Timestamp: collectedAt.Unix()},
		{Code: "600004", Name: "测试4", ListingDate: 20200101, Price: 10, Timestamp: collectedAt.Add(research2QuoteFutureSkew).Unix()},
		{Code: "600005", Name: "测试5", ListingDate: 20200101, Price: 10, Timestamp: collectedAt.Add(research2QuoteFutureSkew + time.Second).Unix()},
	}
	filtered := research2RowsTrustedAtCollection(rows, collectedAt)
	if len(filtered) != 2 || filtered[0].Code != "600001" || filtered[1].Code != "600004" {
		t.Fatalf("unverifiable rows counted toward coverage: %+v", filtered)
	}
}

func TestResearch2CoverageDenominatorExcludesDuplicatesAndNonSelectableRows(t *testing.T) {
	collectedAt := time.Date(2026, 9, 3, 10, 50, 4, 0, shanghaiDataLocation())
	rows := research2StructuredRows(collectedAt, 100)
	rows = append(rows,
		rows[0],
		research2MarketRow{Code: "600900", Name: "退市样本", ListingDate: 20200101},
		research2MarketRow{Code: "600901", Name: "*ST样本", ListingDate: 20200101},
	)
	rows[len(rows)-3].Timestamp = collectedAt.Unix()
	trusted, eligible := research2EligibleCoverageRows(rows, collectedAt)
	if eligible != 100 || len(trusted) != 100 {
		t.Fatalf("coverage universe trusted=%d eligible=%d", len(trusted), eligible)
	}
}

func TestResearch2StructuredEvidenceAcceptsRowsUpdatedWhileSnapshotLoads(t *testing.T) {
	startedAt := time.Date(2026, 9, 3, 9, 50, 0, 0, shanghaiDataLocation())
	rows := research2StructuredRows(startedAt, 5077)
	for index := range rows {
		rows[index].Timestamp = startedAt.Add(time.Duration(index%5) * time.Second).Unix()
	}
	collector := newResearch2StructuredCollector(t, startedAt, rows, 5077, 5)
	collector.fetchSnapshot = func(context.Context, time.Time) (research2FullMarketSnapshot, error) {
		return research2FullMarketSnapshot{Rows: rows, Reported: 5077, SourceID: "fixture-market", SourceName: "测试全市场",
			SourceRef: "fixture-market", CollectedAt: startedAt.Add(4 * time.Second)}, nil
	}
	evidence, err := collector.Collect(context.Background(), startedAt)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.CoveragePct != 100 || !evidence.CutoffAt.Equal(startedAt.Add(4*time.Second)) {
		t.Fatalf("post-start snapshot rows were rejected: coverage=%v cutoff=%s", evidence.CoveragePct, evidence.CutoffAt)
	}
	if !evidence.WindowStartAt.Equal(time.Date(2026, 9, 3, 9, 45, 0, 0, shanghaiDataLocation())) {
		t.Fatalf("window start=%s", evidence.WindowStartAt)
	}
}

func TestResearch2StructuredEvidenceUsesActualCutoffAcrossMinuteBoundary(t *testing.T) {
	startedAt := time.Date(2026, 9, 3, 9, 49, 59, 0, shanghaiDataLocation())
	actualCutoff := time.Date(2026, 9, 3, 9, 50, 2, 0, shanghaiDataLocation())
	rows := research2StructuredRows(actualCutoff, 20)
	collector := newResearch2StructuredCollector(t, startedAt, rows, 20, 5)
	collector.fetchSnapshot = func(context.Context, time.Time) (research2FullMarketSnapshot, error) {
		return research2FullMarketSnapshot{Rows: rows, Reported: 20, SourceID: "fixture-market", SourceName: "测试全市场",
			SourceRef: "fixture-market", CollectedAt: actualCutoff.Add(time.Second)}, nil
	}
	evidence, err := collector.Collect(context.Background(), startedAt)
	if err != nil {
		t.Fatal(err)
	}
	wantStart := time.Date(2026, 9, 3, 9, 45, 0, 0, shanghaiDataLocation())
	if !evidence.CutoffAt.Equal(actualCutoff) || !evidence.WindowStartAt.Equal(wantStart) {
		t.Fatalf("actual cutoff/window mismatch: cutoff=%s window=%s", evidence.CutoffAt, evidence.WindowStartAt)
	}
}

type blockingResearch2MinuteProvider struct{}

func (blockingResearch2MinuteProvider) Window(ctx context.Context, _ string, _, _ time.Time) ([]minuteBar, string, error) {
	<-ctx.Done()
	return nil, "blocking", ctx.Err()
}

func TestCollectResearch2CandidateWindowsHonorsContext(t *testing.T) {
	start := time.Date(2026, 9, 3, 9, 45, 0, 0, shanghaiDataLocation())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result := collectResearch2CandidateWindows(ctx, blockingResearch2MinuteProvider{}, []research.StockCandidate{{Code: "sh600001"}},
		[]research2MarketRow{{Code: "600001", Price: 10}}, start, start.Add(5*time.Minute))
	if len(result) != 1 || result[0].Error == nil {
		t.Fatalf("blocked provider did not return deadline evidence: %+v", result)
	}
}
