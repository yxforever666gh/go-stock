package data

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
	"gorm.io/gorm"
)

var (
	marketSummaryStockCellPattern = regexp.MustCompile(`^(.+?)[（(]([0-9]{6}(?:\.(?:SH|SZ|BJ))?)[）)]$`)
	marketSummaryNumberPattern    = regexp.MustCompile(`\d+(?:\.\d+)?`)
	marketSummaryRangePattern     = regexp.MustCompile(`\d+(?:\.\d+)?\s*(?:-|~|至|到)\s*\d+(?:\.\d+)?`)
)

const marketSummaryProductionScoreFloor = 60

// Keep the legacy 1.4.2 production limit immutable. Strategy 1.5.0 reads its
// independent cap from v150.FixedStrategyV150Config instead.
const marketSummaryMaxProductionCandidates = 4
const marketSummaryMaxActivePendingCandidates = 5
const marketSummaryAnalysisOnlyIrreversibleReason = "analysis_only records are irreversible and cannot be upgraded by repair or supplement output"

var marketSummaryRecommendSaveMu sync.Mutex

var marketSummaryObservationPhrases = []string{
	"仅观察",
	"先看",
	"再看",
	"考虑",
	"不建议先手",
	"不建议追",
	"转入右侧跟踪",
	"右侧跟踪",
	"可加关注",
	"才考虑",
	"再评估",
}

func EnsureMarketSummaryRecommendStocksSaved(summaryText, providerName, modelName string, startedAt time.Time) (int, error) {
	if err := requireStrategyProductionLive(nil, db.Dao); err != nil {
		return 0, err
	}
	marketSummaryRecommendSaveMu.Lock()
	defer marketSummaryRecommendSaveMu.Unlock()

	decisionAt := resolveMarketSummaryDecisionCompletionTime(startedAt)
	drafts := parseMarketSummaryRecommendStockDrafts(summaryText, providerName, modelName, decisionAt)
	applyMarketSummaryDecisionTimeline(drafts, startedAt, decisionAt)
	if len(drafts) == 0 {
		return 0, nil
	}

	startOfDay, endOfDay := marketSummaryDayBounds(decisionAt)
	existing := make([]models.AiRecommendStocks, 0, len(drafts))
	if err := db.Dao.Model(&models.AiRecommendStocks{}).
		Where("data_time >= ? AND data_time < ?", startOfDay, endOfDay).
		Where("(summary_version IN ? OR activation_rule_source IN ?)", marketSummaryKnownVersions(), []string{"market_summary", "market_summary_embedded"}).
		Find(&existing).Error; err != nil {
		return 0, err
	}

	missing := collectMarketSummaryRecommendStocksForSave(drafts, existing)
	if len(missing) == 0 {
		return 0, nil
	}
	productionLimit, err := resolveMarketSummaryV150ProductionLimit(db.Dao, decisionAt, marketSummaryV150RiskOnDailyCap())
	if err != nil {
		return 0, err
	}
	applyMarketSummaryProductionSelectionWithLimit(missing, productionLimit)

	items := make([]*models.AiRecommendStocks, 0, len(missing))
	saved := 0
	failures := make([]string, 0, len(missing))
	for _, draft := range missing {
		item, err := draft.toRecommendStock()
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		if len(failures) == 0 {
			return 0, nil
		}
		return 0, errors.New(strings.Join(failures, "；"))
	}
	service := NewAiRecommendStocksService()
	for idx, item := range items {
		if err := service.CreateAiRecommendStocks(item); err != nil {
			failures = append(failures, fmt.Sprintf("第%d条推荐记录不完整: %v", idx+1, err))
			continue
		}
		saved++
	}
	if len(failures) > 0 {
		return saved, errors.New(strings.Join(failures, "；"))
	}
	return saved, nil
}

func EnsureMarketSummaryRecommendStocksSavedWithResult(summaryText, providerName, modelName string, startedAt time.Time, verifiedCandidates []MarketSummaryVerifiedCandidateSnapshot) (*models.MarketSummaryRecommendSaveResult, error) {
	return EnsureMarketSummaryRecommendStocksSavedWithResultLimits(summaryText, providerName, modelName, startedAt, verifiedCandidates, marketSummaryRecommendationOutputLimit, marketSummaryMaxProductionCandidates)
}

func EnsureMarketSummaryRecommendStocksSavedWithResultLimit(summaryText, providerName, modelName string, startedAt time.Time, verifiedCandidates []MarketSummaryVerifiedCandidateSnapshot, productionLimit int) (*models.MarketSummaryRecommendSaveResult, error) {
	return EnsureMarketSummaryRecommendStocksSavedWithResultLimits(summaryText, providerName, modelName, startedAt, verifiedCandidates, marketSummaryRecommendationOutputLimit, productionLimit)
}

func EnsureMarketSummaryRecommendStocksSavedWithResultLimits(summaryText, providerName, modelName string, startedAt time.Time, verifiedCandidates []MarketSummaryVerifiedCandidateSnapshot, outputLimit, productionLimit int) (*models.MarketSummaryRecommendSaveResult, error) {
	return EnsureMarketSummaryRecommendStocksSavedWithResultOptions(summaryText, providerName, modelName, startedAt, verifiedCandidates, MarketSummaryRecommendSaveOptions{
		NewRecordLimit:  outputLimit,
		ProductionLimit: productionLimit,
	})
}

type MarketSummaryRecommendSaveOptions struct {
	NewRecordLimit        int
	ProductionLimit       int
	RepairableFailures    []models.MarketSummaryTradePlanRepairCandidate
	RequireVerifiedRepair bool
}

type marketSummarySaveSelection struct {
	Draft    *marketSummaryRecommendDraft
	Existing *models.AiRecommendStocks
	Repair   *models.MarketSummaryTradePlanRepairCandidate
}

type marketSummarySaveSelectionStats struct {
	BlockedCount int
	Reasons      []string
}

type marketSummaryPreparedSaveItem struct {
	Selection marketSummarySaveSelection
	Item      *models.AiRecommendStocks
}

func EnsureMarketSummaryRecommendStocksSavedWithResultOptions(summaryText, providerName, modelName string, startedAt time.Time, verifiedCandidates []MarketSummaryVerifiedCandidateSnapshot, options MarketSummaryRecommendSaveOptions) (*models.MarketSummaryRecommendSaveResult, error) {
	if err := requireStrategyProductionLive(nil, db.Dao); err != nil {
		return nil, err
	}
	marketSummaryRecommendSaveMu.Lock()
	defer marketSummaryRecommendSaveMu.Unlock()

	result := &models.MarketSummaryRecommendSaveResult{
		BlockedReasons:              []models.MarketSummaryBlockedReasonItem{},
		ProductionDowngradeReasons:  []models.MarketSummaryBlockedReasonItem{},
		RepairableTradePlanFailures: []models.MarketSummaryTradePlanRepairCandidate{},
		UsedStockCodes:              []string{},
		SavedStockCodes:             []string{},
		UpgradedStockCodes:          []string{},
		RemainingCandidateStocks:    []string{},
	}
	options.NewRecordLimit = normalizeMarketSummaryOutputLimit(options.NewRecordLimit)
	options.ProductionLimit = normalizeMarketSummaryProductionLimit(options.ProductionLimit)
	decisionAt := resolveMarketSummaryDecisionCompletionTime(startedAt)
	drafts := parseMarketSummaryRecommendStockDrafts(summaryText, providerName, modelName, decisionAt)
	applyMarketSummaryDecisionTimeline(drafts, startedAt, decisionAt)
	result.AIOutputCount = len(drafts)
	if len(drafts) == 0 {
		result.BlockedCount = 1
		result.BlockedReasons = []models.MarketSummaryBlockedReasonItem{{Reason: "AI 输出为空", Count: 1}}
		result.RemainingCandidateStocks = remainingMarketSummaryCandidateCodes(verifiedCandidates, nil)
		return result, nil
	}

	startOfDay, endOfDay := marketSummaryDayBounds(decisionAt)
	existing := make([]models.AiRecommendStocks, 0, len(drafts))
	if err := db.Dao.Model(&models.AiRecommendStocks{}).
		Where("data_time >= ? AND data_time < ?", startOfDay, endOfDay).
		Where("(summary_version IN ? OR activation_rule_source IN ?)", marketSummaryKnownVersions(), []string{"market_summary", "market_summary_embedded"}).
		Find(&existing).Error; err != nil {
		return result, err
	}

	selections, selectionStats := selectMarketSummaryRecommendDraftsForSave(drafts, existing, verifiedCandidates, options)
	result.BlockedCount += selectionStats.BlockedCount
	blockedReasons := append([]string(nil), selectionStats.Reasons...)
	if len(selections) == 0 {
		result.BlockedReasons = aggregateMarketSummaryBlockedReasons(blockedReasons)
		result.RemainingCandidateStocks = remainingMarketSummaryCandidateCodes(verifiedCandidates, result.UsedStockCodes)
		return result, nil
	}
	selectedDrafts := make([]*marketSummaryRecommendDraft, 0, len(selections))
	for _, selection := range selections {
		selectedDrafts = append(selectedDrafts, selection.Draft)
	}
	productionLimit, err := resolveMarketSummaryV150ProductionLimit(db.Dao, decisionAt, options.ProductionLimit)
	if err != nil {
		return result, err
	}
	applyMarketSummaryProductionSelectionWithLimit(selectedDrafts, productionLimit)

	items := make([]marketSummaryPreparedSaveItem, 0, len(selections))
	operationFailures := make([]string, 0, len(selections))
	downgradeReasons := make([]string, 0, len(selections))
	repairableFailures := make([]models.MarketSummaryTradePlanRepairCandidate, 0, len(selections))
	for _, selection := range selections {
		item, err := selection.Draft.toRecommendStock()
		if err != nil {
			operationFailures = append(operationFailures, err.Error())
			blockedReasons = append(blockedReasons, err.Error())
			result.BlockedCount++
			continue
		}
		items = append(items, marketSummaryPreparedSaveItem{Selection: selection, Item: item})
	}
	if len(items) == 0 {
		result.BlockedReasons = aggregateMarketSummaryBlockedReasons(blockedReasons)
		result.RemainingCandidateStocks = remainingMarketSummaryCandidateCodes(verifiedCandidates, result.UsedStockCodes)
		if len(operationFailures) == 0 {
			return result, nil
		}
		return result, errors.New(strings.Join(operationFailures, "；"))
	}

	service := NewAiRecommendStocksService()
	for idx, prepared := range items {
		item := prepared.Item
		if prepared.Selection.Existing != nil {
			// A persisted analysis_only decision is terminal for every strategy
			// version. A later model response may never turn it into production.
			reason := marketSummaryAnalysisOnlyIrreversibleReason
			blockedReasons = append(blockedReasons, reason)
			downgradeReasons = append(downgradeReasons, reason)
			result.BlockedCount++
			continue
		}
		if err := service.CreateAiRecommendStocks(item); err != nil {
			failure := fmt.Sprintf("第%d条推荐记录不完整: %v", idx+1, err)
			operationFailures = append(operationFailures, failure)
			blockedReasons = append(blockedReasons, failure)
			result.BlockedCount++
			continue
		}
		result.SavedCount++
		code := normalizeRecommendStockCode(item.StockCode)
		result.SavedStockCodes = append(result.SavedStockCodes, code)
		result.UsedStockCodes = append(result.UsedStockCodes, code)
		if isAnalysisOnlyRecommend(item) {
			result.AnalysisOnlyCount++
			downgradeReason := firstNonEmptyText(item.InvalidCondition, item.ActivationInvalidReason)
			if downgradeReason != "" {
				downgradeReasons = append(downgradeReasons, downgradeReason)
			}
		} else {
			result.ProductionCount++
		}
	}
	result.UsedStockCodes = dedupeNonEmptyStrings(result.UsedStockCodes, 0)
	result.SavedStockCodes = dedupeNonEmptyStrings(result.SavedStockCodes, 0)
	result.UpgradedStockCodes = dedupeNonEmptyStrings(result.UpgradedStockCodes, 0)
	result.RemainingCandidateStocks = remainingMarketSummaryCandidateCodes(verifiedCandidates, result.UsedStockCodes)
	result.BlockedReasons = aggregateMarketSummaryBlockedReasons(blockedReasons)
	result.ProductionDowngradeReasons = aggregateMarketSummaryProductionDowngradeReasons(downgradeReasons)
	result.RepairableTradePlanFailures = repairableFailures
	if len(operationFailures) > 0 {
		return result, errors.New(strings.Join(operationFailures, "；"))
	}
	return result, nil
}

func selectMarketSummaryRecommendDraftsForSave(drafts []*marketSummaryRecommendDraft, existing []models.AiRecommendStocks, verifiedCandidates []MarketSummaryVerifiedCandidateSnapshot, options MarketSummaryRecommendSaveOptions) ([]marketSummarySaveSelection, marketSummarySaveSelectionStats) {
	stats := marketSummarySaveSelectionStats{Reasons: []string{}}
	existingByCode := make(map[string]*models.AiRecommendStocks, len(existing))
	for idx := range existing {
		code := normalizeRecommendStockCode(existing[idx].StockCode)
		if code == "" {
			continue
		}
		existingByCode[code] = &existing[idx]
	}
	repairByCode := make(map[string]*models.MarketSummaryTradePlanRepairCandidate, len(options.RepairableFailures))
	for idx := range options.RepairableFailures {
		code := normalizeRecommendStockCode(options.RepairableFailures[idx].StockCode)
		if code == "" || options.RepairableFailures[idx].RecommendID == 0 {
			continue
		}
		repairByCode[code] = &options.RepairableFailures[idx]
	}
	verifiedCodes := make(map[string]struct{}, len(verifiedCandidates))
	for _, candidate := range verifiedCandidates {
		if code := normalizeRecommendStockCode(candidate.StockCode); code != "" {
			verifiedCodes[code] = struct{}{}
		}
	}

	selections := make([]marketSummarySaveSelection, 0, len(drafts))
	seen := make(map[string]struct{}, len(drafts))
	newRecordCount := 0
	for _, draft := range drafts {
		if draft == nil {
			continue
		}
		if shouldSkipMarketSummaryBackfill(draft) {
			stats.BlockedCount++
			stats.Reasons = append(stats.Reasons, "明确回避项排除")
			continue
		}
		code := normalizeRecommendStockCode(draft.StockCode)
		if code == "" {
			stats.BlockedCount++
			stats.Reasons = append(stats.Reasons, "股票代码为空")
			continue
		}
		if _, ok := seen[code]; ok {
			stats.BlockedCount++
			stats.Reasons = append(stats.Reasons, "本批重复股票排除")
			continue
		}
		seen[code] = struct{}{}
		if existingItem, ok := existingByCode[code]; ok {
			repair := repairByCode[code]
			if reason := validateMarketSummaryRepairSelection(existingItem, repair, verifiedCodes, options.RequireVerifiedRepair); reason != "" {
				stats.BlockedCount++
				stats.Reasons = append(stats.Reasons, reason)
				continue
			}
			selections = append(selections, marketSummarySaveSelection{Draft: draft, Existing: existingItem, Repair: repair})
			continue
		}
		if options.RequireVerifiedRepair {
			stats.BlockedCount++
			stats.Reasons = append(stats.Reasons, "second-round output may repair an authorized record but may not create a new recommendation")
			continue
		}
		if newRecordCount >= options.NewRecordLimit {
			stats.BlockedCount++
			stats.Reasons = append(stats.Reasons, fmt.Sprintf("超出推荐数量上限（最多%d只）", options.NewRecordLimit))
			continue
		}
		newRecordCount++
		selections = append(selections, marketSummarySaveSelection{Draft: draft})
	}
	return selections, stats
}

