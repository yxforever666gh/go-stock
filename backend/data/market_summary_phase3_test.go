package data

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestExtractJSONPayloadFromCodeFence(t *testing.T) {
	text := "这里是解释\n```json\n{\"candidateStocks\":[{\"stockName\":\"中际旭创\",\"stockCode\":\"300308.SZ\"}]}\n```"
	got := extractJSONPayload(text)
	want := `{"candidateStocks":[{"stockName":"中际旭创","stockCode":"300308.SZ"}]}`
	if got != want {
		t.Fatalf("extractJSONPayload() = %q, want %q", got, want)
	}
}

func TestSanitizeMarketSummaryDiscoveryResultLimitsAndResolves(t *testing.T) {
	result := &marketSummaryDiscoveryResult{
		CandidateStocks: append([]marketSummaryRouteCandidate{
			{StockName: "中际旭创", StockCode: "300308.SZ", Direction: "AI算力"},
			{StockName: "中际旭创", StockCode: "300308.SZ", Direction: "AI算力"},
			{StockName: "腾讯控股", StockCode: "00700.HK", Direction: "港股"},
			{StockName: "新易盛", StockCode: "300502.SZ", Direction: "AI算力"},
		}, buildMarketSummaryRouteCandidatesForTest(20)...),
	}
	sanitizeMarketSummaryDiscoveryResult(result)
	if len(result.CandidateStocks) != defaultMarketSummaryRouteBudget().CandidateLimit {
		t.Fatalf("expected %d candidates, got %d", defaultMarketSummaryRouteBudget().CandidateLimit, len(result.CandidateStocks))
	}
	if result.CandidateStocks[0].StockCode != "300308.SZ" {
		t.Fatalf("unexpected first code: %s", result.CandidateStocks[0].StockCode)
	}
	if result.CandidateStocks[1].StockCode != "300502.SZ" {
		t.Fatalf("unexpected second code: %s", result.CandidateStocks[1].StockCode)
	}
}

func TestDefaultMarketSummaryRouteBudgetExpandsV136Candidates(t *testing.T) {
	budget := defaultMarketSummaryRouteBudget()
	if budget.CandidateLimit != 18 {
		t.Fatalf("CandidateLimit = %d, want 18", budget.CandidateLimit)
	}
	if budget.VerificationStockLimit != 12 {
		t.Fatalf("VerificationStockLimit = %d, want 12", budget.VerificationStockLimit)
	}
	if marketSummaryFinalCandidateLimit != 6 {
		t.Fatalf("marketSummaryFinalCandidateLimit = %d, want 6", marketSummaryFinalCandidateLimit)
	}
	if marketSummaryMaxProductionCandidates != 4 {
		t.Fatalf("marketSummaryMaxProductionCandidates = %d, want 4", marketSummaryMaxProductionCandidates)
	}
}

func buildMarketSummaryRouteCandidatesForTest(count int) []marketSummaryRouteCandidate {
	result := make([]marketSummaryRouteCandidate, 0, count)
	for i := 0; i < count; i++ {
		code := fmt.Sprintf("%06d.SZ", 100000+i)
		result = append(result, marketSummaryRouteCandidate{
			StockName: fmt.Sprintf("测试股票%d", i),
			StockCode: code,
			Direction: "测试方向",
		})
	}
	return result
}

func TestBuildPhase3FinalMessagesIncludesVerifiedPayload(t *testing.T) {
	logState := newMarketSummaryRouteLog()
	logState.DroppedCandidates = append(logState.DroppedCandidates,
		"源头质量门槛未通过:蓝思科技(300433.SZ) score=83 reason=最差成交价盈亏比 0.71 低于 0.80",
	)
	messages := buildPhase3FinalMessages(
		"system prompt",
		"总结市场机会",
		marketSummaryDiscoveryInput{
			Question:    "总结市场机会",
			RunSlot:     string(marketSummaryRunSlotMidday),
			WindowStart: "2026-04-09 09:30:00",
			WindowEnd:   "2026-04-09 11:32:00",
		},
		&marketSummaryDiscoveryResult{CandidateStocks: []marketSummaryRouteCandidate{{StockName: "中际旭创", StockCode: "300308.SZ"}}},
		[]marketSummaryVerifiedCandidate{{
			StockName:         "中际旭创",
			StockCode:         "300308.SZ",
			MinutePrice:       "138.20",
			MinuteAmount:      "8650.00",
			AuctionPrice:      "137.80",
			PriceAnchorSource: "minute_bar",
			EvidenceSources:   []aiEvidenceReference{{Type: "一级披露", Summary: "订单公告", TrustLevel: "high"}},
		}},
		[]marketSummaryExcludedStock{{
			StockCode:          "002371.SZ",
			StockName:          "北方华创",
			FirstRecommendTime: "2026-04-09 09:32:00",
		}},
		[]marketSummarySkippedReviewCandidate{{
			RecommendID:       101,
			StockName:         "沪电股份",
			StockCode:         "002463.SZ",
			RecommendBuyPrice: "34.5-35.2",
			SkipReason:        "旧逻辑跳过，需要复审",
		}},
		logState,
	)
	if len(messages) == 0 {
		t.Fatal("expected non-empty messages")
	}
	last := messages[len(messages)-1]["content"].(string)
	if !containsAll(last, []string{"事件发现层", "证据核验层", "固定 7 个一级标题", "推荐股票池", "跳过复审", "原记录ID", "买入区间", "买入依据", "technicalMetrics", "minutePrice", "minuteAmount", "auctionPrice", "价格锚点", "当日已推荐股票排除池", "候选过滤/跳过原因", "analysis_only", "最多输出 6 只", "最多 4 只可作为可交易生产候选", "暂无新增高质量候选标的", "本次筛选窗口"}) {
		t.Fatalf("unexpected final instruction: %s", last)
	}
	if !messagesContainText(messages, "最差成交价盈亏比 0.71 低于 0.80") {
		t.Fatalf("expected dropped candidate reason in messages: %+v", messages)
	}
}

