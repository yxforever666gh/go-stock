package data

import (
	"fmt"
	"github.com/duke-git/lancet/v2/convertor"
	"github.com/duke-git/lancet/v2/slice"
	"github.com/duke-git/lancet/v2/strutil"
	"github.com/go-resty/resty/v2"
	"go-stock/backend/logger"
	"sort"
	"strings"
	"time"
)

// @Author spark
// @Date 2025/2/17 12:33
// @Desc
//-----------------------------------------------------------------------------------

type TushareApi struct {
	client *resty.Client
	config *SettingConfig
}

type TushareMinuteBar struct {
	TradeTime time.Time
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Vol       float64
	Amount    float64
}

func NewTushareApi(config *SettingConfig) *TushareApi {
	return &TushareApi{
		client: newNoProxyRestyClient(),
		config: config,
	}
}

// GetTradeCalOpenMap returns a map of open trading dates (YYYY-MM-DD -> true).
// Best-effort: caller can fall back to weekday-only when token is missing.
func (receiver TushareApi) GetTradeCalOpenMap(exchange string, startDate, endDate time.Time, crawlTimeOut int64) (map[string]bool, error) {
	if receiver.config == nil || strings.TrimSpace(receiver.config.TushareToken) == "" {
		return nil, fmt.Errorf("tushare token is empty")
	}
	loc := cnLocation()
	startDate = time.Date(startDate.In(loc).Year(), startDate.In(loc).Month(), startDate.In(loc).Day(), 0, 0, 0, 0, loc)
	endDate = time.Date(endDate.In(loc).Year(), endDate.In(loc).Month(), endDate.In(loc).Day(), 0, 0, 0, 0, loc)
	if endDate.Before(startDate) {
		startDate, endDate = endDate, startDate
	}

	start := startDate.Format("20060102")
	end := endDate.Format("20060102")
	fields := "exchange,cal_date,is_open"
	resp := &TushareStockBasicResponse{}

	_, err := receiver.client.SetTimeout(time.Duration(crawlTimeOut)*time.Second).R().
		SetHeader("content-type", "application/json").
		SetBody(&TushareRequest{
			ApiName: "trade_cal",
			Token:   receiver.config.TushareToken,
			Params: map[string]any{
				"exchange":   exchange,
				"start_date": start,
				"end_date":   end,
			},
			Fields: fields,
		}).
		SetResult(resp).
		Post(tushareApiUrl)
	if err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		return nil, fmt.Errorf("tushare trade_cal error: %s", resp.Msg)
	}

	fieldIndex := map[string]int{}
	for idx, field := range resp.Data.Fields {
		fieldIndex[field] = idx
	}
	openMap := map[string]bool{}
	for _, item := range resp.Data.Items {
		isOpen := convertor.ToString(item[fieldIndex["is_open"]])
		if isOpen != "1" {
			continue
		}
		calDate := strings.TrimSpace(convertor.ToString(item[fieldIndex["cal_date"]]))
		t, parseErr := time.ParseInLocation("20060102", calDate, loc)
		if parseErr != nil {
			continue
		}
		openMap[t.Format("2006-01-02")] = true
	}
	if len(openMap) == 0 {
		return nil, fmt.Errorf("empty trade calendar")
	}
	return openMap, nil
}

