package research2

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"go-stock/internal/researchevidence"
	"go-stock/internal/trading"
)

// ScoreEvidenceLink describes a proved relationship, never an inferred score.
// It is carried in the existing evidence JSON, not a database column.
type ScoreEvidenceLink struct {
	SourceID     string           `json:"sourceId"`
	Relation     string           `json:"relation"`
	Facts        []map[string]any `json:"facts,omitempty"`
	OmittedFacts int              `json:"omittedFacts,omitempty"`
}

type CandidateScoreEvidence struct {
	SectorState   string              `json:"sectorState"`
	Sector        []ScoreEvidenceLink `json:"sector"`
	CatalystState string              `json:"catalystState"`
	Catalyst      []ScoreEvidenceLink `json:"catalyst"`
	Unavailable   []string            `json:"unavailableSourceIds,omitempty"`
}

func scoreObjects(value any, visit func(map[string]any)) {
	switch value := value.(type) {
	case []any:
		for _, item := range value {
			scoreObjects(item, visit)
		}
	case map[string]any:
		visit(value)
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			scoreObjects(value[key], visit)
		}
	}
}

func scorePayload(document researchevidence.SourceDocument) any {
	var value any
	_ = json.Unmarshal([]byte(document.Content), &value)
	return value
}

func scoreText(value any) string { text, _ := value.(string); return strings.TrimSpace(text) }

func scoreNumberAvailable(value any) bool {
	var number float64
	switch value := value.(type) {
	case float64:
		number = value
	case string:
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return false
		}
		number = parsed
	default:
		return false
	}
	return !math.IsNaN(number) && !math.IsInf(number, 0)
}

func scoreCode(value any) string {
	text := strings.ToLower(scoreText(value))
	if len(text) == 9 && text[6] == '.' {
		text = text[7:] + text[:6]
	}
	if len(text) == 6 {
		if strings.HasPrefix(text, "6") {
			text = "sh" + text
		} else {
			text = "sz" + text
		}
	}
	code, _ := trading.NormalizeMainlandCode(text)
	return code
}

func scoreObjectOwns(row map[string]any, code string) bool {
	for _, key := range []string{"SECURITY_CODE", "SECUCODE", "stockCode", "stock_code", "code"} {
		if scoreCode(row[key]) == code {
			return true
		}
	}
	return scoreText(row["entityId"]) == "stock:"+code
}

// ScoreSourceFacts preserves provider field names and numeric units. In
// particular a leading stock in a sector ranking does not prove membership.
func ScoreSourceFacts(document researchevidence.SourceDocument) []map[string]any {
	if document.SourceName == "全球与国内指数" {
		rows := make([]map[string]any, 0)
		seen := map[string]bool{}
		scoreObjects(scorePayload(document), func(row map[string]any) {
			if scoreText(row["code"]) == "" || scoreText(row["qtcode"]) == "" || scoreText(row["name"]) == "" {
				return
			}
			quoteCode := scoreText(row["qtcode"])
			if seen[quoteCode] {
				return
			}
			seen[quoteCode] = true
			fact := map[string]any{}
			for _, key := range []string{"code", "qtcode", "name", "zxj", "zdf", "state"} {
				if value, exists := row[key]; exists {
					fact[key] = value
				}
			}
			rows = append(rows, fact)
		})
		// Keep mainland indices ahead of overseas rows before the summary's cap.
		sort.SliceStable(rows, func(i, j int) bool {
			mainland := func(row map[string]any) bool {
				code := scoreText(row["qtcode"])
				return strings.HasPrefix(code, "sh") || strings.HasPrefix(code, "sz")
			}
			return mainland(rows[i]) && !mainland(rows[j])
		})
		return rows
	}
	if scoreCatalystDocument(document) {
		facts := scoreCatalystFacts(document, "", time.Time{}, time.Time{})
		for _, fact := range facts {
			delete(fact, "relation")
		}
		return facts
	}
	if document.Category != "sector" && document.Category != "theme" && !strings.Contains(document.Content, "BOARD_NAME") {
		return nil
	}
	rows := make([]map[string]any, 0)
	scoreObjects(scorePayload(document), func(row map[string]any) {
		keys := []string{}
		switch {
		case row["BOARD_NAME"] != nil:
			keys = []string{"SECURITY_CODE", "SECUCODE", "BOARD_NAME", "NEW_BOARD_CODE", "BOARD_YIELD", "BOARD_RANK"}
		case row["bd_name"] != nil:
			keys = []string{"bd_code", "bd_name", "bd_zdf", "bd_zdf5", "bd_zdf20"}
		case row["netamount"] != nil && row["name"] != nil:
			keys = []string{"name", "category", "netamount", "avg_changeratio"}
		case row["netAmount"] != nil && row["name"] != nil:
			keys = []string{"code", "name", "netAmount", "changePct", "mainNetRatio", "superLargeNetAmount", "largeNetAmount", "mediumNetAmount", "smallNetAmount"}
		case row["themeId"] != nil:
			keys = []string{"themeId", "themeName", "snapshot", "event", "claim"}
		}
		if len(keys) == 0 {
			return
		}
		fact := map[string]any{}
		for _, key := range keys {
			if value, exists := row[key]; exists {
				if nested, ok := value.(map[string]any); ok {
					compact := map[string]any{}
					for _, field := range []string{"title", "summary", "eventAt", "firstAvailableAt", "publishedAt", "availableAt", "tradeDate", "frozenAt", "rank", "heatScore", "lifecycleStage", "status", "stance", "sourceName", "sourceCredibilityScore"} {
						if v, exists := nested[field]; exists {
							compact[field] = shortScoreFact(v)
						}
					}
					fact[key] = compact
				} else {
					fact[key] = shortScoreFact(value)
				}
			}
		}
		rows = append(rows, fact)
	})
	return rows
}

