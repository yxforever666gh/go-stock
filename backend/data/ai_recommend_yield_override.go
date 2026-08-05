package data

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
)

const yieldOverrideSourceMarketSummaryRejudge = "market_summary_rejudge"

func normalizeAiRecommendYieldOverride(override *models.AiRecommendYieldOverride) error {
	if override == nil {
		return errors.New("收益率复审覆盖不能为空")
	}
	if override.RecommendID == 0 {
		return errors.New("recommendId 不能为空")
	}

	override.StockCode = normalizeRecommendStockCode(override.StockCode)
	override.ReviewSource = strings.TrimSpace(override.ReviewSource)
	if override.ReviewSource == "" {
		override.ReviewSource = yieldOverrideSourceMarketSummaryRejudge
	}
	override.ActivationStatusOverride = normalizeYieldOverrideActivationStatus(override.ActivationStatusOverride)
	override.BuySignal = normalizeRecommendText(override.BuySignal)
	override.BuySignalDetail = normalizeRecommendText(override.BuySignalDetail)
	override.ActivationRuleJSON = strings.TrimSpace(override.ActivationRuleJSON)
	override.ActivationRuleVersion = strings.TrimSpace(override.ActivationRuleVersion)
	override.ActivationRuleSource = strings.TrimSpace(override.ActivationRuleSource)
	override.InvalidSignal = normalizeRecommendText(override.InvalidSignal)
	override.InvalidCondition = normalizeRecommendText(override.InvalidCondition)
	override.DataStatusReason = normalizeRecommendText(override.DataStatusReason)

	if text, min, max := normalizePriceRangeText(
		override.RecommendBuyPrice,
		override.RecommendBuyPriceMin,
		override.RecommendBuyPriceMax,
	); min > 0 && max > 0 {
		override.RecommendBuyPrice = text
		override.RecommendBuyPriceMin = min
		override.RecommendBuyPriceMax = max
	}

	if text, min, max := normalizePriceRangeText(
		override.RecommendStopProfitPrice,
		override.RecommendStopProfitPriceMin,
		override.RecommendStopProfitPriceMax,
	); min > 0 && max > 0 {
		override.RecommendStopProfitPrice = text
		override.RecommendStopProfitPriceMin = min
		override.RecommendStopProfitPriceMax = max
	}

	if text, value := normalizeSinglePriceText(override.RecommendStopLossPrice); value > 0 {
		override.RecommendStopLossPrice = text
	}

	if override.ReviewedAt == nil || override.ReviewedAt.IsZero() {
		now := time.Now().In(cnLocation())
		override.ReviewedAt = &now
	}

	return nil
}

func normalizeYieldOverrideActivationStatus(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "skipped", "已跳过", "continue_skip", "keep_skipped":
		return "skipped"
	case "pending", "待激活", "reactivate", "reactivated":
		return "pending"
	case "activated", "已激活":
		return "activated"
	case "invalid", "无法回算", "未激活失效":
		return "invalid"
	case "ineligible", "未纳入回测":
		return "ineligible"
	default:
		return ""
	}
}

func upsertAiRecommendYieldOverride(override *models.AiRecommendYieldOverride) error {
	if err := normalizeAiRecommendYieldOverride(override); err != nil {
		return err
	}

	rec := models.AiRecommendStocks{}
	if err := db.Dao.Model(&models.AiRecommendStocks{}).Select("id", "stock_code", "summary_version").First(&rec, override.RecommendID).Error; err != nil {
		return err
	}
	if isFrozenLegacyStrategyRecord(&rec) {
		return fmt.Errorf("strategy cohort %s is frozen; yield overrides are not permitted", strings.TrimSpace(rec.SummaryVersion))
	}
	version := strings.TrimSpace(rec.SummaryVersion)
	if version == "" {
		version = "unversioned"
	}
	return fmt.Errorf("strategy cohort %s is immutable; mutable yield overrides are disabled", version)
}

func loadYieldOverrideMapByRecommendIDs(ids []uint) (map[uint]models.AiRecommendYieldOverride, error) {
	result := map[uint]models.AiRecommendYieldOverride{}
	if len(ids) == 0 {
		return result, nil
	}
	rows := make([]models.AiRecommendYieldOverride, 0, len(ids))
	if err := db.Dao.Model(&models.AiRecommendYieldOverride{}).Where("recommend_id IN ?", ids).Find(&rows).Error; err != nil {
		if isSQLiteNoSuchTable(err) {
			return result, nil
		}
		return nil, err
	}
	for _, row := range rows {
		if row.RecommendID == 0 {
			continue
		}
		result[row.RecommendID] = row
	}
	return result, nil
}

