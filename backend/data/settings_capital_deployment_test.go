package data

import (
	"path/filepath"
	"testing"

	"go-stock/backend/db"
)

func TestCapitalDeploymentSettingsSaveDisablesLegacySchedulerAndPreservesTimes(t *testing.T) {
	initDatabaseForTest(t, filepath.Join(t.TempDir(), "capital-deployment-settings.db"))
	config := GetSettingConfig()
	if config == nil || config.Settings == nil {
		t.Fatal("settings config is unavailable")
	}
	if err := db.Dao.Model(&Settings{}).Where("id = ?", config.ID).Updates(map[string]any{
		"ai_analysis_enabled": true,
		"ai_analysis_times":   "09:31,14:01",
	}).Error; err != nil {
		t.Fatal(err)
	}

	config = GetSettingConfig()
	config.AICapitalDeploymentEnabled = false
	config.AITargetCapitalUtilization = 0.80
	config.AIMaxImmediateBuysPerRun = 1
	config.AIReanalysisIntervalMinutes = 60
	config.AIAnalysisTimes = "malformed legacy value must remain untouched"
	legacyEnabled := true
	config.AIAnalysisAutoEnabled = &legacyEnabled
	if result := UpdateConfig(config); result != "保存成功！" {
		t.Fatalf("save result=%q", result)
	}

	var stored Settings
	if err := db.Dao.First(&stored, config.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.AIAnalysisEnabled {
		t.Fatal("retired fixed-time scheduler was re-enabled")
	}
	if stored.AIAnalysisTimes != "09:31,14:01" {
		t.Fatalf("legacy analysis times=%q", stored.AIAnalysisTimes)
	}
	if stored.AICapitalDeploymentEnabled || stored.AITargetCapitalUtilization != 0.80 || stored.AIMaxImmediateBuysPerRun != 1 || stored.AIReanalysisIntervalMinutes != 60 {
		t.Fatalf("stored capital deployment settings=%+v", stored)
	}
}

func TestFreshSettingsUseCapitalDeploymentDefaultsAndDisableLegacyScheduler(t *testing.T) {
	settings := &Settings{}
	applySettingDefaults(settings)
	if settings.AIAnalysisEnabled {
		t.Fatal("fresh settings enabled retired fixed-time scheduler")
	}
	if !settings.AICapitalDeploymentEnabled || settings.AITargetCapitalUtilization != 0.90 || settings.AIMaxImmediateBuysPerRun != 2 || settings.AIReanalysisIntervalMinutes != 30 {
		t.Fatalf("fresh settings=%+v", settings)
	}
}
