package data

import (
	"fmt"
	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	"sort"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
)

func (m *aiRecommendYieldRecalcManager) Request(force bool, reason string, scope map[string]struct{}) {
	m.mu.Lock()
	if m.running {
		m.pending = true
		m.pendingReason = reason
		if force {
			m.pendingForce = true
			if len(scope) == 0 {
				m.pendingScope = nil
			} else {
				m.pendingScope = mergeScopeMap(m.pendingScope, scope)
			}
		} else if !m.pendingForce {
			m.pendingScope = mergeScopeMap(m.pendingScope, scope)
		}
		m.mu.Unlock()
		return
	}
	m.running = true
	m.mu.Unlock()

	go m.run(force, reason, copyScopeMap(scope))
}

func (m *aiRecommendYieldRecalcManager) run(force bool, reason string, scope map[string]struct{}) {
	nextForce := force
	nextReason := reason
	nextScope := copyScopeMap(scope)
	for {
		err := rebuildAiRecommendYieldSnapshot(nextForce, nextReason, nextScope)
		if err != nil {
			logger.SugaredLogger.Errorf("rebuildAiRecommendYieldSnapshot error: %v", err)
		}

		m.mu.Lock()
		if m.pending {
			nextForce = m.pendingForce
			nextReason = strings.TrimSpace(m.pendingReason)
			if nextReason == "" {
				nextReason = "pending"
			}
			nextScope = copyScopeMap(m.pendingScope)
			m.pending = false
			m.pendingForce = false
			m.pendingReason = ""
			m.pendingScope = nil
			m.mu.Unlock()
			continue
		}
		m.running = false
		m.mu.Unlock()
		return
	}
}

type aiRecommendYieldRecalcRuntime struct {
	meta                  *models.AiRecommendYieldMeta
	now                   time.Time
	inTrading             bool
	latestDate            time.Time
	ctx                   yieldBuildContext
	manualAudit           *aiRecommendYieldManualAudit
	manualDownloadWarning string
}

type aiRecommendYieldTargets struct {
	aggrMap        map[string]*aiRecommendYieldAggregate
	records        []models.AiRecommendStocks
	existingMap    map[string]*models.AiRecommendYieldState
	existingRecord map[uint]*models.AiRecommendYieldRecordState
	allCodes       []string
	allRecordIDs   []uint
	targetCodes    []string
	targetRecords  []models.AiRecommendStocks
}

type aiRecommendYieldSnapshotWriter struct {
	metaID            uint
	recalcTotal       int
	recalcDone        int
	lastProgressFlush time.Time
	lastSnapshotFlush time.Time
	states            []models.AiRecommendYieldState
	recordStates      []models.AiRecommendYieldRecordState
}

type aiRecommendMinuteCoverageTask struct {
	StockCode string
	Start     time.Time
	End       time.Time
	Forced    bool
}

type aiRecommendYieldCalcTask struct {
	StockCode      string
	Aggregate      *aiRecommendYieldAggregate
	Records        []models.AiRecommendStocks
	ExistingState  *models.AiRecommendYieldState
	ExistingRecord map[uint]*models.AiRecommendYieldRecordState
}

type aiRecommendYieldCalcResult struct {
	State        *models.AiRecommendYieldState
	RecordStates []models.AiRecommendYieldRecordState
}

const manualYieldCalcTaskTimeout = 12 * time.Second

var (
	manualMinuteCoverageRetryBudget   = 15 * time.Minute
	manualMinuteCoverageRetryBackoffs = []time.Duration{
		10 * time.Second,
		20 * time.Second,
		40 * time.Second,
		80 * time.Second,
		120 * time.Second,
	}
	manualMinuteCoverageNow   = time.Now
	manualMinuteCoverageSleep = time.Sleep
)

type aiRecommendYieldManualAudit struct {
	StartedAt          time.Time
	ScopeCount         int
	mu                 sync.Mutex
	PrefetchStartedAt  time.Time
	PrefetchFinishedAt time.Time
	RecalcStartedAt    time.Time
	RecalcFinishedAt   time.Time
	FinishedAt         time.Time
	SQLiteBusyCount    int
	ProviderCounts     map[string]int
}

type aiRecommendYieldManualAuditSnapshot struct {
	StartedAt       time.Time
	FinishedAt      time.Time
	ScopeCount      int
	PrefetchMs      int64
	RecalcMs        int64
	TotalMs         int64
	SQLiteBusyCount int
	ProviderSummary string
}

func newAiRecommendYieldManualAudit(startedAt time.Time, scopeCount int) *aiRecommendYieldManualAudit {
	return &aiRecommendYieldManualAudit{
		StartedAt:      startedAt,
		ScopeCount:     scopeCount,
		ProviderCounts: make(map[string]int, 4),
	}
}

func (a *aiRecommendYieldManualAudit) markPrefetchStart(now time.Time) {
	if a == nil || now.IsZero() {
		return
	}
	a.mu.Lock()
	if a.PrefetchStartedAt.IsZero() {
		a.PrefetchStartedAt = now
	}
	a.mu.Unlock()
}

func (a *aiRecommendYieldManualAudit) markPrefetchDone(now time.Time) {
	if a == nil || now.IsZero() {
		return
	}
	a.mu.Lock()
	if a.PrefetchStartedAt.IsZero() {
		a.PrefetchStartedAt = now
	}
	a.PrefetchFinishedAt = now
	a.mu.Unlock()
}

func (a *aiRecommendYieldManualAudit) markRecalcStart(now time.Time) {
	if a == nil || now.IsZero() {
		return
	}
	a.mu.Lock()
	if a.RecalcStartedAt.IsZero() {
		a.RecalcStartedAt = now
	}
	a.mu.Unlock()
}

func (a *aiRecommendYieldManualAudit) markRecalcDone(now time.Time) {
	if a == nil || now.IsZero() {
		return
	}
	a.mu.Lock()
	if a.RecalcStartedAt.IsZero() {
		a.RecalcStartedAt = now
	}
	a.RecalcFinishedAt = now
	a.mu.Unlock()
}

func (a *aiRecommendYieldManualAudit) markFinished(now time.Time) {
	if a == nil || now.IsZero() {
		return
	}
	a.mu.Lock()
	a.FinishedAt = now
	a.mu.Unlock()
}

func (a *aiRecommendYieldManualAudit) incrementSQLiteBusy() {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.SQLiteBusyCount++
	a.mu.Unlock()
}

