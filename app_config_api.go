package main

import (
	"fmt"
	"strings"

	"go-stock/backend/models"
	"go-stock/internal/service"
)

func (a *App) updateConfig(settingConfig *models.SettingConfig) (string, error) {
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
			message := "\u5237\u65b0\u5468\u671f\u65e0\u6548: " + err.Error()
			return message, fmt.Errorf("%w: %s", service.ErrInvalidInput, message)
		}
		a.setCronEntry("MonitorStockPrices", id)
	}

	res, err := a.services.Config.UpdateConfig(settingConfig)
	if err != nil {
		return res, err
	}
	if strings.Contains(res, "\u4fdd\u5b58\u6210\u529f") {
		a.reloadMarketNewsPolling(settingConfig, true)
		a.reloadAIAnalysisCron(settingConfig, false)
		a.reloadResearch2Cron(settingConfig)
	}
	return res, nil
}
