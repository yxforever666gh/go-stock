package data

// @Author spark
// @Date 2024/12/10 9:21
// @Desc
//-----------------------------------------------------------------------------------
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	"go-stock/backend/stocks"
	"io"
	"math"
	url2 "net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/duke-git/lancet/v2/convertor"
	"github.com/duke-git/lancet/v2/slice"
	"github.com/duke-git/lancet/v2/strutil"
	"github.com/go-resty/resty/v2"
	"github.com/robertkrimen/otto"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

const sinaStockUrl = "http://hq.sinajs.cn/rn=%d&list=%s"
const txStockUrl = "http://qt.gtimg.cn/?_=%d&q=%s"
const defaultPublicStockMasterURL = "https://raw.githubusercontent.com/yxforever666gh/go-stock/main/build/stock_basic.json"

// Tushare 官方接口已支持 HTTPS；使用 HTTPS 可以减少中间网络设备对明文 HTTP 的干扰，
// 同时也能降低遇到 EOF/连接中断 的概率。
const tushareApiUrl = "https://api.tushare.pro"

var ErrInvalidDataFormat = errors.New("invalid data format")

type StockDataApi struct {
	client *resty.Client
	config *SettingConfig
}

type TushareRequest struct {
	ApiName string `json:"api_name"`
	Token   string `json:"token"`
	Params  any    `json:"params"`
	Fields  string `json:"fields"`
}
type TushareResponse struct {
	RequestId string `json:"request_id"`
	Code      int    `json:"code"`
	Data      any    `json:"data"`
	Msg       string `json:"msg"`
}

/*
	字段	类型	说明
	ts_code	str	TS代码
	symbol	str	股票代码
	name	str	股票名称
	area	str	地域
	industry	str	所属行业
	fullname	str	股票全称
	enname	str	英文全称
	cnspell	str	拼音缩写
	market	str	市场类型
	exchange	str	交易所代码
	curr_type	str	交易货币
	list_status	str	上市状态 L上市 D退市 P暂停上市
	list_date	str	上市日期
	delist_date	str	退市日期
	is_hs	str	是否沪深港通标的，N否 H沪股通 S深股通
	act_name	str	实控人名称
	act_ent_type	str	实控人企业性质*/

type TushareStockBasicResponse struct {
	TushareResponse
	Data StockBasicResponse `json:"data"`
}

type StockBasicResponse struct {
	Fields  []string `json:"fields"`
	Items   [][]any  `json:"items"`
	HasMore bool     `json:"has_more"`
	Count   int      `json:"count"`
}

func NewStockDataApi() *StockDataApi {
	client := newRealtimeRestyClient()
	return &StockDataApi{
		client: client,
		config: GetSettingConfig(),
	}
}

func (receiver StockDataApi) FetchIndexBasic(ctx context.Context) ([]models.IndexBasic, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if receiver.config == nil || strings.TrimSpace(receiver.config.TushareToken) == "" {
		return nil, errors.New("Tushare token is not configured")
	}
	res := &TushareStockBasicResponse{}
	fields := "ts_code,name,market,publisher,category,base_date,base_point,list_date,fullname,index_type,weight_rule,desc"
	_, err := receiver.client.R().
		SetContext(ctx).
		SetHeader("content-type", "application/json").
		SetBody(&TushareRequest{
			ApiName: "index_basic",
			Token:   receiver.config.TushareToken,
			Params:  nil,
			Fields:  fields}).
		SetResult(res).
		Post(tushareApiUrl)
	if err != nil {
		return nil, fmt.Errorf("fetch Tushare index master: %w", err)
	}
	if res.Code != 0 {
		return nil, fmt.Errorf("Tushare index master rejected request: code=%d message=%s", res.Code, strings.TrimSpace(res.Msg))
	}
	rows := make([]models.IndexBasic, 0, len(res.Data.Items))
	for _, item := range res.Data.Items {
		values := map[string]any{}
		for _, field := range strings.Split(fields, ",") {
			idx := slice.IndexOf(res.Data.Fields, field)
			if idx == -1 || idx >= len(item) {
				continue
			}
			values[field] = item[idx]
		}
		payload, err := json.Marshal(values)
		if err != nil {
			return nil, fmt.Errorf("encode Tushare index row: %w", err)
		}
		var index models.IndexBasic
		if err := json.Unmarshal(payload, &index); err != nil {
			return nil, fmt.Errorf("decode Tushare index row: %w", err)
		}
		index.TsCode = strings.TrimSpace(index.TsCode)
		index.Name = strings.TrimSpace(index.Name)
		if index.TsCode == "" || index.Name == "" {
			return nil, errors.New("Tushare index master contains an incomplete row")
		}
		index.ID = 0
		rows = append(rows, index)
	}
	if len(rows) == 0 {
		return nil, errors.New("Tushare index master returned no valid rows")
	}
	return rows, nil
}

// map转换为结构体

func (receiver StockDataApi) FetchValidatedStockMaster(ctx context.Context) ([]models.StockBasic, models.StockMasterRefreshResult, error) {
	result := models.StockMasterRefreshResult{Source: "tushare", FetchedAt: time.Now().UTC()}
	if ctx == nil {
		ctx = context.Background()
	}
	if receiver.config == nil || strings.TrimSpace(receiver.config.TushareToken) == "" {
		return nil, result, errors.New("Tushare token is not configured")
	}
	fields := "ts_code,symbol,name,area,industry,cnspell,market,list_date,act_name,act_ent_type,fullname,exchange,list_status,curr_type,enname,delist_date,is_hs"
	resp, err := receiver.client.R().
		SetContext(ctx).
		SetHeader("content-type", "application/json").
		SetBody(&TushareRequest{
			ApiName: "stock_basic",
			Token:   receiver.config.TushareToken,
			Params:  nil,
			Fields:  fields,
		}).
		Post(tushareApiUrl)
	if err != nil {
		return nil, result, fmt.Errorf("fetch Tushare stock master: %w", err)
	}
	if resp.IsError() {
		return nil, result, fmt.Errorf("fetch Tushare stock master: status=%s", resp.Status())
	}
	rows, decoded, err := stocks.DecodeStockMasterPayload(resp.Body())
	decoded.Source = result.Source
	if err != nil {
		return nil, decoded, err
	}
	return rows, decoded, nil
}

// FetchValidatedPublicStockMaster loads the controlled public snapshot used
// when Tushare is unavailable. It is validated by the same strict decoder and
// never writes the database itself.
func (receiver StockDataApi) FetchValidatedPublicStockMaster(ctx context.Context) ([]models.StockBasic, models.StockMasterRefreshResult, error) {
	result := models.StockMasterRefreshResult{Source: "controlled_public", FetchedAt: time.Now().UTC()}
	if ctx == nil {
		ctx = context.Background()
	}
	endpoint := strings.TrimSpace(os.Getenv("GO_STOCK_BASEINFO_BASE_URL"))
	if endpoint == "" {
		endpoint = defaultPublicStockMasterURL
	} else {
		endpoint = strings.TrimRight(endpoint, "/") + "/stock_basic.json"
	}
	client := receiver.client
	if client == nil {
		client = newRealtimeRestyClient()
	}
	resp, err := client.R().SetContext(ctx).Get(endpoint)
	if err != nil {
		return nil, result, fmt.Errorf("fetch controlled public stock master: %w", err)
	}
	if resp.IsError() {
		return nil, result, fmt.Errorf("fetch controlled public stock master: status=%s", resp.Status())
	}
	rows, decoded, err := stocks.DecodeStockMasterPayload(resp.Body())
	decoded.Source = result.Source
	if err != nil {
		return nil, decoded, err
	}
	return rows, decoded, nil
}

