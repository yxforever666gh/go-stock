package data

import (
	"strings"
	"testing"
	"time"

	"go-stock/backend/models"
	"go-stock/backend/themes"
)

func TestExistingThemeSourceConvertersPreserveShapeAndProvenance(t *testing.T) {
	observedAt := time.Date(2026, 8, 28, 11, 0, 0, 0, time.UTC)
	publishedAt := observedAt.Add(-time.Minute)

	hotTopics := AdaptHotTopics([]any{map[string]any{
		"TopicName": "商业航天", "TopicDesc": "发射计划", "hotValue": 88,
	}}, observedAt)
	if len(hotTopics) != 1 || hotTopics[0].Kind != themes.ThemeSignalHotTopic || hotTopics[0].SourceName != "东方财富热门话题" {
		t.Fatalf("hot topic conversion failed: %+v", hotTopics)
	}

	hotEvents := AdaptXueqiuHotEvents([]models.HotEvent{{Tag: "固态电池", Content: "行业进展", Hot: 99}}, observedAt)
	if len(hotEvents) != 1 || hotEvents[0].SourceName != "雪球热点事件" || strings.Contains(hotEvents[0].SourceName, "东方财富") {
		t.Fatalf("Xueqiu event was mislabelled: %+v", hotEvents)
	}

	telegraphs := AdaptTelegraphs([]*models.Telegraph{{
		DataTime: &publishedAt, Title: "订单公告", Content: "订单确认", SubjectTags: []string{"算力"},
		StocksTags: []string{"sh600000"}, Source: "财联社电报", Url: "https://example.test/news/1",
	}}, observedAt)
	if len(telegraphs) != 1 || telegraphs[0].ThemeName != "算力" || telegraphs[0].SourceName != "财联社电报" || telegraphs[0].Securities[0].Market != "SH" {
		t.Fatalf("telegraph conversion failed: %+v", telegraphs)
	}

	announcements := AdaptAnnouncements([]any{map[string]any{
		"title": "重大合同公告", "themeName": "电网设备", "notice_date": publishedAt.Format(time.RFC3339),
		"columns": []any{map[string]any{"stock_code": "002001", "short_name": "测试公司"}},
	}}, observedAt)
	if len(announcements) != 1 || announcements[0].Kind != themes.ThemeSignalAnnouncement || announcements[0].Securities[0].Code != "002001" || announcements[0].SourceCredibilityScore != 90 {
		t.Fatalf("announcement conversion failed: %+v", announcements)
	}

	concepts := AdaptConceptInfo([]models.StockConceptInfo{{
		SECURITYCODE: "600000", SECURITYNAMEABBR: "浦发银行", NEWBOARDCODE: "BK001",
		BOARDNAME: "金融科技", SELECTEDBOARDREASON: "入选理由", BOARDRANK: 2,
	}}, observedAt)
	if len(concepts) != 1 || concepts[0].ThemeName != "金融科技" || concepts[0].Rank != 2 {
		t.Fatalf("concept conversion failed: %+v", concepts)
	}

	flows := AdaptConceptFundFlows(ConceptFundFlowSnapshot{
		Rows: []FundFlowRow{{Code: "BK002", Name: "机器人", NetAmount: 123}}, SourceName: "sina",
		SourceRef: "https://example.test/fund-flow", AsOf: publishedAt,
	}, observedAt)
	if len(flows) != 1 || flows[0].Kind != themes.ThemeSignalFundFlow || flows[0].SourceName != "新浪概念资金流" {
		t.Fatalf("fund-flow provenance failed: %+v", flows)
	}
}