func (a *aiRecommendYieldManualAudit) recordProvider(source string) {
	if a == nil {
		return
	}
	source = strings.ToLower(strings.TrimSpace(source))
	if source == "" {
		return
	}
	a.mu.Lock()
	if a.ProviderCounts == nil {
		a.ProviderCounts = make(map[string]int, 4)
	}
	a.ProviderCounts[source]++
	a.mu.Unlock()
}

func (a *aiRecommendYieldManualAudit) snapshot() aiRecommendYieldManualAuditSnapshot {
	if a == nil {
		return aiRecommendYieldManualAuditSnapshot{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	finishedAt := a.FinishedAt
	if finishedAt.IsZero() {
		finishedAt = time.Now()
	}
	return aiRecommendYieldManualAuditSnapshot{
		StartedAt:       a.StartedAt,
		FinishedAt:      finishedAt,
		ScopeCount:      a.ScopeCount,
		PrefetchMs:      durationMillisBetween(a.PrefetchStartedAt, a.PrefetchFinishedAt),
		RecalcMs:        durationMillisBetween(a.RecalcStartedAt, a.RecalcFinishedAt),
		TotalMs:         durationMillisBetween(a.StartedAt, finishedAt),
		SQLiteBusyCount: a.SQLiteBusyCount,
		ProviderSummary: formatManualProviderSummary(a.ProviderCounts),
	}
}

func durationMillisBetween(start, end time.Time) int64 {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return 0
	}
	return end.Sub(start).Milliseconds()
}

func formatManualProviderSummary(counts map[string]int) string {
	if len(counts) == 0 {
		return ""
	}
	keys := make([]string, 0, len(counts))
	for key, count := range counts {
		if count <= 0 {
			continue
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s:%d", key, counts[key]))
	}
	return strings.Join(parts, ", ")
}

func setActiveManualYieldAudit(audit *aiRecommendYieldManualAudit) {
	activeManualYieldAuditState.mu.Lock()
	activeManualYieldAuditState.audit = audit
	activeManualYieldAuditState.mu.Unlock()
}

func clearActiveManualYieldAudit(audit *aiRecommendYieldManualAudit) {
	activeManualYieldAuditState.mu.Lock()
	if activeManualYieldAuditState.audit == audit {
		activeManualYieldAuditState.audit = nil
	}
	activeManualYieldAuditState.mu.Unlock()
}

func currentActiveManualYieldAudit() *aiRecommendYieldManualAudit {
	activeManualYieldAuditState.mu.RLock()
	defer activeManualYieldAuditState.mu.RUnlock()
	return activeManualYieldAuditState.audit
}

func isSQLiteBusyError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "database is locked") ||
		strings.Contains(lower, "database table is locked") ||
		strings.Contains(lower, "sqlite_busy")
}

func runWithSQLiteBusyRetry(fn func() error) error {
	if fn == nil {
		return nil
	}
	backoffs := []time.Duration{
		100 * time.Millisecond,
		200 * time.Millisecond,
		400 * time.Millisecond,
		800 * time.Millisecond,
		1600 * time.Millisecond,
	}
	var lastErr error
	for attempt := 0; attempt <= len(backoffs); attempt++ {
		lastErr = fn()
		if !isSQLiteBusyError(lastErr) {
			return lastErr
		}
		if audit := currentActiveManualYieldAudit(); audit != nil {
			audit.incrementSQLiteBusy()
		}
		if attempt >= len(backoffs) {
			break
		}
		time.Sleep(backoffs[attempt])
	}
	return lastErr
}

func rebuildAiRecommendYieldSnapshot(force bool, reason string, scope map[string]struct{}) error {
	if schemaErr := ensureYieldMetaSchema(); schemaErr != nil {
		return schemaErr
	}

	runtime, finalize, err := beginAiRecommendYieldRecalc(force, reason)
	if err != nil {
		return err
	}
	defer finalize(&err)
	if reason == "manual_minute_download" && runtime.manualAudit != nil {
		runtime.manualAudit.ScopeCount = len(scope)
	}

	targets, err := loadAiRecommendYieldTargets(runtime, scope, force)
	if err != nil {
		markAiRecommendYieldRecalcError(runtime.meta.ID, err)
		return err
	}
	if len(targets.aggrMap) == 0 && len(targets.records) == 0 {
		err = clearAiRecommendYieldSnapshots(runtime.meta.ID)
		if err != nil {
			markAiRecommendYieldRecalcError(runtime.meta.ID, err)
		}
		return err
	}

	writer := newAiRecommendYieldSnapshotWriter(runtime.meta.ID, len(targets.targetCodes)+len(targets.targetRecords)+1)
	if writer.recalcTotal > 0 {
		_ = updateYieldRecalcProgress(runtime.meta.ID, writer.recalcDone, writer.recalcTotal)
	}
	if len(targets.targetCodes) == 0 && len(targets.targetRecords) == 0 {
		return nil
	}

	warmManualAiRecommendMinuteData(reason, runtime, targets.targetCodes)
	if runtime.manualAudit != nil {
		runtime.manualAudit.markPrefetchStart(time.Now())
	}
	prefetchErr := prefetchAiRecommendMinuteCoverage(runtime, targets)
	if runtime.manualAudit != nil {
		runtime.manualAudit.markPrefetchDone(time.Now())
	}
	if prefetchErr != nil {
		err = prefetchErr
		markAiRecommendYieldRecalcError(runtime.meta.ID, err)
		return err
	}
	if runtime.manualAudit != nil {
		runtime.manualAudit.markRecalcStart(time.Now())
	}
	if err = processAiRecommendYieldTargets(runtime, targets, writer); err != nil {
		markAiRecommendYieldRecalcError(runtime.meta.ID, err)
		return err
	}
	if runtime.manualAudit != nil {
		runtime.manualAudit.markRecalcDone(time.Now())
	}
	if err = writer.Flush(); err != nil {
		markAiRecommendYieldRecalcError(runtime.meta.ID, err)
		return err
	}
	if err = clearAiRecommendYieldDirtyCodes(targets.targetCodes); err != nil {
		markAiRecommendYieldRecalcError(runtime.meta.ID, err)
		return err
	}

	fullRecalc := isFullAiRecommendYieldRecalc(force, scope, targets)
	if fullRecalc {
		if err = cleanupAiRecommendYieldSnapshots(targets.allCodes, targets.allRecordIDs); err != nil {
			markAiRecommendYieldRecalcError(runtime.meta.ID, err)
			return err
		}
	}
	if reason == "manual_minute_download" {
		runtime.manualDownloadWarning = buildManualDownloadCoverageWarning(runtime.meta, 5)
	}

	go sendYieldXLSXEmailIfEnabled(reason, fullRecalc)
	return nil
}

