package data

import (
	"errors"
	"fmt"
	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/duke-git/lancet/v2/datetime"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	aiRecommendRecalcStaleTTL      = 8 * time.Minute
	aiRecommendQueryRecalcCooldown = 45 * time.Second
	frozenSellPriceFixVersion      = "open-gap-v1"
)

type aiRecommendYieldAggregate struct {
	StockCode string
	StockName string

	SignalTime time.Time
	BuyTime    time.Time

	BuyAmountSum   float64
	BuyAmountCount int

	StopProfitSum   float64
	StopProfitCount int

	StopLossSum   float64
	StopLossCount int

	BkNames []string
	BkSet   map[string]struct{}

	ModelNames []string
	ModelSet   map[string]struct{}

	RecommendCount               int
	RequirePrevDayActivityFilter bool
}

type aiRecommendYieldRecalcManager struct {
	mu            sync.Mutex
	running       bool
	pending       bool
	pendingForce  bool
	pendingReason string
	pendingScope  map[string]struct{}
}

var globalAiRecommendYieldRecalcManager = &aiRecommendYieldRecalcManager{}
var canonicalAShareTsCodeCache sync.Map
var activeManualYieldAuditState struct {
	mu    sync.RWMutex
	audit *aiRecommendYieldManualAudit
}
var timeNow = time.Now
var fetchMinuteBarsWithTencentFn = fetchMinuteBarsWithTencent
var fetchMinuteBarsWithAkShareFn = fetchMinuteBarsWithAkShare
var fetchMinuteBarsWithSinaFn = fetchMinuteBarsWithSina
var fetchMinuteBarsWithDiemengFn = fetchMinuteBarsWithDiemeng
var fetchCurrentPriceMapFn = fetchCurrentPriceMap
var requestAiRecommendYieldRecalcForQueryFn = requestAiRecommendYieldRecalc
var requestAiRecommendYieldScopedRecalcForQueryFn = requestAiRecommendYieldRecalcWithScope
var requestAiRecommendYieldScopedRecalcForMutationFn = requestAiRecommendYieldRecalcWithScope
var markAiRecommendYieldDirtyCodesForMutationFn = markAiRecommendYieldDirtyCodes

func requestAiRecommendYieldRecalc(force bool, reason string) {
	requestAiRecommendYieldRecalcWithScope(force, reason, nil)
}

func requestAiRecommendYieldRecalcWithScope(force bool, reason string, scopeCodes []string) {
	scopeMap := normalizeScopeCodes(scopeCodes)
	globalAiRecommendYieldRecalcManager.Request(force, reason, scopeMap)
}

func startManualAiRecommendMinuteDownload() (map[string]any, error) {
	EnsureDiemengSelfCheckAsync("manual_minute_download")
	if schemaErr := ensureYieldMetaSchema(); schemaErr != nil {
		return nil, schemaErr
	}
	meta, err := getOrCreateYieldMeta()
	if err != nil {
		return nil, err
	}

	now := time.Now()
	if resetStaleYieldRecalcIfNeeded(meta) {
		meta.RecalcInProgress = false
	}
	if meta.RecalcInProgress {
		return map[string]any{
			"accepted":   false,
			"inProgress": true,
			"message":    "后台任务进行中，请等待完成",
		}, nil
	}

	// Manual download should cover all recommendation records, then re-evaluate
	// status against the latest closed trading window.
	scopeCodes, err := loadScopeCodesForManualDownload()
	if err != nil {
		return nil, err
	}
	if len(scopeCodes) == 0 {
		return map[string]any{
			"accepted":   false,
			"inProgress": false,
			"message":    "暂无股票可下载",
		}, nil
	}

	if err = runWithSQLiteBusyRetry(func() error {
		return db.Dao.Model(&models.AiRecommendYieldMeta{}).Where("id = ?", meta.ID).Updates(map[string]any{
			"last_manual_download_at":       now,
			"manual_cooldown_until":         nil,
			"last_query_recalc_at":          nil,
			"query_cooldown_until":          nil,
			"akshare_install_error":         "",
			"download_in_progress":          true,
			"download_total":                len(scopeCodes),
			"download_done":                 0,
			"download_progress":             0,
			"last_download_error":           "",
			"last_manual_finished_at":       nil,
			"last_manual_scope_count":       len(scopeCodes),
			"last_manual_prefetch_ms":       0,
			"last_manual_recalc_ms":         0,
			"last_manual_total_ms":          0,
			"last_manual_sqlite_busy_count": 0,
			"last_manual_provider_summary":  "",
		}).Error
	}); err != nil {
		return nil, err
	}

	requestAiRecommendYieldRecalcWithScope(true, "manual_minute_download", scopeCodes)
	return map[string]any{
		"accepted":          true,
		"inProgress":        true,
		"scopeCount":        len(scopeCodes),
		"cooldownUntil":     "",
		"cooldownRemainSec": 0,
		"message":           fmt.Sprintf("已开始下载分钟线并触发收益重算（%d 只股票）", len(scopeCodes)),
	}, nil
}

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

