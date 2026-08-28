package main

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/marketdata"
	"go-stock/backend/themes"
)

type themeAPI interface {
	ListThemes(context.Context, themes.ListThemesRequest) (marketdata.DataEnvelope[themes.ThemeListData], error)
	GetTheme(context.Context, string, string) (marketdata.DataEnvelope[themes.Theme], error)
	ListSnapshots(context.Context, themes.ListSnapshotsRequest) (marketdata.DataEnvelope[themes.SnapshotListData], error)
	ListCatalysts(context.Context, themes.ListCatalystsRequest) (marketdata.DataEnvelope[themes.CatalystListData], error)
}

var themeServiceFactory = func() themeAPI {
	return themes.NewService(themes.NewRepository(db.Dao))
}

func registerThemeRoutes(mux *http.ServeMux, _ *App) {
	service := themeServiceFactory()
	mux.HandleFunc("GET /api/v1/themes", func(w http.ResponseWriter, r *http.Request) {
		date, ok := optionalThemeDate(w, r.URL.Query().Get("date"), "date")
		if !ok {
			return
		}
		stage, ok := themeStage(w, r.URL.Query().Get("stage"))
		if !ok {
			return
		}
		sortBy := strings.TrimSpace(r.URL.Query().Get("sort"))
		if sortBy == "" {
			sortBy = "rank"
		}
		if sortBy != "rank" && sortBy != "heat" && sortBy != "stage" {
			writeThemeError(w, http.StatusBadRequest, "sort must be rank, heat or stage")
			return
		}
		limit, err := queryBoundedInt(r, "limit", 20, 1, 100)
		if err != nil {
			writeThemeError(w, http.StatusBadRequest, err.Error())
			return
		}
		cursor := strings.TrimSpace(r.URL.Query().Get("cursor"))
		if !validThemeCursor(cursor) {
			writeThemeError(w, http.StatusBadRequest, "invalid cursor")
			return
		}
		envelope, err := service.ListThemes(r.Context(), themes.ListThemesRequest{Date: date, Stage: stage, Query: strings.TrimSpace(r.URL.Query().Get("q")), Sort: sortBy, Limit: limit, Cursor: cursor})
		if err != nil {
			writeThemeServiceError(w, err)
			return
		}
		mapped := mapThemeListEnvelope(envelope, date)
		for index := range mapped.Data.Items {
			identity, identityErr := service.GetTheme(r.Context(), mapped.Data.Items[index].ThemeID, date)
			if identityErr != nil {
				writeThemeServiceError(w, identityErr)
				return
			}
			mapped.Data.Items[index].Aliases = aliasNames(identity.Data.Aliases)
		}
		writeJSON(w, http.StatusOK, mapped)
	})

	mux.HandleFunc("GET /api/v1/themes/{id}", func(w http.ResponseWriter, r *http.Request) {
		date, ok := optionalThemeDate(w, r.URL.Query().Get("date"), "date")
		if !ok {
			return
		}
		id := strings.TrimSpace(r.PathValue("id"))
		if id == "" {
			writeThemeError(w, http.StatusBadRequest, "id is required")
			return
		}
		envelope, err := service.GetTheme(r.Context(), id, date)
		if err != nil {
			writeThemeServiceError(w, err)
			return
		}
		var catalysts marketdata.DataEnvelope[themes.CatalystListData]
		if envelope.Data.Snapshot != nil {
			cutoff := envelope.Data.Snapshot.FrozenAt
			catalysts, err = service.ListCatalysts(r.Context(), themes.ListCatalystsRequest{ThemeID: envelope.Data.ID, Date: envelope.Data.Snapshot.TradeDate, Limit: 100, Cutoff: &cutoff})
			if err != nil {
				writeThemeServiceError(w, err)
				return
			}
		}
		writeJSON(w, http.StatusOK, mapThemeDetailEnvelope(envelope, catalysts, date))
	})

	mux.HandleFunc("GET /api/v1/themes/{id}/daily-snapshots", func(w http.ResponseWriter, r *http.Request) {
		from, ok := optionalThemeDate(w, r.URL.Query().Get("from"), "from")
		if !ok {
			return
		}
		to, ok := optionalThemeDate(w, r.URL.Query().Get("to"), "to")
		if !ok {
			return
		}
		if from != "" && to != "" && from > to {
			writeThemeError(w, http.StatusBadRequest, "from must not be after to")
			return
		}
		stage, ok := themeStage(w, r.URL.Query().Get("stage"))
		if !ok {
			return
		}
		limit, err := queryBoundedInt(r, "limit", 30, 1, 100)
		if err != nil {
			writeThemeError(w, http.StatusBadRequest, err.Error())
			return
		}
		cursor := strings.TrimSpace(r.URL.Query().Get("cursor"))
		if !validThemeCursor(cursor) {
			writeThemeError(w, http.StatusBadRequest, "invalid cursor")
			return
		}
		id := strings.TrimSpace(r.PathValue("id"))
		if id == "" {
			writeThemeError(w, http.StatusBadRequest, "id is required")
			return
		}
		identity, err := service.GetTheme(r.Context(), id, "")
		if err != nil {
			writeThemeServiceError(w, err)
			return
		}
		envelope, err := service.ListSnapshots(r.Context(), themes.ListSnapshotsRequest{ThemeID: identity.Data.ID, From: from, To: to, Stage: stage, Limit: limit, Cursor: cursor})
		if err != nil {
			writeThemeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, mapThemeSnapshotsEnvelope(envelope, identity.Data.ID))
	})

	mux.HandleFunc("GET /api/v1/themes/{id}/catalysts", func(w http.ResponseWriter, r *http.Request) {
		date, ok := optionalThemeDate(w, r.URL.Query().Get("date"), "date")
		if !ok {
			return
		}
		status := strings.TrimSpace(r.URL.Query().Get("status"))
		if status != "" && status != "active" && status != "disputed" && status != "retracted" && status != "expired" {
			writeThemeError(w, http.StatusBadRequest, "invalid catalyst status")
			return
		}
		minimum, err := queryBoundedInt(r, "minCredibility", 0, 0, 100)
		if err != nil {
			writeThemeError(w, http.StatusBadRequest, err.Error())
			return
		}
		limit, err := queryBoundedInt(r, "limit", 20, 1, 100)
		if err != nil {
			writeThemeError(w, http.StatusBadRequest, err.Error())
			return
		}
		cursor := strings.TrimSpace(r.URL.Query().Get("cursor"))
		if !validThemeCursor(cursor) {
			writeThemeError(w, http.StatusBadRequest, "invalid cursor")
			return
		}
		id := strings.TrimSpace(r.PathValue("id"))
		if id == "" {
			writeThemeError(w, http.StatusBadRequest, "id is required")
			return
		}
		identity, err := service.GetTheme(r.Context(), id, date)
		if err != nil {
			writeThemeServiceError(w, err)
			return
		}
		if identity.Data.Snapshot == nil {
			tradeDate := date
			if tradeDate == "" {
				tradeDate = shanghaiDate(identity.FetchedAt)
			}
			warnings := append([]string(nil), identity.Warnings...)
			warnings = append(warnings, "no frozen snapshot for "+tradeDate)
			empty := marketdata.DataEnvelope[themes.CatalystListData]{Data: themes.CatalystListData{Items: []themes.CatalystEvent{}}, Source: identity.Source,
				AsOf: identity.AsOf, FetchedAt: identity.FetchedAt, Status: identity.Status, Errors: identity.Errors, Sources: identity.Sources,
				Warnings: warnings, EvidenceProfile: identity.EvidenceProfile}
			writeJSON(w, http.StatusOK, mapThemeCatalystsEnvelope(empty, identity.Data.ID, tradeDate, time.Time{}))
			return
		}
		tradeDate := identity.Data.Snapshot.TradeDate
		cutoff := identity.Data.Snapshot.FrozenAt
		envelope, err := service.ListCatalysts(r.Context(), themes.ListCatalystsRequest{ThemeID: identity.Data.ID, Date: tradeDate, Status: status, MinCredibility: minimum, Limit: limit, Cursor: cursor, Cutoff: &cutoff})
		if err != nil {
			writeThemeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, mapThemeCatalystsEnvelope(envelope, identity.Data.ID, tradeDate, cutoff))
	})
}

