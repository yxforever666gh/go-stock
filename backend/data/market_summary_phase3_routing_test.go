package data

import (
	"path/filepath"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
)

func TestResolveMarketSummaryTimeWindowAt(t *testing.T) {
	loc := cnLocation()

	morning := resolveMarketSummaryTimeWindowAt(time.Date(2026, 4, 9, 9, 32, 0, 0, loc))
	if morning.Slot != marketSummaryRunSlotMorning {
		t.Fatalf("morning slot = %s, want %s", morning.Slot, marketSummaryRunSlotMorning)
	}
	if morning.Start.Format(time.DateTime) != "2026-04-08 15:00:00" {
		t.Fatalf("morning start = %s, want 2026-04-08 15:00:00", morning.Start.Format(time.DateTime))
	}
	if morning.End.Format(time.DateTime) != "2026-04-09 09:32:00" {
		t.Fatalf("morning end = %s, want 2026-04-09 09:32:00", morning.End.Format(time.DateTime))
	}

	midday := resolveMarketSummaryTimeWindowAt(time.Date(2026, 4, 9, 11, 32, 0, 0, loc))
	if midday.Slot != marketSummaryRunSlotMidday {
		t.Fatalf("midday slot = %s, want %s", midday.Slot, marketSummaryRunSlotMidday)
	}
	if midday.Start.Format(time.DateTime) != "2026-04-09 09:30:00" {
		t.Fatalf("midday start = %s, want 2026-04-09 09:30:00", midday.Start.Format(time.DateTime))
	}
	if midday.End.Format(time.DateTime) != "2026-04-09 11:32:00" {
		t.Fatalf("midday end = %s, want 2026-04-09 11:32:00", midday.End.Format(time.DateTime))
	}

	evening := resolveMarketSummaryTimeWindowAt(time.Date(2026, 4, 9, 14, 32, 0, 0, loc))
	if evening.Slot != marketSummaryRunSlotEvening {
		t.Fatalf("evening slot = %s, want %s", evening.Slot, marketSummaryRunSlotEvening)
	}
	if evening.Start.Format(time.DateTime) != "2026-04-09 11:30:00" {
		t.Fatalf("evening start = %s, want 2026-04-09 11:30:00", evening.Start.Format(time.DateTime))
	}
	if evening.End.Format(time.DateTime) != "2026-04-09 14:32:00" {
		t.Fatalf("evening end = %s, want 2026-04-09 14:32:00", evening.End.Format(time.DateTime))
	}
}

func TestShouldIncludeMarketSummaryTimeText(t *testing.T) {
	loc := cnLocation()
	window := marketSummaryTimeWindow{
		Slot:  marketSummaryRunSlotMidday,
		Start: time.Date(2026, 4, 9, 9, 30, 0, 0, loc),
		End:   time.Date(2026, 4, 9, 11, 32, 0, 0, loc),
	}

	if !shouldIncludeMarketSummaryTimeText("2026-04-09 10:15:00", window, false) {
		t.Fatal("expected in-window news time to be included")
	}
	if shouldIncludeMarketSummaryTimeText("2026-04-09 13:05:00", window, false) {
		t.Fatal("expected out-of-window news time to be excluded")
	}
	if !shouldIncludeMarketSummaryTimeText("2026-04-09", window, true) {
		t.Fatal("expected same-day date-only item to be included")
	}
	if shouldIncludeMarketSummaryTimeText("2026-04-08", window, true) {
		t.Fatal("expected previous-day date-only item to be excluded")
	}
}