func messagesContainText(messages []map[string]any, needle string) bool {
	for _, message := range messages {
		if strings.Contains(fmt.Sprint(message["content"]), needle) {
			return true
		}
	}
	return false
}

func TestBuildMarketSummaryTechnicalMetrics(t *testing.T) {
	kline := &[]KLineData{
		{Close: "10.00", High: "10.20", Low: "9.80"},
		{Close: "10.20", High: "10.30", Low: "10.00"},
		{Close: "10.40", High: "10.50", Low: "10.10"},
		{Close: "10.60", High: "10.80", Low: "10.30"},
		{Close: "10.90", High: "11.00", Low: "10.50"},
		{Close: "11.10", High: "11.20", Low: "10.80"},
	}
	stockData := &[]StockInfo{{
		Amount: "123456789",
		Volume: "456789",
	}}
	minuteData := &[]MinuteData{
		{Time: "14:54", Volume: 100, Amount: 1000},
		{Time: "14:55", Volume: 110, Amount: 1200},
		{Time: "14:56", Volume: 120, Amount: 1300},
		{Time: "14:57", Volume: 130, Amount: 1500},
		{Time: "14:58", Volume: 140, Amount: 1600},
		{Time: "14:59", Volume: 220, Amount: 2500},
	}
	anchor := marketSummaryPriceAnchor{
		Auction: marketSummaryAuctionSnapshot{
			VolumeRatio:  "1.80",
			TurnoverRate: "2.30",
		},
	}

	metrics := buildMarketSummaryTechnicalMetrics(kline, stockData, minuteData, anchor)
	if metrics.Ma5 == "" || metrics.Ma10 != "" {
		t.Fatalf("unexpected ma values: %+v", metrics)
	}
	if metrics.High5d == "" || metrics.Low5d == "" {
		t.Fatalf("expected high/low window metrics, got %+v", metrics)
	}
	if metrics.MinuteVolumeVsAvg5 == "" {
		t.Fatalf("expected minute volume ratio, got %+v", metrics)
	}
	if !metrics.PriceAboveMa5 || !metrics.Breakout3dHigh {
		t.Fatalf("expected bullish flags, got %+v", metrics)
	}
	if metrics.DayAmount != "123456789" || metrics.TurnoverRate != "2.30" {
		t.Fatalf("expected day/turnover fields, got %+v", metrics)
	}
}

func TestResolveMarketSummaryPriceAnchor_PrefersMinuteBar(t *testing.T) {
	minuteData := &[]MinuteData{
		{Time: "14:58", Price: 12.31, Volume: 321, Amount: 6543.21},
		{Time: "14:59", Price: 12.35, Volume: 456, Amount: 7890.12},
	}
	stockData := &[]StockInfo{{
		Date:  "2026-03-11",
		Time:  "14:59:55",
		Price: "12.36",
	}}

	anchor := resolveMarketSummaryPriceAnchorAt(nil, minuteData, "20260311", stockData, time.Date(2026, 3, 11, 14, 59, 0, 0, cnLocation()))
	if anchor.MinutePrice != "12.35" {
		t.Fatalf("MinutePrice = %q, want 12.35", anchor.MinutePrice)
	}
	if anchor.MinuteAmount != "7890.12" {
		t.Fatalf("MinuteAmount = %q, want 7890.12", anchor.MinuteAmount)
	}
	if anchor.MinuteVolume != "456.00" {
		t.Fatalf("MinuteVolume = %q, want 456.00", anchor.MinuteVolume)
	}
	if anchor.PriceAnchorSource != "minute_bar" {
		t.Fatalf("PriceAnchorSource = %q, want minute_bar", anchor.PriceAnchorSource)
	}
	if anchor.CurrentPrice != "12.36" {
		t.Fatalf("CurrentPrice = %q, want 12.36", anchor.CurrentPrice)
	}
}

