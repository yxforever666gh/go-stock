package execution

import (
	"context"
	"errors"
	"time"
)

var ErrMonitorUnavailable = errors.New("execution monitor unavailable")

type MonitorWindow struct {
	SlotAt           time.Time
	EvaluationCutoff time.Time
}

type MonitorResult struct {
	ObservedAt       time.Time
	EvaluationCutoff time.Time
	PendingCount     int
	OpenCount        int
	ProcessedCount   int
	SkippedCount     int
	Warnings         []string
}

// Monitor is the application-facing entry to the online execution use case.
// Concrete cache, database and provider dependencies stay behind its adapter.
type Monitor interface {
	ResolveWindow(time.Time) (MonitorWindow, bool)
	Run(context.Context, time.Time) (MonitorResult, error)
	SetWakeup(func())
}
