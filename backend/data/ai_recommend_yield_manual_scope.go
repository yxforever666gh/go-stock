package data

import (
	"fmt"
	"go-stock/backend/db"
	"go-stock/backend/models"
	"strings"
)

func loadScopeCodesForManualDownload() ([]string, error) {
	if err := markV132VWAPScaleDirtyCodes(aiRecommendYieldModeStrict); err != nil {
		return nil, err
	}
	loaders := []func() ([]string, error){
		loadManualDownloadScopeCodesByCoverage,
		loadManualDownloadScopeCodesByRecoverablePlans,
	}
	dirtyCodes, err := loadDirtyAiRecommendYieldCodes(aiRecommendYieldModeStrict)
	if err != nil {
		return nil, err
	}
	if len(dirtyCodes) > 0 {
		filtered, filterErr := filterManualDownloadScopeCodes(dirtyCodes)
		if filterErr != nil {
			return nil, filterErr
		}
		if len(filtered) > 0 {
			loaders = append([]func() ([]string, error){
				func() ([]string, error) { return filtered, nil },
			}, loaders...)
		}
	}
	return mergeManualDownloadScopeCodes(loaders...)
}

func mergeManualDownloadScopeCodes(loaders ...func() ([]string, error)) ([]string, error) {
	scope := make(map[string]struct{})
	for _, loader := range loaders {
		if loader == nil {
			continue
		}
		codes, err := loader()
		if err != nil {
			return nil, err
		}
		for _, code := range codes {
			code = normalizeRecommendStockCode(code)
			if code == "" {
				continue
			}
			scope[code] = struct{}{}
		}
	}
	return keysFromScopeMap(scope), nil
}

func filterManualDownloadScopeCodes(codes []string) ([]string, error) {
	normalized := normalizeScopeCodes(codes)
	if len(normalized) == 0 {
		return []string{}, nil
	}
	rows := make([]models.AiRecommendStocks, 0, len(normalized)*2)
	if err := db.Dao.Model(&models.AiRecommendStocks{}).
		Where("stock_code IN ?", keysFromScopeMap(normalized)).
		Order("COALESCE(data_time, created_at) ASC, id ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	rows, err := applyYieldOverridesToRecommendRecords(rows)
	if err != nil {
		return nil, err
	}
	recordStateMap, err := loadExistingYieldRecordStateMap()
	if err != nil {
		return nil, err
	}
	resultSet := make(map[string]struct{}, len(normalized))
	for _, rec := range rows {
		code := normalizeRecommendStockCode(rec.StockCode)
		if code == "" {
			continue
		}
		if shouldIncludeRecoverableMarketDataGapInManualDownload(rec) {
			resultSet[code] = struct{}{}
			continue
		}
		if state, ok := recordStateMap[rec.ID]; ok && shouldRecalcV132VWAPScaleState(rec, state) {
			resultSet[code] = struct{}{}
			continue
		}
		if !shouldDisplayRecommendInYield(&rec) {
			continue
		}
		eligibility, _ := resolveRecommendBacktestEligibility(&rec)
		if eligibility != recommendBacktestEligible {
			continue
		}
		if normalizeRecommendExecutionState(rec.ExecutionState) == recommendExecutionAnalysisOnly {
			continue
		}
		status := strings.TrimSpace(strings.ToLower(rec.ActivationStatus))
		if state, ok := recordStateMap[rec.ID]; ok && strings.TrimSpace(state.ActivationStatus) != "" {
			status = strings.TrimSpace(strings.ToLower(state.ActivationStatus))
		}
		if status == "invalid" || status == "skipped" || status == "expired" || status == "ineligible" {
			continue
		}
		resultSet[code] = struct{}{}
	}
	return keysFromScopeMap(resultSet), nil
}

func shouldRecalcV132VWAPScaleState(rec models.AiRecommendStocks, state *models.AiRecommendYieldRecordState) bool {
	if !isV132Recommend(rec) {
		return false
	}
	stateReason := ""
	if state != nil {
		stateReason = state.DataStatusReason
	}
	reason := strings.TrimSpace(firstNonEmptyText(stateReason, rec.ActivationInvalidReason))
	return reasonIndicatesV132VWAPScaleBug(reason)
}

func reasonIndicatesV132VWAPScaleBug(reason string) bool {
	reason = strings.TrimSpace(reason)
	if !strings.Contains(reason, "V1.3.2强弱过滤未通过") || !strings.Contains(reason, "VWAP") {
		return false
	}
	activationPrice := 0.0
	vwap := 0.0
	if _, err := fmt.Sscanf(reason, "V1.3.2强弱过滤未通过：激活价 %f 低于 VWAP %f", &activationPrice, &vwap); err != nil {
		return false
	}
	return activationPrice > 0 && vwap > activationPrice*20
}

func shouldIncludeRecoverableMarketDataGapInManualDownload(rec models.AiRecommendStocks) bool {
	if isPendingMarketDataRecommend(&rec) {
		return hasRecoverableMarketSummaryTradePlan(rec)
	}
	if normalizeRecommendStatus(rec.RecommendStatus) != "missing_market_data" {
		return false
	}
	return isRecoverableMarketSummaryMarketDataGap(rec)
}

func loadManualDownloadScopeCodesByRecoverablePlans() ([]string, error) {
	rows := make([]models.AiRecommendStocks, 0, 16)
	if err := db.Dao.Model(&models.AiRecommendStocks{}).
		Where("activation_rule_source IN ?", []string{"market_summary", "market_summary_embedded"}).
		Order("COALESCE(data_time, created_at) ASC, id ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	scope := make(map[string]struct{}, len(rows))
	for _, rec := range rows {
		code := normalizeRecommendStockCode(rec.StockCode)
		if code == "" {
			continue
		}
		if shouldIncludeRecoverableMarketDataGapInManualDownload(rec) || marketSummaryRecommendMissingExitPlan(rec) || marketSummaryRecommendHasStaleMissingMarketDataReason(rec) {
			scope[code] = struct{}{}
		}
	}
	return keysFromScopeMap(scope), nil
}

func loadManualDownloadScopeCodesByCoverage() ([]string, error) {
	meta, err := getOrCreateYieldMeta()
	if err != nil {
		return nil, err
	}
	_, issues := computeMinuteDownloadCoverageStatsWithIssues(meta, -1)
	scopeSet := make(map[string]struct{}, len(issues))
	for _, issue := range issues {
		status := strings.TrimSpace(issue.Status)
		if status != "待覆盖" {
			continue
		}
		code := normalizeRecommendStockCode(issue.StockCode)
		if code == "" {
			continue
		}
		scopeSet[code] = struct{}{}
	}
	return keysFromScopeMap(scopeSet), nil
}
