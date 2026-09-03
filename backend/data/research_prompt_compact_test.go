package data

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestCompactResearchDailyPromptKeepsNewestBarsAndReturns(t *testing.T) {
	rows := make([]KLineData, 0, 61)
	for index := 1; index <= 61; index++ {
		rows = append(rows, KLineData{Day: fmt.Sprintf("2026-08-%02d", index), Open: fmt.Sprint(index), High: fmt.Sprint(index + 1), Low: fmt.Sprint(index - 1), Close: fmt.Sprint(index), Volume: "100"})
	}
	content := compactResearchPromptValue("Sina日K sh600000", &rows)
	if !json.Valid([]byte(content)) || !strings.Contains(content, `"asOf":"2026-08-61"`) || !strings.Contains(content, `"2026-08-60"`) {
		t.Fatalf("compact daily content did not retain latest bars: %s", content)
	}
	var payload struct {
		Bars    []compactDailyBar  `json:"bars"`
		Returns map[string]float64 `json:"returns"`
	}
	if json.Unmarshal([]byte(content), &payload) != nil || len(payload.Bars) != 20 || payload.Bars[len(payload.Bars)-1][0] != "2026-08-42" || payload.Returns["60d"] == 0 {
		t.Fatalf("compact daily content retained oldest raw bars or omitted summaries: %s", content)
	}
}

func TestCompactResearchMinutePromptKeepsLatestThirtyOneAndWindows(t *testing.T) {
	rows := make([]MinuteData, 0, 61)
	start := time.Date(2026, 8, 18, 10, 0, 0, 0, shanghaiDataLocation())
	for index := 0; index <= 60; index++ {
		rows = append(rows, MinuteData{Time: start.Add(time.Duration(index) * time.Minute).Format("15:04"), Price: 10 + float64(index)/100, Volume: float64(100 + index), Amount: float64(1000 + index)})
	}
	content := compactResearchPromptValue("Tencent分钟K sh600000", map[string]any{"source": "20260818", "rows": &rows})
	if !json.Valid([]byte(content)) || !strings.Contains(content, `"asOf":"2026-08-18T11:00:00+08:00"`) || !strings.Contains(content, `"minutes":60`) {
		t.Fatalf("compact minute content omitted latest/windows: %s", content)
	}
	var payload struct {
		Bars []compactMinuteBar `json:"bars"`
	}
	if json.Unmarshal([]byte(content), &payload) != nil || len(payload.Bars) != 31 || payload.Bars[len(payload.Bars)-1][0] != "10:30" {
		t.Fatalf("compact minute raw bars were not newest-first bounded: %s", content)
	}
}

func TestCompactResearchOptionalListsKeepNewestWholeRecords(t *testing.T) {
	rows := make([]map[string]any, 0, 8)
	for index := 1; index <= 8; index++ {
		rows = append(rows, map[string]any{"notice_date": fmt.Sprintf("2026-08-%02d", index), "title": fmt.Sprintf("notice-%d", index), "content": fmt.Sprintf("body-%d", index), "ignored_payload": strings.Repeat("x", 1000)})
	}
	content := compactResearchPromptValue("东方财富公告 sh600000", rows)
	if !json.Valid([]byte(content)) || !strings.Contains(content, "notice-8") || !strings.Contains(content, "body-8") || strings.Contains(content, "notice-1") {
		t.Fatalf("optional compaction did not keep latest records: %s", content)
	}
}

func TestCompactResearchRealtimeQuoteKeepsRequiredFields(t *testing.T) {
	rows := []StockInfo{{Date: "2026-09-03", Time: "14:20:01", Code: "sh600000", Name: "Alpha", Price: "10.10", PreClose: "10.00", High: "10.20", Low: "9.90", Volume: "10000", Amount: "100500"}}
	content := compactResearchPromptValue("Sina/Tencent实时行情 sh600000", &rows)
	if !json.Valid([]byte(content)) || !strings.Contains(content, `"price":10.1`) || !strings.Contains(content, `"time":"14:20:01"`) || strings.Contains(content, "DeletedAt") {
		t.Fatalf("realtime quote was not compacted safely: %s", content)
	}
}

func TestResearchDocumentRejectsStaleInternalRealtimeTimestamp(t *testing.T) {
	now := time.Date(2026, 9, 3, 14, 20, 0, 0, shanghaiDataLocation())
	rows := []StockInfo{{Date: "2026-09-03", Time: "14:15:00", Code: "sh600000", Name: "Alpha", Price: "10.10"}}
	document := researchDocument("Sina/Tencent实时行情 sh600000", "stock", now, &rows)
	if document.Error == "" || !json.Valid([]byte(document.Content)) {
		t.Fatalf("stale internal quote was not rejected structurally: %+v", document)
	}
	rows[0].Time = "14:19:30"
	document = researchDocument("Sina/Tencent实时行情 sh600000", "stock", now, &rows)
	if document.Error != "" || !strings.Contains(document.Content, `"asOf":"2026-09-03T14:19:30+08:00"`) {
		t.Fatalf("fresh internal quote was rejected: %+v", document)
	}
}
