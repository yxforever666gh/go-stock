package tools

import (
	"errors"

	"go-stock/backend/models"
)

// ErrToolDataProviderRequired is returned when an agent tool is constructed
// without the data port it needs. Keeping this failure explicit prevents a
// tool from silently reaching into the legacy data package.
var ErrToolDataProviderRequired = errors.New("agent tool data provider is required")

// StockCodeProvider is the consumer-owned port used by the stock code tool.
type StockCodeProvider interface {
	SearchStocks(searchWord string) []models.StockBasic
}

// BKDictProvider is the consumer-owned port used by the board dictionary
// tool. The legacy API always queries the 016 board dictionary, so that
// implementation detail is intentionally hidden behind the port.
type BKDictProvider interface {
	BoardDictionary() []any
}

// MarketNewsProvider is the consumer-owned port used by the market news tool.
// The return shapes use stable model contracts rather than legacy API types.
type MarketNewsProvider interface {
	MarketCalendar() []any
	MarketNews(source string, limit int) []*models.Telegraph
	TradingViewNews() []models.Telegraph
	ReutersNews() *models.ReutersNews
}

// ToolDataProvider is the aggregate port needed to assemble the stock agent.
// Individual tools accept their narrower embedded port so each dependency is
// explicit and remains easy to fake in isolation.
type ToolDataProvider interface {
	StockCodeProvider
	BKDictProvider
	MarketNewsProvider
}
