package data

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"go-stock/backend/marketdata"
	"go-stock/backend/themes"
	"go-stock/internal/migrations"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestThemeLifecycleRuntimeAdvancesDeterministicStages(t *testing.T) {
	base := time.Date(2026, 8, 24, 1, 30, 0, 0, time.UTC)
	byDate := map[string][]RawThemeSignal{}
	for day := 0; day < 6; day++ {
		observedAt := base.AddDate(0, 0, day)
		tradeDate := themeLifecycleTradeDate(observedAt)
		heat := []float64{60, 55, 80, 80, 25, 55}[day]
		count := 1
		if day == 1 || day == 2 || day == 3 || day == 5 {
			count = 2
		}
		for sourceIndex := 0; sourceIndex < count; sourceIndex++ {
			stance := ThemeSignalSupports
			if day == 3 && sourceIndex == 1 {
				stance = ThemeSignalContradicts
			}
			signal := lifecycleRuntimeNewsSignal(observedAt, sourceIndex, heat, stance)
			if day == 3 {
				// Both claims describe the same event and affected security; source
				// coverage differences belong in separate event identities.
				signal.Securities = []RawThemeSecurity{{AssetType: "stock", Market: "SH", Code: "600001", Name: "机器人公司", Role: "representative"}}
			}
			byDate[tradeDate] = append(byDate[tradeDate], signal)
		}
	}
	adapter := SourceAdapterFunc{SourceName: "stage-fixture", CollectFunc: func(_ context.Context, observedAt time.Time) ([]RawThemeSignal, error) {
		return append([]RawThemeSignal(nil), byDate[themeLifecycleTradeDate(observedAt)]...), nil
	}}
	fixture := newThemeLifecycleRuntimeFixture(t, adapter)
	wantStages := []themes.LifecycleStage{
		themes.StageObserve, themes.StageFerment, themes.StageAccelerate,
		themes.StageDiverge, themes.StageFade, themes.StageObserve,
	}
	wantCycles := []int{1, 1, 1, 1, 1, 2}
	var themeID string
	for day := range wantStages {
		observedAt := base.AddDate(0, 0, day)
		fixture.now = observedAt.Add(time.Minute)
		result, err := fixture.runtime.CollectAndFreeze(context.Background(), observedAt)
		require.NoError(t, err)
		require.Equal(t, marketdata.StatusOK, result.Status)
		require.Len(t, result.Themes, 1)
		require.Equal(t, wantStages[day], result.Themes[0].LifecycleStage, "day %d", day+1)
		require.Equal(t, wantCycles[day], result.Themes[0].CycleNo, "day %d", day+1)
		if themeID == "" {
			themeID = result.Themes[0].ThemeID
		} else {
			require.Equal(t, themeID, result.Themes[0].ThemeID)
		}
	}

	conflictDate := themeLifecycleTradeDate(base.AddDate(0, 0, 3))
	conflictSnapshot, err := fixture.repository.SnapshotForDate(context.Background(), themeID, conflictDate, nil)
	require.NoError(t, err)
	require.Equal(t, 1, conflictSnapshot.ConflictingCatalystCount)
	require.Equal(t, 1, conflictSnapshot.CatalystCount)
}

