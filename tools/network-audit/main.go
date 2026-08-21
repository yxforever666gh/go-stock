package main

import (
	"flag"
	"fmt"
	"os"

	"go-stock/internal/bootstrap"
	"go-stock/internal/cli"
)

func main() {
	fs := flag.NewFlagSet("network-audit", flag.ExitOnError)
	dataDir := fs.String("data-dir", "data", "数据目录")
	dbPath := fs.String("db-path", "", "SQLite 主库路径")
	reportDir := fs.String("report-dir", "", "报告输出目录")
	jsonOut := fs.Bool("json", false, "输出 JSON")
	_ = fs.Parse(os.Args[1:])

	resolvedDBPath, err := cli.Bootstrap(*dataDir, *dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	options := auditOptions{DataDir: *dataDir, DBPath: resolvedDBPath, JSON: *jsonOut}
	provider := bootstrap.NewProductionMarketAuditProvider()
	if err := runNetworkAuditWithProvider(provider, *jsonOut, *reportDir, options, os.Stdout, os.Stderr, forceNoProxyEnv()); err != nil {
		fmt.Fprintf(os.Stderr, "执行失败: %v\n", err)
		os.Exit(1)
	}
}
