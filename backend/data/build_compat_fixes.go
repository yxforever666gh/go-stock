package data

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	"gorm.io/gorm"
)

type diemengCallAuctionItem struct {
	StockCode      string    `json:"stock_code"`
	TradeTime      string    `json:"trade_time"`
	CurrentPrice   float64   `json:"current_price"`
	Amount         float64   `json:"amount"`
	Volume         float64   `json:"volume"`
	Open           float64   `json:"open"`
	High           float64   `json:"high"`
	Low            float64   `json:"low"`
	PreClose       float64   `json:"pre_close"`
	TurnoverRate   float64   `json:"turnover_rate"`
	CommitteeRatio float64   `json:"committee_ratio"`
	VolumeRatio    float64   `json:"volume_ratio"`
	BidPrice       []float64 `json:"bid_price"`
	AskPrice       []float64 `json:"ask_price"`
	BidVol         []float64 `json:"bid_vol"`
	AskVol         []float64 `json:"ask_vol"`
}

type yieldEmailRuntimeConfig struct {
	From     string
	To       []string
	Host     string
	Port     int
	Username string
	Password string
}

type mailAttachment struct {
	Filename    string
	ContentType string
	Content     []byte
}

type emailAuditPayload struct {
	SendType     string
	TriggeredAt  time.Time
	Recipients   []string
	Subject      string
	Report       *models.AIResponseResult
	Attachments  []mailAttachment
	ExtraSummary string
}

const latestAIReportCronFreshWindow = 30 * time.Minute

func fetchDiemengCallAuctionData(stockCode string, start, end time.Time) ([]diemengCallAuctionItem, error) {
	_ = stockCode
	_ = start
	_ = end
	return []diemengCallAuctionItem{}, nil
}

func sendYieldXLSXEmailIfEnabled(reason string, fullRecalc bool) {
	cfg := GetSettingConfig()
	if cfg == nil || cfg.Settings == nil {
		return
	}
	if !cfg.YieldEmailEnable || !cfg.YieldEmailCronEnabled {
		return
	}
	if !shouldAutoSendYieldXLSX(reason, fullRecalc) {
		logger.SugaredLogger.Infof("skip async yield xlsx email auto-send: reason=%s fullRecalc=%v", reason, fullRecalc)
		return
	}
	rowCount, err := SendYieldEmailXLSXNow()
	if err != nil {
		logger.SugaredLogger.Warnf("async yield xlsx email auto-send failed: reason=%s fullRecalc=%v err=%v", reason, fullRecalc, err)
		return
	}
	logger.SugaredLogger.Infof("async yield xlsx email auto-send success: reason=%s fullRecalc=%v rows=%d", reason, fullRecalc, rowCount)
}

func shouldAutoSendYieldXLSX(reason string, fullRecalc bool) bool {
	_ = strings.TrimSpace(reason)
	_ = fullRecalc
	// 收益率重算后的自动邮件已关闭，邮件只保留显式触发或定时任务路径。
	return false
}

func SendYieldEmailTestMessage() error {
	triggeredAt := time.Now()
	subject := fmt.Sprintf("go-stock 测试邮件 [%s]", time.Now().Format("2006-01-02 15:04:05"))
	cfg, err := loadYieldEmailRuntimeConfig()
	if err != nil {
		recordEmailSendLog(emailAuditPayload{
			SendType:     "test",
			TriggeredAt:  triggeredAt,
			Subject:      subject,
			ExtraSummary: "测试邮件",
		}, err)
		return err
	}
	body := strings.Join([]string{
		"这是一封来自 go-stock 的测试邮件。",
		"",
		"如果你收到了这封邮件，说明 SMTP 配置可用。",
		"发送时间: " + time.Now().Format("2006-01-02 15:04:05"),
	}, "\n")
	sendErr := classifyEmailSendError(sendSMTPMessage(cfg, subject, body, nil))
	recordEmailSendLog(emailAuditPayload{
		SendType:     "test",
		TriggeredAt:  triggeredAt,
		Recipients:   cfg.To,
		Subject:      subject,
		ExtraSummary: "测试邮件",
	}, sendErr)
	return sendErr
}

