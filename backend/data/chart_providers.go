package data

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var (
	tencentChartKLineURL  = "https://web.ifzq.gtimg.cn/appstock/app/fqkline/get"
	sinaChartKLineURL     = "https://quotes.sina.cn/cn/api/json_v2.php/CN_MarketDataService.getKLineData"
	eastmoneyChartLineURL = "https://push2his.eastmoney.com/api/qt/stock/kline/get"
)

type chartProviderResult struct {
	Bars      []ChartBar
	AsOf      time.Time
	SourceRef string
	Err       error
}

type chartBarProvider interface {
	Name() string
	Fetch(context.Context, ChartRequest) chartProviderResult
}

type chartProviderFactory func(ChartRequest) []chartBarProvider

func productionChartProviders(request ChartRequest) []chartBarProvider {
	if request.Period == ChartPeriod1Minute {
		items := enabledChartMinuteProviders(request.To)
		result := make([]chartBarProvider, 0, len(items))
		for _, item := range items {
			result = append(result, minuteChartProviderAdapter{provider: item})
		}
		return result
	}
	result := []chartBarProvider{tencentDailyChartProvider{}}
	if request.Adjustment == ChartAdjustmentNone {
		result = append(result, sinaDailyChartProvider{})
	}
	return append(result, eastmoneyDailyChartProvider{})
}

type minuteChartProviderAdapter struct{ provider chartMinuteProvider }

func (p minuteChartProviderAdapter) Name() string { return p.provider.name }

func (p minuteChartProviderAdapter) Fetch(ctx context.Context, request ChartRequest) chartProviderResult {
	if err := ctx.Err(); err != nil {
		return chartProviderResult{Bars: []ChartBar{}, Err: err}
	}
	bars, source, err := p.provider.fetch(request.Instrument.Code, request.From, request.To)
	result := chartProviderResult{Bars: make([]ChartBar, 0, len(bars)), AsOf: request.To, SourceRef: p.provider.name, Err: err}
	for _, bar := range bars {
		barSource := strings.TrimSpace(bar.Source)
		if barSource == "" {
			barSource = source
		}
		if !minuteBarSourceProvesUnadjusted(barSource) {
			continue
		}
		result.Bars = append(result.Bars, ChartBar{At: bar.TradeTime, Open: bar.Open, High: bar.High, Low: bar.Low,
			Close: bar.Close, Volume: bar.Volume, Amount: bar.Amount, Source: barSource})
	}
	if err == nil && len(bars) > 0 && len(result.Bars) == 0 {
		result.Err = fmt.Errorf("provider returned minute bars without explicit unadjusted provenance")
	}
	return result
}

type tencentDailyChartProvider struct{}

func (tencentDailyChartProvider) Name() string { return "tencent" }

