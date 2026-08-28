package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go-stock/backend/marketdata"
	"go-stock/backend/themes"
)

type fakeThemeAPI struct {
	listEnvelope     marketdata.DataEnvelope[themes.ThemeListData]
	themeEnvelope    marketdata.DataEnvelope[themes.Theme]
	snapshotEnvelope marketdata.DataEnvelope[themes.SnapshotListData]
	catalystEnvelope marketdata.DataEnvelope[themes.CatalystListData]
	listErr          error
	themeErr         error
	snapshotErr      error
	catalystErr      error
	listCalls        int
	themeCalls       int
	snapshotCalls    int
	catalystCalls    int
	lastListRequest  themes.ListThemesRequest
	lastSnapshot     themes.ListSnapshotsRequest
	lastCatalyst     themes.ListCatalystsRequest
	lastThemeID      string
	lastThemeDate    string
}

func (f *fakeThemeAPI) ListThemes(_ context.Context, request themes.ListThemesRequest) (marketdata.DataEnvelope[themes.ThemeListData], error) {
	f.listCalls++
	f.lastListRequest = request
	return f.listEnvelope, f.listErr
}
func (f *fakeThemeAPI) GetTheme(_ context.Context, id, date string) (marketdata.DataEnvelope[themes.Theme], error) {
	f.themeCalls++
	f.lastThemeID, f.lastThemeDate = id, date
	return f.themeEnvelope, f.themeErr
}
func (f *fakeThemeAPI) ListSnapshots(_ context.Context, request themes.ListSnapshotsRequest) (marketdata.DataEnvelope[themes.SnapshotListData], error) {
	f.snapshotCalls++
	f.lastSnapshot = request
	return f.snapshotEnvelope, f.snapshotErr
}
func (f *fakeThemeAPI) ListCatalysts(_ context.Context, request themes.ListCatalystsRequest) (marketdata.DataEnvelope[themes.CatalystListData], error) {
	f.catalystCalls++
	f.lastCatalyst = request
	return f.catalystEnvelope, f.catalystErr
}

func TestThemeListRouteUsesFlatContractAndPublicStatus(t *testing.T) {
	fixtureTime := time.Date(2026, 8, 28, 2, 0, 0, 0, time.UTC)
	snapshot := themes.DailySnapshot{ID: "snapshot-1", ThemeID: "theme-ai", TradeDate: "2026-08-28", CycleNo: 1, LifecycleStage: themes.StageFerment,
		Rank: 1, HeatScore: 88.5, Summary: "算力催化", ConstituentCount: 1, CatalystCount: 2, ConflictingCatalystCount: 1,
		ObservedAt: fixtureTime, FrozenAt: fixtureTime.Add(time.Minute)}
	fake := &fakeThemeAPI{
		listEnvelope: envelopeFixture(themes.ThemeListData{TradeDate: "2026-08-28", Items: []themes.ThemeListItem{{ID: "theme-ai", CanonicalName: "人工智能", Snapshot: &snapshot,
			PreviousLifecycleStage: stagePointer(themes.StageObserve), StageChanged: true, RepresentativeSecurities: []themes.RepresentativeSecurity{{AssetType: "stock", Market: "SH", Code: "sh600000", Name: "样本", Role: "龙头"}}}}}, marketdata.StatusEmpty, fixtureTime),
		themeEnvelope: envelopeFixture(themes.Theme{ID: "theme-ai", CanonicalName: "人工智能", Aliases: []themes.ThemeAlias{{Alias: "AI"}, {Alias: "算力"}}, Snapshot: &snapshot}, marketdata.StatusOK, fixtureTime),
	}
	mux := themeTestMux(t, fake)
	response := performThemeRequest(mux, "/api/v1/themes?date=2026-08-28&stage=%E5%8F%91%E9%85%B5&sort=heat&limit=5")
	require.Equal(t, http.StatusOK, response.Code)
	body := decodeThemeBody(t, response)
	require.Equal(t, "ok", body["status"])
	data := body["data"].(map[string]any)
	require.Equal(t, "2026-08-28", data["tradeDate"])
	item := data["items"].([]any)[0].(map[string]any)
	require.Equal(t, "theme-ai", item["themeId"])
	require.Equal(t, "人工智能", item["name"])
	require.Equal(t, []any{"AI", "算力"}, item["aliases"])
	require.NotContains(t, item, "canonicalName")
	require.NotContains(t, item, "snapshot")
	require.Equal(t, "snapshot-1", item["snapshotId"])
	require.Equal(t, true, item["stageChanged"])
	require.Equal(t, "观察", item["previousLifecycleStage"])
	require.Equal(t, "发酵", string(fake.lastListRequest.Stage))
	require.Equal(t, "heat", fake.lastListRequest.Sort)
	require.Equal(t, 5, fake.lastListRequest.Limit)
}

