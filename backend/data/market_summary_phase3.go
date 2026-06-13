package data

import (
	"encoding/json"
	"errors"
	"fmt"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/duke-git/lancet/v2/mathutil"
	"github.com/duke-git/lancet/v2/random"
	"github.com/tidwall/gjson"
	"gorm.io/gorm"
)

const marketSummaryPhase3Version = "phase3-v3"
const marketSummaryPhase4Version = "phase3-v4"
const marketSummaryVersionV132 = "v1.3.2"
const marketSummaryVersion136 = "1.3.6"
const marketSummaryCurrentVersion = marketSummaryVersion136

const (
	strategyCohortCurrent = "current"
	strategyCohortAll     = "all"
	strategyCohortLegacy  = "legacy"
)

func normalizeStrategyCohort(raw string, defaultCohort string) string {
	text := strings.ToLower(strings.TrimSpace(raw))
	switch text {
	case "":
		if strings.TrimSpace(defaultCohort) == "" {
			return strategyCohortAll
		}
		return normalizeStrategyCohort(defaultCohort, "")
	case strategyCohortCurrent, strategyCohortAll, strategyCohortLegacy:
		return text
	case "1.3.6", "v1.3.6", "136", "v136":
		return marketSummaryVersion136
	case "1.3.2", "v132", strings.ToLower(marketSummaryVersionV132):
		return marketSummaryVersionV132
	case "1.3.1", "v131":
		return marketSummaryPhase4Version
	case strings.ToLower(marketSummaryPhase3Version):
		return marketSummaryPhase3Version
	case strings.ToLower(marketSummaryPhase4Version):
		return marketSummaryPhase4Version
	default:
		if strings.TrimSpace(defaultCohort) == "" {
			return strategyCohortAll
		}
		return normalizeStrategyCohort(defaultCohort, "")
	}
}

func applyStrategyCohortFilter(q *gorm.DB, cohort string) *gorm.DB {
	if q == nil {
		return nil
	}
	switch normalizeStrategyCohort(cohort, strategyCohortAll) {
	case strategyCohortCurrent:
		return q.Where("summary_version = ?", marketSummaryCurrentVersion)
	case strategyCohortLegacy:
		return q.Where("(TRIM(COALESCE(summary_version, '')) = '' OR summary_version NOT IN ?)", []string{marketSummaryPhase3Version, marketSummaryPhase4Version, marketSummaryVersionV132, marketSummaryVersion136})
	case marketSummaryPhase3Version, marketSummaryPhase4Version, marketSummaryVersionV132, marketSummaryVersion136:
		return q.Where("summary_version = ?", normalizeStrategyCohort(cohort, strategyCohortAll))
	default:
		return q
	}
}

func isCurrentStrategyCohortRecord(rec *models.AiRecommendStocks) bool {
	if rec == nil {
		return false
	}
	return strings.TrimSpace(rec.SummaryVersion) == marketSummaryCurrentVersion
}

type marketSummaryRouteBudget struct {
	TotalCallLimit         int `json:"totalCallLimit"`
	DiscoveryFetchLimit    int `json:"discoveryFetchLimit"`
	DiscoveryModelLimit    int `json:"discoveryModelLimit"`
	CandidateLimit         int `json:"candidateLimit"`
	PerStockFetchLimit     int `json:"perStockFetchLimit"`
	GenerateModelLimit     int `json:"generateModelLimit"`
	VerificationStockLimit int `json:"verificationStockLimit"`
}

type marketSummaryRouteLog struct {
	Version              string                   `json:"version"`
	StartedAt            string                   `json:"startedAt"`
	FinishedAt           string                   `json:"finishedAt,omitempty"`
	RunSlot              string                   `json:"runSlot,omitempty"`
	WindowStart          string                   `json:"windowStart,omitempty"`
	WindowEnd            string                   `json:"windowEnd,omitempty"`
	Budget               marketSummaryRouteBudget `json:"budget"`
	TotalCalls           int                      `json:"totalCalls"`
	PerCategoryCalls     map[string]int           `json:"perCategoryCalls"`
	DiscoveryCandidateCt int                      `json:"discoveryCandidateCount"`
	VerifiedCandidateCt  int                      `json:"verifiedCandidateCount"`
	ExcludedCandidateCt  int                      `json:"excludedCandidateCount,omitempty"`
	DroppedCandidates    []string                 `json:"droppedCandidates,omitempty"`
	Notes                []string                 `json:"notes,omitempty"`
}

type marketSummaryDiscoverySnippet struct {
	Title   string `json:"title"`
	Summary string `json:"summary,omitempty"`
	Time    string `json:"time,omitempty"`
	Source  string `json:"source,omitempty"`
}

type marketSummaryDiscoveryInput struct {
	Question       string                                `json:"question"`
	CurrentTime    string                                `json:"currentTime"`
	MarketStage    string                                `json:"marketStage,omitempty"`
	RunSlot        string                                `json:"runSlot,omitempty"`
	WindowStart    string                                `json:"windowStart,omitempty"`
	WindowEnd      string                                `json:"windowEnd,omitempty"`
	Budget         marketSummaryRouteBudget              `json:"budget"`
	MarketNews     []marketSummaryDiscoverySnippet       `json:"marketNews,omitempty"`
	EventCalendar  []marketSummaryDiscoverySnippet       `json:"eventCalendar,omitempty"`
	IndustryHeat   []marketSummaryDiscoverySnippet       `json:"industryHeat,omitempty"`
	HotStrategies  []marketSummaryDiscoverySnippet       `json:"hotStrategies,omitempty"`
	LongTigerBrief []marketSummaryDiscoverySnippet       `json:"longTigerBrief,omitempty"`
	SkippedReviews []marketSummarySkippedReviewCandidate `json:"skippedReviews,omitempty"`
}

type marketSummarySkippedReviewCandidate struct {
	RecommendID              uint   `json:"recommendId"`
	StockCode                string `json:"stockCode"`
	StockName                string `json:"stockName"`
	RecommendTime            string `json:"recommendTime,omitempty"`
	RecommendBuyPrice        string `json:"recommendBuyPrice,omitempty"`
	RecommendStopProfitPrice string `json:"recommendStopProfitPrice,omitempty"`
	RecommendStopLossPrice   string `json:"recommendStopLossPrice,omitempty"`
	BuySignal                string `json:"buySignal,omitempty"`
	InvalidSignal            string `json:"invalidSignal,omitempty"`
	InvalidCondition         string `json:"invalidCondition,omitempty"`
	SkipReason               string `json:"skipReason,omitempty"`
}

type marketSummaryDiscoveryTheme struct {
	Name     string   `json:"name"`
	Catalyst string   `json:"catalyst,omitempty"`
	Evidence []string `json:"evidence,omitempty"`
}

type marketSummaryDiscoveryDirection struct {
	Name             string   `json:"name"`
	BenefitChain     string   `json:"benefitChain,omitempty"`
	ObserveCondition string   `json:"observeCondition,omitempty"`
	InvalidSignal    string   `json:"invalidSignal,omitempty"`
	RelatedThemes    []string `json:"relatedThemes,omitempty"`
}

type marketSummaryRouteCandidate struct {
	StockName  string `json:"stockName"`
	StockCode  string `json:"stockCode,omitempty"`
	Direction  string `json:"direction,omitempty"`
	BkName     string `json:"bkName,omitempty"`
	Reason     string `json:"reason,omitempty"`
	SourceHint string `json:"sourceHint,omitempty"`
}

type marketSummaryDiscoveryResult struct {
	MarketThemes        []marketSummaryDiscoveryTheme     `json:"marketThemes,omitempty"`
	CandidateDirections []marketSummaryDiscoveryDirection `json:"candidateDirections,omitempty"`
	CandidateStocks     []marketSummaryRouteCandidate     `json:"candidateStocks,omitempty"`
	RiskFlags           []string                          `json:"riskFlags,omitempty"`
}

type marketSummaryVerifiedCandidate struct {
	StockName         string                        `json:"stockName"`
	StockCode         string                        `json:"stockCode"`
	Direction         string                        `json:"direction,omitempty"`
	BkName            string                        `json:"bkName,omitempty"`
	Reason            string                        `json:"reason,omitempty"`
	CurrentPrice      string                        `json:"currentPrice,omitempty"`
	CurrentPriceTime  string                        `json:"currentPriceTime,omitempty"`
	MinutePrice       string                        `json:"minutePrice,omitempty"`
	MinuteAmount      string                        `json:"minuteAmount,omitempty"`
	MinuteVolume      string                        `json:"minuteVolume,omitempty"`
	MinuteTime        string                        `json:"minuteTime,omitempty"`
	MinuteDate        string                        `json:"minuteDate,omitempty"`
	PriceAnchorSource string                        `json:"priceAnchorSource,omitempty"`
	AuctionPrice      string                        `json:"auctionPrice,omitempty"`
	AuctionAmount     string                        `json:"auctionAmount,omitempty"`
	AuctionVolume     string                        `json:"auctionVolume,omitempty"`
	AuctionTime       string                        `json:"auctionTime,omitempty"`
	AuctionDate       string                        `json:"auctionDate,omitempty"`
	AuctionOpen       string                        `json:"auctionOpen,omitempty"`
	AuctionHigh       string                        `json:"auctionHigh,omitempty"`
	AuctionLow        string                        `json:"auctionLow,omitempty"`
	AuctionPreClose   string                        `json:"auctionPreClose,omitempty"`
	AuctionTurnover   string                        `json:"auctionTurnoverRate,omitempty"`
	AuctionCommittee  string                        `json:"auctionCommitteeRatio,omitempty"`
	AuctionVolumeRate string                        `json:"auctionVolumeRatio,omitempty"`
	AuctionBidPrice   []string                      `json:"auctionBidPrice,omitempty"`
	AuctionAskPrice   []string                      `json:"auctionAskPrice,omitempty"`
	AuctionBidVol     []string                      `json:"auctionBidVol,omitempty"`
	AuctionAskVol     []string                      `json:"auctionAskVol,omitempty"`
	TechnicalMetrics  marketSummaryTechnicalMetrics `json:"technicalMetrics,omitempty"`
	TechnicalSnapshot string                        `json:"technicalSnapshot,omitempty"`
	EvidenceSources   []aiEvidenceReference         `json:"evidenceSources,omitempty"`
	PositiveSignals   []string                      `json:"positiveSignals,omitempty"`
	NegativeSignals   []string                      `json:"negativeSignals,omitempty"`
	VerdictHints      []string                      `json:"verdictHints,omitempty"`
}

