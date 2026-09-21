package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"go-stock/internal/diemeng"
)

func mcpRequest(t *testing.T, h http.Handler, method string, params any) json.RawMessage {
	t.Helper()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "http://127.0.0.1:18080/mcp", strings.NewReader(string(body)))
	req = req.WithContext(context.WithValue(req.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 18080}))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2025-06-18")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("%s: HTTP %d %s", method, w.Code, w.Body.String())
	}
	if w.Header().Get("Mcp-Session-Id") != "" {
		t.Fatal("unexpected stateful session")
	}
	var envelope struct {
		Result json.RawMessage
		Error  json.RawMessage
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Error) > 0 {
		t.Fatalf("protocol error: %s", envelope.Error)
	}
	return envelope.Result
}

func TestDiemengFallback(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "1分钟")
	if err := os.MkdirAll(folder, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(folder, "sh600941.csv")
	content := strings.Join(headers, ",") + "\n2026-08-12 13:45:00,96.43,96.43,96.41,96.41,22500,2169410,-0.02,-0.02074043347505963,0.002492334989894246,902767890,21687215000\n"
	if err := os.WriteFile(file, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api/stock/history" || r.Header.Get("apiKey") != "fixture-key" {
			t.Error("bad upstream request")
		}
		var args map[string]any
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			t.Error(err)
		}
		row := map[string]any{"trade_time": args["start_time"], "open": 10, "high": 11, "low": 9, "close": 10, "vol": 2, "amount": 2000}
		json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"total": 1, "list": []any{row}}})
	}))
	defer upstream.Close()
	provider, err := diemeng.NewClient(diemeng.Config{BaseURL: upstream.URL + "/api", APIKey: "fixture-key"})
	if err != nil {
		t.Fatal(err)
	}
	h := handler(fixtureStore(t, root), provider, nil)
	args := map[string]any{"symbol": "sh600941", "time": "2026-08-12 13:45"}
	local := mcpCall(t, h, "get_bar", args, false)
	if local["source"] != "csv" || local["adjustment"] != "unknown" || calls.Load() != 0 {
		t.Fatal("local preference failed", local)
	}
	partial := mcpCall(t, h, "get_bars", map[string]any{"symbol": "sh600941", "start_time": "2026-08-12 13:45", "end_time": "2026-08-12 13:46"}, false)
	if len(partial["data"].([]any)) != 1 || calls.Load() != 0 {
		t.Fatal("partial results must not be mixed")
	}
	args["time"] = "2026-08-12 13:44"
	fallback := mcpCall(t, h, "get_bar", args, false)
	if fallback["source"] != "diemeng" || fallback["adjustment"] != "none" || fallback["bar"].(map[string]any)["volume"] != json.Number("200") {
		t.Fatal(fallback)
	}
	args["symbol"] = "sh600000"
	mcpCall(t, h, "get_bar", args, false)
	args["symbol"] = "sh600941"
	args["time"] = "2026-08-12 13:45"
	args["source"] = "diemeng"
	explicit := mcpCall(t, h, "get_bar", args, false)
	if explicit["source"] != "diemeng" || calls.Load() != 3 {
		t.Fatal(explicit, calls.Load())
	}
	args["source"] = "local"
	args["time"] = "2026-08-12 13:44"
	notFound := mcpCall(t, h, "get_bar", args, false)
	if notFound["found"] != false || calls.Load() != 3 {
		t.Fatal("local-only must not fall back")
	}
	args["source"] = "wrong"
	mcpCall(t, h, "get_bar", args, true)
	args["source"] = "auto"
	if err := os.WriteFile(file, []byte("bad\n"), 0600); err != nil {
		t.Fatal(err)
	}
	mcpCall(t, h, "get_bar", args, true)
	if calls.Load() != 3 {
		t.Fatal("corrupt CSV must not silently fall back")
	}
}

