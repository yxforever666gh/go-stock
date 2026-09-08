package data

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
	"go-stock/backend/logger"
)

const (
	defaultSinaMinuteFetchMinInterval = 650 * time.Millisecond
	defaultSinaMinuteTimeout          = 25 * time.Second
)

type sinaMinuteKLineRow struct {
	Day    string `json:"day"`
	Open   string `json:"open"`
	High   string `json:"high"`
	Low    string `json:"low"`
	Close  string `json:"close"`
	Volume string `json:"volume"`
	Amount string `json:"amount"`
}

func (p *minuteProviders) sinaMinuteFetchMinInterval() time.Duration {
	return time.Duration(p.environment.Minute.SinaMinIntervalMS) * time.Millisecond
}

func (p *minuteProviders) waitForSinaMinuteFetchWindow() {
	interval := p.sinaMinuteFetchMinInterval()
	if interval <= 0 {
		return
	}
	p.state.sinaMinuteFetchMu.Lock()
	defer p.state.sinaMinuteFetchMu.Unlock()
	if !p.state.sinaMinuteLastFetch.IsZero() {
		elapsed := time.Since(p.state.sinaMinuteLastFetch)
		if elapsed < interval {
			time.Sleep(interval - elapsed)
		}
	}
	p.state.sinaMinuteLastFetch = time.Now()
}

func (p *minuteProviders) sinaMinuteCircuitCheck() error {
	p.state.sinaMinuteCircuitMu.Lock()
	defer p.state.sinaMinuteCircuitMu.Unlock()
	if p.state.sinaMinuteCircuitOpenUntil.IsZero() {
		return nil
	}
	if time.Now().Before(p.state.sinaMinuteCircuitOpenUntil) {
		msg := strings.TrimSpace(p.state.sinaMinuteCircuitLastErr)
		if msg == "" {
			msg = "sina minute api unavailable"
		}
		return fmt.Errorf("sina minute api temporarily disabled until %s: %s", p.state.sinaMinuteCircuitOpenUntil.Format("2006-01-02 15:04:05"), msg)
	}
	p.state.sinaMinuteCircuitOpenUntil = time.Time{}
	p.state.sinaMinuteCircuitFailCount = 0
	p.state.sinaMinuteCircuitLastErr = ""
	return nil
}

func (p *minuteProviders) sinaMinuteCircuitRecordFailure(err error) {
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
	} {
		if strings.Contains(msg, needle) {
			likelyGlobal = true
			break
		}
	}
	if !likelyGlobal {
		return
	}

	p.state.sinaMinuteCircuitMu.Lock()
	defer p.state.sinaMinuteCircuitMu.Unlock()
	p.state.sinaMinuteCircuitFailCount++
	p.state.sinaMinuteCircuitLastErr = err.Error()
	if p.state.sinaMinuteCircuitFailCount < 2 {
		return
	}
	backoff := 2 * time.Minute
	if p.state.sinaMinuteCircuitFailCount >= 4 {
		backoff = 5 * time.Minute
	}
	if p.state.sinaMinuteCircuitFailCount >= 8 {
		backoff = 10 * time.Minute
	}
	p.state.sinaMinuteCircuitOpenUntil = time.Now().Add(backoff)
}

func (p *minuteProviders) sinaMinuteCircuitRecordSuccess() {
	p.state.sinaMinuteCircuitMu.Lock()
	defer p.state.sinaMinuteCircuitMu.Unlock()
	p.state.sinaMinuteCircuitOpenUntil = time.Time{}
	p.state.sinaMinuteCircuitFailCount = 0
	p.state.sinaMinuteCircuitLastErr = ""
}

