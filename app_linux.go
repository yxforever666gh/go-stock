//go:build linux
// +build linux

package main

import (
	"context"
	"fmt"

	"github.com/wailsapp/wails/v2/pkg/options"
	"go-stock/backend/logger"
)

// startup is called at application startup
func (a *App) startup(ctx context.Context) {
	defer PanicHandler()
	a.ctx = ctx
	a.registerCommonRuntimeEvents(ctx)
	logger.SugaredLogger.Infof("Version:%s", Version)

	logger.SugaredLogger.Infof("application startup Version:%s", Version)
}

func OnSecondInstanceLaunch(secondInstanceData options.SecondInstanceData) {
	logger.SugaredLogger.Infof("go-stock is already running: args=%v", secondInstanceData.Args)
}

func MonitorStockPrices(a *App) {
	snapshot := a.collectMonitoredStockSnapshot()
	for _, stockInfo := range snapshot.ChangedInfos {
		go emitEvent(a.ctx, "stock_price", stockInfo)
	}

	go emitEvent(a.ctx, "realtime_profit", fmt.Sprintf("  %.2f", snapshot.Total))
}

// beforeClose is called when the application is about to quit,
// either by clicking the window close button or calling runtime.Quit.
// Returning true will cause the application to continue, false will continue shutdown as normal.
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
