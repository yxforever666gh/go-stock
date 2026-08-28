package marketdata

import (
	"context"
	"strings"
	"time"
)

type SourceState struct {
	Provider    string     `json:"provider"`
	Status      string     `json:"status"`
	AsOf        time.Time  `json:"asOf,omitempty"`
	AvailableAt *time.Time `json:"availableAt,omitempty"`
	SourceRef   string     `json:"sourceRef,omitempty"`
	Message     string     `json:"message,omitempty"`
}

type DataError struct {
	Provider string `json:"provider"`
	Code     string `json:"code,omitempty"`
	Message  string `json:"message"`
}

// DataEnvelope is the single transport contract for every market-evidence
// endpoint. Data remains typed while status and provenance stay uniform.
type DataEnvelope[T any] struct {
	Data            T             `json:"data"`
	Source          string        `json:"source"`
	AsOf            time.Time     `json:"asOf"`
	FetchedAt       time.Time     `json:"fetchedAt"`
	Status          string        `json:"status"`
	Errors          []DataError   `json:"errors"`
	Sources         []SourceState `json:"sources,omitempty"`
	Warnings        []string      `json:"warnings,omitempty"`
	EvidenceProfile string        `json:"evidenceProfile,omitempty"`
	EvidenceSetID   string        `json:"evidenceSetId,omitempty"`
}

type ProviderRequest struct {
	Scope     string
	Symbol    string
	Code      string
	AssetType string
	Date      string
	Sort      string
	Cursor    string
	Limit     int
	CutoffAt  time.Time
}

type ProviderResult[T any] struct {
	Status      string
	AsOf        time.Time
	AvailableAt *time.Time
	Data        T
	SourceRef   string
	Warning     string
	Err         error
}

type Provider[T any] interface {
	Name() string
	Collect(context.Context, ProviderRequest) ProviderResult[T]
}

type PrimaryFallbackCollector[T any] struct {
	Primary  Provider[T]
	Fallback Provider[T]
}

// ProviderChainCollector tries providers in priority order. Complete data
// wins immediately; partial or stale data is retained as a candidate while
// later providers are given a chance to return a complete snapshot.
type ProviderChainCollector[T any] struct {
	Providers []Provider[T]
}

func (c ProviderChainCollector[T]) Collect(ctx context.Context, request ProviderRequest) DataEnvelope[T] {
	var zero T
	providers := make([]Provider[T], 0, len(c.Providers))
	for _, provider := range c.Providers {
		if provider != nil {
			providers = append(providers, provider)
		}
	}
	if len(providers) == 0 {
		return DataEnvelope[T]{Status: StatusUnavailable, Data: zero, Source: "", FetchedAt: time.Now(), Sources: []SourceState{}, Warnings: []string{"未配置数据源"}, Errors: []DataError{{Provider: "", Code: "provider_unconfigured", Message: "未配置数据源"}}}
	}

	type candidate struct {
		provider Provider[T]
		result   ProviderResult[T]
	}
	var best *candidate
	sources := make([]SourceState, 0, len(providers))
	warnings := make([]string, 0, len(providers))
	for _, provider := range providers {
		result := collectProvider(ctx, provider, request)
		sources = append(sources, sourceState(provider, result))
		warnings = append(warnings, warningsFor(result)...)
		if !usableStatus(result.Status) {
			continue
		}
		if result.Status == StatusOK || result.Status == StatusEmpty {
			return envelopeFrom(result, provider, sources, uniqueStrings(warnings))
		}
		if best == nil || (best.result.Status == StatusStale && result.Status == StatusPartial) {
			best = &candidate{provider: provider, result: result}
		}
	}
	if best != nil {
		return envelopeFrom(best.result, best.provider, sources, uniqueStrings(warnings))
	}
	return envelopeFrom(ProviderResult[T]{Status: StatusUnavailable, Data: zero}, providers[0], sources, uniqueStrings(warnings))
}

