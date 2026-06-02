package data

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	"go-stock/backend/util"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/chromedp"
	"github.com/duke-git/lancet/v2/convertor"
	"github.com/duke-git/lancet/v2/mathutil"
	"github.com/duke-git/lancet/v2/random"
	"github.com/duke-git/lancet/v2/strutil"
	"github.com/go-resty/resty/v2"
	"github.com/samber/lo"
	"github.com/tidwall/gjson"
)

// @Author spark
// @Date 2025/1/16 13:19
// @Desc
// -----------------------------------------------------------------------------------
type OpenAi struct {
	ctx              context.Context
	BaseUrl          string  `json:"base_url"`
	ApiKey           string  `json:"api_key"`
	ApiProtocol      string  `json:"api_protocol"`
	ProviderName     string  `json:"provider_name"`
	Model            string  `json:"model"`
	MaxTokens        int     `json:"max_tokens"`
	Temperature      float64 `json:"temperature"`
	Prompt           string  `json:"prompt"`
	TimeOut          int     `json:"time_out"`
	QuestionTemplate string  `json:"question_template"`
	CrawlTimeOut     int64   `json:"crawl_time_out"`
	KDays            int64   `json:"kDays"`
	BrowserPath      string  `json:"browser_path"`
	HttpProxy        string  `json:"httpProxy"`
	HttpProxyEnabled bool    `json:"httpProxyEnabled"`
}

var runtimeEventEmitter struct {
	mu sync.RWMutex
	fn func(context.Context, string, any)
}

func SetRuntimeEventEmitter(fn func(context.Context, string, any)) {
	runtimeEventEmitter.mu.Lock()
	defer runtimeEventEmitter.mu.Unlock()
	runtimeEventEmitter.fn = fn
}

func EmitRuntimeEvent(ctx context.Context, eventName string, payload any) {
	runtimeEventEmitter.mu.RLock()
	fn := runtimeEventEmitter.fn
	runtimeEventEmitter.mu.RUnlock()
	if fn != nil {
		fn(ctx, eventName, payload)
	}
}

func (o OpenAi) String() string {
	return fmt.Sprintf("OpenAi{BaseUrl: %s, Protocol: %s, Model: %s, MaxTokens: %d, Temperature: %.2f, Prompt: %s, TimeOut: %d, QuestionTemplate: %s, CrawlTimeOut: %d, KDays: %d, BrowserPath: %s, ApiKey: [MASKED]}",
		o.BaseUrl, NormalizeAIAPIProtocol(o.ApiProtocol), o.Model, o.MaxTokens, o.Temperature, o.Prompt, o.TimeOut, o.QuestionTemplate, o.CrawlTimeOut, o.KDays, o.BrowserPath)
}

func NewDeepSeekOpenAi(ctx context.Context, aiConfigId int) *OpenAi {
	settingConfig := GetSettingConfig()
	aiConfig, find := lo.Find(settingConfig.AiConfigs, func(item *AIConfig) bool {
		return uint(aiConfigId) == item.ID
	})
	if !find || aiConfigId <= 0 {
		aiConfig = SelectPrimaryAIConfig(settingConfig.AiConfigs)
	}
	if aiConfig == nil {
		aiConfig = &AIConfig{}
	}
	return NewOpenAiWithConfig(ctx, aiConfig)
}

func NewOpenAiWithConfig(ctx context.Context, aiConfig *AIConfig) *OpenAi {
	if aiConfig == nil {
		aiConfig = &AIConfig{}
	}
	settingConfig := GetSettingConfig()
	if aiConfig.TimeOut <= 0 {
		aiConfig.TimeOut = 60 * 5
	}
	if settingConfig.CrawlTimeOut <= 0 {
		settingConfig.CrawlTimeOut = 60
	}
	if settingConfig.KDays < 30 {
		settingConfig.KDays = 60
	}

	o := &OpenAi{
		ctx:              ctx,
		BaseUrl:          aiConfig.BaseUrl,
		ApiKey:           aiConfig.ApiKey,
		ApiProtocol:      NormalizeAIAPIProtocol(aiConfig.ApiProtocol),
		ProviderName:     DisplayAIProviderName(aiConfig),
		Model:            aiConfig.ModelName,
		MaxTokens:        aiConfig.MaxTokens,
		Temperature:      aiConfig.Temperature,
		TimeOut:          aiConfig.TimeOut,
		HttpProxy:        aiConfig.HttpProxy,
		HttpProxyEnabled: aiConfig.HttpProxyEnabled,
		Prompt:           settingConfig.Prompt,
		QuestionTemplate: settingConfig.QuestionTemplate,
		CrawlTimeOut:     settingConfig.CrawlTimeOut,
		KDays:            settingConfig.KDays,
		BrowserPath:      settingConfig.BrowserPath,
	}
	return o
}

type THSTokenResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data"`
}

