package themes

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestEventFingerprintIncludesShanghaiTradeDate(t *testing.T) {
	first := time.Date(2026, 8, 27, 3, 0, 0, 0, time.UTC)
	second := first.Add(24 * time.Hour)
	left := EventFingerprint("theme-ai", "policy", "算力新政", "政策发布", first, []string{" SH600000 ", "算力"})
	right := EventFingerprint("theme-ai", "policy", "算力新政", "政策发布", second, []string{"算力", "sh600000"})
	require.NotEqual(t, left, right)
	require.Equal(t, left, EventFingerprint("THEME-AI", "POLICY", "算力 新政", "政策发布", first, []string{"算力", "sh600000"}))
}

func TestLifecycleFreezeIdempotencyAndConflict(t *testing.T) {
	repository, base := newThemeTestRepository(t)
	ctx := context.Background()
	_, err := repository.UpsertTheme(ctx, UpsertThemeRequest{ID: "theme-ai", CanonicalName: "人工智能", Aliases: []AliasInput{{Alias: "AI 概念", Source: "fixture"}}})
	require.NoError(t, err)

	request := FreezeSnapshotRequest{ThemeID: "theme-ai", TradeDate: "2026-08-27", CycleNo: 1, LifecycleStage: StageObserve, Rank: 2, HeatScore: 61.5,
		Summary: "开始观察", ObservedAt: base.Add(time.Minute), FrozenAt: base.Add(2 * time.Minute), Constituents: []SnapshotConstituent{
			{AssetType: "stock", Market: "SH", Code: "sh600000", Name: "浦发银行", Rank: 1},
			{AssetType: "etf", Market: "SH", Code: "sh588000", Name: "科创ETF", Rank: 2},
		}}
	invalidRank := request
	invalidRank.Rank = 0
	_, err = repository.FreezeSnapshot(ctx, invalidRank)
	require.ErrorIs(t, err, ErrInvalidRequest)
	invalidConstituentRank := request
	invalidConstituentRank.Constituents = append([]SnapshotConstituent(nil), request.Constituents...)
	invalidConstituentRank.Constituents[0].Rank = 0
	_, err = repository.FreezeSnapshot(ctx, invalidConstituentRank)
	require.ErrorIs(t, err, ErrInvalidRequest)
	first, err := repository.FreezeSnapshot(ctx, request)
	require.NoError(t, err)
	second, err := repository.FreezeSnapshot(ctx, request)
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
	var snapshotCount int64
	require.NoError(t, repository.db.Model(&snapshotRow{}).Count(&snapshotCount).Error)
	require.EqualValues(t, 1, snapshotCount)

	changed := request
	changed.Summary = "不能覆盖历史"
	_, err = repository.FreezeSnapshot(ctx, changed)
	require.ErrorIs(t, err, ErrSnapshotConflict)

	jump := request
	jump.TradeDate, jump.LifecycleStage = "2026-08-28", StageAccelerate
	jump.ObservedAt, jump.FrozenAt = base.Add(24*time.Hour), base.Add(24*time.Hour+time.Minute)
	_, err = repository.FreezeSnapshot(ctx, jump)
	require.ErrorIs(t, err, ErrInvalidLifecycle)

	next := jump
	next.LifecycleStage = StageFerment
	_, err = repository.FreezeSnapshot(ctx, next)
	require.NoError(t, err)

	newCycle := next
	newCycle.TradeDate, newCycle.CycleNo, newCycle.LifecycleStage = "2026-08-29", 2, StageObserve
	newCycle.ObservedAt, newCycle.FrozenAt = base.Add(48*time.Hour), base.Add(48*time.Hour+time.Minute)
	_, err = repository.FreezeSnapshot(ctx, newCycle)
	require.ErrorIs(t, err, ErrInvalidLifecycle)
}

