// Package marketintel owns normalized evidence shared by recommendation use cases.
package marketintel

import (
	"context"
	"encoding/json"
	"time"
)

type EvidenceType string

const (
	EvidenceEvent        EvidenceType = "event"
	EvidenceIndicator    EvidenceType = "indicator"
	EvidenceQuote        EvidenceType = "quote"
	EvidenceSecurity     EvidenceType = "security"
	EvidenceMarketRegime EvidenceType = "market_regime"
)

type Evidence struct {
	ID          string          `json:"id"`
	Type        EvidenceType    `json:"type"`
	Symbol      string          `json:"symbol,omitempty"`
	Title       string          `json:"title,omitempty"`
	Source      string          `json:"source"`
	SourceAt    time.Time       `json:"sourceAt"`
	AvailableAt time.Time       `json:"availableAt"`
	Payload     json.RawMessage `json:"payload,omitempty"`
}

type Query struct {
	Symbols []string
	Types   []EvidenceType
	Start   time.Time
	End     time.Time
	AsOf    time.Time
}

type Reader interface {
	Evidence(context.Context, Query) ([]Evidence, error)
}
