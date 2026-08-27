package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"go-stock/backend/data"
	"go-stock/backend/db"
	"go-stock/backend/research"
	"go-stock/backend/research2"
	"go-stock/internal/migrations"

	"gorm.io/gorm"
)

type fixedCalendar struct{}

func (fixedCalendar) IsTradingDay(context.Context, time.Time) (bool, error) { return true, nil }

type minuteRow struct {
	StockCode string
	TradeTime int64
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    float64
	Amount    float64
}

type aggregate struct {
	Code   string
	Name   string
	Open   float64
	Close  float64
	High   float64
	Low    float64
	Volume float64
	Amount float64
	Bars   int
}

type historicalCollector struct {
	mainDB   *gorm.DB
	minuteDB *gorm.DB
	date     string
}

func (c historicalCollector) Collect(_ context.Context, cutoff time.Time) (research2.Evidence, error) {
	start := time.Date(cutoff.Year(), cutoff.Month(), cutoff.Day(), 9, 30, 0, 0, cutoff.Location()).UnixMilli()
	end := cutoff.UnixMilli()
	var rows []minuteRow
	if err := c.minuteDB.Table("minute_bar").Where("trade_time BETWEEN ? AND ?", start, end).Order("stock_code ASC, trade_time ASC").Find(&rows).Error; err != nil {
		return research2.Evidence{}, err
	}
	if len(rows) == 0 {
		return research2.Evidence{}, errors.New("historical 09:55 minute evidence is empty")
	}
	names := map[string]string{}
	var basics []struct{ TsCode, Name string }
	_ = c.mainDB.Table("tushare_stock_basic").Select("ts_code, name").Find(&basics).Error
	for _, item := range basics {
		names[strings.ToUpper(strings.TrimSpace(item.TsCode))] = strings.TrimSpace(item.Name)
	}
	values := map[string]*aggregate{}
	for _, row := range rows {
		code := strings.ToUpper(strings.TrimSpace(row.StockCode))
		item := values[code]
		if item == nil {
			item = &aggregate{Code: code, Name: names[code], Open: row.Open, High: row.High, Low: row.Low}
			values[code] = item
		}
		item.Close = row.Close
		item.High = math.Max(item.High, row.High)
		if item.Low == 0 || (row.Low > 0 && row.Low < item.Low) {
			item.Low = row.Low
		}
		item.Volume += row.Volume
		item.Amount += row.Amount
		item.Bars++
	}
	eligible := make([]aggregate, 0, len(values))
	up, down := 0, 0
	for _, value := range values {
		if value.Open <= 0 || value.Close <= 0 || value.Bars < 20 {
			continue
		}
		change := (value.Close/value.Open - 1) * 100
		if change > 0 {
			up++
		} else if change < 0 {
			down++
		}
		code := value.Code
		name := strings.ToUpper(value.Name)
		if change <= 0 || strings.Contains(name, "ST") || strings.Contains(name, "退") || value.Close*100 > research2.InitialCash {
			continue
		}
		if !(strings.HasPrefix(code, "60") && strings.HasSuffix(code, ".SH")) && !(strings.HasPrefix(code, "00") && strings.HasSuffix(code, ".SZ")) {
			continue
		}
		eligible = append(eligible, *value)
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		left := (eligible[i].Close/eligible[i].Open-1)*100*5 + math.Log10(math.Max(1, eligible[i].Amount))
		right := (eligible[j].Close/eligible[j].Open-1)*100*5 + math.Log10(math.Max(1, eligible[j].Amount))
		return left > right
	})
	if len(eligible) > 12 {
		eligible = eligible[:12]
	}
	if len(eligible) == 0 {
		return research2.Evidence{}, errors.New("historical evidence has no eligible candidate")
	}
	candidates := make([]research.StockCandidate, 0, len(eligible))
	var prompt strings.Builder
	fmt.Fprintf(&prompt, "数据源：本机分钟线历史缓存（只读隔离副本）\n交易日：%s\n证据截止：%s\n覆盖股票：%d；上涨：%d；下跌：%d。\n", c.date, cutoff.Format("2006-01-02 15:04:05"), len(values), up, down)
	prompt.WriteString("当前隔离回放没有可靠的截止前新闻催化证据；不得编造催化，证据不足时应明确空仓。\n\n")
	prompt.WriteString("|代码|名称|09:30价格|09:55价格|区间涨幅%|最高|最低|成交量|成交额|分钟数|\n|---|---|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, item := range eligible {
		code := strings.TrimSuffix(item.Code, ".SH")
		prefix := "sh"
		if strings.HasSuffix(item.Code, ".SZ") {
			code, prefix = strings.TrimSuffix(item.Code, ".SZ"), "sz"
		}
		candidates = append(candidates, research.StockCandidate{Code: prefix + code, Name: item.Name})
		fmt.Fprintf(&prompt, "|%s%s|%s|%.2f|%.2f|%.2f|%.2f|%.2f|%.0f|%.0f|%d|\n", prefix, code, item.Name, item.Open, item.Close, (item.Close/item.Open-1)*100, item.High, item.Low, item.Volume, item.Amount, item.Bars)
	}
	status, _ := json.Marshal([]map[string]any{{"sourceId": "local-minute-replay", "sourceName": "本机分钟线历史缓存", "category": "market_stock", "collectedAt": cutoff, "status": "ok", "stockCount": len(values)}})
	return research2.Evidence{Prompt: prompt.String(), SourceStatusJSON: string(status), Candidates: candidates}, nil
}

