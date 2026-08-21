package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go-stock/backend/data"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	"go-stock/backend/research"
)

const (
	researchFundingEntryKey  = "ResearchAccountFunding"
	researchSnapshotEntryKey = "ResearchAccountDailyCloseSnapshot"
)

func (a *App) replaceResearchRuntime(configID int) error {
	factory := a.researchFactory
	if factory == nil {
		factory = data.NewResearchRuntime
	}
	runtime, err := factory(configID)
	if err != nil {
		return err
	}
	a.researchRuntimeMu.Lock()
	a.researchRuntime = runtime
	a.researchRuntimeMu.Unlock()
	return nil
}

func (a *App) getResearchRuntime() (*data.ResearchRuntime, error) {
	a.researchRuntimeMu.RLock()
	runtime := a.researchRuntime
	a.researchRuntimeMu.RUnlock()
	if runtime != nil {
		return runtime, nil
	}
	// Read-only research pages remain available even when every model is off.
	if err := a.replaceResearchRuntime(0); err != nil {
		return nil, err
	}
	a.researchRuntimeMu.RLock()
	defer a.researchRuntimeMu.RUnlock()
	return a.researchRuntime, nil
}

func (a *App) reloadAIAnalysisCron(setting *models.SettingConfig) {
	// Account contributions and daily valuation snapshots belong to the
	// simulated portfolio, not to the model switch. Register them even when AI
	// analysis is disabled or every model configuration is turned off.
	a.ensureResearchAccountCrons()
	for key, entryID := range a.snapshotCronEntries() {
		if strings.HasPrefix(key, aiAnalysisEntryPrefix) || key == aiLifecycleEntryKey {
			a.cron.Remove(entryID)
			a.deleteCronEntry(key)
		}
	}
	// The lifecycle scanner also performs one-shot pending buys and retries a
	// previously approved sell. Those transitions must continue even when new
	// AI analysis is disabled or no model is currently callable.
	entryID, lifecycleErr := a.cron.AddFunc("@every 1m", func() { a.processDueAILifecycle() })
	if lifecycleErr != nil {
		a.recordSchedulerRegistrationError(aiLifecycleEntryKey, "@every 1m", lifecycleErr)
		return
	}
	a.setCronEntry(aiLifecycleEntryKey, entryID)
	a.goTask(func(context.Context) { a.processDueAILifecycle() })
	if setting == nil || setting.Settings == nil {
		return
	}
	selected, err := data.ResolveAIAnalysisConfig(setting)
	if err != nil {
		if replaceErr := a.replaceResearchRuntime(0); replaceErr != nil {
			a.recordSchedulerRegistrationError("AIAnalysisRuntime", setting.AIAnalysisTimes, replaceErr)
			return
		}
		logger.SugaredLogger.Infof("AI 分析没有已启用模型，定时任务不注册: %v", err)
		return
	}
	if err := a.replaceResearchRuntime(int(selected.ID)); err != nil {
		a.recordSchedulerRegistrationError("AIAnalysisRuntime", setting.AIAnalysisTimes, err)
		return
	}
	if !setting.AIAnalysisEnabled {
		logger.SugaredLogger.Info("AI 新推荐定时任务已关闭；手动分析、持仓生命周期与账户任务继续运行")
		return
	}
	times, err := data.NormalizeAIAnalysisTimes(setting.AIAnalysisTimes)
	if err != nil {
		a.recordSchedulerRegistrationError("AIAnalysis", setting.AIAnalysisTimes, err)
		return
	}
	for index, hhmm := range times {
		spec := buildSummaryCronSpec(hhmm)
		entryID, addErr := a.cron.AddFunc(spec, func() { a.runScheduledAIAnalysis() })
		if addErr != nil {
			a.recordSchedulerRegistrationError("AIAnalysis:"+hhmm, spec, addErr)
			continue
		}
		a.setCronEntry(fmt.Sprintf("%s%d", aiAnalysisEntryPrefix, index), entryID)
	}
	logger.SugaredLogger.Infof("AI 分析定时任务生效: %v，生命周期每分钟扫描待买入和固定卖出检查时点", times)
}

