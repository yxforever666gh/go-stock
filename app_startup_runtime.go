package main

import (
	"context"
	"fmt"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	"go-stock/internal/releaseinfo"
	"time"
)

func (a *App) domReady(ctx context.Context) {
	defer PanicHandler()
	if !a.tryMarkDomReadyDone() {
		logger.SugaredLogger.Warn("跳过重复 domReady 初始化: runtime 已完成注册")
		return
	}

	// Web mode invokes domReady directly without the desktop startup hook. The
	// once-guard makes this a fallback only; desktop execution starts earlier.
	a.registerMarketSummaryV150ExecutionRuntime()
	config := a.services.Config.GetConfig()
	a.registerRealtimeRuntime(config)
	a.registerFundRuntime(config)
	a.registerTelegraphRuntime(config)
	a.registerMaintenanceRuntime()
	a.registerConfiguredCronRuntimes(config)
	a.registerFollowAnalysisCrons()
	if err := a.startSchedulerAfterAssembly(); err != nil {
		releaseinfo.MarkSchedulerReady(false)
		releaseinfo.MarkNotReady(err)
		logger.SugaredLogger.Errorf("scheduler assembly failed: %v", err)
		return
	}
	releaseinfo.MarkSchedulerReady(true)
	a.startMarketSummaryV150ExecutionRuntime()
	a.updateBasicInfo()
	a.startImmediateRuntimeTasks(config)
	a.startMaintenanceRuntime(config)

	logger.SugaredLogger.Infof("domReady-cronEntrys:%+v", a.cronEntrys)
	a.emitDomReadyDone()
}

func (a *App) emitDomReadyDone() {
	go func() {
		time.Sleep(2 * time.Second)
		if !releaseinfo.Readiness().Ready {
			return
		}
		emitEvent(a.ctx, "loadingMsg", "done")
	}()
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
}

func (a *App) registerNewsPollingCrons(interval int64, enablePush bool) {
	a.registerCronTask("GetNewTelegraph", fmt.Sprintf("@every %ds", interval+10), func() {
		news := a.services.Market.TelegraphList(30)
		if enablePush {
			go a.NewsPush(news)
		}
		go emitEvent(a.ctx, "newTelegraph", news)
	})

	a.registerCronTask("newSinaNews", fmt.Sprintf("@every %ds", interval+10), func() {
		news := a.services.Market.GetSinaNews(30)
		if enablePush {
			go a.NewsPush(news)
		}
		go emitEvent(a.ctx, "newSinaNews", news)
	})

	a.registerCronTask("tradingViewNews", fmt.Sprintf("@every %ds", interval+10), func() {
		news := a.services.Market.TradingViewNews()
		if enablePush {
			go a.NewsPush(news)
		}
		go emitEvent(a.ctx, "tradingViewNews", news)
	})
}

func (a *App) registerFundRuntime(config *models.SettingConfig) {
	if !config.EnableFund {
		return
	}
	a.registerCronTask("MonitorFundPrices", "@every 60s", func() {
		MonitorFundPrices(a)
	})
}

func (a *App) registerTelegraphRuntime(config *models.SettingConfig) {
	return
}

func (a *App) startImmediateRuntimeTasks(config *models.SettingConfig) {
	go a.services.Market.EnsureMarketDataSelfCheck("app_dom_ready")
	go MonitorStockPrices(a)
	if config.EnableFund {
		go MonitorFundPrices(a)
		go a.services.Fund.AllFund()
	}
}

func (a *App) registerMaintenanceRuntime() {
	if _, err := a.cron.AddFunc("0 0 2 * * *", func() {
		logger.SugaredLogger.Errorf("Checking for updates...")
		a.CheckStockBaseInfo(a.ctx)
	}); err != nil {
		a.recordSchedulerRegistrationError("CheckStockBaseInfo", "0 0 2 * * *", err)
		logger.SugaredLogger.Errorf("注册 CheckStockBaseInfo 定时任务失败: %v", err)
	}
}

func (a *App) startMaintenanceRuntime(config *models.SettingConfig) {
	if config.UpdateBasicInfoOnStart {
		go a.CheckStockBaseInfo(a.ctx)
	}
}

func (a *App) registerConfiguredCronRuntimes(config *models.SettingConfig) {
	a.reloadSummaryStockNewsCron(config)
	a.enableSummaryStockNewsTestCron()
}

func (a *App) registerFollowAnalysisCrons() {
	followList := a.services.Stock.GetFollowList(0)
	for _, follow := range *followList {
		if follow.Cron == nil || *follow.Cron == "" {
			continue
		}
		entryID, err := a.cron.AddFunc(*follow.Cron, a.AddCronTask(follow))
		if err != nil {
			a.recordSchedulerRegistrationError("FollowAnalysis:"+follow.StockCode, *follow.Cron, err)
			logger.SugaredLogger.Errorf("添加自动分析任务失败:%s cron=%s entryID:%v", follow.Name, *follow.Cron, entryID)
			continue
		}
		a.setCronEntry(follow.StockCode, entryID)
	}
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
