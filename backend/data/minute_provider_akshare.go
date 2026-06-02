package data

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	appconfig "go-stock/internal/config"
)

const (
	defaultAkShareFetchMinInterval = 1500 * time.Millisecond
	defaultAkShareRetryWait        = 2 * time.Second
	defaultAkShareFetchTimeout     = 90 * time.Second
	akShareFetchMaxAttempts        = 2
)

var (
	akShareFetchMu   sync.Mutex
	akShareLastFetch time.Time
)

var (
	akShareCircuitMu        sync.Mutex
	akShareCircuitOpenUntil time.Time
	akShareCircuitFailCount int
	akShareCircuitLastErr   string
)

type akShareMinuteRow struct {
	TradeTime string  `json:"trade_time"`
	Open      float64 `json:"open"`
	High      float64 `json:"high"`
	Low       float64 `json:"low"`
	Close     float64 `json:"close"`
	Volume    float64 `json:"volume"`
	Amount    float64 `json:"amount"`
}

func fetchMinuteBarsWithAkShare(tsCode string, start, end time.Time) ([]minuteBar, string, error) {
	if !start.Before(end) {
		return []minuteBar{}, "", nil
	}
	if err := EnsureAkShareRuntime(); err != nil {
		return nil, "", err
	}

	if err := akShareCircuitCheck(); err != nil {
		return nil, "", err
	}

	// Keep tsCode intact (e.g. 002594.SZ) so the Python side can choose the
	// best AkShare source (Sina vs Eastmoney) and infer exchange prefix.
	if extractAShareSymbol(tsCode) == "" {
		return nil, "", fmt.Errorf("invalid stock code for akshare: %s", tsCode)
	}

	scriptPath, err := akShareScriptPath()
	if err != nil {
		return nil, "", err
	}

	startAt := normalizeMinuteTime(start).Format("2006-01-02 15:04:05")
	endAt := normalizeMinuteTime(end).Format("2006-01-02 15:04:05")

	sourcePref := resolveAkshareMinuteSourcePreference()
	if sourcePref == "" {
		return nil, "", fmt.Errorf("AKShare 分钟线来源已在设置中全部关闭")
	}

	fetchRows := func(sourceOverride string) ([]akShareMinuteRow, error) {
		var rows []akShareMinuteRow
		var lastErr error
		for attempt := 1; attempt <= akShareFetchMaxAttempts; attempt++ {
			if err := akShareCircuitCheck(); err != nil {
				return nil, err
			}
			waitForAkShareFetchWindow()
			rows, err = runAkShareMinuteScript(scriptPath, tsCode, startAt, endAt, sourceOverride)
			if err == nil {
				akShareCircuitRecordSuccess()
				lastErr = nil
				break
			}
			akShareCircuitRecordFailure(err)
			lastErr = err
			if attempt < akShareFetchMaxAttempts {
				time.Sleep(akShareRetryWait())
			}
		}
		if lastErr != nil {
			return nil, lastErr
		}
		return rows, nil
	}

	// "auto" means: try Sina first (stable), then fall back to EM when Sina does
	// not fully cover the requested [start, end] window.
	usedSource := ""
	var rows []akShareMinuteRow
	if sourcePref == "auto" {
		sinaRows, sinaErr := fetchRows("sina")
		if sinaErr == nil {
			sinaBars := convertAkShareRowsToBars(sinaRows, start, end)
			if minuteBarsCoverTradingSessions(sinaBars, start, end) {
				return sinaBars, "akshare:sina", nil
			}
			emRows, emErr := fetchRows("em")
			if emErr == nil {
				emBars := convertAkShareRowsToBars(emRows, start, end)
				if len(emBars) > 0 {
					return emBars, "akshare:em", nil
				}
			} else {
				// Record EM failure so we don't keep hammering a broken upstream.
				akShareCircuitRecordFailure(emErr)
			}
			// Fall back to whatever Sina returned (even if incomplete) so we can
			// at least advance the cache tail.
			return sinaBars, "akshare:sina", nil
		}

		// Sina hard-failed: try EM as the only fallback.
		emRows, emErr := fetchRows("em")
		if emErr != nil {
			return nil, "", emErr
		}
		rows = emRows
		usedSource = "akshare:em"
	} else {
		rows, err = fetchRows(sourcePref)
		if err != nil {
			return nil, "", err
		}
		usedSource = "akshare:" + sourcePref
	}

	bars := convertAkShareRowsToBars(rows, start, end)
	return bars, usedSource, nil
}