type marketSummaryTechnicalMetrics struct {
	DayAmount           string `json:"dayAmount,omitempty"`
	DayVolume           string `json:"dayVolume,omitempty"`
	VolumeRatio         string `json:"volumeRatio,omitempty"`
	TurnoverRate        string `json:"turnoverRate,omitempty"`
	Ma5                 string `json:"ma5,omitempty"`
	Ma10                string `json:"ma10,omitempty"`
	Ma20                string `json:"ma20,omitempty"`
	High3d              string `json:"high3d,omitempty"`
	Low3d               string `json:"low3d,omitempty"`
	High5d              string `json:"high5d,omitempty"`
	Low5d               string `json:"low5d,omitempty"`
	High20d             string `json:"high20d,omitempty"`
	Low20d              string `json:"low20d,omitempty"`
	MinuteVolumeVsAvg5  string `json:"minuteVolumeVsAvg5,omitempty"`
	MinuteVolumeVsAvg10 string `json:"minuteVolumeVsAvg10,omitempty"`
	PriceAboveMa5       bool   `json:"priceAboveMa5,omitempty"`
	PriceAboveMa10      bool   `json:"priceAboveMa10,omitempty"`
	Breakout3dHigh      bool   `json:"breakout3dHigh,omitempty"`
	Breakout5dHigh      bool   `json:"breakout5dHigh,omitempty"`
	PullbackNearMa5     bool   `json:"pullbackNearMa5,omitempty"`
}

type marketSummaryAuctionSnapshot struct {
	Price          string
	Amount         string
	Volume         string
	TradeTime      string
	TradeDate      string
	Open           string
	High           string
	Low            string
	PreClose       string
	TurnoverRate   string
	CommitteeRatio string
	VolumeRatio    string
	BidPrice       []string
	AskPrice       []string
	BidVol         []string
	AskVol         []string
}

type marketSummaryPriceAnchor struct {
	CurrentPrice      string
	CurrentPriceTime  string
	MinutePrice       string
	MinuteAmount      string
	MinuteVolume      string
	MinuteTime        string
	MinuteDate        string
	PriceAnchorSource string
	Auction           marketSummaryAuctionSnapshot
}

func defaultMarketSummaryRouteBudget() marketSummaryRouteBudget {
	return marketSummaryRouteBudget{
		TotalCallLimit:         40,
		DiscoveryFetchLimit:    8,
		DiscoveryModelLimit:    1,
		CandidateLimit:         18,
		PerStockFetchLimit:     4,
		GenerateModelLimit:     1,
		VerificationStockLimit: 12,
	}
}

func newMarketSummaryRouteLog() *marketSummaryRouteLog {
	return &marketSummaryRouteLog{
		Version:          marketSummaryCurrentVersion,
		StartedAt:        time.Now().Format(time.DateTime),
		Budget:           defaultMarketSummaryRouteBudget(),
		PerCategoryCalls: map[string]int{},
	}
}

func (l *marketSummaryRouteLog) addCall(category string) {
	if l == nil {
		return
	}
	l.TotalCalls++
	l.PerCategoryCalls[category]++
}

func (l *marketSummaryRouteLog) addNote(format string, args ...any) {
	if l == nil {
		return
	}
	l.Notes = append(l.Notes, fmt.Sprintf(format, args...))
}

func (l *marketSummaryRouteLog) finish() {
	if l == nil {
		return
	}
	l.FinishedAt = time.Now().Format(time.DateTime)
}

func (o *OpenAi) CompleteChat(messages []map[string]any, think bool) (string, string, string, error) {
	switch NormalizeAIAPIProtocol(o.ApiProtocol) {
	case AIAPIProtocolOpenAIResponses:
		return o.completeOpenAIResponses(messages)
	case AIAPIProtocolAnthropicMessage:
		return o.completeAnthropicMessages(messages)
	}
	client := o.newAIClient()
	bodyMap := map[string]any{
		"model":       o.Model,
		"max_tokens":  o.MaxTokens,
		"temperature": o.Temperature,
		"stream":      false,
		"messages":    messages,
	}
	if think {
		bodyMap["thinking"] = map[string]any{"type": "enabled"}
	}

	resp, err := client.R().SetBody(bodyMap).Post("/chat/completions")
	if err != nil && o.HttpProxyEnabled && o.HttpProxy != "" && isProxyConnRefused(err) {
		resp, err = o.newAIClientWithProxy(false).R().SetBody(bodyMap).Post("/chat/completions")
	}
	if err != nil {
		return "", "", "", err
	}
	if resp == nil {
		return "", "", "", errors.New("empty response from model provider")
	}
	if resp.IsError() {
		res := &models.Resp{}
		_ = json.Unmarshal(resp.Body(), res)
		msg := strings.TrimSpace(res.Message)
		if strings.TrimSpace(res.Error.Message) != "" {
			msg = strings.TrimSpace(res.Error.Message)
		}
		if msg == "" {
			msg = strings.TrimSpace(string(resp.Body()))
		}
		if msg == "" {
			msg = fmt.Sprintf("model provider returned status %d", resp.StatusCode())
		}
		return "", "", "", errors.New(msg)
	}
	result := &AiResponse{}
	if err := json.Unmarshal(resp.Body(), result); err != nil {
		return "", "", "", err
	}
	if len(result.Choices) == 0 {
		return "", result.Id, result.Model, errors.New("empty choices from model provider")
	}
	content := strings.TrimSpace(result.Choices[0].Message.Content)
	if content == "" {
		return "", result.Id, result.Model, errors.New("empty content from model provider")
	}
	return content, result.Id, result.Model, nil
}

func (o *OpenAi) NewSummaryStockNewsStreamPhased(userQuestion string, sysPromptId *int, think bool) <-chan map[string]any {
	ch := make(chan map[string]any, 512)
	go func() {
		defer close(ch)
		defer func() {
			if err := recover(); err != nil {
				logger.SugaredLogger.Errorf("NewSummaryStockNewsStreamPhased panic: %v", err)
				ch <- map[string]any{"code": 0, "question": userQuestion, "content": fmt.Sprintf("phase3 route panic: %v", err)}
			}
		}()

		displayQuestion := NormalizeMarketSummaryQuestion(userQuestion)
		logState := newMarketSummaryRouteLog()
		budget := logState.Budget
		emitSummaryToolStatus(ch, "phase3.discovery.fetch", "running", nil, 0)
		discoveryInput, longTigerRaw, window, err := buildMarketSummaryDiscoveryInput(displayQuestion, budget, logState)
		if err != nil {
			logState.addNote("discovery input failed: %v", err)
			emitSummaryToolStatus(ch, "phase3.discovery.fetch", "error", err, 0)
			ch <- map[string]any{"code": 0, "question": displayQuestion, "content": err.Error()}
			return
		}
		skippedReviewRecords, skipErr := loadYieldOverrideCandidatesForRecentTradeDays(3, time.Now())
		if skipErr != nil {
			logState.addNote("load skipped review candidates failed: %v", skipErr)
		} else {
			discoveryInput.SkippedReviews = buildMarketSummarySkippedReviewCandidates(skippedReviewRecords)
			logState.addNote("skipped review candidates=%d", len(discoveryInput.SkippedReviews))
		}
		emitSummaryToolStatus(ch, "phase3.discovery.fetch", "success", nil, 0)

		logState.addCall("discovery_model")
		emitSummaryToolStatus(ch, "phase3.discovery.model", "running", nil, 0)
		discoveryResult, chatID, modelName, err := o.runMarketSummaryDiscovery(discoveryInput)
		if err != nil {
			logState.addNote("discovery model failed: %v", err)
			emitSummaryToolStatus(ch, "phase3.discovery.model", "error", err, 0)
			ch <- map[string]any{"code": 0, "question": displayQuestion, "content": err.Error()}
			return
		}
		emitSummaryToolStatus(ch, "phase3.discovery.model", "success", nil, 0)
		logState.DiscoveryCandidateCt = len(discoveryResult.CandidateStocks)

		emitSummaryToolStatus(ch, "phase3.evidence.verify", "running", nil, 0)
		verifiedCandidates := verifyMarketSummaryCandidates(discoveryInput, discoveryResult, longTigerRaw, budget, logState)
		excludedTodayStocks, excludedTodayIndex, excludedErr := loadSameDayMarketSummaryExcludedStocks(time.Now())
		if excludedErr != nil {
			logState.addNote("load same-day excluded stocks failed: %v", excludedErr)
		} else {
			logState.ExcludedCandidateCt = len(excludedTodayStocks)
			logState.addNote("same-day excluded stocks=%d", len(excludedTodayStocks))
		}
		verifiedCandidates = selectMarketSummaryFinalCandidates(verifiedCandidates, excludedTodayIndex, window, logState, marketSummaryFinalCandidateLimit)
		logState.VerifiedCandidateCt = len(verifiedCandidates)
		emitSummaryToolStatus(ch, "phase3.evidence.verify", "success", nil, 0)

		sysPrompt := ""
		if sysPromptId == nil || *sysPromptId == 0 {
			sysPrompt = RenderMarketSummaryTemplate(o.Prompt)
		} else {
			sysPrompt = RenderMarketSummaryTemplate(NewPromptTemplateApi().GetPromptTemplateByID(*sysPromptId))
		}
		if sysPrompt == "" {
			sysPrompt = RenderMarketSummaryTemplate(o.Prompt)
		}
		messages := buildPhase3FinalMessages(sysPrompt, displayQuestion, discoveryInput, discoveryResult, verifiedCandidates, excludedTodayStocks, discoveryInput.SkippedReviews, logState)
		logState.addCall("generate_model")
		emitSummaryToolStatus(ch, "phase3.generate", "running", nil, 0)
		AskAi(o, messages, ch, displayQuestion, think)
		emitSummaryToolStatus(ch, "phase3.generate", "success", nil, 0)
		logState.finish()
		logger.SugaredLogger.Infof("market summary phase3 route completed: %s", mustJSON(logState))
		if chatID != "" && modelName != "" {
			logger.SugaredLogger.Infof("market summary phase3 discovery meta chatId=%s model=%s", chatID, modelName)
		}
	}()
	return ch
}

