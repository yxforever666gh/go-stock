package data

import (
	"fmt"
	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	appconfig "go-stock/internal/config"
	"runtime"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type minuteSyncInfo struct {
	SyncErr      error
	LastMinuteTs *time.Time
	CacheStart   *time.Time
	CacheEnd     *time.Time
	CacheUpdated *time.Time
	CacheSource  string
	CoverageOK   bool
}

func syncMinuteBars(tsCode string, start, end time.Time, crawlTimeout int64, allowHeadBackfill bool) ([]minuteBar, minuteSyncInfo) {
	return syncMinuteBarsWithFetch(tsCode, start, end, crawlTimeout, allowHeadBackfill, true)
}

func syncMinuteBarsForcedWindow(tsCode string, start, end time.Time, crawlTimeout int64) ([]minuteBar, minuteSyncInfo) {
	return syncMinuteBarsWithOptions(tsCode, start, end, crawlTimeout, false, true, true)
}

func syncMinuteBarsFromCacheOnly(tsCode string, start, end time.Time) ([]minuteBar, minuteSyncInfo) {
	return syncMinuteBarsWithFetch(tsCode, start, end, 0, false, false)
}

func syncMinuteBarsWithFetch(tsCode string, start, end time.Time, crawlTimeout int64, allowHeadBackfill bool, allowFetch bool) ([]minuteBar, minuteSyncInfo) {
	return syncMinuteBarsWithOptions(tsCode, start, end, crawlTimeout, allowHeadBackfill, allowFetch, false)
}

func syncMinuteBarsWithOptions(tsCode string, start, end time.Time, crawlTimeout int64, allowHeadBackfill bool, allowFetch bool, forceWindowFetch bool) ([]minuteBar, minuteSyncInfo) {
	info := minuteSyncInfo{}
	start = normalizeMinuteTime(start)
	end = normalizeMinuteTime(end)
	if start.After(end) {
		return []minuteBar{}, info
	}

	cacheStart, cacheEnd, scopeErr := getMinuteCacheRange(tsCode)
	if scopeErr != nil {
		info.SyncErr = scopeErr
	}
	info.CacheStart = cacheStart
	info.CacheEnd = cacheEnd

	fetchedCount := 0
	if allowFetch {
		missingWindows := buildMinuteFetchWindows(start, end, cacheStart, cacheEnd, allowHeadBackfill)
		if forceWindowFetch {
			missingWindows = []minuteFetchWindow{{Start: start, End: end}}
		}
		for _, window := range missingWindows {
			if window.Start.After(window.End) {
				continue
			}
			fetched, source, fetchErr := fetchMinuteBarsFromProviders(tsCode, window.Start, window.End, minuteProviderAttemptTimeout(crawlTimeout))
			if audit := currentActiveManualYieldAudit(); audit != nil && source != "" {
				audit.recordProvider(source)
			}
			if source != "" {
				info.CacheSource = source
			}
			if fetchErr != nil {
				// Best-effort: some providers may return partial data along with an
				// error (e.g. rate limit on a later page). Persist what we got so the
				// cache can still advance, then keep the error for observability.
				info.SyncErr = mergeSyncErr(info.SyncErr, fetchErr)
			}
			if len(fetched) > 0 {
				inserted, upsertErr := upsertMinuteBarsToCache(tsCode, fetched, source)
				if upsertErr != nil {
					info.SyncErr = mergeSyncErr(info.SyncErr, upsertErr)
					continue
				}
				fetchedCount += inserted
			}
			// If fetch failed and returned nothing, continue to the next window.
		}
	}

	bars, reloadErr := listMinuteBarsFromCache(tsCode, start, end)
	if reloadErr != nil {
		info.SyncErr = mergeSyncErr(info.SyncErr, reloadErr)
		bars = []minuteBar{}
	}

	if len(bars) > 0 {
		last := bars[len(bars)-1].TradeTime
		info.LastMinuteTs = &last
	}

	cacheStart, cacheEnd, scopeErr = getMinuteCacheRange(tsCode)
	if scopeErr != nil {
		info.SyncErr = mergeSyncErr(info.SyncErr, scopeErr)
	} else {
		info.CacheStart = cacheStart
		info.CacheEnd = cacheEnd
	}

	if fetchedCount > 0 {
		now := time.Now()
		info.CacheUpdated = &now
	}

	// Determine whether cached minute bars fully cover the requested window.
	if info.CacheStart != nil && info.CacheEnd != nil {
		startCovered := minuteStartCovered(start, *info.CacheStart)
		endCovered := !info.CacheEnd.Before(end)
		info.CoverageOK = startCovered && endCovered
	}
	return bars, info
}

type minuteFetchWindow struct {
	Start time.Time
	End   time.Time
}

type minuteWindowClass string

const (
	minuteWindowTodayIntraday minuteWindowClass = "today_intraday"
	minuteWindowRecent        minuteWindowClass = "recent"
	minuteWindowHistorical    minuteWindowClass = "historical"
)

type minuteProviderAttempt struct {
	Provider string
	Delay    time.Duration
}

type minuteProviderResult struct {
	Provider string
	Source   string
	Bars     []minuteBar
	Err      error
	Complete bool
}

func fetchMinuteBarsWithNamedProviderTimeout(provider string, tsCode string, start, end time.Time, timeout time.Duration) minuteProviderResult {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	resultCh := make(chan minuteProviderResult, 1)
	go func() {
		bars, source, err := fetchMinuteBarsWithNamedProvider(provider, tsCode, start, end)
		resultCh <- buildMinuteProviderResult(provider, bars, source, err, start, end)
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case res := <-resultCh:
		return res
	case <-timer.C:
		return buildMinuteProviderResult(provider, nil, provider, fmt.Errorf("分钟线数据源响应超时：%s provider=%s", tsCode, provider), start, end)
	}
}

func minuteProviderAttemptTimeout(crawlTimeout int64) time.Duration {
	if crawlTimeout <= 0 {
		crawlTimeout = 20
	}
	timeout := time.Duration(crawlTimeout) * time.Second
	if timeout < 5*time.Second {
		return 5 * time.Second
	}
	if timeout > 20*time.Second {
		return 20 * time.Second
	}
	return timeout
}

func shouldAutoHeadBackfill(start, end time.Time, cacheStart *time.Time) bool {
	if cacheStart == nil || cacheStart.IsZero() {
		return false
	}
	if !cacheStart.After(start) {
		return false
	}

	loc := cnLocation()
	now := timeNow().In(loc)
	start = normalizeMinuteTime(start.In(loc))
	end = normalizeMinuteTime(end.In(loc))
	gapEnd := normalizeMinuteTime(cacheStart.In(loc).Add(-time.Minute))
	if gapEnd.Before(start) {
		return false
	}

	// Keep automatic backfill conservative:
	// only patch recent head gaps so we don't keep hammering public sources for
	// very old windows, while still fixing short missing spans like T+1 gaps.
	if end.Before(now.Add(-14 * 24 * time.Hour)) {
		return false
	}
	if gapEnd.Sub(start) > 3*24*time.Hour {
		return false
	}
	return true
}

func buildMinuteFetchWindows(start, end time.Time, cacheStart, cacheEnd *time.Time, allowHeadBackfill bool) []minuteFetchWindow {
	if start.After(end) {
		return []minuteFetchWindow{}
	}
	if cacheStart == nil || cacheEnd == nil {
		return []minuteFetchWindow{{Start: start, End: end}}
	}

	// Many public providers (including AkShare backends) only provide recent
	// minute data. Continuously trying to "backfill" very old head windows in
	// background refresh will cause repeated downloads and trigger upstream rate
	// limits. Head backfill is allowed only for explicit manual requests.
	autoHeadBackfill := shouldAutoHeadBackfill(start, end, cacheStart)
	windows := make([]minuteFetchWindow, 0, 2)
	if (allowHeadBackfill || autoHeadBackfill) && cacheStart.After(start) {
		headEnd := cacheStart.Add(-time.Minute)
		if headEnd.After(end) {
			headEnd = end
		}
		if !start.After(headEnd) {
			windows = append(windows, minuteFetchWindow{Start: start, End: headEnd})
		}
	}
	if cacheEnd.Before(end) {
		tailStart := cacheEnd.Add(time.Minute)
		if tailStart.Before(start) {
			tailStart = start
		}
		if !tailStart.After(end) {
			windows = append(windows, minuteFetchWindow{Start: tailStart, End: end})
		}
	}
	return windows
}

func mergeSyncErr(base, current error) error {
	if current == nil {
		return base
	}
	if base == nil {
		return current
	}
	if strings.Contains(base.Error(), current.Error()) {
		return base
	}
	return fmt.Errorf("%v; %v", base, current)
}

func fetchMinuteBarsFromProviders(tsCode string, start, end time.Time, timeout time.Duration) ([]minuteBar, string, error) {
	provider := appconfig.Load().Minute.Provider
	switch provider {
	case "public", "diemeng", "akshare", "auto", "sina", "tencent":
	default:
		logger.SugaredLogger.Warnf("unknown minute provider %q; fallback to public", provider)
		provider = "public"
	}
	hedgedPlan, fallbackPlan, err := buildMinuteProviderPlan(provider, start, end)
	if err != nil {
		return []minuteBar{}, "", err
	}
	return executeMinuteProviderPlan(tsCode, start, end, hedgedPlan, fallbackPlan, timeout)
}

func minuteAkshareFallbackEnabled() bool {
	return appconfig.Load().Minute.FallbackAkshare
}

func minuteTencentFallbackEnabled() bool {
	return appconfig.Load().Minute.FallbackTencent
}

func minuteProviderSettings() *Settings {
	cfg := GetSettingConfig()
	if cfg == nil || cfg.Settings == nil {
		return nil
	}
	return cfg.Settings
}

func minutePublicSinaEnabled() bool {
	settings := minuteProviderSettings()
	if settings == nil {
		return true
	}
	if normalizeMinuteProviderMode(settings.MinuteProviderMode) != "public" {
		return true
	}
	return settings.SinaMinuteEnabled
}

func minutePublicTencentEnabled() bool {
	settings := minuteProviderSettings()
	if settings == nil {
		return true
	}
	if normalizeMinuteProviderMode(settings.MinuteProviderMode) != "public" {
		return true
	}
	return settings.TencentMinuteEnabled
}

func minutePublicAkshareEnabled() bool {
	settings := minuteProviderSettings()
	if settings == nil {
		return true
	}
	if normalizeMinuteProviderMode(settings.MinuteProviderMode) != "public" {
		return true
	}
	return settings.AkshareEnabled
}

func minuteHistoricalPrivateFallbackReady() bool {
	settings := minuteProviderSettings()
	if settings == nil || !settings.PrivateMinuteEnabled {
		return false
	}
	if strings.TrimSpace(appconfig.Load().Diemeng.BaseURL) == "" {
		return false
	}
	return hasDiemengKey()
}

func currentMinuteProviderMode() string {
	settings := minuteProviderSettings()
	if settings == nil {
		return "public"
	}
	return normalizeMinuteProviderMode(settings.MinuteProviderMode)
}

func executeMinuteProviderPlan(tsCode string, start, end time.Time, hedgedPlan []minuteProviderAttempt, fallbackPlan []string, timeout time.Duration) ([]minuteBar, string, error) {
	if len(hedgedPlan) == 0 {
		return []minuteBar{}, "", fmt.Errorf("当前分钟线配置不可用，请检查数据源设置")
	}
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	type asyncResult struct {
		result minuteProviderResult
	}
	resultCh := make(chan asyncResult, len(hedgedPlan))
	for _, attempt := range hedgedPlan {
		attempt := attempt
		go func() {
			if attempt.Delay > 0 {
				time.Sleep(attempt.Delay)
			}
			bars, source, err := fetchMinuteBarsWithNamedProvider(attempt.Provider, tsCode, start, end)
			resultCh <- asyncResult{result: buildMinuteProviderResult(attempt.Provider, bars, source, err, start, end)}
		}()
	}

	attempted := make(map[string]struct{}, len(hedgedPlan)+len(fallbackPlan))
	var best minuteProviderResult
	hasBest := false
	var mergedErr error

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for i := 0; i < len(hedgedPlan); i++ {
		var res minuteProviderResult
		select {
		case async := <-resultCh:
			res = async.result
		case <-deadline.C:
			if hasBest && len(best.Bars) > 0 {
				return best.Bars, best.Source, mergeSyncErr(mergedErr, fmt.Errorf("分钟线数据源响应超时：%s", tsCode))
			}
			return []minuteBar{}, "", mergeSyncErr(mergedErr, fmt.Errorf("分钟线数据源响应超时：%s", tsCode))
		}
		attempted[res.Provider] = struct{}{}
		if res.Complete && res.Err == nil {
			return res.Bars, res.Source, nil
		}
		if !hasBest || isBetterMinuteProviderResult(res, best) {
			best = res
			hasBest = len(res.Bars) > 0 || res.Err != nil
		}
		if res.Err != nil {
			mergedErr = mergeSyncErr(mergedErr, res.Err)
		}
		if hasBest && len(best.Bars) > 0 {
			return best.Bars, best.Source, mergedErr
		}
	}

	for _, provider := range fallbackPlan {
		provider = strings.TrimSpace(provider)
		if provider == "" {
			continue
		}
		if _, ok := attempted[provider]; ok {
			continue
		}
		attempted[provider] = struct{}{}
		res := fetchMinuteBarsWithNamedProviderTimeout(provider, tsCode, start, end, timeout)
		if res.Complete && res.Err == nil {
			return res.Bars, res.Source, nil
		}
		if !hasBest || isBetterMinuteProviderResult(res, best) {
			best = res
			hasBest = len(res.Bars) > 0 || res.Err != nil
		}
		if res.Err != nil {
			mergedErr = mergeSyncErr(mergedErr, res.Err)
		}
	}

	if hasBest {
		return best.Bars, best.Source, mergeSyncErr(mergedErr, best.Err)
	}
	return []minuteBar{}, "", mergedErr
}

func buildMinuteProviderPlan(provider string, start, end time.Time) ([]minuteProviderAttempt, []string, error) {
	adaptiveHedged, adaptiveFallback := buildAdaptiveMinuteProviderPlan(start, end)
	switch provider {
	case "sina":
		return []minuteProviderAttempt{{Provider: "sina"}}, mergeUniqueProviders(append(adaptiveProvidersOnly(adaptiveHedged), "tencent", "diemeng", "akshare")...), nil
	case "tencent":
		return []minuteProviderAttempt{{Provider: "tencent"}}, mergeUniqueProviders(append(adaptiveProvidersOnly(adaptiveHedged), "diemeng", "sina", "akshare")...), nil
	case "akshare":
		return []minuteProviderAttempt{{Provider: "akshare"}}, mergeUniqueProviders(append(adaptiveProvidersOnly(adaptiveHedged), "diemeng", "tencent", "sina")...), nil
	case "public":
		return buildPublicMinuteProviderPlan(start, end)
	case "auto", "diemeng":
		return adaptiveHedged, adaptiveFallback, nil
	default:
		return adaptiveHedged, adaptiveFallback, nil
	}
}

func buildPublicMinuteProviderPlan(start, end time.Time) ([]minuteProviderAttempt, []string, error) {
	switch classifyMinuteWindow(start, end) {
	case minuteWindowTodayIntraday:
		attempts := make([]minuteProviderAttempt, 0, 3)
		fallback := make([]string, 0, 3)
		if minutePublicSinaEnabled() {
			attempts = append(attempts, minuteProviderAttempt{Provider: "sina"})
		}
		if minutePublicTencentEnabled() {
			attempts = append(attempts, minuteProviderAttempt{Provider: "tencent", Delay: yieldHedgeTencentDelay()})
			fallback = append(fallback, "tencent")
		}
		if minutePublicAkshareEnabled() {
			fallback = append(fallback, "akshare")
		}
		if len(attempts) == 0 && minutePublicAkshareEnabled() {
			attempts = append(attempts, minuteProviderAttempt{Provider: "akshare"})
		}
		if len(attempts) == 0 {
			return nil, nil, fmt.Errorf("公共分钟线模式下未启用任何可用数据源")
		}
		return attempts, mergeUniqueProviders(fallback...), nil
	case minuteWindowRecent:
		attempts := make([]minuteProviderAttempt, 0, 2)
		fallback := make([]string, 0, 3)
		if minutePublicTencentEnabled() {
			attempts = append(attempts, minuteProviderAttempt{Provider: "tencent"})
		}
		if minutePublicAkshareEnabled() {
			attempts = append(attempts, minuteProviderAttempt{Provider: "akshare", Delay: yieldHedgeTencentDelay()})
		}
		if minutePublicSinaEnabled() {
			fallback = append(fallback, "sina")
		}
		if len(attempts) == 0 && minutePublicSinaEnabled() {
			attempts = append(attempts, minuteProviderAttempt{Provider: "sina"})
		}
		if len(attempts) == 0 {
			return nil, nil, fmt.Errorf("公共分钟线模式下未启用任何可用数据源")
		}
		return attempts, mergeUniqueProviders(fallback...), nil
	default:
		if minuteHistoricalPrivateFallbackReady() {
			return []minuteProviderAttempt{{Provider: "diemeng"}}, nil, nil
		}
		return nil, nil, fmt.Errorf("公共分钟线仅适合实时与短周期窗口，长历史分钟线请改用私人分钟线来源")
	}
}

func buildAdaptiveMinuteProviderPlan(start, end time.Time) ([]minuteProviderAttempt, []string) {
	switch classifyMinuteWindow(start, end) {
	case minuteWindowTodayIntraday:
		return []minuteProviderAttempt{
				{Provider: "sina"},
				{Provider: "tencent", Delay: yieldHedgeTencentDelay()},
				{Provider: "diemeng", Delay: yieldHedgeDiemengDelay()},
			},
			mergeUniqueProviders("akshare")
	case minuteWindowRecent:
		return []minuteProviderAttempt{
				{Provider: "tencent"},
				{Provider: "diemeng", Delay: yieldHedgeTencentDelay()},
			},
			mergeUniqueProviders("sina", "akshare")
	default:
		return []minuteProviderAttempt{
				{Provider: "diemeng"},
			},
			mergeUniqueProviders("akshare", "tencent", "sina")
	}
}

func classifyMinuteWindow(start, end time.Time) minuteWindowClass {
	if canUseSinaMinuteWindow(start, end) {
		loc := cnLocation()
		cur := end.In(loc)
		day := time.Date(cur.Year(), cur.Month(), cur.Day(), 0, 0, 0, 0, loc)
		open931 := time.Date(day.Year(), day.Month(), day.Day(), 9, 31, 0, 0, loc)
		if !cur.Before(open931) {
			return minuteWindowTodayIntraday
		}
	}

	loc := cnLocation()
	currentDay := time.Date(timeNow().In(loc).Year(), timeNow().In(loc).Month(), timeNow().In(loc).Day(), 0, 0, 0, 0, loc)
	if !isCNOpenTradeDaySafe(currentDay) {
		currentDay = shiftToPrevCNOpenTradeDaySafe(currentDay.AddDate(0, 0, -1))
	}
	cutoff := currentDay
	for i := 1; i < yieldRecentWindowTradeDays(); i++ {
		cutoff = shiftToPrevCNOpenTradeDaySafe(cutoff.AddDate(0, 0, -1))
	}
	if !normalizeMinuteTime(end.In(loc)).Before(cutoff) {
		return minuteWindowRecent
	}
	return minuteWindowHistorical
}

func fetchMinuteBarsWithNamedProvider(provider string, tsCode string, start, end time.Time) ([]minuteBar, string, error) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "sina":
		bars, source, err := fetchMinuteBarsWithSinaFn(tsCode, start, end)
		if strings.TrimSpace(source) == "" {
			source = "sina"
		}
		return bars, source, err
	case "tencent":
		bars, source, err := fetchMinuteBarsWithTencentFn(tsCode, start, end)
		if strings.TrimSpace(source) == "" {
			source = "tencent"
		}
		return bars, source, err
	case "akshare":
		bars, source, err := fetchMinuteBarsWithAkShareFn(tsCode, start, end)
		if strings.TrimSpace(source) == "" {
			source = "akshare"
		}
		return bars, source, err
	default:
		bars, source, err := fetchMinuteBarsWithDiemengFn(tsCode, start, end)
		if strings.TrimSpace(source) == "" {
			source = "diemeng"
		}
		return bars, source, err
	}
}

