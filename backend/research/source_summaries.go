package research

import (
	"encoding/json"
	"sort"
	"strings"
)

// Source-specific summaries reserve room for facts from every key category.
// Missing financial fields stay null; announcement lists are not full reports.
func stockSourceSummary(name string, payload json.RawMessage) json.RawMessage {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return nil
	}
	priority := stockSourcePriority(name)
	var summary any
	switch priority {
	case 0, 1, 2:
		if row, ok := value.(map[string]any); ok {
			copy := make(map[string]any, len(row))
			for key, item := range row {
				if key != "bars" && key != "barFields" {
					copy[key] = item
				}
			}
			summary = copy
		} else {
			summary = value
		}
	case 3:
		rows := stockSummaryRows(value)
		sort.SliceStable(rows, func(i, j int) bool { return summaryText(rows[i], "REPORT_DATE") > summaryText(rows[j], "REPORT_DATE") })
		periods, seen := []map[string]any{}, map[string]bool{}
		for _, row := range rows {
			date := summaryText(row, "REPORT_DATE")
			if seen[date] {
				continue
			}
			seen[date] = true
			periods = append(periods, map[string]any{
				"reportDate": summaryField(row, "REPORT_DATE"), "noticeDate": summaryField(row, "NOTICE_DATE"),
				"revenue":     summaryField(row, "TOTAL_OPERATE_INCOME", "OPERATE_INCOME"),
				"totalIncome": summaryField(row, "TOTAL_INCOME"),
				"netProfit":   summaryField(row, "NETPROFIT"), "parentNetProfit": summaryField(row, "PARENT_NETPROFIT"),
				"roe": summaryField(row, "ROE"), "debtAssetRatio": summaryField(row, "DEBT_ASSET_RATIO"),
				"operatingCashFlow": summaryField(row, "NETCASH_OPERATE", "NET_CASH_OPERATE", "NETCASH_OPERATE_ACT"),
			})
			if len(periods) == 2 {
				break
			}
		}
		summary = map[string]any{"periods": periods, "missingFields": "null means unavailable"}
	case 4:
		rows := stockSummaryRows(value)
		sort.SliceStable(rows, func(i, j int) bool { return summaryText(rows[i], "opendate") > summaryText(rows[j], "opendate") })
		items := []map[string]any{}
		for _, row := range rows {
			items = append(items, map[string]any{"date": summaryField(row, "opendate"), "netAmount": summaryField(row, "netamount"), "mainNetAmount": summaryField(row, "r0_net")})
			if len(items) == 3 {
				break
			}
		}
		summary = items
	case 5:
		rows := stockSummaryRows(value)
		sort.SliceStable(rows, func(i, j int) bool {
			return summaryText(rows[i], "notice_date", "display_time") > summaryText(rows[j], "notice_date", "display_time")
		})
		items := []map[string]any{}
		for _, row := range rows {
			item := map[string]any{"date": summaryField(row, "notice_date", "display_time"), "title": truncateUTF8(summaryText(row, "title", "title_ch"), 120), "ref": summaryField(row, "art_code")}
			if body := summaryText(row, "summary", "content"); body != "" {
				item["summary"] = truncateUTF8(body, 90)
			}
			items = append(items, item)
			if len(items) == 5 {
				break
			}
		}
		summary = map[string]any{"kind": "announcement_list", "items": items}
	default:
		return compactPromptRawJSON(payload, 320)
	}
	encoded, _ := json.Marshal(summary)
	return encoded
}

func stockSummaryRows(value any) []map[string]any {
	switch typed := value.(type) {
	case []any:
		rows := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if row, ok := item.(map[string]any); ok {
				rows = append(rows, row)
			}
		}
		return rows
	case map[string]any:
		for _, key := range []string{"data", "result", "items", "list"} {
			if child, ok := typed[key]; ok {
				return stockSummaryRows(child)
			}
		}
	}
	return nil
}

func summaryField(row map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := row[key]; ok && value != nil && value != "" {
			return value
		}
	}
	return nil
}

func summaryText(row map[string]any, keys ...string) string {
	value, _ := summaryField(row, keys...).(string)
	return value
}
