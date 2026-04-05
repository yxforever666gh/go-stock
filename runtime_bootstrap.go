//go:build !webonly
// +build !webonly

package main

import (
	"fmt"
	"strings"

	"go-stock/backend/data"
	log "go-stock/backend/logger"
	"go-stock/internal/bootstrap"
	appconfig "go-stock/internal/config"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/logger"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

func normalizeWebListenAddr(raw string) string {
	if value := strings.TrimSpace(raw); value != "" {
		return value
	}
	return appconfig.DefaultWebListenAddr
}

func ensureRuntimeDirs(cfg appconfig.AppConfig) {
	bootstrap.EnsureRuntimeDirs(cfg)
}

func runDesktopApp(app *App) error {
	setWebEventHub(nil)
	setRuntimeEventsEnabled(true)
	cfg := appconfig.Load()

	appMenu := menu.NewMenu()
	if IsMacOS() {
		appMenu.Append(menu.EditMenu())
	}

	log.SugaredLogger.Info("version: " + Version)
	log.SugaredLogger.Info("commit: " + VersionCommit)

	width, _, minWidth, minHeight, err := getScreenResolution()
	if err != nil {
		log.SugaredLogger.Error("get screen resolution error")
		width = 1456
	}

	darkTheme := data.GetSettingConfig().DarkTheme
	backgroundColour := &options.RGBA{R: 255, G: 255, B: 255, A: 1}
	if darkTheme {
		backgroundColour = &options.RGBA{R: 27, G: 38, B: 54, A: 1}
	}

	return wails.Run(&options.App{
		Title:                    "go-stock：AI赋能股票分析✨ " + OFFICIAL_STATEMENT,
		Width:                    width * 4 / 5,
		Height:                   920,
		MinWidth:                 minWidth,
		MinHeight:                minHeight,
		DisableResize:            false,
		Fullscreen:               false,
		Frameless:                false,
		StartHidden:              false,
		HideWindowOnClose:        false,
		EnableDefaultContextMenu: true,
		BackgroundColour:         backgroundColour,
		Assets:                   assets,
		Menu:                     appMenu,
		Logger:                   logger.NewFileLogger(cfg.LogFilePath("wails.log")),
		LogLevel:                 logger.DEBUG,
		LogLevelProduction:       logger.INFO,
		OnStartup:                app.startup,
		OnDomReady:               app.domReady,
		OnBeforeClose:            app.beforeClose,
		OnShutdown:               app.shutdown,
		WindowStartState:         options.Normal,
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "go-stock",
			OnSecondInstanceLaunch: OnSecondInstanceLaunch,
		},
		Bind: []interface{}{app},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			DisableWindowIcon:    false,
			WebviewUserDataPath:  "",
		},
		Mac: &mac.Options{
			TitleBar: &mac.TitleBar{
				TitlebarAppearsTransparent: false,
				HideTitle:                  false,
				HideTitleBar:               false,
				FullSizeContent:            false,
				UseToolbar:                 true,
			},
			Appearance:           mac.NSAppearanceNameDarkAqua,
			WebviewIsTransparent: true,
			WindowIsTranslucent:  true,
			About: &mac.AboutInfo{
				Title:   "go-stock",
				Message: "go-stock：AI赋能股票分析✨ ",
				Icon:    icon,
			},
		},
	})
}

func logStartupConfig(cfg appconfig.AppConfig) {
	log.SugaredLogger.Infof("startup config: %s", cfg.StartupSummary())
	if cfg.Web.ListenAddr == appconfig.DefaultWebListenAddr {
		log.SugaredLogger.Infof("web mode default listen addr: http://%s", cfg.Web.ListenAddr)
		return
	}
	log.SugaredLogger.Infof("web mode listen addr overridden: http://%s", cfg.Web.ListenAddr)
}

func startupModeLabel(webMode bool) string {
	if webMode {
		return "web"
	}
	return "desktop"
}

func startupBanner(cfg appconfig.AppConfig, webMode bool) string {
	return fmt.Sprintf("starting %s mode, version=%s commit=%s addr=%s", startupModeLabel(webMode), Version, VersionCommit, cfg.Web.ListenAddr)
}
