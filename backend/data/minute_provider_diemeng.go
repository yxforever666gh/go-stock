package data

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-resty/resty/v2"
	"go-stock/backend/logger"
	appconfig "go-stock/internal/config"
)

const (
	defaultDiemengBaseURL = "https://mg.diemeng.chat/api"
	// Defaults tuned for stability (less rate-limit / fewer timeouts) during
	// full backfills. Override via env when needed.
	defaultDiemengFetchMinInterval = 1200 * time.Millisecond
	defaultDiemengTimeout          = 60 * time.Second
	diemengMaxPages                = 50
)

// NOTE: User requested to store the API key in-repo for personal use across machines.
// You can override this at runtime via env `GO_STOCK_DIEMENG_API_KEY`.
const defaultDiemengAPIKey = "b8c0c3039e34bb7a65997e525cf554d6b3b69c306529daa4d0"

var (
	diemengFetchMu   sync.Mutex
	diemengLastFetch time.Time
)

var (
	diemengCircuitMu        sync.Mutex
	diemengCircuitOpenUntil time.Time
	diemengCircuitFailCount int
	diemengCircuitLastErr   string
)

type diemengHistoryReq struct {
	StockCode any    `json:"stock_code"`
	Level     string `json:"level"`
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
	Page      int    `json:"page"`
	PageSize  int    `json:"page_size"`
}

type diemengHistoryItem struct {
	TradeTime string  `json:"trade_time"`
	StockCode string  `json:"stock_code"`
	Open      float64 `json:"open"`
	High      float64 `json:"high"`
	Low       float64 `json:"low"`
	Close     float64 `json:"close"`
	Vol       float64 `json:"vol"`
	Amount    float64 `json:"amount"`
}

type diemengHistoryData struct {
	Total    int                  `json:"total"`
	Page     int                  `json:"page"`
	PageSize int                  `json:"page_size"`
	Items    []diemengHistoryItem `json:"items"`
	List     []diemengHistoryItem `json:"list"`
}

type diemengResponse[T any] struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data T      `json:"data"`
}

func hasDiemengKey() bool {
	return strings.TrimSpace(diemengAPIKey()) != ""
}

func diemengAPIKey() string {
	if v := strings.TrimSpace(appconfig.Load().Diemeng.APIKey); v != "" {
		return v
	}
	return strings.TrimSpace(defaultDiemengAPIKey)
}

func diemengBaseURL() string {
	return appconfig.Load().Diemeng.BaseURL
}

func diemengTimeout() time.Duration {
	return time.Duration(appconfig.Load().Diemeng.TimeoutSec) * time.Second
}

func diemengFetchMinInterval() time.Duration {
	return time.Duration(appconfig.Load().Diemeng.MinIntervalMS) * time.Millisecond
}

func waitForDiemengFetchWindow() {
	interval := diemengFetchMinInterval()
	if interval <= 0 {
		return
	}
	diemengFetchMu.Lock()
	defer diemengFetchMu.Unlock()
	if !diemengLastFetch.IsZero() {
		elapsed := time.Since(diemengLastFetch)
		if elapsed < interval {
			time.Sleep(interval - elapsed)
		}
	}
	diemengLastFetch = time.Now()
}

func diemengCircuitCheck() error {
	diemengCircuitMu.Lock()
	defer diemengCircuitMu.Unlock()
	if diemengCircuitOpenUntil.IsZero() {
		return nil
	}
	if time.Now().Before(diemengCircuitOpenUntil) {
		msg := strings.TrimSpace(diemengCircuitLastErr)
		if msg == "" {
			msg = "diemeng api unavailable"
		}
		return fmt.Errorf("diemeng api temporarily disabled until %s: %s", diemengCircuitOpenUntil.Format("2006-01-02 15:04:05"), msg)
	}
	diemengCircuitOpenUntil = time.Time{}
	diemengCircuitFailCount = 0
	diemengCircuitLastErr = ""
	return nil
}

