package data

import (
	"strings"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
)

const legacySkipRepairReason = "legacy_skip_repair"
const sameDayOnlyLegacySkipReason = "sameDayOnly 生效，旧信号仅允许在信号当日激活"
const sameDayOnlyLegacySkipRepairReason = "same_day_only_legacy_skip_repair"

type legacySkipRepairStats struct {
	Scanned           int
	StillSkipped      int
	OverrideKept      int
	RecordStatesReset int
	AggregateReset    int
	RecalcQueuedCodes int
}

func RepairHistoricalLegacySkippedRecommendations(now time.Time) (legacySkipRepairStats, error) {
	stats := legacySkipRepairStats{}
	cutoff := legacyDirectActivationCutoffStart()

	var rows []models.AiRecommendStocks
	if err := db.Dao.Model(&models.AiRecommendStocks{}).
		Order("COALESCE(data_time, created_at) ASC, id ASC").
		Find(&rows).Error; err != nil {
		return stats, err
	}

	recordStateMap, err := loadExistingYieldRecordStateMap()
	if err != nil {
		return stats, err
	}
	aggregateStateMap, err := loadExistingYieldStateMap()
	if err != nil {
		return stats, err
	}

	recordIDs := make([]uint, 0, len(rows))
	for _, row := range rows {
		if row.ID == 0 {
			continue
		}
		recordIDs = append(recordIDs, row.ID)
	}
	overrideMap, err := loadYieldOverrideMapByRecommendIDs(recordIDs)
	if err != nil {
		return stats, err
	}

	codesToRecalc := map[string]struct{}{}
	aggregateResetCodes := map[string]struct{}{}
	for _, row := range rows {
		recordTime := recommendRecordTime(row)
		if recordTime.IsZero() || !recordTime.Before(cutoff) {
			continue
		}
		stats.Scanned++

		code := normalizeRecommendStockCode(row.StockCode)
		recordState := recordStateMap[row.ID]
		aggregateState := aggregateStateMap[code]
		recordSkipped := recordState != nil && strings.EqualFold(strings.TrimSpace(recordState.ActivationStatus), "skipped")
		aggregateSkipped := aggregateState != nil && strings.EqualFold(strings.TrimSpace(aggregateState.ActivationStatus), "skipped")
		if !recordSkipped && !aggregateSkipped {
			continue
		}

		if override, ok := overrideMap[row.ID]; ok && normalizeYieldOverrideActivationStatus(override.ActivationStatusOverride) == "skipped" {
			stats.OverrideKept++
			continue
		}

		_, _, _, _, skip := resolveRecommendYieldSkipInfo(&row)
		if skip {
			stats.StillSkipped++
			continue
		}

		if recordSkipped {
			if err := resetHistoricalSkippedYieldRecordState(row.ID, now); err != nil {
				return stats, err
			}
			stats.RecordStatesReset++
		}
		if aggregateSkipped {
			if _, ok := aggregateResetCodes[code]; !ok {
				if err := resetHistoricalSkippedYieldAggregateState(code, now); err != nil {
					return stats, err
				}
				aggregateResetCodes[code] = struct{}{}
				stats.AggregateReset++
			}
		}
		if code != "" {
			codesToRecalc[code] = struct{}{}
		}
	}

	if len(codesToRecalc) == 0 {
		return stats, nil
	}
	scopeCodes := make([]string, 0, len(codesToRecalc))
	for code := range codesToRecalc {
		scopeCodes = append(scopeCodes, code)
	}
	if err := markAiRecommendYieldDirtyCodes(scopeCodes, "历史跳过修复后等待重算", aiRecommendYieldModeStrict); err != nil {
		return stats, err
	}
	requestAiRecommendYieldRecalcWithScope(true, legacySkipRepairReason, scopeCodes)
	stats.RecalcQueuedCodes = len(scopeCodes)
	return stats, nil
}

