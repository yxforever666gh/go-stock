package data

import (
	"errors"
	"fmt"
	"go-stock/backend/db"
	"go-stock/backend/models"
	appconfig "go-stock/internal/config"
	"strings"
	"time"

	"gorm.io/gorm"
)

func normalizeAiRecommendYieldMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case aiRecommendYieldModeStrict:
		return aiRecommendYieldModeStrict
	default:
		return aiRecommendYieldModeStrict
	}
}

func resolveAiRecommendYieldMode(query *models.AiRecommendStocksQuery) string {
	defaultMode := normalizeAiRecommendYieldMode(appconfig.Load().Yield.DefaultMode)
	if defaultMode == "" {
		defaultMode = aiRecommendYieldModeStrict
	}
	if query == nil {
		return defaultMode
	}
	mode := normalizeAiRecommendYieldMode(query.YieldMode)
	if strings.TrimSpace(query.YieldMode) == "" {
		mode = defaultMode
	}
	query.YieldMode = mode
	return mode
}

var genericRecommendReasonTexts = map[string]struct{}{
	"推荐":         {},
	"看好":         {},
	"建议关注":       {},
	"市场资讯AI总结推荐": {},
}

const defaultAiRecommendRemarks = "执行前确认量价、板块联动、仓位和风险承受能力"

func firstNumericText(text string) string {
	return strings.TrimSpace(priceNumberRegexp.FindString(strings.TrimSpace(text)))
}

var evidenceTypeAliasMap = map[string]string{
	"市场资讯":     "市场资讯",
	"个股新闻":     "个股新闻",
	"行业研报":     "行业研报",
	"财报":       "财报/财务",
	"财报/财务":    "财报/财务",
	"财务":       "财报/财务",
	"互动易":      "互动易",
	"技术/资金/形态": "技术/资金/形态",
	"技术面":      "技术/资金/形态",
	"资金面":      "技术/资金/形态",
	"形态":       "技术/资金/形态",
	"一级披露":     "一级披露",
	"原始披露":     "一级披露",
	"公告":       "一级披露",
	"资金结构":     "资金结构",
	"股东/筹码":    "股东/筹码",
	"股东筹码":     "股东/筹码",
	"产业高频":     "产业高频",
	"高频指标":     "产业高频",
	"海外风险":     "海外风险",
}

var evidencePositiveKeywords = []string{"回购", "增持", "预增", "中标", "突破", "放量", "改善", "增长", "合作", "投产", "获批", "上调", "扩产", "景气"}
var evidenceNegativeKeywords = []string{"减持", "解禁", "问询", "监管", "立案", "亏损", "下滑", "不及预期", "低于预期", "跌破", "处罚", "终止", "质押", "风险", "波动", "走弱"}

const (
	recommendExecutionImmediate      = "immediate"
	recommendExecutionConditional    = "conditional"
	recommendExecutionAnalysisOnly   = "analysis_only"
	recommendStatusPendingMarketData = "pending_market_data"
	recommendActivationPendingData   = "pending_data"
)

func normalizeAiRecommendStockForSave(recommend *models.AiRecommendStocks) error {
	if recommend == nil {
		return errors.New("推荐记录不能为空")
	}

	normalizeAiRecommendStockBaseFields(recommend)
	signalDrivenMode, structuredMode := normalizeAiRecommendStockModes(recommend)
	normalizeRecommendExecutionFields(recommend)
	repairRecommendBuyRangeFromSignals(recommend)
	fillSignalDrivenRecommendCompat(recommend, signalDrivenMode, structuredMode)
	finalizeAiRecommendStockDefaults(recommend)
	applyAiRecommendStockTimeDefaults(recommend)
	if err := normalizeMarketSummaryExecutionDataForSave(recommend); err != nil {
		return err
	}
	if isAnalysisOnlyRecommend(recommend) {
		recommend.ExecutionState = recommendExecutionAnalysisOnly
		recommend.RecommendCategory = ""
		return validateAiRecommendStockForSave(recommend, 0, 0, 0)
	}

	buyMin, buyMax, stopProfitMin, stopLossValue, err := validateAndNormalizeAiRecommendPrices(recommend)
	if err != nil {
		return err
	}
	applyAiRecommendStockPriceFallbacks(recommend, buyMin)
	if err := validateMarketSummaryRecommendForSave(recommend); err != nil {
		return err
	}
	if err := validateAiRecommendStockForSave(recommend, buyMin, buyMax, stopLossValue); err != nil {
		return err
	}
	if err := normalizeActivationRuleForSave(recommend); err != nil {
		return err
	}
	if stopProfitMin <= buyMax {
		return errors.New("建议止盈区间必须高于建议买入区间上沿")
	}
	if stopLossValue >= buyMax {
		return errors.New("建议止损价必须低于建议买入区间")
	}
	return nil
}

