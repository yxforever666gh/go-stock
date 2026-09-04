package research2app

import (
	"context"
	"errors"

	"go-stock/backend/ai"
	"go-stock/backend/knowledge"
	"go-stock/backend/research2"
	"go-stock/backend/researchaudit"
	"go-stock/internal/recommendationchart"

	"gorm.io/gorm"
)

// Runtime is the application-level Research 2 service graph.
type Runtime struct {
	Repository *research2.Repository
	Valuation  *research2.Service
	Runner     *research2.Runner
	Trading    *research2.TradingService
	Email      *research2.EmailService
}

// Dependencies contains only the infrastructure capabilities needed to build
// the Research 2 application. Concrete HTTP and storage adapters live outside
// this package.
type Dependencies struct {
	Quotes          research2.CurrentQuoteProvider
	Chart           recommendationchart.Provider
	ChartCalendar   recommendationchart.Calendar
	AI              ai.AIClient
	Evidence        EvidenceProvider
	EvidenceStore   EvidenceRepository
	EvidenceBuild   EvidenceItemBuilder
	EvidenceProfile string
	Calendar        research2.Calendar
	Market          research2.MarketProvider
	Audit           *researchaudit.Recorder
	Knowledge       knowledge.ResearchRetriever
	Mailer          research2.Mailer
}

func NewRuntime(mainDB *gorm.DB, dependencies Dependencies) (*Runtime, error) {
	if mainDB == nil {
		return nil, errors.New("database is not initialized")
	}
	repository := research2.NewRepository(mainDB)
	if err := repository.EnsureAccount(context.Background()); err != nil {
		return nil, err
	}
	valuation := research2.NewService(repository, dependencies.Quotes)
	if dependencies.Chart != nil && dependencies.ChartCalendar != nil {
		valuation.SetRecommendationChartProvider(dependencies.Chart, dependencies.ChartCalendar)
	}
	evidence := NewDurableEvidenceCollector(dependencies.Evidence, dependencies.EvidenceStore, dependencies.EvidenceProfile, dependencies.EvidenceBuild)
	runner := research2.NewRunner(repository, dependencies.AI, evidence, dependencies.Calendar)
	if dependencies.Audit != nil {
		runner.ConfigureAudit(dependencies.Audit)
	}
	if dependencies.Knowledge != nil {
		runner.ConfigureKnowledge(dependencies.Knowledge)
	}
	return &Runtime{
		Repository: repository,
		Valuation:  valuation,
		Runner:     runner,
		Trading:    research2.NewTradingService(repository, dependencies.Market, dependencies.Calendar),
		Email:      research2.NewEmailService(repository, dependencies.Mailer),
	}, nil
}