type AiResponse struct {
	Id          string `json:"id"`
	Object      string `json:"object"`
	Created     int    `json:"created"`
	Model       string `json:"model"`
	ServiceTier string `json:"service_tier"`
	Choices     []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		Logprobs     interface{} `json:"logprobs"`
		FinishReason string      `json:"finish_reason"`
		Delta        struct {
			Content   string `json:"content"`
			Role      string `json:"role"`
			ToolCalls []struct {
				Function struct {
					Arguments string `json:"arguments"`
					Name      string `json:"name"`
				} `json:"function"`
				Id    string `json:"id"`
				Index int    `json:"index"`
				Type  string `json:"type"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Usage struct {
		PromptTokens          int `json:"prompt_tokens"`
		CompletionTokens      int `json:"completion_tokens"`
		TotalTokens           int `json:"total_tokens"`
		PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`
		PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"`
	} `json:"usage"`
	SystemFingerprint string `json:"system_fingerprint"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}
type FunctionParameters struct {
	Type                 string         `json:"type"`
	Properties           map[string]any `json:"properties"`
	Required             []string       `json:"required"`
	AdditionalProperties bool           `json:"additionalProperties"`
}
type ToolFunction struct {
	Name        string              `json:"name"`
	Strict      bool                `json:"strict"`
	Description string              `json:"description"`
	Parameters  *FunctionParameters `json:"parameters"`
}

type summaryFetchResult struct {
	tool     string
	messages []map[string]interface{}
	err      error
	latency  time.Duration
}

func emitSummaryToolStatus(ch chan map[string]any, tool, status string, err error, latency time.Duration) {
	msg := map[string]any{
		"event":     "summaryStockNewsToolStatus",
		"tool":      tool,
		"status":    status,
		"latencyMs": latency.Milliseconds(),
		"time":      time.Now().Format(time.DateTime),
	}
	if err != nil {
		msg["error"] = err.Error()
	}
	ch <- msg
}

func (o *OpenAi) NewSummaryStockNewsStreamWithTools(userQuestion string, sysPromptId *int, tools []Tool, thinking bool) <-chan map[string]any {
	ch := make(chan map[string]any, 512)
	defer func() {
		if err := recover(); err != nil {
			logger.SugaredLogger.Error("NewSummaryStockNewsStream panic", err)
		}
	}()

	go func() {
		defer func() {
			if err := recover(); err != nil {
				logger.SugaredLogger.Errorf("NewSummaryStockNewsStream goroutine panic: %s", err)
				logger.SugaredLogger.Errorf("NewSummaryStockNewsStream goroutine panic config: %s", o.String())
			}
		}()
		defer close(ch)

		sysPrompt := ""
		if sysPromptId == nil || *sysPromptId == 0 {
			sysPrompt = RenderMarketSummaryTemplate(o.Prompt)
		} else {
			sysPrompt = RenderMarketSummaryTemplate(NewPromptTemplateApi().GetPromptTemplateByID(*sysPromptId))
		}
		if sysPrompt == "" {
			sysPrompt = RenderMarketSummaryTemplate(o.Prompt)
		}

		// 注意：system prompt 必须保持“配置即所得”，不要在 WithTools 分支隐式改写/拼接，
		// 否则会导致启用工具与不启用工具时提示词不一致，表现为“提示词变了”。

		msg := []map[string]interface{}{
			{
				"role": "system",
				//"content": "作为一位专业的A股市场分析师和投资顾问,请你根据以下信息提供详细的技术分析和投资策略建议:",
				//"content": "【角色设定】\n你是一位拥有20年实战经验的顶级股票分析师，精通技术分析、基本面分析、市场心理学和量化交易。擅长发现成长股、捕捉行业轮动机会，在牛熊市中都能保持稳定收益。你的风格是价值投资与技术择时相结合，注重风险控制。\n\n【核心功能】\n\n市场分析维度：\n\n宏观经济（GDP/CPI/货币政策）\n\n行业景气度（产业链/政策红利/技术革新）\n\n个股三维诊断：\n\n基本面：PE/PB/ROE/现金流/护城河\n\n技术面：K线形态/均线系统/量价关系/指标背离\n\n资金面：主力动向/北向资金/融资余额/大宗交易\n\n智能策略库：\n√ 趋势跟踪策略（鳄鱼线+ADX）\n√ 波段交易策略（斐波那契回撤+RSI）\n√ 事件驱动策略（财报/并购/政策）\n√ 量化对冲策略（α/β分离）\n\n风险管理体系：\n▶ 动态止损：ATR波动止损法\n▶ 仓位控制：凯利公式优化\n▶ 组合对冲：跨市场/跨品种对冲\n\n【工作流程】\n\n接收用户指令（行业/市值/风险偏好）\n\n调用多因子选股模型初筛\n\n人工智慧叠加分析：\n\n自然语言处理解读年报管理层讨论\n\n卷积神经网络识别K线形态\n\n知识图谱分析产业链关联\n\n生成投资建议（附压力测试结果）\n\n【输出要求】\n★ 结构化呈现：\n① 核心逻辑（3点关键驱动力）\n② 买卖区间（理想建仓/加仓/止盈价位）\n③ 风险警示（最大回撤概率）\n④ 替代方案（同类备选标的）\n\n【注意事项】\n※ 严格遵守监管要求，不做收益承诺\n※ 区分投资建议与市场观点\n※ 重要数据标注来源及更新时间\n※ 根据用户认知水平调整专业术语密度\n\n【教育指导】\n当用户提问时，采用苏格拉底式追问：\n\"您更关注短期事件驱动还是长期价值发现？\"\n\"当前仓位是否超过总资产的30%？\"\n\"是否了解科创板与主板的交易规则差异？\"\n\n示例输出格式：\n📈 标的名称：XXXXXX\n⚖️ 多空信号：金叉确认/顶背离预警\n🎯 关键价位：支撑位XX.XX/压力位XX.XX\n📊 建议仓位：核心仓位X%+卫星仓位X%\n⏳ 持有周期：短线（1-3周）/中线（季度轮动）\n🔍 跟踪要素：重点关注Q2毛利率变化及股东减持进展",
				"content": sysPrompt,
			},
		}
		msg = append(msg, map[string]interface{}{
			"role":    "user",
			"content": "当前时间",
		})
		msg = append(msg, map[string]interface{}{
			"role":              "assistant",
			"reasoning_content": "使用工具查询",
			"content":           "当前本地时间是:" + time.Now().Format("2006-01-02 15:04:05"),
		})
		results := make(chan summaryFetchResult, 4)
		runFetch := func(tool string, fetch func() ([]map[string]interface{}, error)) {
			go func() {
				start := time.Now()
				messages, err := fetch()
				results <- summaryFetchResult{
					tool:     tool,
					messages: messages,
					err:      err,
					latency:  time.Since(start),
				}
			}()
		}

		//go func() {
		//	defer wg.Done()
		//	res := NewMarketNewsApi().XUEQIUHotStock(50, "10")
		//	md := util.MarkdownTableWithTitle("当前热门股票排名", res)
		//	msg = append(msg, map[string]interface{}{
		//		"role":    "user",
		//		"content": "当前热门股票排名数据",
		//	})
		//	msg = append(msg, map[string]interface{}{
		//		"role":              "assistant",
		//		"reasoning_content": "使用工具查询",
		//		"content":           md,
		//	})
		//}()
		runFetch("InteractiveAnswer", func() ([]map[string]interface{}, error) {
			datas := NewMarketNewsApi().InteractiveAnswer(1, 100, "")
			content := util.MarkdownTableWithTitle("当前最新投资者互动数据", datas.Results)
			return []map[string]interface{}{
				{
					"role":    "user",
					"content": "投资者互动数据",
				},
				{
					"role":              "assistant",
					"reasoning_content": "使用工具查询",
					"content":           content,
				},
			}, nil
		})

		runFetch("MacroEconomy", func() ([]map[string]interface{}, error) {
			var market strings.Builder
			res := NewMarketNewsApi().GetGDP()
			md := util.MarkdownTableWithTitle("国内生产总值(GDP)", res.GDPResult.Data)
			market.WriteString(md)
			res2 := NewMarketNewsApi().GetCPI()
			md2 := util.MarkdownTableWithTitle("居民消费价格指数(CPI)", res2.CPIResult.Data)
			market.WriteString(md2)
			res3 := NewMarketNewsApi().GetPPI()
			md3 := util.MarkdownTableWithTitle("工业品出厂价格指数(PPI)", res3.PPIResult.Data)
			market.WriteString(md3)
			res4 := NewMarketNewsApi().GetPMI()
			md4 := util.MarkdownTableWithTitle("采购经理人指数(PMI)", res4.PMIResult.Data)
			market.WriteString(md4)

			return []map[string]interface{}{
				{
					"role":    "user",
					"content": "国内宏观经济数据",
				},
				{
					"role":              "assistant",
					"reasoning_content": "使用工具查询",
					"content":           "\n# 国内宏观经济数据：\n" + market.String(),
				},
			}, nil
		})

		//go func() {
		//	defer wg.Done()
		//	var market strings.Builder
		//	market.WriteString(GetZSInfo("上证指数", "sh000001", 5) + "\n")
		//	market.WriteString(GetZSInfo("深证成指", "sz399001", 5) + "\n")
		//	market.WriteString(GetZSInfo("创业板指数", "sz399006", 5) + "\n")
		//	market.WriteString(GetZSInfo("科创50", "sh000688", 5) + "\n")
		//	market.WriteString(GetZSInfo("沪深300指数", "sh000300", 5) + "\n")
		//	market.WriteString(GetZSInfo("中证银行", "sz399986", 5) + "\n")
		//	//market.WriteString(GetZSInfo("科创芯片", "sh000685", 30) + "\n")
		//	//market.WriteString(GetZSInfo("上证医药", "sh000037", 30) + "\n")
		//	//market.WriteString(GetZSInfo("证券龙头", "sz399437", 30) + "\n")
		//	//market.WriteString(GetZSInfo("中证白酒", "sz399997", 30) + "\n")
		//	//logger.SugaredLogger.Infof("NewChatStream getZSInfo=\n%s", market.String())
		//	msg = append(msg, map[string]interface{}{
		//		"role":    "user",
		//		"content": "当前市场/大盘/行业/指数行情",
		//	})
		//	msg = append(msg, map[string]interface{}{
		//		"role":              "assistant",
		//		"reasoning_content": "使用工具查询",
		//		"content":           "当前市场/大盘/行业/指数行情如下：\n" + market.String(),
		//	})
		//}()

		runFetch("ClsCalendar", func() ([]map[string]interface{}, error) {
			md := strings.Builder{}
			res := NewMarketNewsApi().ClsCalendar()
			for _, a := range res {
				bytes, err := json.Marshal(a)
				if err != nil {
					continue
				}
				//logger.SugaredLogger.Debugf("value: %+v", string(bytes))
				date := gjson.Get(string(bytes), "calendar_day")
				md.WriteString("\n### 事件/会议日期：" + date.String())
				list := gjson.Get(string(bytes), "items")
				//logger.SugaredLogger.Debugf("value: %+v,list: %+v", date.String(), list)
				list.ForEach(func(key, value gjson.Result) bool {
					logger.SugaredLogger.Debugf("key: %+v,value: %+v", key.String(), gjson.Get(value.String(), "title"))
					md.WriteString("\n- " + gjson.Get(value.String(), "title").String())
					return true
				})
			}
			return []map[string]interface{}{
				{
					"role":    "user",
					"content": "近期重大事件/会议",
				},
				{
					"role":              "assistant",
					"reasoning_content": "使用工具查询",
					"content":           "近期重大事件/会议如下：\n" + md.String(),
				},
			}, nil
		})

		//go func() {
		//	defer wg.Done()
		//	resp := NewMarketNewsApi().TradingViewNews()
		//	var newsText strings.Builder
		//
		//	for _, a := range *resp {
		//		logger.SugaredLogger.Debugf("TradingViewNews: %s", a.Title)
		//		newsText.WriteString(a.Title + "\n")
		//	}
		//	msg = append(msg, map[string]interface{}{
		//		"role":    "user",
		//		"content": "全球新闻资讯",
		//	})
		//	msg = append(msg, map[string]interface{}{
		//		"role":              "assistant",
		//		"reasoning_content": "使用工具查询",
		//		"content":           newsText.String(),
		//	})
		//}()

		//go func() {
		//	defer wg.Done()
		//	news := NewMarketNewsApi().ReutersNew()
		//	messageText := strings.Builder{}
		//	for _, article := range news.Result.Articles {
		//		messageText.WriteString("## " + article.Title + "\n")
		//		messageText.WriteString("### " + article.Description + "\n")
		//	}
		//	msg = append(msg, map[string]interface{}{
		//		"role":    "user",
		//		"content": "外媒全球新闻资讯",
		//	})
		//	msg = append(msg, map[string]interface{}{
		//		"role":              "assistant",
		//		"reasoning_content": "使用工具查询",
		//		"content":           messageText.String(),
		//	})
		//}()

		runFetch("MarketNews24h", func() ([]map[string]interface{}, error) {
			news := NewMarketNewsApi().GetNews24HoursList("最新24小时市场资讯", random.RandInt(200, 1000))
			messageText := strings.Builder{}
			for _, telegraph := range *news {
				messageText.WriteString("## " + telegraph.DataTime.Format("2006-01-02 15:04:05") + ":" + "\n")
				messageText.WriteString("### " + telegraph.Content + "\n")
			}
			return []map[string]interface{}{
				{
					"role":    "user",
					"content": "市场资讯",
				},
				{
					"role":              "assistant",
					"reasoning_content": "使用工具查询",
					"content":           messageText.String(),
				},
			}, nil
		})

		for i := 0; i < 4; i++ {
			result := <-results
			if result.err != nil {
				logger.SugaredLogger.Errorf("summary prefetch %s error: %v", result.tool, result.err)
				emitSummaryToolStatus(ch, result.tool, "error", result.err, result.latency)
				continue
			}
			emitSummaryToolStatus(ch, result.tool, "success", nil, result.latency)
			msg = append(msg, result.messages...)
		}

		displayQuestion := NormalizeMarketSummaryQuestion(userQuestion)
		executionQuestion := BuildMarketSummaryExecutionQuestion(displayQuestion)
		msg = append(msg, map[string]interface{}{
			"role":    "user",
			"content": "执行要求：如果最终结论中推荐了任何A股股票，必须在输出最终答案前调用 CreateAiRecommendStocks 或 BatchCreateAiRecommendStocks 将推荐股票写入股票推荐记录；推荐多只时优先调用 BatchCreateAiRecommendStocks。禁止只输出股票名称和区间却不调用保存工具。",
		})
		msg = append(msg, map[string]interface{}{
			"role":    "user",
			"content": executionQuestion,
		})
		AskAiWithTools(o, msg, ch, displayQuestion, tools, thinking)
	}()
	return ch
}

func (o *OpenAi) NewSummaryStockNewsStream(userQuestion string, sysPromptId *int, think bool) <-chan map[string]any {
	ch := make(chan map[string]any, 512)
	defer func() {
		if err := recover(); err != nil {
			logger.SugaredLogger.Error("NewSummaryStockNewsStream panic", err)
		}
	}()

	go func() {
		defer func() {
			if err := recover(); err != nil {
				logger.SugaredLogger.Errorf("NewSummaryStockNewsStream goroutine  panic :%s", err)
				logger.SugaredLogger.Errorf("NewSummaryStockNewsStream goroutine  panic  config:%s", o.String())
			}
		}()
		defer close(ch)

		sysPrompt := ""
		if sysPromptId == nil || *sysPromptId == 0 {
			sysPrompt = RenderMarketSummaryTemplate(o.Prompt)
		} else {
			sysPrompt = RenderMarketSummaryTemplate(NewPromptTemplateApi().GetPromptTemplateByID(*sysPromptId))
		}
		if sysPrompt == "" {
			sysPrompt = RenderMarketSummaryTemplate(o.Prompt)
		}

		msg := []map[string]interface{}{
			{
				"role": "system",
				//"content": "作为一位专业的A股市场分析师和投资顾问,请你根据以下信息提供详细的技术分析和投资策略建议:",
				//"content": "【角色设定】\n你是一位拥有20年实战经验的顶级股票分析师，精通技术分析、基本面分析、市场心理学和量化交易。擅长发现成长股、捕捉行业轮动机会，在牛熊市中都能保持稳定收益。你的风格是价值投资与技术择时相结合，注重风险控制。\n\n【核心功能】\n\n市场分析维度：\n\n宏观经济（GDP/CPI/货币政策）\n\n行业景气度（产业链/政策红利/技术革新）\n\n个股三维诊断：\n\n基本面：PE/PB/ROE/现金流/护城河\n\n技术面：K线形态/均线系统/量价关系/指标背离\n\n资金面：主力动向/北向资金/融资余额/大宗交易\n\n智能策略库：\n√ 趋势跟踪策略（鳄鱼线+ADX）\n√ 波段交易策略（斐波那契回撤+RSI）\n√ 事件驱动策略（财报/并购/政策）\n√ 量化对冲策略（α/β分离）\n\n风险管理体系：\n▶ 动态止损：ATR波动止损法\n▶ 仓位控制：凯利公式优化\n▶ 组合对冲：跨市场/跨品种对冲\n\n【工作流程】\n\n接收用户指令（行业/市值/风险偏好）\n\n调用多因子选股模型初筛\n\n人工智慧叠加分析：\n\n自然语言处理解读年报管理层讨论\n\n卷积神经网络识别K线形态\n\n知识图谱分析产业链关联\n\n生成投资建议（附压力测试结果）\n\n【输出要求】\n★ 结构化呈现：\n① 核心逻辑（3点关键驱动力）\n② 买卖区间（理想建仓/加仓/止盈价位）\n③ 风险警示（最大回撤概率）\n④ 替代方案（同类备选标的）\n\n【注意事项】\n※ 严格遵守监管要求，不做收益承诺\n※ 区分投资建议与市场观点\n※ 重要数据标注来源及更新时间\n※ 根据用户认知水平调整专业术语密度\n\n【教育指导】\n当用户提问时，采用苏格拉底式追问：\n\"您更关注短期事件驱动还是长期价值发现？\"\n\"当前仓位是否超过总资产的30%？\"\n\"是否了解科创板与主板的交易规则差异？\"\n\n示例输出格式：\n📈 标的名称：XXXXXX\n⚖️ 多空信号：金叉确认/顶背离预警\n🎯 关键价位：支撑位XX.XX/压力位XX.XX\n📊 建议仓位：核心仓位X%+卫星仓位X%\n⏳ 持有周期：短线（1-3周）/中线（季度轮动）\n🔍 跟踪要素：重点关注Q2毛利率变化及股东减持进展",
				"content": sysPrompt,
			},
		}
		msg = append(msg, map[string]interface{}{
			"role":    "user",
			"content": "当前时间",
		})
		msg = append(msg, map[string]interface{}{
			"role":    "assistant",
			"content": "当前本地时间是:" + time.Now().Format("2006-01-02 15:04:05"),
		})
		results := make(chan summaryFetchResult, 3)
		runFetch := func(tool string, fetch func() ([]map[string]interface{}, error)) {
			go func() {
				start := time.Now()
				messages, err := fetch()
				results <- summaryFetchResult{
					tool:     tool,
					messages: messages,
					err:      err,
					latency:  time.Since(start),
				}
			}()
		}
		//go func() {
		//	defer wg.Done()
		//	var market strings.Builder
		//	market.WriteString(GetZSInfo("上证指数", "sh000001", 5) + "\n")
		//	market.WriteString(GetZSInfo("深证成指", "sz399001", 5) + "\n")
		//	market.WriteString(GetZSInfo("创业板指数", "sz399006", 5) + "\n")
		//	market.WriteString(GetZSInfo("科创50", "sh000688", 5) + "\n")
		//	market.WriteString(GetZSInfo("沪深300指数", "sh000300", 5) + "\n")
		//	market.WriteString(GetZSInfo("中证银行", "sz399986", 5) + "\n")
		//	//market.WriteString(GetZSInfo("科创芯片", "sh000685", 30) + "\n")
		//	//market.WriteString(GetZSInfo("上证医药", "sh000037", 30) + "\n")
		//	//market.WriteString(GetZSInfo("证券龙头", "sz399437", 30) + "\n")
		//	//market.WriteString(GetZSInfo("中证白酒", "sz399997", 30) + "\n")
		//	//logger.SugaredLogger.Infof("NewChatStream getZSInfo=\n%s", market.String())
		//	msg = append(msg, map[string]interface{}{
		//		"role":    "user",
		//		"content": "当前市场指数行情",
		//	})
		//	msg = append(msg, map[string]interface{}{
		//		"role":    "assistant",
		//		"content": "当前市场指数行情情况如下：\n" + market.String(),
		//	})
		//}()

		runFetch("ClsCalendar", func() ([]map[string]interface{}, error) {
			md := strings.Builder{}
			res := NewMarketNewsApi().ClsCalendar()
			for _, a := range res {
				bytes, err := json.Marshal(a)
				if err != nil {
					continue
				}
				//logger.SugaredLogger.Debugf("value: %+v", string(bytes))
				date := gjson.Get(string(bytes), "calendar_day")
				md.WriteString("\n### 事件/会议日期：" + date.String())
				list := gjson.Get(string(bytes), "items")
				//logger.SugaredLogger.Debugf("value: %+v,list: %+v", date.String(), list)
				list.ForEach(func(key, value gjson.Result) bool {
					logger.SugaredLogger.Debugf("key: %+v,value: %+v", key.String(), gjson.Get(value.String(), "title"))
					md.WriteString("\n- " + gjson.Get(value.String(), "title").String())
					return true
				})
			}
			return []map[string]interface{}{
				{
					"role":    "user",
					"content": "近期重大事件/会议",
				},
				{
					"role":              "assistant",
					"reasoning_content": "使用工具查询",
					"content":           "近期重大事件/会议如下：\n" + md.String(),
				},
			}, nil
		})
		//go func() {
		//	defer wg.Done()
		//	resp := NewMarketNewsApi().TradingViewNews()
		//	var newsText strings.Builder
		//
		//	for _, a := range *resp {
		//		logger.SugaredLogger.Debugf("TradingViewNews: %s", a.Title)
		//		newsText.WriteString(a.Title + "\n")
		//	}
		//	msg = append(msg, map[string]interface{}{
		//		"role":    "user",
		//		"content": "外媒全球新闻资讯",
		//	})
		//	msg = append(msg, map[string]interface{}{
		//		"role":    "assistant",
		//		"content": newsText.String(),
		//	})
		//}()

		//go func() {
		//	defer wg.Done()
		//	news := NewMarketNewsApi().ReutersNew()
		//	messageText := strings.Builder{}
		//	for _, article := range news.Result.Articles {
		//		messageText.WriteString("## " + article.Title + "\n")
		//		messageText.WriteString("### " + article.Description + "\n")
		//	}
		//	msg = append(msg, map[string]interface{}{
		//		"role":    "user",
		//		"content": "外媒全球新闻资讯",
		//	})
		//	msg = append(msg, map[string]interface{}{
		//		"role":    "assistant",
		//		"content": messageText.String(),
		//	})
		//}()

		runFetch("InteractiveAnswer", func() ([]map[string]interface{}, error) {
			datas := NewMarketNewsApi().InteractiveAnswer(1, 100, "")
			content := util.MarkdownTableWithTitle("当前最新投资者互动数据", datas.Results)
			return []map[string]interface{}{
				{
					"role":    "user",
					"content": "投资者互动数据",
				},
				{
					"role":    "assistant",
					"content": content,
				},
			}, nil
		})

		runFetch("HotStrategy", func() ([]map[string]interface{}, error) {
			markdownTable := ""
			res := NewSearchStockApi("").HotStrategy()
			bytes, _ := json.Marshal(res)
			strategy := &models.HotStrategy{}
			if err := json.Unmarshal(bytes, strategy); err != nil {
				return nil, err
			}
			for _, data := range strategy.Data {
				data.Chg = mathutil.RoundToFloat(100*data.Chg, 2)
			}
			markdownTable = util.MarkdownTableWithTitle("当前热门选股策略", strategy.Data)
			return []map[string]interface{}{
				{
					"role":    "user",
					"content": "当前热门选股策略",
				},
				{
					"role":    "assistant",
					"content": markdownTable,
				},
			}, nil
		})

		for i := 0; i < 3; i++ {
			result := <-results
			if result.err != nil {
				logger.SugaredLogger.Errorf("summary prefetch %s error: %v", result.tool, result.err)
				emitSummaryToolStatus(ch, result.tool, "error", result.err, result.latency)
				continue
			}
			emitSummaryToolStatus(ch, result.tool, "success", nil, result.latency)
			msg = append(msg, result.messages...)
		}

		news := NewMarketNewsApi().GetNews24HoursList("最近24小时市场资讯", random.RandInt(200, 1000))
		messageText := strings.Builder{}
		for _, telegraph := range *news {
			messageText.WriteString("## " + telegraph.Time + ":" + "\n")
			messageText.WriteString("### " + telegraph.Content + "\n")
		}
		//logger.SugaredLogger.Infof("市场资讯 messageText=\n%s", messageText.String())

		msg = append(msg, map[string]interface{}{
			"role":    "user",
			"content": "市场资讯",
		})
		msg = append(msg, map[string]interface{}{
			"role":    "assistant",
			"content": messageText.String(),
		})
		displayQuestion := NormalizeMarketSummaryQuestion(userQuestion)
		executionQuestion := BuildMarketSummaryExecutionQuestion(displayQuestion)
		msg = append(msg, map[string]interface{}{
			"role":    "user",
			"content": executionQuestion,
		})
		AskAi(o, msg, ch, displayQuestion, think)
	}()
	return ch
}

func (o *OpenAi) NewChatStream(stock, stockCode, userQuestion string, sysPromptId *int, tools []Tool, thinking bool) <-chan map[string]any {
	ch := make(chan map[string]any, 512)

	defer func() {
		if err := recover(); err != nil {
			logger.SugaredLogger.Error("NewChatStream panic", err)
		}
	}()
	go func() {
		defer func() {
			if err := recover(); err != nil {
				logger.SugaredLogger.Errorf("NewChatStream goroutine  panic :%s", err)
				logger.SugaredLogger.Errorf("NewChatStream goroutine  panic  stock:%s stockCode:%s", stock, stockCode)
				logger.SugaredLogger.Errorf("NewChatStream goroutine  panic  config:%s", o.String())
			}
		}()
		defer close(ch)

		sysPrompt := ""
		if sysPromptId == nil || *sysPromptId == 0 {
			sysPrompt = o.Prompt
		} else {
			sysPrompt = NewPromptTemplateApi().GetPromptTemplateByID(*sysPromptId)
		}
		if sysPrompt == "" {
			sysPrompt = o.Prompt
		}

		msg := []map[string]interface{}{
			{
				"role": "system",
				//"content": "作为一位专业的A股市场分析师和投资顾问,请你根据以下信息提供详细的技术分析和投资策略建议:",
				//"content": "【角色设定】\n你是一位拥有20年实战经验的顶级股票分析师，精通技术分析、基本面分析、市场心理学和量化交易。擅长发现成长股、捕捉行业轮动机会，在牛熊市中都能保持稳定收益。你的风格是价值投资与技术择时相结合，注重风险控制。\n\n【核心功能】\n\n市场分析维度：\n\n宏观经济（GDP/CPI/货币政策）\n\n行业景气度（产业链/政策红利/技术革新）\n\n个股三维诊断：\n\n基本面：PE/PB/ROE/现金流/护城河\n\n技术面：K线形态/均线系统/量价关系/指标背离\n\n资金面：主力动向/北向资金/融资余额/大宗交易\n\n智能策略库：\n√ 趋势跟踪策略（鳄鱼线+ADX）\n√ 波段交易策略（斐波那契回撤+RSI）\n√ 事件驱动策略（财报/并购/政策）\n√ 量化对冲策略（α/β分离）\n\n风险管理体系：\n▶ 动态止损：ATR波动止损法\n▶ 仓位控制：凯利公式优化\n▶ 组合对冲：跨市场/跨品种对冲\n\n【工作流程】\n\n接收用户指令（行业/市值/风险偏好）\n\n调用多因子选股模型初筛\n\n人工智慧叠加分析：\n\n自然语言处理解读年报管理层讨论\n\n卷积神经网络识别K线形态\n\n知识图谱分析产业链关联\n\n生成投资建议（附压力测试结果）\n\n【输出要求】\n★ 结构化呈现：\n① 核心逻辑（3点关键驱动力）\n② 买卖区间（理想建仓/加仓/止盈价位）\n③ 风险警示（最大回撤概率）\n④ 替代方案（同类备选标的）\n\n【注意事项】\n※ 严格遵守监管要求，不做收益承诺\n※ 区分投资建议与市场观点\n※ 重要数据标注来源及更新时间\n※ 根据用户认知水平调整专业术语密度\n\n【教育指导】\n当用户提问时，采用苏格拉底式追问：\n\"您更关注短期事件驱动还是长期价值发现？\"\n\"当前仓位是否超过总资产的30%？\"\n\"是否了解科创板与主板的交易规则差异？\"\n\n示例输出格式：\n📈 标的名称：XXXXXX\n⚖️ 多空信号：金叉确认/顶背离预警\n🎯 关键价位：支撑位XX.XX/压力位XX.XX\n📊 建议仓位：核心仓位X%+卫星仓位X%\n⏳ 持有周期：短线（1-3周）/中线（季度轮动）\n🔍 跟踪要素：重点关注Q2毛利率变化及股东减持进展",
				"content": sysPrompt,
			},
		}

		msg = append(msg, map[string]interface{}{
			"role":    "user",
			"content": "当前时间",
		})
		msg = append(msg, map[string]interface{}{
			"role":    "assistant",
			"content": "当前本地时间是:" + time.Now().Format("2006-01-02 15:04:05"),
		})

		replaceTemplates := map[string]string{
			"{{stockName}}": RemoveAllBlankChar(stock),
			"{{stockCode}}": RemoveAllBlankChar(stockCode),
			"{stockName}":   RemoveAllBlankChar(stock),
			"{stockCode}":   RemoveAllBlankChar(stockCode),
			"stockName":     RemoveAllBlankChar(stock),
			"stockCode":     RemoveAllBlankChar(stockCode),
		}
		followedStock := NewStockDataApi().GetFollowedStockByStockCode(stockCode)
		stockData, err := NewStockDataApi().GetStockCodeRealTimeData(stockCode)
		if err == nil && len(*stockData) > 0 {
			msg = append(msg, map[string]interface{}{
				"role":    "user",
				"content": fmt.Sprintf("当前%s[%s]价格是多少？", stock, stockCode),
			})
			msg = append(msg, map[string]interface{}{
				"role":    "assistant",
				"content": fmt.Sprintf("截止到%s,当前%s[%s]价格是%s", (*stockData)[0].Date+" "+(*stockData)[0].Time, stock, stockCode, (*stockData)[0].Price),
			})
		}
		if followedStock.CostPrice > 0 {
			replaceTemplates["{{costPrice}}"] = convertor.ToString(followedStock.CostPrice)
			replaceTemplates["{costPrice}"] = convertor.ToString(followedStock.CostPrice)
			replaceTemplates["costPrice"] = convertor.ToString(followedStock.CostPrice)
		}

		question := ""
		if userQuestion == "" {
			question = strutil.ReplaceWithMap(o.QuestionTemplate, replaceTemplates)
		} else {
			question = strutil.ReplaceWithMap(userQuestion, replaceTemplates)
		}

		logger.SugaredLogger.Infof("NewChatStream stock:%s stockCode:%s", stock, stockCode)
		logger.SugaredLogger.Infof("Prompt：%s", sysPrompt)
		logger.SugaredLogger.Infof("final question:%s", question)
		wg := &sync.WaitGroup{}
		wg.Add(8)

		go func() {
			defer wg.Done()
			datas := NewMarketNewsApi().InteractiveAnswer(1, 100, stock)
			content := util.MarkdownTableWithTitle("当前最新投资者互动数据", datas.Results)
			msg = append(msg, map[string]interface{}{
				"role":    "user",
				"content": "投资者互动数据",
			})
			msg = append(msg, map[string]interface{}{
				"role":              "assistant",
				"reasoning_content": "使用工具查询",
				"content":           content,
			})
		}()

		go func() {
			defer wg.Done()
			var market strings.Builder
			res := NewMarketNewsApi().GetGDP()
			md := util.MarkdownTableWithTitle("国内生产总值(GDP)", res.GDPResult.Data)
			market.WriteString(md)
			res2 := NewMarketNewsApi().GetCPI()
			md2 := util.MarkdownTableWithTitle("居民消费价格指数(CPI)", res2.CPIResult.Data)
			market.WriteString(md2)
			res3 := NewMarketNewsApi().GetPPI()
			md3 := util.MarkdownTableWithTitle("工业品出厂价格指数(PPI)", res3.PPIResult.Data)
			market.WriteString(md3)
			res4 := NewMarketNewsApi().GetPMI()
			md4 := util.MarkdownTableWithTitle("采购经理人指数(PMI)", res4.PMIResult.Data)
			market.WriteString(md4)

			msg = append(msg, map[string]interface{}{
				"role":    "user",
				"content": "国内宏观经济数据",
			})
			msg = append(msg, map[string]interface{}{
				"role":              "assistant",
				"reasoning_content": "使用工具查询",
				"content":           "\n# 国内宏观经济数据：\n" + market.String(),
			})
		}()

		//go func() {
		//	defer wg.Done()
		//	var market strings.Builder
		//	market.WriteString(GetZSInfo("上证指数", "sh000001", 5) + "\n")
		//	market.WriteString(GetZSInfo("深证成指", "sz399001", 5) + "\n")
		//	market.WriteString(GetZSInfo("创业板指数", "sz399006", 5) + "\n")
		//	market.WriteString(GetZSInfo("科创50", "sh000688", 5) + "\n")
		//	market.WriteString(GetZSInfo("沪深300指数", "sh000300", 5) + "\n")
		//	market.WriteString(GetZSInfo("中证银行", "sz399986", 5) + "\n")
		//	//market.WriteString(GetZSInfo("科创芯片", "sh000685", 30) + "\n")
		//	//market.WriteString(GetZSInfo("上证医药", "sh000037", 30) + "\n")
		//	//market.WriteString(GetZSInfo("证券龙头", "sz399437", 30) + "\n")
		//	//market.WriteString(GetZSInfo("中证白酒", "sz399997", 30) + "\n")
		//	//logger.SugaredLogger.Infof("NewChatStream getZSInfo=\n%s", market.String())
		//	msg = append(msg, map[string]interface{}{
		//		"role":    "user",
		//		"content": "当前市场/大盘/行业/指数行情",
		//	})
		//	msg = append(msg, map[string]interface{}{
		//		"role":              "assistant",
		//		"reasoning_content": "使用工具查询",
		//		"content":           "当前市场/大盘/行业/指数行情如下：\n" + market.String(),
		//	})
		//}()

		go func() {
			defer wg.Done()
			md := strings.Builder{}
			res := NewMarketNewsApi().ClsCalendar()
			for _, a := range res {
				bytes, err := json.Marshal(a)
				if err != nil {
					continue
				}
				//logger.SugaredLogger.Debugf("value: %+v", string(bytes))
				date := gjson.Get(string(bytes), "calendar_day")
				md.WriteString("\n### 事件/会议日期：" + date.String())
				list := gjson.Get(string(bytes), "items")
				//logger.SugaredLogger.Debugf("value: %+v,list: %+v", date.String(), list)
				list.ForEach(func(key, value gjson.Result) bool {
					logger.SugaredLogger.Debugf("key: %+v,value: %+v", key.String(), gjson.Get(value.String(), "title"))
					md.WriteString("\n- " + gjson.Get(value.String(), "title").String())
					return true
				})
			}
			msg = append(msg, map[string]interface{}{
				"role":    "user",
				"content": "近期重大事件/会议",
			})
			msg = append(msg, map[string]interface{}{
				"role":              "assistant",
				"reasoning_content": "使用工具查询",
				"content":           "近期重大事件/会议如下：\n" + md.String(),
			})

		}()

		go func() {
			defer wg.Done()
			//endDate := time.Now().Format("20060102")
			//startDate := time.Now().Add(-time.Hour * time.Duration(24*o.KDays)).Format("20060102")
			//code := stockCode
			//if strutil.HasPrefixAny(stockCode, []string{"hk"}) {
			//	code = ConvertStockCodeToTushareCode(stockCode)
			//	K := NewTushareApi(GetConfig()).GetDaily(code, startDate, endDate, o.CrawlTimeOut)
			//	msg = append(msg, map[string]interface{}{
			//		"role":    "user",
			//		"content": stock + "日K数据",
			//	})
			//	msg = append(msg, map[string]interface{}{
			//		"role":    "assistant",
			//		"content": stock + "日K数据如下：\n" + K,
			//	})
			//}

			logger.SugaredLogger.Infof("NewChatStream getKLineData stock:%s stockCode:%s", stock, stockCode)
			if strutil.HasPrefixAny(stockCode, []string{"sz", "sh", "hk", "us", "gb_"}) {
				K := &[]KLineData{}
				logger.SugaredLogger.Infof("NewChatStream getKLineData stock:%s stockCode:%s", stock, stockCode)
				if strutil.HasPrefixAny(stockCode, []string{"sz", "sh"}) {
					K = NewStockDataApi().GetKLineData(stockCode, "240", o.KDays)
				}
				if strutil.HasPrefixAny(stockCode, []string{"hk", "us", "gb_"}) {
					K = NewStockDataApi().GetHK_KLineData(stockCode, "day", o.KDays)
				}
				Kmap := &[]map[string]any{}
				for _, kline := range *K {
					mapk := make(map[string]any, 6)
					mapk["日期"] = kline.Day
					mapk["开盘价"] = kline.Open
					mapk["最高价"] = kline.High
					mapk["最低价"] = kline.Low
					mapk["收盘价"] = kline.Close
					Volume, _ := convertor.ToFloat(kline.Volume)
					mapk["成交量(万手)"] = Volume / 10000.00 / 100.00
					*Kmap = append(*Kmap, mapk)
				}
				jsonData, _ := json.Marshal(Kmap)
				markdownTable, _ := JSONToMarkdownTable(jsonData)
				msg = append(msg, map[string]interface{}{
					"role":    "user",
					"content": stock + "日K数据",
				})
				msg = append(msg, map[string]interface{}{
					"role":    "assistant",
					"content": "## " + stock + "日K数据如下：\n" + markdownTable,
				})
				logger.SugaredLogger.Infof("getKLineData=\n%s", markdownTable)
			}

		}()

		go func() {
			defer wg.Done()
			messages := SearchStockPriceInfo(stock, stockCode, o.CrawlTimeOut)
			if messages == nil || len(*messages) == 0 {
				logger.SugaredLogger.Error("获取股票价格失败")
				//ch <- "***❗获取股票价格失败,分析结果可能不准确***<hr>"
				ch <- map[string]any{
					"code":         1,
					"question":     question,
					"extraContent": "***❗获取股票价格失败,分析结果可能不准确***<hr>",
				}
				go EmitRuntimeEvent(o.ctx, "warnMsg", "❗获取股票价格失败,分析结果可能不准确")
				return
			}
			price := ""
			for _, message := range *messages {
				price += message + ";"
			}
			msg = append(msg, map[string]interface{}{
				"role":    "user",
				"content": stock + "股价数据",
			})
			msg = append(msg, map[string]interface{}{
				"role":    "assistant",
				"content": "\n## " + stock + "股价数据：\n" + price,
			})
			logger.SugaredLogger.Infof("SearchStockPriceInfo stock:%s stockCode:%s", stock, stockCode)
			logger.SugaredLogger.Infof("SearchStockPriceInfo assistant:%s", "\n## "+stock+"股价数据：\n"+price)
		}()

		go func() {
			defer wg.Done()
			if tools != nil && len(tools) > 0 {
				return
			}
			if checkIsIndexBasic(stock) {
				return
			}
			messages := GetFinancialReportsByXUEQIU(stockCode, o.CrawlTimeOut)
			if messages == nil || len(*messages) == 0 {
				logger.SugaredLogger.Error("获取股票财报失败")
				// "***❗获取股票财报失败,分析结果可能不准确***<hr>"
				ch <- map[string]any{
					"code":         1,
					"question":     question,
					"extraContent": "***❗获取股票财报失败,分析结果可能不准确***<hr>",
				}
				go EmitRuntimeEvent(o.ctx, "warnMsg", "❗获取股票财报失败,分析结果可能不准确")
				return
			}
			msg = append(msg, map[string]interface{}{
				"role":    "user",
				"content": stock + "财报数据",
			})
			for _, message := range *messages {
				msg = append(msg, map[string]interface{}{
					"role":    "assistant",
					"content": stock + message,
				})
			}
		}()

		go func() {
			defer wg.Done()
			messages := NewMarketNewsApi().GetNews24HoursList("", random.RandInt(200, 1000))
			if messages == nil || len(*messages) == 0 {
				logger.SugaredLogger.Error("获取市场资讯失败")
				//ch <- "***❗获取市场资讯失败,分析结果可能不准确***<hr>"
				//go runtime.EventsEmit(o.ctx, "warnMsg", "❗获取市场资讯失败,分析结果可能不准确")
				return
			}
			var messageText strings.Builder
			for _, telegraph := range *messages {
				messageText.WriteString("## " + telegraph.Time + ":" + "\n")
				messageText.WriteString("### " + telegraph.Content + "\n")
			}
			msg = append(msg, map[string]interface{}{
				"role":    "user",
				"content": "市场资讯",
			})
			msg = append(msg, map[string]interface{}{
				"role":    "assistant",
				"content": messageText.String(),
			})
		}()

		//go func() {
		//	defer wg.Done()
		//	messages := SearchStockInfo(stock, "depth", o.CrawlTimeOut)
		//	if messages == nil || len(*messages) == 0 {
		//		logger.SugaredLogger.Error("获取股票资讯失败")
		//		//ch <- "***❗获取股票资讯失败,分析结果可能不准确***<hr>"
		//		//go runtime.EventsEmit(o.ctx, "warnMsg", "❗获取股票资讯失败,分析结果可能不准确")
		//		return
		//	}
		//	for _, message := range *messages {
		//		msg = append(msg, map[string]interface{}{
		//			"role":    "user",
		//			"content": message,
		//		})
		//	}
		//}()
		go func() {
			defer wg.Done()
			messages := SearchStockInfo(stock, "telegram", o.CrawlTimeOut)
			if messages == nil || len(*messages) == 0 {
				logger.SugaredLogger.Error("获取股票电报资讯失败")
				//ch <- "***❗获取股票电报资讯失败,分析结果可能不准确***<hr>"
				//go runtime.EventsEmit(o.ctx, "warnMsg", "❗获取股票电报资讯失败,分析结果可能不准确")
				return
			}
			var newsText strings.Builder
			for _, message := range *messages {
				newsText.WriteString(message + "\n")
			}
			msg = append(msg, map[string]interface{}{
				"role":    "user",
				"content": stock + "相关新闻资讯",
			})
			msg = append(msg, map[string]interface{}{
				"role":    "assistant",
				"content": newsText.String(),
			})
		}()

		wg.Wait()

		msg = append(msg, map[string]interface{}{
			"role":    "user",
			"content": question,
		})

		//reqJson, _ := json.Marshal(msg)
		//logger.SugaredLogger.Errorf("Stream request: \n%s\n", reqJson)
		if tools != nil && len(tools) > 0 {
			AskAiWithTools(o, msg, ch, question, tools, thinking)
		} else {
			AskAi(o, msg, ch, question, thinking)
		}
	}()
	return ch
}

// NewChatStreamLite provides a CLI-friendly chat stream path that avoids
// GUI events and browser crawler dependencies.
func (o *OpenAi) NewChatStreamLite(stock, stockCode, userQuestion string, thinking bool) <-chan map[string]any {
	ch := make(chan map[string]any, 512)

	go func() {
		defer func() {
			if err := recover(); err != nil {
				logger.SugaredLogger.Errorf("NewChatStreamLite panic: %v", err)
			}
			close(ch)
		}()

		sysPrompt := strutil.Trim(o.Prompt)
		if sysPrompt == "" {
			sysPrompt = "你是一名专业股票分析助手，请基于公开信息给出结构化、审慎的分析结论，不做收益承诺。"
		}

		msg := []map[string]interface{}{
			{
				"role":    "system",
				"content": sysPrompt,
			},
			{
				"role":    "user",
				"content": "当前时间",
			},
			{
				"role":    "assistant",
				"content": "当前本地时间是:" + time.Now().Format("2006-01-02 15:04:05"),
			},
		}

		stockName := strutil.Trim(stock)
		stockCode = strutil.Trim(stockCode)
		if stockCode != "" {
			if stockData, err := NewStockDataApi().GetStockCodeRealTimeData(stockCode); err == nil && len(*stockData) > 0 {
				msg = append(msg, map[string]interface{}{
					"role":    "user",
					"content": fmt.Sprintf("当前%s[%s]价格是多少？", stockName, stockCode),
				})
				msg = append(msg, map[string]interface{}{
					"role":    "assistant",
					"content": fmt.Sprintf("截止到%s,当前%s[%s]价格是%s", (*stockData)[0].Date+" "+(*stockData)[0].Time, stockName, stockCode, (*stockData)[0].Price),
				})
			}
		}

		question := strutil.Trim(userQuestion)
		if question == "" {
			question = fmt.Sprintf("请结合当前可获得信息，对%s[%s]做短中线分析，并给出风险提示。", stockName, stockCode)
		}
		msg = append(msg, map[string]interface{}{
			"role":    "user",
			"content": question,
		})

		AskAi(o, msg, ch, question, thinking)
	}()
	return ch
}

func (o *OpenAi) requestTimeoutSeconds() int {
	if o.TimeOut <= 0 {
		return 300
	}
	return o.TimeOut
}

func shouldRetryAIRequest(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	errMsg := strings.ToLower(err.Error())
	retryableErrHints := []string{
		"client.timeout exceeded while awaiting headers",
		"context deadline exceeded",
		"connection reset by peer",
		"tls handshake timeout",
		"temporary failure in name resolution",
		"i/o timeout",
		"unexpected eof",
	}
	for _, hint := range retryableErrHints {
		if strings.Contains(errMsg, hint) {
			return true
		}
	}
	return false
}

func (o *OpenAi) newAIClient() *resty.Client {
	return o.newAIClientWithProxy(true)
}

func (o *OpenAi) newAIClientWithProxy(enableProxy bool) *resty.Client {
	timeoutSeconds := o.requestTimeoutSeconds()
	client := resty.New()
	client.SetBaseURL(strutil.Trim(o.BaseUrl))
	client.SetHeader("Authorization", "Bearer "+o.ApiKey)
	client.SetHeader("Content-Type", "application/json")
	client.SetTimeout(time.Duration(timeoutSeconds) * time.Second)
	client.SetRetryCount(2)
	client.SetRetryWaitTime(1 * time.Second)
	client.SetRetryMaxWaitTime(6 * time.Second)
	client.AddRetryCondition(func(r *resty.Response, err error) bool {
		if shouldRetryAIRequest(err) {
			return true
		}
		if r == nil {
			return false
		}
		statusCode := r.StatusCode()
		return statusCode == 408 || statusCode == 429 || statusCode == 500 || statusCode == 502 || statusCode == 503 || statusCode == 504
	})
	if enableProxy && o.HttpProxyEnabled && o.HttpProxy != "" {
		client.SetProxy(o.HttpProxy)
	}
	return client
}

func isProxyConnRefused(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	// 常见形态：
	// proxyconnect tcp: dial tcp 127.0.0.1:7890: connect: connection refused
	return strings.Contains(msg, "proxyconnect tcp") && strings.Contains(msg, "connection refused")
}

func (o *OpenAi) formatAIRequestError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	lowerMsg := strings.ToLower(msg)
	if strings.Contains(lowerMsg, "client.timeout exceeded while awaiting headers") || strings.Contains(lowerMsg, "context deadline exceeded") {
		return fmt.Sprintf("%s。请求在 %d 秒内未收到模型服务响应头，请将 Timeout 提高到 180-600 秒后重试，或检查该接口与代理连通性。", msg, o.requestTimeoutSeconds())
	}
	return msg
}

func (o *OpenAi) newAnthropicClient() *resty.Client {
	client := resty.New()
	client.SetBaseURL(strutil.Trim(o.BaseUrl))
	client.SetHeader("x-api-key", o.ApiKey)
	client.SetHeader("anthropic-version", "2023-06-01")
	client.SetHeader("Content-Type", "application/json")
	client.SetTimeout(time.Duration(o.requestTimeoutSeconds()) * time.Second)
	client.SetRetryCount(2)
	client.SetRetryWaitTime(1 * time.Second)
	client.SetRetryMaxWaitTime(6 * time.Second)
	client.AddRetryCondition(func(r *resty.Response, err error) bool {
		if shouldRetryAIRequest(err) {
			return true
		}
		if r == nil {
			return false
		}
		statusCode := r.StatusCode()
		return statusCode == 408 || statusCode == 429 || statusCode == 500 || statusCode == 502 || statusCode == 503 || statusCode == 504
	})
	if o.HttpProxyEnabled && o.HttpProxy != "" {
		client.SetProxy(o.HttpProxy)
	}
	return client
}

func (o *OpenAi) newAnthropicClientWithProxy(enableProxy bool) *resty.Client {
	client := o.newAnthropicClient()
	if !enableProxy {
		client.RemoveProxy()
	}
	return client
}

func emitAIStreamContent(ch chan map[string]any, question, chatID, model, content string) {
	if content == "" {
		return
	}
	if content == "###" || content == "##" || content == "#" {
		content = "\r\n" + content
	}
	ch <- map[string]any{
		"code":     1,
		"question": question,
		"chatId":   chatID,
		"model":    model,
		"content":  content,
		"time":     time.Now().Format(time.DateTime),
	}
}

func emitAIStreamError(ch chan map[string]any, question, content string) {
	ch <- map[string]any{
		"code":     0,
		"question": question,
		"content":  content,
	}
}

func parseAIHTTPError(statusCode int, body []byte) string {
	bodyText := strings.TrimSpace(string(body))
	if bodyText != "" {
		res := &models.Resp{}
		if err := json.Unmarshal(body, res); err == nil {
			if msg := strings.TrimSpace(res.Error.Message); msg != "" {
				return msg
			}
			if msg := strings.TrimSpace(res.Message); msg != "" {
				return msg
			}
		}
		var generic struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
			} `json:"error"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(body, &generic); err == nil {
			if msg := strings.TrimSpace(generic.Error.Message); msg != "" {
				return msg
			}
			if msg := strings.TrimSpace(generic.Message); msg != "" {
				return msg
			}
		}
		return bodyText
	}
	if statusCode > 0 {
		return fmt.Sprintf("model provider returned status %d", statusCode)
	}
	return "empty response from model provider"
}