func isFullAiRecommendYieldRecalc(force bool, scope map[string]struct{}, targets *aiRecommendYieldTargets) bool {
	if len(scope) > 0 {
		return false
	}
	if force {
		return true
	}
	if targets == nil {
		return false
	}
	return len(targets.targetCodes) == len(targets.allCodes) && len(targets.targetRecords) == len(targets.records)
}

func buildManualDownloadCoverageWarning(meta *models.AiRecommendYieldMeta, limit int) string {
	if limit <= 0 {
		limit = 5
	}
	stats, issues := computeMinuteDownloadCoverageStatsWithIssues(meta, limit)
	if stats.Pending <= 0 {
		return ""
	}
	return formatManualDownloadCoverageStatus("仍有待覆盖", stats.Pending, manualCoverageIssueParts(issues, "待覆盖", limit))
}

func buildManualDownloadCoverageFailure(meta *models.AiRecommendYieldMeta, limit int) string {
	if limit <= 0 {
		limit = 5
	}
	stats, issues := computeMinuteDownloadCoverageStatsWithIssues(meta, limit)
	parts := make([]string, 0, 2)
	if stats.Pending > 0 {
		parts = append(parts, formatManualDownloadCoverageStatus("仍有待覆盖", stats.Pending, manualCoverageIssueParts(issues, "待覆盖", limit)))
	}
	if stats.Uncoverable > 0 {
		parts = append(parts, formatManualDownloadCoverageStatus("不可覆盖", stats.Uncoverable, manualCoverageIssueParts(issues, "不可覆盖", limit)))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "；")
}

func manualCoverageIssueParts(issues []minuteCoverageIssue, status string, limit int) []string {
	parts := make([]string, 0, len(issues))
	for _, issue := range issues {
		if strings.TrimSpace(issue.Status) != status {
			continue
		}
		code := normalizeRecommendStockCode(issue.StockCode)
		if code == "" {
			code = strings.TrimSpace(issue.StockCode)
		}
		reason := strings.TrimSpace(issue.RawReason)
		if reason == "" {
			reason = "分钟线缺口未补齐"
		}
		parts = append(parts, fmt.Sprintf("%s %s", code, reason))
		if len(parts) >= limit {
			break
		}
	}
	return parts
}

func formatManualDownloadCoverageStatus(label string, count int, parts []string) string {
	if len(parts) == 0 {
		return fmt.Sprintf("%s %d 条", label, count)
	}
	if count > len(parts) {
		return fmt.Sprintf("%s %d 条：%s 等", label, count, strings.Join(parts, "；"))
	}
	return fmt.Sprintf("%s %d 条：%s", label, count, strings.Join(parts, "；"))
}

func beginAiRecommendYieldRecalc(force bool, reason string) (*aiRecommendYieldRecalcRuntime, func(*error), error) {
	meta, err := getOrCreateYieldMeta()
	if err != nil {
		return nil, nil, err
	}
	if err = markAiRecommendYieldRecalcStarted(meta.ID); err != nil {
		return nil, nil, err
	}

	heartbeatStop := startAiRecommendYieldHeartbeat(meta.ID)
	now := time.Now()
	runtime, err := buildAiRecommendYieldRecalcRuntime(meta, now, force, reason)
	if err != nil {
		close(heartbeatStop)
		return nil, nil, err
	}
	if reason == "manual_minute_download" {
		runtime.manualAudit = newAiRecommendYieldManualAudit(now, 0)
		setActiveManualYieldAudit(runtime.manualAudit)
	}

	finalize := func(runErr *error) {
		close(heartbeatStop)
		if runtime.manualAudit != nil {
			runtime.manualAudit.markFinished(time.Now())
		}
		finishAiRecommendYieldRecalc(meta.ID, runtime.now, *runErr, runtime.manualDownloadWarning)
		if runtime.manualAudit != nil {
			_ = persistManualYieldAudit(meta.ID, runtime.manualAudit)
			clearActiveManualYieldAudit(runtime.manualAudit)
		}
	}
	return runtime, finalize, nil
}

func markAiRecommendYieldRecalcStarted(metaID uint) error {
	return runWithSQLiteBusyRetry(func() error {
		return db.Dao.Model(&models.AiRecommendYieldMeta{}).Where("id = ?", metaID).Updates(map[string]any{
			"recalc_in_progress": true,
			"last_error":         "",
			"recalc_total":       0,
			"recalc_done":        0,
			"recalc_progress":    1,
		}).Error
	})
}

func startAiRecommendYieldHeartbeat(metaID uint) chan struct{} {
	heartbeatStop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_ = runWithSQLiteBusyRetry(func() error {
					return db.Dao.Model(&models.AiRecommendYieldMeta{}).
						Where("id = ? AND recalc_in_progress = ?", metaID, true).
						Updates(map[string]any{"updated_at": time.Now()}).Error
				})
			case <-heartbeatStop:
				return
			}
		}
	}()
	return heartbeatStop
}

func finishAiRecommendYieldRecalc(metaID uint, startedAt time.Time, runErr error, downloadWarning string) {
	updateMap := map[string]any{
		"recalc_in_progress": false,
		"updated_at":         time.Now(),
	}
	if runErr == nil {
		updateMap["last_full_recalc_at"] = startedAt
		updateMap["recalc_done"] = gorm.Expr("CASE WHEN recalc_total > 0 THEN recalc_total ELSE recalc_done END")
		updateMap["recalc_progress"] = 100
		updateMap["download_in_progress"] = false
		updateMap["download_done"] = gorm.Expr("CASE WHEN download_total > 0 THEN download_total ELSE download_done END")
		updateMap["download_progress"] = 100
		updateMap["last_download_error"] = strings.TrimSpace(downloadWarning)
	} else {
		updateMap["download_in_progress"] = false
		updateMap["last_download_error"] = runErr.Error()
	}
	if e := runWithSQLiteBusyRetry(func() error {
		return db.Dao.Model(&models.AiRecommendYieldMeta{}).Where("id = ?", metaID).Updates(updateMap).Error
	}); e != nil {
		logger.SugaredLogger.Errorf("update ai_recommend_yield_meta failed: %v", e)
	}
}