func mcpCall(t *testing.T, h http.Handler, name string, arguments any, wantError bool) map[string]any {
	t.Helper()
	raw := mcpRequest(t, h, "tools/call", map[string]any{"name": name, "arguments": arguments})
	var result struct {
		IsError    bool                          `json:"isError"`
		Structured json.RawMessage               `json:"structuredContent"`
		Content    []struct{ Type, Text string } `json:"content"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.IsError != wantError {
		t.Fatalf("%s: %s", name, raw)
	}
	if len(result.Content) != 1 || result.Content[0].Type != "text" || result.Content[0].Text == "" {
		t.Fatalf("missing text: %s", raw)
	}
	if wantError {
		return nil
	}
	decode := func(data string) map[string]any {
		var value map[string]any
		d := json.NewDecoder(strings.NewReader(data))
		d.UseNumber()
		if err := d.Decode(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	value := decode(string(result.Structured))
	if !reflect.DeepEqual(value, decode(result.Content[0].Text)) {
		t.Fatal("structured/text mismatch")
	}
	return value
}

func TestMCP(t *testing.T) {
	root := t.TempDir()
	values := "96.43,96.43,96.41,96.41,22500,2169410,-0.02,-0.02074043347505963,0.002492334989894246,902767890,21687215000"
	content := strings.Join(headers, ",") + "\n"
	for _, stamp := range []string{"2026-08-13 00:00:00", "2026-08-12 13:46:00", "2026-08-12 13:45:00", "2026-08-12 00:00:00"} {
		content += stamp + "," + values + "\n"
	}
	for _, folder := range periods {
		if err := os.MkdirAll(filepath.Join(root, folder), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, folder, "sh600941.csv"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	h := handler(fixtureStore(t, root), nil, nil)
	init := mcpRequest(t, h, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"}})
	var initialized struct {
		ProtocolVersion string
		Capabilities    map[string]any
	}
	if err := json.Unmarshal(init, &initialized); err != nil {
		t.Fatal(err)
	}
	if initialized.ProtocolVersion != "2025-06-18" || initialized.Capabilities["tools"] == nil {
		t.Fatalf("initialize: %s", init)
	}
	list := mcpRequest(t, h, "tools/list", map[string]any{})
	var tools struct {
		Tools []struct {
			Name        string
			Annotations struct{ ReadOnlyHint bool }
			InputSchema struct{ Required []string }
		}
	}
	if err := json.Unmarshal(list, &tools); err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 51 {
		t.Fatalf("tools: %s", list)
	}
	for _, tool := range tools.Tools {
		if tool.Name != "get_bar" && tool.Name != "get_bars" {
			continue
		}
		if !tool.Annotations.ReadOnlyHint || (tool.Name != "get_bar" && tool.Name != "get_bars") {
			t.Fatalf("tool: %+v", tool)
		}
		if strings.Contains(strings.Join(tool.InputSchema.Required, ","), "period") {
			t.Fatal("period must be optional")
		}
	}
	for _, period := range []string{"", "1m", "5m", "15m", "30m", "60m"} {
		for _, stamp := range []string{"2026-08-12 13:45", "2026-08-12 13:45:00", "2026-08-12T13:45:00+08:00", "2026-08-12T05:45:00Z"} {
			args := map[string]any{"symbol": "sh600941", "time": stamp}
			if period != "" {
				args["period"] = period
			}
			got := mcpCall(t, h, "get_bar", args, false)
			if got["found"] != true || got["symbol"] != "sh600941" || got["timezone"] != "Asia/Shanghai" {
				t.Fatalf("bar: %+v", got)
			}
			wantPeriod := period
			if wantPeriod == "" {
				wantPeriod = "1m"
			}
			if got["period"] != wantPeriod {
				t.Fatal(got)
			}
			bar := got["bar"].(map[string]any)
			if bar["time"] != "2026-08-12T13:45:00+08:00" {
				t.Fatal(bar)
			}
			for i, value := range strings.Split(values, ",") {
				if bar[fields[i]] != json.Number(value) {
					t.Fatalf("%s: %v", fields[i], bar[fields[i]])
				}
			}
		}
	}
	for _, stamp := range []string{"2026-08-12 13:44", "2026-08-12 13:45:01", "2026-08-12T13:45:00.1+08:00"} {
		got := mcpCall(t, h, "get_bar", map[string]any{"symbol": "sh600941", "time": stamp}, false)
		if got["found"] != false || got["bar"] != nil {
			t.Fatal("must not use nearby bar", got)
		}
	}
	for _, tc := range []struct {
		start, end string
		times      []string
	}{
		{"2026-08-12 13:45", "2026-08-12 13:46", []string{"2026-08-12T13:45:00+08:00", "2026-08-12T13:46:00+08:00"}},
		{"2026-08-12 13:46", "2026-08-13 00:00", []string{"2026-08-12T13:46:00+08:00", "2026-08-13T00:00:00+08:00"}},
		{"2026-08-11T16:00:00Z", "2026-08-12T05:45:00Z", []string{"2026-08-12T00:00:00+08:00", "2026-08-12T13:45:00+08:00"}},
		{"2026-08-12 13:44", "2026-08-12 13:44", []string{}},
	} {
		got := mcpCall(t, h, "get_bars", map[string]any{"symbol": "sh600941", "start_time": tc.start, "end_time": tc.end}, false)
		rows, ok := got["data"].([]any)
		if !ok || len(rows) != len(tc.times) {
			t.Fatal(got)
		}
		for i, row := range rows {
			if row.(map[string]any)["time"] != tc.times[i] {
				t.Fatal(rows)
			}
		}
	}
	for _, args := range []map[string]any{
		{"symbol": "sh600941"}, {"symbol": "../sh600941", "time": "2026-08-12 13:45"},
		{"symbol": "sh600941", "time": "2026-02-30 13:45"}, {"symbol": "sh600941", "time": "2026-08-12T13:45:00"},
		{"symbol": "sh600941", "time": "2026-08-12 13:45", "period": "2m"}, {"symbol": "sh600000", "time": "2026-08-12 13:45"},
		{"symbol": 600941, "time": "2026-08-12 13:45"},
	} {
		mcpCall(t, h, "get_bar", args, true)
	}
	mcpCall(t, h, "get_bars", map[string]any{"symbol": "sh600941", "start_time": "2026-08-12 13:46", "end_time": "2026-08-12 13:45"}, true)
	if err := os.WriteFile(filepath.Join(root, "1分钟", "sh600941.csv"), []byte("bad\n"), 0600); err != nil {
		t.Fatal(err)
	}
	mcpCall(t, h, "get_bar", map[string]any{"symbol": "sh600941", "time": "2026-08-12 13:45"}, true)
	for _, method := range []string{"GET", "DELETE"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, "/mcp", nil))
		if w.Code != http.StatusMethodNotAllowed {
			t.Fatal(fmt.Sprintf("%s: %d", method, w.Code))
		}
	}
}