func buildMarketSummaryDiscoveryInput(question string, budget marketSummaryRouteBudget, logState *marketSummaryRouteLog) (marketSummaryDiscoveryInput, []models.LongTigerRankData, marketSummaryTimeWindow, error) {
	now := time.Now()
	window := resolveMarketSummaryTimeWindowAt(now)
	input := marketSummaryDiscoveryInput{
		Question:    question,
		CurrentTime: now.Format(time.DateTime),
		MarketStage: describeCNMarketTiming(now),
		RunSlot:     string(window.Slot),
		WindowStart: window.Start.Format(time.DateTime),
		WindowEnd:   window.End.Format(time.DateTime),
		Budget:      budget,
	}
	logState.RunSlot = string(window.Slot)
	logState.WindowStart = input.WindowStart
	logState.WindowEnd = input.WindowEnd

	logState.addCall("market_news")
	logState.addCall("calendar")
	logState.addCall("industry_heat")
	logState.addCall("hot_strategy")
	logState.addCall("long_tiger")

	var (
		news           *[]*models.Telegraph
		calendar       []any
		industryRank   map[string]any
		hotStrategyRaw map[string]any
		longTigerRaw   []models.LongTigerRankData
	)

	var wg sync.WaitGroup
	wg.Add(5)
	go func() {
		defer wg.Done()
		news = runWithTimeout(4*time.Second, (*[]*models.Telegraph)(nil), func() *[]*models.Telegraph {
			return NewMarketNewsApi().GetNews24HoursList("最近24小时市场资讯", random.RandInt(180, 260))
		})
	}()
	go func() {
		defer wg.Done()
		calendar = runWithTimeout(4*time.Second, []any{}, func() []any {
			return NewMarketNewsApi().ClsCalendar()
		})
	}()
	go func() {
		defer wg.Done()
		industryRank = runWithTimeout(4*time.Second, map[string]any{"data": []any{}}, func() map[string]any {
			return NewMarketNewsApi().GetIndustryRank("0", 12)
		})
	}()
	go func() {
		defer wg.Done()
		hotStrategyRaw = runWithTimeout(4*time.Second, map[string]any{}, func() map[string]any {
			return NewSearchStockApi("").HotStrategy()
		})
	}()
	go func() {
		defer wg.Done()
		longTigerRaw = runWithTimeout(4*time.Second, []models.LongTigerRankData(nil), func() []models.LongTigerRankData {
			return fetchLatestLongTigerData()
		})
	}()
	wg.Wait()

	input.MarketNews = make([]marketSummaryDiscoverySnippet, 0, 28)
	if news != nil {
		for _, item := range *news {
			if item == nil {
				continue
			}
			if item.DataTime != nil && !item.DataTime.IsZero() {
				if !marketSummaryTimeInWindow(item.DataTime.In(cnLocation()), window) {
					continue
				}
			} else if !shouldIncludeMarketSummaryTimeText(item.Time, window, false) {
				continue
			}
			title := strings.TrimSpace(item.Title)
			summary := strings.TrimSpace(item.Content)
			if title == "" {
				title = summary
			}
			if title == "" {
				continue
			}
			input.MarketNews = append(input.MarketNews, marketSummaryDiscoverySnippet{Title: truncateText(title, 120), Summary: truncateText(summary, 180), Time: item.Time, Source: item.Source})
			if len(input.MarketNews) >= 24 {
				break
			}
		}
	}

	for _, day := range calendar {
		if len(input.EventCalendar) >= 12 {
			break
		}
		b, _ := json.Marshal(day)
		date := gjson.GetBytes(b, "calendar_day").String()
		if !shouldIncludeMarketSummaryTimeText(date, window, true) {
			continue
		}
		items := gjson.GetBytes(b, "items")
		items.ForEach(func(_, value gjson.Result) bool {
			title := strings.TrimSpace(gjson.Get(value.String(), "title").String())
			if title == "" {
				return true
			}
			input.EventCalendar = append(input.EventCalendar, marketSummaryDiscoverySnippet{Title: truncateText(title, 120), Time: date, Source: "财联社日历"})
			return len(input.EventCalendar) < 12
		})
	}

	if rows, ok := industryRank["data"].([]any); ok {
		for _, row := range rows {
			if len(input.IndustryHeat) >= 10 {
				break
			}
			m, ok := row.(map[string]any)
			if !ok {
				continue
			}
			name := firstNonEmptyText(anyToString(m["industry_name"]), anyToString(m["name"]), anyToString(m["plate_name"]), anyToString(m["bk_name"]))
			if name == "" {
				continue
			}
			summary := strings.TrimSpace(strings.Join([]string{
				joinKeyValue("涨幅", firstNonEmptyText(anyToString(m["zdf"]), anyToString(m["change_pct"]))),
				joinKeyValue("主力净流入", firstNonEmptyText(anyToString(m["zlje"]), anyToString(m["net_inflow"]))),
				joinKeyValue("领涨股", firstNonEmptyText(anyToString(m["lead_stock_name"]), anyToString(m["stock_name"]))),
			}, "；"))
			input.IndustryHeat = append(input.IndustryHeat, marketSummaryDiscoverySnippet{Title: name, Summary: truncateText(summary, 120), Source: "行业热度"})
		}
	}

	b, _ := json.Marshal(hotStrategyRaw)
	hotStrategy := &models.HotStrategy{}
	_ = json.Unmarshal(b, hotStrategy)
	for _, item := range hotStrategy.Data {
		if item == nil || len(input.HotStrategies) >= 8 {
			continue
		}
		input.HotStrategies = append(input.HotStrategies, marketSummaryDiscoverySnippet{
			Title:   truncateText(strings.TrimSpace(item.Question), 120),
			Summary: fmt.Sprintf("热度值:%d；平均涨幅:%.2f%%", item.HeatValue, mathutil.RoundToFloat(100*item.Chg, 2)),
			Source:  "热门选股策略",
		})
	}

	for _, item := range longTigerRaw {
		if len(input.LongTigerBrief) >= 10 {
			break
		}
		if !shouldIncludeMarketSummaryTimeText(item.TRADEDATE, window, true) {
			continue
		}
		input.LongTigerBrief = append(input.LongTigerBrief, marketSummaryDiscoverySnippet{
			Title:   fmt.Sprintf("%s(%s)", item.SECURITYNAMEABBR, normalizeRecommendStockCode(item.SECUCODE)),
			Summary: truncateText(strings.TrimSpace(strings.Join([]string{joinKeyValue("净额", formatFloatCompact(item.BILLBOARDNETAMT)), joinKeyValue("换手", formatFloatCompact(item.TURNOVERRATE)), item.EXPLANATION}, "；")), 140),
			Time:    item.TRADEDATE,
			Source:  "龙虎榜摘要",
		})
	}

	logState.addNote("window filtered discovery counts: news=%d calendar=%d longTiger=%d", len(input.MarketNews), len(input.EventCalendar), len(input.LongTigerBrief))
	return input, longTigerRaw, window, nil
}

func buildMarketSummarySkippedReviewCandidates(records []models.AiRecommendStocks) []marketSummarySkippedReviewCandidate {
	if len(records) == 0 {
		return nil
	}
	result := make([]marketSummarySkippedReviewCandidate, 0, len(records))
	for _, rec := range records {
		recordTime := recommendRecordTime(rec)
		_, _, _, skipReason, skip := resolveRecommendYieldSkipInfo(&rec)
		if !skip {
			continue
		}
		item := marketSummarySkippedReviewCandidate{
			RecommendID:              rec.ID,
			StockCode:                normalizeRecommendStockCode(rec.StockCode),
			StockName:                strings.TrimSpace(rec.StockName),
			RecommendTime:            formatYieldDisplayTime(recordTime),
			RecommendBuyPrice:        resolveRecommendBuyRangeDisplay(rec),
			RecommendStopProfitPrice: strings.TrimSpace(rec.RecommendStopProfitPrice),
			RecommendStopLossPrice:   strings.TrimSpace(rec.RecommendStopLossPrice),
			BuySignal:                normalizeRecommendText(firstNonEmptyText(rec.BuySignal, rec.BuySignalDetail)),
			InvalidSignal:            normalizeRecommendText(rec.InvalidSignal),
			InvalidCondition:         normalizeRecommendText(rec.InvalidCondition),
			SkipReason:               strings.TrimSpace(skipReason),
		}
		if item.StockCode == "" || item.RecommendID == 0 {
			continue
		}
		result = append(result, item)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].RecommendTime == result[j].RecommendTime {
			return result[i].RecommendID > result[j].RecommendID
		}
		return result[i].RecommendTime > result[j].RecommendTime
	})
	return result
}

func runWithTimeout[T any](timeout time.Duration, fallback T, fn func() T) T {
	if timeout <= 0 {
		return fn()
	}
	ch := make(chan T, 1)
	go func() {
		defer func() {
			if recover() != nil {
				ch <- fallback
			}
		}()
		ch <- fn()
	}()
	select {
	case result := <-ch:
		return result
	case <-time.After(timeout):
		return fallback
	}
}

