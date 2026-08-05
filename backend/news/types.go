// Package news defines provider-neutral news observations.
package news

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrInvalidWindow = errors.New("invalid news window")

type WindowStatus string

const (
	WindowStatusOK     WindowStatus = "ok"
	WindowStatusEmpty  WindowStatus = "empty"
	WindowStatusFailed WindowStatus = "failed"
	WindowStatusStale  WindowStatus = "stale"
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
	Sources  []string
	Scope    Scope
	Symbol   string
	Industry string
	Start    time.Time
	End      time.Time
	AsOf     time.Time
	Limit    int
}

type NewsWindowResult struct {
	Items   []Item       `json:"items"`
	Status  WindowStatus `json:"status"`
	Sources []string     `json:"sources"`
	From    time.Time    `json:"from"`
	To      time.Time    `json:"to"`
	Warning string       `json:"warning,omitempty"`
}

func (q Query) Validate() error {
	if q.AsOf.IsZero() || q.Start.IsZero() || q.End.IsZero() {
		return fmt.Errorf("%w: start, end and asOf are required", ErrInvalidWindow)
	}
	if q.End.Before(q.Start) || q.End.After(q.AsOf) {
		return fmt.Errorf("%w: require start <= end <= asOf", ErrInvalidWindow)
	}
	if q.Scope != "" && q.Scope != ScopeMarket && q.Scope != ScopeSecurity && q.Scope != ScopeIndustry {
		return fmt.Errorf("%w: unsupported scope %q", ErrInvalidWindow, q.Scope)
	}
	if q.Scope == ScopeSecurity && strings.TrimSpace(q.Symbol) == "" {
		return fmt.Errorf("%w: security scope requires symbol", ErrInvalidWindow)
	}
	if q.Scope == ScopeIndustry && strings.TrimSpace(q.Industry) == "" {
		return fmt.Errorf("%w: industry scope requires industry", ErrInvalidWindow)
	}
	return nil
}

// Reader intentionally exposes observations only. Fetching and caching are
// adapter responsibilities and are not part of the recommendation contract.
type Reader interface {
	GetNewsWindow(context.Context, Query) (NewsWindowResult, error)
}