func normalizeAiRecommendStockBaseFields(recommend *models.AiRecommendStocks) {
	recommend.ProviderName = strings.TrimSpace(recommend.ProviderName)
	recommend.ModelName = strings.TrimSpace(recommend.ModelName)
	if recommend.ProviderName == "" && recommend.ModelName != "" {
		recommend.ProviderName = strings.TrimSpace(DetectAIProviderName(&AIConfig{ModelName: recommend.ModelName}))
	}
	recommend.StockCode = normalizeRecommendStockCode(recommend.StockCode)
	recommend.StockName = strings.TrimSpace(recommend.StockName)
	recommend.BkCode = strings.TrimSpace(strings.ToUpper(recommend.BkCode))
	recommend.BkName = strings.TrimSpace(recommend.BkName)
	recommend.StockPrice = firstNumericText(recommend.StockPrice)
	recommend.StockCurrentPrice = firstNumericText(recommend.StockCurrentPrice)
	recommend.StockCurrentPriceTime = strings.TrimSpace(recommend.StockCurrentPriceTime)
	recommend.StockClosePrice = firstNumericText(recommend.StockClosePrice)
	recommend.StockPrePrice = firstNumericText(recommend.StockPrePrice)
	recommend.RecommendReason = normalizeRecommendText(recommend.RecommendReason)
	recommend.RiskRemarks = normalizeRecommendText(recommend.RiskRemarks)
	recommend.Remarks = normalizeRecommendText(recommend.Remarks)
	recommend.ExecutionState = normalizeRecommendExecutionState(recommend.ExecutionState)
	recommend.BuySignal = normalizeRecommendText(recommend.BuySignal)
	recommend.BuySignalDetail = normalizeRecommendText(recommend.BuySignalDetail)
	recommend.SellSignal = normalizeRecommendText(recommend.SellSignal)
	recommend.SellSignalDetail = normalizeRecommendText(recommend.SellSignalDetail)
	recommend.InvalidSignal = normalizeRecommendText(recommend.InvalidSignal)
	recommend.RecommendCategory = normalizeRecommendCategory(recommend.RecommendCategory)
	recommend.CoreCatalyst = normalizeRecommendText(recommend.CoreCatalyst)
	recommend.KeyEvidence = normalizeRecommendText(recommend.KeyEvidence)
	recommend.EvidenceSources = strings.TrimSpace(recommend.EvidenceSources)
	recommend.InvalidCondition = normalizeRecommendText(recommend.InvalidCondition)
	recommend.ObservePrice = firstNumericText(recommend.ObservePrice)
	recommend.FocusPrice = strings.TrimSpace(recommend.FocusPrice)
	recommend.ExpectedCycle = strings.TrimSpace(recommend.ExpectedCycle)
	recommend.RecommendStatus = normalizeRecommendStatus(recommend.RecommendStatus)
	recommend.SummaryVersion = strings.TrimSpace(recommend.SummaryVersion)
	recommend.StrategyRunID = strings.TrimSpace(recommend.StrategyRunID)
	recommend.StrategyRuleID = strings.TrimSpace(recommend.StrategyRuleID)
	recommend.ActivationRuleJSON = strings.TrimSpace(recommend.ActivationRuleJSON)
	recommend.ActivationRuleVersion = strings.TrimSpace(recommend.ActivationRuleVersion)
	recommend.ActivationRuleSource = strings.TrimSpace(recommend.ActivationRuleSource)
	recommend.ActivationStatus = strings.TrimSpace(recommend.ActivationStatus)
	recommend.ActivationInvalidReason = normalizeRecommendText(recommend.ActivationInvalidReason)
	recommend.EventStrength = clampConfidenceScore(recommend.EventStrength)
	recommend.CapitalConfirmation = clampConfidenceScore(recommend.CapitalConfirmation)
	recommend.FundamentalFit = clampConfidenceScore(recommend.FundamentalFit)
	recommend.TechnicalFit = clampConfidenceScore(recommend.TechnicalFit)
	if recommend.SummaryVersion == "" {
		recommend.SummaryVersion = defaultAiRecommendSummaryVersion
	}
}

