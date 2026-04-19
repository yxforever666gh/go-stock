package data

import (
	"path/filepath"
	"testing"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"
	"gorm.io/gorm"
)

func initEmailSendLogTestDB(t *testing.T) {
	t.Helper()
	db.Init(filepath.Join(t.TempDir(), "email-send-log-test.db"))
	if err := db.Dao.AutoMigrate(&models.EmailSendLog{}); err != nil {
		t.Fatalf("auto migrate email send log failed: %v", err)
	}
}

func TestRecordEmailSendLogAndQuery(t *testing.T) {
	initEmailSendLogTestDB(t)

	triggeredAt := time.Date(2026, 3, 16, 9, 30, 0, 0, time.Local)
	reportTime := triggeredAt.Add(-5 * time.Minute)
	recordEmailSendLog(emailAuditPayload{
		SendType:    "cron_ai",
		TriggeredAt: triggeredAt,
		Recipients:  []string{"alice@example.com", "bob@example.com"},
		Subject:     "AI 报告测试",
		Report: &models.AIResponseResult{
			Model: gorm.Model{
				CreatedAt: reportTime,
			},
			StockCode: "600000",
			StockName: "浦发银行",
		},
		Attachments: []mailAttachment{{
			Filename:    "report.md",
			ContentType: "text/markdown",
			Content:     []byte("hello report"),
		}},
		ExtraSummary: "测试摘要",
	}, nil)

	page, err := NewEmailSendLogService().GetEmailSendLogList(models.EmailSendLogQuery{
		Page:     1,
		PageSize: 10,
		SendType: "cron_ai",
	})
	if err != nil {
		t.Fatalf("query email send logs failed: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("unexpected total: got=%d want=1", page.Total)
	}
	if len(page.List) != 1 {
		t.Fatalf("unexpected list size: got=%d want=1", len(page.List))
	}

	item := page.List[0]
	if item.Status != "success" {
		t.Fatalf("unexpected status: %s", item.Status)
	}
	if item.Recipients != "alice@example.com,bob@example.com" {
		t.Fatalf("unexpected recipients: %s", item.Recipients)
	}
	if item.AttachmentCount != 1 {
		t.Fatalf("unexpected attachment count: %d", item.AttachmentCount)
	}
	if item.AttachmentBytes != int64(len("hello report")) {
		t.Fatalf("unexpected attachment bytes: %d", item.AttachmentBytes)
	}
	if item.ReportStockCode != "600000" || item.ReportStockName != "浦发银行" {
		t.Fatalf("unexpected report info: code=%s name=%s", item.ReportStockCode, item.ReportStockName)
	}
}

func TestRecordEmailSendLogFailure(t *testing.T) {
	initEmailSendLogTestDB(t)

	triggeredAt := time.Date(2026, 3, 16, 10, 0, 0, 0, time.Local)
	recordEmailSendLog(emailAuditPayload{
		SendType:     "test",
		TriggeredAt:  triggeredAt,
		Subject:      "测试邮件",
		ExtraSummary: "测试邮件",
	}, assertErr("smtp auth failed"))

	page, err := NewEmailSendLogService().GetEmailSendLogList(models.EmailSendLogQuery{
		Page:     1,
		PageSize: 10,
		Status:   "failed",
	})
	if err != nil {
		t.Fatalf("query failed email logs failed: %v", err)
	}
	if page.Total != 1 || len(page.List) != 1 {
		t.Fatalf("unexpected failed logs result: total=%d list=%d", page.Total, len(page.List))
	}
	if page.List[0].ErrorMessage != "smtp auth failed" {
		t.Fatalf("unexpected error message: %s", page.List[0].ErrorMessage)
	}
}

type assertErr string

func (e assertErr) Error() string {
	return string(e)
}