func runStockRealtimeWithTimeout(stockCode string, timeout time.Duration) (*[]StockInfo, error) {
	type stockRealtimeResult struct {
		data *[]StockInfo
		err  error
	}
	result := runWithTimeout(timeout, stockRealtimeResult{}, func() stockRealtimeResult {
		data, err := NewStockDataApi().GetStockCodeRealTimeData(stockCode)
		return stockRealtimeResult{data: data, err: err}
	})
	return result.data, result.err
}

func runStockMinuteWithTimeout(stockCode string, timeout time.Duration) (*[]MinuteData, string) {
	type stockMinuteResult struct {
		data *[]MinuteData
		date string
	}
	result := runWithTimeout(timeout, stockMinuteResult{}, func() stockMinuteResult {
		data, date := NewStockDataApi().GetStockMinutePriceData(stockCode)
		return stockMinuteResult{data: data, date: date}
	})
	return result.data, result.date
}

func runStockCallAuctionWithTimeout(stockCode string, timeout time.Duration, now time.Time) ([]diemengCallAuctionItem, error) {
	type stockCallAuctionResult struct {
		items []diemengCallAuctionItem
		err   error
	}
	result := runWithTimeout(timeout, stockCallAuctionResult{}, func() stockCallAuctionResult {
		start, end, ok := buildCNAuctionWindow(now)
		if !ok {
			return stockCallAuctionResult{}
		}
		items, err := fetchDiemengCallAuctionData(stockCode, start, end)
		return stockCallAuctionResult{items: items, err: err}
	})
	return result.items, result.err
}

func resolveMarketSummaryPriceAnchor(minuteData *[]MinuteData, minuteDate string, stockData *[]StockInfo) marketSummaryPriceAnchor {
	return resolveMarketSummaryPriceAnchorAt(nil, minuteData, minuteDate, stockData, time.Now())
}

func resolveMarketSummaryPriceAnchorAt(auctionItems []diemengCallAuctionItem, minuteData *[]MinuteData, minuteDate string, stockData *[]StockInfo, now time.Time) marketSummaryPriceAnchor {
	anchor := marketSummaryPriceAnchor{}
	anchor.Auction = buildMarketSummaryAuctionSnapshot(auctionItems)

	if stockData != nil && len(*stockData) > 0 {
		item := (*stockData)[0]
		anchor.CurrentPrice = strings.TrimSpace(item.Price)
		anchor.CurrentPriceTime = strings.TrimSpace(firstNonEmptyText(item.Date+" "+item.Time, time.Now().Format(time.DateTime)))
	}

	if shouldUseAuctionPriceAnchor(now, anchor.Auction) {
		anchor.MinutePrice = firstNonEmptyText(anchor.Auction.Price, anchor.CurrentPrice)
		anchor.MinuteAmount = anchor.Auction.Amount
		anchor.MinuteVolume = anchor.Auction.Volume
		anchor.MinuteTime = anchor.Auction.TradeTime
		anchor.MinuteDate = anchor.Auction.TradeDate
		anchor.PriceAnchorSource = "call_auction"
		if anchor.CurrentPrice == "" {
			anchor.CurrentPrice = anchor.MinutePrice
		}
		if anchor.CurrentPriceTime == "" {
			anchor.CurrentPriceTime = strings.TrimSpace(strings.TrimSpace(anchor.MinuteDate + " " + anchor.MinuteTime))
		}
		return anchor
	}

	if minuteData != nil && len(*minuteData) > 0 {
		last := (*minuteData)[len(*minuteData)-1]
		anchor.MinutePrice = formatFloatCompact(last.Price)
		anchor.MinuteAmount = formatFloatCompact(last.Amount)
		anchor.MinuteVolume = formatFloatCompact(last.Volume)
		anchor.MinuteTime = strings.TrimSpace(last.Time)
		anchor.MinuteDate = strings.TrimSpace(minuteDate)
		anchor.PriceAnchorSource = "minute_bar"
		if anchor.CurrentPrice == "" {
			anchor.CurrentPrice = anchor.MinutePrice
		}
		if anchor.CurrentPriceTime == "" {
			anchor.CurrentPriceTime = strings.TrimSpace(strings.TrimSpace(anchor.MinuteDate + " " + anchor.MinuteTime))
		}
		return anchor
	}

	if anchor.CurrentPrice != "" {
		anchor.MinutePrice = anchor.CurrentPrice
		anchor.PriceAnchorSource = "realtime_quote_fallback"
		datePart, timePart := splitDateTimeText(anchor.CurrentPriceTime)
		anchor.MinuteDate = datePart
		anchor.MinuteTime = timePart
	}

	return anchor
}

func buildMarketSummaryAuctionSnapshot(items []diemengCallAuctionItem) marketSummaryAuctionSnapshot {
	if len(items) == 0 {
		return marketSummaryAuctionSnapshot{}
	}
	last := items[len(items)-1]
	datePart, timePart := splitDateTimeText(strings.TrimSpace(last.TradeTime))
	return marketSummaryAuctionSnapshot{
		Price:          formatFloatCompact(last.CurrentPrice),
		Amount:         formatFloatCompact(last.Amount),
		Volume:         formatFloatCompact(last.Volume),
		TradeTime:      timePart,
		TradeDate:      datePart,
		Open:           formatFloatCompact(last.Open),
		High:           formatFloatCompact(last.High),
		Low:            formatFloatCompact(last.Low),
		PreClose:       formatFloatCompact(last.PreClose),
		TurnoverRate:   formatFloatCompact(last.TurnoverRate),
		CommitteeRatio: formatFloatCompact(last.CommitteeRatio),
		VolumeRatio:    formatFloatCompact(last.VolumeRatio),
		BidPrice:       formatFloatSliceCompact(last.BidPrice),
		AskPrice:       formatFloatSliceCompact(last.AskPrice),
		BidVol:         formatFloatSliceCompact(last.BidVol),
		AskVol:         formatFloatSliceCompact(last.AskVol),
	}
}

func buildCNAuctionWindow(now time.Time) (time.Time, time.Time, bool) {
	loc := cnLocation()
	t := now.In(loc)
	if !isCNOpenTradeDayBestEffort(t) {
		return time.Time{}, time.Time{}, false
	}
	start := time.Date(t.Year(), t.Month(), t.Day(), 9, 15, 0, 0, loc)
	stop := time.Date(t.Year(), t.Month(), t.Day(), 9, 30, 0, 0, loc)
	if t.Before(start) || !t.Before(stop) {
		return time.Time{}, time.Time{}, false
	}
	end := t
	maxEnd := time.Date(t.Year(), t.Month(), t.Day(), 9, 25, 0, 0, loc)
	if end.After(maxEnd) {
		end = maxEnd
	}
	if end.Before(start) {
		end = start
	}
	return start, end, true
}

func isCNOpenTradeDayBestEffort(day time.Time) (open bool) {
	loc := cnLocation()
	d := day.In(loc)
	if isWeekendCN(d) {
		return false
	}
	defer func() {
		if recover() != nil {
			open = !isWeekendCN(d)
		}
	}()
	return IsCNOpenTradeDay(d)
}

func shouldUseAuctionPriceAnchor(now time.Time, auction marketSummaryAuctionSnapshot) bool {
	if auction.Price == "" {
		return false
	}
	_, _, ok := buildCNAuctionWindow(now)
	return ok
}