func validateMarketSummaryRepairSelection(existing *models.AiRecommendStocks, repair *models.MarketSummaryTradePlanRepairCandidate, verifiedCodes map[string]struct{}, requireVerified bool) string {
	if existing == nil || repair == nil {
		return "同日已推荐排除"
	}
	code := normalizeRecommendStockCode(existing.StockCode)
	if existing.ID == 0 || repair.RecommendID != existing.ID || normalizeRecommendStockCode(repair.StockCode) != code {
		return "修正授权与同日记录不匹配"
	}
	if !isMarketSummaryStoredRecommend(existing) {
		return "非市场总结来源记录禁止修正"
	}
	if !isAnalysisOnlyRecommend(existing) {
		return "仅允许修正同日仅分析记录"
	}
	// analysis_only is a terminal decision. Keep the remaining checks below
	// unreachable by design so neither an authorization token nor a verified
	// snapshot can reopen it.
	return marketSummaryAnalysisOnlyIrreversibleReason
	/*
		if requireVerified {
			if _, ok := verifiedCodes[code]; !ok {
				return "缺少对应 verified 快照，禁止修正"
			}
		}
		return ""
	*/
}

func isMarketSummaryStoredRecommend(item *models.AiRecommendStocks) bool {
	if item == nil {
		return false
	}
	if isMarketSummaryActivationSource(item.ActivationRuleSource) {
		return true
	}
	version := strings.TrimSpace(item.SummaryVersion)
	for _, known := range marketSummaryKnownVersions() {
		if version == known {
			return true
		}
	}
	return false
}

func upgradeMarketSummaryAnalysisOnlyRecommend(recommendID uint, repair *models.MarketSummaryTradePlanRepairCandidate, item *models.AiRecommendStocks, startedAt time.Time) error {
	// Deliberately fail before inspecting or writing the database. This is a
	// cross-version invariant, including legacy records whose SummaryVersion is
	// empty or spoofed by a later model response.
	_ = recommendID
	_ = repair
	_ = item
	_ = startedAt
	return errors.New(marketSummaryAnalysisOnlyIrreversibleReason)
	/*
		if repair.RecommendID != recommendID {
			return errors.New("修正授权记录ID不匹配")
		}
		code := normalizeRecommendStockCode(item.StockCode)
		if code == "" || normalizeRecommendStockCode(repair.StockCode) != code {
			return errors.New("修正授权股票代码不匹配")
		}
		startOfDay, endOfDay := marketSummaryDayBounds(startedAt)
		err := db.Dao.Transaction(func(tx *gorm.DB) error {
			var current models.AiRecommendStocks
			if err := tx.Where("id = ?", recommendID).First(&current).Error; err != nil {
				return err
			}
			if strings.TrimSpace(current.SummaryVersion) == marketSummaryVersion150 && isAnalysisOnlyRecommend(&current) {
				return errors.New("v1.5 analysis_only records are irreversible")
			}
			if current.DataTime == nil || current.DataTime.Before(startOfDay) || !current.DataTime.Before(endOfDay) {
				return errors.New("修正目标不属于本次交易日")
			}
			if normalizeRecommendStockCode(current.StockCode) != code {
				return errors.New("修正目标股票代码不匹配")
			}
			if !isMarketSummaryStoredRecommend(&current) {
				return errors.New("修正目标不是市场总结来源记录")
			}
			if !isAnalysisOnlyRecommend(&current) {
				return errors.New("修正目标已不是仅分析记录")
			}

			candidate, err := buildMarketSummaryRepairUpgradeCandidate(current, item, repair)
			if err != nil {
				return err
			}
			updates := map[string]any{
				"recommend_buy_price":             candidate.RecommendBuyPrice,
				"recommend_buy_price_min":         candidate.RecommendBuyPriceMin,
				"recommend_buy_price_max":         candidate.RecommendBuyPriceMax,
				"recommend_stop_profit_price":     candidate.RecommendStopProfitPrice,
				"recommend_stop_profit_price_min": candidate.RecommendStopProfitPriceMin,
				"recommend_stop_profit_price_max": candidate.RecommendStopProfitPriceMax,
				"recommend_stop_loss_price":       candidate.RecommendStopLossPrice,
				"recommend_category":              candidate.RecommendCategory,
				"execution_state":                 candidate.ExecutionState,
				"buy_signal":                      candidate.BuySignal,
				"buy_signal_detail":               candidate.BuySignalDetail,
				"sell_signal":                     candidate.SellSignal,
				"sell_signal_detail":              candidate.SellSignalDetail,
				"invalid_signal":                  candidate.InvalidSignal,
				"invalid_condition":               candidate.InvalidCondition,
				"activation_rule_json":            candidate.ActivationRuleJSON,
				"activation_rule_version":         candidate.ActivationRuleVersion,
				"activation_rule_source":          candidate.ActivationRuleSource,
				"activation_status":               candidate.ActivationStatus,
				"activation_invalid_reason":       candidate.ActivationInvalidReason,
				"recommend_status":                candidate.RecommendStatus,
			}
			update := tx.Model(&models.AiRecommendStocks{}).
				Where("id = ? AND data_time >= ? AND data_time < ?", recommendID, startOfDay, endOfDay).
				Updates(updates)
			if update.Error != nil {
				return update.Error
			}
			if update.RowsAffected != 1 {
				return fmt.Errorf("修正升级影响记录数异常: %d", update.RowsAffected)
			}
			return nil
		})
		if err != nil {
			return err
		}
		_ = markAiRecommendYieldDirtyCodesForMutationFn([]string{code}, "市场总结修正升级后等待严格模式下载/回算", aiRecommendYieldModeStrict)
		requestAiRecommendYieldScopedRecalcForMutationFn(false, "market_summary_recommend_upgraded", []string{code})
		return nil
	*/
}

func buildMarketSummaryRepairUpgradeCandidate(current models.AiRecommendStocks, item *models.AiRecommendStocks, repair *models.MarketSummaryTradePlanRepairCandidate) (*models.AiRecommendStocks, error) {
	if item == nil || repair == nil {
		return nil, errors.New("修正内容为空")
	}
	candidate := current
	candidate.RecommendBuyPrice = strings.TrimSpace(item.RecommendBuyPrice)
	candidate.RecommendBuyPriceMin = item.RecommendBuyPriceMin
	candidate.RecommendBuyPriceMax = item.RecommendBuyPriceMax
	candidate.RecommendStopProfitPrice = strings.TrimSpace(item.RecommendStopProfitPrice)
	candidate.RecommendStopProfitPriceMin = item.RecommendStopProfitPriceMin
	candidate.RecommendStopProfitPriceMax = item.RecommendStopProfitPriceMax
	candidate.RecommendStopLossPrice = strings.TrimSpace(item.RecommendStopLossPrice)
	candidate.BuySignal = strings.TrimSpace(item.BuySignal)
	candidate.BuySignalDetail = strings.TrimSpace(item.BuySignalDetail)
	candidate.SellSignal = strings.TrimSpace(item.SellSignal)
	candidate.SellSignalDetail = strings.TrimSpace(item.SellSignalDetail)
	candidate.InvalidSignal = firstNonEmptyText(item.InvalidSignal, item.InvalidCondition)
	candidate.InvalidCondition = strings.TrimSpace(item.InvalidCondition)
	candidate.ExecutionState = recommendExecutionConditional
	candidate.RecommendCategory = recommendExecutionConditional
	candidate.RecommendStatus = "valid"
	candidate.ActivationStatus = ""
	candidate.ActivationInvalidReason = ""
	candidate.ActivationRuleJSON = ""
	candidate.ActivationRuleVersion = ""
	candidate.ActivationRuleSource = "market_summary"
	if err := normalizeAiRecommendStockForSave(&candidate); err != nil {
		return nil, err
	}
	if isAnalysisOnlyRecommend(&candidate) {
		return nil, errors.New("修正后仍为仅分析状态")
	}
	if isPendingMarketDataRecommend(&candidate) || normalizeRecommendStatus(candidate.RecommendStatus) == recommendStatusPendingMarketData || strings.EqualFold(strings.TrimSpace(candidate.ActivationStatus), recommendActivationPendingData) {
		return nil, errors.New("修正后仍等待行情数据，禁止升级生产候选")
	}
	if normalizeRecommendExecutionState(candidate.ExecutionState) != recommendExecutionConditional {
		return nil, errors.New("修正后执行状态不是条件触发")
	}
	if !isMarketSummaryActivationSource(candidate.ActivationRuleSource) || strings.TrimSpace(candidate.ActivationRuleJSON) == "" {
		return nil, errors.New("修正后缺少后端生成的市场总结激活规则")
	}
	if err := validateMarketSummaryRepairBounds(repair, &candidate); err != nil {
		return nil, err
	}
	draft := marketSummaryRecommendDraftFromStoredRecommend(candidate)
	if reason := marketSummaryDraftProductionRejectionReason(draft); reason != "" {
		return nil, errors.New(reason)
	}
	return &candidate, nil
}

func validateMarketSummaryRepairBounds(repair *models.MarketSummaryTradePlanRepairCandidate, candidate *models.AiRecommendStocks) error {
	if repair == nil || candidate == nil {
		return errors.New("修正边界校验参数为空")
	}
	_, originalBuyMin, originalBuyMax := normalizePriceRangeText(repair.OriginalBuyPrice, 0, 0)
	if originalBuyMin <= 0 || originalBuyMax <= 0 {
		return errors.New("缺少原始买入区间，禁止修正升级")
	}
	if candidate.RecommendBuyPriceMin+0.01 < originalBuyMin || candidate.RecommendBuyPriceMax-0.01 > originalBuyMax {
		return errors.New("修正买入区间只能在原区间内收窄")
	}
	_, originalProfitMin, _ := normalizePriceRangeText(repair.OriginalStopProfitPrice, 0, 0)
	if originalProfitMin <= 0 || candidate.RecommendStopProfitPriceMin+0.01 < originalProfitMin {
		return errors.New("修正止盈不得低于原止盈")
	}
	_, originalStopLoss := normalizeSinglePriceText(repair.OriginalStopLossPrice)
	_, repairedStopLoss := normalizeSinglePriceText(candidate.RecommendStopLossPrice)
	if originalStopLoss <= 0 || repairedStopLoss <= 0 {
		return errors.New("缺少有效止损，禁止修正升级")
	}
	if math.Abs(repairedStopLoss-originalStopLoss)/originalStopLoss > 0.03 {
		return errors.New("修正止损偏离原止损超过3%，不属于有限微调")
	}
	return nil
}

func marketSummaryRecommendDraftFromStoredRecommend(item models.AiRecommendStocks) *marketSummaryRecommendDraft {
	return &marketSummaryRecommendDraft{
		StockCode:                   item.StockCode,
		StockName:                   item.StockName,
		BkName:                      item.BkName,
		StockPrice:                  item.StockPrice,
		StockCurrentPrice:           item.StockCurrentPrice,
		ObservePrice:                item.ObservePrice,
		RecommendBuyPrice:           item.RecommendBuyPrice,
		RecommendBuyPriceMin:        item.RecommendBuyPriceMin,
		RecommendBuyPriceMax:        item.RecommendBuyPriceMax,
		RecommendStopProfitPrice:    item.RecommendStopProfitPrice,
		RecommendStopProfitPriceMin: item.RecommendStopProfitPriceMin,
		RecommendStopProfitPriceMax: item.RecommendStopProfitPriceMax,
		RecommendStopLossPrice:      item.RecommendStopLossPrice,
		RecommendCategory:           item.RecommendCategory,
		ExecutionState:              item.ExecutionState,
		BuySignal:                   item.BuySignal,
		BuySignalDetail:             item.BuySignalDetail,
		InvalidSignal:               item.InvalidSignal,
		InvalidCondition:            item.InvalidCondition,
		EventStrength:               item.EventStrength,
		CapitalConfirmation:         item.CapitalConfirmation,
		FundamentalFit:              item.FundamentalFit,
		TechnicalFit:                item.TechnicalFit,
		ActivationRuleJSON:          item.ActivationRuleJSON,
		ActivationRuleVersion:       item.ActivationRuleVersion,
		ActivationRuleSource:        item.ActivationRuleSource,
		ActivationInvalidReason:     item.ActivationInvalidReason,
		RecommendStatus:             item.RecommendStatus,
		SummaryVersion:              item.SummaryVersion,
		Remarks:                     item.Remarks,
	}
}

func applyMarketSummaryFeasiblePlanFallback(drafts []*marketSummaryRecommendDraft, verifiedCandidates []MarketSummaryVerifiedCandidateSnapshot) {
	// Intentionally disabled for every strategy version. Once the model/backend classifies a
	// draft as analysis_only, verified feasiblePlans are diagnostic input only
	// and must never manufacture a production trade plan.
	_ = drafts
	_ = verifiedCandidates
}

func marketSummaryDraftNeedsFeasiblePlanFallback(draft *marketSummaryRecommendDraft) bool {
	if draft == nil {
		return false
	}
	if normalizeRecommendExecutionState(draft.ExecutionState) == recommendExecutionAnalysisOnly {
		return true
	}
	if draft.RecommendBuyPriceMin <= 0 || draft.RecommendBuyPriceMax <= 0 {
		return true
	}
	if strings.TrimSpace(draft.RecommendStopProfitPrice) == "" || strings.TrimSpace(draft.RecommendStopLossPrice) == "" {
		return true
	}
	if strings.TrimSpace(draft.ActivationRuleJSON) == "" {
		return true
	}
	return false
}