func keysFromScopeMap(scope map[string]struct{}) []string {
	if len(scope) == 0 {
		return []string{}
	}
	codes := make([]string, 0, len(scope))
	for code := range scope {
		code = normalizeRecommendStockCode(code)
		if code == "" {
			continue
		}
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes
}

func ensureYieldDirtySchema() error {
	return db.Dao.AutoMigrate(&models.AiRecommendYieldDirtyCode{})
}

func markAiRecommendYieldDirtyCodes(scopeCodes []string, reason string, mode string) error {
	if schemaErr := ensureYieldDirtySchema(); schemaErr != nil {
		return schemaErr
	}
	normalized := normalizeScopeCodes(scopeCodes)
	if len(normalized) == 0 {
		return nil
	}
	rows := make([]models.AiRecommendYieldDirtyCode, 0, len(normalized))
	for code := range normalized {
		rows = append(rows, models.AiRecommendYieldDirtyCode{
			StockCode:  code,
			Reason:     strings.TrimSpace(reason),
			ModeNeeded: normalizeAiRecommendYieldMode(mode),
		})
	}
	return db.Dao.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "stock_code"}},
		DoUpdates: clause.AssignmentColumns([]string{"updated_at", "reason", "mode_needed"}),
	}).CreateInBatches(rows, 100).Error
}

func loadDirtyAiRecommendYieldCodes(mode string) ([]string, error) {
	if schemaErr := ensureYieldDirtySchema(); schemaErr != nil {
		return nil, schemaErr
	}
	rows := make([]models.AiRecommendYieldDirtyCode, 0, 64)
	q := db.Dao.Model(&models.AiRecommendYieldDirtyCode{})
	mode = normalizeAiRecommendYieldMode(mode)
	if mode != "" {
		q = q.Where("mode_needed = ? OR mode_needed = ''", mode)
	}
	if err := q.Order("updated_at ASC, stock_code ASC").Find(&rows).Error; err != nil {
		if isSQLiteNoSuchTable(err) {
			return []string{}, nil
		}
		return nil, err
	}
	codes := make([]string, 0, len(rows))
	for _, row := range rows {
		code := normalizeRecommendStockCode(row.StockCode)
		if code == "" {
			continue
		}
		codes = append(codes, code)
	}
	return codes, nil
}

func loadDirtyAiRecommendYieldCodeSet(mode string) (map[string]models.AiRecommendYieldDirtyCode, error) {
	rows := make([]models.AiRecommendYieldDirtyCode, 0, 64)
	if schemaErr := ensureYieldDirtySchema(); schemaErr != nil {
		return nil, schemaErr
	}
	q := db.Dao.Model(&models.AiRecommendYieldDirtyCode{})
	mode = normalizeAiRecommendYieldMode(mode)
	if mode != "" {
		q = q.Where("mode_needed = ? OR mode_needed = ''", mode)
	}
	if err := q.Find(&rows).Error; err != nil {
		if isSQLiteNoSuchTable(err) {
			return map[string]models.AiRecommendYieldDirtyCode{}, nil
		}
		return nil, err
	}
	result := make(map[string]models.AiRecommendYieldDirtyCode, len(rows))
	for _, row := range rows {
		code := normalizeRecommendStockCode(row.StockCode)
		if code == "" {
			continue
		}
		result[code] = row
	}
	return result, nil
}

func markInvalidActivationExitPlanDirtyCodes(mode string) error {
	if db.Dao == nil {
		return nil
	}
	scope := make(map[string]struct{})
	stateRows := make([]models.AiRecommendYieldState, 0, 16)
	if err := db.Dao.Model(&models.AiRecommendYieldState{}).
		Where("activation_status = ? AND buy_amount > 0 AND stop_profit_amount IS NOT NULL AND buy_amount >= stop_profit_amount", "activated").
		Find(&stateRows).Error; err != nil && !isSQLiteNoSuchTable(err) {
		return err
	}
	for _, row := range stateRows {
		code := normalizeRecommendStockCode(row.StockCode)
		if code != "" {
			scope[code] = struct{}{}
		}
	}
	recordRows := make([]models.AiRecommendYieldRecordState, 0, 16)
	if err := db.Dao.Model(&models.AiRecommendYieldRecordState{}).
		Where("activation_status = ? AND buy_amount > 0 AND stop_profit_amount IS NOT NULL AND buy_amount >= stop_profit_amount", "activated").
		Find(&recordRows).Error; err != nil && !isSQLiteNoSuchTable(err) {
		return err
	}
	for _, row := range recordRows {
		code := normalizeRecommendStockCode(row.StockCode)
		if code != "" {
			scope[code] = struct{}{}
		}
	}
	if len(scope) == 0 {
		return nil
	}
	return markAiRecommendYieldDirtyCodes(
		keysFromScopeMap(scope),
		"买入价不低于止盈价，等待按突破追价上限规则重算",
		mode,
	)
}

