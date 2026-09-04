package ai

import (
	"context"
	"time"
)

// Message is the provider-neutral conversation content needed for a model call.
type Message struct {
	RecommendationID string
	Role             string
	Content          string
}

type CompletionRequest struct {
	RecommendationID   string
	Phase              string
	Prompt             string
	Messages           []Message
	PreviousResponseID string
	OnAttempt          func(ModelAttemptRecord)
}

type CompletionResult struct {
	Content    string
	ResponseID string
	Model      string
}

type AIClient interface {
	Complete(context.Context, CompletionRequest) (CompletionResult, error)
}

// ModelAttemptRecord is the sanitized state of one provider call. It excludes
// prompts, response bodies, headers, and credentials.
type ModelAttemptRecord struct {
	ID                        string     `json:"id"`
	Phase                     string     `json:"phase"`
	ConfigID                  uint       `json:"configId"`
	ProviderName              string     `json:"providerName"`
	ModelName                 string     `json:"modelName"`
	APIProtocol               string     `json:"apiProtocol"`
	MaxTokens                 int        `json:"maxTokens"`
	Temperature               float64    `json:"temperature"`
	RequestTimeoutSeconds     int        `json:"requestTimeoutSeconds"`
	InactivityTimeoutSeconds  int        `json:"inactivityTimeoutSeconds"`
	FallbackIndex             int        `json:"fallbackIndex"`
	FallbackCount             int        `json:"fallbackCount"`
	ForcedConfig              bool       `json:"forcedConfig"`
	PreviousResponseIDPresent bool       `json:"previousResponseIdPresent"`
	Attempt                   int        `json:"attempt"`
	MaxAttempts               int        `json:"maxAttempts"`
	StartedAt                 time.Time  `json:"startedAt"`
	LastActivityAt            *time.Time `json:"lastActivityAt,omitempty"`
	CompletedAt               *time.Time `json:"completedAt,omitempty"`
	DurationMS                int64      `json:"durationMs"`
	Status                    string     `json:"status"`
	LastEventType             string     `json:"lastEventType,omitempty"`
	HTTPStatus                int        `json:"httpStatus,omitempty"`
	ErrorCategory             string     `json:"errorCategory,omitempty"`
	ErrorMessage              string     `json:"errorMessage,omitempty"`
	Retryable                 bool       `json:"retryable"`
	NextAction                string     `json:"nextAction,omitempty"`
}

// AuditModelParameters returns a secret-free snapshot of the effective model
// configuration and retry/fallback policy used by the last provider attempt.
func AuditModelParameters(records []ModelAttemptRecord) map[string]any {
	result := map[string]any{"providerAttemptCount": 0}
	if len(records) == 0 {
		return result
	}
	latestByID := make(map[string]ModelAttemptRecord, len(records))
	order := make([]string, 0, len(records))
	for _, record := range records {
		if _, exists := latestByID[record.ID]; !exists {
			order = append(order, record.ID)
		}
		latestByID[record.ID] = record
	}
	last := latestByID[order[len(order)-1]]
	result["providerAttemptCount"] = len(order)
	result["configId"] = last.ConfigID
	result["apiProtocol"] = last.APIProtocol
	result["maxTokens"] = last.MaxTokens
	result["temperature"] = last.Temperature
	result["requestTimeoutSeconds"] = last.RequestTimeoutSeconds
	result["inactivityTimeoutSeconds"] = last.InactivityTimeoutSeconds
	result["attempt"] = last.Attempt
	result["maxAttempts"] = last.MaxAttempts
	result["fallbackIndex"] = last.FallbackIndex
	result["fallbackCount"] = last.FallbackCount
	result["forcedConfig"] = last.ForcedConfig
	result["previousResponseIdPresent"] = last.PreviousResponseIDPresent
	return result
}
