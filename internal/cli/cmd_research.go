package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"go-stock/backend/data"
	"go-stock/backend/db"
	"go-stock/backend/research"
)

type researchRunRepository interface {
	HasRunningAnalysis(context.Context) (bool, error)
}

type researchRunRunner interface {
	Run(context.Context, research.AnalysisRequest) (research.AnalysisRun, error)
}

func executeResearchOnce(ctx context.Context, repository researchRunRepository, runner researchRunRunner, request research.AnalysisRequest) (research.AnalysisRun, error) {
	running, err := repository.HasRunningAnalysis(ctx)
	if err != nil {
		return research.AnalysisRun{}, fmt.Errorf("检查运行中分析失败: %w", err)
	}
	if running {
		return research.AnalysisRun{}, errors.New("已有 running 状态的 AI 分析，本次运行被拒绝")
	}
	return runner.Run(ctx, request)
}

func runResearch(args []string, g GlobalOptions, stdout, stderr io.Writer) error {
	if len(args) == 0 || strings.EqualFold(args[0], "help") || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(stdout, "用法:")
		fmt.Fprintln(stdout, "  go-stock-cli [--db-path PATH] research run-once [--json] [--timeout 0]")
		fmt.Fprintln(stdout, "  go-stock-cli [--db-path PATH] research repair-missed-cash --recommendation-id ID --recommendation-id ID [--json]")
		fmt.Fprintln(stdout, "  go-stock-cli [--db-path PATH] research repair-xd-sell [--json]")
		fmt.Fprintln(stdout, "  go-stock-cli [--db-path PATH] research repair-post-sell-buy [--json]")
		return nil
	}
	if strings.EqualFold(args[0], "repair-missed-cash") {
		return runResearchMissedCashRepair(args[1:], g, stdout, stderr)
	}
	if strings.EqualFold(args[0], "repair-xd-sell") {
		return runResearchXDSellRepair(args[1:], g, stdout, stderr)
	}
	if strings.EqualFold(args[0], "repair-post-sell-buy") {
		return runResearchPostSellBuyRepair(args[1:], g, stdout, stderr)
	}
	if !strings.EqualFold(args[0], "run-once") {
		return fmt.Errorf("未知 research 子命令: %s", args[0])
	}

	fs := flag.NewFlagSet("research run-once", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := g.JSON
	timeout := time.Duration(0)
	fs.BoolVar(&jsonOut, "json", jsonOut, "JSON 输出")
	fs.DurationVar(&timeout, "timeout", timeout, "可选整轮分析超时，0 表示无限等待")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if timeout < 0 {
		return errors.New("timeout 不能小于 0")
	}

	setting := data.GetSettingConfig()
	if setting == nil || setting.Settings == nil {
		return errors.New("AI 分析设置不存在")
	}
	selected, err := data.ResolveAIAnalysisConfig(setting)
	if err != nil {
		return err
	}
	runtime, err := data.NewResearchRuntime(int(selected.ID))
	if err != nil {
		return err
	}

	ctx := context.Background()
	cancel := func() {}
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	run, runErr := executeResearchOnce(ctx, runtime.Repository, runtime.Runner, research.AnalysisRequest{
		ScheduledFor: time.Now(), AIConfigID: selected.ID,
		ProviderName: data.DisplayAIProviderName(selected), ModelName: selected.ModelName, Mode: research.AnalysisModeManual,
	})
	result := struct {
		RunID               string     `json:"runId"`
		Status              string     `json:"status"`
		ScheduledFor        time.Time  `json:"scheduledFor"`
		StartedAt           time.Time  `json:"startedAt"`
		CompletedAt         *time.Time `json:"completedAt,omitempty"`
		ProviderName        string     `json:"providerName"`
		ModelName           string     `json:"modelName"`
		RecommendationCount int        `json:"recommendationCount"`
		FailureReason       string     `json:"failureReason,omitempty"`
	}{
		RunID: run.RunID, Status: run.Status, ScheduledFor: run.ScheduledFor, StartedAt: run.StartedAt,
		CompletedAt: run.CompletedAt, ProviderName: run.ProviderName, ModelName: run.ModelName,
		RecommendationCount: run.RecommendationCount, FailureReason: run.FailureReason,
	}
	if jsonOut {
		body, marshalErr := marshalPrettyJSON(result)
		if marshalErr != nil {
			return marshalErr
		}
		_, _ = fmt.Fprintln(stdout, string(body))
	} else if run.RunID != "" {
		_, _ = fmt.Fprintf(stdout, "run=%s status=%s recommendations=%d\n", run.RunID, run.Status, run.RecommendationCount)
	}
	return runErr
}

