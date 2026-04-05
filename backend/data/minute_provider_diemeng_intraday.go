package data

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"go-stock/backend/logger"
	appconfig "go-stock/internal/config"
)

var (
	diemengIntradayWarmMu   sync.Mutex
	diemengIntradayWarmSeen = map[string]time.Time{} // YYYY-MM-DD -> last attempt
)

func warmupDiemengMinuteBarsIntradayByHistory(now time.Time, codes []string) (int, error) {
	// Only warm up when minute provider is (primary) diemeng (or auto).
	provider := appconfig.Load().Minute.Provider
	if provider != "diemeng" && provider != "auto" {
		return 0, nil
	}

	if len(codes) == 0 {
		return 0, nil
	}

	loc := cnLocation()
	cur := now.In(loc)
	day := time.Date(cur.Year(), cur.Month(), cur.Day(), 0, 0, 0, 0, loc)
	dayKey := day.Format("2006-01-02")

	// Avoid repeated intraday pulls when user clicks multiple times.
	diemengIntradayWarmMu.Lock()
	if last, ok := diemengIntradayWarmSeen[dayKey]; ok && time.Since(last) < 45*time.Second {
		diemengIntradayWarmMu.Unlock()
		return 0, nil
	}
	diemengIntradayWarmSeen[dayKey] = time.Now()
	diemengIntradayWarmMu.Unlock()

	start := time.Date(day.Year(), day.Month(), day.Day(), 9, 30, 0, 0, loc)
	end := normalizeMinuteCoverageEnd(cur)
	if !start.Before(end) {
		return 0, nil
	}

	// Batch: docs say max 100 codes.
	const batchSize = 80
	normalized := make([]string, 0, len(codes))
	seen := map[string]struct{}{}
	for _, raw := range codes {
		code := strings.ToUpper(strings.TrimSpace(raw))
		if code == "" {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		normalized = append(normalized, code)
	}
	if len(normalized) == 0 {
		return 0, nil
	}

	totalInserted := 0
	var mergedErr error
	for i := 0; i < len(normalized); i += batchSize {
		j := i + batchSize
		if j > len(normalized) {
			j = len(normalized)
		}
		batch := normalized[i:j]
		inserted, err := warmupDiemengMinuteBarsHistoryBatch(dayKey, start, end, batch)
		if err != nil {
			mergedErr = mergeSyncErr(mergedErr, err)
			continue
		}
		totalInserted += inserted
	}
	return totalInserted, mergedErr
}

func warmupDiemengMinuteBarsHistoryBatch(dayKey string, start, end time.Time, codes []string) (int, error) {
	if len(codes) == 0 {
		return 0, nil
	}
	if !hasDiemengKey() {
		return 0, fmt.Errorf("missing GO_STOCK_DIEMENG_API_KEY")
	}
	if err := diemengCircuitCheck(); err != nil {
		return 0, err
	}

	client := newDiemengClient()
	apiKey := strings.TrimSpace(diemengAPIKey())

	level := appconfig.Load().Diemeng.Level

	logger.SugaredLogger.Infof("diemeng intraday warmup date=%s codes=%d %s~%s", dayKey, len(codes), start.Format("15:04"), end.Format("15:04"))

	page := 0
	pageSize := 10000
	all := make([]diemengHistoryItem, 0, 4096)

	startAt := normalizeMinuteTime(start).Format("2006-01-02 15:04:05")
	endAt := normalizeMinuteTime(end).Format("2006-01-02 15:04:05")

	for page < diemengMaxPages {
		waitForDiemengFetchWindow()

		reqBody := diemengHistoryReq{
			StockCode: codes, // batch list
			Level:     level,
			StartTime: startAt,
			EndTime:   endAt,
			Page:      page,
			PageSize:  pageSize,
		}

		var resp diemengResponse[diemengHistoryData]
		httpResp, err := client.R().
			SetHeader("apiKey", apiKey).
			SetBody(reqBody).
			SetResult(&resp).
			Post("/stock/history")
		if err != nil {
			diemengCircuitRecordFailure(err)
			return 0, fmt.Errorf("diemeng intraday history request failed: %w", err)
		}
		if httpResp == nil {
			err = fmt.Errorf("diemeng intraday history empty http response")
			diemengCircuitRecordFailure(err)
			return 0, err
		}
		if resp.Code != 200 {
			err = fmt.Errorf("diemeng intraday history api error (code=%d): %s", resp.Code, strings.TrimSpace(resp.Msg))
			diemengCircuitRecordFailure(err)
			return 0, err
		}

		items := resp.Data.Items
		if len(items) == 0 {
			items = resp.Data.List
		}
		if len(items) > 0 {
			all = append(all, items...)
		}
		if len(items) < pageSize {
			break
		}
		page++
	}
	diemengCircuitRecordSuccess()

	if len(all) == 0 {
		// Not an error by itself (some providers may not serve intraday through history).
		return 0, nil
	}

	group := map[string][]minuteBar{}
	for _, it := range all {
		code := strings.ToUpper(strings.TrimSpace(it.StockCode))
		if code == "" {
			continue
		}
		t, err := parseMinuteTime(it.TradeTime)
		if err != nil {
			continue
		}
		t = normalizeMinuteTime(t)
		if t.Before(start) || t.After(end) {
			continue
		}
		group[code] = append(group[code], minuteBar{
			TradeTime: t,
			Open:      it.Open,
			High:      it.High,
			Low:       it.Low,
			Close:     it.Close,
			Volume:    it.Vol,
			Amount:    it.Amount,
		})
	}

	totalInserted := 0
	for code, bars := range group {
		if len(bars) == 0 {
			continue
		}
		inserted, err := upsertMinuteBarsToCache(code, dedupeMinuteBars(bars), "diemeng")
		if err != nil {
			return totalInserted, fmt.Errorf("upsert minute bars failed (code=%s): %w", code, err)
		}
		totalInserted += inserted
	}
	return totalInserted, nil
}
