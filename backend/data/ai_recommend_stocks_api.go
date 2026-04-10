// Package data ai_recommend_stocks_api.go
package data

import (
	"errors"
	"fmt"
	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	appconfig "go-stock/internal/config"
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

type sseBenchmarkCacheState struct {
	mu       sync.RWMutex
	key      string
	rate     float64
	text     string
	expireAt time.Time
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
var globalSSEBenchmarkCache sseBenchmarkCacheState

const aiRecommendEqualPositionCapital = 3000.0
const defaultAiRecommendSummaryVersion = "phase2-v1"
const recommendPendingActivationMaxTradeDays = 5
const sseBenchmarkCalcTimeout = 6 * time.Second
const sseBenchmarkCacheTTL = 5 * time.Minute
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
	recommendExecutionImmediate    = "immediate"
	recommendExecutionConditional  = "conditional"
	recommendExecutionAnalysisOnly = "analysis_only"
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
	if stopProfitMin <= buyMin {
		return errors.New("建议止盈区间必须高于建议买入区间")
	}
	if stopLossValue >= buyMax {
		return errors.New("建议止损价必须低于建议买入区间")
	}
	return nil
}

func normalizeAiRecommendStockBaseFields(recommend *models.AiRecommendStocks) {
	recommend.ModelName = strings.TrimSpace(recommend.ModelName)
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
			_ = markAiRecommendYieldDirtyCodes(scopeCodes, "新增推荐后等待严格模式下载/回算", aiRecommendYieldModeStrict)
			requestAiRecommendYieldRecalcWithScope(false, "recommend_created", scopeCodes)
		}
		return resultErr
	}