func loadYieldOverrideMapByRecommendRecords(records []models.AiRecommendStocks) (map[uint]models.AiRecommendYieldOverride, error) {
	if len(records) == 0 {
		return map[uint]models.AiRecommendYieldOverride{}, nil
	}
	ids := make([]uint, 0, len(records))
	for _, rec := range records {
		if rec.ID == 0 {
			continue
		}
		ids = append(ids, rec.ID)
	}
	return loadYieldOverrideMapByRecommendIDs(ids)
}

func applyYieldOverridesToRecommendRecords(records []models.AiRecommendStocks) ([]models.AiRecommendStocks, error) {
	if len(records) == 0 {
		return records, nil
	}
	ids := make([]uint, 0, len(records))
	for _, rec := range records {
		if rec.ID == 0 {
			continue
		}
		ids = append(ids, rec.ID)
	}
	overrideMap, err := loadYieldOverrideMapByRecommendIDs(ids)
	if err != nil {
		return nil, err
	}
	if len(overrideMap) == 0 {
		return records, nil
	}
	result := make([]models.AiRecommendStocks, 0, len(records))
	for _, rec := range records {
		if override, ok := overrideMap[rec.ID]; ok {
			applyYieldOverrideToRecommend(&rec, &override)
		}
		result = append(result, rec)
	}
	return result, nil
}

func applyYieldOverrideToRecommend(rec *models.AiRecommendStocks, override *models.AiRecommendYieldOverride) {
	if rec == nil || override == nil {
		return
	}
	if isV150CostVersion(rec.SummaryVersion) {
		// V1.5 recommendations are immutable projections of frozen candidate,
		// rule and order snapshots. Applying a mutable historical repair here
		// would alter eligibility before the ledger-backed yield mapper runs.
		return
	}
	analysisOnly := isAnalysisOnlyRecommend(rec)
	originalCategory := rec.RecommendCategory
	originalStatus := rec.RecommendStatus
	if override.RecommendBuyPrice != "" {
		rec.RecommendBuyPrice = override.RecommendBuyPrice
		rec.RecommendBuyPriceMin = override.RecommendBuyPriceMin
		rec.RecommendBuyPriceMax = override.RecommendBuyPriceMax
	}
	if override.RecommendStopProfitPrice != "" {
		rec.RecommendStopProfitPrice = override.RecommendStopProfitPrice
		rec.RecommendStopProfitPriceMin = override.RecommendStopProfitPriceMin
		rec.RecommendStopProfitPriceMax = override.RecommendStopProfitPriceMax
	}
	if override.RecommendStopLossPrice != "" {
		rec.RecommendStopLossPrice = override.RecommendStopLossPrice
	}
	if override.BuySignal != "" {
		rec.BuySignal = override.BuySignal
	}
	if override.BuySignalDetail != "" {
		rec.BuySignalDetail = override.BuySignalDetail
	}
	if strings.TrimSpace(override.ActivationRuleJSON) != "" {
		rec.ActivationRuleJSON = strings.TrimSpace(override.ActivationRuleJSON)
		rec.ActivationRuleVersion = strings.TrimSpace(override.ActivationRuleVersion)
		rec.ActivationRuleSource = strings.TrimSpace(override.ActivationRuleSource)
		rec.ActivationInvalidReason = ""
	}
	if override.InvalidSignal != "" {
		rec.InvalidSignal = override.InvalidSignal
	}
	if override.InvalidCondition != "" {
		rec.InvalidCondition = override.InvalidCondition
	}
	switch override.ActivationStatusOverride {
	case "pending", "activated":
		if analysisOnly {
			break
		}
		rec.RecommendCategory = "conditional"
		rec.RecommendStatus = "valid"
		if strings.TrimSpace(rec.ExecutionState) == "" {
			rec.ExecutionState = recommendExecutionConditional
		}
	case "ineligible":
		if !analysisOnly {
			rec.RecommendStatus = "ineligible"
		}
	case "skipped":
		// Keep original category/status so the row still behaves like skipped, but
		// prefer the latest invalid condition when present.
	}
	if analysisOnly {
		rec.ExecutionState = recommendExecutionAnalysisOnly
		rec.RecommendCategory = originalCategory
		rec.RecommendStatus = originalStatus
	}
	rec.RecommendReason = buildRecommendReasonCompat(rec)
}

