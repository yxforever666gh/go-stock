package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type indexPointInput struct {
	Symbol string `json:"symbol" jsonschema:"指数代码，例如 sh000001；先用 search_indices 查询名称与可用周期"`
	Period string `json:"period,omitempty" jsonschema:"1m、5m、15m、30m、60m，默认1m"`
	Time   string `json:"time" jsonschema:"精确时刻：带时区RFC3339或北京时间 YYYY-MM-DD HH:mm[:ss]"`
}
type indexRangeInput struct {
	Symbol    string `json:"symbol" jsonschema:"指数代码，例如 sh000001"`
	Period    string `json:"period,omitempty" jsonschema:"1m、5m、15m、30m、60m，默认1m"`
	StartTime string `json:"start_time" jsonschema:"起始时刻（含），带时区RFC3339或北京时间 YYYY-MM-DD HH:mm[:ss]"`
	EndTime   string `json:"end_time" jsonschema:"结束时刻（含），格式同start_time"`
}
type auctionPointInput struct {
	Symbol string `json:"symbol" jsonschema:"股票代码，例如 sh600941；与文件中的600941.SH对应"`
	Time   string `json:"time" jsonschema:"精确竞价时刻，不取附近记录；带时区RFC3339或北京时间 YYYY-MM-DD HH:mm:ss"`
}
type auctionRangeInput struct {
	Symbol    string `json:"symbol" jsonschema:"股票代码，例如 sh600941"`
	StartTime string `json:"start_time" jsonschema:"起始时刻（含），带时区RFC3339或北京时间 YYYY-MM-DD HH:mm:ss"`
	EndTime   string `json:"end_time" jsonschema:"结束时刻（含）"`
	Page      int    `json:"page,omitempty" jsonschema:"页码从0开始，默认0"`
	PageSize  int    `json:"page_size,omitempty" jsonschema:"每页默认500，最大5000"`
}

func queryRange(startText, endText string) (time.Time, time.Time, error) {
	start, err := minuteTime(startText)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	end, err := minuteTime(endText)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if start.After(end) {
		return time.Time{}, time.Time{}, fmt.Errorf("开始时间不能晚于结束时间")
	}
	return start, end, nil
}

func indexResult(s localStore, symbol, period string, rows []map[string]any) map[string]any {
	return map[string]any{"symbol": symbol, "name": s.names[symbol], "period": period, "source": "csv_index", "timezone": "Asia/Shanghai", "units": map[string]string{"price": "index_points", "volume": "unknown", "amount": "unknown"}, "data": rows}
}

