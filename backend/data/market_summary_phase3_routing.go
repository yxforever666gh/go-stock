package data

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
)

type marketSummaryRunSlot string

const (
	marketSummaryRunSlotMorning marketSummaryRunSlot = "morning_open"
	marketSummaryRunSlotMidday  marketSummaryRunSlot = "midday"
	marketSummaryRunSlotEvening marketSummaryRunSlot = "evening"
)

const marketSummaryFinalCandidateLimit = 6
const marketSummaryMinFinalCandidateScore = 85

type marketSummaryTimeWindow struct {
	Slot  marketSummaryRunSlot
	Start time.Time
	End   time.Time
}

type marketSummaryExcludedStock struct {
	StockCode          string `json:"stockCode"`
	StockName          string `json:"stockName"`
	FirstRecommendTime string `json:"firstRecommendTime,omitempty"`
}

type marketSummaryCandidateScore struct {
	Candidate           marketSummaryVerifiedCandidate
	TotalScore          int
	WindowEvidenceCt    int
	HighTrustEvidenceCt int
	DistinctEvidenceCt  int
	LatestEvidenceTime  time.Time
}

func defaultMarketSummaryTradeDay(now time.Time) time.Time {
	loc := cnLocation()
	current := time.Date(now.In(loc).Year(), now.In(loc).Month(), now.In(loc).Day(), 0, 0, 0, 0, loc)
	if IsCNOpenTradeDay(current) {
		return current
	}
	return shiftToPrevCNOpenTradeDay(current)
}

func resolveMarketSummaryTimeWindowAt(now time.Time) marketSummaryTimeWindow {
	loc := cnLocation()
	now = now.In(loc)
	tradeDay := defaultMarketSummaryTradeDay(now)
	morningCutoff := time.Date(tradeDay.Year(), tradeDay.Month(), tradeDay.Day(), 10, 30, 0, 0, loc)
	afternoonCutoff := time.Date(tradeDay.Year(), tradeDay.Month(), tradeDay.Day(), 13, 30, 0, 0, loc)
	open0930 := time.Date(tradeDay.Year(), tradeDay.Month(), tradeDay.Day(), 9, 30, 0, 0, loc)
	lunch1130 := time.Date(tradeDay.Year(), tradeDay.Month(), tradeDay.Day(), 11, 30, 0, 0, loc)
	prevTradeDay := shiftToPrevCNOpenTradeDay(tradeDay.AddDate(0, 0, -1))
	prevClose := time.Date(prevTradeDay.Year(), prevTradeDay.Month(), prevTradeDay.Day(), 15, 0, 0, 0, loc)

	switch {
	case now.Before(morningCutoff):
		return marketSummaryTimeWindow{
			Slot:  marketSummaryRunSlotMorning,
			Start: prevClose,
			End:   now,
		}
	case now.Before(afternoonCutoff):
		return marketSummaryTimeWindow{
			Slot:  marketSummaryRunSlotMidday,
			Start: open0930,
			End:   now,
		}
	default:
		return marketSummaryTimeWindow{
			Slot:  marketSummaryRunSlotEvening,
			Start: lunch1130,
			End:   now,
		}
	}
}

func parseMarketSummaryWindowTime(raw string) (time.Time, bool) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return time.Time{}, false
	}
	loc := cnLocation()
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		time.DateTime,
		"2006-01-02 15:04",
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
		"2006/01/02 15:04:05",
		"2006/01/02 15:04",
		"2006-01-02",
		"20060102",
		"2006/01/02",
	}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, text, loc); err == nil {
			return t.In(loc), true
		}
	}
	return time.Time{}, false
}

func marketSummaryTimeInWindow(t time.Time, window marketSummaryTimeWindow) bool {
	if t.IsZero() || window.Start.IsZero() || window.End.IsZero() {
		return false
	}
	return !t.Before(window.Start) && !t.After(window.End)
}

