package data

import (
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"go-stock/backend/models"
	"go-stock/backend/research"
)

type compactDailyBar [6]any
type compactMinuteBar [4]any

func compactResearchPromptValue(name string, value any) string {
	lower := strings.ToLower(name)
	var compact any
	switch {
	case strings.Contains(lower, "实时行情"):
		compact = compactRealtimeQuote(value)
	case strings.Contains(lower, "日k"):
		compact = compactDailyKLine(value)
	case strings.Contains(lower, "分钟k"):
		compact = compactMinuteKLine(value)
	default:
		limit := 10
		if strings.Contains(lower, "新闻") || strings.Contains(lower, "公告") || strings.Contains(lower, "研报") {
			limit = 5
		}
		compact = compactGenericPromptValue(value, limit)
	}
	encoded, err := json.Marshal(compact)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func compactRealtimeQuote(value any) any {
	rows, ok := value.(*[]models.StockInfo)
	if !ok || rows == nil {
		return compactGenericPromptValue(value, 1)
	}
	result := make([]map[string]any, 0, len(*rows))
	asOf := ""
	for _, row := range *rows {
		if strings.TrimSpace(row.Date) != "" && strings.TrimSpace(row.Time) != "" {
			clock := strings.TrimSpace(row.Time)
			if len(clock) == 5 {
				clock += ":00"
			}
			candidateAsOf := compactTradingDate(row.Date) + "T" + clock + "+08:00"
			if candidateAsOf > asOf {
				asOf = candidateAsOf
			}
		}
		result = append(result, map[string]any{
			"date": row.Date, "time": row.Time, "code": row.Code, "name": row.Name,
			"price": numberString(row.Price), "previousClose": numberString(row.PreClose),
			"open": numberString(row.Open), "high": numberString(row.High), "low": numberString(row.Low),
			"volume": numberString(row.Volume), "amount": numberString(row.Amount),
			"bid1": numberString(row.B1P), "ask1": numberString(row.A1P), "market": row.Market,
		})
	}
	return map[string]any{"order": "newest_first", "asOf": asOf, "quotes": result}
}

func compactDailyKLine(value any) any {
	rows, ok := value.(*[]models.KLineData)
	if !ok || rows == nil {
		return compactGenericPromptValue(value, 20)
	}
	ordered := append([]models.KLineData(nil), (*rows)...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Day < ordered[j].Day })
	result := map[string]any{"order": "newest_first", "barCount": len(ordered), "bars": []compactDailyBar{}}
	if len(ordered) == 0 {
		return result
	}
	result["asOf"] = ordered[len(ordered)-1].Day
	result["coverageFrom"], result["coverageTo"] = ordered[0].Day, ordered[len(ordered)-1].Day
	returns := map[string]float64{}
	for _, horizon := range []int{5, 20, 60} {
		if rate, ok := kLineReturn(ordered, horizon); ok {
			returns[strconv.Itoa(horizon)+"d"] = rate
		}
	}
	result["returns"] = returns
	start := len(ordered) - 20
	if start < 0 {
		start = 0
	}
	bars := make([]compactDailyBar, 0, len(ordered)-start)
	for index := len(ordered) - 1; index >= start; index-- {
		row := ordered[index]
		bars = append(bars, compactDailyBar{row.Day, numberString(row.Open), numberString(row.High), numberString(row.Low), numberString(row.Close), numberString(row.Volume)})
	}
	result["barFields"] = []string{"day", "open", "high", "low", "close", "volume"}
	result["bars"] = bars
	return result
}

func kLineReturn(rows []models.KLineData, horizon int) (float64, bool) {
	if len(rows) < 2 || horizon <= 0 {
		return 0, false
	}
	end, ok := parseNumber(rows[len(rows)-1].Close)
	if !ok || end <= 0 {
		return 0, false
	}
	startIndex := len(rows) - 1 - horizon
	if startIndex < 0 {
		startIndex = 0
	}
	start, ok := parseNumber(rows[startIndex].Close)
	if !ok || start <= 0 || startIndex == len(rows)-1 {
		return 0, false
	}
	return end/start - 1, true
}

