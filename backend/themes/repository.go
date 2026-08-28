package themes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct {
	db  *gorm.DB
	now func() time.Time
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db, now: time.Now}
}

type themeRow struct {
	ID             uint      `gorm:"column:id;primaryKey"`
	ThemeID        string    `gorm:"column:theme_id"`
	CanonicalName  string    `gorm:"column:canonical_name"`
	NormalizedName string    `gorm:"column:normalized_name"`
	Description    string    `gorm:"column:description"`
	Status         string    `gorm:"column:status"`
	CreatedAt      time.Time `gorm:"column:created_at"`
	UpdatedAt      time.Time `gorm:"column:updated_at"`
}

func (themeRow) TableName() string { return "market_themes" }

type aliasRow struct {
	ID              uint      `gorm:"column:id;primaryKey"`
	AliasID         string    `gorm:"column:alias_id"`
	ThemeID         string    `gorm:"column:theme_id"`
	Alias           string    `gorm:"column:alias"`
	NormalizedAlias string    `gorm:"column:normalized_alias"`
	Source          string    `gorm:"column:source"`
	CreatedAt       time.Time `gorm:"column:created_at"`
}

func (aliasRow) TableName() string { return "market_theme_aliases" }

type snapshotRow struct {
	ID                       uint      `gorm:"column:id;primaryKey"`
	SnapshotID               string    `gorm:"column:snapshot_id"`
	ThemeID                  string    `gorm:"column:theme_id"`
	TradeDate                string    `gorm:"column:trade_date"`
	CycleNo                  int       `gorm:"column:cycle_no"`
	LifecycleStage           string    `gorm:"column:lifecycle_stage"`
	Rank                     int       `gorm:"column:rank"`
	HeatScore                float64   `gorm:"column:heat_score"`
	Summary                  string    `gorm:"column:summary"`
	ObservedAt               time.Time `gorm:"column:observed_at"`
	FrozenAt                 time.Time `gorm:"column:frozen_at"`
	ContentHash              string    `gorm:"column:content_hash"`
	ConstituentCount         int       `gorm:"column:constituent_count"`
	CatalystCount            int       `gorm:"column:catalyst_count"`
	ConflictingCatalystCount int       `gorm:"column:conflicting_catalyst_count"`
	CreatedAt                time.Time `gorm:"column:created_at"`
}

func (snapshotRow) TableName() string { return "market_theme_daily_snapshots" }

type catalystRow struct {
	ID               uint       `gorm:"column:id;primaryKey"`
	CatalystEventID  string     `gorm:"column:catalyst_event_id"`
	ThemeID          string     `gorm:"column:theme_id"`
	EventFingerprint string     `gorm:"column:event_fingerprint"`
	EventType        string     `gorm:"column:event_type"`
	Title            string     `gorm:"column:title"`
	Summary          string     `gorm:"column:summary"`
	EventAt          time.Time  `gorm:"column:event_at"`
	FirstAvailableAt *time.Time `gorm:"column:first_available_at"`
	CredibilityScore int        `gorm:"column:credibility_score"`
	Status           string     `gorm:"column:status"`
	EntityKeysJSON   string     `gorm:"column:entity_keys_json"`
	CreatedAt        time.Time  `gorm:"column:created_at"`
}

func (catalystRow) TableName() string { return "market_catalyst_events" }

type claimRow struct {
	ID                     uint       `gorm:"column:id;primaryKey"`
	SourceClaimID          string     `gorm:"column:source_claim_id"`
	CatalystEventID        string     `gorm:"column:catalyst_event_id"`
	SourceName             string     `gorm:"column:source_name"`
	SourceRef              string     `gorm:"column:source_ref"`
	SourceRefHash          string     `gorm:"column:source_ref_hash"`
	Stance                 string     `gorm:"column:stance"`
	SourceCredibilityScore int        `gorm:"column:source_credibility_score"`
	Summary                string     `gorm:"column:summary"`
	ClaimFingerprint       string     `gorm:"column:claim_fingerprint"`
	PublishedAt            *time.Time `gorm:"column:published_at"`
	AvailableAt            *time.Time `gorm:"column:available_at"`
	CollectedAt            time.Time  `gorm:"column:collected_at"`
	RawPayloadHash         string     `gorm:"column:raw_payload_hash"`
	CreatedAt              time.Time  `gorm:"column:created_at"`
}

