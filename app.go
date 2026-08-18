package main

import (
	"context"
	"go-stock/backend/data"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	"go-stock/internal/bootstrap"
	"go-stock/internal/service"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/coocood/freecache"
	"github.com/go-resty/resty/v2"
	"github.com/robfig/cron/v3"
)

// App struct
type App struct {
	ctx               context.Context
	cache             *freecache.Cache
	cron              *cron.Cron
	cronEntrys        map[string]cron.EntryID
	cronEntrysMu      sync.RWMutex
	AiTools           []models.Tool
	services          service.AppServices
	domReadyMu        sync.Mutex
	domReadyDone      bool
	schedulerErrorsMu sync.Mutex
	schedulerErrors   []error
	researchRuntimeMu sync.RWMutex
	researchRuntime   *data.ResearchRuntime
	aiAnalysisRunMu   sync.Mutex
	aiAnalysisRunning bool
}

const aiAnalysisEntryPrefix = "AIAnalysisCustom_"
const aiLifecycleEntryKey = "AIAnalysisLifecycleDue"

// NewApp creates a new App application struct
func NewApp() *App {
	services, err := bootstrap.NewProductionServices()
	if err != nil {
		panic(err)
	}
	return NewAppWithServices(services)
}

func NewAppWithServices(services service.AppServices) *App {
	cacheSize := 512 * 1024
	cache := freecache.NewCache(cacheSize)
	c := cron.New(cron.WithSeconds())
	var tools []models.Tool
	tools = AddTools(tools)
	return &App{
		cache:      cache,
		cron:       c,
		cronEntrys: make(map[string]cron.EntryID),
		AiTools:    tools,
		services:   services,
	}
}

