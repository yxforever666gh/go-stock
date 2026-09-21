// Package diemeng adapts the documented private data endpoints without depending on application state.
package diemeng

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

//go:embed catalog.json
var catalogJSON []byte

type Parameter struct {
	Name, Type, Description string
	Required                bool
}

type Field struct{ Name, Type, Description string }

type Endpoint struct {
	Name, Title, Path, Method, Description string
	InputSchema                            json.RawMessage     `json:"input_schema"`
	DateFormats                            map[string][]string `json:"date_formats"`
	Parameters                             []Parameter
	Fields                                 []Field
	Example                                map[string]any
	Download                               bool
	resolved                               *jsonschema.Resolved
}

// Catalog returns the fixed, embedded endpoint definitions, not arbitrary caller URLs.
func Catalog() []Endpoint {
	var endpoints []Endpoint
	if err := json.Unmarshal(catalogJSON, &endpoints); err != nil {
		panic(err)
	}
	for i := range endpoints {
		var schema jsonschema.Schema
		if err := json.Unmarshal(endpoints[i].InputSchema, &schema); err != nil {
			panic(err)
		}
		resolved, err := schema.Resolve(nil)
		if err != nil {
			panic(err)
		}
		endpoints[i].resolved = resolved
	}
	return endpoints
}

func (e Endpoint) validate(args map[string]any) error {
	if err := e.resolved.Validate(args); err != nil {
		return fmt.Errorf("参数不符合 %s 的接口约定: %w", e.Name, err)
	}
	parsed := map[string]time.Time{}
	for name, layouts := range e.DateFormats {
		value, present := args[name]
		if !present {
			continue
		}
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("%s 必须是日期字符串", name)
		}
		valid := false
		for _, layout := range layouts {
			stamp, err := time.ParseInLocation(layout, text, time.FixedZone("Asia/Shanghai", 8*3600))
			if err == nil && stamp.Format(layout) == text {
				parsed[name], valid = stamp, true
				break
			}
		}
		if !valid {
			return fmt.Errorf("%s 日期格式或日期值无效", name)
		}
	}
	for _, pair := range [][2]string{{"start_time", "end_time"}, {"start_date", "end_date"}, {"start_date", "finish_date"}} {
		start, a := parsed[pair[0]]
		end, b := parsed[pair[1]]
		if a && b && start.After(end) {
			return fmt.Errorf("%s 不能晚于 %s", pair[0], pair[1])
		}
	}
	if e.Path == "/api/stock/forecast" {
		_, start := args["start_date"]
		_, finish := args["finish_date"]
		if start != finish {
			return fmt.Errorf("start_date 和 finish_date 必须成对提供")
		}
	}
	if strings.HasSuffix(e.Path, "/macd") {
		fast, slow := 12.0, 26.0
		if value, ok := args["fast_period"].(float64); ok {
			fast = value
		}
		if value, ok := args["slow_period"].(float64); ok {
			slow = value
		}
		if slow <= fast {
			return fmt.Errorf("slow_period 必须大于 fast_period（默认12/26）")
		}
		if e.Path == "/api/stock/macd" && (args["level"] == nil || args["level"] == "daily") {
			if len(args["start_time"].(string)) != 10 || len(args["end_time"].(string)) != 10 {
				return fmt.Errorf("日级 MACD 时间应使用 YYYY-MM-DD")
			}
		}
	}
	for _, key := range []string{"ma_periods", "mavol_periods"} {
		if text, ok := args[key].(string); ok {
			for _, part := range strings.Split(text, ",") {
				var n int
				if _, err := fmt.Sscanf(strings.TrimSpace(part), "%d", &n); err != nil || n < 1 || fmt.Sprint(n) != strings.TrimSpace(part) {
					return fmt.Errorf("%s 必须是正整数周期列表", key)
				}
			}
		}
	}
	return nil
}

// Register adds all 44 tools even when credentials are missing, so configuration errors are explicit.
func Register(server *mcp.Server, client *Client) {
	for _, endpoint := range Catalog() {
		e := endpoint
		annotations := &mcp.ToolAnnotations{ReadOnlyHint: !e.Download, IdempotentHint: !e.Download}
		destructive := false
		annotations.DestructiveHint = &destructive
		description := e.Title + "。" + e.Description + " 数据来源：蝶梦。保留原始字段和单位；分页只返回请求的一页。"
		if e.Download {
			description += "仅在明确要求全市场下载时调用，会在本机保存文件。每日期一天最多10次；不自动重试。"
		}
		mcp.AddTool(server, &mcp.Tool{Name: e.Name, Description: description, InputSchema: e.InputSchema, Annotations: annotations},
			func(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
				result, err := client.Call(ctx, e.Name, args)
				return nil, result, err
			})
	}
}