func TestResolveMarketSummaryPriceAnchor_PrefersCallAuctionDuringAuctionWindow(t *testing.T) {
	auctionData := []diemengCallAuctionItem{
		{
			StockCode:      "300308.SZ",
			TradeTime:      "2026-03-11 09:24:00",
			CurrentPrice:   12.18,
			Open:           12.18,
			High:           12.18,
			Low:            12.18,
			PreClose:       12.05,
			Volume:         5200,
			Amount:         63336,
			CommitteeRatio: 23.5,
			VolumeRatio:    1.8,
			BidPrice:       []float64{12.18, 12.17},
			AskPrice:       []float64{12.19, 12.20},
			BidVol:         []float64{1500, 1200},
			AskVol:         []float64{800, 600},
		},
	}
	minuteData := &[]MinuteData{
		{Time: "09:31", Price: 12.30, Volume: 200, Amount: 2460},
	}
	stockData := &[]StockInfo{{
		Date:  "2026-03-11",
		Time:  "09:24:58",
		Price: "12.18",
	}}

	anchor := resolveMarketSummaryPriceAnchorAt(auctionData, minuteData, "20260311", stockData, time.Date(2026, 3, 11, 9, 24, 30, 0, cnLocation()))
	if anchor.PriceAnchorSource != "call_auction" {
		t.Fatalf("PriceAnchorSource = %q, want call_auction", anchor.PriceAnchorSource)
	}
	if anchor.MinutePrice != "12.18" {
		t.Fatalf("MinutePrice = %q, want 12.18", anchor.MinutePrice)
	}
	if anchor.Auction.Price != "12.18" {
		t.Fatalf("Auction.Price = %q, want 12.18", anchor.Auction.Price)
	}
	if anchor.Auction.CommitteeRatio != "23.50" {
		t.Fatalf("Auction.CommitteeRatio = %q, want 23.50", anchor.Auction.CommitteeRatio)
	}
	if len(anchor.Auction.BidPrice) != 2 || anchor.Auction.BidPrice[0] != "12.18" {
		t.Fatalf("unexpected auction bid prices: %#v", anchor.Auction.BidPrice)
	}
}

func TestResolveMarketSummaryPriceAnchor_PrefersMinuteAfterOpenEvenIfAuctionExists(t *testing.T) {
	auctionData := []diemengCallAuctionItem{
		{
			StockCode:    "300308.SZ",
			TradeTime:    "2026-03-11 09:25:00",
			CurrentPrice: 12.18,
			Volume:       5200,
			Amount:       63336,
		},
	}
	minuteData := &[]MinuteData{
		{Time: "09:31", Price: 12.30, Volume: 200, Amount: 2460},
	}
	stockData := &[]StockInfo{{
		Date:  "2026-03-11",
		Time:  "09:31:15",
		Price: "12.31",
	}}

	anchor := resolveMarketSummaryPriceAnchorAt(auctionData, minuteData, "20260311", stockData, time.Date(2026, 3, 11, 9, 31, 30, 0, cnLocation()))
	if anchor.PriceAnchorSource != "minute_bar" {
		t.Fatalf("PriceAnchorSource = %q, want minute_bar", anchor.PriceAnchorSource)
	}
	if anchor.MinutePrice != "12.30" {
		t.Fatalf("MinutePrice = %q, want 12.30", anchor.MinutePrice)
	}
	if anchor.Auction.Price != "12.18" {
		t.Fatalf("Auction.Price = %q, want 12.18", anchor.Auction.Price)
	}
}

func TestResolveMarketSummaryPriceAnchor_FallbackToRealtimeQuote(t *testing.T) {
	stockData := &[]StockInfo{{
		Date:  "2026-03-11",
		Time:  "10:16:09",
		Price: "18.88",
	}}

	anchor := resolveMarketSummaryPriceAnchorAt(nil, nil, "", stockData, time.Date(2026, 3, 11, 10, 16, 9, 0, cnLocation()))
	if anchor.MinutePrice != "18.88" {
		t.Fatalf("MinutePrice = %q, want 18.88", anchor.MinutePrice)
	}
	if anchor.PriceAnchorSource != "realtime_quote_fallback" {
		t.Fatalf("PriceAnchorSource = %q, want realtime_quote_fallback", anchor.PriceAnchorSource)
	}
	if anchor.MinuteDate != "2026-03-11" {
		t.Fatalf("MinuteDate = %q, want 2026-03-11", anchor.MinuteDate)
	}
	if anchor.MinuteTime != "10:16:09" {
		t.Fatalf("MinuteTime = %q, want 10:16:09", anchor.MinuteTime)
	}
	if anchor.MinuteAmount != "" || anchor.MinuteVolume != "" {
		t.Fatalf("expected empty minute amount/volume on fallback, got amount=%q volume=%q", anchor.MinuteAmount, anchor.MinuteVolume)
	}
}
