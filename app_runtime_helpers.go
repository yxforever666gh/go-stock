package main

import (
	"context"
	"fmt"
	log "go-stock/backend/logger"
	"os"
	"runtime/debug"
	"strings"
)

func (a *App) updateBasicInfo() {
	config := a.services.Config.GetConfig()
	if config == nil {
		return
	}
	if config.UpdateBasicInfoOnStart {
		go func() {
			if _, err := a.services.Stock.RefreshStockBaseInfo(context.Background()); err != nil {
				log.SugaredLogger.Warnf("refresh stock master on startup: %v", err)
			}
		}()
		go a.services.Stock.RefreshIndexBaseInfo()
	}
}

func checkDir(dir string) {
	if strings.TrimSpace(dir) == "" {
		return
	}
	_, err := os.Stat(dir)
	if os.IsNotExist(err) {
		_ = os.MkdirAll(dir, os.ModePerm)
		log.SugaredLogger.Info("create dir: " + dir)
	}
	if BuildKey == "" {
		BuildKey = "cc1e0d684e32f176c56ff1fcf384dcd9"
	}
}

func PanicHandler() {
	if r := recover(); r != nil {
		fmt.Printf("Recovered from panic: %v\n", r)
		debug.PrintStack()
	}
}