func AddTools(tools []models.Tool) []models.Tool {
	tools = append(tools, models.Tool{
		Type: "function",
		Function: models.ToolFunction{
			Name: "SearchStockByIndicators",
			Description: "根据自然语言筛选股票，返回自然语言选股条件要求的股票所有相关数据。输入股票名称可以获取当前股票最新的股价交易数据和基础财务指标信息，多个股票名称使用,分隔。" +
				"例如:分析强势方向：10点半之前涨停，非一字板，行业概念，按成交量从高到低排序。" +
				"例如:查看涨停板：涨停板，按涨幅从高到低排序。" +
				"例如:查看跌停板：跌停板，按跌幅从高到低排序。" +
				"例如:查看龙虎榜：龙虎榜，按涨幅从高到低排序。" +
				"例如:查看昨日龙虎榜：昨日龙虎榜。" +
				"例如:查看板块龙头行情：板块/概念龙头，按涨幅从高到低排序。" +
				"例如:查看板块龙头行情：龙头股，按成交量从高到低排序。" +
				"例如:查看技术指标：上海贝岭,macd,rsi,kdj,boll,5日均线,14日均线,30日均线,60日均线,成交量,OBV,EMA。" +
				"例如:查看近期趋势：量比连续2天>1，主力连续2日净流入且递增，主力净额>3000万元，行业，股价在20日线上。按成交量从高到低排序。" +
				"例如:当日成交量 ≥ 近 5 日平均成交量 ×1.5，收盘价 ≥ 20 日均线，20 日均线 ≥ 60 日均线，当日涨幅 3%-7%， 3日主力资金净流入累计≥5000 万元，当日换手率 5%-15%，筹码集中度（90% 筹码峰）≤15%，非创业板非科创板非ST非北交所，行业。按成交量从高到低排序。" +
				"例如:查看有潜力的成交量爆发股：最近7日成交量量比大于3，出现过一次，非ST。按成交量从高到低排序。" +
				"例如:成交量筛选：当日成交量大于前一日成交量的1.8倍;当日最高价创60日新高;当日收盘价大于5日均线;当日为阳线;股价小于200;" +
				"例1：创新药,半导体;PE<30;净利润增长率>50%。 按成交量从高到低排序。" +
				"例2：上证指数,科创50。 " +
				"例3：长电科技,上海贝岭。" +
				"例4：长电科技,上海贝岭;KDJ,MACD,RSI,BOLL,主力资金。" +
				"例5：换手率大于3%小于25%.量比1以上. 10日内有过涨停.股价处于峰值的二分之一以下.流通股本<100亿.当日和连续四日净流入;股价在20日均线以上.分时图股价在均线之上.热门板块下涨幅领先的A股. 当日量能20000手以上.沪深个股.近一年市盈率波动小于150%.MACD金叉;不要ST股及不要退市股，非北交所，每股收益>0。按成交量从高到低排序。" +
				"例6：沪深主板.流通市值小于100亿.市值大于10亿.60分钟dif大于dea.60分钟skdj指标k值大于d值.skdj指标k值小于90.换手率大于3%.成交额大于1亿元.量比大于2.涨幅大于2%小于7%.股价大于5小于50.创业板.10日均线大于20日均线;不要ST股及不要退市股;不要北交所;不要科创板;不要创业板。按成交量从高到低排序。" +
				"例7：股价在20日线上，一月之内涨停次数>=1，量比大于1，换手率大于3%。按成交量从高到低排序。" +
				"例8：基本条件：前期有爆量，回调到 10 日线，当日是缩量阴线，均线趋势向上。;优选条件：一月之内涨停次数>=1。按成交量从高到低排序。",
			Parameters: &models.FunctionParameters{
				Type: "object",
				Properties: map[string]any{
					"words": map[string]any{
						"type": "string",
						"description": "选股自然语言。" +
							"例如:分析强势方向：10点半之前涨停，非一字板，行业概念，按成交量从高到低排序。" +
							"例如:查看涨停板：涨停板，按涨幅从高到低排序。" +
							"例如:查看跌停板：跌停板，按跌幅从高到低排序。" +
							"例如:查看龙虎榜：龙虎榜，按涨幅从高到低排序。" +
							"例如:查看昨日龙虎榜：昨日龙虎榜。" +
							"例如:查看板块龙头行情：板块/概念龙头，按涨幅从高到低排序。" +
							"例如:查看板块龙头行情：龙头股，按成交量从高到低排序。" +
							"例如:查看技术指标：上海贝岭,macd,rsi,kdj,boll,5日均线,14日均线,30日均线,60日均线,成交量,OBV,EMA。" +
							"例如:查看近期趋势：量比连续2天>1，主力连续2日净流入且递增，主力净额>3000万元，行业，股价在20日线上。按成交量从高到低排序。" +
							"例如:当日成交量 ≥ 近 5 日平均成交量 ×1.5，收盘价 ≥ 20 日均线，20 日均线 ≥ 60 日均线，当日涨幅 3%-7%， 3日主力资金净流入累计≥5000 万元，当日换手率 5%-15%，筹码集中度（90% 筹码峰）≤15%，非创业板非科创板非ST非北交所，行业。按成交量从高到低排序。" +
							"例如:查看有潜力的成交量爆发股：最近7日成交量量比大于3，出现过一次，非ST。按成交量从高到低排序。" +
							"例如:成交量筛选：当日成交量大于前一日成交量的1.8倍;当日最高价创60日新高;当日收盘价大于5日均线;当日为阳线;股价小于200;" +
							"例1：创新药,半导体;PE<30;净利润增长率>50%。 按成交量从高到低排序。" +
							"例2：上证指数,科创50。 " +
							"例3：长电科技,上海贝岭。" +
							"例4：长电科技,上海贝岭;KDJ,MACD,RSI,BOLL,主力资金。" +
							"例5：换手率大于3%小于25%.量比1以上. 10日内有过涨停.股价处于峰值的二分之一以下.流通股本<100亿.当日和连续四日净流入;股价在20日均线以上.分时图股价在均线之上.热门板块下涨幅领先的A股. 当日量能20000手以上.沪深个股.近一年市盈率波动小于150%.MACD金叉;不要ST股及不要退市股，非北交所，每股收益>0。按成交量从高到低排序。" +
							"例6：沪深主板.流通市值小于100亿.市值大于10亿.60分钟dif大于dea.60分钟skdj指标k值大于d值.skdj指标k值小于90.换手率大于3%.成交额大于1亿元.量比大于2.涨幅大于2%小于7%.股价大于5小于50.创业板.10日均线大于20日均线;不要ST股及不要退市股;不要北交所;不要科创板;不要创业板。按成交量从高到低排序。" +
							"例7：股价在20日线上，一月之内涨停次数>=1，量比大于1，换手率大于3%。按成交量从高到低排序。" +
							"例8：基本条件：前期有爆量，回调到 10 日线，当日是缩量阴线，均线趋势向上。;优选条件：一月之内涨停次数>=1。按成交量从高到低排序。",
					},
				},
				Required: []string{"words"},
			},
		},
	})

	tools = append(tools, models.Tool{
		Type: "function",
		Function: models.ToolFunction{
			Name: "SearchBk",
			Description: "根据自然语言查询板块/概念/指数整体数据。" +
				"例如:近3日涨停家数>5的概念板块。" +
				"例如:WR买入信号板块" +
				"例如:WR卖出信号板块" +
				"例如:存储芯片，成分股" +
				"例如:查看指数：上证指数，深证成指，创业板指，科创50。" +
				"例如:查看指数：上证50，沪深300，中证 500，中证1000。" +
				"例如:查看存储芯片板块：存储芯片。" +
				"例如:查看概念板块排名：今日涨幅前15的概念板块。" +
				"例如:查看概念板块排名：今日净流入前15的概念板块。" +
				"例如:查看行业排名：今日涨幅前15的行业板块。" +
				"例如:查看行业排名：今日净流入前15的行业板块。" +
				"例如:查看板块/概念排名数据：今日主力净流出前15的概念板块。" +
				"例如:查看板块板块/概念：今日成交量前15的概念板块。" +
				"例如:查看板块/概念排名数据：今日主力净流出前15的行业板块。" +
				"例如:查看板块板块/概念：今日成交量前15的行业板块。" +
				"例如:通过市盈率查询板块：当前市盈率介于30-50的板块/概念。",

			Parameters: &models.FunctionParameters{
				Type: "object",
				Properties: map[string]any{
					"words": map[string]any{
						"type": "string",
						"description": "板块/概念数据查询自然语言。" +
							"例如:近3日涨停家数>5的概念板块。" +
							"例如:WR买入信号板块" +
							"例如:WR卖出信号板块" +
							"例如:存储芯片，成分股" +
							"例如:查看指数：上证指数，深证成指，创业板指，科创50。" +
							"例如:查看指数：上证50，沪深300，中证 500，中证1000。" +
							"例如:查看存储芯片板块：存储芯片。" +
							"例如:查看概念排名：今日涨幅前15的概念板块。" +
							"例如:查看概念排名：今日净流入前15的概念板块。" +
							"例如:查看行业排名：今日涨幅前15的行业板块。" +
							"例如:查看行业排名：今日净流入前15的行业板块。" +
							"例如:查看板块/概念排名数据：今日主力净流出前15的概念板块。" +
							"例如:查看板块板块/概念：今日成交量前15的概念板块。" +
							"例如:查看板块/概念排名数据：今日主力净流出前15的行业板块。" +
							"例如:查看板块板块/概念：今日成交量前15的行业板块。" +
							"例如:通过市盈率查询板块：当前市盈率介于30-50的板块/概念。",
					},
				},
				Required: []string{"words"},
			},
		},
	})

	tools = append(tools, models.Tool{
		Type: "function",
		Function: models.ToolFunction{
			Name: "SearchETF",
			Description: "根据自然语言查询etf数据。" +
				"例如:创新药或者机器人，按涨幅排序，前50。" +
				"例如:溢价率介于0%~10%之间，前50。" +
				"例如:3日涨幅前50的ETF。" +
				"例如:3日跌幅前50的ETF。" +
				"例如:今日涨幅前50的ETF。",

			Parameters: &models.FunctionParameters{
				Type: "object",
				Properties: map[string]any{
					"words": map[string]any{
						"type": "string",
						"description": "板块/概念数据查询ETF。" +
							"例如:创新药或者机器人，按涨幅排序，前50。" +
							"例如:溢价率介于0%~10%之间，前50。" +
							"例如:3日涨幅前50的ETF。" +
							"例如:3日跌幅前50的ETF。" +
							"例如:今日涨幅前50的ETF。",
					},
				},
				Required: []string{"words"},
			},
		},
	})

	tools = append(tools, models.Tool{
		Type: "function",
		Function: models.ToolFunction{
			Name:        "GetStockKLine",
			Description: "获取股票日K线数据。",
			Parameters: &models.FunctionParameters{
				Type: "object",
				Properties: map[string]any{
					"days": map[string]any{
						"type":        "string",
						"description": "日K数据条数",
					},
					"stockCode": map[string]any{
						"type":        "string",
						"description": "股票代码（A股：sh,sz开头;港股hk开头,美股：us开头）",
					},
				},
				Required: []string{"days", "stockCode"},
			},
		},
	})

	tools = append(tools, models.Tool{
		Type: "function",
		Function: models.ToolFunction{
			Name:        "InteractiveAnswer",
			Description: "获取投资者与上市公司互动问答的数据,反映当前投资者关注的热点问题",
			Parameters: &models.FunctionParameters{
				Type: "object",
				Properties: map[string]any{
					"page": map[string]any{
						"type":        "string",
						"description": "分页号",
					},
					"pageSize": map[string]any{
						"type":        "string",
						"description": "分页大小",
					},
					"keyWord": map[string]any{
						"type":        "string",
						"description": "搜索关键词（可输入股票名称或者当前热门板块/行业/概念/标的/事件等）",
					},
				},
				Required: []string{"page", "pageSize"},
			},
		},
	})

	//tools = append(tools, data.Tool{
	//	Type: "function",
	//	Function: data.ToolFunction{
	//		Name:        "QueryBKDictInfo",
	//		Description: "获取所有板块/行业名称或者代码(bkCode,bkName)",
	//	},
	//})

	//tools = append(tools, data.Tool{
	//	Type: "function",
	//	Function: data.ToolFunction{
	//		Name:        "GetIndustryResearchReport",
	//		Description: "获取行业/板块研究报告,请先使用QueryBKDictInfo工具获取行业代码，然后输入行业代码调用",
	//		Parameters: data.FunctionParameters{
	//			Type: "object",
	//			Properties: map[string]any{
	//				"bkCode": map[string]any{
	//					"type":        "string",
	//					"description": "板块/行业代码",
	//				},
	//			},
	//			Required: []string{"bkCode"},
	//		},
	//	},
	//})

	tools = append(tools, models.Tool{
		Type: "function",
		Function: models.ToolFunction{
			Name:        "GetStockResearchReport",
			Description: "获取市场分析师的股票研究报告",
			Parameters: &models.FunctionParameters{
				Type: "object",
				Properties: map[string]any{
					"stockCode": map[string]any{
						"type":        "string",
						"description": "股票代码",
					},
				},
				Required: []string{"stockCode"},
			},
		},
	})

	tools = append(tools, models.Tool{
		Type: "function",
		Function: models.ToolFunction{
			Name:        "HotStockTable",
			Description: "当前热门股票排名",
			Parameters: &models.FunctionParameters{
				Type: "object",
				Properties: map[string]any{
					"pageSize": map[string]any{
						"type":        "string",
						"description": "分页大小",
					},
				},
				Required: []string{"pageSize"},
			},
		},
	})

	tools = append(tools, models.Tool{
		Type: "function",
		Function: models.ToolFunction{
			Name:        "GetStockMoneyData",
			Description: "今日股票资金流入排名",
			Parameters: &models.FunctionParameters{
				Type: "object",
				Properties: map[string]any{
					"pageSize": map[string]any{
						"type":        "string",
						"description": "分页大小",
					},
				},
				Required: []string{"pageSize"},
			},
		},
	})
	tools = append(tools, models.Tool{
		Type: "function",
		Function: models.ToolFunction{
			Name:        "GetStockConceptInfo",
			Description: "获取股票所属概念详细信息",
			Parameters: &models.FunctionParameters{
				Type: "object",
				Properties: map[string]any{
					"code": map[string]any{
						"type":        "string",
						"description": "股票代码,如：601138.SH。注意 上海证券交易所股票以.SH结尾，深圳证券交易所股票以.SZ结尾，港股股票以.HK结尾，北交所股票以.BJ结尾，",
					},
				},
				Required: []string{"code"},
			},
		},
	})

	tools = append(tools, models.Tool{
		Type: "function",
		Function: models.ToolFunction{
			Name:        "GetStockFinancialInfo",
			Description: "获取股票财务报表信息",
			Parameters: &models.FunctionParameters{
				Type: "object",
				Properties: map[string]any{
					"stockCode": map[string]any{
						"type":        "string",
						"description": "股票代码,如：601138.SH。注意 上海证券交易所股票以.SH结尾，深圳证券交易所股票以.SZ结尾，港股股票以.HK结尾，北交所股票以.BJ结尾，",
					},
				},
				Required: []string{"stockCode"},
			},
		},
	})
	tools = append(tools, models.Tool{
		Type: "function",
		Function: models.ToolFunction{
			Name:        "GetStockHolderNum",
			Description: "获取股票股东人数信息(股东人数与股价比( 注:股票价格通常与股东人数成反比，股东人数越少代表筹码越集中，股价越有可能上涨))",
			Parameters: &models.FunctionParameters{
				Type: "object",
				Properties: map[string]any{
					"stockCode": map[string]any{
						"type":        "string",
						"description": "股票代码,如：601138.SH。注意 上海证券交易所股票以.SH结尾，深圳证券交易所股票以.SZ结尾，港股股票以.HK结尾，北交所股票以.BJ结尾，",
					},
				},
				Required: []string{"stockCode"},
			},
		},
	})

	return tools
}