func TestNextThemeLifecycleRules(t *testing.T) {
	base := themes.DailySnapshot{CycleNo: 1, Rank: 1, HeatScore: 90}
	tests := []struct {
		name      string
		previous  *themes.DailySnapshot
		heat      float64
		rank      int
		sources   int
		conflict  bool
		wantCycle int
		wantStage themes.LifecycleStage
	}{
		{name: "new starts observing", heat: 100, rank: 1, sources: 3, wantCycle: 1, wantStage: themes.StageObserve},
		{name: "observe needs independent sources", previous: lifecycleSnapshot(base, themes.StageObserve), heat: 80, rank: 1, sources: 1, wantCycle: 1, wantStage: themes.StageObserve},
		{name: "observe ferments at threshold", previous: lifecycleSnapshot(base, themes.StageObserve), heat: 50, rank: 20, sources: 2, wantCycle: 1, wantStage: themes.StageFerment},
		{name: "ferment rank gate", previous: lifecycleSnapshot(base, themes.StageFerment), heat: 90, rank: 11, sources: 3, wantCycle: 1, wantStage: themes.StageFerment},
		{name: "ferment accelerates", previous: lifecycleSnapshot(base, themes.StageFerment), heat: 75, rank: 10, sources: 2, wantCycle: 1, wantStage: themes.StageAccelerate},
		{name: "accelerate diverges on heat drop", previous: lifecycleSnapshot(base, themes.StageAccelerate), heat: 75, rank: 1, sources: 2, wantCycle: 1, wantStage: themes.StageDiverge},
		{name: "accelerate diverges on conflict", previous: lifecycleSnapshot(base, themes.StageAccelerate), heat: 90, rank: 1, sources: 2, conflict: true, wantCycle: 1, wantStage: themes.StageDiverge},
		{name: "diverge fades below forty", previous: lifecycleSnapshot(base, themes.StageDiverge), heat: 39.99, rank: 1, sources: 2, wantCycle: 1, wantStage: themes.StageFade},
		{name: "fade starts next cycle", previous: lifecycleSnapshot(base, themes.StageFade), heat: 50, rank: 1, sources: 2, wantCycle: 2, wantStage: themes.StageObserve},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cycle, stage := nextThemeLifecycle(test.previous, test.heat, test.rank, test.sources, test.conflict)
			require.Equal(t, test.wantCycle, cycle)
			require.Equal(t, test.wantStage, stage)
		})
	}
}

func TestThemeLifecycleRuntimeSameDayIsIdempotent(t *testing.T) {
	observedAt := time.Date(2026, 8, 28, 1, 30, 0, 0, time.UTC)
	adapter := SourceAdapterFunc{SourceName: "idempotency", CollectFunc: func(_ context.Context, at time.Time) ([]RawThemeSignal, error) {
		return []RawThemeSignal{lifecycleRuntimeNewsSignal(at, 0, 65, ThemeSignalSupports)}, nil
	}}
	fixture := newThemeLifecycleRuntimeFixture(t, adapter)
	fixture.now = observedAt.Add(time.Minute)
	first, err := fixture.runtime.CollectAndFreeze(context.Background(), observedAt)
	require.NoError(t, err)
	require.Len(t, first.Themes, 1)

	fixture.now = observedAt.Add(10 * time.Minute)
	second, err := fixture.runtime.CollectAndFreeze(context.Background(), observedAt.Add(5*time.Minute))
	require.NoError(t, err)
	require.Len(t, second.Themes, 1)
	require.True(t, second.Themes[0].Existing)
	require.Equal(t, first.Themes[0].SnapshotID, second.Themes[0].SnapshotID)

	catalysts, err := fixture.repository.ListCatalysts(context.Background(), themes.ListCatalystsRequest{ThemeID: first.Themes[0].ThemeID})
	require.NoError(t, err)
	require.Len(t, catalysts, 1, "same-day replay must not append an orphan catalyst")
	var snapshotCount int64
	require.NoError(t, fixture.database.Table("market_theme_daily_snapshots").Count(&snapshotCount).Error)
	require.EqualValues(t, 1, snapshotCount)
}

func TestThemeLifecycleRuntimeConceptAndFundFlowDoNotCreateCatalysts(t *testing.T) {
	observedAt := time.Date(2026, 8, 28, 2, 0, 0, 0, time.UTC)
	adapter := SourceAdapterFunc{SourceName: "background-only", CollectFunc: func(context.Context, time.Time) ([]RawThemeSignal, error) {
		return []RawThemeSignal{
			{ThemeName: "AI 算力", Aliases: []string{"AI算力"}, Kind: ThemeSignalConcept, Title: "概念成分", HeatScore: 45, SourceName: "东方财富概念信息",
				Securities: []RawThemeSecurity{{AssetType: "stock", Market: "SH", Code: "600001", Name: "算力甲"}}},
			{ThemeName: "AI算力", Aliases: []string{"AI 算力"}, Kind: ThemeSignalFundFlow, Title: "概念资金净流入", HeatScore: 70, SourceName: "新浪概念资金流",
				Securities: []RawThemeSecurity{{AssetType: "stock", Market: "SZ", Code: "000002", Name: "算力乙"}}},
		}, nil
	}}
	fixture := newThemeLifecycleRuntimeFixture(t, adapter)
	fixture.now = observedAt.Add(time.Minute)
	result, err := fixture.runtime.CollectAndFreeze(context.Background(), observedAt)
	require.NoError(t, err)
	require.Len(t, result.Themes, 1, "NormalizeName and aliases must merge the two signals")

	snapshot, err := fixture.repository.SnapshotForDate(context.Background(), result.Themes[0].ThemeID, result.TradeDate, nil)
	require.NoError(t, err)
	require.Zero(t, snapshot.CatalystCount)
	require.Empty(t, snapshot.CatalystIDs)
	require.Len(t, snapshot.Constituents, 2)
	require.Greater(t, snapshot.HeatScore, 0.0)
	catalysts, err := fixture.repository.ListCatalysts(context.Background(), themes.ListCatalystsRequest{ThemeID: result.Themes[0].ThemeID})
	require.NoError(t, err)
	require.Empty(t, catalysts)
}

