package research

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	LifecycleQuoteSourceSuffix  = "Q"
	LifecycleMinuteSourceSuffix = "M"
)

type LifecycleContextRequest struct {
	ObservationID     string
	Recommendation    Recommendation
	Phase             string
	WindowFrom        time.Time
	Now               time.Time
	Position          *Position
	KnownFingerprints map[string]struct{}
}

type LifecycleObservationDraft struct {
	Quote           Quote
	MinuteSummary   MinuteEvidenceSummary
	Sources         []LifecycleEvidenceSource
	Status          string
	CriticalFailure string
}

type LifecycleContextProvider interface {
	CollectLifecycleContext(context.Context, LifecycleContextRequest) (LifecycleObservationDraft, error)
}

func LifecycleSourceID(observationID, suffix string) string {
	compact := strings.ReplaceAll(strings.TrimSpace(observationID), "-", "")
	if len(compact) > 10 {
		compact = compact[:10]
	}
	return "OBS-" + strings.ToUpper(compact) + "-" + strings.ToUpper(strings.TrimSpace(suffix))
}

func EvidenceFingerprint(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])
}

func NewLifecycleObservation(request LifecycleContextRequest, draft LifecycleObservationDraft) (LifecycleObservation, error) {
	if strings.TrimSpace(draft.Status) == "" {
		if strings.TrimSpace(draft.CriticalFailure) != "" {
			draft.Status = "critical_failed"
		} else {
			draft.Status = "ready"
		}
	}
	for index := range draft.Sources {
		if draft.Sources[index].ID != "" {
			continue
		}
		suffix := strings.ToUpper(strings.TrimSpace(draft.Sources[index].Category))
		if draft.Sources[index].Category == "quote" {
			suffix = LifecycleQuoteSourceSuffix
		} else if draft.Sources[index].Category == "minute" {
			suffix = LifecycleMinuteSourceSuffix
		}
		draft.Sources[index].ID = LifecycleSourceID(request.ObservationID, suffix)
	}
	quoteJSON, err := json.Marshal(draft.Quote)
	if err != nil {
		return LifecycleObservation{}, fmt.Errorf("marshal lifecycle quote: %w", err)
	}
	minuteJSON, err := json.Marshal(draft.MinuteSummary)
	if err != nil {
		return LifecycleObservation{}, fmt.Errorf("marshal lifecycle minute summary: %w", err)
	}
	evidenceJSON, err := json.Marshal(draft.Sources)
	if err != nil {
		return LifecycleObservation{}, fmt.Errorf("marshal lifecycle evidence: %w", err)
	}
	type sourceStatus struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
		Error  string `json:"error,omitempty"`
	}
	statuses := make([]sourceStatus, 0, len(draft.Sources))
	for _, source := range draft.Sources {
		statuses = append(statuses, sourceStatus{ID: source.ID, Name: source.Name, Status: source.Status, Error: source.Error})
	}
	statusJSON, err := json.Marshal(statuses)
	if err != nil {
		return LifecycleObservation{}, fmt.Errorf("marshal lifecycle source status: %w", err)
	}
	fingerprint := EvidenceFingerprint(string(quoteJSON) + "\n" + string(minuteJSON) + "\n" + string(evidenceJSON))
	return LifecycleObservation{
		ObservationID: request.ObservationID, RecommendationID: request.Recommendation.RecommendationID,
		Phase: request.Phase, WindowFrom: request.WindowFrom, ObservedAt: request.Now, Status: draft.Status,
		QuoteJSON: string(quoteJSON), MinuteSummaryJSON: string(minuteJSON), EvidenceJSON: string(evidenceJSON),
		SourceStatusJSON: string(statusJSON), CriticalFailure: strings.TrimSpace(draft.CriticalFailure), ContentFingerprint: fingerprint,
	}, nil
}

func ParseLifecycleEvidence(observation LifecycleObservation) []LifecycleEvidenceSource {
	var sources []LifecycleEvidenceSource
	_ = json.Unmarshal([]byte(observation.EvidenceJSON), &sources)
	return sources
}

func ObservationHasSource(observation LifecycleObservation, sourceID string) bool {
	for _, source := range ParseLifecycleEvidence(observation) {
		if source.ID == sourceID && (source.Status == "ok" || source.Status == "empty" || source.Status == "unchanged") {
			return true
		}
	}
	return false
}
