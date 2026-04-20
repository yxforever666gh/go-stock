package data

import (
	"fmt"
	"go-stock/backend/db"
	"go-stock/backend/models"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm/clause"
)

const (
	openingReviewPhase0940         = "09:40"
	openingReviewScopePending      = "pending"
	openingReviewScopeHolding      = "holding"
	openingReviewModelName         = "system_opening_review"
	openingReviewActionContinue    = "continue_plan"
	openingReviewActionObserveOnly = "observe_only"
	openingReviewActionCancel      = "cancel_plan"
	openingReviewActionHold        = "hold"
	openingReviewActionReduceRisk  = "reduce_risk"
)

type openingReviewMarketSnapshot struct {
	StockCode    string
	StockName    string
	OpeningPrice float64
	PreClose     float64
	AuctionPrice float64
	MinutePrice  float64
	MinuteVolume float64
	MinuteAmount float64
}

func ensureOpeningReviewSchema() error {
	return db.Dao.AutoMigrate(&models.AiRecommendOpeningReview{})
}

func RunMorningOpeningReview(now time.Time) (string, error) {
	if err := ensureOpeningReviewSchema(); err != nil {
		return "", err
	}

	loc := cnLocation()
	tradeDay := defaultMarketSummaryTradeDay(now.In(loc))
	pendingReviews, err := buildPendingOpeningReviews(tradeDay, now.In(loc))
	if err != nil {
		return "", err
	}
	holdingReviews, err := buildHoldingOpeningReviews(tradeDay, now.In(loc))
	if err != nil {
		return "", err
	}

	all := make([]models.AiRecommendOpeningReview, 0, len(pendingReviews)+len(holdingReviews))
	all = append(all, pendingReviews...)
	all = append(all, holdingReviews...)
	if len(all) > 0 {
		if err := saveOpeningReviews(all); err != nil {
			return "", err
		}
	}
	return buildMorningOpeningReviewMarkdown(tradeDay, pendingReviews, holdingReviews), nil
}

func saveOpeningReviews(rows []models.AiRecommendOpeningReview) error {
	if len(rows) == 0 {
		return nil
	}
	return db.Dao.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "recommend_id"},
			{Name: "trade_date"},
			{Name: "review_scope"},
			{Name: "review_phase"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"updated_at",
			"stock_code",
			"stock_name",
			"opening_price",
			"auction_price",
			"minute_price",
			"minute_volume",
			"minute_amount",
			"gap_type",
			"action",
			"reason",
			"suggested_stop_loss",
			"suggested_take_profit",
			"model_name",
			"raw_summary",
		}),
	}).CreateInBatches(rows, 50).Error
}

func loadLatestOpeningReviewSummaryMap(recommendIDs []uint) (map[uint]*models.AiRecommendOpeningReviewSummary, error) {
	result := make(map[uint]*models.AiRecommendOpeningReviewSummary)
	if len(recommendIDs) == 0 {
		return result, nil
	}
	if err := ensureOpeningReviewSchema(); err != nil {
		return nil, err
	}

	normalized := make([]uint, 0, len(recommendIDs))
	seen := make(map[uint]struct{}, len(recommendIDs))
	for _, id := range recommendIDs {
		if id == 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		normalized = append(normalized, id)
	}
	if len(normalized) == 0 {
		return result, nil
	}

	rows := make([]models.AiRecommendOpeningReview, 0, len(normalized))
	err := db.Dao.Model(&models.AiRecommendOpeningReview{}).
		Where("recommend_id IN ?", normalized).
		Order("trade_date DESC, updated_at DESC, id DESC").
		Find(&rows).Error
	if err != nil {
		if isSQLiteNoSuchTable(err) {
			return result, nil
		}
		return nil, err
	}
	for _, row := range rows {
		if row.RecommendID == 0 {
			continue
		}
		if _, exists := result[row.RecommendID]; exists {
			continue
		}
		result[row.RecommendID] = convertOpeningReviewSummary(row)
	}
	return result, nil
}