func (a *App) addCronTask(follow models.FollowedStock) func() {
	return func() {
		go emitEvent(a.ctx, "warnMsg", "开始自动分析"+follow.Name+"_"+follow.StockCode)
		var res strings.Builder

		chatId := ""
		question := ""
		order := a.services.AI.ResolveAIFallbackOrder(follow.AiConfigId)
		for idx, targetAIConfigID := range order {
			msgs := a.services.AI.NewChatStream(a.ctx, follow.Name, follow.StockCode, "", targetAIConfigID, nil, a.AiTools, true)
			currentMsgs := make([]map[string]any, 0, 128)
			var currentRes strings.Builder
			currentChatID := ""
			currentQuestion := ""
			for msg := range msgs {
				currentMsgs = append(currentMsgs, msg)
				if normalizeMsgCode(msg["code"]) == 0 {
					continue
				}
				if msg["extraContent"] != nil {
					currentRes.WriteString(msg["extraContent"].(string) + "\n")
				}
				if msg["content"] != nil {
					currentRes.WriteString(msg["content"].(string))
				}
				if msg["chatId"] != nil {
					currentChatID = msg["chatId"].(string)
				}
				if msg["question"] != nil {
					currentQuestion = msg["question"].(string)
				}
			}
			if strings.TrimSpace(currentRes.String()) != "" {
				res = currentRes
				chatId = currentChatID
				question = currentQuestion
				follow.AiConfigId = targetAIConfigID
				break
			}
			if !shouldChatFailover(currentMsgs) {
				res = currentRes
				chatId = currentChatID
				question = currentQuestion
				follow.AiConfigId = targetAIConfigID
				break
			}
			if idx < len(order)-1 {
				logger.SugaredLogger.Warnf("定时股票分析失败，自动切换备用模型。from=%d to=%d attempt=%d", targetAIConfigID, order[idx+1], idx+2)
			}
		}

		a.services.AI.SaveAIResponseResult(a.ctx, follow.StockCode, follow.Name, res.String(), chatId, question, follow.AiConfigId)
		go emitEvent(a.ctx, "warnMsg", "AI分析完成："+follow.Name+"_"+follow.StockCode)

	}
}