// GetStockCodeRealTimeDataReadOnly fetches quotes without writing stock_info.
func (receiver StockDataApi) GetStockCodeRealTimeDataReadOnly(ctx context.Context, StockCodes ...string) (*[]models.StockInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return receiver.getStockCodeRealTimeData(ctx, StockCodes...)
}

func (receiver StockDataApi) getStockCodeRealTimeData(ctx context.Context, StockCodes ...string) (*[]models.StockInfo, error) {
	stockInfos := make([]models.StockInfo, 0)

	hkcodes := slice.Filter(StockCodes, func(i int, s string) bool {
		return strutil.HasPrefixAny(s, []string{"hk", "HK", "sh", "sz"})
	})

	if hkcodes != nil && len(hkcodes) > 0 {
		hkcodesStr := slice.JoinFunc(hkcodes, ",", func(s string) string {
			if strutil.HasPrefixAny(s, []string{"hk", "HK"}) {
				return "r_" + strings.ToLower(s)
			} else {
				return strings.ToLower(s)
			}
		})
		url := fmt.Sprintf(txStockUrl, time.Now().Unix(), hkcodesStr)
		resp, err := receiver.client.R().
			SetContext(ctx).
			SetHeader("Host", "qt.gtimg.cn").
			SetHeader("Referer", "https://gu.qq.com/").
			SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36 Edg/119.0.0.0").
			Get(url)
		logger.SugaredLogger.Infof("GetStockCodeRealTimeData %s", url)
		if err != nil {
			logger.SugaredLogger.Error(err.Error())
			return &[]models.StockInfo{}, err
		}
		str := GB18030ToUTF8(resp.Body())
		dataStr := strutil.SplitAndTrim(strings.Trim(str, "\n"), ";")

		for _, data := range dataStr {
			stockData, err := ParseTxStockData(data)
			if err != nil {
				if errors.Is(err, ErrInvalidDataFormat) {
					continue
				}
				logErrorEvery("StockDataApi.ParseTxStockData", 10*time.Minute, "ParseTxStockData err:%s", err.Error())
				continue
			}
			stockInfos = append(stockInfos, *stockData)
		}
	}

	szzsusCodes := slice.Filter(StockCodes, func(i int, s string) bool {
		return !strutil.HasPrefixAny(s, []string{"hk", "HK", "sh", "sz"})
	})

	codes := slice.JoinFunc(szzsusCodes, ",", func(s string) string {
		if strings.HasPrefix(s, "us") {
			s = strings.Replace(s, "us", "gb_", 1)
		}
		if strings.HasPrefix(s, "US") {
			s = strings.Replace(s, "US", "gb_", 1)
		}
		return strings.ToLower(s)
	})

	if strings.TrimSpace(codes) == "" {
		return &stockInfos, nil
	}

	url := fmt.Sprintf(sinaStockUrl, time.Now().Unix(), codes)
	//logger.SugaredLogger.Infof("GetStockCodeRealTimeData %s", url)
	resp, err := receiver.client.R().
		SetContext(ctx).
		SetHeader("Host", "hq.sinajs.cn").
		SetHeader("Referer", "https://finance.sina.com.cn/").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36 Edg/119.0.0.0").
		Get(url)
	if err != nil {
		logger.SugaredLogger.Error(err.Error())
		return &[]models.StockInfo{}, err
	}

	str := GB18030ToUTF8(resp.Body())
	dataStr := strutil.SplitEx(str, "\n", true)

	for _, data := range dataStr {
		//logger.SugaredLogger.Info(data)
		stockData, err := ParseFullSingleStockData(data)
		//logger.SugaredLogger.Infof("GetStockCodeRealTimeData %v", stockData)
		if err != nil {
			if errors.Is(err, ErrInvalidDataFormat) {
				continue
			}
			logErrorEvery("StockDataApi.ParseFullSingleStockData", 10*time.Minute, "ParseFullSingleStockData err:%s", err.Error())
			continue
		}
		if stockData == nil {
			continue
		}
		stockInfos = append(stockInfos, *stockData)

	}

	return &stockInfos, err
}

// GB18030ToUTF8 GB18030 转换为 UTF8
func GB18030ToUTF8(bs []byte) string {
	reader := transform.NewReader(bytes.NewReader(bs), simplifiedchinese.GB18030.NewDecoder())
	d, err := io.ReadAll(reader)
	if err != nil {
		panic(err)
	}
	return string(d)
}

func ParseTxStockData(data string) (*models.StockInfo, error) {
	return parseTencentStockInfoLine(data)
}

func parseTencentStockInfoLine(data string) (*models.StockInfo, error) {
	separator := strings.Index(data, "=")
	if separator <= 0 {
		return nil, fmt.Errorf("%w: Tencent assignment is missing", ErrInvalidDataFormat)
	}
	variable := strings.TrimSpace(data[:separator])
	payload := strings.TrimSpace(data[separator+1:])
	payload = strings.TrimSuffix(payload, ";")
	payload = strings.Trim(payload, "\"")
	if !strings.Contains(variable, "v_r_hk") && !strings.Contains(variable, "v_hk") && !strings.Contains(variable, "v_sz") && !strings.Contains(variable, "v_sh") {
		return nil, fmt.Errorf("%w: unsupported Tencent variable %q", ErrInvalidDataFormat, variable)
	}
	return parseTencentStockInfo(variable, payload)
}