func convertOpeningReviewSummary(row models.AiRecommendOpeningReview) *models.AiRecommendOpeningReviewSummary {
	return &models.AiRecommendOpeningReviewSummary{
		RecommendID:         row.RecommendID,
		TradeDate:           strings.TrimSpace(row.TradeDate),
		ReviewScope:         strings.TrimSpace(row.ReviewScope),
		ReviewPhase:         strings.TrimSpace(row.ReviewPhase),
		OpeningPrice:        round2(row.OpeningPrice),
		AuctionPrice:        round2(row.AuctionPrice),
		MinutePrice:         round2(row.MinutePrice),
		MinuteVolume:        round2(row.MinuteVolume),
		MinuteAmount:        round2(row.MinuteAmount),
		GapType:             strings.TrimSpace(row.GapType),
		Action:              strings.TrimSpace(row.Action),
		Reason:              strings.TrimSpace(row.Reason),
		SuggestedStopLoss:   round2(row.SuggestedStopLoss),
		SuggestedTakeProfit: round2(row.SuggestedTakeProfit),
		ModelName:           strings.TrimSpace(row.ModelName),
		RawSummary:          strings.TrimSpace(row.RawSummary),
	}
}

func buildPendingOpeningReviews(tradeDay time.Time, now time.Time) ([]models.AiRecommendOpeningReview, error) {
	loc := cnLocation()
	dayStart := time.Date(tradeDay.Year(), tradeDay.Month(), tradeDay.Day(), 0, 0, 0, 0, loc)

	records := make([]models.AiRecommendStocks, 0, 64)
	err := db.Dao.Model(&models.AiRecommendStocks{}).
		Where("COALESCE(data_time, created_at) < ?", dayStart).
		Order("COALESCE(data_time, created_at) DESC, id DESC").
		Find(&records).Error
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return []models.AiRecommendOpeningReview{}, nil
	}
	if records, err = applyYieldOverridesToRecommendRecords(records); err != nil {
		return nil, err
	}
	recordStateMap, err := loadYieldRecordStateMapByRecommendRecords(records)
	if err != nil && !isSQLiteNoSuchTable(err) {
		return nil, err
	}

	snapshotCache := make(map[string]openingReviewMarketSnapshot)
	reviews := make([]models.AiRecommendOpeningReview, 0, len(records))
	for _, rec := range records {
		if !shouldDisplayRecommendInYield(&rec) {
			continue
		}
		if eligibility, _ := resolveRecommendBacktestEligibility(&rec); eligibility != recommendBacktestEligible {
			continue
		}
		recordTime := recommendRecordTime(rec)
		if recordTime.IsZero() || isSameCNTradeDate(recordTime, tradeDay) {
			continue
		}
		state, hasState := recordStateMap[rec.ID]
		if hasState {
			status := strings.TrimSpace(state.ActivationStatus)
			if status != "" && status != "pending" {
				continue
			}
		} else {
			status := strings.TrimSpace(rec.ActivationStatus)
			if status == "activated" || status == "skipped" || status == "invalid" || status == "ineligible" {
				continue
			}
		}
		if expiry, _, ok := resolveRecommendPendingActivationExpiry(recordTime, rec.ExpectedCycle); ok && !expiry.After(now) {
			continue
		}

		snapshot, err := loadOpeningReviewMarketSnapshot(normalizeRecommendStockCode(rec.StockCode), strings.TrimSpace(rec.StockName), now, snapshotCache)
		if err != nil {
			continue
		}
		reviews = append(reviews, evaluatePendingOpeningReview(rec, tradeDay, snapshot))
	}

	sort.SliceStable(reviews, func(i, j int) bool {
		return reviews[i].StockCode < reviews[j].StockCode
	})
	return reviews, nil
}