func TestLoadSameDayMarketSummaryExcludedStocks(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "market-summary-routing-test.db"))
	if err := db.Dao.AutoMigrate(&models.AiRecommendStocks{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	loc := cnLocation()
	firstTime := time.Date(2026, 4, 9, 9, 32, 0, 0, loc)
	secondTime := time.Date(2026, 4, 9, 11, 32, 0, 0, loc)
	prevDayTime := time.Date(2026, 4, 8, 14, 32, 0, 0, loc)

	rows := []models.AiRecommendStocks{
		{
			DataTime:               &firstTime,
			StockCode:              "300308.SZ",
			StockName:              "中际旭创",
			SummaryVersion:         marketSummaryPhase3Version,
			ActivationRuleSource:   "market_summary",
			RecommendCategory:      "right_confirm",
			RecommendStatus:        "valid",
			ActivationStatus:       "waiting",
			ActivationRuleVersion:  "v1",
			ActivationRuleJSON:     `{"trigger":"x"}`,
			RecommendBuyPrice:      "165-169",
			RecommendStopLossPrice: "159",
		},
		{
			DataTime:               &secondTime,
			StockCode:              "300308.SZ",
			StockName:              "中际旭创",
			SummaryVersion:         marketSummaryPhase3Version,
			ActivationRuleSource:   "market_summary_embedded",
			RecommendCategory:      "right_confirm",
			RecommendStatus:        "valid",
			ActivationStatus:       "waiting",
			ActivationRuleVersion:  "v1",
			ActivationRuleJSON:     `{"trigger":"x"}`,
			RecommendBuyPrice:      "166-170",
			RecommendStopLossPrice: "160",
		},
		{
			DataTime:               &secondTime,
			StockCode:              "300502.SZ",
			StockName:              "新易盛",
			SummaryVersion:         "legacy",
			ActivationRuleSource:   "market_summary_embedded",
			RecommendCategory:      "right_confirm",
			RecommendStatus:        "valid",
			ActivationStatus:       "waiting",
			ActivationRuleVersion:  "v1",
			ActivationRuleJSON:     `{"trigger":"y"}`,
			RecommendBuyPrice:      "91-93",
			RecommendStopLossPrice: "88",
		},
		{
			DataTime:               &secondTime,
			StockCode:              "002371.SZ",
			StockName:              "北方华创",
			SummaryVersion:         "legacy",
			ActivationRuleSource:   "manual",
			RecommendCategory:      "right_confirm",
			RecommendStatus:        "valid",
			ActivationStatus:       "waiting",
			ActivationRuleVersion:  "v1",
			ActivationRuleJSON:     `{"trigger":"z"}`,
			RecommendBuyPrice:      "430-438",
			RecommendStopLossPrice: "418",
		},
		{
			DataTime:               &prevDayTime,
			StockCode:              "688256.SH",
			StockName:              "寒武纪",
			SummaryVersion:         marketSummaryPhase3Version,
			ActivationRuleSource:   "market_summary",
			RecommendCategory:      "right_confirm",
			RecommendStatus:        "valid",
			ActivationStatus:       "waiting",
			ActivationRuleVersion:  "v1",
			ActivationRuleJSON:     `{"trigger":"w"}`,
			RecommendBuyPrice:      "650-665",
			RecommendStopLossPrice: "628",
		},
	}
	for _, row := range rows {
		if err := db.Dao.Create(&row).Error; err != nil {
			t.Fatalf("seed row failed: %v", err)
		}
	}

	excluded, index, err := loadSameDayMarketSummaryExcludedStocks(time.Date(2026, 4, 9, 14, 32, 0, 0, loc))
	if err != nil {
		t.Fatalf("loadSameDayMarketSummaryExcludedStocks failed: %v", err)
	}
	if len(excluded) != 2 {
		t.Fatalf("excluded len = %d, want 2", len(excluded))
	}
	if len(index) != 2 {
		t.Fatalf("index len = %d, want 2", len(index))
	}
	if excluded[0].StockCode != "300308.SZ" || excluded[0].FirstRecommendTime != "2026-04-09 09:32:00" {
		t.Fatalf("unexpected first excluded item: %+v", excluded[0])
	}
	if _, ok := index["300502.SZ"]; !ok {
		t.Fatalf("expected 300502.SZ in excluded index, got %+v", index)
	}
	if _, ok := index["002371.SZ"]; ok {
		t.Fatalf("did not expect manual source stock in excluded index, got %+v", index["002371.SZ"])
	}
}

func TestSelectMarketSummaryFinalCandidates(t *testing.T) {
	loc := cnLocation()
	window := marketSummaryTimeWindow{
		Slot:  marketSummaryRunSlotEvening,
		Start: time.Date(2026, 4, 9, 11, 30, 0, 0, loc),
		End:   time.Date(2026, 4, 9, 14, 32, 0, 0, loc),
	}
	logState := newMarketSummaryRouteLog()
	verified := []marketSummaryVerifiedCandidate{
		{
			StockName:         "中际旭创",
			StockCode:         "300308.SZ",
			MinutePrice:       "138.20",
			MinuteAmount:      "8650.00",
			CurrentPrice:      "138.30",
			PriceAnchorSource: "minute_bar",
			TechnicalSnapshot: "午后资金回流，5分钟量能放大",
			TechnicalMetrics: marketSummaryTechnicalMetrics{
				PriceAboveMa5:      true,
				PriceAboveMa10:     true,
				Breakout3dHigh:     true,
				MinuteVolumeVsAvg5: "1.80",
			},
			PositiveSignals: []string{"量价共振", "主线强化"},
			EvidenceSources: []aiEvidenceReference{
				{Type: "一级披露", TrustLevel: "high", PublishedAt: "2026-04-09 13:10:00"},
				{Type: "市场资讯", TrustLevel: "medium", PublishedAt: "2026-04-09 13:18:00"},
				{Type: "技术/资金/形态", PublishedAt: "2026-04-09 14:28:00"},
			},
		},
		{
			StockName:         "新易盛",
			StockCode:         "300502.SZ",
			MinutePrice:       "92.30",
			MinuteAmount:      "5200.00",
			CurrentPrice:      "92.35",
			PriceAnchorSource: "minute_bar",
			TechnicalSnapshot: "板块共振上行",
			TechnicalMetrics: marketSummaryTechnicalMetrics{
				PriceAboveMa5:      true,
				Breakout3dHigh:     true,
				MinuteVolumeVsAvg5: "1.45",
			},
			PositiveSignals: []string{"板块共振"},
			EvidenceSources: []aiEvidenceReference{
				{Type: "市场资讯", TrustLevel: "high", PublishedAt: "2026-04-09 14:20:00"},
				{Type: "技术/资金/形态", PublishedAt: "2026-04-09 14:26:00"},
			},
		},
		{
			StockName:         "沪电股份",
			StockCode:         "002463.SZ",
			MinutePrice:       "34.60",
			CurrentPrice:      "34.62",
			PriceAnchorSource: "minute_bar",
			TechnicalSnapshot: "筹码稳定",
			EvidenceSources: []aiEvidenceReference{
				{Type: "行业研报", TrustLevel: "medium", PublishedAt: "2026-04-09 12:20:00"},
				{Type: "技术/资金/形态", PublishedAt: "2026-04-09 14:10:00"},
			},
		},
		{
			StockName:         "寒武纪",
			StockCode:         "688256.SH",
			MinutePrice:       "662.00",
			CurrentPrice:      "662.50",
			PriceAnchorSource: "minute_bar",
			EvidenceSources: []aiEvidenceReference{
				{Type: "市场资讯", TrustLevel: "medium", PublishedAt: "2026-04-08 14:10:00"},
			},
			NegativeSignals: []string{"缺少当窗新证据"},
		},
	}
	excluded := map[string]marketSummaryExcludedStock{
		"300308.SZ": {StockCode: "300308.SZ", StockName: "中际旭创", FirstRecommendTime: "2026-04-09 09:32:00"},
	}

	selected := selectMarketSummaryFinalCandidates(verified, excluded, window, logState, 3)
	if len(selected) != 1 {
		t.Fatalf("selected len = %d, want 1", len(selected))
	}
	if selected[0].StockCode != "300502.SZ" {
		t.Fatalf("selected[0] = %s, want 300502.SZ", selected[0].StockCode)
	}
	for _, item := range selected {
		if item.StockCode == "300308.SZ" {
			t.Fatalf("excluded stock was still selected: %+v", item)
		}
	}
	if !containsAll(stringsJoin(logState.DroppedCandidates, "\n"), []string{"同日已推荐排除:中际旭创(300308.SZ)"}) {
		t.Fatalf("expected exclusion note in dropped candidates, got %+v", logState.DroppedCandidates)
	}
	if !containsAll(stringsJoin(logState.DroppedCandidates, "\n"), []string{"源头质量门槛未通过:沪电股份(002463.SZ)", "源头质量门槛未通过:寒武纪(688256.SH)"}) {
		t.Fatalf("expected quality gate notes in dropped candidates, got %+v", logState.DroppedCandidates)
	}
}

func stringsJoin(items []string, sep string) string {
	if len(items) == 0 {
		return ""
	}
	result := items[0]
	for i := 1; i < len(items); i++ {
		result += sep + items[i]
	}
	return result
}
