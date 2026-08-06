package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"go-stock/backend/models"
	"go-stock/backend/persistence"
	"go-stock/internal/bootstrap"
	cliports "go-stock/internal/cli/ports"
)

var strategyVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)

type strategyBacktestSummary struct {
	CommandName            string                            `json:"commandName"`
	CacheOnly              bool                              `json:"cacheOnly"`
	Persisted              bool                              `json:"persisted"`
	BacktestID             string                            `json:"backtestId"`
	StrategyVersion        string                            `json:"strategyVersion"`
	StartDate              string                            `json:"startDate"`
	EndDate                string                            `json:"endDate"`
	InputHash              string                            `json:"inputHash"`
	ResultHash             string                            `json:"resultHash"`
	RunSnapshots           int                               `json:"runSnapshots"`
	CandidateSnapshots     int                               `json:"candidateSnapshots"`
	EligibleCandidates     int                               `json:"eligibleCandidates"`
	RuleSnapshots          int                               `json:"ruleSnapshots"`
	OrderEvents            int                               `json:"orderEvents"`
	SecurityMasterRows     int                               `json:"securityMasterRows"`
	CorporateActions       int                               `json:"corporateActions"`
	TradeCount             int                               `json:"tradeCount"`
	ReplayMetrics          persistence.OrderEventReplayStats `json:"replayMetrics"`
	CandidateDecisionCount map[string]int                    `json:"candidateDecisionCount"`
	OrderEventTypeCount    map[string]int                    `json:"orderEventTypeCount"`
}

func runStrategyBacktest(args []string, g GlobalOptions, stdout, stderr io.Writer) error {
	repository, err := bootstrap.NewProductionFrozenBacktestRepository()
	if err != nil {
		return err
	}
	return runStrategyBacktestWithRepository(args, g, stdout, stderr, repository)
}

