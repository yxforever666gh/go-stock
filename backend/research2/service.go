package research2

import (
	"context"
	"errors"
	"sync"
	"time"

	"go-stock/internal/marketquote"
	"go-stock/internal/recommendationchart"
)

const defaultQuoteRefreshConcurrency = 5

// CurrentQuoteProvider is the small market-data capability needed to value
// research-center-2 positions. It deliberately matches the provider used by
// research center 1 so callers can share the same quote source.
type CurrentQuoteProvider interface {
	CurrentQuote(context.Context, string) (marketquote.Quote, error)
}

// Service adds best-effort live quote refreshes to the durable repository
// views. Quote failures leave the last successfully persisted mark in place.
type Service struct {
	repository     *Repository
	quotes         CurrentQuoteProvider
	now            func() time.Time
	maxConcurrency int
	chartMu        sync.RWMutex
	chartEngine    *recommendationchart.Engine
}

func NewService(repository *Repository, quotes CurrentQuoteProvider) *Service {
	return &Service{repository: repository, quotes: quotes, now: time.Now, maxConcurrency: defaultQuoteRefreshConcurrency}
}

func (s *Service) SetRecommendationChartProvider(provider recommendationchart.Provider, calendar recommendationchart.Calendar) {
	s.chartMu.Lock()
	s.chartEngine = recommendationchart.NewEngine(provider, calendar, func() time.Time { return s.now() })
	s.chartMu.Unlock()
}

func (s *Service) RecommendationChart(ctx context.Context, id string, refresh bool) (recommendationchart.Chart, error) {
	if err := s.ensureRepository(); err != nil {
		return recommendationchart.Chart{}, err
	}
	s.chartMu.RLock()
	engine := s.chartEngine
	s.chartMu.RUnlock()
	if engine == nil {
		return recommendationchart.Chart{}, recommendationchart.ErrProviderUnavailable
	}
	detail, err := s.repository.GetRecommendation(ctx, id)
	if err != nil {
		return recommendationchart.Chart{}, err
	}
	trades := make([]recommendationchart.Trade, 0, len(detail.Trades))
	for _, trade := range detail.Trades {
		trades = append(trades, recommendationchart.Trade{
			Side: trade.Side, TradedAt: trade.TradedAt, MarketPrice: trade.MarketPrice,
			ExecutionPrice: trade.ExecutionPrice, Quantity: trade.Quantity,
			TotalFees:   trade.Commission + trade.StampDuty + trade.TransferFee,
			NetCashFlow: trade.NetCashFlow,
		})
	}
	chartDetail := recommendationchart.Detail{
		RecommendationID: detail.Recommendation.RecommendationID,
		StockCode:        detail.Recommendation.StockCode,
		StockName:        detail.Recommendation.StockName,
		SignalAt:         detail.Recommendation.SignalAt,
		Trades:           trades,
	}
	if detail.Recommendation.Status == "active" || detail.Recommendation.Status == "sell_pending" {
		chartDetail.Position = &recommendationchart.Position{
			CurrentPrice: detail.Recommendation.CurrentPrice, CurrentPriceAt: detail.Recommendation.CurrentPriceAt, Status: "open",
		}
	}
	return engine.Chart(ctx, chartDetail, refresh)
}

func (s *Service) ListRecommendations(ctx context.Context, limit, offset int) ([]Recommendation, error) {
	if err := s.ensureRepository(); err != nil {
		return nil, err
	}
	return s.repository.ListRecommendations(ctx, limit, offset)
}

func (s *Service) GetRecommendation(ctx context.Context, id string) (RecommendationDetail, error) {
	if err := s.ensureRepository(); err != nil {
		return RecommendationDetail{}, err
	}
	detail, err := s.repository.GetRecommendation(ctx, id)
	if err != nil {
		return RecommendationDetail{}, err
	}
	if err = s.refresh(ctx, []Recommendation{detail.Recommendation}); err != nil {
		return RecommendationDetail{}, err
	}
	return s.repository.GetRecommendation(ctx, id)
}

func (s *Service) Overview(ctx context.Context) (AccountOverview, error) {
	if err := s.RefreshCurrentQuotes(ctx); err != nil {
		return AccountOverview{}, err
	}
	return s.repository.Overview(ctx)
}

func (s *Service) Performance(ctx context.Context) (Performance, error) {
	if err := s.RefreshCurrentQuotes(ctx); err != nil {
		return Performance{}, err
	}
	return s.repository.Performance(ctx)
}

func (s *Service) RefreshCurrentQuotes(ctx context.Context) error {
	if err := s.ensureRepository(); err != nil {
		return err
	}
	items, err := s.repository.ActiveRecommendations(ctx)
	if err != nil {
		return err
	}
	return s.refresh(ctx, items)
}

type quoteRefresh struct {
	recommendationID string
	price            float64
	at               time.Time
}

func (s *Service) refresh(ctx context.Context, items []Recommendation) error {
	if s.quotes == nil || len(items) == 0 {
		return nil
	}
	active := make([]Recommendation, 0, len(items))
	for _, item := range items {
		if item.Status == "active" || item.Status == "sell_pending" {
			active = append(active, item)
		}
	}
	if len(active) == 0 {
		return nil
	}
	workers := s.maxConcurrency
	if workers <= 0 {
		workers = defaultQuoteRefreshConcurrency
	}
	semaphore := make(chan struct{}, workers)
	results := make(chan quoteRefresh, len(active))
	var wait sync.WaitGroup
	for _, item := range active {
		item := item
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				return
			}
			quote, err := s.quotes.CurrentQuote(ctx, item.StockCode)
			if err != nil || quote.Price <= 0 {
				return
			}
			at := quote.At
			if at.IsZero() {
				at = s.now()
			}
			results <- quoteRefresh{recommendationID: item.RecommendationID, price: quote.Price, at: at}
		}()
	}
	wait.Wait()
	close(results)

	var persistErrors []error
	for result := range results {
		if err := s.repository.UpdateCurrentQuote(ctx, result.recommendationID, result.price, result.at); err != nil {
			persistErrors = append(persistErrors, err)
		}
	}
	return errors.Join(persistErrors...)
}

func (s *Service) ensureRepository() error {
	if s == nil || s.repository == nil {
		return errors.New("research2 valuation service is unavailable")
	}
	return nil
}