func buildMinuteProviderResult(provider string, bars []minuteBar, source string, err error, start, end time.Time) minuteProviderResult {
	if strings.TrimSpace(source) == "" {
		source = strings.TrimSpace(provider)
	}
	return minuteProviderResult{
		Provider: provider,
		Source:   source,
		Bars:     bars,
		Err:      err,
		Complete: minuteBarsCoverRange(bars, start, end),
	}
}

func isBetterMinuteProviderResult(candidate, current minuteProviderResult) bool {
	if candidate.Complete != current.Complete {
		return candidate.Complete
	}
	candidateSpan := minuteBarsCoverageSpan(candidate.Bars)
	currentSpan := minuteBarsCoverageSpan(current.Bars)
	if candidateSpan != currentSpan {
		return candidateSpan > currentSpan
	}
	if len(candidate.Bars) != len(current.Bars) {
		return len(candidate.Bars) > len(current.Bars)
	}
	return minuteProviderPriority(candidate.Provider) < minuteProviderPriority(current.Provider)
}

func minuteBarsCoverageSpan(bars []minuteBar) time.Duration {
	if len(bars) < 2 {
		return 0
	}
	first := normalizeMinuteTime(bars[0].TradeTime)
	last := normalizeMinuteTime(bars[len(bars)-1].TradeTime)
	if last.Before(first) {
		return 0
	}
	return last.Sub(first)
}

