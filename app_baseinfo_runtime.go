package main

import (
	"context"
	"fmt"

	"go-stock/backend/logger"
	"go-stock/backend/models"

	"github.com/go-resty/resty/v2"
)

func (a *App) CheckStockBaseInfo(ctx context.Context) {
	defer PanicHandler()
	defer func() {
		go emitEvent(ctx, "loadingMsg", "done")
	}()

	domestic, err := fetchRemoteTable[models.StockBasic](baseInfoURL("stock_basic.json"))
	if err != nil {
		logger.SugaredLogger.Errorf("保存StockBasic股票基础信息失败:%s", err.Error())
		return
	}
	hongKong, err := fetchRemoteTable[models.StockInfoHK](baseInfoURL("stock_base_info_hk.json"))
	if err != nil {
		logger.SugaredLogger.Errorf("保存StockInfoHK股票基础信息失败:%s", err.Error())
		return
	}
	unitedStates, err := fetchRemoteTable[models.StockInfoUS](baseInfoURL("stock_base_info_us.json"))
	if err != nil {
		logger.SugaredLogger.Errorf("保存StockInfoUS股票基础信息失败:%s", err.Error())
		return
	}
	if err := a.services.Stock.ReplaceStockBaseInfo(ctx, domestic, hongKong, unitedStates); err != nil {
		logger.SugaredLogger.Errorf("保存股票基础信息失败:%s", err.Error())
	}
}

func fetchRemoteTable[T any](url string) ([]T, error) {
	dest := make([]T, 0)
	response, err := resty.New().R().
		SetHeader("user", "go-stock").
		SetResult(&dest).
		Get(url)
	if err != nil {
		return nil, err
	}
	if response.IsError() {
		return nil, fmt.Errorf("remote table request failed: status=%s", response.Status())
	}
	return dest, nil
}