func SendYieldEmailXLSXNow() (int, error) {
	triggeredAt := time.Now()
	subject := fmt.Sprintf("go-stock 收益率 XLSX [%s]", time.Now().Format("2006-01-02 15:04:05"))
	cfg, err := loadYieldEmailRuntimeConfig()
	if err != nil {
		recordEmailSendLog(emailAuditPayload{
			SendType:     "xlsx",
			TriggeredAt:  triggeredAt,
			Subject:      subject,
			ExtraSummary: "收益率记录数=0",
		}, err)
		return 0, err
	}
	items, err := loadYieldEmailItems()
	if err != nil {
		recordEmailSendLog(emailAuditPayload{
			SendType:     "xlsx",
			TriggeredAt:  triggeredAt,
			Recipients:   cfg.To,
			Subject:      subject,
			ExtraSummary: "收益率记录数=0",
		}, err)
		return 0, err
	}
	payload, err := buildYieldXLSXAttachment(items)
	if err != nil {
		recordEmailSendLog(emailAuditPayload{
			SendType:     "xlsx",
			TriggeredAt:  triggeredAt,
			Recipients:   cfg.To,
			Subject:      subject,
			ExtraSummary: fmt.Sprintf("收益率记录数=%d", len(items)),
		}, err)
		return 0, err
	}
	attachment := mailAttachment{
		Filename:    fmt.Sprintf("go-stock-yield-%s.xlsx", time.Now().Format("20060102-150405")),
		ContentType: yieldEmailXLSXContentType,
		Content:     payload,
	}
	body := strings.Join([]string{
		"附件为当前收益率全量 XLSX，字段与网页收益率列表保持一致。",
		"",
		fmt.Sprintf("记录数: %d", len(items)),
	}, "\n")
	err = sendSMTPMessage(cfg, subject, body, []mailAttachment{attachment})
	sendErr := classifyEmailSendError(err)
	recordEmailSendLog(emailAuditPayload{
		SendType:     "xlsx",
		TriggeredAt:  triggeredAt,
		Recipients:   cfg.To,
		Subject:      subject,
		Attachments:  []mailAttachment{attachment},
		ExtraSummary: fmt.Sprintf("收益率记录数=%d", len(items)),
	}, sendErr)
	if sendErr != nil {
		return 0, sendErr
	}
	return len(items), nil
}

func SendLatestAIAnalysisReportEmail() (*models.AIResponseResult, error) {
	return sendLatestAIAnalysisReportEmailWithType("manual_ai", func(time.Time) (*models.AIResponseResult, error) {
		return loadLatestAIAnalysisReport()
	})
}

func SendLatestAIAnalysisReportEmailForCron() (*models.AIResponseResult, error) {
	return sendLatestAIAnalysisReportEmailWithType("cron_ai", loadLatestAIAnalysisReportForCron)
}

func SendMarketSummaryEmail(sendType string, report *models.AIResponseResult, failureReason string) error {
	triggeredAt := time.Now()
	sendType = strings.TrimSpace(sendType)
	if sendType == "" {
		sendType = "manual_summary"
	}
	if alreadySent, err := hasSuccessfulMarketSummaryEmailLog(sendType, report, failureReason); err == nil && alreadySent {
		logger.SugaredLogger.Infof("跳过重复的市场资讯邮件发送: type=%s reportTime=%s", sendType, formatReportCreatedAt(report))
		return nil
	} else if err != nil {
		logger.SugaredLogger.Warnf("检查市场资讯邮件重复发送失败: type=%s err=%v", sendType, err)
	}

	cfg, err := loadYieldEmailRuntimeConfig()
	if err != nil {
		recordEmailSendLog(emailAuditPayload{
			SendType:     sendType,
			TriggeredAt:  triggeredAt,
			ExtraSummary: strings.TrimSpace(failureReason),
		}, err)
		return err
	}

	subject, body, attachments, extraSummary, err := buildMarketSummaryEmailPayload(triggeredAt, report, failureReason)
	if err != nil {
		recordEmailSendLog(emailAuditPayload{
			SendType:     sendType,
			TriggeredAt:  triggeredAt,
			Recipients:   cfg.To,
			Report:       report,
			ExtraSummary: extraSummary,
		}, err)
		return err
	}

	sendErr := classifyEmailSendError(sendSMTPMessage(cfg, subject, body, attachments))
	logReport := report
	if strings.TrimSpace(failureReason) != "" {
		logReport = nil
	}
	recordEmailSendLog(emailAuditPayload{
		SendType:     sendType,
		TriggeredAt:  triggeredAt,
		Recipients:   cfg.To,
		Subject:      subject,
		Report:       logReport,
		Attachments:  attachments,
		ExtraSummary: extraSummary,
	}, sendErr)
	return sendErr
}

