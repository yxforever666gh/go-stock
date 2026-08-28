package themes

import (
	"context"
	"errors"
	"time"

	"go-stock/backend/marketdata"
)

type LifecycleStage string

const (
	StageObserve    LifecycleStage = "观察"
	StageFerment    LifecycleStage = "发酵"
	StageAccelerate LifecycleStage = "加速"
	StageDiverge    LifecycleStage = "分歧"
	StageFade       LifecycleStage = "退潮"
)

var (
	ErrNotFound            = errors.New("theme not found")
	ErrSnapshotConflict    = errors.New("daily theme snapshot already frozen with different content")
	ErrInvalidLifecycle    = errors.New("invalid theme lifecycle transition")
	ErrInvalidCursor       = errors.New("invalid cursor")
	ErrInvalidRequest      = errors.New("invalid theme request")
	ErrSourceClaimConflict = errors.New("source link already belongs to a different claim")
)

type Theme struct {
	ID            string         `json:"id"`
	CanonicalName string         `json:"canonicalName"`
	Description   string         `json:"description,omitempty"`
	Status        string         `json:"status"`
	Aliases       []ThemeAlias   `json:"aliases,omitempty"`
	Snapshot      *DailySnapshot `json:"snapshot,omitempty"`
	CreatedAt     time.Time      `json:"createdAt"`
	UpdatedAt     time.Time      `json:"updatedAt"`
}

type ThemeAlias struct {
	ID        string    `json:"id"`
	Alias     string    `json:"alias"`
	Source    string    `json:"source,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

type RepresentativeSecurity struct {
	AssetType string `json:"assetType"`
	Market    string `json:"market"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	Role      string `json:"role,omitempty"`
}

type ThemeListItem struct {
	ID                       string                   `json:"id"`
	CanonicalName            string                   `json:"canonicalName"`
	Description              string                   `json:"description,omitempty"`
	Status                   string                   `json:"status"`
	Snapshot                 *DailySnapshot           `json:"snapshot,omitempty"`
	PreviousLifecycleStage   *LifecycleStage          `json:"previousLifecycleStage,omitempty"`
	StageChanged             bool                     `json:"stageChanged"`
	RepresentativeSecurities []RepresentativeSecurity `json:"representativeSecurities"`
}

