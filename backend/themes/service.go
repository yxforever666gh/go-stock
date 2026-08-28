package themes

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"go-stock/backend/marketdata"
	"gorm.io/gorm"
)

type Service struct {
	repository *Repository
	now        func() time.Time
}

func NewService(repository *Repository) *Service {
	return &Service{repository: repository, now: time.Now}
}

func (s *Service) ListThemes(ctx context.Context, request ListThemesRequest) (marketdata.DataEnvelope[ThemeListData], error) {
	now := s.now().UTC()
	empty := ThemeListData{Items: []ThemeListItem{}}
	if s == nil || s.repository == nil {
		return unavailableEnvelope(empty, now, errors.New("theme repository is unavailable")), nil
	}
	if request.Date != "" && !validDate(request.Date) {
		return marketdata.DataEnvelope[ThemeListData]{}, fmt.Errorf("%w: invalid date", ErrInvalidRequest)
	}
	if request.Stage != "" && !IsValidStage(request.Stage) {
		return marketdata.DataEnvelope[ThemeListData]{}, fmt.Errorf("%w: invalid stage", ErrInvalidRequest)
	}
	if request.Sort == "" {
		request.Sort = "rank"
	}
	if request.Sort != "rank" && request.Sort != "heat" && request.Sort != "stage" {
		return marketdata.DataEnvelope[ThemeListData]{}, fmt.Errorf("%w: invalid sort", ErrInvalidRequest)
	}
	limit, offset, err := pagination(request.Limit, request.Cursor)
	if err != nil {
		return marketdata.DataEnvelope[ThemeListData]{}, err
	}
	effectiveDate := request.Date
	if effectiveDate == "" {
		effectiveDate, err = s.repository.LatestFrozenTradeDate(ctx, now)
		if err != nil {
			return marketdata.DataEnvelope[ThemeListData]{}, err
		}
	}
	rows, err := s.repository.ListThemeRows(ctx, request.Query)
	if err != nil {
		return marketdata.DataEnvelope[ThemeListData]{}, err
	}
	items := make([]ThemeListItem, 0, len(rows))
	var asOf time.Time
	for _, row := range rows {
		if effectiveDate == "" {
			break
		}
		theme, err := s.repository.GetThemeAt(ctx, row.ThemeID, effectiveDate, now)
		if err != nil {
			return marketdata.DataEnvelope[ThemeListData]{}, err
		}
		if theme.Snapshot == nil || (request.Stage != "" && theme.Snapshot.LifecycleStage != request.Stage) {
			continue
		}
		previous, err := s.repository.PreviousSnapshotAt(ctx, row.ThemeID, theme.Snapshot.TradeDate, now)
		if err != nil {
			return marketdata.DataEnvelope[ThemeListData]{}, err
		}
		item := ThemeListItem{ID: theme.ID, CanonicalName: theme.CanonicalName, Description: theme.Description, Status: theme.Status,
			Snapshot: theme.Snapshot, RepresentativeSecurities: []RepresentativeSecurity{}}
		if previous != nil {
			stage := previous.LifecycleStage
			item.PreviousLifecycleStage = &stage
			item.StageChanged = stage != theme.Snapshot.LifecycleStage || previous.CycleNo != theme.Snapshot.CycleNo
		}
		constituents := append([]SnapshotConstituent(nil), theme.Snapshot.Constituents...)
		sort.SliceStable(constituents, func(i, j int) bool { return constituents[i].Rank < constituents[j].Rank })
		for index, value := range constituents {
			if index == 3 {
				break
			}
			item.RepresentativeSecurities = append(item.RepresentativeSecurities, RepresentativeSecurity{AssetType: value.AssetType, Market: value.Market, Code: value.Code, Name: value.Name, Role: value.Role})
		}
		if theme.Snapshot.FrozenAt.After(asOf) {
			asOf = theme.Snapshot.FrozenAt
		}
		items = append(items, item)
	}
	sortThemeItems(items, request.Sort)
	paged, next := page(items, offset, limit)
	status := marketdata.StatusOK
	if len(paged) == 0 {
		status = marketdata.StatusEmpty
	}
	return marketdata.DataEnvelope[ThemeListData]{Data: ThemeListData{TradeDate: effectiveDate, Items: paged, NextCursor: next}, Source: "theme_repository", AsOf: asOf,
		FetchedAt: now, Status: status, Errors: []marketdata.DataError{}, EvidenceProfile: "market-evidence-v2"}, nil
}

