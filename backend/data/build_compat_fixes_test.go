package data

import (
	"path/filepath"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
	"gorm.io/gorm"
)

func TestShouldAutoSendYieldXLSX_DisabledForManualMinuteDownload(t *testing.T) {
	if shouldAutoSendYieldXLSX("manual_minute_download", true) {
		t.Fatal("expected manual minute download auto email to stay disabled")
	}
}

func TestShouldAutoSendYieldXLSX_DisabledForAllReasons(t *testing.T) {
	if shouldAutoSendYieldXLSX("daily_cron", true) {
		t.Fatal("expected auto email to stay disabled for non-manual reasons")
	}
	if shouldAutoSendYieldXLSX("", false) {
		t.Fatal("expected auto email to stay disabled when reason is empty")
	}
}

func TestLoadLatestAIAnalysisReportForCron_RequiresFreshReport(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "latest-ai-report-cron.db"))
	if err := db.Dao.AutoMigrate(&models.AIResponseResult{}); err != nil {
		t.Fatalf("auto migrate ai response result failed: %v", err)
	}

	triggeredAt := time.Date(2026, 4, 7, 9, 35, 0, 0, time.Local)
	oldReport := &models.AIResponseResult{
		StockCode: "市场资讯",
		StockName: "市场资讯",
		Question:  "old",
		Content:   "old report",
	}
	oldReport.CreatedAt = triggeredAt.Add(-72 * time.Hour)
	if err := db.Dao.Create(oldReport).Error; err != nil {
		t.Fatalf("create old report failed: %v", err)
	}

	if _, err := loadLatestAIAnalysisReportForCron(triggeredAt); err == nil {
		t.Fatal("expected cron loader to reject stale report")
	}

	freshReport := &models.AIResponseResult{
		StockCode: "市场资讯",
		StockName: "市场资讯",
		Question:  "fresh",
		Content:   "fresh report",
	}
	freshReport.CreatedAt = triggeredAt.Add(-3 * time.Minute)
	if err := db.Dao.Create(freshReport).Error; err != nil {
		t.Fatalf("create fresh report failed: %v", err)
	}

	got, err := loadLatestAIAnalysisReportForCron(triggeredAt)
	if err != nil {
		t.Fatalf("load fresh report failed: %v", err)
	}
	if got.ID != freshReport.ID {
		t.Fatalf("unexpected report id: got=%d want=%d", got.ID, freshReport.ID)
	}
}

func TestLoadLatestAIAnalysisReport_ManualStillAllowsLatestStoredReport(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "latest-ai-report-manual.db"))
	if err := db.Dao.AutoMigrate(&models.AIResponseResult{}); err != nil {
		t.Fatalf("auto migrate ai response result failed: %v", err)
	}

	report := &models.AIResponseResult{
		StockCode: "市场资讯",
		StockName: "市场资讯",
		Question:  "manual",
		Content:   "manual latest report",
	}
	report.CreatedAt = time.Date(2026, 4, 3, 14, 32, 3, 0, time.Local)
	if err := db.Dao.Create(report).Error; err != nil {
		t.Fatalf("create report failed: %v", err)
	}

	got, err := loadLatestAIAnalysisReport()
	if err != nil {
		t.Fatalf("manual latest report loader failed: %v", err)
	}
	if got.ID != report.ID {
		t.Fatalf("unexpected report id: got=%d want=%d", got.ID, report.ID)
	}
}

func TestHasSuccessfulMarketSummaryEmailLog_DeduplicatesBySendTypeAndReportTime(t *testing.T) {
	db.Init(filepath.Join(t.TempDir(), "market-summary-email-dedupe.db"))
	if err := db.Dao.AutoMigrate(&models.EmailSendLog{}); err != nil {
		t.Fatalf("auto migrate email send log failed: %v", err)
	}

	reportTime := time.Date(2026, 4, 9, 11, 30, 0, 0, time.Local)
	recordEmailSendLog(emailAuditPayload{
		SendType:    "summary_auto",
		TriggeredAt: reportTime.Add(2 * time.Minute),
		Report: &models.AIResponseResult{
			StockCode: "市场资讯",
			StockName: "市场资讯",
			Model:     gorm.Model{CreatedAt: reportTime},
		},
	}, nil)

	ok, err := hasSuccessfulMarketSummaryEmailLog("summary_auto", &models.AIResponseResult{
		StockCode: "市场资讯",
		StockName: "市场资讯",
		Model:     gorm.Model{CreatedAt: reportTime},
	}, "")
	if err != nil {
		t.Fatalf("hasSuccessfulMarketSummaryEmailLog failed: %v", err)
	}
	if !ok {
		t.Fatal("expected duplicate summary email to be detected")
	}

	ok, err = hasSuccessfulMarketSummaryEmailLog("manual_summary", &models.AIResponseResult{
		StockCode: "市场资讯",
		StockName: "市场资讯",
		Model:     gorm.Model{CreatedAt: reportTime},
	}, "")
	if err != nil {
		t.Fatalf("hasSuccessfulMarketSummaryEmailLog failed for different type: %v", err)
	}
	if ok {
		t.Fatal("expected different send type not to be treated as duplicate")
	}
}