func persistManualYieldAudit(metaID uint, audit *aiRecommendYieldManualAudit) error {
	if audit == nil || metaID == 0 {
		return nil
	}
	snapshot := audit.snapshot()
	return runWithSQLiteBusyRetry(func() error {
		return db.Dao.Model(&models.AiRecommendYieldMeta{}).Where("id = ?", metaID).Updates(map[string]any{
			"last_manual_finished_at":       nullableTime(snapshot.FinishedAt),
			"last_manual_scope_count":       snapshot.ScopeCount,
			"last_manual_prefetch_ms":       snapshot.PrefetchMs,
			"last_manual_recalc_ms":         snapshot.RecalcMs,
			"last_manual_total_ms":          snapshot.TotalMs,
			"last_manual_sqlite_busy_count": snapshot.SQLiteBusyCount,
			"last_manual_provider_summary":  snapshot.ProviderSummary,
			"updated_at":                    time.Now(),
		}).Error
	})
}

func nullableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	value := t
	return &value
}

func buildAiRecommendYieldRecalcRuntime(meta *models.AiRecommendYieldMeta, now time.Time, force bool, reason string) (*aiRecommendYieldRecalcRuntime, error) {
	_ = db.Dao.Model(&models.AiRecommendYieldMeta{}).Where("id = ?", meta.ID).Updates(map[string]any{
		"akshare_ready":         false,
		"akshare_checked_at":    time.Now(),
		"akshare_install_error": "",
	}).Error

	setting := GetSettingConfig()
	crawlTimeout := int64(60)
	if setting != nil && setting.CrawlTimeOut > 0 {
		crawlTimeout = setting.CrawlTimeOut
	}
	tushare := NewTushareApi(setting)

	loc, _ := time.LoadLocation("Asia/Shanghai")
	now = now.In(loc)
	inTrading := isCNTradingSession(now)
	latestTradeDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	if !inTrading {
		latest, latestErr := tushare.GetLatestTradeDate(crawlTimeout)
		if latestErr != nil {
			logger.SugaredLogger.Warnf("GetLatestTradeDate failed: %v", latestErr)
		} else {
			latestTradeDate = time.Date(latest.Year(), latest.Month(), latest.Day(), 0, 0, 0, 0, loc)
		}
	}

	_ = db.Dao.Model(&models.AiRecommendYieldMeta{}).Where("id = ?", meta.ID).Updates(map[string]any{
		"current_trade_date": latestTradeDate.Format("2006-01-02"),
	}).Error

	priceMap := map[string]float64{}
	priceTimeMap := map[string]string{}
	if reason != "manual_minute_download" {
		priceMap, priceTimeMap = fetchCurrentPriceMapFn(nil)
	}
	return &aiRecommendYieldRecalcRuntime{
		meta:       meta,
		now:        now,
		inTrading:  inTrading,
		latestDate: latestTradeDate,
		ctx: yieldBuildContext{
			Force:               force,
			Reason:              reason,
			Now:                 now,
			InTradingSession:    inTrading,
			LatestTradeDate:     latestTradeDate,
			CrawlTimeout:        crawlTimeout,
			DisableMinuteFetch:  reason == "manual_minute_download",
			Tushare:             tushare,
			CurrentPriceMap:     priceMap,
			CurrentPriceTimeMap: priceTimeMap,
		},
	}, nil
}

func loadAiRecommendYieldTargets(runtime *aiRecommendYieldRecalcRuntime, scope map[string]struct{}, force bool) (*aiRecommendYieldTargets, error) {
	coverableStart := minuteCoverableStartMinute(runtime.latestDate)

	aggrMap, err := loadAiRecommendYieldAggregatesAfter(coverableStart)
	if err != nil {
		return nil, err
	}
	records, err := loadAiRecommendYieldRecordsAfter(coverableStart)
	if err != nil {
		return nil, err
	}
	existingMap, err := loadExistingYieldStateMap()
	if err != nil {
		return nil, err
	}
	existingRecordMap, err := loadExistingYieldRecordStateMap()
	if err != nil {
		return nil, err
	}

	allCodes := make([]string, 0, len(aggrMap))
	for code := range aggrMap {
		allCodes = append(allCodes, code)
	}
	sort.Strings(allCodes)
	allRecordIDs := extractRecommendRecordIDs(records)
	targetCodes := buildRecalcTargetCodes(allCodes, scope, force)
	targetRecords := buildRecalcTargetRecords(records, scope, force)

	if runtime.ctx.Reason != "manual_minute_download" {
		priceMap, priceTimeMap := fetchCurrentPriceMapFn(aggrMap)
		runtime.ctx.CurrentPriceMap = priceMap
		runtime.ctx.CurrentPriceTimeMap = priceTimeMap
	}

	return &aiRecommendYieldTargets{
		aggrMap:        aggrMap,
		records:        records,
		existingMap:    existingMap,
		existingRecord: existingRecordMap,
		allCodes:       allCodes,
		allRecordIDs:   allRecordIDs,
		targetCodes:    targetCodes,
		targetRecords:  targetRecords,
	}, nil
}

func clearAiRecommendYieldSnapshots(metaID uint) error {
	if err := db.Dao.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&models.AiRecommendYieldState{}).Error; err != nil {
		return err
	}
	if err := db.Dao.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&models.AiRecommendYieldRecordState{}).Error; err != nil {
		return err
	}
	if err := db.Dao.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&models.AiRecommendMinuteBar{}).Error; err != nil {
		return err
	}
	return updateYieldRecalcProgress(metaID, 0, 0)
}

