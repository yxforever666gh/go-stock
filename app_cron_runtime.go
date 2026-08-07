package main

import (
	"fmt"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	"os"
	"strings"
	"time"

	"golang.org/x/exp/slices"
)

func isEnvEnabled(key string) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch value {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func normalizeSummaryCronTimes(input string) []string {
	replacer := strings.NewReplacer("，", ",", "；", ",", ";", ",", "\n", ",", "\t", ",", " ", "")
	raw := replacer.Replace(input)
	if strings.TrimSpace(raw) == "" {
		raw = defaultMarketSummaryCronTimes
	}

	seen := make(map[string]struct{})
	times := make([]string, 0)
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		t, err := time.Parse("15:04", item)
		if err != nil {
			continue
		}
		key := t.Format("15:04")
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		times = append(times, key)
	}

	if len(times) == 0 {
		return []string{"09:40", "11:30", "14:30"}
	}
	slices.Sort(times)
	return times
}

func buildSummaryCronSpec(hhmm string) string {
	parts := strings.Split(hhmm, ":")
	if len(parts) != 2 {
		return ""
	}
	return fmt.Sprintf("CRON_TZ=Asia/Shanghai 0 %s %s * * 1-5", parts[1], parts[0])
}

func buildYieldEmailCronSpec(hhmm string) string {
	parts := strings.Split(hhmm, ":")
	if len(parts) != 2 {
		return ""
	}
	return fmt.Sprintf("CRON_TZ=Asia/Shanghai 0 %s %s * * 1-5", parts[1], parts[0])
}

func (a *App) reloadSummaryStockNewsCron(settingConfig *models.SettingConfig) {
	for key, entryID := range a.snapshotCronEntries() {
		if !strings.HasPrefix(key, summaryStockNewsEntryPrefix) {
			continue
		}
		a.cron.Remove(entryID)
		a.deleteCronEntry(key)
	}

	if settingConfig == nil || settingConfig.Settings == nil {
		return
	}
	if !settingConfig.MarketSummaryCronEnabled {
		logger.SugaredLogger.Infof("市场资讯AI总结定时任务已关闭")
		return
	}

	times := normalizeSummaryCronTimes(settingConfig.MarketSummaryCronTimes)
	for idx, hhmm := range times {
		spec := buildSummaryCronSpec(hhmm)
		if strings.TrimSpace(spec) == "" {
			continue
		}
		entryID, err := a.cron.AddFunc(spec, func() {
			a.runScheduledSummaryStockNews()
		})
		if err != nil {
			a.recordSchedulerRegistrationError("SummaryStockNews:"+hhmm, spec, err)
			logger.SugaredLogger.Errorf("添加市场资讯AI总结定时任务失败:time=%s cron=%s", hhmm, spec)
			continue
		}
		key := fmt.Sprintf("%s%d", summaryStockNewsEntryPrefix, idx)
		a.setCronEntry(key, entryID)
	}
	logger.SugaredLogger.Infof("市场资讯AI总结定时任务生效: %v", times)
}

func (a *App) reloadYieldEmailCron(settingConfig *models.SettingConfig) {
	a.yieldEmailCronMu.Lock()
	defer a.yieldEmailCronMu.Unlock()

	for key, entryID := range a.snapshotCronEntries() {
		if !strings.HasPrefix(key, yieldEmailCronEntryPrefix) {
			continue
		}
		a.cron.Remove(entryID)
		a.deleteCronEntry(key)
	}

	if settingConfig == nil || settingConfig.Settings == nil {
		return
	}
	if !settingConfig.YieldEmailEnable || !settingConfig.YieldEmailCronEnabled {
		logger.SugaredLogger.Infof("最新 AI 分析报告定时发送已关闭")
		return
	}

	times, err := a.services.Market.NormalizeYieldEmailCronTimes(settingConfig.YieldEmailCronTimes)
	if err != nil {
		a.recordSchedulerRegistrationError("YieldEmail", settingConfig.YieldEmailCronTimes, err)
		logger.SugaredLogger.Errorf("最新 AI 分析报告定时发送时间无效: %v", err)
		return
	}
	if len(times) == 0 {
		logger.SugaredLogger.Warn("最新 AI 分析报告定时发送未配置时间，跳过注册")
		return
	}

	for idx, hhmm := range times {
		spec := buildYieldEmailCronSpec(hhmm)
		if strings.TrimSpace(spec) == "" {
			continue
		}
		entryID, addErr := a.cron.AddFunc(spec, func() {
			a.runScheduledLatestAIAnalysisReportEmail()
		})
		if addErr != nil {
			a.recordSchedulerRegistrationError("YieldEmail:"+hhmm, spec, addErr)
			logger.SugaredLogger.Errorf("添加最新 AI 分析报告定时任务失败: time=%s cron=%s err=%v", hhmm, spec, addErr)
			continue
		}
		key := fmt.Sprintf("%s%d", yieldEmailCronEntryPrefix, idx)
		a.setCronEntry(key, entryID)
	}
	logger.SugaredLogger.Infof("最新 AI 分析报告定时发送已生效: %v", times)
}

