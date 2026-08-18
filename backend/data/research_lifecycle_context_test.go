package data

import (
	"math"
	"testing"
	"time"
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