func minuteProviderPriority(provider string) int {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "diemeng":
		return 0
	case "tencent":
		return 1
	case "sina":
		return 2
	case "akshare":
		return 3
	default:
		return 9
	}
}

func adaptiveProvidersOnly(plan []minuteProviderAttempt) []string {
	out := make([]string, 0, len(plan))
	for _, item := range plan {
		out = append(out, item.Provider)
	}
	return out
}

func mergeUniqueProviders(providers ...string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(providers))
	for _, provider := range providers {
		provider = strings.ToLower(strings.TrimSpace(provider))
		if provider == "" {
			continue
		}
		if provider == "akshare" && !yieldAkshareFallbackEnabled() {
			continue
		}
		if _, ok := seen[provider]; ok {
			continue
		}
		seen[provider] = struct{}{}
		out = append(out, provider)
	}
	return out
}

func canUseSinaMinuteWindow(start, end time.Time) bool {
	return isSameCNTradeDate(start, end) && isTodayCN(end)
}

func scanMinuteTriggerFromBars(bars []minuteBar, stopProfit, stopLoss *float64) (string, time.Time, float64) {
	for _, bar := range bars {
		if stopLoss != nil && bar.Open <= *stopLoss {
			return "已止损", bar.TradeTime, bar.Open
		}
		if stopProfit != nil && bar.Open >= *stopProfit {
			return "已止盈", bar.TradeTime, bar.Open
		}
		if stopProfit != nil && stopLoss != nil {
			if bar.Low <= *stopLoss && bar.High >= *stopProfit {
				return "已止损", bar.TradeTime, *stopLoss
			}
		}
		if stopProfit != nil && bar.High >= *stopProfit {
			return "已止盈", bar.TradeTime, *stopProfit
		}
		if stopLoss != nil && bar.Low <= *stopLoss {
			return "已止损", bar.TradeTime, *stopLoss
		}
	}
	return "", time.Time{}, 0
}