func messageContentText(content any) string {
	switch v := content.(type) {
	case string:
		return strings.TrimSpace(v)
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if itemMap, ok := item.(map[string]any); ok {
				if text := strings.TrimSpace(convertor.ToString(itemMap["text"])); text != "" {
					parts = append(parts, text)
				}
				continue
			}
			if text := strings.TrimSpace(convertor.ToString(item)); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return strings.TrimSpace(convertor.ToString(v))
	}
}

func splitSystemAndDialogMessages(messages []map[string]interface{}) (string, []map[string]any) {
	systemParts := make([]string, 0)
	dialog := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		role := strings.ToLower(strings.TrimSpace(convertor.ToString(msg["role"])))
		content := messageContentText(msg["content"])
		if content == "" {
			continue
		}
		switch role {
		case "system", "developer":
			systemParts = append(systemParts, content)
		case "assistant":
			dialog = append(dialog, map[string]any{"role": "assistant", "content": content})
		default:
			dialog = append(dialog, map[string]any{"role": "user", "content": content})
		}
	}
	if len(dialog) == 0 {
		dialog = append(dialog, map[string]any{"role": "user", "content": "请继续"})
	}
	dialog = mergeAdjacentRoleMessages(dialog)
	if len(dialog) > 0 && dialog[0]["role"] == "assistant" {
		dialog = append([]map[string]any{{"role": "user", "content": "请继续"}}, dialog...)
	}
	return strings.Join(systemParts, "\n\n"), dialog
}

