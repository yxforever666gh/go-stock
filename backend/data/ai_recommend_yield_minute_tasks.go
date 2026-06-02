package data

import (
	"go-stock/backend/db"
	"go-stock/backend/logger"
	"sort"
	"strings"
	"sync"
	"time"
)

type aiRecommendMinuteCoverageTask struct {
	StockCode        string
	Start            time.Time
	End              time.Time
	Forced           bool
	PreferHistorical bool
}

func runAiRecommendMinuteCoverageTasks(runtime *aiRecommendYieldRecalcRuntime, tasks []aiRecommendMinuteCoverageTask) {
	total := len(tasks)
	_ = updateYieldDownloadProgress(runtime.meta.ID, 0, total)
	if total == 0 {
		return
	}

	workerCount := yieldDownloadWorkerCount()
	if workerCount > total {
		workerCount = total
	}
	if workerCount <= 0 {
		workerCount = 1
	}

	taskCh := make(chan aiRecommendMinuteCoverageTask)
	progressCh := make(chan struct{}, total)
	var wg sync.WaitGroup

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range taskCh {
				var info minuteSyncInfo
				if task.Forced {
					if task.PreferHistorical {
						_, info = syncMinuteBarsForcedHistoricalWindow(task.StockCode, task.Start, task.End, runtime.ctx.CrawlTimeout)
					} else {
						_, info = syncMinuteBarsForcedWindow(task.StockCode, task.Start, task.End, runtime.ctx.CrawlTimeout)
					}
				} else if runtime.ctx.Reason == "manual_minute_download" {
					_, info = syncMinuteBarsStrict(task.StockCode, task.Start, task.End, runtime.ctx.CrawlTimeout, true)
				} else {
					_, info = syncMinuteBars(task.StockCode, task.Start, task.End, runtime.ctx.CrawlTimeout, false)
				}
				if info.SyncErr != nil {
					logger.SugaredLogger.Warnf("prefetch minute coverage failed: code=%s start=%s end=%s err=%v", task.StockCode, task.Start.In(cnLocation()).Format("2006-01-02 15:04:05"), task.End.In(cnLocation()).Format("2006-01-02 15:04:05"), info.SyncErr)
				}
				progressCh <- struct{}{}
			}
		}()
	}

	go func() {
		for _, task := range tasks {
			taskCh <- task
		}
		close(taskCh)
		wg.Wait()
		close(progressCh)
	}()

	done := 0
	lastFlush := time.Now()
	for range progressCh {
		done++
		if done == total || done%10 == 0 || time.Since(lastFlush) >= time.Second {
			_ = updateYieldDownloadProgress(runtime.meta.ID, done, total)
			lastFlush = time.Now()
		}
	}
}

func buildAiRecommendMinuteCoverageTasks(runtime *aiRecommendYieldRecalcRuntime, targets *aiRecommendYieldTargets) []aiRecommendMinuteCoverageTask {
	if runtime == nil || targets == nil {
		return nil
	}
	recordsByCode := groupRecommendRecordsByCode(targets.targetRecords)
	codeSet := buildMinuteCoverageCodeSet(targets)
	if len(codeSet) == 0 {
		return nil
	}

	end := normalizeMinuteCoverageEnd(resolveMinuteEvalEnd(runtime.now, runtime.inTrading, runtime.latestDate))
	if runtime.ctx.Reason == "manual_minute_download" {
		if runtime.inTrading {
			end = normalizeMinuteCoverageEnd(runtime.now)
		} else {
			end = resolveLatestCloseEvalEnd(runtime.now, runtime.latestDate)
		}
	}

	codes := make([]string, 0, len(codeSet))
	for code := range codeSet {
		codes = append(codes, code)
	}
	sort.Strings(codes)

	tasks := make([]aiRecommendMinuteCoverageTask, 0, len(codes))
	if runtime.ctx.Reason == "manual_minute_download" {
		tasks = append(tasks, buildManualMinuteGapCoverageTasks(codeSet)...)
	}
	for _, code := range codes {
		if !isAShareTsCode(code) {
			continue
		}
		start := time.Time{}
		requirePrevDayActivityPrefetch := false
		if aggr := targets.aggrMap[code]; aggr != nil && !aggr.SignalTime.IsZero() {
			start = resolveRecommendBuyTime(aggr.SignalTime)
			requirePrevDayActivityPrefetch = aggr.RequirePrevDayActivityFilter
		}
		for _, rec := range recordsByCode[code] {
			recordTime := recommendRecordTime(rec)
			if recordTime.IsZero() {
				continue
			}
			candidate := resolveRecommendBuyTime(recordTime)
			if candidate.IsZero() {
				continue
			}
			if start.IsZero() || candidate.Before(start) {
				start = candidate
			}
			if recommendRequiresPrevDayActivityFilter(rec) {
				requirePrevDayActivityPrefetch = true
			}
		}
		if start.IsZero() || !start.Before(end) {
			continue
		}
		if runtime.ctx.Reason == "manual_minute_download" {
			if replayStart := resolveYieldReplayExpandedRangeStart(start); !replayStart.IsZero() && replayStart.Before(start) {
				start = replayStart
			}
		} else if requirePrevDayActivityPrefetch {
			if prevStart := resolveActivitySessionStart(previousTradingMoment(start)); !prevStart.IsZero() && prevStart.Before(start) {
				start = prevStart
			}
		}
		tasks = append(tasks, aiRecommendMinuteCoverageTask{
			StockCode: code,
			Start:     start,
			End:       end,
		})
	}
	return tasks
}

