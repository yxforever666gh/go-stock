package main

import (
	"context"
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

//go:embed build/stock_base_info_hk.json
var stocksBinHK []byte

//go:embed build/stock_base_info_us.json
var stocksBinUS []byte

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
	if len(os.Args) > 1 && os.Args[1] == "migrate-minute-db" {
		runMinuteDBMigration(cfg)
		return
	}
	webMode := flag.Bool("web", false, "run localhost web mode")
	webListenAddr := flag.String("web-addr", cfg.Web.ListenAddr, "web mode listen address")
	flag.Parse()
	cfg.Web.ListenAddr = normalizeWebListenAddr(*webListenAddr)

	defer func() {
		if r := recover(); r != nil {
			log.SugaredLogger.Error("panic: ", r)
			log.SugaredLogger.Error("stack: ", string(debug.Stack()))
		}
	}()

	appRuntime, err := bootstrap.InitApplication(cfg)
	if err != nil {
		log.SugaredLogger.Fatalf("application initialization failed: %v", err)
	}
	bootstrap.ConfigureRuntimeEventEmitter(emitEvent)

	log.SugaredLogger.Info("starting...")
	log.SugaredLogger.Info(startupBanner(cfg, *webMode))
	logStartupConfig(cfg)
	//log.SugaredLogger.Infof("build key: %s", BuildKey)

	// Create an instance of the app structure
	app := NewAppWithServices(appRuntime.Services, bootstrap.NewProductionAgentToolDataProvider(), bootstrap.NewProductionAgentConfigurationProvider())
	if *webMode {
		log.SugaredLogger.Infof("starting web mode at http://%s", cfg.Web.ListenAddr)
		setRuntimeEventsEnabled(false)
		app.ctx = context.Background()
		hub := NewWebEventHub()
		setWebEventHub(hub)
		go app.domReady(app.ctx)

		err := runWebMode(app, cfg.Web.ListenAddr, hub)
		if err != nil {
			log.SugaredLogger.Fatal(err)
		}
		return
	}

	if err := runDesktopApp(app); err != nil {
		log.SugaredLogger.Fatal(err)
	}
}

func runMinuteDBMigration(cfg appconfig.AppConfig) {
	bootstrap.EnsureRuntimeDirs(cfg)
	summary, err := bootstrap.MigrateLegacyMinuteCache(cfg.DB.Path)
	if err != nil {
		log.SugaredLogger.Fatalf("minute db migration failed: %v", err)
	}
	log.SugaredLogger.Infof("minute db migration completed: legacyRows=%d minuteDBRows=%d migratedRows=%d stockCount=%d", summary.LegacyRows, summary.MinuteDBRows, summary.MigratedRows, summary.StockCount)
}