func (claimRow) TableName() string { return "market_catalyst_source_claims" }

type snapshotCatalystRow struct {
	ID              uint      `gorm:"column:id;primaryKey"`
	SnapshotID      string    `gorm:"column:snapshot_id"`
	CatalystEventID string    `gorm:"column:catalyst_event_id"`
	CreatedAt       time.Time `gorm:"column:created_at"`
}

func (snapshotCatalystRow) TableName() string { return "market_theme_snapshot_catalysts" }

type constituentRow struct {
	ID                uint      `gorm:"column:id;primaryKey"`
	ConstituentID     string    `gorm:"column:constituent_id"`
	SnapshotID        string    `gorm:"column:snapshot_id"`
	AssetType         string    `gorm:"column:asset_type"`
	Market            string    `gorm:"column:market"`
	Code              string    `gorm:"column:code"`
	Name              string    `gorm:"column:name"`
	Role              string    `gorm:"column:role"`
	Rank              int       `gorm:"column:rank"`
	ContributionScore float64   `gorm:"column:contribution_score"`
	CreatedAt         time.Time `gorm:"column:created_at"`
}

func (constituentRow) TableName() string { return "market_theme_snapshot_constituents" }

type evidenceLinkRow struct {
	ID              uint      `gorm:"column:id;primaryKey"`
	LinkID          string    `gorm:"column:link_id"`
	ThemeID         string    `gorm:"column:theme_id"`
	SnapshotID      *string   `gorm:"column:snapshot_id"`
	CatalystEventID *string   `gorm:"column:catalyst_event_id"`
	SourceClaimID   *string   `gorm:"column:source_claim_id"`
	EvidenceItemID  string    `gorm:"column:evidence_item_id"`
	LinkType        string    `gorm:"column:link_type"`
	CreatedAt       time.Time `gorm:"column:created_at"`
}

func (evidenceLinkRow) TableName() string { return "market_theme_evidence_links" }

func (r *Repository) UpsertTheme(ctx context.Context, request UpsertThemeRequest) (Theme, error) {
	if r == nil || r.db == nil {
		return Theme{}, errors.New("theme repository is unavailable")
	}
	name := strings.TrimSpace(request.CanonicalName)
	normalized := NormalizeName(name)
	if name == "" || normalized == "" {
		return Theme{}, fmt.Errorf("%w: canonical name is required", ErrInvalidRequest)
	}
	status := strings.ToLower(strings.TrimSpace(request.Status))
	if status == "" {
		status = "active"
	}
	if status != "active" && status != "archived" {
		return Theme{}, fmt.Errorf("%w: invalid theme status", ErrInvalidRequest)
	}
	id := strings.TrimSpace(request.ID)
	if id == "" {
		id = uuid.NewString()
	}
	now := r.now().UTC()
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row := themeRow{ThemeID: id, CanonicalName: name, NormalizedName: normalized, Description: strings.TrimSpace(request.Description), Status: status, CreatedAt: now, UpdatedAt: now}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "theme_id"}}, DoUpdates: clause.Assignments(map[string]any{
			"canonical_name": name, "normalized_name": normalized, "description": row.Description, "status": status, "updated_at": now,
		})}).Create(&row).Error; err != nil {
			return err
		}
		for _, input := range request.Aliases {
			alias := strings.TrimSpace(input.Alias)
			if alias == "" {
				continue
			}
			aliasID := strings.TrimSpace(input.ID)
			if aliasID == "" {
				aliasID = uuid.NewString()
			}
			item := aliasRow{AliasID: aliasID, ThemeID: id, Alias: alias, NormalizedAlias: NormalizeName(alias), Source: strings.TrimSpace(input.Source), CreatedAt: now}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "normalized_alias"}}, DoNothing: true}).Create(&item).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Theme{}, err
	}
	return r.GetTheme(ctx, id, "")
}

