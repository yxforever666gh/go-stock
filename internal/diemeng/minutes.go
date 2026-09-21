package diemeng

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strings"
	"time"
)

var stockSymbol = regexp.MustCompile(`^(sh|sz|bj)[0-9]{6}$`)

// Minutes normalizes only the unadjusted stock/history endpoint, never unrelated provider units.
func (c *Client) Minutes(ctx context.Context, symbol, period string, start, end time.Time) ([]map[string]any, error) {
	if !stockSymbol.MatchString(symbol) {
		return nil, errors.New("股票代码无效")
	}
	level := map[string]string{"1m": "1min", "5m": "5min", "15m": "15min", "30m": "30min", "60m": "60min"}[period]
	if level == "" || start.After(end) {
		return nil, errors.New("分钟周期或时间范围无效")
	}
	zone := time.FixedZone("Asia/Shanghai", 8*3600)
	start, end = start.In(zone), end.In(zone)
	result, err := c.Call(ctx, "diemeng_stock_history", map[string]any{"stock_code": symbol[2:] + "." + strings.ToUpper(symbol[:2]), "level": level, "start_time": start.Format("2006-01-02 15:04:05"), "end_time": end.Format("2006-01-02 15:04:05"), "page": 0, "page_size": 10000})
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Data *struct {
			Total json.Number
			List  json.RawMessage
			Items json.RawMessage
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(result.Response))
	decoder.UseNumber()
	if err := decoder.Decode(&envelope); err != nil {
		return nil, errors.New("蝶梦分钟响应结构无效")
	}
	if envelope.Data == nil {
		return nil, errors.New("蝶梦分钟 data 必须是对象")
	}
	data := envelope.Data.List
	if len(data) == 0 {
		data = envelope.Data.Items
	}
	if len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil, errors.New("蝶梦分钟缺少 list/items 数组")
	}
	var items []map[string]any
	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&items); err != nil {
		return nil, errors.New("蝶梦分钟 list/items 必须是记录数组")
	}
	if envelope.Data.Total != "" {
		total, err := envelope.Data.Total.Int64()
		if err != nil || total < 0 {
			return nil, errors.New("蝶梦分钟 total 无效")
		}
		if total > int64(len(items)) {
			return nil, errors.New("蝶梦分钟结果有后续分页，请缩小时间范围或使用 diemeng_stock_history 分页查询")
		}
	}
	rows := make([]map[string]any, 0, len(items))
	for _, item := range items {
		stampText, _ := item["trade_time"].(string)
		stamp, err := time.ParseInLocation("2006-01-02 15:04:05", stampText, zone)
		if err != nil {
			return nil, errors.New("蝶梦分钟 trade_time 无效")
		}
		row := map[string]any{"time": stamp.Format(time.RFC3339), "change": nil, "change_pct": nil, "turnover_rate_pct": nil, "float_shares": nil, "total_shares": nil}
		for _, key := range []string{"open", "high", "low", "close", "amount", "vol"} {
			value, present := item[key]
			if !present || value == nil {
				return nil, fmt.Errorf("蝶梦分钟缺少 %s", key)
			}
			number := json.Number(fmt.Sprint(value))
			if _, err := json.Marshal(number); err != nil || number == "" {
				return nil, fmt.Errorf("蝶梦分钟 %s 不是有效数值", key)
			}
			if key == "vol" {
				volume, ok := new(big.Rat).SetString(string(number))
				if !ok {
					return nil, errors.New("蝶梦成交量无法转换")
				}
				volume.Mul(volume, big.NewRat(100, 1))
				if !volume.IsInt() {
					return nil, errors.New("蝶梦成交量换算为股后不是整数")
				}
				row["volume"] = json.Number(volume.Num().String())
			} else {
				row[key] = number
			}
		}
		if !stamp.Before(start) && !stamp.After(end) {
			rows = append(rows, row)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i]["time"].(string) < rows[j]["time"].(string) })
	return rows, nil
}