func refreshTelegraphList() *[]string {
	url := "https://www.cls.cn/telegraph"
	response, err := resty.New().R().
		SetHeader("Referer", "https://www.cls.cn/").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/117.0.0.0 Safari/537.36 Edg/117.0.2045.60").
		Get(url)
	if err != nil {
		return &[]string{}
	}
	//logger.SugaredLogger.Info(string(response.Body()))
	document, err := goquery.NewDocumentFromReader(strings.NewReader(string(response.Body())))
	if err != nil {
		return &[]string{}
	}
	var telegraph []string
	document.Find("div.telegraph-content-box").Each(func(i int, selection *goquery.Selection) {
		//logger.SugaredLogger.Info(selection.Text())
		telegraph = append(telegraph, selection.Text())
	})
	return &telegraph
}

// isTradingDay 判断是否是交易日
func isTradingDay(date time.Time) bool {
	weekday := date.Weekday()
	// 判断是否是周末
	if weekday == time.Saturday || weekday == time.Sunday {
		return false
	}
	// 这里可以添加具体的节假日判断逻辑
	// 例如：判断是否是春节、国庆节等
	return true
}

// isTradingTime 判断是否是交易时间
func isTradingTime(date time.Time) bool {
	if !isTradingDay(date) {
		return false
	}

	hour, minute, _ := date.Clock()

	// 判断是否在9:15到11:30之间
	if (hour == 9 && minute >= 15) || (hour == 10) || (hour == 11 && minute <= 30) {
		return true
	}

	// 判断是否在13:00到15:00之间
	if (hour == 13) || (hour == 14) || (hour == 15 && minute <= 0) {
		return true
	}

	return false
}

