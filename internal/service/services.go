package service

type AppServices struct {
	Runtime RuntimeService
	Stock   StockService
	Market  MarketService
	AI      AIService
	Config  ConfigService
	Group   GroupService
	Fund    FundService
}

func NewAppServicesWithDependencies(dependencies Dependencies) (AppServices, error) {
	runtimeService, err := newRuntimeService(dependencies)
	if err != nil {
		return AppServices{}, err
	}
	return AppServices{
		Runtime: runtimeService,
		Stock:   dependencies.Stock,
		Market:  dependencies.Market,
		AI:      dependencies.AI,
		Config:  dependencies.Config,
		Group:   dependencies.Group,
		Fund:    dependencies.Fund,
	}, nil
}
