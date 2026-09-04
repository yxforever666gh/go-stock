package main

import (
	"fmt"
	"time"

	"go-stock/backend/models"
	"go-stock/backend/stocks"
)

var embeddedStockMasterSeedManifest = models.StockMasterSeedManifest{
	GeneratedAt: time.Date(2026, time.August, 27, 14, 58, 11, 0, time.FixedZone("CST", 8*60*60)),
	RowCount:    5418,
	SHA256:      "ee91ce3ae7f91238c24afa2817bc570f1f310683ef13b22424ddc69480502edb",
}

func embeddedDomesticStockMaster() ([]models.StockBasic, models.StockMasterRefreshResult, error) {
	rows, result, err := stocks.DecodeStockMasterPayload(stocksBin)
	if err != nil {
		return nil, result, fmt.Errorf("embedded stock master seed: %w", err)
	}
	result.Source = "embedded_stock_basic"
	result.UsedSeed = true
	if result.ValidRows != embeddedStockMasterSeedManifest.RowCount || result.SHA256 != embeddedStockMasterSeedManifest.SHA256 {
		return nil, result, fmt.Errorf("embedded stock master seed does not match manifest")
	}
	if age := time.Since(embeddedStockMasterSeedManifest.GeneratedAt); age > 60*24*time.Hour {
		return nil, result, fmt.Errorf("embedded stock master seed is older than 60 days")
	}
	return rows, result, nil
}
