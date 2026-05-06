package data

import "strings"

const DefaultMarketSummaryQuestion = "总结和分析股票市场新闻中的投资机会，并推荐2个A股，并给出关键价位与交易计划"

const marketSummaryOutputInstruction = `
【市场资讯AI总结输出规范】
你输出的最终结果必须是 Markdown，且必须包含以下 7 个一级标题，顺序固定：
# 市场主线
# 候选方向
# 风险提示
# 推荐结论
# 交易计划说明
# 推荐股票池
# 跳过复审

要求：
1. “市场主线”必须总结当日/最近市场最强的 2-4 条主线或主题，每条都要说明核心催化与当前证据。
2. “候选方向”必须分点写出每条方向的受益链条、关注条件、失效信号。
3. “风险提示”必须单独列出至少 3 条风险，不得省略。
4. “推荐结论”必须先给出总体判断，再说明为什么这些股票值得进入可交易计划。
5. “交易计划说明”必须单独说明筛选口径与交易口径：不输出观察/淘汰标签，只输出能形成买卖计划的股票；证据不足或逻辑冲突的股票直接不进入推荐股票池。
6. “推荐股票池”必须输出一个 Markdown 表格；如没有高质量候选，可以写“暂无高质量候选标的”，但不要编造股票。
7. “跳过复审”必须输出一个 Markdown 表格，用于复审前三个交易日内已跳过的股票；该节只服务于收益率跟踪覆盖，不影响本次推荐股票池。

“推荐股票池”表格列名必须尽量使用以下字段：
| 股票（代码） | 所属方向 | 核心催化 | 关键证据 | 价格锚点 | 买入区间 | 止盈区间 | 止损位 | 买入依据 | 失效条件 | 风险点 | 预期周期 | 事件强度 | 资金确认度 | 基本面匹配度 | 技术面匹配度 | 操作备注 |

“跳过复审”表格列名必须尽量使用以下字段：
| 原记录ID | 股票（代码） | 复审结论 | 买入区间 | 止盈区间 | 止损位 | 买入依据 | 失效条件 | 跳过/复审说明 |

约束：
- 推荐股票必须是 A 股。
- 推荐股票池最多输出 2 只股票，只保留证据最完整、评分最高、最接近可执行交易计划的两只；不足 2 只时宁缺毋滥。
- 每只股票都必须同时给出：核心催化、关键证据、价格锚点、买入区间、止盈区间、止损位、买入依据、失效条件、风险点、预期周期、4维置信度、操作备注。
- 只允许输出“推荐股票”，不要输出“观察标的 / 回避标的 / 低吸候选 / 右侧确认”这类标签。
- 必须先结合“当前时间”和A股交易时段判断输出方式：
  1. 不再输出“立刻买入”类标签，所有可执行推荐统一写成“等待激活”计划；
  2. 无论盘中还是非交易时段，都必须把触发条件写成未来可验证的激活条件，不能把“当前强势/可以直接买/不追”当成结论；
  3. 若当前时间下无法形成未来3-5个交易日内可验证的交易计划，就不要推荐任何股票。
- 若证据不足、证据冲突、价格位置不支持交易计划，就直接不要把该股票写入推荐股票池。
- “买入依据”必须使用固定硬格式，一行内写成：
  价格触发：...；量能触发：...
- 市场资讯来源默认生成“双路径激活”：
  1. 回踩激活路径：价格先回到主买入区，再做量价确认；
  2. 突破激活路径：若股价未回踩而是继续走强，允许给出靠近当前锚点的突破激活价；
  3. 两条路径都必须是未来3-5个交易日内可验证的机器条件，不能写成自然语言废话。
- 除了 Markdown 表格，你在调用 CreateAiRecommendStocks 或 BatchCreateAiRecommendStocks 时，必须同步传入 activationRuleJson，作为收益率 strict 模式唯一可信的激活依据。
- activationRuleJson 禁止使用自然语言模糊词，必须改成机器字段，例如：
  {"signalType":"price_range_with_volume","evaluationWindow":"5m","baseline":"avg_amount_5x5m","operator":">=","thresholdValue":9.42,"thresholdMax":9.56,"volumeRatio":1.2,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5}
- 若同时给出回踩激活与突破激活，activationRuleJson 必须输出双路径 JSON；breakout 路径里的 thresholdMax 表示最高可买价/追价上限，必须低于止盈区间下沿，例如：
  {"version":"v2","mode":"any_of","paths":[{"name":"pullback","signalType":"price_range_with_volume","evaluationWindow":"5m","baseline":"avg_amount_5x5m","operator":">=","thresholdValue":9.42,"thresholdMax":9.56,"volumeRatio":1.15,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5},{"name":"breakout","signalType":"price_breakout_with_volume","evaluationWindow":"5m","baseline":"avg_amount_5x5m","operator":">=","thresholdValue":9.60,"thresholdMax":9.72,"volumeRatio":1.2,"confirmBars":1,"volumeWindow":5,"volumeMetric":"amount","expireTradeDays":5}]}
- “失效条件”必须使用固定硬格式，一行内写成：
  时间失效：...；价格失效：...
- “买入依据”中的量化条件必须写明：
  1. 触发价位或触发区间；
  2. 观察周期，如 1分钟 / 5分钟 / 15分钟；
  3. 比较基准，如近5个5分钟均量、上一交易日同一时刻成交额、量比阈值；
  4. 明确阈值，如 ≥1.2倍、量比≥1.5、连续2根5分钟K线站稳；
  5. 不能使用“放量”“缩量”“强势”“承接”“高开过大”“不追”这类未量化表述直接充当条件。
- “止盈区间”“止损位”“失效条件”必须彼此匹配，不能出现价格止损和失效条件互相冲突的情况。
- 所有需要等待触发的计划，触发有效期必须限制在未来3-5个交易日内；超过窗口仍未触发，就视为失效，不应再纳入收益率跟踪。
- 若提到放量/缩量/量比/量能，必须同时写清：
  1. 在什么价位、均线、前高/前低附近观察；
  2. 相对什么基准比较，如近5日均量、上一交易日同时段量能、量比阈值；
  3. 用什么周期观察，如1分钟、5分钟、15分钟、日线；
  4. 达到什么阈值才算触发，如 成交额≥近5个5分钟均额的1.2倍。
- 关键证据必须显式带证据标签，如：[市场资讯]、[个股新闻]、[行业研报]、[财报/财务]、[互动易]、[技术/资金/形态]、[一级披露]、[资金结构]、[股东/筹码]、[产业高频]、[海外风险]。
- 若证据核验层给出了 technicalMetrics / technicalSnapshot，买入依据必须优先结合 MA5、MA10、MA20、近3/5/20日高低点、分钟量能相对均量倍数、量比、换手等结构化指标，不要空泛描述“形态不错”。
- 若证据核验层给出了 auctionPrice 或 priceAnchorSource=call_auction，价格锚点、买入区间、止盈区间、止损位必须优先围绕集合竞价价格锚点生成，并结合委比、量比、买卖盘结构判断强弱，不能把竞价结果当成已开盘走势硬推。
- 若证据核验层给出了 minutePrice / minuteAmount / minuteVolume，且当前锚点不是集合竞价，价格锚点、买入区间、止盈区间、止损位必须优先围绕该实时分钟线价格生成，并结合最新一分钟成交额/成交量判断活跃度。
- stockPrice 字段应优先填写当前价格锚点；集合竞价时优先 auctionPrice，其次 minutePrice，当两者都缺失时才允许回退到 CurrentPrice。
- 价格锚点、买入区间、突破激活价、止盈区间、止损位必须与当前锚点保持同一价格数量级；若任一关键价位相对当前锚点偏离超过20%，视为无效输出，必须重写。
- 突破激活价只代表触发确认价，不代表可以无限追高；若设置突破路径，必须同步给出低于止盈区间下沿的最高可买价 thresholdMax，且预留手续费、滑点和最小收益空间。
- 至少要有 2 类不同证据，且至少 1 条来自高信任源（优先：一级披露 / 财报财务 / 互动易）；证据不足时不得进入推荐股票池。
- 如存在公告与媒体解读冲突，必须输出争议结论，并从推荐股票池中剔除，不得强行给出交易计划。
- “跳过复审”中的“原记录ID”必须直接引用输入里的 recommendId，不得编造。
- “跳过复审”中的“复审结论”只能使用以下几种明确表达之一：继续跳过、等待激活、重新纳入、改判可交易。
- 若某只已跳过股票复审后仍不具备未来3-5个交易日内可验证的交易计划，就写“继续跳过”，并把理由写到“跳过/复审说明”。
- 若某只已跳过股票复审后恢复为可交易计划，必须重新写完整的买入区间、止盈区间、止损位、买入依据、失效条件；这些字段将用于覆盖收益率页面对应行。
- 若输入里没有“前三个交易日已跳过股票复审候选池”，“# 跳过复审”章节也不能省略，可在表格中写“暂无需要复审的已跳过股票”。
- 不要输出旧版兼容字段，也不要用标签语义替代清晰交易计划。
- 调用 CreateAiRecommendStocks 或 BatchCreateAiRecommendStocks 前，必须确保记录字段完整，优先传 evidenceSources JSON 字符串和 activationRuleJson，不能只保存空泛观点。`

