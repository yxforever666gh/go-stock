package data

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-stock/backend/db"
	"gorm.io/gorm"
)

func initSettingsTestDB(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "settings.db")
	db.Init(dbPath)
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := db.Dao.AutoMigrate(&Settings{}, &AIConfig{}); err != nil {
		t.Fatalf("migrate failed: %v", err)
	}
	_ = db.Dao.Exec("DELETE FROM settings").Error
	_ = db.Dao.Exec("DELETE FROM ai_config").Error
	return dbPath
}

func requireSettingsSave(t *testing.T, config *SettingConfig) {
	t.Helper()
	if result := UpdateConfig(config); result != "保存成功！" {
		t.Fatalf("unexpected save result: %s", result)
	}
}

func TestUpdateConfigPersistsAIConfigSortOrder(t *testing.T) {
	_ = initSettingsTestDB(t)

	res := UpdateConfig(&SettingConfig{
		Settings: &Settings{
			OpenAiEnable:   true,
			AkshareEnabled: true,
		},
		AiConfigs: []*AIConfig{
			{
				Name:        "second",
				BaseUrl:     "https://second.example.com",
				ApiKey:      "second-key",
				ModelName:   "second-model",
				ApiProtocol: AIAPIProtocolOpenAIResponses,
			},
			{
				Name:        "first",
				BaseUrl:     "https://first.example.com",
				ApiKey:      "first-key",
				ModelName:   "first-model",
				ApiProtocol: "unknown",
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
	if cfg.AiConfigs[0].ApiProtocol != AIAPIProtocolOpenAIResponses {
		t.Fatalf("unexpected first ai protocol: %+v", cfg.AiConfigs[0])
	}
	if cfg.AiConfigs[1].Name != "first" || cfg.AiConfigs[1].Sort != 2 {
		t.Fatalf("unexpected second ai config: %+v", cfg.AiConfigs[1])
	}
	if cfg.AiConfigs[1].ApiProtocol != AIAPIProtocolChatCompletions {
		t.Fatalf("unexpected normalized fallback protocol: %+v", cfg.AiConfigs[1])
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
				ID:          cfg.AiConfigs[0].ID,
				Name:        cfg.AiConfigs[0].Name,
				BaseUrl:     cfg.AiConfigs[0].BaseUrl,
				ApiKey:      cfg.AiConfigs[0].ApiKey,
				ModelName:   cfg.AiConfigs[0].ModelName,
				ApiProtocol: AIAPIProtocolAnthropicMessage,
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
	if cfg.AiConfigs[1].ApiProtocol != AIAPIProtocolAnthropicMessage {
		t.Fatalf("unexpected updated ai protocol: %+v", cfg.AiConfigs[1])
	}
}

func TestUpdateConfigReplacesOldAIConfigsWithNewOnes(t *testing.T) {
	_ = initSettingsTestDB(t)

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

func TestUpdateConfigPersistsManualAIConfigSort(t *testing.T) {
	_ = initSettingsTestDB(t)

	res := UpdateConfig(&SettingConfig{
		Settings: &Settings{
			OpenAiEnable:   true,
			AkshareEnabled: true,
		},
		AiConfigs: []*AIConfig{
			{
				Sort:      20,
				Name:      "later",
				BaseUrl:   "https://later.example.com",
				ApiKey:    "later-key",
				ModelName: "later-model",
			},
			{
				Sort:      10,
				Name:      "earlier",
				BaseUrl:   "https://earlier.example.com",
				ApiKey:    "earlier-key",
				ModelName: "earlier-model",
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
	if cfg.AiConfigs[0].Name != "earlier" || cfg.AiConfigs[0].Sort != 10 {
		t.Fatalf("expected manual sort to drive first item: %+v", cfg.AiConfigs[0])
	}
	if cfg.AiConfigs[1].Name != "later" || cfg.AiConfigs[1].Sort != 20 {
		t.Fatalf("expected manual sort to drive second item: %+v", cfg.AiConfigs[1])
	}
}

func fullSettingsFixture() *Settings {
	return &Settings{
		TushareToken:             "tushare-secret",
		LocalPushEnable:          true,
		DingPushEnable:           true,
		DingRobot:                "ding-secret",
		YieldEmailEnable:         true,
		YieldEmailTo:             "first@example.com,second@example.com",
		YieldEmailFrom:           "sender@example.com",
		YieldEmailSMTPHost:       "smtp.example.com",
		YieldEmailSMTPPort:       587,
		YieldEmailSMTPUsername:   "smtp-user",
		YieldEmailSMTPPassword:   "smtp-secret",
		YieldEmailCronEnabled:    true,
		YieldEmailCronTimes:      "09:15,15:05",
		MarketSummaryEmailEnable: true,
		UpdateBasicInfoOnStart:   true,
		RefreshInterval:          17,
		OpenAiEnable:             false,
		Prompt:                   "system prompt",
		CheckUpdate:              true,
		QuestionTemplate:         "question template",
		CrawlTimeOut:             77,
		KDays:                    88,
		EnableDanmu:              true,
		BrowserPath:              "C:/browser/browser.exe",
		EnableNews:               true,
		DarkTheme:                true,
		BrowserPoolSize:          3,
		EnableFund:               true,
		EnablePushNews:           true,
		EnableOnlyPushRedNews:    true,
		HttpProxy:                "http://127.0.0.1:8899",
		HttpProxyEnabled:         true,
		ForceNoProxyForFetch:     false,
		EnableAgent:              true,
		QgqpBId:                  "qgqp-secret",
		MarketSummaryCronEnabled: true,
		MarketSummaryCronTimes:   "09:40,14:30",
		MinuteProviderMode:       "public",
		MinuteLongHistoryHint:    false,
		PrivateMinuteEnabled:     true,
		PrivateMinuteBaseURL:     "https://minute.example.com",
		PrivateMinuteAPIKey:      "minute-secret",
		PrivateMinuteTimeoutSec:  31,
		PrivateMinuteMinInterval: 456,
		PrivateMinuteProxyMode:   "settings",
		PrivateMinuteLevel:       "5min",
		AkshareEnabled:           true,
		SinaMinuteEnabled:        false,
		TencentMinuteEnabled:     true,
		EastmoneyMinuteEnabled:   false,
		AkshareMinuteSourceMode:  "sina",
	}
}

func TestSettingsAndSecretsRoundTripWithAIDisabled(t *testing.T) {
	dbPath := initSettingsTestDB(t)
	want := fullSettingsFixture()
	requireSettingsSave(t, &SettingConfig{
		Settings: want,
		AiConfigs: []*AIConfig{
			{Name: "primary", BaseUrl: "https://ai-one.example.com", ApiKey: "ai-secret-one", ModelName: "model-one"},
			{Name: "backup", BaseUrl: "https://ai-two.example.com", ApiKey: "ai-secret-two", ModelName: "model-two"},
		},
	})
	if err := db.Close(); err != nil {
		t.Fatalf("close database before reopen: %v", err)
	}
	db.Init(dbPath)

	got := GetSettingConfig()
	if got == nil || got.Settings == nil {
		t.Fatal("expected persisted settings")
	}
	gotSettings := *got.Settings
	gotSettings.Model = gorm.Model{}
	wantSettings := *want
	wantSettings.Model = gorm.Model{}
	gotJSON, _ := json.Marshal(gotSettings)
	wantJSON, _ := json.Marshal(wantSettings)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("settings round trip mismatch\nwant: %s\n got: %s", wantJSON, gotJSON)
	}
	if len(got.AiConfigs) != 2 || got.AiConfigs[0].ApiKey != "ai-secret-one" || got.AiConfigs[1].ApiKey != "ai-secret-two" {
		t.Fatalf("AI configs or keys were lost while AI was disabled: %+v", got.AiConfigs)
	}

	exported := NewSettingsApi().Export()
	for _, secret := range []string{"tushare-secret", "smtp-secret", "minute-secret", "ai-secret-one", "ai-secret-two"} {
		if !strings.Contains(exported, secret) {
			t.Fatalf("full export did not contain %q", secret)
		}
	}
}

func TestUpdateConfigNilAIConfigsPreservesAndEmptyDeletes(t *testing.T) {
	_ = initSettingsTestDB(t)
	requireSettingsSave(t, &SettingConfig{
		Settings:  &Settings{AkshareEnabled: true},
		AiConfigs: []*AIConfig{{Name: "kept", ApiKey: "keep-secret", ModelName: "model"}},
	})
	requireSettingsSave(t, &SettingConfig{
		Settings:  &Settings{AkshareEnabled: true, DarkTheme: true},
		AiConfigs: nil,
	})
	if got := GetSettingConfig(); len(got.AiConfigs) != 1 || got.AiConfigs[0].ApiKey != "keep-secret" {
		t.Fatalf("nil AI configs should preserve existing values: %+v", got.AiConfigs)
	}
	requireSettingsSave(t, &SettingConfig{
		Settings:  &Settings{AkshareEnabled: true},
		AiConfigs: []*AIConfig{},
	})
	if got := GetSettingConfig(); len(got.AiConfigs) != 0 {
		t.Fatalf("explicit empty AI configs should delete all values: %+v", got.AiConfigs)
	}
}

func TestUpdateConfigRejectsNilAIConfigAndRollsBack(t *testing.T) {
	_ = initSettingsTestDB(t)
	requireSettingsSave(t, &SettingConfig{
		Settings:  &Settings{RefreshInterval: 15, AkshareEnabled: true},
		AiConfigs: []*AIConfig{{Name: "existing", ApiKey: "existing-secret", ModelName: "model"}},
	})

	result := UpdateConfig(&SettingConfig{
		Settings:  &Settings{RefreshInterval: 99, AkshareEnabled: true},
		AiConfigs: []*AIConfig{nil},
	})
	if !strings.Contains(result, "AI 配置第 1 项为空") {
		t.Fatalf("unexpected save result: %s", result)
	}

	got := GetSettingConfig()
	if got.RefreshInterval != 15 {
		t.Fatalf("ordinary settings were not rolled back: %d", got.RefreshInterval)
	}
	if len(got.AiConfigs) != 1 || got.AiConfigs[0].ApiKey != "existing-secret" {
		t.Fatalf("AI configs were not preserved: %+v", got.AiConfigs)
	}
}

func TestUpdateConfigRollsBackSettingsWhenAIWriteFails(t *testing.T) {
	_ = initSettingsTestDB(t)
	requireSettingsSave(t, &SettingConfig{
		Settings:  &Settings{AkshareEnabled: true, Prompt: "before"},
		AiConfigs: []*AIConfig{{Name: "existing", ApiKey: "existing-secret", ModelName: "model"}},
	})
	if err := db.Dao.Exec(`CREATE TRIGGER fail_ai_insert BEFORE INSERT ON ai_config BEGIN SELECT RAISE(FAIL, 'forced ai failure'); END`).Error; err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	result := UpdateConfig(&SettingConfig{
		Settings:  &Settings{AkshareEnabled: true, Prompt: "after"},
		AiConfigs: []*AIConfig{{Name: "replacement", ApiKey: "replacement-secret", ModelName: "model"}},
	})
	if !strings.Contains(result, "失败") {
		t.Fatalf("expected transaction failure, got %q", result)
	}
	got := GetSettingConfig()
	if got.Prompt != "before" {
		t.Fatalf("settings update was not rolled back: prompt=%q", got.Prompt)
	}
	if len(got.AiConfigs) != 1 || got.AiConfigs[0].ApiKey != "existing-secret" {
		t.Fatalf("AI configs were not rolled back: %+v", got.AiConfigs)
	}
}

func TestFirstRunImportsEnvironmentOnceThenDatabaseWins(t *testing.T) {
	for key, value := range map[string]string{
		"GO_STOCK_DIEMENG_BASE_URL":        "https://env-minute.example.com",
		"GO_STOCK_DIEMENG_API_KEY":         "env-minute-secret",
		"GO_STOCK_DIEMENG_TIMEOUT_SEC":     "42",
		"GO_STOCK_DIEMENG_MIN_INTERVAL_MS": "789",
		"GO_STOCK_DIEMENG_PROXY_MODE":      "inherit",
		"GO_STOCK_DIEMENG_LEVEL":           "15min",
	} {
		oldValue, existed := os.LookupEnv(key)
		if err := os.Setenv(key, value); err != nil {
			t.Fatalf("set env %s: %v", key, err)
		}
		t.Cleanup(func() {
			if existed {
				_ = os.Setenv(key, oldValue)
			} else {
				_ = os.Unsetenv(key)
			}
		})
	}
	_ = initSettingsTestDB(t)
	first := GetSettingConfig()
	if first.PrivateMinuteBaseURL != "https://env-minute.example.com" || first.PrivateMinuteAPIKey != "env-minute-secret" || first.PrivateMinuteTimeoutSec != 42 || first.PrivateMinuteMinInterval != 789 {
		t.Fatalf("first-run environment values were not persisted: %+v", first.Settings)
	}
	if err := db.Dao.Model(&Settings{}).Where("id = ?", first.ID).Updates(map[string]any{
		"private_minute_base_url": "https://database.example.com",
		"private_minute_api_key":  "",
		"browser_path":            "",
	}).Error; err != nil {
		t.Fatalf("update database values: %v", err)
	}
	second := GetSettingConfig()
	if second.PrivateMinuteBaseURL != "https://database.example.com" || second.PrivateMinuteAPIKey != "" || second.BrowserPath != "" {
		t.Fatalf("environment/defaults overwrote persisted database values: %+v", second.Settings)
	}
}
