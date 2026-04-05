package main

import (
	"context"
	"encoding/json"
	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
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
	db.Init("./data/stock.db")
	NewApp().CheckStockBaseInfo(context.Background())
}

func TestJson(t *testing.T) {
	db.Init("./data/stock.db")

	jsonStr := "{\n\t\t\"id\" : 3334,\n\t\t\"created_at\" : \"2025-02-28 16:49:31.8342514+08:00\",\n\t\t\"updated_at\" : \"2025-02-28 16:49:31.8342514+08:00\",\n\t\t\"deleted_at\" : null,\n\t\t\"code\" : \"PUK.US\",\n\t\t\"name\" : \"英国保诚集团\",\n\t\t\"full_name\" : \"\",\n\t\t\"e_name\" : \"\",\n\t\t\"exchange\" : \"NASDAQ\",\n\t\t\"type\" : \"stock\",\n\t\t\"is_del\" : 0,\n\t\t\"bk_name\" : null,\n\t\t\"bk_code\" : null\n\t}"

	v := &models.StockInfoUS{}
	json.Unmarshal([]byte(jsonStr), v)
	logger.SugaredLogger.Infof("v:%+v", v)

	db.Dao.Model(v).Updates(v)

}

func TestUpdateCheck(t *testing.T) {
	releaseVersion := &models.GitHubReleaseVersion{}
	_, err := resty.New().R().
		SetResult(releaseVersion).
		SetHeader("Accept", "application/vnd.github+json").
		SetHeader("X-GitHub-Api-Version", "2022-11-28").
		Get(latestReleaseAPIURL())
	//  https://api.github.com/repos/OWNER/REPO/releases/latest
	if err != nil {
		logger.SugaredLogger.Errorf("get github release version error:%s", err.Error())
		return
	}
	logger.SugaredLogger.Infof("releaseVersion:%+v", releaseVersion)
}

func TestGetScreenResolution(t *testing.T) {
	x, y, w, h, err := getScreenResolution()
	if err != nil {
		logger.SugaredLogger.Errorf("get screen resolution error:%s", err.Error())
		return
	}
	logger.SugaredLogger.Infof("x:%d,y:%d,w:%d,h:%d", x, y, w, h)

}

func TestCheckUpdate(t *testing.T) {
	db.Init("./data/stock.db")
	BuildKey = "8171b192a21b4d95a42fdcd54478e3ed"
	NewApp().CheckUpdate(1)
}
