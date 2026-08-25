package data

import (
	"context"
	"encoding/json"
	"fmt"
	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/util"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/duke-git/lancet/v2/convertor"
	"github.com/duke-git/lancet/v2/random"
	"github.com/duke-git/lancet/v2/strutil"
	"github.com/go-resty/resty/v2"
)

// @Author spark
// @Date 2024/12/10 9:55
// @Desc
//-----------------------------------------------------------------------------------

func TestGetTelegraph(t *testing.T) {
	requireIntegration(t)
	initDatabaseForTest(t, "../../data/stock.db")

	//telegraphs := GetTelegraphList(30)
	//for _, telegraph := range *telegraphs {
	//	logger.SugaredLogger.Info(telegraph)
	//}
	list := NewMarketNewsApi().GetNewTelegraph(30)
	for _, telegraph := range *list {
		logger.SugaredLogger.Infof("telegraph:%+v", telegraph)
	}
}

func TestGetFinancialReports(t *testing.T) {
	requireIntegration(t)
	initDatabaseForTest(t, "../../data/stock.db")
	//GetFinancialReports("sz000802", 30)
	//GetFinancialReports("hk00927", 30)
	//GetFinancialReports("gb_aapl", 30)
	GetFinancialReportsByXUEQIU("sz000802", 30)
	GetFinancialReportsByXUEQIU("gb_aapl", 30)
	GetFinancialReportsByXUEQIU("hk00927", 30)

}

func TestGetTelegraphSearch(t *testing.T) {
	requireIntegration(t)
	initDatabaseForTest(t, "../../data/stock.db")
	searchWords := "半导体 新能源汽车 机器人"
	//url := "https://www.cls.cn/searchPage?keyword=%E9%97%BB%E6%B3%B0%E7%A7%91%E6%8A%80&type=telegram"
	messages := SearchStockInfo(searchWords, "telegram", 30)
	for _, message := range *messages {
		logger.SugaredLogger.Info(message)
	}

	//https://www.cls.cn/stock?code=sh600745
}
func TestCailianpressWeb(t *testing.T) {
	requireIntegration(t)
	initDatabaseForTest(t, "../../data/stock.db")
	searchWords := "半导体 新能源汽车 机器人"
	res := NewMarketNewsApi().CailianpressWeb(searchWords)
	md := util.MarkdownTableWithTitle(searchWords+"财联社新闻", res.List)
	logger.SugaredLogger.Info(md)
}

func TestSearchStockInfoByCode(t *testing.T) {
	requireIntegration(t)
	initDatabaseForTest(t, "../../data/stock.db")
	SearchStockInfoByCode("sh600745")
}

func TestSearchStockPriceInfo(t *testing.T) {
	requireIntegration(t)
	initDatabaseForTest(t, "../../data/stock.db")
	SearchStockPriceInfo("博安生物", "hk06955", 30)
	SearchStockPriceInfo("上海贝岭", "sh600171", 30)
	//SearchStockPriceInfo("苹果公司", "gb_aapl", 30)
	//SearchStockPriceInfo("微创光电", "bj430198", 30)
	//getZSInfo("创业板指数", "sz399006", 30)
	//getZSInfo("上证综合指数", "sh000001", 30)
	//getZSInfo("沪深300指数", "sh000300", 30)

}
func TestGetStockMinutePriceData(t *testing.T) {
	requireIntegration(t)
	initDatabaseForTest(t, "../../data/stock.db")
	data, date := NewStockDataApi().GetStockMinutePriceData("usTSLA.OQ")
	logger.SugaredLogger.Infof("date:%s", date)
	logger.SugaredLogger.Infof("%+#v", *data)
}
func TestGetKLineData(t *testing.T) {
	requireIntegration(t)
	initDatabaseForTest(t, "../../data/stock.db")
	k := NewStockDataApi().GetKLineData("sh600171", "240", 30)
	//for _, kline := range *k {
	//	logger.SugaredLogger.Infof("%+#v", kline)
	//}
	jsonData, _ := json.Marshal(*k)
	markdownTable, err := JSONToMarkdownTable(jsonData)
	if err != nil {
		logger.SugaredLogger.Errorf("json.Marshal error:%s", err.Error())
	}
	logger.SugaredLogger.Infof("markdownTable:\n%s", markdownTable)

}
func TestGetHK_KLineData(t *testing.T) {
	requireIntegration(t)
	initDatabaseForTest(t, "../../data/stock.db")
	k := NewStockDataApi().GetHK_KLineData("hk01810", "day", 1)
	jsonData, _ := json.Marshal(*k)
	markdownTable, err := JSONToMarkdownTable(jsonData)
	if err != nil {
		logger.SugaredLogger.Errorf("json.Marshal error:%s", err.Error())
	}
	logger.SugaredLogger.Infof("markdownTable:\n%s", markdownTable)

}

