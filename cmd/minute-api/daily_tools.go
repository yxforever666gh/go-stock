package main

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type breadthInput struct {
	StartDate string `json:"start_date" jsonschema:"起始交易日（含），YYYY-MM-DD"`
	EndDate   string `json:"end_date" jsonschema:"结束交易日（含），YYYY-MM-DD"`
}

type dailyBarsInput struct {
	StockCodes []string `json:"stock_codes" jsonschema:"股票代码数组，例如 [\"sh600941\",\"sz000001\"]，最多100个"`
	StartDate  string   `json:"start_date,omitempty" jsonschema:"起始交易日（含），YYYY-MM-DD；省略表示不限"`
	EndDate    string   `json:"end_date,omitempty" jsonschema:"结束交易日（含），YYYY-MM-DD；省略表示不限"`
}

type breadthResult struct {
	Source   string       `json:"source"`
	Timezone string       `json:"timezone"`
	Unit     string       `json:"unit"`
	Data     []breadthRow `json:"data"`
}

type dailyBarsResult struct {
	Source   string           `json:"source"`
	Timezone string           `json:"timezone"`
	Symbols  []string         `json:"symbols"`
	Data     []map[string]any `json:"data"`
}

// registerDailyTools 暴露蝶梦日线的本地物化视图。
// 这些工具只读 daily.sqlite，绝不联网，因此不存在蝶梦 1.2 秒节流叠加导致的超时。
func registerDailyTools(server *mcp.Server, daily *dailyStore) {
	annotations := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
	if daily == nil {
		return
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_refresh_status",
		Description: "查询本地日线库的覆盖范围（最早/最晚交易日、总行数、失败日期）。统计全市场问题前应先调用它确认本地有什么，避免盲目调用蝶梦接口而超时。source=sqlite_local。",
		Annotations: annotations,
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, refreshStatus, error) {
		out, err := daily.status()
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_market_breadth",
		Description: "按交易日返回全市场涨跌家数（up/down/flat/total），纯本地 SQL 聚合，毫秒级。只统计 pct_chg 非空的记录。涨停家数需结合板块涨跌幅规则判定，请用 get_daily_bars 下钻。source=sqlite_local。",
		Annotations: annotations,
	}, func(_ context.Context, _ *mcp.CallToolRequest, args breadthInput) (*mcp.CallToolResult, breadthResult, error) {
		rows, err := daily.breadth(args.StartDate, args.EndDate)
		if err != nil {
			return nil, breadthResult{}, err
		}
		return nil, breadthResult{Source: "sqlite_local", Timezone: "Asia/Shanghai", Unit: "count", Data: rows}, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_daily_bars",
		Description: "按股票代码查询本地日线（不复权口径，来自蝶梦日常全市场转储），按代码与交易日升序。纯本地读取，不联网。symbols 为空或查无记录时 data=[]。source=sqlite_local。",
		Annotations: annotations,
	}, func(_ context.Context, _ *mcp.CallToolRequest, args dailyBarsInput) (*mcp.CallToolResult, dailyBarsResult, error) {
		if len(args.StockCodes) == 0 {
			return nil, dailyBarsResult{}, fmt.Errorf("请至少提供一个股票代码")
		}
		if len(args.StockCodes) > 100 {
			return nil, dailyBarsResult{}, fmt.Errorf("一次最多查询100个股票代码")
		}
		codes := make([]string, 0, len(args.StockCodes))
		for _, raw := range args.StockCodes {
			code := normalizeSymbol(raw)
			if code == "" {
				return nil, dailyBarsResult{}, fmt.Errorf("股票代码无效：%s", raw)
			}
			codes = append(codes, code)
		}
		rows, err := daily.bars(codes, args.StartDate, args.EndDate)
		if err != nil {
			return nil, dailyBarsResult{}, err
		}
		data := make([]map[string]any, 0, len(rows))
		for _, r := range rows {
			data = append(data, map[string]any{
				"stock_code": r.Code, "stock_name": r.Name, "trade_date": r.Date,
				"open": r.Open, "high": r.High, "low": r.Low, "close": r.Close,
				"pre_close": r.PreClose, "pct_chg": r.PctChg,
				"volume": r.Volume, "amount": r.Amount,
			})
		}
		return nil, dailyBarsResult{Source: "sqlite_local", Timezone: "Asia/Shanghai", Symbols: codes, Data: data}, nil
	})
}