func selectBestMarketSummaryFeasiblePlan(candidate MarketSummaryVerifiedCandidateSnapshot) (marketSummaryFeasiblePlan, bool) {
	var best marketSummaryFeasiblePlan
	found := false
	for _, plan := range candidate.FeasiblePlans {
		if !plan.PassHardGate {
			continue
		}
		if !found ||
			plan.RewardRisk > best.RewardRisk ||
			(plan.RewardRisk == best.RewardRisk && plan.DownsidePct < best.DownsidePct) ||
			(plan.RewardRisk == best.RewardRisk && plan.DownsidePct == best.DownsidePct && strings.EqualFold(plan.Path, "pullback") && !strings.EqualFold(best.Path, "pullback")) {
			best = plan
			found = true
		}
	}
	return best, found
}

func applyFeasiblePlanToMarketSummaryDraft(draft *marketSummaryRecommendDraft, candidate MarketSummaryVerifiedCandidateSnapshot, plan marketSummaryFeasiblePlan) {
	// Retained as a compatibility stub for old callers/tests. Feasible plans are
	// diagnostic only and may never mutate a draft into a production candidate.
	_ = draft
	_ = candidate
	_ = plan
	return
	/*
		if draft == nil {
			return
		}
		draft.RepairSource = nil
		entryText := strings.TrimSpace(plan.EntryRange)
		buyText, buyMin, buyMax := parseMarketSummaryBuyRange(entryText)
		if buyMin <= 0 || buyMax <= 0 {
			buyMin = plan.WorstEntry
			buyMax = plan.WorstEntry
			buyText = formatMarketSummaryPlanPrice(plan.WorstEntry)
		}
		stopProfitText := formatMarketSummaryPlanPrice(plan.TakeProfit)
		stopLossText := formatMarketSummaryPlanPrice(plan.StopLoss)
		anchor := firstNonEmptyText(candidate.AuctionPrice, candidate.MinutePrice, candidate.CurrentPrice, draft.StockCurrentPrice, draft.ObservePrice, draft.StockPrice)
		if anchor != "" {
			draft.StockPrice = anchor
			draft.StockCurrentPrice = anchor
			draft.StockClosePrice = anchor
			draft.ObservePrice = anchor
		}
		draft.RecommendBuyPrice = firstNonEmptyText(buyText, entryText)
		draft.RecommendBuyPriceMin = buyMin
		draft.RecommendBuyPriceMax = buyMax
		draft.RecommendStopProfitPrice = stopProfitText
		draft.RecommendStopProfitPriceMin = plan.TakeProfit
		draft.RecommendStopProfitPriceMax = plan.TakeProfit
		draft.RecommendStopLossPrice = stopLossText
		draft.FocusPrice = draft.RecommendBuyPrice
		draft.ExecutionState = recommendExecutionConditional
		draft.RecommendCategory = ""
		draft.RecommendStatus = "valid"
		draft.ActivationRuleSource = "market_summary"
		draft.ActivationRuleVersion = activationRuleVersionV3
		draft.BuySignal = fmt.Sprintf("价格触发：%s路径进入%s；量能触发：5分钟成交额不低于近5个5分钟平均成交额的1.10倍", strings.TrimSpace(plan.Path), draft.RecommendBuyPrice)
		draft.BuySignalDetail = fmt.Sprintf("hardGateFallback=feasiblePlans; worstEntry=%.2f; rewardRisk=%.2f; downsidePct=%.2f", plan.WorstEntry, plan.RewardRisk, plan.DownsidePct)
		draft.InvalidSignal = fmt.Sprintf("时间失效：未来5个交易日内未触发；价格失效：跌破止损位%s", stopLossText)
		draft.InvalidCondition = ""
		draft.ActivationInvalidReason = ""
		draft.Remarks = appendRecommendRemarkNotice(draft.Remarks, fmt.Sprintf("feasiblePlans兜底补齐交易字段：path=%s; entry=%s; takeProfit=%.2f; stopLoss=%.2f; rewardRisk=%.2f; downsidePct=%.2f", plan.Path, draft.RecommendBuyPrice, plan.TakeProfit, plan.StopLoss, plan.RewardRisk, plan.DownsidePct))
		if ruleJSON, version, err := buildActivationRuleFromMarketSummaryFeasiblePlan(draft, plan); err == nil {
			draft.ActivationRuleJSON = ruleJSON
			if version != "" {
				draft.ActivationRuleVersion = version
			}
		} else {
			draft.ActivationRuleJSON = ""
			draft.ActivationInvalidReason = err.Error()
		}
		if reason := marketSummaryDraftProductionRejectionReason(draft); reason != "" {
			downgradeMarketSummaryDraftToAnalysisOnlyWithRepairSource(draft, reason)
		}
	*/
}

func buildActivationRuleFromMarketSummaryFeasiblePlan(draft *marketSummaryRecommendDraft, plan marketSummaryFeasiblePlan) (string, string, error) {
	if draft == nil {
		return "", "", errors.New("market summary draft is nil")
	}
	signalType := "price_range_with_volume"
	if strings.EqualFold(plan.Path, "breakout") {
		signalType = "price_breakout_with_volume"
	}
	path := activationRule{
		Name:             strings.TrimSpace(plan.Path),
		SignalType:       signalType,
		EvaluationWindow: "5m",
		Baseline:         "avg_amount_5x5m",
		Operator:         ">=",
		ThresholdValue:   round2(draft.RecommendBuyPriceMin),
		ThresholdMax:     round2(draft.RecommendBuyPriceMax),
		VolumeRatio:      1.10,
		ConfirmBars:      1,
		VolumeWindow:     5,
		VolumeMetric:     "amount",
		ExpireTradeDays:  recommendPendingActivationMaxTradeDays,
	}
	if path.Name == "" {
		path.Name = "pullback"
	}
	preview := draft.toPreviewRecommend()
	if preview != nil {
		path.OpeningPolicy = buildDefaultActivationOpeningPolicy(preview, draft.RecommendBuyPriceMin, draft.RecommendBuyPriceMax)
	}
	rule := activationRule{
		Version: activationRuleVersionV3,
		Mode:    activationRuleModeAnyOf,
		Paths:   []activationRule{path},
	}
	raw, err := json.Marshal(rule)
	if err != nil {
		return "", "", err
	}
	normalized, version, err := normalizeMarketSummaryActivationRuleForProduction(string(raw))
	if err != nil {
		return "", "", err
	}
	return normalized, version, nil
}

func formatMarketSummaryPlanPrice(value float64) string {
	if value <= 0 {
		return ""
	}
	return strconv.FormatFloat(round2(value), 'f', 2, 64)
}

func EnsureMarketSummaryYieldOverridesSaved(summaryText string, startedAt time.Time) (int, error) {
	if err := requireStrategyProductionLive(nil, db.Dao); err != nil {
		return 0, err
	}
	drafts, err := parseMarketSummaryYieldOverrideDrafts(summaryText, startedAt)
	if err != nil {
		return 0, err
	}
	if len(drafts) == 0 {
		return 0, nil
	}

	saved := 0
	for _, draft := range drafts {
		if draft == nil || draft.Override == nil {
			continue
		}
		if err := upsertAiRecommendYieldOverride(draft.Override); err != nil {
			return saved, err
		}
		saved++
	}
	return saved, nil
}

func collectMarketSummaryDraftCodes(items []*marketSummaryRecommendDraft) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		result = append(result, normalizeRecommendStockCode(item.StockCode))
	}
	return dedupeNonEmptyStrings(result, 0)
}

func remainingMarketSummaryCandidateCodes(candidates []marketSummaryVerifiedCandidate, usedCodes []string) []string {
	used := make(map[string]struct{}, len(usedCodes))
	for _, code := range usedCodes {
		code = normalizeRecommendStockCode(code)
		if code != "" {
			used[code] = struct{}{}
		}
	}
	result := make([]string, 0, len(candidates))
	for _, item := range candidates {
		code := normalizeRecommendStockCode(item.StockCode)
		if code == "" {
			continue
		}
		if _, ok := used[code]; ok {
			continue
		}
		result = append(result, code)
	}
	return dedupeNonEmptyStrings(result, 0)
}

func aggregateMarketSummaryBlockedReasons(rawReasons []string) []models.MarketSummaryBlockedReasonItem {
	if len(rawReasons) == 0 {
		return []models.MarketSummaryBlockedReasonItem{}
	}
	counts := map[string]int{}
	for _, raw := range rawReasons {
		reason := normalizeMarketSummaryBlockedReason(raw)
		if reason == "" {
			continue
		}
		counts[reason]++
	}
	items := make([]models.MarketSummaryBlockedReasonItem, 0, len(counts))
	for reason, count := range counts {
		items = append(items, models.MarketSummaryBlockedReasonItem{Reason: reason, Count: count})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Count != items[j].Count {
			return items[i].Count > items[j].Count
		}
		return items[i].Reason < items[j].Reason
	})
	if len(items) > 5 {
		items = items[:5]
	}
	return items
}

func aggregateMarketSummaryProductionDowngradeReasons(rawReasons []string) []models.MarketSummaryBlockedReasonItem {
	if len(rawReasons) == 0 {
		return []models.MarketSummaryBlockedReasonItem{}
	}
	counts := map[string]int{}
	for _, raw := range rawReasons {
		reason := normalizeMarketSummaryProductionDowngradeReason(raw)
		if reason == "" {
			continue
		}
		counts[reason]++
	}
	items := make([]models.MarketSummaryBlockedReasonItem, 0, len(counts))
	for reason, count := range counts {
		items = append(items, models.MarketSummaryBlockedReasonItem{Reason: reason, Count: count})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Count != items[j].Count {
			return items[i].Count > items[j].Count
		}
		return items[i].Reason < items[j].Reason
	})
	if len(items) > 5 {
		items = items[:5]
	}
	return items
}

func marketSummaryDraftProductionDowngradeReason(item *marketSummaryRecommendDraft) string {
	return marketSummaryDraftProductionRejectionReason(item)
}

func buildMarketSummaryTradePlanRepairCandidate(item *marketSummaryRecommendDraft, reason string) (models.MarketSummaryTradePlanRepairCandidate, bool) {
	if item == nil {
		return models.MarketSummaryTradePlanRepairCandidate{}, false
	}
	source := item
	if item.RepairSource != nil {
		source = item.RepairSource
	}
	rec := source.toPreviewRecommend()
	if rec == nil {
		return models.MarketSummaryTradePlanRepairCandidate{}, false
	}
	stopProfit, profitOK := parseStopProfitPrice(*rec)
	stopLoss, lossOK := parseStopLossPrice(*rec)
	worstEntry := resolveMarketSummaryWorstEntryPriceForGate(source)
	if !profitOK || !lossOK || stopProfit <= 0 || stopLoss <= 0 || worstEntry <= 0 || stopLoss >= worstEntry {
		return models.MarketSummaryTradePlanRepairCandidate{}, false
	}
	downside := worstEntry - stopLoss
	if downside <= 0 {
		return models.MarketSummaryTradePlanRepairCandidate{}, false
	}
	rewardRisk := round2((stopProfit - worstEntry) / downside)
	downsidePct := round2(downside / worstEntry * 100)
	repairable := rewardRisk >= 0.50 && rewardRisk < marketSummaryFeasibleRewardRisk
	if !repairable || rewardRisk < 0.50 {
		return models.MarketSummaryTradePlanRepairCandidate{}, false
	}
	return models.MarketSummaryTradePlanRepairCandidate{
		StockCode:                  normalizeRecommendStockCode(source.StockCode),
		StockName:                  strings.TrimSpace(source.StockName),
		Reason:                     strings.TrimSpace(reason),
		RewardRisk:                 rewardRisk,
		DownsidePct:                downsidePct,
		OriginalBuyPrice:           strings.TrimSpace(source.RecommendBuyPrice),
		OriginalStopProfitPrice:    strings.TrimSpace(source.RecommendStopProfitPrice),
		OriginalStopLossPrice:      strings.TrimSpace(source.RecommendStopLossPrice),
		OriginalBuySignal:          strings.TrimSpace(source.BuySignal),
		OriginalInvalidCondition:   strings.TrimSpace(source.InvalidCondition),
		OriginalActivationRuleJSON: strings.TrimSpace(source.ActivationRuleJSON),
		RepairHint:                 "仅允许收窄买入区间、上移止盈或微调止损；修正后仍必须满足 rewardRisk>=0.80；downsidePct 仅用于诊断。",
	}, true
}

func marketSummaryDayBounds(at time.Time) (time.Time, time.Time) {
	loc := at.Location()
	if loc == nil {
		loc = time.Local
	}
	start := time.Date(at.In(loc).Year(), at.In(loc).Month(), at.In(loc).Day(), 0, 0, 0, 0, loc)
	return start, start.Add(24 * time.Hour)
}

func PrepareMarketSummaryReportForPersistence(summaryText string, startedAt time.Time) (string, marketSummaryReportPrepareStats, error) {
	return PrepareMarketSummaryReportForPersistenceWithLimit(summaryText, startedAt, marketSummaryRecommendationOutputLimit)
}

func PrepareMarketSummaryReportForPersistenceWithLimit(summaryText string, startedAt time.Time, outputLimit int) (string, marketSummaryReportPrepareStats, error) {
	stats := marketSummaryReportPrepareStats{}
	outputLimit = normalizeMarketSummaryOutputLimit(outputLimit)
	lines := strings.Split(summaryText, "\n")
	if len(lines) == 0 {
		return strings.TrimSpace(summaryText), stats, nil
	}

	existingCodes, err := loadMarketSummaryExistingRecommendCodeSet(startedAt)
	if err != nil {
		return "", stats, err
	}

	out := make([]string, 0, len(lines))
	replaced := false
	for idx := 0; idx < len(lines); {
		line := strings.TrimSpace(lines[idx])
		if !strings.HasPrefix(line, "|") {
			out = append(out, lines[idx])
			idx++
			continue
		}
		blockStart := idx
		for idx < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[idx]), "|") {
			idx++
		}
		block := lines[blockStart:idx]
		rows := extractMarkdownRowsFromTableBlock(block)
		if replaced || len(rows) == 0 {
			out = append(out, block...)
			continue
		}
		sanitizedRows, rowStats := sanitizeMarketSummaryRowsForPersistence(rows, existingCodes, startedAt)
		stats.RowsSeen += rowStats.RowsSeen
		stats.DuplicateRowsOmit += rowStats.DuplicateRowsOmit
		stats.AnalysisOnlyRows += rowStats.AnalysisOnlyRows
		if len(sanitizedRows) > outputLimit {
			stats.OutputRowsOmit += len(sanitizedRows) - outputLimit
			sanitizedRows = sanitizedRows[:outputLimit]
		}
		stats.RecommendationRows = len(sanitizedRows)
		out = append(out, buildPreparedMarketSummaryTableLines(sanitizedRows)...)
		replaced = true
	}
	return strings.TrimSpace(strings.Join(out, "\n")), stats, nil
}