func buildMinuteCoverageCodeSet(targets *aiRecommendYieldTargets) map[string]struct{} {
	if targets == nil {
		return nil
	}
	recordsByCode := groupRecommendRecordsByCode(targets.targetRecords)
	codeSet := make(map[string]struct{}, len(targets.targetCodes)+len(recordsByCode))
	for _, code := range targets.targetCodes {
		code = normalizeRecommendStockCode(code)
		if code == "" {
			continue
		}
		codeSet[code] = struct{}{}
	}
	for code := range recordsByCode {
		codeSet[code] = struct{}{}
	}
	return codeSet
}

func buildManualMinuteGapCoverageTasks(codeSet map[string]struct{}) []aiRecommendMinuteCoverageTask {
	if len(codeSet) == 0 {
		return nil
	}
	if db.Dao == nil {
		return nil
	}
	meta, err := getOrCreateYieldMeta()
	if err != nil {
		logger.SugaredLogger.Warnf("load yield meta for manual minute gap tasks failed: %v", err)
		return nil
	}
	_, issues := computeMinuteDownloadCoverageStatsWithSuspensionFetch(meta, -1)
	if len(issues) == 0 {
		return nil
	}

	type taskKey struct {
		code  string
		start string
		end   string
	}
	seen := make(map[taskKey]struct{}, len(issues))
	tasks := make([]aiRecommendMinuteCoverageTask, 0, len(issues))
	for _, issue := range issues {
		if strings.TrimSpace(issue.Status) != "待覆盖" {
			continue
		}
		code := normalizeRecommendStockCode(issue.StockCode)
		if code == "" {
			continue
		}
		if _, ok := codeSet[code]; !ok {
			continue
		}
		start := normalizeMinuteTime(issue.MissingStart)
		end := normalizeMinuteTime(issue.MissingEnd)
		if start.IsZero() || end.IsZero() || start.After(end) {
			continue
		}
		for _, session := range buildMinuteCoverageSessions(start, end) {
			sessionStart := normalizeMinuteTime(session.Start)
			sessionEnd := normalizeMinuteTime(session.End)
			if sessionStart.IsZero() || sessionEnd.IsZero() || sessionStart.After(sessionEnd) {
				continue
			}
			key := taskKey{
				code:  code,
				start: sessionStart.Format(time.RFC3339Nano),
				end:   sessionEnd.Format(time.RFC3339Nano),
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			tasks = append(tasks, aiRecommendMinuteCoverageTask{
				StockCode:        code,
				Start:            sessionStart,
				End:              sessionEnd,
				Forced:           true,
				PreferHistorical: minuteCoverageGapNeedsHistoricalSource(sessionStart, sessionEnd),
			})
		}
	}
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].StockCode != tasks[j].StockCode {
			return tasks[i].StockCode < tasks[j].StockCode
		}
		if !tasks[i].Start.Equal(tasks[j].Start) {
			return tasks[i].Start.Before(tasks[j].Start)
		}
		return tasks[i].End.Before(tasks[j].End)
	})
	return mergeMinuteCoverageTasks(tasks)
}

func mergeMinuteCoverageTasks(tasks []aiRecommendMinuteCoverageTask) []aiRecommendMinuteCoverageTask {
	if len(tasks) <= 1 {
		return tasks
	}
	merged := make([]aiRecommendMinuteCoverageTask, 0, len(tasks))
	for _, task := range tasks {
		if task.StockCode == "" || task.Start.IsZero() || task.End.IsZero() || task.Start.After(task.End) {
			continue
		}
		n := len(merged)
		if n == 0 {
			merged = append(merged, task)
			continue
		}
		last := &merged[n-1]
		if last.StockCode == task.StockCode && last.Forced == task.Forced && last.PreferHistorical == task.PreferHistorical && !task.Start.After(last.End.Add(time.Minute)) {
			if task.End.After(last.End) {
				last.End = task.End
			}
			continue
		}
		merged = append(merged, task)
	}
	return merged
}
