package data

import (
	"errors"
	"fmt"
	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	appconfig "go-stock/internal/config"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/duke-git/lancet/v2/datetime"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	aiRecommendRecalcStaleTTL = 8 * time.Minute
	frozenSellPriceFixVersion = "open-gap-v1"
)

type aiRecommendYieldAggregate struct {
	StockCode string
	StockName string

	SignalTime time.Time
	BuyTime    time.Time

	BuyAmountSum   float64
	BuyAmountCount int

	StopProfitSum   float64
	StopProfitCount int

	StopLossSum   float64
	StopLossCount int

	BkNames []string
	BkSet   map[string]struct{}

	ModelNames []string
	ModelSet   map[string]struct{}

	RecommendCount               int
	RequirePrevDayActivityFilter bool
}

type aiRecommendYieldRecalcManager struct {
	mu           sync.Mutex
	running      bool
	pending      bool
	pendingForce bool
	pendingScope map[string]struct{}
}

var globalAiRecommendYieldRecalcManager = &aiRecommendYieldRecalcManager{}
var ensureYieldMetaSchemaOnce sync.Once
var ensureYieldMetaSchemaErr error
var canonicalAShareTsCodeCache sync.Map
var timeNow = time.Now
var fetchMinuteBarsWithTencentFn = fetchMinuteBarsWithTencent
var fetchMinuteBarsWithAkShareFn = fetchMinuteBarsWithAkShare
var fetchMinuteBarsWithSinaFn = fetchMinuteBarsWithSina
var fetchMinuteBarsWithDiemengFn = fetchMinuteBarsWithDiemeng

func requestAiRecommendYieldRecalc(force bool, reason string) {
	requestAiRecommendYieldRecalcWithScope(force, reason, nil)
}

func requestAiRecommendYieldRecalcWithScope(force bool, reason string, scopeCodes []string) {
	scopeMap := normalizeScopeCodes(scopeCodes)
	globalAiRecommendYieldRecalcManager.Request(force, reason, scopeMap)
}

func startManualAiRecommendMinuteDownload() (map[string]any, error) {
	EnsureDiemengSelfCheckAsync("manual_minute_download")
	if schemaErr := ensureYieldMetaSchema(); schemaErr != nil {
		return nil, schemaErr
	}
	meta, err := getOrCreateYieldMeta()
	if err != nil {
		return nil, err
	}

	now := time.Now()
	if resetStaleYieldRecalcIfNeeded(meta) {
		meta.RecalcInProgress = false
	}
	if meta.RecalcInProgress {
		return map[string]any{
			"accepted":   false,
			"inProgress": true,
			"message":    "后台任务进行中，请等待完成",
		}, nil
	}

	// Manual download should cover all recommendation records, then re-evaluate
	// status against the latest closed trading window.
	scopeCodes, err := loadScopeCodesForManualDownload()
	if err != nil {
		return nil, err
	}
	if len(scopeCodes) == 0 {
		return map[string]any{
			"accepted":   false,
			"inProgress": false,
			"message":    "暂无股票可下载",
		}, nil
	}

	if err = db.Dao.Model(&models.AiRecommendYieldMeta{}).Where("id = ?", meta.ID).Updates(map[string]any{
		"last_manual_download_at": now,
		"manual_cooldown_until":   nil,
		"last_query_recalc_at":    nil,
		"query_cooldown_until":    nil,
		"akshare_install_error":   "",
		"download_in_progress":    true,
		"download_total":          len(scopeCodes),
		"download_done":           0,
		"download_progress":       0,
		"last_download_error":     "",
	}).Error; err != nil {
		return nil, err
	}

	requestAiRecommendYieldRecalcWithScope(true, "manual_minute_download", scopeCodes)
	return map[string]any{
		"accepted":          true,
		"inProgress":        true,
		"scopeCount":        len(scopeCodes),
		"cooldownUntil":     "",
		"cooldownRemainSec": 0,
		"message":           fmt.Sprintf("已开始下载分钟线并触发收益重算（%d 只股票）", len(scopeCodes)),
	}, nil
}

func loadScopeCodesForManualDownload() ([]string, error) {
	dirtyCodes, err := loadDirtyAiRecommendYieldCodes(aiRecommendYieldModeStrict)
	if err != nil {
		return nil, err
	}
	if len(dirtyCodes) > 0 {
		return dirtyCodes, nil
	}
	aggrMap, err := loadAiRecommendYieldAggregates()
	if err != nil {
		return nil, err
	}
	if len(aggrMap) == 0 {
		return []string{}, nil
	}
	codes := make([]string, 0, len(aggrMap))
	for code := range aggrMap {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes, nil
}

func ensureYieldDirtySchema() error {
	return db.Dao.AutoMigrate(&models.AiRecommendYieldDirtyCode{})
}

func markAiRecommendYieldDirtyCodes(scopeCodes []string, reason string, mode string) error {
	if schemaErr := ensureYieldDirtySchema(); schemaErr != nil {
		return schemaErr
	}
	normalized := normalizeScopeCodes(scopeCodes)
	if len(normalized) == 0 {
		return nil
	}
	rows := make([]models.AiRecommendYieldDirtyCode, 0, len(normalized))
	for code := range normalized {
		rows = append(rows, models.AiRecommendYieldDirtyCode{
			StockCode:  code,
			Reason:     strings.TrimSpace(reason),
			ModeNeeded: normalizeAiRecommendYieldMode(mode),
		})
	}
	return db.Dao.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "stock_code"}},
		DoUpdates: clause.AssignmentColumns([]string{"updated_at", "reason", "mode_needed"}),
	}).CreateInBatches(rows, 100).Error
}

func loadDirtyAiRecommendYieldCodes(mode string) ([]string, error) {
	if schemaErr := ensureYieldDirtySchema(); schemaErr != nil {
		return nil, schemaErr
	}
	rows := make([]models.AiRecommendYieldDirtyCode, 0, 64)
	q := db.Dao.Model(&models.AiRecommendYieldDirtyCode{})
	mode = normalizeAiRecommendYieldMode(mode)
	if mode != "" {
		q = q.Where("mode_needed = ? OR mode_needed = ''", mode)
	}
	if err := q.Order("updated_at ASC, stock_code ASC").Find(&rows).Error; err != nil {
		if isSQLiteNoSuchTable(err) {
			return []string{}, nil
		}
		return nil, err
	}
	codes := make([]string, 0, len(rows))
	for _, row := range rows {
		code := normalizeRecommendStockCode(row.StockCode)
		if code == "" {
			continue
		}
		codes = append(codes, code)
	}
	return codes, nil
}

func loadDirtyAiRecommendYieldCodeSet(mode string) (map[string]models.AiRecommendYieldDirtyCode, error) {
	rows := make([]models.AiRecommendYieldDirtyCode, 0, 64)
	if schemaErr := ensureYieldDirtySchema(); schemaErr != nil {
		return nil, schemaErr
	}
	q := db.Dao.Model(&models.AiRecommendYieldDirtyCode{})
	mode = normalizeAiRecommendYieldMode(mode)
	if mode != "" {
		q = q.Where("mode_needed = ? OR mode_needed = ''", mode)
	}
	if err := q.Find(&rows).Error; err != nil {
		if isSQLiteNoSuchTable(err) {
			return map[string]models.AiRecommendYieldDirtyCode{}, nil
		}
		return nil, err
	}
	result := make(map[string]models.AiRecommendYieldDirtyCode, len(rows))
	for _, row := range rows {
		code := normalizeRecommendStockCode(row.StockCode)
		if code == "" {
			continue
		}
		result[code] = row
	}
	return result, nil
}

func clearAiRecommendYieldDirtyCodes(scopeCodes []string) error {
	normalized := normalizeScopeCodes(scopeCodes)
	if len(normalized) == 0 {
		return nil
	}
	codes := make([]string, 0, len(normalized))
	for code := range normalized {
		codes = append(codes, code)
	}
	return db.Dao.Where("stock_code IN ?", codes).Delete(&models.AiRecommendYieldDirtyCode{}).Error
}

func (m *aiRecommendYieldRecalcManager) Request(force bool, reason string, scope map[string]struct{}) {
	m.mu.Lock()
	if m.running {
		m.pending = true
		if force {
			m.pendingForce = true
			m.pendingScope = nil
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
			nextReason = "pending"
			nextScope = copyScopeMap(m.pendingScope)
			m.pending = false
			m.pendingForce = false
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
	meta       *models.AiRecommendYieldMeta
	now        time.Time
	inTrading  bool
	latestDate time.Time
	ctx        yieldBuildContext
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

func rebuildAiRecommendYieldSnapshot(force bool, reason string, scope map[string]struct{}) error {
	if schemaErr := ensureYieldMetaSchema(); schemaErr != nil {
		return schemaErr
	}

	runtime, finalize, err := beginAiRecommendYieldRecalc(force, reason)
	if err != nil {
		return err
	}
	defer finalize(&err)

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
	prefetchAiRecommendMinuteCoverage(runtime, targets)
	if err = processAiRecommendYieldTargets(runtime, targets, writer); err != nil {
		markAiRecommendYieldRecalcError(runtime.meta.ID, err)
		return err
	}
	if err = writer.Flush(); err != nil {
		markAiRecommendYieldRecalcError(runtime.meta.ID, err)
		return err
	}
	if err = clearAiRecommendYieldDirtyCodes(targets.targetCodes); err != nil {
		markAiRecommendYieldRecalcError(runtime.meta.ID, err)
		return err
	}

	fullRecalc := force || len(scope) == 0 || (len(targets.targetCodes) == len(targets.allCodes) && len(targets.targetRecords) == len(targets.records))
	if fullRecalc {
		if err = cleanupAiRecommendYieldSnapshots(targets.allCodes, targets.allRecordIDs); err != nil {
			markAiRecommendYieldRecalcError(runtime.meta.ID, err)
			return err
		}
	}

	go sendYieldCSVEmailIfEnabled(reason, fullRecalc)
	return nil
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

	finalize := func(runErr *error) {
		close(heartbeatStop)
		finishAiRecommendYieldRecalc(meta.ID, runtime.now, *runErr)
	}
	return runtime, finalize, nil
}

func markAiRecommendYieldRecalcStarted(metaID uint) error {
	return db.Dao.Model(&models.AiRecommendYieldMeta{}).Where("id = ?", metaID).Updates(map[string]any{
		"recalc_in_progress": true,
		"last_error":         "",
		"recalc_total":       0,
		"recalc_done":        0,
		"recalc_progress":    1,
	}).Error
}

func startAiRecommendYieldHeartbeat(metaID uint) chan struct{} {
	heartbeatStop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_ = db.Dao.Model(&models.AiRecommendYieldMeta{}).
					Where("id = ? AND recalc_in_progress = ?", metaID, true).
					Updates(map[string]any{"updated_at": time.Now()}).Error
			case <-heartbeatStop:
				return
			}
		}
	}()
	return heartbeatStop
}

