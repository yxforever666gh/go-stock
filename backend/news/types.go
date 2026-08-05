// Package news defines provider-neutral news observations.
package news

import (
	"context"
	"encoding/json"
	"time"
)

type Scope string

const (
	ScopeMarket   Scope = "market"
	ScopeSecurity Scope = "security"
	ScopeIndustry Scope = "industry"
)

type Item struct {
	ID          string          `json:"id"`
	Scope       Scope           `json:"scope"`
	Symbols     []string        `json:"symbols,omitempty"`
	Industries  []string        `json:"industries,omitempty"`
	Title       string          `json:"title"`
	Summary     string          `json:"summary"`
	URL         string          `json:"url"`
	PublishedAt time.Time       `json:"publishedAt"`
	Source      string          `json:"source"`
	SourceAt    time.Time       `json:"sourceAt"`
	AvailableAt time.Time       `json:"availableAt"`
	Payload     json.RawMessage `json:"payload,omitempty"`
}

type Query struct {
	Scope    Scope
	Symbol   string
	Industry string
	Start    time.Time
	End      time.Time
	AsOf     time.Time
	Limit    int
}

// Reader intentionally exposes observations only. Fetching and caching are
// adapter responsibilities and are not part of the recommendation contract.
type Reader interface {
	List(context.Context, Query) ([]Item, error)
}