func normalizeAiRecommendStockModes(recommend *models.AiRecommendStocks) (bool, bool) {
	signalDrivenMode := hasSignalDrivenRecommend(recommend)
	mergeStructuredRecommendCompatFields(recommend)
	normalizeRecommendEvidenceSources(recommend)
	structuredMode := hasStructuredRecommendPayload(recommend)
	if !signalDrivenMode {
		if recommend.RecommendCategory == "" {
			recommend.RecommendCategory = inferRecommendCategory(recommend)
		}
		if recommend.RecommendCategory == "" {
			recommend.RecommendCategory = recommendExecutionConditional
		}
		if recommend.RecommendStatus == "" {
			switch recommend.RecommendCategory {
			case "avoid":
				recommend.RecommendStatus = "avoid"
			default:
				recommend.RecommendStatus = "valid"
			}
		}
	}
	if structuredMode {
		applyStructuredRecommendRules(recommend)
	}
	applyRecommendTimingRules(recommend)
	return signalDrivenMode, structuredMode
}

func finalizeAiRecommendStockDefaults(recommend *models.AiRecommendStocks) {
	if recommend.Remarks == "" {
		recommend.Remarks = buildDefaultRemarks(recommend)
	}
}

func validateAndNormalizeAiRecommendPrices(recommend *models.AiRecommendStocks) (float64, float64, float64, float64, error) {
	buyText, buyMin, buyMax := normalizePriceRangeText(recommend.RecommendBuyPrice, recommend.RecommendBuyPriceMin, recommend.RecommendBuyPriceMax)
	if buyMin <= 0 || buyMax <= 0 {
		return 0, 0, 0, 0, errors.New("建议买入区间不能为空")
	}
	recommend.RecommendBuyPrice = buyText
	recommend.RecommendBuyPriceMin = buyMin
	recommend.RecommendBuyPriceMax = buyMax

	stopProfitText, stopProfitMin, stopProfitMax := normalizePriceRangeText(recommend.RecommendStopProfitPrice, recommend.RecommendStopProfitPriceMin, recommend.RecommendStopProfitPriceMax)
	if stopProfitMin <= 0 || stopProfitMax <= 0 {
		return 0, 0, 0, 0, errors.New("建议止盈区间不能为空")
	}
	recommend.RecommendStopProfitPrice = stopProfitText
	recommend.RecommendStopProfitPriceMin = stopProfitMin
	recommend.RecommendStopProfitPriceMax = stopProfitMax

	stopLossText, stopLossValue := normalizeSinglePriceText(recommend.RecommendStopLossPrice)
	if stopLossValue <= 0 {
		return 0, 0, 0, 0, errors.New("建议止损价不能为空")
	}
	recommend.RecommendStopLossPrice = stopLossText
	return buyMin, buyMax, stopProfitMin, stopLossValue, nil
}

func applyAiRecommendStockPriceFallbacks(recommend *models.AiRecommendStocks, buyMin float64) {
	if recommend.StockPrice == "" {
		switch {
		case recommend.ObservePrice != "":
			recommend.StockPrice = recommend.ObservePrice
		case recommend.StockCurrentPrice != "":
			recommend.StockPrice = recommend.StockCurrentPrice
		case recommend.StockClosePrice != "":
			recommend.StockPrice = recommend.StockClosePrice
		default:
			recommend.StockPrice = formatRecommendPrice(buyMin)
		}
	}
	if recommend.ObservePrice == "" {
		recommend.ObservePrice = recommend.StockPrice
	}
	if recommend.StockCurrentPrice == "" {
		recommend.StockCurrentPrice = recommend.StockPrice
	}
	if recommend.StockClosePrice == "" {
		recommend.StockClosePrice = recommend.StockPrice
	}
	if recommend.StockPrePrice == "" {
		recommend.StockPrePrice = recommend.StockPrice
	}
}