func TestCatalystSourceDedupConflictAndParallelStances(t *testing.T) {
	repository, base := newThemeTestRepository(t)
	ctx := context.Background()
	_, err := repository.UpsertTheme(ctx, UpsertThemeRequest{ID: "theme-robot", CanonicalName: "机器人"})
	require.NoError(t, err)
	available := base.Add(5 * time.Minute)
	request := IngestCatalystRequest{ID: "event-one", ThemeID: "theme-robot", EventType: "announcement", Title: "量产公告", Summary: "公司披露量产计划",
		EventAt: base, CredibilityScore: 90, Status: "active", EntityKeys: []string{"sz000001"}, Claims: []ClaimInput{{
			ID: "claim-support", SourceName: "交易所公告", SourceRef: "HTTPS://EXAMPLE.COM:443/a?id=1&utm_source=x#top", Stance: "supports",
			SourceCredibilityScore: 95, Summary: "公告支持量产计划", AvailableAt: &available, CollectedAt: available,
		}}}
	event, err := repository.IngestCatalyst(ctx, request)
	require.NoError(t, err)
	require.Len(t, event.Claims, 1)

	request.Claims[0].ID = "ignored-on-idempotency"
	repeated, err := repository.IngestCatalyst(ctx, request)
	require.NoError(t, err)
	require.Equal(t, event.ID, repeated.ID)
	require.Len(t, repeated.Claims, 1)

	conflict := request
	conflict.Claims = append([]ClaimInput(nil), request.Claims...)
	conflict.Claims[0].Summary = "相同链接被改写"
	_, err = repository.IngestCatalyst(ctx, conflict)
	require.ErrorIs(t, err, ErrSourceClaimConflict)

	later := available.Add(time.Hour)
	parallel := request
	parallel.Claims = []ClaimInput{{ID: "claim-contradict", SourceName: "权威媒体", SourceRef: "https://another.example/report", Stance: "contradicts",
		SourceCredibilityScore: 88, Summary: "报道认为量产仍有不确定性", AvailableAt: &later, CollectedAt: later}}
	merged, err := repository.IngestCatalyst(ctx, parallel)
	require.NoError(t, err)
	require.Len(t, merged.Claims, 2)
	require.Equal(t, "supports", merged.Claims[0].Stance)
	require.Equal(t, "contradicts", merged.Claims[1].Stance)
	claimID := "claim-support"
	require.NoError(t, repository.db.Create(&evidenceLinkRow{LinkID: "link-b", ThemeID: "theme-robot", CatalystEventID: &event.ID, SourceClaimID: &claimID, EvidenceItemID: "evidence-b", LinkType: "supports", CreatedAt: base}).Error)
	require.NoError(t, repository.db.Create(&evidenceLinkRow{LinkID: "link-a", ThemeID: "theme-robot", CatalystEventID: &event.ID, SourceClaimID: &claimID, EvidenceItemID: "evidence-a", LinkType: "supports", CreatedAt: base}).Error)
	require.NoError(t, repository.db.Create(&evidenceLinkRow{LinkID: "link-a-neutral", ThemeID: "theme-robot", CatalystEventID: &event.ID, SourceClaimID: &claimID, EvidenceItemID: "evidence-a", LinkType: "neutral", CreatedAt: base}).Error)
	loaded, err := repository.IngestCatalyst(ctx, request)
	require.NoError(t, err)
	require.Equal(t, []string{"evidence-a", "evidence-b"}, loaded.Claims[0].EvidenceItemIDs)
}