func runStrategyBacktestWithRepository(args []string, g GlobalOptions, stdout, stderr io.Writer, repository cliports.FrozenBacktestRepository) error {
	if repository == nil {
		return errors.New("frozen backtest repository is required")
	}
	fs := flag.NewFlagSet("strategy-backtest", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var versionText string
	var fromText string
	var toText string
	var startAlias string
	var endAlias string
	var jsonOut bool
	var dryRun bool
	fs.StringVar(&versionText, "version", "", "策略版本，语义版本格式，如 1.5.0")
	fs.StringVar(&fromText, "from", "", "运行快照 cohort 起始交易日，格式 YYYY-MM-DD")
	fs.StringVar(&toText, "to", "", "运行快照 cohort 结束交易日，格式 YYYY-MM-DD（含当日）")
	fs.StringVar(&startAlias, "start", "", "--from 的兼容别名")
	fs.StringVar(&endAlias, "end", "", "--to 的兼容别名")
	fs.BoolVar(&jsonOut, "json", g.JSON, "输出 JSON")
	fs.BoolVar(&dryRun, "dry-run", false, "只读取冻结快照并生成摘要，不持久化")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) > 0 {
		return fmt.Errorf("strategy-backtest 不接受位置参数: %s", strings.Join(fs.Args(), " "))
	}
	fromText, err := resolveStrategyBacktestDateAlias("--from", fromText, "--start", startAlias)
	if err != nil {
		return err
	}
	toText, err = resolveStrategyBacktestDateAlias("--to", toText, "--end", endAlias)
	if err != nil {
		return err
	}
	version, start, end, err := validateStrategyBacktestArgs(versionText, fromText, toText, time.Local)
	if err != nil {
		return err
	}

	inputs, err := repository.LoadFrozenStrategyInputs(context.Background(), version, start, end)
	if err != nil {
		if errors.Is(err, persistence.ErrNoFrozenSnapshots) || errors.Is(err, persistence.ErrIncompleteSnapshots) {
			return fmt.Errorf("本地冻结快照不可用；cache-only 回放拒绝联网补数: %w", err)
		}
		return err
	}
	summary := buildStrategyBacktestSummary(version, start, end, inputs)
	frozenAt := deterministicFrozenTime(inputs)
	trades, replayMetrics, resultHash, err := persistence.ReplayFrozenStrategyInputs(summary.BacktestID, version, inputs, frozenAt)
	if err != nil {
		return fmt.Errorf("冻结订单事件回放失败: %w", err)
	}
	summary.TradeCount = len(trades)
	summary.ReplayMetrics = replayMetrics
	summary.ResultHash = resultHash
	metrics := buildStrategyBacktestMetrics(summary, frozenAt)
	if !dryRun {
		summaryJSON, marshalErr := json.Marshal(summaryForPersistence(summary))
		if marshalErr != nil {
			return marshalErr
		}
		run := models.BacktestRun{
			BacktestID:             summary.BacktestID,
			StrategyVersion:        summary.StrategyVersion,
			StartDate:              summary.StartDate,
			EndDate:                summary.EndDate,
			InputHash:              summary.InputHash,
			Status:                 "completed",
			RunSnapshotCount:       summary.RunSnapshots,
			CandidateSnapshotCount: summary.CandidateSnapshots,
			RuleSnapshotCount:      summary.RuleSnapshots,
			OrderEventCount:        summary.OrderEvents,
			SecuritySnapshotCount:  summary.SecurityMasterRows,
			CorporateActionCount:   summary.CorporateActions,
			SummaryJSON:            string(summaryJSON),
			StartedAt:              frozenAt,
			CompletedAt:            frozenAt,
			FrozenAt:               &frozenAt,
		}
		if err := repository.PersistBacktestResult(context.Background(), persistence.BacktestResult{Run: run, Trades: trades, Metrics: metrics}); err != nil {
			return err
		}
		summary.Persisted = true
	}

	if jsonOut {
		body, err := marshalPrettyJSON(summary)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(stdout, string(body))
		return nil
	}
	_, _ = fmt.Fprintf(stdout, "离线策略回放完成：%s，运行快照 cohort %s 至 %s\n", summary.StrategyVersion, summary.StartDate, summary.EndDate)
	_, _ = fmt.Fprintf(stdout, "  cache-only: true；输入哈希: %s；结果哈希: %s\n", summary.InputHash, summary.ResultHash)
	_, _ = fmt.Fprintf(stdout, "  运行 %d，候选 %d（可执行 %d），规则 %d，订单事件 %d\n", summary.RunSnapshots, summary.CandidateSnapshots, summary.EligibleCandidates, summary.RuleSnapshots, summary.OrderEvents)
	_, _ = fmt.Fprintf(stdout, "  交易 %d（平仓 %d、期末持仓 %d），组合权益 %.2f，组合收益 %.4f%%，20bp/50bp 压力收益 %.4f%%/%.4f%%，Profit Factor %s，已持久化 %t\n", summary.TradeCount, summary.ReplayMetrics.ClosedTradeCount, summary.ReplayMetrics.OpenPositionCount, summary.ReplayMetrics.EndingEquity, summary.ReplayMetrics.PortfolioNetReturnPct, summary.ReplayMetrics.Stress20NetReturnPct, summary.ReplayMetrics.Stress50NetReturnPct, summary.ReplayMetrics.ProfitFactorText, summary.Persisted)
	_, _ = fmt.Fprintf(stdout, "  事件策略约束已验证: %t；开放仓估值口径: %s\n", summary.ReplayMetrics.PolicyValidated, summary.ReplayMetrics.ValuationMode)
	return nil
}

func resolveStrategyBacktestDateAlias(primaryName, primaryValue, aliasName, aliasValue string) (string, error) {
	primaryValue = strings.TrimSpace(primaryValue)
	aliasValue = strings.TrimSpace(aliasValue)
	if primaryValue != "" && aliasValue != "" && primaryValue != aliasValue {
		return "", fmt.Errorf("%s 与 %s 不能提供不同日期", primaryName, aliasName)
	}
	if primaryValue != "" {
		return primaryValue, nil
	}
	return aliasValue, nil
}