func parseTencentStockInfo(variable, payload string) (*models.StockInfo, error) {
	parts := strings.Split(payload, "~")
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
	}
	if len(parts) < 38 {
		return nil, fmt.Errorf("%w: Tencent field count %d is below 38", ErrInvalidDataFormat, len(parts))
	}
	code := strings.TrimSpace(variable)
	code = strings.TrimPrefix(code, "v_r_")
	code = strings.TrimPrefix(code, "v_")
	code = strings.ToLower(strings.TrimSpace(code))
	if !strings.HasPrefix(code, "sh") && !strings.HasPrefix(code, "sz") && !strings.HasPrefix(code, "hk") {
		return nil, fmt.Errorf("%w: unsupported Tencent code %q", ErrInvalidDataFormat, code)
	}

	date, clock, err := parseTencentQuoteTime(parts)
	if err != nil {
		return nil, fmt.Errorf("Tencent quote time: %w", err)
	}
	volume, amount, err := parseTencentTurnover(code, parts)
	if err != nil {
		return nil, fmt.Errorf("Tencent turnover: %w", err)
	}
	// Tencent uses fields 31/32 for absolute change/change percent and
	// fields 33/34 for the session high/low for both A shares and HK shares.
	// Treating 32 as the A-share high silently turns the percentage change
	// into a price and poisons every downstream research prompt.
	highIndex, lowIndex := 33, 34

	info := &models.StockInfo{
		Code:     code,
		Name:     strings.TrimSpace(parts[1]),
		Price:    strings.TrimSpace(parts[3]),
		PreClose: strings.TrimSpace(parts[4]),
		Open:     strings.TrimSpace(parts[5]),
		High:     strings.TrimSpace(parts[highIndex]),
		Low:      strings.TrimSpace(parts[lowIndex]),
		Date:     date,
		Time:     clock,
		Volume:   volume,
		Amount:   amount,
	}
	if strings.HasPrefix(code, "hk") {
		info.Market = "HK"
	} else {
		info.Market = "A"
		info.B1P, info.B1V = strings.TrimSpace(parts[9]), strings.TrimSpace(parts[10])
		info.B2P, info.B2V = strings.TrimSpace(parts[11]), strings.TrimSpace(parts[12])
		info.B3P, info.B3V = strings.TrimSpace(parts[13]), strings.TrimSpace(parts[14])
		info.B4P, info.B4V = strings.TrimSpace(parts[15]), strings.TrimSpace(parts[16])
		info.B5P, info.B5V = strings.TrimSpace(parts[17]), strings.TrimSpace(parts[18])
		info.A1P, info.A1V = strings.TrimSpace(parts[19]), strings.TrimSpace(parts[20])
		info.A2P, info.A2V = strings.TrimSpace(parts[21]), strings.TrimSpace(parts[22])
		info.A3P, info.A3V = strings.TrimSpace(parts[23]), strings.TrimSpace(parts[24])
		info.A4P, info.A4V = strings.TrimSpace(parts[25]), strings.TrimSpace(parts[26])
		info.A5P, info.A5V = strings.TrimSpace(parts[27]), strings.TrimSpace(parts[28])
	}
	if err := validateTencentOHLC(info); err != nil {
		return nil, err
	}
	return info, nil
}

func validateTencentOHLC(info *models.StockInfo) error {
	if info == nil {
		return fmt.Errorf("%w: Tencent quote is nil", ErrInvalidDataFormat)
	}
	parse := func(label, value string) (float64, error) {
		parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed < 0 {
			return 0, fmt.Errorf("%w: Tencent %s %q is invalid", ErrInvalidDataFormat, label, value)
		}
		return parsed, nil
	}
	price, err := parse("price", info.Price)
	if err != nil {
		return err
	}
	open, err := parse("open", info.Open)
	if err != nil {
		return err
	}
	high, err := parse("high", info.High)
	if err != nil {
		return err
	}
	low, err := parse("low", info.Low)
	if err != nil {
		return err
	}
	// Tencent returns zero for both bounds before a first trade or for a
	// suspended symbol.  Once either bound is present, require a coherent
	// traded range so future provider-layout changes fail loudly.
	if high == 0 && low == 0 {
		return nil
	}
	if high == 0 || low == 0 || high < low || (price > 0 && (price > high || price < low)) || (open > 0 && (open > high || open < low)) {
		return fmt.Errorf("%w: Tencent OHLC is inconsistent (open=%s price=%s high=%s low=%s)",
			ErrInvalidDataFormat, info.Open, info.Price, info.High, info.Low)
	}
	return nil
}

func parseTencentQuoteTime(parts []string) (string, string, error) {
	if len(parts) > 30 && strings.Contains(parts[30], "/") {
		fields := strings.Fields(strings.ReplaceAll(strings.TrimSpace(parts[30]), "/", "-"))
		if len(fields) != 2 {
			return "", "", ErrInvalidDataFormat
		}
		return fields[0], fields[1], nil
	}
	timestampIndex := 29
	if len(parts) > 30 && len(strings.TrimSpace(parts[30])) == 14 {
		timestampIndex = 30
	}
	if len(parts) <= timestampIndex {
		return "", "", ErrInvalidDataFormat
	}
	raw := strings.TrimSpace(parts[timestampIndex])
	if len(raw) != 14 {
		return "", "", ErrInvalidDataFormat
	}
	return raw[0:4] + "-" + raw[4:6] + "-" + raw[6:8], raw[8:10] + ":" + raw[10:12] + ":" + raw[12:14], nil
}

func parseTencentTurnover(code string, parts []string) (string, string, error) {
	if strings.HasPrefix(code, "hk") {
		volume, err := strconv.ParseFloat(strings.TrimSpace(parts[36]), 64)
		if err != nil || volume < 0 {
			return "", "", ErrInvalidDataFormat
		}
		amount, err := strconv.ParseFloat(strings.TrimSpace(parts[37]), 64)
		if err != nil || amount < 0 {
			return "", "", ErrInvalidDataFormat
		}
		return strconv.FormatFloat(volume, 'f', -1, 64), strconv.FormatFloat(amount, 'f', 3, 64), nil
	}

	composite := strings.Split(strings.TrimSpace(parts[35]), "/")
	if len(composite) != 3 {
		return "", "", ErrInvalidDataFormat
	}
	volumeLots, err := strconv.ParseFloat(strings.TrimSpace(composite[1]), 64)
	if err != nil || volumeLots < 0 {
		return "", "", ErrInvalidDataFormat
	}
	amount, err := strconv.ParseFloat(strings.TrimSpace(composite[2]), 64)
	if err != nil || amount < 0 {
		return "", "", ErrInvalidDataFormat
	}
	return strconv.FormatFloat(volumeLots*100, 'f', -1, 64), strconv.FormatFloat(amount, 'f', -1, 64), nil
}

func ParseFullSingleStockData(data string) (*models.StockInfo, error) {
	datas := strutil.SplitAndTrim(data, "=", "\"")
	if len(datas) < 2 {
		return nil, ErrInvalidDataFormat
	}
	var result map[string]string
	if strutil.ContainsAny(datas[0], []string{"hq_str_sz", "hq_str_sh", "hq_str_bj", "hq_str_sb"}) {
		result, _ = ParseSHSZStockData(datas)
	}
	if strutil.ContainsAny(datas[0], []string{"hq_str_hk"}) {
		result, _ = ParseHKStockData(datas)
	}
	if strutil.ContainsAny(datas[0], []string{"hq_str_gb"}) {
		result, _ = ParseUSStockData(datas)
	}

	//logger.SugaredLogger.Infof("股票数据解析完成: %v", result)
	marshal, err := json.Marshal(result)
	if err != nil {
		logger.SugaredLogger.Errorf("json.Marshal error:%s", err.Error())
		return nil, err
	}
	//logger.SugaredLogger.Infof("股票数据解析完成marshal: %s", marshal)
	stockInfo := &models.StockInfo{}
	err = json.Unmarshal(marshal, &stockInfo)
	if err != nil {
		logger.SugaredLogger.Errorf("json.Unmarshal error:%s", err.Error())
		return nil, err
	}
	//logger.SugaredLogger.Infof("股票数据解析完成stockInfo: %+v", stockInfo)

	return stockInfo, nil
}

