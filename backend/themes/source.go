package themes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"go-stock/backend/marketdata"
)

// RawThemeSignal is the provider-neutral input contract for the theme
// lifecycle. Provider packages can collect signals without depending on theme
// persistence or lifecycle decisions.
type RawThemeSignal struct {
	ThemeName              string                 `json:"themeName"`
	Aliases                []string               `json:"aliases,omitempty"`
	Kind                   string                 `json:"kind"`
	EventType              string                 `json:"eventType"`
	Title                  string                 `json:"title"`
	Summary                string                 `json:"summary,omitempty"`
	EventAt                time.Time              `json:"eventAt"`
	PublishedAt            *time.Time             `json:"publishedAt,omitempty"`
	FirstObservedAt        time.Time              `json:"firstObservedAt"`
	AvailableAt            time.Time              `json:"availableAt"`
	CollectedAt            time.Time              `json:"collectedAt"`
	SourceName             string                 `json:"sourceName"`
	SourceRef              string                 `json:"sourceRef,omitempty"`
	Stance                 string                 `json:"stance"`
	SourceCredibilityScore int                    `json:"sourceCredibilityScore"`
	Rank                   int                    `json:"rank,omitempty"`
	HeatScore              float64                `json:"heatScore,omitempty"`
	Securities             []RawThemeSecurity     `json:"securities,omitempty"`
	RawPayloadHash         string                 `json:"rawPayloadHash"`
	Metadata               map[string]interface{} `json:"metadata,omitempty"`
}

type RawThemeSecurity struct {
	AssetType string `json:"assetType"`
	Market    string `json:"market"`
	Code      string `json:"code"`
	Name      string `json:"name,omitempty"`
	Role      string `json:"role,omitempty"`
}

const (
	ThemeSignalHotTopic     = "hot_topic"
	ThemeSignalHotEvent     = "hot_event"
	ThemeSignalNews         = "news"
	ThemeSignalAnnouncement = "announcement"
	ThemeSignalConcept      = "concept"
	ThemeSignalFundFlow     = "fund_flow"

	ThemeSignalSupports    = "supports"
	ThemeSignalContradicts = "contradicts"

	themeSourceStatusTimeout = "timeout"
	themeSourceStatusError   = "error"
)

// SourceAdapter is deliberately injectable. Provider packages wrap their
// existing fetchers; tests can supply context-aware implementations without a
// network client.
type SourceAdapter interface {
	Name() string
	Collect(ctx context.Context, firstObservedAt time.Time) ([]RawThemeSignal, error)
}

type SourceAdapterFunc struct {
	SourceName  string
	CollectFunc func(context.Context, time.Time) ([]RawThemeSignal, error)
}

func (adapter SourceAdapterFunc) Name() string {
	if strings.TrimSpace(adapter.SourceName) == "" {
		return "unknown"
	}
	return strings.TrimSpace(adapter.SourceName)
}

func (adapter SourceAdapterFunc) Collect(ctx context.Context, firstObservedAt time.Time) ([]RawThemeSignal, error) {
	if adapter.CollectFunc == nil {
		return nil, errors.New("theme source adapter has no collect function")
	}
	return adapter.CollectFunc(ctx, firstObservedAt)
}

