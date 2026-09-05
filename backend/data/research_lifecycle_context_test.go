package data

import (
	"math"
	"testing"
	"time"

	"go-stock/backend/research"
)

func TestSummarizeLifecycleMinutesBuildsFifteenThirtySixtyMinuteWindows(t *testing.T) {
	location := shanghaiDataLocation()
	now := time.Date(2026, 8, 18, 10, 30, 0, 0, location)
	rows := make([]MinuteData, 0, 61)
	for index := 0; index <= 60; index++ {
		at := now.Add(-time.Duration(60-index) * time.Minute)
		rows = append(rows, MinuteData{Time: at.Format("15:04"), Price: 10 + float64(index)/100, Volume: 100, Amount: (10 + float64(index)/100) * 100})
	}
	summary, err := summarizeLifecycleMinutes(now, "20260818", rows)
	if err != nil {
		t.Fatal(err)
	}
	if summary.TotalBars != 61 || len(summary.Windows) != 3 || summary.LatestPrice != 10.6 {
		t.Fatalf("summary=%+v", summary)
	}
	if summary.Windows[0].Minutes != 15 || summary.Windows[0].Bars != 16 || math.Abs(summary.Windows[0].ReturnRate-((10.6-10.45)/10.45)) > 1e-9 {
		t.Fatalf("15-minute window=%+v", summary.Windows[0])
	}
}

func TestSummarizeLifecycleMinutesAveragePriceFromShareVolume(t *testing.T) {
	window := summarizeLifecycleMinuteWindowForAveragePriceTest(t, []MinuteData{
		{Time: "10:29", Price: 10, Volume: 100, Amount: 1000},
		{Time: "10:30", Price: 12, Volume: 400, Amount: 4600},
	})
	assertLifecycleMinuteAveragePrice(t, window.AveragePrice, window.AveragePriceMethod, 11.5, "amount_divided_by_share_volume")
}

func TestSummarizeLifecycleMinutesAveragePriceFromLotVolume(t *testing.T) {
	window := summarizeLifecycleMinuteWindowForAveragePriceTest(t, []MinuteData{
		{Time: "10:29", Price: 10, Volume: 100, Amount: 100000},
		{Time: "10:30", Price: 12, Volume: 400, Amount: 460000},
	})
	assertLifecycleMinuteAveragePrice(t, window.AveragePrice, window.AveragePriceMethod, 11.5, "amount_divided_by_lot_volume_times_100")
}

func TestSummarizeLifecycleMinutesAveragePriceFallsBackForAbnormalAmount(t *testing.T) {
	window := summarizeLifecycleMinuteWindowForAveragePriceTest(t, []MinuteData{
		{Time: "10:29", Price: 10, Volume: 100, Amount: 100},
		{Time: "10:30", Price: 12, Volume: 400, Amount: 200},
	})
	assertLifecycleMinuteAveragePrice(t, window.AveragePrice, window.AveragePriceMethod, 11.5, "volume_weighted_minute_price_proxy")
}

func TestSummarizeLifecycleMinutesAveragePriceFallsBackForMissingAmount(t *testing.T) {
	window := summarizeLifecycleMinuteWindowForAveragePriceTest(t, []MinuteData{
		{Time: "10:29", Price: 10, Volume: 100},
		{Time: "10:30", Price: 12, Volume: 400},
	})
	assertLifecycleMinuteAveragePrice(t, window.AveragePrice, window.AveragePriceMethod, 11.5, "volume_weighted_minute_price_proxy")
}

func TestSummarizeLifecycleMinutesConvertsCumulativeTurnoverToDeltas(t *testing.T) {
	location := shanghaiDataLocation()
	now := time.Date(2026, 8, 18, 10, 30, 0, 0, location)
	rows := make([]MinuteData, 0, 61)
	for index := 0; index <= 60; index++ {
		volume := float64((index + 1) * 100)
		rows = append(rows, MinuteData{Time: now.Add(-time.Duration(60-index) * time.Minute).Format("15:04"), Price: 10, Volume: volume, Amount: volume * 10 * 100})
	}
	summary, err := summarizeLifecycleMinutes(now, "20260818", rows)
	if err != nil {
		t.Fatal(err)
	}
	window := summary.Windows[0]
	if window.Volume != 1600 || window.Amount != 1600000 || math.Abs(window.AveragePrice-10) > 1e-9 || window.AveragePriceMethod != "amount_divided_by_lot_volume_times_100" {
		t.Fatalf("cumulative turnover was not normalized: %+v", window)
	}
}

