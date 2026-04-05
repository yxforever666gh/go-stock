package data

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-resty/resty/v2"
	"go-stock/backend/logger"
	appconfig "go-stock/internal/config"
)

const (
	defaultTencentMinuteFetchMinInterval = 650 * time.Millisecond
	defaultTencentMinuteTimeout          = 20 * time.Second
)

var tencentMinuteMKLineURL = "https://ifzq.gtimg.cn/appstock/app/kline/mkline"

var (
	tencentMinuteFetchMu   sync.Mutex
	tencentMinuteLastFetch time.Time
)

var (
	tencentMinuteCircuitMu        sync.Mutex
	tencentMinuteCircuitOpenUntil time.Time
	tencentMinuteCircuitFailCount int
	tencentMinuteCircuitLastErr   string
)

type tencentMinuteResp struct {
	Code int                          `json:"code"`
	Msg  string                       `json:"msg"`
	Data map[string]tencentMinuteData `json:"data"`
}

type tencentMinuteData struct {
	M1   [][]any `json:"m1"`
	Prec string  `json:"prec"`
}

func tencentMinuteFetchMinInterval() time.Duration {
	return time.Duration(appconfig.Load().Minute.TencentMinIntervalMS) * time.Millisecond
}

func waitForTencentMinuteFetchWindow() {
	interval := tencentMinuteFetchMinInterval()
	if interval <= 0 {
		return
	}
	tencentMinuteFetchMu.Lock()
	defer tencentMinuteFetchMu.Unlock()
	if !tencentMinuteLastFetch.IsZero() {
		elapsed := time.Since(tencentMinuteLastFetch)
		if elapsed < interval {
			time.Sleep(interval - elapsed)
		}
	}
	tencentMinuteLastFetch = time.Now()
}

func tencentMinuteCircuitCheck() error {
	tencentMinuteCircuitMu.Lock()
	defer tencentMinuteCircuitMu.Unlock()
	if tencentMinuteCircuitOpenUntil.IsZero() {
		return nil
	}
	if time.Now().Before(tencentMinuteCircuitOpenUntil) {
		msg := strings.TrimSpace(tencentMinuteCircuitLastErr)
		if msg == "" {
			msg = "tencent minute api unavailable"
		}
		return fmt.Errorf("tencent minute api temporarily disabled until %s: %s", tencentMinuteCircuitOpenUntil.Format("2006-01-02 15:04:05"), msg)
	}
	tencentMinuteCircuitOpenUntil = time.Time{}
	tencentMinuteCircuitFailCount = 0
	tencentMinuteCircuitLastErr = ""
	return nil
}

