package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"go-stock/internal/diemeng"
)

func newTestDailyStore(t *testing.T) *dailyStore {
	t.Helper()
	store, err := openDailyStore(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.close)
	return store
}

func bar(code, date string, pct float64) dailyBarRow {
	return dailyBarRow{Code: code, Date: date, Name: code, Open: 10, High: 11, Low: 9, Close: 10, PreClose: 10, PctChg: pct, Volume: 100, Amount: 1000}
}

func TestNormalizeSymbol(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"600000.SH", "sh600000"},
		{"000001.SZ", "sz000001"},
		{"920000.BJ", "bj920000"},
		{"sh600941", "sh600941"},
		{"SZ000001", "sz000001"},
		{"", ""},
		{"600000", ""},
		{"600000.XX", ""},
		{"60000.SH", ""},
		{"not-a-code", ""},
	} {
		if got := normalizeSymbol(tc.in); got != tc.want {
			t.Errorf("normalizeSymbol(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDailySchemaIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	for i := range 3 {
		store, err := openDailyStore(dir, true)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		store.close()
	}
	// 只读打开一个已存在的库
	store, err := openDailyStore(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	got, err := store.status()
	if err != nil {
		t.Fatal(err)
	}
	if got.Bars != 0 || got.Dates != 0 {
		t.Fatalf("fresh store should be empty: %+v", got)
	}
}

func TestOpenDailyStoreReadOnlyBootstraps(t *testing.T) {
	// 首次刷新前启动服务不应失败：只读打开应自动建好空库
	store, err := openDailyStore(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	rows, err := store.breadth("", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("want no rows, got %+v", rows)
	}
}

func TestPutBarsIsIdempotent(t *testing.T) {
	store := newTestDailyStore(t)
	first := []dailyBarRow{bar("sh600000", "2025-01-02", 1.5), bar("sz000001", "2025-01-02", -2.0)}
	if err := store.putBars("2025-01-02", first); err != nil {
		t.Fatal(err)
	}
	// 同一日期重复刷新：应替换而不是累加
	second := []dailyBarRow{bar("sh600000", "2025-01-02", 3.0)}
	if err := store.putBars("2025-01-02", second); err != nil {
		t.Fatal(err)
	}
	got, err := store.bars([]string{"sh600000"}, "2025-01-02", "2025-01-02")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].PctChg != 3.0 {
		t.Fatalf("want single replaced row with pct 3.0, got %+v", got)
	}
	all, err := store.status()
	if err != nil {
		t.Fatal(err)
	}
	if all.Bars != 1 {
		t.Fatalf("stale rows survived refresh: %+v", all)
	}
}

func TestBreadthAggregation(t *testing.T) {
	store := newTestDailyStore(t)
	if err := store.putBars("2025-01-02", []dailyBarRow{
		bar("sh600000", "2025-01-02", 5), bar("sh600001", "2025-01-02", -3),
		bar("sh600002", "2025-01-02", 0), bar("sh600003", "2025-01-02", 1.2),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.putBars("2025-01-03", []dailyBarRow{
		bar("sh600000", "2025-01-03", -1), bar("sh600001", "2025-01-03", -2),
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := store.breadth("2025-01-02", "2025-01-03")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 dates, got %+v", rows)
	}
	if rows[0].Date != "2025-01-02" || rows[0].Up != 2 || rows[0].Down != 1 || rows[0].Flat != 1 || rows[0].Total != 4 {
		t.Fatalf("day 1: %+v", rows[0])
	}
	if rows[1].Up != 0 || rows[1].Down != 2 || rows[1].Total != 2 {
		t.Fatalf("day 2: %+v", rows[1])
	}
	// 区间过滤
	only, err := store.breadth("2025-01-03", "2025-01-03")
	if err != nil {
		t.Fatal(err)
	}
	if len(only) != 1 || only[0].Total != 2 {
		t.Fatalf("range filter: %+v", only)
	}
}

func TestBarsFiltersAndSorts(t *testing.T) {
	store := newTestDailyStore(t)
	for _, date := range []string{"2025-01-06", "2025-01-02", "2025-01-03"} {
		if err := store.putBars(date, []dailyBarRow{bar("sh600000", date, 1), bar("sz000001", date, 1)}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.bars([]string{"sh600000"}, "2025-01-02", "2025-01-03")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Date != "2025-01-02" || got[1].Date != "2025-01-03" {
		t.Fatalf("want 2 ascending rows, got %+v", got)
	}
	both, err := store.bars([]string{"sh600000", "sz000001"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(both) != 6 {
		t.Fatalf("want 6 rows for 2 codes, got %d", len(both))
	}
	if _, err := store.bars(nil, "", ""); err == nil {
		t.Fatal("empty code list must be rejected")
	}
}

func TestRefreshLogDrivesResume(t *testing.T) {
	store := newTestDailyStore(t)
	if err := store.markRefreshed("2025-01-02", "ok", 10, "", "2025-01-02T18:00:00+08:00"); err != nil {
		t.Fatal(err)
	}
	if err := store.markRefreshed("2025-01-03", "failed", 0, "boom", "2025-01-03T18:00:00+08:00"); err != nil {
		t.Fatal(err)
	}
	done, err := store.refreshedDates()
	if err != nil {
		t.Fatal(err)
	}
	if done["2025-01-02"] != "ok" || done["2025-01-03"] != "failed" {
		t.Fatalf("unexpected ledger: %+v", done)
	}
	// 失败日期可被重跑覆盖
	if err := store.markRefreshed("2025-01-03", "ok", 5, "", "2025-01-04T09:00:00+08:00"); err != nil {
		t.Fatal(err)
	}
	status, err := store.status()
	if err != nil {
		t.Fatal(err)
	}
	if len(status.FailedDates) != 0 {
		t.Fatalf("failed dates should clear after success: %+v", status)
	}
	if status.FetchedAt == "" {
		t.Fatal("want last fetch time")
	}
}

func TestStatusReportsCoverage(t *testing.T) {
	store := newTestDailyStore(t)
	for _, date := range []string{"2025-01-02", "2025-01-03", "2025-01-06"} {
		if err := store.putBars(date, []dailyBarRow{bar("sh600000", date, 1)}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.status()
	if err != nil {
		t.Fatal(err)
	}
	if got.FirstDate != "2025-01-02" || got.LastDate != "2025-01-06" || got.Bars != 3 || got.Dates != 3 {
		t.Fatalf("coverage: %+v", got)
	}
}

func TestFetchDayPaginates(t *testing.T) {
	var pages atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/stock/daily" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		var args map[string]any
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			t.Error(err)
		}
		page := int(args["page"].(float64))
		pages.Add(1)
		list := []map[string]any{}
		switch page {
		case 0: // 模仿真实响应：total 大于单页返回数，必须继续翻页
			list = append(list, map[string]any{"trade_date": "2025-01-02", "stock_code": "600000.SH", "stock_name": "浦发银行",
				"open": 10, "high": 11, "low": 9, "close": 10.5, "pre_close": 10, "pct_chg": 5, "vol": 100, "amount": 1000})
		case 1:
			list = append(list, map[string]any{"trade_date": "2025-01-02", "stock_code": "000001.SZ",
				"open": 1, "high": 1, "low": 1, "close": 1, "pre_close": 1, "pct_chg": 0, "vol": 2, "amount": 3},
				map[string]any{"trade_date": "2025-01-02", "stock_code": "bogus", "open": 1})
		}
		json.NewEncoder(w).Encode(map[string]any{"code": 200, "msg": "成功",
			"data": map[string]any{"total": 2, "list": list}})
	}))
	defer upstream.Close()
	client, err := diemeng.NewClient(diemeng.Config{BaseURL: upstream.URL + "/api", APIKey: "fixture-key"})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := fetchDay(context.Background(), client, "2025-01-02")
	if err != nil {
		t.Fatal(err)
	}
	if pages.Load() != 2 {
		t.Fatalf("want 2 pages, got %d", pages.Load())
	}
	if len(rows) != 2 {
		t.Fatalf("unrecognized codes must be skipped, got %+v", rows)
	}
	if rows[0].Code != "sh600000" || rows[0].Name != "浦发银行" || rows[0].PctChg != 5 {
		t.Fatalf("row 0: %+v", rows[0])
	}
	if rows[1].Code != "sz000001" || rows[1].Date != "2025-01-02" {
		t.Fatalf("row 1: %+v", rows[1])
	}
}

func TestFetchDayRejectsEmpty(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"code": 200, "msg": "成功",
			"data": map[string]any{"total": 0, "list": []any{}}})
	}))
	defer upstream.Close()
	client, err := diemeng.NewClient(diemeng.Config{BaseURL: upstream.URL + "/api", APIKey: "fixture-key"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fetchDay(context.Background(), client, "2025-01-02"); err == nil {
		t.Fatal("empty day must be an error so it is not recorded as ok")
	}
}

func TestTradingDays(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/basic/calendar" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"code": 200, "msg": "成功", "data": []any{
			map[string]any{"date": "2025-01-01", "is_open": 0},
			map[string]any{"date": "2025-01-02", "is_open": 1},
			map[string]any{"date": "2025-01-03", "is_open": 1},
			map[string]any{"date": "2025-01-04", "is_open": 0},
		}})
	}))
	defer upstream.Close()
	client, err := diemeng.NewClient(diemeng.Config{BaseURL: upstream.URL + "/api", APIKey: "fixture-key"})
	if err != nil {
		t.Fatal(err)
	}
	days, err := tradingDays(context.Background(), client, "2025-01-01", "2025-01-04")
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 2 || days[0] != "2025-01-02" || days[1] != "2025-01-03" {
		t.Fatalf("holidays must be filtered: %+v", days)
	}
}

