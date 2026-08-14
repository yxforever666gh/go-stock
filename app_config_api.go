package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"go-stock/backend/logger"
	"go-stock/backend/models"
)

func (a *App) UpdateConfig(settingConfig *models.SettingConfig) string {
	if settingConfig.RefreshInterval > 0 {
		if entryID, exists := a.getCronEntry("MonitorStockPrices"); exists {
			a.cron.Remove(entryID)
		}
		spec := fmt.Sprintf("@every %ds", settingConfig.RefreshInterval)
		id, err := a.cron.AddFunc(spec, func() {
			MonitorStockPrices(a)
		})
		if err != nil {
			a.recordSchedulerRegistrationError("MonitorStockPrices", spec, err)
			return "\u5237\u65b0\u5468\u671f\u65e0\u6548: " + err.Error()
		}
		a.setCronEntry("MonitorStockPrices", id)
	}

	res := a.services.Config.UpdateConfig(settingConfig)
	if strings.Contains(res, "\u4fdd\u5b58\u6210\u529f") {
		a.reloadAIAnalysisCron(settingConfig)
	}
	return res
}

func (a *App) GetConfig() *models.SettingConfig {
	return a.services.Config.GetConfig()
}

func (a *App) ExportConfig() string {
	config := a.services.Config.ExportConfig()
	file, err := saveFileWithDialog(a.ctx, runtimeSaveFileOptions{
		Title:                "\u5bfc\u51fa\u914d\u7f6e\u6587\u4ef6",
		CanCreateDirectories: true,
		DefaultFilename:      "config.json",
	})
	if err != nil {
		logger.SugaredLogger.Errorf("\u5bfc\u51fa\u914d\u7f6e\u6587\u4ef6\u5931\u8d25:%s", err.Error())
		return err.Error()
	}
	if err := os.WriteFile(file, []byte(config), os.ModePerm); err != nil {
		logger.SugaredLogger.Errorf("\u5bfc\u51fa\u914d\u7f6e\u6587\u4ef6\u5931\u8d25:%s", err.Error())
		return err.Error()
	}
	return "\u5bfc\u51fa\u6210\u529f:" + file
}

func (a *App) TestAIConfig(aiConfigID int) *models.AIModelTestResult {
	return a.services.AI.TestAIConfig(context.Background(), aiConfigID)
}
