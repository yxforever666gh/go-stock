package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go-stock/internal/diemeng"
)

// normalizeSymbol 把蝶梦的 "600000.SH" / "000001.SZ" 统一成本地约定的 sh600000 形式，
// 与 symbolPattern、分钟线 CSV 以及集合竞价索引保持一致，便于后续 join。
func normalizeSymbol(code string) string {
	code = strings.TrimSpace(code)
	if code == "" {
		return ""
	}
	if symbolPattern.MatchString(strings.ToLower(code)) {
		return strings.ToLower(code)
	}
	if i := strings.LastIndex(code, "."); i > 0 {
		num, suffix := code[:i], strings.ToUpper(code[i+1:])
		if len(num) == 6 {
			var prefix string
			switch suffix {
			case "SH":
				prefix = "sh"
			case "SZ":
				prefix = "sz"
			case "BJ":
				prefix = "bj"
			}
			if prefix != "" {
				return prefix + num
			}
		}
	}
	return ""
}

type dailyRecord struct {
	TradeDate string   `json:"trade_date"`
	StockCode string   `json:"stock_code"`
	StockName string   `json:"stock_name"`
	Open      *float64 `json:"open"`
	High      *float64 `json:"high"`
	Low       *float64 `json:"low"`
	Close     *float64 `json:"close"`
	PreClose  *float64 `json:"pre_close"`
	PctChg    *float64 `json:"pct_chg"`
	Vol       *float64 `json:"vol"`
	Amount    *float64 `json:"amount"`
}

func floatOrZero(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

type calendarDay struct {
	Date   string `json:"date"`
	IsOpen int    `json:"is_open"`
}

// tradingDays 只花 1 次调用拿到区间内交易日，避免对休市日浪费调用与节流额度。
func tradingDays(ctx context.Context, client *diemeng.Client, from, to string) ([]string, error) {
	result, err := client.Call(ctx, "diemeng_basic_calendar", map[string]any{"start_time": from, "end_time": to})
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Data []calendarDay `json:"data"`
	}
	if err := json.Unmarshal(result.Response, &envelope); err != nil {
		return nil, fmt.Errorf("交易日历响应无法解析: %w", err)
	}
	days := make([]string, 0, len(envelope.Data))
	for _, d := range envelope.Data {
		if d.IsOpen == 1 && d.Date != "" {
			days = append(days, d.Date)
		}
	}
	return days, nil
}

// dailyPageSize 取 10000：实测单日全市场 5000-5600 只，一页即可装下，
// 但仍按 total 分页，避免未来标的数量增长后静默截断。
const dailyPageSize = 10000

// maxDailyPages 是防跑飞的硬闸：正常一天只需 1 页。
const maxDailyPages = 50

// fetchDay 取某交易日全市场日线。走 diemeng_stock_daily 而不是 daily_dump：
// 后者实测有「只能下载最近 90 天」的隐藏限制，且其 GZIP 信封会被 client 的
// validateDump 误判为业务信封而拒绝。
func fetchDay(ctx context.Context, client *diemeng.Client, day string) ([]dailyBarRow, error) {
	out := make([]dailyBarRow, 0, 6000)
	total := -1
	for page := 0; page < maxDailyPages; page++ {
		result, err := client.Call(ctx, "diemeng_stock_daily", map[string]any{
			"start_time": day, "end_time": day, "page": page, "page_size": dailyPageSize,
		})
		if err != nil {
			return nil, err
		}
		var envelope struct {
			Data struct {
				Total int           `json:"total"`
				List  []dailyRecord `json:"list"`
			} `json:"data"`
		}
		if err := json.Unmarshal(result.Response, &envelope); err != nil {
			return nil, fmt.Errorf("日线响应无法解析: %w", err)
		}
		total = envelope.Data.Total
		if len(envelope.Data.List) == 0 {
			break
		}
		for _, rec := range envelope.Data.List {
			code := normalizeSymbol(rec.StockCode)
			if code == "" {
				continue
			}
			date := rec.TradeDate
			if date == "" {
				date = day
			}
			out = append(out, dailyBarRow{
				Code: code, Date: date, Name: rec.StockName,
				Open: floatOrZero(rec.Open), High: floatOrZero(rec.High),
				Low: floatOrZero(rec.Low), Close: floatOrZero(rec.Close),
				PreClose: floatOrZero(rec.PreClose), PctChg: floatOrZero(rec.PctChg),
				Volume: floatOrZero(rec.Vol), Amount: floatOrZero(rec.Amount),
			})
		}
		if total >= 0 && len(out) >= total {
			break
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("日线返回为空")
	}
	return out, nil
}

type refreshReport struct {
	Total   int
	Skipped int
	OK      int
	Failed  int
	Rows    int
}

// refreshDaily 逐交易日把蝶梦日线物化进本地库。
// 已标记 ok 的日期直接跳过，因此中断后重跑会从断点继续；
// 失败日期记录在案且不重试，避免反复冲击上游。
func refreshDaily(ctx context.Context, store *dailyStore, client *diemeng.Client, from, to string, logf func(string, ...any)) (refreshReport, error) {
	var report refreshReport
	if client == nil {
		return report, fmt.Errorf("蝶梦未配置：缺少本机私有配置文件")
	}
	days, err := tradingDays(ctx, client, from, to)
	if err != nil {
		return report, fmt.Errorf("获取交易日历失败: %w", err)
	}
	report.Total = len(days)
	if report.Total == 0 {
		return report, nil
	}
	done, err := store.refreshedDates()
	if err != nil {
		return report, err
	}
	for i, day := range days {
		select {
		case <-ctx.Done():
			return report, ctx.Err()
		default:
		}
		if done[day] == "ok" {
			report.Skipped++
			continue
		}
		rows, err := fetchDay(ctx, client, day)
		if err != nil {
			report.Failed++
			store.markRefreshed(day, "failed", 0, err.Error(), time.Now().Format(time.RFC3339))
			logf("[%d/%d] %s 失败: %v", i+1, report.Total, day, err)
			continue
		}
		if err := store.putBars(day, rows); err != nil {
			report.Failed++
			store.markRefreshed(day, "failed", 0, err.Error(), time.Now().Format(time.RFC3339))
			logf("[%d/%d] %s 写库失败: %v", i+1, report.Total, day, err)
			continue
		}
		if err := store.markRefreshed(day, "ok", len(rows), "", time.Now().Format(time.RFC3339)); err != nil {
			return report, err
		}
		report.OK++
		report.Rows += len(rows)
		logf("[%d/%d] %s 写入 %d 行", i+1, report.Total, day, len(rows))
	}
	return report, nil
}
