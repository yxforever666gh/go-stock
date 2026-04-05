package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"go-stock/backend/data"
)

func marshalPrettyJSON(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

func marshalJSONLine(v any) ([]byte, error) {
	return json.Marshal(v)
}

func formatQuoteText(item *data.StockInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %s\n", item.Name, item.Code)
	fmt.Fprintf(&b, "最新价: %s\n", item.Price)
	fmt.Fprintf(&b, "涨跌幅: %.2f%%\n", item.ChangePercent)
	fmt.Fprintf(&b, "涨跌额: %.4f\n", item.ChangePrice)
	fmt.Fprintf(&b, "开盘: %s  最高: %s  最低: %s\n", item.Open, item.High, item.Low)
	fmt.Fprintf(&b, "成交量: %s  成交额: %s\n", item.Volume, item.Amount)
	fmt.Fprintf(&b, "时间: %s %s", item.Date, item.Time)
	return b.String()
}

func formatSearchText(res map[string]any) string {
	var b strings.Builder
	if code, ok := res["code"]; ok {
		fmt.Fprintf(&b, "code: %v\n", code)
	}
	if msg, ok := res["message"]; ok && fmt.Sprintf("%v", msg) != "" {
		fmt.Fprintf(&b, "message: %v\n", msg)
	}

	rows := extractSearchRows(res)
	if len(rows) == 0 {
		b.WriteString("rows: 0")
		return b.String()
	}

	limit := len(rows)
	if limit > 20 {
		limit = 20
	}
	fmt.Fprintf(&b, "rows: %d (showing %d)\n", len(rows), limit)
	for i := 0; i < limit; i++ {
		row := rows[i]
		fmt.Fprintf(&b, "%d. %s %s", i+1, row.Code, row.Name)
		if row.Change != "" {
			fmt.Fprintf(&b, " 涨跌幅:%s", row.Change)
		}
		if row.Turnover != "" {
			fmt.Fprintf(&b, " 成交额:%s", row.Turnover)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

type searchRow struct {
	Code     string
	Name     string
	Change   string
	Turnover string
}

func extractSearchRows(res map[string]any) []searchRow {
	dataAny, ok := res["data"]
	if !ok {
		return nil
	}
	dataMap, ok := dataAny.(map[string]any)
	if !ok {
		return nil
	}

	candidates := []string{"list", "items", "data", "records", "result"}
	var rawRows []any
	for _, key := range candidates {
		if rows, ok := dataMap[key].([]any); ok {
			rawRows = rows
			break
		}
	}
	if len(rawRows) == 0 {
		return nil
	}

	out := make([]searchRow, 0, len(rawRows))
	for _, rowAny := range rawRows {
		rowMap, ok := rowAny.(map[string]any)
		if !ok {
			continue
		}
		row := searchRow{
			Code:     pickString(rowMap, "code", "stockCode", "securityCode", "symbol", "f12"),
			Name:     pickString(rowMap, "name", "stockName", "securityName", "f14"),
			Change:   pickString(rowMap, "changePercent", "chg", "f3"),
			Turnover: pickString(rowMap, "turnover", "amount", "f6"),
		}
		out = append(out, row)
	}
	return out
}

func pickString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if val, ok := m[key]; ok {
			s := strings.TrimSpace(fmt.Sprintf("%v", val))
			if s != "" && s != "<nil>" {
				return s
			}
		}
	}
	return ""
}

func asInt(v any, defaultVal int) int {
	switch t := v.(type) {
	case int:
		return t
	case int8:
		return int(t)
	case int16:
		return int(t)
	case int32:
		return int(t)
	case int64:
		return int(t)
	case float32:
		return int(t)
	case float64:
		return int(t)
	default:
		return defaultVal
	}
}
