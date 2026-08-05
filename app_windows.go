//go:build windows && !webonly
// +build windows,!webonly

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/energye/systray"
	"github.com/go-toast/toast"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"go-stock/backend/logger"
)

func (a *App) startup(ctx context.Context) {
	defer PanicHandler()
	logger.SugaredLogger.Infof("Version:%s", Version)
	a.ctx = ctx
	a.registerCommonRuntimeEvents(ctx)

	go systray.Run(func() {
		onReady(a)
	}, func() {
		onExit(a)
	})

	logger.SugaredLogger.Infof("application startup Version:%s", Version)
}

func OnSecondInstanceLaunch(secondInstanceData options.SecondInstanceData) {
	notification := toast.Notification{
		AppID:    "go-stock",
		Title:    "go-stock",
		Message:  "程序已经在运行了",
		Duration: "short",
		Audio:    toast.Default,
	}
	if err := notification.Push(); err != nil {
		logger.SugaredLogger.Error(err)
	}
	time.Sleep(3 * time.Second)
}

func MonitorStockPrices(a *App) {
	snapshot := a.collectMonitoredStockSnapshot()
	for _, stockInfo := range snapshot.ChangedInfos {
		go emitEvent(a.ctx, "stock_price", stockInfo)
	}
	if snapshot.Total != 0 {
		title := "go-stock " + time.Now().Format(time.DateTime) + fmt.Sprintf("  %.2f¥", snapshot.Total)
		systray.SetTooltip(title)
	}
	go emitEvent(a.ctx, "realtime_profit", fmt.Sprintf("  %.2f", snapshot.Total))
}

// onReady is the Windows systray ready callback. Keep it equivalent to the
// existing Darwin startup behavior: record readiness and show the Wails
// window, without launching any helper process or secondary window.
func onReady(a *App) {
	logger.SugaredLogger.Infof("systray onReady")
	runtime.WindowShow(a.ctx)
}

func (a *App) beforeClose(ctx context.Context) (prevent bool) {
	defer PanicHandler()
	dialog, err := runtime.MessageDialog(ctx, runtime.MessageDialogOptions{
		Type:         runtime.QuestionDialog,
		Title:        "go-stock",
		Message:      "确定关闭吗？",
		Buttons:      []string{"确定"},
		Icon:         icon,
		CancelButton: "取消",
	})
	if err != nil {
		logger.SugaredLogger.Errorf("dialog error:%s", err.Error())
		return false
	}
	if dialog == "No" {
		return true
	}
	a.cron.Stop()
	return false
}

func getFrameless() bool {
	return true
}

func getScreenResolution() (int, int, int, int, error) {
	return 1366, 768, 1456, 768, nil
}
