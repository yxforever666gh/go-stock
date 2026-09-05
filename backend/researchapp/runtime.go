package researchapp

import (
	"context"
	"errors"

	"go-stock/backend/ai"
	"go-stock/backend/knowledge"
	"go-stock/backend/research"
	"go-stock/backend/researchaudit"
	"go-stock/internal/recommendationchart"
	"go-stock/internal/researchevidence"

	"gorm.io/gorm"
)

type Runtime struct {
	Repository *research.Repository
	Service    *research.Service
	Runner     *research.AnalysisRunner
	Audit      *researchaudit.Recorder
}

// Dependencies contains provider capabilities, not concrete data-layer
// implementations. The application chooses the active evidence path.
type Dependencies struct {
	AI                  ai.AIClient
	Quotes              research.QuoteProvider
	Calendar            research.TradingCalendar
	Lifecycle           research.LifecycleContextProvider
	Chart               recommendationchart.Provider
	Sources             researchevidence.SourceCollector
	ExperimentalSources researchevidence.SourceCollector
	Audit               *researchaudit.Recorder
	Evidence            research.EvidenceRepository
	EvidenceProfile     string
	Knowledge           knowledge.ResearchRetriever
}

type CapitalDeploymentPolicy struct {
	TargetUtilization float64
	MaxImmediateBuys  int
}

type Options struct {
	SellReviewSchedule   *research.SellReviewSchedule
	CapitalDeployment    *CapitalDeploymentPolicy
	ExperimentalEvidence bool
}

func NewRuntime(mainDB *gorm.DB, dependencies Dependencies, options Options) (*Runtime, error) {
	if mainDB == nil {
		return nil, errors.New("database is not initialized")
	}
	repository := research.NewRepository(mainDB)
	if err := repository.EnsureAccount(context.Background()); err != nil {
		return nil, err
	}
	service := research.NewService(repository, dependencies.AI, dependencies.Quotes, dependencies.Calendar, dependencies.Lifecycle)
	if options.SellReviewSchedule != nil {
		service.SetSellReviewSchedule(*options.SellReviewSchedule)
	}
	if options.CapitalDeployment != nil {
		service.SetCapitalDeploymentPolicy(options.CapitalDeployment.TargetUtilization, options.CapitalDeployment.MaxImmediateBuys)
	}
	if dependencies.Chart != nil {
		service.SetRecommendationChartProvider(dependencies.Chart)
	}
	collector := activeSources(dependencies, options.ExperimentalEvidence)
	runner := research.NewAnalysisRunner(service, collector)
	if dependencies.Audit != nil {
		runner.ConfigureAudit(dependencies.Audit)
	}
	if options.ExperimentalEvidence {
		if dependencies.Evidence != nil && dependencies.EvidenceProfile != "" {
			runner.ConfigureEvidence(dependencies.Evidence, dependencies.EvidenceProfile)
		}
		if dependencies.Knowledge != nil {
			runner.ConfigureKnowledge(dependencies.Knowledge)
		}
	}
	return &Runtime{Repository: repository, Service: service, Runner: runner, Audit: dependencies.Audit}, nil
}

func (runtime *Runtime) ReconcileInterruptedAudits(ctx context.Context) (int, error) {
	if runtime == nil || runtime.Repository == nil || runtime.Audit == nil {
		return 0, nil
	}
	type interruptedRun struct {
		RunID         string
		FailureReason string
	}
	var runs []interruptedRun
	err := runtime.Repository.DB().WithContext(ctx).
		Table("research_v160_analysis_runs AS runs").
		Select("runs.run_id, runs.failure_reason").
		Joins("JOIN research_audit_run_states AS audit ON audit.owner_type = ? AND audit.owner_id = runs.run_id", researchaudit.OwnerResearch1).
		Where("runs.status = ? AND audit.status = ?", "failed", researchaudit.StatusCapturing).
		Find(&runs).Error
	if err != nil {
		return 0, err
	}
	for _, run := range runs {
		reason := run.FailureReason
		if reason == "" {
			reason = "analysis interrupted before audit completion"
		}
		if err := runtime.Audit.Fail(ctx, researchaudit.OwnerResearch1, run.RunID, errors.New(reason)); err != nil {
			return 0, err
		}
	}
	return len(runs), nil
}

func activeSources(dependencies Dependencies, experimental bool) researchevidence.SourceCollector {
	if experimental && dependencies.ExperimentalSources != nil {
		return dependencies.ExperimentalSources
	}
	return dependencies.Sources
}