func resolveAkshareMinuteSourcePreference() string {
	sourcePref := appconfig.Load().Akshare.MinuteSource
	sinaEnabled := minutePublicSinaEnabled()

	switch sourcePref {
	case "sina":
		if sinaEnabled {
			return "sina"
		}
		return ""
	case "em":
		return "em"
	case "auto":
		switch {
		case sinaEnabled:
			return "auto"
		case minutePublicAkshareEnabled():
			return "em"
		default:
			return ""
		}
	default:
		if sinaEnabled {
			return "auto"
		}
		if minutePublicAkshareEnabled() {
			return "em"
		}
		return ""
	}
}

func akShareCircuitCheck() error {
	akShareCircuitMu.Lock()
	defer akShareCircuitMu.Unlock()
	if akShareCircuitOpenUntil.IsZero() {
		return nil
	}
	if time.Now().Before(akShareCircuitOpenUntil) {
		msg := strings.TrimSpace(akShareCircuitLastErr)
		if msg == "" {
			msg = "akshare unavailable"
		}
		return fmt.Errorf("akshare temporarily disabled until %s: %s", akShareCircuitOpenUntil.Format("2006-01-02 15:04:05"), msg)
	}
	akShareCircuitOpenUntil = time.Time{}
	akShareCircuitFailCount = 0
	akShareCircuitLastErr = ""
	return nil
}

func akShareCircuitRecordFailure(err error) {
	if err == nil {
		return
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "" {
		return
	}

	// Only open the circuit for likely-global failures (network/rate-limit).
	likelyGlobal := false
	for _, needle := range []string{
		"remote end closed connection",
		"remotedisconnected",
		"connection aborted",
		"connection reset",
		"tls handshake timeout",
		"i/o timeout",
		"timed out",
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

	akShareCircuitMu.Lock()
	defer akShareCircuitMu.Unlock()
	akShareCircuitFailCount++
	akShareCircuitLastErr = err.Error()
	if akShareCircuitFailCount < 2 {
		return
	}

	backoff := 2 * time.Minute
	if akShareCircuitFailCount >= 4 {
		backoff = 5 * time.Minute
	}
	if akShareCircuitFailCount >= 8 {
		backoff = 10 * time.Minute
	}
	akShareCircuitOpenUntil = time.Now().Add(backoff)
}

func akShareCircuitRecordSuccess() {
	akShareCircuitMu.Lock()
	defer akShareCircuitMu.Unlock()
	akShareCircuitOpenUntil = time.Time{}
	akShareCircuitFailCount = 0
	akShareCircuitLastErr = ""
}

func runAkShareMinuteScript(scriptPath, symbol, startAt, endAt, sourceOverride string) ([]akShareMinuteRow, error) {
	ctx, cancel := context.WithTimeout(context.Background(), akShareFetchTimeout())
	defer cancel()

	pythonBin, _, err := resolvePythonExecutable()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, pythonBin, scriptPath, symbol, startAt, endAt)
	env := akShareScriptEnv()
	if strings.TrimSpace(sourceOverride) != "" {
		env = append(env, "GO_STOCK_AKSHARE_MINUTE_SOURCE="+strings.TrimSpace(sourceOverride))
	}
	cmd.Env = env
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("run akshare script failed: %s", msg)
	}

	rows := make([]akShareMinuteRow, 0)
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		return nil, fmt.Errorf("parse akshare output failed: %w", err)
	}
	return rows, nil
}

