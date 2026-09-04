package aiapp

import (
	"context"
	"path/filepath"
	"testing"

	"go-stock/backend/models"
	cliports "go-stock/internal/cli/ports"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func openCommandAITestDB(t *testing.T) *gorm.DB {
	t.Helper()
	database, err := gorm.Open(
		sqlite.Open(filepath.Join(t.TempDir(), "command-ai.db")),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatalf("get database connection: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.AutoMigrate(&models.AIConfig{}); err != nil {
		t.Fatalf("migrate ai config: %v", err)
	}
	return database
}

func TestResolveCommandAIConfigPrefersCompleteOptions(t *testing.T) {
	database := openCommandAITestDB(t)
	if err := database.Create(&models.AIConfig{BaseUrl: "https://db.example.com", ApiKey: "db-key", ModelName: "db-model"}).Error; err != nil {
		t.Fatalf("seed database config: %v", err)
	}

	got, err := resolveCommandAIConfig(context.Background(), database, cliports.CommandAIOptions{
		BaseURL: "https://flags.example.com", APIKey: "flag-key", Model: "flag-model",
		MaxTokens: 2048, Temperature: 0.5, Timeout: 120,
	})
	if err != nil {
		t.Fatalf("resolve command AI config: %v", err)
	}
	if got.BaseUrl != "https://flags.example.com" || got.ApiKey != "flag-key" || got.ModelName != "flag-model" {
		t.Fatalf("explicit config was not selected: %+v", got)
	}
	if got.MaxTokens != 2048 || got.Temperature != 0.5 || got.TimeOut != 120 {
		t.Fatalf("explicit tuning options were not retained: %+v", got)
	}
}

func TestResolveCommandAIConfigFallsBackToSavedID(t *testing.T) {
	database := openCommandAITestDB(t)
	first := &models.AIConfig{BaseUrl: "https://first.example.com", ApiKey: "first-key", ModelName: "first-model"}
	second := &models.AIConfig{BaseUrl: "https://second.example.com", ApiKey: "second-key", ModelName: "second-model"}
	if err := database.Create(first).Error; err != nil {
		t.Fatalf("seed first config: %v", err)
	}
	if err := database.Create(second).Error; err != nil {
		t.Fatalf("seed second config: %v", err)
	}

	got, err := resolveCommandAIConfig(context.Background(), database, cliports.CommandAIOptions{AIConfigID: int(second.ID)})
	if err != nil {
		t.Fatalf("resolve command AI config: %v", err)
	}
	if got.ID != second.ID || got.BaseUrl != second.BaseUrl || got.ApiKey != second.ApiKey || got.ModelName != second.ModelName {
		t.Fatalf("saved config mismatch: got=%+v want=%+v", got, second)
	}
}

func TestResolveCommandAIConfigRejectsInvalidSources(t *testing.T) {
	database := openCommandAITestDB(t)
	tests := []struct {
		name    string
		options cliports.CommandAIOptions
	}{
		{name: "partial explicit options", options: cliports.CommandAIOptions{BaseURL: "https://partial.example.com"}},
		{name: "missing saved config"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := resolveCommandAIConfig(context.Background(), database, test.options); err == nil {
				t.Fatal("expected configuration error")
			}
		})
	}
}

func TestCommandAIResolverConstructsClient(t *testing.T) {
	resolver, err := NewCommandResolver(openCommandAITestDB(t))
	if err != nil {
		t.Fatal(err)
	}
	client, err := resolver.ResolveCommandAI(context.Background(), cliports.CommandAIOptions{
		BaseURL: "https://example.com", APIKey: "key", Model: "model",
	})
	if err != nil {
		t.Fatalf("resolve command AI client: %v", err)
	}
	if client == nil {
		t.Fatal("expected command AI client")
	}
}

func TestNewCommandResolverRequiresDatabase(t *testing.T) {
	if _, err := NewCommandResolver(nil); err == nil {
		t.Fatal("expected missing database error")
	}
}