const (
	approvedXDSellRecommendationID = "053e7c47-a538-4d6d-9dbd-61e9897d8285"
	approvedXDSellObservationID    = "046ced9c-cc28-4471-b04b-ff8faf2d0055"
	approvedXDSellDecisionEventID  = "3eab7cb7-f454-4b9b-8120-bacf1bf8ef24"
)

func runResearchXDSellRepair(args []string, g GlobalOptions, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("research repair-xd-sell", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := g.JSON
	fs.BoolVar(&jsonOut, "json", jsonOut, "JSON 输出")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("repair-xd-sell 不接受位置参数: %s", strings.Join(fs.Args(), " "))
	}
	if db.Dao == nil {
		return errors.New("主数据库未初始化")
	}
	repository := research.NewRepository(db.Dao)
	if err := repository.EnsureAccount(context.Background()); err != nil {
		return err
	}
	service := research.NewService(repository, nil, nil, data.ResearchTradingCalendar{})
	receipt, err := service.ApplyHistoricalSellCorrection(context.Background(), research.HistoricalSellCorrectionRequest{
		RecommendationID: approvedXDSellRecommendationID,
		ObservationID:    approvedXDSellObservationID,
		DecisionEventID:  approvedXDSellDecisionEventID,
		AppliedAt:        time.Now(),
	})
	if err != nil {
		return err
	}
	if jsonOut {
		body, marshalErr := marshalPrettyJSON(receipt)
		if marshalErr != nil {
			return marshalErr
		}
		_, _ = fmt.Fprintln(stdout, string(body))
		return nil
	}
	_, _ = fmt.Fprintf(stdout, "status=%s recommendation=%s quote_at=%s market_price=%.3f execution_price=%.3f quantity=%d net_pnl=%.2f cash_after=%.2f\n",
		receipt.Status, receipt.RecommendationID, receipt.QuoteAt.Format(time.RFC3339), receipt.MarketPrice,
		receipt.ExecutionPrice, receipt.Quantity, receipt.NetPnL, receipt.CashAfter)
	return nil
}

const approvedPostSellBuyRecommendationID = "d78113c2-fbd9-42c5-ad0e-f203b90ffc61"