type ThemeSourceError struct {
	Source  string `json:"source"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ThemeSourceState struct {
	Source         string `json:"source"`
	Status         string `json:"status"`
	SignalCount    int    `json:"signalCount"`
	DuplicateCount int    `json:"duplicateCount"`
	ElapsedMillis  int64  `json:"elapsedMillis"`
	Error          string `json:"error,omitempty"`
}

type ThemeSourceBatch struct {
	Signals     []RawThemeSignal   `json:"signals"`
	Sources     []ThemeSourceState `json:"sources"`
	Errors      []ThemeSourceError `json:"errors"`
	Status      string             `json:"status"`
	ObservedAt  time.Time          `json:"observedAt"`
	CollectedAt time.Time          `json:"collectedAt"`
}

// ThemeSourceAggregator executes every source independently. The extra
// buffered invocation channel is intentional: several legacy fetchers do not
// accept context cancellation. A timed-out legacy call may finish later, but
// it can never hold up this batch or block while reporting its late result.
type ThemeSourceAggregator struct {
	Adapters         []SourceAdapter
	PerSourceTimeout time.Duration
	Now              func() time.Time
}

func NewThemeSourceAggregator(timeout time.Duration, adapters ...SourceAdapter) *ThemeSourceAggregator {
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	return &ThemeSourceAggregator{
		Adapters:         append([]SourceAdapter(nil), adapters...),
		PerSourceTimeout: timeout,
		Now:              time.Now,
	}
}

type themeSourceAdapterResult struct {
	index   int
	signals []RawThemeSignal
	state   ThemeSourceState
	err     *ThemeSourceError
}

func (aggregator *ThemeSourceAggregator) Collect(ctx context.Context, firstObservedAt time.Time) ThemeSourceBatch {
	now := time.Now
	if aggregator != nil && aggregator.Now != nil {
		now = aggregator.Now
	}
	if firstObservedAt.IsZero() {
		firstObservedAt = now()
	}
	batch := ThemeSourceBatch{
		Signals: []RawThemeSignal{}, Sources: []ThemeSourceState{}, Errors: []ThemeSourceError{},
		Status: marketdata.StatusUnavailable, ObservedAt: firstObservedAt,
	}
	if aggregator == nil || len(aggregator.Adapters) == 0 {
		batch.CollectedAt = now()
		return batch
	}

	timeout := aggregator.PerSourceTimeout
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	results := make(chan themeSourceAdapterResult, len(aggregator.Adapters))
	var wait sync.WaitGroup
	for index, adapter := range aggregator.Adapters {
		wait.Add(1)
		go func(index int, adapter SourceAdapter) {
			defer wait.Done()
			results <- collectThemeSourceAdapter(ctx, adapter, index, firstObservedAt, timeout, now)
		}(index, adapter)
	}
	wait.Wait()
	close(results)

	ordered := make([]themeSourceAdapterResult, len(aggregator.Adapters))
	for result := range results {
		ordered[result.index] = result
	}
	seen := make(map[string]struct{})
	degraded := false
	for _, result := range ordered {
		state := result.state
		if result.err != nil {
			batch.Errors = append(batch.Errors, *result.err)
			degraded = true
		}
		for _, signal := range result.signals {
			signal = NormalizeRawThemeSignal(signal, state.Source, firstObservedAt)
			if strings.TrimSpace(signal.ThemeName) == "" || strings.TrimSpace(signal.Title) == "" {
				state.DuplicateCount++
				degraded = true
				continue
			}
			key := themeSourceDedupeKey(signal)
			if _, exists := seen[key]; exists {
				state.DuplicateCount++
				degraded = true
				continue
			}
			seen[key] = struct{}{}
			batch.Signals = append(batch.Signals, signal)
			state.SignalCount++
		}
		if state.Status == marketdata.StatusOK && state.DuplicateCount > 0 {
			state.Status = marketdata.StatusPartial
		}
		if state.Status != marketdata.StatusOK {
			degraded = true
		}
		batch.Sources = append(batch.Sources, state)
	}

	sort.SliceStable(batch.Signals, func(i, j int) bool {
		if batch.Signals[i].AvailableAt.Equal(batch.Signals[j].AvailableAt) {
			return themeSourceDedupeKey(batch.Signals[i]) < themeSourceDedupeKey(batch.Signals[j])
		}
		return batch.Signals[i].AvailableAt.Before(batch.Signals[j].AvailableAt)
	})
	batch.CollectedAt = now()
	switch {
	case len(batch.Signals) == 0 && len(batch.Errors) == 0:
		batch.Status = marketdata.StatusEmpty
	case len(batch.Signals) == 0:
		batch.Status = marketdata.StatusUnavailable
	case degraded:
		batch.Status = marketdata.StatusPartial
	default:
		batch.Status = marketdata.StatusOK
	}
	return batch
}

func collectThemeSourceAdapter(parent context.Context, adapter SourceAdapter, index int, observedAt time.Time, timeout time.Duration, now func() time.Time) themeSourceAdapterResult {
	started := now()
	name := "unknown"
	if adapter != nil {
		name = adapter.Name()
	}
	result := themeSourceAdapterResult{index: index, state: ThemeSourceState{Source: name, Status: marketdata.StatusOK}}
	if adapter == nil {
		result.state.Status = themeSourceStatusError
		result.state.Error = "nil source adapter"
		result.err = &ThemeSourceError{Source: name, Code: "adapter_unconfigured", Message: result.state.Error}
		return result
	}

	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	type invocation struct {
		signals []RawThemeSignal
		err     error
	}
	finished := make(chan invocation, 1)
	go func() {
		signals, err := adapter.Collect(ctx, observedAt)
		finished <- invocation{signals: signals, err: err}
	}()
	select {
	case call := <-finished:
		result.signals = call.signals
		if call.err != nil {
			result.state.Status = themeSourceStatusError
			result.state.Error = call.err.Error()
			result.err = &ThemeSourceError{Source: name, Code: "source_error", Message: call.err.Error()}
		} else if len(call.signals) == 0 {
			result.state.Status = marketdata.StatusEmpty
		}
	case <-ctx.Done():
		code := "source_timeout"
		result.state.Status = themeSourceStatusTimeout
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			code = "source_canceled"
			result.state.Status = themeSourceStatusError
		}
		result.state.Error = ctx.Err().Error()
		result.err = &ThemeSourceError{Source: name, Code: code, Message: ctx.Err().Error()}
	}
	result.state.ElapsedMillis = now().Sub(started).Milliseconds()
	return result
}

func NormalizeRawThemeSignal(signal RawThemeSignal, adapterName string, observedAt time.Time) RawThemeSignal {
	signal.ThemeName = strings.TrimSpace(signal.ThemeName)
	signal.Title = strings.TrimSpace(signal.Title)
	signal.Summary = strings.TrimSpace(signal.Summary)
	signal.SourceName = strings.TrimSpace(signal.SourceName)
	if signal.SourceName == "" {
		signal.SourceName = strings.TrimSpace(adapterName)
	}
	signal.SourceRef = strings.TrimSpace(signal.SourceRef)
	if signal.Stance != ThemeSignalContradicts {
		signal.Stance = ThemeSignalSupports
	}
	if signal.SourceCredibilityScore < 0 {
		signal.SourceCredibilityScore = 0
	}
	if signal.SourceCredibilityScore > 100 {
		signal.SourceCredibilityScore = 100
	}
	if signal.FirstObservedAt.IsZero() {
		signal.FirstObservedAt = observedAt
	}
	if signal.CollectedAt.IsZero() {
		signal.CollectedAt = observedAt
	}
	availableAt := signal.FirstObservedAt
	if signal.PublishedAt != nil && !signal.PublishedAt.IsZero() && signal.PublishedAt.After(availableAt) {
		availableAt = *signal.PublishedAt
	}
	signal.AvailableAt = availableAt
	if signal.EventAt.IsZero() {
		if signal.PublishedAt != nil && !signal.PublishedAt.IsZero() {
			signal.EventAt = *signal.PublishedAt
		} else {
			signal.EventAt = signal.FirstObservedAt
		}
	}
	if signal.SourceCredibilityScore == 0 {
		signal.SourceCredibilityScore = 60
	}
	if signal.RawPayloadHash == "" {
		signal.RawPayloadHash = hashThemeSourcePayload(struct {
			ThemeName string
			Kind      string
			Title     string
			Summary   string
			Source    string
			Ref       string
			Stance    string
			EventAt   time.Time
		}{signal.ThemeName, signal.Kind, signal.Title, signal.Summary, signal.SourceName, signal.SourceRef, signal.Stance, signal.EventAt})
	}
	return signal
}

func themeSourceDedupeKey(signal RawThemeSignal) string {
	parts := []string{
		themeSourceCanonicalText(signal.ThemeName),
		themeSourceCanonicalText(signal.Kind),
		themeSourceCanonicalText(signal.EventType),
		themeSourceCanonicalText(signal.Title),
		themeSourceCanonicalText(signal.Summary),
		themeSourceCanonicalText(signal.SourceName),
		themeSourceCanonicalText(signal.SourceRef),
		themeSourceCanonicalText(signal.Stance),
		signal.EventAt.UTC().Format(time.RFC3339Nano),
	}
	return strings.Join(parts, "|")
}

func themeSourceCanonicalText(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func hashThemeSourcePayload(value interface{}) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		encoded = []byte(fmt.Sprintf("%v", value))
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
