package data

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/go-resty/resty/v2"
)

type stockDataRoundTripFunc func(*http.Request) (*http.Response, error)

func (function stockDataRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestFetchIndexBasicReturnsValidatedRowsWithoutPersistence(t *testing.T) {
	body := `{"code":0,"data":{"fields":["ts_code","name","market","symbol"],"items":[["000001.SH","上证指数","SSE","000001"]]}}`
	client := resty.New()
	client.SetTransport(stockDataRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		header := make(http.Header)
		header.Set("Content-Type", "application/json")
		return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	}))
	api := StockDataApi{client: client, config: &SettingConfig{Settings: &Settings{TushareToken: "test"}}}
	rows, err := api.FetchIndexBasic(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].TsCode != "000001.SH" || rows[0].Name != "上证指数" {
		t.Fatalf("index rows = %+v", rows)
	}

	body = `{"code":0,"data":{"fields":["ts_code","name"],"items":[["000002.SH",""]]}}`
	if _, err := api.FetchIndexBasic(context.Background()); err == nil {
		t.Fatal("incomplete index row was accepted")
	}
}

func TestParseTxStockData(t *testing.T) {
	input := "v_sz002241=\"51~歌尔股份~002241~21.92~22.27~22.14~109872~40211~69642~21.91~25~21.90~961~21.89~257~21.88~748~21.87~665~21.92~86~21.93~168~21.94~556~21.95~171~21.96~85~~20250509094209~-0.35~-1.57~22.16~21.84~21.92/109872/241183171~109872~24118~0.36~27.78~~22.16~21.84~1.44~675.97~765.22~2.27~24.50~20.04~2.57~1590~21.95~40.80~28.71~~~1.24~24118.3171~0.0000~0~\n~GP-A~-15.07~5.13~1.11~8.18~3.39~30.63~15.70~5.23~15.67~-25.11~3083811231~3490989083~42.72~10.31~3083811231~~~37.23~0.18~~CNY~0~~21.85~1952\";"
	info, err := ParseTxStockData(input)
	if err != nil {
		t.Fatal(err)
	}
	if info.Volume != "10987200" || info.Amount != "241183171" {
		t.Fatalf("A-share turnover = %s/%s, want 10987200/241183171", info.Volume, info.Amount)
	}
	if info.High != "22.16" || info.Low != "21.84" {
		t.Fatalf("A-share high/low = %s/%s, want 22.16/21.84", info.High, info.Low)
	}
	if info.Date != "2025-05-09" || info.Time != "09:42:09" {
		t.Fatalf("A-share quote time = %s %s, want 2025-05-09 09:42:09", info.Date, info.Time)
	}
	invalid := strings.Replace(input, "~-1.57~22.16~21.84~", "~-1.57~20.00~21.84~", 1)
	if _, err = ParseTxStockData(invalid); err == nil || !strings.Contains(err.Error(), "OHLC is inconsistent") {
		t.Fatalf("inconsistent A-share OHLC err=%v", err)
	}
}

func TestParseTxStockDataHongKongTurnover(t *testing.T) {
	input := "v_r_hk09660=\"100~HORIZONROBOT-W~09660~6.270~5.690~5.800~195083034.0~0~0~6.270~0~0~0~0~0~0~0~0~0~6.270~0~0~0~0~0~0~0~0~0~195083034.0~2025/04/29 13:45:41~0.580~10.19~6.450~5.710~6.270~195083034.0~1195673623.140~0~32.66\";"
	info, err := ParseTxStockData(input)
	if err != nil {
		t.Fatal(err)
	}
	if info.Volume != "195083034" || info.Amount != "1195673623.140" {
		t.Fatalf("HK turnover = %s/%s, want 195083034/1195673623.140", info.Volume, info.Amount)
	}
	if info.High != "6.450" || info.Low != "5.710" {
		t.Fatalf("HK high/low = %s/%s, want 6.450/5.710", info.High, info.Low)
	}
}

func TestParseFullSingleStockDataUS(t *testing.T) {
	input := "var hq_str_gb_tsla = \"特斯拉,268.8472,-5.55,2025-03-04 22:52:56,-15.8028,270.9300,278.2800,268.1000,488.5400,138.8030,23618295,88214389,864751599149,2.23,120.550000,0.00,0.00,0.00,0.00,3216517037,61,0.0000,0.00,0.00,,Mar 04 09:52AM EST,284.6500,0,1,2025,6458502467.0000,0.0000,0.0000,0.0000,0.0000,284.6500\";"
	info, err := ParseFullSingleStockData(input)
	if err != nil {
		t.Fatal(err)
	}
	if info.Code != "gb_tsla" || info.Name != "特斯拉" || info.Price != "268.8472" || info.PreClose != "284.6500" {
		t.Fatalf("unexpected US quote: %+v", info)
	}
	if info.Date != "2025-03-04" || info.Time != "22:52:56" {
		t.Fatalf("US quote time = %s %s, want 2025-03-04 22:52:56", info.Date, info.Time)
	}
}