func TestThemeListEmptyMapsInternalEmptyToPublicOK(t *testing.T) {
	fixtureTime := time.Date(2026, 8, 28, 2, 0, 0, 0, time.UTC)
	fake := &fakeThemeAPI{listEnvelope: marketdata.DataEnvelope[themes.ThemeListData]{Data: themes.ThemeListData{Items: []themes.ThemeListItem{}}, Source: "theme_repository", FetchedAt: fixtureTime,
		Status: marketdata.StatusEmpty, Errors: []marketdata.DataError{}, Sources: []marketdata.SourceState{{Provider: "fixture", Status: marketdata.StatusEmpty}}}}
	response := performThemeRequest(themeTestMux(t, fake), "/api/v1/themes")
	require.Equal(t, http.StatusOK, response.Code)
	body := decodeThemeBody(t, response)
	require.Equal(t, "ok", body["status"])
	require.Empty(t, body["data"].(map[string]any)["items"])
	require.Equal(t, "ok", body["sources"].([]any)[0].(map[string]any)["status"])
}

func TestThemeDetailMissingSnapshotIs200WithWarning(t *testing.T) {
	fixtureTime := time.Date(2026, 8, 28, 2, 0, 0, 0, time.UTC)
	fake := &fakeThemeAPI{themeEnvelope: envelopeFixture(themes.Theme{ID: "theme-ai", CanonicalName: "人工智能", Description: "主题说明", Status: "active", Aliases: []themes.ThemeAlias{{Alias: "AI"}}}, marketdata.StatusOK, fixtureTime)}
	response := performThemeRequest(themeTestMux(t, fake), "/api/v1/themes/theme-ai?date=2026-08-27")
	require.Equal(t, http.StatusOK, response.Code)
	body := decodeThemeBody(t, response)
	data := body["data"].(map[string]any)
	require.Nil(t, data["snapshot"])
	require.Empty(t, data["constituents"])
	require.NotEmpty(t, body["warnings"])
	require.Equal(t, 0, fake.catalystCalls)
}

func TestThemeDailySnapshotsResolveAliasToPublicThemeID(t *testing.T) {
	fixtureTime := time.Date(2026, 8, 28, 2, 0, 0, 0, time.UTC)
	fake := &fakeThemeAPI{
		themeEnvelope:    envelopeFixture(themes.Theme{ID: "theme-ai", CanonicalName: "人工智能", Status: "active"}, marketdata.StatusOK, fixtureTime),
		snapshotEnvelope: envelopeFixture(themes.SnapshotListData{Items: []themes.DailySnapshot{{ID: "snapshot-1", TradeDate: "2026-08-28", CycleNo: 1, LifecycleStage: themes.StageObserve, Rank: 1, FrozenAt: fixtureTime}}}, marketdata.StatusOK, fixtureTime),
	}
	response := performThemeRequest(themeTestMux(t, fake), "/api/v1/themes/AI/daily-snapshots?from=2026-08-01&to=2026-08-28&limit=30")
	require.Equal(t, http.StatusOK, response.Code)
	data := decodeThemeBody(t, response)["data"].(map[string]any)
	require.Equal(t, "theme-ai", data["themeId"])
	require.Equal(t, "theme-ai", fake.lastSnapshot.ThemeID)
	require.Equal(t, 1, fake.snapshotCalls)
}