func TestGetHKStockInfo(t *testing.T) {
	requireIntegration(t)
	initDatabaseForTest(t, "../../data/stock.db")
	//NewStockDataApi().GetHKStockInfo(200)
	//NewStockDataApi().GetSinaHKStockInfo()
	//m:105,m:106,m:107  //美股
	//m:128+t:3,m:128+t:4,m:128+t:1,m:128+t:2 //港股
	//274  224 605
	for i := 197; i <= 274; i++ {
		NewStockDataApi().getDCStockInfo("", i, 20)
		time.Sleep(time.Duration(random.RandInt(2, 5)) * time.Second)
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
	legacy, legacyErr := ParseTxHKStockData(strutil.SplitAndTrim(input, "=", "\""))
	if legacyErr != nil || legacy["今日最高价"] != "22.16" || legacy["今日最低价"] != "21.84" || legacy["时间"] != "09:42:09" {
		t.Fatalf("legacy A-share parse=%v err=%v", legacy, legacyErr)
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

func TestGetRealTimeStockPriceInfo(t *testing.T) {
	requireIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	text, texttime := GetRealTimeStockPriceInfo(ctx, "sh600171")
	logger.SugaredLogger.Infof("res:%s,%s", text, texttime)

	text, texttime = GetRealTimeStockPriceInfo(ctx, "sh600438")
	logger.SugaredLogger.Infof("res:%s,%s", text, texttime)

	texttime = strings.ReplaceAll(texttime, "）", "")
	texttime = strings.ReplaceAll(texttime, "（", "")
	parts := strings.Split(texttime, " ")
	logger.SugaredLogger.Infof("parts:%+v", parts)

	//去除中文字符
	// 正则表达式匹配中文字符
	re := regexp.MustCompile(`\p{Han}+`)
	texttime = re.ReplaceAllString(texttime, "")

	logger.SugaredLogger.Infof("texttime:%s", texttime)
	location, err := time.ParseInLocation("2006-01-02 15:04:05", texttime, time.Local)
	if err != nil {
		return
	}
	logger.SugaredLogger.Infof("location:%s", location.Format("2006-01-02 15:04:05"))
}

func TestParseFullSingleStockData(t *testing.T) {
	requireIntegration(t)
	resp, err := resty.New().R().
		SetHeader("Host", "hq.sinajs.cn").
		SetHeader("Referer", "https://finance.sina.com.cn/").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36 Edg/119.0.0.0").
		Get(fmt.Sprintf(sinaStockUrl, time.Now().Unix(), "sh600584,sz000938,hk01810,hk00856,gb_aapl,gb_tsla,sb873721,bj430300"))
	if err != nil {
		logger.SugaredLogger.Error(err.Error())
	}
	data := GB18030ToUTF8(resp.Body())
	strs := strutil.SplitEx(data, "\n", true)
	for _, str := range strs {
		logger.SugaredLogger.Info(str)
		stockData, err := ParseFullSingleStockData(str)
		if err != nil {
			return
		}
		logger.SugaredLogger.Infof("%+#v", stockData)
	}

	result, er := ParseFullSingleStockData("var hq_str_gb_tsla = \"特斯拉,268.8472,-5.55,2025-03-04 22:52:56,-15.8028,270.9300,278.2800,268.1000,488.5400,138.8030,23618295,88214389,864751599149,2.23,120.550000,0.00,0.00,0.00,0.00,3216517037,61,0.0000,0.00,0.00,,Mar 04 09:52AM EST,284.6500,0,1,2025,6458502467.0000,0.0000,0.0000,0.0000,0.0000,284.6500\";")
	if er != nil {
		logger.SugaredLogger.Error(er.Error())
	}
	logger.SugaredLogger.Infof("%+#v", result)
}

func TestNewStockDataApi(t *testing.T) {
	requireIntegration(t)
	initDatabaseForTest(t, "../../data/stock.db")
	stockDataApi := NewStockDataApi()
	datas, _ := stockDataApi.GetStockCodeRealTimeData("sz002352", "sh600859", "sh600745", "gb_tsla", "hk09660", "hk00700")
	for _, data := range *datas {
		t.Log(data)
	}
}

func TestGetStockBaseInfo(t *testing.T) {
	requireIntegration(t)
	initDatabaseForTest(t, "../../data/stock.db")
	stockDataApi := NewStockDataApi()
	stockDataApi.GetStockBaseInfo()
	//stocks := &[]StockBasic{}
	//db.Dao.Model(&StockBasic{}).Find(stocks)
	//for _, stock := range *stocks {
	//	NewStockDataApi().GetStockCodeRealTimeData(getSinaCode(stock.TsCode))
	//}

}
func getSinaCode(code string) string {
	c := strings.Split(code, ".")
	return strings.ToLower(c[1]) + c[0]
}

func TestReadFile(t *testing.T) {
	requireIntegration(t)
	file, err := os.ReadFile("../../stock_basic.json")
	if err != nil {
		t.Log(err)
		return
	}
	res := &TushareStockBasicResponse{}
	if err := json.Unmarshal(file, res); err != nil {
		t.Fatalf("unmarshal stock_basic.json failed: %v", err)
	}
	initDatabaseForTest(t, "../../data/stock.db")
	//[EXCHANGE IS_HS NAME INDUSTRY LIST_STATUS ACT_NAME ID CURR_TYPE AREA LIST_DATE DELIST_DATE ACT_ENT_TYPE TS_CODE SYMBOL CN_SPELL ASSET_CLASS ACT_TYPE CREATE_TIME CREATE_BY UPDATE_TIME FULLNAME ENNAME UPDATE_BY]
	for _, item := range res.Data.Items {
		stock := &StockBasic{}
		stock.Exchange = convertor.ToString(item[0])
		stock.IsHs = convertor.ToString(item[1])
		stock.Name = convertor.ToString(item[2])
		stock.Industry = convertor.ToString(item[3])
		stock.ListStatus = convertor.ToString(item[4])
		stock.ActName = convertor.ToString(item[5])
		stock.ID = uint(item[6].(float64))
		stock.CurrType = convertor.ToString(item[7])
		stock.Area = convertor.ToString(item[8])
		stock.ListDate = convertor.ToString(item[9])
		stock.DelistDate = convertor.ToString(item[10])
		stock.ActEntType = convertor.ToString(item[11])
		stock.TsCode = convertor.ToString(item[12])
		stock.Symbol = convertor.ToString(item[13])
		stock.Cnspell = convertor.ToString(item[14])
		stock.Fullname = convertor.ToString(item[20])
		stock.Ename = convertor.ToString(item[21])
		t.Logf("%+v", stock)
		db.Dao.Model(&StockBasic{}).FirstOrCreate(stock, &StockBasic{TsCode: stock.TsCode}).Updates(stock)
	}

	//t.Log(res.Data.Fields)
}

func TestFollowedList(t *testing.T) {
	requireIntegration(t)
	initDatabaseForTest(t, "../../data/stock.db")
	stockDataApi := NewStockDataApi()
	stockDataApi.GetFollowList(1)

}

func TestStockDataApi_GetIndexBasic(t *testing.T) {
	requireIntegration(t)
	initDatabaseForTest(t, "../../data/stock.db")
	stockDataApi := NewStockDataApi()
	stockDataApi.GetIndexBasic()
}

func TestName(t *testing.T) {
	requireIntegration(t)
	initDatabaseForTest(t, "../../data/stock.db")

	stockBasics := &[]StockBasic{}
	resp, err := resty.New().SetProxy("").R().
		SetHeader("user", "go-stock").
		SetResult(stockBasics).
		Get("http://8.134.249.145:18080/go-stock/stock_basic.json")
	if err != nil {
		t.Fatalf("fetch stock basics failed: %v", err)
	}
	if resp.IsError() {
		t.Fatalf("unexpected status code: %d", resp.StatusCode())
	}

	logger.SugaredLogger.Infof("%+v", stockBasics)
	//db.Dao.Unscoped().Model(&StockBasic{}).Where("1=1").Delete(&StockBasic{})
	//err := db.Dao.CreateInBatches(stockBasics, 400).Error
	//if err != nil {
	//	t.Log(err.Error())
	//}

}
func TestGetStockMoneyData(t *testing.T) {
	requireIntegration(t)
	initDatabaseForTest(t, "../../data/stock.db")
	stockDataApi := NewStockDataApi()
	res := stockDataApi.GetStockMoneyData()
	logger.SugaredLogger.Infof("%s", util.MarkdownTableWithTitle("今日个股资金流向Top50", res.Data.Diff))
}

func TestGetStockConceptInfo(t *testing.T) {
	requireIntegration(t)
	initDatabaseForTest(t, "../../data/stock.db")
	stockDataApi := NewStockDataApi()
	res := stockDataApi.GetStockConceptInfo("601138.SH")
	logger.SugaredLogger.Infof("%s", util.MarkdownTableWithTitle("601138.SH所属概念/板块信息", res.Result.Data))

}
