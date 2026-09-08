package data

import (
	"errors"

	"go-stock/backend/ai"
	"go-stock/backend/marketdata"
	"go-stock/backend/models"
	"go-stock/backend/research"
	"go-stock/backend/researchapp"
	"go-stock/backend/researchaudit"

	"gorm.io/gorm"
)

// NewResearchDependencies adapts the legacy provider implementations to the
// focused Research 1 application boundary.
func NewResearchDependencies(configID int, mainDB, minuteDB *gorm.DB, setting *models.SettingConfig) (researchapp.Dependencies, researchapp.Options, error) {
	if mainDB == nil {
		return researchapp.Dependencies{}, researchapp.Options{}, errors.New("database is not initialized")
	}
	if setting == nil || setting.Settings == nil {
		return researchapp.Dependencies{}, researchapp.Options{}, errors.New("research settings snapshot is required")
	}
	setting = cloneProviderSettings(setting)
	stocks := NewStockDataApiWithSettings(setting)
	news := NewMarketNewsApiWithSettings(setting, mainDB)
	quotes := NewResearchQuoteProviderWithStockData(stocks)
	calendar := NewResearchTradingCalendar(setting)
	baseSources := NewResearchSourceCollectorWithProviders(news, stocks)
	dependencies := researchapp.Dependencies{
		AI: ai.NewResearchClient(configID, ResearchAIClientOptionsForSettings(setting)), Quotes: quotes, Calendar: calendar,
		Lifecycle: NewResearchLifecycleContextCollectorWithProviders(quotes, stocks, news),
		Chart:     NewResearchChartProviderWithStorage(quotes, minuteDB), Sources: baseSources,
		Audit: researchaudit.NewRecorder(researchaudit.NewRepository(mainDB)),
	}
	options := researchapp.Options{}
	schedule, err := research.NewSellReviewSchedule(setting.AIReviewStartTime, setting.AIReviewIntervalMinutes)
	if err != nil {
		return researchapp.Dependencies{}, researchapp.Options{}, err
	}
	target, maxImmediate, _, err := NormalizeAICapitalDeploymentSettings(
		setting.AITargetCapitalUtilization,
		setting.AIMaxImmediateBuysPerRun,
		setting.AIReanalysisIntervalMinutes,
	)
	if err != nil {
		return researchapp.Dependencies{}, researchapp.Options{}, err
	}
	options.SellReviewSchedule = &schedule
	options.CapitalDeployment = &researchapp.CapitalDeploymentPolicy{TargetUtilization: target, MaxImmediateBuys: maxImmediate}
	options.ExperimentalEvidence = setting.ExperimentalEvidenceEnabled
	if options.ExperimentalEvidence {
		dependencies.ExperimentalSources = NewExperimentalResearchSourceCollector(baseSources, NewMarketEvidenceServiceWithSettings(mainDB, minuteDB, setting), newThemeEvidenceReader(mainDB))
		dependencies.Evidence = marketdata.NewRepository(mainDB)
		dependencies.EvidenceProfile = researchThemeEvidenceProfile
		dependencies.Knowledge = NewKnowledgeService(mainDB)
	}
	return dependencies, options, nil
}
