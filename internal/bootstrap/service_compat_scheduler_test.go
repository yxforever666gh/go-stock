package bootstrap

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go-stock/backend/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestSchedulerCompatibilityAdapterPersistsAndQueriesTaskRuns(t *testing.T) {
	database := openSchedulerCompatibilityTestDB(t)
	adapter := compatibilityServiceAdapter{main: database}
	ctx := context.Background()
	start := time.Date(2026, 8, 6, 9, 40, 0, 0, time.FixedZone("CST", 8*60*60))

	run := &models.CronTaskRun{
		TaskName:    "market_summary",
		TriggeredAt: start,
		Status:      "started",
		Attempts:    1,
	}
	if err := adapter.CreateTaskRun(ctx, run); err != nil {
		t.Fatalf("create task run: %v", err)
	}
	if run.ID == 0 {
		t.Fatal("created task run did not receive an ID")
	}
	run.Status = "success"
	run.Attempts = 2
	if err := adapter.UpdateTaskRun(ctx, run); err != nil {
		t.Fatalf("update task run: %v", err)
	}
	var persisted models.CronTaskRun
	if err := database.First(&persisted, run.ID).Error; err != nil {
		t.Fatalf("load task run: %v", err)
	}
	if persisted.Status != "success" || persisted.Attempts != 2 {
		t.Fatalf("persisted task run = status:%s attempts:%d", persisted.Status, persisted.Attempts)
	}

	earlier := &models.CronTaskRun{TaskName: "latest_ai_analysis_email", TriggeredAt: start, Status: "started"}
	later := &models.CronTaskRun{TaskName: "latest_ai_analysis_email", TriggeredAt: start.Add(20 * time.Second), Status: "success"}
	ignored := &models.CronTaskRun{TaskName: "latest_ai_analysis_email", TriggeredAt: start.Add(10 * time.Second), Status: "skipped"}
	for _, taskRun := range []*models.CronTaskRun{earlier, later, ignored} {
		if err := adapter.CreateTaskRun(ctx, taskRun); err != nil {
			t.Fatalf("seed task run: %v", err)
		}
	}
	earliest, err := adapter.EarliestTaskRun(ctx, "latest_ai_analysis_email", start, start.Add(time.Minute), []string{"started", "success"})
	if err != nil {
		t.Fatalf("query earliest task run: %v", err)
	}
	if earliest.ID != earlier.ID {
		t.Fatalf("earliest task run ID = %d, want %d", earliest.ID, earlier.ID)
	}
}

func TestSchedulerCompatibilityAdapterFindsLatestMatchingResponse(t *testing.T) {
	database := openSchedulerCompatibilityTestDB(t)
	adapter := compatibilityServiceAdapter{main: database}
	ctx := context.Background()
	start := time.Date(2026, 8, 6, 9, 40, 0, 0, time.UTC)
	rows := []*models.AIResponseResult{
		{Model: gorm.Model{CreatedAt: start}, StockName: "市场资讯", Question: "question", Content: "old"},
		{Model: gorm.Model{CreatedAt: start.Add(time.Minute)}, StockName: "市场资讯", Question: "question", Content: "latest"},
		{Model: gorm.Model{CreatedAt: start.Add(2 * time.Minute)}, StockName: "other", Question: "question", Content: "unrelated"},
	}
	for _, row := range rows {
		if err := database.Create(row).Error; err != nil {
			t.Fatalf("seed AI response: %v", err)
		}
	}

	latest, err := adapter.LatestAIResponseSince(ctx, "市场资讯", "question", start.Add(30*time.Second))
	if err != nil {
		t.Fatalf("query latest AI response: %v", err)
	}
	if latest.Content != "latest" {
		t.Fatalf("latest AI response content = %q", latest.Content)
	}
}

func openSchedulerCompatibilityTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	database, err := gorm.Open(
		sqlite.Open(filepath.Join(t.TempDir(), "scheduler.db")),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	if err != nil {
		t.Fatalf("open scheduler database: %v", err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatalf("access scheduler database handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.AutoMigrate(
		&models.CronTaskRun{},
		&models.AIResponseResult{},
		&models.Telegraph{},
		&models.Tags{},
		&models.TelegraphTags{},
		&models.StockBasic{},
		&models.StockInfoHK{},
		&models.StockInfoUS{},
	); err != nil {
		t.Fatalf("migrate scheduler tables: %v", err)
	}
	return database
}