func ParseUSStockData(datas []string) (map[string]string, error) {
	code := strings.Split(datas[0], "hq_str_")[1]
	result := make(map[string]string)
	parts := strutil.SplitAndTrim(datas[1], ",", "\"", ";")
	//parts := strings.Split(data, ",")
	//logger.SugaredLogger.Infof("股票数据解析完成: parts:%d", len(parts))
	if len(parts) < 35 {
		return nil, ErrInvalidDataFormat
	}
	/*
		谷歌,   0
		170.2100, 1 现价
		-2.57, 2 涨跌幅
		2025-02-28 09:38:50, 3 时间
		-4.4900, 4 涨跌额
		175.9400, 5 今日开盘价
		176.5900, 6 区间
		169.7520, 7 区间
		208.7000, 8 52周区间
		130.9500, 9 52周区间
		25930485, 10 成交量
		17083496, 11 10日均量
		2074859900000, 12 市值
		8.13, 13 每股收益
		20.940000 , 14 市盈率
		0.00,  15
		0.00,  16
		0.20,  17
		0.00,	18
		12190000000, 19
		71, 20
		170.2000, 21 盘前盘后盘
		-0.01, 22  盘前盘后涨跌幅
		-0.01, 23
		Feb 27 07:59PM EST, 24
		Feb 27 04:00PM EST, 25
		174.7000, 26 前收盘
		2917444, 27
		1, 28
		2025, 29
		4456143849.0000, 30
		176.1200, 31
		163.7039, 32
		496605933.1411, 33
		170.2100, 34 现价
		174.7000 35 前收盘
	*/
	result["股票代码"] = code
	result["股票名称"] = parts[0]
	result["今日开盘价"] = parts[5]

	if len(parts) >= 36 {
		result["昨日收盘价"] = strutil.ReplaceWithMap(strutil.RemoveNonPrintable(parts[26]), map[string]string{"\"": "", ";": ""})
	} else {
		result["昨日收盘价"] = strutil.ReplaceWithMap(strutil.RemoveNonPrintable(parts[len(parts)-1]), map[string]string{"\"": "", ";": ""})
	}

	result["今日最高价"] = parts[6]
	result["今日最低价"] = parts[7]
	result["当前价格"] = parts[1]
	result["盘前盘后"] = parts[21]
	result["盘前盘后涨跌幅"] = parts[22]
	result["日期"] = strutil.SplitAndTrim(parts[3], " ", "")[0]
	result["时间"] = strutil.SplitAndTrim(parts[3], " ", "")[1]
	//logger.SugaredLogger.Infof("美股股票数据解析完成: %v", result)
	return result, nil
}

func ParseHKStockData(datas []string) (map[string]string, error) {
	code := strings.Split(datas[0], "hq_str_")[1]
	result := make(map[string]string)
	parts := strutil.SplitAndTrim(datas[1], ",", "\"", ";")
	//parts := strings.Split(data, ",")
	if len(parts) < 19 {
		return nil, ErrInvalidDataFormat
	}
	/*
		XIAOMI-W,    0
		小米集团－Ｗ,  1 股票名称
		50.050,		 2 今日开盘价
		49.150,		 3 昨日收盘价
		51.950,      4 今日最高价
		49.700,      5 今日最低价
		51.700,      6 当前价格
		2.550,       7 涨跌额
		5.188,		 8 涨跌幅
		51.65000,    9
		51.70000,    10
		15770408249, 11 成交额
		308362585,   12 成交量
		0.000,       13
		0.000,       14
		51.950,		 15 52周最高
		12.560,		 16 52周最低
		2025/02/21,  17
		16:08        18
	*/
	result["股票代码"] = code
	result["股票名称"] = parts[1]
	result["今日开盘价"] = parts[2]
	result["昨日收盘价"] = parts[3]
	result["今日最高价"] = parts[4]
	result["今日最低价"] = parts[5]
	result["当前价格"] = parts[6]
	result["日期"] = strings.ReplaceAll(parts[17], "/", "-")
	result["时间"] = strings.ReplaceAll(parts[18], "\";", ":00")
	//logger.SugaredLogger.Infof("股票数据解析完成: %v", result)
	return result, nil
}

func ParseSHSZStockData(datas []string) (map[string]string, error) {
	code := strings.Split(datas[0], "hq_str_")[1]
	result := make(map[string]string)
	parts := strutil.SplitAndTrim(datas[1], ",", "\"")
	//parts := strings.Split(data, ",")
	if len(parts) < 32 {
		return nil, ErrInvalidDataFormat
	}
	/*
		0：”大秦铁路”，股票名字；
		1：”27.55″，今日开盘价；
		2：”27.25″，昨日收盘价；
		3：”26.91″，当前价格；
		4：”27.55″，今日最高价；
		5：”26.20″，今日最低价；
		6：”26.91″，竞买价，即“买一”报价；
		7：”26.92″，竞卖价，即“卖一”报价；
		8：”22114263″，成交的股票数，由于股票交易以一百股为基本单位，所以在使用时，通常把该值除以一百；
		9：”589824680″，成交金额，单位为“元”，为了一目了然，通常以“万元”为成交金额的单位，所以通常把该值除以一万；
		10：”4695″，“买一”申报4695股，即47手；
		11：”26.91″，“买一”报价；
		12：”57590″，“买二”
		13：”26.90″，“买二”
		14：”14700″，“买三”
		15：”26.89″，“买三”
		16：”14300″，“买四”
		17：”26.88″，“买四”
		18：”15100″，“买五”
		19：”26.87″，“买五”
		20：”3100″，“卖一”申报3100股，即31手；
		21：”26.92″，“卖一”报价
		(22, 23), (24, 25), (26,27), (28, 29)分别为“卖二”至“卖四的情况”
		30：”2008-01-11″，日期；
		31：”15:05:32″，时间；*/
	result["股票代码"] = code
	result["股票名称"] = parts[0]
	result["今日开盘价"] = parts[1]
	result["昨日收盘价"] = parts[2]
	result["当前价格"] = parts[3]
	result["今日最高价"] = parts[4]
	result["今日最低价"] = parts[5]
	result["竞买价"] = parts[6]
	result["竞卖价"] = parts[7]
	result["成交的股票数"] = parts[8]
	result["成交金额"] = parts[9]
	result["买一申报"] = parts[10]
	result["买一报价"] = parts[11]
	result["买二申报"] = parts[12]
	result["买二报价"] = parts[13]
	result["买三申报"] = parts[14]
	result["买三报价"] = parts[15]
	result["买四申报"] = parts[16]
	result["买四报价"] = parts[17]
	result["买五申报"] = parts[18]
	result["买五报价"] = parts[19]
	result["卖一申报"] = parts[20]
	result["卖一报价"] = parts[21]
	result["卖二申报"] = parts[22]
	result["卖二报价"] = parts[23]
	result["卖三申报"] = parts[24]
	result["卖三报价"] = parts[25]
	result["卖四申报"] = parts[26]
	result["卖四报价"] = parts[27]
	result["卖五申报"] = parts[28]
	result["卖五报价"] = parts[29]
	result["日期"] = parts[30]
	result["时间"] = parts[31]
	return result, nil
}

