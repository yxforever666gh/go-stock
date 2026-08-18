package main

import (
	"context"

	"go-stock/backend/logger"
)

func (a *App) checkStockBaseInfo(ctx context.Context) {
	defer PanicHandler()
	defer func() {
		go emitEvent(ctx, "loadingMsg", "done")
	}()

	result, err := a.services.Stock.RefreshStockBaseInfo(ctx)
	if err != nil {
		logger.SugaredLogger.Errorf("refresh stock master failed: %s", err.Error())
		return
	}
	logger.SugaredLogger.Infof(
		"stock master refreshed: source=%s rows=%d sha256=%s",
		result.Source,
		result.ValidRows,
		result.SHA256,
	)
}