func convertAkShareRowsToBars(rows []akShareMinuteRow, start, end time.Time) []minuteBar {
	if len(rows) == 0 {
		return []minuteBar{}
	}
	bars := make([]minuteBar, 0, len(rows))
	for _, row := range rows {
		tradeTime, parseErr := parseMinuteTime(row.TradeTime)
		if parseErr != nil {
			continue
		}
		if tradeTime.Before(start) || tradeTime.After(end) {
			continue
		}
		bars = append(bars, minuteBar{
			TradeTime: normalizeMinuteTime(tradeTime),
			Open:      row.Open,
			High:      row.High,
			Low:       row.Low,
			Close:     row.Close,
			Volume:    row.Volume,
			Amount:    row.Amount,
		})
	}

	sort.SliceStable(bars, func(i, j int) bool {
		return bars[i].TradeTime.Before(bars[j].TradeTime)
	})
	return dedupeMinuteBars(bars)
}

func minuteBarsCoverRange(bars []minuteBar, start, end time.Time) bool {
	if len(bars) == 0 {
		return false
	}
	start = normalizeMinuteTime(start)
	end = normalizeMinuteTime(end)
	first := normalizeMinuteTime(bars[0].TradeTime)
	last := normalizeMinuteTime(bars[len(bars)-1].TradeTime)
	return !first.After(start) && !last.Before(end)
}

func minuteBarsCoverTradingSessions(bars []minuteBar, start, end time.Time) bool {
	return minuteBarsCoverTradingSessionsForStock("", bars, start, end)
}

func minuteBarsCoverTradingSessionsForStock(stockCode string, bars []minuteBar, start, end time.Time) bool {
	return minuteBarsCoverTradingSessionsForStockWithSuspensionFetch(stockCode, bars, start, end, false)
}

func minuteBarsCoverTradingSessionsForStockWithSuspensionFetch(stockCode string, bars []minuteBar, start, end time.Time, allowSuspensionFetch bool) bool {
	sessions := buildMinuteCoverageSessions(start, end)
	if len(sessions) == 0 {
		return true
	}
	if len(bars) == 0 {
		return minuteCoverageGapCoveredBySuspensionWithFetch(stockCode, start, end, allowSuspensionFetch)
	}
	ordered := append([]minuteBar(nil), bars...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return normalizeMinuteTime(ordered[i].TradeTime).Before(normalizeMinuteTime(ordered[j].TradeTime))
	})
	idx := 0
	const tolerance = 5 * time.Minute
	for _, session := range sessions {
		sessionBars := make([]minuteBar, 0, 128)
		for idx < len(ordered) && normalizeMinuteTime(ordered[idx].TradeTime).Before(session.Start) {
			idx++
		}
		for scan := idx; scan < len(ordered); scan++ {
			barTime := normalizeMinuteTime(ordered[scan].TradeTime)
			if barTime.After(session.End) {
				break
			}
			sessionBars = append(sessionBars, ordered[scan])
		}
		if len(sessionBars) == 0 {
			if minuteCoverageGapCoveredBySuspensionWithFetch(stockCode, session.Start, session.End, allowSuspensionFetch) {
				continue
			}
			return false
		}
		first := normalizeMinuteTime(sessionBars[0].TradeTime)
		last := normalizeMinuteTime(sessionBars[len(sessionBars)-1].TradeTime)
		if first.After(session.Start) {
			if !minuteCoverageGapCoveredBySuspensionWithFetch(stockCode, session.Start, first.Add(-time.Minute), allowSuspensionFetch) {
				return false
			}
		}
		if last.Before(session.End) {
			if !minuteCoverageGapCoveredBySuspensionWithFetch(stockCode, last.Add(time.Minute), session.End, allowSuspensionFetch) {
				return false
			}
		}
		prev := first
		for i := 1; i < len(sessionBars); i++ {
			cur := normalizeMinuteTime(sessionBars[i].TradeTime)
			if cur.Sub(prev) > tolerance {
				if minuteCoverageGapCoveredBySuspensionWithFetch(stockCode, prev.Add(time.Minute), cur.Add(-time.Minute), allowSuspensionFetch) {
					prev = cur
					continue
				}
				return false
			}
			prev = cur
		}
	}
	return true
}

func akShareScriptEnv() []string {
	if forceNoProxyForFetchEnabled() {
		return envWithoutProxy()
	}
	mode := appconfig.Load().Akshare.ProxyMode
	switch mode {
	case "inherit":
		// Use whatever proxy settings are present in the environment.
		return os.Environ()
	default:
		// Default: disable proxy to avoid broken proxy env causing hard failures.
		return envWithoutProxy()
	}
}