func (a *App) ensureResearchAccountCrons() {
	if _, exists := a.getCronEntry(researchFundingEntryKey); !exists {
		entryID, err := a.cron.AddFunc("0 * * * * *", func() { a.processScheduledResearchFunding() })
		if err != nil {
			a.recordSchedulerRegistrationError(researchFundingEntryKey, "0 * * * * *", err)
		} else {
			a.setCronEntry(researchFundingEntryKey, entryID)
			a.goTask(func(context.Context) { a.processScheduledResearchFunding() })
		}
	}
	if _, exists := a.getCronEntry(researchSnapshotEntryKey); !exists {
		entryID, err := a.cron.AddFunc("0 * 15 * * *", func() { a.processScheduledResearchSnapshot() })
		if err != nil {
			a.recordSchedulerRegistrationError(researchSnapshotEntryKey, "0 * 15 * * *", err)
		} else {
			a.setCronEntry(researchSnapshotEntryKey, entryID)
			a.goTask(func(context.Context) { a.processScheduledResearchSnapshot() })
		}
	}
}

func (a *App) processScheduledResearchFunding() {
	now := time.Now()
	local := research.ShanghaiTime(now)
	if local.Hour() != 9 || local.Minute() < 20 || local.Minute() >= 30 {
		return
	}
	runtime, err := a.getResearchRuntime()
	if err != nil {
		logger.SugaredLogger.Errorf("模拟账户入金运行时不可用: %v", err)
		return
	}
	ctx := a.taskContext()
	result, err := runtime.Service.ProcessScheduledFunding(ctx, now)
	if err != nil {
		logger.SugaredLogger.Errorf("模拟账户定时入金失败: %v", err)
		return
	}
	if result.Applied && result.CashFlow != nil {
		logger.SugaredLogger.Infof("模拟账户定时入金完成: sequence=%d amount=%.2f tradingDate=%s remaining=%d",
			result.CashFlow.Sequence, result.CashFlow.Amount, result.CashFlow.TradingDate, result.RemainingDeposits)
	}
}

func (a *App) processScheduledResearchSnapshot() {
	now := time.Now()
	local := research.ShanghaiTime(now)
	if local.Hour() < 15 || (local.Hour() == 15 && local.Minute() < 5) {
		return
	}
	runtime, err := a.getResearchRuntime()
	if err != nil {
		logger.SugaredLogger.Errorf("模拟账户收盘快照运行时不可用: %v", err)
		return
	}
	ctx := a.taskContext()
	applied, err := runtime.Service.ProcessScheduledSnapshot(ctx, now)
	if err != nil {
		logger.SugaredLogger.Errorf("模拟账户收盘快照失败: %v", err)
		return
	}
	if applied {
		logger.SugaredLogger.Infof("模拟账户收盘快照完成: tradingDate=%s", local.Format("2006-01-02"))
	}
}

func (a *App) runScheduledAIAnalysis() {
	if err := a.startAIAnalysis(research.AnalysisModeScheduled); err != nil {
		logger.SugaredLogger.Errorf("AI 分析启动失败: %v", err)
	}
}

func (a *App) startAIAnalysis(origin string) error {
	a.aiAnalysisRunMu.Lock()
	defer a.aiAnalysisRunMu.Unlock()
	if a.aiAnalysisRunning {
		return errors.New("已有 running 状态的 AI 分析，本次运行被拒绝")
	}
	runtime, err := a.getResearchRuntime()
	if err != nil {
		return fmt.Errorf("AI 分析运行时不可用: %w", err)
	}
	running, err := runtime.Repository.HasRunningAnalysis(a.taskContext())
	if err != nil {
		return fmt.Errorf("检查运行中分析失败: %w", err)
	}
	if running {
		return errors.New("已有 running 状态的 AI 分析，本次运行被拒绝")
	}
	setting := a.services.Config.GetConfig()
	if setting == nil || setting.Settings == nil {
		return errors.New("AI 分析设置不存在")
	}
	if origin == research.AnalysisModeScheduled && !setting.AIAnalysisEnabled {
		return errors.New("AI 自动分析当前未启用")
	}
	selected, err := data.ResolveAIAnalysisConfig(setting)
	if err != nil {
		return fmt.Errorf("AI 分析配置不可用: %w", err)
	}
	now := time.Now()
	a.aiAnalysisRunning = true
	a.goTask(func(ctx context.Context) {
		defer func() {
			a.aiAnalysisRunMu.Lock()
			a.aiAnalysisRunning = false
			a.aiAnalysisRunMu.Unlock()
		}()
		run, runErr := runtime.Runner.Run(ctx, research.AnalysisRequest{ScheduledFor: now, AIConfigID: selected.ID,
			ProviderName: data.DisplayAIProviderName(selected), ModelName: selected.ModelName, Mode: origin})
		if runErr != nil {
			if errors.Is(runErr, research.ErrScheduledAnalysisSkipped) {
				logger.SugaredLogger.Infof("AI 自动分析已由交易时段门禁跳过: %v", runErr)
				return
			}
			logger.SugaredLogger.Errorf("AI 分析失败 origin=%s run=%s: %v", origin, run.RunID, runErr)
			return
		}
		logger.SugaredLogger.Infof("AI 分析完成 origin=%s run=%s status=%s recommendations=%d", origin, run.RunID, run.Status, run.RecommendationCount)
	})
	return nil
}

