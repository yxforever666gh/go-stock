package main

import (
	"embed"
	"flag"
	"fmt"
	log "go-stock/backend/logger"
	"go-stock/internal/bootstrap"
	"go-stock/internal/cli"
	appconfig "go-stock/internal/config"
	"go-stock/internal/releaseinfo"
	"os"
	"runtime/debug"
	_ "time/tzdata"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var icon []byte

//go:embed build/app.ico
var icon2 []byte

var alipay []byte

var wxpay []byte

var wxgzh []byte

//go:embed build/stock_basic.json
var stocksBin []byte

//go:generate cp -R ./data ./build/bin

var Version string
var VersionCommit string
var OFFICIAL_STATEMENT string
var BuildKey string

func main() {
	if err := releaseinfo.InitializeBuildInfo(""); err != nil {
		fmt.Fprintf(os.Stderr, "initialize build info: %v\n", err)
		os.Exit(1)
	}
	manifest := releaseinfo.Manifest()
	build := releaseinfo.Build()
	Version = manifest.AppVersion
	VersionCommit = build.Commit
	if len(os.Args) > 1 && cli.HasCommand(os.Args[1:]) {
		os.Exit(cli.Execute(os.Args[1:], os.Stdout, os.Stderr))
	}
	cfg := appconfig.Load()
	webListenAddr := flag.String("web-addr", cfg.Web.ListenAddr, "web listen address")
	flag.Parse()
	cfg.Web.ListenAddr = normalizeWebListenAddr(*webListenAddr)

	defer func() {
		if r := recover(); r != nil {
			log.SugaredLogger.Error("panic: ", r)
			log.SugaredLogger.Error("stack: ", string(debug.Stack()))
		}
	}()

	appRuntime, err := bootstrap.InitApplication(cfg, embeddedDomesticStockMaster)
	if err != nil {
		log.SugaredLogger.Fatalf("application initialization failed: %v", err)
	}
	bootstrap.ConfigureRuntimeEventEmitter(emitEvent)

	log.SugaredLogger.Info("starting...")
	log.SugaredLogger.Info(startupBanner(cfg))
	logStartupConfig(cfg)
	//log.SugaredLogger.Infof("build key: %s", BuildKey)

	app := NewAppWithRuntime(appRuntime)
	log.SugaredLogger.Infof("starting web mode at http://%s", cfg.Web.ListenAddr)
	hub := NewWebEventHub()
	setWebEventHub(hub)
	app.goTask(app.domReady)

	if err := runWebMode(app, cfg.Web.ListenAddr, hub); err != nil {
		log.SugaredLogger.Fatal(err)
	}
}