func TestResearchEvidenceHonorsInclusiveCutoff(t *testing.T) {
	repository, base := newThemeTestRepository(t)
	ctx := context.Background()
	_, err := repository.UpsertTheme(ctx, UpsertThemeRequest{ID: "theme-chip", CanonicalName: "芯片"})
	require.NoError(t, err)
	equalCutoff := base.Add(10 * time.Minute)
	afterCutoff := equalCutoff.Add(time.Nanosecond)
	event, err := repository.IngestCatalyst(ctx, IngestCatalystRequest{ThemeID: "theme-chip", EventType: "news", Title: "产业政策", Summary: "政策催化", EventAt: base,
		CredibilityScore: 80, Status: "active", Claims: []ClaimInput{
			{SourceName: "新闻", SourceRef: "https://news.example/one", Stance: "supports", SourceCredibilityScore: 80, Summary: "截止时刻可见", AvailableAt: &equalCutoff, CollectedAt: equalCutoff},
			{SourceName: "电报", SourceRef: "https://news.example/two", Stance: "neutral", SourceCredibilityScore: 70, Summary: "截止后才可见", AvailableAt: &afterCutoff, CollectedAt: afterCutoff},
			{SourceName: "未知", SourceRef: "https://news.example/unknown", Stance: "neutral", SourceCredibilityScore: 50, Summary: "没有可用时间", CollectedAt: base},
		}})
	require.NoError(t, err)
	_, err = repository.FreezeSnapshot(ctx, FreezeSnapshotRequest{ThemeID: "theme-chip", TradeDate: "2026-08-27", CycleNo: 1, LifecycleStage: StageObserve,
		Rank: 1, HeatScore: 80, Summary: "政策催化", ObservedAt: base, FrozenAt: base.Add(time.Minute), CatalystIDs: []string{event.ID}, Constituents: []SnapshotConstituent{
			{AssetType: "stock", Market: "SH", Code: "sh688001", Name: "芯片股", Rank: 1},
			{AssetType: "fund", Market: "OTC", Code: "000001", Name: "芯片基金", Rank: 2},
		}})
	require.NoError(t, err)

	service := NewService(repository)
	service.now = func() time.Time { return base.Add(time.Hour) }
	envelope := service.ResearchEvidence(ctx, equalCutoff)
	require.Equal(t, "ok", envelope.Status)
	require.Equal(t, "market-evidence-v2", envelope.EvidenceProfile)
	require.Len(t, envelope.Data.Themes, 1)
	require.Len(t, envelope.Data.Themes[0].Catalysts, 1)
	require.Len(t, envelope.Data.Themes[0].Catalysts[0].Claims, 1)
	require.Equal(t, "截止时刻可见", envelope.Data.Themes[0].Catalysts[0].Claims[0].Summary)
	require.Equal(t, []string{"fund"}, envelope.Data.Themes[0].BackgroundOnlyAssetTypes)
	require.True(t, envelope.AsOf.Equal(equalCutoff))

	before := equalCutoff.Add(-time.Nanosecond)
	envelope = service.ResearchEvidence(ctx, before)
	require.Empty(t, envelope.Data.Themes[0].Catalysts)
}

func newThemeTestRepository(t *testing.T) (*Repository, time.Time) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&themeRow{}, &aliasRow{}, &snapshotRow{}, &catalystRow{}, &claimRow{}, &snapshotCatalystRow{}, &constituentRow{}, &evidenceLinkRow{}))
	indexes := []string{
		"CREATE UNIQUE INDEX ux_test_theme_id ON market_themes(theme_id)",
		"CREATE UNIQUE INDEX ux_test_theme_name ON market_themes(normalized_name)",
		"CREATE UNIQUE INDEX ux_test_alias_id ON market_theme_aliases(alias_id)",
		"CREATE UNIQUE INDEX ux_test_alias_name ON market_theme_aliases(normalized_alias)",
		"CREATE UNIQUE INDEX ux_test_snapshot_id ON market_theme_daily_snapshots(snapshot_id)",
		"CREATE UNIQUE INDEX ux_test_snapshot_day ON market_theme_daily_snapshots(theme_id, trade_date)",
		"CREATE UNIQUE INDEX ux_test_event_id ON market_catalyst_events(catalyst_event_id)",
		"CREATE UNIQUE INDEX ux_test_event_fp ON market_catalyst_events(theme_id, event_fingerprint)",
		"CREATE UNIQUE INDEX ux_test_claim_id ON market_catalyst_source_claims(source_claim_id)",
		"CREATE UNIQUE INDEX ux_test_claim_ref ON market_catalyst_source_claims(catalyst_event_id, source_ref_hash)",
		"CREATE UNIQUE INDEX ux_test_snapshot_event ON market_theme_snapshot_catalysts(snapshot_id, catalyst_event_id)",
		"CREATE UNIQUE INDEX ux_test_constituent_id ON market_theme_snapshot_constituents(constituent_id)",
		"CREATE UNIQUE INDEX ux_test_constituent_scope ON market_theme_snapshot_constituents(snapshot_id, asset_type, market, code)",
	}
	for _, statement := range indexes {
		require.NoError(t, db.Exec(statement).Error)
	}
	base := time.Date(2026, 8, 27, 1, 30, 0, 0, time.UTC)
	repository := NewRepository(db)
	repository.now = func() time.Time { return base }
	return repository, base
}

func TestErrorIdentity(t *testing.T) {
	require.True(t, errors.Is(ErrSnapshotConflict, ErrSnapshotConflict))
}