// StartAIAnalysis starts one formal research run in the background. The UI
// follows the persisted running report and only enables the button again after
// that report reaches a terminal status.
func (a *App) startManualAIAnalysis() (bool, error) {
	if err := a.startAIAnalysis(research.AnalysisModeManual); err != nil {
		return false, err
	}
	return true, nil
}

func (a *App) processDueAILifecycle() {
	// Protect across runtime replacements as well as ordinary cron overlap. A
	// slow model call must not let a later one-minute tick process the same due
	// recommendation through a newly constructed Service instance.
	if !a.aiLifecycleRunMu.TryLock() {
		return
	}
	defer a.aiLifecycleRunMu.Unlock()
	runtime, err := a.getResearchRuntime()
	if err != nil {
		logger.SugaredLogger.Errorf("AI 生命周期运行时不可用: %v", err)
		return
	}
	ctx := a.taskContext()
	if err := runtime.Service.ProcessDue(ctx); err != nil {
		logger.SugaredLogger.Errorf("AI 生命周期处理失败: %v", err)
	}
}

func normalizedPage(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func (a *App) listAIAnalysisReports(ctx context.Context, limit, offset int) ([]research.AnalysisRunSummary, error) {
	runtime, err := a.getResearchRuntime()
	if err != nil {
		return nil, err
	}
	limit, offset = normalizedPage(limit, offset)
	return runtime.Repository.ListAnalysis(ctx, limit, offset)
}

func (a *App) getAIAnalysisReport(ctx context.Context, runID string) (research.AnalysisRun, error) {
	if strings.TrimSpace(runID) == "" {
		return research.AnalysisRun{}, errors.New("runId is required")
	}
	runtime, err := a.getResearchRuntime()
	if err != nil {
		return research.AnalysisRun{}, err
	}
	return runtime.Repository.Analysis(ctx, runID)
}

func (a *App) listAIRecommendations(ctx context.Context, limit, offset int) ([]research.Recommendation, error) {
	runtime, err := a.getResearchRuntime()
	if err != nil {
		return nil, err
	}
	limit, offset = normalizedPage(limit, offset)
	return runtime.Repository.ListRecommendations(ctx, limit, offset)
}

func (a *App) getAIRecommendation(ctx context.Context, recommendationID string) (research.RecommendationDetail, error) {
	if strings.TrimSpace(recommendationID) == "" {
		return research.RecommendationDetail{}, errors.New("recommendationId is required")
	}
	runtime, err := a.getResearchRuntime()
	if err != nil {
		return research.RecommendationDetail{}, err
	}
	return runtime.Service.Detail(ctx, recommendationID)
}

func (a *App) getAIRecommendationChart(ctx context.Context, recommendationID string, refresh bool) (research.RecommendationChart, error) {
	if strings.TrimSpace(recommendationID) == "" {
		return research.RecommendationChart{}, errors.New("recommendationId is required")
	}
	runtime, err := a.getResearchRuntime()
	if err != nil {
		return research.RecommendationChart{}, err
	}
	return runtime.Service.RecommendationChart(ctx, recommendationID, refresh)
}

func (a *App) getAISimulatedAccountContext(ctx context.Context) (research.AccountOverview, error) {
	runtime, err := a.getResearchRuntime()
	if err != nil {
		return research.AccountOverview{}, err
	}
	return runtime.Service.AccountOverview(ctx)
}

func (a *App) getAIAccountCashFlowsContext(ctx context.Context) ([]research.AccountCashFlow, error) {
	runtime, err := a.getResearchRuntime()
	if err != nil {
		return nil, err
	}
	return runtime.Service.CashFlows(ctx)
}

func (a *App) getAIAccountPerformanceContext(ctx context.Context) (research.AccountPerformance, error) {
	runtime, err := a.getResearchRuntime()
	if err != nil {
		return research.AccountPerformance{}, err
	}
	return runtime.Service.AccountPerformance(ctx)
}