func TestThemeLifecycleRuntimeFreezesUsableDataWhenOneSourceFails(t *testing.T) {
	observedAt := time.Date(2026, 8, 28, 2, 30, 0, 0, time.UTC)
	failed := SourceAdapterFunc{SourceName: "failed", CollectFunc: func(context.Context, time.Time) ([]RawThemeSignal, error) {
		return nil, errors.New("fixture rate limited")
	}}
	usable := SourceAdapterFunc{SourceName: "usable", CollectFunc: func(_ context.Context, at time.Time) ([]RawThemeSignal, error) {
		return []RawThemeSignal{lifecycleRuntimeNewsSignal(at, 0, 60, ThemeSignalSupports)}, nil
	}}
	fixture := newThemeLifecycleRuntimeFixture(t, failed, usable)
	fixture.now = observedAt.Add(time.Minute)
	result, err := fixture.runtime.CollectAndFreeze(context.Background(), observedAt)
	require.NoError(t, err, "a source error is represented in RunResult, not returned as a fatal persistence error")
	require.Equal(t, marketdata.StatusPartial, result.Status)
	require.Len(t, result.SourceErrors, 1)
	require.Len(t, result.FrozenSnapshotIDs, 1)
	require.Len(t, result.Themes, 1)
}

func TestThemeLifecycleRuntimeAllUnavailableCreatesNothing(t *testing.T) {
	failed := SourceAdapterFunc{SourceName: "failed", CollectFunc: func(context.Context, time.Time) ([]RawThemeSignal, error) {
		return nil, errors.New("offline")
	}}
	fixture := newThemeLifecycleRuntimeFixture(t, failed)
	observedAt := time.Date(2026, 8, 28, 3, 0, 0, 0, time.UTC)
	fixture.now = observedAt.Add(time.Minute)
	result, err := fixture.runtime.CollectAndFreeze(context.Background(), observedAt)
	require.NoError(t, err)
	require.Equal(t, marketdata.StatusUnavailable, result.Status)
	require.Empty(t, result.FrozenSnapshotIDs)
	var themeCount int64
	require.NoError(t, fixture.database.Table("market_themes").Count(&themeCount).Error)
	require.Zero(t, themeCount)
}

func TestThemeLifecycleRuntimeFrozenAtFollowsCompletedEvidence(t *testing.T) {
	observedAt := time.Date(2026, 8, 28, 1, 0, 0, 0, time.UTC)
	publishedAt := observedAt.Add(6 * time.Minute)
	collectedAt := observedAt.Add(7 * time.Minute)
	adapter := SourceAdapterFunc{SourceName: "late-evidence", CollectFunc: func(context.Context, time.Time) ([]RawThemeSignal, error) {
		return []RawThemeSignal{{
			ThemeName: "低空经济", Kind: ThemeSignalNews, EventType: "news", Title: "低空经济政策",
			Summary: "政策细节", EventAt: observedAt, PublishedAt: &publishedAt, FirstObservedAt: observedAt.Add(2 * time.Minute),
			CollectedAt: collectedAt, SourceName: "权威媒体", SourceRef: "https://example.test/late-evidence",
			Stance: ThemeSignalSupports, SourceCredibilityScore: 85, HeatScore: 70,
		}}, nil
	}}
	fixture := newThemeLifecycleRuntimeFixture(t, adapter)
	batchCollectedAt := observedAt.Add(5 * time.Minute)
	fixture.now = observedAt.Add(time.Minute)
	fixture.runtime.Sources.Now = func() time.Time { return batchCollectedAt }

	result, err := fixture.runtime.CollectAndFreeze(context.Background(), observedAt)
	require.NoError(t, err)
	require.Equal(t, collectedAt, result.FrozenAt)
	require.False(t, result.FrozenAt.Before(batchCollectedAt))
	require.False(t, result.FrozenAt.Before(publishedAt))
	require.False(t, result.FrozenAt.Before(collectedAt))

	snapshot, err := fixture.repository.SnapshotForDate(context.Background(), result.Themes[0].ThemeID, result.TradeDate, nil)
	require.NoError(t, err)
	require.Equal(t, result.FrozenAt, snapshot.FrozenAt)
}

