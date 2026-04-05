package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"go-stock/backend/data"
	"go-stock/backend/logger"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) UpdateConfig(settingConfig *data.SettingConfig) string {
	s1, _ := json.Marshal(settingConfig)
	logger.SugaredLogger.Infof("UpdateConfig:%s", s1)
	if settingConfig.RefreshInterval > 0 {
		if entryID, exists := a.getCronEntry("MonitorStockPrices"); exists {
			a.cron.Remove(entryID)
		}
		id, _ := a.cron.AddFunc(fmt.Sprintf("@every %ds", settingConfig.RefreshInterval), func() {
			MonitorStockPrices(a)
		})
		a.setCronEntry("MonitorStockPrices", id)
	}

	res := a.services.Config.UpdateConfig(settingConfig)
	if strings.Contains(res, "保存成功") {
		a.reloadSummaryStockNewsCron(settingConfig)
		a.reloadYieldEmailCron(settingConfig)
	}
	return res
}

func (a *App) GetConfig() *data.SettingConfig {
	return a.services.Config.GetConfig()
}

func (a *App) ExportConfig() string {
	config := a.services.Config.ExportConfig()
	file, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:                "导出配置文件",
		CanCreateDirectories: true,
		DefaultFilename:      "config.json",
	})
	if err != nil {
		logger.SugaredLogger.Errorf("导出配置文件失败:%s", err.Error())
		return err.Error()
	}
	err = os.WriteFile(file, []byte(config), os.ModePerm)
	if err != nil {
		logger.SugaredLogger.Errorf("导出配置文件失败:%s", err.Error())
		return err.Error()
	}
	return "导出成功:" + file
}
