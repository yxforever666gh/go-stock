package main

import (
	"context"
	"fmt"
)

// MonitorStockPrices refreshes the browser-facing price and profit streams.
func MonitorStockPrices(a *App) {
	snapshot := a.collectMonitoredStockSnapshot()
	for _, stockInfo := range snapshot.ChangedInfos {
		stockInfo := stockInfo
		a.goTask(func(ctx context.Context) { emitEvent(ctx, "stock_price", stockInfo) })
	}

	a.goTask(func(ctx context.Context) { emitEvent(ctx, "realtime_profit", fmt.Sprintf("  %.2f", snapshot.Total)) })
}
