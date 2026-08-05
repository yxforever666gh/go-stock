package cli

import (
	"flag"
	"fmt"
	"io"
	"time"

	"go-stock/internal/service"
)

func runRepairMarketSummary(args []string, g GlobalOptions, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("repair-market-summary", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", g.JSON, "输出 JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	result, err := service.NewRecommendService().RepairHistoricalMarketSummaryActivationIssues(time.Now())
	if err != nil {
		return err
	}

	if jsonOut {
		body, err := marshalPrettyJSON(map[string]any{
			"scanned":       result.Scanned,
			"downgraded":    result.Downgraded,
			"ruleUpgraded":  result.RuleUpgraded,
			"skippedNoRef":  result.SkippedNoRef,
			"finishedAt":    time.Now().Format(time.DateTime),
			"databasePath":  g.DBPath,
			"commandName":   "repair-market-summary",
			"runtimeStatus": "ok",
		})
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(stdout, string(body))
		return nil
	}

	_, _ = fmt.Fprintf(
		stdout,
		"市场资讯历史激活规则修复完成：扫描 %d 条，降级 %d 条，重建/升级规则 %d 条，跳过 %d 条\n",
		result.Scanned,
		result.Downgraded,
		result.RuleUpgraded,
		result.SkippedNoRef,
	)
	return nil
}