func shouldUpdateActiveStock(existing *models.AiRecommendYieldState, force bool, inTrading bool, latestTradeDate, now time.Time) bool {
	if force {
		return true
	}
	if existing == nil {
		return true
	}
	if existing.Frozen {
		return true
	}
	if existing.LastRecalcAt == nil {
		return true
	}
	if inTrading {
		return now.Sub(*existing.LastRecalcAt) >= 15*time.Minute
	}
	if existing.LastMinuteTs == nil {
		return now.Sub(*existing.LastRecalcAt) >= 2*time.Hour
	}
	lastDay := time.Date(existing.LastMinuteTs.Year(), existing.LastMinuteTs.Month(), existing.LastMinuteTs.Day(), 0, 0, 0, 0, latestTradeDate.Location())
	if lastDay.Before(latestTradeDate) {
		return true
	}
	return now.Sub(*existing.LastRecalcAt) >= 2*time.Hour
}

func shouldUpdateActiveRecord(existing *models.AiRecommendYieldRecordState, force bool, inTrading bool, latestTradeDate, now time.Time) bool {
	if force {
		return true
	}
	if existing == nil {
		return true
	}
	if existing.Frozen {
		return true
	}
	if existing.LastRecalcAt == nil {
		return true
	}
	if inTrading {
		return now.Sub(*existing.LastRecalcAt) >= 15*time.Minute
	}
	if existing.LastMinuteTs == nil {
		return now.Sub(*existing.LastRecalcAt) >= 2*time.Hour
	}
	lastDay := time.Date(existing.LastMinuteTs.Year(), existing.LastMinuteTs.Month(), existing.LastMinuteTs.Day(), 0, 0, 0, 0, latestTradeDate.Location())
	if lastDay.Before(latestTradeDate) {
		return true
	}
	return now.Sub(*existing.LastRecalcAt) >= 2*time.Hour
}