// GetDaily tushare A股日线行情
func (receiver TushareApi) GetDaily(tsCode, startDate, endDate string, crawlTimeOut int64) string {
	//logger.SugaredLogger.Debugf("tushare daily request: ts_code=%s, start_date=%s, end_date=%s", tsCode, startDate, endDate)
	fields := "ts_code,trade_date,open,high,low,close,pre_close,change,pct_chg,vol,amount"
	resp := &TushareStockBasicResponse{}
	stockType := getStockType(tsCode)
	tsCodeNEW := getTsCode(tsCode)
	//logger.SugaredLogger.Debugf("tushare daily request: %s,tsCode:%s,tsCodeNEW:%s", stockType, tsCode, tsCodeNEW)
	_, err := receiver.client.SetTimeout(time.Duration(crawlTimeOut)*time.Second).R().
		SetHeader("content-type", "application/json").
		SetBody(&TushareRequest{
			ApiName: stockType,
			Token:   receiver.config.TushareToken,
			Params: map[string]any{
				"ts_code":    tsCodeNEW,
				"start_date": startDate,
				"end_date":   endDate,
			},
			Fields: fields}).
		SetResult(resp).
		Post(tushareApiUrl)
	if err != nil {
		logger.SugaredLogger.Error(err)
		return ""
	}
	res := ""
	if resp.Data.Items != nil && len(resp.Data.Items) > 0 {
		fieldsStr := slice.JoinFunc(resp.Data.Fields, ",", func(s string) string {
			return "\"" + convertor.ToString(s) + "\""
		})
		res += fieldsStr + "\n"
		for _, item := range resp.Data.Items {
			//logger.SugaredLogger.Debugf("%s", slice.Join(item, ","))
			t := slice.JoinFunc(item, ",", func(s any) any {
				return "\"" + convertor.ToString(s) + "\""
			})
			res += t + "\n"
		}
	}
	//logger.SugaredLogger.Debugf("tushare response: %s", res)
	return res
}

// GetStockMinuteBars tushare A股分钟线（1分钟）
func (receiver TushareApi) GetStockMinuteBars(tsCode string, startTime, endTime time.Time, crawlTimeOut int64) ([]TushareMinuteBar, error) {
	tsCode = strings.ToUpper(strings.TrimSpace(tsCode))
	if !isAShareTsCode(tsCode) {
		return nil, fmt.Errorf("unsupported ts code for minute bars: %s", tsCode)
	}
	if receiver.config == nil || strings.TrimSpace(receiver.config.TushareToken) == "" {
		return nil, fmt.Errorf("tushare token is empty")
	}
	if !startTime.Before(endTime) {
		return []TushareMinuteBar{}, nil
	}

	fields := "ts_code,trade_time,open,high,low,close,vol,amount"
	resp := &TushareStockBasicResponse{}
	_, err := receiver.client.SetTimeout(time.Duration(crawlTimeOut)*time.Second).R().
		SetHeader("content-type", "application/json").
		SetBody(&TushareRequest{
			ApiName: "stk_mins",
			Token:   receiver.config.TushareToken,
			Params: map[string]any{
				"ts_code":    tsCode,
				"freq":       "1min",
				"start_date": startTime.Format("2006-01-02 15:04:05"),
				"end_date":   endTime.Format("2006-01-02 15:04:05"),
				"adj":        "qfq",
			},
			Fields: fields,
		}).
		SetResult(resp).
		Post(tushareApiUrl)
	if err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		return nil, fmt.Errorf("tushare stk_mins error: %s", resp.Msg)
	}
	if len(resp.Data.Items) == 0 || len(resp.Data.Fields) == 0 {
		return []TushareMinuteBar{}, nil
	}

	fieldIndex := map[string]int{}
	for idx, field := range resp.Data.Fields {
		fieldIndex[field] = idx
	}

	bars := make([]TushareMinuteBar, 0, len(resp.Data.Items))
	for _, item := range resp.Data.Items {
		tradeTime, err := parseTushareMinuteTime(convertor.ToString(item[fieldIndex["trade_time"]]))
		if err != nil {
			continue
		}
		bar := TushareMinuteBar{
			TradeTime: tradeTime,
			Open:      toFloatOrZero(item[fieldIndex["open"]]),
			High:      toFloatOrZero(item[fieldIndex["high"]]),
			Low:       toFloatOrZero(item[fieldIndex["low"]]),
			Close:     toFloatOrZero(item[fieldIndex["close"]]),
			Vol:       toFloatOrZero(item[fieldIndex["vol"]]),
			Amount:    toFloatOrZero(item[fieldIndex["amount"]]),
		}
		bars = append(bars, bar)
	}

	sort.SliceStable(bars, func(i, j int) bool {
		return bars[i].TradeTime.Before(bars[j].TradeTime)
	})
	return bars, nil
}

