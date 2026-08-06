package bootstrap

import (
	"context"
	"errors"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
	cliports "go-stock/internal/cli/ports"
	"go-stock/internal/service"

	"gorm.io/gorm"
)

type marketSummaryRecommendationBackfillAdapter struct {
	main     *gorm.DB
	services service.AppServices
}

var _ cliports.MarketSummaryRecommendationBackfill = (*marketSummaryRecommendationBackfillAdapter)(nil)

func NewProductionMarketSummaryRecommendationBackfill() (cliports.MarketSummaryRecommendationBackfill, error) {
	if db.Dao == nil {
		return nil, errors.New("main database is not initialized")
	}
	services, err := NewProductionServices()
	if err != nil {
		return nil, err
	}
	return &marketSummaryRecommendationBackfillAdapter{main: db.Dao, services: services}, nil
}

func (a *marketSummaryRecommendationBackfillAdapter) ListReports(ctx context.Context, start, end time.Time) ([]cliports.MarketSummaryReport, error) {
	reports := make([]models.AIResponseResult, 0, 8)
	if err := a.main.WithContext(ctx).Model(&models.AIResponseResult{}).
		Where("created_at >= ? AND created_at < ?", start, end).
		Where("(stock_code = ? OR stock_name = ?)", "\u5e02\u573a\u8d44\u8baf", "\u5e02\u573a\u8d44\u8baf").
		Order("created_at ASC").
		Find(&reports).Error; err != nil {
		return nil, err
	}
	result := make([]cliports.MarketSummaryReport, 0, len(reports))
	for _, report := range reports {
		result = append(result, cliports.MarketSummaryReport{
			ID:           report.ID,
			CreatedAt:    report.CreatedAt,
			ProviderName: report.ProviderName,
			ModelName:    report.ModelName,
			Content:      report.Content,
		})
	}
	return result, nil
}

func (a *marketSummaryRecommendationBackfillAdapter) SaveRecommendations(_ context.Context, report cliports.MarketSummaryReport) (int, error) {
	return a.services.AI.EnsureMarketSummaryRecommendStocksSaved(
		report.Content,
		report.ProviderName,
		report.ModelName,
		report.CreatedAt,
	)
}
