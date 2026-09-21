//go:build integration

package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLiveHistory only reads existing CSVs and the running API after explicit opt-in.
func TestLiveHistory(t *testing.T) {
	if os.Getenv("GO_STOCK_HISTORY_LIVE") != "1" {
		t.Skip("set GO_STOCK_HISTORY_LIVE=1 to test the running service")
	}
	address, err := os.ReadFile(`H:\Download\go-stock-minute-api\mcp-url.txt`)
	if err != nil {
		t.Fatal(err)
	}
	public := strings.TrimSpace(string(address))
	if !strings.HasPrefix(public, "https://") || !strings.HasSuffix(public, ".trycloudflare.com/mcp") {
		t.Fatal("unexpected MCP address")
	}
	root := filepath.Join("..", "..", "A股历史分钟线数据包", "A股个股")
	paths := []string{filepath.Join(root, "1分钟(2000-2025)", "1分钟", "sh600941.csv"), filepath.Join(root, "2026_20260911_173537", "2026", "1分钟", "sh600941.csv")}
	for _, tc := range []struct{ name, start, end string }{
		{"historical", "2025-08-12 13:45:00", "2025-08-12 13:45:00"},
		{"cross-year", "2025-12-31 15:00:00", "2026-01-05 09:30:00"},
	} {
		start, _ := time.ParseInLocation("2006-01-02 15:04:05", tc.start, beijing)
		end, _ := time.ParseInLocation("2006-01-02 15:04:05", tc.end, beijing)
		want := map[string]map[string]string{}
		for _, path := range paths {
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			reader := csv.NewReader(f)
			reader.ReuseRecord = true
			if _, err := reader.Read(); err != nil {
				f.Close()
				t.Fatal(err)
			}
			for {
				row, err := reader.Read()
				if err == io.EOF {
					break
				}
				if err != nil {
					f.Close()
					t.Fatal(err)
				}
				if row[0] < tc.start || row[0] > tc.end {
					continue
				}
				stamp, err := time.ParseInLocation("2006-01-02 15:04:05", row[0], beijing)
				if err != nil {
					f.Close()
					t.Fatal(err)
				}
				values := map[string]string{}
				for i, key := range fields {
					values[key] = row[i+1]
				}
				want[stamp.Format(time.RFC3339)] = values
			}
			f.Close()
		}
		if len(want) == 0 || (tc.name == "cross-year" && len(want) < 2) {
			t.Fatal("real CSV sample missing")
		}
		compare := func(rows []map[string]any) {
			matched := 0
			seen := map[string]bool{}
			for _, row := range rows {
				stampText := row["time"].(string)
				stamp, err := time.Parse(time.RFC3339, stampText)
				if err != nil {
					t.Fatal(err)
				}
				if stamp.Before(start) || stamp.After(end) {
					continue
				}
				values, ok := want[stampText]
				if !ok || seen[stampText] {
					t.Fatal("unexpected or duplicate timestamp", stampText)
				}
				seen[stampText] = true
				matched++
				for _, key := range fields {
					actual, aOK := new(big.Rat).SetString(string(row[key].(json.Number)))
					expected, bOK := new(big.Rat).SetString(values[key])
					if !aOK || !bOK || actual.Cmp(expected) != 0 {
						t.Fatal("field mismatch", stampText, key)
					}
				}
			}
			if matched != len(want) {
				t.Fatalf("matched %d of %d", matched, len(want))
			}
		}
		localClient := &http.Client{Transport: &http.Transport{}, Timeout: 60 * time.Second}
		httpURL := "http://127.0.0.1:18080/api/bars?symbol=sh600941&period=1m&start=" + tc.start[:10] + "&end=" + tc.end[:10]
		started := time.Now()
		resp, err := localClient.Get(httpURL)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			t.Fatal("HTTP API failed", resp.StatusCode)
		}
		var api response
		decoder := json.NewDecoder(resp.Body)
		decoder.UseNumber()
		err = decoder.Decode(&api)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		compare(api.Data)
		t.Logf("%s local HTTP: %d matched, %s", tc.name, len(want), time.Since(started).Round(time.Millisecond))
		localClient.CloseIdleConnections()
		for _, endpoint := range []string{"http://127.0.0.1:18080/mcp", public} {
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.Proxy = nil
			if strings.HasPrefix(endpoint, "https:") {
				proxy, _ := url.Parse("http://127.0.0.1:7890")
				transport.Proxy = http.ProxyURL(proxy)
			}
			client := &http.Client{Transport: transport, Timeout: 60 * time.Second}
			name := "get_bars"
			args := map[string]any{"symbol": "sh600941", "source": "local", "start_time": tc.start, "end_time": tc.end}
			if tc.name == "historical" {
				name = "get_bar"
				args = map[string]any{"symbol": "sh600941", "time": tc.start}
			}
			body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
			req, _ := http.NewRequest("POST", endpoint, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			req.Header.Set("MCP-Protocol-Version", "2025-06-18")
			started = time.Now()
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != 200 {
				resp.Body.Close()
				t.Fatal("MCP HTTP failed", resp.StatusCode)
			}
			var message struct {
				Error  any
				Result struct {
					IsError    bool
					Structured struct {
						Source, Adjustment string
						Found              bool
						Bar                map[string]any
						Data               []map[string]any
					} `json:"structuredContent"`
				}
			}
			decoder = json.NewDecoder(resp.Body)
			decoder.UseNumber()
			err = decoder.Decode(&message)
			resp.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if message.Error != nil || message.Result.IsError {
				t.Fatal("MCP tool failed")
			}
			value := message.Result.Structured
			if value.Source != "csv" || value.Adjustment != "unknown" {
				t.Fatal("expected CSV source", value.Source)
			}
			rows := value.Data
			if name == "get_bar" {
				if !value.Found {
					t.Fatal("historical bar not found")
				}
				rows = []map[string]any{value.Bar}
			}
			compare(rows)
			t.Logf("%s %s: %d rows, all 11 fields match, %s", tc.name, endpoint, len(rows), time.Since(started).Round(time.Millisecond))
			client.CloseIdleConnections()
		}
	}
}
