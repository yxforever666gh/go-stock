package main

import (
	"context"
	"go-stock/backend/data"
	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/models"

	"github.com/go-resty/resty/v2"
)

func (a *App) CheckStockBaseInfo(ctx context.Context) {
	defer PanicHandler()
	defer func() {
		go emitEvent(ctx, "loadingMsg", "done")
	}()

	if err := syncRemoteTable(baseInfoURL("stock_basic.json"), &[]data.StockBasic{}, &data.StockBasic{}); err != nil {
		logger.SugaredLogger.Errorf("保存StockBasic股票基础信息失败:%s", err.Error())
	}
	if err := syncRemoteTable(baseInfoURL("stock_base_info_hk.json"), &[]models.StockInfoHK{}, &models.StockInfoHK{}); err != nil {
		logger.SugaredLogger.Errorf("保存StockInfoHK股票基础信息失败:%s", err.Error())
	}
	if err := syncRemoteTable(baseInfoURL("stock_base_info_us.json"), &[]models.StockInfoUS{}, &models.StockInfoUS{}); err != nil {
		logger.SugaredLogger.Errorf("保存StockInfoUS股票基础信息失败:%s", err.Error())
	}
}

func syncRemoteTable[T any](url string, dest *[]T, model any) error {
	if _, err := resty.New().R().
		SetHeader("user", "go-stock").
		SetResult(dest).
		Get(url); err != nil {
		return err
	}

	db.Dao.Unscoped().Model(model).Where("1=1").Delete(model)
	if len(*dest) == 0 {
		return nil
	}
	return db.Dao.CreateInBatches(dest, 400).Error
}