func (tencentDailyChartProvider) Fetch(ctx context.Context, request ChartRequest) chartProviderResult {
	query := url.Values{}
	query.Set("param", fmt.Sprintf("%s,day,,,%d,%s", request.Instrument.Code, chartDailyProviderLimit(request), request.Adjustment))
	endpoint := tencentChartKLineURL + "?" + query.Encode()
	client := newFetchRestyClient().SetTimeout(20 * time.Second)
	response, err := client.R().SetContext(ctx).SetHeader("Referer", "https://gu.qq.com/").Get(endpoint)
	if err != nil {
		return chartProviderResult{Bars: []ChartBar{}, SourceRef: endpoint, Err: err}
	}
	if response.StatusCode() != http.StatusOK {
		return chartProviderResult{Bars: []ChartBar{}, SourceRef: endpoint, Err: fmt.Errorf("tencent chart HTTP %d", response.StatusCode())}
	}
	var payload struct {
		Code int                                   `json:"code"`
		Msg  string                                `json:"msg"`
		Data map[string]map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(response.Body(), &payload); err != nil {
		return chartProviderResult{Bars: []ChartBar{}, SourceRef: endpoint, Err: fmt.Errorf("decode tencent chart: %w", err)}
	}
	if payload.Code != 0 {
		return chartProviderResult{Bars: []ChartBar{}, SourceRef: endpoint, Err: fmt.Errorf("tencent chart code=%d: %s", payload.Code, payload.Msg)}
	}
	data := payload.Data[request.Instrument.Code]
	key := "day"
	if request.Adjustment != ChartAdjustmentNone {
		key = request.Adjustment + "day"
	}
	raw := data[key]
	if len(raw) == 0 && request.Adjustment == ChartAdjustmentNone {
		raw = data["qfqday"]
		if len(raw) > 0 {
			return chartProviderResult{Bars: []ChartBar{}, SourceRef: endpoint, Err: fmt.Errorf("tencent returned adjusted bars for adjustment=none")}
		}
	}
	var rows [][]any
	if len(raw) == 0 {
		return chartProviderResult{Bars: []ChartBar{}, SourceRef: endpoint}
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return chartProviderResult{Bars: []ChartBar{}, SourceRef: endpoint, Err: fmt.Errorf("decode tencent chart rows: %w", err)}
	}
	bars := chartBarsFromAnyRows(rows, request, "tencent:"+request.Adjustment)
	return chartProviderResult{Bars: bars, AsOf: latestChartBarTime(bars), SourceRef: endpoint}
}

type sinaDailyChartProvider struct{}

func (sinaDailyChartProvider) Name() string { return "sina" }

func (sinaDailyChartProvider) Fetch(ctx context.Context, request ChartRequest) chartProviderResult {
	if request.Adjustment != ChartAdjustmentNone {
		return chartProviderResult{Bars: []ChartBar{}, Err: fmt.Errorf("sina daily adjustment provenance is unavailable")}
	}
	query := url.Values{}
	query.Set("symbol", request.Instrument.Code)
	query.Set("scale", "240")
	query.Set("ma", "no")
	query.Set("datalen", strconv.Itoa(chartDailyProviderLimit(request)))
	endpoint := sinaChartKLineURL + "?" + query.Encode()
	client := newFetchRestyClient().SetTimeout(20 * time.Second)
	response, err := client.R().SetContext(ctx).Get(endpoint)
	if err != nil {
		return chartProviderResult{Bars: []ChartBar{}, SourceRef: endpoint, Err: err}
	}
	if response.StatusCode() != http.StatusOK {
		return chartProviderResult{Bars: []ChartBar{}, SourceRef: endpoint, Err: fmt.Errorf("sina chart HTTP %d", response.StatusCode())}
	}
	var rows []sinaMinuteKLineRow
	if err := json.Unmarshal(response.Body(), &rows); err != nil {
		return chartProviderResult{Bars: []ChartBar{}, SourceRef: endpoint, Err: fmt.Errorf("decode sina chart: %w", err)}
	}
	bars := make([]ChartBar, 0, len(rows))
	for _, row := range rows {
		at, err := parseChartDate(row.Day)
		if err != nil || at.Before(request.From) || at.After(request.To) {
			continue
		}
		bars = append(bars, ChartBar{At: at, Open: parseChartFloat(row.Open), High: parseChartFloat(row.High), Low: parseChartFloat(row.Low),
			Close: parseChartFloat(row.Close), Volume: parseChartFloat(row.Volume), Amount: parseChartFloat(row.Amount), Source: "sina:none"})
	}
	return chartProviderResult{Bars: bars, AsOf: latestChartBarTime(bars), SourceRef: endpoint}
}

type eastmoneyDailyChartProvider struct{}

func (eastmoneyDailyChartProvider) Name() string { return "eastmoney" }

func (eastmoneyDailyChartProvider) Fetch(ctx context.Context, request ChartRequest) chartProviderResult {
	secid := "0." + request.Instrument.Code[2:]
	if request.Instrument.Market == "SH" {
		secid = "1." + request.Instrument.Code[2:]
	}
	fqt := map[string]string{ChartAdjustmentNone: "0", ChartAdjustmentQFQ: "1", ChartAdjustmentHFQ: "2"}[request.Adjustment]
	query := url.Values{}
	query.Set("secid", secid)
	query.Set("klt", "101")
	query.Set("fqt", fqt)
	query.Set("beg", request.From.Format("20060102"))
	query.Set("end", request.To.Format("20060102"))
	query.Set("lmt", strconv.Itoa(chartDailyProviderLimit(request)))
	query.Set("fields1", "f1,f2,f3,f4,f5,f6")
	query.Set("fields2", "f51,f52,f53,f54,f55,f56,f57")
	endpoint := eastmoneyChartLineURL + "?" + query.Encode()
	client := newFetchRestyClient().SetTimeout(20 * time.Second)
	response, err := client.R().SetContext(ctx).SetHeader("Referer", "https://quote.eastmoney.com/").Get(endpoint)
	if err != nil {
		return chartProviderResult{Bars: []ChartBar{}, SourceRef: endpoint, Err: err}
	}
	if response.StatusCode() != http.StatusOK {
		return chartProviderResult{Bars: []ChartBar{}, SourceRef: endpoint, Err: fmt.Errorf("eastmoney chart HTTP %d", response.StatusCode())}
	}
	var payload struct {
		Data *struct {
			KLines []string `json:"klines"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body(), &payload); err != nil {
		return chartProviderResult{Bars: []ChartBar{}, SourceRef: endpoint, Err: fmt.Errorf("decode eastmoney chart: %w", err)}
	}
	if payload.Data == nil {
		return chartProviderResult{Bars: []ChartBar{}, SourceRef: endpoint}
	}
	bars := make([]ChartBar, 0, len(payload.Data.KLines))
	for _, line := range payload.Data.KLines {
		fields := strings.Split(line, ",")
		if len(fields) < 7 {
			continue
		}
		at, err := parseChartDate(fields[0])
		if err != nil || at.Before(request.From) || at.After(request.To) {
			continue
		}
		bars = append(bars, ChartBar{At: at, Open: parseChartFloat(fields[1]), Close: parseChartFloat(fields[2]), High: parseChartFloat(fields[3]),
			Low: parseChartFloat(fields[4]), Volume: parseChartFloat(fields[5]), Amount: parseChartFloat(fields[6]), Source: "eastmoney:" + request.Adjustment})
	}
	return chartProviderResult{Bars: bars, AsOf: latestChartBarTime(bars), SourceRef: endpoint}
}

func chartBarsFromAnyRows(rows [][]any, request ChartRequest, source string) []ChartBar {
	bars := make([]ChartBar, 0, len(rows))
	for _, row := range rows {
		if len(row) < 6 {
			continue
		}
		at, err := parseChartDate(fmt.Sprint(row[0]))
		if err != nil || at.Before(request.From) || at.After(request.To) {
			continue
		}
		amount := 0.0
		if len(row) > 6 {
			amount = toFloatAny(row, 6)
		}
		bars = append(bars, ChartBar{At: at, Open: toFloatAny(row, 1), Close: toFloatAny(row, 2), High: toFloatAny(row, 3),
			Low: toFloatAny(row, 4), Volume: toFloatAny(row, 5), Amount: amount, Source: source})
	}
	return bars
}

func parseChartDate(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", time.DateOnly, "20060102"} {
		if value, err := time.ParseInLocation(layout, raw, cnLocation()); err == nil {
			return value, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid chart time %q", raw)
}

func parseChartFloat(raw string) float64 {
	value, _ := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	return value
}

func latestChartBarTime(bars []ChartBar) time.Time {
	var latest time.Time
	for _, bar := range bars {
		if bar.At.After(latest) {
			latest = bar.At
		}
	}
	return latest
}

func chartDailyProviderLimit(request ChartRequest) int {
	limit := maxInt(request.Limit, 300)
	if limit > maxChartBaseDailyBars {
		return maxChartBaseDailyBars
	}
	return limit
}