func warmManualAiRecommendMinuteData(reason string, runtime *aiRecommendYieldRecalcRuntime, targetCodes []string) {
	if reason != "manual_minute_download" {
		return
	}
	warmDate := runtime.latestDate
	warmEnd := resolveLatestCloseEvalEnd(runtime.now, runtime.latestDate)
	if !warmEnd.IsZero() {
		loc := cnLocation()
		warmDate = time.Date(warmEnd.In(loc).Year(), warmEnd.In(loc).Month(), warmEnd.In(loc).Day(), 0, 0, 0, 0, loc)
	}
	if warmErr := warmupDiemengMinuteBarsByDailyDump(warmDate, targetCodes); warmErr != nil {
		logger.SugaredLogger.Warnf("warmup diemeng daily_dump failed: %v", warmErr)
	}
	for dayKey, codes := range manualMinuteGapWarmupPlan(targetCodes) {
		day, ok := parseYieldTradeDate(dayKey)
		if !ok {
			continue
		}
		if day.Equal(warmDate) {
			continue
		}
		if warmErr := warmupDiemengMinuteBarsByDailyDump(day, codes); warmErr != nil {
			logger.SugaredLogger.Warnf("warmup diemeng daily_dump for gap failed: date=%s err=%v", dayKey, warmErr)
		}
	}
	if !runtime.inTrading {
		return
	}
	inserted, warmErr := warmupDiemengMinuteBarsIntradayByHistory(runtime.now, targetCodes)
	if warmErr != nil {
		logger.SugaredLogger.Warnf("warmup diemeng intraday minute bars failed: %v", warmErr)
	}
	loc := cnLocation()
	cur := runtime.now.In(loc)
	day := time.Date(cur.Year(), cur.Month(), cur.Day(), 0, 0, 0, 0, loc)
	open931 := time.Date(day.Year(), day.Month(), day.Day(), 9, 31, 0, 0, loc)
	if warmErr != nil || (inserted == 0 && !cur.Before(open931)) {
		if warmErr2 := warmupSinaMinuteBarsIntraday(runtime.now, targetCodes); warmErr2 != nil {
			logger.SugaredLogger.Warnf("warmup sina intraday minute bars failed: %v", warmErr2)
		}
	}
}

func manualMinuteGapWarmupPlan(targetCodes []string) map[string][]string {
	if db.Dao == nil {
		return nil
	}
	codeSet := make(map[string]struct{}, len(targetCodes))
	for _, code := range targetCodes {
		code = normalizeRecommendStockCode(code)
		if code == "" {
			continue
		}
		codeSet[code] = struct{}{}
	}
	if len(codeSet) == 0 {
		return nil
	}
	meta, err := getOrCreateYieldMeta()
	if err != nil {
		logger.SugaredLogger.Warnf("load yield meta for manual gap warmup failed: %v", err)
		return nil
	}
	_, issues := computeMinuteDownloadCoverageStatsWithIssues(meta, -1)
	byDay := make(map[string]map[string]struct{})
	loc := cnLocation()
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
		if issue.MissingStart.IsZero() {
			continue
		}
		day := issue.MissingStart.In(loc).Format("2006-01-02")
		if byDay[day] == nil {
			byDay[day] = map[string]struct{}{}
		}
		byDay[day][code] = struct{}{}
	}
	out := make(map[string][]string, len(byDay))
	for day, codes := range byDay {
		list := make([]string, 0, len(codes))
		for code := range codes {
			list = append(list, code)
		}
		sort.Strings(list)
		out[day] = list
	}
	return out
}

func processAiRecommendYieldTargets(runtime *aiRecommendYieldRecalcRuntime, targets *aiRecommendYieldTargets, writer *aiRecommendYieldSnapshotWriter) error {
	tasks := buildAiRecommendYieldCalcTasks(targets)
	if len(tasks) == 0 {
		return nil
	}

	workerCount := yieldCalcWorkerCount()
	if workerCount > len(tasks) {
		workerCount = len(tasks)
	}
	if workerCount <= 0 {
		workerCount = 1
	}

	taskCh := make(chan aiRecommendYieldCalcTask)
	resultCh := make(chan aiRecommendYieldCalcResult, workerCount)
	writerDoneCh := make(chan error, 1)

	go func() {
		var writeErr error
		for result := range resultCh {
			if writeErr != nil {
				continue
			}
			if result.State != nil {
				if err := writer.AppendState(*result.State); err != nil {
					writeErr = err
					continue
				}
			}
			for _, recordState := range result.RecordStates {
				if err := writer.AppendRecordState(recordState); err != nil {
					writeErr = err
					break
				}
			}
		}
		writerDoneCh <- writeErr
	}()

	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range taskCh {
				resultCh <- executeAiRecommendYieldCalcTaskWithTimeout(task, runtime.ctx)
			}
		}()
	}

	for _, task := range tasks {
		taskCh <- task
	}
	close(taskCh)
	wg.Wait()
	close(resultCh)

	return <-writerDoneCh
}

func executeAiRecommendYieldCalcTaskWithTimeout(task aiRecommendYieldCalcTask, ctx yieldBuildContext) aiRecommendYieldCalcResult {
	if ctx.Reason != "manual_minute_download" {
		return executeAiRecommendYieldCalcTask(task, ctx)
	}
	resultCh := make(chan aiRecommendYieldCalcResult, 1)
	go func() {
		resultCh <- executeAiRecommendYieldCalcTask(task, ctx)
	}()
	select {
	case result := <-resultCh:
		return result
	case <-time.After(manualYieldCalcTaskTimeout):
		logger.SugaredLogger.Warnf("manual yield calc task timed out: code=%s records=%d", task.StockCode, len(task.Records))
		return buildTimedOutYieldCalcResult(task, ctx)
	}
}