func splitDateTimeText(text string) (string, string) {
	parts := strings.Fields(strings.TrimSpace(text))
	if len(parts) >= 2 {
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	if len(parts) == 1 {
		return strings.TrimSpace(parts[0]), ""
	}
	return "", ""
}

func fetchLatestLongTigerData() []models.LongTigerRankData {
	candidates := []string{
		time.Now().Format("2006-01-02"),
		time.Now().Add(-24 * time.Hour).Format("2006-01-02"),
		time.Now().Add(-48 * time.Hour).Format("2006-01-02"),
	}
	for _, date := range candidates {
		rows := NewMarketNewsApi().LongTiger(date)
		if rows != nil && len(*rows) > 0 {
			return *rows
		}
	}
	return nil
}

func (o *OpenAi) runMarketSummaryDiscovery(input marketSummaryDiscoveryInput) (*marketSummaryDiscoveryResult, string, string, error) {
	payload := mustJSON(input)
	messages := []map[string]any{
		{
			"role": "system",
			"content": strings.TrimSpace(`你是A股市场事件发现层分析器。你的职责只有：
1. 从输入的市场资讯、事件日历、板块热度、龙虎榜摘要中提炼主线与候选方向；
2. 输出最多18个候选股票；
3. 不输出Markdown，不输出解释性废话，只输出一个JSON对象；
4. 候选股票必须优先A股，代码若不确定可留空；
5. 不要给买卖建议，不要生成完整推荐正文。`),
		},
		{
			"role": "user",
			"content": strings.TrimSpace(`请严格输出 JSON 对象，结构如下：
{
  "marketThemes": [{"name":"","catalyst":"","evidence":[""]}],
  "candidateDirections": [{"name":"","benefitChain":"","observeCondition":"","invalidSignal":"","relatedThemes":[""]}],
  "candidateStocks": [{"stockName":"","stockCode":"","direction":"","bkName":"","reason":"","sourceHint":""}],
  "riskFlags": [""]
}

约束：
- candidateStocks 最多 18 个；
- 只保留最值得进入证据核验层的标的；
- 如果证据只支持方向，不支持个股，可减少个股数量；
- 不得返回 markdown 代码块。 

输入JSON：` + payload),
		},
	}
	content, chatID, modelName, err := o.CompleteChat(messages, false)
	if err != nil {
		return nil, chatID, modelName, err
	}
	result := &marketSummaryDiscoveryResult{}
	if err := decodeJSONPayload(content, result); err != nil {
		return nil, chatID, modelName, err
	}
	sanitizeMarketSummaryDiscoveryResult(result)
	return result, chatID, modelName, nil
}

func sanitizeMarketSummaryDiscoveryResult(result *marketSummaryDiscoveryResult) {
	if result == nil {
		return
	}
	result.RiskFlags = dedupeNonEmptyStrings(result.RiskFlags, 6)
	for i := range result.MarketThemes {
		result.MarketThemes[i].Name = strings.TrimSpace(result.MarketThemes[i].Name)
		result.MarketThemes[i].Catalyst = strings.TrimSpace(result.MarketThemes[i].Catalyst)
		result.MarketThemes[i].Evidence = dedupeNonEmptyStrings(result.MarketThemes[i].Evidence, 4)
	}
	for i := range result.CandidateDirections {
		item := &result.CandidateDirections[i]
		item.Name = strings.TrimSpace(item.Name)
		item.BenefitChain = strings.TrimSpace(item.BenefitChain)
		item.ObserveCondition = strings.TrimSpace(item.ObserveCondition)
		item.InvalidSignal = strings.TrimSpace(item.InvalidSignal)
		item.RelatedThemes = dedupeNonEmptyStrings(item.RelatedThemes, 4)
	}
	filtered := make([]marketSummaryRouteCandidate, 0, len(result.CandidateStocks))
	seen := map[string]struct{}{}
	for _, item := range result.CandidateStocks {
		candidate := normalizeMarketSummaryCandidate(item)
		if candidate.StockName == "" || candidate.StockCode == "" {
			continue
		}
		if !isAShareTsCode(candidate.StockCode) {
			continue
		}
		key := candidate.StockCode
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		filtered = append(filtered, candidate)
		if len(filtered) >= defaultMarketSummaryRouteBudget().CandidateLimit {
			break
		}
	}
	result.CandidateStocks = filtered
}

func normalizeMarketSummaryCandidate(item marketSummaryRouteCandidate) marketSummaryRouteCandidate {
	item.StockName = strings.TrimSpace(item.StockName)
	item.StockCode = normalizeRecommendStockCode(item.StockCode)
	item.Direction = strings.TrimSpace(item.Direction)
	item.BkName = strings.TrimSpace(item.BkName)
	item.Reason = strings.TrimSpace(item.Reason)
	item.SourceHint = strings.TrimSpace(item.SourceHint)
	resolvedCode, resolvedName := resolveMarketSummaryStockIdentity(item.StockName, item.StockCode)
	if resolvedName != "" {
		item.StockName = resolvedName
	}
	if resolvedCode != "" {
		item.StockCode = normalizeRecommendStockCode(resolvedCode)
	}
	if item.BkName == "" {
		item.BkName = firstNonEmptyText(item.Direction, item.Reason)
	}
	if item.Direction == "" {
		item.Direction = item.BkName
	}
	if len([]rune(item.BkName)) > 64 {
		item.BkName = string([]rune(item.BkName)[:64])
	}
	if len([]rune(item.Direction)) > 64 {
		item.Direction = string([]rune(item.Direction)[:64])
	}
	return item
}

func verifyMarketSummaryCandidates(input marketSummaryDiscoveryInput, discovery *marketSummaryDiscoveryResult, longTigerRaw []models.LongTigerRankData, budget marketSummaryRouteBudget, logState *marketSummaryRouteLog) []marketSummaryVerifiedCandidate {
	if discovery == nil || len(discovery.CandidateStocks) == 0 {
		return nil
	}
	candidates := discovery.CandidateStocks
	if len(candidates) > budget.VerificationStockLimit {
		for _, item := range candidates[budget.VerificationStockLimit:] {
			logState.DroppedCandidates = append(logState.DroppedCandidates, fmt.Sprintf("候选池截断:%s(%s)", item.StockName, item.StockCode))
		}
		candidates = candidates[:budget.VerificationStockLimit]
	}
	verified := make([]marketSummaryVerifiedCandidate, 0, len(candidates))
	for idx, item := range candidates {
		verified = append(verified, verifySingleMarketSummaryCandidate(input, item, idx, longTigerRaw, logState))
	}
	sort.SliceStable(verified, func(i, j int) bool {
		return verified[i].StockCode < verified[j].StockCode
	})
	return verified
}

func verifySingleMarketSummaryCandidate(input marketSummaryDiscoveryInput, candidate marketSummaryRouteCandidate, candidateIndex int, longTigerRaw []models.LongTigerRankData, logState *marketSummaryRouteLog) marketSummaryVerifiedCandidate {
	_ = candidateIndex
	result := marketSummaryVerifiedCandidate{
		StockName: candidate.StockName,
		StockCode: candidate.StockCode,
		Direction: candidate.Direction,
		BkName:    candidate.BkName,
		Reason:    candidate.Reason,
	}

	refs := make([]aiEvidenceReference, 0, 8)
	positive := make([]string, 0, 4)
	negative := make([]string, 0, 4)
	verdictHints := make([]string, 0, 4)

	for _, item := range input.MarketNews {
		content := strings.TrimSpace(item.Title + " " + item.Summary)
		if !containsCandidateKeyword(content, candidate.StockName, candidate.Direction, candidate.BkName) {
			continue
		}
		refs = append(refs, aiEvidenceReference{
			Type:        "市场资讯",
			Summary:     truncateText(firstNonEmptyText(item.Summary, item.Title), 160),
			SourceName:  firstNonEmptyText(item.Source, "最近24小时市场资讯"),
			Title:       truncateText(item.Title, 120),
			PublishedAt: item.Time,
			EntityCode:  candidate.StockCode,
		})
		if len(refs) >= 2 {
			break
		}
	}
	if len(refs) > 0 {
		positive = append(positive, "市场资讯存在与个股/方向直接相关的催化线索")
	}

	logState.addCall("interactive_verify")
	logState.addCall("notice_verify")
	logState.addCall("research_verify")
	logState.addCall("tech_verify")

	priceCode := ConvertTushareCodeToStockCode(candidate.StockCode)
	verifyNow := time.Now().In(cnLocation())
	var (
		interactive *models.InteractiveAnswer
		notices     []any
		reports     []any
		stockData   *[]StockInfo
		klineData   *[]KLineData
		minuteData  *[]MinuteData
		minuteDate  string
		auctionData []diemengCallAuctionItem
		auctionErr  error
		stockErr    error
	)

	var wg sync.WaitGroup
	wg.Add(7)
	go func() {
		defer wg.Done()
		interactive = runWithTimeout(3*time.Second, (*models.InteractiveAnswer)(nil), func() *models.InteractiveAnswer {
			return NewMarketNewsApi().InteractiveAnswer(1, 20, candidate.StockName)
		})
	}()
	go func() {
		defer wg.Done()
		notices = runWithTimeout(3*time.Second, []any{}, func() []any {
			return NewMarketNewsApi().StockNotice(candidate.StockCode)
		})
	}()
	go func() {
		defer wg.Done()
		reports = runWithTimeout(3*time.Second, []any{}, func() []any {
			return NewMarketNewsApi().StockResearchReport(candidate.StockCode, 12)
		})
	}()
	go func() {
		defer wg.Done()
		klineData = runWithTimeout(4*time.Second, (*[]KLineData)(nil), func() *[]KLineData {
			return NewStockDataApi().GetKLineData(priceCode, "240", 30)
		})
	}()
	go func() {
		defer wg.Done()
		minuteData, minuteDate = runStockMinuteWithTimeout(priceCode, 4*time.Second)
	}()
	go func() {
		defer wg.Done()
		auctionData, auctionErr = runStockCallAuctionWithTimeout(candidate.StockCode, 4*time.Second, verifyNow)
	}()
	go func() {
		defer wg.Done()
		stockData, stockErr = runStockRealtimeWithTimeout(priceCode, 4*time.Second)
	}()
	wg.Wait()

	if interactive != nil {
		count := 0
		for _, item := range interactive.Results {
			if normalizeRecommendStockCode(item.StockCode) != candidate.StockCode && !strings.Contains(item.CompanyShortName, candidate.StockName) {
				continue
			}
			summary := strings.TrimSpace(firstNonEmptyText(item.AttachedContent, item.MainContent))
			if summary == "" {
				continue
			}
			refs = append(refs, aiEvidenceReference{
				Type:        "互动易",
				Summary:     truncateText(summary, 160),
				SourceName:  "互动易",
				SourceType:  "原始披露",
				TrustLevel:  "high",
				Title:       truncateText(item.MainContent, 100),
				PublishedAt: item.AttachedPubDate,
				EntityCode:  candidate.StockCode,
			})
			count++
			if count >= 2 {
				break
			}
		}
		if count > 0 {
			positive = append(positive, "互动易存在公司口径补充")
		}
	}

	noticeCount := 0
	for _, raw := range notices {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		title := firstNonEmptyText(anyToString(m["title"]), anyToString(m["notice_title"]))
		if title == "" {
			continue
		}
		summary := firstNonEmptyText(anyToString(m["columns"]), title)
		refs = append(refs, aiEvidenceReference{
			Type:        "一级披露",
			Summary:     truncateText(summary, 160),
			SourceName:  "公司公告",
			SourceType:  "原始披露",
			TrustLevel:  "high",
			Title:       truncateText(title, 120),
			PublishedAt: firstNonEmptyText(anyToString(m["notice_date"]), anyToString(m["eiTime"])),
			EntityCode:  candidate.StockCode,
		})
		noticeCount++
		if containsAnyText(title+summary, evidenceNegativeKeywords) {
			negative = append(negative, "公告存在风险关键词，需要反证检查")
		}
		if noticeCount >= 2 {
			break
		}
	}
	if noticeCount > 0 {
		positive = append(positive, "存在公告/一级披露可供交叉验证")
	}

	reportCount := 0
	for _, raw := range reports {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		title := firstNonEmptyText(anyToString(m["title"]), anyToString(m["reportTitle"]), anyToString(m["research_title"]))
		if title == "" {
			continue
		}
		summary := firstNonEmptyText(anyToString(m["summary"]), anyToString(m["abstract"]), title)
		refs = append(refs, aiEvidenceReference{
			Type:        "行业研报",
			Summary:     truncateText(summary, 160),
			SourceName:  firstNonEmptyText(anyToString(m["orgSName"]), anyToString(m["org_name"]), "研报聚合"),
			SourceType:  "聚合媒体",
			Title:       truncateText(title, 120),
			PublishedAt: firstNonEmptyText(anyToString(m["publishDate"]), anyToString(m["publish_date"])),
			EntityCode:  candidate.StockCode,
		})
		reportCount++
		if reportCount >= 1 {
			break
		}
	}
	if reportCount > 0 {
		positive = append(positive, "存在卖方/研究口径辅助验证")
	}

	priceAnchor := resolveMarketSummaryPriceAnchorAt(auctionData, minuteData, minuteDate, stockData, verifyNow)
	result.CurrentPrice = priceAnchor.CurrentPrice
	result.CurrentPriceTime = priceAnchor.CurrentPriceTime
	result.MinutePrice = priceAnchor.MinutePrice
	result.MinuteAmount = priceAnchor.MinuteAmount
	result.MinuteVolume = priceAnchor.MinuteVolume
	result.MinuteTime = priceAnchor.MinuteTime
	result.MinuteDate = priceAnchor.MinuteDate
	result.PriceAnchorSource = priceAnchor.PriceAnchorSource
	result.AuctionPrice = priceAnchor.Auction.Price
	result.AuctionAmount = priceAnchor.Auction.Amount
	result.AuctionVolume = priceAnchor.Auction.Volume
	result.AuctionTime = priceAnchor.Auction.TradeTime
	result.AuctionDate = priceAnchor.Auction.TradeDate
	result.AuctionOpen = priceAnchor.Auction.Open
	result.AuctionHigh = priceAnchor.Auction.High
	result.AuctionLow = priceAnchor.Auction.Low
	result.AuctionPreClose = priceAnchor.Auction.PreClose
	result.AuctionTurnover = priceAnchor.Auction.TurnoverRate
	result.AuctionCommittee = priceAnchor.Auction.CommitteeRatio
	result.AuctionVolumeRate = priceAnchor.Auction.VolumeRatio
	result.AuctionBidPrice = priceAnchor.Auction.BidPrice
	result.AuctionAskPrice = priceAnchor.Auction.AskPrice
	result.AuctionBidVol = priceAnchor.Auction.BidVol
	result.AuctionAskVol = priceAnchor.Auction.AskVol
	technicalMetrics := buildMarketSummaryTechnicalMetrics(klineData, stockData, minuteData, priceAnchor)
	result.TechnicalMetrics = technicalMetrics
	techSummary := summarizeKLineAndFunds(priceCode, technicalMetrics)
	result.TechnicalSnapshot = techSummary

	if priceAnchor.Auction.Price != "" {
		auctionSummaryParts := make([]string, 0, 8)
		auctionSummaryParts = append(auctionSummaryParts, "集合竞价当前价："+priceAnchor.Auction.Price)
		if priceAnchor.Auction.Open != "" {
			auctionSummaryParts = append(auctionSummaryParts, "开盘价："+priceAnchor.Auction.Open)
		}
		if priceAnchor.Auction.Amount != "" {
			auctionSummaryParts = append(auctionSummaryParts, "成交额："+priceAnchor.Auction.Amount)
		}
		if priceAnchor.Auction.Volume != "" {
			auctionSummaryParts = append(auctionSummaryParts, "成交量："+priceAnchor.Auction.Volume)
		}
		if priceAnchor.Auction.CommitteeRatio != "" {
			auctionSummaryParts = append(auctionSummaryParts, "委比："+priceAnchor.Auction.CommitteeRatio)
		}
		if priceAnchor.Auction.VolumeRatio != "" {
			auctionSummaryParts = append(auctionSummaryParts, "量比："+priceAnchor.Auction.VolumeRatio)
		}
		if len(priceAnchor.Auction.BidPrice) > 0 {
			auctionSummaryParts = append(auctionSummaryParts, "买盘："+strings.Join(priceAnchor.Auction.BidPrice, ","))
		}
		if len(priceAnchor.Auction.AskPrice) > 0 {
			auctionSummaryParts = append(auctionSummaryParts, "卖盘："+strings.Join(priceAnchor.Auction.AskPrice, ","))
		}
		if priceAnchor.Auction.TradeDate != "" || priceAnchor.Auction.TradeTime != "" {
			auctionSummaryParts = append(auctionSummaryParts, "时间："+strings.TrimSpace(strings.TrimSpace(priceAnchor.Auction.TradeDate+" "+priceAnchor.Auction.TradeTime)))
		}
		refs = append(refs, aiEvidenceReference{
			Type:         "技术/资金/形态",
			Summary:      truncateText(strings.Join(auctionSummaryParts, "；"), 160),
			SourceName:   "集合竞价/盘口",
			SourceType:   "数据接口",
			LatencyLevel: "realtime",
			EntityCode:   candidate.StockCode,
		})
		positive = append(positive, "存在集合竞价盘口快照")
	}

	if priceAnchor.MinutePrice != "" {
		minuteSummaryParts := make([]string, 0, 4)
		minuteSummaryParts = append(minuteSummaryParts, "价格锚点来源："+priceAnchor.PriceAnchorSource)
		minuteSummaryParts = append(minuteSummaryParts, "最新价："+priceAnchor.MinutePrice)
		if priceAnchor.MinuteAmount != "" {
			minuteSummaryParts = append(minuteSummaryParts, "最新一分钟成交额："+priceAnchor.MinuteAmount)
		}
		if priceAnchor.MinuteVolume != "" {
			minuteSummaryParts = append(minuteSummaryParts, "最新一分钟成交量："+priceAnchor.MinuteVolume)
		}
		if priceAnchor.MinuteDate != "" || priceAnchor.MinuteTime != "" {
			minuteSummaryParts = append(minuteSummaryParts, "时间："+strings.TrimSpace(strings.TrimSpace(priceAnchor.MinuteDate+" "+priceAnchor.MinuteTime)))
		}
		refs = append(refs, aiEvidenceReference{
			Type:         "技术/资金/形态",
			Summary:      truncateText(strings.Join(minuteSummaryParts, "；"), 160),
			SourceName:   "分钟线/实时行情",
			SourceType:   "数据接口",
			LatencyLevel: "realtime",
			EntityCode:   candidate.StockCode,
		})
		positive = append(positive, "存在分钟线实时价格锚点")
	}

	if techSummary != "" {
		refs = append(refs, aiEvidenceReference{
			Type:         "技术/资金/形态",
			Summary:      truncateText(techSummary, 160),
			SourceName:   "行情/资金",
			SourceType:   "数据接口",
			LatencyLevel: "realtime",
			EntityCode:   candidate.StockCode,
		})
		positive = append(positive, "技术/资金面已有快照验证")
	}
	if stockErr != nil {
		logState.addNote("realtime quote failed for %s(%s): %v", candidate.StockName, candidate.StockCode, stockErr)
	}
	if auctionErr != nil {
		logState.addNote("call auction failed for %s(%s): %v", candidate.StockName, candidate.StockCode, auctionErr)
	}

	for _, item := range longTigerRaw {
		if normalizeRecommendStockCode(item.SECUCODE) != candidate.StockCode {
			continue
		}
		refs = append(refs, aiEvidenceReference{
			Type:         "资金结构",
			Summary:      truncateText(strings.TrimSpace(strings.Join([]string{joinKeyValue("龙虎净额", formatFloatCompact(item.BILLBOARDNETAMT)), joinKeyValue("换手", formatFloatCompact(item.TURNOVERRATE)), item.EXPLANATION}, "；")), 160),
			SourceName:   "龙虎榜",
			SourceType:   "数据接口",
			LatencyLevel: "daily",
			EntityCode:   candidate.StockCode,
		})
		positive = append(positive, "龙虎榜/资金结构出现补充证据")
		break
	}

	refs = normalizeEvidenceRefs(refs, candidate.StockCode)
	for _, ref := range refs {
		if ref.TrustLevel == "high" {
			verdictHints = append(verdictHints, "至少存在一条高信任证据")
			break
		}
	}
	if len(refs) < 2 {
		negative = append(negative, "证据类别不足2类，不能进入可交易推荐")
	}
	tempRecommend := &models.AiRecommendStocks{EvidenceSources: marshalEvidenceSources(refs), StockCode: candidate.StockCode}
	if hasConflictingEvidence(tempRecommend) {
		negative = append(negative, "高信任源与聚合媒体存在冲突，需降级为争议观察")
	}
	if len(negative) == 0 {
		verdictHints = append(verdictHints, "可进入最终推荐生成层，但仍需模型执行正反证平衡")
	}
	result.EvidenceSources = refs
	result.PositiveSignals = dedupeNonEmptyStrings(positive, 6)
	result.NegativeSignals = dedupeNonEmptyStrings(negative, 6)
	result.VerdictHints = dedupeNonEmptyStrings(verdictHints, 4)
	return result
}

func summarizeFinancialByXueqiu(stockCode string) string {
	converted := ConvertTushareCodeToStockCode(stockCode)
	if converted == "" {
		converted = stockCode
	}
	texts := GetFinancialReportsByXUEQIU(converted, 12)
	if texts == nil || len(*texts) == 0 {
		return ""
	}
	text := normalizeRecommendText((*texts)[0])
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	picked := make([]string, 0, 4)
	keywords := []string{"营业收入", "营收", "归母净利润", "净利润", "毛利率", "ROE", "经营现金流"}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		for _, keyword := range keywords {
			if strings.Contains(trimmed, keyword) {
				picked = append(picked, truncateText(trimmed, 80))
				break
			}
		}
		if len(picked) >= 3 {
			break
		}
	}
	if len(picked) == 0 {
		picked = append(picked, truncateText(text, 120))
	}
	return strings.Join(picked, "；")
}