const marketSummaryInstructionMarker = "【市场资讯AI总结输出规范】"

var marketSummaryQuestionPlaceholders = []string{
	"{{stockName}}",
	"{{stockCode}}",
	"{stockName}",
	"{stockCode}",
	"stockName",
	"stockCode",
}

func containsMarketSummaryPlaceholders(text string) bool {
	for _, placeholder := range marketSummaryQuestionPlaceholders {
		if strings.Contains(text, placeholder) {
			return true
		}
	}
	return false
}

func stripMarketSummaryInstruction(text string) string {
	content := strings.TrimSpace(text)
	if content == "" {
		return ""
	}
	if idx := strings.Index(content, marketSummaryInstructionMarker); idx >= 0 {
		content = strings.TrimSpace(content[:idx])
	}
	return strings.TrimSpace(content)
}

func NormalizeMarketSummaryQuestion(question string) string {
	raw := strings.TrimSpace(question)
	if raw == "" {
		return DefaultMarketSummaryQuestion
	}
	if containsMarketSummaryPlaceholders(raw) {
		return DefaultMarketSummaryQuestion
	}

	text := stripMarketSummaryInstruction(raw)
	if text == "" {
		return DefaultMarketSummaryQuestion
	}

	switch text {
	case "总结和分析股票市场新闻中的投资机会":
		return DefaultMarketSummaryQuestion
	case "请根据当前时间，总结和分析股票市场新闻中的投资机会":
		return DefaultMarketSummaryQuestion
	case "总结和分析股票市场新闻中的投资机会，并推荐3只A股股票":
		return DefaultMarketSummaryQuestion
	case "总结和分析股票市场新闻中的投资机会，并推荐3个A股股票":
		return DefaultMarketSummaryQuestion
	case "总结和分析股票市场新闻中的投资机会，并推荐3只A股":
		return DefaultMarketSummaryQuestion
	case "总结和分析股票市场新闻中的投资机会，并推荐3个A股":
		return DefaultMarketSummaryQuestion
	case "市场资讯分析和总结":
		return DefaultMarketSummaryQuestion
	case "市场资讯分析":
		return DefaultMarketSummaryQuestion
	}

	if strings.HasPrefix(text, "请根据当前时间，总结和分析股票市场新闻中的投资机会") &&
		!strings.Contains(text, "买卖区间") &&
		!strings.Contains(text, "关键价位") &&
		!strings.Contains(text, "交易计划") {
		return DefaultMarketSummaryQuestion
	}

	return text
}

func BuildMarketSummaryExecutionQuestion(question string) string {
	content := NormalizeMarketSummaryQuestion(question)
	if content == "" {
		content = DefaultMarketSummaryQuestion
	}
	if strings.Contains(content, marketSummaryInstructionMarker) {
		return content
	}
	return strings.TrimSpace(content + "\n\n" + strings.TrimSpace(marketSummaryOutputInstruction))
}

func RenderMarketSummaryTemplate(text string) string {
	content := strings.TrimSpace(text)
	if content == "" {
		return ""
	}
	replacer := strings.NewReplacer(
		"{{stockName}}", "市场资讯",
		"{{stockCode}}", "市场资讯",
		"{stockName}", "市场资讯",
		"{stockCode}", "市场资讯",
		"stockName", "市场资讯",
		"stockCode", "市场资讯",
	)
	content = strings.TrimSpace(replacer.Replace(content))
	if strings.Contains(content, marketSummaryInstructionMarker) {
		return content
	}
	return strings.TrimSpace(content + "\n\n" + strings.TrimSpace(marketSummaryOutputInstruction))
}

func ResolveMarketSummaryQuestion(question string) string {
	return BuildMarketSummaryExecutionQuestion(question)
}