// runResearchPostSellBuyRepair intentionally fetches the closing-session mark
// before entering research's account transaction. The service itself accepts a
// value object, so no network request can occur while the ledger is locked.
func runResearchPostSellBuyRepair(args []string, g GlobalOptions, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("research repair-post-sell-buy", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := g.JSON
	timeout := 5 * time.Minute
	fs.BoolVar(&jsonOut, "json", jsonOut, "JSON 输出")
	fs.DurationVar(&timeout, "timeout", timeout, "收盘行情采集超时")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("repair-post-sell-buy 不接受位置参数: %s", strings.Join(fs.Args(), " "))
	}
	if timeout <= 0 {
		return errors.New("timeout 必须大于 0")
	}
	if db.Dao == nil {
		return errors.New("主数据库未初始化")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	repository := research.NewRepository(db.Dao)
	if err := repository.EnsureAccount(ctx); err != nil {
		return err
	}
	recommendation, err := repository.Recommendation(ctx, approvedPostSellBuyRecommendationID)
	if err != nil {
		return fmt.Errorf("读取受控历史推荐: %w", err)
	}
	mark, err := historicalPostSellBuyCloseMark(ctx, recommendation)
	if err != nil {
		return err
	}
	service := research.NewService(repository, nil, nil, data.ResearchTradingCalendar{})
	receipt, err := service.ApplyHistoricalPostSellBuyCorrection(ctx, research.HistoricalPostSellBuyCorrectionRequest{MarkQuote: mark, AppliedAt: time.Now()})
	if err != nil {
		return err
	}
	if jsonOut {
		body, marshalErr := marshalPrettyJSON(struct {
			Receipt research.HistoricalPostSellBuyCorrectionReceipt `json:"receipt"`
			Mark    research.Quote                                  `json:"mark"`
		}{receipt, mark})
		if marshalErr != nil {
			return marshalErr
		}
		_, _ = fmt.Fprintln(stdout, string(body))
		return nil
	}
	_, _ = fmt.Fprintf(stdout, "status=%s recommendation=%s quote_at=%s mark_at=%s market_price=%.3f execution_price=%.5f quantity=%d cash_after=%.2f\n", receipt.Status, receipt.RecommendationID, receipt.QuoteAt.Format(time.RFC3339), receipt.MarkAt.Format(time.RFC3339), receipt.MarketPrice, receipt.ExecutionPrice, receipt.Quantity, receipt.CashAfter)
	return nil
}

func historicalPostSellBuyCloseMark(ctx context.Context, recommendation research.Recommendation) (research.Quote, error) {
	date := "2026-08-21"
	start := time.Date(2026, 8, 21, 9, 30, 0, 0, time.FixedZone("CST", 8*60*60))
	end := time.Date(2026, 8, 21, 15, 0, 0, 0, start.Location())
	provider := data.NewResearchChartProvider(data.NewResearchQuoteProvider())
	snapshot, err := provider.Refresh(ctx, recommendation.StockCode, start, end, []string{date})
	if err != nil {
		return research.Quote{}, fmt.Errorf("获取 %s 收盘分钟行情: %w", recommendation.StockName, err)
	}
	var selected *research.ChartMinuteBar
	for index := range snapshot.Bars {
		bar := &snapshot.Bars[index]
		local := bar.At.In(start.Location())
		if local.Format("2006-01-02") != date || local.Hour() < 14 || (local.Hour() == 14 && local.Minute() < 30) || bar.Close <= 0 {
			continue
		}
		if selected == nil || bar.At.After(selected.At) {
			selected = bar
		}
	}
	if selected == nil {
		return research.Quote{}, errors.New("未取得 2026-08-21 收盘时段的中际旭创分钟行情，拒绝执行历史补买")
	}
	market := "SZ"
	if strings.HasPrefix(strings.ToLower(recommendation.StockCode), "sh") {
		market = "SH"
	}
	return research.Quote{Code: recommendation.StockCode, Name: recommendation.StockName, Market: market, Price: selected.Close, At: selected.At}, nil
}

type repeatedStringFlag []string

func (values *repeatedStringFlag) String() string { return strings.Join(*values, ",") }
func (values *repeatedStringFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("recommendation-id 不能为空")
	}
	*values = append(*values, value)
	return nil
}

var approvedHistoricalRepairIDs = []string{
	"c49ade23-12f4-4aa0-8203-b985bfd9d7e4",
	"699640bc-861e-4330-8023-4182173b3e9e",
}