func hasSuccessfulMarketSummaryEmailLog(sendType string, report *models.AIResponseResult, failureReason string) (bool, error) {
	if strings.TrimSpace(failureReason) != "" || report == nil || report.CreatedAt.IsZero() {
		return false, nil
	}
	var count int64
	err := db.Dao.Model(&models.EmailSendLog{}).
		Where("send_type = ? AND status = ?", strings.TrimSpace(sendType), "success").
		Where("report_stock_code = ? AND report_stock_name = ?", strings.TrimSpace(report.StockCode), strings.TrimSpace(report.StockName)).
		Where("report_created_at = ?", report.CreatedAt).
		Count(&count).Error
	return count > 0, err
}

func formatReportCreatedAt(report *models.AIResponseResult) string {
	if report == nil || report.CreatedAt.IsZero() {
		return ""
	}
	return report.CreatedAt.Format("2006-01-02 15:04:05")
}

func sendLatestAIAnalysisReportEmailWithType(sendType string, reportLoader func(time.Time) (*models.AIResponseResult, error)) (*models.AIResponseResult, error) {
	triggeredAt := time.Now()
	cfg, err := loadYieldEmailRuntimeConfig()
	if err != nil {
		recordEmailSendLog(emailAuditPayload{
			SendType:    sendType,
			TriggeredAt: triggeredAt,
		}, err)
		return nil, err
	}
	if reportLoader == nil {
		reportLoader = func(time.Time) (*models.AIResponseResult, error) {
			return loadLatestAIAnalysisReport()
		}
	}
	report, err := reportLoader(triggeredAt)
	if err != nil {
		recordEmailSendLog(emailAuditPayload{
			SendType:    sendType,
			TriggeredAt: triggeredAt,
			Recipients:  cfg.To,
		}, err)
		return nil, err
	}
	subject := buildAIReportSubject(report)
	body, attachment, err := buildAIReportEmail(report)
	if err != nil {
		recordEmailSendLog(emailAuditPayload{
			SendType:     sendType,
			TriggeredAt:  triggeredAt,
			Recipients:   cfg.To,
			Subject:      subject,
			Report:       report,
			ExtraSummary: summarizeReportQuestion(report),
		}, err)
		return nil, err
	}
	attachments := make([]mailAttachment, 0, 1)
	if attachment != nil {
		attachments = append(attachments, *attachment)
	}
	sendErr := classifyEmailSendError(sendSMTPMessage(cfg, subject, body, attachments))
	recordEmailSendLog(emailAuditPayload{
		SendType:     sendType,
		TriggeredAt:  triggeredAt,
		Recipients:   cfg.To,
		Subject:      subject,
		Report:       report,
		Attachments:  attachments,
		ExtraSummary: summarizeReportQuestion(report),
	}, sendErr)
	if sendErr != nil {
		return nil, sendErr
	}
	return report, nil
}