func TestBreadthUsesCoveringIndex(t *testing.T) {
	// 涨跌家数聚合是这套工具最核心的查询。单列 (trade_date) 索引会退回主表逐行查找，
	// 实测 4.8M 行时 15.8s vs 0.25s。用查询计划把这个性能特征钉住，防止被改回去。
	store := newTestDailyStore(t)
	if err := store.putBars("2025-01-02", []dailyBarRow{bar("sh600000", "2025-01-02", 1)}); err != nil {
		t.Fatal(err)
	}
	query, args := breadthQuery("2025-01-01", "2025-12-31")
	rows, err := store.db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	plan := ""
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail + "\n"
	}
	if !strings.Contains(plan, "COVERING INDEX idx_daily_bar_date_pct") {
		t.Fatalf("breadth query must use the covering index, got plan:\n%s", plan)
	}
}

func TestNormalizeSymbolMatchesPattern(t *testing.T) {
	// 归一化结果必须能被 symbolPattern 接受，否则后续无法与分钟线 join
	for _, in := range []string{"600000.SH", "000001.SZ", "920000.BJ"} {
		if got := normalizeSymbol(in); !symbolPattern.MatchString(got) {
			t.Fatalf("normalizeSymbol(%q)=%q fails symbolPattern", in, got)
		}
	}
}

