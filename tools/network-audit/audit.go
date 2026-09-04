package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"go-stock/backend/models"

	"github.com/go-resty/resty/v2"
)

type networkAuditReport struct {
	Version          string              `json:"version"`
	StartedAt        string              `json:"startedAt"`
	FinishedAt       string              `json:"finishedAt"`
	DBPath           string              `json:"dbPath"`
	ReportDir        string              `json:"reportDir"`
	Environment      map[string]any      `json:"environment"`
	Settings         map[string]any      `json:"settings"`
	AIConfigs        []map[string]any    `json:"aiConfigs"`
	MinuteWindows    map[string]any      `json:"minuteWindows"`
	Probes           []networkAuditProbe `json:"probes"`
	Summary          networkAuditSummary `json:"summary"`
	FailureNames     []string            `json:"failureNames,omitempty"`
	SkippedNames     []string            `json:"skippedNames,omitempty"`
	ProxyEnvironment map[string]string   `json:"proxyEnvironment,omitempty"`
	Notes            []string            `json:"notes,omitempty"`
}

type networkAuditProbe struct {
	Category   string         `json:"category"`
	Name       string         `json:"name"`
	Endpoint   string         `json:"endpoint,omitempty"`
	Status     string         `json:"status"`
	DurationMS int64          `json:"durationMs"`
	SideEffect bool           `json:"sideEffect"`
	Sample     string         `json:"sample,omitempty"`
	Error      string         `json:"error,omitempty"`
	Detail     string         `json:"detail,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	StartedAt  string         `json:"startedAt"`
	FinishedAt string         `json:"finishedAt"`
}

type networkAuditSummary struct {
	Total   int `json:"total"`
	OK      int `json:"ok"`
	Error   int `json:"error"`
	Skipped int `json:"skipped"`
}

type networkAuditRunner struct {
	report   *networkAuditReport
	provider marketAuditProvider
}

type auditOptions struct {
	DBPath string
}

type searchRow struct {
	Code string
	Name string
}

func marshalPrettyJSON(value any) ([]byte, error) {
	return json.MarshalIndent(value, "", "  ")
}

func extractSearchRows(result map[string]any) []searchRow {
	dataMap, ok := result["data"].(map[string]any)
	if !ok {
		return nil
	}
	var rawRows []any
	for _, key := range []string{"list", "items", "data", "records", "result"} {
		if rows, exists := dataMap[key].([]any); exists {
			rawRows = rows
			break
		}
	}
	rows := make([]searchRow, 0, len(rawRows))
	for _, raw := range rawRows {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		rows = append(rows, searchRow{
			Code: pickSearchString(item, "code", "stockCode", "securityCode", "symbol", "f12"),
			Name: pickSearchString(item, "name", "stockName", "securityName", "f14"),
		})
	}
	return rows
}

func pickSearchString(item map[string]any, keys ...string) string {
	for _, key := range keys {
		value := strings.TrimSpace(fmt.Sprint(item[key]))
		if value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

type auditSkipError struct {
	message string
}

func (e *auditSkipError) Error() string {
	return e.message
}

func skipAudit(format string, args ...any) error {
	return &auditSkipError{message: fmt.Sprintf(format, args...)}
}

func runNetworkAuditWithProvider(provider marketAuditProvider, jsonOut bool, reportDir string, g auditOptions, stdout, stderr io.Writer, proxyEnv map[string]string) error {
	if provider == nil {
		return errors.New("network audit provider is required")
	}
	cfg := provider.Settings()
	if cfg == nil || cfg.Settings == nil {
		return errors.New("未找到 settings 配置，无法执行网络审计")
	}

	runDir, err := prepareNetworkAuditDir(g.DBPath, reportDir)
	if err != nil {
		return err
	}

	startedAt := time.Now()
	report := &networkAuditReport{
		Version:          "network-audit-v2",
		StartedAt:        startedAt.Format(time.DateTime),
		DBPath:           g.DBPath,
		ReportDir:        runDir,
		Environment:      buildNetworkAuditEnvironment(cfg, provider),
		Settings:         buildNetworkAuditSettings(cfg, provider),
		AIConfigs:        buildNetworkAuditAIConfigs(cfg, provider),
		MinuteWindows:    buildMinuteWindowMetadata(),
		Probes:           make([]networkAuditProbe, 0, 96),
		ProxyEnvironment: proxyEnv,
	}
	runner := &networkAuditRunner{report: report, provider: provider}
	runner.runAll(cfg)
	report.FinishedAt = time.Now().Format(time.DateTime)

	jsonPath := filepath.Join(runDir, "report.json")
	mdPath := filepath.Join(runDir, "report.md")
	if err := writeNetworkAuditReportFiles(report, jsonPath, mdPath); err != nil {
		return err
	}

	if jsonOut {
		body, err := marshalPrettyJSON(report)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(stdout, string(body))
		return nil
	}

	_, _ = fmt.Fprintf(stdout, "网络审计完成: OK %d / 失败 %d / 跳过 %d / 总计 %d\n", report.Summary.OK, report.Summary.Error, report.Summary.Skipped, report.Summary.Total)
	_, _ = fmt.Fprintf(stdout, "JSON 报告: %s\n", jsonPath)
	_, _ = fmt.Fprintf(stdout, "Markdown 报告: %s\n", mdPath)
	if report.Summary.Error > 0 {
		_, _ = fmt.Fprintln(stderr, "失败项:")
		for _, item := range report.FailureNames {
			_, _ = fmt.Fprintln(stderr, " - "+item)
		}
	}
	return nil
}

func (r *networkAuditRunner) runAll(cfg *models.SettingConfig) {
	newsAPI := r.provider.News()
	stockAPI := r.provider.Stock()
	fundAPI := r.provider.Fund()
	crawlTimeout := auditDurationSeconds(cfg.CrawlTimeOut, 20)

	searchFingerprint := strings.TrimSpace(cfg.QgqpBId)
	if searchFingerprint == "" {
		r.report.Notes = append(r.report.Notes, "东财 fingerprint 未配置，部分搜索接口将跳过")
	}
	searchAPI := r.provider.Search("机器人", searchFingerprint)
	searchETFAPI := r.provider.Search("芯片", searchFingerprint)
	latestTradeDate, tushareErr := r.provider.Tushare(cfg).GetLatestTradeDate(cfg.CrawlTimeOut)
	if tushareErr != nil {
		r.report.Notes = append(r.report.Notes, "Tushare 最近交易日探测失败，将使用工作日回退窗口")
	}
	minuteWindow := recentTradingWindow(latestTradeDate)
	todayWindow, todayWindowErr := recentIntradayWindow()
	if todayWindowErr != nil {
		r.report.Notes = append(r.report.Notes, todayWindowErr.Error())
	}

	r.runProbe("market", "cls_telegraph_api", "https://www.cls.cn/nodeapi/telegraphList", false, func() (map[string]any, string, error) {
		items := newsAPI.TelegraphList(15)
		if items == nil || len(*items) == 0 {
			return nil, "", errors.New("财联社电报接口返回空数据")
		}
		first := (*items)[0]
		meta := map[string]any{"count": len(*items), "source": first.Source}
		for key, value := range r.provider.MarketNewsFetchMeta("cls_telegraph_api") {
			meta[key] = value
		}
		return meta, trimSample(first.Content), nil
	})
	r.runProbe("market", "cls_telegraph_web", "https://www.cls.cn/telegraph", false, func() (map[string]any, string, error) {
		items := newsAPI.GetNewTelegraph(15)
		if items == nil || len(*items) == 0 {
			return nil, "", errors.New("财联社网页抓取返回空数据")
		}
		first := (*items)[0]
		meta := map[string]any{"count": len(*items), "source": first.Source}
		for key, value := range r.provider.MarketNewsFetchMeta("cls_telegraph_web") {
			meta[key] = value
		}
		return meta, trimSample(first.Content), nil
	})
	r.runProbe("market", "sina_live_news", "https://zhibo.sina.com.cn/api/zhibo/feed", false, func() (map[string]any, string, error) {
		items := newsAPI.GetSinaNews(15)
		if items == nil || len(*items) == 0 {
			return nil, "", errors.New("新浪财经直播接口返回空数据")
		}
		first := (*items)[0]
		meta := map[string]any{"count": len(*items)}
		for key, value := range r.provider.MarketNewsFetchMeta("sina_live_news") {
			meta[key] = value
		}
		return meta, trimSample(first.Content), nil
	})
	r.runProbe("market", "qq_industry_rank", "https://proxy.finance.qq.com/ifzqgtimg/appstock/app/mktHs/rank", false, func() (map[string]any, string, error) {
		res := newsAPI.GetIndustryRank("desc", 10)
		rows := anySliceFromMap(res, "data")
		if len(rows) == 0 {
			return map[string]any{"raw": res}, "", errors.New("行业排名接口返回空数据")
		}
		return map[string]any{"count": len(rows)}, trimSample(stringifyAny(rows[0])), nil
	})
	r.runProbe("market", "sina_industry_money_rank", "https://vip.stock.finance.sina.com.cn/quotes_service/api/json_v2.php/MoneyFlow.ssl_bkzj_bk", false, func() (map[string]any, string, error) {
		rows := newsAPI.GetIndustryMoneyRankSina("gn", "netamount")
		if len(rows) == 0 {
			return nil, "", errors.New("行业资金流接口返回空数据")
		}
		return map[string]any{"count": len(rows)}, trimSample(stringifyAny(rows[0])), nil
	})
	r.runProbe("market", "sina_stock_money_rank", "https://vip.stock.finance.sina.com.cn/quotes_service/api/json_v2.php/MoneyFlow.ssl_bkzj_ssggzj", false, func() (map[string]any, string, error) {
		rows := newsAPI.GetMoneyRankSina("netamount")
		if len(rows) == 0 {
			return nil, "", errors.New("个股资金流排行返回空数据")
		}
		return map[string]any{"count": len(rows)}, trimSample(stringifyAny(rows[0])), nil
	})
	r.runProbe("market", "sina_stock_money_trend", "http://vip.stock.finance.sina.com.cn/quotes_service/api/json_v2.php/MoneyFlow.ssl_qsfx_zjlrqs", false, func() (map[string]any, string, error) {
		rows := newsAPI.GetStockMoneyTrendByDay("sz000001", 5)
		if len(rows) == 0 {
			return nil, "", errors.New("个股资金流趋势返回空数据")
		}
		return map[string]any{"count": len(rows), "stockCode": "sz000001"}, trimSample(stringifyAny(rows[0])), nil
	})
	r.runProbe("market", "xueqiu_hot_event", "https://xueqiu.com/hot_event/list.json", false, func() (map[string]any, string, error) {
		items := newsAPI.HotEvent(5)
		if items == nil || len(*items) == 0 {
			return nil, "", errors.New("雪球热门事件返回空数据")
		}
		first := (*items)[0]
		return map[string]any{"count": len(*items)}, trimSample(first.Content), nil
	})
	r.runProbe("market", "eastmoney_hot_topic", "https://gubatopic.eastmoney.com/interface/GetData.aspx", false, func() (map[string]any, string, error) {
		items := newsAPI.HotTopic(5)
		if len(items) == 0 {
			return nil, "", errors.New("东财热门话题返回空数据")
		}
		return map[string]any{"count": len(items)}, trimSample(stringifyAny(items[0])), nil
	})
	r.runProbe("market", "cls_calendar", "https://www.cls.cn/api/calendar/web/list", false, func() (map[string]any, string, error) {
		items := newsAPI.ClsCalendar()
		if len(items) == 0 {
			return nil, "", errors.New("财联社日历返回空数据")
		}
		return map[string]any{"count": len(items)}, trimSample(stringifyAny(items[0])), nil
	})
	r.runProbe("market", "eastmoney_gdp", "https://datacenter-web.eastmoney.com/api/data/v1/get?reportName=RPT_ECONOMY_GDP", false, func() (map[string]any, string, error) {
		res := newsAPI.GetGDP()
		if res == nil || len(res.GDPResult.Data) == 0 {
			return nil, "", errors.New("GDP 接口返回空数据")
		}
		return map[string]any{"count": len(res.GDPResult.Data)}, trimSample(res.GDPResult.Data[0].REPORTDATE), nil
	})
	r.runProbe("market", "eastmoney_cpi", "https://datacenter-web.eastmoney.com/api/data/v1/get?reportName=RPT_ECONOMY_CPI", false, func() (map[string]any, string, error) {
		res := newsAPI.GetCPI()
		if res == nil || len(res.CPIResult.Data) == 0 {
			return nil, "", errors.New("CPI 接口返回空数据")
		}
		return map[string]any{"count": len(res.CPIResult.Data)}, trimSample(res.CPIResult.Data[0].REPORTDATE), nil
	})
	r.runProbe("market", "eastmoney_ppi", "https://datacenter-web.eastmoney.com/api/data/v1/get?reportName=RPT_ECONOMY_PPI", false, func() (map[string]any, string, error) {
		res := newsAPI.GetPPI()
		if res == nil || len(res.PPIResult.Data) == 0 {
			return nil, "", errors.New("PPI 接口返回空数据")
		}
		return map[string]any{"count": len(res.PPIResult.Data)}, trimSample(res.PPIResult.Data[0].REPORTDATE), nil
	})
	r.runProbe("market", "eastmoney_pmi", "https://datacenter-web.eastmoney.com/api/data/v1/get?reportName=RPT_ECONOMY_PMI", false, func() (map[string]any, string, error) {
		res := newsAPI.GetPMI()
		if res == nil || len(res.PMIResult.Data) == 0 {
			return nil, "", errors.New("PMI 接口返回空数据")
		}
		return map[string]any{"count": len(res.PMIResult.Data)}, trimSample(res.PMIResult.Data[0].REPORTDATE), nil
	})
	r.runProbe("market", "eastmoney_industry_report_list", "https://reportapi.eastmoney.com/report/list", false, func() (map[string]any, string, error) {
		items := newsAPI.IndustryResearchReport("", 7)
		if len(items) == 0 {
			return nil, "", errors.New("行业研报列表返回空数据")
		}
		firstMap := anyToStringMap(items[0])
		return map[string]any{"count": len(items), "infoCode": pickMapString(firstMap, "infoCode", "info_code", "INFO_CODE")}, trimSample(stringifyAny(items[0])), nil
	})
	r.runProbe("market", "eastmoney_industry_report_detail", "https://data.eastmoney.com/report/zw_industry.jshtml", false, func() (map[string]any, string, error) {
		items := newsAPI.IndustryResearchReport("", 7)
		if len(items) == 0 {
			return nil, "", errors.New("无法获取行业研报详情，因为列表为空")
		}
		infoCode := pickMapString(anyToStringMap(items[0]), "infoCode", "info_code", "INFO_CODE")
		if infoCode == "" {
			return nil, "", errors.New("行业研报列表未返回 infoCode")
		}
		content := strings.TrimSpace(newsAPI.GetIndustryReportInfo(infoCode))
		if content == "" {
			return map[string]any{"infoCode": infoCode}, "", errors.New("行业研报详情内容为空")
		}
		return map[string]any{"infoCode": infoCode, "chars": len(content)}, trimSample(content), nil
	})
	r.runProbe("market", "cninfo_interactive_answer", "https://irm.cninfo.com.cn/newircs/index/search", false, func() (map[string]any, string, error) {
		res := newsAPI.InteractiveAnswer(1, 5, "机器人")
		if res == nil || len(res.Results) == 0 {
			return nil, "", errors.New("互动易接口返回空数据")
		}
		first := res.Results[0]
		return map[string]any{"count": len(res.Results), "stockCode": first.StockCode}, trimSample(first.MainContent), nil
	})
	r.runProbe("market", "tradingview_news", "https://news-mediator.tradingview.com/news-flow/v2/news", false, func() (map[string]any, string, error) {
		items := newsAPI.TradingViewNews()
		if items == nil || len(*items) == 0 {
			return nil, "", errors.New("TradingView 新闻返回空数据")
		}
		first := (*items)[0]
		return map[string]any{"count": len(*items), "source": first.Source}, trimSample(first.Title), nil
	})
	r.runProbe("market", "reuters_news", "https://www.reuters.com/pf/api/v3/content/fetch/recent-stories-by-sections-v1", false, func() (map[string]any, string, error) {
		res := newsAPI.ReutersNew()
		if res == nil || len(res.Result.Articles) == 0 {
			return nil, "", errors.New("Reuters 接口返回空数据")
		}
		first := res.Result.Articles[0]
		return map[string]any{"count": len(res.Result.Articles), "statusCode": res.StatusCode}, trimSample(first.Title), nil
	})
	r.runProbe("market", "xueqiu_hot_stock", "https://stock.xueqiu.com/v5/stock/hot_stock/list.json", false, func() (map[string]any, string, error) {
		items := newsAPI.XUEQIUHotStock(5, "10")
		if items == nil || len(*items) == 0 {
			return nil, "", errors.New("雪球热股返回空数据")
		}
		first := (*items)[0]
		return map[string]any{"count": len(*items), "code": first.Code}, trimSample(first.Name), nil
	})
	r.runProbe("market", "qq_global_stock_indexes", "https://proxy.finance.qq.com/ifzqgtimg/appstock/app/rank/indexRankDetail2", false, func() (map[string]any, string, error) {
		res := newsAPI.GlobalStockIndexes(15)
		count, sample := summarizeAnySliceMap(res, "common", "america", "europe", "asia", "other")
		if count == 0 {
			return map[string]any{"raw": res}, "", errors.New("全球指数接口返回空数据")
		}
		return map[string]any{"count": count}, trimSample(sample), nil
	})
	r.runProbe("market", "jiuyangongshe_invest_calendar", "https://app.jiuyangongshe.com/jystock-app/api/v1/timeline/list", false, func() (map[string]any, string, error) {
		items := newsAPI.InvestCalendar(time.Now().In(cnLocation()).Format("2006-01"))
		if len(items) == 0 {
			return nil, "", errors.New("韭研公社投资日历返回空数据")
		}
		return map[string]any{"count": len(items)}, trimSample(stringifyAny(items[0])), nil
	})
	r.runProbe("market", "eastmoney_long_tiger_rank", "https://datacenter-web.eastmoney.com/api/data/v1/get?reportName=RPT_DAILYBILLBOARD_DETAILSNEW", false, func() (map[string]any, string, error) {
		tradeDate := minuteWindow.Start.Format("2006-01-02")
		items := newsAPI.LongTiger(tradeDate)
		if items == nil || len(*items) == 0 {
			return map[string]any{"tradeDate": tradeDate}, "", errors.New("龙虎榜接口返回空数据")
		}
		first := (*items)[0]
		return map[string]any{"count": len(*items), "tradeDate": tradeDate, "code": first.SECUCODE}, trimSample(first.SECURITYNAMEABBR), nil
	})
	r.runProbe("market", "eastmoney_stock_research_report_list", "https://reportapi.eastmoney.com/report/list2", false, func() (map[string]any, string, error) {
		items := newsAPI.StockResearchReport("600519.SH", 7)
		if len(items) == 0 {
			return nil, "", errors.New("个股研报列表返回空数据")
		}
		firstMap := anyToStringMap(items[0])
		return map[string]any{"count": len(items), "infoCode": pickMapString(firstMap, "infoCode", "info_code", "INFO_CODE")}, trimSample(stringifyAny(items[0])), nil
	})
	r.runProbe("market", "eastmoney_stock_notice", "https://np-anotice-stock.eastmoney.com/api/security/ann", false, func() (map[string]any, string, error) {
		items := newsAPI.StockNotice("600519.SH")
		if len(items) == 0 {
			return nil, "", errors.New("公告列表返回空数据")
		}
		firstMap := anyToStringMap(items[0])
		return map[string]any{"count": len(items), "artCode": pickMapString(firstMap, "art_code", "artCode", "artcode")}, trimSample(stringifyAny(items[0])), nil
	})
	r.runProbe("market", "eastmoney_bk_dict", "https://reportapi.eastmoney.com/report/bk", false, func() (map[string]any, string, error) {
		items := newsAPI.EMDictCode("016")
		if len(items) == 0 {
			return nil, "", errors.New("板块字典接口返回空数据")
		}
		return map[string]any{"count": len(items), "code": "016"}, trimSample(stringifyAny(items[0])), nil
	})
	r.runProbe("market", "tradingview_news_detail", "https://news-headlines.tradingview.com/v3/story", false, func() (map[string]any, string, error) {
		items := newsAPI.TradingViewNews()
		if items == nil || len(*items) == 0 {
			return nil, "", errors.New("TradingView 新闻列表为空，无法探测详情")
		}
		first := (*items)[0]
		id := tradingViewStoryID(first.Url)
		if id == "" {
			return map[string]any{"url": first.Url}, "", errors.New("TradingView 新闻未返回详情 ID")
		}
		detail := newsAPI.TradingViewNewsDetail(id)
		if detail == nil || strings.TrimSpace(detail.Title) == "" {
			return map[string]any{"id": id, "url": first.Url}, "", errors.New("TradingView 新闻详情返回空数据")
		}
		return map[string]any{"id": id, "title": detail.Title}, trimSample(detail.ShortDescription), nil
	})
	r.runProbe("market", "cls_search_api", "https://www.cls.cn/api/csw", false, func() (map[string]any, string, error) {
		res := newsAPI.CailianpressWeb("机器人")
		if res == nil || len(res.List) == 0 {
			return nil, "", errors.New("财联社搜索接口返回空数据")
		}
		first := res.List[0]
		return map[string]any{"count": len(res.List)}, trimSample(first.Title), nil
	})
	r.runProbe("market", "cls_home_top_news", "https://www.cls.cn", false, func() (map[string]any, string, error) {
		rows := derefStringSlice(r.provider.TopNewsList(15))
		if len(rows) == 0 {
			return nil, "", errors.New("财联社首页要闻返回空数据")
		}
		return map[string]any{"count": len(rows)}, trimSample(firstNonEmptyString(rows)), nil
	})

	r.runProbe("search", "eastmoney_search_stock", "https://np-tjxg-g.eastmoney.com/api/smart-tag/stock/v3/pw/search-code", false, func() (map[string]any, string, error) {
		if searchFingerprint == "" {
			return nil, "", skipAudit("qgqp_b_id 未配置")
		}
		res := searchAPI.SearchStock(5)
		rows := extractSearchRows(res)
		if len(rows) == 0 {
			return map[string]any{"rawCode": stringifyAny(res["code"]), "rawMessage": stringifyAny(res["message"])}, "", errors.New("选股搜索返回空数据")
		}
		return map[string]any{"count": len(rows), "keyword": "机器人"}, trimSample(rows[0].Code + " " + rows[0].Name), nil
	})
	r.runProbe("search", "eastmoney_search_bk", "https://np-tjxg-b.eastmoney.com/api/smart-tag/bkc/v3/pw/search-code", false, func() (map[string]any, string, error) {
		if searchFingerprint == "" {
			return nil, "", skipAudit("qgqp_b_id 未配置")
		}
		res := searchAPI.SearchBk(5)
		rows := extractSearchRows(res)
		if len(rows) == 0 {
			return map[string]any{"rawCode": stringifyAny(res["code"]), "rawMessage": stringifyAny(res["message"])}, "", errors.New("板块搜索返回空数据")
		}
		return map[string]any{"count": len(rows), "keyword": "机器人"}, trimSample(rows[0].Code + " " + rows[0].Name), nil
	})
	r.runProbe("search", "eastmoney_search_etf", "https://np-tjxg-b.eastmoney.com/api/smart-tag/etf/v3/pw/search-code", false, func() (map[string]any, string, error) {
		if searchFingerprint == "" {
			return nil, "", skipAudit("qgqp_b_id 未配置")
		}
		res := searchETFAPI.SearchETF(5)
		rows := extractSearchRows(res)
		if len(rows) == 0 {
			return map[string]any{"rawCode": stringifyAny(res["code"]), "rawMessage": stringifyAny(res["message"])}, "", errors.New("ETF 搜索返回空数据")
		}
		return map[string]any{"count": len(rows), "keyword": "芯片"}, trimSample(rows[0].Code + " " + rows[0].Name), nil
	})
	r.runProbe("fund", "eastmoney_fund_list", "https://fund.eastmoney.com", false, func() (map[string]any, string, error) {
		return probeHTTPResource("https://fund.eastmoney.com/allfund.html", httpProbeOptions{
			Timeout:              crawlTimeout,
			Headers:              browserLikeHeaders("https://fund.eastmoney.com/"),
			MinBytes:             1024,
			MustContainAny:       []string{"allfund", "num_right"},
			ExpectedContentTypes: []string{"text/html"},
		})
	})
	r.runProbe("fund", "eastmoney_fund_basic_page", "http://fund.eastmoney.com/000001.html", false, func() (map[string]any, string, error) {
		fund, err := fundAPI.CrawlFundBasic("000001")
		if err != nil {
			return nil, "", err
		}
		if fund == nil || strings.TrimSpace(fund.Name) == "" {
			return nil, "", errors.New("基金详情页返回空数据")
		}
		return map[string]any{"code": fund.Code, "company": fund.Company, "type": fund.Type}, trimSample(fund.Name), nil
	})
	r.runProbe("fund", "fund_estimated_unit_js", "https://fundgz.1234567.com.cn/js/000001.js", false, func() (map[string]any, string, error) {
		return probeHTTPResource("https://fundgz.1234567.com.cn/js/000001.js?rt="+fmt.Sprintf("%d", time.Now().UnixMilli()), httpProbeOptions{
			Timeout:              crawlTimeout,
			Headers:              browserLikeHeaders("https://fund.eastmoney.com/"),
			MinBytes:             32,
			MustContainAny:       []string{"jsonpgz(", "\"fundcode\":\"000001\""},
			ExpectedContentTypes: []string{"javascript", "text/plain", "application/octet-stream"},
		})
	})
	r.runProbe("fund", "sina_fund_realtime_quote", "http://hq.sinajs.cn", false, func() (map[string]any, string, error) {
		return probeHTTPResource(fmt.Sprintf("http://hq.sinajs.cn/rn=%d&list=f_000001", time.Now().UnixMilli()), httpProbeOptions{
			Timeout:        crawlTimeout,
			Headers:        browserLikeHeaders("https://finance.sina.com.cn"),
			MinBytes:       32,
			MustContainAny: []string{"hq_str_f_000001"},
		})
	})

	r.runProbe("stock", "realtime_quote", "http://qt.gtimg.cn", false, func() (map[string]any, string, error) {
		items, err := stockAPI.GetStockCodeRealTimeData("sh600519")
		if err != nil {
			return nil, "", err
		}
		if items == nil || len(*items) == 0 {
			return nil, "", errors.New("实时行情返回空数据")
		}
		first := (*items)[0]
		return map[string]any{"count": len(*items), "code": first.Code, "price": first.Price, "provider": "tencent"}, trimSample(first.Name), nil
	})
	r.runProbe("stock", "minute_quote_tencent_legacy", "https://web.ifzq.gtimg.cn/appstock/app/minute/query", false, func() (map[string]any, string, error) {
		items, date := stockAPI.GetStockMinutePriceData("sz000001")
		if items == nil || len(*items) == 0 {
			return map[string]any{"tradeDate": date}, "", errors.New("传统分时接口返回空数据")
		}
		last := (*items)[len(*items)-1]
		return map[string]any{"count": len(*items), "tradeDate": date, "lastTime": last.Time}, trimSample(stringifyAny(last)), nil
	})
	r.runProbe("stock", "sina_kline", "http://quotes.sina.cn/cn/api/json_v2.php/CN_MarketDataService.getKLineData", false, func() (map[string]any, string, error) {
		items := stockAPI.GetKLineData("sh600519", "240", 5)
		if items == nil || len(*items) == 0 {
			return nil, "", errors.New("Sina K 线返回空数据")
		}
		last := (*items)[len(*items)-1]
		return map[string]any{"count": len(*items), "lastDay": last.Day}, trimSample(stringifyAny(last)), nil
	})
	r.runProbe("stock", "tencent_common_kline", "https://web.ifzq.gtimg.cn/appstock/app/fqkline/get", false, func() (map[string]any, string, error) {
		items := stockAPI.GetCommonKLineData("sh600519", "day", 5)
		if items == nil || len(*items) == 0 {
			return nil, "", errors.New("腾讯 K 线返回空数据")
		}
		last := (*items)[len(*items)-1]
		return map[string]any{"count": len(*items), "lastDay": last.Day}, trimSample(stringifyAny(last)), nil
	})
	r.runProbe("stock", "eastmoney_stock_money_data", "https://push2.eastmoney.com/api/qt/clist/get", false, func() (map[string]any, string, error) {
		res := stockAPI.GetStockMoneyData()
		if len(res.Data.Diff) == 0 {
			return nil, "", errors.New("东财资金流数据返回空数据")
		}
		first := res.Data.Diff[0]
		return map[string]any{"count": len(res.Data.Diff), "total": res.Data.Total}, trimSample(first.F14), nil
	})
	r.runProbe("stock", "eastmoney_stock_concept", "https://datacenter.eastmoney.com/securities/api/data/v1/get?reportName=RPT_F10_CORETHEME_BOARDTYPE", false, func() (map[string]any, string, error) {
		res := stockAPI.GetStockConceptInfo("600519.SH")
		if len(res.Result.Data) == 0 {
			return nil, "", errors.New("概念题材接口返回空数据")
		}
		first := res.Result.Data[0]
		return map[string]any{"count": len(res.Result.Data), "code": first.SECURITYCODE}, trimSample(first.BOARDNAME), nil
	})
	r.runProbe("stock", "eastmoney_stock_financial", "https://datacenter.eastmoney.com/securities/api/data/v1/get?reportName=RPT_F10_FINANCE_DUPONT", false, func() (map[string]any, string, error) {
		res := stockAPI.GetStockFinancialInfo("600519.SH")
		if res == nil || len(res.Result.Data) == 0 {
			return nil, "", errors.New("财务指标接口返回空数据")
		}
		first := res.Result.Data[0]
		return map[string]any{"count": len(res.Result.Data), "code": first.SECURITYCODE}, trimSample(first.REPORTDATE), nil
	})
	r.runProbe("stock", "eastmoney_stock_holder_num", "https://datacenter.eastmoney.com/securities/api/data/v1/get?reportName=RPT_F10_EH_HOLDERNUM", false, func() (map[string]any, string, error) {
		res := stockAPI.GetStockHolderNum("600519.SH")
		if res == nil || len(res.Result.Data) == 0 {
			return nil, "", errors.New("股东户数接口返回空数据")
		}
		first := res.Result.Data[0]
		return map[string]any{"count": len(res.Result.Data), "code": first.SECURITYCODE}, trimSample(first.ENDDATE), nil
	})
	r.runProbe("stock", "eastmoney_quote_page", "https://quote.eastmoney.com/sh600519.html", false, func() (map[string]any, string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), crawlTimeout)
		defer cancel()
		price, priceTime := r.provider.RealTimeStockPriceInfo(ctx, "sh600519")
		if strings.TrimSpace(price) == "" {
			return nil, "", errors.New("东财行情页返回空数据")
		}
		return map[string]any{"code": "sh600519", "priceTime": priceTime}, trimSample(price), nil
	})
	r.runProbe("stock", "sina_quote_page_sh", "https://finance.sina.com.cn/realstock/company/sh600519/nc.shtml", false, func() (map[string]any, string, error) {
		rows := derefStringSlice(r.provider.SearchStockPriceInfo("贵州茅台", "sh600519", int64(crawlTimeout.Seconds())))
		if len(rows) == 0 {
			return nil, "", errors.New("新浪 A 股行情页返回空数据")
		}
		return map[string]any{"count": len(rows), "code": "sh600519"}, trimSample(firstNonEmptyString(rows)), nil
	})
	r.runProbe("stock", "sina_quote_page_hk", "https://stock.finance.sina.com.cn/hkstock/quotes/00700.html", false, func() (map[string]any, string, error) {
		rows := derefStringSlice(r.provider.SearchStockPriceInfo("腾讯控股", "hk00700", int64(crawlTimeout.Seconds())))
		if len(rows) == 0 {
			return nil, "", errors.New("新浪港股行情页返回空数据")
		}
		return map[string]any{"count": len(rows), "code": "hk00700"}, trimSample(firstNonEmptyString(rows)), nil
	})
	r.runProbe("stock", "sina_quote_page_us", "https://stock.finance.sina.com.cn/usstock/quotes/aapl.html", false, func() (map[string]any, string, error) {
		rows := derefStringSlice(r.provider.SearchStockPriceInfo("苹果公司", "gb_aapl", int64(crawlTimeout.Seconds())))
		if len(rows) == 0 {
			return nil, "", errors.New("新浪美股行情页返回空数据")
		}
		return map[string]any{"count": len(rows), "code": "gb_aapl"}, trimSample(firstNonEmptyString(rows)), nil
	})
	r.runProbe("stock", "baidu_gushitong_financial_page", "https://gushitong.baidu.com/stock/ab-600519", false, func() (map[string]any, string, error) {
		rows := derefStringSlice(r.provider.SearchGuShiTongStockInfo("sh600519", int64(crawlTimeout.Seconds())))
		if len(rows) == 0 {
			return nil, "", errors.New("百度股市通财务页返回空数据")
		}
		return map[string]any{"count": len(rows), "code": "sh600519"}, trimSample(firstNonEmptyString(rows)), nil
	})
	r.runProbe("stock", "xueqiu_financial_page", "https://xueqiu.com/snowman/S/600519/detail#/ZYCWZB", false, func() (map[string]any, string, error) {
		rows := derefStringSlice(r.provider.FinancialReportsByXueqiu("sh600519", int64(crawlTimeout.Seconds())))
		if len(rows) == 0 {
			return nil, "", errors.New("雪球财务页返回空数据")
		}
		return map[string]any{"count": len(rows), "code": "sh600519"}, trimSample(firstNonEmptyString(rows)), nil
	})
	r.runProbe("stock", "eastmoney_financial_page", "https://emweb.securities.eastmoney.com/pc_hsf10/pages/index.html?type=web&code=sh600519#/cwfx", false, func() (map[string]any, string, error) {
		rows := derefStringSlice(r.provider.FinancialReports("sh600519", int64(crawlTimeout.Seconds())))
		if len(rows) == 0 {
			return nil, "", errors.New("东财 F10 财务页返回空数据")
		}
		return map[string]any{"count": len(rows), "code": "sh600519"}, trimSample(firstNonEmptyString(rows)), nil
	})

	r.runProbe("tushare", "trade_calendar", "https://api.tushare.pro", false, func() (map[string]any, string, error) {
		if strings.TrimSpace(cfg.TushareToken) == "" {
			return nil, "", skipAudit("tushare token 未配置")
		}
		now := time.Now().In(cnLocation())
		openMap, err := r.provider.Tushare(cfg).GetTradeCalOpenMap("SSE", now.AddDate(0, 0, -10), now, cfg.CrawlTimeOut)
		if err != nil {
			return nil, "", err
		}
		if len(openMap) == 0 {
			return nil, "", errors.New("trade_cal 返回空数据")
		}
		keys := sortedMapKeys(openMap)
		return map[string]any{"count": len(openMap)}, trimSample(keys[len(keys)-1]), nil
	})
	r.runProbe("tushare", "daily", "https://api.tushare.pro", false, func() (map[string]any, string, error) {
		if strings.TrimSpace(cfg.TushareToken) == "" {
			return nil, "", skipAudit("tushare token 未配置")
		}
		now := time.Now().In(cnLocation())
		text := r.provider.Tushare(cfg).GetDaily("600519.SH", now.AddDate(0, 0, -10).Format("20060102"), now.Format("20060102"), cfg.CrawlTimeOut)
		if strings.TrimSpace(text) == "" {
			return nil, "", errors.New("tushare daily 返回空数据")
		}
		lines := strings.Split(strings.TrimSpace(text), "\n")
		return map[string]any{"lines": len(lines)}, trimSample(lines[len(lines)-1]), nil
	})
	r.runProbe("tushare", "latest_trade_date", "https://api.tushare.pro", false, func() (map[string]any, string, error) {
		if strings.TrimSpace(cfg.TushareToken) == "" {
			return nil, "", skipAudit("tushare token 未配置")
		}
		value, err := r.provider.Tushare(cfg).GetLatestTradeDate(cfg.CrawlTimeOut)
		if err != nil {
			return nil, "", err
		}
		return map[string]any{"tradeDate": value.Format("2006-01-02")}, value.Format("2006-01-02"), nil
	})
	r.runProbe("tushare", "minute_bars", "https://api.tushare.pro", false, func() (map[string]any, string, error) {
		if strings.TrimSpace(cfg.TushareToken) == "" {
			return nil, "", skipAudit("tushare token 未配置")
		}
		bars, err := r.provider.Tushare(cfg).GetStockMinuteBars("600519.SH", minuteWindow.Start, minuteWindow.End, cfg.CrawlTimeOut)
		if err != nil {
			return nil, "", err
		}
		if len(bars) == 0 {
			return nil, "", errors.New("tushare minute bars 返回空数据")
		}
		last := bars[len(bars)-1]
		return map[string]any{"count": len(bars), "windowStart": minuteWindow.Start.Format(time.DateTime), "windowEnd": minuteWindow.End.Format(time.DateTime)}, trimSample(last.TradeTime.Format(time.DateTime)), nil
	})
	r.runProbe("tushare", "stock_basic", "https://api.tushare.pro", false, func() (map[string]any, string, error) {
		if strings.TrimSpace(cfg.TushareToken) == "" {
			return nil, "", skipAudit("tushare token 未配置")
		}
		res := &tushareTableResponse{}
		client := resty.New().SetTimeout(crawlTimeout)
		resp, err := client.R().
			SetHeader("content-type", "application/json").
			SetBody(&tushareRequest{
				APIName: "stock_basic",
				Token:   cfg.TushareToken,
				Fields:  "ts_code,symbol,name,market,list_date",
			}).
			SetResult(res).
			Post("https://api.tushare.pro")
		if err != nil {
			return nil, "", err
		}
		if resp == nil || res.Code != 0 || len(res.Data.Items) == 0 {
			return map[string]any{"statusCode": statusCode(resp), "msg": res.Msg}, trimSample(stringifyAny(res)), errors.New("tushare stock_basic 返回空数据")
		}
		return map[string]any{"count": len(res.Data.Items), "fields": len(res.Data.Fields)}, trimSample(stringifyAny(res.Data.Items[0])), nil
	})
	r.runProbe("tushare", "index_basic", "https://api.tushare.pro", false, func() (map[string]any, string, error) {
		if strings.TrimSpace(cfg.TushareToken) == "" {
			return nil, "", skipAudit("tushare token 未配置")
		}
		res := &tushareTableResponse{}
		client := resty.New().SetTimeout(crawlTimeout)
		resp, err := client.R().
			SetHeader("content-type", "application/json").
			SetBody(&tushareRequest{
				APIName: "index_basic",
				Token:   cfg.TushareToken,
				Fields:  "ts_code,name,market,list_date",
			}).
			SetResult(res).
			Post("https://api.tushare.pro")
		if err != nil {
			return nil, "", err
		}
		if resp == nil || res.Code != 0 || len(res.Data.Items) == 0 {
			return map[string]any{"statusCode": statusCode(resp), "msg": res.Msg}, trimSample(stringifyAny(res)), errors.New("tushare index_basic 返回空数据")
		}
		return map[string]any{"count": len(res.Data.Items), "fields": len(res.Data.Fields)}, trimSample(stringifyAny(res.Data.Items[0])), nil
	})

	r.runProbe("public_data", "github_raw_stock_basic", baseInfoEndpoint("stock_basic.json"), false, func() (map[string]any, string, error) {
		res := &tushareTableResponse{}
		client := resty.New().SetTimeout(crawlTimeout)
		resp, err := client.R().SetResult(res).Get(baseInfoEndpoint("stock_basic.json"))
		if err != nil {
			return nil, "", err
		}
		if resp == nil || len(res.Data.Items) == 0 {
			return map[string]any{"statusCode": statusCode(resp)}, trimSample(stringifyAny(res)), errors.New("公开 stock_basic 文件返回空数据")
		}
		return map[string]any{"count": len(res.Data.Items)}, trimSample(stringifyAny(res.Data.Items[0])), nil
	})
	r.runProbe("public_data", "github_raw_stock_base_info_hk", baseInfoEndpoint("stock_base_info_hk.json"), false, func() (map[string]any, string, error) {
		rows := make([]map[string]any, 0)
		client := resty.New().SetTimeout(crawlTimeout)
		resp, err := client.R().SetResult(&rows).Get(baseInfoEndpoint("stock_base_info_hk.json"))
		if err != nil {
			return nil, "", err
		}
		if resp == nil || len(rows) == 0 {
			return map[string]any{"statusCode": statusCode(resp)}, "", errors.New("公开港股基础资料返回空数据")
		}
		return map[string]any{"count": len(rows)}, trimSample(stringifyAny(rows[0])), nil
	})
	r.runProbe("public_data", "github_raw_stock_base_info_us", baseInfoEndpoint("stock_base_info_us.json"), false, func() (map[string]any, string, error) {
		rows := make([]map[string]any, 0)
		client := resty.New().SetTimeout(crawlTimeout)
		resp, err := client.R().SetResult(&rows).Get(baseInfoEndpoint("stock_base_info_us.json"))
		if err != nil {
			return nil, "", err
		}
		if resp == nil || len(rows) == 0 {
			return map[string]any{"statusCode": statusCode(resp)}, "", errors.New("公开美股基础资料返回空数据")
		}
		return map[string]any{"count": len(rows)}, trimSample(stringifyAny(rows[0])), nil
	})

	r.runProbe("minute_provider", "diemeng_selfcheck", r.provider.DiemengBaseURL(), false, func() (map[string]any, string, error) {
		if !cfg.PrivateMinuteEnabled {
			return nil, "", skipAudit("private_minute_enabled=0")
		}
		snap, err := r.provider.WaitDiemengSelfCheck("network-audit", 60*time.Second)
		meta := map[string]any{
			"status":    snap.Status,
			"summary":   snap.Summary,
			"checkedAt": snap.CheckedAt.Format(time.DateTime),
			"probes":    snap.ProbeCount,
		}
		if err != nil {
			return meta, "", err
		}
		if strings.TrimSpace(snap.Status) == "error" {
			return meta, trimSample(snap.Summary), errors.New(snap.Summary)
		}
		return meta, trimSample(snap.Summary), nil
	})
	r.runProbe("minute_provider", "diemeng_history", r.provider.DiemengBaseURL(), false, func() (map[string]any, string, error) {
		if !cfg.PrivateMinuteEnabled {
			return nil, "", skipAudit("private_minute_enabled=0")
		}
		res, err := r.provider.AuditDiemengMinuteBars("600519.SH", minuteWindow.Start, minuteWindow.End)
		if err != nil {
			return nil, "", err
		}
		if res == nil || res.Bars == 0 {
			return map[string]any{"windowStart": minuteWindow.Start.Format(time.DateTime), "windowEnd": minuteWindow.End.Format(time.DateTime)}, "", errors.New("Diemeng 分钟线返回空数据")
		}
		return minuteAuditMeta(res), trimSample(res.LastTradeTime), nil
	})
	r.runProbe("minute_provider", "akshare_history", "python akshare script", false, func() (map[string]any, string, error) {
		if !cfg.AkshareEnabled {
			return nil, "", skipAudit("akshare_enabled=0")
		}
		res, err := r.provider.AuditAkShareMinuteBars("600519.SH", minuteWindow.Start, minuteWindow.End)
		if err != nil {
			return nil, "", err
		}
		if res == nil || res.Bars == 0 {
			return minuteAuditMeta(res), "", errors.New("AkShare 分钟线返回空数据")
		}
		return minuteAuditMeta(res), trimSample(res.LastTradeTime), nil
	})
	r.runProbe("minute_provider", "sina_today_intraday", "http://quotes.sina.cn/cn/api/json_v2.php/CN_MarketDataService.getKLineData", false, func() (map[string]any, string, error) {
		if !cfg.SinaMinuteEnabled {
			return nil, "", skipAudit("sina_minute_enabled=0")
		}
		if todayWindowErr != nil {
			return nil, "", skipAudit("%s", todayWindowErr.Error())
		}
		res, err := r.provider.AuditSinaMinuteBars("600519.SH", todayWindow.Start, todayWindow.End)
		if err != nil {
			return nil, "", err
		}
		if res == nil || res.Bars == 0 {
			return minuteAuditMeta(res), "", errors.New("Sina 分钟线返回空数据")
		}
		return minuteAuditMeta(res), trimSample(res.LastTradeTime), nil
	})
	r.runProbe("minute_provider", "tencent_recent_intraday", "https://ifzq.gtimg.cn/appstock/app/kline/mkline", false, func() (map[string]any, string, error) {
		if !cfg.TencentMinuteEnabled {
			return nil, "", skipAudit("tencent_minute_enabled=0")
		}
		if todayWindowErr != nil {
			return nil, "", skipAudit("%s", todayWindowErr.Error())
		}
		res, err := r.provider.AuditTencentMinuteBars("600519.SH", todayWindow.Start, todayWindow.End)
		if err != nil {
			return nil, "", err
		}
		if res == nil || res.Bars == 0 {
			return minuteAuditMeta(res), "", errors.New("腾讯分钟线返回空数据")
		}
		return minuteAuditMeta(res), trimSample(res.LastTradeTime), nil
	})

	r.runProbe("ai", "provider_connectivity", "chat/completions", true, func() (map[string]any, string, error) {
		if len(cfg.AiConfigs) == 0 {
			return nil, "", skipAudit("未配置 ai_config")
		}
		success := 0
		failures := make([]map[string]any, 0)
		okProviders := make([]string, 0)
		for _, item := range cfg.AiConfigs {
			if item == nil {
				continue
			}
			err := r.runSingleAIProbe(item)
			if err != nil {
				failures = append(failures, map[string]any{
					"id":    item.ID,
					"name":  strings.TrimSpace(item.Name),
					"model": strings.TrimSpace(item.ModelName),
					"error": err.Error(),
				})
				continue
			}
			success++
			okProviders = append(okProviders, fmt.Sprintf("%d:%s", item.ID, strings.TrimSpace(item.Name)))
		}
		meta := map[string]any{
			"configured": len(cfg.AiConfigs),
			"ok":         success,
			"failed":     len(failures),
			"providers":  okProviders,
		}
		if len(failures) > 0 {
			meta["failures"] = failures
		}
		if success == 0 {
			return meta, "", errors.New("所有 AI Provider 调用均失败")
		}
		if len(failures) > 0 {
			return meta, trimSample(strings.Join(okProviders, ", ")), errors.New("部分 AI Provider 调用失败")
		}
		return meta, trimSample(strings.Join(okProviders, ", ")), nil
	})

	r.runProbe("notify", "dingding_robot", strings.TrimSpace(cfg.DingRobot), true, func() (map[string]any, string, error) {
		if !cfg.DingPushEnable {
			return nil, "", skipAudit("ding_push_enable=0")
		}
		if strings.TrimSpace(cfg.DingRobot) == "" {
			return nil, "", skipAudit("ding_robot 为空")
		}
		result := r.provider.SendDingDingMessage(`{"msgtype":"text","text":{"content":"go-stock network audit"}}`)
		if !strings.Contains(result, "成功") {
			return map[string]any{"result": result}, "", errors.New(result)
		}
		return map[string]any{"result": result}, trimSample(result), nil
	})

	r.runProbe("frontend_direct", "stock_sina_min_gif_a", "http://image.sinajs.cn/newchart/min/n/sh600519.gif", false, func() (map[string]any, string, error) {
		return probeHTTPResource("http://image.sinajs.cn/newchart/min/n/sh600519.gif?t="+fmt.Sprintf("%d", time.Now().UnixMilli()), httpProbeOptions{
			Timeout:              crawlTimeout,
			Headers:              browserLikeHeaders("https://finance.sina.com.cn"),
			MinBytes:             512,
			ExpectedContentTypes: []string{"image/gif"},
			ExpectedPrefixes:     [][]byte{[]byte("GIF87a"), []byte("GIF89a")},
		})
	})
	r.runProbe("frontend_direct", "stock_sina_daily_gif_a", "http://image.sinajs.cn/newchart/daily/n/sh600519.gif", false, func() (map[string]any, string, error) {
		return probeHTTPResource("http://image.sinajs.cn/newchart/daily/n/sh600519.gif?t="+fmt.Sprintf("%d", time.Now().UnixMilli()), httpProbeOptions{
			Timeout:              crawlTimeout,
			Headers:              browserLikeHeaders("https://finance.sina.com.cn"),
			MinBytes:             512,
			ExpectedContentTypes: []string{"image/gif"},
			ExpectedPrefixes:     [][]byte{[]byte("GIF87a"), []byte("GIF89a")},
		})
	})
	r.runProbe("frontend_direct", "stock_sina_min_gif_hk", "http://image.sinajs.cn/newchart/hk_stock/min/00700.gif", false, func() (map[string]any, string, error) {
		return probeHTTPResource("http://image.sinajs.cn/newchart/hk_stock/min/00700.gif?t="+fmt.Sprintf("%d", time.Now().UnixMilli()), httpProbeOptions{
			Timeout:              crawlTimeout,
			Headers:              browserLikeHeaders("https://finance.sina.com.cn"),
			MinBytes:             512,
			ExpectedContentTypes: []string{"image/gif"},
			ExpectedPrefixes:     [][]byte{[]byte("GIF87a"), []byte("GIF89a")},
		})
	})
	r.runProbe("frontend_direct", "stock_sina_daily_gif_hk", "http://image.sinajs.cn/newchart/hk_stock/daily/00700.gif", false, func() (map[string]any, string, error) {
		return probeHTTPResource("http://image.sinajs.cn/newchart/hk_stock/daily/00700.gif?t="+fmt.Sprintf("%d", time.Now().UnixMilli()), httpProbeOptions{
			Timeout:              crawlTimeout,
			Headers:              browserLikeHeaders("https://finance.sina.com.cn"),
			MinBytes:             512,
			ExpectedContentTypes: []string{"image/gif"},
			ExpectedPrefixes:     [][]byte{[]byte("GIF87a"), []byte("GIF89a")},
		})
	})
	r.runProbe("frontend_direct", "stock_sina_min_gif_us", "http://image.sinajs.cn/newchart/usstock/min/aapl.gif", false, func() (map[string]any, string, error) {
		return probeHTTPResource("http://image.sinajs.cn/newchart/usstock/min/aapl.gif?t="+fmt.Sprintf("%d", time.Now().UnixMilli()), httpProbeOptions{
			Timeout:              crawlTimeout,
			Headers:              browserLikeHeaders("https://finance.sina.com.cn"),
			MinBytes:             512,
			ExpectedContentTypes: []string{"image/gif"},
			ExpectedPrefixes:     [][]byte{[]byte("GIF87a"), []byte("GIF89a")},
		})
	})
	r.runProbe("frontend_direct", "stock_sina_daily_gif_us", "http://image.sinajs.cn/newchart/usstock/daily/aapl.gif", false, func() (map[string]any, string, error) {
		return probeHTTPResource("http://image.sinajs.cn/newchart/usstock/daily/aapl.gif?t="+fmt.Sprintf("%d", time.Now().UnixMilli()), httpProbeOptions{
			Timeout:              crawlTimeout,
			Headers:              browserLikeHeaders("https://finance.sina.com.cn"),
			MinBytes:             512,
			ExpectedContentTypes: []string{"image/gif"},
			ExpectedPrefixes:     [][]byte{[]byte("GIF87a"), []byte("GIF89a")},
		})
	})
	r.runProbe("frontend_direct", "fund_sina_nav_gif", "https://image.sinajs.cn/newchart/v5/fund/nav/ss/000001.gif", false, func() (map[string]any, string, error) {
		return probeHTTPResource("https://image.sinajs.cn/newchart/v5/fund/nav/ss/000001.gif?t="+fmt.Sprintf("%d", time.Now().UnixMilli()), httpProbeOptions{
			Timeout:              crawlTimeout,
			Headers:              browserLikeHeaders("https://fund.eastmoney.com/"),
			MinBytes:             512,
			ExpectedContentTypes: []string{"image/gif"},
			ExpectedPrefixes:     [][]byte{[]byte("GIF87a"), []byte("GIF89a")},
		})
	})
	r.runProbe("frontend_direct", "stock_notice_pdf", "https://pdf.dfcfw.com/pdf/H2_<art_code>_1.pdf", false, func() (map[string]any, string, error) {
		items := newsAPI.StockNotice("600519.SH")
		if len(items) == 0 {
			return nil, "", errors.New("公告列表为空，无法探测 PDF")
		}
		artCode := pickMapString(anyToStringMap(items[0]), "art_code", "artCode")
		if artCode == "" {
			return map[string]any{"raw": items[0]}, "", errors.New("公告未返回 art_code")
		}
		url := fmt.Sprintf("https://pdf.dfcfw.com/pdf/H2_%s_1.pdf?%d.pdf", artCode, time.Now().UnixMilli())
		return probeHTTPResource(url, httpProbeOptions{
			Timeout:              crawlTimeout,
			Headers:              browserLikeHeaders("https://data.eastmoney.com/"),
			MinBytes:             1024,
			ExpectedContentTypes: []string{"application/pdf"},
			ExpectedPrefixes:     [][]byte{[]byte("%PDF")},
		})
	})
	r.runProbe("frontend_direct", "stock_research_report_pdf", "https://pdf.dfcfw.com/pdf/H3_<infoCode>_1.pdf", false, func() (map[string]any, string, error) {
		items := newsAPI.StockResearchReport("600519.SH", 7)
		if len(items) == 0 {
			return nil, "", errors.New("个股研报列表为空，无法探测 PDF")
		}
		infoCode := pickMapString(anyToStringMap(items[0]), "infoCode", "info_code", "INFO_CODE")
		if infoCode == "" {
			return map[string]any{"raw": items[0]}, "", errors.New("个股研报未返回 infoCode")
		}
		url := fmt.Sprintf("https://pdf.dfcfw.com/pdf/H3_%s_1.pdf?%d.pdf", infoCode, time.Now().UnixMilli())
		return probeHTTPResource(url, httpProbeOptions{
			Timeout:              crawlTimeout,
			Headers:              browserLikeHeaders("https://data.eastmoney.com/"),
			MinBytes:             1024,
			ExpectedContentTypes: []string{"application/pdf"},
			ExpectedPrefixes:     [][]byte{[]byte("%PDF")},
		})
	})
	r.runProbe("frontend_direct", "industry_research_report_pdf", "https://pdf.dfcfw.com/pdf/H3_<infoCode>_1.pdf", false, func() (map[string]any, string, error) {
		items := newsAPI.IndustryResearchReport("", 7)
		if len(items) == 0 {
			return nil, "", errors.New("行业研报列表为空，无法探测 PDF")
		}
		infoCode := pickMapString(anyToStringMap(items[0]), "infoCode", "info_code", "INFO_CODE")
		if infoCode == "" {
			return map[string]any{"raw": items[0]}, "", errors.New("行业研报未返回 infoCode")
		}
		url := fmt.Sprintf("https://pdf.dfcfw.com/pdf/H3_%s_1.pdf?%d.pdf", infoCode, time.Now().UnixMilli())
		return probeHTTPResource(url, httpProbeOptions{
			Timeout:              crawlTimeout,
			Headers:              browserLikeHeaders("https://data.eastmoney.com/"),
			MinBytes:             1024,
			ExpectedContentTypes: []string{"application/pdf"},
			ExpectedPrefixes:     [][]byte{[]byte("%PDF")},
		})
	})
	r.runProbe("frontend_direct", "embed_xuangutong", "https://xuangutong.com.cn", false, func() (map[string]any, string, error) {
		return probeHTTPResource("https://xuangutong.com.cn", httpProbeOptions{
			Timeout:              crawlTimeout,
			Headers:              browserLikeHeaders("https://xuangutong.com.cn"),
			MinBytes:             256,
			ExpectedContentTypes: []string{"text/html"},
			Embeddable:           true,
		})
	})
	r.runProbe("frontend_direct", "embed_baidu_gushitong_home", "https://gushitong.baidu.com", false, func() (map[string]any, string, error) {
		return probeHTTPResource("https://gushitong.baidu.com", httpProbeOptions{
			Timeout:              crawlTimeout,
			Headers:              browserLikeHeaders("https://gushitong.baidu.com"),
			MinBytes:             256,
			ExpectedContentTypes: []string{"text/html"},
			Embeddable:           true,
		})
	})
	r.runProbe("frontend_direct", "embed_eastmoney_hotmap", "https://quote.eastmoney.com/stockhotmap/", false, func() (map[string]any, string, error) {
		return probeHTTPResource("https://quote.eastmoney.com/stockhotmap/", httpProbeOptions{
			Timeout:              crawlTimeout,
			Headers:              browserLikeHeaders("https://quote.eastmoney.com/"),
			MinBytes:             256,
			ExpectedContentTypes: []string{"text/html"},
			Embeddable:           true,
		})
	})
	r.runProbe("frontend_direct", "embed_tophub_finance", "https://tophub.today/c/finance", false, func() (map[string]any, string, error) {
		return probeHTTPResource("https://tophub.today/c/finance", httpProbeOptions{
			Timeout:              crawlTimeout,
			Headers:              browserLikeHeaders("https://tophub.today/"),
			MinBytes:             256,
			ExpectedContentTypes: []string{"text/html"},
			Embeddable:           true,
		})
	})
	r.runProbe("frontend_direct", "embed_996_ninja", "https://996.ninja/", false, func() (map[string]any, string, error) {
		return probeHTTPResource("https://996.ninja/", httpProbeOptions{
			Timeout:              crawlTimeout,
			Headers:              browserLikeHeaders("https://996.ninja/"),
			MinBytes:             256,
			ExpectedContentTypes: []string{"text/html"},
			Embeddable:           true,
		})
	})
	r.runProbe("frontend_direct", "embed_cls_quotation", "https://www.cls.cn/quotation", false, func() (map[string]any, string, error) {
		return probeHTTPResource("https://www.cls.cn/quotation", httpProbeOptions{
			Timeout:              crawlTimeout,
			Headers:              browserLikeHeaders("https://www.cls.cn/"),
			MinBytes:             256,
			ExpectedContentTypes: []string{"text/html"},
			Embeddable:           true,
		})
	})
	r.runProbe("frontend_direct", "iwencai_unified_search", "https://www.iwencai.com/unifiedwap/result?w=贵州茅台", false, func() (map[string]any, string, error) {
		return probeHTTPResource("https://www.iwencai.com/unifiedwap/result?w=贵州茅台", httpProbeOptions{
			Timeout:              crawlTimeout,
			Headers:              browserLikeHeaders("https://www.iwencai.com/"),
			MinBytes:             256,
			ExpectedContentTypes: []string{"text/html"},
		})
	})
}

func (r *networkAuditRunner) runSingleAIProbe(item *models.AIConfig) error {
	cfg := &models.AIConfig{
		ID:               item.ID,
		Name:             item.Name,
		BaseUrl:          item.BaseUrl,
		ApiKey:           item.ApiKey,
		ModelName:        item.ModelName,
		MaxTokens:        minInt(item.MaxTokens, 64),
		Temperature:      0.1,
		TimeOut:          minInt(item.TimeOut, 45),
		HttpProxy:        item.HttpProxy,
		HttpProxyEnabled: item.HttpProxyEnabled,
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 64
	}
	if cfg.TimeOut <= 0 {
		cfg.TimeOut = 45
	}
	content, _, modelName, err := r.provider.CompleteChat(context.Background(), cfg, []map[string]any{
		{"role": "system", "content": "你是网络连通性探针。请只回答 OK。"},
		{"role": "user", "content": "请只回复 OK"},
	}, false)
	if err != nil {
		return fmt.Errorf("%s/%s: %w", strings.TrimSpace(item.Name), strings.TrimSpace(item.ModelName), err)
	}
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("%s/%s: empty model content", strings.TrimSpace(item.Name), strings.TrimSpace(item.ModelName))
	}
	_ = modelName
	return nil
}

func (r *networkAuditRunner) runProbe(category, name, endpoint string, sideEffect bool, fn func() (map[string]any, string, error)) {
	start := time.Now()
	probe := networkAuditProbe{
		Category:   category,
		Name:       name,
		Endpoint:   strings.TrimSpace(endpoint),
		SideEffect: sideEffect,
		StartedAt:  start.Format(time.DateTime),
		Status:     "error",
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			probe.Status = "error"
			probe.Error = fmt.Sprintf("panic: %v", recovered)
		}
		probe.FinishedAt = time.Now().Format(time.DateTime)
		probe.DurationMS = time.Since(start).Milliseconds()
		r.report.Probes = append(r.report.Probes, probe)
		r.report.Summary.Total++
		switch probe.Status {
		case "ok":
			r.report.Summary.OK++
		case "skipped":
			r.report.Summary.Skipped++
			r.report.SkippedNames = append(r.report.SkippedNames, probe.Name)
		default:
			r.report.Summary.Error++
			r.report.FailureNames = append(r.report.FailureNames, probe.Name)
		}
	}()

	type probeResult struct {
		meta   map[string]any
		sample string
		err    error
	}
	timeout := 25 * time.Second
	if category == "frontend_direct" || category == "share" || category == "update" {
		timeout = 12 * time.Second
	}
	resultCh := make(chan probeResult, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				resultCh <- probeResult{
					err: fmt.Errorf("panic: %v", recovered),
				}
			}
		}()
		meta, sample, err := fn()
		resultCh <- probeResult{
			meta:   meta,
			sample: sample,
			err:    err,
		}
	}()

	var (
		meta   map[string]any
		sample string
		err    error
	)
	select {
	case result := <-resultCh:
		meta = result.meta
		sample = result.sample
		err = result.err
	case <-time.After(timeout):
		err = fmt.Errorf("probe timeout after %s", timeout)
	}
	probe.Metadata = meta
	probe.Sample = trimSample(sample)
	if err == nil {
		probe.Status = "ok"
		return
	}
	var skipErr *auditSkipError
	if errors.As(err, &skipErr) {
		probe.Status = "skipped"
		probe.Detail = trimSample(skipErr.Error())
		return
	}
	probe.Status = "error"
	probe.Error = trimSample(err.Error())
}

func writeNetworkAuditReportFiles(report *networkAuditReport, jsonPath, mdPath string) error {
	body, err := marshalPrettyJSON(report)
	if err != nil {
		return err
	}
	if err := os.WriteFile(jsonPath, body, 0o644); err != nil {
		return err
	}
	markdown := buildNetworkAuditMarkdown(report, jsonPath)
	return os.WriteFile(mdPath, []byte(markdown), 0o644)
}

func buildNetworkAuditMarkdown(report *networkAuditReport, jsonPath string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# 网络接口审计报告\n\n")
	fmt.Fprintf(&b, "- 开始时间: %s\n", report.StartedAt)
	fmt.Fprintf(&b, "- 结束时间: %s\n", report.FinishedAt)
	fmt.Fprintf(&b, "- 数据库: %s\n", report.DBPath)
	fmt.Fprintf(&b, "- JSON: %s\n", jsonPath)
	fmt.Fprintf(&b, "- 结果汇总: OK %d / 失败 %d / 跳过 %d / 总计 %d\n\n", report.Summary.OK, report.Summary.Error, report.Summary.Skipped, report.Summary.Total)
	if len(report.Notes) > 0 {
		b.WriteString("## 备注\n")
		for _, note := range report.Notes {
			fmt.Fprintf(&b, "- %s\n", note)
		}
		b.WriteString("\n")
	}
	b.WriteString("## 探针结果\n")
	for _, probe := range report.Probes {
		fmt.Fprintf(&b, "### [%s] %s\n", strings.ToUpper(probe.Status), probe.Name)
		fmt.Fprintf(&b, "- 分类: %s\n", probe.Category)
		if probe.Endpoint != "" {
			fmt.Fprintf(&b, "- 端点: %s\n", probe.Endpoint)
		}
		fmt.Fprintf(&b, "- 耗时: %d ms\n", probe.DurationMS)
		fmt.Fprintf(&b, "- 副作用: %v\n", probe.SideEffect)
		if probe.Sample != "" {
			fmt.Fprintf(&b, "- 示例: %s\n", probe.Sample)
		}
		if probe.Detail != "" {
			fmt.Fprintf(&b, "- 详情: %s\n", probe.Detail)
		}
		if probe.Error != "" {
			fmt.Fprintf(&b, "- 错误: %s\n", probe.Error)
		}
		if len(probe.Metadata) > 0 {
			body, _ := json.Marshal(probe.Metadata)
			fmt.Fprintf(&b, "- 元数据: `%s`\n", string(body))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func prepareNetworkAuditDir(dbPath, override string) (string, error) {
	base := strings.TrimSpace(override)
	if base == "" {
		base = defaultNetworkAuditBaseDir(dbPath)
	}
	runDir := filepath.Join(base, time.Now().Format("20060102-150405"))
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return "", err
	}
	return runDir, nil
}

func defaultNetworkAuditBaseDir(dbPath string) string {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return filepath.Join("runtime", "network-audit")
	}
	dir := filepath.Dir(dbPath)
	if filepath.Base(dir) == "db" {
		return filepath.Join(filepath.Dir(dir), "network-audit")
	}
	return filepath.Join(dir, "network-audit")
}

func buildNetworkAuditEnvironment(cfg *models.SettingConfig, provider marketAuditProvider) map[string]any {
	loc := cnLocation()
	return map[string]any{
		"now":      time.Now().In(loc).Format(time.DateTime),
		"timezone": loc.String(),
		"runtime":  cfg != nil,
		"config": map[string]any{
			"httpProxyEnabled":        cfg.HttpProxyEnabled,
			"forceNoProxyForFetch":    cfg.ForceNoProxyForFetch,
			"minuteProviderOrder":     cfg.MinuteProviderOrder,
			"privateMinuteEnabled":    cfg.PrivateMinuteEnabled,
			"akshareEnabled":          cfg.AkshareEnabled,
			"sinaMinuteEnabled":       cfg.SinaMinuteEnabled,
			"tencentMinuteEnabled":    cfg.TencentMinuteEnabled,
			"eastmoneyMinuteEnabled":  cfg.EastmoneyMinuteEnabled,
			"aiAnalysisTimes":         cfg.AIAnalysisTimes,
			"aiAnalysisAutoEnabled":   cfg.AIAnalysisEnabled,
			"aiReviewStartTime":       cfg.AIReviewStartTime,
			"aiReviewIntervalMinutes": cfg.AIReviewIntervalMinutes,
			"qgqpBidConfigured":       strings.TrimSpace(cfg.QgqpBId) != "",
			"tushareTokenConfigured":  strings.TrimSpace(cfg.TushareToken) != "",
			"dingRobotConfigured":     strings.TrimSpace(cfg.DingRobot) != "",
			"privateMinuteBaseURL":    provider.DiemengBaseURL(),
			"privateMinuteTimeoutSec": cfg.PrivateMinuteTimeoutSec,
			"akshareMinuteSourceMode": cfg.AkshareMinuteSourceMode,
		},
	}
}

func buildNetworkAuditSettings(cfg *models.SettingConfig, provider marketAuditProvider) map[string]any {
	return map[string]any{
		"dingPushEnable":          cfg.DingPushEnable,
		"dingRobotConfigured":     strings.TrimSpace(cfg.DingRobot) != "",
		"httpProxyEnabled":        cfg.HttpProxyEnabled,
		"forceNoProxyForFetch":    cfg.ForceNoProxyForFetch,
		"aiAnalysisAutoEnabled":   cfg.AIAnalysisEnabled,
		"aiAnalysisTimes":         splitCSV(cfg.AIAnalysisTimes),
		"aiReviewStartTime":       cfg.AIReviewStartTime,
		"aiReviewIntervalMinutes": cfg.AIReviewIntervalMinutes,
		"minuteProviderOrder":     cfg.MinuteProviderOrder,
		"privateMinuteEnabled":    cfg.PrivateMinuteEnabled,
		"privateMinuteBaseURL":    provider.DiemengBaseURL(),
		"privateMinuteTimeoutSec": cfg.PrivateMinuteTimeoutSec,
		"akshareEnabled":          cfg.AkshareEnabled,
		"sinaMinuteEnabled":       cfg.SinaMinuteEnabled,
		"tencentMinuteEnabled":    cfg.TencentMinuteEnabled,
		"eastmoneyMinuteEnabled":  cfg.EastmoneyMinuteEnabled,
		"akshareMinuteSourceMode": cfg.AkshareMinuteSourceMode,
		"qgqpBidConfigured":       strings.TrimSpace(cfg.QgqpBId) != "",
		"tushareTokenConfigured":  strings.TrimSpace(cfg.TushareToken) != "",
	}
}

func buildNetworkAuditAIConfigs(cfg *models.SettingConfig, provider marketAuditProvider) []map[string]any {
	items := make([]map[string]any, 0, len(cfg.AiConfigs))
	for _, item := range cfg.AiConfigs {
		if item == nil {
			continue
		}
		items = append(items, map[string]any{
			"id":               item.ID,
			"name":             strings.TrimSpace(item.Name),
			"provider":         strings.TrimSpace(provider.DetectAIProviderName(item)),
			"baseURL":          strings.TrimSpace(item.BaseUrl),
			"model":            strings.TrimSpace(item.ModelName),
			"timeoutSec":       item.TimeOut,
			"httpProxyEnabled": item.HttpProxyEnabled,
		})
	}
	return items
}

func buildMinuteWindowMetadata() map[string]any {
	historical := recentTradingWindow(time.Time{})
	intraday, err := recentIntradayWindow()
	result := map[string]any{
		"historical": map[string]any{
			"start": historical.Start.Format(time.DateTime),
			"end":   historical.End.Format(time.DateTime),
		},
	}
	if err != nil {
		result["intraday"] = map[string]any{"error": err.Error()}
		return result
	}
	result["intraday"] = map[string]any{
		"start": intraday.Start.Format(time.DateTime),
		"end":   intraday.End.Format(time.DateTime),
	}
	return result
}

type auditWindow struct {
	Start time.Time
	End   time.Time
}

func recentTradingWindow(latestTradeDate time.Time) auditWindow {
	loc := cnLocation()
	day := latestTradeDate.In(loc)
	if day.IsZero() {
		day = mostRecentWeekday(time.Now().In(loc))
	}
	start := time.Date(day.Year(), day.Month(), day.Day(), 10, 0, 0, 0, loc)
	end := time.Date(day.Year(), day.Month(), day.Day(), 10, 30, 0, 0, loc)
	return auditWindow{Start: start, End: end}
}

func recentIntradayWindow() (auditWindow, error) {
	loc := cnLocation()
	now := time.Now().In(loc)
	if now.Weekday() == time.Saturday || now.Weekday() == time.Sunday {
		return auditWindow{}, errors.New("当前是周末，Sina/腾讯盘中分钟线探针跳过")
	}
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	start := time.Date(day.Year(), day.Month(), day.Day(), 10, 0, 0, 0, loc)
	end := time.Date(day.Year(), day.Month(), day.Day(), 10, 30, 0, 0, loc)
	if now.Before(end) {
		end = now.Add(-5 * time.Minute)
	}
	if end.Before(start.Add(5 * time.Minute)) {
		return auditWindow{}, errors.New("当前不在可验证的盘中分钟线窗口")
	}
	return auditWindow{Start: start, End: end}, nil
}

func mostRecentWeekday(now time.Time) time.Time {
	for {
		if now.Weekday() != time.Saturday && now.Weekday() != time.Sunday {
			return now
		}
		now = now.AddDate(0, 0, -1)
	}
}

func buildNoProxyKeys() []string {
	return []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy", "NO_PROXY", "no_proxy"}
}

func forceNoProxyEnv() map[string]string {
	snapshot := make(map[string]string)
	for _, key := range buildNoProxyKeys() {
		snapshot[key] = os.Getenv(key)
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		_ = os.Unsetenv(key)
	}
	_ = os.Setenv("NO_PROXY", "*")
	_ = os.Setenv("no_proxy", "*")
	return snapshot
}

func trimSample(input string) string {
	input = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(input, "\n", " "), "\r", " "))
	if len(input) > 180 {
		return input[:180] + "..."
	}
	return input
}

func anySliceFromMap(m map[string]any, key string) []any {
	if m == nil {
		return nil
	}
	raw, ok := m[key]
	if !ok {
		return nil
	}
	rows, ok := raw.([]any)
	if !ok {
		return nil
	}
	return rows
}

func nestedAnySlice(m map[string]any, keys ...string) []any {
	var current any = m
	for _, key := range keys {
		node, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = node[key]
	}
	rows, ok := current.([]any)
	if !ok {
		return nil
	}
	return rows
}

func anyToStringMap(v any) map[string]string {
	result := map[string]string{}
	raw, ok := v.(map[string]any)
	if !ok {
		return result
	}
	for k, value := range raw {
		result[k] = strings.TrimSpace(fmt.Sprintf("%v", value))
	}
	return result
}

func pickMapString(m map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(m[key]); value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func stringifyAny(v any) string {
	if v == nil {
		return ""
	}
	body, err := json.Marshal(v)
	if err == nil {
		return string(body)
	}
	return fmt.Sprintf("%v", v)
}

func splitCSV(input string) []string {
	if strings.TrimSpace(input) == "" {
		return nil
	}
	parts := strings.FieldsFunc(input, func(r rune) bool {
		return r == ',' || r == '，' || r == ';' || r == '；'
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func minInt(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	if value < fallback {
		return value
	}
	return fallback
}

func minuteAuditMeta(res *minuteProviderAuditResult) map[string]any {
	if res == nil {
		return nil
	}
	return map[string]any{
		"provider":       res.Provider,
		"source":         res.Source,
		"bars":           res.Bars,
		"firstTradeTime": res.FirstTradeTime,
		"lastTradeTime":  res.LastTradeTime,
	}
}

func baseInfoEndpoint(fileName string) string {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("GO_STOCK_BASEINFO_BASE_URL")), "/")
	if base == "" {
		base = "https://raw.githubusercontent.com/yxforever666gh/go-stock/main/build"
	}
	return base + "/" + strings.TrimSpace(fileName)
}

func sortedMapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func cnLocation() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err == nil {
		return loc
	}
	return time.FixedZone("CST", 8*60*60)
}

type httpProbeOptions struct {
	Method               string
	Timeout              time.Duration
	Headers              map[string]string
	MinBytes             int
	MustContainAny       []string
	ExpectedContentTypes []string
	ExpectedPrefixes     [][]byte
	Embeddable           bool
}

func probeHTTPResource(url string, opts httpProbeOptions) (map[string]any, string, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	client := resty.New().SetTimeout(timeout)
	req := client.R()
	for key, value := range opts.Headers {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		req.SetHeader(key, value)
	}
	method := strings.ToUpper(strings.TrimSpace(opts.Method))
	if method == "" {
		method = "GET"
	}
	var (
		resp *resty.Response
		err  error
	)
	switch method {
	case "HEAD":
		resp, err = req.Head(url)
	default:
		resp, err = req.Get(url)
	}
	if err != nil {
		return nil, "", err
	}
	if resp == nil {
		return nil, "", errors.New("HTTP 响应为空")
	}
	body := resp.Body()
	contentType := strings.ToLower(strings.TrimSpace(resp.Header().Get("Content-Type")))
	meta := map[string]any{
		"statusCode":  resp.StatusCode(),
		"contentType": contentType,
		"bodyBytes":   len(body),
	}
	if xfo := strings.TrimSpace(resp.Header().Get("X-Frame-Options")); xfo != "" {
		meta["xFrameOptions"] = xfo
	}
	if csp := strings.TrimSpace(resp.Header().Get("Content-Security-Policy")); csp != "" {
		meta["contentSecurityPolicy"] = trimSample(csp)
	}
	if resp.StatusCode() >= 400 {
		return meta, sampleFromHTTPBody(body, contentType), fmt.Errorf("HTTP %d", resp.StatusCode())
	}
	if len(opts.ExpectedContentTypes) > 0 && !matchesContentType(contentType, opts.ExpectedContentTypes) {
		return meta, sampleFromHTTPBody(body, contentType), fmt.Errorf("content-type 不匹配: %s", contentType)
	}
	if opts.MinBytes > 0 && len(body) < opts.MinBytes {
		return meta, sampleFromHTTPBody(body, contentType), fmt.Errorf("响应体过小: %d bytes", len(body))
	}
	if len(opts.MustContainAny) > 0 && !containsAnyFold(string(body), opts.MustContainAny) {
		return meta, sampleFromHTTPBody(body, contentType), errors.New("响应内容未包含预期关键字")
	}
	if len(opts.ExpectedPrefixes) > 0 && !matchesPrefix(body, opts.ExpectedPrefixes) {
		return meta, sampleFromHTTPBody(body, contentType), errors.New("响应内容前缀不符合预期")
	}
	if opts.Embeddable {
		if reason := frameBlockingReason(resp); reason != "" {
			meta["embeddable"] = false
			return meta, sampleFromHTTPBody(body, contentType), errors.New(reason)
		}
		meta["embeddable"] = true
	}
	return meta, sampleFromHTTPBody(body, contentType), nil
}

func auditDurationSeconds(value int64, fallback int64) time.Duration {
	if value <= 0 {
		value = fallback
	}
	return time.Duration(value) * time.Second
}

func browserLikeHeaders(referer string) map[string]string {
	headers := map[string]string{
		"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36",
	}
	if strings.TrimSpace(referer) != "" {
		headers["Referer"] = strings.TrimSpace(referer)
	}
	return headers
}

func matchesContentType(contentType string, expects []string) bool {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	for _, item := range expects {
		item = strings.ToLower(strings.TrimSpace(item))
		if item != "" && strings.Contains(contentType, item) {
			return true
		}
	}
	return false
}

func containsAnyFold(body string, expects []string) bool {
	body = strings.ToLower(body)
	for _, item := range expects {
		item = strings.ToLower(strings.TrimSpace(item))
		if item != "" && strings.Contains(body, item) {
			return true
		}
	}
	return false
}

func matchesPrefix(body []byte, prefixes [][]byte) bool {
	for _, prefix := range prefixes {
		if len(prefix) > 0 && len(body) >= len(prefix) && string(body[:len(prefix)]) == string(prefix) {
			return true
		}
	}
	return false
}

func sampleFromHTTPBody(body []byte, contentType string) string {
	if len(body) == 0 {
		return ""
	}
	if strings.Contains(contentType, "image/") || strings.Contains(contentType, "application/pdf") {
		if len(body) > 8 {
			return fmt.Sprintf("%x", body[:8])
		}
		return fmt.Sprintf("%x", body)
	}
	return trimSample(string(body))
}

func frameBlockingReason(resp *resty.Response) string {
	if resp == nil {
		return ""
	}
	xfo := strings.ToUpper(strings.TrimSpace(resp.Header().Get("X-Frame-Options")))
	if strings.Contains(xfo, "DENY") || strings.Contains(xfo, "SAMEORIGIN") {
		return "页面禁止 iframe 嵌入: X-Frame-Options=" + xfo
	}
	csp := strings.ToLower(strings.TrimSpace(resp.Header().Get("Content-Security-Policy")))
	if csp == "" {
		return ""
	}
	re := regexp.MustCompile(`frame-ancestors\s+([^;]+)`)
	match := re.FindStringSubmatch(csp)
	if len(match) < 2 {
		return ""
	}
	ancestors := strings.TrimSpace(match[1])
	if ancestors == "" {
		return ""
	}
	if strings.Contains(ancestors, "*") || strings.Contains(ancestors, "localhost") || strings.Contains(ancestors, "127.0.0.1") {
		return ""
	}
	return "页面禁止 iframe 嵌入: Content-Security-Policy frame-ancestors=" + trimSample(ancestors)
}

func summarizeAnySliceMap(m map[string]any, keys ...string) (int, string) {
	total := 0
	sample := ""
	for _, key := range keys {
		rows := nestedAnySlice(m, key)
		total += len(rows)
		if sample == "" && len(rows) > 0 {
			sample = stringifyAny(rows[0])
		}
	}
	return total, sample
}

func derefStringSlice(items *[]string) []string {
	if items == nil {
		return nil
	}
	return *items
}

func firstNonEmptyString(items []string) string {
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			return item
		}
	}
	return ""
}

func tradingViewStoryID(url string) string {
	url = strings.TrimSpace(url)
	if url == "" {
		return ""
	}
	if idx := strings.LastIndex(url, "/news/"); idx >= 0 {
		url = url[idx+len("/news/"):]
	}
	return strings.Trim(url, "/")
}

func statusCode(resp *resty.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode()
}

func mapValue(m map[string]any, key string) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	if raw, ok := m[key].(map[string]any); ok {
		return raw
	}
	return map[string]any{}
}