func buildTimedOutYieldCalcResult(task aiRecommendYieldCalcTask, ctx yieldBuildContext) aiRecommendYieldCalcResult {
	result := aiRecommendYieldCalcResult{
		RecordStates: make([]models.AiRecommendYieldRecordState, 0, len(task.Records)),
	}
	recalcAt := ctx.Now
	for _, rec := range task.Records {
		if existing := task.ExistingRecord[rec.ID]; existing != nil {
			state := *existing
			state.LastRecalcAt = &recalcAt
			if strings.TrimSpace(state.DataStatus) == "" {
				state.DataStatus = "无法判定"
			}
			state.DataStatusReason = appendReasonText(state.DataStatusReason, "手动回算单票超时，已保留上次快照")
			result.RecordStates = append(result.RecordStates, state)
			continue
		}
		recordTime := recommendRecordTime(rec)
		state := models.AiRecommendYieldRecordState{
			RecommendID:       rec.ID,
			StockCode:         normalizeRecommendStockCode(rec.StockCode),
			StockName:         strings.TrimSpace(rec.StockName),
			ModelName:         strings.TrimSpace(rec.ModelName),
			BkName:            strings.TrimSpace(rec.BkName),
			RecommendCategory: strings.TrimSpace(rec.RecommendCategory),
			ActivationStatus:  "pending",
			PositionStatus:    "待激活",
			YieldRateText:     "--",
			DataStatus:        "无法判定",
			DataStatusReason:  "手动回算单票超时，等待下次重算",
			TotalScopeEnd:     ctx.Now.Format("2006-01-02"),
			LastRecalcAt:      &recalcAt,
		}
		if !recordTime.IsZero() {
			t := recordTime
			state.RecommendTime = &t
			state.SignalTime = &t
			state.TotalScopeStart = t.Format("2006-01-02")
		}
		fillYieldRecordMetrics(&state)
		result.RecordStates = append(result.RecordStates, state)
	}
	if task.Aggregate != nil {
		if task.ExistingState != nil {
			state := *task.ExistingState
			state.LastRecalcAt = &recalcAt
			if strings.TrimSpace(state.DataStatus) == "" {
				state.DataStatus = "无法判定"
			}
			state.DataStatusReason = appendReasonText(state.DataStatusReason, "手动回算单票超时，已保留上次快照")
			result.State = &state
		} else {
			state := models.AiRecommendYieldState{
				StockCode:        task.Aggregate.StockCode,
				StockName:        task.Aggregate.StockName,
				ModelNames:       strings.Join(task.Aggregate.ModelNames, "、"),
				BkName:           strings.Join(task.Aggregate.BkNames, "、"),
				RecommendCount:   task.Aggregate.RecommendCount,
				ActivationStatus: "pending",
				PositionStatus:   "待激活",
				YieldRateText:    "--",
				DataStatus:       "无法判定",
				DataStatusReason: "手动回算单票超时，等待下次重算",
				TotalScopeStart:  task.Aggregate.SignalTime.Format("2006-01-02"),
				TotalScopeEnd:    ctx.Now.Format("2006-01-02"),
				LastRecalcAt:     &recalcAt,
			}
			if !task.Aggregate.SignalTime.IsZero() {
				t := task.Aggregate.SignalTime
				state.RecommendTime = &t
				state.SignalTime = &t
			}
			fillYieldMetrics(&state)
			result.State = &state
		}
	}
	return result
}

func appendReasonText(existing, reason string) string {
	existing = strings.TrimSpace(existing)
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return existing
	}
	if existing == "" {
		return reason
	}
	if strings.Contains(existing, reason) {
		return existing
	}
	return existing + "；" + reason
}

func executeAiRecommendYieldCalcTask(task aiRecommendYieldCalcTask, ctx yieldBuildContext) aiRecommendYieldCalcResult {
	result := aiRecommendYieldCalcResult{
		RecordStates: make([]models.AiRecommendYieldRecordState, 0, len(task.Records)),
	}
	for _, rec := range task.Records {
		recordState := buildYieldRecordStateFromRecommend(rec, task.ExistingRecord[rec.ID], ctx)
		result.RecordStates = append(result.RecordStates, recordState)
	}
	if task.Aggregate != nil {
		state := buildYieldStateFromAggregate(task.Aggregate, task.ExistingState, ctx)
		mergeAggregateYieldStateWithRecordStates(&state, result.RecordStates)
		result.State = &state
	}
	return result
}

func mergeAggregateYieldStateWithRecordStates(state *models.AiRecommendYieldState, recordStates []models.AiRecommendYieldRecordState) {
	if state == nil || len(recordStates) == 0 {
		return
	}
	if activated := pickLatestActivatedRecordState(recordStates); activated != nil && strings.TrimSpace(state.ActivationStatus) != "activated" {
		applyRecordStateToAggregateYieldState(state, activated)
		return
	}
	if strings.TrimSpace(state.ActivationStatus) == "pending" && allRecordStatesSkipped(recordStates) {
		if skipped := pickLatestSkippedRecordState(recordStates); skipped != nil {
			applyRecordStateToAggregateYieldState(state, skipped)
		}
	}
}

func pickLatestActivatedRecordState(recordStates []models.AiRecommendYieldRecordState) *models.AiRecommendYieldRecordState {
	var chosen *models.AiRecommendYieldRecordState
	for i := range recordStates {
		current := &recordStates[i]
		if strings.TrimSpace(current.ActivationStatus) != "activated" {
			continue
		}
		if chosen == nil {
			chosen = current
			continue
		}
		if compareYieldRecordStatePriority(current, chosen) > 0 {
			chosen = current
		}
	}
	return chosen
}

func pickLatestSkippedRecordState(recordStates []models.AiRecommendYieldRecordState) *models.AiRecommendYieldRecordState {
	var chosen *models.AiRecommendYieldRecordState
	for i := range recordStates {
		current := &recordStates[i]
		if strings.TrimSpace(current.ActivationStatus) != "skipped" {
			continue
		}
		if chosen == nil {
			chosen = current
			continue
		}
		if compareYieldRecordStatePriority(current, chosen) > 0 {
			chosen = current
		}
	}
	return chosen
}

func allRecordStatesSkipped(recordStates []models.AiRecommendYieldRecordState) bool {
	if len(recordStates) == 0 {
		return false
	}
	for i := range recordStates {
		status := strings.TrimSpace(recordStates[i].ActivationStatus)
		if status != "skipped" {
			return false
		}
	}
	return true
}

func compareYieldRecordStatePriority(left, right *models.AiRecommendYieldRecordState) int {
	if left == nil || right == nil {
		return 0
	}
	leftTime := resolveYieldRecordStatePriorityTime(left)
	rightTime := resolveYieldRecordStatePriorityTime(right)
	switch {
	case leftTime.After(rightTime):
		return 1
	case leftTime.Before(rightTime):
		return -1
	case left.RecommendID > right.RecommendID:
		return 1
	case left.RecommendID < right.RecommendID:
		return -1
	default:
		return 0
	}
}

func resolveYieldRecordStatePriorityTime(state *models.AiRecommendYieldRecordState) time.Time {
	if state == nil {
		return time.Time{}
	}
	if state.ActivationTime != nil && !state.ActivationTime.IsZero() {
		return *state.ActivationTime
	}
	if state.RecommendTime != nil && !state.RecommendTime.IsZero() {
		return *state.RecommendTime
	}
	if state.SignalTime != nil && !state.SignalTime.IsZero() {
		return *state.SignalTime
	}
	return time.Time{}
}