func buildMarketSummaryTechnicalMetrics(klineData *[]KLineData, stockData *[]StockInfo, minuteData *[]MinuteData, priceAnchor marketSummaryPriceAnchor) marketSummaryTechnicalMetrics {
	metrics := marketSummaryTechnicalMetrics{}
	if stockData != nil && len(*stockData) > 0 {
		last := (*stockData)[len(*stockData)-1]
		metrics.DayAmount = strings.TrimSpace(last.Amount)
		metrics.DayVolume = strings.TrimSpace(last.Volume)
	}
	if priceAnchor.Auction.TurnoverRate != "" {
		metrics.TurnoverRate = strings.TrimSpace(priceAnchor.Auction.TurnoverRate)
	}
	if priceAnchor.Auction.VolumeRatio != "" {
		metrics.VolumeRatio = strings.TrimSpace(priceAnchor.Auction.VolumeRatio)
	}
	if minuteData != nil && len(*minuteData) > 0 {
		last := (*minuteData)[len(*minuteData)-1]
		if ratio := minuteVolumeRatio(*minuteData, 5); ratio > 0 {
			metrics.MinuteVolumeVsAvg5 = formatFloatCompact(ratio)
		}
		if ratio := minuteVolumeRatio(*minuteData, 10); ratio > 0 {
			metrics.MinuteVolumeVsAvg10 = formatFloatCompact(ratio)
		}
		if metrics.VolumeRatio == "" {
			if ratio := minuteVolumeRatio(*minuteData, 5); ratio > 0 {
				metrics.VolumeRatio = formatFloatCompact(ratio)
			}
		}
		_ = last
	}
	if klineData == nil || len(*klineData) == 0 {
		return metrics
	}
	closes := extractKLineValues(*klineData, func(item KLineData) string { return item.Close })
	highs := extractKLineValues(*klineData, func(item KLineData) string { return item.High })
	lows := extractKLineValues(*klineData, func(item KLineData) string { return item.Low })
	if ma5, ok := averageLast(closes, 5); ok {
		metrics.Ma5 = formatFloatCompact(ma5)
	}
	if ma10, ok := averageLast(closes, 10); ok {
		metrics.Ma10 = formatFloatCompact(ma10)
	}
	if ma20, ok := averageLast(closes, 20); ok {
		metrics.Ma20 = formatFloatCompact(ma20)
	}
	if high3, ok := maxLast(highs, 3); ok {
		metrics.High3d = formatFloatCompact(high3)
	}
	if low3, ok := minLast(lows, 3); ok {
		metrics.Low3d = formatFloatCompact(low3)
	}
	if high5, ok := maxLast(highs, 5); ok {
		metrics.High5d = formatFloatCompact(high5)
	}
	if low5, ok := minLast(lows, 5); ok {
		metrics.Low5d = formatFloatCompact(low5)
	}
	if high20, ok := maxLast(highs, 20); ok {
		metrics.High20d = formatFloatCompact(high20)
	}
	if low20, ok := minLast(lows, 20); ok {
		metrics.Low20d = formatFloatCompact(low20)
	}
	lastClose, ok := lastValue(closes)
	if !ok {
		return metrics
	}
	if ma5, ok := parseMetricValue(metrics.Ma5); ok {
		metrics.PriceAboveMa5 = lastClose >= ma5
		if ma5 > 0 {
			distance := mathutil.Abs(lastClose-ma5) / ma5
			metrics.PullbackNearMa5 = distance <= 0.015
		}
	}
	if ma10, ok := parseMetricValue(metrics.Ma10); ok {
		metrics.PriceAboveMa10 = lastClose >= ma10
	}
	if prevHigh3, ok := maxPrevious(highs, 3); ok {
		metrics.Breakout3dHigh = lastClose > prevHigh3
	}
	if prevHigh5, ok := maxPrevious(highs, 5); ok {
		metrics.Breakout5dHigh = lastClose > prevHigh5
	}
	return metrics
}

