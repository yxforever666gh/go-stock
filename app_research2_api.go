package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go-stock/backend/data"
	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	"go-stock/backend/research2"
	"go-stock/backend/research2app"
	"go-stock/internal/recommendationchart"
	"go-stock/internal/service"
)

const (
	research2AnalysisCronSpec    = "0 55 9 * * 1-5"
	research2AnalysisStartHour   = 9
	research2AnalysisStartMinute = 55
)

func (a *App) ensureResearch2Runtime(configID int) (*research2app.Runtime, error) {
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
	for _, key := range []string{research2AnalysisEntryKey, research2TradingEntryKey, research2MetricsEntryKey, research2EmailEntryKey} {
		if entry, exists := a.getCronEntry(key); exists {
			a.cron.Remove(entry)
		}
		a.deleteCronEntry(key)
	}
	if setting != nil && setting.Settings != nil && !setting.Research2AutoEnabled {
		a.research2RuntimeMu.RLock()
		existing := a.research2Runtime
		a.research2RuntimeMu.RUnlock()
		if existing != nil && existing.Repository != nil {
			now := time.Now().In(research2Location())
			chains, err := existing.Repository.DisableRunningExecutionChains(a.ctx, now.Format("2006-01-02"), now)
			if err != nil {
				logger.SugaredLogger.Errorf("关闭研究中心2补位链失败: %v", err)
			} else {
				for _, chain := range chains {
					a.queueResearch2FinalEmail(existing, setting, chain)
				}
			}
		}
	}
	a.resetResearch2Runtime()
	if setting == nil || setting.Settings == nil {
		return
	}
	configID := int(setting.AIAnalysisConfigID)
	if setting.Research2EmailEnabled {
		entryID, err := a.cron.AddFunc("@every 30s", func() { a.processResearch2Emails() })
		if err != nil {
			a.recordSchedulerRegistrationError(research2EmailEntryKey, "@every 30s", err)
		} else {
			a.setCronEntry(research2EmailEntryKey, entryID)
			go a.processResearch2Emails()
		}
	} else {
		go a.cancelResearch2Emails()
	}
	if !setting.Research2AutoEnabled {
		return
	}
	registrations := []struct {
		key, spec string
		run       func()
	}{
		{research2AnalysisEntryKey, research2AnalysisCronSpec, func() { a.runResearch2Analysis(time.Now()) }},
		{research2TradingEntryKey, "5 * 9-15 * * 1-5", func() {
			now := time.Now()
			a.processResearch2Trades(now)
			go a.resumeResearch2ExecutionChain(now)
		}},
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
	if db.Dao != nil {
		repository := research2.NewRepository(db.Dao)
		expired, expireErr := repository.ExpireStaleExecutionChains(a.ctx, local.Format("2006-01-02"), local)
		if expireErr != nil {
			logger.SugaredLogger.Errorf("结束研究中心2跨日补位链失败: %v", expireErr)
		} else if setting := data.GetSettingConfig(); setting != nil && setting.Settings != nil && setting.Research2EmailEnabled {
			email := research2.NewEmailService(repository, nil)
			for _, chain := range expired {
				if run, runErr := repository.ExecutionChainEmailRun(a.ctx, chain.ChainID); runErr == nil {
					if _, queueErr := email.QueueFinal(a.ctx, run, research2EmailConfig(setting)); queueErr != nil {
						logger.SugaredLogger.Errorf("研究中心2跨日最终邮件入队失败: %v", queueErr)
					}
				}
			}
		}
	}
	if !withinResearch2RecoveryWindow(local) {
		return
	}
	tradeDay, err := data.ResearchTradingCalendar{}.IsTradingDay(a.ctx, local)
	if err != nil || !tradeDay {
		return
	}
	runtime, runtimeErr := a.ensureResearch2Runtime(configID)
	if runtimeErr != nil {
		logger.SugaredLogger.Errorf("恢复研究中心2运行时初始化失败: %v", runtimeErr)
		return
	}
	if recoverErr := runtime.Repository.RecoverInterruptedRunsForDate(a.ctx, local.Format("2006-01-02"), local); recoverErr != nil {
		logger.SugaredLogger.Errorf("恢复研究中心2中断运行失败: %v", recoverErr)
		return
	}
	// Runner atomically decides whether this is attempt 1, an eligible retry,
	// or a no-op returning the latest terminal run.
	scheduled := research2ScheduledRoot(local)
	a.runResearch2Analysis(scheduled)
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
	chainID, parentRunID := "", ""
	for {
		var run research2.AnalysisRun
		if chainID == "" {
			run, err = runtime.Runner.Run(a.ctx, scheduledFor)
		} else {
			run, err = runtime.Runner.RunRefill(a.ctx, time.Now(), chainID, parentRunID)
		}
		if err != nil {
			if errors.Is(err, research2.ErrOutsideAnalysisStartWindow) {
				return
			}
			logger.SugaredLogger.Errorf("研究中心2分析失败: %v", err)
			if run.ChainID != "" {
				if failedChain, chainErr := runtime.Repository.ExecutionChain(a.ctx, run.ChainID); chainErr == nil && failedChain.Status != "running" {
					a.queueResearch2FinalEmail(runtime, setting, failedChain)
				}
			}
			return
		}
		// Complete the time-sensitive simulated trade path before deciding whether
		// the durable chain needs an immediate replacement round.
		a.processResearch2Trades(time.Now())
		if run.ChainID == "" {
			return
		}
		chain, chainErr := runtime.Repository.RefreshExecutionChainFilled(a.ctx, run.ChainID)
		if chainErr != nil {
			logger.SugaredLogger.Errorf("刷新研究中心2补位链失败: %v", chainErr)
			return
		}
		if chain.Status != "running" {
			a.queueResearch2FinalEmail(runtime, setting, chain)
			return
		}
		ready, readyErr := runtime.Repository.ExecutionChainsReadyForRefill(a.ctx, time.Now())
		if readyErr != nil {
			logger.SugaredLogger.Errorf("检查研究中心2补位任务失败: %v", readyErr)
			return
		}
		refill := false
		for _, candidate := range ready {
			if candidate.ChainID == chain.ChainID {
				refill = true
				break
			}
		}
		if !refill {
			return
		}
		chainID, parentRunID = chain.ChainID, chain.LatestRunID
	}
}

func (a *App) resumeResearch2ExecutionChain(now time.Time) {
	setting := data.GetSettingConfig()
	if setting == nil || setting.Settings == nil || !setting.Research2AutoEnabled {
		return
	}
	runtime, err := a.ensureResearch2Runtime(int(setting.AIAnalysisConfigID))
	if err != nil {
		logger.SugaredLogger.Errorf("恢复研究中心2补位链失败: %v", err)
		return
	}
	chain, exists, err := runtime.Repository.ExecutionChainForDate(a.ctx, now.In(research2Location()).Format("2006-01-02"))
	if err != nil || !exists {
		return
	}
	if chain.Status != "running" {
		a.queueResearch2FinalEmail(runtime, setting, chain)
		return
	}
	if !now.In(research2Location()).Before(time.Date(now.In(research2Location()).Year(), now.In(research2Location()).Month(), now.In(research2Location()).Day(), 13, 0, 0, 0, research2Location())) {
		if err = runtime.Repository.ExpireExecutionChainsAtCutoff(a.ctx, now); err != nil {
			logger.SugaredLogger.Errorf("结束研究中心2补位链失败: %v", err)
			return
		}
		chain, _ = runtime.Repository.ExecutionChain(a.ctx, chain.ChainID)
		a.queueResearch2FinalEmail(runtime, setting, chain)
		return
	}
	a.runResearch2Analysis(research2ScheduledRoot(now))
}

func (a *App) queueResearch2FinalEmail(runtime *research2app.Runtime, setting *models.SettingConfig, chain research2.ExecutionChain) {
	if runtime == nil || runtime.Email == nil || setting == nil || setting.Settings == nil || !setting.Research2EmailEnabled || chain.Status == "running" {
		return
	}
	run, err := runtime.Repository.ExecutionChainEmailRun(a.ctx, chain.ChainID)
	if err != nil {
		logger.SugaredLogger.Errorf("生成研究中心2补位汇总失败: %v", err)
		return
	}
	if _, err = runtime.Email.QueueFinal(a.ctx, run, research2EmailConfig(setting)); err != nil {
		logger.SugaredLogger.Errorf("研究中心2最终报告邮件入队失败: %v", err)
		return
	}
	go a.processResearch2Emails()
}

func withinResearch2RecoveryWindow(value time.Time) bool {
	local := value.In(research2Location())
	minutes := local.Hour()*60 + local.Minute()
	return minutes >= research2AnalysisStartHour*60+research2AnalysisStartMinute && minutes < 13*60
}

func research2ScheduledRoot(value time.Time) time.Time {
	local := value.In(research2Location())
	return time.Date(local.Year(), local.Month(), local.Day(), research2AnalysisStartHour, research2AnalysisStartMinute, 0, 0, research2Location())
}

func research2EmailConfig(setting *models.SettingConfig) research2.EmailConfig {
	if setting == nil || setting.Settings == nil {
		return research2.EmailConfig{}
	}
	return research2.EmailConfig{
		Enabled: setting.Research2EmailEnabled, To: setting.Research2EmailTo, From: setting.Research2EmailFrom,
		SMTPHost: setting.Research2EmailSMTPHost, SMTPPort: setting.Research2EmailSMTPPort,
		Username: setting.Research2EmailSMTPUser, Password: setting.Research2EmailSMTPPass, Timeout: 15 * time.Second,
	}
}

func (a *App) processResearch2Emails() {
	if a == nil || !a.research2EmailMu.TryLock() {
		return
	}
	defer a.research2EmailMu.Unlock()
	setting := data.GetSettingConfig()
	if setting == nil || setting.Settings == nil {
		return
	}
	runtime, err := a.ensureResearch2Runtime(int(setting.AIAnalysisConfigID))
	if err != nil {
		logger.SugaredLogger.Errorf("初始化研究中心2邮件服务失败: %v", err)
		return
	}
	if runtime.Email == nil {
		logger.SugaredLogger.Error("初始化研究中心2邮件服务失败: 邮件服务不可用")
		return
	}
	if err = runtime.Email.ProcessDue(a.ctx, research2EmailConfig(setting)); err != nil {
		logger.SugaredLogger.Errorf("研究中心2报告邮件处理失败: %v", err)
	}
}

func (a *App) cancelResearch2Emails() {
	if a == nil || !a.research2EmailMu.TryLock() {
		return
	}
	defer a.research2EmailMu.Unlock()
	repository, err := a.research2Repository()
	if err == nil {
		_ = repository.CancelPendingEmailDeliveries(a.ctx)
	}
}

func (a *App) testResearch2Email(ctx context.Context) error {
	setting := data.GetSettingConfig()
	if setting == nil || setting.Settings == nil {
		return errors.New("研究中心2邮件配置不可用")
	}
	if _, _, configErr := research2.ValidateEmailConfig(research2EmailConfig(setting)); configErr != nil {
		return fmt.Errorf("%w: %s", service.ErrInvalidInput, configErr.Error())
	}
	runtime, err := a.ensureResearch2Runtime(int(setting.AIAnalysisConfigID))
	if err != nil {
		return err
	}
	return runtime.Email.SendTest(ctx, research2EmailConfig(setting))
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

func (a *App) research2Valuation() (*research2.Service, error) {
	setting := data.GetSettingConfig()
	configID := 0
	if setting != nil {
		configID = int(setting.AIAnalysisConfigID)
	}
	runtime, err := a.ensureResearch2Runtime(configID)
	if err != nil {
		return nil, err
	}
	if runtime.Valuation == nil {
		return nil, errors.New("research2 valuation service is unavailable")
	}
	return runtime.Valuation, nil
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
	valuation, err := a.research2Valuation()
	if err != nil {
		return nil, err
	}
	return valuation.ListRecommendations(ctx, limit, offset)
}
func (a *App) getResearch2Recommendation(ctx context.Context, id string) (research2.RecommendationDetail, error) {
	valuation, err := a.research2Valuation()
	if err != nil {
		return research2.RecommendationDetail{}, err
	}
	return valuation.GetRecommendation(ctx, id)
}
func (a *App) getResearch2Account(ctx context.Context) (research2.AccountOverview, error) {
	valuation, err := a.research2Valuation()
	if err != nil {
		return research2.AccountOverview{}, err
	}
	return valuation.Overview(ctx)
}
func (a *App) getResearch2Performance(ctx context.Context) (research2.Performance, error) {
	valuation, err := a.research2Valuation()
	if err != nil {
		return research2.Performance{}, err
	}
	return valuation.Performance(ctx)
}
func (a *App) getResearch2RecommendationChart(ctx context.Context, id string, refresh bool) (recommendationchart.Chart, error) {
	valuation, err := a.research2Valuation()
	if err != nil {
		return recommendationchart.Chart{}, err
	}
	return valuation.RecommendationChart(ctx, id, refresh)
}

func research2Location() *time.Location {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*60*60)
	}
	return location
}