func applyAiRecommendStockTimeDefaults(recommend *models.AiRecommendStocks) {
	if recommend.StockCurrentPriceTime == "" {
		if recommend.DataTime != nil && !recommend.DataTime.IsZero() {
			recommend.StockCurrentPriceTime = recommend.DataTime.Format(time.DateTime)
		} else {
			recommend.StockCurrentPriceTime = time.Now().Format(time.DateTime)
		}
	}
	if recommend.DataTime == nil || recommend.DataTime.IsZero() {
		now := time.Now()
		recommend.DataTime = &now
	}
}

func validateAiRecommendStockForSave(recommend *models.AiRecommendStocks, buyMin, buyMax, stopLossValue float64) error {
	_ = buyMin
	_ = buyMax
	_ = stopLossValue
	if recommend.StockCode == "" {
		return errors.New("股票代码不能为空")
	}
	if recommend.StockName == "" {
		return errors.New("股票名称不能为空")
	}
	if recommend.BkName == "" {
		return errors.New("所属方向/板块不能为空")
	}
	if !hasEnoughRecommendReason(recommend.RecommendReason) {
		return errors.New("推荐理由过短或缺少有效逻辑")
	}
	if len([]rune(recommend.RiskRemarks)) < 6 {
		return errors.New("风险提示不能为空，且至少包含一条有效风险")
	}
	if isAnalysisOnlyRecommend(recommend) {
		return nil
	}
	if err := validateSignalDrivenRecommend(recommend); err != nil {
		return err
	}
	return nil
}

func (s *AiRecommendStocksService) CreateAiRecommendStocks(recommend *models.AiRecommendStocks) error {
	// Preserve the generic API's empty-version input compatibility; normalization
	// assigns its historical default. Any explicitly requested old cohort is
	// rejected so callers cannot deliberately append to a frozen release.
	if recommend != nil && strings.TrimSpace(recommend.SummaryVersion) != "" && isFrozenLegacyStrategyRecord(recommend) {
		return fmt.Errorf("strategy cohort %s is frozen; new records are not permitted", strings.TrimSpace(recommend.SummaryVersion))
	}
	if err := normalizeAiRecommendStockForSave(recommend); err != nil {
		return err
	}
	resultErr := db.Dao.Transaction(func(tx *gorm.DB) error {
		if err := validateRecommendDailyUniqueness(tx, []*models.AiRecommendStocks{recommend}); err != nil {
			return err
		}
		return tx.Create(recommend).Error
	})
	if resultErr == nil {
		scopeCodes := make([]string, 0, 1)
		if recommend != nil {
			code := strings.TrimSpace(recommend.StockCode)
			if code != "" {
				scopeCodes = append(scopeCodes, code)
			}
		}
		_ = markAiRecommendYieldDirtyCodesForMutationFn(scopeCodes, "新增推荐后等待严格模式下载/回算", aiRecommendYieldModeStrict)
		requestAiRecommendYieldScopedRecalcForMutationFn(false, "recommend_created", scopeCodes)
	}
	return resultErr
}