func buildMarketSummaryEmailPayload(triggeredAt time.Time, report *models.AIResponseResult, failureReason string) (string, string, []mailAttachment, string, error) {
	reason := strings.TrimSpace(failureReason)
	if reason != "" {
		subject := fmt.Sprintf("go-stock AI总结报错 [%s]", triggeredAt.Format("2006-01-02 15:04:05"))
		body := "这是AI总结报错，原因：" + reason
		return subject, body, nil, "AI总结报错: " + reason, nil
	}
	if report == nil {
		return "", "", nil, "", errors.New("AI 总结报告为空")
	}

	content := strings.TrimSpace(report.Content)
	if content == "" {
		return "", "", nil, "", errors.New("AI 总结报告内容为空")
	}

	reportTime := report.CreatedAt
	if reportTime.IsZero() {
		reportTime = triggeredAt
	}
	summaryAttachment := mailAttachment{
		Filename:    sanitizeMailFilename(buildMarketSummaryFilename(reportTime)),
		ContentType: "text/plain; charset=utf-8",
		Content:     []byte(content),
	}

	items, err := loadYieldEmailItems()
	if err != nil {
		return "", "", nil, "", err
	}
	xlsxPayload, err := buildYieldXLSXAttachment(items)
	if err != nil {
		return "", "", nil, "", err
	}
	yieldAttachment := mailAttachment{
		Filename:    fmt.Sprintf("go-stock-yield-%s.xlsx", triggeredAt.Format("20060102-150405")),
		ContentType: yieldEmailXLSXContentType,
		Content:     xlsxPayload,
	}

	subject := buildMarketSummarySubject(reportTime)
	lines := []string{
		"市场资讯 AI 总结报告见正文，最新股票收益率全量 XLSX 见附件。",
		"",
		fmt.Sprintf("生成时间: %s", reportTime.Format("2006-01-02 15:04:05")),
	}
	if modelName := strings.TrimSpace(report.ModelName); modelName != "" {
		lines = append(lines, "模型: "+modelName)
	}
	if question := strings.TrimSpace(report.Question); question != "" {
		lines = append(lines, "问题: "+question)
	}
	lines = append(lines,
		fmt.Sprintf("收益率记录数: %d", len(items)),
		"",
		content,
	)
	return subject, strings.Join(lines, "\n"), []mailAttachment{summaryAttachment, yieldAttachment}, fmt.Sprintf("市场资讯AI总结 + 收益率记录数=%d", len(items)), nil
}

func buildMarketSummarySubject(reportTime time.Time) string {
	if reportTime.IsZero() {
		reportTime = time.Now()
	}
	return fmt.Sprintf("go-stock 市场资讯AI总结 [%s]", reportTime.Format("2006-01-02 15:04:05"))
}

func buildMarketSummaryFilename(reportTime time.Time) string {
	if reportTime.IsZero() {
		reportTime = time.Now()
	}
	return fmt.Sprintf("market-summary-%s.txt", reportTime.Format("20060102-150405"))
}

func loadYieldEmailRuntimeConfig() (*yieldEmailRuntimeConfig, error) {
	cfg := GetSettingConfig()
	if cfg == nil || cfg.Settings == nil {
		return nil, errors.New("邮件配置为空")
	}
	if !cfg.YieldEmailEnable {
		return nil, errors.New("邮件功能未启用")
	}
	to, err := parseEmailList(cfg.YieldEmailTo)
	if err != nil {
		return nil, err
	}
	if len(to) == 0 {
		return nil, errors.New("未配置收件邮箱")
	}
	from := strings.TrimSpace(cfg.YieldEmailFrom)
	if from == "" {
		return nil, errors.New("未配置发件邮箱")
	}
	if _, err := mail.ParseAddress(from); err != nil {
		return nil, fmt.Errorf("发件邮箱格式错误: %s", from)
	}
	host := strings.TrimSpace(cfg.YieldEmailSMTPHost)
	if host == "" {
		host = inferSMTPHost(from)
	}
	if host == "" {
		return nil, errors.New("未配置 SMTP 主机")
	}
	port := cfg.YieldEmailSMTPPort
	if port <= 0 {
		port = inferSMTPPort(host)
	}
	if port <= 0 {
		port = 465
	}
	username := strings.TrimSpace(cfg.YieldEmailSMTPUsername)
	if username == "" {
		username = from
	}
	password := strings.TrimSpace(cfg.YieldEmailSMTPPassword)
	if password == "" {
		return nil, errors.New("未配置 SMTP 授权码")
	}
	return &yieldEmailRuntimeConfig{
		From:     from,
		To:       to,
		Host:     host,
		Port:     port,
		Username: username,
		Password: password,
	}, nil
}

func inferSMTPHost(from string) string {
	idx := strings.LastIndex(strings.TrimSpace(from), "@")
	if idx < 0 || idx == len(from)-1 {
		return ""
	}
	domain := strings.ToLower(strings.TrimSpace(from[idx+1:]))
	switch domain {
	case "qq.com":
		return "smtp.qq.com"
	case "163.com":
		return "smtp.163.com"
	case "126.com":
		return "smtp.126.com"
	case "gmail.com":
		return "smtp.gmail.com"
	case "outlook.com", "hotmail.com", "live.com":
		return "smtp-mail.outlook.com"
	default:
		return "smtp." + domain
	}
}

