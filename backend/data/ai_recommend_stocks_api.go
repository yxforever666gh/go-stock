// Package data ai_recommend_stocks_api.go
package data

import (
	"errors"
	"fmt"
	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/duke-git/lancet/v2/datetime"
	"github.com/duke-git/lancet/v2/slice"
	"github.com/duke-git/lancet/v2/strutil"
	"gorm.io/gorm"
)

type AiRecommendStocksService struct{}

type benchmarkSummaryCacheState struct {
	mu       sync.RWMutex
	key      string
	result   benchmarkSummaryResult
	expireAt time.Time
}

type yieldDailyOverviewCacheState struct {
	mu        sync.RWMutex
	signature string
	data      *models.AiRecommendYieldDailyOverviewData
}

type yieldDailyOverviewEntry struct {
	RecommendID      uint
	StockCode        string
	StockName        string
	BuyTime          time.Time
	BuyDay           time.Time
	SellDay          time.Time
	CurrentDay       time.Time
	BuyAmount        float64
	CurrentPrice     float64
	SellAmount       float64
	HasSellAmount    bool
	BuyCostNet       float64
	RealizedValueNet float64
	CurrentPriceTime string
	SellTime         string
}

type yieldDailyOverviewPriceSeries struct {
	Code       string
	CloseByDay map[string]float64
}

type benchmarkSummaryResult struct {
	Code                      string
	Name                      string
	Rate                      float64
	RateText                  string
	ExcessYieldRate           float64
	ExcessYieldRateText       string
	StrategyXirr              float64
	StrategyXirrText          string
	BenchmarkXirr             float64
	BenchmarkXirrText         string
	ExcessXirr                float64
	ExcessXirrText            string
	MaxDrawdown               float64
	MaxDrawdownText           string
	WinRateVsBenchmark        float64
	WinRateVsBenchmarkText    string
	MedianExcessYieldRate     float64
	MedianExcessYieldRateText string
	ItemRateByRecommendID     map[uint]float64
}

type strategySummaryResult struct {
	StrategyXirr     float64
	StrategyXirrText string
	MaxDrawdown      float64
	MaxDrawdownText  string
}

type benchmarkDailySeries struct {
	Code                  string
	Name                  string
	CloseByDay            map[string]float64
	ValueByDay            map[string]float64
	CumulativeAmountByDay map[string]float64
	DailyAmountByDay      map[string]float64
	CumulativeRateByDay   map[string]float64
	DailyRateByDay        map[string]float64
	NavByDay              map[string]float64
}

type benchmarkCashflowPosition struct {
	RecommendID      uint
	BuyDay           time.Time
	EndDay           time.Time
	EndTime          time.Time
	InvestedNet      float64
	Shares           float64
	SellAmount       float64
	HasSellAmount    bool
	CurrentPrice     float64
	CurrentDay       time.Time
	CurrentPriceTime string
}

type xirrCashflow struct {
	At     time.Time
	Amount float64
}

type aiEvidenceReference struct {
	Type         string `json:"type"`
	Summary      string `json:"summary"`
	SourceName   string `json:"sourceName,omitempty"`
	SourceType   string `json:"sourceType,omitempty"`
	TrustLevel   string `json:"trustLevel,omitempty"`
	LatencyLevel string `json:"latencyLevel,omitempty"`
	Title        string `json:"title,omitempty"`
	URL          string `json:"url,omitempty"`
	PublishedAt  string `json:"publishedAt,omitempty"`
	EntityType   string `json:"entityType,omitempty"`
	EntityCode   string `json:"entityCode,omitempty"`
	DedupeKey    string `json:"dedupeKey,omitempty"`
	RawHash      string `json:"rawHash,omitempty"`
}

