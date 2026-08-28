package themes

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPublicThemeReadsUseOneGlobalDateAndHideFutureFrozenSnapshots(t *testing.T) {
	repository, base := newThemeTestRepository(t)
	ctx := context.Background()
	_, err := repository.UpsertTheme(ctx, UpsertThemeRequest{ID: "theme-a", CanonicalName: "主题A"})
	require.NoError(t, err)
	_, err = repository.UpsertTheme(ctx, UpsertThemeRequest{ID: "theme-b", CanonicalName: "主题B"})
	require.NoError(t, err)

	readAt := base.Add(36 * time.Hour)
	_, err = repository.FreezeSnapshot(ctx, FreezeSnapshotRequest{ThemeID: "theme-a", TradeDate: "2026-08-27", CycleNo: 1, LifecycleStage: StageObserve,
		Rank: 2, HeatScore: 40, ObservedAt: base, FrozenAt: base.Add(time.Minute)})
	require.NoError(t, err)
	_, err = repository.FreezeSnapshot(ctx, FreezeSnapshotRequest{ThemeID: "theme-a", TradeDate: "2026-08-29", CycleNo: 1, LifecycleStage: StageFerment,
		Rank: 1, HeatScore: 90, ObservedAt: readAt.Add(time.Hour), FrozenAt: readAt.Add(time.Hour)})
	require.NoError(t, err)
	_, err = repository.FreezeSnapshot(ctx, FreezeSnapshotRequest{ThemeID: "theme-b", TradeDate: "2026-08-28", CycleNo: 1, LifecycleStage: StageObserve,
		Rank: 1, HeatScore: 80, ObservedAt: readAt.Add(-time.Hour), FrozenAt: readAt.Add(-time.Minute)})
	require.NoError(t, err)

	service := NewService(repository)
	service.now = func() time.Time { return readAt }
	list, err := service.ListThemes(ctx, ListThemesRequest{Limit: 20})
	require.NoError(t, err)
	require.Equal(t, "2026-08-28", list.Data.TradeDate)
	require.Len(t, list.Data.Items, 1)
	require.Equal(t, "theme-b", list.Data.Items[0].ID)
	require.Equal(t, "2026-08-28", list.Data.Items[0].Snapshot.TradeDate)

	latestA, err := service.GetTheme(ctx, "theme-a", "")
	require.NoError(t, err)
	require.NotNil(t, latestA.Data.Snapshot)
	require.Equal(t, "2026-08-27", latestA.Data.Snapshot.TradeDate)
	futureA, err := service.GetTheme(ctx, "theme-a", "2026-08-29")
	require.NoError(t, err)
	require.Nil(t, futureA.Data.Snapshot)
	history, err := service.ListSnapshots(ctx, ListSnapshotsRequest{ThemeID: "theme-a", Limit: 20})
	require.NoError(t, err)
	require.Len(t, history.Data.Items, 1)
	require.Equal(t, "2026-08-27", history.Data.Items[0].TradeDate)

	internal, err := repository.GetTheme(ctx, "theme-a", "")
	require.NoError(t, err)
	require.NotNil(t, internal.Snapshot)
	require.Equal(t, "2026-08-29", internal.Snapshot.TradeDate)
}

func TestListThemesExplicitDateStillHonorsReadCutoff(t *testing.T) {
	repository, base := newThemeTestRepository(t)
	ctx := context.Background()
	_, err := repository.UpsertTheme(ctx, UpsertThemeRequest{ID: "theme-future", CanonicalName: "未来主题"})
	require.NoError(t, err)
	readAt := base.Add(time.Hour)
	_, err = repository.FreezeSnapshot(ctx, FreezeSnapshotRequest{ThemeID: "theme-future", TradeDate: "2026-08-28", CycleNo: 1, LifecycleStage: StageObserve,
		Rank: 1, HeatScore: 80, ObservedAt: readAt.Add(time.Hour), FrozenAt: readAt.Add(time.Hour)})
	require.NoError(t, err)
	service := NewService(repository)
	service.now = func() time.Time { return readAt }
	list, err := service.ListThemes(ctx, ListThemesRequest{Date: "2026-08-28", Limit: 20})
	require.NoError(t, err)
	require.Equal(t, "2026-08-28", list.Data.TradeDate)
	require.Empty(t, list.Data.Items)
}