func (r *Repository) ResolveThemeID(ctx context.Context, idOrAlias string) (string, error) {
	value := strings.TrimSpace(idOrAlias)
	var row themeRow
	err := r.db.WithContext(ctx).Where("theme_id = ? OR normalized_name = ?", value, NormalizeName(value)).First(&row).Error
	if err == nil {
		return row.ThemeID, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", err
	}
	var alias aliasRow
	if err = r.db.WithContext(ctx).Where("normalized_alias = ?", NormalizeName(value)).First(&alias).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", ErrNotFound
		}
		return "", err
	}
	return alias.ThemeID, nil
}

func (r *Repository) GetTheme(ctx context.Context, idOrAlias, date string) (Theme, error) {
	return r.getTheme(ctx, idOrAlias, date, nil)
}

// GetThemeAt is the public-read variant. It hides snapshots that had not yet
// been frozen at cutoff while GetTheme remains available to lifecycle writers
// that need to inspect the complete immutable history.
func (r *Repository) GetThemeAt(ctx context.Context, idOrAlias, date string, cutoff time.Time) (Theme, error) {
	cutoff = cutoff.UTC()
	return r.getTheme(ctx, idOrAlias, date, &cutoff)
}

func (r *Repository) getTheme(ctx context.Context, idOrAlias, date string, cutoff *time.Time) (Theme, error) {
	themeID, err := r.ResolveThemeID(ctx, idOrAlias)
	if err != nil {
		return Theme{}, err
	}
	var row themeRow
	if err := r.db.WithContext(ctx).Where("theme_id = ?", themeID).First(&row).Error; err != nil {
		return Theme{}, err
	}
	var aliases []aliasRow
	if err := r.db.WithContext(ctx).Where("theme_id = ?", themeID).Order("created_at, alias_id").Find(&aliases).Error; err != nil {
		return Theme{}, err
	}
	item := mapTheme(row, aliases)
	snapshot, err := r.snapshotForDate(ctx, themeID, date, cutoff)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return Theme{}, err
	}
	if err == nil {
		item.Snapshot = &snapshot
	}
	return item, nil
}

func (r *Repository) FreezeSnapshot(ctx context.Context, request FreezeSnapshotRequest) (DailySnapshot, error) {
	if strings.TrimSpace(request.ThemeID) == "" || !validDate(request.TradeDate) || request.FrozenAt.IsZero() || request.ObservedAt.IsZero() || request.Rank < 1 {
		return DailySnapshot{}, fmt.Errorf("%w: theme, trade date, positive rank and timestamps are required", ErrInvalidRequest)
	}
	hash, err := canonicalSnapshotHash(request)
	if err != nil {
		return DailySnapshot{}, err
	}
	var result DailySnapshot
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var themeCount int64
		if err := tx.Model(&themeRow{}).Where("theme_id = ?", request.ThemeID).Count(&themeCount).Error; err != nil {
			return err
		}
		if themeCount == 0 {
			return ErrNotFound
		}
		var existing snapshotRow
		err := tx.Where("theme_id = ? AND trade_date = ?", request.ThemeID, request.TradeDate).First(&existing).Error
		if err == nil {
			if existing.ContentHash != hash {
				return ErrSnapshotConflict
			}
			loaded, loadErr := loadSnapshot(tx, existing)
			result = loaded
			return loadErr
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var previousRow snapshotRow
		previousErr := tx.Where("theme_id = ? AND trade_date < ?", request.ThemeID, request.TradeDate).Order("trade_date DESC").First(&previousRow).Error
		var previous *DailySnapshot
		if previousErr == nil {
			mapped := mapSnapshot(previousRow)
			previous = &mapped
		} else if !errors.Is(previousErr, gorm.ErrRecordNotFound) {
			return previousErr
		}
		if err := ValidateLifecycle(previous, request.CycleNo, request.LifecycleStage); err != nil {
			return err
		}
		catalystIDs := uniqueNonEmpty(request.CatalystIDs)
		if len(catalystIDs) > 0 {
			var count int64
			if err := tx.Model(&catalystRow{}).Where("theme_id = ? AND catalyst_event_id IN ?", request.ThemeID, catalystIDs).Count(&count).Error; err != nil {
				return err
			}
			if count != int64(len(catalystIDs)) {
				return fmt.Errorf("%w: catalyst does not belong to theme", ErrInvalidRequest)
			}
		}
		constituents, err := normalizedConstituents(request.Constituents)
		if err != nil {
			return err
		}
		conflicts, err := countConflictingCatalysts(tx, catalystIDs)
		if err != nil {
			return err
		}
		now := r.now().UTC()
		row := snapshotRow{SnapshotID: uuid.NewString(), ThemeID: request.ThemeID, TradeDate: request.TradeDate, CycleNo: request.CycleNo,
			LifecycleStage: string(request.LifecycleStage), Rank: request.Rank, HeatScore: request.HeatScore, Summary: strings.TrimSpace(request.Summary),
			ObservedAt: request.ObservedAt.UTC(), FrozenAt: request.FrozenAt.UTC(), ContentHash: hash, ConstituentCount: len(constituents),
			CatalystCount: len(catalystIDs), ConflictingCatalystCount: conflicts, CreatedAt: now}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		for _, item := range constituents {
			item.SnapshotID, item.CreatedAt = row.SnapshotID, now
			if err := tx.Create(&item).Error; err != nil {
				return err
			}
		}
		for _, eventID := range catalystIDs {
			if err := tx.Create(&snapshotCatalystRow{SnapshotID: row.SnapshotID, CatalystEventID: eventID, CreatedAt: now}).Error; err != nil {
				return err
			}
		}
		result, err = loadSnapshot(tx, row)
		return err
	})
	return result, err
}