func marketSummaryDateInWindow(t time.Time, window marketSummaryTimeWindow) bool {
	if t.IsZero() || window.Start.IsZero() || window.End.IsZero() {
		return false
	}
	loc := cnLocation()
	target := time.Date(t.In(loc).Year(), t.In(loc).Month(), t.In(loc).Day(), 0, 0, 0, 0, loc)
	start := time.Date(window.Start.In(loc).Year(), window.Start.In(loc).Month(), window.Start.In(loc).Day(), 0, 0, 0, 0, loc)
	end := time.Date(window.End.In(loc).Year(), window.End.In(loc).Month(), window.End.In(loc).Day(), 0, 0, 0, 0, loc)
	return !target.Before(start) && !target.After(end)
}

func shouldIncludeMarketSummaryTimeText(raw string, window marketSummaryTimeWindow, dateOnly bool) bool {
	t, ok := parseMarketSummaryWindowTime(raw)
	if !ok {
		return false
	}
	if dateOnly {
		return marketSummaryDateInWindow(t, window)
	}
	return marketSummaryTimeInWindow(t, window)
}

func loadSameDayMarketSummaryExcludedStocks(now time.Time) ([]marketSummaryExcludedStock, map[string]marketSummaryExcludedStock, error) {
	loc := cnLocation()
	tradeDay := defaultMarketSummaryTradeDay(now)
	start := time.Date(tradeDay.Year(), tradeDay.Month(), tradeDay.Day(), 0, 0, 0, 0, loc)
	end := start.Add(24*time.Hour - time.Nanosecond)

	var rows []models.AiRecommendStocks
	err := db.Dao.Model(&models.AiRecommendStocks{}).
		Where("COALESCE(data_time, created_at) BETWEEN ? AND ?", start, end).
		Where("(summary_version = ? OR activation_rule_source IN ?)", marketSummaryPhase3Version, []string{"market_summary", "market_summary_embedded"}).
		Order("COALESCE(data_time, created_at) asc").
		Find(&rows).Error
	if err != nil {
		return nil, nil, err
	}

	result := make([]marketSummaryExcludedStock, 0, len(rows))
	index := make(map[string]marketSummaryExcludedStock, len(rows))
	for _, row := range rows {
		if !shouldExcludeSameDayMarketSummaryRecommend(row) {
			continue
		}
		code := normalizeRecommendStockCode(row.StockCode)
		if code == "" {
			continue
		}
		if _, exists := index[code]; exists {
			continue
		}
		recordTime := recommendRecordTime(row)
		item := marketSummaryExcludedStock{
			StockCode:          code,
			StockName:          strings.TrimSpace(row.StockName),
			FirstRecommendTime: formatYieldDisplayTime(recordTime),
		}
		index[code] = item
		result = append(result, item)
	}
	return result, index, nil
}

func shouldExcludeSameDayMarketSummaryRecommend(row models.AiRecommendStocks) bool {
	if !isMarketSummaryActivationSource(row.ActivationRuleSource) {
		return false
	}
	if isPendingMarketDataRecommend(&row) {
		return false
	}
	if normalizeRecommendExecutionState(row.ExecutionState) == recommendExecutionAnalysisOnly {
		return false
	}
	status := normalizeRecommendStatus(row.RecommendStatus)
	if status == "missing_market_data" || status == "avoid" || status == "insufficient_evidence" || status == "controversial" {
		return false
	}
	if normalizeRecommendCategory(row.RecommendCategory) == "avoid" {
		return false
	}
	if !hasRecoverableMarketSummaryTradePlan(row) {
		return false
	}
	return true
}