func shortScoreFact(value any) any {
	if text, ok := value.(string); ok {
		runes := []rune(text)
		if len(runes) > 160 {
			return string(runes[:160]) + "…"
		}
	}
	return value
}

func scoreSourceAvailable(document researchevidence.SourceDocument, freeze time.Time) bool {
	return document.Error == "" && document.AvailableAt != nil && !document.AvailableAt.After(freeze) && research2CitationPayloadUsable(document.Content) && !scorePayloadEmpty(scorePayload(document))
}

func scoreUnavailableState(document researchevidence.SourceDocument, freeze time.Time) string {
	if document.Error != "" {
		return "source_unavailable"
	}
	if document.AvailableAt == nil {
		return "source_time_unverified"
	}
	if document.AvailableAt.After(freeze) {
		return "source_after_freeze"
	}
	return "source_empty"
}

func scorePayloadEmpty(value any) bool {
	switch value := value.(type) {
	case nil:
		return true
	case []any:
		return len(value) == 0
	case map[string]any:
		if scoreText(value["status"]) == "empty" {
			return true
		}
		for _, key := range []string{"data", "items", "rows", "result"} {
			if nested, exists := value[key]; exists {
				return scorePayloadEmpty(nested)
			}
		}
		return len(value) == 0
	default:
		return false
	}
}

func scoreThemeMembers(documents []researchevidence.SourceDocument, code string, freeze time.Time) map[string]string {
	result := map[string]string{}
	for _, document := range documents {
		if document.Category != "theme" || !scoreSourceAvailable(document, freeze) {
			continue
		}
		payload, _ := scorePayload(document).(map[string]any)
		members, _ := payload["stockConstituents"].([]any)
		for _, member := range members {
			row, _ := member.(map[string]any)
			if scoreText(row["assetType"]) == "stock" && scoreObjectOwns(row, code) {
				result[scoreText(payload["themeId"])] = document.SourceID
			}
		}
	}
	delete(result, "")
	return result
}

func scoreCatalystDocument(document researchevidence.SourceDocument) bool {
	name := document.SourceName
	return document.Category == "catalyst" || strings.Contains(name, "公告") || strings.Contains(name, "新闻") || strings.Contains(name, "互动")
}

