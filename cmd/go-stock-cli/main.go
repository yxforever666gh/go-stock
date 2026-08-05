package main

import (
	"os"

	"go-stock/internal/cli"
	"go-stock/internal/releaseinfo"
)

func main() {
	if err := releaseinfo.InitializeBuildInfo(""); err != nil {
		_, _ = os.Stderr.WriteString("initialize build info: " + err.Error() + "\n")
		os.Exit(1)
	}
	os.Exit(cli.Execute(os.Args[1:], os.Stdout, os.Stderr))
}
