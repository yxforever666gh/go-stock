package cli

import (
	"flag"
	"fmt"
	"io"
)

type GlobalOptions struct {
	DataDir string
	DBPath  string
	JSON    bool
}

func Execute(args []string, stdout, stderr io.Writer) int {
	opts, cmd, cmdArgs, err := parseRootArgs(args, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "参数错误: %v\n", err)
		printRootUsage(stderr)
		return 2
	}
	if cmd == "" {
		printRootUsage(stdout)
		return 2
	}

	var resolvedDBPath string
	if cmd == "strategy-rule-replay" {
		resolvedDBPath, err = BootstrapReadOnly(opts.DataDir, opts.DBPath)
	} else {
		resolvedDBPath, err = Bootstrap(opts.DataDir, opts.DBPath)
	}
	if err != nil {
		fmt.Fprintf(stderr, "初始化失败: %v\n", err)
		return 1
	}
	opts.DBPath = resolvedDBPath

	var runErr error
	switch cmd {
	case "quote":
		runErr = runQuote(cmdArgs, opts, stdout, stderr)
	case "search":
		runErr = runSearch(cmdArgs, opts, stdout, stderr)
	case "ai":
		runErr = runAI(cmdArgs, opts, stdout, stderr)
	case "network-audit":
		runErr = runNetworkAudit(cmdArgs, opts, stdout, stderr)
	case "repair-market-summary":
		runErr = runRepairMarketSummary(cmdArgs, opts, stdout, stderr)
	case "backfill-market-summary-recommend":
		runErr = runBackfillMarketSummaryRecommend(cmdArgs, opts, stdout, stderr)
	case "strategy-backtest":
		runErr = runStrategyBacktest(cmdArgs, opts, stdout, stderr)
	case "strategy-rule-replay":
		runErr = runStrategyRuleReplay(cmdArgs, opts, stdout, stderr)
	case "help", "-h", "--help":
		printRootUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "未知命令: %s\n", cmd)
		printRootUsage(stderr)
		return 2
	}

	if runErr != nil {
		fmt.Fprintf(stderr, "执行失败: %v\n", runErr)
		return 1
	}
	return 0
}

func parseRootArgs(args []string, stderr io.Writer) (GlobalOptions, string, []string, error) {
	opts := GlobalOptions{
		DataDir: "data",
	}
	rootFS := flag.NewFlagSet("go-stock-cli", flag.ContinueOnError)
	rootFS.SetOutput(stderr)
	rootFS.StringVar(&opts.DataDir, "data-dir", "data", "数据目录")
	rootFS.StringVar(&opts.DBPath, "db-path", "", "sqlite 数据库路径")
	rootFS.BoolVar(&opts.JSON, "json", false, "默认 JSON 输出")
	if err := rootFS.Parse(args); err != nil {
		return opts, "", nil, err
	}

	rest := rootFS.Args()
	if len(rest) == 0 {
		return opts, "", nil, nil
	}
	return opts, rest[0], rest[1:], nil
}

func printRootUsage(w io.Writer) {
	fmt.Fprintln(w, "go-stock-cli - go-stock 的命令行入口")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "用法:")
	fmt.Fprintln(w, "  go-stock-cli [全局参数] <命令> [命令参数]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "全局参数:")
	fmt.Fprintln(w, "  --data-dir string   数据目录 (默认: data)")
	fmt.Fprintln(w, "  --db-path string    sqlite 数据库路径 (默认: <data-dir>/stock.db)")
	fmt.Fprintln(w, "  --json              默认 JSON 输出")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "命令:")
	fmt.Fprintln(w, "  quote   查询实时行情")
	fmt.Fprintln(w, "  search  自然语言选股")
	fmt.Fprintln(w, "  ai      流式 AI 分析")
	fmt.Fprintln(w, "  network-audit  审计当前环境下所有主要网络数据接口")
	fmt.Fprintln(w, "  repair-market-summary  修复市场资讯历史激活规则与脏数据")
	fmt.Fprintln(w, "  backfill-market-summary-recommend  从历史市场资讯 AI 报告补写推荐记录")
	fmt.Fprintln(w, "  strategy-backtest  仅使用本地冻结订单事件生成并持久化确定性回放")
	fmt.Fprintln(w, "    示例: strategy-backtest --version 1.5.0 --from 2026-01-01 --to 2026-06-30")
	fmt.Fprintln(w, "  strategy-rule-replay  严格只读、cache-only 的历史结构化规则执行审计")
	fmt.Fprintln(w, "    示例: strategy-rule-replay --expect-count 226 --json")
}