func (r *Repository) IngestCatalyst(ctx context.Context, request IngestCatalystRequest) (CatalystEvent, error) {
	if strings.TrimSpace(request.ThemeID) == "" || request.EventAt.IsZero() || strings.TrimSpace(request.Title) == "" || request.CredibilityScore < 0 || request.CredibilityScore > 100 {
		return CatalystEvent{}, fmt.Errorf("%w: invalid catalyst", ErrInvalidRequest)
	}
	status := strings.ToLower(strings.TrimSpace(request.Status))
	if status == "" {
		status = "active"
	}
	if !validEventStatus(status) {
		return CatalystEvent{}, fmt.Errorf("%w: invalid catalyst status", ErrInvalidRequest)
	}
	fingerprint := EventFingerprint(request.ThemeID, request.EventType, request.Title, request.Summary, request.EventAt, request.EntityKeys)
	var event catalystRow
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("theme_id = ? AND event_fingerprint = ?", request.ThemeID, fingerprint).First(&event).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			keys, marshalErr := json.Marshal(uniqueNonEmpty(request.EntityKeys))
			if marshalErr != nil {
				return marshalErr
			}
			eventID := strings.TrimSpace(request.ID)
			if eventID == "" {
				eventID = uuid.NewString()
			}
			event = catalystRow{CatalystEventID: eventID, ThemeID: request.ThemeID, EventFingerprint: fingerprint, EventType: strings.TrimSpace(request.EventType),
				Title: strings.TrimSpace(request.Title), Summary: strings.TrimSpace(request.Summary), EventAt: request.EventAt.UTC(), CredibilityScore: request.CredibilityScore,
				Status: status, EntityKeysJSON: string(keys), CreatedAt: r.now().UTC()}
			if err := tx.Create(&event).Error; err != nil {
				return err
			}
		}
		first := event.FirstAvailableAt
		for _, input := range request.Claims {
			if err := validateClaim(input); err != nil {
				return err
			}
			refHash, claimFingerprint := SourceRefFingerprint(input.SourceRef), ClaimFingerprint(input.Summary)
			var existing claimRow
			err := tx.Where("catalyst_event_id = ? AND source_ref_hash = ?", event.CatalystEventID, refHash).First(&existing).Error
			if err == nil {
				if existing.ClaimFingerprint != claimFingerprint || existing.Stance != strings.ToLower(input.Stance) {
					return ErrSourceClaimConflict
				}
				if input.AvailableAt != nil && (first == nil || input.AvailableAt.Before(*first)) {
					value := input.AvailableAt.UTC()
					first = &value
				}
				continue
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			claimID := strings.TrimSpace(input.ID)
			if claimID == "" {
				claimID = uuid.NewString()
			}
			collectedAt := input.CollectedAt
			if collectedAt.IsZero() {
				collectedAt = r.now()
			}
			row := claimRow{SourceClaimID: claimID, CatalystEventID: event.CatalystEventID, SourceName: strings.TrimSpace(input.SourceName), SourceRef: NormalizeSourceRef(input.SourceRef),
				SourceRefHash: refHash, Stance: strings.ToLower(strings.TrimSpace(input.Stance)), SourceCredibilityScore: input.SourceCredibilityScore,
				Summary: strings.TrimSpace(input.Summary), ClaimFingerprint: claimFingerprint, PublishedAt: utcPointer(input.PublishedAt), AvailableAt: utcPointer(input.AvailableAt),
				CollectedAt: collectedAt.UTC(), RawPayloadHash: strings.TrimSpace(input.RawPayloadHash), CreatedAt: r.now().UTC()}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			if row.AvailableAt != nil && (first == nil || row.AvailableAt.Before(*first)) {
				value := row.AvailableAt.UTC()
				first = &value
			}
		}
		if !sameTimePointer(first, event.FirstAvailableAt) {
			if err := tx.Model(&catalystRow{}).Where("catalyst_event_id = ?", event.CatalystEventID).Update("first_available_at", first).Error; err != nil {
				return err
			}
			event.FirstAvailableAt = first
		}
		return nil
	})
	if err != nil {
		return CatalystEvent{}, err
	}
	return r.loadCatalyst(ctx, event, nil)
}