func markActivationWindowPolicyBugDirtyCodes(mode string) error {
	if db.Dao == nil {
		return nil
	}
	rows := make([]struct {
		StockCode          string
		SignalAt           string
		ActivationRuleJSON string
		StateReason        string
		RecordReason       string
	}, 0, 32)
	err := db.Dao.Table("ai_recommend_stocks AS r").
		Select("r.stock_code, COALESCE(r.data_time, r.created_at) AS signal_at, r.activation_rule_json, COALESCE(s.data_status_reason, '') AS state_reason, COALESCE(r.activation_invalid_reason, '') AS record_reason").
		Joins("LEFT JOIN ai_recommend_yield_record_state AS s ON s.recommend_id = r.id").
		Where("r.deleted_at IS NULL").
		Where("r.activation_rule_source IN ?", []string{"market_summary", "market_summary_embedded"}).
		Where("(COALESCE(s.activation_status, r.activation_status, '') = ? OR COALESCE(s.data_status, '') = ?)", "pending", "待激活").
		Find(&rows).Error
	if err != nil {
		if isSQLiteNoSuchTable(err) {
			return nil
		}
		return err
	}
	scope := make(map[string]struct{})
	for _, row := range rows {
		code := normalizeRecommendStockCode(row.StockCode)
		if code == "" {
			continue
		}
		signalAt, _ := parseSQLiteDateTimeText(row.SignalAt)
		reason := strings.TrimSpace(firstNonEmptyText(row.StateReason, row.RecordReason))
		if reasonIndicatesActivationWindowPolicyBug(reason, signalAt) || activationRuleHasBreakoutPathMissingThresholdMax(row.ActivationRuleJSON) {
			scope[code] = struct{}{}
		}
	}
	if len(scope) == 0 {
		return nil
	}
	return markAiRecommendYieldDirtyCodes(
		keysFromScopeMap(scope),
		"激活扫描窗口或旧突破追价上限规则已修复，等待 strict 重算",
		mode,
	)
}

func markV132VWAPScaleDirtyCodes(mode string) error {
	if db.Dao == nil {
		return nil
	}
	rows := make([]struct {
		StockCode               string
		DataStatusReason        string
		ActivationInvalidReason string
	}, 0, 16)
	err := db.Dao.Table("ai_recommend_stocks AS r").
		Select("r.stock_code, COALESCE(s.data_status_reason, '') AS data_status_reason, COALESCE(r.activation_invalid_reason, '') AS activation_invalid_reason").
		Joins("LEFT JOIN ai_recommend_yield_record_state AS s ON s.recommend_id = r.id").
		Where("r.deleted_at IS NULL").
		Where("r.summary_version = ?", marketSummaryVersionV132).
		Where("(COALESCE(s.data_status_reason, '') LIKE ? OR COALESCE(r.activation_invalid_reason, '') LIKE ?)", "%VWAP%", "%VWAP%").
		Find(&rows).Error
	if err != nil {
		if isSQLiteNoSuchTable(err) {
			return nil
		}
		return err
	}
	scope := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		code := normalizeRecommendStockCode(row.StockCode)
		if code == "" {
			continue
		}
		reason := strings.TrimSpace(firstNonEmptyText(row.DataStatusReason, row.ActivationInvalidReason))
		if reasonIndicatesV132VWAPScaleBug(reason) {
			scope[code] = struct{}{}
		}
	}
	if len(scope) == 0 {
		return nil
	}
	return markAiRecommendYieldDirtyCodes(
		keysFromScopeMap(scope),
		"V1.3.2 VWAP 单位归一化规则已修复，等待 strict 重算",
		mode,
	)
}

func reasonIndicatesActivationWindowPolicyBug(reason string, signalAt time.Time) bool {
	reason = strings.TrimSpace(reason)
	if reason == "" || signalAt.IsZero() {
		return false
	}
	if strings.Contains(reason, "隔夜推荐等待") {
		return !shouldApplyOpeningPolicyForActivation(signalAt, normalizeActivationOpeningPolicy(&activationOpeningPolicy{}))
	}
	if strings.Contains(reason, "主买入区尚未进入可扫描窗口") {
		loc := cnLocation()
		t := normalizeMinuteTime(signalAt.In(loc))
		minutes := t.Hour()*60 + t.Minute()
		return minutes == 11*60+30 || minutes == 15*60
	}
	return false
}

func activationRuleHasBreakoutPathMissingThresholdMax(raw string) bool {
	rule, err := parseActivationRuleJSON(raw)
	if err != nil || rule == nil {
		return false
	}
	for _, path := range activationRulePaths(rule) {
		if strings.TrimSpace(path.SignalType) == "price_breakout_with_volume" && path.ThresholdValue > 0 && path.ThresholdMax <= 0 {
			return true
		}
	}
	return false
}

func clearAiRecommendYieldDirtyCodes(scopeCodes []string) error {
	normalized := normalizeScopeCodes(scopeCodes)
	if len(normalized) == 0 {
		return nil
	}
	codes := make([]string, 0, len(normalized))
	for code := range normalized {
		codes = append(codes, code)
	}
	return db.Dao.Where("stock_code IN ?", codes).Delete(&models.AiRecommendYieldDirtyCode{}).Error
}