func selectMarketSummaryFinalCandidates(verified []marketSummaryVerifiedCandidate, excluded map[string]marketSummaryExcludedStock, window marketSummaryTimeWindow, logState *marketSummaryRouteLog, limit int) []marketSummaryVerifiedCandidate {
	if len(verified) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = marketSummaryFinalCandidateLimit
	}
	scored := make([]marketSummaryCandidateScore, 0, len(verified))
	for _, candidate := range verified {
		code := normalizeRecommendStockCode(candidate.StockCode)
		if code == "" {
			continue
		}
		if excluded != nil {
			if excludedItem, exists := excluded[code]; exists {
				if logState != nil {
					logState.DroppedCandidates = append(logState.DroppedCandidates, fmt.Sprintf("同日已推荐排除:%s(%s) first=%s", firstNonEmptyText(candidate.StockName, excludedItem.StockName), code, excludedItem.FirstRecommendTime))
				}
				continue
			}
		}
		score := scoreMarketSummaryVerifiedCandidate(candidate, window)
		if reason := marketSummaryCandidateQualityRejectionReason(score); reason != "" {
			if logState != nil {
				logState.DroppedCandidates = append(logState.DroppedCandidates, fmt.Sprintf("源头质量门槛未通过:%s(%s) score=%d reason=%s", candidate.StockName, code, score.TotalScore, reason))
			}
			continue
		}
		scored = append(scored, score)
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].TotalScore != scored[j].TotalScore {
			return scored[i].TotalScore > scored[j].TotalScore
		}
		if !scored[i].LatestEvidenceTime.Equal(scored[j].LatestEvidenceTime) {
			return scored[i].LatestEvidenceTime.After(scored[j].LatestEvidenceTime)
		}
		return scored[i].Candidate.StockCode < scored[j].Candidate.StockCode
	})
	if len(scored) > limit {
		for _, item := range scored[limit:] {
			if logState != nil {
				logState.DroppedCandidates = append(logState.DroppedCandidates, fmt.Sprintf("候选评分截断:%s(%s) score=%d", item.Candidate.StockName, item.Candidate.StockCode, item.TotalScore))
			}
		}
		scored = scored[:limit]
	}
	result := make([]marketSummaryVerifiedCandidate, 0, len(scored))
	for _, item := range scored {
		result = append(result, item.Candidate)
		if logState != nil {
			logState.addNote("verified score %s(%s) score=%d windowEvidence=%d highTrust=%d distinctEvidence=%d", item.Candidate.StockName, item.Candidate.StockCode, item.TotalScore, item.WindowEvidenceCt, item.HighTrustEvidenceCt, item.DistinctEvidenceCt)
		}
	}
	return result
}

func marketSummaryCandidateQualityRejectionReason(score marketSummaryCandidateScore) string {
	candidate := score.Candidate
	if score.DistinctEvidenceCt < 2 {
		return fmt.Sprintf("证据类别不足%d类", score.DistinctEvidenceCt)
	}
	if score.HighTrustEvidenceCt <= 0 {
		return "缺少高信任证据"
	}
	if strings.TrimSpace(candidate.MinutePrice) == "" && strings.TrimSpace(candidate.CurrentPrice) == "" && strings.TrimSpace(candidate.AuctionPrice) == "" {
		return "缺少有效价格锚点"
	}
	if strings.TrimSpace(candidate.TechnicalSnapshot) == "" && strings.TrimSpace(candidate.MinuteAmount) == "" && strings.TrimSpace(candidate.AuctionAmount) == "" {
		return "缺少技术/资金确认"
	}
	if len(candidate.NegativeSignals) >= 2 {
		return "反向证据过多"
	}
	if score.WindowEvidenceCt == 0 {
		return "当前筛选窗口内无新证据"
	}
	if score.TotalScore < marketSummaryMinFinalCandidateScore {
		return fmt.Sprintf("综合质量分%d低于%d", score.TotalScore, marketSummaryMinFinalCandidateScore)
	}
	return ""
}