func summarizeLifecycleMinuteWindowForAveragePriceTest(t *testing.T, rows []MinuteData) research.MinuteWindowSummary {
	t.Helper()
	location := shanghaiDataLocation()
	now := time.Date(2026, 8, 18, 10, 30, 0, 0, location)
	summary, err := summarizeLifecycleMinutes(now, "20260818", rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Windows) == 0 {
		t.Fatal("minute summary did not contain a window")
	}
	return summary.Windows[0]
}

func assertLifecycleMinuteAveragePrice(t *testing.T, got float64, gotMethod string, want float64, wantMethod string) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 || gotMethod != wantMethod {
		t.Fatalf("averagePrice=%v method=%q, want %v %q", got, gotMethod, want, wantMethod)
	}
}

func TestSummarizeLifecycleMinutesRejectsStaleTradingData(t *testing.T) {
	location := shanghaiDataLocation()
	now := time.Date(2026, 8, 18, 10, 30, 0, 0, location)
	_, err := summarizeLifecycleMinutes(now, "20260818", []MinuteData{{Time: "10:00", Price: 10}})
	if err == nil {
		t.Fatal("stale minute data was accepted")
	}
}

func TestSummarizeLifecycleMinutesCarriesWindowsAcrossLunchBreak(t *testing.T) {
	location := shanghaiDataLocation()
	now := time.Date(2026, 8, 18, 13, 4, 0, 0, location)
	rows := make([]MinuteData, 0, 126)
	for at := time.Date(2026, 8, 18, 9, 30, 0, 0, location); !at.After(time.Date(2026, 8, 18, 11, 30, 0, 0, location)); at = at.Add(time.Minute) {
		rows = append(rows, MinuteData{Time: at.Format("15:04"), Price: 10, Volume: 100})
	}
	for at := time.Date(2026, 8, 18, 13, 0, 0, 0, location); !at.After(now); at = at.Add(time.Minute) {
		rows = append(rows, MinuteData{Time: at.Format("15:04"), Price: 10.1, Volume: 100})
	}
	summary, err := summarizeLifecycleMinutes(now, "20260818", rows)
	if err != nil {
		t.Fatal(err)
	}
	if got := []int{summary.Windows[0].Bars, summary.Windows[1].Bars, summary.Windows[2].Bars}; got[0] != 16 || got[1] != 31 || got[2] != 61 {
		t.Fatalf("window bars=%v", got)
	}
}

func TestLifecycleConditionKeywordRouting(t *testing.T) {
	if !containsLifecycleKeyword("板块放量且订单落地", "板块", "行业") {
		t.Fatal("sector route was not selected")
	}
	if !containsLifecycleKeyword("板块放量且订单落地", "资金", "放量") {
		t.Fatal("money route was not selected")
	}
	if !containsLifecycleKeyword("板块放量且订单落地", "公告", "订单") {
		t.Fatal("announcement route was not selected")
	}
	if containsLifecycleKeyword("价格站稳均线", "公告", "订单") {
		t.Fatal("unrelated route was selected")
	}
}

func TestLifecycleSourceFingerprintDeduplicatesOptionalContent(t *testing.T) {
	now := time.Date(2026, 8, 18, 10, 0, 0, 0, shanghaiDataLocation())
	first := newLifecycleSource("source-1", "增量新闻", "news", now, map[string]any{"title": "事件"}, nil, true, nil)
	known := map[string]struct{}{first.Fingerprint: {}}
	second := newLifecycleSource("source-2", "增量新闻", "news", now.Add(15*time.Minute), map[string]any{"title": "事件"}, nil, true, known)
	if second.Status != "unchanged" || second.Content == first.Content {
		t.Fatalf("second=%+v first=%+v", second, first)
	}
}

func TestLifecycleSourceMarksNestedEmptyPayloadAsEmpty(t *testing.T) {
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, shanghaiDataLocation())
	source := newLifecycleSource("source-empty", "全球指数", "market", now, map[string]any{"data": []any{}}, nil, false, nil)
	if source.Status != "empty" {
		t.Fatalf("source=%+v", source)
	}
}
