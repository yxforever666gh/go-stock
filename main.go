package main

import (
	"context"
	"embed"
	"flag"
	"go-stock/backend/data"
	"go-stock/backend/db"
	log "go-stock/backend/logger"
	"go-stock/internal/bootstrap"
	appconfig "go-stock/internal/config"
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

	appRuntime := bootstrap.InitApplication(cfg)
	data.SetRuntimeEventEmitter(emitEvent)

	log.SugaredLogger.Info("starting...")
	log.SugaredLogger.Info(startupBanner(cfg, *webMode))
	logStartupConfig(cfg)
	//log.SugaredLogger.Infof("build key: %s", BuildKey)

	// Create an instance of the app structure
	app := NewAppWithServices(appRuntime.Services)
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
	db.Init(cfg.DB.Path)
	summary, err := data.MigrateMinuteCacheToMinuteDB()
	if err != nil {
		log.SugaredLogger.Fatalf("minute db migration failed: %v", err)
	}
	if err := data.OptimizeMinuteCacheDB(); err != nil {
		log.SugaredLogger.Warnf("minute db optimize failed: %v", err)
	}
	log.SugaredLogger.Infof("minute db migration completed: legacyRows=%d minuteDBRows=%d migratedRows=%d stockCount=%d", summary.LegacyRows, summary.MinuteDBRows, summary.MigratedRows, summary.StockCount)
}