func inferSMTPPort(host string) int {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "smtp-mail.outlook.com":
		return 587
	default:
		return 465
	}
}

func loadLatestAIAnalysisReport() (*models.AIResponseResult, error) {
	report := &models.AIResponseResult{}
	err := db.Dao.Model(&models.AIResponseResult{}).Order("created_at desc").First(report).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("数据库中暂无 AI 分析报告")
		}
		return nil, err
	}
	if strings.TrimSpace(report.Content) == "" {
		return nil, errors.New("最新 AI 分析报告内容为空")
	}
	sanitizeAIResponseResultForDisplay(report)
	return report, nil
}

func loadLatestAIAnalysisReportForCron(triggeredAt time.Time) (*models.AIResponseResult, error) {
	if triggeredAt.IsZero() {
		triggeredAt = time.Now()
	}
	windowStart := triggeredAt.Add(-latestAIReportCronFreshWindow)
	report := &models.AIResponseResult{}
	err := db.Dao.Model(&models.AIResponseResult{}).
		Where("created_at >= ? AND created_at <= ?", windowStart, triggeredAt).
		Order("created_at desc").
		First(report).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("当前时段内暂无可发送的 AI 分析报告（仅发送最近%d分钟内生成的报告）", int(latestAIReportCronFreshWindow/time.Minute))
		}
		return nil, err
	}
	if strings.TrimSpace(report.Content) == "" {
		return nil, errors.New("当前时段内最新 AI 分析报告内容为空")
	}
	sanitizeAIResponseResultForDisplay(report)
	return report, nil
}

func buildAIReportSubject(report *models.AIResponseResult) string {
	base := "go-stock 最新 AI 分析报告"
	if report == nil {
		return base
	}
	name := strings.TrimSpace(report.StockName)
	code := strings.TrimSpace(report.StockCode)
	switch {
	case name != "" && code != "":
		return fmt.Sprintf("%s - %s [%s]", base, name, code)
	case name != "":
		return fmt.Sprintf("%s - %s", base, name)
	case code != "":
		return fmt.Sprintf("%s - %s", base, code)
	default:
		return base
	}
}

func buildAIReportEmail(report *models.AIResponseResult) (string, *mailAttachment, error) {
	if report == nil {
		return "", nil, errors.New("AI 分析报告为空")
	}
	content := strings.TrimSpace(report.Content)
	if content == "" {
		return "", nil, errors.New("AI 分析报告内容为空")
	}
	lines := []string{
		"最新 AI 分析报告已附在正文中。",
		"",
		fmt.Sprintf("标题: %s", buildAIReportSubject(report)),
		fmt.Sprintf("生成时间: %s", report.CreatedAt.Format("2006-01-02 15:04:05")),
	}
	if q := strings.TrimSpace(report.Question); q != "" {
		lines = append(lines, "问题: "+q)
	}
	lines = append(lines, "", content)
	body := strings.Join(lines, "\n")
	filename := sanitizeMailFilename(buildAIReportFilename(report))
	attachment := &mailAttachment{
		Filename:    filename,
		ContentType: "text/plain; charset=utf-8",
		Content:     []byte(body),
	}
	return body, attachment, nil
}

func buildAIReportFilename(report *models.AIResponseResult) string {
	parts := make([]string, 0, 3)
	if report != nil {
		if name := strings.TrimSpace(report.StockName); name != "" {
			parts = append(parts, name)
		}
		if code := strings.TrimSpace(report.StockCode); code != "" {
			parts = append(parts, "["+code+"]")
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "latest-ai-analysis-report")
	}
	return strings.Join(parts, "") + ".txt"
}

func sanitizeMailFilename(name string) string {
	raw := strings.TrimSpace(name)
	if raw == "" {
		raw = "attachment.txt"
	}
	raw = strings.Map(func(r rune) rune {
		switch r {
		case '\\', '/', ':', '*', '?', '"', '<', '>', '|', '\n', '\r', '\t':
			return '_'
		default:
			return r
		}
	}, raw)
	if filepath.Ext(raw) == "" {
		raw += ".txt"
	}
	return raw
}

