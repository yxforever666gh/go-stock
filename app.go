package main

import (
	"context"
	"go-stock/backend/data"
	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/models"
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
	ctx                context.Context
	cache              *freecache.Cache
	cron               *cron.Cron
	cronEntrys         map[string]cron.EntryID
	cronEntrysMu       sync.RWMutex
	AiTools            []data.Tool
	services           service.AppServices
	agentSessions      map[string]*AgentSession
	agentSessionsMu    sync.RWMutex
	summaryTaskMu      sync.Mutex
	summaryTaskBusy    bool
	yieldEmailTaskMu   sync.Mutex
	yieldEmailTaskBusy bool
	yieldEmailCronMu   sync.Mutex
	domReadyMu         sync.Mutex
	domReadyDone       bool
}

const defaultMarketSummaryCronTimes = "09:30,11:30,18:00"
const summaryStockNewsEntryPrefix = "SummaryStockNewsCustom_"
const summaryStockNewsTestEntryKey = "SummaryStockNewsTest_1min"
const yieldEmailCronEntryPrefix = "YieldEmailCustom_"

// NewApp creates a new App application struct
func NewApp() *App {
	return NewAppWithServices(service.NewAppServices())
}

func NewAppWithServices(services service.AppServices) *App {
	cacheSize := 512 * 1024
	cache := freecache.NewCache(cacheSize)
	c := cron.New(cron.WithSeconds())
	c.Start()
	var tools []data.Tool
	tools = AddTools(tools)
	return &App{
		cache:         cache,
		cron:          c,
		cronEntrys:    make(map[string]cron.EntryID),
		AiTools:       tools,
		services:      services,
		agentSessions: make(map[string]*AgentSession),
	}
}

func (a *App) setCronEntry(key string, entryID cron.EntryID) {
	a.cronEntrysMu.Lock()
	defer a.cronEntrysMu.Unlock()
	a.cronEntrys[key] = entryID
}

func (a *App) getCronEntry(key string) (cron.EntryID, bool) {
	a.cronEntrysMu.RLock()
	defer a.cronEntrysMu.RUnlock()
	entryID, ok := a.cronEntrys[key]
	return entryID, ok
}

func (a *App) deleteCronEntry(key string) {
	a.cronEntrysMu.Lock()
	defer a.cronEntrysMu.Unlock()
	delete(a.cronEntrys, key)
}

func (a *App) snapshotCronEntries() map[string]cron.EntryID {
	a.cronEntrysMu.RLock()
	defer a.cronEntrysMu.RUnlock()
	entries := make(map[string]cron.EntryID, len(a.cronEntrys))
	for key, entryID := range a.cronEntrys {
		entries[key] = entryID
	}
	return entries
}

func (a *App) tryAcquireSummaryTask() bool {
	a.summaryTaskMu.Lock()
	defer a.summaryTaskMu.Unlock()
	if a.summaryTaskBusy {
		return false
	}
	a.summaryTaskBusy = true
	return true
}

func (a *App) isSummaryTaskBusy() bool {
	a.summaryTaskMu.Lock()
	defer a.summaryTaskMu.Unlock()
	return a.summaryTaskBusy
}

func (a *App) releaseSummaryTask() {
	a.summaryTaskMu.Lock()
	a.summaryTaskBusy = false
	a.summaryTaskMu.Unlock()
}

func (a *App) tryAcquireYieldEmailTask() bool {
	a.yieldEmailTaskMu.Lock()
	defer a.yieldEmailTaskMu.Unlock()
	if a.yieldEmailTaskBusy {
		return false
	}
	a.yieldEmailTaskBusy = true
	return true
}

func (a *App) isYieldEmailTaskBusy() bool {
	a.yieldEmailTaskMu.Lock()
	defer a.yieldEmailTaskMu.Unlock()
	return a.yieldEmailTaskBusy
}

func (a *App) releaseYieldEmailTask() {
	a.yieldEmailTaskMu.Lock()
	a.yieldEmailTaskBusy = false
	a.yieldEmailTaskMu.Unlock()
}

