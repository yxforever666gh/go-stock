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
	"go-stock/backend/researchapp"
	"go-stock/internal/recommendationchart"
)

const (
	researchSnapshotEntryKey         = "ResearchAccountDailyCloseSnapshot"
	capitalDeploymentLeaseDuration   = 10 * time.Minute
	capitalDeploymentHeartbeat       = time.Minute
	capitalDeploymentStartHour       = 9
	capitalDeploymentStartMinute     = 35
	capitalDeploymentCutoffHour      = 14
	capitalDeploymentCutoffMinute    = 25
	defaultCapitalReanalysisInterval = 30 * time.Minute
)

func (a *App) replaceResearchRuntime(configID int) error {
	factory := a.researchFactory
	if factory == nil {
		factory = newResearchRuntime
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

func (a *App) getResearchRuntime() (*researchapp.Runtime, error) {
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

func (a *App) reloadAIAnalysisCron(setting *models.SettingConfig, startup bool) {
	// Account valuation and position lifecycle work are independent from the
	// capital-deployment switch and must survive model/configuration changes.
	a.ensureResearchAccountCrons()
	for key, entryID := range a.snapshotCronEntries() {
		if key == aiLifecycleEntryKey || key == aiDeploymentEntryKey {
			a.cron.Remove(entryID)
			a.deleteCronEntry(key)
		}
	}
	if setting == nil || setting.Settings == nil {
		if err := a.replaceResearchRuntime(0); err != nil {
			a.recordSchedulerRegistrationError("AICapitalDeploymentRuntime", "@every 1m", err)
			return
		}
		a.registerResearchLifecycleScanner()
		return
	}
	target, maxImmediate, _, policyErr := data.NormalizeAICapitalDeploymentSettings(
		setting.AITargetCapitalUtilization,
		setting.AIMaxImmediateBuysPerRun,
		setting.AIReanalysisIntervalMinutes,
	)
	if policyErr != nil {
		a.recordSchedulerRegistrationError("AICapitalDeploymentPolicy", "@every 1m", policyErr)
		target, maxImmediate = 0.90, 2
	}
	selected, err := data.ResolveAIAnalysisConfig(setting)
	if err != nil {
		if replaceErr := a.replaceResearchRuntime(0); replaceErr != nil {
			a.recordSchedulerRegistrationError("AICapitalDeploymentRuntime", "@every 1m", replaceErr)
			return
		}
		logger.SugaredLogger.Infof("资金补位尚无可用模型，事件会持久化等待配置恢复: %v", err)
	} else {
		if err := a.replaceResearchRuntime(int(selected.ID)); err != nil {
			a.recordSchedulerRegistrationError("AICapitalDeploymentRuntime", "@every 1m", err)
			return
		}
	}
	runtime, runtimeErr := a.getResearchRuntime()
	if runtimeErr != nil {
		a.recordSchedulerRegistrationError("AICapitalDeploymentRuntime", "@every 1m", runtimeErr)
		return
	}
	runtime.Service.SetCapitalDeploymentPolicy(target, maxImmediate)
	a.registerResearchLifecycleScanner()
	deploymentID, deploymentErr := a.cron.AddFunc("@every 1m", func() { a.processCapitalDeployment(false) })
	if deploymentErr != nil {
		a.recordSchedulerRegistrationError(aiDeploymentEntryKey, "@every 1m", deploymentErr)
		return
	}
	a.setCronEntry(aiDeploymentEntryKey, deploymentID)
	a.goTask(func(context.Context) { a.processCapitalDeployment(startup) })
	logger.SugaredLogger.Infof("资金补位事件调度器已加载 enabled=%t；持仓生命周期继续每分钟扫描", setting.AICapitalDeploymentEnabled)
}

func (a *App) registerResearchLifecycleScanner() {
	entryID, lifecycleErr := a.cron.AddFunc("@every 1m", func() { a.processDueAILifecycle() })
	if lifecycleErr != nil {
		a.recordSchedulerRegistrationError(aiLifecycleEntryKey, "@every 1m", lifecycleErr)
		return
	}
	a.setCronEntry(aiLifecycleEntryKey, entryID)
	// Run after the selected model runtime is installed so overdue per-stock
	// reviews are recovered immediately without racing a config-0 runtime.
	a.goTask(func(context.Context) { a.processDueAILifecycle() })
}

func (a *App) ensureResearchAccountCrons() {
	if _, exists := a.getCronEntry(researchSnapshotEntryKey); !exists {
		entryID, err := a.cron.AddFunc(researchSnapshotCronSpec, func() { a.processScheduledResearchSnapshot() })
		if err != nil {
			a.recordSchedulerRegistrationError(researchSnapshotEntryKey, researchSnapshotCronSpec, err)
		} else {
			a.setCronEntry(researchSnapshotEntryKey, entryID)
			a.goTask(func(context.Context) { a.processScheduledResearchSnapshot() })
		}
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

// nextCapitalDeploymentWindow normalizes a requested instant to a valid
// Shanghai-market start time. Conclusions are never queued overnight.
func nextCapitalDeploymentWindow(ctx context.Context, service *research.Service, requested time.Time) (time.Time, error) {
	local := research.ShanghaiTime(requested)
	for scanned := 0; scanned < 740; scanned++ {
		day := local.AddDate(0, 0, scanned)
		trading, err := service.IsTradingDay(ctx, day)
		if err != nil {
			return time.Time{}, err
		}
		if !trading {
			continue
		}
		year, month, date := day.Date()
		morning := time.Date(year, month, date, capitalDeploymentStartHour, capitalDeploymentStartMinute, 0, 0, day.Location())
		afternoon := time.Date(year, month, date, 13, 0, 0, 0, day.Location())
		morningClose := time.Date(year, month, date, 11, 30, 0, 0, day.Location())
		cutoff := time.Date(year, month, date, capitalDeploymentCutoffHour, capitalDeploymentCutoffMinute, 0, 0, day.Location())
		if scanned > 0 || local.Before(morning) {
			return morning, nil
		}
		if !local.After(morningClose) {
			return local, nil
		}
		if local.Before(afternoon) {
			return afternoon, nil
		}
		if !local.After(cutoff) {
			return local, nil
		}
	}
	return time.Time{}, errors.New("未来交易日内没有可用的资金补位窗口")
}

func triggerIdentity(source string, now time.Time, suffix string) string {
	local := research.ShanghaiTime(now)
	return fmt.Sprintf("%s:%s:%s", source, local.Format("20060102-1504"), strings.TrimSpace(suffix))
}

func (a *App) processCapitalDeployment(startup bool) {
	if !a.aiDeploymentRunMu.TryLock() {
		return
	}
	defer a.aiDeploymentRunMu.Unlock()
	runtime, err := a.getResearchRuntime()
	if err != nil {
		logger.SugaredLogger.Errorf("资金补位运行时不可用: %v", err)
		return
	}
	ctx, now := a.taskContext(), time.Now()
	if recovered, recoverErr := runtime.Service.RecoverExpiredAnalysisLeases(ctx, now); recoverErr != nil {
		logger.SugaredLogger.Errorf("资金补位恢复过期租约失败: %v", recoverErr)
		return
	} else if recovered > 0 {
		logger.SugaredLogger.Infof("资金补位已恢复过期租约: %d", recovered)
	}
	if reconciled, reconcileErr := runtime.ReconcileInterruptedAudits(ctx); reconcileErr != nil {
		logger.SugaredLogger.Errorf("资金补位中断审计闭合失败: %v", reconcileErr)
		return
	} else if reconciled > 0 {
		logger.SugaredLogger.Infof("资金补位已闭合中断审计: %d", reconciled)
	}
	if normalized, normalizeErr := runtime.Service.NormalizeQueuedAnalysisTriggerWindows(ctx); normalizeErr != nil {
		logger.SugaredLogger.Errorf("资金补位排队窗口规范失败: %v", normalizeErr)
		return
	} else if normalized > 0 {
		logger.SugaredLogger.Infof("资金补位已规范排队窗口: %d", normalized)
	}
	setting := a.services.Config.GetConfig()
	if setting == nil || setting.Settings == nil || !setting.AICapitalDeploymentEnabled {
		return
	}
	target, maxImmediate, reanalysisMinutes, err := data.NormalizeAICapitalDeploymentSettings(
		setting.AITargetCapitalUtilization,
		setting.AIMaxImmediateBuysPerRun,
		setting.AIReanalysisIntervalMinutes,
	)
	if err != nil {
		logger.SugaredLogger.Errorf("资金补位策略无效: %v", err)
		return
	}
	runtime.Service.SetCapitalDeploymentPolicy(target, maxImmediate)
	status, err := runtime.Service.CapitalDeploymentStatus(ctx, now)
	if err != nil {
		logger.SugaredLogger.Errorf("资金补位状态检查失败: %v", err)
		return
	}
	if status.DeployableCash >= research.TargetCashPerTrade && status.PendingTriggerCount+status.RunningTriggerCount == 0 {
		availableAt, windowErr := nextCapitalDeploymentWindow(ctx, runtime.Service, now)
		if windowErr != nil {
			logger.SugaredLogger.Errorf("资金补位定位下一交易窗口失败: %v", windowErr)
			return
		}
		source, reason, suffix := research.TriggerSourceCapitalGap, "检测到可部署资金达到新仓门槛", "gap"
		if startup {
			source, reason, suffix = research.TriggerSourceStartup, "程序启动恢复时检测到资金缺口", a.aiDeploymentLeaseOwner
		}
		if _, enqueueErr := runtime.Service.EnqueueCapitalGapTrigger(ctx, source, triggerIdentity(source, now, suffix), reason, availableAt); enqueueErr != nil {
			logger.SugaredLogger.Errorf("资金补位事件持久化失败: %v", enqueueErr)
			return
		}
	}
	trading, err := runtime.Service.IsTradingDay(ctx, now)
	if err != nil {
		logger.SugaredLogger.Errorf("资金补位交易日检查失败: %v", err)
		return
	}
	if !trading || !research.IsCapitalDeploymentAnalysisWindow(now) {
		return
	}
	selected, err := data.ResolveAIAnalysisConfig(setting)
	if err != nil {
		logger.SugaredLogger.Infof("资金补位事件等待可用模型: %v", err)
		return
	}
	claim, claimed, err := runtime.Service.ClaimAnalysisTriggerBatch(ctx, now, a.aiDeploymentLeaseOwner, capitalDeploymentLeaseDuration)
	if err != nil {
		logger.SugaredLogger.Errorf("资金补位事件认领失败: %v", err)
		return
	}
	if claimed {
		a.startClaimedCapitalDeployment(runtime, selected, claim, time.Duration(reanalysisMinutes)*time.Minute)
	}
}

func (a *App) startClaimedCapitalDeployment(runtime *researchapp.Runtime, selected *models.AIConfig, claim research.AnalysisTriggerClaim, reanalysisInterval time.Duration) {
	a.aiAnalysisRunMu.Lock()
	if a.aiAnalysisRunning {
		a.aiAnalysisRunMu.Unlock()
		_ = runtime.Service.FailAnalysisTriggerBatch(a.taskContext(), claim.Run.RunID, a.aiDeploymentLeaseOwner, time.Now(), errors.New("当前进程已有资金补位分析"))
		return
	}
	a.aiAnalysisRunning = true
	a.aiAnalysisRunMu.Unlock()
	leaseDone := make(chan struct{})
	a.goTask(func(ctx context.Context) {
		ticker := time.NewTicker(capitalDeploymentHeartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-leaseDone:
				return
			case renewedAt := <-ticker.C:
				if err := runtime.Service.RenewAnalysisTriggerLease(ctx, claim.Run.RunID, a.aiDeploymentLeaseOwner, renewedAt, capitalDeploymentLeaseDuration); err != nil {
					logger.SugaredLogger.Errorf("资金补位租约续期失败 run=%s: %v", claim.Run.RunID, err)
					return
				}
			}
		}
	})
	a.goTask(func(ctx context.Context) {
		defer close(leaseDone)
		defer func() {
			a.aiAnalysisRunMu.Lock()
			a.aiAnalysisRunning = false
			a.aiAnalysisRunMu.Unlock()
		}()
		triggerIDs := make([]string, 0, len(claim.Triggers))
		triggerReasons := make([]string, 0, len(claim.Triggers))
		for _, trigger := range claim.Triggers {
			triggerIDs = append(triggerIDs, trigger.TriggerID)
			if trigger.Reason != "" {
				triggerReasons = append(triggerReasons, trigger.Reason)
			}
		}
		run, runErr := runtime.Runner.Run(ctx, research.AnalysisRequest{
			ScheduledFor: claim.Run.ScheduledFor, AIConfigID: selected.ID, ProviderName: data.DisplayAIProviderName(selected),
			ModelName: selected.ModelName, Mode: research.AnalysisModeEvent, ReservedRunID: claim.Run.RunID,
			LeaseOwner: a.aiDeploymentLeaseOwner, TriggerIDs: triggerIDs, TriggerReasons: triggerReasons, TriggerSource: claim.Run.TriggerSource,
			ReanalysisInterval: reanalysisInterval,
		})
		completedAt := time.Now()
		if runErr != nil {
			if err := runtime.Service.FailAnalysisTriggerBatch(ctx, claim.Run.RunID, a.aiDeploymentLeaseOwner, completedAt, runErr); err != nil {
				logger.SugaredLogger.Errorf("资金补位技术失败回退失败 run=%s: %v", claim.Run.RunID, err)
			}
			logger.SugaredLogger.Errorf("资金补位分析失败 run=%s: %v", claim.Run.RunID, runErr)
			return
		}
		if err := runtime.Service.CompleteAnalysisTriggerBatch(ctx, claim.Run.RunID, a.aiDeploymentLeaseOwner, completedAt); err != nil {
			logger.SugaredLogger.Errorf("资金补位事件完成确认失败 run=%s: %v", claim.Run.RunID, err)
			return
		}
		if reanalysisInterval <= 0 {
			reanalysisInterval = defaultCapitalReanalysisInterval
		}
		status, statusErr := runtime.Service.CapitalDeploymentStatus(ctx, completedAt)
		if statusErr == nil && status.DeployableCash >= research.TargetCashPerTrade {
			requested := completedAt.Add(reanalysisInterval)
			waitAt, waitErr := runtime.Repository.EarliestActiveWaitReanalysis(ctx)
			if waitErr != nil {
				logger.SugaredLogger.Warnf("读取待观察重分析时间失败 run=%s: %v", run.RunID, waitErr)
			}
			if waitErr == nil && waitAt != nil && waitAt.Before(requested) {
				requested = *waitAt
				if !requested.After(completedAt) {
					requested = completedAt.Add(5 * time.Minute)
				}
			}
			availableAt, windowErr := nextCapitalDeploymentWindow(ctx, runtime.Service, requested)
			if windowErr == nil {
				_, enqueueErr := runtime.Service.EnqueueCapitalGapTrigger(ctx, research.TriggerSourceCapitalGap,
					triggerIdentity(research.TriggerSourceCapitalGap, completedAt, "followup-"+claim.Run.RunID),
					"上一轮完成后仍有可部署资金，重新执行完整分析", availableAt)
				if enqueueErr != nil {
					logger.SugaredLogger.Errorf("资金补位下一轮事件写入失败 run=%s: %v", claim.Run.RunID, enqueueErr)
				}
			}
		}
		logger.SugaredLogger.Infof("资金补位分析完成 run=%s status=%s buy_now=%d wait=%d reject=%d", run.RunID, run.Status, run.BuyNowCount, run.WaitCount, run.RejectCount)
	})
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

type capitalDeploymentStatusResponse struct {
	Enabled                bool       `json:"enabled"`
	State                  string     `json:"state"`
	Cash                   float64    `json:"cash"`
	ReservedCash           float64    `json:"reservedCash"`
	NetAssetValue          float64    `json:"netAssetValue"`
	ReserveTarget          float64    `json:"reserveTarget"`
	DeployableCash         float64    `json:"deployableCash"`
	CapitalUtilization     float64    `json:"capitalUtilization"`
	AvailableSlots         int        `json:"availableSlots"`
	PendingEventCount      int64      `json:"pendingEventCount"`
	WatchingCandidateCount int        `json:"watchingCandidateCount"`
	LastTriggeredAt        *time.Time `json:"lastTriggeredAt"`
	NextEligibleAt         *time.Time `json:"nextEligibleAt"`
	Reason                 string     `json:"reason"`
}

func (a *App) getAICapitalDeploymentStatusContext(ctx context.Context) (capitalDeploymentStatusResponse, error) {
	runtime, err := a.getResearchRuntime()
	if err != nil {
		return capitalDeploymentStatusResponse{}, err
	}
	setting := a.services.Config.GetConfig()
	enabled := setting != nil && setting.Settings != nil && setting.AICapitalDeploymentEnabled
	if enabled {
		target, maxImmediate, _, policyErr := data.NormalizeAICapitalDeploymentSettings(
			setting.AITargetCapitalUtilization,
			setting.AIMaxImmediateBuysPerRun,
			setting.AIReanalysisIntervalMinutes,
		)
		if policyErr != nil {
			return capitalDeploymentStatusResponse{}, policyErr
		}
		runtime.Service.SetCapitalDeploymentPolicy(target, maxImmediate)
	}
	now := time.Now()
	status, err := runtime.Service.CapitalDeploymentStatus(ctx, now)
	if err != nil {
		return capitalDeploymentStatusResponse{}, err
	}
	response := capitalDeploymentStatusResponse{
		Enabled: enabled, Cash: status.Cash, ReservedCash: status.ReservedCash, NetAssetValue: status.NetAssetValue,
		ReserveTarget: status.CapitalBuffer, DeployableCash: status.DeployableCash, CapitalUtilization: status.CapitalUtilization,
		AvailableSlots: status.AvailableSlots, PendingEventCount: status.PendingTriggerCount + status.RunningTriggerCount,
		NextEligibleAt: status.NextAnalysisAt, Reason: status.Reason,
	}
	opportunities, opportunityErr := runtime.Repository.ListBuyOpportunities(ctx, 200, 0)
	if opportunityErr != nil {
		return capitalDeploymentStatusResponse{}, opportunityErr
	}
	for _, opportunity := range opportunities {
		if opportunity.Action == research.OpportunityActionWait && opportunity.Status == "active" {
			response.WatchingCandidateCount++
		}
	}
	runs, runsErr := runtime.Repository.ListAnalysis(ctx, 50, 0)
	if runsErr != nil {
		return capitalDeploymentStatusResponse{}, runsErr
	}
	for _, run := range runs {
		if strings.TrimSpace(run.TriggerSource) != "" {
			startedAt := run.StartedAt
			response.LastTriggeredAt = &startedAt
			break
		}
	}
	if enabled && status.RunningTriggerCount == 0 && status.PendingTriggerCount > 0 {
		requested := now
		if status.NextAnalysisAt != nil && status.NextAnalysisAt.After(requested) {
			requested = *status.NextAnalysisAt
		}
		if next, windowErr := nextCapitalDeploymentWindow(ctx, runtime.Service, requested); windowErr == nil {
			response.NextEligibleAt = &next
		}
	}
	switch {
	case !enabled:
		response.State, response.Reason = "disabled", "资金补位策略已关闭"
	case status.RunningTriggerCount > 0:
		response.State = "running"
	case status.DeployableCash < research.TargetCashPerTrade:
		response.State = "insufficient_cash"
	case response.NextEligibleAt != nil && response.NextEligibleAt.After(now):
		response.State = "waiting"
	case status.PendingTriggerCount > 0 && research.IsCapitalDeploymentAnalysisWindow(now):
		response.State = "ready"
	case !research.IsCapitalDeploymentAnalysisWindow(now):
		response.State = "outside_window"
		if next, windowErr := nextCapitalDeploymentWindow(ctx, runtime.Service, now); windowErr == nil {
			response.NextEligibleAt = &next
			response.Reason = "等待下一个交易日资金补位窗口"
		}
	default:
		response.State = "idle"
	}
	if enabled {
		if _, configErr := data.ResolveAIAnalysisConfig(setting); configErr != nil {
			response.State, response.Reason = "waiting_model", "资金补位事件已保留，等待可用 AI 模型"
		}
	}
	return response, nil
}

func (a *App) listAIBuyOpportunitiesContext(ctx context.Context, limit, offset int) ([]research.BuyOpportunity, error) {
	runtime, err := a.getResearchRuntime()
	if err != nil {
		return nil, err
	}
	limit, offset = normalizedPage(limit, offset)
	return runtime.Repository.ListBuyOpportunities(ctx, limit, offset)
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

func (a *App) getAIRecommendationChart(ctx context.Context, recommendationID string, refresh bool) (recommendationchart.Chart, error) {
	if strings.TrimSpace(recommendationID) == "" {
		return recommendationchart.Chart{}, errors.New("recommendationId is required")
	}
	runtime, err := a.getResearchRuntime()
	if err != nil {
		return recommendationchart.Chart{}, err
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