// IsHKTradingTime 判断当前时间是否在港股交易时间内
func IsHKTradingTime(date time.Time) bool {
	hour, minute, _ := date.Clock()

	// 开市前竞价时段：09:00 - 09:30
	if (hour == 9 && minute >= 0) || (hour == 9 && minute <= 30) {
		return true
	}

	// 上午持续交易时段：09:30 - 12:00
	if (hour == 9 && minute > 30) || (hour >= 10 && hour < 12) || (hour == 12 && minute == 0) {
		return true
	}

	// 下午持续交易时段：13:00 - 16:00
	if (hour == 13 && minute >= 0) || (hour >= 14 && hour < 16) || (hour == 16 && minute == 0) {
		return true
	}

	// 收市竞价交易时段：16:00 - 16:10
	if (hour == 16 && minute >= 0) || (hour == 16 && minute <= 10) {
		return true
	}
	return false
}

// IsUSTradingTime 判断当前时间是否在美股交易时间内
func IsUSTradingTime(date time.Time) bool {
	// 获取美国东部时区
	est, err := time.LoadLocation("America/New_York")
	var estTime time.Time
	if err != nil {
		estTime = date.Add(time.Hour * -12)
	} else {
		// 将当前时间转换为美国东部时间
		estTime = date.In(est)
	}

	// 判断是否是周末
	weekday := estTime.Weekday()
	if weekday == time.Saturday || weekday == time.Sunday {
		return false
	}

	// 获取小时和分钟
	hour, minute, _ := estTime.Clock()

	// 判断是否在4:00 AM到9:30 AM之间（盘前）
	if (hour == 4) || (hour == 5) || (hour == 6) || (hour == 7) || (hour == 8) || (hour == 9 && minute < 30) {
		return true
	}

	// 判断是否在9:30 AM到4:00 PM之间（盘中）
	if (hour == 9 && minute >= 30) || (hour >= 10 && hour < 16) || (hour == 16 && minute == 0) {
		return true
	}

	// 判断是否在4:00 PM到8:00 PM之间（盘后）
	if (hour == 16 && minute > 0) || (hour >= 17 && hour < 20) || (hour == 20 && minute == 0) {
		return true
	}

	return false
}
func MonitorFundPrices(a *App) {
	for _, follow := range a.services.Fund.GetFollowedFund() {
		_, err := a.services.Fund.CrawlFundBasic(follow.Code)
		if err != nil {
			logger.SugaredLogger.Errorf("获取基金基本信息失败，基金代码：%s，错误信息：%s", follow.Code, err.Error())
			continue
		}
		a.services.Fund.CrawlFundNetEstimatedUnit(follow.Code)
		a.services.Fund.CrawlFundNetUnitValue(follow.Code)
	}
}

