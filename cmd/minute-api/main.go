package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strings"
	"time"

	"go-stock/internal/diemeng"
)

var symbolPattern = regexp.MustCompile(`^(sh|sz|bj)[0-9]{6}$`)
var periods = map[string]string{"1m": "1分钟", "5m": "5分钟", "15m": "15分钟", "30m": "30分钟", "60m": "60分钟"}
var headers = []string{"日期", "开盘", "最高", "最低", "收盘", "成交量(股)", "成交额(元)", "涨跌(元)", "涨跌幅(%)", "换手率(%)", "流通股本(股)", "总股本(股)"}
var fields = []string{"open", "high", "low", "close", "volume", "amount", "change", "change_pct", "turnover_rate_pct", "float_shares", "total_shares"}
var beijing = time.FixedZone("Asia/Shanghai", 8*60*60)

type response struct {
	Symbol   string           `json:"symbol"`
	Period   string           `json:"period"`
	Timezone string           `json:"timezone"`
	Data     []map[string]any `json:"data"`
}

func main() {
	root := flag.String("data-root", "", "包含A股个股、分钟K线-指数、集合竞价的数据包根目录")
	indexDir := flag.String("index-dir", `H:\Download\go-stock-minute-api\auction-index`, "竞价位置索引目录")
	prepare := flag.Bool("prepare-data", false, "完成全部竞价索引后退出，不启动服务")
	doRefreshDaily := flag.Bool("refresh-daily", false, "从蝶梦物化全市场日线到 daily.sqlite 后退出，不启动服务")
	from := flag.String("from", "", "-refresh-daily 的起始日期 YYYY-MM-DD")
	to := flag.String("to", "", "-refresh-daily 的结束日期 YYYY-MM-DD")
	privateConfig := flag.String("diemeng-config", diemeng.DefaultConfigPath(), "蝶梦私有配置文件（不包含在仓库中）")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	// 日线刷新只依赖蝶梦与 index-dir，先于数据发现处理，避免为刷新扫描整个 CSV 数据包。
	if *doRefreshDaily {
		if *from == "" || *to == "" {
			log.Fatal("-refresh-daily 需要同时指定 -from 和 -to")
		}
		daily, err := openDailyStore(*indexDir, true)
		if err != nil {
			log.Fatal(err)
		}
		defer daily.close()
		client, err := diemeng.Load(*privateConfig)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("本地日线库: %s", daily.path())
		report, err := refreshDaily(ctx, daily, client, *from, *to, log.Printf)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("刷新完成：交易日 %d，新写入 %d，跳过 %d，失败 %d，共 %d 行\n",
			report.Total, report.OK, report.Skipped, report.Failed, report.Rows)
		return
	}
	if *root == "" {
		log.Fatal("请指定 -data-root")
	}
	store, err := discoverData(*root)
	if err != nil {
		log.Fatal(err)
	}
	index, err := openAuctionIndex(store.root, *indexDir, *prepare)
	if err != nil {
		log.Fatal(err)
	}
	defer index.close()
	if *prepare {
		if err := index.prepare(ctx, store.auctionFiles, func(message string) { fmt.Println(message) }); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("READY: stock series=%d, index series=%d, auction files=%d\n", len(store.stocks), len(store.indices), len(store.auctionFiles))
		return
	}
	if err := index.ready(store.auctionFiles); err != nil {
		log.Fatal(err)
	}
	store.auction = index
	daily, err := openDailyStore(*indexDir, false)
	if err != nil {
		log.Fatal(err)
	}
	defer daily.close()
	if daily.status(); err == nil {
		log.Printf("本地日线库: %s", daily.path())
	}
	provider, err := diemeng.Load(*privateConfig)
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Addr: "127.0.0.1:18080", Handler: handler(store, provider, daily), ReadHeaderTimeout: 5 * time.Second}
	log.Printf("market API: http://%s", server.Addr)
	log.Fatal(server.ListenAndServe())
}

func handler(store localStore, provider *diemeng.Client, daily *dailyStore) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpHandler(store, provider, daily))
	registerMarketHTTP(mux, store)
	mux.HandleFunc("GET /api/bars", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		symbol, period := q.Get("symbol"), q.Get("period")
		period, err := normalizePeriod(symbol, period)
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": "股票代码或周期无效"})
			return
		}
		start, errStart := date(q.Get("start"))
		end, errEnd := date(q.Get("end"))
		if errStart != nil || errEnd != nil || (!start.IsZero() && !end.IsZero() && start.After(end)) {
			writeJSON(w, 400, map[string]string{"error": "日期须为 YYYY-MM-DD，且开始日期不能晚于结束日期"})
			return
		}
		rows, err := store.read(symbol, period, start, end)
		if err != nil {
			status, message := 500, "数据读取或解析失败"
			if errors.Is(err, os.ErrNotExist) {
				status, message = 404, "股票数据文件不存在"
			}
			log.Printf("%s %s: %v", symbol, period, err)
			writeJSON(w, status, map[string]string{"error": message})
			return
		}
		writeJSON(w, 200, response{symbol, period, "Asia/Shanghai", rows})
	})
	return mux
}

func normalizePeriod(symbol, period string) (string, error) {
	if period == "" {
		period = "1m"
	}
	if !symbolPattern.MatchString(symbol) || periods[period] == "" {
		return "", fmt.Errorf("股票/指数代码或周期无效")
	}
	return period, nil
}

func date(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.ParseInLocation("2006-01-02", value, beijing)
}

func readBars(path string, start, end time.Time) ([]map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.ReuseRecord = true
	header, err := r.Read()
	if err != nil {
		return nil, err
	}
	header[0] = strings.TrimPrefix(header[0], "\ufeff")
	if strings.Join(header, ",") != strings.Join(headers, ",") {
		return nil, fmt.Errorf("CSV 表头不符合约定")
	}
	rows := make([]map[string]any, 0)
	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		stamp, err := time.ParseInLocation("2006-01-02 15:04:05", record[0], beijing)
		if err != nil {
			return nil, err
		}
		for i, key := range fields {
			number := record[i+1]
			if len(number) == 0 || strings.TrimSpace(number) != number || (number[0] != '-' && (number[0] < '0' || number[0] > '9')) || !json.Valid([]byte(number)) {
				return nil, fmt.Errorf("%s 的 %s 数值无效", record[0], key)
			}
		}
		if (!start.IsZero() && stamp.Before(start)) || (!end.IsZero() && !stamp.Before(end.AddDate(0, 0, 1))) {
			continue
		}
		row := map[string]any{"time": stamp.Format(time.RFC3339)}
		for i, key := range fields {
			row[key] = json.Number(record[i+1])
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i]["time"].(string) < rows[j]["time"].(string) })
	return rows, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("response: %v", err)
	}
}