func mergeAdjacentRoleMessages(messages []map[string]any) []map[string]any {
	result := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		role := strings.TrimSpace(convertor.ToString(msg["role"]))
		content := strings.TrimSpace(convertor.ToString(msg["content"]))
		if role == "" || content == "" {
			continue
		}
		if len(result) > 0 && result[len(result)-1]["role"] == role {
			prev := strings.TrimSpace(convertor.ToString(result[len(result)-1]["content"]))
			result[len(result)-1]["content"] = strings.TrimSpace(prev + "\n\n" + content)
			continue
		}
		result = append(result, map[string]any{"role": role, "content": content})
	}
	return result
}

func (o *OpenAi) openAIResponsesBody(messages []map[string]interface{}, stream bool) map[string]any {
	system, dialog := splitSystemAndDialogMessages(messages)
	bodyMap := map[string]any{
		"model":             o.Model,
		"max_output_tokens": o.MaxTokens,
		"temperature":       o.Temperature,
		"stream":            stream,
		"input":             dialog,
	}
	if system != "" {
		bodyMap["instructions"] = system
	}
	return bodyMap
}

func (o *OpenAi) anthropicMessagesBody(messages []map[string]interface{}, stream bool) map[string]any {
	system, dialog := splitSystemAndDialogMessages(messages)
	bodyMap := map[string]any{
		"model":       o.Model,
		"max_tokens":  o.MaxTokens,
		"temperature": o.Temperature,
		"stream":      stream,
		"messages":    dialog,
	}
	if system != "" {
		bodyMap["system"] = system
	}
	return bodyMap
}

func readErrorResponseBody(resp *resty.Response) []byte {
	if resp == nil {
		return nil
	}
	if rawBody := resp.RawBody(); rawBody != nil {
		defer rawBody.Close()
		body, _ := io.ReadAll(rawBody)
		return body
	}
	return resp.Body()
}