// shutdown is called at application termination
func (a *App) shutdown(ctx context.Context) {
	defer PanicHandler()
	// Perform your teardown here
	//os.Exit(0)
	logger.SugaredLogger.Infof("application shutdown Version:%s", Version)
}

func resolveChatTools(enableTools bool, tools []models.Tool) []models.Tool {
	if enableTools {
		return tools
	}
	return []models.Tool{}
}

func shouldChatFailover(msgs []map[string]any) bool {
	if len(msgs) == 0 {
		return true
	}
	errs := make([]string, 0)
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		if content, ok := msg["content"].(string); ok && strings.TrimSpace(content) != "" {
			if code, exists := msg["code"]; !exists || normalizeMsgCode(code) != 0 {
				return false
			}
			errs = append(errs, content)
		}
		if extraContent, ok := msg["extraContent"].(string); ok && strings.TrimSpace(extraContent) != "" {
			return false
		}
	}
	if chatAttemptHasVisibleContent(msgs) {
		return false
	}
	if len(errs) == 0 {
		return true
	}
	for _, message := range errs {
		lower := strings.ToLower(message)
		for _, marker := range []string{"timeout", "connection", "429", "rate limit", "502", "503", "504", "unauthorized", "forbidden"} {
			if strings.Contains(lower, marker) {
				return true
			}
		}
	}
	return false
}

