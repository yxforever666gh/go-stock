package data

import (
	"path/filepath"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
)

func TestMarketSummaryV150CandidateDailySeriesSwitchesAtMarketClose(t *testing.T) {
	loc := cnLocation()
	tradeDay := time.Date(2026, 8, 5, 0, 0, 0, 0, loc)
	intraday := time.Date(2026, 8, 5, 14, 59, 0, 0, loc)
	afterClose := time.Date(2026, 8, 5, 15, 1, 0, 0, loc)
	bars := marketSummaryV150DailyCompletionFixture(tradeDay, 66)
	previousDay := normalizeDailyTradeDate(bars[len(bars)-2].TradeDate)

	item := marketSummaryIndicatorCandidate{StockName: "test", StockCode: "000001.SZ", BkName: "software", Metrics: map[string]string{}}
	basic := StockBasic{TsCode: item.StockCode, Name: item.StockName, Industry: item.BkName, ListStatus: "L", ListDate: "20200101"}
	intradayQuote := StockInfo{Date: tradeDay.Format(time.DateOnly), Time: "14:59:00", Price: "12", PreClose: "10", Open: "10", Amount: "200000000", Volume: "20000000"}
	afterCloseQuote := intradayQuote
	afterCloseQuote.Time = "15:00:00"
	intradaySource := MarketSummaryV150DailyDataSource{AdjustmentSource: "tencent_qfq", LatestTradeDate: previousDay.Format(time.DateOnly), AdjustmentFactor: 1, Complete: true}
	afterCloseSource := MarketSummaryV150DailyDataSource{AdjustmentSource: "tencent_qfq", LatestTradeDate: tradeDay.Format(time.DateOnly), AdjustmentFactor: 1, Complete: true}

	intradayCandidate, intradayWarnings, _, _ := buildMarketSummaryV150Candidate(item, basic, intradayQuote, bars, marketSummaryDiscoveryInput{}, intraday, intradaySource)
	if !intradayCandidate.HasDailyData {
		t.Fatalf("intraday candidate rejected completed prior-day series: warnings=%v candidate=%+v", intradayWarnings, intradayCandidate)
	}
	if got := marketSummaryV150RequiredLatestDailyBar(intraday); !got.Equal(previousDay) {
		t.Fatalf("intraday required daily bar=%s, want %s", got.Format(time.DateOnly), previousDay.Format(time.DateOnly))
	}

	afterCloseCandidate, afterCloseWarnings, _, _ := buildMarketSummaryV150Candidate(item, basic, afterCloseQuote, bars, marketSummaryDiscoveryInput{}, afterClose, afterCloseSource)
	if !afterCloseCandidate.HasDailyData {
		t.Fatalf("post-close candidate rejected final current-day series: warnings=%v candidate=%+v", afterCloseWarnings, afterCloseCandidate)
	}
	if got := marketSummaryV150RequiredLatestDailyBar(afterClose); !got.Equal(tradeDay) {
		t.Fatalf("post-close required daily bar=%s, want %s", got.Format(time.DateOnly), tradeDay.Format(time.DateOnly))
	}
	if afterCloseCandidate.MA20 <= intradayCandidate.MA20 {
		t.Fatalf("post-close MA20=%.4f did not consume current close; intraday MA20=%.4f", afterCloseCandidate.MA20, intradayCandidate.MA20)
	}
}