func resolveMinuteEvalEnd(now time.Time, inTrading bool, latestTradeDate time.Time) time.Time {
	if inTrading {
		return now
	}
	if latestTradeDate.IsZero() {
		return now
	}
	loc := latestTradeDate.Location()
	end := time.Date(latestTradeDate.Year(), latestTradeDate.Month(), latestTradeDate.Day(), 15, 0, 0, 0, loc)
	if end.Before(now) {
		return end
	}
	return now
}

func resolveLatestCloseEvalEnd(now, latestTradeDate time.Time) time.Time {
	loc := cnLocation()
	cur := now.In(loc)
	if latestTradeDate.IsZero() {
		latestTradeDate = cur
	}
	day := time.Date(latestTradeDate.Year(), latestTradeDate.Month(), latestTradeDate.Day(), 0, 0, 0, 0, loc)
	close1500 := time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, loc)

	// If this day's close hasn't happened yet, use the previous trading close.
	if cur.Before(close1500) {
		probe := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
		return normalizeMinuteCoverageEnd(probe)
	}
	return normalizeMinuteCoverageEnd(close1500)
}

func isCNTradingSession(now time.Time) bool {
	weekday := now.Weekday()
	if weekday == time.Saturday || weekday == time.Sunday {
		return false
	}
	minutes := now.Hour()*60 + now.Minute()
	// Treat the whole trading day (including lunch break) as "in trading" so
	// minute coverage end can clamp correctly to 11:30 during lunch when users
	// click "手动下载分钟线".
	return minutes >= 9*60+30 && minutes <= 15*60
}

