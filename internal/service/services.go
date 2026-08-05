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
	Notify    NotifyService
}

func NewAppServices() AppServices {
	services, _ := NewAppServicesWithDependencies(Dependencies{
		Clock:       wallClock{},
		Initializer: noOpApplicationInitializer{},
	})
	return services
}

func NewAppServicesWithDependencies(dependencies Dependencies) (AppServices, error) {
	runtimeService, err := newRuntimeService(dependencies)
	if err != nil {
		return AppServices{}, err
	}
	return AppServices{
		Runtime:   runtimeService,
		Stock:     NewStockService(),
		Market:    NewMarketService(),
		AI:        NewAIService(),
		Config:    NewConfigService(),
		Group:     NewGroupService(),
		Fund:      NewFundService(),
		History:   NewHistoryService(),
		Recommend: NewRecommendService(),
		Notify:    NewNotifyService(),
	}, nil
}
