package research2

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go-stock/backend/models"
)

type fakeMailer struct {
	calls    int
	failures int
	messages []EmailMessage
}

func (m *fakeMailer) Send(_ context.Context, _ EmailConfig, message EmailMessage) error {
	m.calls++
	m.messages = append(m.messages, message)
	if m.calls <= m.failures {
		return errors.New("smtp temporary failure for sender@example.com using auth-code")
	}
	return nil
}

func emailTestService(t *testing.T, mailer Mailer) (*EmailService, *Repository) {
	t.Helper()
	repository := research2TestRepository(t)
	if err := repository.db.AutoMigrate(&EmailDelivery{}, &models.EmailSendLog{}); err != nil {
		t.Fatal(err)
	}
	return NewEmailService(repository, mailer), repository
}

func validEmailConfig() EmailConfig {
	return EmailConfig{Enabled: true, To: "recipient@example.com", From: "sender@example.com", SMTPHost: "smtp.example.com", SMTPPort: 465, Username: "sender@example.com", Password: "auth-code"}
}

func eligibleRun(status string) AnalysisRun {
	loc := shanghai()
	now := time.Date(2026, 8, 27, 9, 58, 0, 0, loc)
	return AnalysisRun{RunID: "00000000-0000-0000-0000-000000000001", TradingDate: "2026-08-27", AttemptNo: 2, ScheduledFor: now.Add(-8 * time.Minute), EvidenceCutoffAt: now.Add(-3 * time.Minute), GeneratedAt: &now, Status: status, RecommendationCount: 2, ReportMarkdown: "# 研究中心2报告\n\n正文", FailureReason: "报告生成窗口已经结束"}
}