func validEventStatus(status string) bool {
	switch status {
	case "active", "disputed", "retracted", "expired":
		return true
	default:
		return false
	}
}

func validateClaim(input ClaimInput) error {
	stance := strings.ToLower(strings.TrimSpace(input.Stance))
	if input.SourceName == "" || input.SourceRef == "" || input.Summary == "" || input.SourceCredibilityScore < 0 || input.SourceCredibilityScore > 100 {
		return fmt.Errorf("%w: invalid source claim", ErrInvalidRequest)
	}
	if stance != "supports" && stance != "contradicts" && stance != "neutral" {
		return fmt.Errorf("%w: invalid source claim stance", ErrInvalidRequest)
	}
	return nil
}

func (r *Repository) ListThemeRows(ctx context.Context, query string) ([]themeRow, error) {
	dbQuery := r.db.WithContext(ctx).Model(&themeRow{}).Where("status = ?", "active")
	if normalized := NormalizeName(query); normalized != "" {
		like := "%" + normalized + "%"
		dbQuery = dbQuery.Where("normalized_name LIKE ? OR EXISTS (SELECT 1 FROM market_theme_aliases a WHERE a.theme_id = market_themes.theme_id AND a.normalized_alias LIKE ?)", like, like)
	}
	var rows []themeRow
	err := dbQuery.Find(&rows).Error
	return rows, err
}

func (r *Repository) SnapshotForDate(ctx context.Context, themeID, date string, cutoff *time.Time) (DailySnapshot, error) {
	return r.snapshotForDate(ctx, themeID, date, cutoff)
}

func (r *Repository) LatestFrozenTradeDate(ctx context.Context, cutoff time.Time) (string, error) {
	var result struct {
		TradeDate string `gorm:"column:trade_date"`
	}
	err := r.db.WithContext(ctx).Raw(`SELECT COALESCE(MAX(s.trade_date), '') AS trade_date
FROM market_theme_daily_snapshots s
JOIN market_themes t ON t.theme_id = s.theme_id AND t.status = 'active'
WHERE s.frozen_at <= ?`, cutoff.UTC()).Scan(&result).Error
	return result.TradeDate, err
}

