package funds

import (
	"errors"
	"strings"
	"testing"

	"go-stock/backend/models"
	appservice "go-stock/internal/service"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestApplicationServiceFundAndETFWatchlists(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.FundBasic{}, &models.FollowedFund{}, &models.ETFWatchlistItem{}); err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&models.FundBasic{Code: "000001", Name: "价值混合"}).Error; err != nil {
		t.Fatal(err)
	}

	service := NewApplicationService(database)
	if message, err := service.FollowFund("000001"); err != nil || message != "关注成功" {
		t.Fatalf("follow fund = %q, %v", message, err)
	}
	if items := service.GetFollowedFund(); len(items) != 1 || items[0].Code != "000001" || items[0].FundBasic.Name != "价值混合" {
		t.Fatalf("followed funds = %+v", items)
	}
	if message, err := service.UnFollowFund("000001"); err != nil || message != "取消关注成功" {
		t.Fatalf("unfollow fund = %q, %v", message, err)
	}
	if _, err := service.UnFollowFund("000001"); !errors.Is(err, appservice.ErrNotFound) {
		t.Fatalf("second fund removal error = %v, want not found", err)
	}

	item := models.ETFWatchlistItem{Code: " 510300 ", Name: "沪深300ETF", Market: "sh", Category: "BROAD"}
	if message, err := service.FollowETF(item); err != nil || message != "关注 ETF 成功" {
		t.Fatalf("follow ETF = %q, %v", message, err)
	}
	item.Name = "沪深300ETF（更新）"
	if _, err := service.FollowETF(item); err != nil {
		t.Fatalf("idempotent ETF follow: %v", err)
	}
	etfs, err := service.GetFollowedETFs()
	if err != nil {
		t.Fatal(err)
	}
	if len(etfs) != 1 || etfs[0].Code != "sh510300" || etfs[0].Name != item.Name || etfs[0].Category != "broad" {
		t.Fatalf("ETF watchlist = %+v", etfs)
	}
	if message, err := service.UnFollowETF("510300"); err != nil || message != "取消关注 ETF 成功" {
		t.Fatalf("unfollow ETF = %q, %v", message, err)
	}
	if _, err := service.UnFollowETF("510300"); !errors.Is(err, appservice.ErrNotFound) {
		t.Fatalf("second ETF removal error = %v, want not found", err)
	}
}

func TestParseLegacyFundBasic(t *testing.T) {
	html := `<div class="merchandiseDetail"><div class="fundDetail-tit">价值混合查看相关ETF&gt;</div></div>
<div class="infoOfFund"><table><tr><td>基金类型：混合型</td><td>成立日期：2020-01-01</td><td>基金规模：12亿元</td></tr></table></div>
<div class="dataOfFund"><dl><dd>近1月：1.25%</dd><dd>近1年：8.50%</dd></dl></div>`
	fund, err := parseLegacyFundBasic([]byte(html), "000001")
	if err != nil {
		t.Fatal(err)
	}
	if fund.Name != "价值混合" || fund.Type != "混合型" || fund.Scale != "12亿元" {
		t.Fatalf("fund basic = %+v", fund)
	}
	if fund.NetGrowth1 == nil || *fund.NetGrowth1 != 1.25 || fund.NetGrowth12 == nil || *fund.NetGrowth12 != 8.5 {
		t.Fatalf("fund growth parsing = %+v", fund)
	}
	if strings.TrimSpace(fund.Establishment) != "2020-01-01" {
		t.Fatalf("fund establishment = %q", fund.Establishment)
	}
}
