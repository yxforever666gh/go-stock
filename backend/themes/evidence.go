package themes

import (
	"context"
	"errors"
	"sort"
	"time"

	"go-stock/backend/marketdata"
	"gorm.io/gorm"
)

func (s *Service) ResearchEvidence(ctx context.Context, cutoff time.Time) marketdata.DataEnvelope[ResearchEvidence] {
	cutoff = cutoff.UTC()
	now := s.now().UTC()
	data := ResearchEvidence{CutoffAt: cutoff, Themes: []ResearchTheme{}}
	if s == nil || s.repository == nil || s.repository.db == nil {
		return unavailableEnvelope(data, now, errors.New("theme repository is unavailable"))
	}
	rows, err := s.repository.ListThemeRows(ctx, "")
	if err != nil {
		return unavailableEnvelope(data, now, err)
	}
	var asOf time.Time
	for _, row := range rows {
		snapshot, err := s.repository.SnapshotForDate(ctx, row.ThemeID, "", &cutoff)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return unavailableEnvelope(data, now, err)
		}
		catalysts, err := s.repository.catalystsForSnapshot(ctx, snapshot.ID, cutoff)
		if err != nil {
			return unavailableEnvelope(data, now, err)
		}
		background := make(map[string]struct{})
		for _, constituent := range snapshot.Constituents {
			if constituent.AssetType == "etf" || constituent.AssetType == "fund" {
				background[constituent.AssetType] = struct{}{}
			}
		}
		backgroundTypes := make([]string, 0, len(background))
		for value := range background {
			backgroundTypes = append(backgroundTypes, value)
		}
		sort.Strings(backgroundTypes)
		data.Themes = append(data.Themes, ResearchTheme{ID: row.ThemeID, Name: row.CanonicalName, Snapshot: snapshot, Catalysts: catalysts,
			Constituents: snapshot.Constituents, BackgroundOnlyAssetTypes: backgroundTypes})
		if snapshot.FrozenAt.After(asOf) {
			asOf = snapshot.FrozenAt
		}
		for _, event := range catalysts {
			for _, claim := range event.Claims {
				if claim.AvailableAt != nil && claim.AvailableAt.After(asOf) {
					asOf = *claim.AvailableAt
				}
			}
		}
	}
	sort.Slice(data.Themes, func(i, j int) bool {
		if data.Themes[i].Snapshot.Rank != data.Themes[j].Snapshot.Rank {
			return data.Themes[i].Snapshot.Rank < data.Themes[j].Snapshot.Rank
		}
		return data.Themes[i].ID < data.Themes[j].ID
	})
	status := marketdata.StatusOK
	if len(data.Themes) == 0 {
		status = marketdata.StatusEmpty
	}
	return marketdata.DataEnvelope[ResearchEvidence]{Data: data, Source: "theme_repository", AsOf: asOf, FetchedAt: now, Status: status,
		Errors: []marketdata.DataError{}, EvidenceProfile: "market-evidence-v2"}
}

func (r *Repository) catalystsForSnapshot(ctx context.Context, snapshotID string, cutoff time.Time) ([]CatalystEvent, error) {
	var rows []catalystRow
	err := r.db.WithContext(ctx).Model(&catalystRow{}).
		Joins("JOIN market_theme_snapshot_catalysts sc ON sc.catalyst_event_id = market_catalyst_events.catalyst_event_id AND sc.snapshot_id = ?", snapshotID).
		Where("market_catalyst_events.first_available_at IS NOT NULL AND market_catalyst_events.first_available_at <= ?", cutoff.UTC()).
		Order("market_catalyst_events.event_at, market_catalyst_events.catalyst_event_id").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	items := make([]CatalystEvent, 0, len(rows))
	for _, row := range rows {
		item, err := r.loadCatalyst(ctx, row, &cutoff)
		if err != nil {
			return nil, err
		}
		if len(item.Claims) == 0 {
			continue
		}
		items = append(items, item)
	}
	return items, nil
}
