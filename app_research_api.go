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

func (a *App) replaceResearchRuntime(configID int) error {
	runtime, err := data.NewResearchRuntime(configID)
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
	for key, entryID := range a.snapshotCronEntries() {
		if strings.HasPrefix(key, aiAnalysisEntryPrefix) || key == aiLifecycleEntryKey {
			a.cron.Remove(entryID)
			a.deleteCronEntry(key)
		}
	}
	if setting == nil || setting.Settings == nil || !setting.AIAnalysisEnabled {
		logger.SugaredLogger.Info("AI 分析定时任务已关闭")
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
	entryID, err := a.cron.AddFunc("@every 1m", func() { a.processDueAILifecycle() })
	if err != nil {
		a.recordSchedulerRegistrationError(aiLifecycleEntryKey, "@every 1m", err)
		return
	}
	a.setCronEntry(aiLifecycleEntryKey, entryID)
	logger.SugaredLogger.Infof("AI 分析定时任务生效: %v，生命周期每分钟扫描到期的15分钟任务", times)
}

func (a *App) runScheduledAIAnalysis() {
	if err := a.startAIAnalysis("scheduled"); err != nil {
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
	running, err := runtime.Repository.HasRunningAnalysis(context.Background())
	if err != nil {
		return fmt.Errorf("检查运行中分析失败: %w", err)
	}
	if running {
		return errors.New("已有 running 状态的 AI 分析，本次运行被拒绝")
	}
	setting := a.services.Config.GetConfig()
	if setting == nil || setting.Settings == nil || !setting.AIAnalysisEnabled {
		return errors.New("AI 分析当前未启用")
	}
	selected, err := data.ResolveAIAnalysisConfig(setting)
	if err != nil {
		return fmt.Errorf("AI 分析配置不可用: %w", err)
	}
	now := time.Now()
	a.aiAnalysisRunning = true
	go func() {
		defer func() {
			a.aiAnalysisRunMu.Lock()
			a.aiAnalysisRunning = false
			a.aiAnalysisRunMu.Unlock()
		}()
		ctx := a.ctx
		if ctx == nil {
			ctx = context.Background()
		}
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
	}()
	return nil
}

// StartAIAnalysis starts one formal research run in the background. The UI
// follows the persisted running report and only enables the button again after
// that report reaches a terminal status.
func (a *App) StartAIAnalysis() (bool, error) {
	if err := a.startAIAnalysis("manual"); err != nil {
		return false, err
	}
	return true, nil
}

func (a *App) processDueAILifecycle() {
	runtime, err := a.getResearchRuntime()
	if err != nil {
		logger.SugaredLogger.Errorf("AI 生命周期运行时不可用: %v", err)
		return
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
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

func (a *App) ListAIAnalysisReports(limit, offset int) ([]research.AnalysisRunSummary, error) {
	runtime, err := a.getResearchRuntime()
	if err != nil {
		return nil, err
	}
	limit, offset = normalizedPage(limit, offset)
	return runtime.Repository.ListAnalysis(context.Background(), limit, offset)
}

func (a *App) GetAIAnalysisReport(runID string) (research.AnalysisRun, error) {
	if strings.TrimSpace(runID) == "" {
		return research.AnalysisRun{}, errors.New("runId is required")
	}
	runtime, err := a.getResearchRuntime()
	if err != nil {
		return research.AnalysisRun{}, err
	}
	return runtime.Repository.Analysis(context.Background(), runID)
}

func (a *App) ListAIRecommendations(limit, offset int) ([]research.Recommendation, error) {
	runtime, err := a.getResearchRuntime()
	if err != nil {
		return nil, err
	}
	limit, offset = normalizedPage(limit, offset)
	return runtime.Repository.ListRecommendations(context.Background(), limit, offset)
}

func (a *App) GetAIRecommendation(recommendationID string) (research.RecommendationDetail, error) {
	if strings.TrimSpace(recommendationID) == "" {
		return research.RecommendationDetail{}, errors.New("recommendationId is required")
	}
	runtime, err := a.getResearchRuntime()
	if err != nil {
		return research.RecommendationDetail{}, err
	}
	return runtime.Service.Detail(context.Background(), recommendationID)
}

func (a *App) GetAISimulatedAccount() (research.AccountOverview, error) {
	runtime, err := a.getResearchRuntime()
	if err != nil {
		return research.AccountOverview{}, err
	}
	return runtime.Service.AccountOverview(context.Background())
}