func RepairSameDayOnlyLegacySkippedRecommendations(now time.Time) (legacySkipRepairStats, error) {
	stats := legacySkipRepairStats{}
	var states []models.AiRecommendYieldRecordState
	if err := db.Dao.
		Where("activation_status = ? AND data_status_reason = ?", "skipped", sameDayOnlyLegacySkipReason).
		Find(&states).Error; err != nil {
		return stats, err
	}
	stats.Scanned = len(states)
	if len(states) == 0 {
		return stats, nil
	}

	recommendIDs := make([]uint, 0, len(states))
	codeSet := map[string]struct{}{}
	for _, state := range states {
		if state.RecommendID != 0 {
			recommendIDs = append(recommendIDs, state.RecommendID)
		}
		if code := normalizeRecommendStockCode(state.StockCode); code != "" {
			codeSet[code] = struct{}{}
		}
	}
	if len(recommendIDs) > 0 {
		if err := db.Dao.Model(&models.AiRecommendStocks{}).
			Where("id IN ? AND activation_status = ?", recommendIDs, "skipped").
			Updates(map[string]any{
				"activation_status":         "pending",
				"activation_invalid_reason": "",
				"updated_at":                now,
			}).Error; err != nil {
			return stats, err
		}
		if err := resetSameDayOnlySkippedYieldRecordStates(recommendIDs, now); err != nil {
			return stats, err
		}
		stats.RecordStatesReset = len(recommendIDs)
	}

	scopeCodes := make([]string, 0, len(codeSet))
	for code := range codeSet {
		scopeCodes = append(scopeCodes, code)
	}
	if len(scopeCodes) > 0 {
		if err := resetSameDayOnlySkippedYieldAggregateStates(scopeCodes, now); err != nil {
			return stats, err
		}
		stats.AggregateReset = len(scopeCodes)
		if err := markAiRecommendYieldDirtyCodes(scopeCodes, "sameDayOnly 历史硬跳过修复后等待重算", aiRecommendYieldModeStrict); err != nil {
			return stats, err
		}
		requestAiRecommendYieldRecalcWithScope(true, sameDayOnlyLegacySkipRepairReason, scopeCodes)
		stats.RecalcQueuedCodes = len(scopeCodes)
	}
	return stats, nil
}

func resetHistoricalSkippedYieldRecordState(recommendID uint, now time.Time) error {
	if recommendID == 0 {
		return nil
	}
	return db.Dao.Model(&models.AiRecommendYieldRecordState{}).
		Where("recommend_id = ? AND activation_status = ?", recommendID, "skipped").
		Updates(map[string]any{
			"activation_status":    "pending",
			"activation_time":      nil,
			"activation_price":     0,
			"buy_time":             nil,
			"buy_amount":           0,
			"position_status":      "待激活",
			"sell_time":            nil,
			"realized_sell_amount": nil,
			"yield_rate":           0,
			"yield_rate_text":      "--",
			"data_status":          "待激活",
			"data_status_reason":   "历史跳过修复后待重算",
			"last_recalc_at":       &now,
			"frozen":               false,
		}).Error
}

func resetSameDayOnlySkippedYieldRecordStates(recommendIDs []uint, now time.Time) error {
	if len(recommendIDs) == 0 {
		return nil
	}
	return db.Dao.Model(&models.AiRecommendYieldRecordState{}).
		Where("recommend_id IN ? AND activation_status = ? AND data_status_reason = ?", recommendIDs, "skipped", sameDayOnlyLegacySkipReason).
		Updates(map[string]any{
			"activation_status":    "pending",
			"activation_time":      nil,
			"activation_price":     0,
			"buy_time":             nil,
			"buy_amount":           0,
			"position_status":      "待激活",
			"sell_time":            nil,
			"realized_sell_amount": nil,
			"yield_rate":           0,
			"yield_rate_text":      "--",
			"data_status":          "待激活",
			"data_status_reason":   "sameDayOnly 历史硬跳过修复后待重算",
			"last_recalc_at":       &now,
			"frozen":               false,
		}).Error
}

func resetHistoricalSkippedYieldAggregateState(stockCode string, now time.Time) error {
	stockCode = normalizeRecommendStockCode(stockCode)
	if stockCode == "" {
		return nil
	}
	return db.Dao.Model(&models.AiRecommendYieldState{}).
		Where("stock_code = ? AND activation_status = ?", stockCode, "skipped").
		Updates(map[string]any{
			"activation_status":    "pending",
			"activation_time":      nil,
			"activation_price":     0,
			"buy_time":             nil,
			"buy_amount":           0,
			"position_status":      "待激活",
			"sell_time":            nil,
			"realized_sell_amount": nil,
			"yield_rate":           0,
			"yield_rate_text":      "--",
			"data_status":          "待激活",
			"data_status_reason":   "历史跳过修复后待重算",
			"last_recalc_at":       &now,
			"frozen":               false,
		}).Error
}

func resetSameDayOnlySkippedYieldAggregateStates(stockCodes []string, now time.Time) error {
	normalized := normalizeScopeCodes(stockCodes)
	if len(normalized) == 0 {
		return nil
	}
	codes := make([]string, 0, len(normalized))
	for code := range normalized {
		codes = append(codes, code)
	}
	return db.Dao.Model(&models.AiRecommendYieldState{}).
		Where("stock_code IN ? AND activation_status = ? AND data_status_reason = ?", codes, "skipped", sameDayOnlyLegacySkipReason).
		Updates(map[string]any{
			"activation_status":    "pending",
			"activation_time":      nil,
			"activation_price":     0,
			"buy_time":             nil,
			"buy_amount":           0,
			"position_status":      "待激活",
			"sell_time":            nil,
			"realized_sell_amount": nil,
			"yield_rate":           0,
			"yield_rate_text":      "--",
			"data_status":          "待激活",
			"data_status_reason":   "sameDayOnly 历史硬跳过修复后待重算",
			"last_recalc_at":       &now,
			"frozen":               false,
		}).Error
}