func (r *Repository) snapshotForDate(ctx context.Context, themeID, date string, cutoff *time.Time) (DailySnapshot, error) {
	query := r.db.WithContext(ctx).Where("theme_id = ?", themeID)
	if date != "" {
		query = query.Where("trade_date = ?", date)
	}
	if cutoff != nil {
		query = query.Where("frozen_at <= ?", cutoff.UTC())
	}
	var row snapshotRow
	if err := query.Order("trade_date DESC").First(&row).Error; err != nil {
		return DailySnapshot{}, err
	}
	return loadSnapshot(r.db.WithContext(ctx), row)
}

func (r *Repository) PreviousSnapshot(ctx context.Context, themeID, beforeDate string) (*DailySnapshot, error) {
	return r.previousSnapshot(ctx, themeID, beforeDate, nil)
}

func (r *Repository) PreviousSnapshotAt(ctx context.Context, themeID, beforeDate string, cutoff time.Time) (*DailySnapshot, error) {
	cutoff = cutoff.UTC()
	return r.previousSnapshot(ctx, themeID, beforeDate, &cutoff)
}

func (r *Repository) previousSnapshot(ctx context.Context, themeID, beforeDate string, cutoff *time.Time) (*DailySnapshot, error) {
	var row snapshotRow
	query := r.db.WithContext(ctx).Where("theme_id = ? AND trade_date < ?", themeID, beforeDate)
	if cutoff != nil {
		query = query.Where("frozen_at <= ?", cutoff.UTC())
	}
	err := query.Order("trade_date DESC").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	mapped := mapSnapshot(row)
	return &mapped, nil
}

func (r *Repository) ListSnapshots(ctx context.Context, request ListSnapshotsRequest) ([]DailySnapshot, error) {
	return r.listSnapshots(ctx, request, nil)
}

func (r *Repository) ListSnapshotsAt(ctx context.Context, request ListSnapshotsRequest, cutoff time.Time) ([]DailySnapshot, error) {
	cutoff = cutoff.UTC()
	return r.listSnapshots(ctx, request, &cutoff)
}