func loadMarketSummaryExistingRecommendCodeSet(startedAt time.Time) (map[string]struct{}, error) {
	startOfDay, endOfDay := marketSummaryDayBounds(startedAt)
	rows := make([]models.AiRecommendStocks, 0, 16)
	if err := db.Dao.Model(&models.AiRecommendStocks{}).
		Where("data_time >= ? AND data_time < ?", startOfDay, endOfDay).
		Where("(summary_version IN ? OR activation_rule_source IN ?)", marketSummaryKnownVersions(), []string{"market_summary", "market_summary_embedded"}).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		code := normalizeRecommendStockCode(row.StockCode)
		if code == "" {
			continue
		}
		result[code] = struct{}{}
	}
	return result, nil
}

func sanitizeMarketSummaryRowsForPersistence(rows []marketSummaryRow, existingCodes map[string]struct{}, startedAt time.Time) ([]marketSummaryRow, marketSummaryReportPrepareStats) {
	stats := marketSummaryReportPrepareStats{}
	if len(rows) == 0 {
		return nil, stats
	}
	seen := make(map[string]struct{}, len(rows))
	out := make([]marketSummaryRow, 0, len(rows))
	for _, row := range rows {
		stats.RowsSeen++
		stockName, stockCode := parseMarketSummaryStockCell(row.stockCell)
		if stockName == "" {
			continue
		}
		resolvedCode, resolvedName := resolveMarketSummaryStockIdentity(stockName, stockCode)
		if resolvedName != "" {
			stockName = resolvedName
		}
		if resolvedCode != "" {
			stockCode = resolvedCode
		}
		stockCode = normalizeRecommendStockCode(stockCode)
		if stockCode != "" {
			if _, ok := existingCodes[stockCode]; ok {
				stats.DuplicateRowsOmit++
				continue
			}
			if _, ok := seen[stockCode]; ok {
				stats.DuplicateRowsOmit++
				continue
			}
			seen[stockCode] = struct{}{}
		}
		sanitized, analysisOnly := sanitizeSingleMarketSummaryRow(row, stockName, stockCode, startedAt)
		if analysisOnly {
			stats.AnalysisOnlyRows++
		}
		out = append(out, sanitized)
	}
	return out, stats
}

func sanitizeSingleMarketSummaryRow(row marketSummaryRow, stockName, stockCode string, startedAt time.Time) (marketSummaryRow, bool) {
	sanitized := row
	if stockName != "" {
		if stockCode != "" {
			sanitized.stockCell = fmt.Sprintf("%s(%s)", stockName, stockCode)
		} else {
			sanitized.stockCell = stockName
		}
	}
	sanitized.remarks = HumanizeRecommendRemarks(sanitized.remarks)

	rec := buildMarketSummaryPriceCheckRecommend(row, stockName, stockCode, startedAt)
	if rec == nil {
		return sanitized, false
	}
	snapshot, ok := loadMarketSummaryReferenceSnapshot(*rec)
	if !ok || snapshot.Price <= 0 || snapshot.Amount <= 0 {
		return rewriteMarketSummaryRowAsAnalysisOnly(sanitized, marketSummaryReferenceSnapshot{}, marketSummaryAnalysisOnlySkipReason), true
	}
	if reason, bad := detectMarketSummaryPriceMismatch(*rec); bad {
		return rewriteMarketSummaryRowAsAnalysisOnly(sanitized, snapshot, reason+"，本次仅保留逻辑分析"), true
	}
	return sanitized, false
}

func buildMarketSummaryPriceCheckRecommend(row marketSummaryRow, stockName, stockCode string, startedAt time.Time) *models.AiRecommendStocks {
	if strings.TrimSpace(stockCode) == "" {
		return nil
	}
	observePrice := firstNumericValue(row.latestPrice)
	focusText, focusMin, focusMax := parseMarketSummaryBuyRange(firstNonEmptyText(
		row.focusPrice,
		extractSignalPriceRange(row.buySignal),
		extractSignalPriceRange(row.buySignalDetail),
		observePrice,
	))
	stopProfitText, stopProfitMin, stopProfitMax := parseMarketSummaryNumericRange(row.stopProfit)
	stopLossText := firstNumericValue(row.stopLoss)
	executionState := normalizeRecommendExecutionState(row.executionState)
	if executionState == "" {
		executionState = recommendExecutionConditional
	}
	return &models.AiRecommendStocks{
		DataTime:                    &startedAt,
		StockCode:                   stockCode,
		StockName:                   strings.TrimSpace(stockName),
		StockPrice:                  observePrice,
		StockCurrentPrice:           observePrice,
		StockCurrentPriceTime:       startedAt.Format(time.DateTime),
		StockClosePrice:             observePrice,
		RecommendBuyPrice:           focusText,
		RecommendBuyPriceMin:        focusMin,
		RecommendBuyPriceMax:        focusMax,
		RecommendStopProfitPrice:    stopProfitText,
		RecommendStopProfitPriceMin: stopProfitMin,
		RecommendStopProfitPriceMax: stopProfitMax,
		RecommendStopLossPrice:      stopLossText,
		ExecutionState:              executionState,
		BuySignal:                   normalizeRecommendText(firstNonEmptyText(row.buySignal, row.buySignalDetail)),
		BuySignalDetail:             normalizeRecommendText(row.buySignalDetail),
		SellSignal:                  normalizeRecommendText(row.sellSignal),
		SellSignalDetail:            normalizeRecommendText(row.sellSignalDetail),
		InvalidSignal:               normalizeRecommendText(firstNonEmptyText(row.invalid, row.invalidSignal)),
		InvalidCondition:            normalizeRecommendText(firstNonEmptyText(row.invalid, row.invalidSignal)),
		ObservePrice:                observePrice,
		FocusPrice:                  focusText,
		ExpectedCycle:               strings.TrimSpace(row.expectedCycle),
		ActivationRuleSource:        "market_summary",
		SummaryVersion:              marketSummaryCurrentVersion,
	}
}

func rewriteMarketSummaryRowAsAnalysisOnly(row marketSummaryRow, snapshot marketSummaryReferenceSnapshot, reason string) marketSummaryRow {
	row.executionState = "仅分析"
	if snapshot.Price > 0 {
		row.latestPrice = formatRecommendPrice(snapshot.Price)
	} else {
		row.latestPrice = "数据缺失"
	}
	row.focusPrice = "-"
	row.stopProfit = "-"
	row.stopLoss = "-"
	row.buySignal = "缺少真实价格/量能数据，本次仅保留逻辑分析"
	row.buySignalDetail = ""
	row.sellSignal = "-"
	row.sellSignalDetail = ""
	row.invalidSignal = marketSummaryAnalysisOnlySkipReason
	row.invalid = marketSummaryAnalysisOnlySkipReason
	reason = normalizeRecommendText(reason)
	if reason == "" {
		reason = marketSummaryAnalysisOnlySkipReason
	}
	remarkParts := []string{}
	if text := HumanizeRecommendRemarks(row.remarks); text != "" {
		remarkParts = append(remarkParts, text)
	}
	if !strings.Contains(strings.Join(remarkParts, "\n"), reason) {
		remarkParts = append(remarkParts, reason)
	}
	row.remarks = strings.Join(remarkParts, "\n")
	return row
}

func buildPreparedMarketSummaryTableLines(rows []marketSummaryRow) []string {
	headers := []string{
		"| 股票（代码） | 所属方向 | 核心催化 | 关键证据 | 价格锚点 | 买入区间 | 止盈区间 | 止损位 | 买入依据 | 失效条件 | 风险点 | 预期周期 | 事件强度 | 资金确认度 | 基本面匹配度 | 技术面匹配度 | 操作备注 |",
		"| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |",
	}
	lines := append([]string{}, headers...)
	if len(rows) == 0 {
		lines = append(lines, "| 暂无新增高质量候选标的 | - | - | - | - | - | - | - | - | - | - | - | - | - | - | - | 同日已无新增正式推荐 |")
		return lines
	}
	for _, row := range rows {
		line := []string{
			renderPreparedMarketSummaryCell(row.stockCell),
			renderPreparedMarketSummaryCell(row.direction),
			renderPreparedMarketSummaryCell(row.catalyst),
			renderPreparedMarketSummaryCell(row.evidence),
			renderPreparedMarketSummaryCell(row.latestPrice),
			renderPreparedMarketSummaryCell(row.focusPrice),
			renderPreparedMarketSummaryCell(row.stopProfit),
			renderPreparedMarketSummaryCell(row.stopLoss),
			renderPreparedMarketSummaryCell(row.buySignal),
			renderPreparedMarketSummaryCell(firstNonEmptyText(row.invalid, row.invalidSignal)),
			renderPreparedMarketSummaryCell(row.risk),
			renderPreparedMarketSummaryCell(row.expectedCycle),
			renderPreparedMarketSummaryCell(row.eventStrength),
			renderPreparedMarketSummaryCell(row.capitalConfirmation),
			renderPreparedMarketSummaryCell(row.fundamentalFit),
			renderPreparedMarketSummaryCell(row.technicalFit),
			renderPreparedMarketSummaryCell(row.remarks),
		}
		lines = append(lines, "| "+strings.Join(line, " | ")+" |")
	}
	return lines
}

func renderPreparedMarketSummaryCell(text string) string {
	text = HumanizeRecommendRemarks(normalizeRecommendText(text))
	if text == "" {
		return "-"
	}
	return strings.ReplaceAll(text, "\n", "<br>")
}

func collectMarketSummaryRecommendStocksForSave(parsed []*marketSummaryRecommendDraft, existing []models.AiRecommendStocks) []*marketSummaryRecommendDraft {
	return collectMarketSummaryRecommendStocksForSaveWithLimits(parsed, existing, marketSummaryRecommendationOutputLimit, marketSummaryMaxProductionCandidates)
}

func collectMarketSummaryRecommendStocksForSaveWithProductionLimit(parsed []*marketSummaryRecommendDraft, existing []models.AiRecommendStocks, productionLimit int) []*marketSummaryRecommendDraft {
	return collectMarketSummaryRecommendStocksForSaveWithLimits(parsed, existing, marketSummaryRecommendationOutputLimit, productionLimit)
}

