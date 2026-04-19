package data

import (
	"path/filepath"
	"testing"

	"go-stock/backend/db"
	"gorm.io/gorm"
)

func initSettingsTestDB(t *testing.T) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "settings.db")
	db.Init(dbPath)
	if err := db.Dao.AutoMigrate(&Settings{}, &AIConfig{}); err != nil {
		t.Fatalf("migrate failed: %v", err)
	}
	_ = db.Dao.Exec("DELETE FROM settings").Error
	_ = db.Dao.Exec("DELETE FROM ai_config").Error
}

func TestUpdateConfigPersistsAIConfigSortOrder(t *testing.T) {
	initSettingsTestDB(t)

	res := UpdateConfig(&SettingConfig{
		Settings: &Settings{
			OpenAiEnable:   true,
			AkshareEnabled: true,
		},
		AiConfigs: []*AIConfig{
			{
				Name:      "second",
				BaseUrl:   "https://second.example.com",
				ApiKey:    "second-key",
				ModelName: "second-model",
			},
			{
				Name:      "first",
				BaseUrl:   "https://first.example.com",
				ApiKey:    "first-key",
				ModelName: "first-model",
			},
		},
	})
	if res != "保存成功！" {
		t.Fatalf("unexpected save result: %s", res)
	}

	cfg := GetSettingConfig()
	if cfg == nil || len(cfg.AiConfigs) != 2 {
		t.Fatalf("unexpected ai config count: %+v", cfg)
	}
	if cfg.AiConfigs[0].Name != "second" || cfg.AiConfigs[0].Sort != 1 {
		t.Fatalf("unexpected first ai config: %+v", cfg.AiConfigs[0])
	}
	if cfg.AiConfigs[1].Name != "first" || cfg.AiConfigs[1].Sort != 2 {
		t.Fatalf("unexpected second ai config: %+v", cfg.AiConfigs[1])
	}

	reordered := UpdateConfig(&SettingConfig{
		Settings: &Settings{
			Model:          gorm.Model{ID: cfg.Settings.ID},
			OpenAiEnable:   true,
			AkshareEnabled: true,
		},
		AiConfigs: []*AIConfig{
			{
				ID:        cfg.AiConfigs[1].ID,
				Name:      cfg.AiConfigs[1].Name,
				BaseUrl:   cfg.AiConfigs[1].BaseUrl,
				ApiKey:    cfg.AiConfigs[1].ApiKey,
				ModelName: cfg.AiConfigs[1].ModelName,
			},
			{
				ID:        cfg.AiConfigs[0].ID,
				Name:      cfg.AiConfigs[0].Name,
				BaseUrl:   cfg.AiConfigs[0].BaseUrl,
				ApiKey:    cfg.AiConfigs[0].ApiKey,
				ModelName: cfg.AiConfigs[0].ModelName,
			},
		},
	})
	if reordered != "保存成功！" {
		t.Fatalf("unexpected reorder result: %s", reordered)
	}

	cfg = GetSettingConfig()
	if cfg == nil || len(cfg.AiConfigs) != 2 {
		t.Fatalf("unexpected ai config count after reorder: %+v", cfg)
	}
	if cfg.AiConfigs[0].Name != "first" || cfg.AiConfigs[0].Sort != 1 {
		t.Fatalf("unexpected reordered first ai config: %+v", cfg.AiConfigs[0])
	}
	if cfg.AiConfigs[1].Name != "second" || cfg.AiConfigs[1].Sort != 2 {
		t.Fatalf("unexpected reordered second ai config: %+v", cfg.AiConfigs[1])
	}
}

func TestUpdateConfigReplacesOldAIConfigsWithNewOnes(t *testing.T) {
	initSettingsTestDB(t)

	res := UpdateConfig(&SettingConfig{
		Settings: &Settings{
			OpenAiEnable:   true,
			AkshareEnabled: true,
		},
		AiConfigs: []*AIConfig{
			{
				Name:      "old-one",
				BaseUrl:   "https://old-one.example.com",
				ApiKey:    "old-one-key",
				ModelName: "old-one-model",
			},
			{
				Name:      "old-two",
				BaseUrl:   "https://old-two.example.com",
				ApiKey:    "old-two-key",
				ModelName: "old-two-model",
			},
		},
	})
	if res != "保存成功！" {
		t.Fatalf("unexpected initial save result: %s", res)
	}

	current := GetSettingConfig()
	if current == nil || current.Settings == nil {
		t.Fatalf("expected current settings after initial save: %+v", current)
	}

	replaced := UpdateConfig(&SettingConfig{
		Settings: &Settings{
			Model:          gorm.Model{ID: current.Settings.ID},
			OpenAiEnable:   true,
			AkshareEnabled: true,
		},
		AiConfigs: []*AIConfig{
			{
				Name:      "new-only",
				BaseUrl:   "https://new-only.example.com",
				ApiKey:    "new-only-key",
				ModelName: "new-only-model",
			},
		},
	})
	if replaced != "保存成功！" {
		t.Fatalf("unexpected replace result: %s", replaced)
	}

	cfg := GetSettingConfig()
	if cfg == nil || len(cfg.AiConfigs) != 1 {
		t.Fatalf("unexpected ai config count after replace: %+v", cfg)
	}
	if cfg.AiConfigs[0].Name != "new-only" || cfg.AiConfigs[0].Sort != 1 {
		t.Fatalf("unexpected replaced ai config: %+v", cfg.AiConfigs[0])
	}
}
