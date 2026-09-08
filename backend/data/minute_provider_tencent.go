package data

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
	"go-stock/backend/logger"
)

const (
	defaultTencentMinuteFetchMinInterval = 650 * time.Millisecond
	defaultTencentMinuteTimeout          = 20 * time.Second
)

var tencentMinuteMKLineURL = "https://ifzq.gtimg.cn/appstock/app/kline/mkline"

type tencentMinuteResp struct {
	Code int                          `json:"code"`
	Msg  string                       `json:"msg"`
	Data map[string]tencentMinuteData `json:"data"`
}

type tencentMinuteData struct {
	M1   [][]any `json:"m1"`
	Prec string  `json:"prec"`
}

func (p *minuteProviders) tencentMinuteFetchMinInterval() time.Duration {
	return time.Duration(p.environment.Minute.TencentMinIntervalMS) * time.Millisecond
}

func (p *minuteProviders) waitForTencentMinuteFetchWindow() {
	_ = p.waitForTencentMinuteFetchWindowContext(context.Background())
}

func (p *minuteProviders) waitForTencentMinuteFetchWindowContext(ctx context.Context) error {
	interval := p.tencentMinuteFetchMinInterval()
	if interval <= 0 {
		return nil
	}
	p.state.tencentMinuteFetchMu.Lock()
	defer p.state.tencentMinuteFetchMu.Unlock()
	if !p.state.tencentMinuteLastFetch.IsZero() {
		elapsed := time.Since(p.state.tencentMinuteLastFetch)
		if elapsed < interval {
			timer := time.NewTimer(interval - elapsed)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	p.state.tencentMinuteLastFetch = time.Now()
	return nil
}

func (p *minuteProviders) tencentMinuteCircuitCheck() error {
	p.state.tencentMinuteCircuitMu.Lock()
	defer p.state.tencentMinuteCircuitMu.Unlock()
	if p.state.tencentMinuteCircuitOpenUntil.IsZero() {
		return nil
	}
	if time.Now().Before(p.state.tencentMinuteCircuitOpenUntil) {
		msg := strings.TrimSpace(p.state.tencentMinuteCircuitLastErr)
		if msg == "" {
			msg = "tencent minute api unavailable"
		}
		return fmt.Errorf("tencent minute api temporarily disabled until %s: %s", p.state.tencentMinuteCircuitOpenUntil.Format("2006-01-02 15:04:05"), msg)
	}
	p.state.tencentMinuteCircuitOpenUntil = time.Time{}
	p.state.tencentMinuteCircuitFailCount = 0
	p.state.tencentMinuteCircuitLastErr = ""
	return nil
}

func (p *minuteProviders) tencentMinuteCircuitRecordFailure(err error) {
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

	p.state.tencentMinuteCircuitMu.Lock()
	defer p.state.tencentMinuteCircuitMu.Unlock()
	p.state.tencentMinuteCircuitFailCount++
	p.state.tencentMinuteCircuitLastErr = err.Error()
	if p.state.tencentMinuteCircuitFailCount < 2 {
		return
	}
	backoff := 2 * time.Minute
	if p.state.tencentMinuteCircuitFailCount >= 4 {
		backoff = 5 * time.Minute
	}
	if p.state.tencentMinuteCircuitFailCount >= 8 {
		backoff = 10 * time.Minute
	}
	p.state.tencentMinuteCircuitOpenUntil = time.Now().Add(backoff)
}

func (p *minuteProviders) tencentMinuteCircuitRecordSuccess() {
	p.state.tencentMinuteCircuitMu.Lock()
	defer p.state.tencentMinuteCircuitMu.Unlock()
	p.state.tencentMinuteCircuitOpenUntil = time.Time{}
	p.state.tencentMinuteCircuitFailCount = 0
	p.state.tencentMinuteCircuitLastErr = ""
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

func (p *minuteProviders) fetchMinuteBarsWithTencent(tsCode string, start, end time.Time) ([]minuteBar, string, error) {
	return p.fetchMinuteBarsWithTencentContext(context.Background(), tsCode, start, end)
}

func (p *minuteProviders) fetchMinuteBarsWithTencentContext(ctx context.Context, tsCode string, start, end time.Time) ([]minuteBar, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !start.Before(end) {
		return []minuteBar{}, "tencent", nil
	}
	if !tencentMinuteRecentWindow(end) {
		return []minuteBar{}, "tencent", fmt.Errorf("tencent minute provider only enabled for recent windows")
	}
	if err := p.tencentMinuteCircuitCheck(); err != nil {
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

	if err := p.waitForTencentMinuteFetchWindowContext(ctx); err != nil {
		return []minuteBar{}, "tencent", err
	}
	client := newTencentMinuteClient()

	var body []byte
	var lastErr error
	for attempt := 1; attempt <= 2; attempt++ {
		resp, reqErr := client.R().SetContext(ctx).Get(url)
		if reqErr != nil {
			lastErr = reqErr
			p.tencentMinuteCircuitRecordFailure(reqErr)
			if attempt < 2 {
				timer := time.NewTimer(time.Duration(attempt) * 900 * time.Millisecond)
				select {
				case <-ctx.Done():
					timer.Stop()
					return []minuteBar{}, "tencent", ctx.Err()
				case <-timer.C:
				}
			}
			continue
		}
		if resp == nil {
			lastErr = fmt.Errorf("empty http response")
			p.tencentMinuteCircuitRecordFailure(lastErr)
			break
		}
		if resp.StatusCode() == http.StatusTooManyRequests {
			lastErr = fmt.Errorf("tencent rate limited (HTTP 429)")
			p.tencentMinuteCircuitRecordFailure(lastErr)
			break
		}
		if resp.StatusCode() >= 400 {
			lastErr = fmt.Errorf("tencent http status %d", resp.StatusCode())
			p.tencentMinuteCircuitRecordFailure(lastErr)
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
		p.tencentMinuteCircuitRecordFailure(err)
		return []minuteBar{}, "tencent", err
	}

	var result tencentMinuteResp
	if err := json.Unmarshal(body, &result); err != nil {
		p.tencentMinuteCircuitRecordFailure(err)
		return []minuteBar{}, "tencent", fmt.Errorf("decode tencent minute json failed: %w", err)
	}
	if result.Code != 0 {
		err := fmt.Errorf("tencent minute api error (code=%d): %s", result.Code, strings.TrimSpace(result.Msg))
		p.tencentMinuteCircuitRecordFailure(err)
		return []minuteBar{}, "tencent", err
	}
	payload, ok := result.Data[symbol]
	if !ok {
		err := fmt.Errorf("tencent minute missing data for %s", symbol)
		p.tencentMinuteCircuitRecordFailure(err)
		return []minuteBar{}, "tencent", err
	}
	p.tencentMinuteCircuitRecordSuccess()

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
