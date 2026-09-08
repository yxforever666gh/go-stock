package main

import (
	"errors"
	"testing"

	"go-stock/backend/models"
	"go-stock/internal/service"
)

type failingConfigService struct{ service.ConfigService }

func (failingConfigService) UpdateConfig(*models.SettingConfig) (string, error) {
	return "更新配置失败", service.ErrOperationFailed
}

func TestFailedConfigSaveDoesNotReplaceScheduler(t *testing.T) {
	app := NewAppWithServices(service.AppServices{Config: failingConfigService{}})
	app.registerCronTask("MonitorStockPrices", "@every 1m", func() {})
	before, _ := app.getCronEntry("MonitorStockPrices")
	_, err := app.updateConfig(&models.SettingConfig{Settings: &models.Settings{RefreshInterval: 5}})
	if !errors.Is(err, service.ErrOperationFailed) {
		t.Fatalf("save err=%v", err)
	}
	after, exists := app.getCronEntry("MonitorStockPrices")
	if !exists || after != before || len(app.cron.Entries()) != 1 {
		t.Fatalf("failed save changed scheduler: before=%d after=%d entries=%d", before, after, len(app.cron.Entries()))
	}
}