func (s *AiRecommendStocksService) BatchCreateAiRecommendStocks(recommends []*models.AiRecommendStocks) error {
	normalized := make([]*models.AiRecommendStocks, 0, len(recommends))
	for idx, item := range recommends {
		if item == nil {
			continue
		}
		if strings.TrimSpace(item.SummaryVersion) != "" && isFrozenLegacyStrategyRecord(item) {
			return fmt.Errorf("strategy cohort %s is frozen; new records are not permitted", strings.TrimSpace(item.SummaryVersion))
		}
		if err := normalizeAiRecommendStockForSave(item); err != nil {
			return fmt.Errorf("第%d条推荐记录不完整: %w", idx+1, err)
		}
		normalized = append(normalized, item)
	}
	if len(normalized) == 0 {
		return errors.New("没有可保存的推荐记录")
	}
	resultErr := db.Dao.Transaction(func(tx *gorm.DB) error {
		if err := validateRecommendDailyUniqueness(tx, normalized); err != nil {
			return err
		}
		return tx.Create(normalized).Error
	})
	if resultErr == nil {
		scopeCodes := make([]string, 0, len(normalized))
		for _, item := range normalized {
			if item == nil {
				continue
			}
			code := strings.TrimSpace(item.StockCode)
			if code == "" {
				continue
			}
			scopeCodes = append(scopeCodes, code)
		}
		_ = markAiRecommendYieldDirtyCodesForMutationFn(scopeCodes, "批量新增推荐后等待严格模式下载/回算", aiRecommendYieldModeStrict)
		requestAiRecommendYieldScopedRecalcForMutationFn(false, "recommend_batch_created", scopeCodes)
	}
	return resultErr
}

type recommendDailyKey struct {
	StockCode string
	DayText   string
}

func validateRecommendDailyUniqueness(tx *gorm.DB, recommends []*models.AiRecommendStocks) error {
	if len(recommends) == 0 {
		return nil
	}
	if tx == nil {
		tx = db.Dao
	}

	seen := make(map[recommendDailyKey]*models.AiRecommendStocks, len(recommends))
	for _, recommend := range recommends {
		if recommend == nil {
			continue
		}
		dayStart, dayEnd, dayText, ok := recommendDayBounds(recommend)
		if !ok {
			continue
		}
		code := normalizeRecommendStockCode(recommend.StockCode)
		if code == "" {
			continue
		}
		key := recommendDailyKey{StockCode: code, DayText: dayText}
		if existing := seen[key]; existing != nil {
			return duplicateRecommendDailyError(recommend, dayText, true)
		}
		seen[key] = recommend

		query := tx.Model(&models.AiRecommendStocks{}).
			Where("stock_code = ?", code).
			Where("COALESCE(data_time, created_at) BETWEEN ? AND ?", dayStart, dayEnd)
		if recommend.ID != 0 {
			query = query.Where("id <> ?", recommend.ID)
		}
		var count int64
		if err := query.Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return duplicateRecommendDailyError(recommend, dayText, false)
		}
	}
	return nil
}

func recommendDayBounds(recommend *models.AiRecommendStocks) (time.Time, time.Time, string, bool) {
	if recommend == nil {
		return time.Time{}, time.Time{}, "", false
	}
	recordTime := time.Time{}
	if recommend.DataTime != nil && !recommend.DataTime.IsZero() {
		recordTime = recommend.DataTime.In(cnLocation())
	} else if !recommend.CreatedAt.IsZero() {
		recordTime = recommend.CreatedAt.In(cnLocation())
	}
	if recordTime.IsZero() {
		return time.Time{}, time.Time{}, "", false
	}
	dayStart := time.Date(recordTime.Year(), recordTime.Month(), recordTime.Day(), 0, 0, 0, 0, cnLocation())
	dayEnd := dayStart.Add(24*time.Hour - time.Nanosecond)
	return dayStart, dayEnd, dayStart.Format("2006-01-02"), true
}

func duplicateRecommendDailyError(recommend *models.AiRecommendStocks, dayText string, inBatch bool) error {
	code := ""
	name := ""
	if recommend != nil {
		code = normalizeRecommendStockCode(recommend.StockCode)
		name = strings.TrimSpace(recommend.StockName)
	}
	label := strings.TrimSpace(strings.TrimSpace(name) + " " + strings.TrimSpace(code))
	if label == "" {
		label = code
	}
	if label == "" {
		label = "该股票"
	}
	if inBatch {
		return fmt.Errorf("硬性拦截：%s 在 %s 的批量推荐中重复出现，同一天不能同时买入同一只股票", label, dayText)
	}
	return fmt.Errorf("硬性拦截：%s 在 %s 已有推荐记录，同一天不能同时买入同一只股票", label, dayText)
}
