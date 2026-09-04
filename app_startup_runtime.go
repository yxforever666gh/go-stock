package main

import (
	"context"
	"fmt"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	"go-stock/internal/releaseinfo"
	"time"
)

const marketNewsPollingMinimumInterval = 5 * time.Minute

func (a *App) domReady(ctx context.Context) {
	defer PanicHandler()
	if !a.tryMarkDomReadyDone() {
		logger.SugaredLogger.Warn("跳过重复 domReady 初始化: runtime 已完成注册")
		return
	}

	// The Web bootstrap invokes domReady after the HTTP runtime is assembled.
	// Keep the guard because tests and compatibility startup hooks may call it again.
	config := a.services.Config.GetConfig()
	a.registerRealtimeRuntime(config)
	a.registerFundRuntime(config)
	a.registerMaintenanceRuntime()
	a.registerConfiguredCronRuntimes(config)
	if err := a.startSchedulerAfterAssembly(); err != nil {
		releaseinfo.MarkSchedulerReady(false)
		releaseinfo.MarkNotReady(err)
		logger.SugaredLogger.Errorf("scheduler assembly failed: %v", err)
		return
	}
	releaseinfo.MarkSchedulerReady(true)
	a.startImmediateRuntimeTasks(config)
	a.startMaintenanceRuntime(config)

	logger.SugaredLogger.Infof("domReady-cronEntrys:%+v", a.cronEntrys)
	a.emitDomReadyDone()
}

func (a *App) emitDomReadyDone() {
	a.goTask(func(ctx context.Context) {
		time.Sleep(2 * time.Second)
		if !releaseinfo.Readiness().Ready {
			return
		}
		emitEvent(ctx, "loadingMsg", "done")
	})
}

func (a *App) registerRealtimeRuntime(config *models.SettingConfig) {
	interval := config.RefreshInterval
	if interval <= 0 {
		interval = 1
	}
	if _, err := a.cron.AddFunc(fmt.Sprintf("@every %ds", interval+60), func() {
		a.services.Market.AnalyzeNews("", true)
	}); err != nil {
		a.recordSchedulerRegistrationError("NewsAnalyze", fmt.Sprintf("@every %ds", interval+60), err)
		logger.SugaredLogger.Errorf("注册 NewsAnalyze 定时任务失败: %v", err)
	}

	a.registerCronTask("MonitorStockPrices", fmt.Sprintf("@every %ds", interval), func() {
		MonitorStockPrices(a)
	})
	a.reloadMarketNewsPolling(config, false)
}

func (a *App) reloadMarketNewsPolling(config *models.SettingConfig, immediate bool) {
	for _, key := range []string{"GetNewTelegraph", "newSinaNews", "tradingViewNews"} {
		if entryID, exists := a.getCronEntry(key); exists {
			a.cron.Remove(entryID)
			a.deleteCronEntry(key)
		}
	}
	if !marketNewsPollingEnabled(config) {
		return
	}
	interval := config.RefreshInterval
	if interval <= 0 {
		interval = 1
	}
	a.registerNewsPollingCrons(interval)
	if immediate {
		a.goTask(func(context.Context) { a.services.Market.TelegraphList(30) })
		a.goTask(func(context.Context) { a.services.Market.GetSinaNews(30) })
		a.goTask(func(context.Context) { a.services.Market.TradingViewNews() })
	}
}

func (a *App) registerNewsPollingCrons(interval int64) {
	pollingInterval := marketNewsPollingInterval(interval)
	spec := fmt.Sprintf("@every %ds", int64(pollingInterval/time.Second))
	a.registerCronTask("GetNewTelegraph", spec, func() {
		news := a.services.Market.TelegraphList(30)
		emitEvent(a.taskContext(), "newTelegraph", news)
	})

	a.registerCronTask("newSinaNews", spec, func() {
		news := a.services.Market.GetSinaNews(30)
		emitEvent(a.taskContext(), "newSinaNews", news)
	})

	a.registerCronTask("tradingViewNews", spec, func() {
		news := a.services.Market.TradingViewNews()
		emitEvent(a.taskContext(), "tradingViewNews", news)
	})
}

func marketNewsPollingEnabled(config *models.SettingConfig) bool {
	if config == nil || config.Settings == nil {
		return false
	}
	if config.EnableNews {
		return true
	}
	return config.AICapitalDeploymentEnabled
}

func marketNewsPollingInterval(configuredSeconds int64) time.Duration {
	interval := time.Duration(configuredSeconds+10) * time.Second
	if interval < marketNewsPollingMinimumInterval {
		return marketNewsPollingMinimumInterval
	}
	return interval
}

func (a *App) registerFundRuntime(config *models.SettingConfig) {
	if !config.EnableFund {
		return
	}
	a.registerCronTask("MonitorFundPrices", "@every 60s", func() {
		MonitorFundPrices(a)
	})
}

func (a *App) startImmediateRuntimeTasks(config *models.SettingConfig) {
	a.goTask(func(context.Context) { a.services.Market.EnsureMarketDataSelfCheck("app_dom_ready") })
	a.goTask(func(context.Context) { MonitorStockPrices(a) })
	if marketNewsPollingEnabled(config) {
		a.goTask(func(context.Context) { a.services.Market.TelegraphList(30) })
		a.goTask(func(context.Context) { a.services.Market.GetSinaNews(30) })
		a.goTask(func(context.Context) { a.services.Market.TradingViewNews() })
	}
	if config.EnableFund {
		a.goTask(func(context.Context) { MonitorFundPrices(a) })
		a.goTask(func(context.Context) { a.services.Fund.AllFund() })
	}
}

func (a *App) registerMaintenanceRuntime() {
	if _, err := a.cron.AddFunc("0 0 2 * * *", func() {
		logger.SugaredLogger.Errorf("Checking for updates...")
		a.checkStockBaseInfo(a.ctx)
	}); err != nil {
		a.recordSchedulerRegistrationError("CheckStockBaseInfo", "0 0 2 * * *", err)
		logger.SugaredLogger.Errorf("注册 CheckStockBaseInfo 定时任务失败: %v", err)
	}
}

func (a *App) startMaintenanceRuntime(config *models.SettingConfig) {
	if config.UpdateBasicInfoOnStart {
		a.goTask(a.checkStockBaseInfo)
		a.goTask(func(context.Context) { a.services.Stock.RefreshIndexBaseInfo() })
	}
}

func (a *App) registerConfiguredCronRuntimes(config *models.SettingConfig) {
	a.reloadAIAnalysisCron(config, true)
	a.reloadResearch2Cron(config)
	a.registerThemeLifecycleCron()
}

func (a *App) registerCronTask(key, spec string, task func()) {
	entryID, err := a.cron.AddFunc(spec, task)
	if err != nil {
		a.recordSchedulerRegistrationError(key, spec, err)
		logger.SugaredLogger.Errorf("AddFunc error:%s", err.Error())
		return
	}
	a.setCronEntry(key, entryID)
}
