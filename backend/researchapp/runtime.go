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
	return &Runtime{Repository: repository, Service: service, Runner: runner}, nil
}

func activeSources(dependencies Dependencies, experimental bool) researchevidence.SourceCollector {
	if experimental && dependencies.ExperimentalSources != nil {
		return dependencies.ExperimentalSources
	}
	return dependencies.Sources
}