func collectMarketSummaryRecommendStocksForSaveWithLimits(parsed []*marketSummaryRecommendDraft, existing []models.AiRecommendStocks, outputLimit, productionLimit int) []*marketSummaryRecommendDraft {
	existingCodes := make(map[string]struct{}, len(existing))
	for _, item := range existing {
		code := strings.ToUpper(strings.TrimSpace(item.StockCode))
		if code == "" {
			continue
		}
		existingCodes[code] = struct{}{}
	}

	missing := make([]*marketSummaryRecommendDraft, 0, len(parsed))
	seen := make(map[string]struct{}, len(parsed))
	for _, item := range parsed {
		if item == nil || shouldSkipMarketSummaryBackfill(item) {
			continue
		}
		code := strings.ToUpper(strings.TrimSpace(item.StockCode))
		if code == "" {
			continue
		}
		if _, ok := existingCodes[code]; ok {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		missing = append(missing, item)
	}
	outputLimit = normalizeMarketSummaryOutputLimit(outputLimit)
	if len(missing) > outputLimit {
		missing = missing[:outputLimit]
	}
	applyMarketSummaryProductionSelectionWithLimit(missing, productionLimit)
	return missing
}

func shouldSkipMarketSummaryBackfill(item *marketSummaryRecommendDraft) bool {
	if item == nil {
		return true
	}
	category := normalizeRecommendCategory(item.RecommendCategory)
	return category == "avoid"
}

func applyMarketSummaryProductionSelection(items []*marketSummaryRecommendDraft) {
	applyMarketSummaryProductionSelectionWithLimit(items, marketSummaryMaxProductionCandidates)
}

func applyMarketSummaryProductionSelectionWithLimit(items []*marketSummaryRecommendDraft, productionLimit int) {
	if len(items) == 0 {
		return
	}
	productionLimit = normalizeMarketSummaryProductionLimit(productionLimit)
	productionIndexes := make([]int, 0, len(items))
	for idx := range items {
		if items[idx] != nil && isAnalysisOnlyRecommend(items[idx].toPreviewRecommend()) {
			continue
		}
		reason := marketSummaryDraftProductionRejectionReason(items[idx])
		if reason != "" {
			downgradeMarketSummaryDraftToAnalysisOnlyWithRepairSource(items[idx], reason)
			continue
		}
		productionIndexes = append(productionIndexes, idx)
	}
	if len(productionIndexes) <= productionLimit {
		return
	}
	sort.SliceStable(productionIndexes, func(i, j int) bool {
		left := items[productionIndexes[i]]
		right := items[productionIndexes[j]]
		if marketSummaryDraftPriorityScore(left) != marketSummaryDraftPriorityScore(right) {
			return marketSummaryDraftPriorityScore(left) > marketSummaryDraftPriorityScore(right)
		}
		if left.EventStrength != right.EventStrength {
			return left.EventStrength > right.EventStrength
		}
		if left.CapitalConfirmation != right.CapitalConfirmation {
			return left.CapitalConfirmation > right.CapitalConfirmation
		}
		if left.TechnicalFit != right.TechnicalFit {
			return left.TechnicalFit > right.TechnicalFit
		}
		return strings.TrimSpace(left.StockCode) < strings.TrimSpace(right.StockCode)
	})
	for idx := productionLimit; idx < len(productionIndexes); idx++ {
		downgradeMarketSummaryDraftToAnalysisOnly(items[productionIndexes[idx]], fmt.Sprintf("超出当次市场总结最多%d只生产候选上限，已降级为仅分析", productionLimit))
	}
}

func normalizeMarketSummaryProductionLimit(productionLimit int) int {
	if productionLimit < 0 {
		return 0
	}
	if productionLimit > marketSummaryMaxProductionCandidates {
		return marketSummaryMaxProductionCandidates
	}
	return productionLimit
}

func normalizeMarketSummaryOutputLimit(outputLimit int) int {
	if outputLimit < 0 {
		return 0
	}
	if outputLimit > marketSummaryRecommendationOutputLimit {
		return marketSummaryRecommendationOutputLimit
	}
	return outputLimit
}

type marketSummaryV150Capacity struct {
	DailyProduction int64
	ActivePending   int64
}

func resolveMarketSummaryDecisionCompletionTime(startedAt time.Time) time.Time {
	now := timeNow()
	if now.IsZero() {
		now = time.Now()
	}
	if startedAt.IsZero() {
		return now
	}
	loc := cnLocation()
	startedLocal := startedAt.In(loc)
	nowLocal := now.In(loc)
	if nowLocal.Before(startedLocal) ||
		startedLocal.Year() != nowLocal.Year() ||
		startedLocal.YearDay() != nowLocal.YearDay() {
		// Historical imports/tests have no observable completion timestamp. Keep
		// their supplied decision time instead of rewriting them to today.
		return startedAt
	}
	return now
}

func applyMarketSummaryDecisionTimeline(drafts []*marketSummaryRecommendDraft, dataCutoff, decisionAt time.Time) {
	if decisionAt.IsZero() {
		return
	}
	if dataCutoff.IsZero() || dataCutoff.After(decisionAt) {
		dataCutoff = decisionAt
	}
	for _, draft := range drafts {
		if draft == nil || strings.TrimSpace(draft.SummaryVersion) != marketSummaryVersion150 {
			continue
		}
		completedAt := decisionAt
		draft.DataTime = &completedAt
		if strings.TrimSpace(draft.StockCurrentPriceTime) == "" {
			draft.StockCurrentPriceTime = dataCutoff.Format(time.DateTime)
		}
		raw := strings.TrimSpace(draft.ActivationRuleJSON)
		if raw == "" {
			continue
		}
		rule, err := parseActivationRuleJSON(raw)
		if err != nil || rule == nil {
			continue
		}
		validFrom := nextMarketSummary15MinuteBarStart(completedAt)
		setActivationRuleCausalTimeline(rule, completedAt, dataCutoff, validFrom)
		normalized, err := json.Marshal(rule)
		if err != nil {
			continue
		}
		draft.ActivationRuleJSON = string(normalized)
	}
}

func resolveMarketSummaryV150ProductionLimit(tx *gorm.DB, at time.Time, requested int) (int, error) {
	requested = normalizeMarketSummaryProductionLimit(requested)
	v150DailyCap := marketSummaryV150RiskOnDailyCap()
	if requested > v150DailyCap {
		requested = v150DailyCap
	}
	if requested == 0 {
		return 0, nil
	}
	capacity, err := loadMarketSummaryV150Capacity(tx, at)
	if err != nil {
		return 0, err
	}
	dailyAvailable := v150DailyCap - int(capacity.DailyProduction)
	activeAvailable := marketSummaryMaxActivePendingCandidates - int(capacity.ActivePending)
	if dailyAvailable < 0 {
		dailyAvailable = 0
	}
	if activeAvailable < 0 {
		activeAvailable = 0
	}
	if requested > dailyAvailable {
		requested = dailyAvailable
	}
	if requested > activeAvailable {
		requested = activeAvailable
	}
	return requested, nil
}

func nextMarketSummary15MinuteBarStart(decisionAt time.Time) time.Time {
	if decisionAt.IsZero() {
		return time.Time{}
	}
	loc := cnLocation()
	decision := decisionAt.In(loc)
	day := time.Date(decision.Year(), decision.Month(), decision.Day(), 0, 0, 0, 0, loc)
	marketOpen := func(target time.Time) time.Time {
		return time.Date(target.Year(), target.Month(), target.Day(), 9, 30, 0, 0, loc)
	}
	if !isCNOpenTradeDaySafe(day) {
		return marketOpen(shiftToNextCNOpenTradeDaySafe(day))
	}

	minuteOfDay := decision.Hour()*60 + decision.Minute()
	switch {
	case minuteOfDay < 9*60+30:
		return marketOpen(day)
	case minuteOfDay < 11*60+30:
		nextMinute := (minuteOfDay/15 + 1) * 15
		if nextMinute < 11*60+30 {
			return time.Date(day.Year(), day.Month(), day.Day(), nextMinute/60, nextMinute%60, 0, 0, loc)
		}
		return time.Date(day.Year(), day.Month(), day.Day(), 13, 0, 0, 0, loc)
	case minuteOfDay < 13*60:
		return time.Date(day.Year(), day.Month(), day.Day(), 13, 0, 0, 0, loc)
	case minuteOfDay < 15*60:
		nextMinute := (minuteOfDay/15 + 1) * 15
		if nextMinute < 15*60 {
			return time.Date(day.Year(), day.Month(), day.Day(), nextMinute/60, nextMinute%60, 0, 0, loc)
		}
	}
	nextDay := shiftToNextCNOpenTradeDaySafe(day.AddDate(0, 0, 1))
	return marketOpen(nextDay)
}

func loadMarketSummaryV150Capacity(tx *gorm.DB, at time.Time) (marketSummaryV150Capacity, error) {
	capacity := marketSummaryV150Capacity{}
	if tx == nil {
		tx = db.Dao
	}
	if tx == nil {
		return capacity, errors.New("market summary database is not initialized")
	}
	startOfDay, endOfDay := marketSummaryDayBounds(at)
	base := func() *gorm.DB {
		return tx.Table("ai_recommend_stocks AS r").
			Where("r.deleted_at IS NULL").
			Where("r.summary_version = ?", marketSummaryVersion150).
			Where("COALESCE(TRIM(r.execution_state), '') <> ?", recommendExecutionAnalysisOnly)
	}
	if err := base().
		Where("COALESCE(r.data_time, r.created_at) >= ? AND COALESCE(r.data_time, r.created_at) < ?", startOfDay, endOfDay).
		Count(&capacity.DailyProduction).Error; err != nil {
		return capacity, err
	}

	if tx.Migrator().HasTable(&models.AiRecommendYieldRecordState{}) {
		effectiveStatus := "COALESCE(NULLIF(TRIM(s.activation_status), ''), NULLIF(TRIM(r.activation_status), ''), 'pending')"
		if err := base().
			Joins("LEFT JOIN ai_recommend_yield_record_state AS s ON s.recommend_id = r.id").
			Where("("+effectiveStatus+" = ? OR ("+effectiveStatus+" = ? AND s.sell_time IS NULL))", "pending", "activated").
			Distinct("r.id").
			Count(&capacity.ActivePending).Error; err != nil {
			return capacity, err
		}
		return capacity, nil
	}

	effectiveStatus := "COALESCE(NULLIF(TRIM(r.activation_status), ''), 'pending')"
	if err := base().
		Where(effectiveStatus+" IN ?", []string{"pending", "activated"}).
		Distinct("r.id").
		Count(&capacity.ActivePending).Error; err != nil {
		return capacity, err
	}
	return capacity, nil
}

func marketSummaryDraftPriorityScore(item *marketSummaryRecommendDraft) int {
	if item == nil {
		return 0
	}
	return item.EventStrength + item.CapitalConfirmation + item.FundamentalFit + item.TechnicalFit
}

func marketSummaryDraftProductionRejectionReason(item *marketSummaryRecommendDraft) string {
	if item == nil {
		return "推荐草稿为空"
	}
	if isAnalysisOnlyRecommend(item.toPreviewRecommend()) {
		return ""
	}
	if containsMarketSummaryObservationPhrase(item.StockName, item.BkName, item.BuySignal, item.BuySignalDetail, item.InvalidSignal, item.Remarks) {
		return "观察型/右侧跟踪表述不进入生产交易候选"
	}
	if normalizeRecommendExecutionState(item.ExecutionState) != recommendExecutionConditional {
		return "执行状态未收敛为条件触发"
	}
	if !isMarketSummaryActivationSource(item.ActivationRuleSource) || strings.TrimSpace(item.ActivationRuleJSON) == "" {
		return "缺少结构化激活规则，已降级为仅分析"
	}
	if item.EventStrength < marketSummaryProductionScoreFloor ||
		item.CapitalConfirmation < marketSummaryProductionScoreFloor ||
		item.FundamentalFit < marketSummaryProductionScoreFloor ||
		item.TechnicalFit < marketSummaryProductionScoreFloor {
		return "四维评分未全部达到60分，已降级为仅分析"
	}
	switch strings.TrimSpace(item.SummaryVersion) {
	case marketSummaryVersion136, marketSummaryVersion140, marketSummaryVersion141, marketSummaryVersion142, marketSummaryVersion150:
		if reason := marketSummaryDraftV136TradePlanRejectionReason(item); reason != "" {
			return reason
		}
	}
	return ""
}

func marketSummaryDraftV136TradePlanRejectionReason(item *marketSummaryRecommendDraft) string {
	rec := item.toPreviewRecommend()
	if rec == nil {
		return "V1.3.6源头质量门槛未通过：推荐草稿为空"
	}
	if item.RecommendBuyPriceMin <= 0 || item.RecommendBuyPriceMax <= 0 {
		return "V1.3.6源头质量门槛未通过：缺少有效买入区间"
	}
	stopProfit, profitOK := parseStopProfitPrice(*rec)
	stopLoss, lossOK := parseStopLossPrice(*rec)
	if !profitOK || !lossOK || stopProfit <= 0 || stopLoss <= 0 {
		return "V1.3.6源头质量门槛未通过：缺少有效止盈止损"
	}
	worstEntry := resolveMarketSummaryWorstEntryPriceForGate(item)
	if worstEntry <= 0 {
		return "V1.3.6源头质量门槛未通过：缺少最差可成交价"
	}
	if stopProfit <= worstEntry || stopLoss >= worstEntry {
		return "V1.3.6源头质量门槛未通过：止盈止损与最差可成交价位置无效"
	}
	downside := worstEntry - stopLoss
	if downside <= 0 {
		return "V1.3.6源头质量门槛未通过：下行风险无效"
	}
	ratio := (stopProfit - worstEntry) / downside
	if ratio < 0.8 {
		return fmt.Sprintf("V1.3.6源头质量门槛未通过：最差成交价盈亏比 %.2f 低于 0.80", round2(ratio))
	}
	downsidePct := downside / worstEntry * 100
	if isMarketSummaryDownsideCapEnabled(item.SummaryVersion) && downsidePct > v132MaxDownsideRiskPct {
		return fmt.Sprintf("V1.3.6源头质量门槛未通过：止损空间 %.2f%% 超过 %.2f%%", round2(downsidePct), v132MaxDownsideRiskPct)
	}
	if anchor, ok := parseBuyPrice(firstNonEmptyText(item.StockCurrentPrice, item.ObservePrice, item.StockPrice)); ok && anchor > 0 {
		for _, price := range []float64{item.RecommendBuyPriceMin, item.RecommendBuyPriceMax, stopProfit, stopLoss, worstEntry} {
			if price <= 0 {
				continue
			}
			if math.Abs(price-anchor)/anchor > 0.2 {
				return fmt.Sprintf("V1.3.6源头质量门槛未通过：关键价位 %.2f 与价格锚点 %.2f 偏离超过20%%", round2(price), round2(anchor))
			}
		}
	}
	return ""
}

func isMarketSummaryDownsideCapEnabled(summaryVersion string) bool {
	normalized := normalizeStrategyCohort(summaryVersion, strings.TrimSpace(summaryVersion))
	switch strings.TrimSpace(normalized) {
	case marketSummaryVersion136, marketSummaryVersion140, marketSummaryVersion141:
		return true
	default:
		return false
	}
}

func resolveMarketSummaryWorstEntryPriceForGate(item *marketSummaryRecommendDraft) float64 {
	if item == nil {
		return 0
	}
	worst := item.RecommendBuyPriceMax
	if rule, err := parseActivationRuleJSON(item.ActivationRuleJSON); err == nil && rule != nil {
		for _, path := range activationRulePaths(rule) {
			if path.ThresholdMax > worst {
				worst = path.ThresholdMax
			}
		}
	}
	return round2(worst)
}

func containsMarketSummaryObservationPhrase(texts ...string) bool {
	for _, text := range texts {
		normalized := strings.ToLower(strings.TrimSpace(text))
		if normalized == "" {
			continue
		}
		for _, phrase := range marketSummaryObservationPhrases {
			if strings.Contains(normalized, phrase) {
				return true
			}
		}
	}
	return false
}

func downgradeMarketSummaryDraftToAnalysisOnly(draft *marketSummaryRecommendDraft, reason string) {
	if draft == nil {
		return
	}
	reason = normalizeRecommendText(reason)
	if reason == "" {
		reason = "不满足生产交易候选要求，已降级为仅分析"
	}
	draft.ExecutionState = recommendExecutionAnalysisOnly
	draft.RecommendCategory = ""
	draft.RecommendBuyPrice = ""
	draft.RecommendBuyPriceMin = 0
	draft.RecommendBuyPriceMax = 0
	draft.RecommendStopProfitPrice = ""
	draft.RecommendStopProfitPriceMin = 0
	draft.RecommendStopProfitPriceMax = 0
	draft.RecommendStopLossPrice = ""
	draft.FocusPrice = ""
	draft.BuySignal = "本次仅保留逻辑分析，不纳入生产交易候选"
	draft.BuySignalDetail = ""
	draft.SellSignal = ""
	draft.SellSignalDetail = ""
	draft.InvalidSignal = reason
	draft.InvalidCondition = reason
	draft.ActivationRuleJSON = ""
	draft.ActivationRuleVersion = ""
	draft.ActivationInvalidReason = reason
	draft.Remarks = appendRecommendRemarkNotice(draft.Remarks, reason)
}

func downgradeMarketSummaryDraftToAnalysisOnlyWithRepairSource(draft *marketSummaryRecommendDraft, reason string) {
	if draft == nil {
		return
	}
	repairSource := draft.RepairSource
	if repairSource == nil {
		repairSource = cloneMarketSummaryRecommendDraft(draft)
	}
	downgradeMarketSummaryDraftToAnalysisOnly(draft, reason)
	draft.RepairSource = repairSource
}

func cloneMarketSummaryRecommendDraft(draft *marketSummaryRecommendDraft) *marketSummaryRecommendDraft {
	if draft == nil {
		return nil
	}
	cloned := *draft
	cloned.RepairSource = nil
	return &cloned
}

func normalizeMarketSummaryActivationRuleForProduction(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", nil
	}
	rule, err := parseActivationRuleJSON(raw)
	if err != nil || rule == nil {
		return "", "", err
	}
	if len(rule.Paths) > 0 {
		for idx := range rule.Paths {
			rule.Paths[idx].OpeningPolicy = normalizeActivationOpeningPolicy(rule.Paths[idx].OpeningPolicy)
			if rule.Paths[idx].OpeningPolicy == nil {
				rule.Paths[idx].OpeningPolicy = &activationOpeningPolicy{}
			}
			rule.Paths[idx].OpeningPolicy.SameDayOnly = true
		}
		rule.Version = activationRuleVersionV3
	} else {
		rule.OpeningPolicy = normalizeActivationOpeningPolicy(rule.OpeningPolicy)
		if rule.OpeningPolicy == nil {
			rule.OpeningPolicy = &activationOpeningPolicy{}
		}
		rule.OpeningPolicy.SameDayOnly = true
		if strings.TrimSpace(rule.Version) == "" || strings.TrimSpace(rule.Version) == activationRuleVersionV1 {
			rule.Version = activationRuleVersionV3
		}
	}
	normalized, err := json.Marshal(rule)
	if err != nil {
		return "", "", err
	}
	return string(normalized), strings.TrimSpace(rule.Version), nil
}