func clearAiRecommendYieldDirtyCodesByRecordStates(states []models.AiRecommendYieldRecordState) error {
	if len(states) == 0 {
		return nil
	}
	scopeSet := make(map[string]struct{}, len(states))
	for _, state := range states {
		status := strings.TrimSpace(strings.ToLower(state.ActivationStatus))
		if status != "invalid" && status != "skipped" && status != "expired" && status != "ineligible" {
			continue
		}
		code := normalizeRecommendStockCode(state.StockCode)
		if code == "" {
			continue
		}
		scopeSet[code] = struct{}{}
	}
	if len(scopeSet) == 0 {
		return nil
	}
	scopeCodes := make([]string, 0, len(scopeSet))
	for code := range scopeSet {
		scopeCodes = append(scopeCodes, code)
	}
	sort.Strings(scopeCodes)
	return clearAiRecommendYieldDirtyCodes(scopeCodes)
}

func getOrCreateYieldMeta() (*models.AiRecommendYieldMeta, error) {
	meta := &models.AiRecommendYieldMeta{}
	err := db.Dao.Model(&models.AiRecommendYieldMeta{}).First(meta).Error
	if err == nil {
		if fixErr := applyFrozenSellPriceFix(meta); fixErr != nil {
			logger.SugaredLogger.Warnf("apply frozen sell price fix failed: %v", fixErr)
		}
		return meta, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if createErr := db.Dao.Create(meta).Error; createErr != nil {
		return nil, createErr
	}
	if fixErr := applyFrozenSellPriceFix(meta); fixErr != nil {
		logger.SugaredLogger.Warnf("apply frozen sell price fix failed: %v", fixErr)
	}
	return meta, nil
}

type frozenSellPriceSnapshot interface {
	getID() uint
	getStockCode() string
	getSellTime() *time.Time
	getRealizedSellAmount() *float64
	getBuyAmount() float64
	getPositionStatus() string
	getStopProfitAmount() *float64
	getStopLossAmount() *float64
}

type frozenSellPriceUpdater interface {
	updateSellSnapshot(id uint, sellPrice, yield float64, yieldText string, updatedAt time.Time) error
}

type frozenYieldStateSnapshot struct{ models.AiRecommendYieldState }

type frozenYieldRecordSnapshot struct {
	models.AiRecommendYieldRecordState
}

type frozenYieldStateUpdater struct{}

type frozenYieldRecordUpdater struct{}

func applyFrozenSellPriceFix(meta *models.AiRecommendYieldMeta) error {
	if meta == nil || meta.ID == 0 {
		return nil
	}
	if strings.TrimSpace(meta.FrozenSellPriceFixVersion) == frozenSellPriceFixVersion {
		return nil
	}
	if err := rewriteFrozenSellSnapshots(loadFrozenYieldStateSnapshots, frozenYieldStateUpdater{}); err != nil {
		return err
	}
	if err := rewriteFrozenSellSnapshots(loadFrozenYieldRecordSnapshots, frozenYieldRecordUpdater{}); err != nil {
		return err
	}
	now := time.Now()
	if err := db.Dao.Model(&models.AiRecommendYieldMeta{}).Where("id = ?", meta.ID).Updates(map[string]any{
		"frozen_sell_price_fix_version": frozenSellPriceFixVersion,
		"updated_at":                    now,
	}).Error; err != nil {
		return err
	}
	meta.FrozenSellPriceFixVersion = frozenSellPriceFixVersion
	meta.UpdatedAt = now
	return nil
}

func loadFrozenYieldStateSnapshots() ([]frozenSellPriceSnapshot, error) {
	rows := make([]models.AiRecommendYieldState, 0, 64)
	if err := db.Dao.Model(&models.AiRecommendYieldState{}).
		Where("frozen = ? AND sell_time IS NOT NULL AND realized_sell_amount IS NOT NULL AND position_status IN ?", true, []string{"已止盈", "已止损"}).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]frozenSellPriceSnapshot, 0, len(rows))
	for _, row := range rows {
		result = append(result, frozenYieldStateSnapshot{AiRecommendYieldState: row})
	}
	return result, nil
}

func loadFrozenYieldRecordSnapshots() ([]frozenSellPriceSnapshot, error) {
	rows := make([]models.AiRecommendYieldRecordState, 0, 64)
	if err := db.Dao.Model(&models.AiRecommendYieldRecordState{}).
		Where("frozen = ? AND sell_time IS NOT NULL AND realized_sell_amount IS NOT NULL AND position_status IN ?", true, []string{"已止盈", "已止损"}).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]frozenSellPriceSnapshot, 0, len(rows))
	for _, row := range rows {
		result = append(result, frozenYieldRecordSnapshot{AiRecommendYieldRecordState: row})
	}
	return result, nil
}

