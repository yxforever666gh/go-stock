//go:build webonly
// +build webonly

package main

import "context"

func (a *App) registerCommonRuntimeEvents(ctx context.Context) {
	a.registerMarketSummaryV150ExecutionRuntime()
}

func (a *App) reloadWindowTheme(ctx context.Context) {
}