var priceNumberRegexp = regexp.MustCompile(`\d+(?:\.\d+)?`)
var evidenceTagRegexp = regexp.MustCompile(`\[([^\]]+)\]`)
var recommendActionKeywordRegexp = regexp.MustCompile(`(?i)(观察|关注|站稳|站上|突破|放量|回踩|承接|企稳|不破|再看|考虑|跟踪|确认|追买|追涨|若|如果|仅观察|先看)`)
var signalDrivenExplicitBuyRangeRegexp = regexp.MustCompile(`(?:激活买入区间|主买入区|买入区间|触发区间|关注区间|进场区间|激活区间)[:：]?\s*(\d+(?:\.\d+)?)\s*(?:-|~|至|到)\s*(\d+(?:\.\d+)?)`)
var signalDrivenMinTriggerPriceRegexp = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*(?:以上|上方)`)
var signalDrivenMaxChasePriceRegexp = regexp.MustCompile(`(?:远离|脱离)\s*(\d+(?:\.\d+)?)`)
var quantifiedThresholdRegexp = regexp.MustCompile(`(?:≥|<=|<=|>=|＞=|＜=|大于等于|小于等于|高于|低于|不少于|不低于|不高于|至少|至多|不超过|倍|%|百分之|连续\d+根|连续两根|连续三根|量比\s*[><=≥≤]?\s*\d+(?:\.\d+)?)`)
var ambiguousTriggerPhraseList = []string{
	"保持强势",
	"主线未失真",
	"结构仍在",
	"不能是缩量拉升",
	"高开过大",
	"不追",
	"冲高失败",
	"放量回落",
	"承接弱",
	"承接不足",
	"量价配合",
	"技术面改善",
}
var recommendObservationSkipPhrases = []string{
	"仅观察",
	"只观察",
	"观望",
	"观察标的",
	"以观察为主",
	"观察为主",
	"只可观察",
	"仅作观察标的",
	"暂不买入",
	"不建议先手",
	"不建议先手硬做",
	"不建议主动追买",
	"不建议追",
	"不宜追",
	"未确认前只观察",
	"未站稳前不建议",
	"不建议当前直接买入",
	"只适合观察",
}
var aiRecommendYieldDailyOverviewCache yieldDailyOverviewCacheState

var globalBenchmarkSummaryCache benchmarkSummaryCacheState

const aiRecommendEqualPositionCapital = 3000.0
const defaultAiRecommendSummaryVersion = "phase2-v1"
const recommendPendingActivationMaxTradeDays = 5
const benchmarkSummaryCalcTimeout = 6 * time.Second
const benchmarkSummaryCacheTTL = 5 * time.Minute
const defaultBenchmarkCode = "sh510300"
const defaultBenchmarkModelCode = "510300.SH"
const defaultBenchmarkName = "沪深300ETF（510300.SH，现金流匹配，已扣成本）"
const recommendKeywordInterceptionBypassDate = "2026-04-07"

const (
	recommendBacktestEligible   = "eligible"
	recommendBacktestIneligible = "ineligible"
	recommendBacktestSkipped    = "skipped"
	aiRecommendYieldModeFast    = "fast"
	aiRecommendYieldModeStrict  = "strict"
)

func NewAiRecommendStocksService() *AiRecommendStocksService {
	return &AiRecommendStocksService{}
}

func recommendKeywordInterceptionBypassStart() time.Time {
	loc := cnLocation()
	start, err := time.ParseInLocation("2006-01-02", recommendKeywordInterceptionBypassDate, loc)
	if err != nil {
		return time.Date(2026, 4, 7, 0, 0, 0, 0, loc)
	}
	return start
}

func shouldBypassRecommendKeywordInterceptionAt(at time.Time) bool {
	if at.IsZero() {
		return false
	}
	return !at.In(cnLocation()).Before(recommendKeywordInterceptionBypassStart())
}

func shouldBypassRecommendKeywordInterception(dataTime *time.Time) bool {
	if dataTime == nil {
		return false
	}
	return shouldBypassRecommendKeywordInterceptionAt(*dataTime)
}

// GetAiRecommendStocksList 分页查询AI推荐股票记录
func (s *AiRecommendStocksService) GetAiRecommendStocksList(query *models.AiRecommendStocksQuery) (*models.AiRecommendStocksPageData, error) {
	if query == nil {
		query = &models.AiRecommendStocksQuery{}
	}
	query.StrategyCohort = normalizeStrategyCohort(query.StrategyCohort, strategyCohortAll)

	var rawList []models.AiRecommendStocks

	q := applyStrategyCohortFilter(db.Dao.Model(&models.AiRecommendStocks{}), query.StrategyCohort)
	loc := cnLocation()

	// 构建查询条件
	if query.StockCode != "" {
		q.Or("stock_code LIKE ?", "%"+query.StockCode+"%")
	}
	if query.StockName != "" {
		q.Or("stock_name LIKE ?", "%"+query.StockName+"%")
	}
	if query.BkCode != "" {
		q.Or("bk_code LIKE ?", "%"+query.BkCode+"%")
	}
	if query.BkName != "" {
		q.Or("bk_name LIKE ?", "%"+query.BkName+"%")
	}
	if query.ModelName != "" {
		q.Or("model_name LIKE ?", "%"+query.ModelName+"%")
		q.Or("provider_name LIKE ?", "%"+query.ModelName+"%")
	}

	if query.StartDate != "" && query.EndDate != "" {
		query.StartDate = strutil.ReplaceWithMap(query.StartDate, map[string]string{
			"T": " ",
			"Z": "",
		})
		query.EndDate = strutil.ReplaceWithMap(query.EndDate, map[string]string{
			"T": " ",
			"Z": "",
		})
		startDate, err := time.ParseInLocation("2006-01-02 15:04:05", query.StartDate, loc)
		if err != nil {
			startDate, _ = time.ParseInLocation("2006-01-02", query.StartDate, loc)
		}

		endDate, err := time.ParseInLocation("2006-01-02 15:04:05", query.EndDate, loc)
		if err != nil {
			endDate, _ = time.ParseInLocation("2006-01-02", query.EndDate, loc)
		}

		q.Where("data_time BETWEEN ? AND ?", datetime.BeginOfDay(startDate), datetime.EndOfDay(endDate))
	}
	if query.StartDate == "" && query.EndDate == "" {
		now := time.Now().In(loc)
		q.Where("data_time BETWEEN ? AND ?", datetime.BeginOfDay(now), datetime.EndOfDay(now))
	}

	if query.StartDate != "" && query.EndDate == "" {
		query.StartDate = strutil.ReplaceWithMap(query.StartDate, map[string]string{
			"T": " ",
			"Z": "",
		})
		startDate, _ := time.ParseInLocation("2006-01-02", query.StartDate, loc)
		q.Where("data_time BETWEEN ? AND ?", datetime.BeginOfDay(startDate), datetime.EndOfDay(startDate))
	}

	// 设置默认分页参数
	page := query.Page
	pageSize := query.PageSize
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 10
	}

	err := q.Order("created_at DESC").Find(&rawList).Error
	if err != nil {
		return nil, err
	}
	list := collapseRecommendRecordsSameDayByCode(rawList)
	total := int64(len(list))

	totalPages := int((total + int64(pageSize) - 1) / int64(pageSize))
	offset := (page - 1) * pageSize
	if offset < 0 {
		offset = 0
	}
	if offset > len(list) {
		offset = len(list)
	}
	end := offset + pageSize
	if end > len(list) {
		end = len(list)
	}
	pageList := list[offset:end]

	stockCodes := slice.Map(pageList, func(index int, item models.AiRecommendStocks) string {
		return ConvertTushareCodeToStockCode(item.StockCode)
	})
	stockData, _ := NewStockDataApi().GetStockCodeRealTimeData(stockCodes...)
	for _, info := range *stockData {
		for idx, item := range pageList {
			if ConvertTushareCodeToStockCode(item.StockCode) == ConvertTushareCodeToStockCode(info.Code) {
				pageList[idx].StockCurrentPrice = info.Price
				pageList[idx].StockPrePrice = info.PreClose
				pageList[idx].StockCurrentPriceTime = info.Date + " " + info.Time
			}
		}
	}
	for idx := range pageList {
		repairRecommendBuyRangeFromSignals(&pageList[idx])
		sanitizeAiRecommendStockForDisplay(&pageList[idx])
	}
	if latestReviewMap, err := loadLatestOpeningReviewSummaryMap(slice.Map(pageList, func(index int, item models.AiRecommendStocks) uint {
		return item.ID
	})); err == nil {
		for idx := range pageList {
			pageList[idx].LatestOpeningReview = latestReviewMap[pageList[idx].ID]
		}
	}

	return &models.AiRecommendStocksPageData{
		List:           pageList,
		Total:          total,
		Page:           page,
		PageSize:       pageSize,
		TotalPages:     totalPages,
		StrategyCohort: query.StrategyCohort,
	}, nil
}

// GetAiRecommendStocksDateRange 获取推荐记录日期范围（最早记录日至最新记录日）
func (s *AiRecommendStocksService) GetAiRecommendStocksDateRange() (string, string, error) {
	type dateRange struct {
		StartDate *time.Time `gorm:"column:start_date"`
		EndDate   *time.Time `gorm:"column:end_date"`
	}
	var result dateRange
	err := db.Dao.Model(&models.AiRecommendStocks{}).
		Select("MIN(COALESCE(data_time, created_at)) AS start_date, MAX(COALESCE(data_time, created_at)) AS end_date").
		Scan(&result).Error
	if err != nil {
		return "", "", err
	}
	if result.StartDate == nil || result.EndDate == nil {
		return "", "", nil
	}
	return result.StartDate.Format("2006-01-02"), result.EndDate.Format("2006-01-02"), nil
}

func parseYieldTradeDate(raw string) (time.Time, bool) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return time.Time{}, false
	}
	loc := cnLocation()
	t, err := time.ParseInLocation("2006-01-02", text, loc)
	if err != nil {
		return time.Time{}, false
	}
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc), true
}

func resolveExpectedYieldTradeDate(now time.Time) time.Time {
	loc := cnLocation()
	cur := now.In(loc)
	day := time.Date(cur.Year(), cur.Month(), cur.Day(), 0, 0, 0, 0, loc)
	if isCNOpenTradeDay(day) {
		return day
	}
	return shiftToPrevCNOpenTradeDay(day)
}

func shouldTriggerYieldQueryRecalc(meta *models.AiRecommendYieldMeta, expectedTradeDate, now time.Time) bool {
	if meta == nil || meta.ID == 0 || meta.RecalcInProgress || expectedTradeDate.IsZero() {
		return false
	}
	if meta.QueryCooldownUntil != nil && meta.QueryCooldownUntil.After(now) {
		return false
	}
	currentTradeDate, ok := parseYieldTradeDate(meta.CurrentTradeDate)
	if !ok {
		return true
	}
	return currentTradeDate.Before(expectedTradeDate)
}

func triggerYieldQueryRecalcIfStale(meta *models.AiRecommendYieldMeta, expectedTradeDate, now time.Time) bool {
	if !shouldTriggerYieldQueryRecalc(meta, expectedTradeDate, now) {
		return false
	}
	cooldownUntil := now.Add(aiRecommendQueryRecalcCooldown)
	update := map[string]any{
		"last_query_recalc_at": now,
		"query_cooldown_until": cooldownUntil,
		"updated_at":           now,
	}
	result := db.Dao.Model(&models.AiRecommendYieldMeta{}).
		Where("id = ? AND recalc_in_progress = ?", meta.ID, false).
		Where("(query_cooldown_until IS NULL OR query_cooldown_until <= ?)", now).
		Updates(update)
	if result.Error != nil {
		logger.SugaredLogger.Warnf("mark query recalc trigger failed: %v", result.Error)
		return false
	}
	if result.RowsAffected == 0 {
		return false
	}
	meta.LastQueryRecalcAt = &now
	meta.QueryCooldownUntil = &cooldownUntil
	requestAiRecommendYieldRecalcForQueryFn(true, "query_stale_trade_date")
	return true
}

func collectYieldPendingIntradayRecalcScope(
	now time.Time,
	latestTradeDate time.Time,
	records []models.AiRecommendStocks,
	recordStateMap map[uint]models.AiRecommendYieldRecordState,
) []string {
	if !isCNTradingSession(now) || latestTradeDate.IsZero() || len(records) == 0 {
		return nil
	}
	scopeSet := make(map[string]struct{}, len(records))
	for _, rec := range records {
		code := normalizeRecommendStockCode(rec.StockCode)
		if code == "" {
			continue
		}
		recordTime := recommendRecordTime(rec)
		if recordTime.IsZero() {
			continue
		}
		recordDay := time.Date(
			recordTime.In(cnLocation()).Year(),
			recordTime.In(cnLocation()).Month(),
			recordTime.In(cnLocation()).Day(),
			0, 0, 0, 0,
			cnLocation(),
		)
		if state, ok := recordStateMap[rec.ID]; ok {
			if strings.TrimSpace(state.ActivationStatus) != "pending" {
				continue
			}
			if state.LastRecalcAt == nil || now.Sub(*state.LastRecalcAt) >= 15*time.Minute {
				scopeSet[code] = struct{}{}
			}
			continue
		}
		if !recordDay.After(latestTradeDate) {
			scopeSet[code] = struct{}{}
		}
	}
	if len(scopeSet) == 0 {
		return nil
	}
	scopeCodes := make([]string, 0, len(scopeSet))
	for code := range scopeSet {
		scopeCodes = append(scopeCodes, code)
	}
	sort.Strings(scopeCodes)
	return scopeCodes
}

func triggerYieldPendingIntradayRecalcIfStale(
	meta *models.AiRecommendYieldMeta,
	now time.Time,
	latestTradeDate time.Time,
	records []models.AiRecommendStocks,
	recordStateMap map[uint]models.AiRecommendYieldRecordState,
) bool {
	if meta == nil || meta.ID == 0 || meta.RecalcInProgress {
		return false
	}
	if meta.QueryCooldownUntil != nil && meta.QueryCooldownUntil.After(now) {
		return false
	}
	scopeCodes := collectYieldPendingIntradayRecalcScope(now, latestTradeDate, records, recordStateMap)
	if len(scopeCodes) == 0 {
		return false
	}
	cooldownUntil := now.Add(aiRecommendQueryRecalcCooldown)
	update := map[string]any{
		"last_query_recalc_at": now,
		"query_cooldown_until": cooldownUntil,
		"updated_at":           now,
	}
	result := db.Dao.Model(&models.AiRecommendYieldMeta{}).
		Where("id = ? AND recalc_in_progress = ?", meta.ID, false).
		Where("(query_cooldown_until IS NULL OR query_cooldown_until <= ?)", now).
		Updates(update)
	if result.Error != nil {
		logger.SugaredLogger.Warnf("mark pending intraday recalc trigger failed: %v", result.Error)
		return false
	}
	if result.RowsAffected == 0 {
		return false
	}
	meta.LastQueryRecalcAt = &now
	meta.QueryCooldownUntil = &cooldownUntil
	requestAiRecommendYieldScopedRecalcForQueryFn(false, "query_pending_intraday", scopeCodes)
	return true
}

// GetAiRecommendStocksYieldList 聚合查询AI推荐股票收益率
func (s *AiRecommendStocksService) GetAiRecommendStocksYieldList(query *models.AiRecommendStocksQuery) (*models.AiRecommendStocksYieldPageData, error) {
	if query == nil {
		query = &models.AiRecommendStocksQuery{}
	}
	query.StrategyCohort = normalizeStrategyCohort(query.StrategyCohort, strategyCohortCurrent)

	EnsureDiemengSelfCheckAsync("yield_list")
	if err := ensureYieldMetaSchema(); err != nil {
		return nil, err
	}
	page := query.Page
	pageSize := query.PageSize
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 100
	}
	yieldMode := resolveAiRecommendYieldMode(query)

	meta := models.AiRecommendYieldMeta{}
	dataAsOf := ""
	recalcInProgress := false
	recalcProgress := 0
	downloadInProgress := false
	downloadProgress := 0
	downloadDone := 0
	downloadTotal := 0
	manualCooldownUntil := ""
	manualCooldownRemainSec := 0
	diemengHealthStatus := ""
	diemengHealthSummary := ""
	diemengHealthCheckedAt := ""
	minuteDone := 0
	minuteTotal := 0
	minutePending := 0
	minuteUncoverable := 0
	coverageIssues := make([]minuteCoverageIssue, 0, 32)
	loc := cnLocation()
	now := timeNow().In(loc)
	expectedTradeDate := resolveExpectedYieldTradeDate(now)
	latestTradeDate := expectedTradeDate
	queryTriggeredRecalc := false
	lastManualStartedAt := ""
	lastManualFinishedAt := ""
	lastManualScopeCount := 0
	lastManualPrefetchMs := int64(0)
	lastManualRecalcMs := int64(0)
	lastManualTotalMs := int64(0)
	lastManualSqliteBusyCount := 0
	lastManualProviderSummary := ""
	lastManualAuditReady := false
	diemengHealthStatus, diemengHealthSummary, diemengHealthCheckedAt = GetDiemengSelfCheckView()
	metaPtr := (*models.AiRecommendYieldMeta)(nil)
	if err := db.Dao.Model(&models.AiRecommendYieldMeta{}).First(&meta).Error; err == nil {
		metaPtr = &meta
		if resetStaleYieldRecalcIfNeeded(&meta) {
			requestAiRecommendYieldRecalc(true, "recover_stale_recalc")
		}
		recalcInProgress = meta.RecalcInProgress
		recalcProgress = meta.RecalcProgress
		downloadInProgress = meta.DownloadInProgress
		downloadProgress = meta.DownloadProgress
		downloadDone = meta.DownloadDone
		downloadTotal = meta.DownloadTotal
		if meta.LastFullRecalcAt != nil {
			dataAsOf = meta.LastFullRecalcAt.Format("2006-01-02 15:04:05")
		}
		if meta.LastManualDownloadAt != nil {
			lastManualStartedAt = meta.LastManualDownloadAt.Format("2006-01-02 15:04:05")
		}
		if meta.LastManualFinishedAt != nil {
			lastManualFinishedAt = meta.LastManualFinishedAt.Format("2006-01-02 15:04:05")
		}
		lastManualScopeCount = meta.LastManualScopeCount
		lastManualPrefetchMs = meta.LastManualPrefetchMs
		lastManualRecalcMs = meta.LastManualRecalcMs
		lastManualTotalMs = meta.LastManualTotalMs
		lastManualSqliteBusyCount = meta.LastManualSqliteBusyCount
		lastManualProviderSummary = strings.TrimSpace(meta.LastManualProviderSummary)
		lastManualAuditReady = meta.LastManualFinishedAt != nil
		manualCooldownUntil, manualCooldownRemainSec = resolveManualCooldownInfo(meta.ManualCooldownUntil)
		stats, issues := computeMinuteDownloadCoverageStatsWithIssues(&meta, -1)
		minuteDone, minuteTotal, minutePending, minuteUncoverable = stats.Done, stats.Total, stats.Pending, stats.Uncoverable
		coverageIssues = issues
		if t, ok := parseYieldTradeDate(meta.CurrentTradeDate); ok {
			latestTradeDate = t
		}
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		logger.SugaredLogger.Warnf("load ai_recommend_yield_meta failed: %v", err)
	}
	if metaPtr == nil {
		if createdMeta, createErr := getOrCreateYieldMeta(); createErr == nil {
			meta = *createdMeta
			metaPtr = &meta
		} else {
			logger.SugaredLogger.Warnf("getOrCreateYieldMeta failed: %v", createErr)
		}
	}
	if expectedTradeDate.After(latestTradeDate) {
		latestTradeDate = expectedTradeDate
	}
	if metaPtr != nil && triggerYieldQueryRecalcIfStale(metaPtr, expectedTradeDate, now) {
		queryTriggeredRecalc = true
		recalcInProgress = true
		if recalcProgress < 0 {
			recalcProgress = 0
		}
	}
	if minuteTotal <= 0 {
		stats, issues := computeMinuteDownloadCoverageStatsWithIssues(nil, -1)
		minuteDone, minuteTotal, minutePending, minuteUncoverable = stats.Done, stats.Total, stats.Pending, stats.Uncoverable
		coverageIssues = issues
	}
	if recalcProgress < 0 {
		recalcProgress = 0
	}
	if recalcProgress > 100 {
		recalcProgress = 100
	}
	if queryTriggeredRecalc && minutePending == 0 && minuteTotal > minuteDone {
		minutePending = minuteTotal - minuteDone
	}

	latestTradeDate = time.Date(latestTradeDate.Year(), latestTradeDate.Month(), latestTradeDate.Day(), 0, 0, 0, 0, loc)
	coverableStart := minuteCoverableStartMinute(latestTradeDate)
	recordCoverableStart := coverableStart
	if yieldMode == aiRecommendYieldModeFast {
		recordCoverableStart = time.Time{}
	}

	records, err := listAiRecommendStocksForYield(query, recordCoverableStart)
	if err != nil {
		return nil, err
	}
	rawRepeatCountMap := countRecommendOccurrencesByCode(records)
	records = collapseRecommendRecordsSameDayByCode(records)
	if err := markInvalidActivationExitPlanDirtyCodes(aiRecommendYieldModeStrict); err != nil {
		logger.SugaredLogger.Warnf("mark invalid activation exit plan dirty codes failed: %v", err)
	}
	if err := markActivationWindowPolicyBugDirtyCodes(aiRecommendYieldModeStrict); err != nil {
		logger.SugaredLogger.Warnf("mark activation window policy bug dirty codes failed: %v", err)
	}
	dirtyMap, err := loadDirtyAiRecommendYieldCodeSet(aiRecommendYieldModeStrict)
	if err != nil {
		return nil, err
	}

	if len(records) == 0 {
		if yieldMode == aiRecommendYieldModeStrict {
			fallbackPage, fallbackErr := s.buildYieldFallbackPage(
				query,
				page,
				pageSize,
				dataAsOf,
				recalcInProgress,
				recalcProgress,
				manualCooldownUntil,
				manualCooldownRemainSec,
				coverableStart,
			)
			if fallbackErr == nil && fallbackPage != nil && fallbackPage.Total > 0 {
				fallbackPage.CalcMode = yieldMode
				return fallbackPage, nil
			}
		}
		return &models.AiRecommendStocksYieldPageData{
			List:                      []models.AiRecommendStocksYieldItem{},
			Total:                     0,
			Page:                      page,
			PageSize:                  pageSize,
			TotalPages:                0,
			CalcMode:                  yieldMode,
			StrategyCohort:            query.StrategyCohort,
			TotalYieldRate:            0,
			TotalYieldRateText:        "--",
			BenchmarkCode:             defaultBenchmarkModelCode,
			BenchmarkName:             defaultBenchmarkName,
			BenchmarkRate:             0,
			BenchmarkRateText:         "--",
			ExcessYieldRate:           0,
			ExcessYieldRateText:       "--",
			StrategyXirr:              0,
			StrategyXirrText:          "--",
			BenchmarkXirr:             0,
			BenchmarkXirrText:         "--",
			ExcessXirr:                0,
			ExcessXirrText:            "--",
			MaxDrawdown:               0,
			MaxDrawdownText:           "--",
			WinRateVsBenchmark:        0,
			WinRateVsBenchmarkText:    "--",
			MedianExcessYieldRate:     0,
			MedianExcessYieldRateText: "--",
			DataAsOf:                  dataAsOf,
			RecalcInProgress:          recalcInProgress,
			RecalcProgress:            recalcProgress,
			DownloadInProgress:        downloadInProgress,
			DownloadProgress:          downloadProgress,
			DownloadDone:              downloadDone,
			DownloadTotal:             downloadTotal,
			MinuteDownloadDone:        minuteDone,
			MinuteDownloadTotal:       minuteTotal,
			MinuteDownloadPending:     minutePending,
			MinuteDownloadUncoverable: minuteUncoverable,
			ManualCooldownUntil:       manualCooldownUntil,
			ManualCooldownRemainSec:   manualCooldownRemainSec,
			LastManualStartedAt:       lastManualStartedAt,
			LastManualFinishedAt:      lastManualFinishedAt,
			LastManualScopeCount:      lastManualScopeCount,
			LastManualPrefetchMs:      lastManualPrefetchMs,
			LastManualRecalcMs:        lastManualRecalcMs,
			LastManualTotalMs:         lastManualTotalMs,
			LastManualSqliteBusyCount: lastManualSqliteBusyCount,
			LastManualProviderSummary: lastManualProviderSummary,
			LastManualAuditReady:      lastManualAuditReady,
			DiemengHealthStatus:       diemengHealthStatus,
			DiemengHealthSummary:      diemengHealthSummary,
			DiemengHealthCheckedAt:    diemengHealthCheckedAt,
		}, nil
	}

	if yieldMode == aiRecommendYieldModeFast {
		return s.buildFastYieldPage(
			query,
			records,
			page,
			pageSize,
			dataAsOf,
			recalcInProgress,
			recalcProgress,
			manualCooldownUntil,
			manualCooldownRemainSec,
			minuteDone,
			minuteTotal,
			minutePending,
			minuteUncoverable,
			dirtyMap,
			rawRepeatCountMap,
		)
	}

	recordStateMap, err := loadYieldRecordStateMapByRecommendRecords(records)
	if err != nil {
		return nil, err
	}
	if metaPtr != nil && triggerYieldPendingIntradayRecalcIfStale(metaPtr, now, latestTradeDate, records, recordStateMap) {
		recalcInProgress = true
		if recalcProgress < 0 {
			recalcProgress = 0
		}
	}
	stateMap, err := loadYieldStateMapByRecommendRecords(records)
	if err != nil {
		return nil, err
	}
	overrideMap, err := loadYieldOverrideMapByRecommendRecords(records)
	if err != nil {
		return nil, err
	}
	items := buildStrictYieldRecordItems(records, recordStateMap, stateMap, overrideMap, dirtyMap, coverageIssues)
	latestPriceMap, latestPriceTimeMap := loadCurrentPriceSnapshotForRecommendRecords(records)
	applyLatestCurrentPriceSnapshot(items, latestPriceMap, latestPriceTimeMap)
	applyRecommendRepeatCountByCodeMap(items, rawRepeatCountMap)
	diagnostics := calculateYieldDiagnosticSummary(records, items)

	totalYieldRate, totalYieldRateText := calculateYieldTotalByItems(items)
	benchmarkSummary := calculateBenchmarkSummaryByItems(items)
	total := int64(len(items))
	totalPages := int((total + int64(pageSize) - 1) / int64(pageSize))
	offset := (page - 1) * pageSize
	if offset < 0 {
		offset = 0
	}
	if offset > len(items) {
		offset = len(items)
	}
	end := offset + pageSize
	if end > len(items) {
		end = len(items)
	}
	pageItems := items[offset:end]
	if latestReviewMap, reviewErr := loadLatestOpeningReviewSummaryMap(slice.Map(pageItems, func(index int, item models.AiRecommendStocksYieldItem) uint {
		return item.RecommendID
	})); reviewErr == nil {
		for idx := range pageItems {
			pageItems[idx].LatestOpeningReview = latestReviewMap[pageItems[idx].RecommendID]
		}
	}

	return &models.AiRecommendStocksYieldPageData{
		List:                       pageItems,
		Total:                      total,
		Page:                       page,
		PageSize:                   pageSize,
		TotalPages:                 totalPages,
		CalcMode:                   aiRecommendYieldModeStrict,
		StrategyCohort:             query.StrategyCohort,
		TotalYieldRate:             totalYieldRate,
		TotalYieldRateText:         totalYieldRateText,
		BenchmarkCode:              benchmarkSummary.Code,
		BenchmarkName:              benchmarkSummary.Name,
		BenchmarkRate:              benchmarkSummary.Rate,
		BenchmarkRateText:          benchmarkSummary.RateText,
		ExcessYieldRate:            benchmarkSummary.ExcessYieldRate,
		ExcessYieldRateText:        benchmarkSummary.ExcessYieldRateText,
		StrategyXirr:               benchmarkSummary.StrategyXirr,
		StrategyXirrText:           benchmarkSummary.StrategyXirrText,
		BenchmarkXirr:              benchmarkSummary.BenchmarkXirr,
		BenchmarkXirrText:          benchmarkSummary.BenchmarkXirrText,
		ExcessXirr:                 benchmarkSummary.ExcessXirr,
		ExcessXirrText:             benchmarkSummary.ExcessXirrText,
		MaxDrawdown:                benchmarkSummary.MaxDrawdown,
		MaxDrawdownText:            benchmarkSummary.MaxDrawdownText,
		WinRateVsBenchmark:         benchmarkSummary.WinRateVsBenchmark,
		WinRateVsBenchmarkText:     benchmarkSummary.WinRateVsBenchmarkText,
		MedianExcessYieldRate:      benchmarkSummary.MedianExcessYieldRate,
		MedianExcessYieldRateText:  benchmarkSummary.MedianExcessYieldRateText,
		SameDayActivationRate:      diagnostics.SameDayActivationRate,
		SameDayActivationRateText:  diagnostics.SameDayActivationRateText,
		StaleActivationRate:        diagnostics.StaleActivationRate,
		StaleActivationRateText:    diagnostics.StaleActivationRateText,
		StructuredRuleCoverage:     diagnostics.StructuredRuleCoverage,
		StructuredRuleCoverageText: diagnostics.StructuredRuleCoverageText,
		AnalysisOnlyRate:           diagnostics.AnalysisOnlyRate,
		AnalysisOnlyRateText:       diagnostics.AnalysisOnlyRateText,
		StopLossCount:              diagnostics.StopLossCount,
		TakeProfitCount:            diagnostics.TakeProfitCount,
		OpenCount:                  diagnostics.OpenCount,
		V132GateBlockedCount:       diagnostics.V132GateBlockedCount,
		V132StrengthBlockedCount:   diagnostics.V132StrengthBlockedCount,
		V132RewardRiskBlockedCount: diagnostics.V132RewardRiskBlockedCount,
		V132CooldownBlockedCount:   diagnostics.V132CooldownBlockedCount,
		DataAsOf:                   dataAsOf,
		RecalcInProgress:           recalcInProgress,
		RecalcProgress:             recalcProgress,
		DownloadInProgress:         downloadInProgress,
		DownloadProgress:           downloadProgress,
		DownloadDone:               downloadDone,
		DownloadTotal:              downloadTotal,
		MinuteDownloadDone:         minuteDone,
		MinuteDownloadTotal:        minuteTotal,
		MinuteDownloadPending:      minutePending,
		MinuteDownloadUncoverable:  minuteUncoverable,
		ManualCooldownUntil:        manualCooldownUntil,
		ManualCooldownRemainSec:    manualCooldownRemainSec,
		LastManualStartedAt:        lastManualStartedAt,
		LastManualFinishedAt:       lastManualFinishedAt,
		LastManualScopeCount:       lastManualScopeCount,
		LastManualPrefetchMs:       lastManualPrefetchMs,
		LastManualRecalcMs:         lastManualRecalcMs,
		LastManualTotalMs:          lastManualTotalMs,
		LastManualSqliteBusyCount:  lastManualSqliteBusyCount,
		LastManualProviderSummary:  lastManualProviderSummary,
		LastManualAuditReady:       lastManualAuditReady,
		DiemengHealthStatus:        diemengHealthStatus,
		DiemengHealthSummary:       diemengHealthSummary,
		DiemengHealthCheckedAt:     diemengHealthCheckedAt,
	}, nil
}

func (s *AiRecommendStocksService) GetAiRecommendYieldMinuteChart(recommendID uint) (*models.AiRecommendYieldMinuteChartData, error) {
	if recommendID == 0 {
		return nil, errors.New("recommend id 不能为空")
	}

	rec := models.AiRecommendStocks{}
	if err := db.Dao.Model(&models.AiRecommendStocks{}).First(&rec, recommendID).Error; err != nil {
		return nil, err
	}
	if recs, err := applyYieldOverridesToRecommendRecords([]models.AiRecommendStocks{rec}); err != nil {
		return nil, err
	} else if len(recs) > 0 {
		rec = recs[0]
	}

	var recordState *models.AiRecommendYieldRecordState
	state := models.AiRecommendYieldRecordState{}
	if err := db.Dao.Model(&models.AiRecommendYieldRecordState{}).Where("recommend_id = ?", recommendID).First(&state).Error; err == nil {
		recordState = &state
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) && !isSQLiteNoSuchTable(err) {
		return nil, err
	}

	stateMap, err := loadYieldStateMapByRecommendRecords([]models.AiRecommendStocks{rec})
	if err != nil {
		return nil, err
	}
	recordStateMap := map[uint]models.AiRecommendYieldRecordState{}
	if recordState != nil {
		recordStateMap[recommendID] = *recordState
	}
	item := mapRecommendRecordToYieldItemWithRecordState(rec, recordStateMap, stateMap)
	if overrideMap, err := loadYieldOverrideMapByRecommendIDs([]uint{recommendID}); err != nil {
		return nil, err
	} else if override, ok := overrideMap[recommendID]; ok {
		applyYieldOverrideToYieldItem(&item, &override)
	}

	code := normalizeRecommendStockCode(rec.StockCode)
	if code == "" {
		code = strings.TrimSpace(item.StockCode)
	}

	chart := &models.AiRecommendYieldMinuteChartData{
		RecommendID:      recommendID,
		StockCode:        code,
		StockName:        strings.TrimSpace(item.StockName),
		SignalTime:       strings.TrimSpace(item.SignalTime),
		BuyTime:          strings.TrimSpace(item.BuyTime),
		SellTime:         strings.TrimSpace(item.SellTime),
		CurrentPrice:     round2(item.CurrentPrice),
		CurrentPriceTime: strings.TrimSpace(item.CurrentPriceTime),
		ActivationStatus: strings.TrimSpace(item.ActivationStatus),
		PositionStatus:   strings.TrimSpace(item.PositionStatus),
		DataStatus:       strings.TrimSpace(item.DataStatus),
		DataStatusReason: strings.TrimSpace(item.DataStatusReason),
		ChartStatus:      "missing",
		Bars:             []models.AiRecommendYieldMinuteBarDTO{},
		Markers:          []models.AiRecommendYieldChartMarker{},
	}
	if latestReviewMap, reviewErr := loadLatestOpeningReviewSummaryMap([]uint{recommendID}); reviewErr == nil {
		chart.LatestOpeningReview = latestReviewMap[recommendID]
	}
	if chart.StockName == "" {
		chart.StockName = strings.TrimSpace(rec.StockName)
	}

	signalAt := resolveYieldReplaySignalTime(rec, recordState)
	if signalAt.IsZero() {
		chart.ChartStatus = "unsupported"
		chart.Message = "缺少信号时间，无法绘制分钟回放"
		return chart, nil
	}

	rangeEnd, endReason := resolveYieldReplayRangeEnd(item, recordState)
	if rangeEnd.IsZero() {
		chart.ChartStatus = "unsupported"
		chart.RangeStart = formatYieldDisplayTime(signalAt)
		chart.RangeEnd = ""
		chart.RangeLabel = "信号后暂无可回放终点"
		chart.Message = endReason
		return chart, nil
	}

	coreRangeStart := normalizeMinuteTime(signalAt.In(cnLocation()))
	coreRangeEnd := normalizeMinuteTime(rangeEnd.In(cnLocation()))
	rangeStart, rangeEnd := expandYieldReplayQueryWindow(signalAt, rangeEnd, isYieldReplayHolding(item, recordState))
	if rangeEnd.Before(rangeStart) {
		chart.ChartStatus = "unsupported"
		chart.RangeStart = formatYieldDisplayTime(rangeStart)
		chart.RangeEnd = formatYieldDisplayTime(rangeEnd)
		chart.RangeLabel = chart.RangeStart + " -> " + chart.RangeEnd
		chart.Message = "回放终点早于起点，当前记录无法绘制"
		return chart, nil
	}

	queryEnd := rangeEnd
	if !rangeStart.Before(queryEnd) {
		queryEnd = rangeEnd.Add(time.Minute)
	}
	bars, err := listMinuteBarsFromCache(code, rangeStart, queryEnd)
	if err != nil {
		return nil, err
	}

	chart.RangeStart = formatYieldDisplayTime(rangeStart)
	chart.RangeEnd = formatYieldDisplayTime(rangeEnd)
	chart.RangeLabel = chart.RangeStart + " -> " + chart.RangeEnd

	messages := make([]string, 0, 6)
	appendYieldReplayMessage(&messages, replayChartStatusHint(chart.DataStatus, chart.DataStatusReason))

	if len(bars) == 0 {
		chart.ChartStatus = "missing"
		appendYieldReplayMessage(&messages, "目标时间段内没有可用分钟线，请先手动下载分钟线")
		chart.Message = strings.Join(messages, "；")
		return chart, nil
	}

	chart.ChartStatus = "ready"
	firstBarTime := normalizeMinuteTime(bars[0].TradeTime.In(cnLocation()))
	lastBarTime := normalizeMinuteTime(bars[len(bars)-1].TradeTime.In(cnLocation()))
	if firstBarTime.After(coreRangeStart) {
		chart.ChartStatus = "partial"
		appendYieldReplayMessage(&messages, fmt.Sprintf("核心区间起点缺口：首根分钟线从 %s 开始", formatYieldDisplayTime(firstBarTime)))
	}
	if lastBarTime.Before(coreRangeEnd) {
		chart.ChartStatus = "partial"
		appendYieldReplayMessage(&messages, fmt.Sprintf("核心区间终点缺口：最新分钟线只到 %s", formatYieldDisplayTime(lastBarTime)))
	}

	chart.Bars = buildYieldReplayBars(bars)
	markers, markerStatus, markerMessages := buildYieldReplayMarkers(bars, signalAt, item, recordState)
	chart.Markers = markers
	if markerStatus == "partial" && chart.ChartStatus == "ready" {
		chart.ChartStatus = "partial"
	}
	for _, msg := range markerMessages {
		appendYieldReplayMessage(&messages, msg)
	}

	chart.Message = strings.Join(messages, "；")
	return chart, nil
}

type minuteCoverageStats struct {
	Done        int
	Total       int
	Pending     int
	Uncoverable int
}

type minuteCoverageIssue struct {
	RowKey       string
	RecordID     uint
	RecordTime   time.Time
	StockCode    string
	StockName    string
	Status       string
	RawReason    string
	IssueKind    string
	MissingStart time.Time
	MissingEnd   time.Time
}

func applyRecommendRepeatCount(items []models.AiRecommendStocksYieldItem) {
	if len(items) == 0 {
		return
	}
	counts := make(map[string]int, len(items))
	for i := range items {
		code := strings.ToUpper(strings.TrimSpace(items[i].StockCode))
		if code == "" {
			code = strings.TrimSpace(items[i].StockName)
		}
		if code == "" {
			continue
		}
		counts[code]++
	}
	for i := range items {
		code := strings.ToUpper(strings.TrimSpace(items[i].StockCode))
		if code == "" {
			code = strings.TrimSpace(items[i].StockName)
		}
		repeat := counts[code]
		if repeat <= 1 {
			items[i].RecommendCount = 0
			continue
		}
		items[i].RecommendCount = repeat
	}
}

func applyRecommendRepeatCountByCodeMap(items []models.AiRecommendStocksYieldItem, counts map[string]int) {
	if len(items) == 0 || len(counts) == 0 {
		return
	}
	for i := range items {
		code := strings.ToUpper(strings.TrimSpace(items[i].StockCode))
		if code == "" {
			code = strings.TrimSpace(items[i].StockName)
		}
		repeat := counts[code]
		if repeat <= 1 {
			items[i].RecommendCount = 0
			continue
		}
		items[i].RecommendCount = repeat
	}
}

func countRecommendOccurrencesByCode(records []models.AiRecommendStocks) map[string]int {
	counts := make(map[string]int, len(records))
	for _, rec := range records {
		code := strings.ToUpper(normalizeRecommendStockCode(rec.StockCode))
		if code == "" {
			code = strings.TrimSpace(rec.StockName)
		}
		if code == "" {
			continue
		}
		counts[code]++
	}
	return counts
}

func collapseRecommendRecordsSameDayByCode(records []models.AiRecommendStocks) []models.AiRecommendStocks {
	if len(records) <= 1 {
		return records
	}
	seen := make(map[string]struct{}, len(records))
	result := make([]models.AiRecommendStocks, 0, len(records))
	for _, rec := range records {
		code := normalizeRecommendStockCode(rec.StockCode)
		recordTime := recommendRecordTime(rec)
		if code == "" || recordTime.IsZero() {
			result = append(result, rec)
			continue
		}
		dayKey := recordTime.In(cnLocation()).Format("2006-01-02")
		key := strings.ToUpper(code) + "|" + dayKey
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, rec)
	}
	return result
}

func computeMinuteDownloadCoverageStats(meta *models.AiRecommendYieldMeta) minuteCoverageStats {
	stats, _ := computeMinuteDownloadCoverageStatsWithIssues(meta, 0)
	return stats
}

func resolveMinuteCoverageScope(state *models.AiRecommendYieldRecordState, stockCode string, cacheRanges map[string]minuteCacheRange) (time.Time, time.Time, bool) {
	if state != nil && state.MinuteCacheStart != nil && state.MinuteCacheEnd != nil {
		return normalizeMinuteTime(state.MinuteCacheStart.In(cnLocation())), normalizeMinuteTime(state.MinuteCacheEnd.In(cnLocation())), true
	}
	code := normalizeRecommendStockCode(stockCode)
	if code == "" {
		return time.Time{}, time.Time{}, false
	}
	if rng, ok := cacheRanges[code]; ok && rng.Start != nil && rng.End != nil {
		return normalizeMinuteTime(rng.Start.In(cnLocation())), normalizeMinuteTime(rng.End.In(cnLocation())), true
	}
	return time.Time{}, time.Time{}, false
}

type minuteCoverageSession struct {
	Start time.Time
	End   time.Time
}

func buildMinuteCoverageSessions(start, end time.Time) []minuteCoverageSession {
	if start.IsZero() || end.IsZero() || start.After(end) {
		return nil
	}
	loc := cnLocation()
	start = normalizeMinuteTime(start.In(loc))
	end = normalizeMinuteTime(end.In(loc))
	startDay := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, loc)
	endDay := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, loc)
	sessions := make([]minuteCoverageSession, 0, int(endDay.Sub(startDay).Hours()/24+1)*2)
	for day, guard := startDay, 0; !day.After(endDay) && guard < 370; day, guard = day.AddDate(0, 0, 1), guard+1 {
		if !isCNOpenTradeDay(day) {
			continue
		}
		daySessions := [][2]time.Time{
			{
				time.Date(day.Year(), day.Month(), day.Day(), 9, 31, 0, 0, loc),
				time.Date(day.Year(), day.Month(), day.Day(), 11, 30, 0, 0, loc),
			},
			{
				time.Date(day.Year(), day.Month(), day.Day(), 13, 1, 0, 0, loc),
				time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, loc),
			},
		}
		for _, session := range daySessions {
			sessionStart := session[0]
			sessionEnd := session[1]
			if sessionEnd.Before(start) || sessionStart.After(end) {
				continue
			}
			if sessionStart.Before(start) {
				sessionStart = start
			}
			if sessionEnd.After(end) {
				sessionEnd = end
			}
			if sessionStart.After(sessionEnd) {
				continue
			}
			sessions = append(sessions, minuteCoverageSession{
				Start: normalizeMinuteTime(sessionStart),
				End:   normalizeMinuteTime(sessionEnd),
			})
		}
	}
	return sessions
}

func minuteCoverageContinuityIssue(bars []minuteBar, sessions []minuteCoverageSession) string {
	issue := computeMinuteCoverageContinuityIssue(bars, sessions)
	return issue.Reason
}

type minuteCoverageContinuityIssueResult struct {
	Reason       string
	Kind         string
	MissingStart time.Time
	MissingEnd   time.Time
}

func minuteCoverageContinuityIssueFromWindow(reason, kind string, start, end time.Time) minuteCoverageContinuityIssueResult {
	if start.IsZero() || end.IsZero() || start.After(end) {
		return minuteCoverageContinuityIssueResult{Reason: reason, Kind: kind}
	}
	return minuteCoverageContinuityIssueResult{
		Reason:       reason,
		Kind:         kind,
		MissingStart: normalizeMinuteTime(start),
		MissingEnd:   normalizeMinuteTime(end),
	}
}

func computeMinuteCoverageContinuityIssue(bars []minuteBar, sessions []minuteCoverageSession) minuteCoverageContinuityIssueResult {
	if len(sessions) == 0 {
		return minuteCoverageContinuityIssueResult{}
	}
	loc := cnLocation()
	const tolerance = 5 * time.Minute
	idx := 0
	for _, session := range sessions {
		sessionBars := make([]minuteBar, 0, 128)
		for idx < len(bars) && normalizeMinuteTime(bars[idx].TradeTime.In(loc)).Before(session.Start) {
			idx++
		}
		for scan := idx; scan < len(bars); scan++ {
			barTime := normalizeMinuteTime(bars[scan].TradeTime.In(loc))
			if barTime.After(session.End) {
				break
			}
			sessionBars = append(sessionBars, bars[scan])
		}
		if len(sessionBars) == 0 {
			reason := fmt.Sprintf("分钟线交易时段缺口：%s~%s 无数据", session.Start.Format("2006-01-02 15:04"), session.End.Format("15:04"))
			return minuteCoverageContinuityIssueFromWindow(reason, "session_empty", session.Start, session.End)
		}
		first := normalizeMinuteTime(sessionBars[0].TradeTime.In(loc))
		last := normalizeMinuteTime(sessionBars[len(sessionBars)-1].TradeTime.In(loc))
		if first.After(session.Start.Add(tolerance)) {
			reason := fmt.Sprintf("分钟线交易时段缺口：%s~%s 首根为 %s", session.Start.Format("2006-01-02 15:04"), session.End.Format("15:04"), first.Format("15:04"))
			return minuteCoverageContinuityIssueFromWindow(reason, "session_late_first", session.Start, first.Add(-time.Minute))
		}
		if last.Before(session.End.Add(-tolerance)) {
			reason := fmt.Sprintf("分钟线交易时段缺口：%s~%s 末根为 %s", session.Start.Format("2006-01-02 15:04"), session.End.Format("15:04"), last.Format("15:04"))
			return minuteCoverageContinuityIssueFromWindow(reason, "session_early_last", last.Add(time.Minute), session.End)
		}
		prev := first
		for i := 1; i < len(sessionBars); i++ {
			cur := normalizeMinuteTime(sessionBars[i].TradeTime.In(loc))
			if cur.Sub(prev) > tolerance {
				reason := fmt.Sprintf("分钟线交易时段缺口：%s %s~%s 断档", cur.Format("2006-01-02"), prev.Format("15:04"), cur.Format("15:04"))
				return minuteCoverageContinuityIssueFromWindow(reason, "session_gap", prev.Add(time.Minute), cur.Add(-time.Minute))
			}
			prev = cur
		}
	}
	return minuteCoverageContinuityIssueResult{}
}

func resolveMinuteCoverageContinuityIssue(stockCode string, start, end time.Time) (string, error) {
	issue, err := resolveMinuteCoverageContinuityIssueDetail(stockCode, start, end)
	return issue.Reason, err
}

func resolveMinuteCoverageContinuityIssueDetail(stockCode string, start, end time.Time) (minuteCoverageContinuityIssueResult, error) {
	sessions := buildMinuteCoverageSessions(start, end)
	if len(sessions) == 0 {
		return minuteCoverageContinuityIssueResult{}, nil
	}
	bars, err := listMinuteBarsFromCache(stockCode, sessions[0].Start, sessions[len(sessions)-1].End)
	if err != nil {
		return minuteCoverageContinuityIssueResult{}, err
	}
	return computeMinuteCoverageContinuityIssue(bars, sessions), nil
}

func resolveYieldMinuteCoverageRequiredStart(recordTime time.Time) time.Time {
	if recordTime.IsZero() {
		return time.Time{}
	}
	start := resolveRecommendBuyTime(recordTime.In(cnLocation()))
	if start.IsZero() {
		return time.Time{}
	}
	if replayStart := resolveYieldReplayExpandedRangeStart(start); !replayStart.IsZero() && replayStart.Before(start) {
		start = replayStart
	}
	return normalizeMinuteTime(start)
}

func computeMinuteDownloadCoverageStatsWithIssues(meta *models.AiRecommendYieldMeta, issueLimit int) (minuteCoverageStats, []minuteCoverageIssue) {
	loc := cnLocation()
	now := timeNow().In(loc)
	tradeDate := resolveExpectedYieldTradeDate(now)
	if meta != nil {
		if t, ok := parseYieldTradeDate(meta.CurrentTradeDate); ok {
			if tradeDate.IsZero() || t.After(tradeDate) {
				tradeDate = t
			}
		}
	}
	latestTradeDate := time.Date(tradeDate.Year(), tradeDate.Month(), tradeDate.Day(), 0, 0, 0, 0, loc)
	endTime := resolveLatestCloseEvalEnd(now, latestTradeDate)
	coverableStartMinute := minuteCoverableStartMinute(latestTradeDate)

	// Coverage is computed per recommendation record (not per unique stock).
	// This matches the "股票收益率" list semantics (no folding) and the user's
	// expectation that "推荐了多少次就显示多少次".
	records := make([]models.AiRecommendStocks, 0, 128)
	if err := db.Dao.Model(&models.AiRecommendStocks{}).
		Order("COALESCE(data_time, created_at) ASC, id ASC").
		Find(&records).Error; err != nil {
		return minuteCoverageStats{}, nil
	}
	updatedRecords, err := applyYieldOverridesToRecommendRecords(records)
	if err != nil {
		return minuteCoverageStats{}, nil
	}
	records = updatedRecords
	filteredRecords := make([]models.AiRecommendStocks, 0, len(records))
	for _, rec := range records {
		if !shouldDisplayRecommendInYield(&rec) {
			continue
		}
		if eligibility, _ := resolveRecommendBacktestEligibility(&rec); eligibility == recommendBacktestEligible {
			filteredRecords = append(filteredRecords, rec)
		}
	}
	records = filteredRecords
	if len(records) == 0 {
		return minuteCoverageStats{}, nil
	}

	// Load record-level state rows (for sell_time + cache scope), and fall back
	// to raw minute cache ranges for old snapshots that haven't populated
	// minute_cache_* yet.
	recordIDs := make([]uint, 0, len(records))
	for _, rec := range records {
		if rec.ID == 0 {
			continue
		}
		recordIDs = append(recordIDs, rec.ID)
	}
	stateRows := make([]models.AiRecommendYieldRecordState, 0, len(recordIDs))
	if len(recordIDs) > 0 {
		if err := db.Dao.Model(&models.AiRecommendYieldRecordState{}).Where("recommend_id IN ?", recordIDs).Find(&stateRows).Error; err != nil {
			if !isSQLiteNoSuchTable(err) {
				logger.SugaredLogger.Warnf("load ai_recommend_yield_record_state failed: %v", err)
			}
			stateRows = stateRows[:0]
		}
	}
	stateMap := make(map[uint]models.AiRecommendYieldRecordState, len(stateRows))
	for _, row := range stateRows {
		if row.RecommendID == 0 {
			continue
		}
		stateMap[row.RecommendID] = row
	}
	cacheRanges := map[string]minuteCacheRange{}
	if m, err := loadMinuteCacheRangeMap(); err == nil && len(m) > 0 {
		cacheRanges = m
	}
	continuityIssueCache := make(map[string]minuteCoverageContinuityIssueResult)

	formatTs := func(t time.Time) string {
		if t.IsZero() {
			return "--"
		}
		return t.In(loc).Format("2006-01-02 15:04:05")
	}
	issueCap := 0
	if issueLimit > 0 {
		issueCap = issueLimit
	}
	issues := make([]minuteCoverageIssue, 0, issueCap)
	appendIssue := func(rec models.AiRecommendStocks, recordTime time.Time, code, status, reason string, detail minuteCoverageContinuityIssueResult) {
		if issueLimit == 0 {
			return
		}
		if issueLimit > 0 && len(issues) >= issueLimit {
			return
		}
		if strings.TrimSpace(reason) == "" {
			return
		}
		issues = append(issues, minuteCoverageIssue{
			RowKey:       yieldRowKeyFromRecommend(rec, code),
			RecordID:     rec.ID,
			RecordTime:   recordTime.In(loc),
			StockCode:    code,
			StockName:    strings.TrimSpace(rec.StockName),
			Status:       status,
			RawReason:    strings.TrimSpace(reason),
			IssueKind:    strings.TrimSpace(detail.Kind),
			MissingStart: detail.MissingStart,
			MissingEnd:   detail.MissingEnd,
		})
	}

	done := 0
	total := 0
	pending := 0
	uncoverable := 0
	for _, rec := range records {
		if eligibility, _ := resolveRecommendBacktestEligibility(&rec); eligibility != recommendBacktestEligible {
			continue
		}

		code := normalizeRecommendStockCode(rec.StockCode)
		if code == "" || !isAShareTsCode(code) {
			continue
		}

		// Only count records that actually require minute evaluation.
		_, hasStopProfit := parseStopProfitPrice(rec)
		_, hasStopLoss := parseStopLossPrice(rec)
		if !hasStopProfit && !hasStopLoss {
			continue
		}

		recordTime := recommendRecordTime(rec)
		if recordTime.IsZero() {
			continue
		}
		requiredStart := resolveYieldMinuteCoverageRequiredStart(recordTime.In(loc))
		if !coverableStartMinute.IsZero() && requiredStart.Before(coverableStartMinute) {
			// Outside coverable window: do not count it in done/total.
			continue
		}
		state, hasState := stateMap[rec.ID]
		requiredEnd := endTime
		if hasState && state.SellTime != nil && !state.SellTime.IsZero() {
			// 已止盈/止损的记录，分钟线覆盖终点应收敛到卖出时点，
			// 不需要继续覆盖到最新收盘时间。
			soldEnd := normalizeMinuteCoverageEnd(state.SellTime.In(loc))
			if !soldEnd.IsZero() && soldEnd.Before(requiredEnd) {
				requiredEnd = soldEnd
			}
		}
		if !requiredStart.Before(requiredEnd) {
			continue
		}
		total++
		var statePtr *models.AiRecommendYieldRecordState
		if hasState {
			statePtr = &state
		}
		cacheStart, cacheEnd, hasScope := resolveMinuteCoverageScope(statePtr, code, cacheRanges)
		if !hasScope {
			if hasState && strings.TrimSpace(state.DataStatus) == "无法判定" {
				uncoverable++
				reason := strings.TrimSpace(state.DataStatusReason)
				if reason == "" {
					reason = fmt.Sprintf("无缓存范围（目标 %s~%s）", formatTs(requiredStart), formatTs(requiredEnd))
				}
				appendIssue(rec, recordTime, code, "不可覆盖", reason, minuteCoverageContinuityIssueResult{})
			} else {
				pending++
				appendIssue(rec, recordTime, code, "待覆盖",
					fmt.Sprintf("无缓存范围（目标 %s~%s）", formatTs(requiredStart), formatTs(requiredEnd)), minuteCoverageContinuityIssueResult{})
			}
			continue
		}

		if !minuteStartCovered(requiredStart, cacheStart) {
			if hasState && strings.TrimSpace(state.DataStatus) == "无法判定" {
				uncoverable++
				reason := strings.TrimSpace(state.DataStatusReason)
				if reason == "" {
					reason = fmt.Sprintf("起点未覆盖（缓存 %s~%s，目标 %s~%s）",
						formatTs(cacheStart), formatTs(cacheEnd), formatTs(requiredStart), formatTs(requiredEnd))
				}
				appendIssue(rec, recordTime, code, "不可覆盖", reason, minuteCoverageContinuityIssueResult{})
			} else {
				pending++
				appendIssue(rec, recordTime, code, "待覆盖",
					fmt.Sprintf("起点未覆盖（缓存 %s~%s，目标 %s~%s）",
						formatTs(cacheStart), formatTs(cacheEnd), formatTs(requiredStart), formatTs(requiredEnd)), minuteCoverageContinuityIssueResult{})
			}
			continue
		}
		if cacheEnd.Before(requiredEnd) {
			if hasState && strings.TrimSpace(state.DataStatus) == "无法判定" {
				uncoverable++
				reason := strings.TrimSpace(state.DataStatusReason)
				if reason == "" {
					reason = fmt.Sprintf("终点未覆盖（缓存 %s~%s，目标 %s~%s）",
						formatTs(cacheStart), formatTs(cacheEnd), formatTs(requiredStart), formatTs(requiredEnd))
				}
				appendIssue(rec, recordTime, code, "不可覆盖", reason, minuteCoverageContinuityIssueResult{})
			} else {
				pending++
				appendIssue(rec, recordTime, code, "待覆盖",
					fmt.Sprintf("终点未覆盖（缓存 %s~%s，目标 %s~%s）",
						formatTs(cacheStart), formatTs(cacheEnd), formatTs(requiredStart), formatTs(requiredEnd)), minuteCoverageContinuityIssueResult{})
			}
			continue
		}
		continuityKey := strings.Join([]string{code, requiredStart.Format(time.RFC3339Nano), requiredEnd.Format(time.RFC3339Nano)}, "|")
		continuityDetail, checked := continuityIssueCache[continuityKey]
		if !checked {
			if detail, err := resolveMinuteCoverageContinuityIssueDetail(code, requiredStart, requiredEnd); err != nil {
				continuityDetail = minuteCoverageContinuityIssueResult{Reason: fmt.Sprintf("分钟线连续性检查失败（目标 %s~%s）：%v", formatTs(requiredStart), formatTs(requiredEnd), err)}
			} else {
				continuityDetail = detail
			}
			continuityIssueCache[continuityKey] = continuityDetail
		}
		if strings.TrimSpace(continuityDetail.Reason) != "" {
			pending++
			appendIssue(rec, recordTime, code, "待覆盖",
				fmt.Sprintf("%s（目标 %s~%s）", strings.TrimSpace(continuityDetail.Reason), formatTs(requiredStart), formatTs(requiredEnd)), continuityDetail)
			continue
		}
		done++
	}
	return minuteCoverageStats{
		Done:        done,
		Total:       total,
		Pending:     pending,
		Uncoverable: uncoverable,
	}, issues
}

func resolveRecommendDisplayTime(recordTime time.Time) time.Time {
	if recordTime.IsZero() {
		return time.Time{}
	}
	return recordTime.In(cnLocation())
}

func formatYieldDisplayTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(cnLocation()).Format("2006-01-02 15:04:05")
}

func resolveRecommendActualBuyTime(recordTime time.Time) time.Time {
	if recordTime.IsZero() {
		return time.Time{}
	}
	loc := cnLocation()
	t := normalizeMinuteTime(recordTime.In(loc))
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)

	if !isCNOpenTradeDaySafe(day) {
		nextDay := shiftToNextCNOpenTradeDaySafe(day)
		return time.Date(nextDay.Year(), nextDay.Month(), nextDay.Day(), 9, 30, 0, 0, loc)
	}

	morningOpen := time.Date(day.Year(), day.Month(), day.Day(), 9, 30, 0, 0, loc)
	morningClose := time.Date(day.Year(), day.Month(), day.Day(), 11, 30, 0, 0, loc)
	afternoonOpen := time.Date(day.Year(), day.Month(), day.Day(), 13, 0, 0, 0, loc)
	close1500 := time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, loc)

	switch {
	case t.Before(morningOpen):
		return morningOpen
	case !t.Before(morningOpen) && !t.After(morningClose):
		return t
	case t.Before(afternoonOpen):
		return afternoonOpen
	case !t.Before(afternoonOpen) && t.Before(close1500):
		return t
	default:
		nextDay := shiftToNextCNOpenTradeDaySafe(day.AddDate(0, 0, 1))
		return time.Date(nextDay.Year(), nextDay.Month(), nextDay.Day(), 9, 30, 0, 0, loc)
	}
}

func resolveRecommendBuyTime(recordTime time.Time) time.Time {
	return resolveRecommendActualBuyTime(recordTime)
}

func resolveRecommendYieldStateBuyTime(recordTime time.Time, existing *models.AiRecommendYieldRecordState) time.Time {
	if existing != nil && existing.BuyTime != nil && !existing.BuyTime.IsZero() {
		return *existing.BuyTime
	}
	actualBuyTime := resolveRecommendActualBuyTime(recordTime)
	if actualBuyTime.IsZero() {
		return actualBuyTime
	}
	loc := cnLocation()
	recordTime = recordTime.In(loc)
	day := time.Date(recordTime.Year(), recordTime.Month(), recordTime.Day(), 0, 0, 0, 0, loc)
	if isCNOpenTradeDaySafe(day) && isCNTradingSession(recordTime) {
		return time.Date(day.Year(), day.Month(), day.Day(), 9, 30, 0, 0, loc)
	}
	return actualBuyTime
}

func resolveNextCNTradeOpen(day time.Time) time.Time {
	if day.IsZero() {
		return time.Time{}
	}
	loc := cnLocation()
	day = day.In(loc)
	day = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	nextDay := shiftToNextCNOpenTradeDaySafe(day.AddDate(0, 0, 1))
	return time.Date(nextDay.Year(), nextDay.Month(), nextDay.Day(), 9, 30, 0, 0, loc)
}

func resolveCNTradeDayReplayStart(day time.Time) time.Time {
	if day.IsZero() {
		return time.Time{}
	}
	loc := cnLocation()
	day = day.In(loc)
	day = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	return time.Date(day.Year(), day.Month(), day.Day(), 9, 31, 0, 0, loc)
}

func resolveCNTradeDayReplayEnd(day time.Time) time.Time {
	if day.IsZero() {
		return time.Time{}
	}
	loc := cnLocation()
	day = day.In(loc)
	day = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	return time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, loc)
}

func resolveYieldReplayExpandedRangeStart(signalAt time.Time) time.Time {
	if signalAt.IsZero() {
		return time.Time{}
	}
	loc := cnLocation()
	anchor := resolveRecommendBuyTime(signalAt.In(loc))
	if anchor.IsZero() {
		anchor = signalAt.In(loc)
	}
	anchorDay := time.Date(anchor.Year(), anchor.Month(), anchor.Day(), 0, 0, 0, 0, loc)
	prevDay := shiftToPrevCNOpenTradeDaySafe(anchorDay.AddDate(0, 0, -1))
	return resolveCNTradeDayReplayStart(prevDay)
}

func resolveYieldReplayExpandedRangeEnd(endAt time.Time) time.Time {
	if endAt.IsZero() {
		return time.Time{}
	}
	loc := cnLocation()
	anchor := normalizeMinuteTime(endAt.In(loc))
	anchorDay := time.Date(anchor.Year(), anchor.Month(), anchor.Day(), 0, 0, 0, 0, loc)
	if !isCNOpenTradeDaySafe(anchorDay) {
		anchorDay = shiftToPrevCNOpenTradeDaySafe(anchorDay)
	}
	nextDay := shiftToNextCNOpenTradeDaySafe(anchorDay.AddDate(0, 0, 1))
	expandedEnd := resolveCNTradeDayReplayEnd(nextDay)
	if expandedEnd.Before(anchor) {
		return anchor
	}
	return expandedEnd
}

func expandYieldReplayQueryWindow(signalAt, endAt time.Time, holding bool) (time.Time, time.Time) {
	start := normalizeMinuteTime(signalAt.In(cnLocation()))
	end := normalizeMinuteTime(endAt.In(cnLocation()))
	if start.IsZero() || end.IsZero() {
		return start, end
	}
	if expandedStart := resolveYieldReplayExpandedRangeStart(signalAt); !expandedStart.IsZero() && expandedStart.Before(start) {
		start = expandedStart
	}
	if holding && !isCNTradingSession(timeNow().In(cnLocation())) {
		if clampedEnd := normalizeMinuteCoverageEnd(endAt.In(cnLocation())); !clampedEnd.IsZero() && !clampedEnd.Before(start) {
			end = clampedEnd
		}
		return start, end
	}
	if expandedEnd := resolveYieldReplayExpandedRangeEnd(endAt); !expandedEnd.IsZero() && expandedEnd.After(end) {
		end = expandedEnd
	}
	return start, end
}

func resolveNextSellEligibleTime(buyTime time.Time) time.Time {
	if buyTime.IsZero() {
		return time.Time{}
	}
	return resolveNextCNTradeOpen(buyTime)
}

func resolveRecommendSellEligibleTime(recordTime time.Time) time.Time {
	if recordTime.IsZero() {
		return time.Time{}
	}
	return resolveNextSellEligibleTime(resolveRecommendBuyTime(recordTime))
}

func normalizeMinuteCoverageEnd(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	loc := cnLocation()
	t = t.In(loc)
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)

	prevTradingDay := func(d time.Time) time.Time {
		cur := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
		for isWeekendCN(cur) {
			cur = cur.AddDate(0, 0, -1)
		}
		return cur
	}

	// Keep buy-time semantics stable: the first minute bar is often labeled as 09:31.
	open931 := time.Date(day.Year(), day.Month(), day.Day(), 9, 31, 0, 0, loc)
	morningClose := time.Date(day.Year(), day.Month(), day.Day(), 11, 30, 0, 0, loc)
	afternoonOpen := time.Date(day.Year(), day.Month(), day.Day(), 13, 1, 0, 0, loc)
	close1500 := time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, loc)

	// Weekend moments clamp to the previous trading day's close.
	if isWeekendCN(day) {
		prev := prevTradingDay(day.AddDate(0, 0, -1))
		return time.Date(prev.Year(), prev.Month(), prev.Day(), 15, 0, 0, 0, loc)
	}

	// Before market open: use previous trading day's 15:00.
	if t.Before(open931) {
		prev := prevTradingDay(day.AddDate(0, 0, -1))
		return time.Date(prev.Year(), prev.Month(), prev.Day(), 15, 0, 0, 0, loc)
	}
	// Lunch break: clamp to 11:30.
	if t.After(morningClose) && t.Before(afternoonOpen) {
		return morningClose
	}
	// After close: clamp to 15:00.
	if t.After(close1500) {
		return close1500
	}
	return normalizeMinuteTime(t)
}

func listAiRecommendStocksForYield(query *models.AiRecommendStocksQuery, coverableStartMinute time.Time) ([]models.AiRecommendStocks, error) {
	q := db.Dao.Model(&models.AiRecommendStocks{})
	if query != nil {
		q = applyStrategyCohortFilter(q, query.StrategyCohort)
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
					q = q.Where("COALESCE(data_time, created_at) BETWEEN ? AND ?", datetime.BeginOfDay(startTime), datetime.EndOfDay(endTime))
				}
			}
		}
		if query.StartDate != "" && query.EndDate == "" {
			startDate := normalizeDateTime(query.StartDate)
			startTime, err := parseDateTimeWithFallback(startDate)
			if err == nil {
				q = q.Where("COALESCE(data_time, created_at) BETWEEN ? AND ?", datetime.BeginOfDay(startTime), datetime.EndOfDay(startTime))
			}
		}
	}

	rows := make([]models.AiRecommendStocks, 0)
	err := q.Order("COALESCE(data_time, created_at) DESC, id DESC").Find(&rows).Error
	if err != nil {
		return rows, err
	}
	rows, err = applyYieldOverridesToRecommendRecords(rows)
	if err != nil {
		return nil, err
	}
	coverableStartMinute = normalizeMinuteTime(coverableStartMinute)
	if len(rows) == 0 {
		return rows, nil
	}
	filtered := make([]models.AiRecommendStocks, 0, len(rows))
	for _, rec := range rows {
		if !shouldDisplayRecommendInYield(&rec) {
			continue
		}
		eligibility, _ := resolveRecommendBacktestEligibility(&rec)
		if eligibility != recommendBacktestEligible {
			filtered = append(filtered, rec)
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
			// Skip old recommendations that minute providers cannot cover.
			continue
		}
		filtered = append(filtered, rec)
	}
	return filtered, nil
}

func loadYieldStateMapByRecommendRecords(records []models.AiRecommendStocks) (map[string]models.AiRecommendYieldState, error) {
	stateMap := map[string]models.AiRecommendYieldState{}
	if len(records) == 0 {
		return stateMap, nil
	}
	codeSet := map[string]struct{}{}
	codes := make([]string, 0, len(records))
	for _, rec := range records {
		code := normalizeRecommendStockCode(rec.StockCode)
		if code == "" {
			continue
		}
		if _, ok := codeSet[code]; ok {
			continue
		}
		codeSet[code] = struct{}{}
		codes = append(codes, code)
	}
	if len(codes) == 0 {
		return stateMap, nil
	}
	rows := make([]models.AiRecommendYieldState, 0, len(codes))
	if err := db.Dao.Model(&models.AiRecommendYieldState{}).Where("stock_code IN ?", codes).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		stateMap[normalizeRecommendStockCode(row.StockCode)] = row
	}
	return stateMap, nil
}

func buildStrictAggregateYieldItems(
	records []models.AiRecommendStocks,
	recordStateMap map[uint]models.AiRecommendYieldRecordState,
	stateMap map[string]models.AiRecommendYieldState,
	overrideMap map[uint]models.AiRecommendYieldOverride,
	dirtyMap map[string]models.AiRecommendYieldDirtyCode,
	coverageIssues []minuteCoverageIssue,
) ([]models.AiRecommendStocksYieldItem, []models.AiRecommendStocksYieldItem) {
	if len(records) == 0 {
		return []models.AiRecommendStocksYieldItem{}, []models.AiRecommendStocksYieldItem{}
	}

	recordIssueMap := make(map[string]minuteCoverageIssue, len(coverageIssues))
	stockIssueMap := make(map[string]minuteCoverageIssue, len(coverageIssues))
	for _, issue := range coverageIssues {
		rowKey := strings.TrimSpace(issue.RowKey)
		if rowKey != "" {
			recordIssueMap[rowKey] = issue
		}
		code := normalizeRecommendStockCode(issue.StockCode)
		if code == "" {
			code = normalizeRecommendStockCode(rowKey)
		}
		if code == "" {
			continue
		}
		if _, exists := stockIssueMap[code]; exists {
			continue
		}
		stockIssueMap[code] = issue
	}

	grouped := make(map[string][]models.AiRecommendStocks, len(records))
	order := make([]string, 0, len(records))
	fallbackList := make([]models.AiRecommendStocks, 0, len(records))
	for _, rec := range records {
		code := normalizeRecommendStockCode(rec.StockCode)
		if code == "" {
			fallbackList = append(fallbackList, rec)
			continue
		}
		if _, exists := grouped[code]; !exists {
			order = append(order, code)
		}
		grouped[code] = append(grouped[code], rec)
	}

	listItems := make([]models.AiRecommendStocksYieldItem, 0, len(order)+len(fallbackList))
	metricItems := make([]models.AiRecommendStocksYieldItem, 0, len(records))

	for _, rec := range fallbackList {
		item := mapRecommendRecordToYieldItemWithRecordState(rec, recordStateMap, stateMap)
		if override, ok := overrideMap[rec.ID]; ok {
			applyYieldOverrideToYieldItem(&item, &override)
		}
		item.CalcMode = aiRecommendYieldModeStrict
		if issue, ok := recordIssueMap[item.RowKey]; ok {
			item.DataStatus = issue.Status
			item.DataStatusReason = issue.RawReason
		}
		applyStrictPendingStateToYieldItem(&item, dirtyMap)
		listItems = append(listItems, item)
		metricItems = append(metricItems, item)
	}

	for _, code := range order {
		group := grouped[code]
		if len(group) == 0 {
			continue
		}
		state, hasState := stateMap[code]
		displayRecord := group[0]
		representative := pickRepresentativeRecommendRecordForYieldAggregate(group, hasState, state)
		recommendCount := len(group)
		if recommendCount < 1 {
			recommendCount = 1
		}

		if hasState {
			listItem := mapRecommendAggregateStateToYieldItem(displayRecord, state, recommendCount)
			listItem.RecommendID = 0
			listItem.RowKey = code
			listItem.CalcMode = aiRecommendYieldModeStrict
			if issue, ok := stockIssueMap[code]; ok && shouldOverrideAggregateItemByCoverageIssue(listItem) {
				listItem.DataStatus = issue.Status
				listItem.DataStatusReason = issue.RawReason
			}
			applyStrictPendingStateToYieldItem(&listItem, dirtyMap)
			listItems = append(listItems, listItem)
		} else {
			listItem := mapRecommendRecordToYieldItemWithRecordState(representative, recordStateMap, stateMap)
			if override, ok := overrideMap[representative.ID]; ok {
				applyYieldOverrideToYieldItem(&listItem, &override)
			}
			listItem.RowKey = code
			listItem.RecommendCount = recommendCount
			listItem.CalcMode = aiRecommendYieldModeStrict
			if issue, ok := stockIssueMap[code]; ok {
				listItem.DataStatus = issue.Status
				listItem.DataStatusReason = issue.RawReason
			}
			applyStrictPendingStateToYieldItem(&listItem, dirtyMap)
			listItems = append(listItems, listItem)
		}

		representativeID := representative.ID
		representativeUsed := false
		for _, rec := range group {
			var item models.AiRecommendStocksYieldItem
			if hasState && !representativeUsed && representativeID != 0 && rec.ID == representativeID {
				item = mapRecommendAggregateStateToYieldItem(rec, state, 1)
				representativeUsed = true
			} else {
				item = mapRecommendRecordToYieldItemWithRecordState(rec, recordStateMap, stateMap)
				if override, ok := overrideMap[rec.ID]; ok {
					applyYieldOverrideToYieldItem(&item, &override)
				}
			}
			item.CalcMode = aiRecommendYieldModeStrict
			if issue, ok := recordIssueMap[item.RowKey]; ok {
				item.DataStatus = issue.Status
				item.DataStatusReason = issue.RawReason
			}
			applyStrictPendingStateToYieldItem(&item, dirtyMap)
			metricItems = append(metricItems, item)
		}
	}

	return listItems, metricItems
}

func buildStrictYieldRecordItems(
	records []models.AiRecommendStocks,
	recordStateMap map[uint]models.AiRecommendYieldRecordState,
	stateMap map[string]models.AiRecommendYieldState,
	overrideMap map[uint]models.AiRecommendYieldOverride,
	dirtyMap map[string]models.AiRecommendYieldDirtyCode,
	coverageIssues []minuteCoverageIssue,
) []models.AiRecommendStocksYieldItem {
	if len(records) == 0 {
		return []models.AiRecommendStocksYieldItem{}
	}

	recordIssueMap := make(map[string]minuteCoverageIssue, len(coverageIssues))
	for _, issue := range coverageIssues {
		rowKey := strings.TrimSpace(issue.RowKey)
		if rowKey == "" {
			continue
		}
		recordIssueMap[rowKey] = issue
	}

	items := make([]models.AiRecommendStocksYieldItem, 0, len(records))
	for _, rec := range records {
		item := buildStrictYieldRecordItem(rec, recordStateMap, stateMap)
		if override, ok := overrideMap[rec.ID]; ok {
			applyYieldOverrideToYieldItem(&item, &override)
		}
		item.CalcMode = aiRecommendYieldModeStrict
		if issue, ok := recordIssueMap[item.RowKey]; ok {
			item.DataStatus = issue.Status
			item.DataStatusReason = issue.RawReason
		}
		applyStrictPendingStateToYieldItem(&item, dirtyMap)
		items = append(items, item)
	}
	applyRecommendRepeatCount(items)
	return items
}

func loadYieldRecordStateMapByRecommendRecords(records []models.AiRecommendStocks) (map[uint]models.AiRecommendYieldRecordState, error) {
	stateMap := map[uint]models.AiRecommendYieldRecordState{}
	if len(records) == 0 {
		return stateMap, nil
	}
	ids := make([]uint, 0, len(records))
	seen := make(map[uint]struct{}, len(records))
	for _, rec := range records {
		if rec.ID == 0 {
			continue
		}
		if _, ok := seen[rec.ID]; ok {
			continue
		}
		seen[rec.ID] = struct{}{}
		ids = append(ids, rec.ID)
	}
	if len(ids) == 0 {
		return stateMap, nil
	}
	rows := make([]models.AiRecommendYieldRecordState, 0, len(ids))
	if err := db.Dao.Model(&models.AiRecommendYieldRecordState{}).Where("recommend_id IN ?", ids).Find(&rows).Error; err != nil {
		if isSQLiteNoSuchTable(err) {
			return stateMap, nil
		}
		return nil, err
	}
	for _, row := range rows {
		if row.RecommendID == 0 {
			continue
		}
		stateMap[row.RecommendID] = row
	}
	return stateMap, nil
}

func mapRecommendRecordToYieldItemWithRecordState(
	rec models.AiRecommendStocks,
	recordStateMap map[uint]models.AiRecommendYieldRecordState,
	stateMap map[string]models.AiRecommendYieldState,
) models.AiRecommendStocksYieldItem {
	if rec.ID != 0 {
		if state, ok := recordStateMap[rec.ID]; ok {
			return mapRecommendRecordStateToYieldItem(rec, state)
		}
	}
	return mapRecommendRecordToYieldItem(rec, stateMap)
}

func buildStrictYieldRecordItem(
	rec models.AiRecommendStocks,
	recordStateMap map[uint]models.AiRecommendYieldRecordState,
	stateMap map[string]models.AiRecommendYieldState,
) models.AiRecommendStocksYieldItem {
	code := normalizeRecommendStockCode(rec.StockCode)
	state, hasState := stateMap[code]
	recordTime := recommendRecordTime(rec)
	if recordState, hasRecordState := recordStateMap[rec.ID]; hasRecordState {
		if hasState &&
			aggregateYieldStateMatchesRecordTime(state, recordTime) &&
			aggregateYieldStateConsistentForRecordTime(state, recordTime) &&
			shouldPreferAggregateOverRecordPendingState(recordState, state) {
			item := mapRecommendAggregateStateToYieldItem(rec, state, 1)
			item.RecommendID = rec.ID
			item.RowKey = yieldRowKeyFromRecommend(rec, code)
			item.RecommendCount = 1
			return item
		}
		return mapRecommendRecordStateToYieldItem(rec, recordState)
	}
	if hasState && aggregateYieldStateMatchesRecordTime(state, recordTime) && aggregateYieldStateConsistentForRecordTime(state, recordTime) {
		item := mapRecommendAggregateStateToYieldItem(rec, state, 1)
		item.RecommendID = rec.ID
		item.RowKey = yieldRowKeyFromRecommend(rec, code)
		item.RecommendCount = 1
		return item
	}
	return mapRecommendRecordToYieldItem(rec, map[string]models.AiRecommendYieldState{})
}

func aggregateYieldStateMatchesRecordTime(state models.AiRecommendYieldState, recordTime time.Time) bool {
	if recordTime.IsZero() {
		return false
	}
	if state.RecommendTime != nil && !state.RecommendTime.IsZero() && yieldTimesAlmostEqual(recordTime, *state.RecommendTime) {
		return true
	}
	if state.SignalTime != nil && !state.SignalTime.IsZero() && yieldTimesAlmostEqual(recordTime, *state.SignalTime) {
		return true
	}
	return false
}

func aggregateYieldStateConsistentForRecordTime(state models.AiRecommendYieldState, recordTime time.Time) bool {
	if recordTime.IsZero() {
		return false
	}
	if strings.TrimSpace(state.ActivationStatus) != "activated" {
		return true
	}
	if state.ActivationTime == nil || state.ActivationTime.IsZero() {
		return true
	}
	return !state.ActivationTime.Before(recordTime)
}

func shouldPreferAggregateOverRecordPendingState(recordState models.AiRecommendYieldRecordState, aggregateState models.AiRecommendYieldState) bool {
	recordStatus := strings.TrimSpace(recordState.ActivationStatus)
	if recordStatus != "" && recordStatus != "pending" {
		return false
	}
	aggregateStatus := strings.TrimSpace(aggregateState.ActivationStatus)
	return aggregateStatus != "" && aggregateStatus != "pending"
}

func pickRepresentativeRecommendRecordForYieldAggregate(
	records []models.AiRecommendStocks,
	hasState bool,
	state models.AiRecommendYieldState,
) models.AiRecommendStocks {
	if len(records) == 0 {
		return models.AiRecommendStocks{}
	}
	if !hasState {
		return records[0]
	}

	matchByTargetTime := func(target *time.Time) (models.AiRecommendStocks, bool) {
		if target == nil || target.IsZero() {
			return models.AiRecommendStocks{}, false
		}
		for _, rec := range records {
			if yieldTimesAlmostEqual(recommendRecordTime(rec), *target) {
				return rec, true
			}
		}
		return models.AiRecommendStocks{}, false
	}
	if rec, ok := matchByTargetTime(state.RecommendTime); ok {
		return rec
	}
	if rec, ok := matchByTargetTime(state.SignalTime); ok {
		return rec
	}

	if state.ActivationTime != nil && !state.ActivationTime.IsZero() {
		bestIndex := -1
		bestDelta := time.Duration(1<<63 - 1)
		for index, rec := range records {
			recordTime := recommendRecordTime(rec)
			if recordTime.IsZero() || recordTime.After(*state.ActivationTime) {
				continue
			}
			delta := state.ActivationTime.Sub(recordTime)
			if delta < bestDelta {
				bestDelta = delta
				bestIndex = index
			}
		}
		if bestIndex >= 0 {
			return records[bestIndex]
		}
	}

	if strings.TrimSpace(state.ActivationStatus) == "activated" {
		for _, rec := range records {
			if eligibility, _ := resolveRecommendBacktestEligibility(&rec); eligibility == recommendBacktestEligible {
				return rec
			}
		}
	}

	if strings.TrimSpace(state.ActivationStatus) == "skipped" {
		for _, rec := range records {
			if eligibility, _ := resolveRecommendBacktestEligibility(&rec); eligibility == recommendBacktestSkipped {
				return rec
			}
		}
	}

	return records[0]
}

func yieldTimesAlmostEqual(lhs, rhs time.Time) bool {
	if lhs.IsZero() || rhs.IsZero() {
		return false
	}
	diff := lhs.Sub(rhs)
	if diff < 0 {
		diff = -diff
	}
	return diff < time.Second
}

func mapRecommendAggregateStateToYieldItem(
	rec models.AiRecommendStocks,
	state models.AiRecommendYieldState,
	recommendCount int,
) models.AiRecommendStocksYieldItem {
	item := mapRecommendRecordToYieldItem(rec, map[string]models.AiRecommendYieldState{})
	code := normalizeRecommendStockCode(state.StockCode)
	if code == "" {
		code = normalizeRecommendStockCode(rec.StockCode)
	}
	if code != "" {
		item.RowKey = code
		item.StockCode = code
	}
	if strings.TrimSpace(item.StockName) == "" && strings.TrimSpace(state.StockName) != "" {
		item.StockName = strings.TrimSpace(state.StockName)
	}
	if strings.TrimSpace(item.ModelNames) == "" && strings.TrimSpace(state.ModelNames) != "" {
		item.ModelNames = strings.TrimSpace(state.ModelNames)
	}
	if strings.TrimSpace(item.BkName) == "" && strings.TrimSpace(state.BkName) != "" {
		item.BkName = strings.TrimSpace(state.BkName)
	}
	if recommendCount < 1 {
		recommendCount = 1
	}
	item.RecommendCount = recommendCount
	if strings.TrimSpace(state.ActivationStatus) != "" {
		item.ActivationStatus = strings.TrimSpace(state.ActivationStatus)
	}
	if state.ActivationTime != nil && !state.ActivationTime.IsZero() {
		item.ActivationTime = state.ActivationTime.Format("2006-01-02 15:04:05")
	}
	item.ActivationPrice = round2(state.ActivationPrice)
	if state.BuyTime != nil && !state.BuyTime.IsZero() {
		item.BuyTime = state.BuyTime.Format("2006-01-02 15:04:05")
	}
	if state.BuyAmount > 0 {
		item.BuyAmount = round2(state.BuyAmount)
	}
	if strings.TrimSpace(state.PositionStatus) != "" {
		item.PositionStatus = strings.TrimSpace(state.PositionStatus)
	}
	if state.SellTime != nil && !state.SellTime.IsZero() {
		item.SellTime = state.SellTime.Format("2006-01-02 15:04:05")
		item.SellAmount = copyFloatPointer(state.RealizedSellAmount)
	} else if strings.TrimSpace(state.ActivationStatus) == "activated" {
		item.SellTime = "持有"
	}
	if state.CurrentPrice > 0 {
		item.CurrentPrice = round2(state.CurrentPrice)
	}
	if strings.TrimSpace(state.CurrentPriceTime) != "" {
		item.CurrentPriceTime = strings.TrimSpace(state.CurrentPriceTime)
	}
	if strings.TrimSpace(state.DataStatus) != "" {
		item.DataStatus = strings.TrimSpace(state.DataStatus)
	}
	if strings.TrimSpace(state.DataStatusReason) != "" {
		item.DataStatusReason = strings.TrimSpace(state.DataStatusReason)
	}
	if strings.TrimSpace(state.YieldRateText) != "" {
		item.YieldRateText = strings.TrimSpace(state.YieldRateText)
		item.YieldRate = round2(state.YieldRate)
	}

	switch strings.TrimSpace(state.ActivationStatus) {
	case "activated":
		applyAggregateStateBacktestEligibility(&item, state)
	case "skipped":
		applyAggregateStateBacktestEligibility(&item, state)
	case "expired":
		applyAggregateStateBacktestEligibility(&item, state)
	case "ineligible":
		applyAggregateStateBacktestEligibility(&item, state)
	}
	applyInactiveYieldDefaults(&item)
	return item
}

func applyAggregateStateBacktestEligibility(item *models.AiRecommendStocksYieldItem, state models.AiRecommendYieldState) {
	if item == nil {
		return
	}
	switch strings.TrimSpace(state.ActivationStatus) {
	case "activated":
		item.BacktestEligibility = recommendBacktestEligible
		item.BacktestEligibilityReason = ""
	case "skipped":
		item.BacktestEligibility = recommendBacktestSkipped
		if strings.TrimSpace(item.BacktestEligibilityReason) == "" {
			item.BacktestEligibilityReason = strings.TrimSpace(state.DataStatusReason)
		}
	case "expired":
		item.BacktestEligibility = recommendBacktestEligible
	case "ineligible":
		item.BacktestEligibility = recommendBacktestIneligible
		if strings.TrimSpace(item.BacktestEligibilityReason) == "" {
			item.BacktestEligibilityReason = strings.TrimSpace(state.DataStatusReason)
		}
	default:
		item.BacktestEligibility = recommendBacktestEligible
	}
}

func shouldOverrideAggregateItemByCoverageIssue(item models.AiRecommendStocksYieldItem) bool {
	if strings.TrimSpace(item.ActivationStatus) == "activated" {
		return false
	}
	status := strings.TrimSpace(item.DataStatus)
	return status == "" || status == "正常" || status == "计算中" || status == "待激活"
}

func resolveRecommendSignalView(rec models.AiRecommendStocks) models.AiRecommendStocks {
	view := rec
	view.ExecutionState = normalizeRecommendExecutionState(view.ExecutionState)
	view.BuySignal = normalizeRecommendText(view.BuySignal)
	view.BuySignalDetail = normalizeRecommendText(view.BuySignalDetail)
	view.SellSignal = normalizeRecommendText(view.SellSignal)
	view.SellSignalDetail = normalizeRecommendText(view.SellSignalDetail)
	view.InvalidSignal = normalizeRecommendText(view.InvalidSignal)
	view.InvalidCondition = normalizeRecommendText(view.InvalidCondition)
	fillSignalDrivenRecommendCompat(&view, true, false)
	return view
}

func mapRecommendRecordStateToYieldItem(rec models.AiRecommendStocks, state models.AiRecommendYieldRecordState) models.AiRecommendStocksYieldItem {
	code := normalizeRecommendStockCode(rec.StockCode)
	recordTime := recommendRecordTime(rec)
	signalView := resolveRecommendSignalView(rec)

	var stopProfitAmount *float64
	if v, ok := parseStopProfitPrice(rec); ok {
		stopProfitAmount = &v
	}
	var stopLossAmount *float64
	if v, ok := parseStopLossPrice(rec); ok {
		stopLossAmount = &v
	}

	if state.StopProfitAmount != nil {
		v := *state.StopProfitAmount
		stopProfitAmount = &v
	}
	if state.StopLossAmount != nil {
		v := *state.StopLossAmount
		stopLossAmount = &v
	}

	item := models.AiRecommendStocksYieldItem{
		RecommendID:             rec.ID,
		RowKey:                  yieldRowKeyFromRecommend(rec, code),
		StockCode:               code,
		StockName:               strings.TrimSpace(rec.StockName),
		ModelNames:              strings.TrimSpace(rec.ModelName),
		BacktestEligibility:     recommendBacktestEligible,
		BkName:                  strings.TrimSpace(rec.BkName),
		RecommendCategory:       strings.TrimSpace(rec.RecommendCategory),
		RecommendCategoryLabel:  recommendCategoryDisplayLabel(rec.RecommendCategory),
		ExecutionState:          signalView.ExecutionState,
		ExecutionStateLabel:     recommendExecutionStateLabel(signalView.ExecutionState),
		BuySignal:               signalView.BuySignal,
		BuySignalDetail:         signalView.BuySignalDetail,
		SellSignal:              signalView.SellSignal,
		SellSignalDetail:        signalView.SellSignalDetail,
		InvalidSignal:           signalView.InvalidSignal,
		ActivationRule:          strings.TrimSpace(rec.ActivationRuleJSON),
		ActivationInvalidReason: strings.TrimSpace(rec.ActivationInvalidReason),
		RecommendCount:          1,
		RecommendTime:           formatYieldDisplayTime(recordTime),
		SignalTime:              formatYieldDisplayTime(recordTime),
		RecommendBuyPrice:       resolveRecommendBuyRangeDisplay(rec),
		StopProfitAmount:        stopProfitAmount,
		StopLossAmount:          stopLossAmount,
		SellTime:                "待激活",
		SellAmountText:          buildSellAmountText(stopProfitAmount, stopLossAmount),
		PositionStatus:          "待激活",
		CurrentPrice:            round2(state.CurrentPrice),
		CurrentPriceTime:        strings.TrimSpace(rec.StockCurrentPriceTime),
		YieldRate:               0,
		YieldRateText:           "--",
		DataStatus:              "正常",
		DataStatusReason:        strings.TrimSpace(state.DataStatusReason),
	}

	if item.StockName == "" {
		item.StockName = strings.TrimSpace(state.StockName)
	}
	if item.ModelNames == "" {
		item.ModelNames = strings.TrimSpace(state.ModelName)
	}
	if item.BkName == "" {
		item.BkName = strings.TrimSpace(state.BkName)
	}
	item.ActivationStatus = strings.TrimSpace(state.ActivationStatus)
	if item.ActivationStatus == "" {
		item.ActivationStatus = "pending"
	}
	if state.SignalTime != nil && !state.SignalTime.IsZero() {
		item.SignalTime = state.SignalTime.Format("2006-01-02 15:04:05")
	}
	if state.ActivationTime != nil && !state.ActivationTime.IsZero() {
		item.ActivationTime = state.ActivationTime.Format("2006-01-02 15:04:05")
		item.BuyTime = item.ActivationTime
	}
	item.ActivationPrice = round2(state.ActivationPrice)
	if state.BuyAmount > 0 {
		item.BuyAmount = round2(state.BuyAmount)
	}
	if strings.TrimSpace(state.SellAmountText) != "" {
		item.SellAmountText = strings.TrimSpace(state.SellAmountText)
	}
	if strings.TrimSpace(state.PositionStatus) != "" {
		item.PositionStatus = strings.TrimSpace(state.PositionStatus)
	}
	if item.ActivationStatus != "activated" && item.PositionStatus == "持有" {
		item.PositionStatus = "待激活"
	}
	if state.SellTime != nil && !state.SellTime.IsZero() {
		item.SellTime = state.SellTime.Format("2006-01-02 15:04:05")
		item.SellAmount = copyFloatPointer(state.RealizedSellAmount)
	} else if item.ActivationStatus == "activated" {
		item.SellTime = "持有"
		if strings.TrimSpace(item.PositionStatus) == "" || item.PositionStatus == "待激活" {
			item.PositionStatus = "持有"
		}
	}
	if strings.TrimSpace(state.CurrentPriceTime) != "" {
		item.CurrentPriceTime = strings.TrimSpace(state.CurrentPriceTime)
	}
	if strings.TrimSpace(state.DataStatus) != "" {
		item.DataStatus = strings.TrimSpace(state.DataStatus)
	}
	if item.CurrentPrice <= 0 {
		item.CurrentPrice = item.BuyAmount
	}
	if item.ActivationStatus == "activated" && item.BuyAmount > 0 {
		if item.SellAmount != nil && *item.SellAmount > 0 {
			result := calculateNetYield(item.StockCode, item.BuyAmount, *item.SellAmount)
			if result.Valid {
				item.YieldRate = result.YieldRate
				item.YieldRateText = result.YieldText
			}
		} else if item.CurrentPrice > 0 {
			result := calculateNetYield(item.StockCode, item.BuyAmount, item.CurrentPrice)
			if result.Valid {
				item.YieldRate = result.YieldRate
				item.YieldRateText = result.YieldText
			}
		}
	}
	applyRecommendBacktestEligibilityOverride(&item, &rec)
	applyInactiveYieldDefaults(&item)
	return item
}

func mapRecommendRecordToYieldItem(rec models.AiRecommendStocks, stateMap map[string]models.AiRecommendYieldState) models.AiRecommendStocksYieldItem {
	code := normalizeRecommendStockCode(rec.StockCode)
	state, hasState := stateMap[code]
	recordTime := recommendRecordTime(rec)
	signalView := resolveRecommendSignalView(rec)

	var stopProfitAmount *float64
	if v, ok := parseStopProfitPrice(rec); ok {
		stopProfitAmount = &v
	}
	var stopLossAmount *float64
	if v, ok := parseStopLossPrice(rec); ok {
		stopLossAmount = &v
	}

	item := models.AiRecommendStocksYieldItem{
		RecommendID:             rec.ID,
		RowKey:                  yieldRowKeyFromRecommend(rec, code),
		StockCode:               code,
		StockName:               strings.TrimSpace(rec.StockName),
		ModelNames:              strings.TrimSpace(rec.ModelName),
		BacktestEligibility:     recommendBacktestEligible,
		BkName:                  strings.TrimSpace(rec.BkName),
		RecommendCategory:       strings.TrimSpace(rec.RecommendCategory),
		RecommendCategoryLabel:  recommendCategoryDisplayLabel(rec.RecommendCategory),
		ExecutionState:          signalView.ExecutionState,
		ExecutionStateLabel:     recommendExecutionStateLabel(signalView.ExecutionState),
		BuySignal:               signalView.BuySignal,
		BuySignalDetail:         signalView.BuySignalDetail,
		SellSignal:              signalView.SellSignal,
		SellSignalDetail:        signalView.SellSignalDetail,
		InvalidSignal:           signalView.InvalidSignal,
		ActivationRule:          strings.TrimSpace(rec.ActivationRuleJSON),
		ActivationInvalidReason: strings.TrimSpace(rec.ActivationInvalidReason),
		RecommendCount:          1,
		RecommendTime:           formatYieldDisplayTime(recordTime),
		SignalTime:              formatYieldDisplayTime(recordTime),
		ActivationStatus:        "pending",
		RecommendBuyPrice:       resolveRecommendBuyRangeDisplay(rec),
		StopProfitAmount:        stopProfitAmount,
		StopLossAmount:          stopLossAmount,
		SellTime:                "待激活",
		SellAmountText:          buildSellAmountText(stopProfitAmount, stopLossAmount),
		PositionStatus:          "待激活",
		CurrentPrice:            0,
		CurrentPriceTime:        strings.TrimSpace(rec.StockCurrentPriceTime),
		YieldRate:               0,
		YieldRateText:           "--",
		DataStatus:              "计算中",
	}

	if item.StockName == "" && hasState {
		item.StockName = state.StockName
	}
	if hasState {
		item.ActivationStatus = strings.TrimSpace(state.ActivationStatus)
		if item.ActivationStatus == "" {
			item.ActivationStatus = "pending"
		}
		if state.SignalTime != nil && !state.SignalTime.IsZero() {
			item.SignalTime = state.SignalTime.Format("2006-01-02 15:04:05")
		}
		if state.ActivationTime != nil && !state.ActivationTime.IsZero() {
			item.ActivationTime = state.ActivationTime.Format("2006-01-02 15:04:05")
			item.BuyTime = item.ActivationTime
		}
		item.ActivationPrice = round2(state.ActivationPrice)
		item.BuyAmount = round2(state.BuyAmount)
		item.PositionStatus = state.PositionStatus
		item.CurrentPrice = round2(state.CurrentPrice)
		if strings.TrimSpace(state.CurrentPriceTime) != "" {
			item.CurrentPriceTime = state.CurrentPriceTime
		}
		item.DataStatus = strings.TrimSpace(state.DataStatus)
		item.DataStatusReason = state.DataStatusReason
		if item.DataStatus == "" {
			item.DataStatus = "正常"
		}
		if state.SellTime != nil {
			// Guard against cross-record pollution when the same stock is recommended
			// multiple times: a historical sell event must not be applied to a later
			// recommendation record.
			if recordTime.IsZero() || !state.SellTime.Before(recordTime) {
				item.SellTime = state.SellTime.Format("2006-01-02 15:04:05")
				item.SellAmount = state.RealizedSellAmount
			} else {
				item.SellTime = "持有"
				item.SellAmount = nil
				item.PositionStatus = "持有"
			}
		} else if item.ActivationStatus == "activated" {
			item.SellTime = "持有"
			if strings.TrimSpace(item.PositionStatus) == "" || item.PositionStatus == "待激活" {
				item.PositionStatus = "持有"
			}
		} else if item.PositionStatus == "已止盈" || item.PositionStatus == "已止损" {
			item.PositionStatus = "持有"
		}
	}
	if item.CurrentPrice <= 0 {
		item.CurrentPrice = item.BuyAmount
	}

	if item.ActivationStatus == "activated" && item.BuyAmount > 0 {
		if item.SellAmount != nil && *item.SellAmount > 0 {
			result := calculateNetYield(item.StockCode, item.BuyAmount, *item.SellAmount)
			if result.Valid {
				item.YieldRate = result.YieldRate
				item.YieldRateText = result.YieldText
			}
		} else if item.CurrentPrice > 0 {
			result := calculateNetYield(item.StockCode, item.BuyAmount, item.CurrentPrice)
			if result.Valid {
				item.YieldRate = result.YieldRate
				item.YieldRateText = result.YieldText
			}
		}
	}
	applyRecommendBacktestEligibilityOverride(&item, &rec)
	applyInactiveYieldDefaults(&item)

	return item
}

func applyRecommendBacktestEligibilityOverride(item *models.AiRecommendStocksYieldItem, rec *models.AiRecommendStocks) {
	if item == nil || rec == nil {
		return
	}
	eligibility, reason := resolveRecommendBacktestEligibility(rec)
	item.BacktestEligibility = eligibility
	item.BacktestEligibilityReason = reason
	switch eligibility {
	case recommendBacktestSkipped:
		activationStatus, positionStatus, dataStatus, _, skip := resolveRecommendYieldSkipInfo(rec)
		if !skip {
			return
		}
		item.ActivationStatus = activationStatus
		item.PositionStatus = positionStatus
		item.DataStatus = dataStatus
		item.DataStatusReason = reason
	case recommendBacktestIneligible:
		item.ActivationStatus = "ineligible"
		item.PositionStatus = "未纳入回测"
		item.DataStatus = "未结构化"
		item.DataStatusReason = reason
	}
}

func applyInactiveYieldDefaults(item *models.AiRecommendStocksYieldItem) {
	if item == nil {
		return
	}
	status := strings.TrimSpace(strings.ToLower(item.ActivationStatus))
	if status == "activated" {
		return
	}

	item.ActivationTime = ""
	item.ActivationPrice = 0
	item.BuyTime = ""
	item.BuyAmount = 0
	item.SellAmount = nil
	item.YieldRate = 0
	item.YieldRateText = "--"

	switch status {
	case "skipped":
		item.SellTime = "已跳过"
		if strings.TrimSpace(item.PositionStatus) == "" || item.PositionStatus == "待激活" || item.PositionStatus == "持有" {
			item.PositionStatus = "已放弃"
		}
		if strings.TrimSpace(item.DataStatus) == "" || item.DataStatus == "正常" || item.DataStatus == "计算中" {
			item.DataStatus = "已跳过"
		}
	case "expired":
		item.SellTime = "过期未触发"
		if strings.TrimSpace(item.PositionStatus) == "" || item.PositionStatus == "待激活" || item.PositionStatus == "持有" {
			item.PositionStatus = "过期未触发"
		}
		if strings.TrimSpace(item.DataStatus) == "" || item.DataStatus == "正常" || item.DataStatus == "计算中" {
			item.DataStatus = "已过期"
		}
	case "ineligible":
		item.SellTime = "未纳入回测"
		item.PositionStatus = "未纳入回测"
		if strings.TrimSpace(item.DataStatus) == "" || item.DataStatus == "正常" || item.DataStatus == "计算中" {
			item.DataStatus = "未结构化"
		}
	case "invalid":
		item.SellTime = "无法回算"
		if strings.TrimSpace(item.PositionStatus) == "" || item.PositionStatus == "待激活" || item.PositionStatus == "持有" {
			item.PositionStatus = "无法回算"
		}
		if strings.TrimSpace(item.DataStatus) == "" || item.DataStatus == "正常" {
			item.DataStatus = "无法判定"
		}
	default:
		item.SellTime = "待激活"
		item.PositionStatus = "待激活"
	}
}

func yieldRowKeyFromRecommend(rec models.AiRecommendStocks, normalizedCode string) string {
	code := strings.TrimSpace(normalizedCode)
	if code == "" {
		code = normalizeRecommendStockCode(rec.StockCode)
	}
	if code == "" {
		code = strings.TrimSpace(rec.StockCode)
	}
	return fmt.Sprintf("%s-%d", code, rec.ID)
}

func calculateYieldTotalByItems(items []models.AiRecommendStocksYieldItem) (float64, string) {
	totalBuy := 0.0
	totalValue := 0.0
	for _, item := range items {
		if strings.TrimSpace(item.BacktestEligibility) != "" && strings.TrimSpace(item.BacktestEligibility) != recommendBacktestEligible {
			continue
		}
		if strings.TrimSpace(item.ActivationStatus) != "activated" {
			continue
		}
		if item.BuyAmount <= 0 {
			continue
		}
		buyCost := calcBuyTradeCost(item.BuyAmount, resolveTradingMarket(item.StockCode))
		if buyCost.NetAmount <= 0 {
			continue
		}
		totalBuy += buyCost.NetAmount
		valuePrice := item.BuyAmount
		if item.SellAmount != nil && *item.SellAmount > 0 {
			valuePrice = *item.SellAmount
		} else if item.CurrentPrice > 0 {
			valuePrice = item.CurrentPrice
		}
		sellNet := calcSellTradeCost(item.BuyAmount, valuePrice, resolveTradingMarket(item.StockCode))
		if sellNet.NetAmount <= 0 {
			continue
		}
		totalValue += sellNet.NetAmount
	}
	if totalBuy <= 0 {
		return 0, "--"
	}
	totalYieldRate := round2((totalValue - totalBuy) / totalBuy * 100)
	return totalYieldRate, formatSignedPercent(totalYieldRate)
}

func calculateBenchmarkSummaryByItems(items []models.AiRecommendStocksYieldItem) benchmarkSummaryResult {
	cacheKey, hasWindow := buildBenchmarkSummaryCacheKey(items)
	if hasWindow {
		if result, ok := loadBenchmarkSummaryCache(cacheKey, false); ok {
			applyBenchmarkRatesToItems(items, result)
			return result
		}
	}
	fallback := defaultBenchmarkSummaryResult()
	if hasWindow {
		if result, ok := loadBenchmarkSummaryCache(cacheKey, true); ok {
			fallback = result
		}
	}
	result := runWithTimeout(benchmarkSummaryCalcTimeout, fallback, func() benchmarkSummaryResult {
		return calculateBenchmarkSummaryByItemsCore(items)
	})
	applyBenchmarkRatesToItems(items, result)
	if hasWindow && hasBenchmarkSummaryMetricData(result) {
		storeBenchmarkSummaryCache(cacheKey, result)
	}
	return result
}

func calculateBenchmarkSummaryByItemsCore(items []models.AiRecommendStocksYieldItem) benchmarkSummaryResult {
	result := defaultBenchmarkSummaryResult()
	entries := buildYieldDailyOverviewEntries(items)
	if len(entries) == 0 {
		return result
	}
	strategySummary := calculateStrategySummaryByEntries(entries)
	result.StrategyXirr = strategySummary.StrategyXirr
	result.StrategyXirrText = strategySummary.StrategyXirrText
	result.MaxDrawdown = strategySummary.MaxDrawdown
	result.MaxDrawdownText = strategySummary.MaxDrawdownText
	startDay, endDay, ok := resolveYieldDailyOverviewWindow(entries)
	if !ok {
		return result
	}
	tradingDays, benchmarkPriceSeries, err := loadYieldDailyOverviewTradingDays(startDay, endDay)
	if err != nil || len(tradingDays) == 0 || benchmarkPriceSeries == nil {
		return result
	}
	benchmarkSeries, itemRateMap, _, benchmarkXirr, _, winRate, medianExcess, comparableCount, ok := calculateCashflowMatchedBenchmark(entries, tradingDays, benchmarkPriceSeries)
	if !ok {
		return result
	}
	for idx := range items {
		rate, hasRate := itemRateMap[items[idx].RecommendID]
		if !hasRate {
			continue
		}
		items[idx].BenchmarkYieldRate = rate
		items[idx].BenchmarkYieldRateText = formatSignedPercent(rate)
		if items[idx].YieldRateText != "" && items[idx].YieldRateText != "--" {
			excess := round2(items[idx].YieldRate - rate)
			items[idx].ExcessYieldRate = excess
			items[idx].ExcessYieldRateText = formatSignedPercent(excess)
		}
	}
	lastTradeDate := tradingDays[len(tradingDays)-1].Format("2006-01-02")
	benchmarkAmount := round2(benchmarkSeries.CumulativeAmountByDay[lastTradeDate])
	costBasis := 0.0
	for _, entry := range entries {
		costBasis += entry.BuyCostNet
	}
	result.Rate = 0
	if costBasis > 0 {
		result.Rate = round2(benchmarkAmount / costBasis * 100)
		result.RateText = formatSignedPercent(result.Rate)
	}
	totalYieldRate, _ := calculateYieldTotalByItems(items)
	result.ExcessYieldRate = round2(totalYieldRate - result.Rate)
	result.ExcessYieldRateText = formatSignedPercent(result.ExcessYieldRate)
	result.BenchmarkXirr = benchmarkXirr
	result.BenchmarkXirrText = formatSignedPercent(benchmarkXirr)
	result.ExcessXirr = round2(result.StrategyXirr - benchmarkXirr)
	result.ExcessXirrText = formatSignedPercent(result.ExcessXirr)
	if comparableCount > 0 {
		result.WinRateVsBenchmark = winRate
		result.WinRateVsBenchmarkText = formatSignedPercent(winRate)
		result.MedianExcessYieldRate = medianExcess
		result.MedianExcessYieldRateText = formatSignedPercent(medianExcess)
	}
	return result
}

func defaultBenchmarkSummaryResult() benchmarkSummaryResult {
	return benchmarkSummaryResult{
		Code:                      defaultBenchmarkModelCode,
		Name:                      defaultBenchmarkName,
		RateText:                  "--",
		ExcessYieldRateText:       "--",
		StrategyXirrText:          "--",
		BenchmarkXirrText:         "--",
		ExcessXirrText:            "--",
		MaxDrawdownText:           "--",
		WinRateVsBenchmarkText:    "--",
		MedianExcessYieldRateText: "--",
	}
}

func hasBenchmarkSummaryMetricData(result benchmarkSummaryResult) bool {
	return hasDisplayMetricText(result.RateText) ||
		hasDisplayMetricText(result.StrategyXirrText) ||
		hasDisplayMetricText(result.MaxDrawdownText)
}

func hasDisplayMetricText(text string) bool {
	text = strings.TrimSpace(text)
	return text != "" && text != "--"
}

func buildYieldDailyOverviewEntries(items []models.AiRecommendStocksYieldItem) []yieldDailyOverviewEntry {
	entries := make([]yieldDailyOverviewEntry, 0, len(items))
	for _, item := range items {
		entry, ok := buildYieldDailyOverviewEntry(item)
		if !ok {
			continue
		}
		entries = append(entries, entry)
	}
	return entries
}

func calculateStrategySummaryByEntries(entries []yieldDailyOverviewEntry) strategySummaryResult {
	result := strategySummaryResult{
		StrategyXirrText: "--",
		MaxDrawdownText:  "--",
	}
	if strategyXirr, ok := calculateStrategyXirrByEntries(entries); ok {
		result.StrategyXirr = strategyXirr
		result.StrategyXirrText = formatSignedPercent(strategyXirr)
	}
	if maxDrawdown, ok := calculateStrategyMaxDrawdownByEntries(entries); ok {
		result.MaxDrawdown = maxDrawdown
		result.MaxDrawdownText = formatSignedPercent(maxDrawdown)
	}
	return result
}

func calculateStrategyXirrByEntries(entries []yieldDailyOverviewEntry) (float64, bool) {
	if len(entries) == 0 {
		return 0, false
	}
	cashflows := make([]xirrCashflow, 0, len(entries)*2)
	for _, entry := range entries {
		if entry.BuyCostNet <= 0 || entry.BuyTime.IsZero() {
			continue
		}
		endTime, endValue, ok := resolveStrategyExitCashflow(entry)
		if !ok || endValue <= 0 {
			continue
		}
		cashflows = append(cashflows, xirrCashflow{At: entry.BuyTime, Amount: -entry.BuyCostNet})
		cashflows = append(cashflows, xirrCashflow{At: endTime, Amount: endValue})
	}
	return calculateXirr(cashflows)
}

func resolveStrategyExitCashflow(entry yieldDailyOverviewEntry) (time.Time, float64, bool) {
	loc := cnLocation()
	if entry.BuyCostNet <= 0 || entry.BuyTime.IsZero() {
		return time.Time{}, 0, false
	}
	endDay := entry.CurrentDay
	endTime := time.Date(endDay.Year(), endDay.Month(), endDay.Day(), 15, 0, 0, 0, loc)
	endValue := resolveStrategyCurrentNetValue(entry)
	if entry.HasSellAmount {
		endDay = entry.SellDay
		endValue = entry.RealizedValueNet
		if sellTime, ok := parseYieldOverviewDisplayTime(entry.SellTime); ok {
			endTime = sellTime
		} else if !endDay.IsZero() {
			endTime = time.Date(endDay.Year(), endDay.Month(), endDay.Day(), 15, 0, 0, 0, loc)
		}
	} else if currentTime, ok := parseYieldOverviewDisplayTime(entry.CurrentPriceTime); ok {
		endTime = currentTime
	}
	if endTime.IsZero() {
		if endDay.IsZero() {
			return time.Time{}, 0, false
		}
		endTime = time.Date(endDay.Year(), endDay.Month(), endDay.Day(), 15, 0, 0, 0, loc)
	}
	if endTime.Before(entry.BuyTime) {
		return time.Time{}, 0, false
	}
	return endTime, round2(endValue), endValue > 0
}

func calculateStrategyMaxDrawdownByEntries(entries []yieldDailyOverviewEntry) (float64, bool) {
	if len(entries) == 0 {
		return 0, false
	}
	startDay, endDay, ok := resolveYieldDailyOverviewWindow(entries)
	if !ok {
		return 0, false
	}
	tradingDays := buildYieldOverviewTradingDays(startDay, endDay)
	if len(tradingDays) == 0 {
		return 0, false
	}
	priceSeriesMap, _, err := loadYieldDailyOverviewPriceSeries(entries, tradingDays)
	if err != nil || len(priceSeriesMap) == 0 {
		return 0, false
	}
	filteredEntries := make([]yieldDailyOverviewEntry, 0, len(entries))
	for _, entry := range entries {
		if _, exists := priceSeriesMap[entry.StockCode]; !exists {
			continue
		}
		filteredEntries = append(filteredEntries, entry)
	}
	if len(filteredEntries) == 0 {
		return 0, false
	}
	return calculateMaxDrawdownByDailyRatesWithPriceSeries(filteredEntries, tradingDays, priceSeriesMap), true
}

func buildYieldOverviewTradingDays(startDay, endDay time.Time) []time.Time {
	loc := cnLocation()
	startDay = time.Date(startDay.In(loc).Year(), startDay.In(loc).Month(), startDay.In(loc).Day(), 0, 0, 0, 0, loc)
	endDay = time.Date(endDay.In(loc).Year(), endDay.In(loc).Month(), endDay.In(loc).Day(), 0, 0, 0, 0, loc)
	if startDay.IsZero() || endDay.IsZero() || endDay.Before(startDay) {
		return nil
	}
	tradingDays := make([]time.Time, 0, int(endDay.Sub(startDay).Hours()/24)+1)
	for day := startDay; !day.After(endDay); day = day.AddDate(0, 0, 1) {
		if IsCNOpenTradeDay(day) {
			tradingDays = append(tradingDays, day)
		}
	}
	return tradingDays
}

func buildBenchmarkSummaryCacheKey(items []models.AiRecommendStocksYieldItem) (string, bool) {
	if len(items) == 0 {
		return "", false
	}
	loc := cnLocation()
	var earliest time.Time
	var latest time.Time
	count := 0
	for _, item := range items {
		entry, ok := buildYieldDailyOverviewEntry(item)
		if !ok {
			continue
		}
		if earliest.IsZero() || entry.BuyDay.Before(earliest) {
			earliest = entry.BuyDay
		}
		candidate := entry.CurrentDay
		if !entry.SellDay.IsZero() {
			candidate = entry.SellDay
		}
		if latest.IsZero() || candidate.After(latest) {
			latest = candidate
		}
		count += 1
	}
	if count == 0 {
		return "", false
	}
	return fmt.Sprintf(
		"%s:%s:%d:%s",
		earliest.In(loc).Format("2006-01-02"),
		latest.In(loc).Format("2006-01-02"),
		count,
		defaultBenchmarkModelCode,
	), true
}

func loadBenchmarkSummaryCache(cacheKey string, allowExpired bool) (benchmarkSummaryResult, bool) {
	if strings.TrimSpace(cacheKey) == "" {
		return benchmarkSummaryResult{}, false
	}
	globalBenchmarkSummaryCache.mu.RLock()
	defer globalBenchmarkSummaryCache.mu.RUnlock()
	if globalBenchmarkSummaryCache.key != cacheKey {
		return benchmarkSummaryResult{}, false
	}
	if strings.TrimSpace(globalBenchmarkSummaryCache.result.RateText) == "" {
		return benchmarkSummaryResult{}, false
	}
	if !allowExpired && time.Now().After(globalBenchmarkSummaryCache.expireAt) {
		return benchmarkSummaryResult{}, false
	}
	return globalBenchmarkSummaryCache.result, true
}

func storeBenchmarkSummaryCache(cacheKey string, result benchmarkSummaryResult) {
	if strings.TrimSpace(cacheKey) == "" || strings.TrimSpace(result.RateText) == "" {
		return
	}
	globalBenchmarkSummaryCache.mu.Lock()
	defer globalBenchmarkSummaryCache.mu.Unlock()
	globalBenchmarkSummaryCache.key = cacheKey
	globalBenchmarkSummaryCache.result = result
	globalBenchmarkSummaryCache.expireAt = time.Now().Add(benchmarkSummaryCacheTTL)
}

func applyBenchmarkRatesToItems(items []models.AiRecommendStocksYieldItem, result benchmarkSummaryResult) {
	for idx := range items {
		if items[idx].BenchmarkYieldRateText == "" {
			items[idx].BenchmarkYieldRateText = "--"
		}
		if items[idx].ExcessYieldRateText == "" {
			items[idx].ExcessYieldRateText = "--"
		}
	}
	for idx := range items {
		if result.ItemRateByRecommendID == nil {
			continue
		}
		rate, ok := result.ItemRateByRecommendID[items[idx].RecommendID]
		if !ok {
			continue
		}
		items[idx].BenchmarkYieldRate = rate
		items[idx].BenchmarkYieldRateText = formatSignedPercent(rate)
		if items[idx].YieldRateText != "" && items[idx].YieldRateText != "--" {
			excess := round2(items[idx].YieldRate - rate)
			items[idx].ExcessYieldRate = excess
			items[idx].ExcessYieldRateText = formatSignedPercent(excess)
		}
	}
}

func estimateBenchmarkKlineDays(startDay, endDay time.Time) int64 {
	if startDay.IsZero() {
		return 120
	}
	if endDay.IsZero() || endDay.Before(startDay) {
		endDay = startDay
	}
	calendarDays := int(endDay.Sub(startDay).Hours()/24) + 1
	if calendarDays < 1 {
		calendarDays = 1
	}
	days := calendarDays*2 + 30
	if days < 120 {
		days = 120
	}
	if days > 5000 {
		days = 5000
	}
	return int64(days)
}

func resolveBenchmarkEndPrice(indexCode string, fallback float64, minDay time.Time) float64 {
	if fallback <= 0 {
		return 0
	}
	quote := loadLatestCachedBenchmarkQuote(indexCode)
	return resolveBenchmarkEndPriceFromCachedQuote(fallback, quote, minDay)
}

func resolveBenchmarkEndPriceFromCachedQuote(fallback float64, quote *StockInfo, minDay time.Time) float64 {
	if fallback <= 0 {
		return 0
	}
	if quote == nil {
		return fallback
	}
	price, ok := parseBuyPrice(strings.TrimSpace(quote.Price))
	if !ok || price <= 0 {
		return fallback
	}
	if !minDay.IsZero() {
		quoteDay, ok := parseBenchmarkQuoteDay(quote.Date, quote.UpdatedAt)
		if !ok || quoteDay.Before(time.Date(minDay.Year(), minDay.Month(), minDay.Day(), 0, 0, 0, 0, cnLocation())) {
			return fallback
		}
	}
	return price
}

func loadLatestCachedBenchmarkQuote(indexCode string) *StockInfo {
	if db.Dao == nil {
		return nil
	}
	code := strings.ToLower(strings.TrimSpace(indexCode))
	if code == "" {
		return nil
	}
	var quote StockInfo
	err := db.Dao.
		Model(&StockInfo{}).
		Where("lower(code) = ?", code).
		Order("date desc").
		Order("time desc").
		Order("updated_at desc").
		Order("id desc").
		First(&quote).Error
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			logErrorEvery("AiRecommendStocksService.loadLatestCachedBenchmarkQuote", 10*time.Minute, "load cached benchmark quote err:%s", err.Error())
		}
		return nil
	}
	return &quote
}

func parseBenchmarkQuoteDay(dateText string, updatedAt time.Time) (time.Time, bool) {
	loc := cnLocation()
	text := strings.TrimSpace(dateText)
	if text != "" {
		for _, layout := range []string{"2006-01-02", "20060102", "2006/01/02"} {
			if t, err := time.ParseInLocation(layout, text, loc); err == nil {
				return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc), true
			}
		}
	}
	if !updatedAt.IsZero() {
		t := updatedAt.In(loc)
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc), true
	}
	return time.Time{}, false
}

func normalizeSSEBenchmarkStartOpenDay(recordTime time.Time) time.Time {
	return normalizeBenchmarkStartOpenDay(recordTime)
}

func estimateSSEBenchmarkKlineDays(startDay, endDay time.Time) int64 {
	return estimateBenchmarkKlineDays(startDay, endDay)
}

func selectSSEBenchmarkOpenClose(kLines []KLineData, startOpenDay time.Time) (float64, float64, bool) {
	return selectBenchmarkOpenClose(kLines, startOpenDay)
}

func selectSSEBenchmarkOpenCloseWindow(kLines []KLineData, startOpenDay time.Time) (float64, float64, time.Time, bool) {
	return selectBenchmarkOpenCloseWindow(kLines, startOpenDay)
}

func resolveSSEBenchmarkEndPriceFromCachedQuote(fallback float64, quote *StockInfo, minDay time.Time) float64 {
	return resolveBenchmarkEndPriceFromCachedQuote(fallback, quote, minDay)
}

func parseSSEBenchmarkQuoteDay(dateText string, updatedAt time.Time) (time.Time, bool) {
	return parseBenchmarkQuoteDay(dateText, updatedAt)
}

func calculateSSEBenchmarkRateByItems(items []models.AiRecommendStocksYieldItem) (float64, string) {
	result := calculateBenchmarkSummaryByItems(items)
	return result.Rate, result.RateText
}

func normalizeBenchmarkStartOpenDay(recordTime time.Time) time.Time {
	if recordTime.IsZero() {
		return time.Time{}
	}
	loc := cnLocation()
	recordTime = recordTime.In(loc)
	day := time.Date(recordTime.Year(), recordTime.Month(), recordTime.Day(), 0, 0, 0, 0, loc)
	if isCNOpenTradeDaySafe(day) {
		return day
	}
	return shiftToNextCNOpenTradeDaySafe(day)
}

func selectBenchmarkOpenClose(kLines []KLineData, startOpenDay time.Time) (float64, float64, bool) {
	startOpen, endClose, _, ok := selectBenchmarkOpenCloseWindow(kLines, startOpenDay)
	return startOpen, endClose, ok
}

func selectBenchmarkOpenCloseWindow(kLines []KLineData, startOpenDay time.Time) (float64, float64, time.Time, bool) {
	if len(kLines) == 0 {
		return 0, 0, time.Time{}, false
	}
	type benchmarkPoint struct {
		Day   time.Time
		Open  float64
		Close float64
	}
	points := make([]benchmarkPoint, 0, len(kLines))
	for _, line := range kLines {
		day, ok := parseKLineDayInCN(line.Day)
		if !ok {
			continue
		}
		open, errOpen := strconv.ParseFloat(strings.TrimSpace(line.Open), 64)
		closePrice, errClose := strconv.ParseFloat(strings.TrimSpace(line.Close), 64)
		if errOpen != nil || errClose != nil || open <= 0 || closePrice <= 0 {
			continue
		}
		points = append(points, benchmarkPoint{
			Day:   day,
			Open:  open,
			Close: closePrice,
		})
	}
	if len(points) == 0 {
		return 0, 0, time.Time{}, false
	}
	sort.Slice(points, func(i, j int) bool {
		return points[i].Day.Before(points[j].Day)
	})

	startOpen := 0.0
	for _, point := range points {
		if point.Day.Before(startOpenDay) {
			continue
		}
		startOpen = point.Open
		break
	}
	if startOpen <= 0 {
		return 0, 0, time.Time{}, false
	}

	endClose := 0.0
	lastCloseDay := time.Time{}
	for i := len(points) - 1; i >= 0; i-- {
		if points[i].Close <= 0 {
			continue
		}
		endClose = points[i].Close
		lastCloseDay = points[i].Day
		break
	}
	if endClose <= 0 {
		return 0, 0, time.Time{}, false
	}
	return startOpen, endClose, lastCloseDay, true
}

func calculateCashflowMatchedBenchmark(
	entries []yieldDailyOverviewEntry,
	tradingDays []time.Time,
	priceSeries *yieldDailyOverviewPriceSeries,
) (*benchmarkDailySeries, map[uint]float64, float64, float64, float64, float64, float64, int, bool) {
	if len(entries) == 0 || len(tradingDays) == 0 || priceSeries == nil {
		return nil, nil, 0, 0, 0, 0, 0, 0, false
	}
	positions := make([]benchmarkCashflowPosition, 0, len(entries))
	strategyCashflows := make([]xirrCashflow, 0, len(entries)*2)
	benchmarkCashflows := make([]xirrCashflow, 0, len(entries)*2)
	itemRateMap := make(map[uint]float64, len(entries))
	excesses := make([]float64, 0, len(entries))
	winCount := 0
	comparableCount := 0
	for _, entry := range entries {
		buyClose := priceSeries.CloseByDay[entry.BuyDay.Format("2006-01-02")]
		if buyClose <= 0 || entry.BuyCostNet <= 0 {
			continue
		}
		benchmarkBuy := calcBenchmarkETFBuyTrade(entry.BuyCostNet, buyClose)
		if !benchmarkBuy.Valid || benchmarkBuy.Shares <= 0 {
			continue
		}
		endDay := entry.CurrentDay
		endTime := time.Date(endDay.Year(), endDay.Month(), endDay.Day(), 15, 0, 0, 0, cnLocation())
		if !entry.SellDay.IsZero() {
			endDay = entry.SellDay
			if sellTime, ok := parseYieldOverviewDisplayTime(entry.SellTime); ok {
				endTime = sellTime
			} else {
				endTime = time.Date(endDay.Year(), endDay.Month(), endDay.Day(), 15, 0, 0, 0, cnLocation())
			}
		} else if currentTime, ok := parseYieldOverviewDisplayTime(entry.CurrentPriceTime); ok {
			endTime = currentTime
		}
		endPrice := priceSeries.CloseByDay[endDay.Format("2006-01-02")]
		if entry.HasSellAmount && endPrice <= 0 {
			endPrice = buyClose
		}
		if !entry.HasSellAmount && endDay.Equal(entry.CurrentDay) {
			endPrice = resolveBenchmarkEndPrice(defaultBenchmarkCode, endPrice, endDay)
		}
		if endPrice <= 0 {
			endPrice = buyClose
		}
		position := benchmarkCashflowPosition{
			RecommendID:      entry.RecommendID,
			BuyDay:           entry.BuyDay,
			EndDay:           endDay,
			EndTime:          endTime,
			InvestedNet:      entry.BuyCostNet,
			Shares:           benchmarkBuy.Shares,
			SellAmount:       endPrice,
			HasSellAmount:    entry.HasSellAmount,
			CurrentPrice:     endPrice,
			CurrentDay:       entry.CurrentDay,
			CurrentPriceTime: entry.CurrentPriceTime,
		}
		strategyEndValue := entry.RealizedValueNet
		if !entry.HasSellAmount {
			strategyEndValue = resolveStrategyCurrentNetValue(entry)
		}
		benchmarkSell := calcBenchmarkETFSellTrade(position.Shares, endPrice)
		if !benchmarkSell.Valid || benchmarkSell.NetAmount <= 0 {
			continue
		}
		benchmarkEndValue := benchmarkSell.NetAmount
		positions = append(positions, position)
		strategyCashflows = append(strategyCashflows, xirrCashflow{At: entry.BuyTime, Amount: -entry.BuyCostNet})
		benchmarkCashflows = append(benchmarkCashflows, xirrCashflow{At: entry.BuyTime, Amount: -entry.BuyCostNet})
		strategyCashflows = append(strategyCashflows, xirrCashflow{At: endTime, Amount: strategyEndValue})
		benchmarkCashflows = append(benchmarkCashflows, xirrCashflow{At: endTime, Amount: benchmarkEndValue})
		if entry.BuyCostNet > 0 && benchmarkEndValue > 0 {
			benchmarkRate := round2((benchmarkEndValue - entry.BuyCostNet) / entry.BuyCostNet * 100)
			itemRateMap[entry.RecommendID] = benchmarkRate
		}
	}
	if len(positions) == 0 {
		return nil, nil, 0, 0, 0, 0, 0, 0, false
	}

	valueByDay := make(map[string]float64, len(tradingDays))
	cumulativeAmountByDay := make(map[string]float64, len(tradingDays))
	dailyAmountByDay := make(map[string]float64, len(tradingDays))
	cumulativeRateByDay := make(map[string]float64, len(tradingDays))
	dailyRateByDay := make(map[string]float64, len(tradingDays))
	navByDay := make(map[string]float64, len(tradingDays))

	prevValue := 0.0
	nav := 1.0
	moneyMarketDailyRate := moneyMarketAnnualRate / 365.0 / 100.0
	for _, tradeDay := range tradingDays {
		tradeDate := tradeDay.Format("2006-01-02")
		totalValue := 0.0
		costBasisNet := 0.0
		dailyHoldingCostNet := 0.0
		for _, entry := range entries {
			if tradeDay.Before(entry.BuyDay) {
				continue
			}
			costBasisNet += entry.BuyCostNet
			if shouldIncludeYieldDailyOverviewEntryInDailyCost(entry, tradeDay) {
				dailyHoldingCostNet += entry.BuyCostNet
			}
		}
		for _, position := range positions {
			if tradeDay.Before(position.BuyDay) {
				continue
			}
			price := priceSeries.CloseByDay[tradeDate]
			if !position.EndDay.IsZero() && !tradeDay.Before(position.EndDay) {
				price = position.SellAmount
			}
			if price <= 0 {
				continue
			}
			benchmarkValue := calcBenchmarkETFSellTrade(position.Shares, price)
			if !benchmarkValue.Valid || benchmarkValue.NetAmount <= 0 {
				continue
			}
			totalValue += benchmarkValue.NetAmount
		}
		cumulativeAmount := round2(totalValue - costBasisNet)
		dailyAmount := round2(totalValue - prevValue)
		benchmarkDailyRate := 0.0
		benchmarkCumulativeRate := 0.0
		if costBasisNet > 0 {
			benchmarkCumulativeRate = round2(cumulativeAmount / costBasisNet * 100)
		}
		if dailyHoldingCostNet > 0 {
			benchmarkDailyRate = round2(dailyAmount / dailyHoldingCostNet * 100)
			nav = round4(nav * (1 + benchmarkDailyRate/100))
		} else if costBasisNet > 0 {
			// 空仓期：按货币基金收益计算（年化2.5%）
			idleCashDailyReturn := prevValue * moneyMarketDailyRate
			dailyAmount = round2(idleCashDailyReturn)
			if prevValue > 0 {
				benchmarkDailyRate = round2(dailyAmount / prevValue * 100)
				nav = round4(nav * (1 + benchmarkDailyRate/100))
			}
			totalValue = prevValue + dailyAmount
			cumulativeAmount = round2(totalValue - costBasisNet)
			if costBasisNet > 0 {
				benchmarkCumulativeRate = round2(cumulativeAmount / costBasisNet * 100)
			}
		}
		valueByDay[tradeDate] = round2(totalValue)
		cumulativeAmountByDay[tradeDate] = cumulativeAmount
		dailyAmountByDay[tradeDate] = dailyAmount
		cumulativeRateByDay[tradeDate] = benchmarkCumulativeRate
		dailyRateByDay[tradeDate] = benchmarkDailyRate
		navByDay[tradeDate] = round4(nav)
		prevValue = totalValue
	}

	for _, entry := range entries {
		rate, ok := itemRateMap[entry.RecommendID]
		if !ok {
			continue
		}
		var strategyRate float64
		strategyEndValue := entry.RealizedValueNet
		if !entry.HasSellAmount {
			strategyEndValue = resolveStrategyCurrentNetValue(entry)
		}
		if entry.BuyCostNet > 0 {
			strategyRate = round2((strategyEndValue - entry.BuyCostNet) / entry.BuyCostNet * 100)
			excess := round2(strategyRate - rate)
			excesses = append(excesses, excess)
			comparableCount += 1
			if excess > 0 {
				winCount += 1
			}
		}
	}

	strategyXirr, strategyOK := calculateXirr(strategyCashflows)
	benchmarkXirr, benchmarkOK := calculateXirr(benchmarkCashflows)
	maxDrawdown := calculateMaxDrawdownByDailyRates(entries, tradingDays)
	winRate := 0.0
	if comparableCount > 0 {
		winRate = round2(float64(winCount) / float64(comparableCount) * 100)
	}
	medianExcess := medianFloat64(excesses)
	series := &benchmarkDailySeries{
		Code:                  defaultBenchmarkModelCode,
		Name:                  defaultBenchmarkName,
		CloseByDay:            priceSeries.CloseByDay,
		ValueByDay:            valueByDay,
		CumulativeAmountByDay: cumulativeAmountByDay,
		DailyAmountByDay:      dailyAmountByDay,
		CumulativeRateByDay:   cumulativeRateByDay,
		DailyRateByDay:        dailyRateByDay,
		NavByDay:              navByDay,
	}
	if !strategyOK {
		strategyXirr = 0
	}
	if !benchmarkOK {
		benchmarkXirr = 0
	}
	return series, itemRateMap, strategyXirr, benchmarkXirr, maxDrawdown, winRate, medianExcess, comparableCount, true
}

func resolveStrategyCurrentNetValue(entry yieldDailyOverviewEntry) float64 {
	price := entry.CurrentPrice
	if price <= 0 {
		price = entry.BuyAmount
	}
	sellNet := calcSellTradeCost(entry.BuyAmount, price, resolveTradingMarket(entry.StockCode))
	return round2(sellNet.NetAmount)
}

func calculateXirr(cashflows []xirrCashflow) (float64, bool) {
	if len(cashflows) < 2 {
		return 0, false
	}
	sort.Slice(cashflows, func(i, j int) bool {
		return cashflows[i].At.Before(cashflows[j].At)
	})
	hasPositive := false
	hasNegative := false
	base := cashflows[0].At
	for _, cf := range cashflows {
		if cf.Amount > 0 {
			hasPositive = true
		}
		if cf.Amount < 0 {
			hasNegative = true
		}
	}
	if !hasPositive || !hasNegative {
		return 0, false
	}
	npv := func(rate float64) float64 {
		total := 0.0
		for _, cf := range cashflows {
			years := cf.At.Sub(base).Hours() / 24.0 / 365.0
			total += cf.Amount / math.Pow(1+rate, years)
		}
		return total
	}
	dnpv := func(rate float64) float64 {
		total := 0.0
		for _, cf := range cashflows {
			years := cf.At.Sub(base).Hours() / 24.0 / 365.0
			if years == 0 {
				continue
			}
			total += -years * cf.Amount / math.Pow(1+rate, years+1)
		}
		return total
	}
	rate := 0.1
	for i := 0; i < 20; i++ {
		value := npv(rate)
		derivative := dnpv(rate)
		if math.Abs(derivative) < 1e-9 {
			break
		}
		next := rate - value/derivative
		if next <= -0.9999 || math.IsNaN(next) || math.IsInf(next, 0) {
			break
		}
		if math.Abs(next-rate) < 1e-7 {
			return round2(next * 100), true
		}
		rate = next
	}
	low := -0.9999
	high := 10.0
	lowValue := npv(low)
	highValue := npv(high)
	if math.IsNaN(lowValue) || math.IsNaN(highValue) || lowValue*highValue > 0 {
		return 0, false
	}
	for i := 0; i < 100; i++ {
		mid := (low + high) / 2
		value := npv(mid)
		if math.Abs(value) < 1e-7 {
			return round2(mid * 100), true
		}
		if lowValue*value < 0 {
			high = mid
		} else {
			low = mid
			lowValue = value
		}
	}
	return round2(((low + high) / 2) * 100), true
}

func calculateMaxDrawdownByDailyRates(entries []yieldDailyOverviewEntry, tradingDays []time.Time) float64 {
	if len(entries) == 0 || len(tradingDays) == 0 {
		return 0
	}
	priceSeriesMap, _, err := loadYieldDailyOverviewPriceSeries(entries, tradingDays)
	if err != nil || len(priceSeriesMap) == 0 {
		return 0
	}
	return calculateMaxDrawdownByDailyRatesWithPriceSeries(entries, tradingDays, priceSeriesMap)
}

func calculateMaxDrawdownByDailyRatesWithPriceSeries(
	entries []yieldDailyOverviewEntry,
	tradingDays []time.Time,
	priceSeriesMap map[string]*yieldDailyOverviewPriceSeries,
) float64 {
	if len(entries) == 0 || len(tradingDays) == 0 || len(priceSeriesMap) == 0 {
		return 0
	}
	points := buildYieldDailyOverviewPoints(entries, tradingDays, priceSeriesMap, nil)
	if len(points) == 0 {
		return 0
	}
	peak := 1.0
	nav := 1.0
	maxDrawdown := 0.0
	for _, point := range points {
		nav = round4(nav * (1 + point.DailyYieldRate/100))
		if nav > peak {
			peak = nav
		}
		if peak > 0 {
			drawdown := round2((nav - peak) / peak * 100)
			if drawdown < maxDrawdown {
				maxDrawdown = drawdown
			}
		}
	}
	return maxDrawdown
}

func medianFloat64(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	cloned := append([]float64(nil), values...)
	sort.Float64s(cloned)
	mid := len(cloned) / 2
	if len(cloned)%2 == 1 {
		return round2(cloned[mid])
	}
	return round2((cloned[mid-1] + cloned[mid]) / 2)
}

func round4(value float64) float64 {
	return math.Round(value*10000) / 10000
}

func parseKLineDayInCN(rawDay string) (time.Time, bool) {
	text := strings.TrimSpace(rawDay)
	if text == "" {
		return time.Time{}, false
	}
	loc := cnLocation()
	if t, err := time.ParseInLocation("2006-01-02", text, loc); err == nil {
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc), true
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", text, loc); err == nil {
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc), true
	}
	return time.Time{}, false
}

func isCNOpenTradeDaySafe(day time.Time) (open bool) {
	loc := cnLocation()
	day = time.Date(day.In(loc).Year(), day.In(loc).Month(), day.In(loc).Day(), 0, 0, 0, 0, loc)
	open = !isWeekendCN(day)
	defer func() {
		if recover() != nil {
			open = !isWeekendCN(day)
		}
	}()
	open = isCNOpenTradeDay(day)
	return open
}

func shiftToNextCNOpenTradeDaySafe(day time.Time) (result time.Time) {
	loc := cnLocation()
	day = time.Date(day.In(loc).Year(), day.In(loc).Month(), day.In(loc).Day(), 0, 0, 0, 0, loc)
	result = shiftToNextWeekday(day)
	defer func() {
		if recover() != nil {
			result = shiftToNextWeekday(day)
		}
	}()
	if d := shiftToNextCNOpenTradeDay(day); !d.IsZero() {
		result = d
	}
	return result
}

func shiftToPrevCNOpenTradeDaySafe(day time.Time) (result time.Time) {
	loc := cnLocation()
	day = time.Date(day.In(loc).Year(), day.In(loc).Month(), day.In(loc).Day(), 0, 0, 0, 0, loc)
	result = shiftToPrevWeekday(day)
	defer func() {
		if recover() != nil {
			result = shiftToPrevWeekday(day)
		}
	}()
	if d := shiftToPrevCNOpenTradeDay(day); !d.IsZero() {
		result = d
	}
	return result
}

func shiftToNextWeekday(day time.Time) time.Time {
	cur := day
	for i := 0; i < 14; i++ {
		if !isWeekendCN(cur) {
			return cur
		}
		cur = cur.AddDate(0, 0, 1)
	}
	return cur
}

func shiftToPrevWeekday(day time.Time) time.Time {
	cur := day
	for i := 0; i < 14; i++ {
		if !isWeekendCN(cur) {
			return cur
		}
		cur = cur.AddDate(0, 0, -1)
	}
	return cur
}

// StartAiRecommendMinuteDownload 手动触发分钟线下载+收益重算（全持仓）
func (s *AiRecommendStocksService) StartAiRecommendMinuteDownload() (map[string]any, error) {
	return startManualAiRecommendMinuteDownload()
}

// GetAiRecommendYieldErrorLogs 获取股票收益率相关错误日志（已做中文可读化）
func (s *AiRecommendStocksService) GetAiRecommendYieldErrorLogs(limit int) ([]map[string]string, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}

	logs := make([]map[string]string, 0, limit)
	seen := make(map[string]struct{}, limit*2)
	appendLog := func(ts time.Time, source, stockCode, stockName, status, rawReason string) {
		rawReason = strings.TrimSpace(rawReason)
		if rawReason == "" {
			return
		}
		displayTime := "--"
		if !ts.IsZero() {
			displayTime = ts.In(cnLocation()).Format("2006-01-02 15:04:05")
		}
		key := displayTime + "|" + strings.TrimSpace(source) + "|" + strings.TrimSpace(stockCode) + "|" + strings.TrimSpace(status) + "|" + rawReason
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		logs = append(logs, map[string]string{
			"time":      displayTime,
			"source":    strings.TrimSpace(source),
			"stockCode": strings.TrimSpace(stockCode),
			"stockName": strings.TrimSpace(stockName),
			"status":    strings.TrimSpace(status),
			"reason":    humanizeYieldErrorReason(rawReason),
			"rawReason": rawReason,
		})
	}

	if snap := GetDiemengSelfCheckSnapshot(); !snap.CheckedAt.IsZero() {
		for _, probe := range snap.Probes {
			if len(logs) >= limit {
				break
			}
			status := strings.TrimSpace(probe.Status)
			switch status {
			case "ok":
				status = "通过"
			case "skipped":
				status = "未配置"
			default:
				status = "失败"
			}
			rawReason := strings.TrimSpace(probe.Summary)
			if rawReason == "" {
				rawReason = "暂无详情"
			}
			appendLog(snap.CheckedAt, "蝶梦自检", "", strings.TrimSpace(probe.Label), status, rawReason)
		}
	}

	meta := models.AiRecommendYieldMeta{}
	var metaPtr *models.AiRecommendYieldMeta
	if err := db.Dao.Model(&models.AiRecommendYieldMeta{}).First(&meta).Error; err == nil {
		metaPtr = &meta
		if msg := strings.TrimSpace(meta.LastError); msg != "" {
			appendLog(meta.UpdatedAt, "系统", "", "", "任务错误", msg)
		}
		if msg := strings.TrimSpace(meta.AkshareInstallError); msg != "" {
			appendLog(meta.UpdatedAt, "系统", "", "", "AkShare 环境", msg)
		}
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return logs, err
	}

	rows := make([]models.AiRecommendYieldState, 0, limit*2)
	if err := db.Dao.Model(&models.AiRecommendYieldState{}).
		Where("TRIM(COALESCE(data_status_reason, '')) <> '' OR (TRIM(COALESCE(data_status, '')) <> '' AND data_status <> ?)", "正常").
		Order("updated_at DESC").
		Limit(limit * 2).
		Find(&rows).Error; err != nil {
		return logs, err
	}

	for _, row := range rows {
		if len(logs) >= limit {
			break
		}
		status := strings.TrimSpace(row.DataStatus)
		if status == "" {
			status = strings.TrimSpace(row.PositionStatus)
		}
		if status == "" {
			status = "异常"
		}
		reason := strings.TrimSpace(row.DataStatusReason)
		if reason == "" && status != "正常" {
			reason = status
		}
		appendLog(row.UpdatedAt, "持仓计算", row.StockCode, row.StockName, status, reason)
	}

	// Also include synthetic "coverage check" rows so pending/uncoverable records
	// are visible even when state status is still "正常".
	if len(logs) < limit {
		_, issues := computeMinuteDownloadCoverageStatsWithIssues(metaPtr, limit-len(logs))
		for _, issue := range issues {
			if len(logs) >= limit {
				break
			}
			appendLog(issue.RecordTime, "覆盖检查", issue.StockCode, issue.StockName, issue.Status, issue.RawReason)
		}
	}

	sort.SliceStable(logs, func(i, j int) bool {
		return logs[i]["time"] > logs[j]["time"]
	})
	if len(logs) > limit {
		logs = logs[:limit]
	}
	return logs, nil
}

func humanizeYieldErrorReason(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	lower := strings.ToLower(raw)
	msgs := make([]string, 0, 8)
	addMsg := func(msg string) {
		msg = strings.TrimSpace(msg)
		if msg == "" {
			return
		}
		for _, existing := range msgs {
			if existing == msg {
				return
			}
		}
		msgs = append(msgs, msg)
	}

	if strings.Contains(raw, "分钟线覆盖不完整") {
		addMsg("分钟线覆盖不完整，目标时间段数据还没补齐")
	}
	if strings.Contains(raw, "分钟线不可用") {
		addMsg("分钟线暂时不可用，当前无法判定止盈止损")
	}
	if strings.Contains(lower, "akshare temporarily disabled until") {
		addMsg("AkShare 已临时熔断，稍后会自动恢复")
	}
	if strings.Contains(lower, "run akshare script failed") {
		addMsg("AkShare 脚本执行失败")
	}
	if strings.Contains(lower, "diemeng rate limited") || strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "too many requests") || strings.Contains(lower, "429") {
		addMsg("请求过于频繁，数据源触发限流")
	}
	if strings.Contains(lower, "diemeng returned empty data") {
		addMsg("数据源返回空数据，可能是上游无该时间段分钟线/接口口径变化；可尝试开启 HTTP 代理或切换分钟线数据源")
	}
	if strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded") || strings.Contains(lower, "timed out") {
		addMsg("请求超时，可能是网络波动或上游响应慢")
	}
	if strings.Contains(lower, "no such host") {
		addMsg("域名解析失败，请检查网络或 DNS")
	}
	if strings.Contains(lower, "connection refused") {
		addMsg("目标服务拒绝连接")
	}
	if strings.Contains(lower, "proxyconnect tcp") {
		addMsg("代理连接失败，请检查代理设置")
	}
	if strings.Contains(lower, "dial tcp") {
		addMsg("网络连接失败")
	}
	if strings.Contains(lower, "remote end closed connection") || strings.Contains(lower, "remotedisconnected") ||
		strings.Contains(lower, "connection reset by peer") || lower == "eof" || strings.HasSuffix(lower, ": eof") {
		addMsg("连接被服务端中断")
	}
	if strings.Contains(lower, "tls handshake timeout") {
		addMsg("TLS 握手超时")
	}

	if len(msgs) == 0 {
		return raw
	}
	return strings.Join(msgs, "；")
}

func (s *AiRecommendStocksService) buildYieldFallbackPage(
	query *models.AiRecommendStocksQuery,
	page, pageSize int,
	dataAsOf string,
	recalcInProgress bool,
	recalcProgress int,
	manualCooldownUntil string,
	manualCooldownRemainSec int,
	coverableStartMinute time.Time,
) (*models.AiRecommendStocksYieldPageData, error) {
	_ = coverableStartMinute
	strategyCohort := normalizeStrategyCohort("", strategyCohortCurrent)
	if query != nil {
		strategyCohort = normalizeStrategyCohort(query.StrategyCohort, strategyCohortCurrent)
	}
	diemengHealthStatus, diemengHealthSummary, diemengHealthCheckedAt := GetDiemengSelfCheckView()
	meta := models.AiRecommendYieldMeta{}
	if err := db.Dao.Model(&models.AiRecommendYieldMeta{}).First(&meta).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		logger.SugaredLogger.Warnf("load ai_recommend_yield_meta fallback failed: %v", err)
	}
	records, err := listAiRecommendStocksForYield(query, time.Time{})
	if err != nil {
		return nil, err
	}
	rawRepeatCountMap := countRecommendOccurrencesByCode(records)
	records = collapseRecommendRecordsSameDayByCode(records)
	if len(records) == 0 {
		stats := computeMinuteDownloadCoverageStats(nil)
		return &models.AiRecommendStocksYieldPageData{
			List:                      []models.AiRecommendStocksYieldItem{},
			Total:                     0,
			Page:                      page,
			PageSize:                  pageSize,
			TotalPages:                0,
			StrategyCohort:            strategyCohort,
			TotalYieldRate:            0,
			TotalYieldRateText:        "--",
			BenchmarkCode:             defaultBenchmarkModelCode,
			BenchmarkName:             defaultBenchmarkName,
			BenchmarkRate:             0,
			BenchmarkRateText:         "--",
			ExcessYieldRate:           0,
			ExcessYieldRateText:       "--",
			StrategyXirr:              0,
			StrategyXirrText:          "--",
			BenchmarkXirr:             0,
			BenchmarkXirrText:         "--",
			ExcessXirr:                0,
			ExcessXirrText:            "--",
			MaxDrawdown:               0,
			MaxDrawdownText:           "--",
			WinRateVsBenchmark:        0,
			WinRateVsBenchmarkText:    "--",
			MedianExcessYieldRate:     0,
			MedianExcessYieldRateText: "--",
			DataAsOf:                  dataAsOf,
			RecalcInProgress:          recalcInProgress,
			RecalcProgress:            recalcProgress,
			MinuteDownloadDone:        stats.Done,
			MinuteDownloadTotal:       stats.Total,
			MinuteDownloadPending:     stats.Pending,
			MinuteDownloadUncoverable: stats.Uncoverable,
			ManualCooldownUntil:       manualCooldownUntil,
			ManualCooldownRemainSec:   manualCooldownRemainSec,
			LastManualStartedAt:       formatOptionalYieldMetaTime(meta.LastManualDownloadAt),
			LastManualFinishedAt:      formatOptionalYieldMetaTime(meta.LastManualFinishedAt),
			LastManualScopeCount:      meta.LastManualScopeCount,
			LastManualPrefetchMs:      meta.LastManualPrefetchMs,
			LastManualRecalcMs:        meta.LastManualRecalcMs,
			LastManualTotalMs:         meta.LastManualTotalMs,
			LastManualSqliteBusyCount: meta.LastManualSqliteBusyCount,
			LastManualProviderSummary: strings.TrimSpace(meta.LastManualProviderSummary),
			DiemengHealthStatus:       diemengHealthStatus,
			DiemengHealthSummary:      diemengHealthSummary,
			DiemengHealthCheckedAt:    diemengHealthCheckedAt,
		}, nil
	}

	recordStateMap, err := loadYieldRecordStateMapByRecommendRecords(records)
	if err != nil {
		return nil, err
	}
	stateMap, err := loadYieldStateMapByRecommendRecords(records)
	if err != nil {
		return nil, err
	}
	overrideMap, err := loadYieldOverrideMapByRecommendRecords(records)
	if err != nil {
		return nil, err
	}
	resultItems := make([]models.AiRecommendStocksYieldItem, 0, len(records))
	for _, rec := range records {
		item := mapRecommendRecordToYieldItemWithRecordState(rec, recordStateMap, stateMap)
		if override, ok := overrideMap[rec.ID]; ok {
			applyYieldOverrideToYieldItem(&item, &override)
		}
		if strings.TrimSpace(item.DataStatus) == "" {
			item.DataStatus = "计算中"
			item.DataStatusReason = "快照生成中"
		}
		resultItems = append(resultItems, item)
	}
	applyRecommendRepeatCountByCodeMap(resultItems, rawRepeatCountMap)
	diagnostics := calculateYieldDiagnosticSummary(records, resultItems)

	totalYieldRate, totalYieldRateText := calculateYieldTotalByItems(resultItems)
	benchmarkSummary := calculateBenchmarkSummaryByItems(resultItems)

	total := int64(len(resultItems))
	totalPages := int((total + int64(pageSize) - 1) / int64(pageSize))
	offset := (page - 1) * pageSize
	if offset < 0 {
		offset = 0
	}
	if offset > len(resultItems) {
		offset = len(resultItems)
	}
	end := offset + pageSize
	if end > len(resultItems) {
		end = len(resultItems)
	}

	stats := computeMinuteDownloadCoverageStats(nil)
	return &models.AiRecommendStocksYieldPageData{
		List:                       resultItems[offset:end],
		Total:                      total,
		Page:                       page,
		PageSize:                   pageSize,
		TotalPages:                 totalPages,
		StrategyCohort:             strategyCohort,
		TotalYieldRate:             totalYieldRate,
		TotalYieldRateText:         totalYieldRateText,
		BenchmarkCode:              benchmarkSummary.Code,
		BenchmarkName:              benchmarkSummary.Name,
		BenchmarkRate:              benchmarkSummary.Rate,
		BenchmarkRateText:          benchmarkSummary.RateText,
		ExcessYieldRate:            benchmarkSummary.ExcessYieldRate,
		ExcessYieldRateText:        benchmarkSummary.ExcessYieldRateText,
		StrategyXirr:               benchmarkSummary.StrategyXirr,
		StrategyXirrText:           benchmarkSummary.StrategyXirrText,
		BenchmarkXirr:              benchmarkSummary.BenchmarkXirr,
		BenchmarkXirrText:          benchmarkSummary.BenchmarkXirrText,
		ExcessXirr:                 benchmarkSummary.ExcessXirr,
		ExcessXirrText:             benchmarkSummary.ExcessXirrText,
		MaxDrawdown:                benchmarkSummary.MaxDrawdown,
		MaxDrawdownText:            benchmarkSummary.MaxDrawdownText,
		WinRateVsBenchmark:         benchmarkSummary.WinRateVsBenchmark,
		WinRateVsBenchmarkText:     benchmarkSummary.WinRateVsBenchmarkText,
		MedianExcessYieldRate:      benchmarkSummary.MedianExcessYieldRate,
		MedianExcessYieldRateText:  benchmarkSummary.MedianExcessYieldRateText,
		SameDayActivationRate:      diagnostics.SameDayActivationRate,
		SameDayActivationRateText:  diagnostics.SameDayActivationRateText,
		StaleActivationRate:        diagnostics.StaleActivationRate,
		StaleActivationRateText:    diagnostics.StaleActivationRateText,
		StructuredRuleCoverage:     diagnostics.StructuredRuleCoverage,
		StructuredRuleCoverageText: diagnostics.StructuredRuleCoverageText,
		AnalysisOnlyRate:           diagnostics.AnalysisOnlyRate,
		AnalysisOnlyRateText:       diagnostics.AnalysisOnlyRateText,
		StopLossCount:              diagnostics.StopLossCount,
		TakeProfitCount:            diagnostics.TakeProfitCount,
		OpenCount:                  diagnostics.OpenCount,
		V132GateBlockedCount:       diagnostics.V132GateBlockedCount,
		V132StrengthBlockedCount:   diagnostics.V132StrengthBlockedCount,
		V132RewardRiskBlockedCount: diagnostics.V132RewardRiskBlockedCount,
		V132CooldownBlockedCount:   diagnostics.V132CooldownBlockedCount,
		DataAsOf:                   dataAsOf,
		RecalcInProgress:           true,
		RecalcProgress:             recalcProgress,
		MinuteDownloadDone:         stats.Done,
		MinuteDownloadTotal:        stats.Total,
		MinuteDownloadPending:      stats.Pending,
		MinuteDownloadUncoverable:  stats.Uncoverable,
		ManualCooldownUntil:        manualCooldownUntil,
		ManualCooldownRemainSec:    manualCooldownRemainSec,
		LastManualStartedAt:        formatOptionalYieldMetaTime(meta.LastManualDownloadAt),
		LastManualFinishedAt:       formatOptionalYieldMetaTime(meta.LastManualFinishedAt),
		LastManualScopeCount:       meta.LastManualScopeCount,
		LastManualPrefetchMs:       meta.LastManualPrefetchMs,
		LastManualRecalcMs:         meta.LastManualRecalcMs,
		LastManualTotalMs:          meta.LastManualTotalMs,
		LastManualSqliteBusyCount:  meta.LastManualSqliteBusyCount,
		LastManualProviderSummary:  strings.TrimSpace(meta.LastManualProviderSummary),
		DiemengHealthStatus:        diemengHealthStatus,
		DiemengHealthSummary:       diemengHealthSummary,
		DiemengHealthCheckedAt:     diemengHealthCheckedAt,
	}, nil
}

type yieldDiagnosticSummary struct {
	SameDayActivationRate      float64
	SameDayActivationRateText  string
	StaleActivationRate        float64
	StaleActivationRateText    string
	StructuredRuleCoverage     float64
	StructuredRuleCoverageText string
	AnalysisOnlyRate           float64
	AnalysisOnlyRateText       string
	StopLossCount              int
	TakeProfitCount            int
	OpenCount                  int
	V132GateBlockedCount       int
	V132StrengthBlockedCount   int
	V132RewardRiskBlockedCount int
	V132CooldownBlockedCount   int
}

func calculateYieldDiagnosticSummary(records []models.AiRecommendStocks, items []models.AiRecommendStocksYieldItem) yieldDiagnosticSummary {
	result := yieldDiagnosticSummary{
		SameDayActivationRateText:  "--",
		StaleActivationRateText:    "--",
		StructuredRuleCoverageText: "--",
		AnalysisOnlyRateText:       "--",
	}
	if len(records) > 0 {
		structuredCount := 0
		analysisOnlyCount := 0
		for idx := range records {
			if strings.TrimSpace(records[idx].ActivationRuleJSON) != "" {
				structuredCount++
			}
			if isAnalysisOnlyRecommend(&records[idx]) {
				analysisOnlyCount++
			}
		}
		result.StructuredRuleCoverage = round2(float64(structuredCount) * 100 / float64(len(records)))
		result.StructuredRuleCoverageText = formatSignedPercent(result.StructuredRuleCoverage)
		result.AnalysisOnlyRate = round2(float64(analysisOnlyCount) * 100 / float64(len(records)))
		result.AnalysisOnlyRateText = formatSignedPercent(result.AnalysisOnlyRate)
	}

	activatedCount := 0
	sameDayCount := 0
	for _, item := range items {
		if strings.TrimSpace(item.ActivationStatus) != "activated" {
			reason := strings.TrimSpace(item.DataStatusReason)
			if strings.Contains(reason, "V1.3.2") {
				result.V132GateBlockedCount++
				switch {
				case strings.Contains(reason, "强弱过滤"):
					result.V132StrengthBlockedCount++
				case strings.Contains(reason, "盈亏比"):
					result.V132RewardRiskBlockedCount++
				case strings.Contains(reason, "重复止损冷却"):
					result.V132CooldownBlockedCount++
				}
			}
			continue
		}
		activatedCount++
		switch strings.TrimSpace(item.PositionStatus) {
		case "已止损":
			result.StopLossCount++
		case "已止盈":
			result.TakeProfitCount++
		default:
			result.OpenCount++
		}

		signalRaw := strings.TrimSpace(firstNonEmptyText(item.SignalTime, item.RecommendTime))
		activationRaw := strings.TrimSpace(item.ActivationTime)
		if signalRaw == "" || activationRaw == "" {
			continue
		}
		signalTime, signalErr := parseDateTimeWithFallback(normalizeDateTime(signalRaw))
		activationTime, activationErr := parseDateTimeWithFallback(normalizeDateTime(activationRaw))
		if signalErr == nil && activationErr == nil && isSameCNTradeDate(signalTime, activationTime) {
			sameDayCount++
		}
	}
	if activatedCount > 0 {
		result.SameDayActivationRate = round2(float64(sameDayCount) * 100 / float64(activatedCount))
		result.SameDayActivationRateText = formatSignedPercent(result.SameDayActivationRate)
		result.StaleActivationRate = round2(float64(activatedCount-sameDayCount) * 100 / float64(activatedCount))
		result.StaleActivationRateText = formatSignedPercent(result.StaleActivationRate)
	}

	return result
}

func (s *AiRecommendStocksService) buildFastYieldPage(
	query *models.AiRecommendStocksQuery,
	records []models.AiRecommendStocks,
	page int,
	pageSize int,
	dataAsOf string,
	recalcInProgress bool,
	recalcProgress int,
	manualCooldownUntil string,
	manualCooldownRemainSec int,
	minuteDone int,
	minuteTotal int,
	minutePending int,
	minuteUncoverable int,
	dirtyMap map[string]models.AiRecommendYieldDirtyCode,
	rawRepeatCountMap map[string]int,
) (*models.AiRecommendStocksYieldPageData, error) {
	strategyCohort := normalizeStrategyCohort("", strategyCohortCurrent)
	if query != nil {
		strategyCohort = normalizeStrategyCohort(query.StrategyCohort, strategyCohortCurrent)
	}
	diemengHealthStatus, diemengHealthSummary, diemengHealthCheckedAt := GetDiemengSelfCheckView()
	meta := models.AiRecommendYieldMeta{}
	if err := db.Dao.Model(&models.AiRecommendYieldMeta{}).First(&meta).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		logger.SugaredLogger.Warnf("load ai_recommend_yield_meta fast failed: %v", err)
	}
	currentPriceMap, currentPriceTimeMap := loadCurrentPriceSnapshotForRecommendRecords(records)
	strictStateMap, err := loadYieldRecordStateMapByRecommendRecords(records)
	if err != nil {
		return nil, err
	}
	overrideMap, err := loadYieldOverrideMapByRecommendRecords(records)
	if err != nil {
		return nil, err
	}

	items := make([]models.AiRecommendStocksYieldItem, 0, len(records))
	for _, rec := range records {
		item := mapRecommendRecordToFastYieldItem(rec, currentPriceMap, currentPriceTimeMap, strictStateMap, dirtyMap)
		if override, ok := overrideMap[rec.ID]; ok {
			applyYieldOverrideToYieldItem(&item, &override)
		}
		items = append(items, item)
	}
	applyRecommendRepeatCountByCodeMap(items, rawRepeatCountMap)
	diagnostics := calculateYieldDiagnosticSummary(records, items)

	totalYieldRate, totalYieldRateText := calculateYieldTotalByItems(items)
	benchmarkSummary := calculateBenchmarkSummaryByItems(items)
	total := int64(len(items))
	totalPages := int((total + int64(pageSize) - 1) / int64(pageSize))
	offset := (page - 1) * pageSize
	if offset < 0 {
		offset = 0
	}
	if offset > len(items) {
		offset = len(items)
	}
	end := offset + pageSize
	if end > len(items) {
		end = len(items)
	}

	return &models.AiRecommendStocksYieldPageData{
		List:                       items[offset:end],
		Total:                      total,
		Page:                       page,
		PageSize:                   pageSize,
		TotalPages:                 totalPages,
		CalcMode:                   aiRecommendYieldModeFast,
		StrategyCohort:             strategyCohort,
		TotalYieldRate:             totalYieldRate,
		TotalYieldRateText:         totalYieldRateText,
		BenchmarkCode:              benchmarkSummary.Code,
		BenchmarkName:              benchmarkSummary.Name,
		BenchmarkRate:              benchmarkSummary.Rate,
		BenchmarkRateText:          benchmarkSummary.RateText,
		ExcessYieldRate:            benchmarkSummary.ExcessYieldRate,
		ExcessYieldRateText:        benchmarkSummary.ExcessYieldRateText,
		StrategyXirr:               benchmarkSummary.StrategyXirr,
		StrategyXirrText:           benchmarkSummary.StrategyXirrText,
		BenchmarkXirr:              benchmarkSummary.BenchmarkXirr,
		BenchmarkXirrText:          benchmarkSummary.BenchmarkXirrText,
		ExcessXirr:                 benchmarkSummary.ExcessXirr,
		ExcessXirrText:             benchmarkSummary.ExcessXirrText,
		MaxDrawdown:                benchmarkSummary.MaxDrawdown,
		MaxDrawdownText:            benchmarkSummary.MaxDrawdownText,
		WinRateVsBenchmark:         benchmarkSummary.WinRateVsBenchmark,
		WinRateVsBenchmarkText:     benchmarkSummary.WinRateVsBenchmarkText,
		MedianExcessYieldRate:      benchmarkSummary.MedianExcessYieldRate,
		MedianExcessYieldRateText:  benchmarkSummary.MedianExcessYieldRateText,
		SameDayActivationRate:      diagnostics.SameDayActivationRate,
		SameDayActivationRateText:  diagnostics.SameDayActivationRateText,
		StaleActivationRate:        diagnostics.StaleActivationRate,
		StaleActivationRateText:    diagnostics.StaleActivationRateText,
		StructuredRuleCoverage:     diagnostics.StructuredRuleCoverage,
		StructuredRuleCoverageText: diagnostics.StructuredRuleCoverageText,
		AnalysisOnlyRate:           diagnostics.AnalysisOnlyRate,
		AnalysisOnlyRateText:       diagnostics.AnalysisOnlyRateText,
		StopLossCount:              diagnostics.StopLossCount,
		TakeProfitCount:            diagnostics.TakeProfitCount,
		OpenCount:                  diagnostics.OpenCount,
		V132GateBlockedCount:       diagnostics.V132GateBlockedCount,
		V132StrengthBlockedCount:   diagnostics.V132StrengthBlockedCount,
		V132RewardRiskBlockedCount: diagnostics.V132RewardRiskBlockedCount,
		V132CooldownBlockedCount:   diagnostics.V132CooldownBlockedCount,
		DataAsOf:                   dataAsOf,
		RecalcInProgress:           recalcInProgress,
		RecalcProgress:             recalcProgress,
		MinuteDownloadDone:         minuteDone,
		MinuteDownloadTotal:        minuteTotal,
		MinuteDownloadPending:      minutePending,
		MinuteDownloadUncoverable:  minuteUncoverable,
		ManualCooldownUntil:        manualCooldownUntil,
		ManualCooldownRemainSec:    manualCooldownRemainSec,
		LastManualStartedAt:        formatOptionalYieldMetaTime(meta.LastManualDownloadAt),
		LastManualFinishedAt:       formatOptionalYieldMetaTime(meta.LastManualFinishedAt),
		LastManualScopeCount:       meta.LastManualScopeCount,
		LastManualPrefetchMs:       meta.LastManualPrefetchMs,
		LastManualRecalcMs:         meta.LastManualRecalcMs,
		LastManualTotalMs:          meta.LastManualTotalMs,
		LastManualSqliteBusyCount:  meta.LastManualSqliteBusyCount,
		LastManualProviderSummary:  strings.TrimSpace(meta.LastManualProviderSummary),
		DiemengHealthStatus:        diemengHealthStatus,
		DiemengHealthSummary:       diemengHealthSummary,
		DiemengHealthCheckedAt:     diemengHealthCheckedAt,
	}, nil
}

func formatOptionalYieldMetaTime(value *time.Time) string {
	if value == nil || value.IsZero() {
		return ""
	}
	return value.In(cnLocation()).Format("2006-01-02 15:04:05")
}

func loadCurrentPriceSnapshotForRecommendRecords(records []models.AiRecommendStocks) (map[string]float64, map[string]string) {
	priceMap := map[string]float64{}
	priceTimeMap := map[string]string{}
	if len(records) == 0 {
		return priceMap, priceTimeMap
	}

	queryCodes := make([]string, 0, len(records))
	reverseMap := map[string]string{}
	for _, rec := range records {
		code := normalizeRecommendStockCode(rec.StockCode)
		if code == "" {
			continue
		}
		quoteCode := toQuoteCode(code)
		if quoteCode == "" {
			continue
		}
		if _, ok := reverseMap[strings.ToLower(quoteCode)]; ok {
			continue
		}
		reverseMap[strings.ToLower(quoteCode)] = code
		queryCodes = append(queryCodes, quoteCode)
	}
	if len(queryCodes) == 0 {
		return priceMap, priceTimeMap
	}
	stockData, err := NewStockDataApi().GetStockCodeRealTimeData(queryCodes...)
	if err != nil || stockData == nil {
		return priceMap, priceTimeMap
	}
	for _, info := range *stockData {
		code := reverseMap[strings.ToLower(strings.TrimSpace(info.Code))]
		if code == "" {
			continue
		}
		if price, ok := parseBuyPrice(info.Price); ok {
			priceMap[code] = round2(price)
		}
		priceTime := strings.TrimSpace(strings.TrimSpace(info.Date) + " " + strings.TrimSpace(info.Time))
		if priceTime != "" {
			priceTimeMap[code] = priceTime
		}
	}
	return priceMap, priceTimeMap
}

func applyLatestCurrentPriceSnapshot(
	items []models.AiRecommendStocksYieldItem,
	currentPriceMap map[string]float64,
	currentPriceTimeMap map[string]string,
) {
	if len(items) == 0 {
		return
	}
	for idx := range items {
		code := normalizeRecommendStockCode(items[idx].StockCode)
		if code == "" {
			continue
		}
		if currentPrice, ok := currentPriceMap[code]; ok && currentPrice > 0 {
			items[idx].CurrentPrice = round2(currentPrice)
		}
		if ts := strings.TrimSpace(currentPriceTimeMap[code]); ts != "" {
			items[idx].CurrentPriceTime = ts
		}
	}
}

func mapRecommendRecordToFastYieldItem(
	rec models.AiRecommendStocks,
	currentPriceMap map[string]float64,
	currentPriceTimeMap map[string]string,
	strictStateMap map[uint]models.AiRecommendYieldRecordState,
	dirtyMap map[string]models.AiRecommendYieldDirtyCode,
) models.AiRecommendStocksYieldItem {
	code := normalizeRecommendStockCode(rec.StockCode)
	recordTime := recommendRecordTime(rec)
	signalView := resolveRecommendSignalView(rec)

	var stopProfitAmount *float64
	if v, ok := parseStopProfitPrice(rec); ok {
		stopProfitAmount = &v
	}
	var stopLossAmount *float64
	if v, ok := parseStopLossPrice(rec); ok {
		stopLossAmount = &v
	}

	item := models.AiRecommendStocksYieldItem{
		RecommendID:             rec.ID,
		RowKey:                  yieldRowKeyFromRecommend(rec, code),
		CalcMode:                aiRecommendYieldModeFast,
		StockCode:               code,
		StockName:               strings.TrimSpace(rec.StockName),
		ModelNames:              strings.TrimSpace(rec.ModelName),
		BacktestEligibility:     recommendBacktestEligible,
		BkName:                  strings.TrimSpace(rec.BkName),
		RecommendCategory:       strings.TrimSpace(rec.RecommendCategory),
		RecommendCategoryLabel:  recommendCategoryDisplayLabel(rec.RecommendCategory),
		ExecutionState:          signalView.ExecutionState,
		ExecutionStateLabel:     recommendExecutionStateLabel(signalView.ExecutionState),
		BuySignal:               signalView.BuySignal,
		BuySignalDetail:         signalView.BuySignalDetail,
		SellSignal:              signalView.SellSignal,
		SellSignalDetail:        signalView.SellSignalDetail,
		InvalidSignal:           signalView.InvalidSignal,
		ActivationRule:          strings.TrimSpace(rec.ActivationRuleJSON),
		ActivationInvalidReason: strings.TrimSpace(rec.ActivationInvalidReason),
		RecommendCount:          1,
		RecommendTime:           formatYieldDisplayTime(recordTime),
		SignalTime:              formatYieldDisplayTime(recordTime),
		RecommendBuyPrice:       resolveRecommendBuyRangeDisplay(rec),
		StopProfitAmount:        stopProfitAmount,
		StopLossAmount:          stopLossAmount,
		SellAmountText:          buildSellAmountText(stopProfitAmount, stopLossAmount),
		SellTime:                "持有",
		PositionStatus:          "持有",
		YieldRateText:           "--",
		DataStatus:              "快速估算",
		DataStatusReason:        "fast 模式按报告主买入区直接买入，不扫描分钟线触发条件",
	}

	if currentPrice, ok := currentPriceMap[code]; ok {
		item.CurrentPrice = currentPrice
	}
	if item.CurrentPrice <= 0 {
		if p, ok := parseBuyPrice(rec.StockCurrentPrice); ok {
			item.CurrentPrice = round2(p)
		}
	}
	if ts := strings.TrimSpace(currentPriceTimeMap[code]); ts != "" {
		item.CurrentPriceTime = ts
	} else {
		item.CurrentPriceTime = strings.TrimSpace(rec.StockCurrentPriceTime)
	}

	if strictState, ok := strictStateMap[rec.ID]; ok && strings.TrimSpace(strictState.ActivationStatus) != "" {
		item.StrictReady = true
	}
	if dirtyState, ok := dirtyMap[code]; ok {
		item.StrictReady = false
		item.StrictPendingReason = strings.TrimSpace(dirtyState.Reason)
		if item.StrictPendingReason == "" {
			item.StrictPendingReason = "该股票存在待下载或待回算的严格模式任务"
		}
	} else if !item.StrictReady {
		item.StrictPendingReason = "严格模式尚未生成快照"
	}

	buyTime := resolveRecommendBuyTime(recordTime)
	minPrice, _, ok := parseRecommendEntryRange(rec)
	if !ok || minPrice <= 0 || buyTime.IsZero() {
		item.ActivationStatus = "invalid"
		item.PositionStatus = "无法回算"
		item.DataStatus = "无法判定"
		item.DataStatusReason = "fast 模式无法解析主买入区"
		applyRecommendBacktestEligibilityOverride(&item, &rec)
		applyInactiveYieldDefaults(&item)
		return item
	}

	item.ActivationStatus = "activated"
	item.ActivationTime = formatYieldDisplayTime(buyTime)
	item.ActivationPrice = round2(minPrice)
	item.BuyTime = item.ActivationTime
	item.BuyAmount = round2(minPrice)
	if item.CurrentPrice <= 0 {
		item.CurrentPrice = item.BuyAmount
	}

	if stopProfitAmount != nil && item.CurrentPrice >= *stopProfitAmount {
		sell := round2(*stopProfitAmount)
		item.SellAmount = &sell
		item.SellTime = item.CurrentPriceTime
		if item.SellTime == "" {
			item.SellTime = formatYieldDisplayTime(time.Now())
		}
		item.PositionStatus = "已止盈"
	} else if stopLossAmount != nil && item.CurrentPrice > 0 && item.CurrentPrice <= *stopLossAmount {
		sell := round2(*stopLossAmount)
		item.SellAmount = &sell
		item.SellTime = item.CurrentPriceTime
		if item.SellTime == "" {
			item.SellTime = formatYieldDisplayTime(time.Now())
		}
		item.PositionStatus = "已止损"
	}

	if item.SellAmount != nil && *item.SellAmount > 0 {
		result := calculateNetYield(item.StockCode, item.BuyAmount, *item.SellAmount)
		if result.Valid {
			item.YieldRate = result.YieldRate
			item.YieldRateText = result.YieldText
		}
	} else if item.CurrentPrice > 0 {
		result := calculateNetYield(item.StockCode, item.BuyAmount, item.CurrentPrice)
		if result.Valid {
			item.YieldRate = result.YieldRate
			item.YieldRateText = result.YieldText
		}
	}

	applyRecommendBacktestEligibilityOverride(&item, &rec)
	applyInactiveYieldDefaults(&item)
	return item
}

func applyStrictPendingStateToYieldItem(item *models.AiRecommendStocksYieldItem, dirtyMap map[string]models.AiRecommendYieldDirtyCode) {
	if item == nil {
		return
	}
	status := strings.TrimSpace(strings.ToLower(item.ActivationStatus))
	if status == "invalid" || status == "skipped" || status == "expired" || status == "ineligible" {
		item.StrictReady = true
		item.StrictPendingReason = ""
		return
	}
	if normalizeRecommendExecutionState(item.ExecutionState) == recommendExecutionAnalysisOnly {
		item.StrictReady = true
		item.StrictPendingReason = ""
		return
	}
	dirty, ok := dirtyMap[normalizeRecommendStockCode(item.StockCode)]
	if !ok {
		item.StrictReady = true
		return
	}
	item.StrictReady = false
	item.StrictPendingReason = strings.TrimSpace(dirty.Reason)
	if item.StrictPendingReason == "" {
		item.StrictPendingReason = "该股票存在待下载或待回算的严格模式任务"
	}
	item.DataStatus = "待回算"
	item.DataStatusReason = item.StrictPendingReason
	item.ActivationStatus = "pending"
	applyInactiveYieldDefaults(item)
}

func resolveManualCooldownInfo(cooldownUntil *time.Time) (string, int) {
	if cooldownUntil == nil {
		return "", 0
	}
	target := *cooldownUntil
	remain := int(target.Sub(time.Now()).Seconds())
	if remain <= 0 {
		return "", 0
	}
	return target.Format("2006-01-02 15:04:05"), remain
}

func matchYieldKeywordFilter(query *models.AiRecommendStocksQuery, aggr *aiRecommendYieldAggregate) bool {
	if query == nil {
		return true
	}
	if query.StockCode != "" && !strings.Contains(strings.ToLower(aggr.StockCode), strings.ToLower(query.StockCode)) {
		return false
	}
	if query.StockName != "" && !strings.Contains(strings.ToLower(aggr.StockName), strings.ToLower(query.StockName)) {
		return false
	}
	if query.BkName != "" {
		joined := strings.ToLower(strings.Join(aggr.BkNames, "、"))
		if !strings.Contains(joined, strings.ToLower(query.BkName)) {
			return false
		}
	}
	if query.ModelName != "" {
		joined := strings.ToLower(strings.Join(aggr.ModelNames, "、"))
		if !strings.Contains(joined, strings.ToLower(query.ModelName)) {
			return false
		}
	}
	return true
}

func matchYieldDateFilter(query *models.AiRecommendStocksQuery, buyTime time.Time) bool {
	if query == nil {
		return true
	}
	if query.StartDate == "" && query.EndDate == "" {
		return true
	}
	if query.StartDate != "" && query.EndDate != "" {
		startTime, startErr := parseDateTimeWithFallback(normalizeDateTime(query.StartDate))
		endTime, endErr := parseDateTimeWithFallback(normalizeDateTime(query.EndDate))
		if startErr == nil && endErr == nil {
			return !buyTime.Before(datetime.BeginOfDay(startTime)) && !buyTime.After(datetime.EndOfDay(endTime))
		}
	}
	if query.StartDate != "" && query.EndDate == "" {
		startTime, err := parseDateTimeWithFallback(normalizeDateTime(query.StartDate))
		if err == nil {
			return !buyTime.Before(datetime.BeginOfDay(startTime)) && !buyTime.After(datetime.EndOfDay(startTime))
		}
	}
	return true
}

func applyYieldStateFilters(q *gorm.DB, query *models.AiRecommendStocksQuery) *gorm.DB {
	if query == nil {
		return q
	}
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
		q = q.Where("model_names LIKE ?", "%"+query.ModelName+"%")
	}

	if query.StartDate != "" && query.EndDate != "" {
		startDate := normalizeDateTime(query.StartDate)
		endDate := normalizeDateTime(query.EndDate)
		startTime, err := parseDateTimeWithFallback(startDate)
		if err == nil {
			endTime, endErr := parseDateTimeWithFallback(endDate)
			if endErr == nil {
				q = q.Where("buy_time BETWEEN ? AND ?", datetime.BeginOfDay(startTime), datetime.EndOfDay(endTime))
			}
		}
	}
	if query.StartDate != "" && query.EndDate == "" {
		startDate := normalizeDateTime(query.StartDate)
		startTime, err := parseDateTimeWithFallback(startDate)
		if err == nil {
			q = q.Where("buy_time BETWEEN ? AND ?", datetime.BeginOfDay(startTime), datetime.EndOfDay(startTime))
		}
	}

	return q
}

func normalizeDateTime(v string) string {
	return strutil.ReplaceWithMap(v, map[string]string{
		"T": " ",
		"Z": "",
	})
}

func parseDateTimeWithFallback(v string) (time.Time, error) {
	loc := cnLocation()
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", v, loc); err == nil {
		return t, nil
	}
	return time.ParseInLocation("2006-01-02", v, loc)
}

func mapYieldStateToItem(state models.AiRecommendYieldState) models.AiRecommendStocksYieldItem {
	buyTime := ""
	if strings.TrimSpace(state.ActivationStatus) == "activated" && state.BuyTime != nil {
		buyTime = state.BuyTime.Format("2006-01-02 15:04:05")
	}
	signalTime := ""
	if state.SignalTime != nil {
		signalTime = state.SignalTime.Format("2006-01-02 15:04:05")
	}
	activationTime := ""
	if state.ActivationTime != nil {
		activationTime = state.ActivationTime.Format("2006-01-02 15:04:05")
	}
	sellTime := "持有"
	if strings.TrimSpace(state.ActivationStatus) != "activated" {
		sellTime = "待激活"
	}
	if state.SellTime != nil {
		sellTime = state.SellTime.Format("2006-01-02 15:04:05")
	}
	sellAmountText := strings.TrimSpace(state.SellAmountText)
	if sellAmountText == "" {
		sellAmountText = buildSellAmountText(state.StopProfitAmount, state.StopLossAmount)
	}
	dataStatus := strings.TrimSpace(state.DataStatus)
	if dataStatus == "" {
		dataStatus = "正常"
	}
	yieldRateText := strings.TrimSpace(state.YieldRateText)
	if yieldRateText == "" {
		yieldRateText = "--"
	}
	positionStatus := strings.TrimSpace(state.PositionStatus)
	if positionStatus == "" {
		positionStatus = "待激活"
	}
	if strings.TrimSpace(state.ActivationStatus) != "activated" {
		positionStatus = "待激活"
	}

	return models.AiRecommendStocksYieldItem{
		RecommendID:         0,
		RowKey:              state.StockCode,
		StockCode:           state.StockCode,
		StockName:           state.StockName,
		ModelNames:          state.ModelNames,
		BacktestEligibility: recommendBacktestEligible,
		BkName:              state.BkName,
		RecommendCount:      state.RecommendCount,
		SignalTime:          signalTime,
		ActivationStatus:    state.ActivationStatus,
		ActivationTime:      activationTime,
		ActivationPrice:     round2(state.ActivationPrice),
		BuyTime:             buyTime,
		BuyAmount:           round2(state.BuyAmount),
		StopProfitAmount:    state.StopProfitAmount,
		StopLossAmount:      state.StopLossAmount,
		SellTime:            sellTime,
		SellAmount:          state.RealizedSellAmount,
		SellAmountText:      sellAmountText,
		PositionStatus:      positionStatus,
		CurrentPrice:        round2(state.CurrentPrice),
		CurrentPriceTime:    state.CurrentPriceTime,
		YieldRate:           round2(state.YieldRate),
		YieldRateText:       yieldRateText,
		DataStatus:          dataStatus,
		DataStatusReason:    state.DataStatusReason,
	}
}

func expandYieldStateItems(states []models.AiRecommendYieldState) []models.AiRecommendStocksYieldItem {
	items := make([]models.AiRecommendStocksYieldItem, 0, len(states))
	for _, state := range states {
		base := mapYieldStateToItem(state)
		repeat := state.RecommendCount
		if repeat < 1 {
			repeat = 1
		}
		for i := 0; i < repeat; i++ {
			item := base
			item.RecommendCount = 1
			items = append(items, item)
		}
	}
	return items
}

func calculateYieldTotalExpanded(states []models.AiRecommendYieldState) (float64, string) {
	totalBuy := 0.0
	totalValue := 0.0
	for _, state := range states {
		if strings.TrimSpace(state.ActivationStatus) != "activated" {
			continue
		}
		repeat := state.RecommendCount
		if repeat < 1 {
			repeat = 1
		}
		buy := state.BuyAmount
		if buy <= 0 {
			continue
		}
		buyCost := calcBuyTradeCost(buy, resolveTradingMarket(state.StockCode))
		if buyCost.NetAmount <= 0 {
			continue
		}
		totalBuy += buyCost.NetAmount * float64(repeat)
		valuePrice := buy
		if state.RealizedSellAmount != nil && *state.RealizedSellAmount > 0 {
			valuePrice = *state.RealizedSellAmount
		} else if state.CurrentPrice > 0 {
			valuePrice = state.CurrentPrice
		}
		sellNet := calcSellTradeCost(buy, valuePrice, resolveTradingMarket(state.StockCode))
		if sellNet.NetAmount <= 0 {
			continue
		}
		totalValue += sellNet.NetAmount * float64(repeat)
	}

	if totalBuy <= 0 {
		return 0, "--"
	}
	totalYieldRate := round2((totalValue - totalBuy) / totalBuy * 100)
	return totalYieldRate, formatSignedPercent(totalYieldRate)
}

func calculateYieldTotal(states []models.AiRecommendYieldState) (float64, string) {
	totalBuy := 0.0
	totalValue := 0.0
	for _, state := range states {
		if strings.TrimSpace(state.ActivationStatus) != "activated" {
			continue
		}
		buy := state.BuyAmount
		if buy <= 0 {
			continue
		}
		buyCost := calcBuyTradeCost(buy, resolveTradingMarket(state.StockCode))
		if buyCost.NetAmount <= 0 {
			continue
		}
		totalBuy += buyCost.NetAmount
		valuePrice := buy
		if state.RealizedSellAmount != nil && *state.RealizedSellAmount > 0 {
			valuePrice = *state.RealizedSellAmount
		} else if state.CurrentPrice > 0 {
			valuePrice = state.CurrentPrice
		}
		sellNet := calcSellTradeCost(buy, valuePrice, resolveTradingMarket(state.StockCode))
		if sellNet.NetAmount <= 0 {
			continue
		}
		totalValue += sellNet.NetAmount
	}

	if totalBuy <= 0 {
		return 0, "--"
	}
	totalYieldRate := round2((totalValue - totalBuy) / totalBuy * 100)
	return totalYieldRate, formatSignedPercent(totalYieldRate)
}