func TestThemeCatalystsMapClaimsToSources(t *testing.T) {
	fixtureTime := time.Date(2026, 8, 28, 2, 0, 0, 0, time.UTC)
	available := fixtureTime.Add(time.Minute)
	snapshot := &themes.DailySnapshot{ID: "snapshot-1", ThemeID: "theme-ai", TradeDate: "2026-08-28", CycleNo: 1, LifecycleStage: themes.StageObserve, Rank: 1, FrozenAt: available}
	event := themes.CatalystEvent{ID: "event-1", EventType: "news", Title: "产业消息", Summary: "出现分歧", EventAt: fixtureTime, FirstAvailableAt: &available,
		CredibilityScore: 80, Status: "disputed", Claims: []themes.SourceClaim{
			{ID: "claim-1", SourceName: "公告", SourceRef: "https://example.test/1", Stance: "supports", SourceCredibilityScore: 90, Summary: "支持", AvailableAt: &available, CollectedAt: available, EvidenceItemIDs: []string{"evidence-2", "evidence-1"}},
			{ID: "claim-2", SourceName: "媒体", SourceRef: "https://example.test/2", Stance: "contradicts", SourceCredibilityScore: 70, Summary: "反对", AvailableAt: &available, CollectedAt: available},
		}}
	fake := &fakeThemeAPI{
		themeEnvelope:    envelopeFixture(themes.Theme{ID: "theme-ai", CanonicalName: "人工智能", Status: "active", Snapshot: snapshot}, marketdata.StatusOK, fixtureTime),
		catalystEnvelope: envelopeFixture(themes.CatalystListData{Items: []themes.CatalystEvent{event}}, marketdata.StatusOK, fixtureTime),
	}
	response := performThemeRequest(themeTestMux(t, fake), "/api/v1/themes/theme-ai/catalysts?status=disputed&minCredibility=70&limit=10")
	require.Equal(t, http.StatusOK, response.Code)
	data := decodeThemeBody(t, response)["data"].(map[string]any)
	require.Equal(t, "theme-ai", data["themeId"])
	require.Equal(t, "2026-08-28", data["tradeDate"])
	item := data["items"].([]any)[0].(map[string]any)
	require.Equal(t, "event-1", item["catalystEventId"])
	require.Equal(t, true, item["hasConflict"])
	require.NotContains(t, item, "claims")
	source := item["sources"].([]any)[0].(map[string]any)
	require.Equal(t, "claim-1", source["sourceClaimId"])
	require.Equal(t, []any{"evidence-2", "evidence-1"}, source["evidenceItemIds"])
	require.Equal(t, 70, fake.lastCatalyst.MinCredibility)
	require.NotNil(t, fake.lastCatalyst.Cutoff)
	require.True(t, fake.lastCatalyst.Cutoff.Equal(snapshot.FrozenAt))
}

func TestThemeCatalystCutoffUsesLatestSnapshotAndIncludesEquality(t *testing.T) {
	fixtureTime := time.Date(2026, 8, 28, 2, 0, 0, 0, time.UTC)
	frozenAt := fixtureTime.Add(10 * time.Minute)
	afterFrozen := frozenAt.Add(time.Nanosecond)
	firstAvailable := frozenAt.Add(-time.Minute)
	snapshot := &themes.DailySnapshot{ID: "snapshot-latest", ThemeID: "theme-ai", TradeDate: "2026-08-28", CycleNo: 1, LifecycleStage: themes.StageObserve, Rank: 1, FrozenAt: frozenAt}
	event := themes.CatalystEvent{ID: "event-1", EventType: "news", Title: "催化", EventAt: fixtureTime, FirstAvailableAt: &firstAvailable, CredibilityScore: 80, Status: "active",
		Claims: []themes.SourceClaim{
			{ID: "claim-equal", SourceName: "公告", SourceRef: "https://example.test/equal", Stance: "supports", SourceCredibilityScore: 90, Summary: "冻结时刻到达", AvailableAt: &frozenAt, CollectedAt: frozenAt},
			{ID: "claim-after", SourceName: "媒体", SourceRef: "https://example.test/after", Stance: "contradicts", SourceCredibilityScore: 70, Summary: "冻结后到达", AvailableAt: &afterFrozen, CollectedAt: afterFrozen},
		}}
	fake := &fakeThemeAPI{
		themeEnvelope:    envelopeFixture(themes.Theme{ID: "theme-ai", CanonicalName: "人工智能", Status: "active", Snapshot: snapshot}, marketdata.StatusOK, fixtureTime),
		catalystEnvelope: envelopeFixture(themes.CatalystListData{Items: []themes.CatalystEvent{event}}, marketdata.StatusOK, fixtureTime),
	}
	mux := themeTestMux(t, fake)

	response := performThemeRequest(mux, "/api/v1/themes/theme-ai/catalysts")
	require.Equal(t, http.StatusOK, response.Code)
	data := decodeThemeBody(t, response)["data"].(map[string]any)
	require.Equal(t, "2026-08-28", data["tradeDate"])
	sources := data["items"].([]any)[0].(map[string]any)["sources"].([]any)
	require.Len(t, sources, 1)
	require.Equal(t, "claim-equal", sources[0].(map[string]any)["sourceClaimId"])
	require.Equal(t, "", fake.lastThemeDate)
	require.Equal(t, "2026-08-28", fake.lastCatalyst.Date)
	require.NotNil(t, fake.lastCatalyst.Cutoff)
	require.True(t, fake.lastCatalyst.Cutoff.Equal(frozenAt))

	detail := performThemeRequest(mux, "/api/v1/themes/theme-ai")
	require.Equal(t, http.StatusOK, detail.Code)
	summary := decodeThemeBody(t, detail)["data"].(map[string]any)["catalystSummary"].(map[string]any)
	require.EqualValues(t, 1, summary["total"])
	require.EqualValues(t, 1, summary["supports"])
	require.EqualValues(t, 0, summary["contradicts"])
	require.Equal(t, false, summary["hasConflict"])
	require.True(t, fake.lastCatalyst.Cutoff.Equal(frozenAt))
}