func akShareFetchTimeout() time.Duration {
	return time.Duration(appconfig.Load().Akshare.TimeoutSec) * time.Second
}

func waitForAkShareFetchWindow() {
	interval := akShareFetchMinInterval()
	if interval <= 0 {
		return
	}
	akShareFetchMu.Lock()
	defer akShareFetchMu.Unlock()

	if !akShareLastFetch.IsZero() {
		elapsed := time.Since(akShareLastFetch)
		if elapsed < interval {
			time.Sleep(interval - elapsed)
		}
	}
	akShareLastFetch = time.Now()
}

func akShareFetchMinInterval() time.Duration {
	return time.Duration(appconfig.Load().Akshare.MinIntervalMS) * time.Millisecond
}

func akShareRetryWait() time.Duration {
	return time.Duration(appconfig.Load().Akshare.RetryWaitMS) * time.Millisecond
}

func akShareScriptPath() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("GO_STOCK_AKSHARE_SCRIPT")); configured != "" {
		if filepath.IsAbs(configured) {
			if _, err := os.Stat(configured); err == nil {
				return configured, nil
			}
		} else if absPath, err := filepath.Abs(configured); err == nil {
			if _, statErr := os.Stat(absPath); statErr == nil {
				return absPath, nil
			}
		}
	}

	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		candidates := []string{
			filepath.Join(exeDir, "runtime", "python", "scripts", "akshare_minute_fetch.py"),
			filepath.Join(exeDir, "scripts", "akshare_minute_fetch.py"),
		}
		for _, candidate := range candidates {
			if _, statErr := os.Stat(candidate); statErr == nil {
				return candidate, nil
			}
		}
	}

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("resolve akshare script path failed")
	}
	baseDir := filepath.Dir(currentFile)
	candidate := filepath.Join(baseDir, "scripts", "akshare_minute_fetch.py")
	if _, err := os.Stat(candidate); err != nil {
		return "", fmt.Errorf("resolve akshare script path failed: %w", err)
	}
	return candidate, nil
}

func parseMinuteTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	loc := cnLocation()
	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"20060102150405",
		"200601021504",
		"2006/01/02 15:04:05",
		"2006/01/02 15:04",
	}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, raw, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unknown minute time: %s", raw)
}

func dedupeMinuteBars(bars []minuteBar) []minuteBar {
	if len(bars) <= 1 {
		return bars
	}
	result := make([]minuteBar, 0, len(bars))
	seen := make(map[time.Time]struct{}, len(bars))
	for _, bar := range bars {
		t := normalizeMinuteTime(bar.TradeTime)
		if t.IsZero() {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		bar.TradeTime = t
		result = append(result, bar)
	}
	return result
}

func extractAShareSymbol(stockCode string) string {
	code := strings.ToUpper(strings.TrimSpace(stockCode))
	if code == "" {
		return ""
	}
	if strings.Contains(code, ".") {
		parts := strings.Split(code, ".")
		if len(parts) == 2 {
			n := RemoveAllNonDigitChar(parts[0])
			if len(n) == 6 {
				return n
			}
		}
	}
	if strings.HasPrefix(strings.ToLower(code), "sh") || strings.HasPrefix(strings.ToLower(code), "sz") {
		n := RemoveAllNonDigitChar(code)
		if len(n) == 6 {
			return n
		}
	}
	n := RemoveAllNonDigitChar(code)
	if len(n) == 6 {
		return n
	}
	return ""
}

func envWithoutProxy() []string {
	raw := os.Environ()
	env := make([]string, 0, len(raw)+2)
	for _, item := range raw {
		parts := strings.SplitN(item, "=", 2)
		if len(parts) == 0 {
			continue
		}
		key := strings.ToUpper(strings.TrimSpace(parts[0]))
		switch key {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy":
			continue
		default:
			env = append(env, item)
		}
	}
	env = append(env, "NO_PROXY=*")
	env = append(env, "no_proxy=*")
	return env
}