func (s *Service) GetTheme(ctx context.Context, id, date string) (marketdata.DataEnvelope[Theme], error) {
	if strings.TrimSpace(id) == "" || (date != "" && !validDate(date)) {
		return marketdata.DataEnvelope[Theme]{}, fmt.Errorf("%w: invalid theme or date", ErrInvalidRequest)
	}
	now := s.now().UTC()
	item, err := s.repository.GetThemeAt(ctx, id, date, now)
	if err != nil {
		return marketdata.DataEnvelope[Theme]{}, err
	}
	asOf := item.UpdatedAt
	if item.Snapshot != nil {
		asOf = item.Snapshot.FrozenAt
	}
	return marketdata.DataEnvelope[Theme]{Data: item, Source: "theme_repository", AsOf: asOf, FetchedAt: now, Status: marketdata.StatusOK,
		Errors: []marketdata.DataError{}, EvidenceProfile: "market-evidence-v2"}, nil
}

func (s *Service) ListSnapshots(ctx context.Context, request ListSnapshotsRequest) (marketdata.DataEnvelope[SnapshotListData], error) {
	now := s.now().UTC()
	if request.Stage != "" && !IsValidStage(request.Stage) {
		return marketdata.DataEnvelope[SnapshotListData]{}, fmt.Errorf("%w: invalid stage", ErrInvalidRequest)
	}
	if request.From != "" && !validDate(request.From) || request.To != "" && !validDate(request.To) {
		return marketdata.DataEnvelope[SnapshotListData]{}, fmt.Errorf("%w: invalid date range", ErrInvalidRequest)
	}
	themeID, err := s.repository.ResolveThemeID(ctx, request.ThemeID)
	if err != nil {
		return marketdata.DataEnvelope[SnapshotListData]{}, err
	}
	request.ThemeID = themeID
	limit, offset, err := pagination(request.Limit, request.Cursor)
	if err != nil {
		return marketdata.DataEnvelope[SnapshotListData]{}, err
	}
	items, err := s.repository.ListSnapshotsAt(ctx, request, now)
	if err != nil {
		return marketdata.DataEnvelope[SnapshotListData]{}, err
	}
	paged, next := page(items, offset, limit)
	var asOf time.Time
	for _, item := range paged {
		if item.FrozenAt.After(asOf) {
			asOf = item.FrozenAt
		}
	}
	status := marketdata.StatusOK
	if len(paged) == 0 {
		status = marketdata.StatusEmpty
	}
	return marketdata.DataEnvelope[SnapshotListData]{Data: SnapshotListData{Items: paged, NextCursor: next}, Source: "theme_repository", AsOf: asOf,
		FetchedAt: now, Status: status, Errors: []marketdata.DataError{}, EvidenceProfile: "market-evidence-v2"}, nil
}