func applyYieldOverrideToYieldItem(item *models.AiRecommendStocksYieldItem, override *models.AiRecommendYieldOverride) {
	if item == nil || override == nil {
		return
	}
	if isV150CostVersion(item.SummaryVersion) {
		// V1.5 execution and accounting are projections of frozen rules and the
		// append-only order ledger. A mutable display override must never change
		// activation, prices, or eligibility for this cohort.
		return
	}
	if override.RecommendBuyPrice != "" {
		item.RecommendBuyPrice = override.RecommendBuyPrice
	}
	if override.RecommendStopProfitPriceMin > 0 {
		v := round2(override.RecommendStopProfitPriceMin)
		item.StopProfitAmount = &v
	}
	if override.RecommendStopLossPrice != "" {
		if v, ok := parseStopLossPrice(models.AiRecommendStocks{RecommendStopLossPrice: override.RecommendStopLossPrice}); ok {
			val := round2(v)
			item.StopLossAmount = &val
		}
	}
	if override.BuySignal != "" {
		item.BuySignal = override.BuySignal
	}
	if override.BuySignalDetail != "" {
		item.BuySignalDetail = override.BuySignalDetail
	}
	if strings.TrimSpace(override.ActivationRuleJSON) != "" {
		item.ActivationRule = strings.TrimSpace(override.ActivationRuleJSON)
		item.ActivationInvalidReason = ""
	}
	if override.InvalidSignal != "" {
		item.InvalidSignal = override.InvalidSignal
	}
	if strings.TrimSpace(override.DataStatusReason) != "" {
		item.DataStatusReason = strings.TrimSpace(override.DataStatusReason)
	}
	if override.ActivationStatusOverride != "" && normalizeRecommendExecutionState(item.ExecutionState) != recommendExecutionAnalysisOnly {
		item.ActivationStatus = override.ActivationStatusOverride
		switch override.ActivationStatusOverride {
		case "pending":
			item.DataStatus = firstNonEmptyText(item.DataStatus, "正常")
		case "skipped":
			item.DataStatus = "已跳过"
		case "invalid":
			item.DataStatus = "无法判定"
		case "ineligible":
			item.DataStatus = "未结构化"
		}
		applyInactiveYieldDefaults(item)
	}
}

func loadYieldOverrideCandidatesForRecentTradeDays(tradeDays int, now time.Time) ([]models.AiRecommendStocks, error) {
	if tradeDays <= 0 {
		return []models.AiRecommendStocks{}, nil
	}
	records := make([]models.AiRecommendStocks, 0, 64)
	if err := db.Dao.Model(&models.AiRecommendStocks{}).
		Where("summary_version = ?", marketSummaryVersion150).
		Order("COALESCE(data_time, created_at) DESC, id DESC").
		Find(&records).Error; err != nil {
		return nil, err
	}
	records, err := applyYieldOverridesToRecommendRecords(records)
	if err != nil {
		return nil, err
	}

	cutoffDays := recentCNTradeDaysSet(tradeDays, now)
	if len(cutoffDays) == 0 {
		return []models.AiRecommendStocks{}, nil
	}

	result := make([]models.AiRecommendStocks, 0, len(records))
	for _, rec := range records {
		recordTime := recommendRecordTime(rec)
		if recordTime.IsZero() {
			continue
		}
		dayKey := recordTime.In(cnLocation()).Format("2006-01-02")
		if _, ok := cutoffDays[dayKey]; !ok {
			continue
		}
		if _, _, _, _, skip := resolveRecommendYieldSkipInfo(&rec); !skip {
			continue
		}
		result = append(result, rec)
	}
	return result, nil
}

func recentCNTradeDaysSet(tradeDays int, now time.Time) map[string]struct{} {
	result := map[string]struct{}{}
	if tradeDays <= 0 {
		return result
	}
	loc := cnLocation()
	cur := time.Date(now.In(loc).Year(), now.In(loc).Month(), now.In(loc).Day(), 0, 0, 0, 0, loc)
	for len(result) < tradeDays {
		if !isWeekendCN(cur) {
			result[cur.Format("2006-01-02")] = struct{}{}
		}
		cur = cur.AddDate(0, 0, -1)
	}
	return result
}

func loadYieldOverrideTargetRecord(recommendID uint) (*models.AiRecommendStocks, error) {
	if recommendID == 0 {
		return nil, errors.New("recommendId 不能为空")
	}
	rec := models.AiRecommendStocks{}
	if err := db.Dao.Model(&models.AiRecommendStocks{}).First(&rec, recommendID).Error; err != nil {
		return nil, err
	}
	if strings.TrimSpace(rec.SummaryVersion) != marketSummaryVersion150 {
		return nil, fmt.Errorf("strategy cohort %s is frozen; yield override target is read-only", strings.TrimSpace(rec.SummaryVersion))
	}
	list, err := applyYieldOverridesToRecommendRecords([]models.AiRecommendStocks{rec})
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("recommendId=%d not found", recommendID)
	}
	return &list[0], nil
}
