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
		return nil
	}
	if !strings.EqualFold(args[0], "backfill-performance") {
		return fmt.Errorf("未知 research2 子命令: %s", args[0])
	}
	flags := flag.NewFlagSet("research2 backfill-performance", flag.ContinueOnError)
	flags.SetOutput(stderr)
	all, jsonOutput := false, options.JSON
	flags.BoolVar(&all, "all", false, "补算全部24个账户历史")
	flags.BoolVar(&jsonOutput, "json", jsonOutput, "JSON 输出")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if !all {
		return errors.New("backfill-performance requires --all")
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