func rewriteFrozenSellSnapshots(load func() ([]frozenSellPriceSnapshot, error), updater frozenSellPriceUpdater) error {
	rows, err := load()
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.getSellTime() == nil || row.getSellTime().IsZero() {
			continue
		}
		barTime := normalizeMinuteTime(row.getSellTime().In(cnLocation()))
		bars, err := listMinuteBarsFromCache(row.getStockCode(), barTime, barTime.Add(time.Minute))
		if err != nil || len(bars) == 0 {
			continue
		}
		price, ok := correctedFrozenSellPrice(row.getPositionStatus(), row.getStopProfitAmount(), row.getStopLossAmount(), bars[0])
		if !ok {
			continue
		}
		if row.getRealizedSellAmount() != nil && round2(*row.getRealizedSellAmount()) == price {
			continue
		}
		yield, yieldText := calculateFrozenSellYield(row.getStockCode(), row.getBuyAmount(), price)
		if err := updater.updateSellSnapshot(row.getID(), price, yield, yieldText, time.Now()); err != nil {
			return err
		}
	}
	return nil
}

func calculateFrozenSellYield(stockCode string, buyAmount, sellPrice float64) (float64, string) {
	yield := 0.0
	yieldText := "--"
	if buyAmount <= 0 {
		return yield, yieldText
	}
	result := calculateNetYield(stockCode, buyAmount, sellPrice)
	if !result.Valid {
		return yield, yieldText
	}
	return result.YieldRate, result.YieldText
}

func (f frozenYieldStateSnapshot) getID() uint                     { return f.ID }
func (f frozenYieldStateSnapshot) getStockCode() string            { return f.StockCode }
func (f frozenYieldStateSnapshot) getSellTime() *time.Time         { return f.SellTime }
func (f frozenYieldStateSnapshot) getRealizedSellAmount() *float64 { return f.RealizedSellAmount }
func (f frozenYieldStateSnapshot) getBuyAmount() float64           { return f.BuyAmount }
func (f frozenYieldStateSnapshot) getPositionStatus() string       { return f.PositionStatus }
func (f frozenYieldStateSnapshot) getStopProfitAmount() *float64   { return f.StopProfitAmount }
func (f frozenYieldStateSnapshot) getStopLossAmount() *float64     { return f.StopLossAmount }

func (f frozenYieldRecordSnapshot) getID() uint                     { return f.ID }
func (f frozenYieldRecordSnapshot) getStockCode() string            { return f.StockCode }
func (f frozenYieldRecordSnapshot) getSellTime() *time.Time         { return f.SellTime }
func (f frozenYieldRecordSnapshot) getRealizedSellAmount() *float64 { return f.RealizedSellAmount }
func (f frozenYieldRecordSnapshot) getBuyAmount() float64           { return f.BuyAmount }
func (f frozenYieldRecordSnapshot) getPositionStatus() string       { return f.PositionStatus }
func (f frozenYieldRecordSnapshot) getStopProfitAmount() *float64   { return f.StopProfitAmount }
func (f frozenYieldRecordSnapshot) getStopLossAmount() *float64     { return f.StopLossAmount }

func (frozenYieldStateUpdater) updateSellSnapshot(id uint, sellPrice, yield float64, yieldText string, updatedAt time.Time) error {
	return db.Dao.Model(&models.AiRecommendYieldState{}).Where("id = ?", id).Updates(map[string]any{
		"realized_sell_amount": sellPrice,
		"yield_rate":           yield,
		"yield_rate_text":      yieldText,
		"updated_at":           updatedAt,
	}).Error
}

func (frozenYieldRecordUpdater) updateSellSnapshot(id uint, sellPrice, yield float64, yieldText string, updatedAt time.Time) error {
	return db.Dao.Model(&models.AiRecommendYieldRecordState{}).Where("id = ?", id).Updates(map[string]any{
		"realized_sell_amount": sellPrice,
		"yield_rate":           yield,
		"yield_rate_text":      yieldText,
		"updated_at":           updatedAt,
	}).Error
}

func correctedFrozenSellPrice(status string, stopProfit, stopLoss *float64, bar minuteBar) (float64, bool) {
	if isSoldPositionStatus(status) && strings.TrimSpace(status) == "已止盈" && stopProfit != nil && bar.Open >= *stopProfit {
		return round2(bar.Open), true
	}
	if isSoldPositionStatus(status) && strings.TrimSpace(status) == "已止损" && stopLoss != nil && bar.Open <= *stopLoss {
		return round2(bar.Open), true
	}
	return 0, false
}

func syncYieldStateIdentityFields() error {
	now := time.Now()
	return db.Dao.Exec(`
UPDATE ai_recommend_yield_state
SET recommend_category = '',
    recommend_time = signal_time,
    updated_at = ?
WHERE COALESCE(recommend_category, '') <> ''
   OR COALESCE(recommend_time, '') <> COALESCE(signal_time, '')
`, now).Error
}