func TestDailyMCPTools(t *testing.T) {
	daily := newTestDailyStore(t)
	if err := daily.putBars("2025-01-02", []dailyBarRow{
		bar("sh600000", "2025-01-02", 5), bar("sh600001", "2025-01-02", -3),
		bar("sh600002", "2025-01-02", 0),
	}); err != nil {
		t.Fatal(err)
	}
	if err := daily.putBars("2025-01-03", []dailyBarRow{bar("sh600000", "2025-01-03", 1)}); err != nil {
		t.Fatal(err)
	}
	if err := daily.markRefreshed("2025-01-02", "ok", 3, "", "2025-01-02T18:00:00+08:00"); err != nil {
		t.Fatal(err)
	}
	h := handler(fixtureStore(t, t.TempDir()), nil, daily)

	// 工具必须在 tools/list 中可见
	list := mcpRequest(t, h, "tools/list", map[string]any{})
	text := string(list)
	for _, name := range []string{"get_refresh_status", "get_market_breadth", "get_daily_bars"} {
		if !strings.Contains(text, name) {
			t.Fatalf("%s not registered: %s", name, text)
		}
	}

	status := mcpCall(t, h, "get_refresh_status", map[string]any{}, false)
	if status["first_date"] != "2025-01-02" || status["last_date"] != "2025-01-03" {
		t.Fatalf("status: %+v", status)
	}
	if status["bars"] != json.Number("4") || status["dates"] != json.Number("2") {
		t.Fatalf("status counts: %+v", status)
	}

	breadth := mcpCall(t, h, "get_market_breadth", map[string]any{"start_date": "2025-01-02", "end_date": "2025-01-03"}, false)
	if breadth["source"] != "sqlite_local" {
		t.Fatalf("breadth source: %+v", breadth)
	}
	rows := breadth["data"].([]any)
	if len(rows) != 2 {
		t.Fatalf("breadth rows: %+v", rows)
	}
	day1 := rows[0].(map[string]any)
	if day1["up"] != json.Number("1") || day1["down"] != json.Number("1") || day1["flat"] != json.Number("1") || day1["total"] != json.Number("3") {
		t.Fatalf("day1 breadth: %+v", day1)
	}

	bars := mcpCall(t, h, "get_daily_bars", map[string]any{"stock_codes": []string{"600000.SH"}, "start_date": "2025-01-02", "end_date": "2025-01-02"}, false)
	if bars["source"] != "sqlite_local" {
		t.Fatalf("bars source: %+v", bars)
	}
	data := bars["data"].([]any)
	if len(data) != 1 || data[0].(map[string]any)["trade_date"] != "2025-01-02" {
		t.Fatalf("bars data: %+v", data)
	}
	// 蝶梦风格的代码应被归一化成本地约定的 sh600000
	if symbols := bars["symbols"].([]any); symbols[0] != "sh600000" {
		t.Fatalf("symbol normalization: %+v", symbols)
	}

	mcpCall(t, h, "get_daily_bars", map[string]any{"stock_codes": []string{}}, true)
	mcpCall(t, h, "get_daily_bars", map[string]any{"stock_codes": []string{"bogus"}}, true)
}

func TestDailyToolsAbsentWhenStoreMissing(t *testing.T) {
	// daily 为 nil 时不应注册这些工具，且服务仍能正常启动
	daily, err := openDailyStore(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	daily.close()
	h := handler(fixtureStore(t, t.TempDir()), nil, nil)
	list := string(mcpRequest(t, h, "tools/list", map[string]any{}))
	if strings.Contains(list, "get_market_breadth") {
		t.Fatal("daily tools must not register without a store")
	}
	if !strings.Contains(list, "get_bar") {
		t.Fatal("existing tools must still register")
	}
}
