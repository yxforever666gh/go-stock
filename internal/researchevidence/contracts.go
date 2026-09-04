package researchevidence

import (
	"context"
	"time"
)

type SourceDocument struct {
	SourceID    string     `json:"sourceId"`
	SourceName  string     `json:"sourceName"`
	SourceRef   string     `json:"-"`
	Category    string     `json:"category"`
	CollectedAt time.Time  `json:"collectedAt"`
	AvailableAt *time.Time `json:"-"`
	Content     string     `json:"content"`
	// PromptContent is a compact, structurally valid representation used only
	// while composing model input. Content remains the audit/source snapshot.
	PromptContent string `json:"-"`
	Error         string `json:"error,omitempty"`
}

type StockCandidate struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type SourceCollector interface {
	CollectMarket(context.Context, time.Time) ([]SourceDocument, error)
	CollectSectors(context.Context, time.Time) ([]SourceDocument, error)
	CollectStocks(context.Context, time.Time, []StockCandidate) ([]SourceDocument, error)
}