func (s *AiRecommendStocksService) BatchCreateAiRecommendStocks(recommends []*models.AiRecommendStocks) error {
	normalized := make([]*models.AiRecommendStocks, 0, len(recommends))
	for idx, item := range recommends {
		if item == nil {
			continue
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
			_ = markAiRecommendYieldDirtyCodes(scopeCodes, "批量新增推荐后等待严格模式下载/回算", aiRecommendYieldModeStrict)
			requestAiRecommendYieldRecalcWithScope(false, "recommend_batch_created", scopeCodes)
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

// GetAiRecommendStocksList 分页查询AI推荐股票记录
func (s *AiRecommendStocksService) GetAiRecommendStocksList(query *models.AiRecommendStocksQuery) (*models.AiRecommendStocksPageData, error) {
	var rawList []models.AiRecommendStocks

	q := db.Dao.Model(&models.AiRecommendStocks{})
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
		List:       pageList,
		Total:      total,
		Page:       page,
		PageSize:   pageSize,
		TotalPages: totalPages,
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
	EnsureDiemengSelfCheckAsync("yield_list")
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
	diemengHealthStatus, diemengHealthSummary, diemengHealthCheckedAt = GetDiemengSelfCheckView()
	metaPtr := (*models.AiRecommendYieldMeta)(nil)
	if err := db.Dao.Model(&models.AiRecommendYieldMeta{}).First(&meta).Error; err == nil {
		metaPtr = &meta
		if resetStaleYieldRecalcIfNeeded(&meta) {
			requestAiRecommendYieldRecalc(true, "recover_stale_recalc")
		}
		recalcInProgress = meta.RecalcInProgress
		recalcProgress = meta.RecalcProgress
		if meta.LastFullRecalcAt != nil {
			dataAsOf = meta.LastFullRecalcAt.Format("2006-01-02 15:04:05")
		}
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
			TotalYieldRate:            0,
			TotalYieldRateText:        "--",
			SseBenchmarkRate:          0,
			SseBenchmarkRateText:      "--",
			DataAsOf:                  dataAsOf,
			RecalcInProgress:          recalcInProgress,
			RecalcProgress:            recalcProgress,
			MinuteDownloadDone:        minuteDone,
			MinuteDownloadTotal:       minuteTotal,
			MinuteDownloadPending:     minutePending,
			MinuteDownloadUncoverable: minuteUncoverable,
			ManualCooldownUntil:       manualCooldownUntil,
			ManualCooldownRemainSec:   manualCooldownRemainSec,
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
	applyRecommendRepeatCountByCodeMap(items, rawRepeatCountMap)

	totalYieldRate, totalYieldRateText := calculateYieldTotalByItems(items)
	sseBenchmarkRate, sseBenchmarkRateText := calculateSSEBenchmarkRateByItems(items)
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
		List:                      pageItems,
		Total:                     total,
		Page:                      page,
		PageSize:                  pageSize,
		TotalPages:                totalPages,
		CalcMode:                  aiRecommendYieldModeStrict,
		TotalYieldRate:            totalYieldRate,
		TotalYieldRateText:        totalYieldRateText,
		SseBenchmarkRate:          sseBenchmarkRate,
		SseBenchmarkRateText:      sseBenchmarkRateText,
		DataAsOf:                  dataAsOf,
		RecalcInProgress:          recalcInProgress,
		RecalcProgress:            recalcProgress,
		MinuteDownloadDone:        minuteDone,
		MinuteDownloadTotal:       minuteTotal,
		MinuteDownloadPending:     minutePending,
		MinuteDownloadUncoverable: minuteUncoverable,
		ManualCooldownUntil:       manualCooldownUntil,
		ManualCooldownRemainSec:   manualCooldownRemainSec,
		DiemengHealthStatus:       diemengHealthStatus,
		DiemengHealthSummary:      diemengHealthSummary,
		DiemengHealthCheckedAt:    diemengHealthCheckedAt,
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

func resolveYieldReplaySignalTime(rec models.AiRecommendStocks, state *models.AiRecommendYieldRecordState) time.Time {
	if state != nil && state.SignalTime != nil && !state.SignalTime.IsZero() {
		return state.SignalTime.In(cnLocation())
	}
	return recommendRecordTime(rec)
}

func resolveYieldReplayRangeEnd(item models.AiRecommendStocksYieldItem, state *models.AiRecommendYieldRecordState) (time.Time, string) {
	if state != nil && state.SellTime != nil && !state.SellTime.IsZero() {
		return state.SellTime.In(cnLocation()), ""
	}
	if sellAt, ok := parseYieldReplayTime(item.SellTime); ok {
		return sellAt, ""
	}

	positionStatus := strings.TrimSpace(item.PositionStatus)
	sellTimeText := strings.TrimSpace(item.SellTime)
	if positionStatus == "持有" || sellTimeText == "持有" {
		if currentAt, ok := resolveYieldReplayCurrentTime(item, state); ok {
			return currentAt, ""
		}
		if state != nil && state.LastMinuteTs != nil && !state.LastMinuteTs.IsZero() {
			return state.LastMinuteTs.In(cnLocation()), ""
		}
		return time.Time{}, "当前仍在持有，但缺少 currentPriceTime/LastMinuteTs，无法确定回放终点"
	}

	if currentAt, ok := resolveYieldReplayCurrentTime(item, state); ok && strings.TrimSpace(item.ActivationStatus) == "activated" {
		return currentAt, ""
	}

	return time.Time{}, "该记录未形成可回放终点"
}

func isYieldReplayHolding(item models.AiRecommendStocksYieldItem, state *models.AiRecommendYieldRecordState) bool {
	if state != nil {
		if strings.TrimSpace(state.PositionStatus) == "持有" && (state.SellTime == nil || state.SellTime.IsZero()) {
			return true
		}
	}
	positionStatus := strings.TrimSpace(item.PositionStatus)
	sellTimeText := strings.TrimSpace(item.SellTime)
	return positionStatus == "持有" || sellTimeText == "持有"
}

func resolveYieldReplayCurrentTime(item models.AiRecommendStocksYieldItem, state *models.AiRecommendYieldRecordState) (time.Time, bool) {
	if state != nil {
		if currentAt, ok := parseYieldReplayTime(state.CurrentPriceTime); ok {
			return normalizeMinuteCoverageEnd(currentAt), true
		}
	}
	if currentAt, ok := parseYieldReplayTime(item.CurrentPriceTime); ok {
		return normalizeMinuteCoverageEnd(currentAt), true
	}
	return time.Time{}, false
}

func parseYieldReplayTime(raw string) (time.Time, bool) {
	text := strings.TrimSpace(raw)
	if text == "" || text == "持有" || text == "待激活" || text == "已跳过" || text == "未纳入回测" || text == "无法回算" {
		return time.Time{}, false
	}
	t, err := parseDateTimeWithFallback(normalizeDateTime(text))
	if err != nil {
		return time.Time{}, false
	}
	return t.In(cnLocation()), true
}

func buildYieldReplayBars(bars []minuteBar) []models.AiRecommendYieldMinuteBarDTO {
	result := make([]models.AiRecommendYieldMinuteBarDTO, 0, len(bars))
	for _, bar := range bars {
		result = append(result, models.AiRecommendYieldMinuteBarDTO{
			TradeTime: formatYieldDisplayTime(bar.TradeTime),
			Open:      round2(bar.Open),
			High:      round2(bar.High),
			Low:       round2(bar.Low),
			Close:     round2(bar.Close),
			Volume:    bar.Volume,
			Amount:    bar.Amount,
		})
	}
	return result
}

func buildYieldReplayMarkers(
	bars []minuteBar,
	signalAt time.Time,
	item models.AiRecommendStocksYieldItem,
	state *models.AiRecommendYieldRecordState,
) ([]models.AiRecommendYieldChartMarker, string, []string) {
	markers := make([]models.AiRecommendYieldChartMarker, 0, 4)
	messages := make([]string, 0, 4)
	status := "ready"

	appendMarker := func(markerType, label string, target time.Time, price float64) {
		if target.IsZero() {
			return
		}
		marker, ok, exact := locateYieldReplayMarker(bars, markerType, label, target, price)
		if !ok {
			if markerType != "signal" {
				status = "partial"
			}
			appendYieldReplayMessage(&messages, label+"点未在分钟线中定位到")
			return
		}
		if !exact {
			if msg := buildYieldReplayMarkerApproxMessage(markerType, label, marker.Time); msg != "" {
				appendYieldReplayMessage(&messages, msg)
			}
		}
		markers = append(markers, marker)
	}

	appendMarker("signal", "信号", signalAt, 0)

	if buyAt, ok := resolveYieldReplayBuyTime(item, state); ok {
		appendMarker("buy", "买入", buyAt, resolveYieldReplayBuyPrice(item, state))
	}

	if sellAt, ok := resolveYieldReplaySellTime(item, state); ok {
		appendMarker("sell", "卖出", sellAt, resolveYieldReplaySellPrice(item, state))
	} else if currentAt, ok := resolveYieldReplayCurrentTime(item, state); ok && (strings.TrimSpace(item.PositionStatus) == "持有" || strings.TrimSpace(item.SellTime) == "持有") {
		appendMarker("current", "当前", currentAt, resolveYieldReplayCurrentPrice(item, state))
	}

	return markers, status, messages
}

func resolveYieldReplayBuyTime(item models.AiRecommendStocksYieldItem, state *models.AiRecommendYieldRecordState) (time.Time, bool) {
	if state != nil && state.BuyTime != nil && !state.BuyTime.IsZero() {
		return state.BuyTime.In(cnLocation()), true
	}
	return parseYieldReplayTime(item.BuyTime)
}

func resolveYieldReplayBuyPrice(item models.AiRecommendStocksYieldItem, state *models.AiRecommendYieldRecordState) float64 {
	if state != nil && state.BuyAmount > 0 {
		return round2(state.BuyAmount)
	}
	if item.BuyAmount > 0 {
		return round2(item.BuyAmount)
	}
	return 0
}

func resolveYieldReplaySellTime(item models.AiRecommendStocksYieldItem, state *models.AiRecommendYieldRecordState) (time.Time, bool) {
	if state != nil && state.SellTime != nil && !state.SellTime.IsZero() {
		return state.SellTime.In(cnLocation()), true
	}
	return parseYieldReplayTime(item.SellTime)
}

func resolveYieldReplaySellPrice(item models.AiRecommendStocksYieldItem, state *models.AiRecommendYieldRecordState) float64 {
	if state != nil && state.RealizedSellAmount != nil && *state.RealizedSellAmount > 0 {
		return round2(*state.RealizedSellAmount)
	}
	if item.SellAmount != nil && *item.SellAmount > 0 {
		return round2(*item.SellAmount)
	}
	return 0
}

func resolveYieldReplayCurrentPrice(item models.AiRecommendStocksYieldItem, state *models.AiRecommendYieldRecordState) float64 {
	if state != nil && state.CurrentPrice > 0 {
		return round2(state.CurrentPrice)
	}
	if item.CurrentPrice > 0 {
		return round2(item.CurrentPrice)
	}
	return 0
}

func locateYieldReplayMarker(bars []minuteBar, markerType, label string, target time.Time, preferredPrice float64) (models.AiRecommendYieldChartMarker, bool, bool) {
	if len(bars) == 0 || target.IsZero() {
		return models.AiRecommendYieldChartMarker{}, false, false
	}
	target = normalizeMinuteTime(target.In(cnLocation()))
	for _, bar := range bars {
		barTime := normalizeMinuteTime(bar.TradeTime.In(cnLocation()))
		if barTime.Before(target) {
			continue
		}
		price := round2(bar.Close)
		if preferredPrice > 0 {
			price = round2(preferredPrice)
		}
		return models.AiRecommendYieldChartMarker{
			Type:   markerType,
			Time:   formatYieldDisplayTime(bar.TradeTime),
			Price:  price,
			Label:  label,
			Status: yieldReplayMarkerLocateStatus(barTime.Equal(target)),
		}, true, barTime.Equal(target)
	}
	return models.AiRecommendYieldChartMarker{}, false, false
}

func yieldReplayMarkerLocateStatus(exact bool) string {
	if exact {
		return "exact"
	}
	return "approximated"
}

func buildYieldReplayMarkerApproxMessage(markerType, label, resolvedTime string) string {
	if markerType == "signal" {
		return ""
	}
	timeText := strings.TrimSpace(resolvedTime)
	if timeText == "" {
		return label + "点不在交易分钟，已顺延到下一根可用分钟线显示"
	}
	return fmt.Sprintf("%s点不在交易分钟，已对齐到 %s 显示", label, timeText)
}

func appendYieldReplayMessage(messages *[]string, raw string) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return
	}
	for _, msg := range *messages {
		if msg == text {
			return
		}
	}
	*messages = append(*messages, text)
}

func replayChartStatusHint(dataStatus, dataStatusReason string) string {
	status := strings.TrimSpace(dataStatus)
	reason := strings.TrimSpace(dataStatusReason)
	switch status {
	case "待覆盖":
		if reason != "" {
			return reason
		}
		return "分钟线尚未完全覆盖该时间段"
	case "不可覆盖", "无法判定":
		if reason != "" {
			return reason
		}
		return "当前分钟线无法完整覆盖该时间段"
	default:
		if status != "" && status != "正常" && status != "已跳过" && status != "未结构化" && reason != "" {
			return reason
		}
	}
	return ""
}

type minuteCoverageStats struct {
	Done        int
	Total       int
	Pending     int
	Uncoverable int
}

type minuteCoverageIssue struct {
	RowKey     string
	RecordID   uint
	RecordTime time.Time
	StockCode  string
	StockName  string
	Status     string
	RawReason  string
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
	appendIssue := func(rec models.AiRecommendStocks, recordTime time.Time, code, status, reason string) {
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
			RowKey:     yieldRowKeyFromRecommend(rec, code),
			RecordID:   rec.ID,
			RecordTime: recordTime.In(loc),
			StockCode:  code,
			StockName:  strings.TrimSpace(rec.StockName),
			Status:     status,
			RawReason:  strings.TrimSpace(reason),
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
		requiredStart := resolveRecommendSellEligibleTime(recordTime.In(loc))
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
				appendIssue(rec, recordTime, code, "不可覆盖", reason)
			} else {
				pending++
				appendIssue(rec, recordTime, code, "待覆盖",
					fmt.Sprintf("无缓存范围（目标 %s~%s）", formatTs(requiredStart), formatTs(requiredEnd)))
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
				appendIssue(rec, recordTime, code, "不可覆盖", reason)
			} else {
				pending++
				appendIssue(rec, recordTime, code, "待覆盖",
					fmt.Sprintf("起点未覆盖（缓存 %s~%s，目标 %s~%s）",
						formatTs(cacheStart), formatTs(cacheEnd), formatTs(requiredStart), formatTs(requiredEnd)))
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
				appendIssue(rec, recordTime, code, "不可覆盖", reason)
			} else {
				pending++
				appendIssue(rec, recordTime, code, "待覆盖",
					fmt.Sprintf("终点未覆盖（缓存 %s~%s，目标 %s~%s）",
						formatTs(cacheStart), formatTs(cacheEnd), formatTs(requiredStart), formatTs(requiredEnd)))
			}
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
		if p, ok := parseBuyPrice(rec.StockCurrentPrice); ok {
			item.CurrentPrice = round2(p)
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
		if p, ok := parseBuyPrice(rec.StockCurrentPrice); ok {
			item.CurrentPrice = round2(p)
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

func calculateSSEBenchmarkRateByItems(items []models.AiRecommendStocksYieldItem) (float64, string) {
	type benchmarkResult struct {
		rate float64
		text string
	}
	cacheKey, hasWindow := buildSSEBenchmarkCacheKey(items)
	if hasWindow {
		if rate, text, ok := loadSSEBenchmarkCache(cacheKey, false); ok {
			return rate, text
		}
	}
	fallback := benchmarkResult{rate: 0, text: "--"}
	if hasWindow {
		if rate, text, ok := loadSSEBenchmarkCache(cacheKey, true); ok {
			fallback = benchmarkResult{rate: rate, text: text}
		}
	}
	result := runWithTimeout(sseBenchmarkCalcTimeout, fallback, func() benchmarkResult {
		rate, text := calculateSSEBenchmarkRateByItemsCore(items)
		return benchmarkResult{rate: rate, text: text}
	})
	if hasWindow && result.text != "--" {
		storeSSEBenchmarkCache(cacheKey, result.rate, result.text)
	}
	return result.rate, result.text
}

func calculateSSEBenchmarkRateByItemsCore(items []models.AiRecommendStocksYieldItem) (float64, string) {
	startOpenDay, nowDay, ok := resolveSSEBenchmarkWindow(items)
	if !ok {
		return 0, "--"
	}

	klineDays := estimateSSEBenchmarkKlineDays(startOpenDay, nowDay)
	kLines := NewStockDataApi().GetKLineData("sh000001", "240", klineDays)
	if kLines == nil || len(*kLines) == 0 {
		return 0, "--"
	}

	startOpen, endClose, latestCloseDay, ok := selectSSEBenchmarkOpenCloseWindow(*kLines, startOpenDay)
	if !ok || startOpen <= 0 || endClose <= 0 {
		return 0, "--"
	}
	endPrice := resolveSSEBenchmarkEndPrice("sh000001", endClose, latestCloseDay)

	rate := round2((endPrice - startOpen) / startOpen * 100)
	return rate, formatSignedPercent(rate)
}

func buildSSEBenchmarkCacheKey(items []models.AiRecommendStocksYieldItem) (string, bool) {
	startOpenDay, nowDay, ok := resolveSSEBenchmarkWindow(items)
	if !ok {
		return "", false
	}
	return startOpenDay.Format("2006-01-02") + ":" + nowDay.Format("2006-01-02"), true
}

func resolveSSEBenchmarkWindow(items []models.AiRecommendStocksYieldItem) (time.Time, time.Time, bool) {
	if len(items) == 0 {
		return time.Time{}, time.Time{}, false
	}
	loc := cnLocation()
	var earliest time.Time
	for _, item := range items {
		if strings.TrimSpace(item.BacktestEligibility) != "" && strings.TrimSpace(item.BacktestEligibility) != recommendBacktestEligible {
			continue
		}
		if strings.TrimSpace(item.ActivationStatus) != "activated" {
			continue
		}
		activationText := strings.TrimSpace(item.ActivationTime)
		if activationText == "" {
			continue
		}
		activationTime, err := time.ParseInLocation("2006-01-02 15:04:05", activationText, loc)
		if err != nil || activationTime.IsZero() {
			continue
		}
		if earliest.IsZero() || activationTime.Before(earliest) {
			earliest = activationTime
		}
	}
	if earliest.IsZero() {
		return time.Time{}, time.Time{}, false
	}
	startOpenDay := normalizeSSEBenchmarkStartOpenDay(earliest)
	if startOpenDay.IsZero() {
		return time.Time{}, time.Time{}, false
	}
	nowDay := time.Now().In(loc)
	nowDay = time.Date(nowDay.Year(), nowDay.Month(), nowDay.Day(), 0, 0, 0, 0, loc)
	if nowDay.Before(startOpenDay) {
		nowDay = startOpenDay
	}
	return startOpenDay, nowDay, true
}

func loadSSEBenchmarkCache(cacheKey string, allowExpired bool) (float64, string, bool) {
	if strings.TrimSpace(cacheKey) == "" {
		return 0, "", false
	}
	globalSSEBenchmarkCache.mu.RLock()
	defer globalSSEBenchmarkCache.mu.RUnlock()
	if globalSSEBenchmarkCache.key != cacheKey {
		return 0, "", false
	}
	if strings.TrimSpace(globalSSEBenchmarkCache.text) == "" || globalSSEBenchmarkCache.text == "--" {
		return 0, "", false
	}
	if !allowExpired && time.Now().After(globalSSEBenchmarkCache.expireAt) {
		return 0, "", false
	}
	return globalSSEBenchmarkCache.rate, globalSSEBenchmarkCache.text, true
}

func storeSSEBenchmarkCache(cacheKey string, rate float64, text string) {
	if strings.TrimSpace(cacheKey) == "" || strings.TrimSpace(text) == "" || text == "--" {
		return
	}
	globalSSEBenchmarkCache.mu.Lock()
	defer globalSSEBenchmarkCache.mu.Unlock()
	globalSSEBenchmarkCache.key = cacheKey
	globalSSEBenchmarkCache.rate = rate
	globalSSEBenchmarkCache.text = text
	globalSSEBenchmarkCache.expireAt = time.Now().Add(sseBenchmarkCacheTTL)
}

func normalizeSSEBenchmarkStartOpenDay(recordTime time.Time) time.Time {
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

func estimateSSEBenchmarkKlineDays(startDay, endDay time.Time) int64 {
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
	// 使用较宽松窗口覆盖交易日与节假日。
	days := calendarDays*2 + 30
	if days < 120 {
		days = 120
	}
	if days > 5000 {
		days = 5000
	}
	return int64(days)
}

func selectSSEBenchmarkOpenClose(kLines []KLineData, startOpenDay time.Time) (float64, float64, bool) {
	startOpen, endClose, _, ok := selectSSEBenchmarkOpenCloseWindow(kLines, startOpenDay)
	return startOpen, endClose, ok
}

func selectSSEBenchmarkOpenCloseWindow(kLines []KLineData, startOpenDay time.Time) (float64, float64, time.Time, bool) {
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

func resolveSSEBenchmarkEndPrice(indexCode string, fallback float64, minDay time.Time) float64 {
	if fallback <= 0 {
		return 0
	}
	quote := loadLatestCachedSSEBenchmarkQuote(indexCode)
	return resolveSSEBenchmarkEndPriceFromCachedQuote(fallback, quote, minDay)
}

func resolveSSEBenchmarkEndPriceFromCachedQuote(fallback float64, quote *StockInfo, minDay time.Time) float64 {
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
		quoteDay, ok := parseSSEBenchmarkQuoteDay(quote.Date, quote.UpdatedAt)
		if !ok || quoteDay.Before(time.Date(minDay.Year(), minDay.Month(), minDay.Day(), 0, 0, 0, 0, cnLocation())) {
			return fallback
		}
	}
	return price
}

func loadLatestCachedSSEBenchmarkQuote(indexCode string) *StockInfo {
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
			logErrorEvery("AiRecommendStocksService.loadLatestCachedSSEBenchmarkQuote", 10*time.Minute, "load cached benchmark quote err:%s", err.Error())
		}
		return nil
	}
	return &quote
}

func parseSSEBenchmarkQuoteDay(dateText string, updatedAt time.Time) (time.Time, bool) {
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
	diemengHealthStatus, diemengHealthSummary, diemengHealthCheckedAt := GetDiemengSelfCheckView()
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
			TotalYieldRate:            0,
			TotalYieldRateText:        "--",
			SseBenchmarkRate:          0,
			SseBenchmarkRateText:      "--",
			DataAsOf:                  dataAsOf,
			RecalcInProgress:          recalcInProgress,
			RecalcProgress:            recalcProgress,
			MinuteDownloadDone:        stats.Done,
			MinuteDownloadTotal:       stats.Total,
			MinuteDownloadPending:     stats.Pending,
			MinuteDownloadUncoverable: stats.Uncoverable,
			ManualCooldownUntil:       manualCooldownUntil,
			ManualCooldownRemainSec:   manualCooldownRemainSec,
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

	totalYieldRate, totalYieldRateText := calculateYieldTotalByItems(resultItems)
	sseBenchmarkRate, sseBenchmarkRateText := calculateSSEBenchmarkRateByItems(resultItems)

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
		List:                      resultItems[offset:end],
		Total:                     total,
		Page:                      page,
		PageSize:                  pageSize,
		TotalPages:                totalPages,
		TotalYieldRate:            totalYieldRate,
		TotalYieldRateText:        totalYieldRateText,
		SseBenchmarkRate:          sseBenchmarkRate,
		SseBenchmarkRateText:      sseBenchmarkRateText,
		DataAsOf:                  dataAsOf,
		RecalcInProgress:          true,
		RecalcProgress:            recalcProgress,
		MinuteDownloadDone:        stats.Done,
		MinuteDownloadTotal:       stats.Total,
		MinuteDownloadPending:     stats.Pending,
		MinuteDownloadUncoverable: stats.Uncoverable,
		ManualCooldownUntil:       manualCooldownUntil,
		ManualCooldownRemainSec:   manualCooldownRemainSec,
		DiemengHealthStatus:       diemengHealthStatus,
		DiemengHealthSummary:      diemengHealthSummary,
		DiemengHealthCheckedAt:    diemengHealthCheckedAt,
	}, nil
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
	diemengHealthStatus, diemengHealthSummary, diemengHealthCheckedAt := GetDiemengSelfCheckView()
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

	totalYieldRate, totalYieldRateText := calculateYieldTotalByItems(items)
	sseBenchmarkRate, sseBenchmarkRateText := calculateSSEBenchmarkRateByItems(items)
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
		List:                      items[offset:end],
		Total:                     total,
		Page:                      page,
		PageSize:                  pageSize,
		TotalPages:                totalPages,
		CalcMode:                  aiRecommendYieldModeFast,
		TotalYieldRate:            totalYieldRate,
		TotalYieldRateText:        totalYieldRateText,
		SseBenchmarkRate:          sseBenchmarkRate,
		SseBenchmarkRateText:      sseBenchmarkRateText,
		DataAsOf:                  dataAsOf,
		RecalcInProgress:          recalcInProgress,
		RecalcProgress:            recalcProgress,
		MinuteDownloadDone:        minuteDone,
		MinuteDownloadTotal:       minuteTotal,
		MinuteDownloadPending:     minutePending,
		MinuteDownloadUncoverable: minuteUncoverable,
		ManualCooldownUntil:       manualCooldownUntil,
		ManualCooldownRemainSec:   manualCooldownRemainSec,
		DiemengHealthStatus:       diemengHealthStatus,
		DiemengHealthSummary:      diemengHealthSummary,
		DiemengHealthCheckedAt:    diemengHealthCheckedAt,
	}, nil
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

func fillSignalDrivenRecommendCompat(recommend *models.AiRecommendStocks, signalDrivenMode bool, structuredMode bool) {
	if recommend == nil || !signalDrivenMode {
		return
	}
	if buySignal := normalizeRecommendText(recommend.BuySignal); buySignal != "" &&
		!hasRequiredStructuredPlanSection(buySignal, []string{"价格触发：", "量能触发："}) {
		if strings.TrimSpace(recommend.RecommendReason) == "" && strings.TrimSpace(recommend.BuySignalDetail) == "" {
			recommend.BuySignalDetail = buySignal
		}
		recommend.BuySignal = ""
	}
	if recommend.InvalidSignal == "" {
		recommend.InvalidSignal = firstNonEmptyText(recommend.InvalidCondition, recommend.InvalidSignal)
	}
	if invalidSignal := normalizeRecommendText(recommend.InvalidSignal); invalidSignal != "" &&
		!hasRequiredStructuredPlanSection(invalidSignal, []string{"时间失效：", "价格失效："}) {
		if strings.TrimSpace(recommend.InvalidCondition) == "" {
			recommend.InvalidCondition = invalidSignal
		}
		recommend.InvalidSignal = ""
	}
	if recommend.ExecutionState == "" {
		recommend.ExecutionState = inferExecutionStateFromSignals(recommend.BuySignal, recommend.BuySignalDetail)
	}
	recommend.ExecutionState = recommendExecutionConditional
	if recommend.RecommendCategory != "avoid" {
		recommend.RecommendCategory = recommendExecutionConditional
	}
	if recommend.BuySignal == "" {
		recommend.BuySignal = buildCompatBuySignal(recommend.ExecutionState, recommend.RecommendBuyPrice)
	}
	if recommend.SellSignal == "" {
		recommend.SellSignal = buildCompatSellSignal(recommend.RecommendStopProfitPrice, recommend.RecommendStopLossPrice)
	}
	if recommend.InvalidSignal == "" {
		recommend.InvalidSignal = buildCompatInvalidSignal(recommend.RecommendStopLossPrice)
	}
	if recommend.BuySignalDetail == "" {
		recommend.BuySignalDetail = buildCompatBuySignalDetail(recommend.Remarks, shouldBypassRecommendKeywordInterception(recommend.DataTime))
	}
	if recommend.SellSignalDetail == "" {
		recommend.SellSignalDetail = buildCompatSellSignalDetail(recommend.Remarks, shouldBypassRecommendKeywordInterception(recommend.DataTime))
	}
	if !structuredMode && hasSignalDrivenRecommend(recommend) {
		recommend.RecommendCategory = ""
		recommend.RecommendStatus = ""
		recommend.FocusPrice = ""
	}
}

func inferExecutionStateFromSignals(buySignal, buyDetail string) string {
	text := normalizeRecommendText(strings.TrimSpace(buySignal + "\n" + buyDetail))
	if text == "" {
		return ""
	}
	return recommendExecutionConditional
}

func buildCompatBuySignal(executionState string, buyRange string) string {
	rangeText := strings.TrimSpace(buyRange)
	if rangeText == "" {
		rangeText = "主买入区"
	}
	return "价格触发：未来3-5个交易日内股价进入" + rangeText + "主买入区；量能触发：5分钟成交额不低于近5个5分钟均额的1.0倍"
}

func buildCompatBuySignalDetail(remarks string, bypassKeywordInterception bool) string {
	lines := strings.Split(normalizeRecommendText(remarks), "\n")
	matched := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if recommendActionKeywordRegexp.MatchString(line) {
			if !bypassKeywordInterception {
				if phrase := findAmbiguousTriggerPhrase(line); phrase != "" && !quantifiedThresholdRegexp.MatchString(line) {
					continue
				}
			}
			if !bypassKeywordInterception && hasVolumeSignal(line) && !hasCompleteVolumeContext(line) {
				continue
			}
			matched = append(matched, line)
		}
	}
	return strings.Join(matched, "\n")
}

func buildCompatSellSignal(stopProfit, stopLoss string) string {
	profit := strings.TrimSpace(stopProfit)
	loss := strings.TrimSpace(stopLoss)
	switch {
	case profit != "" && loss != "":
		return "触及" + profit + "止盈区间卖出；若跌破" + loss + "止损位立即止损"
	case profit != "":
		return "触及" + profit + "止盈区间卖出"
	case loss != "":
		return "跌破" + loss + "止损位立即止损"
	default:
		return ""
	}
}

func buildCompatSellSignalDetail(remarks string, bypassKeywordInterception bool) string {
	return buildCompatBuySignalDetail(remarks, bypassKeywordInterception)
}

func buildCompatInvalidSignal(stopLoss string) string {
	stopLoss = strings.TrimSpace(stopLoss)
	if stopLoss == "" {
		return ""
	}
	return "时间失效：未来5个交易日内仍未触发主买入区；价格失效：任一5分钟收盘价跌破" + stopLoss
}

func validateSignalDrivenRecommend(recommend *models.AiRecommendStocks) error {
	if recommend == nil || !hasSignalDrivenRecommend(recommend) {
		return nil
	}
	if recommend.ExecutionState != recommendExecutionConditional {
		return errors.New("执行状态只能是 conditional/等待激活")
	}
	if strings.TrimSpace(recommend.BuySignal) == "" {
		return errors.New("买入信号不能为空")
	}
	if strings.TrimSpace(recommend.SellSignal) == "" {
		return errors.New("卖出信号不能为空")
	}
	if strings.TrimSpace(recommend.InvalidSignal) == "" {
		return errors.New("失效信号不能为空")
	}
	buyCombined := normalizeRecommendText(strings.TrimSpace(recommend.BuySignal + "\n" + recommend.BuySignalDetail))
	sellCombined := normalizeRecommendText(strings.TrimSpace(recommend.SellSignal + "\n" + recommend.SellSignalDetail))
	invalidCombined := normalizeRecommendText(strings.TrimSpace(recommend.InvalidSignal + "\n" + recommend.InvalidCondition))
	if !containsConditionalCue(buyCombined) {
		return errors.New("条件触发型记录的买入信号必须明确说明触发条件")
	}
	if !hasRequiredStructuredPlanSection(buyCombined, []string{"价格触发：", "量能触发："}) {
		return errors.New("买入信号必须严格包含“价格触发 / 量能触发”两段")
	}
	if !hasRequiredStructuredPlanSection(invalidCombined, []string{"时间失效：", "价格失效："}) {
		return errors.New("失效信号必须严格包含“时间失效 / 价格失效”两段")
	}
	if hasVolumeSignal(buyCombined) && !hasCompleteVolumeContext(buyCombined) {
		return errors.New("买入信号中的量能条件必须写清锚点价位、比较基准、观察周期和触发阈值")
	}
	if hasVolumeSignal(sellCombined) && !hasCompleteVolumeContext(sellCombined) {
		return errors.New("卖出信号中的量能条件必须写清锚点价位、比较基准、观察周期和触发阈值")
	}
	if hasVolumeSignal(invalidCombined) && !hasCompleteVolumeContext(invalidCombined) {
		return errors.New("失效信号中的量能条件必须写清锚点价位、比较基准、观察周期和触发阈值")
	}
	if !shouldBypassRecommendKeywordInterception(recommend.DataTime) {
		if phrase := findAmbiguousTriggerPhrase(buyCombined); phrase != "" {
			return fmt.Errorf("买入信号包含未量化表述“%s”，必须改成可机械执行的阈值条件", phrase)
		}
		if phrase := findAmbiguousTriggerPhrase(invalidCombined); phrase != "" {
			return fmt.Errorf("失效信号包含未量化表述“%s”，必须改成可机械执行的阈值条件", phrase)
		}
	}
	return nil
}

func containsConditionalCue(text string) bool {
	keywords := []string{"若", "如果", "当", "等待", "触发", "进入", "回到", "站上", "站稳", "突破", "回踩", "确认后", "不破", "放量", "缩量", "承接", "企稳", "跌破"}
	return containsAnyKeyword(text, keywords)
}

func containsImmediateCue(text string) bool {
	keywords := []string{"当前可执行", "当前可买", "当前已满足", "立即买入", "可直接买入", "可直接执行", "现价可买", "现在可买"}
	return containsAnyKeyword(text, keywords)
}

func hasVolumeSignal(text string) bool {
	keywords := []string{"放量", "缩量", "量能", "成交量", "量比"}
	return containsAnyKeyword(text, keywords)
}

func hasCompleteVolumeContext(text string) bool {
	anchorPattern := regexp.MustCompile(`\d+(?:\.\d+)?(?:\s*-\s*\d+(?:\.\d+)?)?`)
	hasAnchor := anchorPattern.MatchString(text) || containsAnyKeyword(text, []string{"买入区", "压力线", "支撑线", "前高", "前低", "昨高", "昨低", "均线"})
	hasBaseline := containsAnyKeyword(text, []string{"均量", "量比", "倍", "前5", "前10", "近5", "近10", "较昨日", "较前一日", "对比"})
	hasCycle := containsAnyKeyword(text, []string{"1分钟", "5分钟", "15分钟", "30分钟", "60分钟", "日线", "周线", "小时"})
	hasThreshold := quantifiedThresholdRegexp.MatchString(text)
	return hasAnchor && hasBaseline && hasCycle && hasThreshold
}

func hasRequiredStructuredPlanSection(text string, prefixes []string) bool {
	normalized := normalizeRecommendText(text)
	if normalized == "" {
		return false
	}
	for _, prefix := range prefixes {
		if !strings.Contains(normalized, prefix) {
			return false
		}
	}
	return true
}

func findAmbiguousTriggerPhrase(text string) string {
	normalized := normalizeRecommendText(text)
	if normalized == "" {
		return ""
	}
	for _, phrase := range ambiguousTriggerPhraseList {
		if strings.Contains(normalized, phrase) {
			return phrase
		}
	}
	return ""
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
