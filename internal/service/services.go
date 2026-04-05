package service

type AppServices struct {
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
	return AppServices{
		Stock:     NewStockService(),
		Market:    NewMarketService(),
		AI:        NewAIService(),
		Config:    NewConfigService(),
		Group:     NewGroupService(),
		Fund:      NewFundService(),
		History:   NewHistoryService(),
		Recommend: NewRecommendService(),
		Notify:    NewNotifyService(),
	}
}