func TestThemeLifecycleRuntimeCrossBatchConflictTriggersDivergence(t *testing.T) {
	base := time.Date(2026, 8, 24, 1, 30, 0, 0, time.UTC)
	fixedEventAt := base.AddDate(0, 0, 2).Add(-time.Minute)
	byDate := map[string][]RawThemeSignal{}
	byDate[themeLifecycleTradeDate(base)] = []RawThemeSignal{lifecycleRuntimeNewsSignal(base, 0, 60, ThemeSignalSupports)}
	byDate[themeLifecycleTradeDate(base.AddDate(0, 0, 1))] = []RawThemeSignal{
		lifecycleRuntimeNewsSignal(base.AddDate(0, 0, 1), 0, 60, ThemeSignalSupports),
		lifecycleRuntimeNewsSignal(base.AddDate(0, 0, 1), 1, 60, ThemeSignalSupports),
	}
	support := lifecycleRuntimeNewsSignal(base.AddDate(0, 0, 2), 0, 85, ThemeSignalSupports)
	support.Title = "机器人订单落地"
	support.Summary = "公告确认订单"
	support.EventAt = fixedEventAt
	support.SourceRef = "https://exchange.example/order"
	support.Securities = []RawThemeSecurity{{AssetType: "stock", Market: "SH", Code: "600001", Name: "机器人公司"}}
	byDate[themeLifecycleTradeDate(base.AddDate(0, 0, 2))] = []RawThemeSignal{support}
	contradiction := support
	contradiction.Summary = "媒体质疑订单条件"
	contradiction.EventAt = fixedEventAt.Add(20 * time.Second)
	contradiction.SourceName = "权威媒体"
	contradiction.SourceRef = "https://news.example/order-review"
	contradiction.Stance = ThemeSignalContradicts
	contradiction.HeatScore = 85
	byDate[themeLifecycleTradeDate(base.AddDate(0, 0, 3))] = []RawThemeSignal{contradiction}

	adapter := SourceAdapterFunc{SourceName: "cross-batch", CollectFunc: func(_ context.Context, at time.Time) ([]RawThemeSignal, error) {
		return append([]RawThemeSignal(nil), byDate[themeLifecycleTradeDate(at)]...), nil
	}}
	fixture := newThemeLifecycleRuntimeFixture(t, adapter)
	wantStages := []themes.LifecycleStage{themes.StageObserve, themes.StageFerment, themes.StageAccelerate, themes.StageDiverge}
	var themeID string
	for day, wantStage := range wantStages {
		observedAt := base.AddDate(0, 0, day)
		fixture.now = observedAt.Add(time.Minute)
		result, err := fixture.runtime.CollectAndFreeze(context.Background(), observedAt)
		require.NoError(t, err)
		require.Len(t, result.Themes, 1)
		require.Equal(t, wantStage, result.Themes[0].LifecycleStage, "day %d", day+1)
		themeID = result.Themes[0].ThemeID
	}

	lastDate := themeLifecycleTradeDate(base.AddDate(0, 0, 3))
	snapshot, err := fixture.repository.SnapshotForDate(context.Background(), themeID, lastDate, nil)
	require.NoError(t, err)
	require.Equal(t, 1, snapshot.ConflictingCatalystCount)
	catalysts, err := fixture.repository.ListCatalysts(context.Background(), themes.ListCatalystsRequest{ThemeID: themeID})
	require.NoError(t, err)
	var matched *themes.CatalystEvent
	for index := range catalysts {
		if catalysts[index].Title == support.Title {
			matched = &catalysts[index]
			break
		}
	}
	require.NotNil(t, matched)
	require.Len(t, matched.Claims, 2, "different source summaries must merge as claims on one real event")
	require.True(t, themeLifecycleStoredClaimsConflict(matched.Claims))
}

