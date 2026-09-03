package research2

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	EmailStatusPending   = "pending"
	EmailStatusSending   = "sending"
	EmailStatusRetryWait = "retry_wait"
	EmailStatusSent      = "sent"
	EmailStatusFailed    = "failed"
	EmailStatusCancelled = "cancelled"
	maxEmailAttempts     = 4
)

var emailRetryDelays = []time.Duration{time.Minute, 3 * time.Minute, 10 * time.Minute}

type EmailConfig struct {
	Enabled  bool
	To       string
	From     string
	SMTPHost string
	SMTPPort int
	Username string
	Password string
	Timeout  time.Duration
}

type EmailMessage struct {
	From      string
	To        []string
	Subject   string
	Body      string
	MessageID string
}

type Mailer interface {
	Send(context.Context, EmailConfig, EmailMessage) error
}

type EmailService struct {
	repository *Repository
	mailer     Mailer
	now        func() time.Time
	mu         sync.Mutex
}

func NewEmailService(repository *Repository, mailer Mailer) *EmailService {
	if mailer == nil {
		mailer = SMTPMailer{}
	}
	return &EmailService{repository: repository, mailer: mailer, now: time.Now}
}

func (s *EmailService) SetNowForTest(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}

func EligibleEmailStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "success", "no_recommendation", "missed_window":
		return true
	default:
		return false
	}
}

func ParseEmailRecipients(value string) ([]string, error) {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' || r == '\n' || r == '\r' })
	result := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		address, err := mail.ParseAddress(strings.TrimSpace(part))
		if err != nil || strings.TrimSpace(address.Address) == "" {
			return nil, fmt.Errorf("无效收件人地址: %s", strings.TrimSpace(part))
		}
		key := strings.ToLower(address.Address)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, address.Address)
	}
	if len(result) == 0 {
		return nil, errors.New("请填写至少一个收件人地址")
	}
	return result, nil
}

func ValidateEmailConfig(config EmailConfig) (EmailConfig, []string, error) {
	config.To = strings.TrimSpace(config.To)
	config.From = strings.TrimSpace(config.From)
	config.SMTPHost = strings.TrimSpace(config.SMTPHost)
	config.Username = strings.TrimSpace(config.Username)
	if config.From == "" {
		config.From = config.Username
	}
	if config.SMTPHost == "" {
		return config, nil, errors.New("请填写 SMTP 主机")
	}
	if config.SMTPPort < 1 || config.SMTPPort > 65535 {
		return config, nil, errors.New("SMTP 端口必须在 1 到 65535 之间")
	}
	if config.Username == "" || config.Password == "" {
		return config, nil, errors.New("请填写 SMTP 用户名和授权码")
	}
	from, err := mail.ParseAddress(config.From)
	if err != nil || from.Address == "" {
		return config, nil, errors.New("发件人地址无效")
	}
	config.From = from.Address
	recipients, err := ParseEmailRecipients(config.To)
	if err != nil {
		return config, nil, err
	}
	if config.Timeout <= 0 {
		config.Timeout = 15 * time.Second
	}
	return config, recipients, nil
}

func (s *EmailService) Queue(ctx context.Context, run AnalysisRun, config EmailConfig) (bool, error) {
	if s == nil || s.repository == nil || !config.Enabled || !EligibleEmailStatus(run.Status) {
		return false, nil
	}
	normalized, recipients, err := ValidateEmailConfig(config)
	if err != nil {
		return false, err
	}
	subject, body := reportEmailContent(run)
	now := s.now()
	delivery := EmailDelivery{
		AnalysisRunID: run.RunID, Status: EmailStatusPending, NextAttemptAt: &now,
		Recipients: strings.Join(recipients, ","), Sender: normalized.From,
		Subject: subject, Body: body, MessageID: fmt.Sprintf("<research2-%s@%s>", run.RunID, normalized.SMTPHost),
	}
	return s.repository.CreateEmailDelivery(ctx, &delivery)
}

func reportEmailContent(run AnalysisRun) (string, string) {
	suffix := fmt.Sprintf("分析报告（%d只）", run.RecommendationCount)
	if run.Status == "no_recommendation" {
		suffix = "无推荐"
	} else if run.Status == "missed_window" {
		suffix = "错过交易窗口"
	}
	attemptNo := normalizedAttemptNo(run.AttemptNo)
	subject := fmt.Sprintf("[go-stock][研究中心2] %s 第%d次尝试 %s", run.TradingDate, attemptNo, suffix)
	body := strings.TrimSpace(run.ReportMarkdown)
	body = compactEmailDegradedReasons(body)
	attemptLine := fmt.Sprintf("当日尝试：第%d次", attemptNo)
	if body != "" && !strings.Contains(body, attemptLine) {
		body = attemptLine + "\n\n" + body
	}
	if body == "" {
		body = fmt.Sprintf("研究中心2分析结果\n\n交易日：%s\n当日尝试：第%d次\n计划开始：%s\n实际启动：%s\n证据截止：%s\n报告生成：%s\n状态：%s\n说明：%s",
			run.TradingDate, attemptNo, run.ScheduledFor.In(shanghai()).Format("2006-01-02 15:04:05"),
			run.StartedAt.In(shanghai()).Format("2006-01-02 15:04:05"), run.EvidenceCutoffAt.In(shanghai()).Format("2006-01-02 15:04:05"), formatOptionalResearch2Time(run.GeneratedAt),
			run.Status, strings.TrimSpace(run.FailureReason))
	}
	return subject, body
}