func compactMinuteKLine(value any) any {
	payload, ok := value.(map[string]any)
	if !ok {
		return compactGenericPromptValue(value, 31)
	}
	rows, ok := payload["rows"].(*[]MinuteData)
	if !ok || rows == nil {
		return compactGenericPromptValue(value, 31)
	}
	ordered := append([]MinuteData(nil), (*rows)...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Time < ordered[j].Time })
	result := map[string]any{"order": "newest_first", "barCount": len(ordered), "bars": []compactMinuteBar{}}
	tradingDate, _ := payload["source"].(string)
	if tradingDate != "" {
		result["tradingDate"] = tradingDate
	}
	if len(ordered) == 0 {
		return result
	}
	if tradingDate != "" {
		result["asOf"] = compactTradingDate(tradingDate) + "T" + ordered[len(ordered)-1].Time + ":00+08:00"
	} else {
		result["asOf"] = ordered[len(ordered)-1].Time
	}
	result["coverageFrom"], result["coverageTo"] = ordered[0].Time, ordered[len(ordered)-1].Time
	windows := make([]map[string]any, 0, 3)
	for _, horizon := range []int{15, 30, 60} {
		start := len(ordered) - 1 - horizon
		if start < 0 {
			start = 0
		}
		selected := ordered[start:]
		if len(selected) < 2 || selected[0].Price <= 0 {
			continue
		}
		high, low := selected[0].Price, selected[0].Price
		for _, row := range selected[1:] {
			high, low = math.Max(high, row.Price), math.Min(low, row.Price)
		}
		windows = append(windows, map[string]any{"minutes": horizon, "bars": len(selected), "returnRate": selected[len(selected)-1].Price/selected[0].Price - 1, "high": high, "low": low})
	}
	result["windows"] = windows
	start := len(ordered) - 31
	if start < 0 {
		start = 0
	}
	bars := make([]compactMinuteBar, 0, len(ordered)-start)
	for index := len(ordered) - 1; index >= start; index-- {
		row := ordered[index]
		bars = append(bars, compactMinuteBar{row.Time, row.Price, row.Volume, row.Amount})
	}
	result["barFields"] = []string{"time", "price", "volume", "amount"}
	result["bars"] = bars
	return result
}

func compactTradingDate(value string) string {
	value = strings.TrimSpace(value)
	if len(value) == 8 && !strings.Contains(value, "-") {
		return value[:4] + "-" + value[4:6] + "-" + value[6:]
	}
	return value
}

func validateCompactStockSourceAt(name, content string, collectedAt time.Time) error {
	lower := strings.ToLower(name)
	if !strings.Contains(lower, "实时行情") && !strings.Contains(lower, "分钟k") {
		return nil
	}
	var payload map[string]any
	if json.Unmarshal([]byte(content), &payload) != nil {
		return errors.New("compact stock source is not valid JSON")
	}
	raw, _ := payload["asOf"].(string)
	asOf, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return errors.New("compact stock source is missing a valid asOf")
	}
	localNow, localAsOf := research.ShanghaiTime(collectedAt), research.ShanghaiTime(asOf)
	if localNow.Format("2006-01-02") != localAsOf.Format("2006-01-02") {
		return errors.New("compact stock source is from a different trading date")
	}
	lag := localNow.Sub(localAsOf)
	if lag < -lifecycleEvidenceClockSkew {
		return errors.New("compact stock source is later than its collection time")
	}
	if research.IsTradingSession(localNow) && lag > lifecycleEvidenceMaxLag {
		return errors.New("compact stock source is stale for the active session")
	}
	return nil
}

func compactGenericPromptValue(value any, limit int) any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return map[string]any{"status": "marshal_failed"}
	}
	var generic any
	if json.Unmarshal(encoded, &generic) != nil {
		return map[string]any{"status": "decode_failed"}
	}
	return compactGenericNode(generic, limit, 0)
}

func compactGenericNode(value any, limit, depth int) any {
	if depth > 4 {
		return nil
	}
	switch typed := value.(type) {
	case []any:
		ordered := append([]any(nil), typed...)
		sort.SliceStable(ordered, func(i, j int) bool { return genericTimestamp(ordered[i]) > genericTimestamp(ordered[j]) })
		if limit > 0 && len(ordered) > limit {
			ordered = ordered[:limit]
		}
		result := make([]any, 0, len(ordered))
		for _, item := range ordered {
			result = append(result, compactGenericNode(item, limit, depth+1))
		}
		return result
	case map[string]any:
		result := make(map[string]any)
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child := typed[key]
			if depth >= 1 && !usefulPromptKey(key) {
				continue
			}
			result[key] = compactGenericNode(child, limit, depth+1)
		}
		return result
	case string:
		return truncatePromptString(typed, 300)
	default:
		return value
	}
}

func genericTimestamp(value any) string {
	row, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	for _, key := range []string{"sort_date", "notice_date", "display_time", "publishTime", "publishDate", "dataTime", "data_time", "opendate", "date", "day", "createdAt", "created_at", "time"} {
		if raw, exists := row[key]; exists {
			return strings.TrimSpace(toString(raw))
		}
	}
	return ""
}

func usefulPromptKey(key string) bool {
	lower := strings.ToLower(key)
	for _, token := range []string{"date", "time", "title", "name", "code", "price", "open", "close", "high", "low", "volume", "amount", "eps", "pe", "pb", "roe", "rating", "net", "ratio", "profit", "revenue", "concept", "industry", "summary", "message", "content", "question", "answer", "reply", "attached", "text", "body", "detail", "status", "result", "data", "items", "list"} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func numberString(value string) any {
	if number, ok := parseNumber(value); ok {
		return number
	}
	return value
}

func parseNumber(value string) (float64, bool) {
	number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	return number, err == nil && !math.IsNaN(number) && !math.IsInf(number, 0)
}

func toString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func truncatePromptString(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	end := maxBytes - len("...")
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end] + "..."
}