type themeEnvelopeDTO[T any] struct {
	Data            T                        `json:"data"`
	Source          string                   `json:"source"`
	AsOf            time.Time                `json:"asOf"`
	FetchedAt       time.Time                `json:"fetchedAt"`
	Status          string                   `json:"status"`
	Errors          []marketdata.DataError   `json:"errors"`
	Sources         []marketdata.SourceState `json:"sources,omitempty"`
	Warnings        []string                 `json:"warnings,omitempty"`
	EvidenceProfile string                   `json:"evidenceProfile,omitempty"`
}

type themeSummaryDTO struct {
	ThemeID                  string                           `json:"themeId"`
	Name                     string                           `json:"name"`
	Aliases                  []string                         `json:"aliases"`
	SnapshotID               string                           `json:"snapshotId"`
	CycleNo                  int                              `json:"cycleNo"`
	LifecycleStage           themes.LifecycleStage            `json:"lifecycleStage"`
	PreviousLifecycleStage   *themes.LifecycleStage           `json:"previousLifecycleStage,omitempty"`
	StageChanged             bool                             `json:"stageChanged"`
	Rank                     int                              `json:"rank"`
	HeatScore                float64                          `json:"heatScore"`
	Summary                  string                           `json:"summary"`
	ConstituentCount         int                              `json:"constituentCount"`
	CatalystCount            int                              `json:"catalystCount"`
	ConflictingCatalystCount int                              `json:"conflictingCatalystCount"`
	RepresentativeSecurities []themeRepresentativeSecurityDTO `json:"representativeSecurities"`
	ObservedAt               time.Time                        `json:"observedAt"`
	FrozenAt                 time.Time                        `json:"frozenAt"`
}

