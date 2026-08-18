package main

import "fmt"

// MonitorStockPrices refreshes the browser-facing price and profit streams.
func MonitorStockPrices(a *App) {
	snapshot := a.collectMonitoredStockSnapshot()
	for _, stockInfo := range snapshot.ChangedInfos {
		go emitEvent(a.ctx, "stock_price", stockInfo)
	}

	go emitEvent(a.ctx, "realtime_profit", fmt.Sprintf("  %.2f", snapshot.Total))
}