func parseMarketSummaryRecommendStocks(summaryText, providerName, modelName string, dataTime time.Time) []*models.AiRecommendStocks {
	drafts := parseMarketSummaryRecommendStockDrafts(summaryText, providerName, modelName, dataTime)
	if len(drafts) == 0 {
		return nil
	}
	items := make([]*models.AiRecommendStocks, 0, len(drafts))
	for _, draft := range drafts {
		item, err := draft.toRecommendStock()
		if err != nil {
			continue
		}
		items = append(items, item)
	}
	return items
}

type marketSummaryRecommendDraft struct {
	DataTime                    *time.Time
	ProviderName                string
	ModelName                   string
	StockCode                   string
	StockName                   string
	BkName                      string
	StockPrice                  string
	StockCurrentPrice           string
	StockCurrentPriceTime       string
	StockClosePrice             string
	RecommendBuyPrice           string
	RecommendBuyPriceMin        float64
	RecommendBuyPriceMax        float64
	RecommendStopProfitPrice    string
	RecommendStopProfitPriceMin float64
	RecommendStopProfitPriceMax float64
	RecommendStopLossPrice      string
	RecommendCategory           string
	ExecutionState              string
	BuySignal                   string
	BuySignalDetail             string
	SellSignal                  string
	SellSignalDetail            string
	InvalidSignal               string
	CoreCatalyst                string
	KeyEvidence                 string
	InvalidCondition            string
	ObservePrice                string
	FocusPrice                  string
	ExpectedCycle               string
	EventStrength               int
	CapitalConfirmation         int
	FundamentalFit              int
	TechnicalFit                int
	ActivationRuleJSON          string
	ActivationRuleVersion       string
	ActivationRuleSource        string
	ActivationInvalidReason     string
	RecommendStatus             string
	SummaryVersion              string
	RiskRemarks                 string
	Remarks                     string
	RepairSource                *marketSummaryRecommendDraft
}

type marketSummaryYieldOverrideDraft struct {
	RecommendID uint
	Override    *models.AiRecommendYieldOverride
}

type marketSummaryYieldOverrideRow struct {
	recommendID      string
	stockCell        string
	verdict          string
	buyRange         string
	stopProfit       string
	stopLoss         string
	buySignal        string
	invalidCondition string
	reviewReason     string
}

type marketSummaryReportPrepareStats struct {
	RowsSeen           int
	DuplicateRowsOmit  int
	OutputRowsOmit     int
	AnalysisOnlyRows   int
	RecommendationRows int
}

func (d *marketSummaryRecommendDraft) toPreviewRecommend() *models.AiRecommendStocks {
	if d == nil {
		return nil
	}
	return &models.AiRecommendStocks{
		DataTime:                    d.DataTime,
		ProviderName:                d.ProviderName,
		ModelName:                   d.ModelName,
		StockCode:                   d.StockCode,
		StockName:                   d.StockName,
		BkName:                      d.BkName,
		StockPrice:                  d.StockPrice,
		StockCurrentPrice:           d.StockCurrentPrice,
		StockCurrentPriceTime:       d.StockCurrentPriceTime,
		StockClosePrice:             d.StockClosePrice,
		RecommendBuyPrice:           d.RecommendBuyPrice,
		RecommendBuyPriceMin:        d.RecommendBuyPriceMin,
		RecommendBuyPriceMax:        d.RecommendBuyPriceMax,
		RecommendStopProfitPrice:    d.RecommendStopProfitPrice,
		RecommendStopProfitPriceMin: d.RecommendStopProfitPriceMin,
		RecommendStopProfitPriceMax: d.RecommendStopProfitPriceMax,
		RecommendStopLossPrice:      d.RecommendStopLossPrice,
		RecommendCategory:           d.RecommendCategory,
		ExecutionState:              d.ExecutionState,
		BuySignal:                   d.BuySignal,
		BuySignalDetail:             d.BuySignalDetail,
		SellSignal:                  d.SellSignal,
		SellSignalDetail:            d.SellSignalDetail,
		InvalidSignal:               d.InvalidSignal,
		CoreCatalyst:                d.CoreCatalyst,
		KeyEvidence:                 d.KeyEvidence,
		InvalidCondition:            d.InvalidCondition,
		ObservePrice:                d.ObservePrice,
		FocusPrice:                  d.FocusPrice,
		ExpectedCycle:               d.ExpectedCycle,
		EventStrength:               d.EventStrength,
		CapitalConfirmation:         d.CapitalConfirmation,
		FundamentalFit:              d.FundamentalFit,
		TechnicalFit:                d.TechnicalFit,
		ActivationRuleJSON:          d.ActivationRuleJSON,
		ActivationRuleVersion:       d.ActivationRuleVersion,
		ActivationRuleSource:        d.ActivationRuleSource,
		ActivationInvalidReason:     d.ActivationInvalidReason,
		RecommendStatus:             d.RecommendStatus,
		SummaryVersion:              d.SummaryVersion,
		RiskRemarks:                 d.RiskRemarks,
		Remarks:                     d.Remarks,
	}
}

func (d *marketSummaryRecommendDraft) applyPreviewRecommend(rec *models.AiRecommendStocks) {
	if d == nil || rec == nil {
		return
	}
	d.StockPrice = rec.StockPrice
	d.StockCurrentPrice = rec.StockCurrentPrice
	d.StockCurrentPriceTime = rec.StockCurrentPriceTime
	d.StockClosePrice = rec.StockClosePrice
	d.RecommendBuyPrice = rec.RecommendBuyPrice
	d.RecommendBuyPriceMin = rec.RecommendBuyPriceMin
	d.RecommendBuyPriceMax = rec.RecommendBuyPriceMax
	d.RecommendStopProfitPrice = rec.RecommendStopProfitPrice
	d.RecommendStopProfitPriceMin = rec.RecommendStopProfitPriceMin
	d.RecommendStopProfitPriceMax = rec.RecommendStopProfitPriceMax
	d.RecommendStopLossPrice = rec.RecommendStopLossPrice
	d.ExecutionState = rec.ExecutionState
	d.BuySignal = rec.BuySignal
	d.BuySignalDetail = rec.BuySignalDetail
	d.SellSignal = rec.SellSignal
	d.SellSignalDetail = rec.SellSignalDetail
	d.InvalidSignal = rec.InvalidSignal
	d.InvalidCondition = rec.InvalidCondition
	d.ObservePrice = rec.ObservePrice
	d.FocusPrice = rec.FocusPrice
	d.ActivationRuleJSON = rec.ActivationRuleJSON
	d.ActivationRuleVersion = rec.ActivationRuleVersion
	d.ActivationInvalidReason = rec.ActivationInvalidReason
	d.RecommendStatus = rec.RecommendStatus
	d.Remarks = rec.Remarks
}

func (d *marketSummaryRecommendDraft) toRecommendStock() (*models.AiRecommendStocks, error) {
	if d == nil {
		return nil, fmt.Errorf("market summary recommend draft is nil")
	}
	item := &models.AiRecommendStocks{
		DataTime:                    d.DataTime,
		ProviderName:                d.ProviderName,
		ModelName:                   d.ModelName,
		StockCode:                   d.StockCode,
		StockName:                   d.StockName,
		BkName:                      d.BkName,
		StockPrice:                  d.StockPrice,
		StockCurrentPrice:           d.StockCurrentPrice,
		StockCurrentPriceTime:       d.StockCurrentPriceTime,
		StockClosePrice:             d.StockClosePrice,
		RecommendBuyPrice:           d.RecommendBuyPrice,
		RecommendBuyPriceMin:        d.RecommendBuyPriceMin,
		RecommendBuyPriceMax:        d.RecommendBuyPriceMax,
		RecommendStopProfitPrice:    d.RecommendStopProfitPrice,
		RecommendStopProfitPriceMin: d.RecommendStopProfitPriceMin,
		RecommendStopProfitPriceMax: d.RecommendStopProfitPriceMax,
		RecommendStopLossPrice:      d.RecommendStopLossPrice,
		RecommendCategory:           d.RecommendCategory,
		ExecutionState:              d.ExecutionState,
		BuySignal:                   d.BuySignal,
		BuySignalDetail:             d.BuySignalDetail,
		SellSignal:                  d.SellSignal,
		SellSignalDetail:            d.SellSignalDetail,
		InvalidSignal:               d.InvalidSignal,
		CoreCatalyst:                d.CoreCatalyst,
		KeyEvidence:                 d.KeyEvidence,
		EvidenceSources:             buildMarketSummaryEvidenceSourcesJSON(d.KeyEvidence, d.StockCode),
		InvalidCondition:            d.InvalidCondition,
		ObservePrice:                d.ObservePrice,
		FocusPrice:                  d.FocusPrice,
		ExpectedCycle:               d.ExpectedCycle,
		EventStrength:               d.EventStrength,
		CapitalConfirmation:         d.CapitalConfirmation,
		FundamentalFit:              d.FundamentalFit,
		TechnicalFit:                d.TechnicalFit,
		ActivationRuleJSON:          d.ActivationRuleJSON,
		ActivationRuleVersion:       d.ActivationRuleVersion,
		ActivationRuleSource:        d.ActivationRuleSource,
		ActivationInvalidReason:     d.ActivationInvalidReason,
		RecommendStatus:             d.RecommendStatus,
		SummaryVersion:              d.SummaryVersion,
		RiskRemarks:                 d.RiskRemarks,
		Remarks:                     d.Remarks,
	}
	item.RecommendReason = buildRecommendReasonCompat(item)
	if !isAnalysisOnlyRecommend(item) {
		fillSignalDrivenRecommendCompat(item, hasSignalDrivenRecommend(item), false)
	}
	return item, nil
}

type marketSummaryRow struct {
	category            string
	stockCell           string
	direction           string
	catalyst            string
	evidence            string
	latestPrice         string
	executionState      string
	buySignal           string
	buySignalDetail     string
	sellSignal          string
	sellSignalDetail    string
	invalidSignal       string
	focusPrice          string
	stopProfit          string
	stopLoss            string
	risk                string
	invalid             string
	expectedCycle       string
	eventStrength       string
	capitalConfirmation string
	fundamentalFit      string
	technicalFit        string
	remarks             string
}

func parseMarketSummaryRecommendStockDrafts(summaryText, providerName, modelName string, dataTime time.Time) []*marketSummaryRecommendDraft {
	return parseMarketSummaryRecommendStockDraftsWithVersion(summaryText, providerName, modelName, dataTime, marketSummaryCurrentVersion)
}

func parseMarketSummaryRecommendStockDraftsWithVersion(summaryText, providerName, modelName string, dataTime time.Time, summaryVersion string) []*marketSummaryRecommendDraft {
	section := extractMarkdownSection(summaryText, "推荐股票池")
	if strings.TrimSpace(section) == "" {
		section = summaryText
	}
	rows := extractMarkdownTableRows(section)
	if len(rows) == 0 {
		return nil
	}

	drafts := make([]*marketSummaryRecommendDraft, 0, len(rows))
	for _, row := range rows {
		draft := buildRecommendStockDraftFromRow(row, providerName, modelName, dataTime, summaryVersion)
		if draft == nil {
			continue
		}
		drafts = append(drafts, draft)
	}

	return drafts
}

func extractMarkdownTableRows(summaryText string) []marketSummaryRow {
	lines := strings.Split(summaryText, "\n")
	result := make([]marketSummaryRow, 0, 8)
	for idx := 0; idx < len(lines); {
		line := strings.TrimSpace(lines[idx])
		if !strings.HasPrefix(line, "|") {
			idx++
			continue
		}
		block := make([]string, 0, 8)
		for idx < len(lines) {
			current := strings.TrimSpace(lines[idx])
			if !strings.HasPrefix(current, "|") {
				break
			}
			block = append(block, current)
			idx++
		}
		result = append(result, extractMarkdownRowsFromTableBlock(block)...)
	}
	return result
}

func extractMarkdownSection(summaryText string, heading string) string {
	lines := strings.Split(summaryText, "\n")
	target := strings.TrimSpace(heading)
	if target == "" {
		return ""
	}
	start := -1
	for idx, raw := range lines {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "#") {
			continue
		}
		title := strings.TrimSpace(strings.TrimLeft(line, "#"))
		if title == target {
			start = idx + 1
			break
		}
	}
	if start < 0 {
		return ""
	}
	end := len(lines)
	for idx := start; idx < len(lines); idx++ {
		line := strings.TrimSpace(lines[idx])
		if strings.HasPrefix(line, "# ") {
			end = idx
			break
		}
	}
	return strings.TrimSpace(strings.Join(lines[start:end], "\n"))
}

func parseMarketSummaryYieldOverrideDrafts(summaryText string, startedAt time.Time) ([]*marketSummaryYieldOverrideDraft, error) {
	section := extractMarkdownSection(summaryText, "跳过复审")
	if strings.TrimSpace(section) == "" {
		return nil, nil
	}
	rows := extractMarkdownYieldOverrideRows(section)
	if len(rows) == 0 {
		return nil, nil
	}
	drafts := make([]*marketSummaryYieldOverrideDraft, 0, len(rows))
	for _, row := range rows {
		draft, err := buildMarketSummaryYieldOverrideDraftFromRow(row, startedAt)
		if err != nil {
			return nil, err
		}
		if draft == nil {
			continue
		}
		drafts = append(drafts, draft)
	}
	return drafts, nil
}

func extractMarkdownYieldOverrideRows(sectionText string) []marketSummaryYieldOverrideRow {
	lines := strings.Split(sectionText, "\n")
	rows := make([]marketSummaryYieldOverrideRow, 0, 8)
	for idx := 0; idx < len(lines); {
		line := strings.TrimSpace(lines[idx])
		if !strings.HasPrefix(line, "|") {
			idx++
			continue
		}
		block := make([]string, 0, 8)
		for idx < len(lines) {
			current := strings.TrimSpace(lines[idx])
			if !strings.HasPrefix(current, "|") {
				break
			}
			block = append(block, current)
			idx++
		}
		rows = append(rows, extractMarkdownYieldOverrideRowsFromTableBlock(block)...)
	}
	return rows
}