func fillYieldMetrics(state *models.AiRecommendYieldState) {
	state.YieldRate = 0
	state.YieldRateText = "--"
	if state.BuyAmount <= 0 {
		return
	}

	if state.RealizedSellAmount != nil {
		result := calculateNetYield(state.StockCode, state.BuyAmount, *state.RealizedSellAmount)
		if result.Valid {
			state.YieldRate = result.YieldRate
			state.YieldRateText = result.YieldText
		}
		return
	}
	if state.CurrentPrice > 0 {
		result := calculateNetYield(state.StockCode, state.BuyAmount, state.CurrentPrice)
		if result.Valid {
			state.YieldRate = result.YieldRate
			state.YieldRateText = result.YieldText
		}
	}
}

func fillYieldRecordMetrics(state *models.AiRecommendYieldRecordState) {
	state.YieldRate = 0
	state.YieldRateText = "--"
	if state.BuyAmount <= 0 {
		return
	}

	if state.RealizedSellAmount != nil {
		result := calculateNetYield(state.StockCode, state.BuyAmount, *state.RealizedSellAmount)
		if result.Valid {
			state.YieldRate = result.YieldRate
			state.YieldRateText = result.YieldText
		}
		return
	}
	if state.CurrentPrice > 0 {
		result := calculateNetYield(state.StockCode, state.BuyAmount, state.CurrentPrice)
		if result.Valid {
			state.YieldRate = result.YieldRate
			state.YieldRateText = result.YieldText
		}
	}
}

func buildSellAmountText(stopProfit, stopLoss *float64) string {
	profitText := formatPricePointer(stopProfit)
	lossText := formatPricePointer(stopLoss)
	return profitText + "/" + lossText
}

func formatPricePointer(v *float64) string {
	if v == nil {
		return "--"
	}
	if *v <= 0 {
		return "--"
	}
	return fmt.Sprintf("%.2f", *v)
}

func calculateAvg(sum float64, count int) float64 {
	if count <= 0 {
		return 0
	}
	return round2(sum / float64(count))
}

func toQuoteCode(stockCode string) string {
	code := strings.TrimSpace(stockCode)
	if code == "" {
		return ""
	}
	upper := strings.ToUpper(code)
	if strings.Contains(upper, ".") {
		return strings.ToLower(ConvertTushareCodeToStockCode(upper))
	}
	return strings.ToLower(code)
}