// GetLatestTradeDate 获取最近交易日（SSE）
func (receiver TushareApi) GetLatestTradeDate(crawlTimeOut int64) (time.Time, error) {
	if receiver.config == nil || strings.TrimSpace(receiver.config.TushareToken) == "" {
		return fallbackLatestTradeDate(), fmt.Errorf("tushare token is empty")
	}

	now := time.Now()
	startDate := now.AddDate(0, 0, -20).Format("20060102")
	endDate := now.Format("20060102")
	fields := "exchange,cal_date,is_open"
	resp := &TushareStockBasicResponse{}

	_, err := receiver.client.SetTimeout(time.Duration(crawlTimeOut)*time.Second).R().
		SetHeader("content-type", "application/json").
		SetBody(&TushareRequest{
			ApiName: "trade_cal",
			Token:   receiver.config.TushareToken,
			Params: map[string]any{
				"exchange":   "SSE",
				"start_date": startDate,
				"end_date":   endDate,
			},
			Fields: fields,
		}).
		SetResult(resp).
		Post(tushareApiUrl)
	if err != nil {
		return fallbackLatestTradeDate(), err
	}
	if resp.Code != 0 {
		return fallbackLatestTradeDate(), fmt.Errorf("tushare trade_cal error: %s", resp.Msg)
	}

	fieldIndex := map[string]int{}
	for idx, field := range resp.Data.Fields {
		fieldIndex[field] = idx
	}
	latest := time.Time{}
	for _, item := range resp.Data.Items {
		isOpen := convertor.ToString(item[fieldIndex["is_open"]])
		if isOpen != "1" {
			continue
		}
		calDate := convertor.ToString(item[fieldIndex["cal_date"]])
		t, err := time.ParseInLocation("20060102", calDate, time.Local)
		if err != nil {
			continue
		}
		if latest.IsZero() || t.After(latest) {
			latest = t
		}
	}
	if latest.IsZero() {
		return fallbackLatestTradeDate(), fmt.Errorf("empty trade calendar")
	}
	return latest, nil
}

func getTsCode(code string) any {
	if strutil.HasPrefixAny(code, []string{"US", "us", "gb_"}) {
		code = strings.Replace(code, "gb_", "", 1)
		code = strings.Replace(code, "us", "", 1)
		return code
	}
	return code
}

func parseTushareMinuteTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"20060102 15:04:05",
		"20060102 15:04",
		"20060102150405",
		"200601021504",
	}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unknown time format: %s", raw)
}

func fallbackLatestTradeDate() time.Time {
	now := time.Now()
	day := now
	// 盘前默认使用上一交易日
	if day.Weekday() >= time.Monday && day.Weekday() <= time.Friday && day.Hour() < 9 {
		day = day.AddDate(0, 0, -1)
	}
	for day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
		day = day.AddDate(0, 0, -1)
	}
	latest := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	return latest
}

func toFloatOrZero(v any) float64 {
	n, err := convertor.ToFloat(v)
	if err != nil {
		return 0
	}
	return n
}

func getStockType(code string) string {
	if strutil.HasSuffixAny(code, []string{"SZ", "SH", "sh", "sz"}) {
		return "daily"
	}
	if strutil.HasSuffixAny(code, []string{"HK", "hk"}) {
		return "hk_daily"
	}
	if strutil.HasPrefixAny(code, []string{"US", "us", "gb_"}) {
		return "us_daily"
	}
	return ""
}

func isAShareTsCode(code string) bool {
	code = strings.ToUpper(strings.TrimSpace(code))
	return strings.HasSuffix(code, ".SH") || strings.HasSuffix(code, ".SZ")
}
