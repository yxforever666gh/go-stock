//go:build webonly
// +build webonly

package main

import (
	"fmt"
	"strings"

	log "go-stock/backend/logger"
	"go-stock/internal/bootstrap"
	appconfig "go-stock/internal/config"
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
	setRuntimeEventsEnabled(false)
	return fmt.Errorf("desktop mode is disabled in webonly build; use --web")
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
	return "desktop-disabled"
}

func startupBanner(cfg appconfig.AppConfig, webMode bool) string {
	return fmt.Sprintf("starting %s mode, version=%s commit=%s addr=%s", startupModeLabel(webMode), Version, VersionCommit, cfg.Web.ListenAddr)
}