func TestEmailQueueEligibilityAndIdempotence(t *testing.T) {
	service, repository := emailTestService(t, &fakeMailer{})
	created, err := service.Queue(context.Background(), eligibleRun("success"), validEmailConfig())
	if err != nil || !created {
		t.Fatalf("first queue created=%v err=%v", created, err)
	}
	created, err = service.Queue(context.Background(), eligibleRun("success"), validEmailConfig())
	if err != nil || created {
		t.Fatalf("duplicate queue created=%v err=%v", created, err)
	}
	if created, err = service.Queue(context.Background(), eligibleRun("failed"), validEmailConfig()); err != nil || created {
		t.Fatalf("failed run must not queue: created=%v err=%v", created, err)
	}
	var count int64
	if err = repository.db.Model(&EmailDelivery{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("delivery count=%d err=%v", count, err)
	}
}

func TestEmailDeliveryRetriesThreeTimesAfterInitialFailure(t *testing.T) {
	mailer := &fakeMailer{failures: 4}
	service, repository := emailTestService(t, mailer)
	now := time.Date(2026, 8, 27, 10, 0, 0, 0, shanghai())
	service.SetNowForTest(func() time.Time { return now })
	if _, err := service.Queue(context.Background(), eligibleRun("no_recommendation"), validEmailConfig()); err != nil {
		t.Fatal(err)
	}
	for _, advance := range []time.Duration{0, time.Minute, 3 * time.Minute, 10 * time.Minute} {
		now = now.Add(advance)
		if err := service.ProcessDue(context.Background(), validEmailConfig()); err != nil {
			t.Fatal(err)
		}
	}
	var delivery EmailDelivery
	if err := repository.db.First(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	if mailer.calls != 4 || delivery.AttemptCount != 4 || delivery.Status != EmailStatusFailed || delivery.NextAttemptAt != nil {
		t.Fatalf("calls=%d delivery=%+v", mailer.calls, delivery)
	}
	if strings.Contains(delivery.LastError, "auth-code") || strings.Contains(delivery.LastError, "sender@example.com") {
		t.Fatalf("delivery error leaked SMTP credentials: %s", delivery.LastError)
	}
}

func TestEmailDeliverySuccessAndCancellation(t *testing.T) {
	mailer := &fakeMailer{}
	service, repository := emailTestService(t, mailer)
	now := time.Date(2026, 8, 27, 10, 0, 0, 0, shanghai())
	service.SetNowForTest(func() time.Time { return now })
	if _, err := service.Queue(context.Background(), eligibleRun("missed_window"), validEmailConfig()); err != nil {
		t.Fatal(err)
	}
	if err := service.ProcessDue(context.Background(), validEmailConfig()); err != nil {
		t.Fatal(err)
	}
	var delivery EmailDelivery
	if err := repository.db.First(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	if delivery.Status != EmailStatusSent || delivery.SentAt == nil || mailer.calls != 1 || !strings.Contains(mailer.messages[0].Subject, "错过交易窗口") {
		t.Fatalf("unexpected sent delivery: %+v messages=%+v", delivery, mailer.messages)
	}

	other := eligibleRun("success")
	other.RunID = "00000000-0000-0000-0000-000000000002"
	if _, err := service.Queue(context.Background(), other, validEmailConfig()); err != nil {
		t.Fatal(err)
	}
	disabled := validEmailConfig()
	disabled.Enabled = false
	if err := service.ProcessDue(context.Background(), disabled); err != nil {
		t.Fatal(err)
	}
	delivery = EmailDelivery{}
	if err := repository.db.Where("analysis_run_id = ?", other.RunID).First(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	if delivery.Status != EmailStatusCancelled || mailer.calls != 1 {
		t.Fatalf("pending delivery was not cancelled: %+v", delivery)
	}
}

func TestEmailDeliveryRecoversAStaleSendingTaskAfterRestart(t *testing.T) {
	mailer := &fakeMailer{}
	service, repository := emailTestService(t, mailer)
	now := time.Date(2026, 8, 27, 10, 0, 0, 0, shanghai())
	service.SetNowForTest(func() time.Time { return now })
	if _, err := service.Queue(context.Background(), eligibleRun("success"), validEmailConfig()); err != nil {
		t.Fatal(err)
	}
	stale := now.Add(-3 * time.Minute)
	if err := repository.db.Model(&EmailDelivery{}).Where("analysis_run_id = ?", eligibleRun("success").RunID).
		Updates(map[string]any{"status": EmailStatusSending, "updated_at": stale}).Error; err != nil {
		t.Fatal(err)
	}

	restarted := NewEmailService(repository, mailer)
	restarted.SetNowForTest(func() time.Time { return now })
	if err := restarted.ProcessDue(context.Background(), validEmailConfig()); err != nil {
		t.Fatal(err)
	}
	var delivery EmailDelivery
	if err := repository.db.First(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	if delivery.Status != EmailStatusSent || delivery.AttemptCount != 1 || mailer.calls != 1 {
		t.Fatalf("stale delivery was not recovered: calls=%d delivery=%+v", mailer.calls, delivery)
	}
}

func TestReportEmailContentForAllEligibleResults(t *testing.T) {
	for _, testCase := range []struct {
		status, subjectPart, bodyPart string
	}{
		{status: "success", subjectPart: "分析报告（2只）", bodyPart: "# 研究中心2报告"},
		{status: "no_recommendation", subjectPart: "无推荐", bodyPart: "# 研究中心2报告"},
		{status: "missed_window", subjectPart: "错过交易窗口", bodyPart: "# 研究中心2报告"},
	} {
		t.Run(testCase.status, func(t *testing.T) {
			subject, body := reportEmailContent(eligibleRun(testCase.status))
			if !strings.Contains(subject, testCase.subjectPart) || !strings.Contains(subject, "第2次尝试") || !strings.Contains(body, "当日尝试：第2次") || !strings.Contains(body, testCase.bodyPart) {
				t.Fatalf("subject=%q body=%q", subject, body)
			}
		})
	}
}

func TestReportEmailFallbackHasNoCompletionDeadline(t *testing.T) {
	run := eligibleRun("success")
	run.ReportMarkdown = ""
	_, body := reportEmailContent(run)
	if strings.Contains(body, "11:25") || strings.Contains(body, "11:30") || strings.Contains(body, "硬截止") {
		t.Fatalf("fallback email still exposes removed deadlines: %q", body)
	}
	for _, expected := range []string{"实际启动：", "证据截止：", "报告生成："} {
		if !strings.Contains(body, expected) {
			t.Fatalf("fallback email missing %q: %q", expected, body)
		}
	}
}

func TestPlainTextMessageUsesUTF8AndStableMessageID(t *testing.T) {
	encoded := encodePlainTextMessage(EmailMessage{From: "sender@example.com", To: []string{"recipient@example.com"}, Subject: "研究中心2报告", Body: "第一行\n第二行", MessageID: "<stable@example.com>"})
	for _, expected := range []string{"Content-Type: text/plain; charset=\"UTF-8\"", "Message-ID: <stable@example.com>", "Subject: =?UTF-8?", "第一行\r\n第二行\r\n"} {
		if !strings.Contains(encoded, expected) {
			t.Fatalf("encoded message missing %q:\n%s", expected, encoded)
		}
	}
}
