package main

import (
	"context"
	"fmt"
	"go-stock/backend/data"
	"go-stock/backend/logger"
	appconfig "go-stock/internal/config"
	"time"
)

func (a *App) domReady(ctx context.Context) {
	defer PanicHandler()
	defer a.emitDomReadyDone()

	if !a.tryMarkDomReadyDone() {
		logger.SugaredLogger.Warn("跳过重复 domReady 初始化: runtime 已完成注册")
		return
	}

	updateBasicInfo()

	config := a.services.Config.GetConfig()
	a.registerRealtimeRuntime(config)
	a.registerFundRuntime(config)
	a.registerTelegraphRuntime(config)
	a.startImmediateRuntimeTasks(config)
	a.registerMaintenanceRuntime(config)
	a.registerConfiguredCronRuntimes(config)
	a.registerFollowAnalysisCrons()

	logger.SugaredLogger.Infof("domReady-cronEntrys:%+v", a.cronEntrys)
}

func (a *App) emitDomReadyDone() {
	go func() {
		time.Sleep(2 * time.Second)
		emitEvent(a.ctx, "loadingMsg", "done")
	}()
}

func (a *App) registerRealtimeRuntime(config *data.SettingConfig) {
	go func() {
		interval := config.RefreshInterval
		if interval <= 0 {
			interval = 1
		}
		if _, err := a.cron.AddFunc(fmt.Sprintf("@every %ds", interval+60), func() {
			data.NewsAnalyze("", true)
		}); err != nil {
			logger.SugaredLogger.Errorf("注册 NewsAnalyze 定时任务失败: %v", err)
		}

		a.registerCronTask("MonitorStockPrices", fmt.Sprintf("@every %ds", interval), func() {
			MonitorStockPrices(a)
		})
	}()
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

func (a *App) registerFundRuntime(config *data.SettingConfig) {
	go func() {
		if !config.EnableFund {
			return
		}
		a.registerCronTask("MonitorFundPrices", "@every 60s", func() {
			MonitorFundPrices(a)
		})
	}()
}

func (a *App) registerTelegraphRuntime(config *data.SettingConfig) {
	return
}

func (a *App) startImmediateRuntimeTasks(config *data.SettingConfig) {
	go data.EnsureDiemengSelfCheckAsync("app_dom_ready")
	go func() {
		stats, err := data.RepairHistoricalLegacySkippedRecommendations(time.Now())
		if err != nil {
			logger.SugaredLogger.Errorf("历史跳过记录修复失败: %v", err)
			return
		}
		if stats.RecalcQueuedCodes > 0 || stats.RecordStatesReset > 0 || stats.AggregateReset > 0 {
			logger.SugaredLogger.Infof(
				"历史跳过记录修复完成: scanned=%d stillSkipped=%d overrideKept=%d recordReset=%d aggregateReset=%d queuedCodes=%d",
				stats.Scanned,
				stats.StillSkipped,
				stats.OverrideKept,
				stats.RecordStatesReset,
				stats.AggregateReset,
				stats.RecalcQueuedCodes,
			)
		}
	}()
	go MonitorStockPrices(a)
	if config.EnableFund {
		go MonitorFundPrices(a)
		go a.services.Fund.AllFund()
	}
}

func (a *App) registerMaintenanceRuntime(config *data.SettingConfig) {
	go func() {
		if appconfig.Load().Update.SelfUpdateEnabled && config.CheckUpdate {
			a.CheckUpdate(0)
		}
		go a.CheckStockBaseInfo(a.ctx)

		if _, err := a.cron.AddFunc("0 0 2 * * *", func() {
			logger.SugaredLogger.Errorf("Checking for updates...")
			a.CheckStockBaseInfo(a.ctx)
		}); err != nil {
			logger.SugaredLogger.Errorf("注册 CheckStockBaseInfo 定时任务失败: %v", err)
		}
		if appconfig.Load().Update.SelfUpdateEnabled && config.CheckUpdate {
			if _, err := a.cron.AddFunc("30 05 8,12,20 * * *", func() {
				logger.SugaredLogger.Errorf("Checking for updates...")
				a.CheckUpdate(0)
			}); err != nil {
				logger.SugaredLogger.Errorf("注册 CheckUpdate 定时任务失败: %v", err)
			}
		}
	}()
}

func (a *App) registerConfiguredCronRuntimes(config *data.SettingConfig) {
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
			logger.SugaredLogger.Errorf("添加自动分析任务失败:%s cron=%s entryID:%v", follow.Name, *follow.Cron, entryID)
			continue
		}
		a.setCronEntry(follow.StockCode, entryID)
	}
}

func (a *App) registerCronTask(key, spec string, task func()) {
	entryID, err := a.cron.AddFunc(spec, task)
	if err != nil {
		logger.SugaredLogger.Errorf("AddFunc error:%s", err.Error())
		return
	}
	a.setCronEntry(key, entryID)
}