func applyRecordStateToAggregateYieldState(target *models.AiRecommendYieldState, record *models.AiRecommendYieldRecordState) {
	if target == nil || record == nil {
		return
	}
	target.ActivationStatus = strings.TrimSpace(record.ActivationStatus)
	if record.ActivationTime != nil && !record.ActivationTime.IsZero() {
		t := *record.ActivationTime
		target.ActivationTime = &t
	} else {
		target.ActivationTime = nil
	}
	target.ActivationPrice = round2(record.ActivationPrice)
	if record.BuyTime != nil && !record.BuyTime.IsZero() {
		t := *record.BuyTime
		target.BuyTime = &t
		target.TotalScopeStart = t.Format("2006-01-02")
	} else {
		target.BuyTime = nil
	}
	target.BuyAmount = round2(record.BuyAmount)
	target.PositionStatus = strings.TrimSpace(record.PositionStatus)
	if record.SellTime != nil && !record.SellTime.IsZero() {
		t := *record.SellTime
		target.SellTime = &t
	} else {
		target.SellTime = nil
	}
	if record.RealizedSellAmount != nil {
		v := round2(*record.RealizedSellAmount)
		target.RealizedSellAmount = &v
	} else {
		target.RealizedSellAmount = nil
	}
	target.CurrentPrice = round2(record.CurrentPrice)
	target.CurrentPriceTime = record.CurrentPriceTime
	target.YieldRate = round2(record.YieldRate)
	target.YieldRateText = record.YieldRateText
	target.DataStatus = record.DataStatus
	target.DataStatusReason = record.DataStatusReason
	target.LastMinuteTs = record.LastMinuteTs
	target.LastRecalcAt = record.LastRecalcAt
	target.MinuteCacheStart = record.MinuteCacheStart
	target.MinuteCacheEnd = record.MinuteCacheEnd
	target.MinuteCacheSource = record.MinuteCacheSource
	target.MinuteCacheUpdated = record.MinuteCacheUpdated
	target.Frozen = record.Frozen
}

func buildAiRecommendYieldCalcTasks(targets *aiRecommendYieldTargets) []aiRecommendYieldCalcTask {
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
	if len(codeSet) == 0 {
		return nil
	}
	codes := make([]string, 0, len(codeSet))
	for code := range codeSet {
		codes = append(codes, code)
	}
	sort.Strings(codes)

	tasks := make([]aiRecommendYieldCalcTask, 0, len(codes))
	for _, code := range codes {
		tasks = append(tasks, aiRecommendYieldCalcTask{
			StockCode:      code,
			Aggregate:      targets.aggrMap[code],
			Records:        recordsByCode[code],
			ExistingState:  targets.existingMap[code],
			ExistingRecord: targets.existingRecord,
		})
	}
	return tasks
}

func groupRecommendRecordsByCode(records []models.AiRecommendStocks) map[string][]models.AiRecommendStocks {
	if len(records) == 0 {
		return map[string][]models.AiRecommendStocks{}
	}
	grouped := make(map[string][]models.AiRecommendStocks, len(records))
	for _, rec := range records {
		code := normalizeRecommendStockCode(rec.StockCode)
		if code == "" {
			continue
		}
		grouped[code] = append(grouped[code], rec)
	}
	return grouped
}

func prefetchAiRecommendMinuteCoverage(runtime *aiRecommendYieldRecalcRuntime, targets *aiRecommendYieldTargets) error {
	tasks := buildAiRecommendMinuteCoverageTasks(runtime, targets)
	if len(tasks) == 0 {
		_ = updateYieldDownloadProgress(runtime.meta.ID, 0, 0)
		return nil
	}

	runAiRecommendMinuteCoverageTasks(runtime, tasks)

	if runtime.ctx.Reason != "manual_minute_download" {
		return nil
	}
	return closeManualMinuteCoverageGaps(runtime, buildMinuteCoverageCodeSet(targets))
}

func closeManualMinuteCoverageGaps(runtime *aiRecommendYieldRecalcRuntime, codeSet map[string]struct{}) error {
	if runtime == nil || runtime.meta == nil || len(codeSet) == 0 {
		return nil
	}
	deadline := manualMinuteCoverageNow().Add(manualMinuteCoverageRetryBudget)
	round := 0
	for {
		stats, issues := computeMinuteDownloadCoverageStatsWithIssues(runtime.meta, -1)
		if stats.Pending == 0 && stats.Uncoverable == 0 {
			_ = runWithSQLiteBusyRetry(func() error {
				return db.Dao.Model(&models.AiRecommendYieldMeta{}).
					Where("id = ?", runtime.meta.ID).
					Update("last_download_error", "").Error
			})
			return nil
		}
		if !manualMinuteCoverageNow().Before(deadline) {
			failure := buildManualDownloadCoverageFailure(runtime.meta, 5)
			if failure == "" {
				failure = "分钟线缺口未补齐"
			}
			return fmt.Errorf("分钟线补齐失败：15分钟内仍未全部连续覆盖；%s", failure)
		}

		nextTasks := buildManualMinuteGapCoverageTasks(codeSet)
		if len(nextTasks) == 0 {
			failure := buildManualDownloadCoverageFailure(runtime.meta, 5)
			if failure == "" {
				failure = "存在覆盖问题，但没有可执行的缺口下载任务"
			}
			return fmt.Errorf("分钟线补齐失败：%s", failure)
		}

		round++
		_ = updateManualMinuteCoverageRetryStatus(runtime.meta.ID, round, stats, issues)
		runAiRecommendMinuteCoverageTasks(runtime, nextTasks)

		stats, _ = computeMinuteDownloadCoverageStatsWithIssues(runtime.meta, 0)
		if stats.Pending == 0 && stats.Uncoverable == 0 {
			continue
		}
		if wait := manualMinuteCoverageRetryBackoff(round - 1); wait > 0 {
			if remaining := deadline.Sub(manualMinuteCoverageNow()); remaining > 0 && wait > remaining {
				wait = remaining
			}
			if wait > 0 {
				manualMinuteCoverageSleep(wait)
			}
		}
	}
}

func updateManualMinuteCoverageRetryStatus(metaID uint, round int, stats minuteCoverageStats, issues []minuteCoverageIssue) error {
	if metaID == 0 {
		return nil
	}
	parts := manualCoverageIssueParts(issues, "待覆盖", 3)
	message := fmt.Sprintf("正在重试分钟线缺口（第%d轮，待覆盖:%d，不可覆盖:%d）", round, stats.Pending, stats.Uncoverable)
	if len(parts) > 0 {
		message += "：" + strings.Join(parts, "；")
	}
	return runWithSQLiteBusyRetry(func() error {
		return db.Dao.Model(&models.AiRecommendYieldMeta{}).
			Where("id = ?", metaID).
			Update("last_download_error", message).Error
	})
}

