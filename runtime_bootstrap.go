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

func logStartupConfig(cfg appconfig.AppConfig) {
	log.SugaredLogger.Infof("startup config: %s", cfg.StartupSummary())
	if cfg.Web.ListenAddr == appconfig.DefaultWebListenAddr {
		log.SugaredLogger.Infof("web mode default listen addr: http://%s", cfg.Web.ListenAddr)
		return
	}
	log.SugaredLogger.Infof("web mode listen addr overridden: http://%s", cfg.Web.ListenAddr)
}

func startupBanner(cfg appconfig.AppConfig) string {
	return fmt.Sprintf("starting web mode, version=%s commit=%s addr=%s", Version, VersionCommit, cfg.Web.ListenAddr)
}