func sendSMTPMessage(cfg *yieldEmailRuntimeConfig, subject, body string, attachments []mailAttachment) error {
	if cfg == nil {
		return errors.New("邮件配置为空")
	}
	message, err := buildSMTPMessage(cfg, subject, body, attachments)
	if err != nil {
		return err
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	if cfg.Port == 465 {
		return sendSMTPSSL(addr, cfg, message)
	}
	return sendSMTPSTARTTLS(addr, cfg, message)
}

func buildSMTPMessage(cfg *yieldEmailRuntimeConfig, subject, body string, attachments []mailAttachment) ([]byte, error) {
	buf := &bytes.Buffer{}
	writer := multipart.NewWriter(buf)
	headers := []string{
		"From: " + cfg.From,
		"To: " + strings.Join(cfg.To, ", "),
		"Subject: " + encodeRFC2047(subject),
		"MIME-Version: 1.0",
		`Content-Type: multipart/mixed; boundary="` + writer.Boundary() + `"`,
	}
	if _, err := buf.WriteString(strings.Join(headers, "\r\n") + "\r\n\r\n"); err != nil {
		return nil, err
	}
	textHeader := make(textproto.MIMEHeader)
	textHeader.Set("Content-Type", `text/plain; charset="utf-8"`)
	textHeader.Set("Content-Transfer-Encoding", "base64")
	textPart, err := writer.CreatePart(textHeader)
	if err != nil {
		return nil, err
	}
	if err := writeBase64Chunked(textPart, []byte(body)); err != nil {
		return nil, err
	}
	for _, attachment := range attachments {
		if len(attachment.Content) == 0 {
			continue
		}
		partHeader := make(textproto.MIMEHeader)
		contentType := strings.TrimSpace(attachment.ContentType)
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		partHeader.Set("Content-Type", contentType+`; name="`+mime.QEncoding.Encode("utf-8", attachment.Filename)+`"`)
		partHeader.Set("Content-Transfer-Encoding", "base64")
		partHeader.Set("Content-Disposition", `attachment; filename="`+mime.QEncoding.Encode("utf-8", attachment.Filename)+`"`)
		part, err := writer.CreatePart(partHeader)
		if err != nil {
			return nil, err
		}
		if err := writeBase64Chunked(part, attachment.Content); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func sendSMTPSSL(addr string, cfg *yieldEmailRuntimeConfig, message []byte) error {
	dialer, bindDesc := newDirectSMTPDialer()
	conn, err := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{
		ServerName: cfg.Host,
		MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		return err
	}
	defer conn.Close()
	if bindDesc != "" {
		logger.SugaredLogger.Infof("SMTP 直连已启用: host=%s bind=%s", cfg.Host, bindDesc)
	}
	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return err
	}
	defer client.Quit()
	return sendWithSMTPClient(client, cfg, message)
}

func sendSMTPSTARTTLS(addr string, cfg *yieldEmailRuntimeConfig, message []byte) error {
	dialer, bindDesc := newDirectSMTPDialer()
	conn, err := dialer.Dial("tcp", addr)
	if err != nil {
		return err
	}
	if bindDesc != "" {
		logger.SugaredLogger.Infof("SMTP 直连已启用: host=%s bind=%s", cfg.Host, bindDesc)
	}
	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer client.Quit()
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	return sendWithSMTPClient(client, cfg, message)
}

func sendWithSMTPClient(client *smtp.Client, cfg *yieldEmailRuntimeConfig, message []byte) error {
	if ok, _ := client.Extension("AUTH"); ok {
		auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(cfg.From); err != nil {
		return err
	}
	for _, recipient := range cfg.To {
		if err := client.Rcpt(recipient); err != nil {
			return err
		}
	}
	wc, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := wc.Write(message); err != nil {
		_ = wc.Close()
		return err
	}
	return wc.Close()
}

func encodeRFC2047(value string) string {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return ""
	}
	return mime.QEncoding.Encode("utf-8", raw)
}

func writeBase64Chunked(w io.Writer, payload []byte) error {
	encoded := base64.StdEncoding.EncodeToString(payload)
	for len(encoded) > 76 {
		if _, err := io.WriteString(w, encoded[:76]+"\r\n"); err != nil {
			return err
		}
		encoded = encoded[76:]
	}
	if _, err := io.WriteString(w, encoded+"\r\n"); err != nil {
		return err
	}
	return nil
}

func classifyEmailSendError(err error) error {
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(err.Error())
	if strings.HasPrefix(msg, "邮件发送失败:") {
		return err
	}
	lower := strings.ToLower(msg)
	switch {
	case msg == "EOF" || strings.Contains(lower, "connection unexpectedly closed"):
		return fmt.Errorf("邮件发送失败: SMTP 连接被服务器提前关闭，常见原因是 QQ SMTP 未开启、授权码错误，或账号被风控。原始信息: %s", msg)
	case strings.Contains(msg, "535"):
		return fmt.Errorf("邮件发送失败: SMTP 认证被拒绝，请检查邮箱 SMTP 服务是否开启、授权码是否正确。原始信息: %s", msg)
	case strings.Contains(lower, "timeout"):
		return fmt.Errorf("邮件发送失败: 连接 SMTP 服务器超时，请检查网络或稍后重试。原始信息: %s", msg)
	case strings.Contains(lower, "no such host"):
		return fmt.Errorf("邮件发送失败: SMTP 主机无法解析，请检查 SMTP 主机配置。原始信息: %s", msg)
	case strings.Contains(lower, "connection refused"):
		return fmt.Errorf("邮件发送失败: SMTP 服务器拒绝连接，请检查主机和端口配置。原始信息: %s", msg)
	default:
		return fmt.Errorf("邮件发送失败: %s", msg)
	}
}

func formatTimePtr(value *time.Time) string {
	if value == nil || value.IsZero() {
		return ""
	}
	return value.Format("2006-01-02 15:04:05")
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', 4, 64)
}

func formatFloatPtr(value *float64) string {
	if value == nil {
		return ""
	}
	return formatFloat(*value)
}

func summarizeReportQuestion(report *models.AIResponseResult) string {
	if report == nil {
		return ""
	}
	question := strings.TrimSpace(report.Question)
	if question == "" {
		return ""
	}
	runes := []rune(question)
	if len(runes) > 180 {
		return string(runes[:180]) + "..."
	}
	return question
}

func parseEmailList(input string) ([]string, error) {
	raw := strings.NewReplacer("，", ",", "；", ",", ";", ",", "\n", ",").Replace(input)
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		addr := strings.TrimSpace(part)
		if addr == "" {
			continue
		}
		if _, err := mail.ParseAddress(addr); err != nil {
			return nil, fmt.Errorf("邮箱地址格式错误: %s", addr)
		}
		key := strings.ToLower(addr)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, addr)
	}
	sort.Strings(out)
	return out, nil
}