type ThemeListData struct {
	TradeDate  string          `json:"tradeDate"`
	Items      []ThemeListItem `json:"items"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

type DailySnapshot struct {
	ID                       string                `json:"id"`
	ThemeID                  string                `json:"themeId"`
	TradeDate                string                `json:"tradeDate"`
	CycleNo                  int                   `json:"cycleNo"`
	LifecycleStage           LifecycleStage        `json:"lifecycleStage"`
	Rank                     int                   `json:"rank"`
	HeatScore                float64               `json:"heatScore"`
	Summary                  string                `json:"summary,omitempty"`
	ObservedAt               time.Time             `json:"observedAt"`
	FrozenAt                 time.Time             `json:"frozenAt"`
	ContentHash              string                `json:"contentHash"`
	ConstituentCount         int                   `json:"constituentCount"`
	CatalystCount            int                   `json:"catalystCount"`
	ConflictingCatalystCount int                   `json:"conflictingCatalystCount"`
	Constituents             []SnapshotConstituent `json:"constituents,omitempty"`
	CatalystIDs              []string              `json:"catalystIds,omitempty"`
	CreatedAt                time.Time             `json:"createdAt"`
}

type SnapshotConstituent struct {
	ID                string  `json:"id"`
	AssetType         string  `json:"assetType"`
	Market            string  `json:"market"`
	Code              string  `json:"code"`
	Name              string  `json:"name"`
	Role              string  `json:"role,omitempty"`
	Rank              int     `json:"rank"`
	ContributionScore float64 `json:"contributionScore"`
}

type SnapshotListData struct {
	Items      []DailySnapshot `json:"items"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

type CatalystEvent struct {
	ID               string        `json:"id"`
	ThemeID          string        `json:"themeId"`
	EventFingerprint string        `json:"eventFingerprint"`
	EventType        string        `json:"eventType"`
	Title            string        `json:"title"`
	Summary          string        `json:"summary,omitempty"`
	EventAt          time.Time     `json:"eventAt"`
	FirstAvailableAt *time.Time    `json:"firstAvailableAt,omitempty"`
	CredibilityScore int           `json:"credibilityScore"`
	Status           string        `json:"status"`
	EntityKeys       []string      `json:"entityKeys"`
	Claims           []SourceClaim `json:"claims,omitempty"`
	CreatedAt        time.Time     `json:"createdAt"`
}

type SourceClaim struct {
	ID                     string     `json:"id"`
	SourceName             string     `json:"sourceName"`
	SourceRef              string     `json:"sourceRef"`
	Stance                 string     `json:"stance"`
	SourceCredibilityScore int        `json:"sourceCredibilityScore"`
	Summary                string     `json:"summary"`
	ClaimFingerprint       string     `json:"claimFingerprint"`
	PublishedAt            *time.Time `json:"publishedAt,omitempty"`
	AvailableAt            *time.Time `json:"availableAt,omitempty"`
	CollectedAt            time.Time  `json:"collectedAt"`
	RawPayloadHash         string     `json:"rawPayloadHash"`
	EvidenceItemIDs        []string   `json:"evidenceItemIds"`
	CreatedAt              time.Time  `json:"createdAt"`
}

type CatalystListData struct {
	Items      []CatalystEvent `json:"items"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

type ListThemesRequest struct {
	Date   string
	Stage  LifecycleStage
	Query  string
	Sort   string
	Limit  int
	Cursor string
}

type ListSnapshotsRequest struct {
	ThemeID string
	From    string
	To      string
	Stage   LifecycleStage
	Limit   int
	Cursor  string
}

type ListCatalystsRequest struct {
	ThemeID        string
	Date           string
	Status         string
	MinCredibility int
	Limit          int
	Cursor         string
	Cutoff         *time.Time
}

type UpsertThemeRequest struct {
	ID            string
	CanonicalName string
	Description   string
	Status        string
	Aliases       []AliasInput
}

type AliasInput struct {
	ID     string
	Alias  string
	Source string
}

type FreezeSnapshotRequest struct {
	ThemeID        string
	TradeDate      string
	CycleNo        int
	LifecycleStage LifecycleStage
	Rank           int
	HeatScore      float64
	Summary        string
	ObservedAt     time.Time
	FrozenAt       time.Time
	Constituents   []SnapshotConstituent
	CatalystIDs    []string
}

type IngestCatalystRequest struct {
	ID               string
	ThemeID          string
	EventType        string
	Title            string
	Summary          string
	EventAt          time.Time
	CredibilityScore int
	Status           string
	EntityKeys       []string
	Claims           []ClaimInput
}

type ClaimInput struct {
	ID                     string
	SourceName             string
	SourceRef              string
	Stance                 string
	SourceCredibilityScore int
	Summary                string
	PublishedAt            *time.Time
	AvailableAt            *time.Time
	CollectedAt            time.Time
	RawPayloadHash         string
}

// EvidenceReader is deliberately narrow so both research runtimes can consume
// frozen theme evidence without gaining mutation access to the theme store.
type EvidenceReader interface {
	ResearchEvidence(context.Context, time.Time) marketdata.DataEnvelope[ResearchEvidence]
}

type ResearchEvidence struct {
	CutoffAt time.Time       `json:"cutoffAt"`
	Themes   []ResearchTheme `json:"themes"`
}

type ResearchTheme struct {
	ID                       string                `json:"id"`
	Name                     string                `json:"name"`
	Snapshot                 DailySnapshot         `json:"snapshot"`
	Catalysts                []CatalystEvent       `json:"catalysts"`
	Constituents             []SnapshotConstituent `json:"constituents"`
	BackgroundOnlyAssetTypes []string              `json:"backgroundOnlyAssetTypes,omitempty"`
}