func chatAttemptHasVisibleContent(msgs []map[string]any) bool {
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		if content, ok := msg["content"].(string); ok && strings.TrimSpace(content) != "" {
			if code, exists := msg["code"]; exists && normalizeMsgCode(code) == 0 {
				continue
			}
			return true
		}
		if extraContent, ok := msg["extraContent"].(string); ok && strings.TrimSpace(extraContent) != "" {
			return true
		}
	}
	return false
}

func normalizeMsgCode(codeAny any) int {
	switch v := codeAny.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return 1
}

//// checkChromeOnWindows 在 Windows 系统上检查谷歌浏览器是否安装
//func checkChromeOnWindows() bool {
//	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\chrome.exe`, registry.QUERY_VALUE)
//	if err != nil {
//		// 尝试在 WOW6432Node 中查找（适用于 64 位系统上的 32 位程序）
//		key, err = registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\App Paths\chrome.exe`, registry.QUERY_VALUE)
//		if err != nil {
//			return false
//		}
//		defer key.Close()
//	}
//	defer key.Close()
//	_, _, err = key.GetValue("Path", nil)
//	return err == nil
//}
//
//// checkEdgeOnWindows 在 Windows 系统上检查Edge浏览器是否安装，并返回安装路径
//func checkEdgeOnWindows() (string, bool) {
//	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\msedge.exe`, registry.QUERY_VALUE)
//	if err != nil {
//		// 尝试在 WOW6432Node 中查找（适用于 64 位系统上的 32 位程序）
//		key, err = registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\App Paths\msedge.exe`, registry.QUERY_VALUE)
//		if err != nil {
//			return "", false
//		}
//		defer key.Close()
//	}
//	defer key.Close()
//	path, _, err := key.GetStringValue("Path")
//	if err != nil {
//		return "", false
//	}
//	return path, true
//}