func normalizeRecommendStockCode(stockCode string) string {
	code := strings.TrimSpace(strings.ToUpper(stockCode))
	if code == "" {
		return ""
	}
	if isAShareTsCode(code) {
		return canonicalizeAShareTsCode(code)
	}
	if strings.Contains(code, ".") {
		return code
	}

	lower := strings.ToLower(code)
	if strings.HasPrefix(lower, "sh") || strings.HasPrefix(lower, "sz") {
		return canonicalizeAShareTsCode(strings.ToUpper(ConvertStockCodeToTushareCode(lower)))
	}

	digits := RemoveAllNonDigitChar(code)
	if len(digits) == 6 {
		if strings.HasPrefix(digits, "6") || strings.HasPrefix(digits, "9") || strings.HasPrefix(digits, "5") {
			return canonicalizeAShareTsCode(digits + ".SH")
		}
		return canonicalizeAShareTsCode(digits + ".SZ")
	}
	return code
}

func canonicalizeAShareTsCode(code string) string {
	upper := strings.ToUpper(strings.TrimSpace(code))
	if !isAShareTsCode(upper) {
		return upper
	}
	symbol := RemoveAllNonDigitChar(upper)
	if len(symbol) != 6 {
		return upper
	}
	canonical := lookupCanonicalAShareTsCode(symbol)
	if canonical == "" {
		return upper
	}
	return canonical
}

func lookupCanonicalAShareTsCode(symbol string) string {
	symbol = strings.TrimSpace(symbol)
	if len(symbol) != 6 {
		return ""
	}
	if cached, ok := canonicalAShareTsCodeCache.Load(symbol); ok {
		return cached.(string)
	}

	canonical := ""
	if db.Dao != nil {
		row := StockBasic{}
		err := db.Dao.Model(&StockBasic{}).
			Select("ts_code").
			Where("symbol = ?", symbol).
			Where("deleted_at IS NULL").
			Where("(list_status = ? OR list_status = '' OR list_status IS NULL)", "L").
			Order("updated_at DESC").
			Limit(1).
			Take(&row).Error
		if err == nil {
			tsCode := strings.ToUpper(strings.TrimSpace(row.TsCode))
			if isAShareTsCode(tsCode) && RemoveAllNonDigitChar(tsCode) == symbol {
				canonical = tsCode
			}
		}
	}

	canonicalAShareTsCodeCache.Store(symbol, canonical)
	return canonical
}

func upsertYieldStates(states []models.AiRecommendYieldState) error {
	updateColumns := []string{
		"updated_at",
		"stock_name",
		"model_names",
		"bk_name",
		"recommend_count",
		"recommend_category",
		"recommend_time",
		"signal_time",
		"activation_status",
		"activation_time",
		"activation_price",
		"buy_time",
		"buy_amount",
		"stop_profit_amount",
		"stop_loss_amount",
		"sell_amount_text",
		"position_status",
		"sell_time",
		"realized_sell_amount",
		"current_price",
		"current_price_time",
		"yield_rate",
		"yield_rate_text",
		"data_status",
		"data_status_reason",
		"last_minute_ts",
		"last_recalc_at",
		"minute_cache_start",
		"minute_cache_end",
		"minute_cache_source",
		"minute_cache_updated",
		"frozen",
		"total_scope_start",
		"total_scope_end",
	}

	return runWithSQLiteBusyRetry(func() error {
		return db.Dao.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "stock_code"}},
			DoUpdates: clause.AssignmentColumns(updateColumns),
		}).CreateInBatches(states, 100).Error
	})
}

func upsertYieldRecordStates(states []models.AiRecommendYieldRecordState) error {
	updateColumns := []string{
		"updated_at",
		"stock_code",
		"stock_name",
		"model_name",
		"bk_name",
		"recommend_category",
		"recommend_time",
		"signal_time",
		"activation_status",
		"activation_time",
		"activation_price",
		"buy_time",
		"buy_amount",
		"stop_profit_amount",
		"stop_loss_amount",
		"sell_amount_text",
		"position_status",
		"sell_time",
		"realized_sell_amount",
		"current_price",
		"current_price_time",
		"yield_rate",
		"yield_rate_text",
		"data_status",
		"data_status_reason",
		"last_minute_ts",
		"last_recalc_at",
		"minute_cache_start",
		"minute_cache_end",
		"minute_cache_source",
		"minute_cache_updated",
		"frozen",
		"total_scope_start",
		"total_scope_end",
	}

	if err := runWithSQLiteBusyRetry(func() error {
		return db.Dao.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "recommend_id"}},
			DoUpdates: clause.AssignmentColumns(updateColumns),
		}).CreateInBatches(states, 100).Error
	}); err != nil {
		return err
	}
	if err := syncRecommendActivationStatusFromRecordStates(states); err != nil {
		return err
	}
	return clearAiRecommendYieldDirtyCodesByRecordStates(states)
}