func (a *App) enableSummaryStockNewsTestCron() {
	if !isEnvEnabled("GO_STOCK_MARKET_SUMMARY_TEST_1MIN") {
		return
	}
	if _, exists := a.getCronEntry(summaryStockNewsTestEntryKey); exists {
		return
	}
	entryID, err := a.cron.AddFunc("@every 60s", func() {
		a.runSummaryStockNewsTestOnce()
	})
	if err != nil {
		a.recordSchedulerRegistrationError(summaryStockNewsTestEntryKey, "@every 60s", err)
		logger.SugaredLogger.Errorf("添加市场资讯AI总结1分钟测试任务失败: %v", err)
		return
	}
	a.setCronEntry(summaryStockNewsTestEntryKey, entryID)
	logger.SugaredLogger.Infof("市场资讯AI总结1分钟测试任务已启用")
}

func (a *App) createCronTaskRun(taskRun *models.CronTaskRun) {
	if err := a.services.Scheduler.CreateTaskRun(a.ctx, taskRun); err != nil {
		logger.SugaredLogger.Errorf("persist cron task run failed: %v", err)
	}
}

func (a *App) updateCronTaskRun(taskRun *models.CronTaskRun, status, errorMessage string) {
	if taskRun == nil || taskRun.ID == 0 {
		return
	}
	taskRun.Status = status
	taskRun.ErrorMessage = errorMessage
	if err := a.services.Scheduler.UpdateTaskRun(a.ctx, taskRun); err != nil {
		logger.SugaredLogger.Errorf("update cron task run failed: %v", err)
	}
}

func (a *App) runSummaryStockNewsTestOnce() {
	setting := a.services.Config.GetConfig()
	if setting == nil || setting.Settings == nil {
		logger.SugaredLogger.Warn("跳过市场资讯AI总结1分钟测试: 配置为空")
		return
	}
	if !setting.OpenAiEnable {
		logger.SugaredLogger.Warn("跳过市场资讯AI总结1分钟测试: OpenAI未启用")
		return
	}
	if len(setting.AiConfigs) == 0 {
		logger.SugaredLogger.Warn("跳过市场资讯AI总结1分钟测试: 未配置AI模型")
		return
	}

	if a.isSummaryTaskBusy() {
		logger.SugaredLogger.Warn("跳过市场资讯AI总结1分钟测试: 上一次任务仍在执行")
		return
	}

	aiConfigId := a.services.AI.ResolveDefaultAIConfigID()
	if aiConfigId <= 0 {
		logger.SugaredLogger.Warn("跳过市场资讯AI总结1分钟测试: 无可用AI配置")
		return
	}
	start := time.Now()
	taskRun := &models.CronTaskRun{
		TaskName:    "market_summary_test_1min",
		TriggeredAt: start,
		Status:      "started",
		Attempts:    1,
		AiConfigId:  aiConfigId,
	}
	a.createCronTaskRun(taskRun)

	marketSummaryQuestion := a.services.AI.NormalizeMarketSummaryQuestion(setting.QuestionTemplate)
	res := a.runSummaryStockNewsTask(marketSummaryQuestion, aiConfigId, nil, true, false)

	status := "failed"
	errMsg := summarizeSummaryRunError(res)
	if usableMarketSummaryRunResult(res) {
		status = "success"
		errMsg = ""
	}
	a.updateCronTaskRun(taskRun, status, errMsg)

	logger.SugaredLogger.Infof("市场资讯AI总结1分钟测试任务完成 aiConfigId=%d status=%s", aiConfigId, status)
}

