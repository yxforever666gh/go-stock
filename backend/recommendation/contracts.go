// Package recommendation defines the V1.5 recommendation production boundary.
package recommendation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go-stock/backend/marketintel"
	"go-stock/backend/strategy/v150"
)

var ErrInvalidPublication = errors.New("invalid recommendation publication")

type CandidateSource interface {
	Candidates(context.Context, CandidateRequest) ([]v150.Candidate, error)
}

type CandidateRequest struct {
	RunContext v150.RunContext
	Benchmark  v150.BenchmarkSnapshot
}

// EventVerifier is defined by the recommendation consumer. One call carries
// the already-determined verification batch without serializing or reshaping
// provider messages at the boundary.
type EventVerifier interface {
	Verify(context.Context, EventVerificationCall) (EventVerificationCompletion, error)
}

type EventVerificationCall struct {
	Messages []map[string]any
	Think    bool
}

type EventVerificationCompletion struct {
	Content    string
	ResponseID string
	Model      string
}

type VerificationResult struct {
	Verified   bool                   `json:"verified"`
	Decision   string                 `json:"decision"`
	Reason     string                 `json:"reason,omitempty"`
	Evidence   []marketintel.Evidence `json:"evidence"`
	Provider   string                 `json:"provider"`
	Model      string                 `json:"model,omitempty"`
	PromptHash string                 `json:"promptHash,omitempty"`
	VerifiedAt time.Time              `json:"verifiedAt"`
}

type RunSnapshot struct {
	Context      v150.RunContext        `json:"context"`
	TradeDate    string                 `json:"tradeDate"`
	RunSlot      string                 `json:"runSlot"`
	Benchmark    v150.BenchmarkSnapshot `json:"benchmark"`
	Regime       v150.RegimeDecision    `json:"regime"`
	InputHash    string                 `json:"inputHash"`
	SnapshotHash string                 `json:"snapshotHash"`
	FrozenAt     time.Time              `json:"frozenAt"`
}

type CandidateSnapshot struct {
	CandidateID  string                 `json:"candidateId"`
	RunID        string                 `json:"runId"`
	Candidate    v150.ScoredCandidate   `json:"candidate"`
	Verification *VerificationResult    `json:"verification,omitempty"`
	Evidence     []marketintel.Evidence `json:"evidence"`
	SnapshotHash string                 `json:"snapshotHash"`
	FrozenAt     time.Time              `json:"frozenAt"`
}

type RuleSnapshot struct {
	RuleID       string         `json:"ruleId"`
	RunID        string         `json:"runId"`
	CandidateID  string         `json:"candidateId"`
	Plan         v150.TradePlan `json:"plan"`
	SnapshotHash string         `json:"snapshotHash"`
	FrozenAt     time.Time      `json:"frozenAt"`
}

type InitialOrderEvent struct {
	EventID      string          `json:"eventId"`
	RunID        string          `json:"runId"`
	RuleID       string          `json:"ruleId,omitempty"`
	Symbol       string          `json:"symbol"`
	EventType    string          `json:"eventType"`
	Sequence     int             `json:"sequence"`
	EventAt      time.Time       `json:"eventAt"`
	Payload      json.RawMessage `json:"payload"`
	SnapshotHash string          `json:"snapshotHash"`
	FrozenAt     time.Time       `json:"frozenAt"`
}

// DisplayProjection is a compatibility view for old tables and UI queries. It
// is output-only and is never consumed to decide strategy or execution state.
type DisplayProjection struct {
	ProjectionID string          `json:"projectionId"`
	RunID        string          `json:"runId"`
	RuleID       string          `json:"ruleId,omitempty"`
	Symbol       string          `json:"symbol"`
	Payload      json.RawMessage `json:"payload"`
}

// Publication is the complete transaction boundary: final quota/rank output,
// immutable snapshots, initial events, and the compatibility projection.
type Publication struct {
	Run                RunSnapshot         `json:"run"`
	Candidates         []CandidateSnapshot `json:"candidates"`
	Rules              []RuleSnapshot      `json:"rules"`
	InitialOrderEvents []InitialOrderEvent `json:"initialOrderEvents"`
	DisplayProjections []DisplayProjection `json:"displayProjections"`
}

// Publisher must commit all fields of Publication in one database transaction.
// Implementations must never expose per-table write methods to this package.
type Publisher interface {
	Publish(context.Context, Publication) error
}

type SnapshotReader interface {
	Run(context.Context, string) (RunSnapshot, error)
	Candidates(context.Context, string) ([]CandidateSnapshot, error)
	Rules(context.Context, string) ([]RuleSnapshot, error)
}

func (p Publication) Validate() error {
	runID := strings.TrimSpace(p.Run.Context.RunID)
	if runID == "" || strings.TrimSpace(p.Run.Context.StrategyVersion) == "" || p.Run.FrozenAt.IsZero() {
		return fmt.Errorf("%w: run identity, version and frozenAt are required", ErrInvalidPublication)
	}
	if !p.Run.Context.ValidTimeline() {
		return fmt.Errorf("%w: run timeline is invalid", ErrInvalidPublication)
	}
	for i, candidate := range p.Candidates {
		if candidate.RunID != runID || strings.TrimSpace(candidate.CandidateID) == "" || candidate.FrozenAt.IsZero() {
			return fmt.Errorf("%w: candidate %d does not belong to the frozen run", ErrInvalidPublication, i)
		}
	}
	for i, rule := range p.Rules {
		if rule.RunID != runID || strings.TrimSpace(rule.RuleID) == "" || rule.Plan.Symbol == "" || rule.FrozenAt.IsZero() {
			return fmt.Errorf("%w: rule %d does not belong to the frozen run", ErrInvalidPublication, i)
		}
	}
	for i, event := range p.InitialOrderEvents {
		if event.RunID != runID || strings.TrimSpace(event.EventID) == "" || event.EventAt.IsZero() || event.FrozenAt.IsZero() || !json.Valid(event.Payload) {
			return fmt.Errorf("%w: initial event %d is incomplete", ErrInvalidPublication, i)
		}
	}
	for i, projection := range p.DisplayProjections {
		if projection.RunID != runID || strings.TrimSpace(projection.ProjectionID) == "" || !json.Valid(projection.Payload) {
			return fmt.Errorf("%w: display projection %d is incomplete", ErrInvalidPublication, i)
		}
	}
	return nil
}
