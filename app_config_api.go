package main

import (
	"fmt"
	"strings"

	"go-stock/backend/models"
)

func (a *App) updateConfig(settingConfig *models.SettingConfig) string {
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