func runResearchMissedCashRepair(args []string, g GlobalOptions, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("research repair-missed-cash", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := g.JSON
	var recommendationIDs repeatedStringFlag
	fundingAtText := "2026-08-18T09:20:00+08:00"
	buyDate := "2026-08-18"
	firstSellText := "2026-08-20T09:50:00+08:00"
	timeout := 10 * time.Minute
	fs.Var(&recommendationIDs, "recommendation-id", "待纠正推荐 ID（必须重复两次）")
	fs.StringVar(&fundingAtText, "funding-at", fundingAtText, "前两期资金共同有效时间（RFC3339）")
	fs.StringVar(&buyDate, "buy-date", buyDate, "历史买入交易日")
	fs.StringVar(&firstSellText, "first-sell-check", firstSellText, "首次卖出 AI 判断时间（RFC3339）")
	fs.DurationVar(&timeout, "timeout", timeout, "行情证据采集超时")
	fs.BoolVar(&jsonOut, "json", jsonOut, "JSON 输出")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if timeout <= 0 {
		return errors.New("timeout 必须大于 0")
	}
	if !sameStringSet(recommendationIDs, approvedHistoricalRepairIDs) {
		return errors.New("1.7.1 历史纠正只允许指定的中际旭创和中微公司推荐 ID")
	}
	fundingAt, err := time.Parse(time.RFC3339, fundingAtText)
	if err != nil {
		return fmt.Errorf("解析 funding-at: %w", err)
	}
	firstSell, err := time.Parse(time.RFC3339, firstSellText)
	if err != nil {
		return fmt.Errorf("解析 first-sell-check: %w", err)
	}
	if db.Dao == nil {
		return errors.New("主数据库未初始化")
	}
	repository := research.NewRepository(db.Dao)
	if err := repository.EnsureAccount(context.Background()); err != nil {
		return err
	}
	service := research.NewService(repository, nil, data.NewResearchQuoteProvider(), data.ResearchTradingCalendar{})
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	type evidenceOutput struct {
		RecommendationID string                        `json:"recommendationId"`
		EntrySource      string                        `json:"entrySource"`
		EntryQuote       research.Quote                `json:"entryQuote"`
		MarkQuote        *research.Quote               `json:"markQuote,omitempty"`
		ProviderErrors   []research.ChartProviderError `json:"providerErrors"`
	}
	evidenceRows := make([]evidenceOutput, 0, len(recommendationIDs))
	buys := make([]research.HistoricalBuyEvidence, 0, len(recommendationIDs))
	for _, id := range recommendationIDs {
		recommendation, loadErr := repository.Recommendation(ctx, id)
		if loadErr != nil {
			return fmt.Errorf("读取推荐 %s: %w", id, loadErr)
		}
		evidence, evidenceErr := data.ResolveHistoricalBuyMarketEvidence(ctx, recommendation.StockCode, recommendation.StockName, buyDate, time.Now())
		if evidenceErr != nil {
			return fmt.Errorf("获取 %s(%s) 历史行情证据: %w", recommendation.StockName, recommendation.StockCode, evidenceErr)
		}
		buys = append(buys, research.HistoricalBuyEvidence{RecommendationID: id, EntryQuote: evidence.EntryQuote,
			EntrySource: evidence.EntrySource, MarkQuote: evidence.MarkQuote})
		evidenceRows = append(evidenceRows, evidenceOutput{RecommendationID: id, EntrySource: evidence.EntrySource,
			EntryQuote: evidence.EntryQuote, MarkQuote: evidence.MarkQuote, ProviderErrors: evidence.ProviderErrors})
	}
	receipt, err := service.ApplyHistoricalMissedCashCorrection(ctx, research.HistoricalMissedCashCorrectionRequest{
		FundingEffectiveAt: fundingAt, BuyTradingDate: buyDate, FirstSellCheckAt: firstSell,
		AppliedAt: time.Now(), Buys: buys,
	})
	if err != nil {
		return err
	}
	output := struct {
		Receipt  research.HistoricalMissedCashCorrectionReceipt `json:"receipt"`
		Evidence []evidenceOutput                               `json:"evidence"`
	}{Receipt: receipt, Evidence: evidenceRows}
	if jsonOut {
		body, marshalErr := marshalPrettyJSON(output)
		if marshalErr != nil {
			return marshalErr
		}
		_, _ = fmt.Fprintln(stdout, string(body))
	} else {
		_, _ = fmt.Fprintf(stdout, "status=%s funding=%.2f cash_after=%.2f buys=%d\n", receipt.Status, receipt.FundingAmount, receipt.CashAfter, len(receipt.Buys))
	}
	return nil
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	a, b := append([]string(nil), left...), append([]string(nil), right...)
	sort.Strings(a)
	sort.Strings(b)
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}
