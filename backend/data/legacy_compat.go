package data

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go-stock/backend/legacy"
	"go-stock/backend/models"

	"gorm.io/gorm"
)

// CompatibilityLegacyRepository exposes historical recommendation rows only.
// The interface intentionally has no write, repair, backfill or execution
// operation.
type CompatibilityLegacyRepository struct {
	database *gorm.DB
}

func NewCompatibilityLegacyRepository(database *gorm.DB) CompatibilityLegacyRepository {
	return CompatibilityLegacyRepository{database: database}
}

func (r CompatibilityLegacyRepository) Find(ctx context.Context, id uint) (legacy.Recommendation, error) {
	if err := ctx.Err(); err != nil {
		return legacy.Recommendation{}, err
	}
	if r.database == nil || !r.database.Migrator().HasTable(&models.AiRecommendStocks{}) {
		return legacy.Recommendation{}, fmt.Errorf("legacy recommendation storage is unavailable")
	}
	var row models.AiRecommendStocks
	if err := r.database.WithContext(ctx).First(&row, id).Error; err != nil {
		return legacy.Recommendation{}, err
	}
	if !legacy.IsFrozenVersion(row.SummaryVersion) {
		return legacy.Recommendation{}, fmt.Errorf("%w: %q", legacy.ErrNotFrozenLegacy, row.SummaryVersion)
	}
	return compatibilityLegacyRecommendation(row)
}

func (r CompatibilityLegacyRepository) List(ctx context.Context, query legacy.Query) ([]legacy.Recommendation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.database == nil || !r.database.Migrator().HasTable(&models.AiRecommendStocks{}) {
		return nil, fmt.Errorf("legacy recommendation storage is unavailable")
	}
	if !query.Start.IsZero() && !query.End.IsZero() && query.End.Before(query.Start) {
		return nil, fmt.Errorf("legacy recommendation end is before start")
	}

	dbq := r.database.WithContext(ctx).Model(&models.AiRecommendStocks{})
	if len(query.Symbols) > 0 {
		codes := make([]string, 0, len(query.Symbols))
		for _, symbol := range query.Symbols {
			if code := normalizeRecommendStockCode(symbol); code != "" {
				codes = append(codes, code)
			}
		}
		if len(codes) == 0 {
			return []legacy.Recommendation{}, nil
		}
		dbq = dbq.Where("stock_code IN ?", codes)
	}
	if !query.Start.IsZero() {
		dbq = dbq.Where("COALESCE(data_time, created_at) >= ?", query.Start)
	}
	if !query.End.IsZero() {
		dbq = dbq.Where("COALESCE(data_time, created_at) <= ?", query.End)
	}
	rows := make([]models.AiRecommendStocks, 0)
	if err := dbq.Order("COALESCE(data_time, created_at) DESC, id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]legacy.Recommendation, 0, len(rows))
	for _, row := range rows {
		if !legacy.IsFrozenVersion(row.SummaryVersion) {
			continue
		}
		mapped, err := compatibilityLegacyRecommendation(row)
		if err != nil {
			return nil, err
		}
		result = append(result, mapped)
		if query.Limit > 0 && len(result) >= query.Limit {
			break
		}
	}
	return result, nil
}

func compatibilityLegacyRecommendation(row models.AiRecommendStocks) (legacy.Recommendation, error) {
	payload, err := json.Marshal(row)
	if err != nil {
		return legacy.Recommendation{}, fmt.Errorf("marshal legacy recommendation %d: %w", row.ID, err)
	}
	tradeDate := row.CreatedAt
	if row.DataTime != nil && !row.DataTime.IsZero() {
		tradeDate = *row.DataTime
	}
	return legacy.Recommendation{
		ID: row.ID, StrategyVersion: strings.TrimSpace(row.SummaryVersion), TradeDate: tradeDate,
		Symbol: normalizeRecommendStockCode(row.StockCode), Name: strings.TrimSpace(row.StockName),
		Status:  firstNonEmptyText(row.RecommendStatus, row.ActivationStatus, row.ExecutionState),
		Payload: payload,
	}, nil
}

var _ legacy.Repository = CompatibilityLegacyRepository{}