type themeRepresentativeSecurityDTO struct {
	AssetType string `json:"assetType"`
	Market    string `json:"market"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	Role      string `json:"role"`
}
type themeListDataDTO struct {
	TradeDate  string            `json:"tradeDate"`
	Items      []themeSummaryDTO `json:"items"`
	NextCursor string            `json:"nextCursor,omitempty"`
}
type themeIdentityDTO struct {
	ThemeID     string   `json:"themeId"`
	Name        string   `json:"name"`
	Aliases     []string `json:"aliases"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
}
type themeSnapshotDTO struct {
	SnapshotID               string                `json:"snapshotId"`
	TradeDate                string                `json:"tradeDate"`
	CycleNo                  int                   `json:"cycleNo"`
	LifecycleStage           themes.LifecycleStage `json:"lifecycleStage"`
	Rank                     int                   `json:"rank"`
	HeatScore                float64               `json:"heatScore"`
	Summary                  string                `json:"summary"`
	ConstituentCount         int                   `json:"constituentCount"`
	CatalystCount            int                   `json:"catalystCount"`
	ConflictingCatalystCount int                   `json:"conflictingCatalystCount"`
	ObservedAt               time.Time             `json:"observedAt"`
	FrozenAt                 time.Time             `json:"frozenAt"`
}
type themeConstituentDTO struct {
	ConstituentID     string  `json:"constituentId"`
	AssetType         string  `json:"assetType"`
	Market            string  `json:"market"`
	Code              string  `json:"code"`
	Name              string  `json:"name"`
	Role              string  `json:"role"`
	Rank              int     `json:"rank"`
	ContributionScore float64 `json:"contributionScore"`
}
type themeCatalystSummaryDTO struct {
	Total       int  `json:"total"`
	Supports    int  `json:"supports"`
	Contradicts int  `json:"contradicts"`
	HasConflict bool `json:"hasConflict"`
}
type themeDetailDataDTO struct {
	Theme           themeIdentityDTO        `json:"theme"`
	Snapshot        *themeSnapshotDTO       `json:"snapshot"`
	Constituents    []themeConstituentDTO   `json:"constituents"`
	CatalystSummary themeCatalystSummaryDTO `json:"catalystSummary"`
}
type themeDailySnapshotDTO struct {
	SnapshotID       string                `json:"snapshotId"`
	TradeDate        string                `json:"tradeDate"`
	CycleNo          int                   `json:"cycleNo"`
	LifecycleStage   themes.LifecycleStage `json:"lifecycleStage"`
	Rank             int                   `json:"rank"`
	HeatScore        float64               `json:"heatScore"`
	Summary          string                `json:"summary"`
	ConstituentCount int                   `json:"constituentCount"`
	CatalystCount    int                   `json:"catalystCount"`
	FrozenAt         time.Time             `json:"frozenAt"`
}
type themeDailyDataDTO struct {
	ThemeID    string                  `json:"themeId"`
	Items      []themeDailySnapshotDTO `json:"items"`
	NextCursor string                  `json:"nextCursor,omitempty"`
}
type themeCatalystSourceDTO struct {
	SourceClaimID          string     `json:"sourceClaimId"`
	SourceName             string     `json:"sourceName"`
	SourceRef              string     `json:"sourceRef"`
	Stance                 string     `json:"stance"`
	SourceCredibilityScore int        `json:"sourceCredibilityScore"`
	Summary                string     `json:"summary"`
	PublishedAt            *time.Time `json:"publishedAt,omitempty"`
	AvailableAt            time.Time  `json:"availableAt"`
	CollectedAt            time.Time  `json:"collectedAt"`
	EvidenceItemIDs        []string   `json:"evidenceItemIds"`
}
type themeCatalystDTO struct {
	CatalystEventID  string                   `json:"catalystEventId"`
	EventType        string                   `json:"eventType"`
	Title            string                   `json:"title"`
	Summary          string                   `json:"summary"`
	EventAt          time.Time                `json:"eventAt"`
	FirstAvailableAt time.Time                `json:"firstAvailableAt"`
	CredibilityScore int                      `json:"credibilityScore"`
	Status           string                   `json:"status"`
	HasConflict      bool                     `json:"hasConflict"`
	Sources          []themeCatalystSourceDTO `json:"sources"`
}
type themeCatalystsDataDTO struct {
	ThemeID    string             `json:"themeId"`
	TradeDate  string             `json:"tradeDate"`
	Items      []themeCatalystDTO `json:"items"`
	NextCursor string             `json:"nextCursor,omitempty"`
}