func finishAiRecommendYieldRecalc(metaID uint, startedAt time.Time, runErr error) {
	updateMap := map[string]any{
		"recalc_in_progress": false,
		"updated_at":         time.Now(),
	}
	if runErr == nil {
		updateMap["last_full_recalc_at"] = startedAt
		updateMap["recalc_progress"] = 100
		updateMap["download_in_progress"] = false
		updateMap["download_done"] = gorm.Expr("CASE WHEN download_total > 0 THEN download_total ELSE download_done END")
		updateMap["download_progress"] = gorm.Expr("CASE WHEN download_total > 0 THEN 100 ELSE download_progress END")
		updateMap["last_download_error"] = ""
	} else {
		updateMap["download_in_progress"] = false
		updateMap["last_download_error"] = runErr.Error()
	}
	if e := db.Dao.Model(&models.AiRecommendYieldMeta{}).Where("id = ?", metaID).Updates(updateMap).Error; e != nil {
		logger.SugaredLogger.Errorf("update ai_recommend_yield_meta failed: %v", e)
	}
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

	priceMap, priceTimeMap := fetchCurrentPriceMap(nil)
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

	priceMap, priceTimeMap := fetchCurrentPriceMap(aggrMap)
	runtime.ctx.CurrentPriceMap = priceMap
	runtime.ctx.CurrentPriceTimeMap = priceTimeMap

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
				resultCh <- executeAiRecommendYieldCalcTask(task, runtime.ctx)
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

func executeAiRecommendYieldCalcTask(task aiRecommendYieldCalcTask, ctx yieldBuildContext) aiRecommendYieldCalcResult {
	result := aiRecommendYieldCalcResult{
		RecordStates: make([]models.AiRecommendYieldRecordState, 0, len(task.Records)),
	}
	if task.Aggregate != nil {
		state := buildYieldStateFromAggregate(task.Aggregate, task.ExistingState, ctx)
		result.State = &state
	}
	for _, rec := range task.Records {
		recordState := buildYieldRecordStateFromRecommend(rec, task.ExistingRecord[rec.ID], ctx)
		result.RecordStates = append(result.RecordStates, recordState)
	}
	return result
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

func prefetchAiRecommendMinuteCoverage(runtime *aiRecommendYieldRecalcRuntime, targets *aiRecommendYieldTargets) {
	tasks := buildAiRecommendMinuteCoverageTasks(runtime, targets)
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
				_, info := syncMinuteBars(task.StockCode, task.Start, task.End, runtime.ctx.CrawlTimeout, runtime.ctx.Reason == "manual_minute_download")
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
	for range progressCh {
		done++
		_ = updateYieldDownloadProgress(runtime.meta.ID, done, total)
	}
}

func buildAiRecommendMinuteCoverageTasks(runtime *aiRecommendYieldRecalcRuntime, targets *aiRecommendYieldTargets) []aiRecommendMinuteCoverageTask {
	if runtime == nil || targets == nil {
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
	_ = db.Dao.Model(&models.AiRecommendYieldMeta{}).Where("id = ?", metaID).Updates(map[string]any{
		"last_error": err.Error(),
	}).Error
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
	if force || len(scope) == 0 {
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
	if force || len(scope) == 0 {
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

func getOrCreateYieldMeta() (*models.AiRecommendYieldMeta, error) {
	meta := &models.AiRecommendYieldMeta{}
	err := db.Dao.Model(&models.AiRecommendYieldMeta{}).First(meta).Error
	if err == nil {
		if fixErr := applyFrozenSellPriceFix(meta); fixErr != nil {
			logger.SugaredLogger.Warnf("apply frozen sell price fix failed: %v", fixErr)
		}
		return meta, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if createErr := db.Dao.Create(meta).Error; createErr != nil {
		return nil, createErr
	}
	if fixErr := applyFrozenSellPriceFix(meta); fixErr != nil {
		logger.SugaredLogger.Warnf("apply frozen sell price fix failed: %v", fixErr)
	}
	return meta, nil
}

type frozenSellPriceSnapshot interface {
	getID() uint
	getStockCode() string
	getSellTime() *time.Time
	getRealizedSellAmount() *float64
	getBuyAmount() float64
	getPositionStatus() string
	getStopProfitAmount() *float64
	getStopLossAmount() *float64
}

type frozenSellPriceUpdater interface {
	updateSellSnapshot(id uint, sellPrice, yield float64, yieldText string, updatedAt time.Time) error
}

type frozenYieldStateSnapshot struct{ models.AiRecommendYieldState }

type frozenYieldRecordSnapshot struct {
	models.AiRecommendYieldRecordState
}

type frozenYieldStateUpdater struct{}

type frozenYieldRecordUpdater struct{}

func applyFrozenSellPriceFix(meta *models.AiRecommendYieldMeta) error {
	if meta == nil || meta.ID == 0 {
		return nil
	}
	if strings.TrimSpace(meta.FrozenSellPriceFixVersion) == frozenSellPriceFixVersion {
		return nil
	}
	if err := rewriteFrozenSellSnapshots(loadFrozenYieldStateSnapshots, frozenYieldStateUpdater{}); err != nil {
		return err
	}
	if err := rewriteFrozenSellSnapshots(loadFrozenYieldRecordSnapshots, frozenYieldRecordUpdater{}); err != nil {
		return err
	}
	now := time.Now()
	if err := db.Dao.Model(&models.AiRecommendYieldMeta{}).Where("id = ?", meta.ID).Updates(map[string]any{
		"frozen_sell_price_fix_version": frozenSellPriceFixVersion,
		"updated_at":                    now,
	}).Error; err != nil {
		return err
	}
	meta.FrozenSellPriceFixVersion = frozenSellPriceFixVersion
	meta.UpdatedAt = now
	return nil
}

func loadFrozenYieldStateSnapshots() ([]frozenSellPriceSnapshot, error) {
	rows := make([]models.AiRecommendYieldState, 0, 64)
	if err := db.Dao.Model(&models.AiRecommendYieldState{}).
		Where("frozen = ? AND sell_time IS NOT NULL AND realized_sell_amount IS NOT NULL AND position_status IN ?", true, []string{"已止盈", "已止损"}).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]frozenSellPriceSnapshot, 0, len(rows))
	for _, row := range rows {
		result = append(result, frozenYieldStateSnapshot{AiRecommendYieldState: row})
	}
	return result, nil
}

func loadFrozenYieldRecordSnapshots() ([]frozenSellPriceSnapshot, error) {
	rows := make([]models.AiRecommendYieldRecordState, 0, 64)
	if err := db.Dao.Model(&models.AiRecommendYieldRecordState{}).
		Where("frozen = ? AND sell_time IS NOT NULL AND realized_sell_amount IS NOT NULL AND position_status IN ?", true, []string{"已止盈", "已止损"}).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]frozenSellPriceSnapshot, 0, len(rows))
	for _, row := range rows {
		result = append(result, frozenYieldRecordSnapshot{AiRecommendYieldRecordState: row})
	}
	return result, nil
}

func rewriteFrozenSellSnapshots(load func() ([]frozenSellPriceSnapshot, error), updater frozenSellPriceUpdater) error {
	rows, err := load()
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.getSellTime() == nil || row.getSellTime().IsZero() {
			continue
		}
		barTime := normalizeMinuteTime(row.getSellTime().In(cnLocation()))
		bars, err := listMinuteBarsFromCache(row.getStockCode(), barTime, barTime.Add(time.Minute))
		if err != nil || len(bars) == 0 {
			continue
		}
		price, ok := correctedFrozenSellPrice(row.getPositionStatus(), row.getStopProfitAmount(), row.getStopLossAmount(), bars[0])
		if !ok {
			continue
		}
		if row.getRealizedSellAmount() != nil && round2(*row.getRealizedSellAmount()) == price {
			continue
		}
		yield, yieldText := calculateFrozenSellYield(row.getStockCode(), row.getBuyAmount(), price)
		if err := updater.updateSellSnapshot(row.getID(), price, yield, yieldText, time.Now()); err != nil {
			return err
		}
	}
	return nil
}

func calculateFrozenSellYield(stockCode string, buyAmount, sellPrice float64) (float64, string) {
	yield := 0.0
	yieldText := "--"
	if buyAmount <= 0 {
		return yield, yieldText
	}
	result := calculateNetYield(stockCode, buyAmount, sellPrice)
	if !result.Valid {
		return yield, yieldText
	}
	return result.YieldRate, result.YieldText
}

func (f frozenYieldStateSnapshot) getID() uint                     { return f.ID }
func (f frozenYieldStateSnapshot) getStockCode() string            { return f.StockCode }
func (f frozenYieldStateSnapshot) getSellTime() *time.Time         { return f.SellTime }
func (f frozenYieldStateSnapshot) getRealizedSellAmount() *float64 { return f.RealizedSellAmount }
func (f frozenYieldStateSnapshot) getBuyAmount() float64           { return f.BuyAmount }
func (f frozenYieldStateSnapshot) getPositionStatus() string       { return f.PositionStatus }
func (f frozenYieldStateSnapshot) getStopProfitAmount() *float64   { return f.StopProfitAmount }
func (f frozenYieldStateSnapshot) getStopLossAmount() *float64     { return f.StopLossAmount }

func (f frozenYieldRecordSnapshot) getID() uint                     { return f.ID }
func (f frozenYieldRecordSnapshot) getStockCode() string            { return f.StockCode }
func (f frozenYieldRecordSnapshot) getSellTime() *time.Time         { return f.SellTime }
func (f frozenYieldRecordSnapshot) getRealizedSellAmount() *float64 { return f.RealizedSellAmount }
func (f frozenYieldRecordSnapshot) getBuyAmount() float64           { return f.BuyAmount }
func (f frozenYieldRecordSnapshot) getPositionStatus() string       { return f.PositionStatus }
func (f frozenYieldRecordSnapshot) getStopProfitAmount() *float64   { return f.StopProfitAmount }
func (f frozenYieldRecordSnapshot) getStopLossAmount() *float64     { return f.StopLossAmount }

func (frozenYieldStateUpdater) updateSellSnapshot(id uint, sellPrice, yield float64, yieldText string, updatedAt time.Time) error {
	return db.Dao.Model(&models.AiRecommendYieldState{}).Where("id = ?", id).Updates(map[string]any{
		"realized_sell_amount": sellPrice,
		"yield_rate":           yield,
		"yield_rate_text":      yieldText,
		"updated_at":           updatedAt,
	}).Error
}

func (frozenYieldRecordUpdater) updateSellSnapshot(id uint, sellPrice, yield float64, yieldText string, updatedAt time.Time) error {
	return db.Dao.Model(&models.AiRecommendYieldRecordState{}).Where("id = ?", id).Updates(map[string]any{
		"realized_sell_amount": sellPrice,
		"yield_rate":           yield,
		"yield_rate_text":      yieldText,
		"updated_at":           updatedAt,
	}).Error
}

func correctedFrozenSellPrice(status string, stopProfit, stopLoss *float64, bar minuteBar) (float64, bool) {
	if isSoldPositionStatus(status) && strings.TrimSpace(status) == "已止盈" && stopProfit != nil && bar.Open >= *stopProfit {
		return round2(bar.Open), true
	}
	if isSoldPositionStatus(status) && strings.TrimSpace(status) == "已止损" && stopLoss != nil && bar.Open <= *stopLoss {
		return round2(bar.Open), true
	}
	return 0, false
}

func syncYieldStateIdentityFields() error {
	now := time.Now()
	return db.Dao.Exec(`
UPDATE ai_recommend_yield_state
SET recommend_category = '',
    recommend_time = signal_time,
    updated_at = ?
WHERE COALESCE(recommend_category, '') <> ''
   OR COALESCE(recommend_time, '') <> COALESCE(signal_time, '')
`, now).Error
}

func syncYieldRecordStateIdentityFields() error {
	now := time.Now()
	return db.Dao.Exec(`
UPDATE ai_recommend_yield_record_state
SET stock_code = COALESCE((SELECT stock_code FROM ai_recommend_stocks WHERE id = ai_recommend_yield_record_state.recommend_id), stock_code),
    stock_name = COALESCE((SELECT stock_name FROM ai_recommend_stocks WHERE id = ai_recommend_yield_record_state.recommend_id), stock_name),
    model_name = COALESCE((SELECT model_name FROM ai_recommend_stocks WHERE id = ai_recommend_yield_record_state.recommend_id), model_name),
    bk_name = COALESCE((SELECT bk_name FROM ai_recommend_stocks WHERE id = ai_recommend_yield_record_state.recommend_id), bk_name),
    recommend_category = COALESCE((SELECT recommend_category FROM ai_recommend_stocks WHERE id = ai_recommend_yield_record_state.recommend_id), ''),
    recommend_time = COALESCE((SELECT COALESCE(data_time, created_at) FROM ai_recommend_stocks WHERE id = ai_recommend_yield_record_state.recommend_id), recommend_time),
    signal_time = COALESCE((SELECT COALESCE(data_time, created_at) FROM ai_recommend_stocks WHERE id = ai_recommend_yield_record_state.recommend_id), signal_time),
    updated_at = ?
WHERE EXISTS (
    SELECT 1
    FROM ai_recommend_stocks ars
    WHERE ars.id = ai_recommend_yield_record_state.recommend_id
      AND (
        COALESCE(ai_recommend_yield_record_state.stock_code, '') <> COALESCE(ars.stock_code, '')
        OR COALESCE(ai_recommend_yield_record_state.stock_name, '') <> COALESCE(ars.stock_name, '')
        OR COALESCE(ai_recommend_yield_record_state.model_name, '') <> COALESCE(ars.model_name, '')
        OR COALESCE(ai_recommend_yield_record_state.bk_name, '') <> COALESCE(ars.bk_name, '')
        OR COALESCE(ai_recommend_yield_record_state.recommend_category, '') <> COALESCE(ars.recommend_category, '')
        OR COALESCE(ai_recommend_yield_record_state.recommend_time, '') <> COALESCE(COALESCE(ars.data_time, ars.created_at), '')
        OR COALESCE(ai_recommend_yield_record_state.signal_time, '') <> COALESCE(COALESCE(ars.data_time, ars.created_at), '')
      )
)
`, now).Error
}

func resetStaleYieldRecalcIfNeeded(meta *models.AiRecommendYieldMeta) bool {
	if meta == nil || !meta.RecalcInProgress || meta.ID == 0 {
		return false
	}
	last := meta.UpdatedAt
	if last.IsZero() {
		last = time.Now()
	}
	if time.Since(last) < aiRecommendRecalcStaleTTL {
		return false
	}

	now := time.Now()
	err := db.Dao.Model(&models.AiRecommendYieldMeta{}).
		Where("id = ? AND recalc_in_progress = ?", meta.ID, true).
		Updates(map[string]any{
			"recalc_in_progress": false,
			"last_error":         "检测到历史重算任务卡死，已自动解锁并等待重新触发",
			"updated_at":         now,
		}).Error
	if err != nil {
		logger.SugaredLogger.Warnf("reset stale ai_recommend_yield_meta failed: %v", err)
		return false
	}
	meta.RecalcInProgress = false
	meta.UpdatedAt = now
	return true
}

func ensureYieldMetaSchema() error {
	ensureYieldMetaSchemaOnce.Do(func() {
		ensureYieldMetaSchemaErr = db.Dao.AutoMigrate(
			&models.AiRecommendYieldMeta{},
			&models.AiRecommendYieldState{},
			&models.AiRecommendYieldRecordState{},
			&models.AiRecommendYieldDirtyCode{},
			&models.AiRecommendMinuteBar{},
		)
		if ensureYieldMetaSchemaErr != nil {
			return
		}
		if err := syncYieldStateIdentityFields(); err != nil {
			ensureYieldMetaSchemaErr = err
			return
		}
		if err := syncYieldRecordStateIdentityFields(); err != nil {
			ensureYieldMetaSchemaErr = err
		}
	})
	return ensureYieldMetaSchemaErr
}

func loadYieldScopeCodesForQuery(query *models.AiRecommendStocksQuery) ([]string, error) {
	q := db.Dao.Model(&models.AiRecommendStocks{})

	if query != nil {
		if query.StockCode != "" {
			q = q.Where("stock_code LIKE ?", "%"+query.StockCode+"%")
		}
		if query.StockName != "" {
			q = q.Where("stock_name LIKE ?", "%"+query.StockName+"%")
		}
		if query.BkName != "" {
			q = q.Where("bk_name LIKE ?", "%"+query.BkName+"%")
		}
		if query.ModelName != "" {
			q = q.Where("model_name LIKE ?", "%"+query.ModelName+"%")
		}
		if query.StartDate != "" && query.EndDate != "" {
			startDate := normalizeDateTime(query.StartDate)
			endDate := normalizeDateTime(query.EndDate)
			startTime, err := parseDateTimeWithFallback(startDate)
			if err == nil {
				endTime, endErr := parseDateTimeWithFallback(endDate)
				if endErr == nil {
					q = q.Where("data_time BETWEEN ? AND ?", datetime.BeginOfDay(startTime), datetime.EndOfDay(endTime))
				}
			}
		}
		if query.StartDate != "" && query.EndDate == "" {
			startDate := normalizeDateTime(query.StartDate)
			startTime, err := parseDateTimeWithFallback(startDate)
			if err == nil {
				q = q.Where("data_time BETWEEN ? AND ?", datetime.BeginOfDay(startTime), datetime.EndOfDay(startTime))
			}
		}
	}

	rows := make([]models.AiRecommendStocks, 0)
	if err := q.Select("stock_code", "recommend_category", "recommend_status").Find(&rows).Error; err != nil {
		return nil, err
	}

	set := map[string]struct{}{}
	codes := make([]string, 0, len(rows))
	for _, row := range rows {
		if !shouldTrackRecommendInYield(&row) {
			continue
		}
		code := normalizeRecommendStockCode(row.StockCode)
		if code == "" {
			continue
		}
		if _, ok := set[code]; ok {
			continue
		}
		set[code] = struct{}{}
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes, nil
}

func loadAiRecommendYieldAggregates() (map[string]*aiRecommendYieldAggregate, error) {
	return loadAiRecommendYieldAggregatesAfter(time.Time{})
}

func loadAiRecommendYieldAggregatesAfter(coverableStartMinute time.Time) (map[string]*aiRecommendYieldAggregate, error) {
	var list []models.AiRecommendStocks
	err := db.Dao.Model(&models.AiRecommendStocks{}).
		Order("data_time ASC, created_at ASC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	list, err = applyYieldOverridesToRecommendRecords(list)
	if err != nil {
		return nil, err
	}
	coverableStartMinute = normalizeMinuteTime(coverableStartMinute)

	aggrMap := map[string]*aiRecommendYieldAggregate{}
	for _, item := range list {
		if !shouldTrackRecommendInYield(&item) {
			continue
		}
		if !coverableStartMinute.IsZero() {
			recordTime := recommendRecordTime(item)
			if recordTime.IsZero() {
				continue
			}
			requiredStart := resolveRecommendSellEligibleTime(recordTime)
			if requiredStart.Before(coverableStartMinute) {
				// Outside the minute-data coverable window; skip so the yield snapshot
				// only tracks what AkShare can realistically cover.
				continue
			}
		}

		code := normalizeRecommendStockCode(item.StockCode)
		if code == "" {
			continue
		}
		aggr, exists := aggrMap[code]
		if !exists {
			recordTime := recommendRecordTime(item)
			aggr = &aiRecommendYieldAggregate{
				StockCode:  code,
				StockName:  strings.TrimSpace(item.StockName),
				SignalTime: recordTime,
				BuyTime:    resolveRecommendBuyTime(recordTime),
				BkSet:      map[string]struct{}{},
				ModelSet:   map[string]struct{}{},
				BkNames:    make([]string, 0, 2),
				ModelNames: make([]string, 0, 2),
			}
			aggrMap[code] = aggr
		}

		aggr.RecommendCount++
		if aggr.StockName == "" && strings.TrimSpace(item.StockName) != "" {
			aggr.StockName = strings.TrimSpace(item.StockName)
		}
		recordTime := recommendRecordTime(item)
		if aggr.SignalTime.IsZero() || recordTime.Before(aggr.SignalTime) {
			aggr.SignalTime = recordTime
		}
		buyTime := resolveRecommendBuyTime(recordTime)
		if buyTime.Before(aggr.BuyTime) {
			aggr.BuyTime = buyTime
		}

		if buy, ok := parseBuyPrice(item.StockPrice); ok {
			aggr.BuyAmountSum += buy
			aggr.BuyAmountCount++
		}
		if recommendRequiresPrevDayActivityFilter(item) {
			aggr.RequirePrevDayActivityFilter = true
		}
		if stopProfit, ok := parseStopProfitPrice(item); ok {
			aggr.StopProfitSum += stopProfit
			aggr.StopProfitCount++
		}
		if stopLoss, ok := parseStopLossPrice(item); ok {
			aggr.StopLossSum += stopLoss
			aggr.StopLossCount++
		}

		bkName := strings.TrimSpace(item.BkName)
		if bkName != "" {
			if _, has := aggr.BkSet[bkName]; !has {
				aggr.BkSet[bkName] = struct{}{}
				aggr.BkNames = append(aggr.BkNames, bkName)
			}
		}

		modelName := strings.TrimSpace(item.ModelName)
		if modelName != "" {
			if _, has := aggr.ModelSet[modelName]; !has {
				aggr.ModelSet[modelName] = struct{}{}
				aggr.ModelNames = append(aggr.ModelNames, modelName)
			}
		}
	}

	for _, aggr := range aggrMap {
		sort.Strings(aggr.BkNames)
		sort.Strings(aggr.ModelNames)
	}

	return aggrMap, nil
}

func loadAiRecommendYieldRecordsAfter(coverableStartMinute time.Time) ([]models.AiRecommendStocks, error) {
	records := make([]models.AiRecommendStocks, 0, 128)
	err := db.Dao.Model(&models.AiRecommendStocks{}).
		Order("COALESCE(data_time, created_at) ASC, id ASC").
		Find(&records).Error
	if err != nil {
		return nil, err
	}
	records, err = applyYieldOverridesToRecommendRecords(records)
	if err != nil {
		return nil, err
	}
	coverableStartMinute = normalizeMinuteTime(coverableStartMinute)
	if len(records) == 0 {
		return records, nil
	}
	filtered := make([]models.AiRecommendStocks, 0, len(records))
	for _, rec := range records {
		if !shouldDisplayRecommendInYield(&rec) {
			continue
		}
		if eligibility, _ := resolveRecommendBacktestEligibility(&rec); eligibility != recommendBacktestEligible {
			continue
		}
		recordTime := recommendRecordTime(rec)
		if recordTime.IsZero() {
			continue
		}
		if coverableStartMinute.IsZero() {
			filtered = append(filtered, rec)
			continue
		}
		requiredStart := resolveRecommendSellEligibleTime(recordTime)
		if requiredStart.Before(coverableStartMinute) {
			continue
		}
		filtered = append(filtered, rec)
	}
	return filtered, nil
}

func recommendRecordTime(item models.AiRecommendStocks) time.Time {
	if item.DataTime != nil && !item.DataTime.IsZero() {
		return *item.DataTime
	}
	return item.CreatedAt
}

func loadExistingYieldStateMap() (map[string]*models.AiRecommendYieldState, error) {
	var states []models.AiRecommendYieldState
	err := db.Dao.Model(&models.AiRecommendYieldState{}).Find(&states).Error
	if err != nil {
		return nil, err
	}
	result := make(map[string]*models.AiRecommendYieldState, len(states))
	for i := range states {
		state := states[i]
		state.StockCode = strings.ToUpper(strings.TrimSpace(state.StockCode))
		copied := state
		result[state.StockCode] = &copied
	}
	return result, nil
}

func loadExistingYieldRecordStateMap() (map[uint]*models.AiRecommendYieldRecordState, error) {
	var states []models.AiRecommendYieldRecordState
	err := db.Dao.Model(&models.AiRecommendYieldRecordState{}).Find(&states).Error
	if err != nil {
		return nil, err
	}
	result := make(map[uint]*models.AiRecommendYieldRecordState, len(states))
	for i := range states {
		state := states[i]
		if state.RecommendID == 0 {
			continue
		}
		state.StockCode = strings.ToUpper(strings.TrimSpace(state.StockCode))
		copied := state
		result[state.RecommendID] = &copied
	}
	return result, nil
}

func fetchCurrentPriceMap(aggrMap map[string]*aiRecommendYieldAggregate) (map[string]float64, map[string]string) {
	priceMap := map[string]float64{}
	priceTimeMap := map[string]string{}
	if len(aggrMap) == 0 {
		return priceMap, priceTimeMap
	}

	queryCodes := make([]string, 0, len(aggrMap))
	reverseMap := map[string]string{}
	for code := range aggrMap {
		quoteCode := toQuoteCode(code)
		if quoteCode == "" {
			continue
		}
		queryCodes = append(queryCodes, quoteCode)
		reverseMap[strings.ToLower(quoteCode)] = code
	}
	if len(queryCodes) == 0 {
		return priceMap, priceTimeMap
	}

	stockData, err := NewStockDataApi().GetStockCodeRealTimeData(queryCodes...)
	if err != nil || stockData == nil {
		return priceMap, priceTimeMap
	}
	for _, info := range *stockData {
		key := strings.ToLower(strings.TrimSpace(info.Code))
		normalizedCode := reverseMap[key]
		if normalizedCode == "" {
			continue
		}
		if price, ok := parseBuyPrice(info.Price); ok {
			priceMap[normalizedCode] = round2(price)
		}
		priceTime := strings.TrimSpace(strings.TrimSpace(info.Date) + " " + strings.TrimSpace(info.Time))
		if priceTime != "" {
			priceTimeMap[normalizedCode] = priceTime
		}
	}
	return priceMap, priceTimeMap
}

type yieldBuildContext struct {
	Force               bool
	Reason              string
	Now                 time.Time
	InTradingSession    bool
	LatestTradeDate     time.Time
	CrawlTimeout        int64
	Tushare             *TushareApi
	CurrentPriceMap     map[string]float64
	CurrentPriceTimeMap map[string]string
}

func isSoldPositionStatus(status string) bool {
	status = strings.TrimSpace(status)
	return status == "已止盈" || status == "已止损"
}

func sanitizeYieldSellSnapshot(sellFloorTime time.Time, positionStatus *string, sellTime **time.Time, realizedSellAmount **float64, frozen *bool) bool {
	invalid := false
	if positionStatus != nil && isSoldPositionStatus(*positionStatus) {
		if sellTime == nil || *sellTime == nil {
			invalid = true
		} else if !sellFloorTime.IsZero() && (*sellTime).Before(sellFloorTime) {
			invalid = true
		}
	}
	if !invalid {
		return false
	}
	if positionStatus != nil {
		*positionStatus = "持有"
	}
	if sellTime != nil {
		*sellTime = nil
	}
	if realizedSellAmount != nil {
		*realizedSellAmount = nil
	}
	if frozen != nil {
		*frozen = false
	}
	return true
}

func buildYieldStateFromAggregate(aggr *aiRecommendYieldAggregate, existing *models.AiRecommendYieldState, ctx yieldBuildContext) models.AiRecommendYieldState {
	state := models.AiRecommendYieldState{
		StockCode:         aggr.StockCode,
		StockName:         aggr.StockName,
		ModelNames:        strings.Join(aggr.ModelNames, "、"),
		BkName:            strings.Join(aggr.BkNames, "、"),
		RecommendCount:    aggr.RecommendCount,
		RecommendCategory: "",
		PositionStatus:    "待激活",
		YieldRateText:     "--",
		DataStatus:        "正常",
		TotalScopeStart:   aggr.SignalTime.Format("2006-01-02"),
		TotalScopeEnd:     ctx.Now.Format("2006-01-02"),
	}
	if !aggr.SignalTime.IsZero() {
		t := aggr.SignalTime
		state.RecommendTime = &t
		state.SignalTime = &t
	}
	state.ActivationStatus = "pending"

	if aggr.StopProfitCount > 0 {
		v := calculateAvg(aggr.StopProfitSum, aggr.StopProfitCount)
		state.StopProfitAmount = &v
	}
	if aggr.StopLossCount > 0 {
		v := calculateAvg(aggr.StopLossSum, aggr.StopLossCount)
		state.StopLossAmount = &v
	}
	state.SellAmountText = buildSellAmountText(state.StopProfitAmount, state.StopLossAmount)

	if existing != nil {
		state.ID = existing.ID
		state.CreatedAt = existing.CreatedAt
		state.SignalTime = existing.SignalTime
		state.ActivationStatus = existing.ActivationStatus
		state.ActivationTime = existing.ActivationTime
		state.ActivationPrice = existing.ActivationPrice
		state.BuyTime = existing.BuyTime
		state.BuyAmount = existing.BuyAmount
		state.SellTime = existing.SellTime
		state.RealizedSellAmount = existing.RealizedSellAmount
		state.PositionStatus = existing.PositionStatus
		state.CurrentPrice = existing.CurrentPrice
		state.CurrentPriceTime = existing.CurrentPriceTime
		state.YieldRate = existing.YieldRate
		state.YieldRateText = existing.YieldRateText
		state.DataStatus = existing.DataStatus
		state.DataStatusReason = existing.DataStatusReason
		state.LastMinuteTs = existing.LastMinuteTs
		state.LastRecalcAt = existing.LastRecalcAt
		state.MinuteCacheStart = existing.MinuteCacheStart
		state.MinuteCacheEnd = existing.MinuteCacheEnd
		state.MinuteCacheSource = existing.MinuteCacheSource
		state.MinuteCacheUpdated = existing.MinuteCacheUpdated
		state.Frozen = existing.Frozen
	}
	if state.SignalTime == nil || state.SignalTime.IsZero() {
		if !aggr.SignalTime.IsZero() {
			t := aggr.SignalTime
			state.SignalTime = &t
		}
	}
	state.RecommendCategory = ""
	if !aggr.SignalTime.IsZero() {
		t := aggr.SignalTime
		state.SignalTime = &t
		state.RecommendTime = &t
	}
	if strings.TrimSpace(state.ActivationStatus) == "" {
		state.ActivationStatus = "pending"
	}
	buyTime := aggr.BuyTime
	if state.BuyTime != nil && !state.BuyTime.IsZero() {
		buyTime = *state.BuyTime
	}
	sellFloorTime := time.Time{}
	if !buyTime.IsZero() {
		sellFloorTime = resolveNextSellEligibleTime(buyTime)
	}
	sanitizeYieldSellSnapshot(sellFloorTime, &state.PositionStatus, &state.SellTime, &state.RealizedSellAmount, &state.Frozen)

	if p, ok := ctx.CurrentPriceMap[aggr.StockCode]; ok {
		state.CurrentPrice = round2(p)
	}
	if pTime, ok := ctx.CurrentPriceTimeMap[aggr.StockCode]; ok {
		state.CurrentPriceTime = pTime
	}

	manualBackfill := ctx.Reason == "manual_minute_download"
	frozenSold := state.Frozen && isSoldPositionStatus(state.PositionStatus)

	if !manualBackfill && !shouldUpdateActiveStock(existing, ctx.Force, ctx.InTradingSession, ctx.LatestTradeDate, ctx.Now) {
		fillYieldMetrics(&state)
		return state
	}

	prevPositionStatus := state.PositionStatus
	var prevSellTime *time.Time
	if state.SellTime != nil {
		t := *state.SellTime
		prevSellTime = &t
	}
	var prevRealizedSellAmount *float64
	if state.RealizedSellAmount != nil {
		v := *state.RealizedSellAmount
		prevRealizedSellAmount = &v
	}

	recalcAt := ctx.Now
	state.LastRecalcAt = &recalcAt
	state.PositionStatus = "待激活"
	state.SellTime = nil
	state.RealizedSellAmount = nil
	state.Frozen = false
	state.BuyTime = nil
	state.BuyAmount = 0
	state.ActivationTime = nil
	state.ActivationPrice = 0
	state.ActivationStatus = "pending"

	if !isAShareTsCode(aggr.StockCode) {
		state.DataStatus = "无法判定"
		state.DataStatusReason = "非A股"
		fillYieldMetrics(&state)
		return state
	}

	if state.StopProfitAmount == nil && state.StopLossAmount == nil {
		state.DataStatus = "无法判定"
		state.DataStatusReason = "缺少止盈止损"
		fillYieldMetrics(&state)
		return state
	}

	activationTime, activationPrice, activationInfo := resolveAggregateActivation(aggr, ctx, manualBackfill)
	if activationInfo.LastMinuteTs != nil {
		state.LastMinuteTs = activationInfo.LastMinuteTs
	}
	state.MinuteCacheStart = activationInfo.CacheStart
	state.MinuteCacheEnd = activationInfo.CacheEnd
	if activationInfo.CacheSource != "" {
		state.MinuteCacheSource = activationInfo.CacheSource
	}
	if activationInfo.CacheUpdated != nil {
		state.MinuteCacheUpdated = activationInfo.CacheUpdated
	}
	state.DataStatus = activationInfo.DataStatus
	state.DataStatusReason = activationInfo.DataStatusReason

	if activationTime == nil || activationTime.IsZero() || activationPrice <= 0 {
		if state.DataStatus == "已跳过" {
			state.ActivationStatus = "skipped"
			state.PositionStatus = "已放弃"
			fillYieldMetrics(&state)
			return state
		}
		if state.DataStatus == "正常" {
			state.DataStatus = "待激活"
			state.DataStatusReason = "未触发主买入区"
		}
		fillYieldMetrics(&state)
		return state
	}

	state.ActivationStatus = "activated"
	state.ActivationTime = activationTime
	state.ActivationPrice = round2(activationPrice)
	state.BuyTime = activationTime
	state.BuyAmount = round2(activationPrice)
	state.TotalScopeStart = activationTime.Format("2006-01-02")
	state.PositionStatus = "持有"

	sellFloorTime = resolveNextSellEligibleTime(*activationTime)
	scanStart := sellFloorTime
	manualFullCheck := manualBackfill
	if !manualFullCheck && !frozenSold && existing != nil && existing.LastMinuteTs != nil && existing.LastMinuteTs.After(scanStart) {
		scanStart = existing.LastMinuteTs.Add(time.Minute)
	}
	scanEnd := normalizeMinuteCoverageEnd(resolveMinuteEvalEnd(ctx.Now, ctx.InTradingSession, ctx.LatestTradeDate))
	if manualBackfill {
		if ctx.InTradingSession {
			scanEnd = normalizeMinuteCoverageEnd(ctx.Now)
		} else {
			scanEnd = resolveLatestCloseEvalEnd(ctx.Now, ctx.LatestTradeDate)
		}
	}

	triggerStatus, triggerTime, triggerPrice, evalInfo := evaluatePositionWithMinuteAndDaily(
		aggr.StockCode,
		scanStart,
		scanEnd,
		state.StopProfitAmount,
		state.StopLossAmount,
		ctx.Tushare,
		ctx.CrawlTimeout,
		manualBackfill,
	)

	if evalInfo.LastMinuteTs != nil {
		state.LastMinuteTs = evalInfo.LastMinuteTs
	}
	state.MinuteCacheStart = evalInfo.CacheStart
	state.MinuteCacheEnd = evalInfo.CacheEnd
	if evalInfo.CacheSource != "" {
		state.MinuteCacheSource = evalInfo.CacheSource
	}
	if evalInfo.CacheUpdated != nil {
		state.MinuteCacheUpdated = evalInfo.CacheUpdated
	}
	state.DataStatus = evalInfo.DataStatus
	state.DataStatusReason = evalInfo.DataStatusReason

	if triggerStatus != "" {
		if !sellFloorTime.IsZero() && triggerTime.Before(sellFloorTime) {
			logger.SugaredLogger.Warnf("ignore invalid aggregate sell trigger before sell-eligible time: code=%s buy=%s sell_floor=%s sell=%s status=%s", aggr.StockCode, activationTime.In(cnLocation()).Format("2006-01-02 15:04:05"), sellFloorTime.In(cnLocation()).Format("2006-01-02 15:04:05"), triggerTime.In(cnLocation()).Format("2006-01-02 15:04:05"), triggerStatus)
		} else {
			state.Frozen = true
			state.PositionStatus = triggerStatus
			t := triggerTime
			state.SellTime = &t
			p := round2(triggerPrice)
			state.RealizedSellAmount = &p
		}
	} else if manualBackfill && frozenSold {
		state.Frozen = true
		state.PositionStatus = prevPositionStatus
		state.SellTime = prevSellTime
		state.RealizedSellAmount = prevRealizedSellAmount
	}

	fillYieldMetrics(&state)
	return state
}

func buildYieldRecordStateFromRecommend(rec models.AiRecommendStocks, existing *models.AiRecommendYieldRecordState, ctx yieldBuildContext) models.AiRecommendYieldRecordState {
	recordTime := recommendRecordTime(rec)
	code := normalizeRecommendStockCode(rec.StockCode)
	state := models.AiRecommendYieldRecordState{
		RecommendID:       rec.ID,
		StockCode:         code,
		StockName:         strings.TrimSpace(rec.StockName),
		ModelName:         strings.TrimSpace(rec.ModelName),
		BkName:            strings.TrimSpace(rec.BkName),
		RecommendCategory: strings.TrimSpace(rec.RecommendCategory),
		PositionStatus:    "待激活",
		YieldRateText:     "--",
		DataStatus:        "正常",
		TotalScopeEnd:     ctx.Now.Format("2006-01-02"),
	}
	if !recordTime.IsZero() {
		t := recordTime
		state.RecommendTime = &t
		state.SignalTime = &t
		state.TotalScopeStart = t.Format("2006-01-02")
	}
	state.ActivationStatus = "pending"

	if v, ok := parseStopProfitPrice(rec); ok {
		state.StopProfitAmount = &v
	}
	if v, ok := parseStopLossPrice(rec); ok {
		state.StopLossAmount = &v
	}
	state.SellAmountText = buildSellAmountText(state.StopProfitAmount, state.StopLossAmount)

	if existing != nil {
		state.ID = existing.ID
		state.CreatedAt = existing.CreatedAt
		state.SignalTime = existing.SignalTime
		state.ActivationStatus = existing.ActivationStatus
		state.ActivationTime = existing.ActivationTime
		state.ActivationPrice = existing.ActivationPrice
		state.BuyTime = existing.BuyTime
		state.BuyAmount = existing.BuyAmount
		state.SellTime = existing.SellTime
		state.RealizedSellAmount = existing.RealizedSellAmount
		state.PositionStatus = existing.PositionStatus
		state.CurrentPrice = existing.CurrentPrice
		state.CurrentPriceTime = existing.CurrentPriceTime
		state.YieldRate = existing.YieldRate
		state.YieldRateText = existing.YieldRateText
		state.DataStatus = existing.DataStatus
		state.DataStatusReason = existing.DataStatusReason
		state.LastMinuteTs = existing.LastMinuteTs
		state.LastRecalcAt = existing.LastRecalcAt
		state.MinuteCacheStart = existing.MinuteCacheStart
		state.MinuteCacheEnd = existing.MinuteCacheEnd
		state.MinuteCacheSource = existing.MinuteCacheSource
		state.MinuteCacheUpdated = existing.MinuteCacheUpdated
		state.Frozen = existing.Frozen
	}
	if state.SignalTime == nil || state.SignalTime.IsZero() {
		if !recordTime.IsZero() {
			t := recordTime
			state.SignalTime = &t
		}
	}
	state.RecommendCategory = strings.TrimSpace(rec.RecommendCategory)
	if !recordTime.IsZero() {
		t := recordTime
		state.SignalTime = &t
		state.RecommendTime = &t
	}
	if strings.TrimSpace(state.ActivationStatus) == "" {
		state.ActivationStatus = "pending"
	}
	sellFloorTime := time.Time{}
	if state.BuyTime != nil && !state.BuyTime.IsZero() {
		sellFloorTime = resolveNextSellEligibleTime(*state.BuyTime)
		state.TotalScopeStart = state.BuyTime.Format("2006-01-02")
	}
	sanitizeYieldSellSnapshot(sellFloorTime, &state.PositionStatus, &state.SellTime, &state.RealizedSellAmount, &state.Frozen)

	if p, ok := ctx.CurrentPriceMap[code]; ok {
		state.CurrentPrice = round2(p)
	}
	if pTime, ok := ctx.CurrentPriceTimeMap[code]; ok {
		state.CurrentPriceTime = pTime
	}
	if state.CurrentPrice <= 0 {
		if p, ok := parseBuyPrice(rec.StockCurrentPrice); ok {
			state.CurrentPrice = round2(p)
		}
	}
	if strings.TrimSpace(state.CurrentPriceTime) == "" {
		state.CurrentPriceTime = strings.TrimSpace(rec.StockCurrentPriceTime)
	}

	if eligibility, reason := resolveRecommendBacktestEligibility(&rec); eligibility != recommendBacktestEligible {
		recalcAt := ctx.Now
		state.LastRecalcAt = &recalcAt
		switch eligibility {
		case recommendBacktestSkipped:
			activationStatus, positionStatus, dataStatus, _, _ := resolveRecommendYieldSkipInfo(&rec)
			state.ActivationStatus = activationStatus
			state.PositionStatus = positionStatus
			state.DataStatus = dataStatus
		default:
			state.ActivationStatus = "ineligible"
			state.PositionStatus = "未纳入回测"
			state.DataStatus = "未结构化"
		}
		state.ActivationTime = nil
		state.ActivationPrice = 0
		state.BuyTime = nil
		state.BuyAmount = 0
		state.SellTime = nil
		state.RealizedSellAmount = nil
		state.Frozen = false
		state.DataStatusReason = reason
		state.TotalScopeStart = ""
		fillYieldRecordMetrics(&state)
		return state
	}

	manualBackfill := ctx.Reason == "manual_minute_download"
	frozenSold := state.Frozen && isSoldPositionStatus(state.PositionStatus)

	if !manualBackfill && !shouldUpdateActiveRecord(existing, ctx.Force, ctx.InTradingSession, ctx.LatestTradeDate, ctx.Now) {
		fillYieldRecordMetrics(&state)
		return state
	}

	prevPositionStatus := state.PositionStatus
	var prevSellTime *time.Time
	if state.SellTime != nil {
		t := *state.SellTime
		prevSellTime = &t
	}
	var prevRealizedSellAmount *float64
	if state.RealizedSellAmount != nil {
		v := *state.RealizedSellAmount
		prevRealizedSellAmount = &v
	}

	recalcAt := ctx.Now
	state.LastRecalcAt = &recalcAt
	state.PositionStatus = "待激活"
	state.SellTime = nil
	state.RealizedSellAmount = nil
	state.Frozen = false
	state.BuyTime = nil
	state.BuyAmount = 0
	state.ActivationTime = nil
	state.ActivationPrice = 0
	state.ActivationStatus = "pending"

	if !isAShareTsCode(code) {
		state.DataStatus = "无法判定"
		state.DataStatusReason = "非A股"
		fillYieldRecordMetrics(&state)
		return state
	}

	if state.StopProfitAmount == nil && state.StopLossAmount == nil {
		state.DataStatus = "无法判定"
		state.DataStatusReason = "缺少止盈止损"
		fillYieldRecordMetrics(&state)
		return state
	}

	activationTime, activationPrice, activationInfo := resolveRecommendActivation(rec, ctx, manualBackfill)
	if activationInfo.LastMinuteTs != nil {
		state.LastMinuteTs = activationInfo.LastMinuteTs
	}
	state.MinuteCacheStart = activationInfo.CacheStart
	state.MinuteCacheEnd = activationInfo.CacheEnd
	if activationInfo.CacheSource != "" {
		state.MinuteCacheSource = activationInfo.CacheSource
	}
	if activationInfo.CacheUpdated != nil {
		state.MinuteCacheUpdated = activationInfo.CacheUpdated
	}
	state.DataStatus = activationInfo.DataStatus
	state.DataStatusReason = activationInfo.DataStatusReason

	if activationTime == nil || activationTime.IsZero() || activationPrice <= 0 {
		if state.DataStatus == "已跳过" {
			state.ActivationStatus = "skipped"
			state.PositionStatus = "已放弃"
			fillYieldRecordMetrics(&state)
			return state
		}
		if state.DataStatus == "正常" {
			state.DataStatus = "待激活"
			state.DataStatusReason = "未触发主买入区"
		}
		fillYieldRecordMetrics(&state)
		return state
	}

	state.ActivationStatus = "activated"
	state.ActivationTime = activationTime
	state.ActivationPrice = round2(activationPrice)
	state.BuyTime = activationTime
	state.BuyAmount = round2(activationPrice)
	state.TotalScopeStart = activationTime.Format("2006-01-02")
	state.PositionStatus = "持有"

	sellFloorTime = resolveNextSellEligibleTime(*activationTime)
	scanStart := sellFloorTime
	if !manualBackfill && !frozenSold && existing != nil && existing.LastMinuteTs != nil && existing.LastMinuteTs.After(scanStart) {
		scanStart = existing.LastMinuteTs.Add(time.Minute)
	}
	scanEnd := normalizeMinuteCoverageEnd(resolveMinuteEvalEnd(ctx.Now, ctx.InTradingSession, ctx.LatestTradeDate))
	if manualBackfill {
		if ctx.InTradingSession {
			scanEnd = normalizeMinuteCoverageEnd(ctx.Now)
		} else {
			scanEnd = resolveLatestCloseEvalEnd(ctx.Now, ctx.LatestTradeDate)
		}
	}

	triggerStatus, triggerTime, triggerPrice, evalInfo := evaluatePositionWithMinuteAndDaily(
		code,
		scanStart,
		scanEnd,
		state.StopProfitAmount,
		state.StopLossAmount,
		ctx.Tushare,
		ctx.CrawlTimeout,
		manualBackfill,
	)

	if evalInfo.LastMinuteTs != nil {
		state.LastMinuteTs = evalInfo.LastMinuteTs
	}
	state.MinuteCacheStart = evalInfo.CacheStart
	state.MinuteCacheEnd = evalInfo.CacheEnd
	if evalInfo.CacheSource != "" {
		state.MinuteCacheSource = evalInfo.CacheSource
	}
	if evalInfo.CacheUpdated != nil {
		state.MinuteCacheUpdated = evalInfo.CacheUpdated
	}
	state.DataStatus = evalInfo.DataStatus
	state.DataStatusReason = evalInfo.DataStatusReason

	if triggerStatus != "" {
		if !sellFloorTime.IsZero() && triggerTime.Before(sellFloorTime) {
			logger.SugaredLogger.Warnf("ignore invalid record sell trigger before sell-eligible time: code=%s recommend_id=%d buy=%s sell_floor=%s sell=%s status=%s", code, rec.ID, activationTime.In(cnLocation()).Format("2006-01-02 15:04:05"), sellFloorTime.In(cnLocation()).Format("2006-01-02 15:04:05"), triggerTime.In(cnLocation()).Format("2006-01-02 15:04:05"), triggerStatus)
		} else {
			state.Frozen = true
			state.PositionStatus = triggerStatus
			t := triggerTime
			state.SellTime = &t
			p := round2(triggerPrice)
			state.RealizedSellAmount = &p
		}
	} else if manualBackfill && frozenSold {
		state.Frozen = true
		state.PositionStatus = prevPositionStatus
		state.SellTime = prevSellTime
		state.RealizedSellAmount = prevRealizedSellAmount
	}

	fillYieldRecordMetrics(&state)
	return state
}

func parseRecommendEntryRange(rec models.AiRecommendStocks) (float64, float64, bool) {
	_, min, max, ok := resolveRecommendBuyRange(rec)
	if !ok || min <= 0 || max <= 0 {
		return 0, 0, false
	}
	if min > max {
		min, max = max, min
	}
	return min, max, true
}

func scanActivationFromBars(bars []minuteBar, minPrice, maxPrice float64) (time.Time, float64, bool) {
	if len(bars) == 0 || minPrice <= 0 || maxPrice <= 0 {
		return time.Time{}, 0, false
	}
	for _, bar := range bars {
		if activationTime, activationPrice, ok := resolveActivationCandidateFromBar(bar, minPrice, maxPrice); ok {
			return activationTime, activationPrice, true
		}
	}
	return time.Time{}, 0, false
}

func resolveActivationCandidateFromBar(bar minuteBar, minPrice, maxPrice float64) (time.Time, float64, bool) {
	if bar.TradeTime.IsZero() {
		return time.Time{}, 0, false
	}
	if bar.Low > maxPrice || bar.High < minPrice {
		return time.Time{}, 0, false
	}
	price := bar.Close
	if price <= 0 {
		price = bar.Open
	}
	if price < minPrice {
		price = minPrice
	}
	if price > maxPrice {
		price = maxPrice
	}
	return bar.TradeTime, round2(price), true
}

func resolveActivationWindow(recordTime time.Time, now time.Time, inTrading bool, latestTradeDate time.Time) (time.Time, time.Time) {
	start := resolveRecommendBuyTime(recordTime)
	end := normalizeMinuteCoverageEnd(resolveMinuteEvalEnd(now, inTrading, latestTradeDate))
	if start.After(end) {
		return start, start
	}
	return start, end
}

func resolveRecommendActivation(rec models.AiRecommendStocks, ctx yieldBuildContext, allowHeadBackfill bool) (*time.Time, float64, triggerEvalInfo) {
	info := triggerEvalInfo{DataStatus: "正常", DataStatusReason: ""}
	recordTime := recommendRecordTime(rec)
	if recordTime.IsZero() {
		info.DataStatus = "无法判定"
		info.DataStatusReason = "缺少推荐时间"
		return nil, 0, info
	}
	if normalizeRecommendCategory(rec.RecommendCategory) == "avoid" {
		info.DataStatus = "回避"
		info.DataStatusReason = "回避标的不参与收益率"
		return nil, 0, info
	}
	minPrice, maxPrice, ok := parseRecommendEntryRange(rec)
	if !ok {
		info.DataStatus = "无法判定"
		info.DataStatusReason = "主买入区无法解析"
		return nil, 0, info
	}
	start, end := resolveActivationWindow(recordTime, ctx.Now, ctx.InTradingSession, ctx.LatestTradeDate)
	if !start.Before(end) {
		info.DataStatus = "待激活"
		info.DataStatusReason = "主买入区尚未进入可扫描窗口"
		return nil, 0, info
	}
	bars, cacheInfo := syncMinuteBars(normalizeRecommendStockCode(rec.StockCode), start, end, ctx.CrawlTimeout, allowHeadBackfill)
	info.CacheStart = cacheInfo.CacheStart
	info.CacheEnd = cacheInfo.CacheEnd
	info.CacheUpdated = cacheInfo.CacheUpdated
	info.CacheSource = cacheInfo.CacheSource
	info.LastMinuteTs = cacheInfo.LastMinuteTs
	activationTime, activationPrice, ok, activityReason, activitySync := scanActivationFromBarsWithActivityFilter(
		normalizeRecommendStockCode(rec.StockCode),
		bars,
		minPrice,
		maxPrice,
		recommendRequiresPrevDayActivityFilter(rec),
		ctx,
		allowHeadBackfill,
	)
	mergeTriggerEvalInfoCache(&info, activitySync)
	if ok {
		t := activationTime
		info.ActivationTime = &t
		info.ActivationPrice = activationPrice
		return &t, activationPrice, info
	}
	if skipReason, skip := resolvePendingRecommendInvalidation(rec, recordTime, end, bars, cacheInfo.CoverageOK); skip {
		info.DataStatus = "已跳过"
		info.DataStatusReason = skipReason
		return nil, 0, info
	}
	if strings.TrimSpace(activityReason) != "" {
		info.DataStatus = "待激活"
		info.DataStatusReason = activityReason
		return nil, 0, info
	}
	if cacheInfo.SyncErr != nil {
		info.DataStatus = "无法判定"
		info.DataStatusReason = "主买入区扫描失败；" + strings.TrimSpace(cacheInfo.SyncErr.Error())
		return nil, 0, info
	}
	if len(bars) == 0 {
		info.DataStatus = "待激活"
		info.DataStatusReason = "分钟线不可用或尚未覆盖主买入区"
		return nil, 0, info
	}
	info.DataStatus = "待激活"
	info.DataStatusReason = "未触发主买入区"
	return nil, 0, info
}

func resolvePendingRecommendInvalidation(rec models.AiRecommendStocks, recordTime, evalEnd time.Time, bars []minuteBar, coverageOK bool) (string, bool) {
	if !coverageOK {
		return "", false
	}
	if stopLoss, ok := parseStopLossPrice(rec); ok && stopLoss > 0 {
		if triggerTime, triggerPrice, hit := scanPendingStopLossInvalidationFromBars(bars, stopLoss); hit {
			reason := fmt.Sprintf(
				"激活前已跌破止损/失效位 %.2f（%s，触发价 %.2f）",
				round2(stopLoss),
				triggerTime.In(cnLocation()).Format("2006-01-02 15:04:05"),
				round2(triggerPrice),
			)
			return appendRecommendInvalidConditionText(reason, rec.InvalidCondition), true
		}
	}
	if expiryTime, effectiveCycle, ok := resolveRecommendPendingActivationExpiry(recordTime, rec.ExpectedCycle); ok && !evalEnd.Before(expiryTime) {
		rawCycle := strings.TrimSpace(rec.ExpectedCycle)
		reason := ""
		switch {
		case rawCycle != "" && rawCycle != effectiveCycle:
			reason = fmt.Sprintf(
				"超过待激活有效期 %s（原预期周期 %s）仍未触发主买入区（截止 %s）",
				effectiveCycle,
				rawCycle,
				expiryTime.In(cnLocation()).Format("2006-01-02 15:04:05"),
			)
		case effectiveCycle != "":
			reason = fmt.Sprintf(
				"超过待激活有效期 %s 仍未触发主买入区（截止 %s）",
				effectiveCycle,
				expiryTime.In(cnLocation()).Format("2006-01-02 15:04:05"),
			)
		default:
			reason = fmt.Sprintf(
				"超过待激活有效期仍未触发主买入区（截止 %s）",
				expiryTime.In(cnLocation()).Format("2006-01-02 15:04:05"),
			)
		}
		return appendRecommendInvalidConditionText(reason, rec.InvalidCondition), true
	}
	return "", false
}

func scanPendingStopLossInvalidationFromBars(bars []minuteBar, stopLoss float64) (time.Time, float64, bool) {
	if len(bars) == 0 || stopLoss <= 0 {
		return time.Time{}, 0, false
	}
	for _, bar := range bars {
		if bar.TradeTime.IsZero() {
			continue
		}
		if bar.Open > 0 && bar.Open <= stopLoss {
			return bar.TradeTime, bar.Open, true
		}
		if bar.Low > 0 && bar.Low <= stopLoss {
			return bar.TradeTime, stopLoss, true
		}
	}
	return time.Time{}, 0, false
}

func parseExpectedCycleTradeDays(expectedCycle string) (int, bool) {
	text := strings.TrimSpace(strings.ToLower(expectedCycle))
	if text == "" {
		return 0, false
	}
	maxValue, ok := parsePriceMaxFromText(text)
	if !ok || maxValue <= 0 {
		return 0, false
	}

	multiplier := 1.0
	switch {
	case strings.Contains(text, "月"):
		multiplier = 21
	case strings.Contains(text, "周"):
		multiplier = 5
	case strings.Contains(text, "交易日"), strings.Contains(text, "个交易日"), strings.Contains(text, "天"), strings.Contains(text, "日"):
		multiplier = 1
	default:
		return 0, false
	}

	days := int(maxValue * multiplier)
	if float64(days) < maxValue*multiplier {
		days++
	}
	if days <= 0 {
		return 0, false
	}
	return days, true
}

func resolveRecommendTradeDayExpiry(recordTime time.Time, tradeDays int) (time.Time, bool) {
	if tradeDays <= 0 {
		return time.Time{}, false
	}
	start := resolveRecommendBuyTime(recordTime)
	if start.IsZero() {
		return time.Time{}, false
	}
	loc := cnLocation()
	day := time.Date(start.In(loc).Year(), start.In(loc).Month(), start.In(loc).Day(), 0, 0, 0, 0, loc)
	if !isCNOpenTradeDaySafe(day) {
		day = shiftToNextCNOpenTradeDaySafe(day)
	}
	for i := 1; i < tradeDays; i++ {
		day = shiftToNextCNOpenTradeDaySafe(day.AddDate(0, 0, 1))
	}
	return time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, loc), true
}

func resolveRecommendExpectedCycleExpiry(recordTime time.Time, expectedCycle string) (time.Time, bool) {
	tradeDays, ok := parseExpectedCycleTradeDays(expectedCycle)
	if !ok || tradeDays <= 0 {
		return time.Time{}, false
	}
	return resolveRecommendTradeDayExpiry(recordTime, tradeDays)
}

func resolveRecommendPendingActivationExpiry(recordTime time.Time, expectedCycle string) (time.Time, string, bool) {
	rawLabel := strings.TrimSpace(expectedCycle)
	tradeDays, ok := parseExpectedCycleTradeDays(expectedCycle)
	label := rawLabel
	if !ok || tradeDays <= 0 {
		tradeDays = recommendPendingActivationMaxTradeDays
		label = fmt.Sprintf("%d个交易日", tradeDays)
	}
	if tradeDays > recommendPendingActivationMaxTradeDays {
		tradeDays = recommendPendingActivationMaxTradeDays
		label = fmt.Sprintf("%d个交易日", tradeDays)
	}
	expiry, ok := resolveRecommendTradeDayExpiry(recordTime, tradeDays)
	if !ok {
		return time.Time{}, "", false
	}
	return expiry, label, true
}

func resolveAggregateActivation(aggr *aiRecommendYieldAggregate, ctx yieldBuildContext, allowHeadBackfill bool) (*time.Time, float64, triggerEvalInfo) {
	info := triggerEvalInfo{DataStatus: "正常", DataStatusReason: ""}
	if aggr == nil || aggr.SignalTime.IsZero() {
		info.DataStatus = "无法判定"
		info.DataStatusReason = "缺少推荐时间"
		return nil, 0, info
	}
	if aggr.BuyAmountCount <= 0 {
		info.DataStatus = "无法判定"
		info.DataStatusReason = "缺少主买入区"
		return nil, 0, info
	}
	minPrice := calculateAvg(aggr.BuyAmountSum, aggr.BuyAmountCount)
	if minPrice <= 0 {
		info.DataStatus = "无法判定"
		info.DataStatusReason = "主买入区无效"
		return nil, 0, info
	}
	start, end := resolveActivationWindow(aggr.SignalTime, ctx.Now, ctx.InTradingSession, ctx.LatestTradeDate)
	if !start.Before(end) {
		info.DataStatus = "待激活"
		info.DataStatusReason = "主买入区尚未进入可扫描窗口"
		return nil, 0, info
	}
	bars, cacheInfo := syncMinuteBars(aggr.StockCode, start, end, ctx.CrawlTimeout, allowHeadBackfill)
	info.CacheStart = cacheInfo.CacheStart
	info.CacheEnd = cacheInfo.CacheEnd
	info.CacheUpdated = cacheInfo.CacheUpdated
	info.CacheSource = cacheInfo.CacheSource
	info.LastMinuteTs = cacheInfo.LastMinuteTs
	activationTime, activationPrice, ok, activityReason, activitySync := scanActivationFromBarsWithActivityFilter(
		aggr.StockCode,
		bars,
		minPrice,
		minPrice,
		aggr.RequirePrevDayActivityFilter,
		ctx,
		allowHeadBackfill,
	)
	mergeTriggerEvalInfoCache(&info, activitySync)
	if ok {
		t := activationTime
		info.ActivationTime = &t
		info.ActivationPrice = activationPrice
		return &t, activationPrice, info
	}
	if strings.TrimSpace(activityReason) != "" {
		info.DataStatus = "待激活"
		info.DataStatusReason = activityReason
		return nil, 0, info
	}
	if cacheInfo.SyncErr != nil {
		info.DataStatus = "无法判定"
		info.DataStatusReason = "主买入区扫描失败；" + strings.TrimSpace(cacheInfo.SyncErr.Error())
		return nil, 0, info
	}
	if len(bars) == 0 {
		info.DataStatus = "待激活"
		info.DataStatusReason = "分钟线不可用或尚未覆盖主买入区"
		return nil, 0, info
	}
	info.DataStatus = "待激活"
	info.DataStatusReason = "未触发主买入区"
	return nil, 0, info
}

type triggerEvalInfo struct {
	DataStatus       string
	DataStatusReason string
	LastMinuteTs     *time.Time
	CacheStart       *time.Time
	CacheEnd         *time.Time
	CacheUpdated     *time.Time
	CacheSource      string
	ActivationTime   *time.Time
	ActivationPrice  float64
}

type activitySessionSnapshot struct {
	Bars       []minuteBar
	SyncInfo   minuteSyncInfo
	FetchedEnd time.Time
}

type minuteActivityWindow struct {
	Count      int
	AmountSum  float64
	VolumeSum  float64
	Start      time.Time
	End        time.Time
	MetricName string
}

func recommendRequiresPrevDayActivityFilter(rec models.AiRecommendStocks) bool {
	texts := []string{rec.BuySignal, rec.BuySignalDetail, rec.Remarks}
	for _, text := range texts {
		normalized := strings.TrimSpace(text)
		if normalized == "" {
			continue
		}
		hasPrevDay := containsAnyKeyword(normalized, []string{"上一交易日", "前一交易日", "上个交易日", "较前一日", "较上一交易日"})
		hasActivity := containsAnyKeyword(normalized, []string{"活跃度", "量能", "成交额", "成交量", "量比"})
		if hasPrevDay && hasActivity {
			return true
		}
	}
	return false
}

func scanActivationFromBarsWithActivityFilter(
	tsCode string,
	bars []minuteBar,
	minPrice, maxPrice float64,
	requireActivity bool,
	ctx yieldBuildContext,
	allowHeadBackfill bool,
) (time.Time, float64, bool, string, minuteSyncInfo) {
	if !requireActivity {
		when, price, ok := scanActivationFromBars(bars, minPrice, maxPrice)
		return when, price, ok, "", minuteSyncInfo{}
	}
	sessionCache := map[string]*activitySessionSnapshot{}
	mergedSync := minuteSyncInfo{}
	lastReason := ""
	for _, bar := range bars {
		activationTime, activationPrice, ok := resolveActivationCandidateFromBar(bar, minPrice, maxPrice)
		if !ok {
			continue
		}
		passed, reason, syncInfo := validatePrevDayActivityForActivation(tsCode, activationTime, ctx, allowHeadBackfill, sessionCache)
		mergedSync = mergeMinuteSyncInfo(mergedSync, syncInfo)
		if passed {
			return activationTime, activationPrice, true, "", mergedSync
		}
		if strings.TrimSpace(reason) != "" {
			lastReason = reason
		}
	}
	return time.Time{}, 0, false, lastReason, mergedSync
}

func validatePrevDayActivityForActivation(
	tsCode string,
	triggerTime time.Time,
	ctx yieldBuildContext,
	allowHeadBackfill bool,
	sessionCache map[string]*activitySessionSnapshot,
) (bool, string, minuteSyncInfo) {
	currentBars, currentSync, ok := loadSessionBarsForActivity(tsCode, triggerTime, ctx, allowHeadBackfill, sessionCache)
	if !ok {
		reason := "已进入主买入区，但当前5分钟活跃度缺失"
		if currentSync.SyncErr != nil {
			reason = reason + "；" + strings.TrimSpace(currentSync.SyncErr.Error())
		}
		return false, reason, currentSync
	}
	currentWindow := buildRecentActivityWindow(currentBars, triggerTime, 5)
	if currentWindow.Count <= 0 {
		reason := "已进入主买入区，但当前5分钟活跃度缺失"
		if currentSync.SyncErr != nil {
			reason = reason + "；" + strings.TrimSpace(currentSync.SyncErr.Error())
		}
		return false, reason, currentSync
	}

	prevTriggerTime := previousTradingMoment(triggerTime)
	prevBars, prevSync, ok := loadSessionBarsForActivity(tsCode, prevTriggerTime, ctx, allowHeadBackfill, sessionCache)
	mergedSync := mergeMinuteSyncInfo(currentSync, prevSync)
	if !ok {
		reason := "已进入主买入区，但缺少上一交易日活跃度基准"
		if prevSync.SyncErr != nil {
			reason = reason + "；" + strings.TrimSpace(prevSync.SyncErr.Error())
		}
		return false, reason, mergedSync
	}
	prevWindow := buildRecentActivityWindow(prevBars, prevTriggerTime, currentWindow.Count)
	if prevWindow.Count < currentWindow.Count {
		reason := "已进入主买入区，但缺少上一交易日活跃度基准"
		if prevSync.SyncErr != nil {
			reason = reason + "；" + strings.TrimSpace(prevSync.SyncErr.Error())
		}
		return false, reason, mergedSync
	}

	if currentWindow.AmountSum > 0 && prevWindow.AmountSum > 0 {
		if currentWindow.AmountSum >= prevWindow.AmountSum {
			return true, "", mergedSync
		}
		return false, fmt.Sprintf(
			"已进入主买入区，但5分钟成交额 %.2f 低于上一交易日同一时刻 %.2f",
			round2(currentWindow.AmountSum),
			round2(prevWindow.AmountSum),
		), mergedSync
	}

	if currentWindow.VolumeSum > 0 && prevWindow.VolumeSum > 0 {
		if currentWindow.VolumeSum >= prevWindow.VolumeSum {
			return true, "", mergedSync
		}
		return false, fmt.Sprintf(
			"已进入主买入区，但5分钟成交量 %.2f 低于上一交易日同一时刻 %.2f",
			round2(currentWindow.VolumeSum),
			round2(prevWindow.VolumeSum),
		), mergedSync
	}

	if prevWindow.AmountSum <= 0 && prevWindow.VolumeSum <= 0 {
		reason := "已进入主买入区，但缺少上一交易日活跃度基准"
		if prevSync.SyncErr != nil {
			reason = reason + "；" + strings.TrimSpace(prevSync.SyncErr.Error())
		}
		return false, reason, mergedSync
	}

	reason := "已进入主买入区，但当前5分钟活跃度缺失"
	if currentSync.SyncErr != nil {
		reason = reason + "；" + strings.TrimSpace(currentSync.SyncErr.Error())
	}
	return false, reason, mergedSync
}

func loadSessionBarsForActivity(
	tsCode string,
	endTime time.Time,
	ctx yieldBuildContext,
	allowHeadBackfill bool,
	sessionCache map[string]*activitySessionSnapshot,
) ([]minuteBar, minuteSyncInfo, bool) {
	sessionStart := resolveActivitySessionStart(endTime)
	if sessionStart.IsZero() {
		return nil, minuteSyncInfo{}, false
	}
	cacheKey := buildActivitySessionCacheKey(tsCode, endTime)
	if snapshot, ok := sessionCache[cacheKey]; ok && !snapshot.FetchedEnd.Before(normalizeMinuteTime(endTime)) {
		return snapshot.Bars, snapshot.SyncInfo, len(snapshot.Bars) > 0
	}
	bars, syncInfo := syncMinuteBars(tsCode, sessionStart, normalizeMinuteTime(endTime), ctx.CrawlTimeout, allowHeadBackfill)
	sessionCache[cacheKey] = &activitySessionSnapshot{
		Bars:       bars,
		SyncInfo:   syncInfo,
		FetchedEnd: normalizeMinuteTime(endTime),
	}
	return bars, syncInfo, len(bars) > 0
}

func resolveActivitySessionStart(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	loc := t.Location()
	if loc == nil {
		loc = cnLocation()
	}
	morningClose := time.Date(t.Year(), t.Month(), t.Day(), 11, 30, 0, 0, loc)
	afternoonOpen := time.Date(t.Year(), t.Month(), t.Day(), 13, 1, 0, 0, loc)
	switch {
	case !t.After(morningClose):
		return time.Date(t.Year(), t.Month(), t.Day(), 9, 31, 0, 0, loc)
	case !t.Before(afternoonOpen):
		return afternoonOpen
	default:
		return time.Time{}
	}
}

func buildActivitySessionCacheKey(tsCode string, t time.Time) string {
	sessionStart := resolveActivitySessionStart(t)
	if sessionStart.IsZero() {
		return normalizeRecommendStockCode(tsCode)
	}
	return normalizeRecommendStockCode(tsCode) + "|" + sessionStart.Format("2006-01-02 15:04")
}

func buildRecentActivityWindow(bars []minuteBar, endTime time.Time, maxCount int) minuteActivityWindow {
	if len(bars) == 0 || endTime.IsZero() || maxCount <= 0 {
		return minuteActivityWindow{}
	}
	sessionStart := resolveActivitySessionStart(endTime)
	if sessionStart.IsZero() {
		return minuteActivityWindow{}
	}
	endTime = normalizeMinuteTime(endTime)
	selected := make([]minuteBar, 0, maxCount)
	for idx := len(bars) - 1; idx >= 0; idx-- {
		bar := bars[idx]
		if bar.TradeTime.IsZero() {
			continue
		}
		if bar.TradeTime.After(endTime) || bar.TradeTime.Before(sessionStart) {
			continue
		}
		selected = append(selected, bar)
		if len(selected) >= maxCount {
			break
		}
	}
	if len(selected) == 0 {
		return minuteActivityWindow{}
	}
	window := minuteActivityWindow{
		Count: len(selected),
		End:   selected[0].TradeTime,
		Start: selected[len(selected)-1].TradeTime,
	}
	for _, bar := range selected {
		if bar.Amount > 0 {
			window.AmountSum += bar.Amount
		}
		if bar.Volume > 0 {
			window.VolumeSum += bar.Volume
		}
	}
	return window
}

func previousTradingMoment(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	loc := t.Location()
	if loc == nil {
		loc = cnLocation()
	}
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	prevDay := subtractTradingDaysByWeekday(day, 1)
	return time.Date(prevDay.Year(), prevDay.Month(), prevDay.Day(), t.Hour(), t.Minute(), 0, 0, loc)
}

func mergeMinuteSyncInfo(base, current minuteSyncInfo) minuteSyncInfo {
	result := base
	result.SyncErr = mergeSyncErr(base.SyncErr, current.SyncErr)
	if current.LastMinuteTs != nil && (result.LastMinuteTs == nil || result.LastMinuteTs.Before(*current.LastMinuteTs)) {
		t := *current.LastMinuteTs
		result.LastMinuteTs = &t
	}
	if current.CacheStart != nil && (result.CacheStart == nil || result.CacheStart.After(*current.CacheStart)) {
		t := *current.CacheStart
		result.CacheStart = &t
	}
	if current.CacheEnd != nil && (result.CacheEnd == nil || result.CacheEnd.Before(*current.CacheEnd)) {
		t := *current.CacheEnd
		result.CacheEnd = &t
	}
	if current.CacheUpdated != nil && (result.CacheUpdated == nil || result.CacheUpdated.Before(*current.CacheUpdated)) {
		t := *current.CacheUpdated
		result.CacheUpdated = &t
	}
	if strings.TrimSpace(current.CacheSource) != "" {
		result.CacheSource = current.CacheSource
	}
	result.CoverageOK = result.CoverageOK || current.CoverageOK
	return result
}

func mergeTriggerEvalInfoCache(info *triggerEvalInfo, syncInfo minuteSyncInfo) {
	if info == nil {
		return
	}
	if syncInfo.LastMinuteTs != nil && (info.LastMinuteTs == nil || info.LastMinuteTs.Before(*syncInfo.LastMinuteTs)) {
		t := *syncInfo.LastMinuteTs
		info.LastMinuteTs = &t
	}
	if syncInfo.CacheStart != nil && (info.CacheStart == nil || info.CacheStart.After(*syncInfo.CacheStart)) {
		t := *syncInfo.CacheStart
		info.CacheStart = &t
	}
	if syncInfo.CacheEnd != nil && (info.CacheEnd == nil || info.CacheEnd.Before(*syncInfo.CacheEnd)) {
		t := *syncInfo.CacheEnd
		info.CacheEnd = &t
	}
	if syncInfo.CacheUpdated != nil && (info.CacheUpdated == nil || info.CacheUpdated.Before(*syncInfo.CacheUpdated)) {
		t := *syncInfo.CacheUpdated
		info.CacheUpdated = &t
	}
	if strings.TrimSpace(syncInfo.CacheSource) != "" {
		info.CacheSource = syncInfo.CacheSource
	}
}

func evaluatePositionWithMinuteAndDaily(
	tsCode string,
	start, end time.Time,
	stopProfit, stopLoss *float64,
	_ *TushareApi,
	crawlTimeout int64,
	allowHeadBackfill bool,
) (string, time.Time, float64, triggerEvalInfo) {
	info := triggerEvalInfo{DataStatus: "正常", DataStatusReason: ""}
	if !start.Before(end) {
		return "", time.Time{}, 0, info
	}

	bars, cacheInfo := syncMinuteBars(tsCode, start, end, crawlTimeout, allowHeadBackfill)
	info.CacheStart = cacheInfo.CacheStart
	info.CacheEnd = cacheInfo.CacheEnd
	info.CacheUpdated = cacheInfo.CacheUpdated
	info.CacheSource = cacheInfo.CacheSource
	info.LastMinuteTs = cacheInfo.LastMinuteTs

	if len(bars) > 0 {
		status, t, price := scanMinuteTriggerFromBars(bars, stopProfit, stopLoss)
		if status != "" {
			return status, t, price, info
		}
		if cacheInfo.CoverageOK {
			return "", time.Time{}, 0, info
		}
		info.DataStatus = "无法判定"
		if cacheInfo.CacheStart != nil && cacheInfo.CacheEnd != nil {
			info.DataStatusReason = fmt.Sprintf(
				"分钟线覆盖不完整（缓存 %s~%s，目标 %s~%s）",
				cacheInfo.CacheStart.In(cnLocation()).Format("2006-01-02 15:04:05"),
				cacheInfo.CacheEnd.In(cnLocation()).Format("2006-01-02 15:04:05"),
				start.In(cnLocation()).Format("2006-01-02 15:04:05"),
				end.In(cnLocation()).Format("2006-01-02 15:04:05"),
			)
		} else {
			info.DataStatusReason = "分钟线覆盖不完整"
		}
		if cacheInfo.SyncErr != nil {
			info.DataStatusReason = info.DataStatusReason + "；" + strings.TrimSpace(cacheInfo.SyncErr.Error())
		}
		return "", time.Time{}, 0, info
	}

	info.DataStatus = "无法判定"
	if cacheInfo.SyncErr != nil {
		info.DataStatusReason = "分钟线不可用；" + strings.TrimSpace(cacheInfo.SyncErr.Error())
	} else {
		info.DataStatusReason = "分钟线不可用"
	}
	return "", time.Time{}, 0, info
}

type minuteSyncInfo struct {
	SyncErr      error
	LastMinuteTs *time.Time
	CacheStart   *time.Time
	CacheEnd     *time.Time
	CacheUpdated *time.Time
	CacheSource  string
	CoverageOK   bool
}

func syncMinuteBars(tsCode string, start, end time.Time, _ int64, allowHeadBackfill bool) ([]minuteBar, minuteSyncInfo) {
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

	missingWindows := buildMinuteFetchWindows(start, end, cacheStart, cacheEnd, allowHeadBackfill)
	fetchedCount := 0
	for _, window := range missingWindows {
		if window.Start.After(window.End) {
			continue
		}
		fetched, source, fetchErr := fetchMinuteBarsFromProviders(tsCode, window.Start, window.End)
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

func fetchMinuteBarsFromProviders(tsCode string, start, end time.Time) ([]minuteBar, string, error) {
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
	return executeMinuteProviderPlan(tsCode, start, end, hedgedPlan, fallbackPlan)
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

func currentMinuteProviderMode() string {
	settings := minuteProviderSettings()
	if settings == nil {
		return "public"
	}
	return normalizeMinuteProviderMode(settings.MinuteProviderMode)
}

func executeMinuteProviderPlan(tsCode string, start, end time.Time, hedgedPlan []minuteProviderAttempt, fallbackPlan []string) ([]minuteBar, string, error) {
	if len(hedgedPlan) == 0 {
		return []minuteBar{}, "", fmt.Errorf("当前分钟线配置不可用，请检查数据源设置")
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

	for i := 0; i < len(hedgedPlan); i++ {
		res := (<-resultCh).result
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
		bars, source, err := fetchMinuteBarsWithNamedProvider(provider, tsCode, start, end)
		res := buildMinuteProviderResult(provider, bars, source, err, start, end)
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

	return db.Dao.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "stock_code"}},
		DoUpdates: clause.AssignmentColumns(updateColumns),
	}).CreateInBatches(states, 100).Error
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

	return db.Dao.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "recommend_id"}},
		DoUpdates: clause.AssignmentColumns(updateColumns),
	}).CreateInBatches(states, 100).Error
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
	return db.Dao.Model(&models.AiRecommendYieldMeta{}).Where("id = ?", metaID).Updates(map[string]any{
		"recalc_done":     done,
		"recalc_total":    total,
		"recalc_progress": percent,
		"updated_at":      time.Now(),
	}).Error
}

func updateYieldDownloadProgress(metaID uint, done, total int) error {
	percent := calculateRecalcPercent(done, total)
	return db.Dao.Model(&models.AiRecommendYieldMeta{}).Where("id = ?", metaID).Updates(map[string]any{
		"download_done":        done,
		"download_total":       total,
		"download_progress":    percent,
		"download_in_progress": total > 0 && done < total,
		"updated_at":           time.Now(),
	}).Error
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
