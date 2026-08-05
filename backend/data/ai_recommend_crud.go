package data

import (
	"fmt"
	"go-stock/backend/db"
	"go-stock/backend/models"
	"strings"
)

func (s *AiRecommendStocksService) GetAiRecommendStocksByID(id uint) (*models.AiRecommendStocks, error) {
	var recommend models.AiRecommendStocks
	err := db.Dao.First(&recommend, id).Error
	if err != nil {
		return nil, err
	}
	return &recommend, nil
}

// UpdateAiRecommendStocks 更新AI推荐股票记录
func (s *AiRecommendStocksService) UpdateAiRecommendStocks(id uint, recommend *models.AiRecommendStocks) error {
	if err := requireStrategyProductionLive(nil, db.Dao); err != nil {
		return err
	}
	if err := validateRecommendationUpdate(id, recommend); err != nil {
		return err
	}
	existingCodes := loadRecommendScopeCodesByIDs([]uint{id})
	result := db.Dao.Model(&models.AiRecommendStocks{}).Where("id = ?", id).Updates(recommend)
	if result.Error == nil {
		scopeCodes := append([]string{}, existingCodes...)
		if recommend != nil {
			code := strings.TrimSpace(recommend.StockCode)
			if code != "" {
				scopeCodes = append(scopeCodes, code)
			}
		}
		_ = markAiRecommendYieldDirtyCodes(scopeCodes, "推荐记录更新后等待严格模式回算", aiRecommendYieldModeStrict)
		requestAiRecommendYieldRecalcWithScope(false, "recommend_updated", scopeCodes)
	}
	return result.Error
}

func validateRecommendationUpdate(id uint, update *models.AiRecommendStocks) error {
	if id == 0 {
		return nil
	}
	var current models.AiRecommendStocks
	if err := db.Dao.Model(&models.AiRecommendStocks{}).
		Select("id", "summary_version", "execution_state", "recommend_status").
		First(&current, id).Error; err != nil {
		return err
	}
	if isFrozenLegacyStrategyRecord(&current) {
		return fmt.Errorf("strategy cohort %s is frozen; recommendation %d is read-only", strings.TrimSpace(current.SummaryVersion), current.ID)
	}
	if update == nil {
		return nil
	}
	if version := strings.TrimSpace(update.SummaryVersion); version != "" && version != strings.TrimSpace(current.SummaryVersion) {
		return fmt.Errorf("summary_version is immutable: %s -> %s", strings.TrimSpace(current.SummaryVersion), version)
	}
	if isAnalysisOnlyRecommend(&current) {
		// GORM's struct Updates keeps zero-value fields unchanged. Mirror that
		// behavior here and validate the effective post-update state, including
		// records represented as analysis_only solely by missing_market_data.
		effective := current
		if strings.TrimSpace(update.ExecutionState) != "" {
			effective.ExecutionState = update.ExecutionState
			if normalizeRecommendExecutionState(update.ExecutionState) != recommendExecutionAnalysisOnly {
				return fmt.Errorf("%s: recommendation %d", marketSummaryAnalysisOnlyIrreversibleReason, current.ID)
			}
		}
		if strings.TrimSpace(update.RecommendStatus) != "" {
			effective.RecommendStatus = update.RecommendStatus
		}
		if !isAnalysisOnlyRecommend(&effective) {
			return fmt.Errorf("%s: recommendation %d", marketSummaryAnalysisOnlyIrreversibleReason, current.ID)
		}
	}
	return nil
}

// DeleteAiRecommendStocks 根据ID删除AI推荐股票记录
func (s *AiRecommendStocksService) DeleteAiRecommendStocks(id uint) error {
	if err := requireStrategyProductionLive(nil, db.Dao); err != nil {
		return err
	}
	if err := rejectFrozenLegacyRecommendationMutation([]uint{id}); err != nil {
		return err
	}
	scopeCodes := loadRecommendScopeCodesByIDs([]uint{id})
	// 使用软删除
	result := db.Dao.Where("id = ?", id).Delete(&models.AiRecommendStocks{})
	if result.Error == nil {
		if id != 0 {
			_ = db.Dao.Where("recommend_id = ?", id).Delete(&models.AiRecommendYieldRecordState{}).Error
		}
		_ = markAiRecommendYieldDirtyCodes(scopeCodes, "推荐记录删除后等待严格模式回算", aiRecommendYieldModeStrict)
		requestAiRecommendYieldRecalcWithScope(false, "recommend_deleted", scopeCodes)
	}
	return result.Error
}

// BatchDeleteAiRecommendStocks 批量删除AI推荐股票记录
func (s *AiRecommendStocksService) BatchDeleteAiRecommendStocks(ids []uint) error {
	if err := requireStrategyProductionLive(nil, db.Dao); err != nil {
		return err
	}
	if err := rejectFrozenLegacyRecommendationMutation(ids); err != nil {
		return err
	}
	scopeCodes := loadRecommendScopeCodesByIDs(ids)
	// 使用软删除
	result := db.Dao.Where("id IN ?", ids).Delete(&models.AiRecommendStocks{})
	if result.Error == nil {
		if len(ids) > 0 {
			_ = db.Dao.Where("recommend_id IN ?", ids).Delete(&models.AiRecommendYieldRecordState{}).Error
		}
		_ = markAiRecommendYieldDirtyCodes(scopeCodes, "批量删除推荐后等待严格模式回算", aiRecommendYieldModeStrict)
		requestAiRecommendYieldRecalcWithScope(false, "recommend_batch_deleted", scopeCodes)
	}
	return result.Error
}

func rejectFrozenLegacyRecommendationMutation(ids []uint) error {
	if len(ids) == 0 {
		return nil
	}
	rows := make([]models.AiRecommendStocks, 0, len(ids))
	if err := db.Dao.Model(&models.AiRecommendStocks{}).
		Select("id", "summary_version").
		Where("id IN ?", ids).
		Find(&rows).Error; err != nil {
		return err
	}
	for idx := range rows {
		if isFrozenLegacyStrategyRecord(&rows[idx]) {
			return fmt.Errorf("strategy cohort %s is frozen; recommendation %d is read-only", strings.TrimSpace(rows[idx].SummaryVersion), rows[idx].ID)
		}
	}
	return nil
}

func loadRecommendScopeCodesByIDs(ids []uint) []string {
	if len(ids) == 0 {
		return []string{}
	}
	rows := make([]models.AiRecommendStocks, 0, len(ids))
	if err := db.Dao.Model(&models.AiRecommendStocks{}).Select("stock_code").Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return []string{}
	}
	result := make([]string, 0, len(rows))
	seen := map[string]struct{}{}
	for _, row := range rows {
		code := normalizeRecommendStockCode(row.StockCode)
		if code == "" {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		result = append(result, code)
	}
	return result
}