func summarizeKLineAndFunds(stockCode string, metrics marketSummaryTechnicalMetrics) string {
	parts := make([]string, 0, 8)
	if metrics.Ma5 != "" || metrics.Ma10 != "" || metrics.Ma20 != "" {
		parts = append(parts, strings.TrimSpace(strings.Join([]string{
			joinKeyValue("MA5", metrics.Ma5),
			joinKeyValue("MA10", metrics.Ma10),
			joinKeyValue("MA20", metrics.Ma20),
		}, "；")))
	}
	if metrics.High5d != "" || metrics.Low5d != "" || metrics.High20d != "" || metrics.Low20d != "" {
		parts = append(parts, strings.TrimSpace(strings.Join([]string{
			joinKeyValue("近5日高", metrics.High5d),
			joinKeyValue("近5日低", metrics.Low5d),
			joinKeyValue("近20日高", metrics.High20d),
			joinKeyValue("近20日低", metrics.Low20d),
		}, "；")))
	}
	if metrics.MinuteVolumeVsAvg5 != "" || metrics.MinuteVolumeVsAvg10 != "" || metrics.VolumeRatio != "" {
		parts = append(parts, strings.TrimSpace(strings.Join([]string{
			joinKeyValue("最新分钟量/近5均量", metrics.MinuteVolumeVsAvg5),
			joinKeyValue("最新分钟量/近10均量", metrics.MinuteVolumeVsAvg10),
			joinKeyValue("量比", metrics.VolumeRatio),
		}, "；")))
	}
	flags := make([]string, 0, 4)
	if metrics.PriceAboveMa5 {
		flags = append(flags, "现价站上MA5")
	}
	if metrics.PriceAboveMa10 {
		flags = append(flags, "现价站上MA10")
	}
	if metrics.Breakout3dHigh {
		flags = append(flags, "突破近3日高点")
	}
	if metrics.Breakout5dHigh {
		flags = append(flags, "突破近5日高点")
	}
	if metrics.PullbackNearMa5 {
		flags = append(flags, "价格贴近MA5")
	}
	if len(flags) > 0 {
		parts = append(parts, strings.Join(flags, "；"))
	}
	funds := NewMarketNewsApi().GetStockMoneyTrendByDay(stockCode, 5)
	if len(funds) > 0 {
		last := funds[0]
		netText := firstNonEmptyText(anyToString(last["netamount"]), anyToString(last["net_amount"]), anyToString(last["netin"]), anyToString(last["main_net_inflow"]))
		if netText != "" {
			parts = append(parts, fmt.Sprintf("最近资金净额%s", netText))
		}
	}
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(part, "； ")
		if part != "" {
			filtered = append(filtered, part)
		}
	}
	return strings.Join(filtered, "；")
}

func extractKLineValues(kline []KLineData, selector func(KLineData) string) []float64 {
	values := make([]float64, 0, len(kline))
	for _, item := range kline {
		value, ok := parseMetricValue(selector(item))
		if !ok {
			continue
		}
		values = append(values, value)
	}
	return values
}

