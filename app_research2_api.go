package main

import (
	"context"
	"errors"
	"time"

	"go-stock/backend/data"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	"go-stock/backend/research2"
)

func (a *App) ensureResearch2Runtime(configID int) (*data.Research2Runtime, error) {
	if a == nil {
		return nil, errors.New("application is unavailable")
	}
	a.research2RuntimeMu.RLock()
	runtime := a.research2Runtime
	a.research2RuntimeMu.RUnlock()
	if runtime != nil {
		return runtime, nil
	}
	a.research2RuntimeMu.Lock()
	defer a.research2RuntimeMu.Unlock()
	if a.research2Runtime != nil {
		return a.research2Runtime, nil
	}
	if a.research2Factory == nil {
		return nil, errors.New("research2 runtime factory is unavailable")
	}
	created, err := a.research2Factory(configID)
	if err != nil {
		return nil, err
	}
	a.research2Runtime = created
	return created, nil
}

func (a *App) resetResearch2Runtime() {
	a.research2RuntimeMu.Lock()
	a.research2Runtime = nil
	a.research2RuntimeMu.Unlock()
}

func (a *App) reloadResearch2Cron(setting *models.SettingConfig) {
	for _, key := range []string{research2AnalysisEntryKey, research2TradingEntryKey, research2MetricsEntryKey} {
		if entry, exists := a.getCronEntry(key); exists {
			a.cron.Remove(entry)
		}
		a.deleteCronEntry(key)
	}
	a.resetResearch2Runtime()
	if setting == nil || setting.Settings == nil || !setting.Research2AutoEnabled {
		return
	}
	configID := int(setting.AIAnalysisConfigID)
	registrations := []struct {
		key, spec string
		run       func()
	}{
		{research2AnalysisEntryKey, "0 50 9 * * 1-5", func() { a.runResearch2Analysis(time.Now()) }},
		{research2TradingEntryKey, "5 * 9-15 * * 1-5", func() { a.processResearch2Trades(time.Now()) }},
		{research2MetricsEntryKey, "0 5 15 * * 1-5", func() { a.finalizeResearch2Metrics(time.Now()) }},
	}
	for _, registration := range registrations {
		entryID, err := a.cron.AddFunc(registration.spec, registration.run)
		if err != nil {
			a.recordSchedulerRegistrationError(registration.key, registration.spec, err)
			continue
		}
		a.setCronEntry(registration.key, entryID)
	}
	go a.recoverResearch2Schedule(configID, time.Now())
}

func (a *App) recoverResearch2Schedule(configID int, now time.Time) {
	local := now.In(research2Location())
	if local.Hour() < 9 || (local.Hour() == 9 && local.Minute() < 50) || local.Hour() >= 15 {
		return
	}
	runtime, err := a.ensureResearch2Runtime(configID)
	if err != nil {
		logger.SugaredLogger.Errorf("初始化研究中心2失败: %v", err)
		return
	}
	tradeDay, err := data.ResearchTradingCalendar{}.IsTradingDay(a.ctx, local)
	if err != nil || !tradeDay {
		return
	}
	if _, exists, lookupErr := runtime.Repository.RunForDate(a.ctx, local.Format("2006-01-02")); lookupErr == nil && !exists {
		scheduled := time.Date(local.Year(), local.Month(), local.Day(), 9, 50, 0, 0, research2Location())
		a.runResearch2Analysis(scheduled)
	}
	a.processResearch2Trades(local)
}

func (a *App) runResearch2Analysis(scheduledFor time.Time) {
	if !a.research2RunMu.TryLock() {
		return
	}
	defer a.research2RunMu.Unlock()
	setting := data.GetSettingConfig()
	if setting == nil || setting.Settings == nil || !setting.Research2AutoEnabled {
		return
	}
	runtime, err := a.ensureResearch2Runtime(int(setting.AIAnalysisConfigID))
	if err != nil {
		logger.SugaredLogger.Errorf("初始化研究中心2失败: %v", err)
		return
	}
	if _, err = runtime.Runner.Run(a.ctx, scheduledFor); err != nil {
		logger.SugaredLogger.Errorf("研究中心2分析失败: %v", err)
		return
	}
	a.processResearch2Trades(time.Now())
}

func (a *App) processResearch2Trades(now time.Time) {
	if !a.research2TradeMu.TryLock() {
		return
	}
	defer a.research2TradeMu.Unlock()
	setting := data.GetSettingConfig()
	if setting == nil || setting.Settings == nil || !setting.Research2AutoEnabled {
		return
	}
	runtime, err := a.ensureResearch2Runtime(int(setting.AIAnalysisConfigID))
	if err != nil {
		logger.SugaredLogger.Errorf("初始化研究中心2失败: %v", err)
		return
	}
	if err = runtime.Trading.ProcessDue(a.ctx, now); err != nil {
		logger.SugaredLogger.Errorf("研究中心2模拟成交处理失败: %v", err)
	}
}

func (a *App) finalizeResearch2Metrics(now time.Time) {
	if !a.research2MetricMu.TryLock() {
		return
	}
	defer a.research2MetricMu.Unlock()
	setting := data.GetSettingConfig()
	if setting == nil || setting.Settings == nil || !setting.Research2AutoEnabled {
		return
	}
	runtime, err := a.ensureResearch2Runtime(int(setting.AIAnalysisConfigID))
	if err != nil {
		logger.SugaredLogger.Errorf("初始化研究中心2失败: %v", err)
		return
	}
	if err = runtime.Trading.FinalizeMetrics(a.ctx, now); err != nil {
		logger.SugaredLogger.Errorf("研究中心2指标结算失败: %v", err)
	}
}

func (a *App) research2Repository() (*research2.Repository, error) {
	setting := data.GetSettingConfig()
	configID := 0
	if setting != nil {
		configID = int(setting.AIAnalysisConfigID)
	}
	runtime, err := a.ensureResearch2Runtime(configID)
	if err != nil {
		return nil, err
	}
	return runtime.Repository, nil
}

func (a *App) listResearch2Runs(ctx context.Context, limit, offset int) ([]research2.AnalysisRunSummary, error) {
	repository, err := a.research2Repository()
	if err != nil {
		return nil, err
	}
	return repository.ListRuns(ctx, limit, offset)
}
func (a *App) getResearch2Run(ctx context.Context, id string) (research2.AnalysisRun, error) {
	repository, err := a.research2Repository()
	if err != nil {
		return research2.AnalysisRun{}, err
	}
	return repository.GetRun(ctx, id)
}
func (a *App) listResearch2Recommendations(ctx context.Context, limit, offset int) ([]research2.Recommendation, error) {
	repository, err := a.research2Repository()
	if err != nil {
		return nil, err
	}
	return repository.ListRecommendations(ctx, limit, offset)
}
func (a *App) getResearch2Recommendation(ctx context.Context, id string) (research2.RecommendationDetail, error) {
	repository, err := a.research2Repository()
	if err != nil {
		return research2.RecommendationDetail{}, err
	}
	return repository.GetRecommendation(ctx, id)
}
func (a *App) getResearch2Account(ctx context.Context) (research2.AccountOverview, error) {
	repository, err := a.research2Repository()
	if err != nil {
		return research2.AccountOverview{}, err
	}
	return repository.Overview(ctx)
}
func (a *App) getResearch2Performance(ctx context.Context) (research2.Performance, error) {
	repository, err := a.research2Repository()
	if err != nil {
		return research2.Performance{}, err
	}
	return repository.Performance(ctx)
}

func research2Location() *time.Location {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*60*60)
	}
	return location
}