func tsCodeToSinaSymbol(tsCode string) (string, error) {
	code := strings.ToUpper(strings.TrimSpace(tsCode))
	if code == "" {
		return "", fmt.Errorf("empty stock code")
	}
	// Accept already-prefixed symbols (sz000001/sh600000).
	lower := strings.ToLower(code)
	if strings.HasPrefix(lower, "sz") || strings.HasPrefix(lower, "sh") {
		sym := RemoveAllNonDigitChar(code)
		if len(sym) != 6 {
			return "", fmt.Errorf("invalid sina stock symbol: %s", tsCode)
		}
		return lower[:2] + sym, nil
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

func isSameCNTradeDate(a, b time.Time) bool {
	loc := cnLocation()
	aa := a.In(loc)
	bb := b.In(loc)
	return aa.Year() == bb.Year() && aa.Month() == bb.Month() && aa.Day() == bb.Day()
}

func isTodayCN(t time.Time) bool {
	loc := cnLocation()
	now := time.Now().In(loc)
	tt := t.In(loc)
	return now.Year() == tt.Year() && now.Month() == tt.Month() && now.Day() == tt.Day()
}

func newSinaMinuteClient() *resty.Client {
	client := newRealtimeRestyClient().
		SetTimeout(defaultSinaMinuteTimeout).
		SetRetryCount(0).
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36")
	return client
}

func (p *minuteProviders) fetchMinuteBarsWithSina(tsCode string, start, end time.Time) ([]minuteBar, string, error) {
	if !start.Before(end) {
		return []minuteBar{}, "sina", nil
	}
	// This endpoint is best-effort for intraday windows (same trade date). It is
	// not a general-purpose multi-day minute provider.
	if !isSameCNTradeDate(start, end) {
		return []minuteBar{}, "sina", fmt.Errorf("sina minute provider only supports same-day windows")
	}

	// Prefer Sina only for today's intraday coverage to avoid unexpected cross-day
	// pulls in background tasks.
	if !isTodayCN(end) {
		return []minuteBar{}, "sina", fmt.Errorf("sina minute provider only enabled for today")
	}

	if err := p.sinaMinuteCircuitCheck(); err != nil {
		return []minuteBar{}, "sina", err
	}

	symbol, err := tsCodeToSinaSymbol(tsCode)
	if err != nil {
		return []minuteBar{}, "sina", err
	}

	loc := cnLocation()
	start = normalizeMinuteTime(start.In(loc))
	end = normalizeMinuteTime(end.In(loc))
	if !start.Before(end) {
		return []minuteBar{}, "sina", nil
	}

	spanMin := int(end.Sub(start).Minutes())
	// Include some buffer for lunch break / provider quirks. Too small datalen
	// often results in missing early bars.
	datalen := spanMin + 160
	if datalen < 200 {
		datalen = 200
	}
	if datalen > 2200 {
		datalen = 2200
	}

	url := fmt.Sprintf("http://quotes.sina.cn/cn/api/json_v2.php/CN_MarketDataService.getKLineData?symbol=%s&scale=1&ma=no&datalen=%d", symbol, datalen)

	p.waitForSinaMinuteFetchWindow()
	client := newSinaMinuteClient()

	var body []byte
	var lastErr error
	for attempt := 1; attempt <= 2; attempt++ {
		resp, reqErr := client.R().Get(url)
		if reqErr != nil {
			lastErr = reqErr
			p.sinaMinuteCircuitRecordFailure(reqErr)
			if attempt < 2 {
				time.Sleep(time.Duration(attempt) * 900 * time.Millisecond)
			}
			continue
		}
		if resp == nil {
			lastErr = fmt.Errorf("empty http response")
			p.sinaMinuteCircuitRecordFailure(lastErr)
			break
		}
		if resp.StatusCode() == http.StatusTooManyRequests {
			lastErr = fmt.Errorf("sina rate limited (HTTP 429)")
			p.sinaMinuteCircuitRecordFailure(lastErr)
			break
		}
		if resp.StatusCode() >= 400 {
			lastErr = fmt.Errorf("sina http status %d", resp.StatusCode())
			p.sinaMinuteCircuitRecordFailure(lastErr)
			break
		}
		body = resp.Body()
		lastErr = nil
		break
	}
	if lastErr != nil {
		return []minuteBar{}, "sina", lastErr
	}
	if len(body) == 0 {
		err := fmt.Errorf("sina empty body")
		p.sinaMinuteCircuitRecordFailure(err)
		return []minuteBar{}, "sina", err
	}

	var rows []sinaMinuteKLineRow
	if err := json.Unmarshal(body, &rows); err != nil {
		p.sinaMinuteCircuitRecordFailure(err)
		return []minuteBar{}, "sina", fmt.Errorf("decode sina minute json failed: %w", err)
	}
	p.sinaMinuteCircuitRecordSuccess()

	bars := make([]minuteBar, 0, len(rows))
	for _, row := range rows {
		t, err := parseMinuteTime(row.Day)
		if err != nil {
			continue
		}
		t = normalizeMinuteTime(t.In(loc))
		if t.Before(start) || t.After(end) {
			continue
		}

		open, _ := strconv.ParseFloat(strings.TrimSpace(row.Open), 64)
		high, _ := strconv.ParseFloat(strings.TrimSpace(row.High), 64)
		low, _ := strconv.ParseFloat(strings.TrimSpace(row.Low), 64)
		closeP, _ := strconv.ParseFloat(strings.TrimSpace(row.Close), 64)
		vol, _ := strconv.ParseFloat(strings.TrimSpace(row.Volume), 64)
		amt, _ := strconv.ParseFloat(strings.TrimSpace(row.Amount), 64)

		bars = append(bars, minuteBar{
			TradeTime: t,
			Open:      open,
			High:      high,
			Low:       low,
			Close:     closeP,
			Volume:    vol,
			Amount:    amt,
		})
	}

	sort.SliceStable(bars, func(i, j int) bool {
		return bars[i].TradeTime.Before(bars[j].TradeTime)
	})

	if len(bars) == 0 {
		// Keep this observable so we can tell "provider ok but window not covered"
		// from a pure network failure.
		logger.SugaredLogger.Warnf("sina minute returned empty bars (stock=%s, %s~%s, datalen=%d)", tsCode, start.Format("15:04"), end.Format("15:04"), datalen)
	}
	return dedupeMinuteBars(bars), "sina", nil
}