func TestThemeLifecycleEventIdentityDrivesAggregationAndConflict(t *testing.T) {
	eventAt := time.Date(2026, 8, 28, 2, 30, 10, 0, time.UTC)
	support := RawThemeSignal{
		ThemeName: "机器人", Kind: ThemeSignalNews, EventType: "announcement", Title: "量产公告", Summary: "交易所摘要",
		EventAt: eventAt, SourceName: "交易所公告", SourceRef: "https://exchange.example/one", Stance: ThemeSignalSupports,
		SourceCredibilityScore: 90, Securities: []RawThemeSecurity{{Market: "SZ", Code: "000001"}},
	}
	contradiction := support
	contradiction.Summary = "媒体的不同摘要"
	contradiction.EventAt = eventAt.Add(20 * time.Second)
	contradiction.SourceName = "权威媒体"
	contradiction.SourceRef = "https://news.example/two"
	contradiction.Stance = ThemeSignalContradicts

	require.True(t, hasThemeLifecycleConflict([]RawThemeSignal{support, contradiction}))
	requests := makeThemeLifecycleCatalystRequests("theme-robot", []RawThemeSignal{support, contradiction})
	require.Len(t, requests, 1)
	require.Len(t, requests[0].Claims, 2)
	require.Equal(t, "disputed", requests[0].Status)

	for _, test := range []struct {
		name   string
		mutate func(*RawThemeSignal)
	}{
		{name: "later event", mutate: func(signal *RawThemeSignal) { signal.EventAt = eventAt.Add(time.Minute) }},
		{name: "different entity", mutate: func(signal *RawThemeSignal) { signal.Securities[0].Code = "000002" }},
		{name: "different title", mutate: func(signal *RawThemeSignal) { signal.Title = "另一项量产公告" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			distinct := contradiction
			distinct.Securities = append([]RawThemeSecurity(nil), contradiction.Securities...)
			test.mutate(&distinct)
			require.False(t, hasThemeLifecycleConflict([]RawThemeSignal{support, distinct}))
			require.Len(t, makeThemeLifecycleCatalystRequests("theme-robot", []RawThemeSignal{support, distinct}), 2)
		})
	}
}

type themeLifecycleRuntimeFixture struct {
	database   *gorm.DB
	repository *themes.Repository
	runtime    *ThemeLifecycleRuntime
	now        time.Time
}

func newThemeLifecycleRuntimeFixture(t *testing.T, adapters ...SourceAdapter) *themeLifecycleRuntimeFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "theme-runtime.db")
	database, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, migrations.MigrateMain(database))
	sqlDB, err := database.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	repository := themes.NewRepository(database)
	fixture := &themeLifecycleRuntimeFixture{database: database, repository: repository}
	aggregator := NewThemeSourceAggregator(50*time.Millisecond, adapters...)
	aggregator.Now = func() time.Time { return fixture.now }
	fixture.runtime = NewThemeLifecycleRuntime(aggregator, themes.NewService(repository), repository, func() time.Time { return fixture.now })
	return fixture
}

func lifecycleRuntimeNewsSignal(observedAt time.Time, sourceIndex int, heat float64, stance string) RawThemeSignal {
	source := []string{"财联社电报", "交易所公告"}[sourceIndex%2]
	code := []string{"600001", "000002"}[sourceIndex%2]
	market := []string{"SH", "SZ"}[sourceIndex%2]
	tradeDate := themeLifecycleTradeDate(observedAt)
	return RawThemeSignal{
		ThemeName: "机器人", Aliases: []string{"机器人 概念"}, Kind: ThemeSignalNews, EventType: "industry_event",
		Title: "机器人产业事件 " + tradeDate, Summary: "机器人产业信号 " + tradeDate, EventAt: observedAt.Add(-time.Minute),
		SourceName: source, SourceRef: "https://example.test/" + tradeDate + "/" + market, Stance: stance,
		SourceCredibilityScore: 80, HeatScore: heat,
		Securities: []RawThemeSecurity{{AssetType: "stock", Market: market, Code: code, Name: "机器人公司", Role: "representative"}},
	}
}

func lifecycleSnapshot(base themes.DailySnapshot, stage themes.LifecycleStage) *themes.DailySnapshot {
	copy := base
	copy.LifecycleStage = stage
	return &copy
}