func recordEmailSendLog(payload emailAuditPayload, sendErr error) {
	log := &models.EmailSendLog{
		SendType:     strings.TrimSpace(payload.SendType),
		TriggeredAt:  payload.TriggeredAt,
		Status:       "success",
		Recipients:   strings.Join(payload.Recipients, ","),
		Subject:      strings.TrimSpace(payload.Subject),
		ExtraSummary: strings.TrimSpace(payload.ExtraSummary),
	}
	if log.TriggeredAt.IsZero() {
		log.TriggeredAt = time.Now()
	}
	if sendErr != nil {
		log.Status = "failed"
		log.ErrorMessage = sendErr.Error()
	}
	if payload.Report != nil {
		log.ReportStockCode = strings.TrimSpace(payload.Report.StockCode)
		log.ReportStockName = strings.TrimSpace(payload.Report.StockName)
		if !payload.Report.CreatedAt.IsZero() {
			reportTime := payload.Report.CreatedAt
			log.ReportCreatedAt = &reportTime
		}
	}
	if len(payload.Attachments) > 0 {
		names := make([]string, 0, len(payload.Attachments))
		var totalBytes int64
		for _, attachment := range payload.Attachments {
			if strings.TrimSpace(attachment.Filename) != "" {
				names = append(names, attachment.Filename)
			}
			totalBytes += int64(len(attachment.Content))
		}
		log.AttachmentNames = strings.Join(names, ",")
		log.AttachmentCount = len(payload.Attachments)
		log.AttachmentBytes = totalBytes
	}
	if err := db.Dao.Create(log).Error; err != nil {
		logger.SugaredLogger.Warnf("记录邮件发送审计失败: type=%s err=%v", payload.SendType, err)
	}
}