func buildHoldingOpeningReviews(tradeDay time.Time, now time.Time) ([]models.AiRecommendOpeningReview, error) {
	rows := make([]models.AiRecommendYieldRecordState, 0, 32)
	err := db.Dao.Model(&models.AiRecommendYieldRecordState{}).
		Where("activation_status = ?", "activated").
		Order("activation_time DESC, recommend_id DESC").
		Find(&rows).Error
	if err != nil {
		if isSQLiteNoSuchTable(err) {
			return []models.AiRecommendOpeningReview{}, nil
		}
		return nil, err
	}
	if len(rows) == 0 {
		return []models.AiRecommendOpeningReview{}, nil
	}

	ids := make([]uint, 0, len(rows))
	for _, row := range rows {
		if isSoldPositionStatus(row.PositionStatus) || row.RecommendID == 0 {
			continue
		}
		ids = append(ids, row.RecommendID)
	}
	if len(ids) == 0 {
		return []models.AiRecommendOpeningReview{}, nil
	}

	records := make([]models.AiRecommendStocks, 0, len(ids))
	if err := db.Dao.Model(&models.AiRecommendStocks{}).Where("id IN ?", ids).Find(&records).Error; err != nil {
		return nil, err
	}
	recordMap := make(map[uint]models.AiRecommendStocks, len(records))
	for _, rec := range records {
		recordMap[rec.ID] = rec
	}

	snapshotCache := make(map[string]openingReviewMarketSnapshot)
	reviews := make([]models.AiRecommendOpeningReview, 0, len(rows))
	for _, state := range rows {
		if isSoldPositionStatus(state.PositionStatus) || state.RecommendID == 0 {
			continue
		}
		rec, exists := recordMap[state.RecommendID]
		if !exists {
			continue
		}
		snapshot, err := loadOpeningReviewMarketSnapshot(normalizeRecommendStockCode(rec.StockCode), strings.TrimSpace(rec.StockName), now, snapshotCache)
		if err != nil {
			continue
		}
		reviews = append(reviews, evaluateHoldingOpeningReview(rec, state, tradeDay, snapshot))
	}

	sort.SliceStable(reviews, func(i, j int) bool {
		return reviews[i].StockCode < reviews[j].StockCode
	})
	return reviews, nil
}

func loadOpeningReviewMarketSnapshot(stockCode, stockName string, now time.Time, cache map[string]openingReviewMarketSnapshot) (openingReviewMarketSnapshot, error) {
	if snapshot, exists := cache[stockCode]; exists {
		return snapshot, nil
	}
	if strings.TrimSpace(stockCode) == "" {
		return openingReviewMarketSnapshot{}, fmt.Errorf("stock code is empty")
	}

	priceCode := ConvertTushareCodeToStockCode(stockCode)
	minuteData, minuteDate := runStockMinuteWithTimeout(priceCode, 4*time.Second)
	stockData, _ := runStockRealtimeWithTimeout(priceCode, 4*time.Second)
	auctionData, _ := runStockCallAuctionWithTimeout(stockCode, 4*time.Second, now)
	anchor := resolveMarketSummaryPriceAnchorAt(auctionData, minuteData, minuteDate, stockData, now)

	snapshot := openingReviewMarketSnapshot{
		StockCode:    stockCode,
		StockName:    stockName,
		OpeningPrice: parseFirstPositiveFloat(anchor.Auction.Open),
		AuctionPrice: parseFirstPositiveFloat(anchor.Auction.Price),
		MinutePrice:  parseFirstPositiveFloat(anchor.MinutePrice),
		MinuteVolume: parseFirstPositiveFloat(anchor.MinuteVolume),
		MinuteAmount: parseFirstPositiveFloat(anchor.MinuteAmount),
	}
	if stockData != nil && len(*stockData) > 0 {
		item := (*stockData)[0]
		if snapshot.OpeningPrice <= 0 {
			snapshot.OpeningPrice = parseFirstPositiveFloat(item.Open)
		}
		snapshot.PreClose = parseFirstPositiveFloat(item.PreClose)
	}
	cache[stockCode] = snapshot
	return snapshot, nil
}