func mapThemeListEnvelope(source marketdata.DataEnvelope[themes.ThemeListData], requestedDate string) themeEnvelopeDTO[themeListDataDTO] {
	tradeDate := source.Data.TradeDate
	if tradeDate == "" {
		tradeDate = requestedDate
	}
	items := make([]themeSummaryDTO, 0, len(source.Data.Items))
	for _, item := range source.Data.Items {
		if item.Snapshot == nil {
			continue
		}
		aliases := make([]string, 0)
		// List service intentionally returns compact items. Aliases remain a
		// required empty array when no expanded identity was requested.
		representatives := make([]themeRepresentativeSecurityDTO, 0, len(item.RepresentativeSecurities))
		for _, value := range item.RepresentativeSecurities {
			representatives = append(representatives, themeRepresentativeSecurityDTO{value.AssetType, value.Market, value.Code, value.Name, value.Role})
		}
		items = append(items, themeSummaryDTO{ThemeID: item.ID, Name: item.CanonicalName, Aliases: aliases, SnapshotID: item.Snapshot.ID, CycleNo: item.Snapshot.CycleNo,
			LifecycleStage: item.Snapshot.LifecycleStage, PreviousLifecycleStage: item.PreviousLifecycleStage, StageChanged: item.StageChanged, Rank: item.Snapshot.Rank,
			HeatScore: item.Snapshot.HeatScore, Summary: item.Snapshot.Summary, ConstituentCount: item.Snapshot.ConstituentCount, CatalystCount: item.Snapshot.CatalystCount,
			ConflictingCatalystCount: item.Snapshot.ConflictingCatalystCount, RepresentativeSecurities: representatives, ObservedAt: item.Snapshot.ObservedAt, FrozenAt: item.Snapshot.FrozenAt})
	}
	if tradeDate == "" {
		tradeDate = shanghaiDate(source.FetchedAt)
	}
	return mapThemeEnvelope(source, themeListDataDTO{TradeDate: tradeDate, Items: items, NextCursor: source.Data.NextCursor}, nil)
}