type RealTimeStockPriceInfo struct {
	StockCode string
	Price     string `json:"当前价格"`
	Time      time.Time
}

func GetRealTimeStockPriceInfo(ctx context.Context, stockCode string) (price, priceTime string) {
	if strutil.HasPrefixAny(stockCode, []string{"SZ", "SH", "sh", "sz"}) {
		crawlerAPI := CrawlerApi{}
		crawlerBaseInfo := CrawlerBaseInfo{
			Name:        "EastmoneyCrawler",
			Description: "EastmoneyCrawler Description",
			BaseUrl:     "https://quote.eastmoney.com/",
			Headers:     map[string]string{"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36 Edg/133.0.0.0"},
		}
		crawlerAPI = crawlerAPI.NewCrawler(ctx, crawlerBaseInfo)
		htmlContent, ok := crawlerAPI.GetHtml(fmt.Sprintf("https://quote.eastmoney.com/%s.html", stockCode), "div.zxj", true)
		if ok {
			price := ""
			priceTime := ""
			document, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
			if err != nil {
				logger.SugaredLogger.Debugf("GetRealTimeStockPriceInfo parse html failed: %v", err)
				return price, priceTime
			}
			document.Find("div.zxj").Each(func(i int, selection *goquery.Selection) {
				price = selection.Text()
				//logger.SugaredLogger.Infof("股票代码: %s, 当前价格: %s", stockCode, price)
			})

			document.Find("span.quote_title_time").Each(func(i int, selection *goquery.Selection) {
				priceTime = selection.Text()
				//logger.SugaredLogger.Infof("股票代码: %s, 当前价格时间: %s", stockCode, priceTime)
			})
			return price, priceTime
		}
	}
	return price, priceTime
}

func SearchStockPriceInfo(stockName, stockCode string, crawlTimeOut int64) *[]string {

	if strutil.HasPrefixAny(stockCode, []string{"SZ", "SH", "sh", "sz", "bj"}) {
		//if strutil.HasPrefixAny(stockCode, []string{"bj", "BJ"}) {
		//	stockCode = strutil.ReplaceWithMap(stockCode, map[string]string{
		//		"bj": "",
		//		"BJ": "",
		//	}) + ".BJ"
		//}

		return getSHSZStockPriceInfo(stockName, stockCode, crawlTimeOut)
	}
	if strutil.HasPrefixAny(stockCode, []string{"HK", "hk"}) {
		return getHKStockPriceInfo(stockCode, crawlTimeOut)
	}
	if strutil.HasPrefixAny(stockCode, []string{"US", "us", "gb_"}) {
		return getUSStockPriceInfo(stockCode, crawlTimeOut)
	}
	return &[]string{}
}

func getUSStockPriceInfo(stockCode string, crawlTimeOut int64) *[]string {
	var messages []string
	crawlerAPI := CrawlerApi{}
	crawlerBaseInfo := CrawlerBaseInfo{
		Name:        "SinaCrawler",
		Description: "SinaCrawler Crawler Description",
		BaseUrl:     "https://stock.finance.sina.com.cn",
		Headers:     map[string]string{"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36 Edg/133.0.0.0"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(crawlTimeOut)*time.Second)
	defer cancel()
	crawlerAPI = crawlerAPI.NewCrawler(ctx, crawlerBaseInfo)

	url := fmt.Sprintf("https://stock.finance.sina.com.cn/usstock/quotes/%s.html", strings.ReplaceAll(stockCode, "gb_", ""))
	htmlContent, ok := crawlerAPI.GetHtml(url, "div#hqPrice", true)
	if !ok {
		return &[]string{}
	}
	document, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		logger.SugaredLogger.Error(err.Error())
	}
	stockName := ""
	stockPrice := ""
	stockPriceTime := ""
	document.Find("div.hq_title >h1").Each(func(i int, selection *goquery.Selection) {
		stockName = strutil.RemoveNonPrintable(selection.Text())
		//logger.SugaredLogger.Infof("股票名称-:%s", stockName)
	})

	document.Find("#hqPrice").Each(func(i int, selection *goquery.Selection) {
		stockPrice = strutil.RemoveNonPrintable(selection.Text())
		//logger.SugaredLogger.Infof("现价: %s", stockPrice)
	})

	document.Find("div.hq_time").Each(func(i int, selection *goquery.Selection) {
		stockPriceTime = strutil.RemoveNonPrintable(selection.Text())
		//logger.SugaredLogger.Infof("时间: %s", stockPriceTime)
	})

	messages = append(messages, fmt.Sprintf("%s:%s现价%s", stockPriceTime, stockName, stockPrice))
	//logger.SugaredLogger.Infof("股票: %s", messages)

	document.Find("div#hqDetails >table tbody tr").Each(func(i int, selection *goquery.Selection) {
		text := strutil.RemoveNonPrintable(selection.Text())
		//logger.SugaredLogger.Infof("股票名称-%s: %s", stockName, text)
		messages = append(messages, text)
	})

	logger.SugaredLogger.Infof("messages: %s", messages)
	return &messages
}

func getHKStockPriceInfo(stockCode string, crawlTimeOut int64) *[]string {
	var messages []string
	crawlerAPI := CrawlerApi{}
	crawlerBaseInfo := CrawlerBaseInfo{
		Name:        "SinaCrawler",
		Description: "SinaCrawler Crawler Description",
		BaseUrl:     "https://stock.finance.sina.com.cn",
		Headers:     map[string]string{"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36 Edg/133.0.0.0"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(crawlTimeOut)*time.Second)
	defer cancel()
	crawlerAPI = crawlerAPI.NewCrawler(ctx, crawlerBaseInfo)

	url := fmt.Sprintf("https://stock.finance.sina.com.cn/hkstock/quotes/%s.html", strings.ReplaceAll(stockCode, "hk", ""))
	logger.SugaredLogger.Infof("CrawlHKStockPriceInfo url:%s", url)
	htmlContent, ok := crawlerAPI.GetHtml(url, "div.deta_hqContainer >.deta03>ul ", false)
	if !ok {
		return &[]string{}
	}
	//logger.SugaredLogger.Infof("CrawlHKStockPriceInfo htmlContent:%s", htmlContent)
	document, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		logger.SugaredLogger.Error(err.Error())
	}
	stockName := ""
	stockPrice := ""
	stockPriceTime := ""
	document.Find("#stock_cname").Each(func(i int, selection *goquery.Selection) {
		stockName = strutil.RemoveNonPrintable(selection.Text())
		//logger.SugaredLogger.Infof("股票名称-:%s", stockName)
	})

	document.Find("#mts_stock_hk_price").Each(func(i int, selection *goquery.Selection) {
		stockPrice = strutil.RemoveNonPrintable(selection.Text())
		//logger.SugaredLogger.Infof("现价: %s", stockPrice)
	})

	document.Find("#mts_stock_hk_time").Each(func(i int, selection *goquery.Selection) {
		stockPriceTime = strutil.RemoveNonPrintable(selection.Text())
		//logger.SugaredLogger.Infof("时间: %s", stockPriceTime)
	})

	messages = append(messages, fmt.Sprintf("%s:%s现价%s", stockPriceTime, stockName, stockPrice))
	//logger.SugaredLogger.Infof("股票: %s", messages)

	document.Find(".deta_hqContainer >.deta03 li").Each(func(i int, selection *goquery.Selection) {
		text := strutil.RemoveNonPrintable(selection.Text())
		//logger.SugaredLogger.Infof("股票名称-%s: %s", stockName, text)
		messages = append(messages, text)
	})

	logger.SugaredLogger.Infof("messages: %s", messages)
	return &messages
}