func (r *Repository) listSnapshots(ctx context.Context, request ListSnapshotsRequest, cutoff *time.Time) ([]DailySnapshot, error) {
	query := r.db.WithContext(ctx).Where("theme_id = ?", request.ThemeID)
	if request.From != "" {
		query = query.Where("trade_date >= ?", request.From)
	}
	if request.To != "" {
		query = query.Where("trade_date <= ?", request.To)
	}
	if request.Stage != "" {
		query = query.Where("lifecycle_stage = ?", string(request.Stage))
	}
	if cutoff != nil {
		query = query.Where("frozen_at <= ?", cutoff.UTC())
	}
	var rows []snapshotRow
	if err := query.Order("trade_date DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]DailySnapshot, 0, len(rows))
	for _, row := range rows {
		item, err := loadSnapshot(r.db.WithContext(ctx), row)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (r *Repository) ListCatalysts(ctx context.Context, request ListCatalystsRequest) ([]CatalystEvent, error) {
	query := r.db.WithContext(ctx).Model(&catalystRow{}).Where("market_catalyst_events.theme_id = ?", request.ThemeID)
	if request.Status != "" {
		query = query.Where("market_catalyst_events.status = ?", request.Status)
	}
	if request.MinCredibility > 0 {
		query = query.Where("market_catalyst_events.credibility_score >= ?", request.MinCredibility)
	}
	if request.Date != "" {
		query = query.Joins("JOIN market_theme_snapshot_catalysts sc ON sc.catalyst_event_id = market_catalyst_events.catalyst_event_id").
			Joins("JOIN market_theme_daily_snapshots s ON s.snapshot_id = sc.snapshot_id AND s.trade_date = ?", request.Date)
	}
	if request.Cutoff != nil {
		query = query.Where("market_catalyst_events.first_available_at IS NOT NULL AND market_catalyst_events.first_available_at <= ?", request.Cutoff.UTC())
	}
	var rows []catalystRow
	if err := query.Order("market_catalyst_events.event_at DESC, market_catalyst_events.catalyst_event_id").Find(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]CatalystEvent, 0, len(rows))
	for _, row := range rows {
		item, err := r.loadCatalyst(ctx, row, request.Cutoff)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (r *Repository) loadCatalyst(ctx context.Context, row catalystRow, cutoff *time.Time) (CatalystEvent, error) {
	query := r.db.WithContext(ctx).Where("catalyst_event_id = ?", row.CatalystEventID)
	if cutoff != nil {
		query = query.Where("available_at IS NOT NULL AND available_at <= ?", cutoff.UTC())
	}
	var claims []claimRow
	if err := query.Order("available_at, source_claim_id").Find(&claims).Error; err != nil {
		return CatalystEvent{}, err
	}
	evidenceIDs := make(map[string][]string, len(claims))
	claimIDs := make([]string, 0, len(claims))
	for _, claim := range claims {
		claimIDs = append(claimIDs, claim.SourceClaimID)
	}
	if len(claimIDs) > 0 {
		var links []evidenceLinkRow
		if err := r.db.WithContext(ctx).Where("source_claim_id IN ?", claimIDs).Order("source_claim_id, evidence_item_id").Find(&links).Error; err != nil {
			return CatalystEvent{}, err
		}
		seen := make(map[string]map[string]struct{}, len(claimIDs))
		for _, link := range links {
			if link.SourceClaimID == nil || strings.TrimSpace(link.EvidenceItemID) == "" {
				continue
			}
			claimID := *link.SourceClaimID
			if seen[claimID] == nil {
				seen[claimID] = make(map[string]struct{})
			}
			if _, exists := seen[claimID][link.EvidenceItemID]; exists {
				continue
			}
			seen[claimID][link.EvidenceItemID] = struct{}{}
			evidenceIDs[claimID] = append(evidenceIDs[claimID], link.EvidenceItemID)
		}
	}
	return mapCatalyst(row, claims, evidenceIDs), nil
}

func loadSnapshot(db *gorm.DB, row snapshotRow) (DailySnapshot, error) {
	var constituents []constituentRow
	if err := db.Where("snapshot_id = ?", row.SnapshotID).Order("rank, constituent_id").Find(&constituents).Error; err != nil {
		return DailySnapshot{}, err
	}
	var links []snapshotCatalystRow
	if err := db.Where("snapshot_id = ?", row.SnapshotID).Order("catalyst_event_id").Find(&links).Error; err != nil {
		return DailySnapshot{}, err
	}
	item := mapSnapshot(row)
	for _, value := range constituents {
		item.Constituents = append(item.Constituents, mapConstituent(value))
	}
	for _, value := range links {
		item.CatalystIDs = append(item.CatalystIDs, value.CatalystEventID)
	}
	return item, nil
}

func mapTheme(row themeRow, aliases []aliasRow) Theme {
	item := Theme{ID: row.ThemeID, CanonicalName: row.CanonicalName, Description: row.Description, Status: row.Status, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, Aliases: []ThemeAlias{}}
	for _, alias := range aliases {
		item.Aliases = append(item.Aliases, ThemeAlias{ID: alias.AliasID, Alias: alias.Alias, Source: alias.Source, CreatedAt: alias.CreatedAt})
	}
	return item
}

func mapSnapshot(row snapshotRow) DailySnapshot {
	return DailySnapshot{ID: row.SnapshotID, ThemeID: row.ThemeID, TradeDate: row.TradeDate, CycleNo: row.CycleNo, LifecycleStage: LifecycleStage(row.LifecycleStage), Rank: row.Rank,
		HeatScore: row.HeatScore, Summary: row.Summary, ObservedAt: row.ObservedAt, FrozenAt: row.FrozenAt, ContentHash: row.ContentHash,
		ConstituentCount: row.ConstituentCount, CatalystCount: row.CatalystCount, ConflictingCatalystCount: row.ConflictingCatalystCount,
		Constituents: []SnapshotConstituent{}, CatalystIDs: []string{}, CreatedAt: row.CreatedAt}
}

func mapConstituent(row constituentRow) SnapshotConstituent {
	return SnapshotConstituent{ID: row.ConstituentID, AssetType: row.AssetType, Market: row.Market, Code: row.Code, Name: row.Name, Role: row.Role, Rank: row.Rank, ContributionScore: row.ContributionScore}
}

func mapCatalyst(row catalystRow, claims []claimRow, evidenceIDs map[string][]string) CatalystEvent {
	var keys []string
	_ = json.Unmarshal([]byte(row.EntityKeysJSON), &keys)
	item := CatalystEvent{ID: row.CatalystEventID, ThemeID: row.ThemeID, EventFingerprint: row.EventFingerprint, EventType: row.EventType, Title: row.Title, Summary: row.Summary,
		EventAt: row.EventAt, FirstAvailableAt: row.FirstAvailableAt, CredibilityScore: row.CredibilityScore, Status: row.Status, EntityKeys: keys, Claims: []SourceClaim{}, CreatedAt: row.CreatedAt}
	for _, claim := range claims {
		claimEvidenceIDs := append([]string(nil), evidenceIDs[claim.SourceClaimID]...)
		if claimEvidenceIDs == nil {
			claimEvidenceIDs = []string{}
		}
		item.Claims = append(item.Claims, SourceClaim{ID: claim.SourceClaimID, SourceName: claim.SourceName, SourceRef: claim.SourceRef, Stance: claim.Stance,
			SourceCredibilityScore: claim.SourceCredibilityScore, Summary: claim.Summary, ClaimFingerprint: claim.ClaimFingerprint, PublishedAt: claim.PublishedAt,
			AvailableAt: claim.AvailableAt, CollectedAt: claim.CollectedAt, RawPayloadHash: claim.RawPayloadHash, EvidenceItemIDs: claimEvidenceIDs, CreatedAt: claim.CreatedAt})
	}
	return item
}

func normalizedConstituents(values []SnapshotConstituent) ([]constituentRow, error) {
	seen := make(map[string]struct{}, len(values))
	rows := make([]constituentRow, 0, len(values))
	for _, item := range values {
		assetType := strings.ToLower(strings.TrimSpace(item.AssetType))
		if assetType != "stock" && assetType != "index" && assetType != "etf" && assetType != "fund" {
			return nil, fmt.Errorf("%w: invalid constituent asset type", ErrInvalidRequest)
		}
		market, code := strings.ToUpper(strings.TrimSpace(item.Market)), strings.ToLower(strings.TrimSpace(item.Code))
		key := assetType + "|" + market + "|" + code
		if code == "" || item.Rank < 1 {
			return nil, fmt.Errorf("%w: constituent code and positive rank are required", ErrInvalidRequest)
		}
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("%w: duplicate constituent", ErrInvalidRequest)
		}
		seen[key] = struct{}{}
		id := strings.TrimSpace(item.ID)
		if id == "" {
			id = uuid.NewString()
		}
		rows = append(rows, constituentRow{ConstituentID: id, AssetType: assetType, Market: market, Code: code, Name: strings.TrimSpace(item.Name), Role: strings.TrimSpace(item.Role), Rank: item.Rank, ContributionScore: item.ContributionScore})
	}
	return rows, nil
}

func countConflictingCatalysts(tx *gorm.DB, ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	type result struct{ CatalystEventID string }
	var rows []result
	err := tx.Model(&claimRow{}).Select("catalyst_event_id").Where("catalyst_event_id IN ?", ids).Group("catalyst_event_id").
		Having("SUM(CASE WHEN stance = 'supports' THEN 1 ELSE 0 END) > 0 AND SUM(CASE WHEN stance = 'contradicts' THEN 1 ELSE 0 END) > 0").Scan(&rows).Error
	return len(rows), err
}

func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func utcPointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := value.UTC()
	return &result
}
func sameTimePointer(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}
func validDate(value string) bool {
	parsed, err := time.Parse("2006-01-02", value)
	return err == nil && parsed.Format("2006-01-02") == value
}