func evaluatePendingOpeningReview(rec models.AiRecommendStocks, tradeDay time.Time, snapshot openingReviewMarketSnapshot) models.AiRecommendOpeningReview {
	buyMin, buyMax, _ := parseRecommendEntryRange(rec)
	stopLoss, _ := parseStopLossPrice(rec)
	takeProfit, _ := parseStopProfitPrice(rec)
	openPrice := snapshot.OpeningPrice
	if openPrice <= 0 {
		openPrice = snapshot.MinutePrice
	}

	action := openingReviewActionObserveOnly
	reason := "未拿到可靠开盘价/分钟线，无法做出更强结论，原计划暂保留。"
	if openPrice > 0 {
		maxChase := resolveRecommendOpeningMaxChasePrice(&rec, buyMax)
		switch {
		case stopLoss > 0 && openPrice <= stopLoss:
			action = openingReviewActionCancel
			reason = fmt.Sprintf("真实开盘价 %.2f 已低于止损/失效位 %.2f，隔夜计划应直接取消。", round2(openPrice), round2(stopLoss))
		case maxChase > 0 && openPrice > maxChase:
			action = openingReviewActionCancel
			reason = fmt.Sprintf("真实开盘价 %.2f 高于可接受追价上限 %.2f，已脱离原计划性价比。", round2(openPrice), round2(maxChase))
		case buyMin > 0 && openPrice >= buyMin && openPrice <= buyMax:
			action = openingReviewActionContinue
			reason = fmt.Sprintf("真实开盘价 %.2f 仍处于原买入区间 %.2f-%.2f 内，09:40 后可继续按原计划观察触发。", round2(openPrice), round2(buyMin), round2(buyMax))
		case buyMax > 0 && maxChase > 0 && openPrice > buyMax && openPrice <= maxChase:
			action = openingReviewActionContinue
			reason = fmt.Sprintf("真实开盘价 %.2f 高于买入区间上沿 %.2f，但仍未超过追价上限 %.2f，需等待 09:40 后确认承接与量能。", round2(openPrice), round2(buyMax), round2(maxChase))
		case buyMin > 0 && openPrice < buyMin:
			action = openingReviewActionObserveOnly
			reason = fmt.Sprintf("真实开盘价 %.2f 低于买入区间下沿 %.2f，说明开盘承接偏弱，继续观察而不是提前激活。", round2(openPrice), round2(buyMin))
		default:
			action = openingReviewActionObserveOnly
			reason = fmt.Sprintf("真实开盘价 %.2f 与原计划未形成直接冲突，但仍需等待 09:40 后量价确认。", round2(openPrice))
		}
	}

	rawSummary := fmt.Sprintf(
		"%s（%s）开盘复核：动作=%s；开盘价=%.2f；竞价价=%.2f；09:40 最新价=%.2f；原因：%s",
		firstNonEmptyText(rec.StockName, snapshot.StockName),
		normalizeRecommendStockCode(rec.StockCode),
		action,
		round2(snapshot.OpeningPrice),
		round2(snapshot.AuctionPrice),
		round2(snapshot.MinutePrice),
		reason,
	)

	return models.AiRecommendOpeningReview{
		RecommendID:         rec.ID,
		StockCode:           normalizeRecommendStockCode(rec.StockCode),
		StockName:           strings.TrimSpace(rec.StockName),
		TradeDate:           tradeDay.Format("2006-01-02"),
		ReviewScope:         openingReviewScopePending,
		ReviewPhase:         openingReviewPhase0940,
		OpeningPrice:        round2(snapshot.OpeningPrice),
		AuctionPrice:        round2(snapshot.AuctionPrice),
		MinutePrice:         round2(snapshot.MinutePrice),
		MinuteVolume:        round2(snapshot.MinuteVolume),
		MinuteAmount:        round2(snapshot.MinuteAmount),
		GapType:             classifyOpeningGap(snapshot.OpeningPrice, snapshot.PreClose),
		Action:              action,
		Reason:              reason,
		SuggestedStopLoss:   round2(stopLoss),
		SuggestedTakeProfit: round2(takeProfit),
		ModelName:           openingReviewModelName,
		RawSummary:          rawSummary,
	}
}