func getSHSZStockPriceInfo(stockName, stockCode string, crawlTimeOut int64) *[]string {
	url := "https://finance.sina.com.cn/realstock/company/" + stockCode + "/nc.shtml"
	crawlerAPI := CrawlerApi{}
	crawlerBaseInfo := CrawlerBaseInfo{
		Name:        "TestCrawler",
		Description: "Test Crawler Description",
		BaseUrl:     "https://finance.sina.com.cn",
		Headers:     map[string]string{"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36 Edg/133.0.0.0"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(crawlTimeOut)*time.Second)
	defer cancel()
	crawlerAPI = crawlerAPI.NewCrawler(ctx, crawlerBaseInfo)
	html, ok := crawlerAPI.GetHtml(url, "div#hqDetails table", true)
	if !ok {
		return &[]string{""}
	}
	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		logger.SugaredLogger.Error(err.Error())
	}

	//price
	price := strutil.RemoveWhiteSpace(document.Find("div#price").First().Text(), false)
	hqTime := strutil.RemoveWhiteSpace(document.Find("div#hqTime").First().Text(), false)

	var markdown strings.Builder
	markdown.WriteString(fmt.Sprintf("### %s现价：%s 现价时间：%s\n", stockName, price, hqTime))
	GetTableMarkdown(document, "div#hqDetails table", &markdown)
	return &[]string{markdown.String()}
}

// 分时数据
func (receiver StockDataApi) GetStockMinutePriceData(stockCode string) (*[]MinuteData, string) {
	url := fmt.Sprintf("https://web.ifzq.gtimg.cn/appstock/app/minute/query?code=%s", stockCode)
	if strutil.HasPrefixAny(stockCode, []string{"gb_", "GB_"}) {
		stockCode = strings.Replace(strings.ToUpper(stockCode), "GB_", "us", 1) + ".OQ"
	}
	if strutil.HasPrefixAny(stockCode, []string{"us", "US"}) {
		url = fmt.Sprintf("https://web.ifzq.gtimg.cn/appstock/app/UsMinute/query?code=%s", stockCode)
	}
	logger.SugaredLogger.Infof("GetStockMinutePriceData url:%s", url)
	res := make(map[string]interface{})
	resp, err := receiver.client.SetTimeout(time.Duration(receiver.config.CrawlTimeOut)*time.Second).R().
		SetHeader("Host", "web.ifzq.gtimg.cn").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36 Edg/119.0.0.0").
		Get(url)

	date := ""
	minuteDatas := &[]MinuteData{}

	if err != nil {
		logger.SugaredLogger.Errorf("err:%s", err.Error())
		return minuteDatas, date
	}
	//logger.SugaredLogger.Infof("resp:%s", resp.Body())
	if err := json.Unmarshal(resp.Body(), &res); err != nil {
		logger.SugaredLogger.Errorf("GetStockMinutePriceData json.Unmarshal err:%v", err)
		return minuteDatas, date
	}
	code, _ := convertor.ToInt(res["code"])
	if res["data"] != nil && code == 0 {
		data := res["data"].(map[string]interface{})
		if stockData, ok := data[stockCode]; ok {
			m := stockData.(map[string]interface{})
			if d, ok := m["data"]; ok {
				if m2, ok := d.(map[string]any); ok {
					minutePriceData := m2["data"]
					datas := minutePriceData.([]any)
					for _, item := range datas {
						minuteDataSplit := strutil.SplitEx(strutil.ReplaceWithMap(item.(string), map[string]string{
							"\r\n": " ",
						}), " ", true)
						price, _ := convertor.ToFloat(minuteDataSplit[1])
						volume, _ := convertor.ToFloat(minuteDataSplit[2])
						amount := float64(0)
						if len(minuteDataSplit) >= 4 {
							amount, _ = convertor.ToFloat(minuteDataSplit[3])
						}
						minuteData := &MinuteData{
							Time:   minuteDataSplit[0][0:2] + ":" + minuteDataSplit[0][2:4],
							Price:  price,
							Volume: volume,
							Amount: amount,
						}
						*minuteDatas = append(*minuteDatas, *minuteData)
					}
					date = m2["date"].(string)
				}
			}
		}
	}
	return minuteDatas, date
}