func registerMarketTools(server *mcp.Server, s localStore) {
	closed := false
	annotations := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closed}
	mcp.AddTool(server, &mcp.Tool{Name: "search_indices", Description: "查找本地指数代码/名称和实际可用分钟周期。股票与指数目录分开，名称相同的不同代码不擅自合并。", Annotations: annotations}, func(_ context.Context, _ *mcp.CallToolRequest, args struct {
		Query string `json:"query,omitempty" jsonschema:"代码或名称片段；省略返回全部可用指数"`
	}) (*mcp.CallToolResult, any, error) {
		return nil, map[string]any{"source": "csv_index", "indices": s.searchIndices(args.Query)}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "get_index_bar", Description: "精确读取本地指数某时刻K线；无记录found=false。不调整时间，不将成交量0改成缺失，不换算未注明的单位。", Annotations: annotations}, func(_ context.Context, _ *mcp.CallToolRequest, args indexPointInput) (*mcp.CallToolResult, any, error) {
		period, err := normalizePeriod(args.Symbol, args.Period)
		if err != nil {
			return nil, nil, err
		}
		stamp, err := minuteTime(args.Time)
		if err != nil {
			return nil, nil, err
		}
		rows, err := s.indexBars(args.Symbol, period, stamp, stamp)
		if err != nil {
			return nil, nil, err
		}
		result := indexResult(s, args.Symbol, period, rows)
		delete(result, "data")
		result["time"] = stamp.Format(time.RFC3339Nano)
		result["found"] = len(rows) > 0
		result["bar"] = nil
		if len(rows) > 0 {
			result["bar"] = rows[0]
		}
		return nil, result, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "get_index_bars", Description: "读取本地指数分钟区间，包含两端，排序并去除相同记录；冲突明确报错。仅本地数据，未注明量额单位保留原值。", Annotations: annotations}, func(_ context.Context, _ *mcp.CallToolRequest, args indexRangeInput) (*mcp.CallToolResult, any, error) {
		period, err := normalizePeriod(args.Symbol, args.Period)
		if err != nil {
			return nil, nil, err
		}
		start, end, err := queryRange(args.StartTime, args.EndTime)
		if err != nil {
			return nil, nil, err
		}
		rows, err := s.indexBars(args.Symbol, period, start, end)
		if err != nil {
			return nil, nil, err
		}
		return nil, indexResult(s, args.Symbol, period, rows), nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "get_auction_snapshot", Description: "读取精确时刻的本地集合竞价快照，不取附近时间。保留19个原字段和标准化time；空白为null、0仍为0，单位未注明，不推算未匹配量。", Annotations: annotations}, func(ctx context.Context, _ *mcp.CallToolRequest, args auctionPointInput) (*mcp.CallToolResult, any, error) {
		stamp, err := minuteTime(args.Time)
		if err != nil {
			return nil, nil, err
		}
		result, err := s.auction.query(ctx, args.Symbol, stamp, stamp, 0, 1)
		if err != nil {
			return nil, nil, err
		}
		out := map[string]any{"symbol": args.Symbol, "source": result.Source, "timezone": result.Timezone, "units": result.Units, "time": stamp.Format(time.RFC3339Nano), "found": result.Total > 0, "snapshot": nil}
		if result.Total > 0 {
			out["snapshot"] = result.Data[0]
		}
		return nil, out, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "get_auction_snapshots", Description: "分页读取本地集合竞价区间，包含两端。全部原始字段保留；空白null、数值0不丢弃，单位未知。返回total和next_page，不静默截断。", Annotations: annotations}, func(ctx context.Context, _ *mcp.CallToolRequest, args auctionRangeInput) (*mcp.CallToolResult, any, error) {
		start, end, err := queryRange(args.StartTime, args.EndTime)
		if err != nil {
			return nil, nil, err
		}
		if args.PageSize == 0 {
			args.PageSize = 500
		}
		result, err := s.auction.query(ctx, args.Symbol, start, end, args.Page, args.PageSize)
		return nil, result, err
	})
}

func httpRange(q url.Values) (time.Time, time.Time, error) {
	if q.Get("time") != "" {
		stamp, err := minuteTime(q.Get("time"))
		return stamp, stamp, err
	}
	return queryRange(q.Get("start_time"), q.Get("end_time"))
}

func marketHTTPError(w http.ResponseWriter, err error) {
	status := 500
	if os.IsNotExist(err) {
		status = 404
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func registerMarketHTTP(mux *http.ServeMux, s localStore) {
	mux.HandleFunc("GET /api/indices", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"source": "csv_index", "indices": s.searchIndices(r.URL.Query().Get("query"))})
	})
	mux.HandleFunc("GET /api/index/bars", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		symbol := q.Get("symbol")
		period, err := normalizePeriod(symbol, q.Get("period"))
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		start, end, err := httpRange(q)
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		rows, err := s.indexBars(symbol, period, start, end)
		if err != nil {
			marketHTTPError(w, err)
			return
		}
		writeJSON(w, 200, indexResult(s, symbol, period, rows))
	})
	mux.HandleFunc("GET /api/auction/snapshots", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		symbol := q.Get("symbol")
		start, end, err := httpRange(q)
		if err != nil || !symbolPattern.MatchString(symbol) {
			writeJSON(w, 400, map[string]string{"error": "股票代码或时间范围无效"})
			return
		}
		page, size := 0, 500
		if text := q.Get("page"); text != "" {
			page, err = strconv.Atoi(text)
			if err != nil {
				page = -1
			}
		}
		if text := q.Get("page_size"); text != "" {
			size, err = strconv.Atoi(text)
			if err != nil {
				size = -1
			}
		}
		if page < 0 || size < 1 || size > 5000 {
			writeJSON(w, 400, map[string]string{"error": "page须非负，page_size须在1至5000之间"})
			return
		}
		result, err := s.auction.query(r.Context(), symbol, start, end, page, size)
		if err != nil {
			marketHTTPError(w, err)
			return
		}
		writeJSON(w, 200, result)
	})
}