func scoreCatalystFacts(document researchevidence.SourceDocument, code string, cutoff, freshSince time.Time) []map[string]any {
	result := []map[string]any{}
	payload := scorePayload(document)
	if object, ok := payload.(map[string]any); ok && object["themeId"] != nil && object["event"] != nil {
		payload = object["event"]
	}
	scoreObjects(payload, func(row map[string]any) {
		if code != "" {
			for _, key := range []string{"stockCode", "stock_code", "SECURITY_CODE", "SECUCODE"} {
				if owner := scoreCode(row[key]); owner != "" && owner != code {
					return
				}
			}
		}
		title, summary := "", ""
		for _, key := range []string{"title", "Title", "mainContent"} {
			if value := scoreText(row[key]); value != "" {
				title = value
				break
			}
		}
		for _, key := range []string{"attachedContent", "summary", "content", "Content"} {
			if value := scoreText(row[key]); value != "" {
				summary = value
				break
			}
		}
		if title == "" && summary == "" {
			return
		}
		keys := []string{"eventAt", "dataTime", "notice_date", "NOTICE_DATE", "publishedAt", "publishTime", "PUBLISH_DATE"}
		if strings.Contains(document.SourceName, "互动") {
			keys = []string{"attachedPubDate"}
		}
		raw, field := "", ""
		for _, key := range keys {
			if value := scoreEventTimeText(row[key]); value != "" {
				raw, field = value, key
				break
			}
		}
		eventAt := parseScoreEventTime(raw)
		state := "time_unverified"
		if !eventAt.IsZero() && !freshSince.IsZero() && !cutoff.IsZero() {
			switch {
			case eventAt.After(cutoff):
				state = "after_snapshot"
			case eventAt.Before(freshSince):
				state = "old_background"
			default:
				state = "fresh_available"
			}
		}
		result = append(result, map[string]any{"title": shortScoreFact(title), "summary": shortScoreFact(summary), "eventTime": raw, "timeField": field, "relation": state})
	})
	return result
}

func scoreEventTimeText(value any) string {
	switch value := value.(type) {
	case string:
		return strings.TrimSpace(value)
	case json.Number:
		return value.String()
	case float64:
		if !math.IsNaN(value) && !math.IsInf(value, 0) && value == math.Trunc(value) {
			return strconv.FormatFloat(value, 'f', -1, 64)
		}
	}
	return ""
}

func parseScoreEventTime(raw string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02", "20060102"} {
		if at, err := time.ParseInLocation(layout, raw, shanghai()); err == nil {
			return at
		}
	}
	if len(raw) != 10 && len(raw) != 13 {
		return time.Time{}
	}
	for _, digit := range raw {
		if digit < '0' || digit > '9' {
			return time.Time{}
		}
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return time.Time{}
	}
	if len(raw) == 13 {
		return time.UnixMilli(value).In(shanghai())
	}
	return time.Unix(value, 0).In(shanghai())
}