func newDirectSMTPDialer() (*net.Dialer, string) {
	dialer := &net.Dialer{
		Timeout:   20 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	ip, desc := selectDirectSMTPBindIP()
	if ip == nil {
		return dialer, ""
	}
	dialer.LocalAddr = &net.TCPAddr{IP: ip}
	return dialer, desc
}

func selectDirectSMTPBindIP() (net.IP, string) {
	if ip, desc := directSMTPBindFromEnv(); ip != nil {
		return ip, desc
	}
	if ip, desc := directSMTPBindFromDefaultRoute(); ip != nil {
		return ip, desc
	}
	if ip, desc := directSMTPBindFromInterfaces(); ip != nil {
		return ip, desc
	}
	return nil, ""
}

func directSMTPBindFromEnv() (net.IP, string) {
	if raw := strings.TrimSpace(os.Getenv("GO_STOCK_SMTP_DIRECT_BIND_IP")); raw != "" {
		ip := net.ParseIP(raw)
		if ip != nil {
			return ip.To4(), "env-ip:" + raw
		}
	}
	if ifName := strings.TrimSpace(os.Getenv("GO_STOCK_SMTP_DIRECT_INTERFACE")); ifName != "" {
		ip := firstUsableIPv4ByInterfaceName(ifName)
		if ip != nil {
			return ip, "env-iface:" + ifName + "/" + ip.String()
		}
	}
	return nil, ""
}

func directSMTPBindFromDefaultRoute() (net.IP, string) {
	data, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return nil, ""
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines[1:] {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		if fields[1] != "00000000" {
			continue
		}
		iface := strings.TrimSpace(fields[0])
		if isVirtualOrTunnelInterface(iface) {
			continue
		}
		ip := firstUsableIPv4ByInterfaceName(iface)
		if ip != nil {
			return ip, "route-iface:" + iface + "/" + ip.String()
		}
	}
	return nil, ""
}

func directSMTPBindFromInterfaces() (net.IP, string) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, ""
	}
	type candidate struct {
		ip   net.IP
		desc string
		rank int
	}
	candidates := make([]candidate, 0, len(ifaces))
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if isVirtualOrTunnelInterface(iface.Name) {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip := extractIPv4(addr)
			if ip == nil {
				continue
			}
			rank := 1
			if isPrivateIPv4(ip) {
				rank = 0
			}
			candidates = append(candidates, candidate{
				ip:   ip,
				desc: "scan-iface:" + iface.Name + "/" + ip.String(),
				rank: rank,
			})
			break
		}
	}
	if len(candidates) == 0 {
		return nil, ""
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].rank != candidates[j].rank {
			return candidates[i].rank < candidates[j].rank
		}
		return candidates[i].desc < candidates[j].desc
	})
	return candidates[0].ip, candidates[0].desc
}

func firstUsableIPv4ByInterfaceName(ifName string) net.IP {
	iface, err := net.InterfaceByName(ifName)
	if err != nil {
		return nil
	}
	if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
		return nil
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return nil
	}
	for _, addr := range addrs {
		if ip := extractIPv4(addr); ip != nil {
			return ip
		}
	}
	return nil
}

func extractIPv4(addr net.Addr) net.IP {
	switch v := addr.(type) {
	case *net.IPNet:
		if ip := v.IP.To4(); ip != nil {
			return ip
		}
	case *net.IPAddr:
		if ip := v.IP.To4(); ip != nil {
			return ip
		}
	}
	return nil
}

func isPrivateIPv4(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsPrivate() {
		return true
	}
	return strings.HasPrefix(ip.String(), "198.19.")
}

func isVirtualOrTunnelInterface(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" {
		return true
	}
	prefixes := []string{
		"lo", "docker", "veth", "br-", "virbr", "vmnet", "zt", "tailscale",
		"tun", "tap", "utun", "mihomo", "wg", "ppp", "kube", "flannel", "cni",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}