func evaluateHoldingOpeningReview(rec models.AiRecommendStocks, state models.AiRecommendYieldRecordState, tradeDay time.Time, snapshot openingReviewMarketSnapshot) models.AiRecommendOpeningReview {
	stopLoss, _ := parseStopLossPrice(rec)
	takeProfit, _ := parseStopProfitPrice(rec)
	openPrice := snapshot.OpeningPrice
	if openPrice <= 0 {
		openPrice = snapshot.MinutePrice
	}

	action := openingReviewActionHold
	reason := "持仓未出现开盘级别异常，保持原止盈止损框架，盘中继续按既有纪律执行。"
	switch {
	case openPrice > 0 && stopLoss > 0 && openPrice <= stopLoss:
		action = openingReviewActionReduceRisk
		reason = fmt.Sprintf("真实开盘价 %.2f 已跌破止损位 %.2f，说明开盘跳空风险已经兑现，应优先执行风控。", round2(openPrice), round2(stopLoss))
	case openPrice > 0 && takeProfit > 0 && openPrice >= takeProfit:
		action = openingReviewActionReduceRisk
		reason = fmt.Sprintf("真实开盘价 %.2f 已高于止盈位 %.2f，说明收益在开盘已兑现，宜优先做止盈处理。", round2(openPrice), round2(takeProfit))
	case snapshot.MinutePrice > 0 && stopLoss > 0 && snapshot.MinutePrice <= stopLoss*1.01:
		action = openingReviewActionReduceRisk
		reason = fmt.Sprintf("09:40 最新价 %.2f 已逼近止损位 %.2f，建议收紧纪律，避免把开盘波动拖成被动止损。", round2(snapshot.MinutePrice), round2(stopLoss))
	}

	rawSummary := fmt.Sprintf(
		"%s（%s）持仓复核：动作=%s；开盘价=%.2f；09:40 最新价=%.2f；原因：%s",
		firstNonEmptyText(rec.StockName, snapshot.StockName),
		normalizeRecommendStockCode(rec.StockCode),
		action,
		round2(snapshot.OpeningPrice),
		round2(snapshot.MinutePrice),
		reason,
	)

	return models.AiRecommendOpeningReview{
		RecommendID:         state.RecommendID,
		StockCode:           normalizeRecommendStockCode(rec.StockCode),
		StockName:           strings.TrimSpace(rec.StockName),
		TradeDate:           tradeDay.Format("2006-01-02"),
		ReviewScope:         openingReviewScopeHolding,
		ReviewPhase:         openingReviewPhase0940,
		OpeningPrice:        round2(snapshot.OpeningPrice),
		AuctionPrice:        round2(snapshot.AuctionPrice),
		MinutePrice:         round2(snapshot.MinutePrice),
		MinuteVolume:        round2(snapshot.MinuteVolume),
		MinuteAmount:        round2(snapshot.MinuteAmount),
		GapType:             classifyOpeningGap(snapshot.OpeningPrice, snapshot.PreClose),
		Action:              action,
		Reason:              reason,
		SuggestedStopLoss:   round2(stopLoss),
		SuggestedTakeProfit: round2(takeProfit),
		ModelName:           openingReviewModelName,
		RawSummary:          rawSummary,
	}
}

func buildMorningOpeningReviewMarkdown(tradeDay time.Time, pending, holding []models.AiRecommendOpeningReview) string {
	lines := []string{
		"",
		"## 09:40 开盘复核",
		fmt.Sprintf("交易日：%s", tradeDay.Format("2006-01-02")),
		"",
		"### 待激活隔夜推荐",
	}
	if len(pending) == 0 {
		lines = append(lines, "- 本时点没有需要复核的隔夜待激活推荐。")
	} else {
		for _, item := range pending {
			lines = append(lines, "- "+strings.TrimSpace(item.RawSummary))
		}
	}
	lines = append(lines, "", "### 已持有股票")
	if len(holding) == 0 {
		lines = append(lines, "- 本时点没有需要复核的持仓股票。")
	} else {
		for _, item := range holding {
			lines = append(lines, "- "+strings.TrimSpace(item.RawSummary))
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func parseFirstPositiveFloat(values ...string) float64 {
	for _, raw := range values {
		text := strings.TrimSpace(raw)
		if text == "" {
			continue
		}
		v, err := parseFlexibleFloat(text)
		if err == nil && v > 0 {
			return round2(v)
		}
	}
	return 0
}

func parseFlexibleFloat(raw string) (float64, error) {
	text := strings.TrimSpace(raw)
	text = strings.ReplaceAll(text, ",", "")
	return strconv.ParseFloat(text, 64)
}

func classifyOpeningGap(openPrice, preClose float64) string {
	if openPrice <= 0 || preClose <= 0 {
		return ""
	}
	gapPct := (openPrice - preClose) / preClose
	switch {
	case gapPct >= 0.03:
		return "gap_up_large"
	case gapPct > 0:
		return "gap_up"
	case gapPct <= -0.03:
		return "gap_down_large"
	case gapPct < 0:
		return "gap_down"
	default:
		return "flat_open"
	}
}

func resolveRecommendOpeningMaxChasePrice(rec *models.AiRecommendStocks, buyMax float64) float64 {
	if rec == nil {
		return 0
	}
	if rule, err := parseActivationRuleJSON(strings.TrimSpace(rec.ActivationRuleJSON)); err == nil {
		if policy := resolveActivationOpeningPolicy(rule); policy != nil && policy.MaxChasePrice > 0 {
			return round2(policy.MaxChasePrice)
		}
	}
	refPrice := resolveRecommendReferencePrice(*rec)
	maxChase := buyMax
	if refPrice > 0 && refPrice*1.03 > maxChase {
		maxChase = refPrice * 1.03
	}
	if buyMax > 0 && buyMax*1.015 > maxChase {
		maxChase = buyMax * 1.015
	}
	return round2(maxChase)
}
