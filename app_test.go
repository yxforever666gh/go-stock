package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"go-stock/backend/logger"
	"go-stock/backend/models"
)

// @Author spark
// @Date 2025/2/24 9:35
// @Desc
// -----------------------------------------------------------------------------------
func TestIsHKTradingTime(t *testing.T) {
	f := IsHKTradingTime(time.Now())
	t.Log(f)
}

func TestIsUSTradingTime(t *testing.T) {

	date := time.Now()
	hour, minute, _ := date.Clock()
	logger.SugaredLogger.Infof("当前时间: %d:%d", hour, minute)

	t.Log(IsUSTradingTime(time.Now()))
}

func TestCheckStockBaseInfo(t *testing.T) {
	requireIntegration(t)
	initDatabaseForTest(t, "./data/stock.db")
	NewApp().CheckStockBaseInfo(context.Background())
}

func TestStockInfoUSJSONUnmarshal(t *testing.T) {
	jsonStr := "{\n\t\t\"id\" : 3334,\n\t\t\"created_at\" : \"2025-02-28 16:49:31.8342514+08:00\",\n\t\t\"updated_at\" : \"2025-02-28 16:49:31.8342514+08:00\",\n\t\t\"deleted_at\" : null,\n\t\t\"code\" : \"PUK.US\",\n\t\t\"name\" : \"英国保诚集团\",\n\t\t\"full_name\" : \"\",\n\t\t\"e_name\" : \"\",\n\t\t\"exchange\" : \"NASDAQ\",\n\t\t\"type\" : \"stock\",\n\t\t\"is_del\" : 0,\n\t\t\"bk_name\" : null,\n\t\t\"bk_code\" : null\n\t}"

	v := &models.StockInfoUS{}
	if err := json.Unmarshal([]byte(jsonStr), v); err != nil {
		t.Fatalf("json unmarshal failed: %v", err)
	}
	if v.Code != "PUK.US" {
		t.Fatalf("unexpected code: %s", v.Code)
	}
	if v.Name != "英国保诚集团" {
		t.Fatalf("unexpected name: %s", v.Name)
	}
	if v.Exchange != "NASDAQ" {
		t.Fatalf("unexpected exchange: %s", v.Exchange)
	}
}

func TestUpdateCheck(t *testing.T) {
	requireIntegration(t)
	releaseVersion, err := NewApp().fetchLatestReleaseVersion()
	if err != nil {
		t.Fatalf("fetch latest release version failed: %v", err)
	}
	if releaseVersion == nil {
		t.Fatal("expected release version")
	}
	if releaseVersion.TagName == "" {
		t.Fatal("expected non-empty release tag")
	}
	logger.SugaredLogger.Infof("releaseVersion:%+v", releaseVersion)
}

func TestGetScreenResolution(t *testing.T) {
	requireDesktopTest(t)
	x, y, w, h, err := getScreenResolution()
	if err != nil {
		t.Fatalf("get screen resolution error:%s", err.Error())
	}
	logger.SugaredLogger.Infof("x:%d,y:%d,w:%d,h:%d", x, y, w, h)
}