func syncRecommendActivationStatusFromRecordStates(states []models.AiRecommendYieldRecordState) error {
	if len(states) == 0 {
		return nil
	}

	type recommendActivationSync struct {
		RecommendID             uint
		ActivationStatus        string
		ActivationInvalidReason string
	}

	updates := make([]recommendActivationSync, 0, len(states))
	seen := make(map[uint]struct{}, len(states))
	for _, state := range states {
		if state.RecommendID == 0 {
			continue
		}
		if _, exists := seen[state.RecommendID]; exists {
			continue
		}
		seen[state.RecommendID] = struct{}{}

		status := strings.TrimSpace(strings.ToLower(state.ActivationStatus))
		if status == "" {
			continue
		}
		update := recommendActivationSync{
			RecommendID:      state.RecommendID,
			ActivationStatus: status,
		}
		if status == "invalid" {
			update.ActivationInvalidReason = strings.TrimSpace(state.DataStatusReason)
		}
		updates = append(updates, update)
	}
	if len(updates) == 0 {
		return nil
	}

	for _, update := range updates {
		updateMap := map[string]any{
			"activation_status": update.ActivationStatus,
		}
		if update.ActivationStatus == "invalid" {
			updateMap["activation_invalid_reason"] = update.ActivationInvalidReason
		} else {
			updateMap["activation_invalid_reason"] = ""
		}
		if err := runWithSQLiteBusyRetry(func() error {
			return db.Dao.Model(&models.AiRecommendStocks{}).
				Where("id = ?", update.RecommendID).
				Updates(updateMap).Error
		}); err != nil {
			return err
		}
	}
	return nil
}

func cleanRemovedYieldStates(codes []string) error {
	if len(codes) == 0 {
		return db.Dao.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&models.AiRecommendYieldState{}).Error
	}
	return db.Dao.Where("stock_code NOT IN ?", codes).Delete(&models.AiRecommendYieldState{}).Error
}

func cleanRemovedYieldRecordStates(recordIDs []uint) error {
	if len(recordIDs) == 0 {
		return db.Dao.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&models.AiRecommendYieldRecordState{}).Error
	}
	return db.Dao.Where("recommend_id NOT IN ?", recordIDs).Delete(&models.AiRecommendYieldRecordState{}).Error
}

func updateYieldRecalcProgress(metaID uint, done, total int) error {
	percent := calculateRecalcPercent(done, total)
	return runWithSQLiteBusyRetry(func() error {
		return db.Dao.Model(&models.AiRecommendYieldMeta{}).Where("id = ?", metaID).Updates(map[string]any{
			"recalc_done":     done,
			"recalc_total":    total,
			"recalc_progress": percent,
			"updated_at":      time.Now(),
		}).Error
	})
}

func updateYieldDownloadProgress(metaID uint, done, total int) error {
	percent := calculateRecalcPercent(done, total)
	return runWithSQLiteBusyRetry(func() error {
		return db.Dao.Model(&models.AiRecommendYieldMeta{}).Where("id = ?", metaID).Updates(map[string]any{
			"download_done":        done,
			"download_total":       total,
			"download_progress":    percent,
			"download_in_progress": total > 0 && done < total,
			"updated_at":           time.Now(),
		}).Error
	})
}

func yieldDownloadWorkerCount() int {
	count := appconfig.Load().Yield.DownloadWorkers
	if count <= 0 {
		return 1
	}
	return count
}

func yieldCalcWorkerCount() int {
	count := appconfig.Load().Yield.CalcWorkers
	if count > 0 {
		return count
	}
	count = runtime.NumCPU()
	if count <= 0 {
		count = 1
	}
	if count > 8 {
		count = 8
	}
	return count
}

func yieldRecentWindowTradeDays() int {
	days := appconfig.Load().Yield.RecentWindowTradeDays
	if days <= 0 {
		return 1
	}
	return days
}

func yieldHedgeTencentDelay() time.Duration {
	return time.Duration(appconfig.Load().Yield.HedgeTencentDelayMS) * time.Millisecond
}

func yieldHedgeDiemengDelay() time.Duration {
	return time.Duration(appconfig.Load().Yield.HedgeDiemengDelayMS) * time.Millisecond
}

func yieldAkshareFallbackEnabled() bool {
	return appconfig.Load().Yield.AkshareFallback || minuteAkshareFallbackEnabled()
}

func calculateRecalcPercent(done, total int) int {
	if total <= 0 {
		if done > 0 {
			return 100
		}
		return 0
	}
	if done <= 0 {
		return 0
	}
	if done >= total {
		return 100
	}
	percent := int(float64(done) * 100 / float64(total))
	if percent < 0 {
		return 0
	}
	if percent > 100 {
		return 100
	}
	return percent
}

func normalizeScopeCodes(codes []string) map[string]struct{} {
	if len(codes) == 0 {
		return nil
	}
	result := make(map[string]struct{}, len(codes))
	for _, raw := range codes {
		code := normalizeRecommendStockCode(raw)
		if code == "" {
			continue
		}
		result[code] = struct{}{}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func mergeScopeMap(base, extra map[string]struct{}) map[string]struct{} {
	if len(extra) == 0 {
		return copyScopeMap(base)
	}
	if len(base) == 0 {
		return copyScopeMap(extra)
	}
	result := copyScopeMap(base)
	for code := range extra {
		result[code] = struct{}{}
	}
	return result
}

func copyScopeMap(scope map[string]struct{}) map[string]struct{} {
	if len(scope) == 0 {
		return nil
	}
	result := make(map[string]struct{}, len(scope))
	for code := range scope {
		result[code] = struct{}{}
	}
	return result
}
