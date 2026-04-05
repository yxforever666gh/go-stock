package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"go-stock/backend/data"
	"go-stock/backend/db"
	"go-stock/backend/models"

	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func latestMarketSummaryAfter(question string, startedAt time.Time) *models.AIResponseResult {
	var latest models.AIResponseResult
	err := db.Dao.Model(&models.AIResponseResult{}).
		Where("stock_name = ? AND question = ? AND created_at >= ?", "市场资讯", question, startedAt).
		Order("id desc").
		Limit(1).
		First(&latest).Error
	if err != nil {
		return nil
	}
	if latest.ID == 0 || strings.TrimSpace(latest.Content) == "" {
		return nil
	}
	return &latest
}

func TestRunMarketSummaryOnce(t *testing.T) {
	if os.Getenv("GO_STOCK_ENABLE_MARKET_SUMMARY_TEST") != "1" {
		t.Skip("set GO_STOCK_ENABLE_MARKET_SUMMARY_TEST=1 to run")
	}

	dbPath := strings.TrimSpace(os.Getenv("GO_STOCK_DB_PATH"))
	if dbPath == "" {
		dbPath = "data/stock.db"
	}

	setRuntimeEventsEnabled(false)
	setWebEventHub(nil)
	db.Init(dbPath)
	db.Dao = db.Dao.Session(&gorm.Session{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	data.InitAnalyzeSentiment()
	AutoMigrate()

	setting := data.GetSettingConfig()
	if setting == nil || setting.Settings == nil {
		t.Fatalf("AI_SUMMARY_BACKEND_FAIL reason=no_settings db=%s", dbPath)
	}
	if !setting.OpenAiEnable {
		t.Fatalf("AI_SUMMARY_BACKEND_FAIL reason=openai_disabled db=%s", dbPath)
	}
	if len(setting.AiConfigs) == 0 || setting.AiConfigs[0] == nil || int(setting.AiConfigs[0].ID) <= 0 {
		t.Fatalf("AI_SUMMARY_BACKEND_FAIL reason=no_ai_config db=%s", dbPath)
	}

	question := data.NormalizeMarketSummaryQuestion(setting.QuestionTemplate)
	app := NewApp()
	defer app.cron.Stop()
	app.ctx = context.Background()

	startedAt := time.Now()
	attempts := 1
	aiConfigID := int(setting.AiConfigs[0].ID)
	app.SummaryStockNews(question, aiConfigID, nil, true, false)
	latest := latestMarketSummaryAfter(question, startedAt)
	if latest == nil {
		attempts = 2
		app.SummaryStockNews(question, aiConfigID, nil, true, true)
		latest = latestMarketSummaryAfter(question, startedAt)
	}
	if latest == nil {
		t.Fatalf("AI_SUMMARY_BACKEND_FAIL reason=no_saved_summary attempts=%d duration=%ds", attempts, int(time.Since(startedAt).Seconds()))
	}

	fmt.Printf(
		"AI_SUMMARY_BACKEND_OK id=%d created_at=%s attempts=%d duration=%ds\n",
		latest.ID,
		latest.CreatedAt.In(time.FixedZone("CST", 8*3600)).Format("2006-01-02 15:04:05 -0700"),
		attempts,
		int(time.Since(startedAt).Seconds()),
	)
}