func TestMarketSummaryV150BenchmarkDailySeriesSwitchesOnlyAfterFinalCloseRefresh(t *testing.T) {
	initDatabaseForTest(t, filepath.Join(t.TempDir(), "v150-benchmark-daily-completion.db"))
	if err := db.Dao.AutoMigrate(&models.AiRecommendDailyBar{}); err != nil {
		t.Fatalf("migrate daily cache: %v", err)
	}

	loc := cnLocation()
	tradeDay := time.Date(2026, 8, 5, 0, 0, 0, 0, loc)
	intraday := time.Date(2026, 8, 5, 14, 59, 0, 0, loc)
	afterClose := time.Date(2026, 8, 5, 15, 1, 0, 0, loc)
	bars := marketSummaryV150DailyCompletionFixture(tradeDay, 66)
	previousDay := normalizeDailyTradeDate(bars[len(bars)-2].TradeDate)
	rows := make([]models.AiRecommendDailyBar, 0, len(bars))
	for _, bar := range bars {
		day := normalizeDailyTradeDate(bar.TradeDate)
		availableAt := day.Add(15*time.Hour + time.Minute)
		if day.Equal(tradeDay) {
			// Simulate the legacy forming row that existed before the close.
			availableAt = day.Add(14*time.Hour + 30*time.Minute)
		}
		rows = append(rows, models.AiRecommendDailyBar{
			StockCode: defaultBenchmarkModelCode, TradeDate: day,
			Open: bar.Open, High: bar.High, Low: bar.Low, Close: bar.Close, Volume: bar.Volume, Amount: bar.Amount,
			Source: "tencent_qfq", CreatedAt: availableAt, UpdatedAt: availableAt,
		})
	}
	if err := db.Dao.Create(&rows).Error; err != nil {
		t.Fatalf("seed benchmark daily cache: %v", err)
	}

	previousLoader := loadMarketSummaryV150DailyBarsWithCache
	loadMarketSummaryV150DailyBarsWithCache = func(string, string, time.Time, time.Time, int64) ([]dailyBar, error) {
		return append([]dailyBar(nil), bars...), nil
	}
	t.Cleanup(func() { loadMarketSummaryV150DailyBarsWithCache = previousLoader })

	intradayBenchmark, intradaySource := loadMarketSummaryV150Benchmark(intraday)
	if intradayBenchmark.Stale || !intradayBenchmark.DataPresent || intradaySource.LatestTradeDate != previousDay.Format(time.DateOnly) {
		t.Fatalf("intraday benchmark=%+v source=%+v, want completed prior-day series", intradayBenchmark, intradaySource)
	}

	partialBenchmark, partialSource := loadMarketSummaryV150Benchmark(afterClose)
	if !partialBenchmark.Stale || partialSource.Complete {
		t.Fatalf("pre-close cached row was accepted after close: benchmark=%+v source=%+v", partialBenchmark, partialSource)
	}

	finalAvailableAt := tradeDay.Add(15*time.Hour + time.Minute)
	if err := db.Dao.Model(&models.AiRecommendDailyBar{}).
		Where("stock_code = ? AND trade_date = ?", defaultBenchmarkModelCode, tradeDay).
		Updates(map[string]any{"created_at": finalAvailableAt, "updated_at": finalAvailableAt}).Error; err != nil {
		t.Fatalf("mark current benchmark close refreshed: %v", err)
	}
	finalBenchmark, finalSource := loadMarketSummaryV150Benchmark(afterClose)
	if finalBenchmark.Stale || !finalBenchmark.DataPresent || !finalSource.Complete || finalSource.LatestTradeDate != tradeDay.Format(time.DateOnly) {
		t.Fatalf("final post-close benchmark=%+v source=%+v, want current completed series", finalBenchmark, finalSource)
	}
	if finalBenchmark.MA20 <= intradayBenchmark.MA20 || finalBenchmark.Close != bars[len(bars)-1].Close {
		t.Fatalf("post-close benchmark did not consume current close: intraday=%+v final=%+v", intradayBenchmark, finalBenchmark)
	}
}

func marketSummaryV150DailyCompletionFixture(lastDay time.Time, count int) []dailyBar {
	descending := make([]dailyBar, 0, count)
	for day := normalizeDailyTradeDate(lastDay); len(descending) < count; day = day.AddDate(0, 0, -1) {
		if !isCNOpenTradeDaySafe(day) {
			continue
		}
		closePrice := 10.0
		if day.Equal(normalizeDailyTradeDate(lastDay)) {
			closePrice = 12
		}
		descending = append(descending, dailyBar{
			TradeDate: day, Open: 10, High: closePrice + 0.2, Low: 9.8, Close: closePrice,
			Volume: 20_000_000, Amount: 200_000_000,
		})
	}
	result := make([]dailyBar, len(descending))
	for index := range descending {
		result[len(descending)-1-index] = descending[index]
	}
	return result
}