func latestCompleteDate(database *gorm.DB) (string, error) {
	var result struct{ TradingDate string }
	err := database.Raw(`SELECT strftime('%Y-%m-%d', trade_time / 1000, 'unixepoch', 'localtime') AS trading_date
		FROM minute_bar GROUP BY trading_date HAVING COUNT(DISTINCT stock_code) >= 200 ORDER BY trading_date DESC LIMIT 1`).Scan(&result).Error
	if err != nil {
		return "", err
	}
	if result.TradingDate == "" {
		return "", errors.New("no historical trading day has at least 200 cached stocks")
	}
	return result.TradingDate, nil
}

func main() {
	mainPath := flag.String("main", "", "isolated writable stock.db copy")
	flag.Parse()
	if strings.TrimSpace(*mainPath) == "" {
		fmt.Fprintln(os.Stderr, "--main is required")
		os.Exit(2)
	}
	db.InitSilent(*mainPath)
	defer db.Close()
	if err := migrations.MigrateAll(db.Dao, db.MinuteDao); err != nil {
		fmt.Fprintln(os.Stderr, "migrate isolated databases:", err)
		os.Exit(1)
	}
	setting := data.GetSettingConfig()
	if setting == nil || setting.Settings == nil || setting.AIAnalysisConfigID == 0 {
		fmt.Fprintln(os.Stderr, "active AI configuration is unavailable")
		os.Exit(1)
	}
	config := research2.EmailConfig{Enabled: true, To: setting.Research2EmailTo, From: setting.Research2EmailFrom, SMTPHost: setting.Research2EmailSMTPHost, SMTPPort: setting.Research2EmailSMTPPort, Username: setting.Research2EmailSMTPUser, Password: setting.Research2EmailSMTPPass, Timeout: 20 * time.Second}
	if _, _, err := research2.ValidateEmailConfig(config); err != nil {
		fmt.Fprintln(os.Stderr, "isolated email configuration:", err)
		os.Exit(1)
	}
	tradingDate, err := latestCompleteDate(db.MinuteDao)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	location, _ := time.LoadLocation("Asia/Shanghai")
	date, _ := time.ParseInLocation("2006-01-02", tradingDate, location)
	cutoff := time.Date(date.Year(), date.Month(), date.Day(), 9, 55, 0, 0, location)
	repository := research2.NewRepository(db.Dao)
	if err = repository.EnsureAccount(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	runner := research2.NewRunner(repository, data.NewResearchAIClient(int(setting.AIAnalysisConfigID)), historicalCollector{mainDB: db.Dao, minuteDB: db.MinuteDao, date: tradingDate}, fixedCalendar{})
	fixedNow := cutoff.Add(2 * time.Minute)
	runner.ConfigureReplayClock(func() time.Time { return fixedNow }, func(context.Context, time.Time) error { return nil })
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	run, err := runner.Run(ctx, cutoff.Add(-5*time.Minute))
	if err != nil || !research2.EligibleEmailStatus(run.Status) || strings.TrimSpace(run.ReportMarkdown) == "" {
		fmt.Fprintf(os.Stderr, "real model report failed: status=%s err=%v reason=%s\n", run.Status, err, run.FailureReason)
		os.Exit(1)
	}
	email := research2.NewEmailService(repository, nil)
	created, err := email.Queue(ctx, run, config)
	if err != nil || !created {
		fmt.Fprintf(os.Stderr, "queue real report email: created=%v err=%v\n", created, err)
		os.Exit(1)
	}
	subject := reportSubject(run)
	if err = repository.DB().Model(&research2.EmailDelivery{}).Where("analysis_run_id = ?", run.RunID).Update("subject", "[链路测试]"+subject).Error; err != nil {
		fmt.Fprintln(os.Stderr, "mark test subject:", err)
		os.Exit(1)
	}
	if err = email.ProcessDue(ctx, config); err != nil {
		fmt.Fprintln(os.Stderr, "send real report email:", err)
		os.Exit(1)
	}
	stored, err := repository.GetRun(ctx, run.RunID)
	if err != nil || stored.EmailDeliveryStatus != research2.EmailStatusSent {
		fmt.Fprintf(os.Stderr, "email delivery not sent: status=%s err=%v detail=%s\n", stored.EmailDeliveryStatus, err, stored.EmailLastError)
		os.Exit(1)
	}
	output := map[string]any{"isolated": true, "tradingDate": tradingDate, "coveredStocksAtLeast": 200, "runStatus": run.Status, "recommendationCount": run.RecommendationCount, "reportCharacters": len([]rune(run.ReportMarkdown)), "provider": run.ProviderName, "model": run.ModelName, "emailStatus": stored.EmailDeliveryStatus, "emailAttempts": stored.EmailAttemptCount, "emailSentAt": stored.EmailSentAt, "productionTradesCreated": 0}
	encoded, _ := json.MarshalIndent(output, "", "  ")
	fmt.Println(string(encoded))
}

func reportSubject(run research2.AnalysisRun) string {
	suffix := fmt.Sprintf("分析报告（%d只）", run.RecommendationCount)
	if run.Status == "no_recommendation" {
		suffix = "无推荐"
	}
	return fmt.Sprintf("[go-stock][研究中心2] %s %s", run.TradingDate, suffix)
}