func validateStrategyBacktestArgs(versionText, startText, endText string, loc *time.Location) (string, time.Time, time.Time, error) {
	versionText = strings.TrimSpace(versionText)
	startText = strings.TrimSpace(startText)
	endText = strings.TrimSpace(endText)
	if !strategyVersionPattern.MatchString(versionText) {
		return "", time.Time{}, time.Time{}, errors.New("--version 必须提供有效语义版本，例如 1.5.0")
	}
	if loc == nil {
		loc = time.Local
	}
	if startText == "" || endText == "" {
		return "", time.Time{}, time.Time{}, errors.New("必须同时提供 --from 与 --to，格式 YYYY-MM-DD（--start/--end 仍可作为兼容别名）")
	}
	start, err := time.ParseInLocation(time.DateOnly, startText, loc)
	if err != nil {
		return "", time.Time{}, time.Time{}, fmt.Errorf("--from 格式错误: %w", err)
	}
	end, err := time.ParseInLocation(time.DateOnly, endText, loc)
	if err != nil {
		return "", time.Time{}, time.Time{}, fmt.Errorf("--to 格式错误: %w", err)
	}
	if end.Before(start) {
		return "", time.Time{}, time.Time{}, errors.New("--to 不能早于 --from")
	}
	return versionText, start, end, nil
}

func buildStrategyBacktestSummary(version string, start, end time.Time, inputs persistence.FrozenStrategyInputs) strategyBacktestSummary {
	inputHash := persistence.FrozenStrategyInputHash(inputs)
	identity := sha256.Sum256([]byte(strings.Join([]string{"strategy-backtest-snapshot-v2", version, start.Format(time.DateOnly), end.Format(time.DateOnly), inputHash}, "|")))
	summary := strategyBacktestSummary{
		CommandName:            "strategy-backtest",
		CacheOnly:              true,
		BacktestID:             "bt-" + hex.EncodeToString(identity[:16]),
		StrategyVersion:        version,
		StartDate:              start.Format(time.DateOnly),
		EndDate:                end.Format(time.DateOnly),
		InputHash:              inputHash,
		RunSnapshots:           len(inputs.Runs),
		CandidateSnapshots:     len(inputs.Candidates),
		RuleSnapshots:          len(inputs.Rules),
		OrderEvents:            len(inputs.OrderEvents),
		SecurityMasterRows:     len(inputs.SecurityMaster),
		CorporateActions:       len(inputs.CorporateActions),
		CandidateDecisionCount: map[string]int{},
		OrderEventTypeCount:    map[string]int{},
	}
	for _, row := range inputs.Candidates {
		if row.Eligible {
			summary.EligibleCandidates++
		}
		key := normalizedSummaryKey(row.Decision)
		summary.CandidateDecisionCount[key]++
	}
	for _, row := range inputs.OrderEvents {
		key := normalizedSummaryKey(row.EventType)
		summary.OrderEventTypeCount[key]++
	}
	return summary
}

func normalizedSummaryKey(raw string) string {
	if key := strings.ToLower(strings.TrimSpace(raw)); key != "" {
		return key
	}
	return "unspecified"
}

func summaryForPersistence(summary strategyBacktestSummary) strategyBacktestSummary {
	summary.Persisted = true
	return summary
}

