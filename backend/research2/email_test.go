package research2

import (
	"context"
	"errors"
	"io"
	"mime/quotedprintable"
	"strings"
	"testing"
	"time"

	"go-stock/backend/models"
	"go-stock/backend/researchconfig"
)

type fakeMailer struct {
	calls    int
	failures int
	sendErr  error
	messages []EmailMessage
}

func (m *fakeMailer) Send(_ context.Context, _ EmailConfig, message EmailMessage) error {
	m.calls++
	m.messages = append(m.messages, message)
	if m.calls <= m.failures {
		if m.sendErr != nil {
			return m.sendErr
		}
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
	return AnalysisRun{RunID: "00000000-0000-0000-0000-000000000001", TradingDate: "2026-08-27", Slot: "10:00", AttemptNo: 2, ScheduledFor: now.Add(-8 * time.Minute), EvidenceCutoffAt: now.Add(-3 * time.Minute), GeneratedAt: &now, Status: status, RecommendationCount: 2, ReportMarkdown: "# 研究中心2报告\n\n正文", FailureReason: "报告生成窗口已经结束"}
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
	if created, err := service.Queue(context.Background(), eligibleRun("missed_window"), validEmailConfig()); err != nil || created {
		t.Fatalf("missed window must not queue: created=%v err=%v", created, err)
	}
	if _, err := service.Queue(context.Background(), eligibleRun("success"), validEmailConfig()); err != nil {
		t.Fatal(err)
	}
	if err := service.ProcessDue(context.Background(), validEmailConfig()); err != nil {
		t.Fatal(err)
	}
	var delivery EmailDelivery
	if err := repository.db.First(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	if delivery.Status != EmailStatusSent || delivery.SentAt == nil || mailer.calls != 1 || !strings.Contains(mailer.messages[0].Subject, "10:00–10:05") {
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
	} {
		t.Run(testCase.status, func(t *testing.T) {
			subject, body := reportEmailContent(eligibleRun(testCase.status))
			if !strings.Contains(subject, testCase.subjectPart) || !strings.Contains(subject, "第2次尝试") || !strings.Contains(body, "当日尝试：第2次") || !strings.Contains(body, testCase.bodyPart) {
				t.Fatalf("subject=%q body=%q", subject, body)
			}
		})
	}
}

func TestNormalizeEmailSlotsValidatesDeduplicatesAndSorts(t *testing.T) {
	slots, err := NormalizeEmailSlots([]string{"10:00", "09:30", "10:00", "11:25"})
	if err != nil || strings.Join(slots, ",") != "09:30,10:00,11:25" {
		t.Fatalf("slots=%v err=%v", slots, err)
	}
	if _, err = NormalizeEmailSlots([]string{"09:31"}); err == nil {
		t.Fatal("invalid slot was accepted")
	}
	encoded, normalized, err := MarshalEmailSlots([]string{"11:25", "09:30"})
	parsed, parseErr := ParseEmailSlots(encoded)
	if err != nil || parseErr != nil || encoded != `["09:30","11:25"]` || len(normalized) != 2 || strings.Join(parsed, ",") != "09:30,11:25" {
		t.Fatalf("encoded=%s normalized=%v err=%v", encoded, normalized, err)
	}
}

func configureSelectedEmailSlots(t *testing.T, repository *Repository, slots []string) {
	t.Helper()
	if err := repository.db.AutoMigrate(&EmailDelivery{}, &researchconfig.Record{}); err != nil {
		t.Fatal(err)
	}
	config := &models.SettingConfig{
		Settings: &models.Settings{
			Research2AutoEnabled: true, Research2EmailEnabled: true,
			Research2EmailTo: "recipient@example.com", Research2EmailFrom: "sender@example.com",
			Research2EmailSMTPHost: "smtp.example.com", Research2EmailSMTPPort: 465,
			Research2EmailSMTPUser: "sender@example.com", Research2EmailSMTPPass: "auth-code",
		},
		Research2EmailSlots: slots,
	}
	raw, err := researchconfig.ConfigJSON(researchconfig.Research2, config)
	if err != nil {
		t.Fatal(err)
	}
	if err = repository.db.Create(&researchconfig.Record{Center: researchconfig.Research2, ConfigJSON: string(raw), Revision: 1}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestPublishedReportQueuesSelectedActualSlotInSameTransaction(t *testing.T) {
	repository := slotRepository(t)
	configureSelectedEmailSlots(t, repository, []string{"09:35", "10:00"})
	ctx := context.Background()
	now := slotClock(9, 36)
	repository.now = func() time.Time { return now }
	run := AnalysisRun{RunID: "selected-empty", TradingDate: "2026-09-18", ScheduledSlot: "09:30", AttemptNo: 1, Status: "no_recommendation"}
	if err := repository.FinalizeRun(ctx, &run, nil, func() string { return "empty report" }); err != nil {
		t.Fatal(err)
	}
	if !run.Published || run.Slot != "09:35" {
		t.Fatalf("run=%+v", run)
	}
	var delivery EmailDelivery
	if err := repository.db.Where("analysis_run_id = ?", run.RunID).First(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	if delivery.Status != EmailStatusPending || !strings.Contains(delivery.Subject, "09:35–09:40") || delivery.Body != "当日尝试：第1次\n\nempty report" {
		t.Fatalf("delivery=%+v", delivery)
	}
	if err := repository.FinalizeRun(ctx, &run, nil, func() string { return "changed" }); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := repository.db.Model(&EmailDelivery{}).Where("analysis_run_id = ?", run.RunID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	archived := AnalysisRun{RunID: "same-slot-archived", TradingDate: "2026-09-18", ScheduledSlot: "09:35", AttemptNo: 2, Status: "success"}
	if err := repository.FinalizeRun(ctx, &archived, []Recommendation{{RecommendationID: "archived-stock", StockCode: "sh600001"}}, func() string { return "archived report" }); err != nil {
		t.Fatal(err)
	}
	count = 0
	if archived.Published || archived.ArchiveReason == "" {
		t.Fatalf("archived run=%+v", archived)
	}
	if err := repository.db.Model(&EmailDelivery{}).Where("analysis_run_id = ?", archived.RunID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("archived delivery count=%d err=%v", count, err)
	}

	now = slotClock(9, 41)
	unselected := AnalysisRun{RunID: "unselected", TradingDate: "2026-09-18", ScheduledSlot: "09:40", AttemptNo: 1, Status: "success"}
	if err := repository.FinalizeRun(ctx, &unselected, []Recommendation{{RecommendationID: "unselected-stock", StockCode: "sh600000"}}, func() string { return "report" }); err != nil {
		t.Fatal(err)
	}
	if err := repository.db.Model(&EmailDelivery{}).Where("analysis_run_id = ?", unselected.RunID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("unselected delivery count=%d err=%v", count, err)
	}
}

func TestPublishedReportRollsBackWhenDeliveryInsertFails(t *testing.T) {
	repository := slotRepository(t)
	configureSelectedEmailSlots(t, repository, []string{"09:35"})
	if err := repository.db.Exec(`CREATE TRIGGER fail_research2_email BEFORE INSERT ON research2_email_deliveries BEGIN SELECT RAISE(ABORT, 'email insert failed'); END`).Error; err != nil {
		t.Fatal(err)
	}
	repository.now = func() time.Time { return slotClock(9, 36) }
	run := AnalysisRun{RunID: "atomic", TradingDate: "2026-09-18", ScheduledSlot: "09:35", AttemptNo: 1, Status: "no_recommendation"}
	if err := repository.FinalizeRun(context.Background(), &run, nil, func() string { return "report" }); err == nil {
		t.Fatal("expected delivery insert failure")
	}
	var runCount, chainCount, deliveryCount int64
	repository.db.Model(&AnalysisRun{}).Where("run_id = ?", run.RunID).Count(&runCount)
	repository.db.Model(&ExecutionChain{}).Where("winner_run_id = ?", run.RunID).Count(&chainCount)
	repository.db.Model(&EmailDelivery{}).Where("analysis_run_id = ?", run.RunID).Count(&deliveryCount)
	if runCount != 0 || chainCount != 0 || deliveryCount != 0 {
		t.Fatalf("partial transaction persisted: run=%d chain=%d delivery=%d", runCount, chainCount, deliveryCount)
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
	for _, expected := range []string{"Content-Type: text/plain; charset=\"UTF-8\"", "Content-Transfer-Encoding: quoted-printable", "Message-ID: <stable@example.com>", "Subject: =?UTF-8?"} {
		if !strings.Contains(encoded, expected) {
			t.Fatalf("encoded message missing %q:\n%s", expected, encoded)
		}
	}
	if body := decodeQuotedPrintableBody(t, encoded); body != "第一行\r\n第二行" {
		t.Fatalf("decoded body=%q", body)
	}
}

func TestPlainTextMessageFoldsArbitrarilyLongUTF8BodyLines(t *testing.T) {
	body := "核心结论：继续观察。" + strings.Repeat("这是很长的中文降级说明", 1000)
	encoded := encodePlainTextMessage(EmailMessage{From: "sender@example.com", To: []string{"recipient@example.com"}, Subject: "长中文报告", Body: body, MessageID: "<long@example.com>"})
	for lineNo, line := range strings.Split(encoded, "\r\n") {
		if len([]byte(line)) > 998 {
			t.Fatalf("physical line %d has %d octets", lineNo+1, len([]byte(line)))
		}
	}
	if decoded := decodeQuotedPrintableBody(t, encoded); decoded != body {
		t.Fatalf("long UTF-8 body changed after MIME round trip: got=%d want=%d", len(decoded), len(body))
	}
}

func TestReportEmailCompactsOnlyDegradedReasonsAndKeepsCoreConclusion(t *testing.T) {
	run := eligibleRun("success")
	reasons := make([]string, 0, 12)
	for index := 1; index <= 12; index++ {
		reasons = append(reasons, strings.Repeat("降级明细", 40)+string(rune('甲'+index)))
	}
	run.ReportMarkdown = "# 核心结论\n\n建议保持空仓。\n\n- 降级原因：" + strings.Join(reasons, "；") + "\n\n## 风险\n核心风险不变。"
	_, body := reportEmailContent(run)
	for _, core := range []string{"# 核心结论", "建议保持空仓。", "## 风险", "核心风险不变。", "降级原因摘要", "完整明细见数据库审计"} {
		if !strings.Contains(body, core) {
			t.Fatalf("compacted report missing %q:\n%s", core, body)
		}
	}
	if strings.Contains(body, reasons[0]) || strings.Count(body, "  - ") > 9 {
		t.Fatalf("degraded details were not compacted:\n%s", body)
	}
	if run.ReportMarkdown == body {
		t.Fatal("email body should be a compact copy, not a mutation of persisted report content")
	}
}

func TestLineTooLongSMTPErrorDoesNotRetryIdenticalPayload(t *testing.T) {
	mailer := &fakeMailer{failures: 2, sendErr: errors.New("500 Line too long")}
	service, repository := emailTestService(t, mailer)
	now := time.Date(2026, 8, 27, 10, 0, 0, 0, shanghai())
	service.SetNowForTest(func() time.Time { return now })
	if _, err := service.Queue(context.Background(), eligibleRun("success"), validEmailConfig()); err != nil {
		t.Fatal(err)
	}
	if err := service.ProcessDue(context.Background(), validEmailConfig()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	if err := service.ProcessDue(context.Background(), validEmailConfig()); err != nil {
		t.Fatal(err)
	}
	var delivery EmailDelivery
	if err := repository.db.First(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	if mailer.calls != 1 || delivery.AttemptCount != 1 || delivery.Status != EmailStatusFailed || delivery.NextAttemptAt != nil {
		t.Fatalf("deterministic format error retried: calls=%d delivery=%+v", mailer.calls, delivery)
	}
}

func decodeQuotedPrintableBody(t *testing.T, message string) string {
	t.Helper()
	parts := strings.SplitN(message, "\r\n\r\n", 2)
	if len(parts) != 2 {
		t.Fatalf("message has no header/body separator:\n%s", message)
	}
	decoded, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(parts[1])))
	if err != nil {
		t.Fatal(err)
	}
	return string(decoded)
}
