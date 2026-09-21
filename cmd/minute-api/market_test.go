package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFixture(t *testing.T, root, rel, body string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func auctionRow(code, stamp, price string) string {
	return code + "," + stamp + ",10," + price + ",0,0,0,10,100,,0,10,100,,0,0,,0,"
}
func testDiscovery(t *testing.T, root string) localStore {
	t.Helper()
	s, err := discoverData(root)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func prepareFixtureIndex(t *testing.T, s *localStore, dir string) *auctionIndex {
	t.Helper()
	index, err := openAuctionIndex(s.root, dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := index.prepare(context.Background(), s.auctionFiles, func(string) {}); err != nil {
		index.close()
		t.Fatal(err)
	}
	s.auction = index
	t.Cleanup(index.close)
	return index
}

func TestIndexToolsAndConflicts(t *testing.T) {
	root := t.TempDir()
	header := "日期,时间,开盘,最高,最低,收盘,成交量,成交额\n"
	writeFixture(t, root, "分钟K线-指数/对应名称.csv", "index,code,name\n1,sh000001,上证指数\n2,sh999999,上证指数\n")
	writeFixture(t, root, "分钟K线-指数/2024/1分钟/sh000001.csv", header+"2024-12-31,15:00,1,2,1,2,0,100\n")
	second := writeFixture(t, root, "分钟K线-指数/2025/1分钟/sh000001.csv", header+"2025-01-02,09:31,2,3,2,3,0,200\n2024-12-31,15:00,1.0,2.0,1.0,2.0,0.0,100.0\n")
	writeFixture(t, root, "A股个股/2025/1分钟/sh000001.csv", strings.Join(headers, ",")+"\n"+historyRecord("2025-01-02 09:31:00", "99")+"\n")
	s := testDiscovery(t, root)
	h := handler(s, nil, nil)
	search := mcpCall(t, h, "search_indices", map[string]any{"query": "上证"}, false)
	if len(search["indices"].([]any)) != 1 {
		t.Fatal(search)
	}
	single := mcpCall(t, h, "get_index_bar", map[string]any{"symbol": "sh000001", "time": "2025-01-02T01:31:00Z"}, false)
	if single["bar"].(map[string]any)["close"] != json.Number("3") || single["source"] != "csv_index" {
		t.Fatal(single)
	}
	missing := mcpCall(t, h, "get_index_bar", map[string]any{"symbol": "sh000001", "time": "2025-01-02 09:30"}, false)
	if missing["found"] != false || missing["bar"] != nil {
		t.Fatal(missing)
	}
	rangeArgs := map[string]any{"symbol": "sh000001", "start_time": "2024-12-31 15:00", "end_time": "2025-01-02 09:31"}
	result := mcpCall(t, h, "get_index_bars", rangeArgs, false)
	if len(result["data"].([]any)) != 2 {
		t.Fatal(result)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/index/bars?symbol=sh000001&time=2025-01-02T09:31:00%2B08:00", nil))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := os.WriteFile(second, []byte(header+"2024-12-31,15:00,1,2,1,8,0,100\n"), 0600); err != nil {
		t.Fatal(err)
	}
	start, _ := minuteTime("2024-12-31 15:00")
	end, _ := minuteTime("2025-01-02 09:31")
	if _, err := s.indexBars("sh000001", "1m", start, end); err == nil || !strings.Contains(err.Error(), "2024/1分钟/sh000001.csv") || !strings.Contains(err.Error(), "2025/1分钟/sh000001.csv") {
		t.Fatal("conflict must identify both files", err)
	}
}

func TestAuctionIndexLifecycleAndTools(t *testing.T) {
	root := t.TempDir()
	head := strings.Join(auctionHeaders, ",") + "\r\n"
	first := writeFixture(t, root, "集合竞价/2025/a.csv", head+auctionRow("600941.SH", "2025-08-12 09:15:03", "10")+"\r\n"+auctionRow("000001.SZ", "2025-08-12 09:15:00", "11")+"\r\n"+auctionRow("600941.SH", "2025-08-12 09:15:00", "9")+"\r\n")
	writeFixture(t, root, "集合竞价/2025/b.csv", head+auctionRow("600941.SH", "2025-08-12 09:15:00", "9.0")+"\r\n")
	s := testDiscovery(t, root)
	dir := t.TempDir()
	index := prepareFixtureIndex(t, &s, dir)
	if err := index.ready(s.auctionFiles); err != nil {
		t.Fatal(err)
	}
	h := handler(s, nil, nil)
	args := map[string]any{"symbol": "sh600941", "start_time": "2025-08-12 09:15:00", "end_time": "2025-08-12 09:15:03", "page_size": 1}
	r := mcpCall(t, h, "get_auction_snapshots", args, false)
	if r["total"] != json.Number("2") || r["next_page"] != json.Number("1") {
		t.Fatal(r)
	}
	row := r["data"].([]any)[0].(map[string]any)
	if len(row) != 20 || row["current"] != json.Number("9.0") || row["volume"] != json.Number("0") || row["b2_p"] != nil || row["average_ask"] != nil {
		t.Fatal("raw fields or empty/zero values altered", row)
	}
	args["page"] = 1
	r = mcpCall(t, h, "get_auction_snapshots", args, false)
	if r["next_page"] != nil || r["data"].([]any)[0].(map[string]any)["time"] != "2025-08-12T09:15:03+08:00" {
		t.Fatal(r)
	}
	point := mcpCall(t, h, "get_auction_snapshot", map[string]any{"symbol": "sh600941", "time": "2025-08-12 09:15:01"}, false)
	if point["found"] != false || point["snapshot"] != nil {
		t.Fatal("must not select nearby snapshot")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/auction/snapshots?symbol=sh600941&time=2025-08-12T09:15:03%2B08:00", nil))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, q := range []string{"page=-1", "page_size=5001", "page_size=bad"} {
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/api/auction/snapshots?symbol=sh600941&time=2025-08-12T09:15:03%2B08:00&"+q, nil))
		if w.Code != 400 {
			t.Fatal(q, w.Code)
		}
	}
	var messages []string
	if err := index.prepare(context.Background(), s.auctionFiles, func(m string) { messages = append(messages, m) }); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || !strings.Contains(messages[0], "cached") {
		t.Fatal("unchanged files reindexed", messages)
	}
	if err := os.WriteFile(first, []byte(head+auctionRow("600941.SH", "2025-08-12 09:15:00", "12")+"\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	start, _ := minuteTime("2025-08-12 09:15")
	end := start.Add(time.Minute)
	if _, err := index.query(context.Background(), "sh600941", start, end, 0, 500); err == nil {
		t.Fatal("changed CSV used stale offsets")
	}
	updated := testDiscovery(t, root)
	if err := index.prepare(context.Background(), updated.auctionFiles, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := index.query(context.Background(), "sh600941", start, end, 0, 500); err == nil || !strings.Contains(err.Error(), "数据冲突") {
		t.Fatal("conflicting snapshots not rejected", err)
	}
	if err := os.Remove(filepath.Join(root, "集合竞价", "2025", "b.csv")); err != nil {
		t.Fatal(err)
	}
	updated = testDiscovery(t, root)
	if err := index.ready(updated.auctionFiles); err == nil {
		t.Fatal("deleted file not detected")
	}
	if err := index.prepare(context.Background(), updated.auctionFiles, func(string) {}); err != nil {
		t.Fatal(err)
	}
	result, err := index.query(context.Background(), "sh600941", start, end, 0, 500)
	if err != nil || result.Total != 1 {
		t.Fatal(result, err)
	}
	// Completed files remain usable after a cancelled build; a retry can continue.
	writeFixture(t, root, "集合竞价/2026/c.csv", head+auctionRow("600941.SH", "2026-01-05 09:15:00", "13")+"\r\n")
	updated = testDiscovery(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := index.prepare(ctx, updated.auctionFiles, func(string) {}); err == nil {
		t.Fatal("cancel ignored")
	}
	var count int
	if err := index.db.QueryRow("SELECT COUNT(*) FROM files").Scan(&count); err != nil || count != 1 {
		t.Fatal("completed checkpoint lost", count, err)
	}
	if err := index.prepare(context.Background(), updated.auctionFiles, func(string) {}); err != nil {
		t.Fatal(err)
	}
	readonly, err := openAuctionIndex(root, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer readonly.close()
	if err := readonly.ready(updated.auctionFiles); err != nil {
		t.Fatal(err)
	}
}

func TestAuctionMalformedFileDoesNotCommit(t *testing.T) {
	root := t.TempDir()
	head := strings.Join(auctionHeaders, ",") + "\n"
	writeFixture(t, root, "集合竞价/a.csv", head+auctionRow("600941.SH", "2025-08-12 09:15:00", "9")+"\n")
	writeFixture(t, root, "集合竞价/b.csv", head+auctionRow("600941.SH", "2025-08-12 09:15:03", "10")+"\nshort,row\n")
	s := testDiscovery(t, root)
	index, err := openAuctionIndex(root, t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer index.close()
	if err := index.prepare(context.Background(), s.auctionFiles, func(string) {}); err == nil {
		t.Fatal("malformed CSV accepted")
	}
	var count int
	if err := index.db.QueryRow("SELECT COUNT(*) FROM files").Scan(&count); err != nil || count != 1 {
		t.Fatal("partial file committed", count, err)
	}
}