func mapThemeDetailEnvelope(source marketdata.DataEnvelope[themes.Theme], catalysts marketdata.DataEnvelope[themes.CatalystListData], requestedDate string) themeEnvelopeDTO[themeDetailDataDTO] {
	aliases := aliasNames(source.Data.Aliases)
	data := themeDetailDataDTO{Theme: themeIdentityDTO{source.Data.ID, source.Data.CanonicalName, aliases, source.Data.Description, source.Data.Status}, Constituents: []themeConstituentDTO{}}
	warnings := append([]string(nil), source.Warnings...)
	if source.Data.Snapshot == nil {
		dateLabel := requestedDate
		if dateLabel == "" {
			dateLabel = "latest"
		}
		warnings = append(warnings, "no frozen snapshot for "+dateLabel)
	} else {
		snapshot := mapThemeSnapshot(*source.Data.Snapshot)
		data.Snapshot = &snapshot
		for _, value := range source.Data.Snapshot.Constituents {
			data.Constituents = append(data.Constituents, themeConstituentDTO{value.ID, value.AssetType, value.Market, value.Code, value.Name, value.Role, value.Rank, value.ContributionScore})
		}
		data.CatalystSummary = summarizeCatalysts(catalysts.Data.Items, source.Data.Snapshot.FrozenAt)
	}
	return mapThemeEnvelope(source, data, warnings)
}

func mapThemeSnapshotsEnvelope(source marketdata.DataEnvelope[themes.SnapshotListData], themeID string) themeEnvelopeDTO[themeDailyDataDTO] {
	items := make([]themeDailySnapshotDTO, 0, len(source.Data.Items))
	for _, value := range source.Data.Items {
		items = append(items, themeDailySnapshotDTO{value.ID, value.TradeDate, value.CycleNo, value.LifecycleStage, value.Rank, value.HeatScore, value.Summary, value.ConstituentCount, value.CatalystCount, value.FrozenAt})
	}
	return mapThemeEnvelope(source, themeDailyDataDTO{ThemeID: themeID, Items: items, NextCursor: source.Data.NextCursor}, nil)
}

func mapThemeCatalystsEnvelope(source marketdata.DataEnvelope[themes.CatalystListData], themeID, tradeDate string, cutoff time.Time) themeEnvelopeDTO[themeCatalystsDataDTO] {
	items := make([]themeCatalystDTO, 0, len(source.Data.Items))
	for _, event := range source.Data.Items {
		if event.FirstAvailableAt == nil || (!cutoff.IsZero() && event.FirstAvailableAt.After(cutoff)) {
			continue
		}
		sources := make([]themeCatalystSourceDTO, 0, len(event.Claims))
		supports, contradicts := false, false
		for _, claim := range event.Claims {
			if claim.AvailableAt == nil || (!cutoff.IsZero() && claim.AvailableAt.After(cutoff)) {
				continue
			}
			evidenceItemIDs := append([]string(nil), claim.EvidenceItemIDs...)
			if evidenceItemIDs == nil {
				evidenceItemIDs = []string{}
			}
			sources = append(sources, themeCatalystSourceDTO{claim.ID, claim.SourceName, claim.SourceRef, claim.Stance, claim.SourceCredibilityScore, claim.Summary, claim.PublishedAt, *claim.AvailableAt, claim.CollectedAt, evidenceItemIDs})
			if claim.Stance == "supports" {
				supports = true
			}
			if claim.Stance == "contradicts" {
				contradicts = true
			}
		}
		items = append(items, themeCatalystDTO{event.ID, event.EventType, event.Title, event.Summary, event.EventAt, *event.FirstAvailableAt, event.CredibilityScore, event.Status, supports && contradicts, sources})
	}
	return mapThemeEnvelope(source, themeCatalystsDataDTO{ThemeID: themeID, TradeDate: tradeDate, Items: items, NextCursor: source.Data.NextCursor}, nil)
}