func (a *App) runScheduledSummaryStockNews() {
	defer PanicHandler()

	setting := a.services.Config.GetConfig()
	if setting == nil || setting.Settings == nil {
		a.createCronTaskRun(&models.CronTaskRun{
			TaskName:     "market_summary",
			TriggeredAt:  time.Now(),
			Status:       "skipped",
			ErrorMessage: "配置为空",
		})
		logger.SugaredLogger.Warn("跳过市场资讯AI总结定时任务: 配置为空")
		return
	}
	if !setting.OpenAiEnable {
		a.createCronTaskRun(&models.CronTaskRun{
			TaskName:     "market_summary",
			TriggeredAt:  time.Now(),
			Status:       "skipped",
			ErrorMessage: "OpenAI未启用",
		})
		logger.SugaredLogger.Warn("跳过市场资讯AI总结定时任务: OpenAI未启用")
		return
	}
	if len(setting.AiConfigs) == 0 {
		a.createCronTaskRun(&models.CronTaskRun{
			TaskName:     "market_summary",
			TriggeredAt:  time.Now(),
			Status:       "skipped",
			ErrorMessage: "未配置AI模型",
		})
		logger.SugaredLogger.Warn("跳过市场资讯AI总结定时任务: 未配置AI模型")
		return
	}
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.Local
	}
	now := time.Now().In(loc)
	if !a.services.Market.IsCNOpenTradeDay(now) {
		a.createCronTaskRun(&models.CronTaskRun{
			TaskName:     "market_summary",
			TriggeredAt:  now,
			Status:       "skipped",
			ErrorMessage: fmt.Sprintf("非A股开盘日 day=%s", now.Format("2006-01-02")),
		})
		logger.SugaredLogger.Infof("跳过市场资讯AI总结定时任务: 非A股开盘日 day=%s", now.Format("2006-01-02"))
		return
	}

	if a.isSummaryTaskBusy() {
		a.createCronTaskRun(&models.CronTaskRun{
			TaskName:     "market_summary",
			TriggeredAt:  now,
			Status:       "skipped",
			ErrorMessage: "上一次任务仍在执行",
		})
		logger.SugaredLogger.Warn("跳过市场资讯AI总结定时任务: 上一次任务仍在执行")
		return
	}

	aiConfigId := a.services.AI.ResolveDefaultAIConfigID()
	taskRun := &models.CronTaskRun{
		TaskName:    "market_summary",
		TriggeredAt: now,
		Status:      "started",
		Attempts:    1,
		AiConfigId:  aiConfigId,
	}
	a.createCronTaskRun(taskRun)

	logger.SugaredLogger.Infof("开始执行市场资讯AI总结定时任务 aiConfigId=%d", aiConfigId)
	marketSummaryQuestion := a.services.AI.NormalizeMarketSummaryQuestion(setting.QuestionTemplate)
	res := a.runSummaryStockNewsTask(marketSummaryQuestion, aiConfigId, nil, true, false)

	status := "failed"
	errMsg := summarizeSummaryRunError(res)
	if usableMarketSummaryRunResult(res) {
		status = "success"
		errMsg = ""
	}

	a.updateCronTaskRun(taskRun, status, errMsg)

	logger.SugaredLogger.Infof("市场资讯AI总结定时任务执行完成 aiConfigId=%d status=%s", aiConfigId, status)
}