// buildCandidateScoreEvidence binds only exact board names/codes and explicit
// theme constituents. FreshSince is the verified previous trading close; zero
// means freshness is not independently known, not that material is fresh.
func buildCandidateScoreEvidence(code string, documents []researchevidence.SourceDocument, cutoff, freeze, freshSince time.Time) CandidateScoreEvidence {
	result := CandidateScoreEvidence{SectorState: "membership_unverified", Sector: []ScoreEvidenceLink{}, CatalystState: "source_missing", Catalyst: []ScoreEvidenceLink{}}
	members := map[string]bool{}
	themes := scoreThemeMembers(documents, code, freeze)
	for _, document := range documents {
		// Do not repeatedly decode the full-market universe and minute windows
		// while looking for membership; they cannot prove this relationship.
		if document.Category != "theme" && !strings.Contains(document.SourceName, "概念") && !strings.Contains(document.Content, "BOARD_NAME") {
			continue
		}
		if !scoreSourceAvailable(document, freeze) {
			if strings.Contains(document.SourceName, "概念") && sourceDocumentAppliesToStock(document, code, nil) {
				result.Unavailable = append(result.Unavailable, document.SourceID)
				if result.SectorState != "available" && result.SectorState != "membership_only" {
					result.SectorState = scoreUnavailableState(document, freeze)
				}
			}
			continue
		}
		facts := []map[string]any{}
		for _, row := range ScoreSourceFacts(document) {
			if row["BOARD_NAME"] != nil && scoreObjectOwns(row, code) {
				facts = append(facts, row)
				for _, key := range []string{"NEW_BOARD_CODE", "BOARD_NAME"} {
					if text := scoreText(row[key]); text != "" {
						members[text] = true
					}
				}
				if result.SectorState != "available" {
					result.SectorState = "membership_only"
				}
				if scoreNumberAvailable(row["BOARD_YIELD"]) {
					result.SectorState = "available"
				}
			}
		}
		if len(facts) > 0 {
			result.Sector = append(result.Sector, ScoreEvidenceLink{SourceID: document.SourceID, Relation: "verified_membership", Facts: facts})
		}
		for _, id := range themes {
			if id == document.SourceID {
				result.SectorState = "available"
				result.Sector = append(result.Sector, ScoreEvidenceLink{SourceID: id, Relation: "verified_theme_constituent", Facts: ScoreSourceFacts(document)})
				break
			}
		}
	}
	for _, document := range documents {
		if document.Category != "sector" && !scoreCatalystDocument(document) {
			continue
		}
		payload, _ := scorePayload(document).(map[string]any)
		_, themeApplies := themes[scoreText(payload["themeId"])]
		stockApplies := false
		if document.Category == "stock" || document.Category == "quote" || document.Category == "minute" || strings.HasPrefix(document.Category, "stock_") {
			stockApplies = sourceDocumentAppliesToStock(document, code, nil)
		}
		if scoreSourceAvailable(document, freeze) && document.Category == "sector" {
			facts := []map[string]any{}
			for _, row := range ScoreSourceFacts(document) {
				for _, key := range []string{"bd_code", "bd_name", "code", "name"} {
					if members[scoreText(row[key])] {
						facts = append(facts, row)
						break
					}
				}
			}
			if len(facts) > 0 {
				result.SectorState = "available"
				result.Sector = append(result.Sector, ScoreEvidenceLink{SourceID: document.SourceID, Relation: "exact_board_match", Facts: facts})
			}
		}
		if !scoreCatalystDocument(document) || (!stockApplies && !themeApplies) {
			continue
		}
		if !scoreSourceAvailable(document, freeze) {
			if document.Error == "" && document.AvailableAt != nil && !document.AvailableAt.After(freeze) && scorePayloadEmpty(scorePayload(document)) {
				result.Catalyst = append(result.Catalyst, ScoreEvidenceLink{SourceID: document.SourceID, Relation: "no_fresh_catalyst"})
				if result.CatalystState == "source_missing" || strings.HasPrefix(result.CatalystState, "source_") {
					result.CatalystState = "no_fresh_catalyst"
				}
			} else {
				result.Unavailable = append(result.Unavailable, document.SourceID)
				if result.CatalystState == "source_missing" {
					result.CatalystState = scoreUnavailableState(document, freeze)
				}
			}
			continue
		}
		facts := scoreCatalystFacts(document, code, cutoff, freshSince)
		priority := map[string]int{"fresh_available": 4, "time_unverified": 3, "old_background": 2, "after_snapshot": 1}
		sort.SliceStable(facts, func(i, j int) bool {
			return priority[scoreText(facts[i]["relation"])] > priority[scoreText(facts[j]["relation"])]
		})
		state := "time_unverified"
		if len(facts) > 0 {
			state = scoreText(facts[0]["relation"])
		}
		omitted := 0
		if len(facts) > 8 {
			omitted = len(facts) - 8
			facts = facts[:8]
		}
		result.Catalyst = append(result.Catalyst, ScoreEvidenceLink{SourceID: document.SourceID, Relation: state, Facts: facts, OmittedFacts: omitted})
		if strings.HasPrefix(result.CatalystState, "source_") || result.CatalystState == "no_fresh_catalyst" || state == "fresh_available" || (state == "time_unverified" && result.CatalystState != "fresh_available") {
			result.CatalystState = state
		}
	}
	return result
}

func previousCatalystSessionClose(ctx context.Context, calendar Calendar, cutoff time.Time) time.Time {
	if calendar == nil {
		return time.Time{}
	}
	day := cutoff.In(shanghai())
	for index := 1; index <= 370; index++ {
		previous := day.AddDate(0, 0, -index)
		open, err := calendar.IsTradingDay(ctx, previous)
		if err != nil {
			return time.Time{}
		}
		if open {
			return time.Date(previous.Year(), previous.Month(), previous.Day(), 15, 0, 0, 0, shanghai())
		}
	}
	return time.Time{}
}