func (a *App) tryMarkDomReadyDone() bool {
	a.domReadyMu.Lock()
	defer a.domReadyMu.Unlock()
	if a.domReadyDone {
		return false
	}
	a.domReadyDone = true
	return true
}

func (a *App) withYieldEmailTaskLock(taskName string, fn func() string) string {
	if !a.tryAcquireYieldEmailTask() {
		logger.SugaredLogger.Warnf("跳过邮件发送任务: task=%s reason=上一次邮件任务仍在执行", taskName)
		return "发送失败: 上一次邮件任务仍在执行"
	}
	defer a.releaseYieldEmailTask()

	logger.SugaredLogger.Infof("开始执行邮件发送任务: task=%s", taskName)
	return fn()
}

func AddTools(tools []data.Tool) []data.Tool {
	tools = append(tools, data.Tool{
		Type: "function",
		Function: data.ToolFunction{
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
				"例如:超短线策略：当日成交量大于前一日成交量的1.8倍;当日最高价创60日新高当日收盘价大于5日均线;当日为阳线;股价小于200;" +
				"例1：创新药,半导体;PE<30;净利润增长率>50%。 按成交量从高到低排序。" +
				"例2：上证指数,科创50。 " +
				"例3：长电科技,上海贝岭。" +
				"例4：长电科技,上海贝岭;KDJ,MACD,RSI,BOLL,主力资金。" +
				"例5：换手率大于3%小于25%.量比1以上. 10日内有过涨停.股价处于峰值的二分之一以下.流通股本<100亿.当日和连续四日净流入;股价在20日均线以上.分时图股价在均线之上.热门板块下涨幅领先的A股. 当日量能20000手以上.沪深个股.近一年市盈率波动小于150%.MACD金叉;不要ST股及不要退市股，非北交所，每股收益>0。按成交量从高到低排序。" +
				"例6：沪深主板.流通市值小于100亿.市值大于10亿.60分钟dif大于dea.60分钟skdj指标k值大于d值.skdj指标k值小于90.换手率大于3%.成交额大于1亿元.量比大于2.涨幅大于2%小于7%.股价大于5小于50.创业板.10日均线大于20日均线;不要ST股及不要退市股;不要北交所;不要科创板;不要创业板。按成交量从高到低排序。" +
				"例7：股价在20日线上，一月之内涨停次数>=1，量比大于1，换手率大于3%。按成交量从高到低排序。" +
				"例8：基本条件：前期有爆量，回调到 10 日线，当日是缩量阴线，均线趋势向上。;优选条件：一月之内涨停次数>=1。按成交量从高到低排序。",
			Parameters: &data.FunctionParameters{
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
							"例如:超短线策略：当日成交量大于前一日成交量的1.8倍;当日最高价创60日新高当日收盘价大于5日均线;当日为阳线;股价小于200;" +
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

	tools = append(tools, data.Tool{
		Type: "function",
		Function: data.ToolFunction{
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

			Parameters: &data.FunctionParameters{
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

	tools = append(tools, data.Tool{
		Type: "function",
		Function: data.ToolFunction{
			Name: "SearchETF",
			Description: "根据自然语言查询etf数据。" +
				"例如:创新药或者机器人，按涨幅排序，前50。" +
				"例如:溢价率介于0%~10%之间，前50。" +
				"例如:3日涨幅前50的ETF。" +
				"例如:3日跌幅前50的ETF。" +
				"例如:今日涨幅前50的ETF。",

			Parameters: &data.FunctionParameters{
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

	tools = append(tools, data.Tool{
		Type: "function",
		Function: data.ToolFunction{
			Name:        "GetStockKLine",
			Description: "获取股票日K线数据。",
			Parameters: &data.FunctionParameters{
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

	tools = append(tools, data.Tool{
		Type: "function",
		Function: data.ToolFunction{
			Name:        "InteractiveAnswer",
			Description: "获取投资者与上市公司互动问答的数据,反映当前投资者关注的热点问题",
			Parameters: &data.FunctionParameters{
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

	tools = append(tools, data.Tool{
		Type: "function",
		Function: data.ToolFunction{
			Name:        "GetStockResearchReport",
			Description: "获取市场分析师的股票研究报告",
			Parameters: &data.FunctionParameters{
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

	tools = append(tools, data.Tool{
		Type: "function",
		Function: data.ToolFunction{
			Name:        "HotStrategyTable",
			Description: "获取当前热门选股策略",
		},
	})

	tools = append(tools, data.Tool{
		Type: "function",
		Function: data.ToolFunction{
			Name:        "HotStockTable",
			Description: "当前热门股票排名",
			Parameters: &data.FunctionParameters{
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

	tools = append(tools, data.Tool{
		Type: "function",
		Function: data.ToolFunction{
			Name:        "GetStockMoneyData",
			Description: "今日股票资金流入排名",
			Parameters: &data.FunctionParameters{
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
	tools = append(tools, data.Tool{
		Type: "function",
		Function: data.ToolFunction{
			Name:        "GetStockConceptInfo",
			Description: "获取股票所属概念详细信息",
			Parameters: &data.FunctionParameters{
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

	tools = append(tools, data.Tool{
		Type: "function",
		Function: data.ToolFunction{
			Name:        "GetStockFinancialInfo",
			Description: "获取股票财务报表信息",
			Parameters: &data.FunctionParameters{
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
	tools = append(tools, data.Tool{
		Type: "function",
		Function: data.ToolFunction{
			Name:        "GetStockHolderNum",
			Description: "获取股票股东人数信息(股东人数与股价比( 注:股票价格通常与股东人数成反比，股东人数越少代表筹码越集中，股价越有可能上涨))",
			Parameters: &data.FunctionParameters{
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

	//CreateAiRecommendStocks
	tools = append(tools, data.Tool{
		Type: "function",
		Function: data.ToolFunction{
			Name:        "CreateAiRecommendStocks",
			Description: "创建/保存AI推荐股票记录。必须优先输出结构化决策字段：分类、核心催化、关键证据、失效条件、观察价、关注位、止盈区间、止损位、预期周期、4维置信度。执行语义已统一为“等待激活”，不再允许输出立刻买入/低吸/右侧确认等标签。推荐理由里必须写出硬格式交易计划：买入依据需明确为“价格触发：...；量能触发：...；逻辑触发：...”，失效条件需明确为“时间失效：...；价格失效：...；逻辑失效：...”，并把量能条件量化到价位、周期、比较基准、阈值。第二阶段要求优先补充 evidenceSources JSON 字符串，标明来源名称、来源类型、信任级别、时效级别。只有满足至少两类证据且至少一条高信任证据时，才允许进入等待激活计划；证据不足或存在冲突时应直接回避。AI总结场景下若证据核验层提供了集合竞价或实时分钟线价格锚点，应围绕该价格锚点定价。",
			Parameters: &data.FunctionParameters{
				Type: "object",
				Properties: map[string]any{
					"modelName": map[string]any{
						"type":        "string",
						"description": "模型名称",
					},
					"stockCode": map[string]any{
						"type":        "string",
						"description": "股票代码,如：601138.SH。注意 上海证券交易所股票以.SH结尾，深圳证券交易所股票以.SZ结尾，港股股票以.HK结尾，北交所股票以.BJ结尾，",
					},
					"stockName": map[string]any{
						"type":        "string",
						"description": "股票名称",
					},
					"bkCode": map[string]any{
						"type":        "string",
						"description": "板块/行业代码",
					},
					"bkName": map[string]any{
						"type":        "string",
						"description": "板块/概念/行业名称",
					},
					"stockPrice": map[string]any{
						"type":        "string",
						"description": "推荐时股票价格。AI总结场景下应优先填写当前价格锚点：集合竞价时优先auctionPrice，开盘后优先实时分钟线最新价；两者缺失时才回退到CurrentPrice/实时行情价。",
					},
					"stockPrePrice": map[string]any{
						"type":        "string",
						"description": "前一交易日股票价格",
					},
					"stockClosePrice": map[string]any{
						"type":        "string",
						"description": "推荐时股票收盘价格",
					},
					"recommendReason": map[string]any{
						"type":        "string",
						"description": "推荐理由/驱动因素/逻辑。必须包含结构化交易计划文本，至少写出：买入依据：价格触发...；量能触发...；逻辑触发...。失效条件：时间失效...；价格失效...；逻辑失效...。禁止只写“放量/缩量/强势/承接/不追”等抽象词，必须写清价位、周期、比较基准、阈值。",
					},
					"recommendBuyPrice": map[string]any{
						"type":        "string",
						"description": "ai建议买入价区间最低价和最高价之间用`-`分隔。AI总结场景下必须围绕当前价格锚点生成：竞价时段围绕auctionPrice，开盘后围绕实时分钟线价格，不能明显偏离当前价。",
					},
					"recommendBuyPriceMax": map[string]any{
						"type":        "number",
						"description": "ai建议最高买入价",
					},
					"recommendBuyPriceMin": map[string]any{
						"type":        "number",
						"description": "ai建议最低买入价",
					},
					"recommendStopProfitPrice": map[string]any{
						"type":        "string",
						"description": "ai建议止盈价区间最低价和最高价之间用`-`分隔",
					},
					"recommendStopProfitPriceMax": map[string]any{
						"type":        "number",
						"description": "ai建议最高止盈价",
					},
					"recommendStopProfitPriceMin": map[string]any{
						"type":        "number",
						"description": "ai建议最低止盈价",
					},

					"recommendStopLossPrice": map[string]any{
						"type":        "string",
						"description": "ai建议止损价",
					},
					"recommendCategory": map[string]any{
						"type":        "string",
						"description": "推荐分类。未来新记录默认只允许 conditional/等待激活；若结论明确回避，才允许 avoid。",
					},
					"coreCatalyst": map[string]any{
						"type":        "string",
						"description": "核心催化，不能写空泛观点",
					},
					"keyEvidence": map[string]any{
						"type":        "string",
						"description": "关键证据，必须带证据标签，如：[市场资讯]... [个股新闻]... [行业研报]... [财报/财务]... [互动易]... [技术/资金/形态]... [一级披露]... [资金结构]... [股东/筹码]... [产业高频]... [海外风险]...",
					},
					"evidenceSources": map[string]any{
						"type":        "string",
						"description": "可选但强烈建议传入 JSON 字符串数组。每项至少包含 type 和 summary，建议补充 sourceName、sourceType(原始披露/聚合媒体/数据接口/高频指标)、trustLevel(high/medium/low)、latencyLevel(realtime/daily/periodic)、title、url、publishedAt。",
					},
					"invalidCondition": map[string]any{
						"type":        "string",
						"description": "失效条件。必须写成：时间失效：...；价格失效：...；逻辑失效：...，不能只写“逻辑走弱”“不及预期”等空泛表述。",
					},
					"observePrice": map[string]any{
						"type":        "string",
						"description": "观察价。AI总结场景下应优先等于或围绕当前价格锚点；竞价时段优先auctionPrice，开盘后优先实时分钟线最新价。",
					},
					"focusPrice": map[string]any{
						"type":        "string",
						"description": "关注位/建仓区间，建议与recommendBuyPrice保持一致。AI总结场景下应围绕当前价格锚点生成；竞价时段优先auctionPrice，开盘后优先实时分钟线最新价。",
					},
					"expectedCycle": map[string]any{
						"type":        "string",
						"description": "预期周期，如1-3天、1-2周",
					},
					"eventStrength": map[string]any{
						"type":        "integer",
						"description": "事件强度，0-100整数",
					},
					"capitalConfirmation": map[string]any{
						"type":        "integer",
						"description": "资金确认度，0-100整数",
					},
					"fundamentalFit": map[string]any{
						"type":        "integer",
						"description": "基本面匹配度，0-100整数",
					},
					"technicalFit": map[string]any{
						"type":        "integer",
						"description": "技术面匹配度，0-100整数",
					},
					"riskRemarks": map[string]any{
						"type":        "string",
						"description": "风险提示/风险点",
					},
					"remarks": map[string]any{
						"type":        "string",
						"description": "操作总结/备注",
					},
				},
				Required: []string{"stockCode", "stockName", "bkName", "recommendCategory", "coreCatalyst", "keyEvidence", "riskRemarks", "invalidCondition", "observePrice", "focusPrice", "recommendBuyPrice", "recommendStopProfitPrice", "recommendStopLossPrice", "expectedCycle", "eventStrength", "capitalConfirmation", "fundamentalFit", "technicalFit"},
			},
		},
	})

	//BatchCreateAiRecommendStocks
	tools = append(tools, data.Tool{
		Type: "function",
		Function: data.ToolFunction{
			Name:        "BatchCreateAiRecommendStocks",
			Description: "批量创建/保存AI推荐股票记录。每条记录都必须包含结构化决策字段：分类、核心催化、关键证据、失效条件、观察价、关注位、止盈区间、止损位、预期周期、4维置信度。执行语义已统一为“等待激活”，不再允许输出立刻买入/低吸/右侧确认等标签。推荐理由里必须写出硬格式交易计划：买入依据需明确为“价格触发：...；量能触发：...；逻辑触发：...”，失效条件需明确为“时间失效：...；价格失效：...；逻辑失效：...”，并把量能条件量化到价位、周期、比较基准、阈值。第二阶段要求优先补充 evidenceSources JSON 字符串，标明来源名称、来源类型、信任级别、时效级别。证据不足或存在冲突时只能直接回避，不应混入观察标签；建议每次批量保存不超过5条。AI总结场景下若证据核验层提供了集合竞价或实时分钟线价格锚点，应围绕该价格锚点定价。",
			Parameters: &data.FunctionParameters{
				Type: "object",
				Properties: map[string]any{
					"stocks": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"modelName": map[string]any{
									"type":        "string",
									"description": "模型名称",
								},
								"stockCode": map[string]any{
									"type":        "string",
									"description": "股票代码,如：601138.SH。注意 上海证券交易所股票以.SH结尾，深圳证券交易所股票以.SZ结尾，港股股票以.HK结尾，北交所股票以.BJ结尾，",
								},
								"stockName": map[string]any{
									"type":        "string",
									"description": "股票名称",
								},
								"bkCode": map[string]any{
									"type":        "string",
									"description": "板块/行业代码",
								},
								"bkName": map[string]any{
									"type":        "string",
									"description": "板块/概念/行业名称",
								},
								"stockPrice": map[string]any{
									"type":        "string",
									"description": "推荐时股票价格。AI总结场景下应优先填写当前价格锚点：集合竞价时优先auctionPrice，开盘后优先实时分钟线最新价；两者缺失时才回退到CurrentPrice/实时行情价。",
								},
								"stockPrePrice": map[string]any{
									"type":        "string",
									"description": "前一交易日股票价格",
								},
								"stockClosePrice": map[string]any{
									"type":        "string",
									"description": "推荐时股票收盘价格",
								},
								"recommendReason": map[string]any{
									"type":        "string",
									"description": "推荐理由/驱动因素/逻辑。必须包含结构化交易计划文本，至少写出：买入依据：价格触发...；量能触发...；逻辑触发...。失效条件：时间失效...；价格失效...；逻辑失效...。禁止只写“放量/缩量/强势/承接/不追”等抽象词，必须写清价位、周期、比较基准、阈值。",
								},
								"recommendBuyPrice": map[string]any{
									"type":        "string",
									"description": "ai建议买入价区间最低价和最高价之间用`-`分隔。AI总结场景下必须围绕当前价格锚点生成：竞价时段围绕auctionPrice，开盘后围绕实时分钟线价格，不能明显偏离当前价。",
								},
								"recommendBuyPriceMin": map[string]any{
									"type":        "number",
									"description": "ai建议最低买入价",
								},
								"recommendBuyPriceMax": map[string]any{
									"type":        "number",
									"description": "ai建议最高买入价",
								},
								"recommendStopProfitPrice": map[string]any{
									"type":        "string",
									"description": "ai建议止盈价区间最低价和最高价之间用`-`分隔",
								},
								"recommendStopProfitPriceMin": map[string]any{
									"type":        "number",
									"description": "ai建议最低止盈价",
								},
								"recommendStopProfitPriceMax": map[string]any{
									"type":        "number",
									"description": "ai建议最高止盈价",
								},
								"recommendStopLossPrice": map[string]any{
									"type":        "string",
									"description": "ai建议止损价",
								},
								"recommendCategory": map[string]any{
									"type":        "string",
									"description": "推荐分类。未来新记录默认只允许 conditional/等待激活；若结论明确回避，才允许 avoid。",
								},
								"coreCatalyst": map[string]any{
									"type":        "string",
									"description": "核心催化，不能写空泛观点",
								},
								"keyEvidence": map[string]any{
									"type":        "string",
									"description": "关键证据，必须带证据标签，如：[市场资讯]... [个股新闻]... [行业研报]... [财报/财务]... [互动易]... [技术/资金/形态]... [一级披露]... [资金结构]... [股东/筹码]... [产业高频]... [海外风险]...",
								},
								"invalidCondition": map[string]any{
									"type":        "string",
									"description": "失效条件。必须写成：时间失效：...；价格失效：...；逻辑失效：...，不能只写“逻辑走弱”“不及预期”等空泛表述。",
								},
								"observePrice": map[string]any{
									"type":        "string",
									"description": "观察价。AI总结场景下应优先等于或围绕当前价格锚点；竞价时段优先auctionPrice，开盘后优先实时分钟线最新价。",
								},
								"focusPrice": map[string]any{
									"type":        "string",
									"description": "关注位/建仓区间，建议与recommendBuyPrice保持一致。AI总结场景下应围绕当前价格锚点生成；竞价时段优先auctionPrice，开盘后优先实时分钟线最新价。",
								},
								"expectedCycle": map[string]any{
									"type":        "string",
									"description": "预期周期，如1-3天、1-2周",
								},
								"eventStrength": map[string]any{
									"type":        "integer",
									"description": "事件强度，0-100整数",
								},
								"capitalConfirmation": map[string]any{
									"type":        "integer",
									"description": "资金确认度，0-100整数",
								},
								"fundamentalFit": map[string]any{
									"type":        "integer",
									"description": "基本面匹配度，0-100整数",
								},
								"technicalFit": map[string]any{
									"type":        "integer",
									"description": "技术面匹配度，0-100整数",
								},
								"riskRemarks": map[string]any{
									"type":        "string",
									"description": "风险提示/风险点",
								},
								"remarks": map[string]any{
									"type":        "string",
									"description": "操作总结/备注",
								},
							},
							"required": []string{"stockCode", "stockName", "bkName", "recommendCategory", "coreCatalyst", "keyEvidence", "riskRemarks", "invalidCondition", "observePrice", "focusPrice", "recommendBuyPrice", "recommendStopProfitPrice", "recommendStopLossPrice", "expectedCycle", "eventStrength", "capitalConfirmation", "fundamentalFit", "technicalFit"},
						},
					},
				},

				Required: []string{"stocks"},
			},
		},
	})

	return tools
}

func (a *App) NewsPush(news *[]models.Telegraph) {
	return
}

func (a *App) AddCronTask(follow data.FollowedStock) func() {
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
	dest := &[]data.FollowedFund{}
	db.Dao.Model(&data.FollowedFund{}).Find(dest)
	for _, follow := range *dest {
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

func resolveChatTools(enableTools bool, tools []data.Tool) []data.Tool {
	if enableTools {
		return tools
	}
	return []data.Tool{}
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
	return isLikelyRequestLevelFailure(errs)
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

func onExit(a *App) {
	// 清理操作
	logger.SugaredLogger.Infof("systray onExit")
	//systray.Quit()
	//runtime.Quit(a.ctx)
}