func formatOptionalResearch2Time(value *time.Time) string {
	if value == nil || value.IsZero() {
		return "--"
	}
	return value.In(shanghai()).Format("2006-01-02 15:04:05")
}

func (s *EmailService) ProcessDue(ctx context.Context, config EmailConfig) error {
	if s == nil || s.repository == nil {
		return errors.New("research2 email service is unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !config.Enabled {
		return s.repository.CancelPendingEmailDeliveries(ctx)
	}
	normalized, _, err := ValidateEmailConfig(config)
	if err != nil {
		return err
	}
	now := s.now()
	if err = s.repository.RecoverStaleEmailDeliveries(ctx, now.Add(-2*time.Minute), now); err != nil {
		return err
	}
	deliveries, err := s.repository.DueEmailDeliveries(ctx, now, 20)
	if err != nil {
		return err
	}
	for _, delivery := range deliveries {
		claimed, claimErr := s.repository.ClaimEmailDelivery(ctx, delivery.ID)
		if claimErr != nil {
			return claimErr
		}
		if !claimed {
			continue
		}
		attempts := delivery.AttemptCount + 1
		message := EmailMessage{From: delivery.Sender, To: strings.Split(delivery.Recipients, ","), Subject: delivery.Subject, Body: delivery.Body, MessageID: delivery.MessageID}
		sendErr := s.mailer.Send(ctx, normalized, message)
		attemptedAt := s.now()
		if sendErr == nil {
			if err = s.repository.CompleteEmailDelivery(ctx, delivery.ID, attempts, attemptedAt); err != nil {
				return err
			}
			_ = s.repository.RecordEmailAttempt(ctx, delivery, "success", "", attemptedAt)
			continue
		}
		redacted := RedactEmailError(sendErr, normalized, message.To)
		var next *time.Time
		if attempts < maxEmailAttempts && !isDeterministicEmailFormatError(sendErr) {
			value := attemptedAt.Add(emailRetryDelays[attempts-1])
			next = &value
		}
		if err = s.repository.FailEmailDelivery(ctx, delivery.ID, attempts, next, redacted); err != nil {
			return err
		}
		_ = s.repository.RecordEmailAttempt(ctx, delivery, "failed", redacted, attemptedAt)
	}
	return nil
}

func (s *EmailService) SendTest(ctx context.Context, config EmailConfig) error {
	if s == nil || s.repository == nil {
		return errors.New("research2 email service is unavailable")
	}
	normalized, recipients, err := ValidateEmailConfig(config)
	if err != nil {
		return err
	}
	now := s.now().In(shanghai())
	delivery := EmailDelivery{AnalysisRunID: "test", Recipients: strings.Join(recipients, ","), Sender: normalized.From, Subject: "[链路测试][go-stock][研究中心2] 邮件配置测试", MessageID: fmt.Sprintf("<research2-test-%d@%s>", now.UnixNano(), normalized.SMTPHost)}
	delivery.Body = fmt.Sprintf("研究中心2邮件链路测试成功。\n\n发送时间：%s\n此邮件不包含交易指令。", now.Format("2006-01-02 15:04:05"))
	err = s.mailer.Send(ctx, normalized, EmailMessage{From: delivery.Sender, To: recipients, Subject: delivery.Subject, Body: delivery.Body, MessageID: delivery.MessageID})
	status, detail := "success", ""
	if err != nil {
		status, detail = "failed", RedactEmailError(err, normalized, recipients)
	}
	_ = s.repository.RecordEmailAttempt(ctx, delivery, status, detail, s.now())
	if err != nil {
		return errors.New(detail)
	}
	return nil
}

func RedactEmailError(err error, config EmailConfig, recipients []string) string {
	if err == nil {
		return ""
	}
	value := err.Error()
	for _, secret := range append([]string{config.Password, config.Username, config.From}, recipients...) {
		if strings.TrimSpace(secret) != "" {
			value = strings.ReplaceAll(value, secret, "[已脱敏]")
		}
	}
	if len(value) > 1000 {
		value = value[:1000]
	}
	return value
}

type SMTPMailer struct{}

func (SMTPMailer) Send(ctx context.Context, config EmailConfig, message EmailMessage) error {
	normalized, _, err := ValidateEmailConfig(config)
	if err != nil {
		return err
	}
	address := net.JoinHostPort(normalized.SMTPHost, strconv.Itoa(normalized.SMTPPort))
	dialer := &net.Dialer{Timeout: normalized.Timeout}
	var connection net.Conn
	if normalized.SMTPPort == 465 {
		connection, err = tls.DialWithDialer(dialer, "tcp", address, &tls.Config{ServerName: normalized.SMTPHost, MinVersion: tls.VersionTLS12})
	} else {
		connection, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return fmt.Errorf("连接 SMTP 服务器失败: %w", err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(normalized.Timeout))
	client, err := smtp.NewClient(connection, normalized.SMTPHost)
	if err != nil {
		return fmt.Errorf("创建 SMTP 会话失败: %w", err)
	}
	defer client.Close()
	if normalized.SMTPPort != 465 {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP 服务器不支持 STARTTLS")
		}
		if err = client.StartTLS(&tls.Config{ServerName: normalized.SMTPHost, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("启用 STARTTLS 失败: %w", err)
		}
	}
	if err = client.Auth(smtp.PlainAuth("", normalized.Username, normalized.Password, normalized.SMTPHost)); err != nil {
		return fmt.Errorf("SMTP 认证失败: %w", err)
	}
	if err = client.Mail(message.From); err != nil {
		return fmt.Errorf("SMTP 发件人被拒绝: %w", err)
	}
	for _, recipient := range message.To {
		if err = client.Rcpt(recipient); err != nil {
			return fmt.Errorf("SMTP 收件人被拒绝: %w", err)
		}
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA 失败: %w", err)
	}
	if _, err = io.WriteString(writer, encodePlainTextMessage(message)); err != nil {
		_ = writer.Close()
		return fmt.Errorf("写入邮件失败: %w", err)
	}
	if err = writer.Close(); err != nil {
		return fmt.Errorf("提交邮件失败: %w", err)
	}
	return client.Quit()
}

func encodePlainTextMessage(message EmailMessage) string {
	header := textproto.MIMEHeader{}
	header.Set("From", message.From)
	header.Set("To", strings.Join(message.To, ", "))
	header.Set("Subject", mimeEncode(message.Subject))
	header.Set("Date", time.Now().Format(time.RFC1123Z))
	header.Set("Message-ID", message.MessageID)
	header.Set("MIME-Version", "1.0")
	header.Set("Content-Type", `text/plain; charset="UTF-8"`)
	header.Set("Content-Transfer-Encoding", "quoted-printable")
	var builder strings.Builder
	for _, key := range []string{"From", "To", "Subject", "Date", "Message-ID", "MIME-Version", "Content-Type", "Content-Transfer-Encoding"} {
		builder.WriteString(key + ": " + header.Get(key) + "\r\n")
	}
	builder.WriteString("\r\n")
	body := strings.ReplaceAll(message.Body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")
	body = strings.ReplaceAll(body, "\n", "\r\n")
	encodedBody := quotedprintable.NewWriter(&builder)
	_, _ = encodedBody.Write([]byte(body))
	_ = encodedBody.Close()
	return builder.String()
}

func mimeEncode(value string) string { return mime.QEncoding.Encode("UTF-8", value) }

func isDeterministicEmailFormatError(err error) bool {
	if err == nil {
		return false
	}
	value := strings.ToLower(err.Error())
	return strings.Contains(value, "line too long") || strings.Contains(value, "line length exceeded")
}

func compactEmailDegradedReasons(body string) string {
	if strings.TrimSpace(body) == "" {
		return body
	}
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		prefix := ""
		for _, candidate := range []string{"- 降级原因：", "降级原因："} {
			if strings.HasPrefix(trimmed, candidate) {
				prefix = candidate
				break
			}
		}
		if prefix == "" {
			result = append(result, line)
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
		reasons := compactEmailReasonList(raw)
		result = append(result, "- 降级原因摘要：")
		result = append(result, reasons...)
	}
	return strings.Join(result, "\n")
}

func compactEmailReasonList(raw string) []string {
	const maxReasons = 8
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == '；' || r == ';' || r == '\n' || r == '\r' })
	seen := make(map[string]struct{}, len(parts))
	reasons := make([]string, 0, len(parts))
	truncatedContent := false
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, exists := seen[part]; exists {
			continue
		}
		seen[part] = struct{}{}
		compact, truncated := truncateEmailRunes(part, 80)
		truncatedContent = truncatedContent || truncated
		reasons = append(reasons, compact)
	}
	if len(reasons) == 0 {
		return []string{"  - 未提供；完整明细见数据库审计"}
	}
	omitted := 0
	if len(reasons) > maxReasons {
		omitted = len(reasons) - maxReasons
		reasons = reasons[:maxReasons]
	}
	result := make([]string, 0, len(reasons)+1)
	for _, reason := range reasons {
		result = append(result, "  - "+reason)
	}
	if omitted > 0 {
		result = append(result, fmt.Sprintf("  - 其余%d项完整明细见数据库审计", omitted))
	} else if truncatedContent {
		result = append(result, "  - 部分内容已截短，完整明细见数据库审计")
	}
	return result
}

func truncateEmailRunes(value string, maximum int) (string, bool) {
	runes := []rune(strings.TrimSpace(value))
	if maximum <= 0 || len(runes) <= maximum {
		return string(runes), false
	}
	return string(runes[:maximum]) + "…", true
}
