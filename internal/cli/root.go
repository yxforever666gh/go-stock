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

	if _, err := Bootstrap(opts.DataDir, opts.DBPath); err != nil {
		fmt.Fprintf(stderr, "初始化失败: %v\n", err)
		return 1
	}

	var runErr error
	switch cmd {
	case "quote":
		runErr = runQuote(cmdArgs, opts, stdout, stderr)
	case "search":
		runErr = runSearch(cmdArgs, opts, stdout, stderr)
	case "ai":
		runErr = runAI(cmdArgs, opts, stdout, stderr)
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
}
