package data

import (
	"path/filepath"
	"strings"
	"testing"

	"go-stock/backend/db"
	"go-stock/backend/researchconfig"
)

func TestResolveAIAnalysisConfigUsesFirstEnabledRow(t *testing.T) {
	setting := &SettingConfig{AiConfigs: []*AIConfig{
		{ID: 1, Sort: 1, Disabled: true, Name: "disabled"},
		{ID: 2, Sort: 2, Name: "primary"},
		{ID: 3, Sort: 3, Name: "fallback"},
	}}
	selected, err := ResolveAIAnalysisConfig(setting)
	if err != nil {
		t.Fatal(err)
	}
	if selected.ID != 2 {
		t.Fatalf("selected ID = %d, want 2", selected.ID)
	}
	setting.AiConfigs[1].Disabled = true
	setting.AiConfigs[2].Disabled = true
	if _, err := ResolveAIAnalysisConfig(setting); err == nil {
		t.Fatal("all-disabled configuration must not resolve a model")
	}
}

func TestGlobalModelSettingsCannotReadOrDeleteCenterModels(t *testing.T) {
	initDatabaseForTest(t, filepath.Join(t.TempDir(), "settings.db"), testSchemaSettings)
	global := &AIConfig{Name: "global", Owner: researchconfig.Global}
	center := &AIConfig{Name: "r2", Owner: researchconfig.Research2}
	for _, model := range []*AIConfig{global, center} {
		if err := db.Dao.Create(model).Error; err != nil {
			t.Fatal(err)
		}
	}
	cfg := GetSettingConfig()
	if len(cfg.AiConfigs) != 1 || cfg.AiConfigs[0].ID != global.ID {
		t.Fatal("global settings exposed another center's models")
	}
	cfg.AiConfigs = []*AIConfig{center}
	if message := UpdateConfig(cfg); !strings.Contains(message, researchconfig.ErrModelOwnership.Error()) {
		t.Fatalf("cross-scope save accepted: %s", message)
	}
	cfg.AiConfigs = []*AIConfig{}
	if message := UpdateConfig(cfg); message != "保存成功！" {
		t.Fatal(message)
	}
	var savedCenter AIConfig
	if err := db.Dao.First(&savedCenter, center.ID).Error; err != nil || savedCenter.ArchivedAt != nil {
		t.Fatalf("global model removal changed center model: %v", err)
	}
	var historicalGlobal AIConfig
	if err := db.Dao.First(&historicalGlobal, global.ID).Error; err != nil || historicalGlobal.ArchivedAt == nil {
		t.Fatalf("removed model's historical ID not retained: %v", err)
	}
	if cfg := GetSettingConfig(); len(cfg.AiConfigs) != 0 {
		t.Fatal("global settings returned archived model")
	}
}
