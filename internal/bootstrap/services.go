package bootstrap

import (
	"go-stock/backend/data"
	"go-stock/backend/funds"
	"go-stock/backend/groups"
	"go-stock/backend/marketapp"
	"go-stock/backend/models"
	"go-stock/backend/stocks"
	"go-stock/internal/aiapp"
	"go-stock/internal/service"
	"go-stock/internal/settingsapp"

	"gorm.io/gorm"
)

var _ service.FundService = (*funds.Service)(nil)
var _ service.GroupService = (*groups.Service)(nil)
var _ service.MarketService = (*marketapp.Service)(nil)
var _ service.StockService = (*stocks.Service)(nil)

// newProductionServiceDependencies wires concrete domain services once for the app runtime.
func newProductionServiceDependencies(main *gorm.DB, seed ...func() ([]models.StockBasic, models.StockMasterRefreshResult, error)) service.Dependencies {
	fund := funds.NewApplicationService(main)
	group := groups.NewService(main)
	marketAPI := data.NewMarketNewsApi()
	market := marketapp.NewService(marketapp.Dependencies{
		Database:  main,
		News:      marketAPI,
		Snapshots: marketAPI,
		AnalyzeNews: func(text string, save bool) {
			data.NewsAnalyze(text, save)
		},
		StartSelfCheck:  data.EnsureDiemengSelfCheckAsync,
		IsOpenDay:       data.IsCNOpenTradeDay,
		IsOpenDayStrict: data.IsCNOpenTradeDayStrict,
	})
	stockAPI := data.NewStockDataApi()
	var stockMasterSeed func() ([]models.StockBasic, models.StockMasterRefreshResult, error)
	if len(seed) > 0 {
		stockMasterSeed = seed[0]
	}
	stock := stocks.NewService(stocks.Dependencies{
		Database:        main,
		Master:          stockAPI,
		StockMasterSeed: stockMasterSeed,
		FetchIndex:      stockAPI.FetchIndexBasic,
		ListGroupStocks: group.GetGroupStockList,
		StockKLine: func(code string, days int64) *[]models.KLineData {
			return stockAPI.GetHK_KLineData(code, "day", days)
		},
		StockMinutePriceLine: func(code, name string) map[string]any {
			priceData, date := stockAPI.GetStockMinutePriceData(code)
			return map[string]any{"priceData": priceData, "date": date, "stockName": name, "stockCode": code}
		},
		Search: func(words string) map[string]any {
			return data.NewSearchStockApi(words).SearchStock(5000)
		},
		SearchWithFingerprint: func(words, fingerprint string, pageSize int) map[string]any {
			return data.NewSearchStockApiWithFingerprint(words, fingerprint).SearchStock(pageSize)
		},
		Realtime: stockAPI.GetStockCodeRealTimeDataReadOnly,
	})
	return service.Dependencies{
		Clock:       systemClock{},
		Initializer: legacyApplicationInitializer{},
		AI:          aiapp.NewService(),
		Config:      settingsapp.NewService(data.NewSettingsProvider()),
		Fund:        fund,
		Group:       group,
		Market:      market,
		Stock:       stock,
	}
}

// productionRuntimeDependencies assembles the application services from
// explicit storage supplied by the composition root.
func productionRuntimeDependencies(storage Storage, seed ...StockMasterSeedLoader) RuntimeDependencies {
	var seedLoaders []func() ([]models.StockBasic, models.StockMasterRefreshResult, error)
	if len(seed) > 0 && seed[0] != nil {
		seedLoaders = append(seedLoaders, seed[0])
	}
	return RuntimeDependencies{
		Storage:  storage,
		Services: newProductionServiceDependencies(storage.Main, seedLoaders...),
	}
}