func parseMetricValue(text string) (float64, bool) {
	raw := firstNumericText(text)
	if raw == "" {
		return 0, false
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

func lastValue(values []float64) (float64, bool) {
	if len(values) == 0 {
		return 0, false
	}
	return values[len(values)-1], true
}

func averageLast(values []float64, window int) (float64, bool) {
	if len(values) < window || window <= 0 {
		return 0, false
	}
	total := 0.0
	for _, value := range values[len(values)-window:] {
		total += value
	}
	return mathutil.RoundToFloat(total/float64(window), 4), true
}

func maxLast(values []float64, window int) (float64, bool) {
	if len(values) < window || window <= 0 {
		return 0, false
	}
	maxValue := values[len(values)-window]
	for _, value := range values[len(values)-window+1:] {
		if value > maxValue {
			maxValue = value
		}
	}
	return maxValue, true
}

func minLast(values []float64, window int) (float64, bool) {
	if len(values) < window || window <= 0 {
		return 0, false
	}
	minValue := values[len(values)-window]
	for _, value := range values[len(values)-window+1:] {
		if value < minValue {
			minValue = value
		}
	}
	return minValue, true
}

func maxPrevious(values []float64, window int) (float64, bool) {
	if len(values) <= window || window <= 0 {
		return 0, false
	}
	end := len(values) - 1
	start := end - window
	maxValue := values[start]
	for _, value := range values[start+1 : end] {
		if value > maxValue {
			maxValue = value
		}
	}
	return maxValue, true
}

func minuteVolumeRatio(minuteData []MinuteData, window int) float64 {
	if len(minuteData) <= 1 || window <= 0 {
		return 0
	}
	end := len(minuteData) - 1
	start := end - window
	if start < 0 {
		start = 0
	}
	if start >= end {
		return 0
	}
	total := 0.0
	count := 0
	for _, item := range minuteData[start:end] {
		if item.Volume <= 0 {
			continue
		}
		total += item.Volume
		count++
	}
	if count == 0 || minuteData[end].Volume <= 0 {
		return 0
	}
	return mathutil.RoundToFloat(minuteData[end].Volume/(total/float64(count)), 4)
}

func buildPhase3FinalMessages(sysPrompt string, question string, discoveryInput marketSummaryDiscoveryInput, discovery *marketSummaryDiscoveryResult, verified []marketSummaryVerifiedCandidate, excludedToday []marketSummaryExcludedStock, skippedReviews []marketSummarySkippedReviewCandidate, logState *marketSummaryRouteLog) []map[string]any {
	now := time.Now()
	currentTiming := fmt.Sprintf("当前本地时间是:%s；市场时段判定:%s", now.Format("2006-01-02 15:04:05"), describeCNMarketTiming(now))
	droppedCandidates := []string{}
	if logState != nil && len(logState.DroppedCandidates) > 0 {
		droppedCandidates = append(droppedCandidates, logState.DroppedCandidates...)
	}
	messages := []map[string]any{
		{"role": "system", "content": sysPrompt},
		{"role": "user", "content": "当前时间"},
		{"role": "assistant", "content": currentTiming},
		{"role": "user", "content": "以下是事件发现层的结构化输入(JSON)"},
		{"role": "assistant", "content": mustJSON(discoveryInput)},
		{"role": "user", "content": "以下是事件发现层输出(JSON)"},
		{"role": "assistant", "content": mustJSON(discovery)},
		{"role": "user", "content": "以下是证据核验层输出(JSON)"},
		{"role": "assistant", "content": mustJSON(verified)},
		{"role": "user", "content": "以下是候选过滤/跳过原因(JSON)。这些候选没有进入最终推荐时，必须在正文中说明对应原因，不能笼统写“证据核验层为空”"},
		{"role": "assistant", "content": mustJSON(droppedCandidates)},
		{"role": "user", "content": "以下是当日已推荐股票排除池(JSON)"},
		{"role": "assistant", "content": mustJSON(excludedToday)},
		{"role": "user", "content": "以下是前三个交易日已跳过股票复审候选池(JSON)"},
		{"role": "assistant", "content": mustJSON(skippedReviews)},
	}
	instruction := strings.TrimSpace(`请基于上面的“事件发现层”和“证据核验层”结果，完成最终推荐生成层。
要求：
1. 只能基于已经进入证据核验层的候选股票生成结论，严禁新增候选股票；
2. “推荐股票池”最多输出 6 只股票，只保留证据完整、评分靠前、最接近可执行交易计划的候选；其中最多 4 只可作为可交易生产候选，剩余候选若强度不足必须写清仅分析/观察原因；
3. 同日已出现在“当日已推荐股票排除池(JSON)”里的股票，禁止再次写入“推荐股票池”，也不要在正文里重复展示；
4. 本次推荐只允许使用当前时间窗口内的新催化、新证据、新量价确认，不允许用前一时段已推荐股票反复充数；
5. 若排除同日已推荐股票后，没有新的高质量候选标的，必须在“推荐股票池”明确写“暂无新增高质量候选标的”，不能复用旧票凑数；
6. 严禁再假设额外工具结果；
7. 对每只候选股执行“正向证据 + 反向证据”平衡判断；
8. 证据不足(<2类)或不存在高信任证据时，直接不要把该股票写进推荐股票池；
9. 若存在冲突证据，必须明确写出争议，并把该股票从推荐股票池中剔除；
9.1. 若证据核验层最终为空，必须读取“候选过滤/跳过原因(JSON)”并逐项说明为空的根因；禁止只写“证据核验层为空”“缺少真实数据”这类无根因结论；
9.2. 若股票为 analysis_only，只能解释为“仅分析”，并写清具体原因，例如质量门槛、行情缺失、激活规则缺失、同日排除或当前窗口无新证据；
10. 输出必须兼容研究中心：保持 Markdown，必须包含固定 7 个一级标题，并在“推荐股票池”和“跳过复审”中使用标准结构化表格；
11. “关键证据”栏必须显式保留证据标签，如：[市场资讯] [一级披露] [互动易] [财报/财务] [行业研报] [技术/资金/形态] [资金结构]；
12. 若没有足够可交易标的，只允许在表格中写“暂无新增高质量候选标的”，不得编造；
13. 若证据核验层存在 auctionPrice 或 priceAnchorSource=call_auction，价格锚点、买入区间、止盈区间、止损位必须优先围绕集合竞价价格锚点给出，并结合委比、量比、买卖盘结构判断强弱，不能把竞价结果当成已开盘趋势硬推；
14. 若证据核验层存在 minutePrice，且 priceAnchorSource 不是 call_auction，价格锚点、买入区间、止盈区间、止损位必须优先围绕 minutePrice 给出，不能脱离实时价主观编造；
15. 若证据核验层存在 minuteAmount / minuteVolume / technicalMetrics / technicalSnapshot，买入依据必须显式利用这些结构化信息，不得只写“量价配合”“技术面改善”；
16. 若 minutePrice 缺失但 CurrentPrice 存在，可退化使用 CurrentPrice 作为价格锚点；
17. stockPrice 字段应填写当前价格锚点；集合竞价时优先 auctionPrice，其次 minutePrice，再次 CurrentPrice；
18. 若实时价与现有证据、价格结构明显冲突，应从推荐股票池中剔除，不得硬推；
19. 推荐股票池只允许输出交易计划，不允许输出观察/淘汰/低吸/右侧等分类标签；
20. “买入依据”必须至少同时交代：价格位置、量能或强弱确认；
21. “止盈区间”“止损位”“失效条件”必须彼此匹配，不能自相矛盾；
22. 若提到放量/缩量/量比/量能，必须同时写清锚点价位、比较基准、观察周期；
23. 市场资讯来源默认使用“双路径激活”思路：回踩激活路径 + 突破激活路径；若未回踩但继续走强，允许给出贴近当前锚点的突破激活价，不得只保留单一路径的过度保守方案；
24. 不再输出“立刻买入”类标签，所有可交易计划统一写成“等待激活”；无论盘中还是非交易时段，都必须给出未来3-5个交易日内可验证的激活条件；
25. “买入依据”必须严格写成：价格触发：...；量能触发：...；
26. “失效条件”必须严格写成：时间失效：...；价格失效：...；
27. 若提到放量/缩量/量比/量能/强弱，必须同时写清触发价位、观察周期、比较基准、阈值，不允许只写“放量”“缩量”“强势”“承接”“不追”“高开过大”这类抽象词；
28. 所有需要等待条件触发的计划，都必须把触发有效期限定在未来3-5个交易日内；超过该窗口仍未触发，就视为失效；
29. 价格锚点、主买入区、突破激活价、止盈区间、止损位必须与当前 minutePrice / auctionPrice / CurrentPrice 保持同一价格数量级；若相对当前锚点偏离超过20%，视为无效方案，必须重写；
30. 对“前三个交易日已跳过股票复审候选池”里的股票，必须单独输出到“# 跳过复审”章节；不得把这些股票混入“推荐股票池”；
31. “# 跳过复审”的表格必须包含“原记录ID”，并严格复用输入 JSON 中的 recommendId；
32. 若复审结论为“继续跳过”，可不重写完整交易计划，但必须在“跳过/复审说明”里写清继续跳过原因；
33. 若复审结论为“等待激活 / 重新纳入 / 改判可交易”，必须重写买入区间、止盈区间、止损位、买入依据、失效条件，这些字段会覆盖收益率页面对应股票行；
34. 若没有可复审对象，也必须保留“# 跳过复审”标题，并在表格中明确写“暂无需要复审的已跳过股票”；
35. “# 交易计划说明”中必须明确写出本次筛选窗口，格式示例：本次筛选窗口：2026-04-09 09:30:00 至 2026-04-09 11:32:00；
36. 不要输出旧版兼容字段，也不要回到标签式分层表达。`) + "\n\n" + BuildMarketSummaryExecutionQuestion(question)
	messages = append(messages, map[string]any{"role": "user", "content": instruction})
	logState.addNote("verified candidates payload size=%d", len(verified))
	return messages
}

func decodeJSONPayload(text string, target any) error {
	payload := extractJSONPayload(text)
	if payload == "" {
		return fmt.Errorf("model did not return valid JSON payload: %s", truncateText(strings.TrimSpace(text), 200))
	}
	if err := json.Unmarshal([]byte(payload), target); err != nil {
		return err
	}
	return nil
}

func extractJSONPayload(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSuffix(trimmed, "```")
	trimmed = strings.TrimSpace(trimmed)
	if json.Valid([]byte(trimmed)) {
		return trimmed
	}
	start := strings.Index(trimmed, "{")
	if start < 0 {
		return ""
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(trimmed); i++ {
		ch := trimmed[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch ch {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				candidate := strings.TrimSpace(trimmed[start : i+1])
				if json.Valid([]byte(candidate)) {
					return candidate
				}
			}
		}
	}
	return ""
}

func truncateText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len([]rune(text)) <= limit {
		return text
	}
	return string([]rune(text)[:limit])
}

func dedupeNonEmptyStrings(items []string, limit int) []string {
	result := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result
}

func containsCandidateKeyword(content string, keywords ...string) bool {
	content = strings.ToLower(strings.TrimSpace(content))
	if content == "" {
		return false
	}
	for _, keyword := range keywords {
		keyword = strings.ToLower(strings.TrimSpace(keyword))
		if keyword == "" {
			continue
		}
		if strings.Contains(content, keyword) {
			return true
		}
	}
	return false
}

func containsAnyText(text string, keywords []string) bool {
	lower := strings.ToLower(text)
	for _, keyword := range keywords {
		if keyword != "" && strings.Contains(lower, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

func formatFloatCompact(value float64) string {
	if value == 0 {
		return "0"
	}
	return strconv.FormatFloat(value, 'f', 2, 64)
}

func formatFloatSliceCompact(values []float64) []string {
	if len(values) == 0 {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, formatFloatCompact(value))
	}
	return result
}

func anyToString(v any) string {
	switch val := v.(type) {
	case string:
		return strings.TrimSpace(val)
	case fmt.Stringer:
		return strings.TrimSpace(val.String())
	case float64:
		return strconv.FormatFloat(val, 'f', 2, 64)
	case float32:
		return strconv.FormatFloat(float64(val), 'f', 2, 64)
	case int:
		return strconv.Itoa(val)
	case int64:
		return strconv.FormatInt(val, 10)
	case uint:
		return strconv.FormatUint(uint64(val), 10)
	case uint64:
		return strconv.FormatUint(val, 10)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func joinKeyValue(key, value string) string {
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" || value == "" || value == "<nil>" {
		return ""
	}
	return key + ":" + value
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