func manualMinuteCoverageRetryBackoff(round int) time.Duration {
	if len(manualMinuteCoverageRetryBackoffs) == 0 {
		return 0
	}
	if round < 0 {
		round = 0
	}
	if round >= len(manualMinuteCoverageRetryBackoffs) {
		return manualMinuteCoverageRetryBackoffs[len(manualMinuteCoverageRetryBackoffs)-1]
	}
	return manualMinuteCoverageRetryBackoffs[round]
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
					_, info = syncMinuteBarsForcedWindow(task.StockCode, task.Start, task.End, runtime.ctx.CrawlTimeout)
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
	_, issues := computeMinuteDownloadCoverageStatsWithIssues(meta, -1)
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
		key := taskKey{
			code:  code,
			start: start.Format(time.RFC3339Nano),
			end:   end.Format(time.RFC3339Nano),
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		tasks = append(tasks, aiRecommendMinuteCoverageTask{
			StockCode: code,
			Start:     start,
			End:       end,
			Forced:    true,
		})
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
		if last.StockCode == task.StockCode && last.Forced == task.Forced && !task.Start.After(last.End.Add(time.Minute)) {
			if task.End.After(last.End) {
				last.End = task.End
			}
			continue
		}
		merged = append(merged, task)
	}
	return merged
}

func cleanupAiRecommendYieldSnapshots(allCodes []string, allRecordIDs []uint) error {
	if err := cleanRemovedYieldStates(allCodes); err != nil {
		return err
	}
	if err := cleanRemovedYieldRecordStates(allRecordIDs); err != nil {
		return err
	}
	return cleanMinuteCacheForTrackedCodes(allCodes)
}

func markAiRecommendYieldRecalcError(metaID uint, err error) {
	if err == nil {
		return
	}
	_ = runWithSQLiteBusyRetry(func() error {
		return db.Dao.Model(&models.AiRecommendYieldMeta{}).Where("id = ?", metaID).Updates(map[string]any{
			"last_error": err.Error(),
		}).Error
	})
}

func newAiRecommendYieldSnapshotWriter(metaID uint, total int) *aiRecommendYieldSnapshotWriter {
	writer := &aiRecommendYieldSnapshotWriter{
		metaID:            metaID,
		recalcTotal:       total,
		recalcDone:        1,
		lastProgressFlush: time.Now(),
		lastSnapshotFlush: time.Now(),
		states:            make([]models.AiRecommendYieldState, 0, 8),
		recordStates:      make([]models.AiRecommendYieldRecordState, 0, 8),
	}
	if total <= 0 {
		writer.recalcDone = 0
	}
	return writer
}

func (w *aiRecommendYieldSnapshotWriter) AppendState(state models.AiRecommendYieldState) error {
	w.states = append(w.states, state)
	w.recalcDone++
	if len(w.states) >= 50 || w.recalcDone == w.recalcTotal || time.Since(w.lastSnapshotFlush) >= 5*time.Second {
		if err := w.flushStates(); err != nil {
			return err
		}
	}
	return w.flushProgressIfNeeded()
}

func (w *aiRecommendYieldSnapshotWriter) AppendRecordState(state models.AiRecommendYieldRecordState) error {
	w.recordStates = append(w.recordStates, state)
	w.recalcDone++
	if len(w.recordStates) >= 50 || w.recalcDone == w.recalcTotal || time.Since(w.lastSnapshotFlush) >= 5*time.Second {
		if err := w.flushRecordStates(); err != nil {
			return err
		}
	}
	return w.flushProgressIfNeeded()
}

func (w *aiRecommendYieldSnapshotWriter) Flush() error {
	if err := w.flushStates(); err != nil {
		return err
	}
	if err := w.flushRecordStates(); err != nil {
		return err
	}
	return nil
}

func (w *aiRecommendYieldSnapshotWriter) flushStates() error {
	if len(w.states) == 0 {
		return nil
	}
	if err := upsertYieldStates(w.states); err != nil {
		return err
	}
	w.states = w.states[:0]
	w.lastSnapshotFlush = time.Now()
	return nil
}

func (w *aiRecommendYieldSnapshotWriter) flushRecordStates() error {
	if len(w.recordStates) == 0 {
		return nil
	}
	if err := upsertYieldRecordStates(w.recordStates); err != nil {
		return err
	}
	w.recordStates = w.recordStates[:0]
	w.lastSnapshotFlush = time.Now()
	return nil
}

func (w *aiRecommendYieldSnapshotWriter) flushProgressIfNeeded() error {
	if w.recalcDone == w.recalcTotal || w.recalcDone%20 == 0 || time.Since(w.lastProgressFlush) >= 5*time.Second {
		if err := updateYieldRecalcProgress(w.metaID, w.recalcDone, w.recalcTotal); err != nil {
			return err
		}
		w.lastProgressFlush = time.Now()
	}
	return nil
}

func buildRecalcTargetCodes(allCodes []string, scope map[string]struct{}, force bool) []string {
	if len(scope) == 0 {
		return allCodes
	}
	result := make([]string, 0, len(allCodes))
	for _, code := range allCodes {
		if _, ok := scope[code]; ok {
			result = append(result, code)
		}
	}
	return result
}

func buildRecalcTargetRecords(allRecords []models.AiRecommendStocks, scope map[string]struct{}, force bool) []models.AiRecommendStocks {
	if len(scope) == 0 {
		return allRecords
	}
	result := make([]models.AiRecommendStocks, 0, len(allRecords))
	for _, rec := range allRecords {
		code := normalizeRecommendStockCode(rec.StockCode)
		if code == "" {
			continue
		}
		if _, ok := scope[code]; ok {
			result = append(result, rec)
		}
	}
	return result
}

func extractRecommendRecordIDs(records []models.AiRecommendStocks) []uint {
	if len(records) == 0 {
		return nil
	}
	ids := make([]uint, 0, len(records))
	for _, rec := range records {
		if rec.ID == 0 {
			continue
		}
		ids = append(ids, rec.ID)
	}
	return ids
}