func scoreScopedDocumentApplies(document researchevidence.SourceDocument, code string, candidates []researchevidence.StockCandidate, documents []researchevidence.SourceDocument, freeze time.Time) bool {
	if document.Category != "theme" && document.Category != "catalyst" {
		return sourceDocumentAppliesToStock(document, code, candidates)
	}
	payload, _ := scorePayload(document).(map[string]any)
	if scoreObjectOwns(payload, code) {
		return true
	}
	themes := scoreThemeMembers(documents, code, freeze)
	_, ok := themes[scoreText(payload["themeId"])]
	return ok
}

func scoreStateText(state string) string {
	switch state {
	case "available":
		return "有可核验板块依据"
	case "membership_only":
		return "归属已核验，尚缺匹配的板块强度数据"
	case "source_unavailable":
		return "来源失败或不可用"
	case "source_empty":
		return "来源成功但返回空数据"
	case "source_after_freeze":
		return "来源晚于证据冻结时点"
	case "source_time_unverified":
		return "来源可用时间尚未核验"
	case "membership_unverified":
		return "候选板块归属尚未核验"
	case "source_missing":
		return "缺少适用于该股票的可用催化来源"
	case "no_fresh_catalyst":
		return "来源已核实为空，没有新催化"
	case "old_background":
		return "只有旧背景，不能奖励新催化分"
	case "time_unverified":
		return "材料可用，但新鲜度尚未核验"
	case "after_snapshot":
		return "事件发生在行情快照之后，不用于快照时评分"
	case "fresh_available":
		return "存在带可核验时间的新催化材料，质量仍需判断"
	default:
		return state
	}
}

func scoreReportLines(item Recommendation, value modelRecommendation, evidence preparedEvidence) []string {
	freeze := evidence.FreezeAt
	support := evidence.scoreEvidence[item.StockCode]
	refs := map[string][]string{}
	cited := map[string]bool{}
	for _, id := range strings.Split(item.SourceRefs, "\n") {
		cited[strings.TrimSpace(id)] = true
	}
	for _, document := range evidence.Documents {
		if !scoreSourceAvailable(document, freeze) || !scoreScopedDocumentApplies(document, item.StockCode, evidence.Candidates, evidence.Documents, freeze) {
			continue
		}
		if document.Category == "market" {
			refs["market"] = append(refs["market"], document.SourceID)
		}
		if document.Category == "stock" || document.Category == "quote" || document.Category == "minute" {
			refs["stock"] = append(refs["stock"], document.SourceID)
		}
		if cited[document.SourceID] {
			refs["risk"] = append(refs["risk"], document.SourceID)
		}
	}
	for _, link := range support.Sector {
		refs["sector"] = append(refs["sector"], link.SourceID)
	}
	for _, link := range support.Catalyst {
		refs["catalyst"] = append(refs["catalyst"], link.SourceID)
	}
	lines := []string{"", "#### 分项评分依据", "", "| 分项 | 分数 | 依据与0分原因 | 相关可用来源 |", "| --- | ---: | --- | --- |"}
	for _, dimension := range []struct {
		key, label      string
		points          float64
		fallback, state string
	}{
		{"market", "市场", item.MarketScore, item.QuantData, "同一冻结快照的市场分可相同"},
		{"sector", "板块", item.SectorScore, item.QuantData, scoreStateText(support.SectorState)},
		{"stock", "个股", item.StockScore, item.QuantData, ""},
		{"catalyst", "催化", item.CatalystScore, item.FreshCatalyst, scoreStateText(support.CatalystState)},
		{"risk", "风险扣分", item.RiskDeduction, item.MainRisk, "扣分不是加分"},
	} {
		reason := strings.TrimSpace(value.ScoreReasons[dimension.key])
		if reason == "" {
			reason = "模型未提供该项独立解释；已有依据：" + defaultReportText(dimension.fallback)
		}
		if dimension.state != "" {
			reason = dimension.state + "；" + reason
		}
		if dimension.points == 0 && (dimension.key == "market" || dimension.key == "stock" || support.SectorState == "available" && dimension.key == "sector" || support.CatalystState == "fresh_available" && dimension.key == "catalyst") && len(refs[dimension.key]) > 0 {
			reason += "；该项有可用来源，0分不能直接解释成接口失败"
		}
		lines = append(lines, "| "+dimension.label+" | "+fmt.Sprintf("%.1f", dimension.points)+" | "+escapeMarkdownCell(reason)+" | "+escapeMarkdownCell(defaultReportText(strings.Join(refs[dimension.key], "、")))+" |")
	}
	return lines
}
