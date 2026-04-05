//go:build windows && webonly
// +build windows,webonly

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/duke-git/lancet/v2/convertor"
	"github.com/duke-git/lancet/v2/strutil"
	"github.com/wailsapp/wails/v2/pkg/options"
	"go-stock/backend/data"
	"go-stock/backend/db"
	"go-stock/backend/logger"
)

func (a *App) startup(ctx context.Context) {
	defer PanicHandler()
	a.ctx = ctx
	logger.SugaredLogger.Infof("Version:%s", Version)
	logger.SugaredLogger.Infof("application startup Version:%s", Version)
}

func OnSecondInstanceLaunch(secondInstanceData options.SecondInstanceData) {
	logger.SugaredLogger.Infof("go-stock is already running: args=%v", secondInstanceData.Args)
}

func MonitorStockPrices(a *App) {
	dest := &[]data.FollowedStock{}
	db.Dao.Model(&data.FollowedStock{}).Find(dest)
	total := float64(0)

	stockInfos := GetStockInfos(*dest...)
	for _, stockInfo := range *stockInfos {
		if strutil.HasPrefixAny(stockInfo.Code, []string{"SZ", "SH", "sh", "sz"}) && !isTradingTime(time.Now()) {
			continue
		}
		if strutil.HasPrefixAny(stockInfo.Code, []string{"hk", "HK"}) && !IsHKTradingTime(time.Now()) {
			continue
		}
		if strutil.HasPrefixAny(stockInfo.Code, []string{"us", "US", "gb_"}) && !IsUSTradingTime(time.Now()) {
			continue
		}

		total += stockInfo.ProfitAmountToday
		price, _ := convertor.ToFloat(stockInfo.Price)
		if stockInfo.PrePrice != price {
			go emitEvent(a.ctx, "stock_price", stockInfo)
		}
	}

	go emitEvent(a.ctx, "realtime_profit", fmt.Sprintf("  %.2f", total))
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
