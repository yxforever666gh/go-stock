package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"go-stock/backend/data"
	"go-stock/backend/db"
	"go-stock/backend/research2app"
	"go-stock/backend/researchconfig"
)

func runResearch2(args []string, options GlobalOptions, stdout, stderr io.Writer) error {
	if len(args) == 0 || strings.EqualFold(args[0], "help") || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(stdout, "用法:")
		fmt.Fprintln(stdout, "  go-stock [--db-path PATH] research2 backfill-performance --all [--json]")
		fmt.Fprintln(stdout, "  go-stock [--db-path PATH] research2 replay-allocation --all [--dry-run] [--json]")
		return nil
	}
	command := strings.ToLower(strings.TrimSpace(args[0]))
	if command != "backfill-performance" && command != "replay-allocation" {
		return fmt.Errorf("未知 research2 子命令: %s", args[0])
	}
	flags := flag.NewFlagSet("research2 "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	all, dryRun, jsonOutput := false, false, options.JSON
	flags.BoolVar(&all, "all", false, "补算全部24个账户历史")
	if command == "replay-allocation" {
		flags.BoolVar(&dryRun, "dry-run", false, "只补行情并生成重放计划，不改主库")
	}
	flags.BoolVar(&jsonOutput, "json", jsonOutput, "JSON 输出")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if !all {
		return fmt.Errorf("%s requires --all", command)
	}
	snapshot, err := researchconfig.New(db.Dao).Load(context.Background(), researchconfig.Research2)
	if err != nil {
		return fmt.Errorf("读取研究中心2设置: %w", err)
	}
	dependencies, err := data.NewResearch2Dependencies(int(snapshot.Settings.AIAnalysisConfigID), db.Dao, db.MinuteDao, snapshot.Settings)
	if err != nil {
		return err
	}
	runtime, err := research2app.NewRuntime(db.Dao, dependencies)
	if err != nil {
		return err
	}
	if command == "replay-allocation" {
		if runtime.AllocationReplay == nil {
			return errors.New("研究中心2历史仓位重放不可用")
		}
		result, replayErr := runtime.AllocationReplay.ReplayAll(context.Background(), dryRun)
		if jsonOutput {
			body, marshalErr := marshalPrettyJSON(result)
			if marshalErr != nil {
				return marshalErr
			}
			_, _ = fmt.Fprintln(stdout, string(body))
		} else {
			_, _ = fmt.Fprintf(stdout, "replay=%s candidates=%d buys=%d sells=%d missingBuy=%d missingSell=%d dryRun=%t reused=%t\n", result.ReplayID, result.CandidateCount, result.BuyCount, result.SellCount, result.MissingBuyCount, result.MissingSellCount, result.DryRun, result.Reused)
		}
		return replayErr
	}
	if runtime.PerformanceBackfill == nil {
		return errors.New("研究中心2历史收益补算不可用")
	}
	result, backfillErr := runtime.PerformanceBackfill.BackfillAll(context.Background())
	if jsonOutput {
		body, marshalErr := marshalPrettyJSON(result)
		if marshalErr != nil {
			return marshalErr
		}
		_, _ = fmt.Fprintln(stdout, string(body))
	} else {
		_, _ = fmt.Fprintf(stdout, "outcomes=%d unavailable=%d valuations=%d valuationUnavailable=%d\n", result.OutcomesCompleted, result.OutcomesUnavailable, result.ValuationsCompleted, result.ValuationsUnavailable)
	}
	return backfillErr
}