func (c PrimaryFallbackCollector[T]) Collect(ctx context.Context, request ProviderRequest) DataEnvelope[T] {
	var zero T
	if c.Primary == nil && c.Fallback == nil {
		return DataEnvelope[T]{Status: StatusUnavailable, Data: zero, Source: "", FetchedAt: time.Now(), Sources: []SourceState{}, Warnings: []string{"未配置数据源"}, Errors: []DataError{{Provider: "", Code: "provider_unconfigured", Message: "未配置数据源"}}}
	}
	primary := collectProvider(ctx, c.Primary, request)
	sources := []SourceState{sourceState(c.Primary, primary)}
	warnings := warningsFor(primary)
	if usableStatus(primary.Status) && primary.Status != StatusPartial {
		return envelopeFrom(primary, c.Primary, sources, warnings)
	}
	if c.Fallback == nil {
		return envelopeFrom(primary, c.Primary, sources, warnings)
	}
	fallback := collectProvider(ctx, c.Fallback, request)
	sources = append(sources, sourceState(c.Fallback, fallback))
	warnings = append(warnings, warningsFor(fallback)...)
	if usableStatus(fallback.Status) {
		return envelopeFrom(fallback, c.Fallback, sources, uniqueStrings(warnings))
	}
	if usableStatus(primary.Status) {
		return envelopeFrom(primary, c.Primary, sources, uniqueStrings(warnings))
	}
	result := ProviderResult[T]{Status: StatusUnavailable, Data: zero}
	return envelopeFrom(result, c.Primary, sources, uniqueStrings(warnings))
}

func envelopeFrom[T any](result ProviderResult[T], provider Provider[T], sources []SourceState, warnings []string) DataEnvelope[T] {
	name := ""
	if provider != nil {
		name = provider.Name()
	}
	errors := make([]DataError, 0)
	for _, state := range sources {
		if state.Status != StatusUnavailable && state.Status != StatusFailed {
			continue
		}
		message := strings.TrimSpace(state.Message)
		if message == "" {
			message = "数据源不可用"
		}
		errors = append(errors, DataError{Provider: state.Provider, Code: "provider_unavailable", Message: message})
	}
	status := publicStatus(result.Status)
	return DataEnvelope[T]{Data: result.Data, Source: name, AsOf: result.AsOf, FetchedAt: time.Now(), Status: status, Errors: errors, Sources: sources, Warnings: warnings}
}

func collectProvider[T any](ctx context.Context, provider Provider[T], request ProviderRequest) ProviderResult[T] {
	var zero T
	if provider == nil {
		return ProviderResult[T]{Status: StatusUnavailable, Data: zero}
	}
	result := provider.Collect(ctx, request)
	result.Status = normalizedStatus(result.Status)
	if result.Err != nil && usableStatus(result.Status) {
		result.Status = StatusPartial
	}
	if result.Err != nil && strings.TrimSpace(result.Warning) == "" {
		result.Warning = result.Err.Error()
	}
	return result
}

func sourceState[T any](provider Provider[T], result ProviderResult[T]) SourceState {
	name := "unconfigured"
	if provider != nil {
		name = provider.Name()
	}
	message := strings.TrimSpace(result.Warning)
	if message == "" && result.Err != nil {
		message = result.Err.Error()
	}
	return SourceState{Provider: name, Status: publicStatus(result.Status), AsOf: result.AsOf, AvailableAt: result.AvailableAt, SourceRef: result.SourceRef, Message: message}
}

func warningsFor[T any](result ProviderResult[T]) []string {
	if strings.TrimSpace(result.Warning) != "" {
		return []string{strings.TrimSpace(result.Warning)}
	}
	if result.Err != nil {
		return []string{result.Err.Error()}
	}
	return []string{}
}

func usableStatus(status string) bool {
	switch normalizedStatus(status) {
	case StatusOK, StatusPartial, StatusEmpty, StatusStale:
		return true
	default:
		return false
	}
}

func normalizedStatus(status string) string {
	switch strings.TrimSpace(status) {
	case StatusOK, StatusPartial, StatusUnavailable, StatusEmpty, StatusStale, StatusFailed, StatusAfterCutoff, StatusCollecting, StatusFrozen:
		return strings.TrimSpace(status)
	default:
		return StatusUnavailable
	}
}

func publicStatus(status string) string {
	switch normalizedStatus(status) {
	case StatusOK, StatusPartial, StatusStale, StatusUnavailable, StatusAfterCutoff:
		return normalizedStatus(status)
	case StatusEmpty:
		return StatusOK
	default:
		return StatusUnavailable
	}
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