func diemengCircuitRecordFailure(err error) {
	if err == nil {
		return
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "" {
		return
	}

	likelyGlobal := false
	for _, needle := range []string{
		"too many requests",
		"429",
		"timeout",
		"timed out",
		"connection reset",
		"connection refused",
		"remote end closed connection",
	} {
		if strings.Contains(msg, needle) {
			likelyGlobal = true
			break
		}
	}
	if !likelyGlobal {
		return
	}

	diemengCircuitMu.Lock()
	defer diemengCircuitMu.Unlock()
	diemengCircuitFailCount++
	diemengCircuitLastErr = err.Error()
	if diemengCircuitFailCount < 2 {
		return
	}
	backoff := 2 * time.Minute
	if diemengCircuitFailCount >= 4 {
		backoff = 5 * time.Minute
	}
	diemengCircuitOpenUntil = time.Now().Add(backoff)
}

func diemengCircuitRecordSuccess() {
	diemengCircuitMu.Lock()
	defer diemengCircuitMu.Unlock()
	diemengCircuitOpenUntil = time.Time{}
	diemengCircuitFailCount = 0
	diemengCircuitLastErr = ""
}

func diemengProxyMode() string {
	return appconfig.Load().Diemeng.ProxyMode
}

func diemengProxyFromSettings() string {
	config := GetSettingConfig()
	if config == nil || config.Settings == nil {
		return ""
	}
	if !config.HttpProxyEnabled {
		return ""
	}
	return strings.TrimSpace(config.HttpProxy)
}

func newDiemengClient() *resty.Client {
	client := newNoProxyRestyClient().
		SetBaseURL(diemengBaseURL()).
		SetTimeout(diemengTimeout()).
		SetRetryCount(0).
		SetHeader("Content-Type", "application/json")
	if forceNoProxyForFetchEnabled() {
		return client
	}

	// Proxy strategy (in order):
	// 1) If GO_STOCK_DIEMENG_PROXY_MODE is set:
	//    - inherit: use system env proxy (default Go behavior)
	//    - settings/config: use app Settings.HttpProxy
	//    - off/disable: force no proxy
	mode := diemengProxyMode()
	switch mode {
	case "inherit":
		client = resty.New().
			SetBaseURL(diemengBaseURL()).
			SetTimeout(diemengTimeout()).
			SetRetryCount(0).
			SetHeader("Content-Type", "application/json")
		return client
	case "settings", "config":
		settingsProxy := diemengProxyFromSettings()
		if settingsProxy == "" {
			logger.SugaredLogger.Warnf("GO_STOCK_DIEMENG_PROXY_MODE=%s but settings http proxy is empty; fallback to no-proxy", mode)
			break
		}
		// Validate URL early so errors are explicit to users.
		u, err := url.Parse(settingsProxy)
		if err != nil || u == nil || strings.TrimSpace(u.Scheme) == "" || strings.TrimSpace(u.Host) == "" {
			logger.SugaredLogger.Warnf("invalid settings http proxy url=%q (need scheme://host:port); fallback to no-proxy: %v", settingsProxy, err)
			break
		}
		client.SetProxy(settingsProxy)
		return client
	case "", "off", "disable", "none", "0", "false":
		// fallthrough to "no proxy"
	default:
		logger.SugaredLogger.Warnf("unknown GO_STOCK_DIEMENG_PROXY_MODE=%q; fallback to no-proxy", mode)
	}
	return client
}

func fetchMinuteBarsWithDiemeng(tsCode string, start, end time.Time) ([]minuteBar, string, error) {
	if !start.Before(end) {
		return []minuteBar{}, "diemeng", nil
	}
	if !hasDiemengKey() {
		return []minuteBar{}, "diemeng", fmt.Errorf("missing GO_STOCK_DIEMENG_API_KEY")
	}
	if err := diemengCircuitCheck(); err != nil {
		return []minuteBar{}, "diemeng", err
	}
	if extractAShareSymbol(tsCode) == "" {
		return []minuteBar{}, "diemeng", fmt.Errorf("invalid stock code for diemeng: %s", tsCode)
	}

	// Use 1-minute by default for trigger scan accuracy.
	level := appconfig.Load().Diemeng.Level

	client := newDiemengClient()
	apiKey := strings.TrimSpace(diemengAPIKey())

	start = normalizeMinuteTime(start)
	end = normalizeMinuteTime(end)

	// Guard: avoid requesting too large time windows at once (especially for 1min).
	maxSpan := 3 * 24 * time.Hour
	if level == "1min" {
		maxSpan = 48 * time.Hour
	}
	if level == "5min" {
		maxSpan = 10 * 24 * time.Hour
	}

	fetchWindow := func(winStart, winEnd time.Time) ([]diemengHistoryItem, error) {
		startAt := normalizeMinuteTime(winStart).Format("2006-01-02 15:04:05")
		endAt := normalizeMinuteTime(winEnd).Format("2006-01-02 15:04:05")

		all := make([]diemengHistoryItem, 0, 1024)
		page := 0
		pageSize := 10000

		for page < diemengMaxPages {
			waitForDiemengFetchWindow()

			reqBody := diemengHistoryReq{
				StockCode: tsCode,
				Level:     level,
				StartTime: startAt,
				EndTime:   endAt,
				Page:      page,
				PageSize:  pageSize,
			}

			var resp diemengResponse[diemengHistoryData]
			var httpResp *resty.Response
			var err error
			for attempt := 1; attempt <= 2; attempt++ {
				r := client.R().
					SetHeader("apiKey", apiKey).
					SetBody(reqBody).
					SetResult(&resp)
				httpResp, err = r.Post("/stock/history")
				if err == nil && httpResp != nil && httpResp.StatusCode() != http.StatusTooManyRequests {
					break
				}
				if attempt < 2 {
					time.Sleep(time.Duration(attempt) * 1200 * time.Millisecond)
				}
			}
			if err != nil {
				diemengCircuitRecordFailure(err)
				return nil, fmt.Errorf("diemeng request failed: %w", err)
			}
			if httpResp == nil {
				err = fmt.Errorf("empty http response")
				diemengCircuitRecordFailure(err)
				return nil, err
			}
			if httpResp.StatusCode() == http.StatusTooManyRequests {
				err = fmt.Errorf("diemeng rate limited (HTTP 429)")
				diemengCircuitRecordFailure(err)
				return nil, err
			}
			if httpResp.StatusCode() >= 400 {
				err = fmt.Errorf("diemeng http status %d", httpResp.StatusCode())
				diemengCircuitRecordFailure(err)
				return nil, err
			}
			if resp.Code != 200 {
				err = fmt.Errorf("diemeng api error (code=%d): %s", resp.Code, strings.TrimSpace(resp.Msg))
				diemengCircuitRecordFailure(err)
				return nil, err
			}

			items := resp.Data.Items
			if len(items) == 0 {
				items = resp.Data.List
			}
			// 兼容：部分环境下上游会返回 code=200 但数据为空，导致“手动下载分钟线”
			// 在 UI 侧表现为一直待覆盖却没有可读错误原因。
			//
			// 仅当第一页就为空时将其视为异常；后续页为空则视为正常结束。
			if len(items) == 0 && page == 0 {
				err = fmt.Errorf("diemeng returned empty data (stock=%s, level=%s, %s~%s)", tsCode, level, startAt, endAt)
				diemengCircuitRecordFailure(err)
				return nil, err
			}

			diemengCircuitRecordSuccess()
			if len(items) > 0 {
				all = append(all, items...)
			}
			// Some deployments return `data.list` without paging fields. Treat that as
			// "single page done".
			if resp.Data.PageSize == 0 && len(resp.Data.Items) == 0 && len(resp.Data.List) > 0 {
				break
			}
			if len(items) < pageSize {
				break
			}
			page++
		}
		return all, nil
	}

	all := make([]diemengHistoryItem, 0, 1024)
	var partialErr error
	for winStart := start; winStart.Before(end); {
		winEnd := winStart.Add(maxSpan)
		if winEnd.After(end) {
			winEnd = end
		}
		items, err := fetchWindow(winStart, winEnd)
		if err != nil {
			// Return whatever we fetched so far so the caller can still persist
			// partial cache data (then retry later).
			partialErr = err
			break
		}
		if len(items) > 0 {
			all = append(all, items...)
		}
		if !winEnd.After(winStart) {
			break
		}
		// Move forward by one minute to avoid duplicating the boundary bar.
		winStart = winEnd.Add(time.Minute)
	}

	if len(all) == 0 {
		return []minuteBar{}, "diemeng", partialErr
	}

	bars := make([]minuteBar, 0, len(all))
	for _, it := range all {
		t, err := parseMinuteTime(it.TradeTime)
		if err != nil {
			continue
		}
		if t.Before(start) || t.After(end) {
			continue
		}
		bars = append(bars, minuteBar{
			TradeTime: normalizeMinuteTime(t),
			Open:      it.Open,
			High:      it.High,
			Low:       it.Low,
			Close:     it.Close,
			Volume:    it.Vol,
			Amount:    it.Amount,
		})
	}

	sort.SliceStable(bars, func(i, j int) bool {
		return bars[i].TradeTime.Before(bars[j].TradeTime)
	})
	return dedupeMinuteBars(bars), "diemeng", partialErr
}

// For debugging: decode raw JSON quickly when Resty SetResult doesn't match.
func _diemengDecodeJSON(body []byte, out any) error {
	return json.Unmarshal(body, out)
}
