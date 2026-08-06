package service

type AppServices struct {
	Runtime   RuntimeService
	Stock     StockService
	Market    MarketService
	AI        AIService
	Config    ConfigService
	Group     GroupService
	Fund      FundService
	History   HistoryService
	Recommend RecommendService
	Scheduler SchedulerService
	Notify    NotifyService
	Execution ExecutionService
	System    SystemService
}

func NewAppServicesWithDependencies(dependencies Dependencies) (AppServices, error) {
	runtimeService, err := newRuntimeService(dependencies)
	if err != nil {
		return AppServices{}, err
	}
	return AppServices{
		Runtime:   runtimeService,
		Stock:     NewStockService(dependencies.Operations.Stock),
		Market:    NewMarketService(dependencies.Operations.Market),
		AI:        NewAIService(dependencies.Operations.AI),
		Config:    NewConfigService(dependencies.Operations.Config),
		Group:     NewGroupService(dependencies.Operations.Group),
		Fund:      NewFundService(dependencies.Operations.Fund),
		History:   NewHistoryService(dependencies.Operations.History),
		Recommend: NewRecommendService(dependencies.Operations.Recommend),
		Scheduler: NewSchedulerService(dependencies.Operations.Scheduler),
		Notify:    NewNotifyService(dependencies.Operations.Notify),
		Execution: NewExecutionService(dependencies.ExecutionMonitor, dependencies.Operations.Scheduler, dependencies.Operations.System),
		System:    NewSystemService(dependencies.Operations.System),
	}, nil
}