func tencentMinuteCircuitRecordFailure(err error) {
	if err == nil {
		return
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "" {
		return
	}

	likelyGlobal := false
	for _, needle := range []string{
		"timeout",
		"timed out",
		"i/o timeout",
		"tls handshake timeout",
		"connection reset",
		"connection refused",
		"remote end closed connection",
		"too many requests",
		"429",
		"502",
		"503",
		"504",
	} {
		if strings.Contains(msg, needle) {
			likelyGlobal = true
			break
		}
	}
	if !likelyGlobal {
		return
	}

	tencentMinuteCircuitMu.Lock()
	defer tencentMinuteCircuitMu.Unlock()
	tencentMinuteCircuitFailCount++
	tencentMinuteCircuitLastErr = err.Error()
	if tencentMinuteCircuitFailCount < 2 {
		return
	}
	backoff := 2 * time.Minute
	if tencentMinuteCircuitFailCount >= 4 {
		backoff = 5 * time.Minute
	}
	if tencentMinuteCircuitFailCount >= 8 {
		backoff = 10 * time.Minute
	}
	tencentMinuteCircuitOpenUntil = time.Now().Add(backoff)
}

func tencentMinuteCircuitRecordSuccess() {
	tencentMinuteCircuitMu.Lock()
	defer tencentMinuteCircuitMu.Unlock()
	tencentMinuteCircuitOpenUntil = time.Time{}
	tencentMinuteCircuitFailCount = 0
	tencentMinuteCircuitLastErr = ""
}

func newTencentMinuteClient() *resty.Client {
	return newRealtimeRestyClient().
		SetTimeout(defaultTencentMinuteTimeout).
		SetRetryCount(0).
		SetHeader("Referer", "https://gu.qq.com/").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36")
}

func tsCodeToTencentSymbol(tsCode string) (string, error) {
	code := strings.ToUpper(strings.TrimSpace(tsCode))
	if code == "" {
		return "", fmt.Errorf("empty stock code")
	}
	lower := strings.ToLower(code)
	if strings.HasPrefix(lower, "sh") || strings.HasPrefix(lower, "sz") {
		symbol := RemoveAllNonDigitChar(code)
		if len(symbol) != 6 {
			return "", fmt.Errorf("invalid tencent stock symbol: %s", tsCode)
		}
		return lower[:2] + symbol, nil
	}
	if strings.HasSuffix(code, ".BJ") || strings.HasPrefix(lower, "bj") {
		return "", fmt.Errorf("tencent minute provider does not support bj code: %s", tsCode)
	}

	symbol := extractAShareSymbol(code)
	if len(symbol) != 6 {
		return "", fmt.Errorf("invalid a-share ts code: %s", tsCode)
	}
	if strings.HasSuffix(code, ".SH") || strings.HasPrefix(symbol, "6") {
		return "sh" + symbol, nil
	}
	return "sz" + symbol, nil
}

func tencentMinuteRecentWindow(end time.Time) bool {
	loc := cnLocation()
	now := time.Now().In(loc)
	end = end.In(loc)
	if end.After(now.Add(2 * time.Minute)) {
		return false
	}
	return end.After(now.Add(-7 * 24 * time.Hour))
}

func fetchMinuteBarsWithTencent(tsCode string, start, end time.Time) ([]minuteBar, string, error) {
	if !start.Before(end) {
		return []minuteBar{}, "tencent", nil
	}
	if !tencentMinuteRecentWindow(end) {
		return []minuteBar{}, "tencent", fmt.Errorf("tencent minute provider only enabled for recent windows")
	}
	if err := tencentMinuteCircuitCheck(); err != nil {
		return []minuteBar{}, "tencent", err
	}

	symbol, err := tsCodeToTencentSymbol(tsCode)
	if err != nil {
		return []minuteBar{}, "tencent", err
	}

	loc := cnLocation()
	start = normalizeMinuteTime(start.In(loc))
	end = normalizeMinuteTime(end.In(loc))
	if !start.Before(end) {
		return []minuteBar{}, "tencent", nil
	}

	spanMin := int(end.Sub(start).Minutes())
	datalen := spanMin + 160
	if datalen < 240 {
		datalen = 240
	}
	if datalen > 1200 {
		datalen = 1200
	}

	url := fmt.Sprintf("%s?param=%s,m1,,,%d", strings.TrimRight(tencentMinuteMKLineURL, "/"), symbol, datalen)

	waitForTencentMinuteFetchWindow()
	client := newTencentMinuteClient()

	var body []byte
	var lastErr error
	for attempt := 1; attempt <= 2; attempt++ {
		resp, reqErr := client.R().Get(url)
		if reqErr != nil {
			lastErr = reqErr
			tencentMinuteCircuitRecordFailure(reqErr)
			if attempt < 2 {
				time.Sleep(time.Duration(attempt) * 900 * time.Millisecond)
			}
			continue
		}
		if resp == nil {
			lastErr = fmt.Errorf("empty http response")
			tencentMinuteCircuitRecordFailure(lastErr)
			break
		}
		if resp.StatusCode() == http.StatusTooManyRequests {
			lastErr = fmt.Errorf("tencent rate limited (HTTP 429)")
			tencentMinuteCircuitRecordFailure(lastErr)
			break
		}
		if resp.StatusCode() >= 400 {
			lastErr = fmt.Errorf("tencent http status %d", resp.StatusCode())
			tencentMinuteCircuitRecordFailure(lastErr)
			break
		}
		body = resp.Body()
		lastErr = nil
		break
	}
	if lastErr != nil {
		return []minuteBar{}, "tencent", lastErr
	}
	if len(body) == 0 {
		err := fmt.Errorf("tencent empty body")
		tencentMinuteCircuitRecordFailure(err)
		return []minuteBar{}, "tencent", err
	}

	var result tencentMinuteResp
	if err := json.Unmarshal(body, &result); err != nil {
		tencentMinuteCircuitRecordFailure(err)
		return []minuteBar{}, "tencent", fmt.Errorf("decode tencent minute json failed: %w", err)
	}
	if result.Code != 0 {
		err := fmt.Errorf("tencent minute api error (code=%d): %s", result.Code, strings.TrimSpace(result.Msg))
		tencentMinuteCircuitRecordFailure(err)
		return []minuteBar{}, "tencent", err
	}
	payload, ok := result.Data[symbol]
	if !ok {
		err := fmt.Errorf("tencent minute missing data for %s", symbol)
		tencentMinuteCircuitRecordFailure(err)
		return []minuteBar{}, "tencent", err
	}
	tencentMinuteCircuitRecordSuccess()

	bars := make([]minuteBar, 0, len(payload.M1))
	for _, row := range payload.M1 {
		if len(row) < 6 {
			continue
		}
		tradeTime, err := parseMinuteTime(fmt.Sprint(row[0]))
		if err != nil {
			continue
		}
		tradeTime = normalizeMinuteTime(tradeTime.In(loc))
		if tradeTime.Before(start) || tradeTime.After(end) {
			continue
		}
		bars = append(bars, minuteBar{
			TradeTime: tradeTime,
			Open:      toFloatAny(row, 1),
			Close:     toFloatAny(row, 2),
			High:      toFloatAny(row, 3),
			Low:       toFloatAny(row, 4),
			Volume:    toFloatAny(row, 5),
		})
	}

	sort.SliceStable(bars, func(i, j int) bool {
		return bars[i].TradeTime.Before(bars[j].TradeTime)
	})
	if len(bars) == 0 {
		logger.SugaredLogger.Warnf("tencent minute returned empty bars (stock=%s, %s~%s, datalen=%d)", tsCode, start.Format("2006-01-02 15:04"), end.Format("2006-01-02 15:04"), datalen)
	}
	return dedupeMinuteBars(bars), "tencent", nil
}