func (receiver StockDataApi) GetKLineData(stockCode string, kLineType string, days int64) *[]models.KLineData {
	url := fmt.Sprintf("https://quotes.sina.cn/cn/api/json_v2.php/CN_MarketDataService.getKLineData?symbol=%s&scale=%s&ma=yes&datalen=%d", stockCode, kLineType, days)
	K := &[]models.KLineData{}
	_, err := receiver.client.SetTimeout(time.Duration(receiver.config.CrawlTimeOut)*time.Second).R().
		SetHeader("Host", "quotes.sina.cn").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36 Edg/119.0.0.0").
		SetResult(K).
		Get(url)
	if err != nil {
		logger.SugaredLogger.Errorf("err:%s", err.Error())
		return K
	}
	return K
}
func (receiver StockDataApi) GetHK_KLineData(stockCode string, kLineType string, days int64) *[]models.KLineData {

	logger.SugaredLogger.Infof("GetHK_KLineData stockCode:%s,kLineType:%s,days:%d", stockCode, kLineType, days)
	if strutil.HasPrefixAny(stockCode, []string{"gb_", "GB_"}) {
		stockCode = strings.Replace(stockCode, "gb_", "us", 1) + ".OQ"
	}

	url := fmt.Sprintf("https://web.ifzq.gtimg.cn/appstock/app/fqkline/get?param=%s,%s,,,%d,qfq", stockCode, kLineType, days)
	//logger.SugaredLogger.Infof("url:%s", url)
	K := &[]models.KLineData{}
	res := make(map[string]interface{})
	resp, err := receiver.client.SetTimeout(time.Duration(receiver.config.CrawlTimeOut)*time.Second).R().
		SetHeader("Host", "web.ifzq.gtimg.cn").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36 Edg/119.0.0.0").
		Get(url)
	if err != nil {
		logger.SugaredLogger.Errorf("err:%s", err.Error())
		return K
	}
	//logger.SugaredLogger.Infof("resp:%s", resp.Body())
	if err := json.Unmarshal(resp.Body(), &res); err != nil {
		logger.SugaredLogger.Errorf("GetKLineData json.Unmarshal err:%v", err)
		return K
	}
	code, _ := convertor.ToInt(res["code"])
	if code != 0 {
		return K
	}
	if res["data"] != nil && code == 0 {
		data := res["data"].(map[string]interface{})[stockCode].(map[string]interface{})
		if data != nil {
			var day []any
			if data["qfqday"] != nil {
				day = data["qfqday"].([]any)
			}
			if data["day"] != nil {
				day = data["day"].([]any)
			}
			for _, v := range day {
				if v != nil {
					vv := v.([]any)
					KLine := &models.KLineData{
						Day:    convertor.ToString(vv[0]),
						Open:   convertor.ToString(vv[1]),
						Close:  convertor.ToString(vv[2]),
						High:   convertor.ToString(vv[3]),
						Low:    convertor.ToString(vv[4]),
						Volume: convertor.ToString(vv[5]),
					}
					*K = append(*K, *KLine)
				}
			}
		}
	}
	return K
}
func (receiver StockDataApi) GetCommonKLineData(stockCode string, kLineType string, days int64) *[]models.KLineData {

	url := fmt.Sprintf("https://web.ifzq.gtimg.cn/appstock/app/fqkline/get?param=%s,%s,,,%d,qfq", stockCode, kLineType, days)
	logger.SugaredLogger.Infof("url:%s", url)
	K := &[]models.KLineData{}
	res := make(map[string]interface{})
	resp, err := receiver.client.SetTimeout(time.Duration(receiver.config.CrawlTimeOut)*time.Second).R().
		SetHeader("Host", "web.ifzq.gtimg.cn").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36 Edg/119.0.0.0").
		Get(url)
	if err != nil {
		logger.SugaredLogger.Errorf("err:%s", err.Error())
		return K
	}
	logger.SugaredLogger.Infof("resp:%s", resp.Body())
	if err := json.Unmarshal(resp.Body(), &res); err != nil {
		logger.SugaredLogger.Errorf("GetCommonKLineData json.Unmarshal err:%v", err)
		return K
	}
	code, _ := convertor.ToInt(res["code"])
	if code != 0 {
		return K
	}
	if res["data"] != nil && code == 0 {
		data := res["data"].(map[string]interface{})[stockCode].(map[string]interface{})
		if data != nil {
			var day []any
			if data["qfqday"] != nil {
				day = data["qfqday"].([]any)
			}
			if data["day"] != nil {
				day = data["day"].([]any)
			}
			for _, v := range day {
				if v != nil {
					vv := v.([]any)
					KLine := &models.KLineData{
						Day:    convertor.ToString(vv[0]),
						Open:   convertor.ToString(vv[1]),
						Close:  convertor.ToString(vv[2]),
						High:   convertor.ToString(vv[3]),
						Low:    convertor.ToString(vv[4]),
						Volume: convertor.ToString(vv[5]),
					}
					*K = append(*K, *KLine)
				}
			}
		}
	}
	return K
}

// GetStockMoneyData 获取个股资金流数据
func (receiver StockDataApi) GetStockMoneyData() models.StockMoneyDataResp {
	var resData models.StockMoneyDataResp
	url := "https://push2.eastmoney.com/api/qt/clist/get?cb=data&fid=f62&po=1&pz=50&pn=1&np=1&fltt=2&invt=2&ut=8dec03ba335b81bf4ebdf7b29ec27d15&fs=m:0+t:6+f:!2,m:0+t:13+f:!2,m:0+t:80+f:!2,m:1+t:2+f:!2,m:1+t:23+f:!2,m:0+t:7+f:!2,m:1+t:3+f:!2&fields=f12,f14,f2,f3,f62,f184,f66,f69,f72,f75,f78,f81,f84,f87,f204,f205,f124,f1,f13,f100,f265"
	resp, err := receiver.client.SetTimeout(time.Duration(receiver.config.CrawlTimeOut)*time.Second).R().
		SetHeader("Host", "push2.eastmoney.com").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36 Edg/119.0.0.0").
		Get(url)
	if err != nil {
		logger.SugaredLogger.Errorf("err:%s", err.Error())
	}
	body := string(resp.Body())
	logger.SugaredLogger.Infof("resp:%s", body)
	vm := otto.New()
	if _, err := vm.Run("function data(res){return res};"); err != nil {
		logger.SugaredLogger.Errorf("vm.Run init error:%v", err.Error())
		return models.StockMoneyDataResp{}
	}
	val, err := vm.Run(body)
	if err != nil {
		logger.SugaredLogger.Errorf("err:%s", err.Error())
	}
	value, err := val.Export()
	if err != nil {
		logger.SugaredLogger.Errorf("err:%s", err.Error())
	}
	marshal, err := json.Marshal(value)
	if err != nil {
		logger.SugaredLogger.Errorf("err:%s", err.Error())
		return models.StockMoneyDataResp{}
	}
	err = json.Unmarshal(marshal, &resData)
	if err != nil {
		logger.SugaredLogger.Errorf("err:%s", err.Error())
		return models.StockMoneyDataResp{}
	}
	return resData
}

// 获取股票概念题材信息
func (receiver StockDataApi) GetStockConceptInfo(stockCode string) models.StockConceptInfoResp {
	//601138.SH
	if !strutil.ContainsAny(stockCode, []string{"."}) {
		stockCode = ConvertStockCodeToTushareCode(stockCode)
	}
	url := "https://datacenter.eastmoney.com/securities/api/data/v1/get?reportName=RPT_F10_CORETHEME_BOARDTYPE&columns=SECUCODE%2CSECURITY_CODE%2CSECURITY_NAME_ABBR%2CNEW_BOARD_CODE%2CBOARD_NAME%2CSELECTED_BOARD_REASON%2CIS_PRECISE%2CBOARD_RANK%2CBOARD_YIELD%2CDERIVE_BOARD_CODE&quoteColumns=f3~05~NEW_BOARD_CODE~BOARD_YIELD&filter=(SECUCODE%3D%22" + stockCode + "%22)(IS_PRECISE%3D%221%22)&pageNumber=1&pageSize=&sortTypes=1&sortColumns=BOARD_RANK&source=HSF10&client=PC&v=005634233622011753"
	logger.SugaredLogger.Infof("url:%s", url2.QueryEscape(url))
	var data models.StockConceptInfoResp
	resp, err := receiver.client.SetTimeout(time.Duration(receiver.config.CrawlTimeOut)*time.Second).R().
		SetHeader("Host", "datacenter.eastmoney.com").
		SetHeader("Referer", "https://emweb.securities.eastmoney.com/").
		SetHeader("Origin", "https://emweb.securities.eastmoney.com").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:148.0) Gecko/20100101 Firefox/148.0").
		Get(url)
	if err != nil {
		logger.SugaredLogger.Errorf("err:%s", err.Error())
	}
	err = json.Unmarshal(resp.Body(), &data)
	if err != nil {
		logger.SugaredLogger.Errorf("err:%s", err.Error())
		return models.StockConceptInfoResp{}
	}
	return data
}

