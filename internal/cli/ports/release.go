package ports

import (
	"context"
	"time"
)

// ReleaseReplayInspectionRequest identifies a pair of already hash-verified
// SQLite snapshots. The repository must open both files read-only and must not
// fetch or repair missing replay data.
type ReleaseReplayInspectionRequest struct {
	MainDatabasePath   string
	MinuteDatabasePath string
	RecommendationTo   time.Time
	ExpectedRuleCount  int
}

// ReleaseReplayInspection is the storage-backed evidence used by the release
// command after file identity has been verified.
type ReleaseReplayInspection struct {
	LegacyRuleRows      int64
	LegacyMinuteBarRows int64
	MinuteBarRows       int64
	MinuteAsOf          time.Time
	ReplayRuleCount     int
	ResultHash          string
	RepeatedResultHash  string
	Deterministic       bool
	DeterminismFailures int
}

// ReleaseInspectionRepository owns the read-only database pair used by local
// release preflight and deployment verification. CLI code never receives or
// swaps process-global database handles.
type ReleaseInspectionRepository interface {
	InspectReplayBundle(context.Context, ReleaseReplayInspectionRequest) (ReleaseReplayInspection, error)
}
