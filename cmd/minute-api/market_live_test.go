//go:build integration

package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLiveMarketDatasets(t *testing.T) {
	if os.Getenv("GO_STOCK_MARKET_LIVE") != "1" {
		t.Skip("set GO_STOCK_MARKET_LIVE=1 for explicit local/public verification")
	}
	address, err := os.ReadFile(`H:\Download\go-stock-minute-api\mcp-url.txt`)
	if err != nil {
		t.Fatal(err)
	}
	public := strings.TrimSpace(string(address))
	if !strings.HasPrefix(public, "https://") || !strings.HasSuffix(public, ".trycloudflare.com/mcp") {
		t.Fatal("unexpected MCP URL")
	}
	root := filepath.Join("..", "..", "A股历史分钟线数据包")
	sample := func(rel string) ([]string, []string, []string) {
		f, err := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		r := csv.NewReader(f)
		h, err := r.Read()
		if err != nil {
			t.Fatal(err)
		}
		a, err := r.Read()
		if err != nil {
			t.Fatal(err)
		}
		b, err := r.Read()
		if err != nil {
			t.Fatal(err)
		}
		return h, a, b
	}
	_, stock, _ := sample("A股个股/2026_20260911_173537/2026/1分钟/sh600941.csv")
	_, indexA, indexB := sample("分钟K线-指数/2026/1分钟/sh000001.csv")
	_, auctionA, auctionB := sample("集合竞价/2026/tick_3s_2026-01-01_2026-01-30.csv")
	_, oldAuction, _ := sample("集合竞价/2020-2024/2023/tick_3s_2023-01-01_2023-01-31_1.csv")
	checkNumber := func(actual any, want string) {
		if want == "" {
			if actual != nil {
				t.Fatal("empty field not null")
			}
			return
		}
		x, ok := new(big.Rat).SetString(fmt.Sprint(actual))
		y, valid := new(big.Rat).SetString(want)
		if !ok || !valid || x.Cmp(y) != 0 {
			t.Fatalf("number mismatch: %v != %s", actual, want)
		}
	}
	checkAuction := func(row map[string]any, want []string) {
		if len(row) != 20 || row["code"] != want[0] || row["trade_date"] != want[1] {
			t.Fatal("auction original fields missing", row)
		}
		for i := 2; i < len(want); i++ {
			checkNumber(row[auctionHeaders[i]], want[i])
		}
	}
	for _, endpoint := range []string{"http://127.0.0.1:18080/mcp", public} {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.Proxy = nil
		if strings.HasPrefix(endpoint, "https:") {
			p, _ := url.Parse("http://127.0.0.1:7890")
			tr.Proxy = http.ProxyURL(p)
		}
		client := &http.Client{Transport: tr, Timeout: 60 * time.Second}
		doJSON := func(method, address string, body any) map[string]any {
			var encoded []byte
			if body != nil {
				encoded, _ = json.Marshal(body)
			}
			req, err := http.NewRequest(method, address, bytes.NewReader(encoded))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			req.Header.Set("MCP-Protocol-Version", "2025-06-18")
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Fatalf("HTTP %d", resp.StatusCode)
			}
			var value map[string]any
			d := json.NewDecoder(resp.Body)
			d.UseNumber()
			if err := d.Decode(&value); err != nil {
				t.Fatal(err)
			}
			return value
		}
		rpc := func(method string, params any) map[string]any {
			v := doJSON("POST", endpoint, map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
			if v["error"] != nil {
				t.Fatal(v["error"])
			}
			return v["result"].(map[string]any)
		}
		rpc("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "data-expansion-verification", "version": "1"}})
		tools := rpc("tools/list", map[string]any{})["tools"].([]any)
		if len(tools) != 51 {
			t.Fatal("tool count", len(tools))
		}
		call := func(name string, args map[string]any) map[string]any {
			r := rpc("tools/call", map[string]any{"name": name, "arguments": args})
			if r["isError"] == true {
				t.Fatal(name, r["content"])
			}
			return r["structuredContent"].(map[string]any)
		}
		stockResult := call("get_bar", map[string]any{"symbol": "sh600941", "time": stock[0], "source": "local"})
		if stockResult["source"] != "csv" {
			t.Fatal(stockResult)
		}
		bar := stockResult["bar"].(map[string]any)
		for i, key := range fields {
			checkNumber(bar[key], stock[i+1])
		}
		matches := call("search_indices", map[string]any{"query": "上证指数"})["indices"].([]any)
		found := false
		for _, v := range matches {
			if v.(map[string]any)["symbol"] == "sh000001" {
				found = true
			}
		}
		if !found {
			t.Fatal("index name missing")
		}
		idxTime := indexA[0] + " " + indexA[1]
		idx := call("get_index_bar", map[string]any{"symbol": "sh000001", "time": idxTime})
		if idx["source"] != "csv_index" || idx["found"] != true {
			t.Fatal(idx)
		}
		bar = idx["bar"].(map[string]any)
		for i, key := range []string{"open", "high", "low", "close", "volume", "amount"} {
			checkNumber(bar[key], indexA[i+2])
		}
		idxRange := call("get_index_bars", map[string]any{"symbol": "sh000001", "start_time": idxTime, "end_time": indexB[0] + " " + indexB[1]})
		if len(idxRange["data"].([]any)) != 2 {
			t.Fatal("index range boundary mismatch")
		}
		for _, want := range [][]string{auctionA, oldAuction} {
			shot := call("get_auction_snapshot", map[string]any{"symbol": "sz000001", "time": want[1]})
			if shot["found"] != true || shot["source"] != "csv_auction" {
				t.Fatal(shot)
			}
			checkAuction(shot["snapshot"].(map[string]any), want)
		}
		args := map[string]any{"symbol": "sz000001", "start_time": auctionA[1], "end_time": auctionB[1], "page_size": 1}
		page := call("get_auction_snapshots", args)
		if page["total"] != json.Number("2") || page["next_page"] != json.Number("1") {
			t.Fatal(page)
		}
		checkAuction(page["data"].([]any)[0].(map[string]any), auctionA)
		args["page"] = 1
		page = call("get_auction_snapshots", args)
		if page["next_page"] != nil {
			t.Fatal(page)
		}
		checkAuction(page["data"].([]any)[0].(map[string]any), auctionB)
		base := strings.TrimSuffix(endpoint, "/mcp")
		httpIndex := doJSON("GET", base+"/api/index/bars?"+url.Values{"symbol": {"sh000001"}, "time": {idxTime}}.Encode(), nil)
		if len(httpIndex["data"].([]any)) != 1 {
			t.Fatal(httpIndex)
		}
		httpAuction := doJSON("GET", base+"/api/auction/snapshots?"+url.Values{"symbol": {"sz000001"}, "time": {auctionA[1]}}.Encode(), nil)
		checkAuction(httpAuction["data"].([]any)[0].(map[string]any), auctionA)
		if len(doJSON("GET", base+"/api/indices?query=sh000001", nil)["indices"].([]any)) != 1 {
			t.Fatal("HTTP index search failed")
		}
		t.Logf("PASS %s: 51 tools; stock 11 fields; index 6 values; 2023/2026 auction 19 fields; pagination and HTTP routes", endpoint)
		client.CloseIdleConnections()
	}
}
