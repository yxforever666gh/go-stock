package cli

import (
	"context"
	"path/filepath"
	"testing"

	"go-stock/backend/data"
	"go-stock/backend/db"
)

func initTestDB(t *testing.T) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "stock.db")
	db.Init(dbPath)
	if err := db.Dao.AutoMigrate(&data.Settings{}, &data.AIConfig{}); err != nil {
		t.Fatalf("migrate failed: %v", err)
	}
	_ = db.Dao.Exec("DELETE FROM settings").Error
	_ = db.Dao.Exec("DELETE FROM ai_config").Error
}

func TestResolveAIForCommand_ParameterPriority(t *testing.T) {
	initTestDB(t)

	// Database config should be ignored when full parameter mode is provided.
	if err := db.Dao.Create(&data.AIConfig{
		BaseUrl:   "https://db.example.com",
		ApiKey:    "db-key",
		ModelName: "db-model",
	}).Error; err != nil {
		t.Fatalf("seed ai config failed: %v", err)
	}

	o, err := ResolveAIForCommand(context.Background(), AIOptions{
		BaseURL:     "https://param.example.com",
		APIKey:      "param-key",
		Model:       "param-model",
		MaxTokens:   2048,
		Temperature: 0.5,
		Timeout:     120,
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if o.BaseUrl != "https://param.example.com" || o.ApiKey != "param-key" || o.Model != "param-model" {
		t.Fatalf("parameter mode not applied: %+v", o)
	}
}

func TestResolveAIForCommand_DBFallbackByID(t *testing.T) {
	initTestDB(t)

	first := &data.AIConfig{
		BaseUrl:   "https://first.example.com",
		ApiKey:    "first-key",
		ModelName: "first-model",
	}
	second := &data.AIConfig{
		BaseUrl:   "https://second.example.com",
		ApiKey:    "second-key",
		ModelName: "second-model",
	}
	if err := db.Dao.Create(first).Error; err != nil {
		t.Fatalf("seed first failed: %v", err)
	}
	if err := db.Dao.Create(second).Error; err != nil {
		t.Fatalf("seed second failed: %v", err)
	}

	o, err := ResolveAIForCommand(context.Background(), AIOptions{
		AIConfigID: int(second.ID),
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if o.BaseUrl != second.BaseUrl || o.ApiKey != second.ApiKey || o.Model != second.ModelName {
		t.Fatalf("db fallback mismatch: got=%+v expect=%+v", o, second)
	}
}

func TestResolveAIForCommand_NoConfig(t *testing.T) {
	initTestDB(t)
	_, err := ResolveAIForCommand(context.Background(), AIOptions{})
	if err == nil {
		t.Fatal("expected error when no ai config exists")
	}
}

func TestResolveFingerprint(t *testing.T) {
	initTestDB(t)

	got, err := ResolveFingerprint("from-flag")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "from-flag" {
		t.Fatalf("flag priority failed: %s", got)
	}

	if err := db.Dao.Create(&data.Settings{QgqpBId: "from-db"}).Error; err != nil {
		t.Fatalf("seed settings failed: %v", err)
	}
	got, err = ResolveFingerprint("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "from-db" {
		t.Fatalf("db fallback failed: %s", got)
	}

	_ = db.Dao.Exec("DELETE FROM settings").Error
	_, err = ResolveFingerprint("")
	if err == nil {
		t.Fatal("expected error when qgqp_b_id missing")
	}
}
