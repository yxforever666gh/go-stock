package service

type AppServices struct {
	Runtime RuntimeService
	Stock   StockService
	Market  MarketService
	AI      AIService
	Config  ConfigService
	Group   GroupService
	Fund    FundService
	Notify  NotifyService
}

func NewAppServicesWithDependencies(dependencies Dependencies) (AppServices, error) {
	runtimeService, err := newRuntimeService(dependencies)
	if err != nil {
		return AppServices{}, err
	}
	return AppServices{
		Runtime: runtimeService,
		Stock:   NewStockService(dependencies.Operations.Stock),
		Market:  NewMarketService(dependencies.Operations.Market),
		AI:      NewAIService(dependencies.Operations.AI),
		Config:  NewConfigService(dependencies.Operations.Config),
		Group:   NewGroupService(dependencies.Operations.Group),
		Fund:    NewFundService(dependencies.Operations.Fund),
		Notify:  NewNotifyService(dependencies.Operations.Notify),
	}, nil
}
