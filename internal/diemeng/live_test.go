//go:build integration

package diemeng

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPublicMCP(t *testing.T) {
	if os.Getenv("GO_STOCK_MCP_LIVE") != "1" {
		t.Skip("set GO_STOCK_MCP_LIVE=1 for running local/public MCP verification")
	}
	address, err := os.ReadFile(`H:\Download\go-stock-minute-api\mcp-url.txt`)
	if err != nil {
		t.Fatal(err)
	}
	public := strings.TrimSpace(string(address))
	if !strings.HasPrefix(public, "https://") || !strings.HasSuffix(public, ".trycloudflare.com/mcp") {
		t.Fatal("unexpected public MCP URL")
	}
	for _, endpoint := range []string{"http://127.0.0.1:18080/mcp", public} {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		if strings.HasPrefix(endpoint, "https:") {
			p, _ := url.Parse("http://127.0.0.1:7890")
			transport.Proxy = http.ProxyURL(p)
		}
		client := &http.Client{Transport: transport, Timeout: 60 * time.Second}
		rpc := func(method string, params any) map[string]any {
			body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
			req, _ := http.NewRequest("POST", endpoint, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			req.Header.Set("MCP-Protocol-Version", "2025-06-18")
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal("MCP network request failed")
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Fatalf("MCP HTTP %d", resp.StatusCode)
			}
			var message struct {
				Result map[string]any
				Error  any
			}
			dec := json.NewDecoder(resp.Body)
			dec.UseNumber()
			if err := dec.Decode(&message); err != nil {
				t.Fatal(err)
			}
			if message.Error != nil {
				t.Fatal("MCP protocol error")
			}
			return message.Result
		}
		rpc("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "stock-integration-probe", "version": "1"}})
		list := rpc("tools/list", map[string]any{})["tools"].([]any)
		if len(list) != 51 {
			t.Fatalf("expected 51 tools, got %d", len(list))
		}
		writes := 0
		for _, entry := range list {
			tool := entry.(map[string]any)
			annotations := tool["annotations"].(map[string]any)
			if annotations["readOnlyHint"] != true {
				writes++
				if tool["name"] != "diemeng_stock_daily_dump" {
					t.Fatal("unexpected write tool")
				}
			}
		}
		if writes != 1 {
			t.Fatal("download write annotation missing")
		}
		call := func(name string, args map[string]any) map[string]any {
			result := rpc("tools/call", map[string]any{"name": name, "arguments": args})
			if result["isError"] == true {
				t.Fatalf("tool %s returned a tool error", name)
			}
			return result["structuredContent"].(map[string]any)
		}
		args := map[string]any{"symbol": "sh600941", "time": "2026-08-12 13:45"}
		local := call("get_bar", args)
		if local["source"] != "csv" || local["found"] != true {
			t.Fatal("local preference not preserved")
		}
		bar := local["bar"].(map[string]any)
		for key, want := range map[string]string{"open": "96.43", "high": "96.43", "low": "96.41", "close": "96.41", "volume": "22500", "amount": "2169410", "change": "-0.02", "change_pct": "-0.02074043347505963", "turnover_rate_pct": "0.002492334989894246", "float_shares": "902767890", "total_shares": "21687215000"} {
			if fmt.Sprint(bar[key]) != want {
				t.Fatalf("local field mismatch: %s", key)
			}
		}
		args["source"] = "diemeng"
		remote := call("get_bar", args)
		if remote["source"] != "diemeng" || remote["adjustment"] != "none" || remote["found"] != true {
			t.Fatal("provider minute unavailable or wrong source")
		}
		raw := call("diemeng_stock_history", map[string]any{"stock_code": "600941.SH", "level": "1min", "start_time": "2026-08-12 13:45:00", "end_time": "2026-08-12 13:45:00", "page": 0, "page_size": 1})
		payload := raw["response"].(map[string]any)["data"].(map[string]any)
		items, ok := payload["list"].([]any)
		if !ok {
			items = payload["items"].([]any)
		}
		if len(items) != 1 {
			t.Fatal("expected one provider row")
		}
		providerRow := items[0].(map[string]any)
		normalized := remote["bar"].(map[string]any)
		volume, ok := new(big.Rat).SetString(fmt.Sprint(providerRow["vol"]))
		if !ok {
			t.Fatal("bad provider volume")
		}
		volume.Mul(volume, big.NewRat(100, 1))
		if fmt.Sprint(normalized["volume"]) != volume.Num().String() {
			t.Fatal("volume unit mismatch")
		}
		for _, key := range []string{"open", "high", "low", "close", "amount"} {
			a, aOK := new(big.Rat).SetString(fmt.Sprint(normalized[key]))
			b, bOK := new(big.Rat).SetString(fmt.Sprint(providerRow[key]))
			if !aOK || !bOK || a.Cmp(b) != 0 {
				t.Fatalf("provider normalization mismatch %s: %v vs %v", key, normalized[key], providerRow[key])
			}
		}
		t.Logf("PASS %s: 51 tools, CSV 11 fields, provider minute source and units", endpoint)
		transport.CloseIdleConnections()
	}
	if report := os.Getenv("GO_STOCK_MCP_REPORT"); report != "" {
		f, err := os.Create(report)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		io.WriteString(f, public+"\n51 tools; local CSV 11 fields; Diemeng single-minute and volume conversion: PASS\n")
	}
}

// TestLiveSamples is an explicit opt-in provider probe, excluded from fast/domain/release.
func TestLiveSamples(t *testing.T) {
	if os.Getenv("GO_STOCK_DIEMENG_LIVE") != "1" {
		t.Skip("set GO_STOCK_DIEMENG_LIVE=1 for live samples")
	}
	c, err := Load(DefaultConfigPath())
	if err != nil || c == nil {
		t.Fatal("private configuration unavailable")
	}
	type observation struct {
		Tool   string `json:"tool"`
		Status string `json:"status"`
		Detail string `json:"detail,omitempty"`
	}
	report := make([]observation, 0, 44)
	stock := map[string]any{"stock_code": "600941.SH", "start_time": "2026-08-12", "end_time": "2026-08-12", "page": 0, "page_size": 1}
	samples := map[string]map[string]any{
		"diemeng_basic_calendar":               {"start_time": "2026-08-12", "end_time": "2026-08-12"},
		"diemeng_stock_history":                {"stock_code": "600941.SH", "level": "1min", "start_time": "2026-08-12 13:45:00", "end_time": "2026-08-12 13:45:00", "page": 0, "page_size": 1},
		"diemeng_stock_income":                 {"stock_code": []string{"600941.SH"}, "end_date": "2025-12-31", "page": 0, "page_size": 1},
		"diemeng_stock_finance":                stock,
		"diemeng_stock_daily":                  stock,
		"diemeng_stock_suspension":             {"stock_code": "600941.SH", "trade_date": "2026-08-12", "page": 0, "page_size": 1},
		"diemeng_stock_ma":                     {"stock_code": "600941.SH", "level": "1min", "start_time": "2026-08-12 13:45:00", "end_time": "2026-08-12 13:45:00", "ma_periods": []int{5}, "page": 0, "page_size": 1},
		"diemeng_index_daily":                  {"stock_code": "000001.SH", "start_date": "2026-08-12", "end_date": "2026-08-12", "page": 0, "page_size": 1},
		"diemeng_index_weight":                 {"index_code": "000300.SH", "stock_code": "600941.SH", "trade_date": "2026-08", "page": 0, "page_size": 1},
		"diemeng_tdx_block_stocks":             {"stock_code": "600941.SH", "page": 0, "page_size": 1},
		"diemeng_dc_block_stocks":              {"stock_code": "600941.SH", "trade_date": "2026-08-12", "page": 0, "page_size": 1},
		"diemeng_index_ths_constituent_stocks": {"stock_code": "600941.SH", "page": 0, "page_size": 1},
	}
	for _, e := range Catalog() {
		item := observation{Tool: e.Name, Status: "尚未实测"}
		args, ok := samples[e.Name]
		if ok {
			result, err := c.Call(context.Background(), e.Name, args)
			if err != nil {
				item.Status = "调用失败"
				item.Detail = err.Error()
				if strings.Contains(item.Detail, "401") || strings.Contains(item.Detail, "403") || strings.Contains(item.Detail, "权限") {
					item.Status = "权限受限"
				}
			} else {
				item.Status = "已验证"
				var body struct{ Data any }
				json.Unmarshal(result.Response, &body)
				item.Detail = "单页请求成功（空结果也表示调用成功，不保证该日期存在记录）"
			}
		}
		if e.Download {
			item.Detail = "仅模拟验证，未消耗真实下载次数"
		}
		report = append(report, item)
		t.Logf("%s: %s %s", item.Tool, item.Status, item.Detail)
	}
	if path := os.Getenv("GO_STOCK_DIEMENG_REPORT"); path != "" {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