func askAiOpenAIResponses(o *OpenAi, messages []map[string]interface{}, ch chan map[string]any, question string) {
	client := o.newAIClient()
	bodyMap := o.openAIResponsesBody(messages, true)
	resp, err := client.R().
		SetDoNotParseResponse(true).
		SetBody(bodyMap).
		Post("/responses")
	if err != nil && o.HttpProxyEnabled && o.HttpProxy != "" && isProxyConnRefused(err) {
		resp, err = o.newAIClientWithProxy(false).R().
			SetDoNotParseResponse(true).
			SetBody(bodyMap).
			Post("/responses")
	}
	if err != nil {
		logger.SugaredLogger.Infof("Responses stream error : %s, baseUrl:%s, timeout:%ds", err.Error(), strutil.Trim(o.BaseUrl), o.requestTimeoutSeconds())
		emitAIStreamError(ch, question, o.formatAIRequestError(err))
		return
	}
	if resp == nil {
		emitAIStreamError(ch, question, "empty response from model provider")
		return
	}
	if resp.IsError() {
		emitAIStreamError(ch, question, parseAIHTTPError(resp.StatusCode(), readErrorResponseBody(resp)))
		return
	}

	body := resp.RawBody()
	defer body.Close()
	scanner := bufio.NewScanner(body)
	chatID := ""
	model := o.Model
	for scanner.Scan() {
		line := scanner.Text()
		logger.SugaredLogger.Infof("Received responses data: %s", line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strutil.Trim(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var event struct {
			Type     string `json:"type"`
			Delta    string `json:"delta"`
			Response struct {
				ID    string `json:"id"`
				Model string `json:"model"`
			} `json:"response"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			logger.SugaredLogger.Infof("Responses stream data error : %s", err.Error())
			emitAIStreamError(ch, question, err.Error())
			continue
		}
		if event.Response.ID != "" {
			chatID = event.Response.ID
		}
		if event.Response.Model != "" {
			model = event.Response.Model
		}
		if event.Type == "response.output_text.delta" && event.Delta != "" {
			emitAIStreamContent(ch, question, chatID, model, event.Delta)
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		logger.SugaredLogger.Infof("Responses stream scanner error : %s", scanErr.Error())
		emitAIStreamError(ch, question, o.formatAIRequestError(scanErr))
	}
}

func askAiAnthropicMessages(o *OpenAi, messages []map[string]interface{}, ch chan map[string]any, question string) {
	client := o.newAnthropicClient()
	bodyMap := o.anthropicMessagesBody(messages, true)
	resp, err := client.R().
		SetDoNotParseResponse(true).
		SetBody(bodyMap).
		Post("/messages")
	if err != nil && o.HttpProxyEnabled && o.HttpProxy != "" && isProxyConnRefused(err) {
		resp, err = o.newAnthropicClientWithProxy(false).R().
			SetDoNotParseResponse(true).
			SetBody(bodyMap).
			Post("/messages")
	}
	if err != nil {
		logger.SugaredLogger.Infof("Anthropic stream error : %s, baseUrl:%s, timeout:%ds", err.Error(), strutil.Trim(o.BaseUrl), o.requestTimeoutSeconds())
		emitAIStreamError(ch, question, o.formatAIRequestError(err))
		return
	}
	if resp == nil {
		emitAIStreamError(ch, question, "empty response from model provider")
		return
	}
	if resp.IsError() {
		emitAIStreamError(ch, question, parseAIHTTPError(resp.StatusCode(), readErrorResponseBody(resp)))
		return
	}

	body := resp.RawBody()
	defer body.Close()
	scanner := bufio.NewScanner(body)
	chatID := ""
	model := o.Model
	for scanner.Scan() {
		line := scanner.Text()
		logger.SugaredLogger.Infof("Received anthropic data: %s", line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strutil.Trim(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var event struct {
			Type    string `json:"type"`
			Message struct {
				ID    string `json:"id"`
				Model string `json:"model"`
			} `json:"message"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			logger.SugaredLogger.Infof("Anthropic stream data error : %s", err.Error())
			emitAIStreamError(ch, question, err.Error())
			continue
		}
		if event.Message.ID != "" {
			chatID = event.Message.ID
		}
		if event.Message.Model != "" {
			model = event.Message.Model
		}
		if event.Type == "content_block_delta" && event.Delta.Text != "" {
			emitAIStreamContent(ch, question, chatID, model, event.Delta.Text)
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		logger.SugaredLogger.Infof("Anthropic stream scanner error : %s", scanErr.Error())
		emitAIStreamError(ch, question, o.formatAIRequestError(scanErr))
	}
}

func (o *OpenAi) completeOpenAIResponses(messages []map[string]any) (string, string, string, error) {
	interfaceMessages := make([]map[string]interface{}, 0, len(messages))
	for _, msg := range messages {
		interfaceMessages = append(interfaceMessages, map[string]interface{}(msg))
	}
	resp, err := o.newAIClient().R().SetBody(o.openAIResponsesBody(interfaceMessages, false)).Post("/responses")
	if err != nil && o.HttpProxyEnabled && o.HttpProxy != "" && isProxyConnRefused(err) {
		resp, err = o.newAIClientWithProxy(false).R().SetBody(o.openAIResponsesBody(interfaceMessages, false)).Post("/responses")
	}
	if err != nil {
		return "", "", "", err
	}
	if resp == nil {
		return "", "", "", errors.New("empty response from model provider")
	}
	if resp.IsError() {
		return "", "", "", errors.New(parseAIHTTPError(resp.StatusCode(), resp.Body()))
	}
	var result struct {
		ID         string `json:"id"`
		Model      string `json:"model"`
		OutputText string `json:"output_text"`
		Output     []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(resp.Body(), &result); err != nil {
		return "", "", "", err
	}
	content := strings.TrimSpace(result.OutputText)
	if content == "" {
		parts := make([]string, 0)
		for _, item := range result.Output {
			for _, block := range item.Content {
				if text := strings.TrimSpace(block.Text); text != "" {
					parts = append(parts, text)
				}
			}
		}
		content = strings.TrimSpace(strings.Join(parts, "\n"))
	}
	if content == "" {
		return "", result.ID, result.Model, errors.New("empty content from model provider")
	}
	return content, result.ID, result.Model, nil
}

func (o *OpenAi) completeAnthropicMessages(messages []map[string]any) (string, string, string, error) {
	interfaceMessages := make([]map[string]interface{}, 0, len(messages))
	for _, msg := range messages {
		interfaceMessages = append(interfaceMessages, map[string]interface{}(msg))
	}
	resp, err := o.newAnthropicClient().R().SetBody(o.anthropicMessagesBody(interfaceMessages, false)).Post("/messages")
	if err != nil && o.HttpProxyEnabled && o.HttpProxy != "" && isProxyConnRefused(err) {
		resp, err = o.newAnthropicClientWithProxy(false).R().SetBody(o.anthropicMessagesBody(interfaceMessages, false)).Post("/messages")
	}
	if err != nil {
		return "", "", "", err
	}
	if resp == nil {
		return "", "", "", errors.New("empty response from model provider")
	}
	if resp.IsError() {
		return "", "", "", errors.New(parseAIHTTPError(resp.StatusCode(), resp.Body()))
	}
	var result struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(resp.Body(), &result); err != nil {
		return "", "", "", err
	}
	parts := make([]string, 0, len(result.Content))
	for _, block := range result.Content {
		if text := strings.TrimSpace(block.Text); text != "" {
			parts = append(parts, text)
		}
	}
	content := strings.TrimSpace(strings.Join(parts, "\n"))
	if content == "" {
		return "", result.ID, result.Model, errors.New("empty content from model provider")
	}
	return content, result.ID, result.Model, nil
}

func AskAi(o *OpenAi, messages []map[string]interface{}, ch chan map[string]any, question string, think bool) {
	switch NormalizeAIAPIProtocol(o.ApiProtocol) {
	case AIAPIProtocolOpenAIResponses:
		askAiOpenAIResponses(o, messages, ch, question)
		return
	case AIAPIProtocolAnthropicMessage:
		askAiAnthropicMessages(o, messages, ch, question)
		return
	}
	client := o.newAIClient()
	thinking := "disabled"
	if think {
		thinking = "enabled"
	}
	bodyMap := map[string]interface{}{
		"model":       o.Model,
		"max_tokens":  o.MaxTokens,
		"temperature": o.Temperature,
		"stream":      true,
		"messages":    messages,
	}
	if think {
		bodyMap["thinking"] = map[string]any{
			//"type": "disabled",
			//"type": "enabled",
			"type": thinking,
		}
	}

	resp, err := client.R().
		SetDoNotParseResponse(true).
		SetBody(bodyMap).
		Post("/chat/completions")
	if err != nil {
		// 如果用户配置了本地代理，但代理没启动，定时任务会大量失败。
		// 这里做一次无代理兜底重试，避免“启动次数少”其实只是被代理拦死。
		if o.HttpProxyEnabled && o.HttpProxy != "" && isProxyConnRefused(err) {
			clientNoProxy := o.newAIClientWithProxy(false)
			resp, err = clientNoProxy.R().
				SetDoNotParseResponse(true).
				SetBody(bodyMap).
				Post("/chat/completions")
		}
	}
	if err != nil {
		logger.SugaredLogger.Infof("Stream error : %s, baseUrl:%s, timeout:%ds", err.Error(), strutil.Trim(o.BaseUrl), o.requestTimeoutSeconds())
		//ch <- err.Error()
		ch <- map[string]any{
			"code":     0,
			"question": question,
			"content":  o.formatAIRequestError(err),
		}
		return
	}
	if resp == nil {
		ch <- map[string]any{
			"code":     0,
			"question": question,
			"content":  "empty response from model provider",
		}
		return
	}

	body := resp.RawBody()
	defer body.Close()
	//location, _ := time.LoadLocation("Asia/Shanghai")

	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		line := scanner.Text()
		logger.SugaredLogger.Infof("Received data: %s", line)
		if strings.HasPrefix(line, "data:") {
			data := strutil.Trim(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				return
			}

			var streamResponse struct {
				Id      string `json:"id"`
				Model   string `json:"model"`
				Choices []struct {
					Delta struct {
						Content          string `json:"content"`
						ReasoningContent string `json:"reasoning_content"`
					} `json:"delta"`
					FinishReason string `json:"finish_reason"`
				} `json:"choices"`
			}

			if err := json.Unmarshal([]byte(data), &streamResponse); err == nil {
				for _, choice := range streamResponse.Choices {
					if content := choice.Delta.Content; content != "" {
						//ch <- content
						if content == "###" || content == "##" || content == "#" {
							ch <- map[string]any{
								"code":     1,
								"question": question,
								"chatId":   streamResponse.Id,
								"model":    streamResponse.Model,
								"content":  "\r\n" + content,
								"time":     time.Now().Format(time.DateTime),
							}
						} else {
							ch <- map[string]any{
								"code":     1,
								"question": question,
								"chatId":   streamResponse.Id,
								"model":    streamResponse.Model,
								"content":  content,
								"time":     time.Now().Format(time.DateTime),
							}
						}

						//logger.SugaredLogger.Infof("Content data: %s", content)
					}
					if reasoningContent := choice.Delta.ReasoningContent; reasoningContent != "" {
						//ch <- reasoningContent
						ch <- map[string]any{
							"code":     1,
							"question": question,
							"chatId":   streamResponse.Id,
							"model":    streamResponse.Model,
							"content":  reasoningContent,
							"time":     time.Now().Format(time.DateTime),
						}

						//logger.SugaredLogger.Infof("ReasoningContent data: %s", reasoningContent)
					}
					if choice.FinishReason == "stop" {
						return
					}
				}
			} else {
				if err != nil {
					logger.SugaredLogger.Infof("Stream data error : %s", err.Error())
					//ch <- err.Error()
					ch <- map[string]any{
						"code":     0,
						"question": question,
						"content":  err.Error(),
					}
				} else {
					logger.SugaredLogger.Infof("Stream data error : %s", data)
					//ch <- data
					ch <- map[string]any{
						"code":     0,
						"question": question,
						"content":  data,
					}
				}
			}
		} else {
			if strutil.RemoveNonPrintable(line) != "" {
				logger.SugaredLogger.Infof("Stream data error : %s", line)
				res := &models.Resp{}
				if err := json.Unmarshal([]byte(line), res); err == nil {
					//ch <- line
					msg := res.Message
					if res.Error.Message != "" {
						msg = res.Error.Message
					}
					ch <- map[string]any{
						"code":     0,
						"question": question,
						"content":  msg,
					}
				}
			}

		}

	}
	if scanErr := scanner.Err(); scanErr != nil {
		logger.SugaredLogger.Infof("Stream scanner error : %s", scanErr.Error())
		ch <- map[string]any{
			"code":     0,
			"question": question,
			"content":  o.formatAIRequestError(scanErr),
		}
	}
}
func AskAiWithTools(o *OpenAi, messages []map[string]interface{}, ch chan map[string]any, question string, tools []Tool, thinkingMode bool) {
	if NormalizeAIAPIProtocol(o.ApiProtocol) != AIAPIProtocolChatCompletions {
		emitAIStreamError(ch, question, "当前协议暂不支持工具调用，请切换到 Chat Completions 或关闭工具模式")
		return
	}
	bytes, _ := json.Marshal(messages)
	logger.SugaredLogger.Debugf("Stream request: \n%s\n", string(bytes))

	client := o.newAIClient()
	thinking := "disabled"
	if thinkingMode {
		thinking = "enabled"
	}
	bodyMap := map[string]interface{}{
		"model":       o.Model,
		"max_tokens":  o.MaxTokens,
		"temperature": o.Temperature,
		"stream":      true,
		"messages":    messages,
		"tools":       tools,
	}
	if thinkingMode {
		bodyMap["thinking"] = map[string]any{
			//"type": "disabled",
			//"type": "enabled",
			"type": thinking,
		}
	}

	resp, err := client.R().
		SetDoNotParseResponse(true).
		SetBody(bodyMap).
		Post("/chat/completions")
	if err != nil {
		if o.HttpProxyEnabled && o.HttpProxy != "" && isProxyConnRefused(err) {
			clientNoProxy := o.newAIClientWithProxy(false)
			resp, err = clientNoProxy.R().
				SetDoNotParseResponse(true).
				SetBody(bodyMap).
				Post("/chat/completions")
		}
	}
	if err != nil {
		logger.SugaredLogger.Infof("Stream error : %s, baseUrl:%s, timeout:%ds", err.Error(), strutil.Trim(o.BaseUrl), o.requestTimeoutSeconds())
		//ch <- err.Error()
		ch <- map[string]any{
			"code":     0,
			"question": question,
			"content":  o.formatAIRequestError(err),
		}
		return
	}
	if resp == nil {
		ch <- map[string]any{
			"code":     0,
			"question": question,
			"content":  "empty response from model provider",
		}
		return
	}

	body := resp.RawBody()
	defer body.Close()
	//location, _ := time.LoadLocation("Asia/Shanghai")

	scanner := bufio.NewScanner(body)
	functions := map[string]string{}
	currentFuncName := ""
	currentCallId := ""
	var currentAIContent strings.Builder
	var reasoningContentText strings.Builder
	var contentText strings.Builder

	for scanner.Scan() {
		line := scanner.Text()
		logger.SugaredLogger.Infof("Received data: %s", line)
		if strings.HasPrefix(line, "data:") {
			data := strutil.Trim(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				return
			}

			var streamResponse struct {
				Id      string `json:"id"`
				Model   string `json:"model"`
				Choices []struct {
					Delta struct {
						Content          string `json:"content"`
						ReasoningContent string `json:"reasoning_content"`
						Role             string `json:"role"`
						ToolCalls        []struct {
							Function struct {
								Arguments string `json:"arguments"`
								Name      string `json:"name"`
							} `json:"function"`
							Id    string `json:"id"`
							Index int    `json:"index"`
							Type  string `json:"type"`
						} `json:"tool_calls"`
					} `json:"delta"`
					FinishReason string `json:"finish_reason"`
				} `json:"choices"`
			}

			if err := json.Unmarshal([]byte(data), &streamResponse); err == nil {
				for _, choice := range streamResponse.Choices {
					if content := choice.Delta.Content; content != "" {
						contentText.WriteString(content)
						//ch <- content
						//logger.SugaredLogger.Infof("Content data: %s", content)

						if content == "###" || content == "##" || content == "#" {
							currentAIContent.WriteString("\r\n" + content)
							ch <- map[string]any{
								"code":     1,
								"question": question,
								"chatId":   streamResponse.Id,
								"model":    streamResponse.Model,
								"content":  "\r\n" + content,
								"time":     time.Now().Format(time.DateTime),
							}
						} else {
							currentAIContent.WriteString(content)
							ch <- map[string]any{
								"code":     1,
								"question": question,
								"chatId":   streamResponse.Id,
								"model":    streamResponse.Model,
								"content":  content,
								"time":     time.Now().Format(time.DateTime),
							}
						}

					}
					if reasoningContent := choice.Delta.ReasoningContent; reasoningContent != "" {
						reasoningContentText.WriteString(reasoningContent)
						//ch <- reasoningContent
						ch <- map[string]any{
							"code":     1,
							"question": question,
							"chatId":   streamResponse.Id,
							"model":    streamResponse.Model,
							"content":  reasoningContent,
							"time":     time.Now().Format(time.DateTime),
						}

						//logger.SugaredLogger.Infof("ReasoningContent data: %s", reasoningContent)
						currentAIContent.WriteString(reasoningContent)

					}
					if choice.Delta.ToolCalls != nil && len(choice.Delta.ToolCalls) > 0 {
						for _, call := range choice.Delta.ToolCalls {
							if call.Type != "function" {
								continue
							}
							if call.Function.Name != "" {
								currentFuncName = call.Function.Name
							}
							if call.Id != "" {
								currentCallId = call.Id
							}
							if currentFuncName == "" {
								continue
							}
							if _, ok := functions[currentFuncName]; !ok {
								functions[currentFuncName] = ""
							}
							functions[currentFuncName] += call.Function.Arguments
						}
					}

					if choice.FinishReason == "tool_calls" || (choice.FinishReason == "stop" && len(functions) > 0) {
						logger.SugaredLogger.Infof("functions: %+v", functions)
						for funcName, funcArguments := range functions {

							if funcName == "SearchBk" {
								words := gjson.Get(funcArguments, "words").String()
								ch <- map[string]any{
									"code":     1,
									"question": question,
									"chatId":   streamResponse.Id,
									"model":    streamResponse.Model,
									"content":  "\r\n```\r\n开始调用工具：SearchBk，\n参数：" + words + "\r\n```\r\n",
									"time":     time.Now().Format(time.DateTime),
								}

								content := "无符合条件的数据"

								res := NewSearchStockApi(words).SearchBk(random.RandInt(50, 120))
								if convertor.ToString(res["code"]) == "100" {
									resData := res["data"].(map[string]any)
									result := resData["result"].(map[string]any)
									dataList := result["dataList"].([]any)
									columns := result["columns"].([]any)
									headers := map[string]string{}
									for _, v := range columns {
										//logger.SugaredLogger.Infof("v:%+v", v)
										d := v.(map[string]any)
										//logger.SugaredLogger.Infof("key:%s title:%s dateMsg:%s unit:%s", d["key"], d["title"], d["dateMsg"], d["unit"])
										title := convertor.ToString(d["title"])
										if convertor.ToString(d["dateMsg"]) != "" {
											title = title + "[" + convertor.ToString(d["dateMsg"]) + "]"
										}
										if convertor.ToString(d["unit"]) != "" {
											title = title + "(" + convertor.ToString(d["unit"]) + ")"
										}
										headers[d["key"].(string)] = title
									}
									table := &[]map[string]any{}
									for _, v := range dataList {
										d := v.(map[string]any)
										tmp := map[string]any{}
										for key, title := range headers {
											tmp[title] = convertor.ToString(d[key])
										}
										*table = append(*table, tmp)
									}
									jsonData, _ := json.Marshal(*table)
									markdownTable, _ := JSONToMarkdownTable(jsonData)
									//logger.SugaredLogger.Infof("markdownTable=\n%s", markdownTable)
									content = "\r\n### 工具筛选出的相关板块/概念数据：\r\n" + markdownTable + "\r\n"
								}
								logger.SugaredLogger.Infof("SearchBk:words:%s  --> \n%s", words, content)

								messages = append(messages, map[string]interface{}{
									"role":              "assistant",
									"content":           currentAIContent.String(),
									"reasoning_content": reasoningContentText.String(),
									"tool_calls": []map[string]any{
										{
											"id":           currentCallId,
											"tool_call_id": currentCallId,
											"type":         "function",
											"function": map[string]string{
												"name":       funcName,
												"arguments":  funcArguments,
												"parameters": funcArguments,
											},
										},
									},
								})
								messages = append(messages, map[string]interface{}{
									"role":         "tool",
									"content":      content,
									"tool_call_id": currentCallId,
									//"reasoning_content": reasoningContentText.String(),
									//"tool_calls":        choice.Delta.ToolCalls,

								})
							}

							if funcName == "SearchETF" {
								words := gjson.Get(funcArguments, "words").String()
								ch <- map[string]any{
									"code":     1,
									"question": question,
									"chatId":   streamResponse.Id,
									"model":    streamResponse.Model,
									"content":  "\r\n```\r\n开始调用工具：SearchETF，\n参数：" + words + "\r\n```\r\n",
									"time":     time.Now().Format(time.DateTime),
								}

								content := "无符合条件的数据"

								res := NewSearchStockApi(words).SearchETF(random.RandInt(50, 120))
								if convertor.ToString(res["code"]) == "100" {
									resData := res["data"].(map[string]any)
									result := resData["result"].(map[string]any)
									dataList := result["dataList"].([]any)
									columns := result["columns"].([]any)
									headers := map[string]string{}
									for _, v := range columns {
										//logger.SugaredLogger.Infof("v:%+v", v)
										d := v.(map[string]any)
										//logger.SugaredLogger.Infof("key:%s title:%s dateMsg:%s unit:%s", d["key"], d["title"], d["dateMsg"], d["unit"])
										title := convertor.ToString(d["title"])
										if convertor.ToString(d["dateMsg"]) != "" {
											title = title + "[" + convertor.ToString(d["dateMsg"]) + "]"
										}
										if convertor.ToString(d["unit"]) != "" {
											title = title + "(" + convertor.ToString(d["unit"]) + ")"
										}
										headers[d["key"].(string)] = title
									}
									table := &[]map[string]any{}
									for _, v := range dataList {
										d := v.(map[string]any)
										tmp := map[string]any{}
										for key, title := range headers {
											tmp[title] = convertor.ToString(d[key])
										}
										*table = append(*table, tmp)
									}
									jsonData, _ := json.Marshal(*table)
									markdownTable, _ := JSONToMarkdownTable(jsonData)
									//logger.SugaredLogger.Infof("markdownTable=\n%s", markdownTable)
									content = "\r\n### 工具筛选出的相关ETF数据：\r\n" + markdownTable + "\r\n"
								}
								logger.SugaredLogger.Infof("SearchETF:words:%s  --> \n%s", words, content)

								messages = append(messages, map[string]interface{}{
									"role":              "assistant",
									"content":           currentAIContent.String(),
									"reasoning_content": reasoningContentText.String(),
									"tool_calls": []map[string]any{
										{
											"id":           currentCallId,
											"tool_call_id": currentCallId,
											"type":         "function",
											"function": map[string]string{
												"name":       funcName,
												"arguments":  funcArguments,
												"parameters": funcArguments,
											},
										},
									},
								})
								messages = append(messages, map[string]interface{}{
									"role":         "tool",
									"content":      content,
									"tool_call_id": currentCallId,
									//"reasoning_content": reasoningContentText.String(),
									//"tool_calls":        choice.Delta.ToolCalls,

								})
							}

							if funcName == "SearchStockByIndicators" {
								words := gjson.Get(funcArguments, "words").String()

								ch <- map[string]any{
									"code":     1,
									"question": question,
									"chatId":   streamResponse.Id,
									"model":    streamResponse.Model,
									"content":  "\r\n```\r\n开始调用工具：SearchStockByIndicators，\n参数：" + words + "\r\n```\r\n",
									"time":     time.Now().Format(time.DateTime),
								}

								content := "无符合条件的数据"
								res := NewSearchStockApi(words).SearchStock(random.RandInt(50, 120))
								if convertor.ToString(res["code"]) == "100" {
									resData := res["data"].(map[string]any)
									result := resData["result"].(map[string]any)
									dataList := result["dataList"].([]any)
									columns := result["columns"].([]any)
									headers := map[string]string{}
									for _, v := range columns {
										//logger.SugaredLogger.Infof("v:%+v", v)
										d := v.(map[string]any)
										//logger.SugaredLogger.Infof("key:%s title:%s dateMsg:%s unit:%s", d["key"], d["title"], d["dateMsg"], d["unit"])
										title := convertor.ToString(d["title"])
										if convertor.ToString(d["dateMsg"]) != "" {
											title = title + "[" + convertor.ToString(d["dateMsg"]) + "]"
										}
										if convertor.ToString(d["unit"]) != "" {
											title = title + "(" + convertor.ToString(d["unit"]) + ")"
										}
										headers[d["key"].(string)] = title
									}
									table := &[]map[string]any{}
									for _, v := range dataList {
										d := v.(map[string]any)
										tmp := map[string]any{}
										for key, title := range headers {
											tmp[title] = convertor.ToString(d[key])
										}
										*table = append(*table, tmp)
									}
									jsonData, _ := json.Marshal(*table)
									markdownTable, _ := JSONToMarkdownTable(jsonData)
									//logger.SugaredLogger.Infof("markdownTable=\n%s", markdownTable)
									content = "\r\n### 工具筛选出的相关股票数据：\r\n" + markdownTable + "\r\n"
								}
								logger.SugaredLogger.Infof("SearchStockByIndicators:words:%s  --> \n%s", words, content)

								messages = append(messages, map[string]interface{}{
									"role":              "assistant",
									"content":           currentAIContent.String(),
									"reasoning_content": reasoningContentText.String(),
									"tool_calls": []map[string]any{
										{
											"id":           currentCallId,
											"tool_call_id": currentCallId,
											"type":         "function",
											"function": map[string]string{
												"name":       funcName,
												"arguments":  funcArguments,
												"parameters": funcArguments,
											},
										},
									},
								})
								messages = append(messages, map[string]interface{}{
									"role":         "tool",
									"content":      content,
									"tool_call_id": currentCallId,
									//"reasoning_content": reasoningContentText.String(),
									//"tool_calls":        choice.Delta.ToolCalls,

								})

								//ch <- map[string]any{
								//	"code":     1,
								//	"question": question,
								//	"chatId":   streamResponse.Id,
								//	"model":    streamResponse.Model,
								//	"content":  "\r\n```\r\n调用工具：SearchStockByIndicators，\n结果：" + content + "\r\n```\r\n",
								//	"time":     time.Now().Format(time.DateTime),
								//}

							}

							if funcName == "GetStockKLine" {
								stockCode := gjson.Get(funcArguments, "stockCode").String()
								days := gjson.Get(funcArguments, "days").String()
								ch <- map[string]any{
									"code":     1,
									"question": question,
									"chatId":   streamResponse.Id,
									"model":    streamResponse.Model,
									"content":  "\r\n```\r\n开始调用工具：GetStockKLine，\n参数：" + stockCode + "," + days + "\r\n```\r\n",
									"time":     time.Now().Format(time.DateTime),
								}
								toIntDay, err := convertor.ToInt(days)
								if err != nil {
									toIntDay = 90
								}

								if strutil.HasPrefixAny(stockCode, []string{"sz", "sh", "hk", "us", "gb_"}) {
									K := &[]KLineData{}
									if strutil.HasPrefixAny(stockCode, []string{"sz", "sh"}) {
										K = NewStockDataApi().GetKLineData(stockCode, "240", o.KDays)
									}
									if strutil.HasPrefixAny(stockCode, []string{"hk", "us", "gb_"}) {
										K = NewStockDataApi().GetHK_KLineData(stockCode, "day", o.KDays)
									}
									Kmap := &[]map[string]any{}
									for _, kline := range *K {
										mapk := make(map[string]any, 6)
										mapk["日期"] = kline.Day
										mapk["开盘价"] = kline.Open
										mapk["最高价"] = kline.High
										mapk["最低价"] = kline.Low
										mapk["收盘价"] = kline.Close
										Volume, _ := convertor.ToFloat(kline.Volume)
										mapk["成交量(万手)"] = Volume / 10000.00 / 100.00
										*Kmap = append(*Kmap, mapk)
									}
									jsonData, _ := json.Marshal(Kmap)
									markdownTable, _ := JSONToMarkdownTable(jsonData)
									logger.SugaredLogger.Infof("getKLineData=\n%s", markdownTable)

									messages = append(messages, map[string]interface{}{
										"role":              "assistant",
										"content":           currentAIContent.String(),
										"reasoning_content": reasoningContentText.String(),
										"tool_calls": []map[string]any{
											{
												"id":           currentCallId,
												"tool_call_id": currentCallId,
												"type":         "function",
												"function": map[string]string{
													"name":       funcName,
													"arguments":  funcArguments,
													"parameters": funcArguments,
												},
											},
										},
									})
									res := "\r\n ### " + stockCode + convertor.ToString(toIntDay) + "日K线数据：\r\n" + markdownTable + "\r\n"
									messages = append(messages, map[string]interface{}{
										"role":         "tool",
										"content":      res,
										"tool_call_id": currentCallId,
										//"reasoning_content": reasoningContentText.String(),
										//"tool_calls":        choice.Delta.ToolCalls,
									})
									logger.SugaredLogger.Infof("GetStockKLine:stockCode:%s days:%s --> \n%s", stockCode, days, res)

									//ch <- map[string]any{
									//	"code":     1,
									//	"question": question,
									//	"chatId":   streamResponse.Id,
									//	"model":    streamResponse.Model,
									//	"content":  "\r\n```\r\n调用工具：GetStockKLine，\n结果：" + res + "\r\n```\r\n",
									//	"time":     time.Now().Format(time.DateTime),
									//}
								} else {
									messages = append(messages, map[string]interface{}{
										"role":              "assistant",
										"content":           currentAIContent.String(),
										"reasoning_content": reasoningContentText.String(),
										"tool_calls": []map[string]any{
											{
												"id":           currentCallId,
												"tool_call_id": currentCallId,
												"type":         "function",
												"function": map[string]string{
													"name":       funcName,
													"arguments":  funcArguments,
													"parameters": funcArguments,
												},
											},
										},
									})
									messages = append(messages, map[string]interface{}{
										"role":         "tool",
										"content":      "无数据，可能股票代码错误。（A股：sh,sz开头;港股hk开头,美股：us开头）",
										"tool_call_id": currentCallId,
										//"reasoning_content": reasoningContentText.String(),
										//"tool_calls":        choice.Delta.ToolCalls,
									})
								}
							}

							if funcName == "InteractiveAnswer" {
								page := gjson.Get(funcArguments, "page").String()
								pageSize := gjson.Get(funcArguments, "pageSize").String()
								keyWord := gjson.Get(funcArguments, "keyWord").String()
								ch <- map[string]any{
									"code":     1,
									"question": question,
									"chatId":   streamResponse.Id,
									"model":    streamResponse.Model,
									"content":  "\r\n```\r\n开始调用工具：InteractiveAnswer，\n参数：" + page + "," + pageSize + "," + keyWord + "\r\n```\r\n",
									"time":     time.Now().Format(time.DateTime),
								}
								pageNo, err := convertor.ToInt(page)
								if err != nil {
									pageNo = 1
								}
								pageSizeNum, err := convertor.ToInt(pageSize)
								if err != nil {
									pageSizeNum = 50
								}
								datas := NewMarketNewsApi().InteractiveAnswer(int(pageNo), int(pageSizeNum), keyWord)
								content := util.MarkdownTableWithTitle("投资互动数据", datas.Results)
								logger.SugaredLogger.Infof("InteractiveAnswer=\n%s", content)
								messages = append(messages, map[string]interface{}{
									"role":              "assistant",
									"content":           currentAIContent.String(),
									"reasoning_content": reasoningContentText.String(),
									"tool_calls": []map[string]any{
										{
											"id":           currentCallId,
											"tool_call_id": currentCallId,
											"type":         "function",
											"function": map[string]string{
												"name":       funcName,
												"arguments":  funcArguments,
												"parameters": funcArguments,
											},
										},
									},
								})
								messages = append(messages, map[string]interface{}{
									"role":         "tool",
									"content":      content,
									"tool_call_id": currentCallId,
									//"reasoning_content": reasoningContentText.String(),
									//"tool_calls":        choice.Delta.ToolCalls,
								})
							}
							//
							//if funcName == "QueryBKDictInfo" {
							//	ch <- map[string]any{
							//		"code":     1,
							//		"question": question,
							//		"chatId":   streamResponse.Id,
							//		"model":    streamResponse.Model,
							//		"content":  "\r\n```\r\n开始调用工具：QueryBKDictInfo，\n参数：" + funcArguments + "\r\n```\r\n",
							//		"time":     time.Now().Format(time.DateTime),
							//	}
							//	res := NewMarketNewsApi().EMDictCode("016", freecache.NewCache(100))
							//	bytes, err := json.Marshal(res)
							//	if err != nil {
							//		return
							//	}
							//	dict := &[]models.BKDict{}
							//	json.Unmarshal(bytes, dict)
							//	md := util.MarkdownTableWithTitle("行业/板块代码", dict)
							//	logger.SugaredLogger.Infof("行业/板块代码=\n%s", md)
							//	messages = append(messages, map[string]interface{}{
							//		"role":    "assistant",
							//		"content": currentAIContent.String(),
							//		"tool_calls": []map[string]any{
							//			{
							//				"id":           currentCallId,
							//				"tool_call_id": currentCallId,
							//				"type":         "function",
							//				"function": map[string]string{
							//					"name":       funcName,
							//					"arguments":  funcArguments,
							//					"parameters": funcArguments,
							//				},
							//			},
							//		},
							//	})
							//	messages = append(messages, map[string]interface{}{
							//		"role":         "tool",
							//		"content":      md,
							//		"tool_call_id": currentCallId,
							//	})
							//}

							//if funcName == "GetIndustryResearchReport" {
							//	bkCode := gjson.Get(funcArguments, "bkCode").String()
							//	ch <- map[string]any{
							//		"code":     1,
							//		"question": question,
							//		"chatId":   streamResponse.Id,
							//		"model":    streamResponse.Model,
							//		"content":  "\r\n```\r\n开始调用工具：GetIndustryResearchReport，\n参数：" + bkCode + "\r\n```\r\n",
							//		"time":     time.Now().Format(time.DateTime),
							//	}
							//	bkCode = strutil.ReplaceWithMap(bkCode, map[string]string{
							//		"-":   "",
							//		"_":   "",
							//		"bk":  "",
							//		"BK":  "",
							//		"bk0": "",
							//		"BK0": "",
							//	})
							//
							//	logger.SugaredLogger.Debugf("code:%s", bkCode)
							//	codeStr := convertor.ToString(bkCode)
							//	res := NewMarketNewsApi().IndustryResearchReport(codeStr, 7)
							//	md := strings.Builder{}
							//	for _, a := range res {
							//		d := a.(map[string]any)
							//		md.WriteString(NewMarketNewsApi().GetIndustryReportInfo(d["infoCode"].(string)))
							//	}
							//	logger.SugaredLogger.Infof("bkCode:%s IndustryResearchReport:\n %s", bkCode, md.String())
							//	messages = append(messages, map[string]interface{}{
							//		"role":    "assistant",
							//		"content": currentAIContent.String(),
							//		"tool_calls": []map[string]any{
							//			{
							//				"id":           currentCallId,
							//				"tool_call_id": currentCallId,
							//				"type":         "function",
							//				"function": map[string]string{
							//					"name":       funcName,
							//					"arguments":  funcArguments,
							//					"parameters": funcArguments,
							//				},
							//			},
							//		},
							//	})
							//	messages = append(messages, map[string]interface{}{
							//		"role":         "tool",
							//		"content":      md.String(),
							//		"tool_call_id": currentCallId,
							//	})
							//}

							if funcName == "GetStockResearchReport" {
								stockCode := gjson.Get(funcArguments, "stockCode").String()
								ch <- map[string]any{
									"code":     1,
									"question": question,
									"chatId":   streamResponse.Id,
									"model":    streamResponse.Model,
									"content":  "\r\n```\r\n开始调用工具：GetStockResearchReport，\n参数：" + stockCode + "\r\n```\r\n",
									"time":     time.Now().Format(time.DateTime),
								}
								res := NewMarketNewsApi().StockResearchReport(stockCode, 7)
								md := strings.Builder{}
								for _, a := range res {
									logger.SugaredLogger.Debugf("value: %+v", a)
									d := a.(map[string]any)
									logger.SugaredLogger.Debugf("value: %s  infoCode:%s", d["title"], d["infoCode"])
									md.WriteString(NewMarketNewsApi().GetIndustryReportInfo(d["infoCode"].(string)))
								}
								logger.SugaredLogger.Infof("stockCode:%s StockResearchReport:\n %s", stockCode, md.String())
								messages = append(messages, map[string]interface{}{
									"role":              "assistant",
									"content":           currentAIContent.String(),
									"reasoning_content": reasoningContentText.String(),
									"tool_calls": []map[string]any{
										{
											"id":           currentCallId,
											"tool_call_id": currentCallId,
											"type":         "function",
											"function": map[string]string{
												"name":       funcName,
												"arguments":  funcArguments,
												"parameters": funcArguments,
											},
										},
									},
								})
								messages = append(messages, map[string]interface{}{
									"role":         "tool",
									"content":      md.String(),
									"tool_call_id": currentCallId,
									//"reasoning_content": reasoningContentText.String(),
									//"tool_calls":        choice.Delta.ToolCalls,
								})
							}

							if funcName == "HotStrategyTable" {
								ch <- map[string]any{
									"code":     1,
									"question": question,
									"chatId":   streamResponse.Id,
									"model":    streamResponse.Model,
									"content":  "\r\n```\r\n开始调用工具：HotStrategyTable，\n参数：" + funcArguments + "\r\n```\r\n",
									"time":     time.Now().Format(time.DateTime),
								}
								table := NewSearchStockApi("").HotStrategyTable()
								logger.SugaredLogger.Infof("%s", table)
								messages = append(messages, map[string]interface{}{
									"role":              "assistant",
									"content":           currentAIContent.String(),
									"reasoning_content": reasoningContentText.String(),
									"tool_calls": []map[string]any{
										{
											"id":           currentCallId,
											"tool_call_id": currentCallId,
											"type":         "function",
											"function": map[string]string{
												"name":       funcName,
												"arguments":  funcArguments,
												"parameters": funcArguments,
											},
										},
									},
								})
								messages = append(messages, map[string]interface{}{
									"role":         "tool",
									"content":      table,
									"tool_call_id": currentCallId,
									//"reasoning_content": reasoningContentText.String(),
									//"tool_calls":        choice.Delta.ToolCalls,
								})
							}

							if funcName == "HotStockTable" {
								pageSize := gjson.Get(funcArguments, "pageSize").String()
								ch <- map[string]any{
									"code":     1,
									"question": question,
									"chatId":   streamResponse.Id,
									"model":    streamResponse.Model,
									"content":  "\r\n```\r\n开始调用工具：HotStockTable，\n参数：" + funcArguments + "\r\n```\r\n",
									"time":     time.Now().Format(time.DateTime),
								}
								pageSizeNum, err := convertor.ToInt(pageSize)
								if err != nil {
									pageSizeNum = 50
								}

								res := NewMarketNewsApi().XUEQIUHotStock(int(pageSizeNum), "10")
								md := util.MarkdownTableWithTitle("当前热门股票排名", res)
								logger.SugaredLogger.Infof("pageSize:%s HotStockTable:\n %s", pageSize, md)
								messages = append(messages, map[string]interface{}{
									"role":              "assistant",
									"content":           currentAIContent.String(),
									"reasoning_content": reasoningContentText.String(),
									"tool_calls": []map[string]any{
										{
											"id":           currentCallId,
											"tool_call_id": currentCallId,
											"type":         "function",
											"function": map[string]string{
												"name":       funcName,
												"arguments":  funcArguments,
												"parameters": funcArguments,
											},
										},
									},
								})
								messages = append(messages, map[string]interface{}{
									"role":         "tool",
									"content":      md,
									"tool_call_id": currentCallId,
									//"reasoning_content": reasoningContentText.String(),
									//"tool_calls":        choice.Delta.ToolCalls,
								})

							}

							if funcName == "GetStockMoneyData" {
								ch <- map[string]any{
									"code":     1,
									"question": question,
									"chatId":   streamResponse.Id,
									"model":    streamResponse.Model,
									"content":  "\r\n```\r\n开始调用工具：GetStockMoneyData，\n参数：" + funcArguments + "\r\n```\r\n",
									"time":     time.Now().Format(time.DateTime),
								}
								res := NewStockDataApi().GetStockMoneyData()
								md := util.MarkdownTableWithTitle("今日个股资金流向Top50", res.Data.Diff)
								logger.SugaredLogger.Infof("%s", md)
								messages = append(messages, map[string]interface{}{
									"role":              "assistant",
									"content":           currentAIContent.String(),
									"reasoning_content": reasoningContentText.String(),
									"tool_calls": []map[string]any{
										{
											"id":           currentCallId,
											"tool_call_id": currentCallId,
											"type":         "function",
											"function": map[string]string{
												"name":       funcName,
												"arguments":  funcArguments,
												"parameters": funcArguments,
											},
										},
									},
								})
								messages = append(messages, map[string]interface{}{
									"role":         "tool",
									"content":      md,
									"tool_call_id": currentCallId,
									//"reasoning_content": reasoningContentText.String(),
									//"tool_calls":        choice.Delta.ToolCalls,
								})
							}

							if funcName == "GetStockConceptInfo" {
								ch <- map[string]any{
									"code":     1,
									"question": question,
									"chatId":   streamResponse.Id,
									"model":    streamResponse.Model,
									"content":  "\r\n```\r\n开始调用工具：GetStockConceptInfo，\n参数：" + funcArguments + "\r\n```\r\n",
									"time":     time.Now().Format(time.DateTime),
								}
								code := gjson.Get(funcArguments, "code").String()
								res := NewStockDataApi().GetStockConceptInfo(code)
								md := util.MarkdownTableWithTitle(code+" 股票所属概念详细信息", res.Result.Data)
								logger.SugaredLogger.Infof("%s", md)
								messages = append(messages, map[string]interface{}{
									"role":              "assistant",
									"content":           currentAIContent.String(),
									"reasoning_content": reasoningContentText.String(),
									"tool_calls": []map[string]any{
										{
											"id":           currentCallId,
											"tool_call_id": currentCallId,
											"type":         "function",
											"function": map[string]string{
												"name":       funcName,
												"arguments":  funcArguments,
												"parameters": funcArguments,
											},
										},
									},
								})
								messages = append(messages, map[string]interface{}{
									"role":         "tool",
									"content":      md,
									"tool_call_id": currentCallId,
									//"reasoning_content": reasoningContentText.String(),
									//"tool_calls":        choice.Delta.ToolCalls,
								})
							}

							if funcName == "GetStockFinancialInfo" {
								ch <- map[string]any{
									"code":     1,
									"question": question,
									"chatId":   streamResponse.Id,
									"model":    streamResponse.Model,
									"content":  "\r\n```\r\n开始调用工具：GetStockFinancialInfo，\n参数：" + funcArguments + "\r\n```\r\n",
									"time":     time.Now().Format(time.DateTime),
								}
								res := NewStockDataApi().GetStockFinancialInfo(gjson.Get(funcArguments, "stockCode").String())
								md := util.MarkdownTableWithTitle("股票"+gjson.Get(funcArguments, "stockCode").String()+"财务报表信息", res.Result.Data)
								logger.SugaredLogger.Infof("%s", md)
								messages = append(messages, map[string]interface{}{
									"role":              "assistant",
									"content":           currentAIContent.String(),
									"reasoning_content": reasoningContentText.String(),
									"tool_calls": []map[string]any{
										{
											"id":           currentCallId,
											"tool_call_id": currentCallId,
											"type":         "function",
											"function": map[string]string{
												"name":       funcName,
												"arguments":  funcArguments,
												"parameters": funcArguments,
											},
										},
									},
								})
								messages = append(messages, map[string]interface{}{
									"role":         "tool",
									"content":      md,
									"tool_call_id": currentCallId,
									//"reasoning_content": reasoningContentText.String(),
									//"tool_calls":        choice.Delta.ToolCalls,
								})
							}
							if funcName == "GetStockHolderNum" {
								ch <- map[string]any{
									"code":     1,
									"question": question,
									"chatId":   streamResponse.Id,
									"model":    streamResponse.Model,
									"content":  "\r\n```\r\n开始调用工具：GetStockHolderNum，\n参数：" + funcArguments + "\r\n```\r\n",
									"time":     time.Now().Format(time.DateTime),
								}
								res := NewStockDataApi().GetStockHolderNum(gjson.Get(funcArguments, "stockCode").String())
								md := util.MarkdownTableWithTitle("股票"+gjson.Get(funcArguments, "stockCode").String()+"股东人数信息", res.Result.Data)
								logger.SugaredLogger.Infof("%s", md)
								messages = append(messages, map[string]interface{}{
									"role":              "assistant",
									"content":           currentAIContent.String(),
									"reasoning_content": reasoningContentText.String(),
									"tool_calls": []map[string]any{
										{
											"id":           currentCallId,
											"tool_call_id": currentCallId,
											"type":         "function",
											"function": map[string]string{
												"name":       funcName,
												"arguments":  funcArguments,
												"parameters": funcArguments,
											},
										},
									},
								})
								messages = append(messages, map[string]interface{}{
									"role":         "tool",
									"content":      md,
									"tool_call_id": currentCallId,
									//"reasoning_content": reasoningContentText.String(),
									//"tool_calls":        choice.Delta.ToolCalls,
								})
							}

							if funcName == "CreateAiRecommendStocks" {
								toolStart := time.Now()
								emitSummaryToolStatus(ch, "CreateAiRecommendStocks", "running", nil, 0)
								ch <- map[string]any{
									"code":     1,
									"question": question,
									"chatId":   streamResponse.Id,
									"model":    streamResponse.Model,
									"content":  "\r\n```\r\n开始调用工具：CreateAiRecommendStocks，\n参数：" + funcArguments + "\r\n```\r\n",
									"time":     time.Now().Format(time.DateTime),
								}
								recommend := models.AiRecommendStocks{}
								err := json.Unmarshal([]byte(funcArguments), &recommend)
								if err != nil {
									logger.SugaredLogger.Infof("CreateAiRecommendStocks error : %s", err.Error())
									emitSummaryToolStatus(ch, "CreateAiRecommendStocks", "error", err, time.Since(toolStart))
									return
								}
								if strings.TrimSpace(recommend.ProviderName) == "" {
									recommend.ProviderName = strings.TrimSpace(o.ProviderName)
								}
								err = NewAiRecommendStocksService().CreateAiRecommendStocks(&recommend)
								messages = append(messages, map[string]interface{}{
									"role":              "assistant",
									"content":           currentAIContent.String(),
									"reasoning_content": reasoningContentText.String(),
									"tool_calls": []map[string]any{
										{
											"id":           currentCallId,
											"tool_call_id": currentCallId,
											"type":         "function",
											"function": map[string]string{
												"name":       funcName,
												"arguments":  funcArguments,
												"parameters": funcArguments,
											},
										},
									},
								})
								if err != nil {
									logger.SugaredLogger.Infof("CreateAiRecommendStocks error : %s", err.Error())
									emitSummaryToolStatus(ch, "CreateAiRecommendStocks", "error", err, time.Since(toolStart))
									ch <- map[string]any{
										"code":     0,
										"question": question,
										"content":  "保存股票推荐失败:" + err.Error(),
									}
									return
								}
								messages = append(messages, map[string]interface{}{
									"role":         "tool",
									"content":      "保存股票推荐成功",
									"tool_call_id": currentCallId,
									//"reasoning_content": reasoningContentText.String(),
									//"tool_calls":        choice.Delta.ToolCalls,
								})
								emitSummaryToolStatus(ch, "CreateAiRecommendStocks", "success", nil, time.Since(toolStart))
							}

							//BatchCreateAiRecommendStocks
							if funcName == "BatchCreateAiRecommendStocks" {
								toolStart := time.Now()
								emitSummaryToolStatus(ch, "BatchCreateAiRecommendStocks", "running", nil, 0)
								ch <- map[string]any{
									"code":     1,
									"question": question,
									"chatId":   streamResponse.Id,
									"model":    streamResponse.Model,
									"content":  "\r\n```\r\n开始调用工具：BatchCreateAiRecommendStocks，\n参数：" + funcArguments + "\r\n```\r\n",
									"time":     time.Now().Format(time.DateTime),
								}
								stocks := gjson.Get(funcArguments, "stocks").String()
								var recommends []*models.AiRecommendStocks
								err := json.Unmarshal([]byte(stocks), &recommends)
								if err != nil {
									logger.SugaredLogger.Infof("BatchCreateAiRecommendStocks error : %s", err.Error())
									emitSummaryToolStatus(ch, "BatchCreateAiRecommendStocks", "error", err, time.Since(toolStart))
									return
								}
								providerName := strings.TrimSpace(o.ProviderName)
								for _, item := range recommends {
									if item == nil || strings.TrimSpace(item.ProviderName) != "" {
										continue
									}
									item.ProviderName = providerName
								}
								err = NewAiRecommendStocksService().BatchCreateAiRecommendStocks(recommends)
								messages = append(messages, map[string]interface{}{
									"role":              "assistant",
									"content":           currentAIContent.String(),
									"reasoning_content": reasoningContentText.String(),
									"tool_calls": []map[string]any{
										{
											"id":           currentCallId,
											"tool_call_id": currentCallId,
											"type":         "function",
											"function": map[string]string{
												"name":       funcName,
												"arguments":  funcArguments,
												"parameters": funcArguments,
											},
										},
									},
								})
								if err != nil {
									logger.SugaredLogger.Infof("BatchCreateAiRecommendStocks error : %s", err.Error())
									emitSummaryToolStatus(ch, "BatchCreateAiRecommendStocks", "error", err, time.Since(toolStart))
									ch <- map[string]any{
										"code":     0,
										"question": question,
										"content":  "批量保存股票推荐失败:" + err.Error(),
									}
									return
								}
								messages = append(messages, map[string]interface{}{
									"role":         "tool",
									"content":      "批量保存股票推荐成功",
									"tool_call_id": currentCallId,
									//"reasoning_content": reasoningContentText.String(),
									//"tool_calls":        choice.Delta.ToolCalls,
								})
								emitSummaryToolStatus(ch, "BatchCreateAiRecommendStocks", "success", nil, time.Since(toolStart))
							}

						}
						AskAiWithTools(o, messages, ch, question, tools, thinkingMode)
						return
					}

					if choice.FinishReason == "stop" {
						return
					}
				}
			} else {
				if err != nil {
					logger.SugaredLogger.Infof("Stream data error : %s", err.Error())
					//ch <- err.Error()
					ch <- map[string]any{
						"code":     0,
						"question": question,
						"content":  err.Error(),
					}
				} else {
					logger.SugaredLogger.Infof("Stream data error : %s", data)
					//ch <- data
					ch <- map[string]any{
						"code":     0,
						"question": question,
						"content":  data,
					}
				}
			}
		} else {
			if strutil.RemoveNonPrintable(line) != "" {
				logger.SugaredLogger.Infof("Stream data error : %s", line)
				res := &models.Resp{}
				if err := json.Unmarshal([]byte(line), res); err == nil {
					//ch <- line
					msg := res.Message
					if res.Error.Message != "" {
						msg = res.Error.Message
					}

					if msg == "Function call is not supported for this model." {
						var newMessages []map[string]any
						for _, message := range messages {
							if message["role"] == "tool" {
								continue
							}
							if _, ok := message["tool_calls"]; ok {
								continue
							}
							newMessages = append(newMessages, message)
						}
						AskAi(o, newMessages, ch, question, thinkingMode)
					} else {
						ch <- map[string]any{
							"code":     0,
							"question": question,
							"content":  msg,
						}
					}

				}
			}

		}

	}
	if scanErr := scanner.Err(); scanErr != nil {
		logger.SugaredLogger.Infof("Stream scanner error : %s", scanErr.Error())
		ch <- map[string]any{
			"code":     0,
			"question": question,
			"content":  o.formatAIRequestError(scanErr),
		}
	}
}
func checkIsIndexBasic(stock string) bool {
	count := int64(0)
	db.Dao.Model(&IndexBasic{}).Where("name =  ?", stock).Count(&count)
	return count > 0
}

func SearchGuShiTongStockInfo(stock string, crawlTimeOut int64) *[]string {
	crawlerAPI := CrawlerApi{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(crawlTimeOut)*time.Second)
	defer cancel()

	crawlerAPI = crawlerAPI.NewCrawler(ctx, CrawlerBaseInfo{
		Name:    "百度股市通",
		BaseUrl: "https://gushitong.baidu.com",
		Headers: map[string]string{"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36 Edg/133.0.0.0"},
	})
	url := "https://gushitong.baidu.com/stock/ab-" + RemoveAllNonDigitChar(stock)

	if strutil.HasPrefixAny(stock, []string{"HK", "hk"}) {
		url = "https://gushitong.baidu.com/stock/hk-" + RemoveAllNonDigitChar(stock)
	}
	if strutil.HasPrefixAny(stock, []string{"SZ", "SH", "sh", "sz"}) {
		url = "https://gushitong.baidu.com/stock/ab-" + RemoveAllNonDigitChar(stock)
	}
	if strutil.HasPrefixAny(stock, []string{"us", "US", "gb_", "gb"}) {
		url = "https://gushitong.baidu.com/stock/us-" + strings.Replace(stock, "gb_", "", 1)
	}

	//logger.SugaredLogger.Infof("SearchGuShiTongStockInfo搜索股票-%s: %s", stock, url)
	actions := []chromedp.Action{
		chromedp.Navigate(url),
		chromedp.WaitVisible("div.cos-tab"),
		chromedp.Click("div.cos-tab:nth-child(5)", chromedp.ByQuery),
		chromedp.ScrollIntoView("div.body-box"),
		chromedp.WaitVisible("div.body-col"),
		chromedp.Evaluate(`window.scrollTo(0, document.body.scrollHeight);`, nil),
		chromedp.Sleep(1 * time.Second),
	}
	htmlContent, success := crawlerAPI.GetHtmlWithActions(&actions, true)
	var messages []string
	if success {
		document, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
		if err != nil {
			logger.SugaredLogger.Error(err.Error())
			return &[]string{}
		}
		document.Find("div.finance-hover,div.list-date").Each(func(i int, selection *goquery.Selection) {
			text := strutil.RemoveWhiteSpace(selection.Text(), false)
			messages = append(messages, ReplaceSensitiveWords(text))
			//logger.SugaredLogger.Infof("SearchGuShiTongStockInfo搜索到消息-%s: %s", "", text)
		})
		//logger.SugaredLogger.Infof("messages:%d", len(messages))
	}
	return &messages
}
func GetFinancialReportsByXUEQIU(stockCode string, crawlTimeOut int64) *[]string {
	if strutil.HasPrefixAny(stockCode, []string{"HK", "hk"}) {
		stockCode = strings.ReplaceAll(stockCode, "hk", "")
		stockCode = strings.ReplaceAll(stockCode, "HK", "")
	}
	if strutil.HasPrefixAny(stockCode, []string{"us", "gb_"}) {
		stockCode = strings.ReplaceAll(stockCode, "us", "")
		stockCode = strings.ReplaceAll(stockCode, "gb_", "")
	}
	url := fmt.Sprintf("https://xueqiu.com/snowman/S/%s/detail#/ZYCWZB", stockCode)
	waitVisible := "div.tab-table-responsive table"
	crawlerAPI := CrawlerApi{}
	crawlerBaseInfo := CrawlerBaseInfo{
		Name:        "TestCrawler",
		Description: "Test Crawler Description",
		BaseUrl:     "https://xueqiu.com",
		Headers:     map[string]string{"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36 Edg/133.0.0.0"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(crawlTimeOut)*time.Second)
	defer cancel()
	crawlerAPI = crawlerAPI.NewCrawler(ctx, crawlerBaseInfo)

	var markdown strings.Builder
	markdown.WriteString("\n## 财务数据：\n")
	html, ok := crawlerAPI.GetHtml(url, waitVisible, true)
	if !ok {
		return &[]string{""}
	}
	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		logger.SugaredLogger.Error(err.Error())
	}
	GetTableMarkdown(document, waitVisible, &markdown)
	return &[]string{markdown.String()}
}
func GetFinancialReports(stockCode string, crawlTimeOut int64) *[]string {
	url := "https://emweb.securities.eastmoney.com/pc_hsf10/pages/index.html?type=web&code=" + stockCode + "#/cwfx"
	waitVisible := "div.report_table table"
	if strutil.HasPrefixAny(stockCode, []string{"HK", "hk"}) {
		stockCode = strings.ReplaceAll(stockCode, "hk", "")
		stockCode = strings.ReplaceAll(stockCode, "HK", "")
		url = "https://emweb.securities.eastmoney.com/PC_HKF10/pages/home/index.html?code=" + stockCode + "&type=web&color=w#/NewFinancialAnalysis"
		waitVisible = "div table.commonTable"
	}
	if strutil.HasPrefixAny(stockCode, []string{"us", "gb_"}) {
		stockCode = strings.ReplaceAll(stockCode, "us", "")
		stockCode = strings.ReplaceAll(stockCode, "gb_", "")
		url = "https://emweb.securities.eastmoney.com/pc_usf10/pages/index.html?type=web&code=" + stockCode + "#/cwfx"
		waitVisible = "div.zyzb_table_detail table"

	}

	//logger.SugaredLogger.Infof("GetFinancialReports搜索股票-%s: %s", stockCode, url)

	crawlerAPI := CrawlerApi{}
	crawlerBaseInfo := CrawlerBaseInfo{
		Name:        "TestCrawler",
		Description: "Test Crawler Description",
		BaseUrl:     "https://emweb.securities.eastmoney.com",
		Headers:     map[string]string{"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36 Edg/133.0.0.0"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(crawlTimeOut)*time.Second)
	defer cancel()
	crawlerAPI = crawlerAPI.NewCrawler(ctx, crawlerBaseInfo)

	var markdown strings.Builder
	markdown.WriteString("\n## 财务数据：\n")
	html, ok := crawlerAPI.GetHtml(url, waitVisible, true)
	if !ok {
		return &[]string{""}
	}
	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		logger.SugaredLogger.Error(err.Error())
	}
	GetTableMarkdown(document, waitVisible, &markdown)
	return &[]string{markdown.String()}
}

func GetTelegraphList(crawlTimeOut int64) *[]string {
	url := "https://www.cls.cn/telegraph"
	response, err := newFetchRestyClient().SetTimeout(time.Duration(crawlTimeOut)*time.Second).R().
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
		telegraph = append(telegraph, ReplaceSensitiveWords(selection.Text()))
	})
	return &telegraph
}

func GetTopNewsList(crawlTimeOut int64) *[]string {
	url := "https://www.cls.cn"
	response, err := newFetchRestyClient().SetTimeout(time.Duration(crawlTimeOut)*time.Second).R().
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
	document.Find("div.home-article-title a,div.home-article-rec a").Each(func(i int, selection *goquery.Selection) {
		//logger.SugaredLogger.Info(selection.Text())
		telegraph = append(telegraph, ReplaceSensitiveWords(selection.Text()))
	})
	return &telegraph
}

func (o *OpenAi) SaveAIResponseResult(stockCode, stockName, result, chatId, question string) {
	db.Dao.Create(&models.AIResponseResult{
		StockCode:    stockCode,
		StockName:    stockName,
		ProviderName: strings.TrimSpace(o.ProviderName),
		ModelName:    o.Model,
		Content:      result,
		ChatId:       chatId,
		Question:     question,
	})
}

func (o *OpenAi) GetAIResponseResult(stock string) *models.AIResponseResult {
	var result models.AIResponseResult
	db.Dao.Where("stock_code = ?", stock).Order("id desc").Limit(1).Find(&result)
	sanitizeAIResponseResultForDisplay(&result)
	return &result
}
