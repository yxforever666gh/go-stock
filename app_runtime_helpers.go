package main

import (
	"context"
	"encoding/json"
	"fmt"
	"go-stock/backend/data"
	"go-stock/backend/db"
	log "go-stock/backend/logger"
	"go-stock/backend/models"
	"os"
	"runtime/debug"
	"strings"

	"github.com/duke-git/lancet/v2/slice"
)

func initStockDataUS(ctx context.Context) {
	defer func() {
		go emitEvent(ctx, "loadingMsg", "done")
	}()
	var v []models.StockInfoUS
	err := json.Unmarshal(stocksBinUS, &v)
	if err != nil {
		log.SugaredLogger.Error(err.Error())
		return
	}
	log.SugaredLogger.Infof("init stock data us %d", len(v))
	var total int64
	db.Dao.Model(&models.StockInfoUS{}).Count(&total)
	if total != int64(len(v)) {
		for _, item := range v {
			var count int64
			db.Dao.Model(&models.StockInfoUS{}).Where("code = ?", item.Code).Count(&count)
			if count > 0 {
				continue
			}
			db.Dao.Model(&models.StockInfoUS{}).Create(&item)
		}
	}
}

func initStockDataHK(ctx context.Context) {
	defer func() {
		go emitEvent(ctx, "loadingMsg", "done")
	}()
	var v []models.StockInfoHK
	err := json.Unmarshal(stocksBinHK, &v)
	if err != nil {
		log.SugaredLogger.Error(err.Error())
		return
	}
	log.SugaredLogger.Infof("init stock data hk %d", len(v))
	var total int64
	db.Dao.Model(&models.StockInfoHK{}).Count(&total)
	if total != int64(len(v)) {
		for _, item := range v {
			var count int64
			db.Dao.Model(&models.StockInfoHK{}).Where("code = ?", item.Code).Count(&count)
			if count > 0 {
				continue
			}
			db.Dao.Model(&models.StockInfoHK{}).Create(&item)
		}
	}
}

func updateBasicInfo() {
	config := data.GetSettingConfig()
	if config.UpdateBasicInfoOnStart {
		go data.NewStockDataApi().GetStockBaseInfo()
		go data.NewStockDataApi().GetIndexBasic()
	}
}

func initStockData(ctx context.Context) {
	defer func() {
		go emitEvent(ctx, "loadingMsg", "done")
	}()
	fields := "ts_code,symbol,name,area,industry,cnspell,market,list_date,act_name,act_ent_type,fullname,exchange,list_status,curr_type,enname,delist_date,is_hs"
	log.SugaredLogger.Info("init stock data")
	res := &data.TushareStockBasicResponse{}
	err := json.Unmarshal(stocksBin, res)
	if err != nil {
		log.SugaredLogger.Error(err.Error())
		return
	}

	for _, item := range res.Data.Items {
		stock := &data.StockBasic{}
		stockData := map[string]any{}
		for _, field := range strings.Split(fields, ",") {
			idx := slice.IndexOf(res.Data.Fields, field)
			if idx == -1 {
				continue
			}
			stockData[field] = item[idx]
		}
		jsonData, _ := json.Marshal(stockData)
		err := json.Unmarshal(jsonData, stock)
		if err != nil {
			continue
		}
		stock.ID = 0
		var count int64
		db.Dao.Model(&data.StockBasic{}).Where("ts_code = ?", stock.TsCode).Count(&count)
		if count > 0 {
			continue
		}
		db.Dao.Create(stock)
	}
}

func checkDir(dir string) {
	if strings.TrimSpace(dir) == "" {
		return
	}
	_, err := os.Stat(dir)
	if os.IsNotExist(err) {
		_ = os.MkdirAll(dir, os.ModePerm)
		log.SugaredLogger.Info("create dir: " + dir)
	}
	if BuildKey == "" {
		BuildKey = "cc1e0d684e32f176c56ff1fcf384dcd9"
	}
}

func PanicHandler() {
	if r := recover(); r != nil {
		fmt.Printf("Recovered from panic: %v\n", r)
		debug.PrintStack()
	}
}