func extractMarkdownYieldOverrideRowsFromTableBlock(tableLines []string) []marketSummaryYieldOverrideRow {
	if len(tableLines) < 3 {
		return nil
	}
	headers := splitMarkdownTableLine(tableLines[0])
	if len(headers) == 0 {
		return nil
	}
	index := map[string]int{}
	for idx, header := range headers {
		text := normalizeMarkdownCell(header)
		switch {
		case strings.Contains(text, "原记录ID"):
			index["recommend_id"] = idx
		case strings.Contains(text, "股票"):
			index["stock"] = idx
		case strings.Contains(text, "复审结论"):
			index["verdict"] = idx
		case strings.Contains(text, "买入区间"):
			index["buy_range"] = idx
		case strings.Contains(text, "止盈区间"):
			index["stop_profit"] = idx
		case strings.Contains(text, "止损位"):
			index["stop_loss"] = idx
		case strings.Contains(text, "买入依据"):
			index["buy_signal"] = idx
		case strings.Contains(text, "失效条件"):
			index["invalid_condition"] = idx
		case strings.Contains(text, "跳过/复审说明") || strings.Contains(text, "复审说明") || strings.Contains(text, "跳过说明"):
			index["review_reason"] = idx
		}
	}
	if _, ok := index["recommend_id"]; !ok {
		return nil
	}
	if _, ok := index["stock"]; !ok {
		return nil
	}
	if _, ok := index["verdict"]; !ok {
		return nil
	}

	result := make([]marketSummaryYieldOverrideRow, 0, len(tableLines)-2)
	for _, line := range tableLines[2:] {
		cells := splitMarkdownTableLine(line)
		if len(cells) == 0 || isMarkdownSeparatorRow(cells) {
			continue
		}
		row := marketSummaryYieldOverrideRow{}
		assignTableCell(index, cells, "recommend_id", &row.recommendID)
		assignTableCell(index, cells, "stock", &row.stockCell)
		assignTableCell(index, cells, "verdict", &row.verdict)
		assignTableCell(index, cells, "buy_range", &row.buyRange)
		assignTableCell(index, cells, "stop_profit", &row.stopProfit)
		assignTableCell(index, cells, "stop_loss", &row.stopLoss)
		assignTableCell(index, cells, "buy_signal", &row.buySignal)
		assignTableCell(index, cells, "invalid_condition", &row.invalidCondition)
		assignTableCell(index, cells, "review_reason", &row.reviewReason)
		combined := normalizeRecommendText(strings.Join([]string{
			row.recommendID,
			row.stockCell,
			row.verdict,
			row.reviewReason,
		}, " "))
		if combined == "" || strings.Contains(combined, "暂无需要复审") {
			continue
		}
		result = append(result, row)
	}
	return result
}

func extractMarkdownRowsFromTableBlock(tableLines []string) []marketSummaryRow {
	if len(tableLines) < 3 {
		return nil
	}
	headers := splitMarkdownTableLine(tableLines[0])
	if len(headers) == 0 {
		return nil
	}
	index := map[string]int{}
	for idx, header := range headers {
		text := normalizeMarkdownCell(header)
		switch {
		case strings.Contains(text, "买入依据"):
			index["buy_signal"] = idx
		case strings.Contains(text, "买入补充条件") || strings.Contains(text, "买入条件补充") || strings.Contains(text, "买入信号补充") || strings.Contains(text, "买入补充说明"):
			index["buy_signal_detail"] = idx
		case strings.Contains(text, "执行状态"):
			index["execution_state"] = idx
		case strings.Contains(text, "买入信号"):
			index["buy_signal"] = idx
		case strings.Contains(text, "卖出补充条件") || strings.Contains(text, "卖出条件补充") || strings.Contains(text, "卖出信号补充"):
			index["sell_signal_detail"] = idx
		case strings.Contains(text, "卖出信号"):
			index["sell_signal"] = idx
		case strings.Contains(text, "失效信号"):
			index["invalid_signal"] = idx
		case strings.Contains(text, "分类"):
			index["category"] = idx
		case strings.Contains(text, "股票"):
			index["stock"] = idx
		case strings.Contains(text, "方向") || strings.Contains(text, "板块") || strings.Contains(text, "主题"):
			index["direction"] = idx
		case strings.Contains(text, "核心催化") || strings.Contains(text, "核心逻辑") || strings.Contains(text, "逻辑") || strings.Contains(text, "理由"):
			index["catalyst"] = idx
		case strings.Contains(text, "证据"):
			index["evidence"] = idx
		case strings.Contains(text, "价格锚点") || strings.Contains(text, "观察价") || strings.Contains(text, "最新"):
			index["latest"] = idx
		case strings.Contains(text, "买入区间") || strings.Contains(text, "买入区") || strings.Contains(text, "买入价") || strings.Contains(text, "关注位"):
			index["focus"] = idx
		case strings.Contains(text, "卖出区间") || strings.Contains(text, "止盈区间") || strings.Contains(text, "卖出") || strings.Contains(text, "止盈"):
			index["stop_profit"] = idx
		case strings.Contains(text, "止损位") || strings.Contains(text, "止损"):
			index["stop_loss"] = idx
		case strings.Contains(text, "风险"):
			index["risk"] = idx
		case strings.Contains(text, "失效"):
			index["invalid"] = idx
		case strings.Contains(text, "周期"):
			index["expected_cycle"] = idx
		case strings.Contains(text, "事件强度"):
			index["event_strength"] = idx
		case strings.Contains(text, "资金确认"):
			index["capital_confirmation"] = idx
		case strings.Contains(text, "基本面"):
			index["fundamental_fit"] = idx
		case strings.Contains(text, "技术面"):
			index["technical_fit"] = idx
		case strings.Contains(text, "备注") || strings.Contains(text, "操作"):
			index["remarks"] = idx
		}
	}
	if _, ok := index["stock"]; !ok {
		return nil
	}
	if _, ok := index["buy_signal"]; !ok {
		if _, hasFocus := index["focus"]; !hasFocus {
			return nil
		}
	}
	if _, ok := index["latest"]; !ok {
		if _, hasBuySignal := index["buy_signal"]; !hasBuySignal {
			return nil
		}
	}
	if _, hasBuySignal := index["buy_signal"]; !hasBuySignal {
		if _, hasFocus := index["focus"]; !hasFocus {
			return nil
		}
	}
	if _, hasBuySignal := index["buy_signal"]; !hasBuySignal && len(index) == 1 {
		return nil
	}

	rows := make([]marketSummaryRow, 0, len(tableLines)-2)
	for _, line := range tableLines[2:] {
		cells := splitMarkdownTableLine(line)
		if len(cells) == 0 || isMarkdownSeparatorRow(cells) {
			continue
		}
		row := marketSummaryRow{}
		assignTableCell(index, cells, "category", &row.category)
		assignTableCell(index, cells, "stock", &row.stockCell)
		assignTableCell(index, cells, "direction", &row.direction)
		assignTableCell(index, cells, "catalyst", &row.catalyst)
		assignTableCell(index, cells, "evidence", &row.evidence)
		assignTableCell(index, cells, "latest", &row.latestPrice)
		assignTableCell(index, cells, "execution_state", &row.executionState)
		assignTableCell(index, cells, "buy_signal", &row.buySignal)
		assignTableCell(index, cells, "buy_signal_detail", &row.buySignalDetail)
		assignTableCell(index, cells, "sell_signal", &row.sellSignal)
		assignTableCell(index, cells, "sell_signal_detail", &row.sellSignalDetail)
		assignTableCell(index, cells, "invalid_signal", &row.invalidSignal)
		assignTableCell(index, cells, "focus", &row.focusPrice)
		assignTableCell(index, cells, "stop_profit", &row.stopProfit)
		assignTableCell(index, cells, "stop_loss", &row.stopLoss)
		assignTableCell(index, cells, "risk", &row.risk)
		assignTableCell(index, cells, "invalid", &row.invalid)
		assignTableCell(index, cells, "expected_cycle", &row.expectedCycle)
		assignTableCell(index, cells, "event_strength", &row.eventStrength)
		assignTableCell(index, cells, "capital_confirmation", &row.capitalConfirmation)
		assignTableCell(index, cells, "fundamental_fit", &row.fundamentalFit)
		assignTableCell(index, cells, "technical_fit", &row.technicalFit)
		assignTableCell(index, cells, "remarks", &row.remarks)
		if row.stockCell == "" || strings.Contains(row.stockCell, "暂无") {
			continue
		}
		rows = append(rows, row)
	}
	return rows
}

func assignTableCell(index map[string]int, cells []string, key string, target *string) {
	idx, ok := index[key]
	if !ok || idx >= len(cells) {
		return
	}
	*target = normalizeMarkdownCell(cells[idx])
}