func TestThemeCatalystsMissingRequestedSnapshotReturnsEmptyWarning(t *testing.T) {
	fixtureTime := time.Date(2026, 8, 28, 2, 0, 0, 0, time.UTC)
	fake := &fakeThemeAPI{themeEnvelope: envelopeFixture(themes.Theme{ID: "theme-ai", CanonicalName: "人工智能", Status: "active"}, marketdata.StatusOK, fixtureTime)}
	response := performThemeRequest(themeTestMux(t, fake), "/api/v1/themes/theme-ai/catalysts?date=2026-08-27")
	require.Equal(t, http.StatusOK, response.Code)
	body := decodeThemeBody(t, response)
	data := body["data"].(map[string]any)
	require.Equal(t, "2026-08-27", data["tradeDate"])
	require.Empty(t, data["items"])
	require.NotEmpty(t, body["warnings"])
	require.Equal(t, "2026-08-27", fake.lastThemeDate)
	require.Zero(t, fake.catalystCalls)
}

func TestThemeRoutesRejectInvalidParametersBeforeService(t *testing.T) {
	fake := &fakeThemeAPI{}
	mux := themeTestMux(t, fake)
	paths := []string{
		"/api/v1/themes?date=2026-8-28", "/api/v1/themes?stage=未知", "/api/v1/themes?sort=name", "/api/v1/themes?limit=0", "/api/v1/themes?cursor=not-base64!",
		"/api/v1/themes/id/daily-snapshots?from=2026-08-29&to=2026-08-28", "/api/v1/themes/id/daily-snapshots?stage=未知", "/api/v1/themes/id/daily-snapshots?limit=101",
		"/api/v1/themes/id/catalysts?date=bad", "/api/v1/themes/id/catalysts?status=unknown", "/api/v1/themes/id/catalysts?minCredibility=101", "/api/v1/themes/id/catalysts?limit=-1",
	}
	for _, path := range paths {
		response := performThemeRequest(mux, path)
		require.Equal(t, http.StatusBadRequest, response.Code, path)
		body := decodeThemeBody(t, response)
		require.Len(t, body, 1)
		require.Contains(t, body, "error")
	}
	require.Zero(t, fake.listCalls)
	require.Zero(t, fake.themeCalls)
	require.Zero(t, fake.snapshotCalls)
	require.Zero(t, fake.catalystCalls)

	unknown := performThemeRequest(mux, "/api/v1/themes/id/unknown")
	require.Equal(t, http.StatusNotFound, unknown.Code)
}

func TestThemeRoutesReturn404ForUnknownTheme(t *testing.T) {
	fake := &fakeThemeAPI{themeErr: themes.ErrNotFound}
	response := performThemeRequest(themeTestMux(t, fake), "/api/v1/themes/missing")
	require.Equal(t, http.StatusNotFound, response.Code)
	require.Equal(t, map[string]any{"error": "theme not found"}, decodeThemeBody(t, response))
}

func themeTestMux(t *testing.T, service themeAPI) *http.ServeMux {
	t.Helper()
	previous := themeServiceFactory
	themeServiceFactory = func() themeAPI { return service }
	t.Cleanup(func() { themeServiceFactory = previous })
	mux := http.NewServeMux()
	registerThemeRoutes(mux, nil)
	return mux
}

func performThemeRequest(handler http.Handler, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeThemeBody(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body), response.Body.String())
	return body
}

func envelopeFixture[T any](data T, status string, at time.Time) marketdata.DataEnvelope[T] {
	return marketdata.DataEnvelope[T]{Data: data, Source: "theme_repository", AsOf: at, FetchedAt: at, Status: status, Errors: []marketdata.DataError{}, EvidenceProfile: "market-evidence-v2"}
}

func stagePointer(value themes.LifecycleStage) *themes.LifecycleStage { return &value }

var _ = errors.Is