func buildStrategyBacktestMetrics(summary strategyBacktestSummary, frozenAt time.Time) []models.Metric {
	counts := []struct {
		name  string
		value int
	}{
		{name: "run_snapshot_count", value: summary.RunSnapshots},
		{name: "candidate_snapshot_count", value: summary.CandidateSnapshots},
		{name: "eligible_candidate_count", value: summary.EligibleCandidates},
		{name: "rule_snapshot_count", value: summary.RuleSnapshots},
		{name: "order_event_count", value: summary.OrderEvents},
		{name: "security_master_count", value: summary.SecurityMasterRows},
		{name: "corporate_action_count", value: summary.CorporateActions},
		{name: "trade_count", value: summary.TradeCount},
		{name: "closed_trade_count", value: summary.ReplayMetrics.ClosedTradeCount},
		{name: "open_position_count", value: summary.ReplayMetrics.OpenPositionCount},
	}
	metrics := make([]models.Metric, 0, len(counts)+16)
	for i, item := range counts {
		metrics = append(metrics, models.Metric{
			MetricID:    summary.BacktestID + ":summary:" + item.name,
			BacktestID:  summary.BacktestID,
			Name:        item.name,
			Scope:       "summary",
			Value:       float64(item.value),
			ValueText:   fmt.Sprintf("%d", item.value),
			Unit:        "count",
			Ordinal:     i + 1,
			PayloadJSON: `{}`,
			FrozenAt:    &frozenAt,
		})
	}
	financial := []struct {
		name      string
		value     float64
		valueText string
		unit      string
	}{
		{name: "initial_cash", value: summary.ReplayMetrics.InitialCash, unit: "CNY"},
		{name: "ending_cash", value: summary.ReplayMetrics.EndingCash, unit: "CNY"},
		{name: "ending_equity", value: summary.ReplayMetrics.EndingEquity, unit: "CNY"},
		{name: "gross_pnl", value: summary.ReplayMetrics.GrossPnL, unit: "CNY"},
		{name: "fees", value: summary.ReplayMetrics.Fees, unit: "CNY"},
		{name: "net_pnl", value: summary.ReplayMetrics.NetPnL, unit: "CNY"},
		{name: "portfolio_net_return_pct", value: summary.ReplayMetrics.PortfolioNetReturnPct, unit: "percent"},
		{name: "net_mean_return_pct", value: summary.ReplayMetrics.NetMeanReturnPct, unit: "percent"},
		{name: "win_rate_pct", value: summary.ReplayMetrics.WinRatePct, unit: "percent"},
		{name: "profit_factor", valueText: summary.ReplayMetrics.ProfitFactorText, unit: "ratio"},
		{name: "stress_20bp_ending_equity", value: summary.ReplayMetrics.Stress20EndingEquity, unit: "CNY"},
		{name: "stress_20bp_net_pnl", value: summary.ReplayMetrics.Stress20NetPnL, unit: "CNY"},
		{name: "stress_20bp_net_return_pct", value: summary.ReplayMetrics.Stress20NetReturnPct, unit: "percent"},
		{name: "stress_50bp_ending_equity", value: summary.ReplayMetrics.Stress50EndingEquity, unit: "CNY"},
		{name: "stress_50bp_net_pnl", value: summary.ReplayMetrics.Stress50NetPnL, unit: "CNY"},
		{name: "stress_50bp_net_return_pct", value: summary.ReplayMetrics.Stress50NetReturnPct, unit: "percent"},
	}
	if summary.ReplayMetrics.ProfitFactor != nil {
		for i := range financial {
			if financial[i].name == "profit_factor" {
				financial[i].value = *summary.ReplayMetrics.ProfitFactor
				break
			}
		}
	}
	for i, item := range financial {
		valueText := item.valueText
		if valueText == "" {
			valueText = fmt.Sprintf("%.8f", item.value)
		}
		metrics = append(metrics, models.Metric{
			MetricID:    summary.BacktestID + ":summary:" + item.name,
			BacktestID:  summary.BacktestID,
			Name:        item.name,
			Scope:       "summary",
			Value:       item.value,
			ValueText:   valueText,
			Unit:        item.unit,
			Ordinal:     len(counts) + i + 1,
			PayloadJSON: `{}`,
			FrozenAt:    &frozenAt,
		})
	}
	return metrics
}

func deterministicFrozenTime(inputs persistence.FrozenStrategyInputs) time.Time {
	times := make([]time.Time, 0, len(inputs.Runs)+len(inputs.Candidates)+len(inputs.Rules)+len(inputs.OrderEvents)+len(inputs.SecurityMaster)+len(inputs.CorporateActions))
	appendTime := func(value *time.Time) {
		if value != nil && !value.IsZero() {
			times = append(times, value.UTC())
		}
	}
	for _, row := range inputs.Runs {
		appendTime(row.FrozenAt)
	}
	for _, row := range inputs.Candidates {
		appendTime(row.FrozenAt)
	}
	for _, row := range inputs.Rules {
		appendTime(row.FrozenAt)
	}
	for _, row := range inputs.OrderEvents {
		appendTime(row.FrozenAt)
	}
	for _, row := range inputs.SecurityMaster {
		appendTime(row.FrozenAt)
	}
	for _, row := range inputs.CorporateActions {
		appendTime(row.FrozenAt)
	}
	if len(times) == 0 {
		return time.Unix(0, 0).UTC()
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	return times[len(times)-1]
}