func splitMarkdownTableLine(line string) []string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	parts := strings.Split(trimmed, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func isMarkdownSeparatorRow(cells []string) bool {
	for _, cell := range cells {
		text := strings.TrimSpace(cell)
		text = strings.ReplaceAll(text, ":", "")
		text = strings.ReplaceAll(text, "-", "")
		if strings.TrimSpace(text) != "" {
			return false
		}
	}
	return true
}

func normalizeMarkdownCell(cell string) string {
	text := strings.TrimSpace(cell)
	replacer := strings.NewReplacer("**", "", "__", "", "`", "", "<br>", " ", "<br/>", " ", "<br />", " ")
	text = replacer.Replace(text)
	text = strings.ReplaceAll(text, "&nbsp;", " ")
	text = strings.TrimSpace(text)
	return text
}

func buildRecommendStockDraftFromRow(row marketSummaryRow, providerName, modelName string, dataTime time.Time, summaryVersion string) *marketSummaryRecommendDraft {
	stockName, stockCode := parseMarketSummaryStockCell(row.stockCell)
	if stockName == "" {
		return nil
	}
	summaryVersion = strings.TrimSpace(summaryVersion)
	if summaryVersion == "" {
		summaryVersion = marketSummaryCurrentVersion
	}
	resolvedCode, resolvedName := resolveMarketSummaryStockIdentity(stockName, stockCode)
	if resolvedName != "" {
		stockName = resolvedName
	}
	if resolvedCode != "" {
		stockCode = resolvedCode
	}
	if stockCode == "" {
		return nil
	}

	catalyst := strings.TrimSpace(row.catalyst)
	evidence := strings.TrimSpace(row.evidence)
	invalid := normalizeRecommendText(firstNonEmptyText(row.invalid, row.invalidSignal))
	risk := strings.TrimSpace(row.risk)
	if catalyst == "" {
		catalyst = strings.TrimSpace(row.direction)
	}
	if risk == "" {
		risk = "需复核成交量、事件兑现节奏与板块联动"
	}
	if catalyst == "" || evidence == "" || risk == "" {
		return nil
	}

	observePrice := firstNumericValue(row.latestPrice)
	buyAnchorText := firstNonEmptyText(row.focusPrice, extractSignalPriceRange(row.buySignal), extractSignalPriceRange(row.buySignalDetail), observePrice)
	focusText, focusMin, focusMax := parseMarketSummaryBuyRange(buyAnchorText)
	stopProfitText, stopProfitMin, stopProfitMax := parseMarketSummaryNumericRange(row.stopProfit)
	stopLossText := firstNumericValue(row.stopLoss)
	executionState := normalizeRecommendExecutionState(row.executionState)
	if executionState == "" {
		executionState = recommendExecutionConditional
	}
	buySignal := normalizeRecommendText(firstNonEmptyText(row.buySignal, row.buySignalDetail))
	buySignalDetail := normalizeRecommendText(row.buySignalDetail)
	sellSignal := normalizeRecommendText(row.sellSignal)
	sellSignalDetail := normalizeRecommendText(row.sellSignalDetail)
	invalidSignal := normalizeRecommendText(invalid)
	signalDriven := executionState != "" || buySignal != "" || buySignalDetail != "" || sellSignal != "" || sellSignalDetail != "" || invalidSignal != ""
	if focusText == "" && !signalDriven {
		return nil
	}
	if !signalDriven {
		return nil
	}
	if observePrice == "" {
		observePrice = firstNumericValue(focusText)
	}

	embeddedRuleJSONs := extractActivationRuleJSONPayloads(row.remarks)
	remarks := HumanizeRecommendRemarks(strings.TrimSpace(row.remarks))
	if remarks == "" && buySignal != "" {
		remarks = "买入依据：" + buySignal
	}
	if remarks == "" && invalid != "" {
		remarks = "失效条件：" + invalid
	}
	if remarks == "" {
		remarks = "auto-backfill-market-summary"
	}
	if shouldRejectMarketSummaryBackfillRow(row, dataTime, focusText, buySignal, buySignalDetail, remarks) {
		return nil
	}
	analysisOnlyReason := detectPreparedMarketSummaryAnalysisOnlyReason(row)
	if analysisOnlyReason != "" {
		executionState = recommendExecutionAnalysisOnly
	}

	bkName := strings.TrimSpace(row.direction)
	if bkName == "" {
		bkName = catalyst
	}
	if bkName == "" {
		return nil
	}
	if len([]rune(bkName)) > 64 {
		bkName = string([]rune(bkName)[:64])
	}

	draft := &marketSummaryRecommendDraft{
		DataTime:                    &dataTime,
		ProviderName:                strings.TrimSpace(providerName),
		ModelName:                   strings.TrimSpace(modelName),
		StockCode:                   strings.ToUpper(strings.TrimSpace(stockCode)),
		StockName:                   strings.TrimSpace(stockName),
		BkName:                      bkName,
		StockPrice:                  observePrice,
		StockCurrentPrice:           observePrice,
		StockCurrentPriceTime:       dataTime.Format(time.DateTime),
		StockClosePrice:             observePrice,
		RecommendBuyPrice:           focusText,
		RecommendBuyPriceMin:        focusMin,
		RecommendBuyPriceMax:        focusMax,
		RecommendStopProfitPrice:    stopProfitText,
		RecommendStopProfitPriceMin: stopProfitMin,
		RecommendStopProfitPriceMax: stopProfitMax,
		RecommendStopLossPrice:      stopLossText,
		RecommendCategory:           "",
		ExecutionState:              executionState,
		BuySignal:                   buySignal,
		BuySignalDetail:             buySignalDetail,
		SellSignal:                  sellSignal,
		SellSignalDetail:            sellSignalDetail,
		InvalidSignal:               invalidSignal,
		CoreCatalyst:                catalyst,
		KeyEvidence:                 evidence,
		InvalidCondition:            invalid,
		ObservePrice:                observePrice,
		FocusPrice:                  focusText,
		ExpectedCycle:               strings.TrimSpace(row.expectedCycle),
		EventStrength:               parseCellInt(row.eventStrength),
		CapitalConfirmation:         parseCellInt(row.capitalConfirmation),
		FundamentalFit:              parseCellInt(row.fundamentalFit),
		TechnicalFit:                parseCellInt(row.technicalFit),
		ActivationRuleVersion:       activationRuleVersionV1,
		ActivationRuleSource:        "market_summary",
		RecommendStatus:             "valid",
		SummaryVersion:              summaryVersion,
		RiskRemarks:                 risk,
		Remarks:                     remarks,
	}
	preview := draft.toPreviewRecommend()
	if rule, err := buildActivationRuleFromRecommend(preview); err == nil && rule != nil {
		if raw, marshalErr := json.Marshal(rule); marshalErr == nil {
			draft.ActivationRuleJSON = string(raw)
		}
	} else if err != nil {
		draft.ActivationInvalidReason = err.Error()
		for _, rawJSON := range embeddedRuleJSONs {
			rule, parseErr := parseActivationRuleJSON(rawJSON)
			if parseErr != nil || rule == nil {
				continue
			}
			raw, marshalErr := json.Marshal(rule)
			if marshalErr != nil {
				continue
			}
			draft.ActivationRuleJSON = string(raw)
			draft.ActivationRuleSource = "market_summary_embedded"
			draft.ActivationInvalidReason = ""
			break
		}
	}
	forcedRuleJSON, forcedVersion, forceErr := normalizeMarketSummaryActivationRuleForProduction(draft.ActivationRuleJSON)
	if forceErr != nil {
		draft.ActivationInvalidReason = forceErr.Error()
	} else {
		draft.ActivationRuleJSON = forcedRuleJSON
		if strings.TrimSpace(forcedVersion) != "" {
			draft.ActivationRuleVersion = forcedVersion
		}
	}
	if analysisOnlyReason != "" {
		draft.RecommendStatus = "missing_market_data"
		downgradeMarketSummaryDraftToAnalysisOnly(draft, analysisOnlyReason)
	}
	preview = draft.toPreviewRecommend()
	if preview != nil {
		_ = normalizeMarketSummaryExecutionDataForSave(preview)
		draft.applyPreviewRecommend(preview)
	}
	if reason := marketSummaryDraftProductionRejectionReason(draft); reason != "" {
		downgradeMarketSummaryDraftToAnalysisOnlyWithRepairSource(draft, reason)
	}
	preview = draft.toPreviewRecommend()
	if preview != nil && !isAnalysisOnlyRecommend(preview) &&
		(stopProfitText == "" || stopLossText == "" || strings.TrimSpace(row.expectedCycle) == "") {
		return nil
	}
	if draft.ModelName == "" {
		draft.ModelName = "market-summary-auto-backfill"
	}
	if draft.ProviderName == "" && draft.ModelName != "" {
		draft.ProviderName = strings.TrimSpace(DetectAIProviderName(&AIConfig{ModelName: draft.ModelName}))
	}
	applyMarketSummaryDecisionTimeline([]*marketSummaryRecommendDraft{draft}, dataTime, dataTime)
	return draft
}

func buildMarketSummaryYieldOverrideDraftFromRow(row marketSummaryYieldOverrideRow, startedAt time.Time) (*marketSummaryYieldOverrideDraft, error) {
	recommendIDText := firstNumericText(strings.TrimSpace(row.recommendID))
	if recommendIDText == "" {
		return nil, nil
	}
	recommendID64, err := strconv.ParseUint(recommendIDText, 10, 64)
	if err != nil || recommendID64 == 0 {
		return nil, nil
	}
	recommendID := uint(recommendID64)

	target, err := loadYieldOverrideTargetRecord(recommendID)
	if err != nil {
		return nil, err
	}

	_, stockCode := parseMarketSummaryStockCell(row.stockCell)
	if stockCode == "" && target != nil {
		stockCode = target.StockCode
	}

	activationStatus := normalizeMarketSummaryYieldOverrideVerdict(row.verdict)
	if activationStatus == "" {
		return nil, nil
	}

	reviewedAt := startedAt.In(cnLocation())
	override := &models.AiRecommendYieldOverride{
		RecommendID:              recommendID,
		StockCode:                normalizeRecommendStockCode(stockCode),
		ReviewSource:             yieldOverrideSourceMarketSummaryRejudge,
		ReviewedAt:               &reviewedAt,
		ActivationStatusOverride: activationStatus,
		BuySignal:                normalizeRecommendText(row.buySignal),
		InvalidCondition:         normalizeRecommendText(row.invalidCondition),
		DataStatusReason:         normalizeRecommendText(firstNonEmptyText(row.reviewReason, row.invalidCondition)),
	}

	if text, min, max := parseMarketSummaryBuyRange(row.buyRange); text != "" {
		override.RecommendBuyPrice = text
		override.RecommendBuyPriceMin = min
		override.RecommendBuyPriceMax = max
	}
	if text, min, max := parseMarketSummaryNumericRange(row.stopProfit); text != "" {
		override.RecommendStopProfitPrice = text
		override.RecommendStopProfitPriceMin = min
		override.RecommendStopProfitPriceMax = max
	}
	override.RecommendStopLossPrice = firstNumericValue(row.stopLoss)
	override.InvalidSignal = normalizeRecommendText(firstNonEmptyText(row.invalidCondition, target.InvalidSignal))

	if activationStatus == "skipped" {
		if override.DataStatusReason == "" {
			override.DataStatusReason = normalizeRecommendText(firstNonEmptyText(row.reviewReason, row.invalidCondition, target.InvalidCondition))
		}
	} else {
		if override.BuySignal == "" {
			override.BuySignal = normalizeRecommendText(firstNonEmptyText(row.buySignal, target.BuySignal, target.BuySignalDetail))
		}
		if override.InvalidCondition == "" {
			override.InvalidCondition = normalizeRecommendText(firstNonEmptyText(row.invalidCondition, target.InvalidCondition))
		}
		if override.InvalidSignal == "" {
			override.InvalidSignal = normalizeRecommendText(firstNonEmptyText(row.invalidCondition, target.InvalidSignal, target.InvalidCondition))
		}
	}

	return &marketSummaryYieldOverrideDraft{
		RecommendID: recommendID,
		Override:    override,
	}, nil
}

func normalizeMarketSummaryYieldOverrideVerdict(raw string) string {
	text := strings.TrimSpace(raw)
	switch {
	case text == "":
		return ""
	case strings.Contains(text, "继续跳过"), strings.Contains(text, "保留跳过"):
		return "skipped"
	case strings.Contains(text, "等待激活"), strings.Contains(text, "重新纳入"), strings.Contains(text, "改判可交易"), strings.Contains(text, "恢复跟踪"), strings.Contains(text, "恢复可交易"):
		return "pending"
	case strings.Contains(text, "已激活"):
		return "activated"
	case strings.Contains(text, "无法回算"), strings.Contains(text, "未激活失效"), strings.Contains(text, "无法判定"):
		return "invalid"
	default:
		return normalizeYieldOverrideActivationStatus(text)
	}
}

func shouldRejectMarketSummaryBackfillRow(row marketSummaryRow, dataTime time.Time, focusText, buySignal, buySignalDetail, remarks string) bool {
	if shouldBypassRecommendKeywordInterceptionAt(dataTime) {
		return false
	}
	texts := []string{
		row.category,
		row.focusPrice,
		focusText,
		row.buySignal,
		row.buySignalDetail,
		buySignal,
		buySignalDetail,
		remarks,
	}
	for _, text := range texts {
		normalized := strings.ToLower(strings.TrimSpace(text))
		if normalized == "" {
			continue
		}
		for _, phrase := range marketSummaryObservationPhrases {
			if strings.Contains(normalized, phrase) {
				return true
			}
		}
	}
	return false
}

func detectPreparedMarketSummaryAnalysisOnlyReason(row marketSummaryRow) string {
	text := normalizeRecommendText(strings.Join([]string{
		row.buySignal,
		row.invalid,
		row.invalidSignal,
		row.remarks,
	}, "\n"))
	if text == "" {
		return ""
	}
	switch {
	case strings.Contains(text, "已跳过激活与回测"):
		return text
	case strings.Contains(text, "仅保留逻辑分析"):
		return text
	case strings.Contains(text, "缺少真实价格/量能数据"):
		return text
	default:
		return ""
	}
}

func extractSignalPriceRange(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if matched := strings.TrimSpace(marketSummaryRangePattern.FindString(text)); matched != "" {
		return matched
	}
	return firstNumericValue(text)
}

func firstNumericValue(text string) string {
	return strings.TrimSpace(marketSummaryNumberPattern.FindString(strings.TrimSpace(text)))
}

func parseMarketSummaryBuyRange(text string) (string, float64, float64) {
	primary := extractMarketSummaryPrimaryBuyRangeText(text)
	if primary == "" {
		return "", 0, 0
	}
	return parseMarketSummaryNumericRange(primary)
}

func extractMarketSummaryPrimaryBuyRangeText(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	for _, label := range []string{"买入区间", "主买入区", "价格进入"} {
		idx := strings.Index(trimmed, label)
		if idx < 0 {
			continue
		}
		fragment := trimmed[idx:]
		if len(fragment) > 120 {
			fragment = fragment[:120]
		}
		if matched := strings.TrimSpace(marketSummaryRangePattern.FindString(fragment)); matched != "" {
			return matched
		}
	}
	segments := splitMarketSummaryRangeSegments(trimmed)
	for _, segment := range segments {
		if !strings.Contains(segment, "回踩") {
			continue
		}
		if matched := strings.TrimSpace(marketSummaryRangePattern.FindString(segment)); matched != "" {
			return matched
		}
	}
	for _, segment := range segments {
		if containsAnyText(segment, []string{"突破", "上破", "breakout"}) {
			continue
		}
		if matched := strings.TrimSpace(marketSummaryRangePattern.FindString(segment)); matched != "" {
			return matched
		}
	}
	if matched := strings.TrimSpace(marketSummaryRangePattern.FindString(trimmed)); matched != "" {
		return matched
	}
	return firstNumericValue(trimmed)
}

func splitMarketSummaryRangeSegments(text string) []string {
	rawSegments := strings.FieldsFunc(text, func(r rune) bool {
		return r == '\n' || r == ';' || r == '；'
	})
	segments := make([]string, 0, len(rawSegments))
	for _, segment := range rawSegments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		segments = append(segments, segment)
	}
	if len(segments) == 0 && strings.TrimSpace(text) != "" {
		return []string{strings.TrimSpace(text)}
	}
	return segments
}

func parseMarketSummaryNumericRange(text string) (string, float64, float64) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", 0, 0
	}
	matches := marketSummaryNumberPattern.FindAllString(trimmed, -1)
	if len(matches) == 0 {
		return "", 0, 0
	}
	minVal, err := strconv.ParseFloat(matches[0], 64)
	if err != nil {
		return "", 0, 0
	}
	maxVal := minVal
	if len(matches) > 1 {
		if parsedMax, err := strconv.ParseFloat(matches[len(matches)-1], 64); err == nil {
			maxVal = parsedMax
		}
	}
	if minVal > maxVal {
		minVal, maxVal = maxVal, minVal
	}
	return trimmed, minVal, maxVal
}

func buildMarketSummaryEvidenceSourcesJSON(text string, entityCode string) string {
	refs := normalizeEvidenceRefs(parseEvidenceSourcesFromText(text), entityCode)
	if len(refs) == 0 {
		return ""
	}
	payload, err := json.Marshal(refs)
	if err != nil {
		return ""
	}
	return string(payload)
}

func parseCellInt(text string) int {
	trimmed := strings.TrimSpace(text)
	value := firstNumericText(trimmed)
	if value == "" {
		switch {
		case strings.Contains(trimmed, "高"):
			return 80
		case strings.Contains(trimmed, "中"):
			return 60
		case strings.Contains(trimmed, "低"):
			return 40
		default:
			return 0
		}
	}
	f, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0
	}
	return int(f)
}

func parseMarketSummaryStockCell(text string) (string, string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || strings.Contains(trimmed, "暂无") {
		return "", ""
	}
	matches := marketSummaryStockCellPattern.FindStringSubmatch(trimmed)
	if len(matches) == 3 {
		return strings.TrimSpace(matches[1]), strings.ToUpper(strings.TrimSpace(matches[2]))
	}
	return trimmed, ""
}

func resolveMarketSummaryStockIdentity(stockName string, stockCode string) (string, string) {
	code := strings.TrimSpace(stockCode)
	name := strings.TrimSpace(stockName)
	if code != "" {
		return normalizeMarketSummaryStockCode(code), name
	}
	if name == "" {
		return "", ""
	}
	api := NewSearchStockApi(name)
	res := api.SearchStock(20)
	if res == nil {
		return "", name
	}
	if marketSummaryStringValue(res["code"]) != "100" {
		return "", name
	}
	data, _ := res["data"].(map[string]any)
	result, _ := data["result"].(map[string]any)
	list, _ := result["dataList"].([]any)
	for _, item := range list {
		row, _ := item.(map[string]any)
		candidateName := strings.TrimSpace(marketSummaryStringValue(row["SECURITY_NAME_ABBR"]))
		if candidateName == "" || candidateName != name {
			continue
		}
		candidateCode := strings.TrimSpace(marketSummaryStringValue(row["SECURITY_CODE_A"]))
		if candidateCode == "" {
			continue
		}
		if marketCode := strings.TrimSpace(marketSummaryStringValue(row["TRADE_MARKET_CODE"])); marketCode != "" {
			candidateCode = candidateCode + "." + strings.ToUpper(marketCode)
		}
		return normalizeMarketSummaryStockCode(candidateCode), candidateName
	}
	return "", name
}

func normalizeMarketSummaryStockCode(code string) string {
	trimmed := strings.ToUpper(strings.TrimSpace(code))
	if trimmed == "" {
		return ""
	}
	if strings.Contains(trimmed, ".") {
		return trimmed
	}
	if len(trimmed) != 6 {
		return trimmed
	}
	switch trimmed[0] {
	case '6':
		return trimmed + ".SH"
	case '8', '4':
		return trimmed + ".BJ"
	default:
		return trimmed + ".SZ"
	}
}

func marketSummaryStringValue(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	case []byte:
		return strings.TrimSpace(string(v))
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}
