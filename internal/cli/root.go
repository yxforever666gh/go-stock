package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"go-stock/backend/data"
	"go-stock/backend/db"
	"go-stock/backend/stocks"
	"go-stock/internal/aiapp"
	"go-stock/internal/settingsapp"
)

type GlobalOptions struct {
	DataDir string
	DBPath  string
	JSON    bool
}

func newCLIStockService() *stocks.Service {
	stockAPI := data.NewStockDataApi()
	return stocks.NewService(stocks.Dependencies{
		Database: db.Dao,
		SearchWithFingerprint: func(words, fingerprint string, pageSize int) map[string]any {
			return data.NewSearchStockApiWithFingerprint(words, fingerprint).SearchStock(pageSize)
		},
		Realtime: stockAPI.GetStockCodeRealTimeDataReadOnly,
	})
}

func newCLISettingsService() *settingsapp.Service {
	return settingsapp.NewService(data.NewSettingsProvider())
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
	if cmd == "release" {
		if err := runRelease(cmdArgs, opts, stdout); err != nil {
			fmt.Fprintf(stderr, "execution failed: %v\n", err)
			return 1
		}
		return 0
	}
	if cmd == "db" {
		if err := runDB(cmdArgs, opts, stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "execution failed: %v\n", err)
			return 1
		}
		return 0
	}
	resolvedDBPath, err := Bootstrap(opts.DataDir, opts.DBPath)
	if err != nil {
		fmt.Fprintf(stderr, "初始化失败: %v\n", err)
		return 1
	}
	opts.DBPath = resolvedDBPath

	var runErr error
	switch cmd {
	case "quote":
		runErr = runQuote(cmdArgs, opts, stdout, stderr, newCLIStockService())
	case "search":
		runErr = runSearch(cmdArgs, opts, stdout, stderr, newCLIStockService(), newCLISettingsService())
	case "ai":
		resolver, resolverErr := aiapp.NewCommandResolver(db.Dao)
		if resolverErr != nil {
			runErr = resolverErr
			break
		}
		runErr = runAI(cmdArgs, opts, stdout, stderr, resolver)
	case "research":
		runErr = runResearch(cmdArgs, opts, stdout, stderr)
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

func IsCommand(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "quote", "search", "ai", "research", "db", "release", "help", "-h", "--help":
		return true
	default:
		return false
	}
}

func HasCommand(args []string) bool {
	for _, arg := range args {
		if IsCommand(arg) {
			return true
		}
	}
	return false
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
	fmt.Fprintln(w, "  research run-once  运行一次研究任务")
	fmt.Fprintln(w, "  db status|archive|backup|compact|migrate|verify  管理、归档并校验主库和分钟库")
	fmt.Fprintln(w, "  release inspect  查看 App 与数据库版本身份")
}
