//go:build webonly
// +build webonly

package main

import (
	"context"
	"fmt"

	"go-stock/backend/logger"
)

func (a *App) startup(ctx context.Context) {
	defer PanicHandler()
	a.ctx = ctx
	logger.SugaredLogger.Infof("Version:%s", Version)
	logger.SugaredLogger.Infof("application startup Version:%s", Version)
}

func MonitorStockPrices(a *App) {
	snapshot := a.collectMonitoredStockSnapshot()
	for _, stockInfo := range snapshot.ChangedInfos {
		go emitEvent(a.ctx, "stock_price", stockInfo)
	}

	go emitEvent(a.ctx, "realtime_profit", fmt.Sprintf("  %.2f", snapshot.Total))
}

func (a *App) beforeClose(ctx context.Context) (prevent bool) {
	defer PanicHandler()
	a.cron.Stop()
	return false
}

func getFrameless() bool {
	return false
}

func getScreenResolution() (int, int, int, int, error) {
	return 1366, 768, 1200, 800, nil
}