func mapThemeSnapshot(value themes.DailySnapshot) themeSnapshotDTO {
	return themeSnapshotDTO{value.ID, value.TradeDate, value.CycleNo, value.LifecycleStage, value.Rank, value.HeatScore, value.Summary, value.ConstituentCount, value.CatalystCount, value.ConflictingCatalystCount, value.ObservedAt, value.FrozenAt}
}
func summarizeCatalysts(items []themes.CatalystEvent, cutoff time.Time) themeCatalystSummaryDTO {
	result := themeCatalystSummaryDTO{}
	for _, event := range items {
		if event.FirstAvailableAt == nil || event.FirstAvailableAt.After(cutoff) {
			continue
		}
		result.Total++
		supports, contradicts := false, false
		for _, claim := range event.Claims {
			if claim.AvailableAt == nil || claim.AvailableAt.After(cutoff) {
				continue
			}
			if claim.Stance == "supports" {
				supports = true
			}
			if claim.Stance == "contradicts" {
				contradicts = true
			}
		}
		if supports {
			result.Supports++
		}
		if contradicts {
			result.Contradicts++
		}
		if supports && contradicts {
			result.HasConflict = true
		}
	}
	return result
}

func mapThemeEnvelope[S, T any](source marketdata.DataEnvelope[S], data T, warnings []string) themeEnvelopeDTO[T] {
	if warnings == nil {
		warnings = source.Warnings
	}
	errorsValue := source.Errors
	if errorsValue == nil {
		errorsValue = []marketdata.DataError{}
	}
	sources := append([]marketdata.SourceState(nil), source.Sources...)
	for index := range sources {
		sources[index].Status = themePublicStatus(sources[index].Status)
	}
	return themeEnvelopeDTO[T]{Data: data, Source: source.Source, AsOf: source.AsOf, FetchedAt: source.FetchedAt, Status: themePublicStatus(source.Status), Errors: errorsValue, Sources: sources, Warnings: warnings, EvidenceProfile: source.EvidenceProfile}
}

func aliasNames(values []themes.ThemeAlias) []string {
	aliases := make([]string, 0, len(values))
	for _, value := range values {
		aliases = append(aliases, value.Alias)
	}
	return aliases
}

func themePublicStatus(status string) string {
	switch status {
	case marketdata.StatusOK, marketdata.StatusPartial, marketdata.StatusStale, marketdata.StatusUnavailable, marketdata.StatusAfterCutoff:
		return status
	case marketdata.StatusEmpty:
		return marketdata.StatusOK
	default:
		return marketdata.StatusUnavailable
	}
}
func optionalThemeDate(w http.ResponseWriter, raw, name string) (string, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", true
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil || parsed.Format("2006-01-02") != value {
		writeThemeError(w, http.StatusBadRequest, name+" must be YYYY-MM-DD")
		return "", false
	}
	return value, true
}
func themeStage(w http.ResponseWriter, raw string) (themes.LifecycleStage, bool) {
	value := themes.LifecycleStage(strings.TrimSpace(raw))
	if value != "" && !themes.IsValidStage(value) {
		writeThemeError(w, http.StatusBadRequest, "invalid lifecycle stage")
		return "", false
	}
	return value, true
}
func validThemeCursor(raw string) bool {
	if raw == "" {
		return true
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return false
	}
	value, err := strconv.Atoi(string(decoded))
	return err == nil && value >= 0
}
func shanghaiDate(value time.Time) string {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		location = time.FixedZone("CST", 8*60*60)
	}
	if value.IsZero() {
		value = time.Now()
	}
	return value.In(location).Format("2006-01-02")
}
func writeThemeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
func writeThemeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, themes.ErrNotFound):
		writeThemeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, themes.ErrInvalidRequest), errors.Is(err, themes.ErrInvalidCursor):
		writeThemeError(w, http.StatusBadRequest, err.Error())
	default:
		writeThemeError(w, http.StatusInternalServerError, "theme service failed")
	}
}