func (receiver StockDataApi) GetStockFinancialInfo(stockCode string) *models.StockFinancialInfoResp {

	if !strutil.ContainsAny(stockCode, []string{"."}) {
		stockCode = ConvertStockCodeToTushareCode(stockCode)
	}

	url := "https://datacenter.eastmoney.com/securities/api/data/v1/get?reportName=RPT_F10_FINANCE_DUPONT&columns=SECUCODE%2CSECURITY_CODE%2CSECURITY_NAME_ABBR%2CORG_CODE%2CORG_TYPE%2CREPORT_DATE%2CREPORT_TYPE%2CREPORT_DATE_NAME%2CSECURITY_TYPE_CODE%2CNOTICE_DATE%2CUPDATE_DATE%2CCURRENCY%2CNETPROFIT%2CTOTAL_OPERATE_INCOME%2CTOTAL_ASSETS%2CTOTAL_LIABILITIES%2CTOTAL_CURRENT_ASSETS%2CTOTAL_NONCURRENT_ASSETS%2CPARENT_NETPROFIT%2CSALE_NPR%2CTOTAL_ASSETS_TR%2CJROA%2CPARENT_NETPROFIT_RATIO%2CEQUITY_MULTIPLIER%2CROE%2CDEBT_ASSET_RATIO%2CTOTAL_INCOME%2CTOTAL_COST%2CTOTAL_EXPENSE%2CMONETARYFUNDS%2CTRADE_FINASSET%2CNOTE_RECE%2CACCOUNTS_RECE%2CFINANCE_RECE%2COTHER_RECE%2CINVENTORY%2CCREDITOR_INVEST%2CLONG_EQUITY_INVEST%2CINVEST_REALESTATE%2CFIXED_ASSET%2CCIP%2CUSERIGHT_ASSET%2CINTANGIBLE_ASSET%2CDEVELOP_EXPENSE%2CGOODWILL%2CLONG_PREPAID_EXPENSE%2CDEFER_TAX_ASSET%2CINVEST_INCOME%2CEXCHANGE_INCOME%2CFAIRVALUE_CHANGE_INCOME%2CASSET_DISPOSAL_INCOME%2COPERATE_COST%2CSURRENDER_VALUE%2CNET_COMPENSATE_EXPENSE%2CNET_CONTRACT_RESERVE%2CPOLICY_BONUS_EXPENSE%2COPERATE_TAX_ADD%2CINCOME_TAX%2CASSET_IMPAIRMENT_INCOME%2CCREDIT_IMPAIRMENT_INCOME%2CNONBUSINESS_EXPENSE%2CFINANCE_EXPENSE%2CSALE_EXPENSE%2CMANAGE_EXPENSE%2CRESEARCH_EXPENSE%2CINTEREST_NI%2CFEE_COMMISSION_NI%2CEARNED_PREMIUM%2CBUSINESS_MANAGE_EXPENSE%2COTHER_CREDITOR_INVEST%2COTHER_EQUITY_INVEST%2CLONG_RECE%2CAVAILABLE_SALE_FINASSET%2CHOLD_MATURITY_INVEST%2CFEE_COMMISSION_EXPENSE&quoteColumns=&filter=(SECUCODE%3D%22" + stockCode + "%22)&pageNumber=1&pageSize=12&sortTypes=-1&sortColumns=REPORT_DATE&source=HSF10&client=PC&v=" + convertor.ToString(time.Now().Unix())
	logger.SugaredLogger.Infof("url:%s", url)
	var data models.StockFinancialInfoResp
	resp, err := receiver.client.SetTimeout(time.Duration(receiver.config.CrawlTimeOut)*time.Second).R().
		SetHeader("Host", "datacenter.eastmoney.com").
		SetHeader("Referer", "https://emweb.securities.eastmoney.com/").
		SetHeader("Origin", "https://emweb.securities.eastmoney.com").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:148.0) Gecko/20100101 Firefox/148.0").
		//SetResult(&data).
		Get(url)
	if err != nil {
		logger.SugaredLogger.Errorf("err:%s", err.Error())
	}
	//logger.SugaredLogger.Infof("resp:%s", string(resp.Body()))
	err = json.Unmarshal(resp.Body(), &data)
	if err != nil {
		logger.SugaredLogger.Errorf("err:%s", err.Error())
		return &models.StockFinancialInfoResp{}
	}
	logger.SugaredLogger.Infof("data:%v", data)
	return &data
}

func (receiver StockDataApi) GetStockHolderNum(stockCode string) *models.StockHolderNumResp {
	if !strutil.ContainsAny(stockCode, []string{"."}) {
		stockCode = ConvertStockCodeToTushareCode(stockCode)
	}
	url := "https://datacenter.eastmoney.com/securities/api/data/v1/get?reportName=RPT_F10_EH_HOLDERNUM&columns=SECUCODE%2CSECURITY_CODE%2CEND_DATE%2CHOLDER_TOTAL_NUM%2CTOTAL_NUM_RATIO%2CAVG_FREE_SHARES%2CAVG_FREESHARES_RATIO%2CHOLD_FOCUS%2CPRICE%2CAVG_HOLD_AMT%2CHOLD_RATIO_TOTAL%2CFREEHOLD_RATIO_TOTAL&quoteColumns=&filter=(SECUCODE%3D%22" + stockCode + "%22)&pageNumber=1&pageSize=12&sortTypes=-1&sortColumns=END_DATE&source=HSF10&client=PC&v=" + strconv.Itoa(time.Now().Nanosecond())
	logger.SugaredLogger.Infof("url:%s", url)
	var data models.StockHolderNumResp
	resp, err := receiver.client.SetTimeout(time.Duration(receiver.config.CrawlTimeOut)*time.Second).R().
		SetHeader("Host", "datacenter.eastmoney.com").
		SetHeader("Referer", "https://emweb.securities.eastmoney.com/").
		SetHeader("Origin", "https://emweb.securities.eastmoney.com").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:148.0) Gecko/20100101 Firefox/148.0").
		//SetResult(&data).
		Get(url)
	if err != nil {
		logger.SugaredLogger.Errorf("err:%s", err.Error())
	}
	err = json.Unmarshal(resp.Body(), &data)
	if err != nil {
		logger.SugaredLogger.Errorf("err:%s", err.Error())
		return &models.StockHolderNumResp{}
	}
	return &data
}

// JSONToMarkdownTable 将JSON数据转换为Markdown表格
func JSONToMarkdownTable(jsonData []byte) (string, error) {
	var data []map[string]interface{}
	err := json.Unmarshal(jsonData, &data)
	if err != nil {
		return "", err
	}

	if len(data) == 0 {
		return "", nil
	}

	// 获取表头
	headers := []string{}
	for key := range data[0] {
		headers = append(headers, key)
	}

	// 构建表头行
	headerRow := "|"
	for _, header := range headers {
		headerRow += fmt.Sprintf(" %s |", header)
	}
	headerRow += "\n"

	// 构建分隔行
	separatorRow := "|"
	for range headers {
		separatorRow += " --- |"
	}
	separatorRow += "\n"

	// 构建数据行
	bodyRows := ""
	for _, rowData := range data {
		bodyRow := "|"
		for _, header := range headers {
			value := rowData[header]
			bodyRow += fmt.Sprintf(" %v |", value)
		}
		bodyRows += bodyRow + "\n"
	}

	return headerRow + separatorRow + bodyRows, nil
}

type MinuteData struct {
	Time   string  `json:"time"`
	Price  float64 `json:"price"`
	Volume float64 `json:"volume"`
	Amount float64 `json:"amount"`
}