func syncYieldRecordStateIdentityFields() error {
	now := time.Now()
	return db.Dao.Exec(`
UPDATE ai_recommend_yield_record_state
SET stock_code = COALESCE((SELECT stock_code FROM ai_recommend_stocks WHERE id = ai_recommend_yield_record_state.recommend_id), stock_code),
    stock_name = COALESCE((SELECT stock_name FROM ai_recommend_stocks WHERE id = ai_recommend_yield_record_state.recommend_id), stock_name),
    model_name = COALESCE((SELECT model_name FROM ai_recommend_stocks WHERE id = ai_recommend_yield_record_state.recommend_id), model_name),
    bk_name = COALESCE((SELECT bk_name FROM ai_recommend_stocks WHERE id = ai_recommend_yield_record_state.recommend_id), bk_name),
    recommend_category = COALESCE((SELECT recommend_category FROM ai_recommend_stocks WHERE id = ai_recommend_yield_record_state.recommend_id), ''),
    recommend_time = COALESCE((SELECT COALESCE(data_time, created_at) FROM ai_recommend_stocks WHERE id = ai_recommend_yield_record_state.recommend_id), recommend_time),
    signal_time = COALESCE((SELECT COALESCE(data_time, created_at) FROM ai_recommend_stocks WHERE id = ai_recommend_yield_record_state.recommend_id), signal_time),
    updated_at = ?
WHERE EXISTS (
    SELECT 1
    FROM ai_recommend_stocks ars
    WHERE ars.id = ai_recommend_yield_record_state.recommend_id
      AND (
        COALESCE(ai_recommend_yield_record_state.stock_code, '') <> COALESCE(ars.stock_code, '')
        OR COALESCE(ai_recommend_yield_record_state.stock_name, '') <> COALESCE(ars.stock_name, '')
        OR COALESCE(ai_recommend_yield_record_state.model_name, '') <> COALESCE(ars.model_name, '')
        OR COALESCE(ai_recommend_yield_record_state.bk_name, '') <> COALESCE(ars.bk_name, '')
        OR COALESCE(ai_recommend_yield_record_state.recommend_category, '') <> COALESCE(ars.recommend_category, '')
        OR COALESCE(ai_recommend_yield_record_state.recommend_time, '') <> COALESCE(COALESCE(ars.data_time, ars.created_at), '')
        OR COALESCE(ai_recommend_yield_record_state.signal_time, '') <> COALESCE(COALESCE(ars.data_time, ars.created_at), '')
      )
)
`, now).Error
}

func resetStaleYieldRecalcIfNeeded(meta *models.AiRecommendYieldMeta) bool {
	if meta == nil || !meta.RecalcInProgress || meta.ID == 0 {
		return false
	}
	last := meta.UpdatedAt
	if last.IsZero() {
		last = time.Now()
	}
	if time.Since(last) < aiRecommendRecalcStaleTTL {
		return false
	}

	now := time.Now()
	err := db.Dao.Model(&models.AiRecommendYieldMeta{}).
		Where("id = ? AND recalc_in_progress = ?", meta.ID, true).
		Updates(map[string]any{
			"recalc_in_progress": false,
			"last_error":         "检测到历史重算任务卡死，已自动解锁并等待重新触发",
			"updated_at":         now,
		}).Error
	if err != nil {
		logger.SugaredLogger.Warnf("reset stale ai_recommend_yield_meta failed: %v", err)
		return false
	}
	meta.RecalcInProgress = false
	meta.UpdatedAt = now
	return true
}

func ResetInterruptedAiRecommendYieldTasksOnStartup() {
	if db.Dao == nil {
		return
	}
	now := time.Now()
	err := db.Dao.Model(&models.AiRecommendYieldMeta{}).
		Where("recalc_in_progress = ? OR download_in_progress = ?", true, true).
		Updates(map[string]any{
			"recalc_in_progress":   false,
			"download_in_progress": false,
			"last_error":           "检测到上次程序退出前收益率任务未正常结束，已在本次启动时自动解锁",
			"last_download_error":  "",
			"updated_at":           now,
		}).Error
	if err != nil {
		if isSQLiteNoSuchTable(err) {
			return
		}
		logger.SugaredLogger.Warnf("reset interrupted ai recommend yield tasks on startup failed: %v", err)
	}
}

func ensureYieldMetaSchema() error {
	if err := db.Dao.AutoMigrate(
		&models.AiRecommendYieldMeta{},
		&models.AiRecommendYieldState{},
		&models.AiRecommendYieldRecordState{},
		&models.AiRecommendYieldDirtyCode{},
		&models.AiRecommendMinuteBar{},
		&models.AiRecommendDailyBar{},
	); err != nil {
		return err
	}
	if err := syncYieldStateIdentityFields(); err != nil {
		return err
	}
	if err := syncYieldRecordStateIdentityFields(); err != nil {
		return err
	}
	return nil
}