func (s *Service) ListCatalysts(ctx context.Context, request ListCatalystsRequest) (marketdata.DataEnvelope[CatalystListData], error) {
	if request.Date != "" && !validDate(request.Date) {
		return marketdata.DataEnvelope[CatalystListData]{}, fmt.Errorf("%w: invalid date", ErrInvalidRequest)
	}
	if request.MinCredibility < 0 || request.MinCredibility > 100 {
		return marketdata.DataEnvelope[CatalystListData]{}, fmt.Errorf("%w: invalid credibility", ErrInvalidRequest)
	}
	if request.Status != "" && !validEventStatus(request.Status) {
		return marketdata.DataEnvelope[CatalystListData]{}, fmt.Errorf("%w: invalid catalyst status", ErrInvalidRequest)
	}
	themeID, err := s.repository.ResolveThemeID(ctx, request.ThemeID)
	if err != nil {
		return marketdata.DataEnvelope[CatalystListData]{}, err
	}
	request.ThemeID = themeID
	limit, offset, err := pagination(request.Limit, request.Cursor)
	if err != nil {
		return marketdata.DataEnvelope[CatalystListData]{}, err
	}
	items, err := s.repository.ListCatalysts(ctx, request)
	if err != nil {
		return marketdata.DataEnvelope[CatalystListData]{}, err
	}
	paged, next := page(items, offset, limit)
	var asOf time.Time
	for _, event := range paged {
		if event.EventAt.After(asOf) {
			asOf = event.EventAt
		}
		for _, claim := range event.Claims {
			if claim.AvailableAt != nil && claim.AvailableAt.After(asOf) {
				asOf = *claim.AvailableAt
			}
		}
	}
	status := marketdata.StatusOK
	if len(paged) == 0 {
		status = marketdata.StatusEmpty
	}
	return marketdata.DataEnvelope[CatalystListData]{Data: CatalystListData{Items: paged, NextCursor: next}, Source: "theme_repository", AsOf: asOf,
		FetchedAt: s.now().UTC(), Status: status, Errors: []marketdata.DataError{}, EvidenceProfile: "market-evidence-v2"}, nil
}

func (s *Service) FreezeSnapshot(ctx context.Context, request FreezeSnapshotRequest) (DailySnapshot, error) {
	return s.repository.FreezeSnapshot(ctx, request)
}
func (s *Service) IngestCatalyst(ctx context.Context, request IngestCatalystRequest) (CatalystEvent, error) {
	return s.repository.IngestCatalyst(ctx, request)
}

func pagination(limit int, cursor string) (int, int, error) {
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > 100 {
		return 0, 0, fmt.Errorf("%w: limit must be 1..100", ErrInvalidRequest)
	}
	if cursor == "" {
		return limit, 0, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, 0, ErrInvalidCursor
	}
	offset, err := strconv.Atoi(string(decoded))
	if err != nil || offset < 0 {
		return 0, 0, ErrInvalidCursor
	}
	return limit, offset, nil
}

func page[T any](items []T, offset, limit int) ([]T, string) {
	if offset >= len(items) {
		return []T{}, ""
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	next := ""
	if end < len(items) {
		next = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(end)))
	}
	return items[offset:end], next
}

func sortThemeItems(items []ThemeListItem, mode string) {
	stageRank := func(item ThemeListItem) int {
		if item.Snapshot == nil {
			return -1
		}
		return lifecycleOrder[item.Snapshot.LifecycleStage]
	}
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		switch mode {
		case "heat":
			if left.Snapshot.HeatScore != right.Snapshot.HeatScore {
				return left.Snapshot.HeatScore > right.Snapshot.HeatScore
			}
		case "stage":
			if stageRank(left) != stageRank(right) {
				return stageRank(left) > stageRank(right)
			}
		default:
			if left.Snapshot.Rank != right.Snapshot.Rank {
				return left.Snapshot.Rank < right.Snapshot.Rank
			}
		}
		return left.ID < right.ID
	})
}

func unavailableEnvelope[T any](data T, now time.Time, err error) marketdata.DataEnvelope[T] {
	return marketdata.DataEnvelope[T]{Data: data, Source: "theme_repository", FetchedAt: now, Status: marketdata.StatusUnavailable,
		Errors: []marketdata.DataError{{Provider: "theme_repository", Code: "repository_unavailable", Message: err.Error()}}, EvidenceProfile: "market-evidence-v2"}
}

var _ = gorm.ErrRecordNotFound