func scoreMarketSummaryVerifiedCandidate(candidate marketSummaryVerifiedCandidate, window marketSummaryTimeWindow) marketSummaryCandidateScore {
	score := marketSummaryCandidateScore{Candidate: candidate}
	distinctEvidence := map[string]struct{}{}
	for _, ref := range candidate.EvidenceSources {
		if typeName := strings.TrimSpace(ref.Type); typeName != "" {
			distinctEvidence[typeName] = struct{}{}
		}
		if strings.EqualFold(strings.TrimSpace(ref.TrustLevel), "high") {
			score.HighTrustEvidenceCt++
			score.TotalScore += 12
		} else if strings.EqualFold(strings.TrimSpace(ref.TrustLevel), "medium") {
			score.TotalScore += 6
		}
		if publishedAt, ok := parseMarketSummaryWindowTime(ref.PublishedAt); ok {
			if publishedAt.After(score.LatestEvidenceTime) {
				score.LatestEvidenceTime = publishedAt
			}
			if marketSummaryTimeInWindow(publishedAt, window) || marketSummaryDateInWindow(publishedAt, window) {
				score.WindowEvidenceCt++
				score.TotalScore += 10
			}
		}
	}
	score.DistinctEvidenceCt = len(distinctEvidence)
	score.TotalScore += minInt(score.DistinctEvidenceCt*10, 40)
	score.TotalScore += minInt(len(candidate.PositiveSignals)*4, 16)
	score.TotalScore -= minInt(len(candidate.NegativeSignals)*5, 20)

	if strings.TrimSpace(candidate.MinutePrice) != "" {
		score.TotalScore += 16
	}
	if strings.TrimSpace(candidate.MinuteAmount) != "" {
		score.TotalScore += 8
	}
	if strings.TrimSpace(candidate.CurrentPrice) != "" {
		score.TotalScore += 6
	}
	switch strings.TrimSpace(candidate.PriceAnchorSource) {
	case "minute_bar":
		score.TotalScore += 10
	case "call_auction":
		score.TotalScore += 7
	case "current_quote":
		score.TotalScore += 4
	}
	if strings.TrimSpace(candidate.TechnicalSnapshot) != "" {
		score.TotalScore += 8
	}
	if candidate.TechnicalMetrics.PriceAboveMa5 {
		score.TotalScore += 4
	}
	if candidate.TechnicalMetrics.PriceAboveMa10 {
		score.TotalScore += 4
	}
	if candidate.TechnicalMetrics.Breakout3dHigh {
		score.TotalScore += 4
	}
	if candidate.TechnicalMetrics.Breakout5dHigh {
		score.TotalScore += 4
	}
	if candidate.TechnicalMetrics.PullbackNearMa5 {
		score.TotalScore += 3
	}
	if v := parseScoreFloat(candidate.TechnicalMetrics.MinuteVolumeVsAvg5); v >= 1.2 {
		score.TotalScore += 8
	} else if v >= 1 {
		score.TotalScore += 4
	}
	if v := parseScoreFloat(candidate.TechnicalMetrics.MinuteVolumeVsAvg10); v >= 1.2 {
		score.TotalScore += 5
	} else if v >= 1 {
		score.TotalScore += 2
	}
	if score.WindowEvidenceCt == 0 {
		score.TotalScore -= 12
	}
	if score.LatestEvidenceTime.IsZero() {
		score.LatestEvidenceTime = fallbackMarketSummaryCandidateTime(candidate)
	}
	return score
}

func fallbackMarketSummaryCandidateTime(candidate marketSummaryVerifiedCandidate) time.Time {
	for _, raw := range []string{
		candidate.MinuteDate + " " + candidate.MinuteTime,
		candidate.CurrentPriceTime,
		candidate.AuctionDate + " " + candidate.AuctionTime,
	} {
		if t, ok := parseMarketSummaryWindowTime(strings.TrimSpace(raw)); ok {
			return t
		}
	}
	return time.Time{}
}

func parseScoreFloat(raw string) float64 {
	text := strings.TrimSpace(strings.TrimSuffix(raw, "%"))
	if text == "" {
		return 0
	}
	v, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0
	}
	return v
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