func loadYieldScopeCodesForQuery(query *models.AiRecommendStocksQuery) ([]string, error) {
	q := db.Dao.Model(&models.AiRecommendStocks{})

	if query != nil {
		if query.StockCode != "" {
			q = q.Where("stock_code LIKE ?", "%"+query.StockCode+"%")
		}
		if query.StockName != "" {
			q = q.Where("stock_name LIKE ?", "%"+query.StockName+"%")
		}
		if query.BkName != "" {
			q = q.Where("bk_name LIKE ?", "%"+query.BkName+"%")
		}
		if query.ModelName != "" {
			q = q.Where("model_name LIKE ?", "%"+query.ModelName+"%")
		}
		if query.StartDate != "" && query.EndDate != "" {
			startDate := normalizeDateTime(query.StartDate)
			endDate := normalizeDateTime(query.EndDate)
			startTime, err := parseDateTimeWithFallback(startDate)
			if err == nil {
				endTime, endErr := parseDateTimeWithFallback(endDate)
				if endErr == nil {
					q = q.Where("data_time BETWEEN ? AND ?", datetime.BeginOfDay(startTime), datetime.EndOfDay(endTime))
				}
			}
		}
		if query.StartDate != "" && query.EndDate == "" {
			startDate := normalizeDateTime(query.StartDate)
			startTime, err := parseDateTimeWithFallback(startDate)
			if err == nil {
				q = q.Where("data_time BETWEEN ? AND ?", datetime.BeginOfDay(startTime), datetime.EndOfDay(startTime))
			}
		}
	}

	rows := make([]models.AiRecommendStocks, 0)
	if err := q.Select("stock_code", "recommend_category", "recommend_status").Find(&rows).Error; err != nil {
		return nil, err
	}

	set := map[string]struct{}{}
	codes := make([]string, 0, len(rows))
	for _, row := range rows {
		if !shouldTrackRecommendInYield(&row) {
			continue
		}
		code := normalizeRecommendStockCode(row.StockCode)
		if code == "" {
			continue
		}
		if _, ok := set[code]; ok {
			continue
		}
		set[code] = struct{}{}
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes, nil
}

func loadAiRecommendYieldAggregates() (map[string]*aiRecommendYieldAggregate, error) {
	return loadAiRecommendYieldAggregatesAfter(time.Time{})
}

func loadAiRecommendYieldAggregatesAfter(coverableStartMinute time.Time) (map[string]*aiRecommendYieldAggregate, error) {
	var list []models.AiRecommendStocks
	err := db.Dao.Model(&models.AiRecommendStocks{}).
		Order("data_time ASC, created_at ASC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	list, err = applyYieldOverridesToRecommendRecords(list)
	if err != nil {
		return nil, err
	}
	coverableStartMinute = normalizeMinuteTime(coverableStartMinute)

	aggrMap := map[string]*aiRecommendYieldAggregate{}
	for _, item := range list {
		if !shouldTrackRecommendInYield(&item) {
			continue
		}
		if !coverableStartMinute.IsZero() {
			recordTime := recommendRecordTime(item)
			if recordTime.IsZero() {
				continue
			}
			requiredStart := resolveRecommendSellEligibleTime(recordTime)
			if requiredStart.Before(coverableStartMinute) {
				// Outside the minute-data coverable window; skip so the yield snapshot
				// only tracks what AkShare can realistically cover.
				continue
			}
		}

		code := normalizeRecommendStockCode(item.StockCode)
		if code == "" {
			continue
		}
		aggr, exists := aggrMap[code]
		if !exists {
			recordTime := recommendRecordTime(item)
			aggr = &aiRecommendYieldAggregate{
				StockCode:  code,
				StockName:  strings.TrimSpace(item.StockName),
				SignalTime: recordTime,
				BuyTime:    resolveRecommendBuyTime(recordTime),
				BkSet:      map[string]struct{}{},
				ModelSet:   map[string]struct{}{},
				BkNames:    make([]string, 0, 2),
				ModelNames: make([]string, 0, 2),
			}
			aggrMap[code] = aggr
		}

		aggr.RecommendCount++
		if aggr.StockName == "" && strings.TrimSpace(item.StockName) != "" {
			aggr.StockName = strings.TrimSpace(item.StockName)
		}
		recordTime := recommendRecordTime(item)
		if aggr.SignalTime.IsZero() || recordTime.Before(aggr.SignalTime) {
			aggr.SignalTime = recordTime
		}
		buyTime := resolveRecommendBuyTime(recordTime)
		if buyTime.Before(aggr.BuyTime) {
			aggr.BuyTime = buyTime
		}

		if buy, ok := parseBuyPrice(item.StockPrice); ok {
			aggr.BuyAmountSum += buy
			aggr.BuyAmountCount++
		}
		if recommendRequiresPrevDayActivityFilter(item) {
			aggr.RequirePrevDayActivityFilter = true
		}
		if stopProfit, ok := parseStopProfitPrice(item); ok {
			aggr.StopProfitSum += stopProfit
			aggr.StopProfitCount++
		}
		if stopLoss, ok := parseStopLossPrice(item); ok {
			aggr.StopLossSum += stopLoss
			aggr.StopLossCount++
		}

		bkName := strings.TrimSpace(item.BkName)
		if bkName != "" {
			if _, has := aggr.BkSet[bkName]; !has {
				aggr.BkSet[bkName] = struct{}{}
				aggr.BkNames = append(aggr.BkNames, bkName)
			}
		}

		modelName := strings.TrimSpace(item.ModelName)
		if modelName != "" {
			if _, has := aggr.ModelSet[modelName]; !has {
				aggr.ModelSet[modelName] = struct{}{}
				aggr.ModelNames = append(aggr.ModelNames, modelName)
			}
		}
	}

	for _, aggr := range aggrMap {
		sort.Strings(aggr.BkNames)
		sort.Strings(aggr.ModelNames)
	}

	return aggrMap, nil
}

func loadAiRecommendYieldRecordsAfter(coverableStartMinute time.Time) ([]models.AiRecommendStocks, error) {
	records := make([]models.AiRecommendStocks, 0, 128)
	err := db.Dao.Model(&models.AiRecommendStocks{}).
		Order("COALESCE(data_time, created_at) ASC, id ASC").
		Find(&records).Error
	if err != nil {
		return nil, err
	}
	records, err = applyYieldOverridesToRecommendRecords(records)
	if err != nil {
		return nil, err
	}
	coverableStartMinute = normalizeMinuteTime(coverableStartMinute)
	if len(records) == 0 {
		return records, nil
	}
	filtered := make([]models.AiRecommendStocks, 0, len(records))
	for _, rec := range records {
		if !shouldDisplayRecommendInYield(&rec) {
			continue
		}
		if eligibility, _ := resolveRecommendBacktestEligibility(&rec); eligibility != recommendBacktestEligible {
			continue
		}
		recordTime := recommendRecordTime(rec)
		if recordTime.IsZero() {
			continue
		}
		if coverableStartMinute.IsZero() {
			filtered = append(filtered, rec)
			continue
		}
		requiredStart := resolveRecommendSellEligibleTime(recordTime)
		if requiredStart.Before(coverableStartMinute) {
			continue
		}
		filtered = append(filtered, rec)
	}
	return filtered, nil
}

func recommendRecordTime(item models.AiRecommendStocks) time.Time {
	if item.DataTime != nil && !item.DataTime.IsZero() {
		return *item.DataTime
	}
	return item.CreatedAt
}

func loadExistingYieldStateMap() (map[string]*models.AiRecommendYieldState, error) {
	var states []models.AiRecommendYieldState
	err := db.Dao.Model(&models.AiRecommendYieldState{}).Find(&states).Error
	if err != nil {
		return nil, err
	}
	result := make(map[string]*models.AiRecommendYieldState, len(states))
	for i := range states {
		state := states[i]
		state.StockCode = strings.ToUpper(strings.TrimSpace(state.StockCode))
		copied := state
		result[state.StockCode] = &copied
	}
	return result, nil
}

func loadExistingYieldRecordStateMap() (map[uint]*models.AiRecommendYieldRecordState, error) {
	var states []models.AiRecommendYieldRecordState
	err := db.Dao.Model(&models.AiRecommendYieldRecordState{}).Find(&states).Error
	if err != nil {
		return nil, err
	}
	result := make(map[uint]*models.AiRecommendYieldRecordState, len(states))
	for i := range states {
		state := states[i]
		if state.RecommendID == 0 {
			continue
		}
		state.StockCode = strings.ToUpper(strings.TrimSpace(state.StockCode))
		copied := state
		result[state.RecommendID] = &copied
	}
	return result, nil
}

func fetchCurrentPriceMap(aggrMap map[string]*aiRecommendYieldAggregate) (map[string]float64, map[string]string) {
	priceMap := map[string]float64{}
	priceTimeMap := map[string]string{}
	if len(aggrMap) == 0 {
		return priceMap, priceTimeMap
	}

	queryCodes := make([]string, 0, len(aggrMap))
	reverseMap := map[string]string{}
	for code := range aggrMap {
		quoteCode := toQuoteCode(code)
		if quoteCode == "" {
			continue
		}
		queryCodes = append(queryCodes, quoteCode)
		reverseMap[strings.ToLower(quoteCode)] = code
	}
	if len(queryCodes) == 0 {
		return priceMap, priceTimeMap
	}

	stockData, err := NewStockDataApi().GetStockCodeRealTimeData(queryCodes...)
	if err != nil || stockData == nil {
		return priceMap, priceTimeMap
	}
	for _, info := range *stockData {
		key := strings.ToLower(strings.TrimSpace(info.Code))
		normalizedCode := reverseMap[key]
		if normalizedCode == "" {
			continue
		}
		if price, ok := parseBuyPrice(info.Price); ok {
			priceMap[normalizedCode] = round2(price)
		}
		priceTime := strings.TrimSpace(strings.TrimSpace(info.Date) + " " + strings.TrimSpace(info.Time))
		if priceTime != "" {
			priceTimeMap[normalizedCode] = priceTime
		}
	}
	return priceMap, priceTimeMap
}