func (a *App) runScheduledLatestAIAnalysisReportEmail() {
	defer PanicHandler()

	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.Local
	}
	now := time.Now().In(loc)
	createTaskRun := func(status, message string) *models.CronTaskRun {
		taskRun := &models.CronTaskRun{
			TaskName:     "latest_ai_analysis_email",
			TriggeredAt:  now,
			Status:       status,
			Attempts:     1,
			ErrorMessage: message,
		}
		a.createCronTaskRun(taskRun)
		return taskRun
	}
	updateTaskRun := func(taskRun *models.CronTaskRun, status, message string) {
		if taskRun == nil || taskRun.ID == 0 {
			createTaskRun(status, message)
			return
		}
		a.updateCronTaskRun(taskRun, status, message)
	}

	if !a.tryAcquireYieldEmailTask() {
		createTaskRun("skipped", "上一次邮件发送任务仍在执行")
		logger.SugaredLogger.Warn("跳过最新 AI 分析报告定时发送: 上一次邮件发送任务仍在执行")
		return
	}
	defer a.releaseYieldEmailTask()

	taskRun := createTaskRun("started", "")

	setting := a.services.Config.GetConfig()
	if setting == nil || setting.Settings == nil {
		updateTaskRun(taskRun, "skipped", "配置为空")
		logger.SugaredLogger.Warn("跳过最新 AI 分析报告定时发送: 配置为空")
		return
	}
	if !setting.YieldEmailEnable {
		updateTaskRun(taskRun, "skipped", "邮箱推送收益率未启用")
		logger.SugaredLogger.Warn("跳过最新 AI 分析报告定时发送: 邮箱推送收益率未启用")
		return
	}
	if !setting.YieldEmailCronEnabled {
		updateTaskRun(taskRun, "skipped", "最新 AI 分析报告定时发送未启用")
		logger.SugaredLogger.Warn("跳过最新 AI 分析报告定时发送: 定时开关未启用")
		return
	}
	open, tradeErr := a.services.Market.IsCNOpenTradeDayStrict(now)
	if tradeErr != nil {
		msg := fmt.Sprintf("交易日历不可用，跳过发送 day=%s err=%v", now.Format("2006-01-02"), tradeErr)
		updateTaskRun(taskRun, "skipped", msg)
		logger.SugaredLogger.Warnf("跳过最新 AI 分析报告定时发送: %s", msg)
		return
	}
	if !open {
		msg := fmt.Sprintf("非A股开盘日 day=%s", now.Format("2006-01-02"))
		updateTaskRun(taskRun, "skipped", msg)
		logger.SugaredLogger.Infof("跳过最新 AI 分析报告定时发送: %s", msg)
		return
	}

	minuteStart := now.Truncate(time.Minute)
	minuteEnd := minuteStart.Add(time.Minute)
	earliestTask, earliestErr := a.services.Scheduler.EarliestTaskRun(a.ctx, "latest_ai_analysis_email", minuteStart, minuteEnd, []string{"started", "success"})
	if earliestErr == nil && earliestTask.ID != taskRun.ID {
		msg := fmt.Sprintf("同一分钟内已有更早的任务记录 id=%d，跳过重复发送", earliestTask.ID)
		updateTaskRun(taskRun, "skipped", msg)
		logger.SugaredLogger.Warnf("跳过最新 AI 分析报告定时发送: %s", msg)
		return
	}

	result, err := a.services.AI.SendLatestAIAnalysisReportEmailForCron()
	if err != nil {
		updateTaskRun(taskRun, "failed", err.Error())
		logger.SugaredLogger.Warnf("最新 AI 分析报告定时发送失败: %v", err)
		return
	}
	if result == nil {
		updateTaskRun(taskRun, "failed", "未生成可确认的发送结果")
		logger.SugaredLogger.Warn("最新 AI 分析报告定时发送失败: 未生成可确认的发送结果")
		return
	}

	title := "最新 AI 分析报告"
	if name := strings.TrimSpace(result.StockName); name != "" && strings.TrimSpace(result.StockCode) != "" {
		title = fmt.Sprintf("%s [%s]", name, strings.TrimSpace(result.StockCode))
	} else if name != "" {
		title = name
	} else if code := strings.TrimSpace(result.StockCode); code != "" {
		title = code
	}
	msg := "发送成功，报告=" + title
	updateTaskRun(taskRun, "success", msg)
	logger.SugaredLogger.Infof("最新 AI 分析报告定时发送成功: report=%s", title)
}
