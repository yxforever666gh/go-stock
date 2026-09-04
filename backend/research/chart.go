package research

import (
	"context"
	"errors"
	"strings"

	"go-stock/internal/recommendationchart"
)

func (s *Service) RecommendationChart(ctx context.Context, recommendationID string, refresh bool) (recommendationchart.Chart, error) {
	recommendationID = strings.TrimSpace(recommendationID)
	if recommendationID == "" {
		return recommendationchart.Chart{}, errors.New("recommendationId is required")
	}
	s.chartMu.Lock()
	engine := s.chartEngine
	s.chartMu.Unlock()
	if engine == nil {
		return recommendationchart.Chart{}, recommendationchart.ErrProviderUnavailable
	}
	detail, err := s.repository.Detail(ctx, recommendationID)
	if err != nil {
		return recommendationchart.Chart{}, err
	}
	return engine.Chart(ctx, recommendationChartDetail(detail), refresh)
}

func recommendationChartDetail(detail RecommendationDetail) recommendationchart.Detail {
	result := recommendationchart.Detail{
		RecommendationID: detail.Recommendation.RecommendationID,
		StockCode:        detail.Recommendation.StockCode,
		StockName:        detail.Recommendation.StockName,
		SignalAt:         detail.Recommendation.SignalAt,
		Trades:           make([]recommendationchart.Trade, 0, len(detail.Trades)),
	}
	for _, trade := range detail.Trades {
		result.Trades = append(result.Trades, recommendationchart.Trade{
			Side: trade.Side, TradedAt: trade.TradedAt, MarketPrice: trade.MarketPrice,
			ExecutionPrice: trade.ExecutionPrice, Quantity: trade.Quantity,
			TotalFees: trade.TotalFees, NetCashFlow: trade.NetCashFlow,
		})
	}
	if detail.Position != nil {
		result.Position = &recommendationchart.Position{
			CurrentPrice: detail.Position.CurrentPrice, CurrentPriceAt: detail.Position.CurrentPriceAt,
			Status: detail.Position.Status, ExitAt: detail.Position.ExitAt,
		}
	}
	return result
}
