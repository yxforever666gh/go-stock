package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go-stock/internal/diemeng"
)

type barInput struct {
	Source string `json:"source,omitempty" jsonschema:"auto（默认）优先本地，缺文件或无记录时查询蝶梦；local 仅本地；diemeng 仅蝶梦"`
	Symbol string `json:"symbol" jsonschema:"完整股票代码，例如中国移动 sh600941"`
	Period string `json:"period,omitempty" jsonschema:"周期：1m、5m、15m、30m、60m；省略为 1m"`
	Time   string `json:"time" jsonschema:"指定时刻：带时区的 RFC3339，或北京时间 YYYY-MM-DD HH:mm[:ss]"`
}

type barsInput struct {
	Source    string `json:"source,omitempty" jsonschema:"auto（默认）优先本地，无记录时查询蝶梦；不拼接部分本地结果。可选 local、diemeng"`
	Symbol    string `json:"symbol" jsonschema:"完整股票代码，例如中国移动 sh600941"`
	Period    string `json:"period,omitempty" jsonschema:"周期：1m、5m、15m、30m、60m；省略为 1m"`
	StartTime string `json:"start_time" jsonschema:"起始时刻（含），格式同 time；无时区时按北京时间解释"`
	EndTime   string `json:"end_time" jsonschema:"结束时刻（含）：带时区 RFC3339 或北京时间 YYYY-MM-DD HH:mm[:ss]"`
}

type barResult struct {
	Source     string `json:"source"`
	Adjustment string `json:"adjustment"`
	Symbol     string `json:"symbol"`
	Period     string `json:"period"`
	Timezone   string `json:"timezone"`
	Time       string `json:"time"`
	Found      bool   `json:"found"`
	Bar        any    `json:"bar"`
}

type minuteResult struct {
	Symbol     string           `json:"symbol"`
	Period     string           `json:"period"`
	Timezone   string           `json:"timezone"`
	Source     string           `json:"source"`
	Adjustment string           `json:"adjustment"`
	Data       []map[string]any `json:"data"`
}

func mcpHandler(store localStore, provider *diemeng.Client, daily *dailyStore) http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: "stock-minute-data", Version: "1.0.0"}, nil)
	diemeng.Register(server, provider)
	registerMarketTools(server, store)
	registerDailyTools(server, daily)
	annotations := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
	mcp.AddTool(server, &mcp.Tool{
		Name: "get_bar", Description: "查询指定时刻的一根 K 线，不匹配附近时刻。默认本地优先，缺数据时查询蝶梦。无记录时 found=false、bar=null。source 标明来源；成交量统一为股，CSV 复权口径 unknown、蝶梦 none，蝶梦未提供字段为 null。",
		Annotations: annotations,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args barInput) (*mcp.CallToolResult, barResult, error) {
		stamp, err := minuteTime(args.Time)
		if err != nil {
			return nil, barResult{}, err
		}
		data, err := queryWithSource(ctx, store, provider, args.Symbol, args.Period, args.Source, stamp, stamp)
		if err != nil {
			return nil, barResult{}, err
		}
		result := barResult{Symbol: data.Symbol, Period: data.Period, Timezone: data.Timezone, Time: stamp.Format(time.RFC3339Nano), Source: data.Source, Adjustment: data.Adjustment}
		if len(data.Data) > 0 {
			result.Found, result.Bar = true, data.Data[0]
		}
		return nil, result, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "get_bars", Description: "查询指定起止时刻内的 K 线（包含两端），按时间升序。默认本地优先，无记录时查询蝶梦；部分本地结果不混合或推断缺失，可用 source=diemeng 查询整个区间。无记录时 data=[]。source/adjustment 标明来源和复权，成交量为股。",
		Annotations: annotations,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args barsInput) (*mcp.CallToolResult, minuteResult, error) {
		start, err := minuteTime(args.StartTime)
		if err != nil {
			return nil, minuteResult{}, err
		}
		end, err := minuteTime(args.EndTime)
		if err != nil {
			return nil, minuteResult{}, err
		}
		data, err := queryWithSource(ctx, store, provider, args.Symbol, args.Period, args.Source, start, end)
		return nil, data, err
	})
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
}

func queryWithSource(ctx context.Context, store localStore, provider *diemeng.Client, symbol, period, source string, start, end time.Time) (minuteResult, error) {
	if source == "" {
		source = "auto"
	}
	if source != "auto" && source != "local" && source != "diemeng" {
		return minuteResult{}, fmt.Errorf("source 仅支持 auto、local、diemeng")
	}
	period, err := normalizePeriod(symbol, period)
	if err != nil {
		return minuteResult{}, err
	}
	if start.After(end) {
		return minuteResult{}, fmt.Errorf("开始时间不能晚于结束时间")
	}
	if source != "diemeng" {
		local, err := queryMinutes(store, symbol, period, start, end)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return minuteResult{}, err
		}
		if source == "local" || (err == nil && len(local.Data) > 0) || provider == nil {
			return minuteResult{Symbol: symbol, Period: period, Timezone: "Asia/Shanghai", Source: "csv", Adjustment: "unknown", Data: local.Data}, err
		}
	}
	rows, err := provider.Minutes(ctx, symbol, period, start, end)
	return minuteResult{Symbol: symbol, Period: period, Timezone: "Asia/Shanghai", Source: "diemeng", Adjustment: "none", Data: rows}, err
}

func minuteTime(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04", "2006-01-02 15:04:05"} {
		if stamp, err := time.ParseInLocation(layout, value, beijing); err == nil {
			return stamp.In(beijing), nil
		}
	}
	return time.Time{}, fmt.Errorf("时间无效：使用带时区 RFC3339 或北京时间 YYYY-MM-DD HH:mm[:ss]")
}

func queryMinutes(store localStore, symbol, period string, start, end time.Time) (response, error) {
	period, err := normalizePeriod(symbol, period)
	if err != nil {
		return response{}, err
	}
	if start.After(end) {
		return response{}, fmt.Errorf("开始时间不能晚于结束时间")
	}
	firstDay, _ := date(start.Format("2006-01-02"))
	lastDay, _ := date(end.Format("2006-01-02"))
	rows, err := store.read(symbol, period, firstDay, lastDay)
	if err != nil {
		log.Printf("MCP %s %s: %v", symbol, period, err)
		if errors.Is(err, os.ErrNotExist) {
			return response{}, fmt.Errorf("股票数据文件不存在: %w", os.ErrNotExist)
		}
		return response{}, fmt.Errorf("数据读取或解析失败")
	}
	selected := rows[:0]
	for _, row := range rows {
		stamp, _ := time.Parse(time.RFC3339, row["time"].(string))
		if !stamp.Before(start) && !stamp.After(end) {
			selected = append(selected, row)
		}
	}
	return response{symbol, period, "Asia/Shanghai", selected}, nil
}
