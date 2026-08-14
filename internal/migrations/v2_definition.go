package migrations

// mainMigrationV2Definition is the exact published schema/guard checksum input.
// Do not derive it from current Go models or the current release manifest.
const mainMigrationV2Definition = `main_models:35
0:StockInfo:51
0:Model:gorm.io/gorm.Model:"":true
1:Date:string:"json:\"日期\" gorm:\"index\"":false
2:Time:string:"json:\"时间\" gorm:\"index\"":false
3:Code:string:"json:\"股票代码\" gorm:\"index\"":false
4:Name:string:"json:\"股票名称\" gorm:\"index\"":false
5:PrePrice:float64:"json:\"上次当前价格\"":false
6:Price:string:"json:\"当前价格\"":false
7:Volume:string:"json:\"成交的股票数\"":false
8:Amount:string:"json:\"成交金额\"":false
9:Open:string:"json:\"今日开盘价\"":false
10:PreClose:string:"json:\"昨日收盘价\"":false
11:High:string:"json:\"今日最高价\"":false
12:Low:string:"json:\"今日最低价\"":false
13:Bid:string:"json:\"竞买价\"":false
14:Ask:string:"json:\"竞卖价\"":false
15:B1P:string:"json:\"买一报价\"":false
16:B1V:string:"json:\"买一申报\"":false
17:B2P:string:"json:\"买二报价\"":false
18:B2V:string:"json:\"买二申报\"":false
19:B3P:string:"json:\"买三报价\"":false
20:B3V:string:"json:\"买三申报\"":false
21:B4P:string:"json:\"买四报价\"":false
22:B4V:string:"json:\"买四申报\"":false
23:B5P:string:"json:\"买五报价\"":false
24:B5V:string:"json:\"买五申报\"":false
25:A1P:string:"json:\"卖一报价\"":false
26:A1V:string:"json:\"卖一申报\"":false
27:A2P:string:"json:\"卖二报价\"":false
28:A2V:string:"json:\"卖二申报\"":false
29:A3P:string:"json:\"卖三报价\"":false
30:A3V:string:"json:\"卖三申报\"":false
31:A4P:string:"json:\"卖四报价\"":false
32:A4V:string:"json:\"卖四申报\"":false
33:A5P:string:"json:\"卖五报价\"":false
34:A5V:string:"json:\"卖五申报\"":false
35:Market:string:"json:\"市场\"":false
36:BA:string:"json:\"盘前盘后\"":false
37:BAChange:string:"json:\"盘前盘后涨跌幅\"":false
38:ChangePercent:float64:"json:\"changePercent\"":false
39:ChangePrice:float64:"json:\"changePrice\"":false
40:HighRate:float64:"json:\"highRate\"":false
41:LowRate:float64:"json:\"lowRate\"":false
42:CostPrice:float64:"json:\"costPrice\"":false
43:CostVolume:int64:"json:\"costVolume\"":false
44:Profit:float64:"json:\"profit\"":false
45:ProfitAmount:float64:"json:\"profitAmount\"":false
46:ProfitAmountToday:float64:"json:\"profitAmountToday\"":false
47:Sort:int64:"json:\"sort\"":false
48:AlarmChangePercent:float64:"json:\"alarmChangePercent\"":false
49:AlarmPrice:float64:"json:\"alarmPrice\"":false
50:Groups:[]GroupStock:"gorm:\"-:all\"":false
1:StockBasic:20
0:Model:gorm.io/gorm.Model:"":true
1:TsCode:string:"json:\"ts_code\" gorm:\"index\"":false
2:Symbol:string:"json:\"symbol\" gorm:\"index\"":false
3:Name:string:"json:\"name\" gorm:\"index\"":false
4:Area:string:"json:\"area\"":false
5:Industry:string:"json:\"industry\" gorm:\"index\"":false
6:Fullname:string:"json:\"fullname\"":false
7:Ename:string:"json:\"enname\"":false
8:Cnspell:string:"json:\"cnspell\"":false
9:Market:string:"json:\"market\"":false
10:Exchange:string:"json:\"exchange\"":false
11:CurrType:string:"json:\"curr_type\"":false
12:ListStatus:string:"json:\"list_status\"":false
13:ListDate:string:"json:\"list_date\"":false
14:DelistDate:string:"json:\"delist_date\"":false
15:IsHs:string:"json:\"is_hs\"":false
16:ActName:string:"json:\"act_name\"":false
17:ActEntType:string:"json:\"act_ent_type\"":false
18:BKName:string:"json:\"bk_name\"":false
19:BKCode:string:"json:\"bk_code\"":false
2:FollowedStock:15
0:StockCode:string:"":false
1:Name:string:"":false
2:Volume:int64:"":false
3:CostPrice:float64:"":false
4:Price:float64:"":false
5:PriceChange:float64:"":false
6:ChangePercent:float64:"":false
7:AlarmChangePercent:float64:"":false
8:AlarmPrice:float64:"":false
9:Time:time.Time:"":false
10:Sort:int64:"":false
11:Cron:*string:"":false
12:IsDel:gorm.io/plugin/soft_delete.DeletedAt:"gorm:\"softDelete:flag\"":false
13:Groups:[]GroupStock:"gorm:\"foreignKey:StockCode;references:StockCode\"":false
14:AiConfigId:int:"":false
3:IndexBasic:14
0:Model:gorm.io/gorm.Model:"":true
1:TsCode:string:"json:\"ts_code\" gorm:\"index\"":false
2:Symbol:string:"json:\"symbol\" gorm:\"index\"":false
3:Name:string:"json:\"name\" gorm:\"index\"":false
4:FullName:string:"json:\"fullname\"":false
5:IndexType:string:"json:\"index_type\"":false
6:IndexCategory:string:"json:\"category\"":false
7:Market:string:"json:\"market\"":false
8:ListDate:string:"json:\"list_date\"":false
9:BaseDate:string:"json:\"base_date\"":false
10:BasePoint:float64:"json:\"base_point\"":false
11:Publisher:string:"json:\"publisher\"":false
12:WeightRule:string:"json:\"weight_rule\"":false
13:DESC:string:"json:\"desc\"":false
4:Settings:52
0:Model:gorm.io/gorm.Model:"":true
1:TushareToken:string:"json:\"tushareToken\"":false
2:LocalPushEnable:bool:"json:\"localPushEnable\"":false
3:DingPushEnable:bool:"json:\"dingPushEnable\"":false
4:DingRobot:string:"json:\"dingRobot\"":false
5:YieldEmailEnable:bool:"json:\"yieldEmailEnable\"":false
6:YieldEmailTo:string:"json:\"yieldEmailTo\"":false
7:YieldEmailFrom:string:"json:\"yieldEmailFrom\"":false
8:YieldEmailSMTPHost:string:"json:\"yieldEmailSmtpHost\"":false
9:YieldEmailSMTPPort:int:"json:\"yieldEmailSmtpPort\"":false
10:YieldEmailSMTPUsername:string:"json:\"yieldEmailSmtpUsername\"":false
11:YieldEmailSMTPPassword:string:"json:\"yieldEmailSmtpPassword\"":false
12:YieldEmailCronEnabled:bool:"json:\"yieldEmailCronEnabled\"":false
13:YieldEmailCronTimes:string:"json:\"yieldEmailCronTimes\"":false
14:MarketSummaryEmailEnable:bool:"json:\"marketSummaryEmailEnabled\"":false
15:UpdateBasicInfoOnStart:bool:"json:\"updateBasicInfoOnStart\"":false
16:RefreshInterval:int64:"json:\"refreshInterval\"":false
17:OpenAiEnable:bool:"json:\"openAiEnable\"":false
18:Prompt:string:"json:\"prompt\"":false
19:CheckUpdate:bool:"json:\"checkUpdate\"":false
20:QuestionTemplate:string:"json:\"questionTemplate\"":false
21:CrawlTimeOut:int64:"json:\"crawlTimeOut\"":false
22:KDays:int64:"json:\"kDays\"":false
23:EnableDanmu:bool:"json:\"enableDanmu\"":false
24:BrowserPath:string:"json:\"browserPath\"":false
25:EnableNews:bool:"json:\"enableNews\"":false
26:DarkTheme:bool:"json:\"darkTheme\"":false
27:BrowserPoolSize:int:"json:\"browserPoolSize\"":false
28:EnableFund:bool:"json:\"enableFund\"":false
29:EnablePushNews:bool:"json:\"enablePushNews\"":false
30:EnableOnlyPushRedNews:bool:"json:\"enableOnlyPushRedNews\"":false
31:HttpProxy:string:"json:\"httpProxy\"":false
32:HttpProxyEnabled:bool:"json:\"httpProxyEnabled\"":false
33:ForceNoProxyForFetch:bool:"json:\"forceNoProxyForFetch\" gorm:\"default:true\"":false
34:EnableAgent:bool:"json:\"enableAgent\"":false
35:QgqpBId:string:"json:\"qgqpBId\" gorm:\"column:qgqp_b_id\"":false
36:MarketSummaryCronEnabled:bool:"json:\"marketSummaryCronEnabled\" gorm:\"default:true\"":false
37:MarketSummaryCronTimes:string:"json:\"marketSummaryCronTimes\" gorm:\"default:'09:40,11:30,14:30'\"":false
38:MinuteProviderMode:string:"json:\"minuteProviderMode\" gorm:\"default:'public'\"":false
39:MinuteLongHistoryHint:bool:"json:\"minuteLongHistoryHintEnabled\" gorm:\"column:minute_long_history_hint_enabled;default:true\"":false
40:PrivateMinuteEnabled:bool:"json:\"privateMinuteEnabled\"":false
41:PrivateMinuteBaseURL:string:"json:\"privateMinuteBaseUrl\"":false
42:PrivateMinuteAPIKey:string:"json:\"privateMinuteApiKey\"":false
43:PrivateMinuteTimeoutSec:int:"json:\"privateMinuteTimeoutSec\"":false
44:PrivateMinuteMinInterval:int:"json:\"privateMinuteMinIntervalMs\"":false
45:PrivateMinuteProxyMode:string:"json:\"privateMinuteProxyMode\" gorm:\"default:'disable'\"":false
46:PrivateMinuteLevel:string:"json:\"privateMinuteLevel\" gorm:\"default:'1min'\"":false
47:AkshareEnabled:bool:"json:\"akshareEnabled\" gorm:\"default:true\"":false
48:SinaMinuteEnabled:bool:"json:\"sinaMinuteEnabled\" gorm:\"default:true\"":false
49:TencentMinuteEnabled:bool:"json:\"tencentMinuteEnabled\" gorm:\"default:true\"":false
50:EastmoneyMinuteEnabled:bool:"json:\"eastmoneyMinuteEnabled\" gorm:\"default:true\"":false
51:AkshareMinuteSourceMode:string:"json:\"akshareMinuteSourceMode\" gorm:\"default:'auto'\"":false
5:AIResponseResult:9
0:Model:gorm.io/gorm.Model:"":true
1:ChatId:string:"json:\"chatId\"":false
2:ProviderName:string:"json:\"providerName\" gorm:\"size:128\"":false
3:ModelName:string:"json:\"modelName\"":false
4:StockCode:string:"json:\"stockCode\"":false
5:StockName:string:"json:\"stockName\"":false
6:Question:string:"json:\"question\"":false
7:Content:string:"json:\"content\"":false
8:IsDel:gorm.io/plugin/soft_delete.DeletedAt:"gorm:\"softDelete:flag\"":false
6:AgentChatSession:9
0:Model:gorm.io/gorm.Model:"":true
1:SessionId:string:"json:\"sessionId\" gorm:\"size:128;uniqueIndex\"":false
2:Title:string:"json:\"title\" gorm:\"size:128\"":false
3:AiConfigId:uint:"json:\"aiConfigId\" gorm:\"index\"":false
4:ModelName:string:"json:\"modelName\" gorm:\"size:128\"":false
5:LastMessageAt:*time.Time:"json:\"lastMessageAt\" gorm:\"index\"":false
6:MessageCount:int:"json:\"messageCount\"":false
7:IsPinned:bool:"json:\"isPinned\" gorm:\"index\"":false
8:IsDel:gorm.io/plugin/soft_delete.DeletedAt:"json:\"isDel\" gorm:\"softDelete:flag\"":false
7:AgentChatMessage:7
0:Model:gorm.io/gorm.Model:"":true
1:SessionId:string:"json:\"sessionId\" gorm:\"size:128;index\"":false
2:Role:string:"json:\"role\" gorm:\"size:32;index\"":false
3:Content:string:"json:\"content\"":false
4:Reasoning:string:"json:\"reasoning\"":false
5:Seq:int:"json:\"seq\" gorm:\"index\"":false
6:IsDel:gorm.io/plugin/soft_delete.DeletedAt:"json:\"isDel\" gorm:\"softDelete:flag\"":false
8:StockInfoHK:8
0:Model:gorm.io/gorm.Model:"":true
1:Code:string:"json:\"code\"":false
2:Name:string:"json:\"name\"":false
3:FullName:string:"json:\"fullName\"":false
4:EName:string:"json:\"eName\"":false
5:IsDel:gorm.io/plugin/soft_delete.DeletedAt:"gorm:\"softDelete:flag\"":false
6:BKName:string:"json:\"bk_name\"":false
7:BKCode:string:"json:\"bk_code\"":false
9:StockInfoUS:10
0:Model:gorm.io/gorm.Model:"":true
1:Code:string:"json:\"code\"":false
2:Name:string:"json:\"name\"":false
3:FullName:string:"json:\"fullName\"":false
4:EName:string:"json:\"eName\"":false
5:Exchange:string:"json:\"exchange\"":false
6:Type:string:"json:\"type\"":false
7:IsDel:gorm.io/plugin/soft_delete.DeletedAt:"gorm:\"softDelete:flag\"":false
8:BKName:string:"json:\"bk_name\"":false
9:BKCode:string:"json:\"bk_code\"":false
10:FollowedFund:10
0:Model:gorm.io/gorm.Model:"":true
1:Code:string:"json:\"code\" gorm:\"index\"":false
2:Name:string:"json:\"name\"":false
3:NetUnitValue:*float64:"json:\"netUnitValue\"":false
4:NetUnitValueDate:string:"json:\"netUnitValueDate\"":false
5:NetEstimatedUnit:*float64:"json:\"netEstimatedUnit\"":false
6:NetEstimatedTime:string:"json:\"netEstimatedUnitTime\"":false
7:NetAccumulated:*float64:"json:\"netAccumulated\"":false
8:NetEstimatedRate:*float64:"json:\"netEstimatedRate\"":false
9:FundBasic:FundBasic:"json:\"fundBasic\" gorm:\"foreignKey:Code;references:Code\"":false
11:FundBasic:24
0:Model:gorm.io/gorm.Model:"":true
1:Code:string:"json:\"code\" gorm:\"index\"":false
2:Name:string:"json:\"name\"":false
3:FullName:string:"json:\"fullName\"":false
4:Type:string:"json:\"type\"":false
5:Establishment:string:"json:\"establishment\"":false
6:Scale:string:"json:\"scale\"":false
7:Company:string:"json:\"company\"":false
8:Manager:string:"json:\"manager\"":false
9:Rating:string:"json:\"rating\"":false
10:TrackingTarget:string:"json:\"trackingTarget\"":false
11:NetUnitValue:*float64:"json:\"netUnitValue\"":false
12:NetUnitValueDate:string:"json:\"netUnitValueDate\"":false
13:NetEstimatedUnit:*float64:"json:\"netEstimatedUnit\"":false
14:NetEstimatedTime:string:"json:\"netEstimatedUnitTime\"":false
15:NetAccumulated:*float64:"json:\"netAccumulated\"":false
16:NetGrowth1:*float64:"json:\"netGrowth1\"":false
17:NetGrowth3:*float64:"json:\"netGrowth3\"":false
18:NetGrowth6:*float64:"json:\"netGrowth6\"":false
19:NetGrowth12:*float64:"json:\"netGrowth12\"":false
20:NetGrowth36:*float64:"json:\"netGrowth36\"":false
21:NetGrowth60:*float64:"json:\"netGrowth60\"":false
22:NetGrowthYTD:*float64:"json:\"netGrowthYTD\"":false
23:NetGrowthAll:*float64:"json:\"netGrowthAll\"":false
12:PromptTemplate:6
0:ID:int:"gorm:\"primarykey\"":false
1:CreatedAt:time.Time:"":false
2:UpdatedAt:time.Time:"":false
3:Name:string:"json:\"name\"":false
4:Content:string:"json:\"content\"":false
5:Type:string:"json:\"type\"":false
13:Group:3
0:Model:gorm.io/gorm.Model:"":true
1:Name:string:"json:\"name\" gorm:\"index\"":false
2:Sort:int:"json:\"sort\"":false
14:GroupStock:4
0:Model:gorm.io/gorm.Model:"":true
1:StockCode:string:"json:\"stockCode\" gorm:\"index\"":false
2:GroupId:int:"json:\"groupId\" gorm:\"index\"":false
3:GroupInfo:Group:"json:\"groupInfo\" gorm:\"foreignKey:GroupId;references:ID\"":false
15:Tags:3
0:Model:gorm.io/gorm.Model:"":true
1:Name:string:"json:\"name\"":false
2:Type:string:"json:\"type\"":false
16:Telegraph:12
0:Model:gorm.io/gorm.Model:"":true
1:Time:string:"json:\"time\"":false
2:DataTime:*time.Time:"json:\"dataTime\" gorm:\"index\"":false
3:Title:string:"json:\"title\" gorm:\"index\"":false
4:Content:string:"json:\"content\" gorm:\"index\"":false
5:SubjectTags:[]string:"json:\"subjects\" gorm:\"-:all\"":false
6:StocksTags:[]string:"json:\"stocks\" gorm:\"-:all\"":false
7:IsRed:bool:"json:\"isRed\" gorm:\"index\"":false
8:Url:string:"json:\"url\"":false
9:Source:string:"json:\"source\" gorm:\"index\"":false
10:TelegraphTags:[]TelegraphTags:"json:\"tags\" gorm:\"-:migration;foreignKey:TelegraphId\"":false
11:SentimentResult:string:"json:\"sentimentResult\" gorm:\"index\"":false
17:TelegraphTags:3
0:Model:gorm.io/gorm.Model:"":true
1:TagId:uint:"json:\"tagId\"":false
2:TelegraphId:uint:"json:\"telegraphId\"":false
18:LongTigerRankData:18
0:ACCUMAMOUNT:float64:"json:\"ACCUM_AMOUNT\"":false
1:BILLBOARDBUYAMT:float64:"json:\"BILLBOARD_BUY_AMT\"":false
2:BILLBOARDDEALAMT:float64:"json:\"BILLBOARD_DEAL_AMT\"":false
3:BILLBOARDNETAMT:float64:"json:\"BILLBOARD_NET_AMT\"":false
4:BILLBOARDSELLAMT:float64:"json:\"BILLBOARD_SELL_AMT\"":false
5:CHANGERATE:float64:"json:\"CHANGE_RATE\"":false
6:CLOSEPRICE:float64:"json:\"CLOSE_PRICE\"":false
7:DEALAMOUNTRATIO:float64:"json:\"DEAL_AMOUNT_RATIO\"":false
8:DEALNETRATIO:float64:"json:\"DEAL_NET_RATIO\"":false
9:EXPLAIN:string:"json:\"EXPLAIN\"":false
10:EXPLANATION:string:"json:\"EXPLANATION\"":false
11:FREEMARKETCAP:float64:"json:\"FREE_MARKET_CAP\"":false
12:SECUCODE:string:"json:\"SECUCODE\" gorm:\"index\"":false
13:SECURITYCODE:string:"json:\"SECURITY_CODE\"":false
14:SECURITYNAMEABBR:string:"json:\"SECURITY_NAME_ABBR\"":false
15:SECURITYTYPECODE:string:"json:\"SECURITY_TYPE_CODE\"":false
16:TRADEDATE:string:"json:\"TRADE_DATE\" gorm:\"index\"":false
17:TURNOVERRATE:float64:"json:\"TURNOVERRATE\"":false
19:AIConfig:14
0:ID:uint:"gorm:\"primarykey\"":false
1:CreatedAt:time.Time:"":false
2:UpdatedAt:time.Time:"":false
3:Sort:int:"json:\"sort\" gorm:\"index\"":false
4:Name:string:"json:\"name\"":false
5:BaseUrl:string:"json:\"baseUrl\"":false
6:ApiKey:string:"json:\"apiKey\" ":false
7:ModelName:string:"json:\"modelName\"":false
8:ApiProtocol:string:"json:\"apiProtocol\" gorm:\"default:'chat_completions'\"":false
9:MaxTokens:int:"json:\"maxTokens\"":false
10:Temperature:float64:"json:\"temperature\"":false
11:TimeOut:int:"json:\"timeOut\"":false
12:HttpProxy:string:"json:\"httpProxy\"":false
13:HttpProxyEnabled:bool:"json:\"httpProxyEnabled\"":false
20:BKDict:6
0:Model:gorm.io/gorm.Model:"md:\"-\"":true
1:BkCode:string:"json:\"bkCode\" md:\"行业/板块代码\"":false
2:BkName:string:"json:\"bkName\" md:\"行业/板块名称\"":false
3:FirstLetter:string:"json:\"firstLetter\" md:\"first_letter\"":false
4:FubkCode:string:"json:\"fubkCode\" md:\"fubk_code\"":false
5:PublishCode:string:"json:\"publishCode\" md:\"publish_code\"":false
21:WordAnalyze:3
0:Model:gorm.io/gorm.Model:"":true
1:DataTime:*time.Time:"json:\"dataTime\" gorm:\"index;autoCreateTime\"":false
2:WordFreqWithWeight:WordFreqWithWeight:"":true
22:SentimentResultAnalyze:3
0:Model:gorm.io/gorm.Model:"":true
1:DataTime:*time.Time:"json:\"dataTime\" gorm:\"index;autoCreateTime\"":false
2:SentimentResult:SentimentResult:"":true
23:AiRecommendStocks:51
0:Model:gorm.io/gorm.Model:"":true
1:DataTime:*time.Time:"json:\"dataTime\" gorm:\"index;autoCreateTime\"":false
2:ProviderName:string:"json:\"providerName\" gorm:\"size:128\"":false
3:ModelName:string:"json:\"modelName\" md:\"模型名称\"":false
4:StockCode:string:"json:\"stockCode\" md:\"股票代码\"":false
5:StockName:string:"json:\"stockName\" md:\"股票名称\"":false
6:BkCode:string:"json:\"bkCode\" md:\"行业/板块代码\"":false
7:BkName:string:"json:\"bkName\" md:\"行业/板块名称\"":false
8:StockPrice:string:"json:\"stockPrice\" md:\"推荐时股票价格\"":false
9:StockCurrentPrice:string:"json:\"stockCurrentPrice\" md:\"当前价格\"":false
10:StockCurrentPriceTime:string:"json:\"stockCurrentPriceTime\" md:\"当前价格时间\"":false
11:StockClosePrice:string:"json:\"stockClosePrice\" md:\"推荐时股票收盘价格\"":false
12:StockPrePrice:string:"json:\"stockPrePrice\" md:\"前一交易日股票价格\"":false
13:RecommendReason:string:"json:\"recommendReason\" md:\"推荐理由/驱动因素/逻辑\"":false
14:RecommendBuyPrice:string:"json:\"recommendBuyPrice\" md:\"ai建议买入价范围\"":false
15:RecommendBuyPriceMin:float64:"json:\"recommendBuyPriceMin\" md:\"ai建议最低买入价\"":false
16:RecommendBuyPriceMax:float64:"json:\"recommendBuyPriceMax\" md:\"ai建议最高买入价\"":false
17:RecommendStopProfitPrice:string:"json:\"recommendStopProfitPrice\" md:\"ai建议止盈价范围\"":false
18:RecommendStopProfitPriceMin:float64:"json:\"recommendStopProfitPriceMin\" md:\"ai建议最低止盈价\"":false
19:RecommendStopProfitPriceMax:float64:"json:\"recommendStopProfitPriceMax\" md:\"ai建议最高止盈价\"":false
20:RecommendStopLossPrice:string:"json:\"recommendStopLossPrice\" md:\"ai建议止损价\"":false
21:RecommendCategory:string:"json:\"recommendCategory\" md:\"推荐分类\"":false
22:ExecutionState:string:"json:\"executionState\" md:\"执行状态\"":false
23:BuySignal:string:"json:\"buySignal\" md:\"买入信号\"":false
24:BuySignalDetail:string:"json:\"buySignalDetail\" md:\"买入信号补充条件\"":false
25:SellSignal:string:"json:\"sellSignal\" md:\"卖出信号\"":false
26:SellSignalDetail:string:"json:\"sellSignalDetail\" md:\"卖出信号补充条件\"":false
27:InvalidSignal:string:"json:\"invalidSignal\" md:\"失效信号\"":false
28:CoreCatalyst:string:"json:\"coreCatalyst\" md:\"核心催化\"":false
29:KeyEvidence:string:"json:\"keyEvidence\" md:\"关键证据\"":false
30:EvidenceSources:string:"json:\"evidenceSources\" md:\"证据引用JSON\"":false
31:InvalidCondition:string:"json:\"invalidCondition\" md:\"失效条件\"":false
32:ObservePrice:string:"json:\"observePrice\" md:\"观察价\"":false
33:FocusPrice:string:"json:\"focusPrice\" md:\"关注位\"":false
34:ExpectedCycle:string:"json:\"expectedCycle\" md:\"预期周期\"":false
35:EventStrength:int:"json:\"eventStrength\" md:\"事件强度\"":false
36:CapitalConfirmation:int:"json:\"capitalConfirmation\" md:\"资金确认度\"":false
37:FundamentalFit:int:"json:\"fundamentalFit\" md:\"基本面匹配度\"":false
38:TechnicalFit:int:"json:\"technicalFit\" md:\"技术面匹配度\"":false
39:ActivationRuleJSON:string:"json:\"activationRuleJson\" md:\"结构化激活规则JSON\"":false
40:ActivationRuleVersion:string:"json:\"activationRuleVersion\" md:\"结构化激活规则版本\"":false
41:ActivationRuleSource:string:"json:\"activationRuleSource\" md:\"结构化激活规则来源\"":false
42:ActivationStatus:string:"json:\"activationStatus\" gorm:\"index\" md:\"激活状态\"":false
43:ActivationInvalidReason:string:"json:\"activationInvalidReason\" md:\"激活规则无效原因\"":false
44:RecommendStatus:string:"json:\"recommendStatus\" md:\"推荐状态\"":false
45:SummaryVersion:string:"json:\"summaryVersion\" md:\"总结版本\"":false
46:StrategyRunID:string:"json:\"strategyRunId,omitempty\" gorm:\"size:96;index\" md:\"策略运行ID\"":false
47:StrategyRuleID:string:"json:\"strategyRuleId,omitempty\" gorm:\"size:128;index\" md:\"策略规则ID\"":false
48:RiskRemarks:string:"json:\"riskRemarks\" md:\"风险提示\"":false
49:Remarks:string:"json:\"remarks\" md:\"备注\"":false
50:LatestOpeningReview:*AiRecommendOpeningReviewSummary:"json:\"latestOpeningReview,omitempty\" gorm:\"-\"":false
24:AiRecommendOpeningReview:21
0:ID:uint:"json:\"id\" gorm:\"primarykey\"":false
1:CreatedAt:time.Time:"json:\"createdAt\"":false
2:UpdatedAt:time.Time:"json:\"updatedAt\"":false
3:RecommendID:uint:"json:\"recommendId\" gorm:\"uniqueIndex:idx_ai_rec_opening_review_key\"":false
4:StockCode:string:"json:\"stockCode\" gorm:\"size:32;index\"":false
5:StockName:string:"json:\"stockName\"":false
6:TradeDate:string:"json:\"tradeDate\" gorm:\"size:10;index;uniqueIndex:idx_ai_rec_opening_review_key\"":false
7:ReviewScope:string:"json:\"reviewScope\" gorm:\"size:16;index;uniqueIndex:idx_ai_rec_opening_review_key\"":false
8:ReviewPhase:string:"json:\"reviewPhase\" gorm:\"size:16;index;uniqueIndex:idx_ai_rec_opening_review_key\"":false
9:OpeningPrice:float64:"json:\"openingPrice\"":false
10:AuctionPrice:float64:"json:\"auctionPrice\"":false
11:MinutePrice:float64:"json:\"minutePrice\"":false
12:MinuteVolume:float64:"json:\"minuteVolume\"":false
13:MinuteAmount:float64:"json:\"minuteAmount\"":false
14:GapType:string:"json:\"gapType\" gorm:\"size:32\"":false
15:Action:string:"json:\"action\" gorm:\"size:32;index\"":false
16:Reason:string:"json:\"reason\" gorm:\"type:text\"":false
17:SuggestedStopLoss:float64:"json:\"suggestedStopLoss\"":false
18:SuggestedTakeProfit:float64:"json:\"suggestedTakeProfit\"":false
19:ModelName:string:"json:\"modelName\" gorm:\"size:128\"":false
20:RawSummary:string:"json:\"rawSummary\" gorm:\"type:text\"":false
25:AiRecommendYieldState:37
0:ID:uint:"json:\"id\" gorm:\"primarykey\"":false
1:CreatedAt:time.Time:"json:\"createdAt\"":false
2:UpdatedAt:time.Time:"json:\"updatedAt\"":false
3:StockCode:string:"json:\"stockCode\" gorm:\"size:32;uniqueIndex\"":false
4:StockName:string:"json:\"stockName\"":false
5:ModelNames:string:"json:\"modelNames\"":false
6:BkName:string:"json:\"bkName\"":false
7:RecommendCount:int:"json:\"recommendCount\"":false
8:RecommendCategory:string:"json:\"recommendCategory\"":false
9:RecommendTime:*time.Time:"json:\"recommendTime\" gorm:\"index\"":false
10:SignalTime:*time.Time:"json:\"signalTime\" gorm:\"index\"":false
11:ActivationStatus:string:"json:\"activationStatus\" gorm:\"index\"":false
12:ActivationTime:*time.Time:"json:\"activationTime\" gorm:\"index\"":false
13:ActivationPrice:float64:"json:\"activationPrice\"":false
14:BuyTime:*time.Time:"json:\"buyTime\" gorm:\"index\"":false
15:BuyAmount:float64:"json:\"buyAmount\"":false
16:StopProfitAmount:*float64:"json:\"stopProfitAmount\"":false
17:StopLossAmount:*float64:"json:\"stopLossAmount\"":false
18:SellAmountText:string:"json:\"sellAmountText\"":false
19:PositionStatus:string:"json:\"positionStatus\"":false
20:SellTime:*time.Time:"json:\"sellTime\"":false
21:RealizedSellAmount:*float64:"json:\"realizedSellAmount\"":false
22:CurrentPrice:float64:"json:\"currentPrice\"":false
23:CurrentPriceTime:string:"json:\"currentPriceTime\"":false
24:YieldRate:float64:"json:\"yieldRate\"":false
25:YieldRateText:string:"json:\"yieldRateText\"":false
26:DataStatus:string:"json:\"dataStatus\"":false
27:DataStatusReason:string:"json:\"dataStatusReason\"":false
28:LastMinuteTs:*time.Time:"json:\"lastMinuteTs\" gorm:\"index\"":false
29:LastRecalcAt:*time.Time:"json:\"lastRecalcAt\" gorm:\"index\"":false
30:MinuteCacheStart:*time.Time:"json:\"minuteCacheStart\" gorm:\"index\"":false
31:MinuteCacheEnd:*time.Time:"json:\"minuteCacheEnd\" gorm:\"index\"":false
32:MinuteCacheSource:string:"json:\"minuteCacheSource\"":false
33:MinuteCacheUpdated:*time.Time:"json:\"minuteCacheUpdated\"":false
34:Frozen:bool:"json:\"frozen\" gorm:\"index\"":false
35:TotalScopeStart:string:"json:\"totalScopeStart\"":false
36:TotalScopeEnd:string:"json:\"totalScopeEnd\"":false
26:AiRecommendYieldOverride:24
0:ID:uint:"json:\"id\" gorm:\"primarykey\"":false
1:CreatedAt:time.Time:"json:\"createdAt\"":false
2:UpdatedAt:time.Time:"json:\"updatedAt\"":false
3:RecommendID:uint:"json:\"recommendId\" gorm:\"uniqueIndex\"":false
4:StockCode:string:"json:\"stockCode\" gorm:\"size:32;index\"":false
5:ReviewRound:int:"json:\"reviewRound\"":false
6:ReviewSource:string:"json:\"reviewSource\" gorm:\"size:64\"":false
7:ReviewedAt:*time.Time:"json:\"reviewedAt\" gorm:\"index\"":false
8:ActivationStatusOverride:string:"json:\"activationStatusOverride\" gorm:\"size:32;index\"":false
9:RecommendBuyPrice:string:"json:\"recommendBuyPrice\"":false
10:RecommendBuyPriceMin:float64:"json:\"recommendBuyPriceMin\"":false
11:RecommendBuyPriceMax:float64:"json:\"recommendBuyPriceMax\"":false
12:RecommendStopProfitPrice:string:"json:\"recommendStopProfitPrice\"":false
13:RecommendStopProfitPriceMin:float64:"json:\"recommendStopProfitPriceMin\"":false
14:RecommendStopProfitPriceMax:float64:"json:\"recommendStopProfitPriceMax\"":false
15:RecommendStopLossPrice:string:"json:\"recommendStopLossPrice\"":false
16:BuySignal:string:"json:\"buySignal\"":false
17:BuySignalDetail:string:"json:\"buySignalDetail\"":false
18:ActivationRuleJSON:string:"json:\"activationRuleJson\"":false
19:ActivationRuleVersion:string:"json:\"activationRuleVersion\"":false
20:ActivationRuleSource:string:"json:\"activationRuleSource\"":false
21:InvalidSignal:string:"json:\"invalidSignal\"":false
22:InvalidCondition:string:"json:\"invalidCondition\"":false
23:DataStatusReason:string:"json:\"dataStatusReason\"":false
27:AiRecommendYieldRecordState:37
0:ID:uint:"json:\"id\" gorm:\"primarykey\"":false
1:CreatedAt:time.Time:"json:\"createdAt\"":false
2:UpdatedAt:time.Time:"json:\"updatedAt\"":false
3:RecommendID:uint:"json:\"recommendId\" gorm:\"uniqueIndex\"":false
4:StockCode:string:"json:\"stockCode\" gorm:\"size:32;index\"":false
5:StockName:string:"json:\"stockName\"":false
6:ModelName:string:"json:\"modelName\"":false
7:BkName:string:"json:\"bkName\"":false
8:RecommendCategory:string:"json:\"recommendCategory\"":false
9:RecommendTime:*time.Time:"json:\"recommendTime\" gorm:\"index\"":false
10:SignalTime:*time.Time:"json:\"signalTime\" gorm:\"index\"":false
11:ActivationStatus:string:"json:\"activationStatus\" gorm:\"index\"":false
12:ActivationTime:*time.Time:"json:\"activationTime\" gorm:\"index\"":false
13:ActivationPrice:float64:"json:\"activationPrice\"":false
14:BuyTime:*time.Time:"json:\"buyTime\" gorm:\"index\"":false
15:BuyAmount:float64:"json:\"buyAmount\"":false
16:StopProfitAmount:*float64:"json:\"stopProfitAmount\"":false
17:StopLossAmount:*float64:"json:\"stopLossAmount\"":false
18:SellAmountText:string:"json:\"sellAmountText\"":false
19:PositionStatus:string:"json:\"positionStatus\"":false
20:SellTime:*time.Time:"json:\"sellTime\"":false
21:RealizedSellAmount:*float64:"json:\"realizedSellAmount\"":false
22:CurrentPrice:float64:"json:\"currentPrice\"":false
23:CurrentPriceTime:string:"json:\"currentPriceTime\"":false
24:YieldRate:float64:"json:\"yieldRate\"":false
25:YieldRateText:string:"json:\"yieldRateText\"":false
26:DataStatus:string:"json:\"dataStatus\"":false
27:DataStatusReason:string:"json:\"dataStatusReason\"":false
28:LastMinuteTs:*time.Time:"json:\"lastMinuteTs\" gorm:\"index\"":false
29:LastRecalcAt:*time.Time:"json:\"lastRecalcAt\" gorm:\"index\"":false
30:MinuteCacheStart:*time.Time:"json:\"minuteCacheStart\" gorm:\"index\"":false
31:MinuteCacheEnd:*time.Time:"json:\"minuteCacheEnd\" gorm:\"index\"":false
32:MinuteCacheSource:string:"json:\"minuteCacheSource\"":false
33:MinuteCacheUpdated:*time.Time:"json:\"minuteCacheUpdated\"":false
34:Frozen:bool:"json:\"frozen\" gorm:\"index\"":false
35:TotalScopeStart:string:"json:\"totalScopeStart\"":false
36:TotalScopeEnd:string:"json:\"totalScopeEnd\"":false
28:AiRecommendYieldMeta:32
0:ID:uint:"json:\"id\" gorm:\"primarykey\"":false
1:CreatedAt:time.Time:"json:\"createdAt\"":false
2:UpdatedAt:time.Time:"json:\"updatedAt\"":false
3:LastFullRecalcAt:*time.Time:"json:\"lastFullRecalcAt\"":false
4:LastYieldEmailSentAt:*time.Time:"json:\"lastYieldEmailSentAt\" gorm:\"index\"":false
5:LastYieldEmailSentReason:string:"json:\"lastYieldEmailSentReason\"":false
6:LastQueryRecalcAt:*time.Time:"json:\"lastQueryRecalcAt\" gorm:\"index\"":false
7:QueryCooldownUntil:*time.Time:"json:\"queryCooldownUntil\" gorm:\"index\"":false
8:LastManualDownloadAt:*time.Time:"json:\"lastManualDownloadAt\" gorm:\"index\"":false
9:ManualCooldownUntil:*time.Time:"json:\"manualCooldownUntil\" gorm:\"index\"":false
10:RecalcInProgress:bool:"json:\"recalcInProgress\" gorm:\"index\"":false
11:RecalcTotal:int:"json:\"recalcTotal\"":false
12:RecalcDone:int:"json:\"recalcDone\"":false
13:RecalcProgress:int:"json:\"recalcProgress\"":false
14:DownloadInProgress:bool:"json:\"downloadInProgress\" gorm:\"index\"":false
15:DownloadTotal:int:"json:\"downloadTotal\"":false
16:DownloadDone:int:"json:\"downloadDone\"":false
17:DownloadProgress:int:"json:\"downloadProgress\"":false
18:LastDownloadError:string:"json:\"lastDownloadError\"":false
19:LastError:string:"json:\"lastError\"":false
20:CurrentTradeDate:string:"json:\"currentTradeDate\"":false
21:AkshareReady:bool:"json:\"akshareReady\"":false
22:AkshareCheckedAt:*time.Time:"json:\"akshareCheckedAt\"":false
23:AkshareInstallError:string:"json:\"akshareInstallError\"":false
24:FrozenSellPriceFixVersion:string:"json:\"frozenSellPriceFixVersion\"":false
25:LastManualFinishedAt:*time.Time:"json:\"lastManualFinishedAt\" gorm:\"index\"":false
26:LastManualScopeCount:int:"json:\"lastManualScopeCount\"":false
27:LastManualPrefetchMs:int64:"json:\"lastManualPrefetchMs\"":false
28:LastManualRecalcMs:int64:"json:\"lastManualRecalcMs\"":false
29:LastManualTotalMs:int64:"json:\"lastManualTotalMs\"":false
30:LastManualSqliteBusyCount:int:"json:\"lastManualSqliteBusyCount\"":false
31:LastManualProviderSummary:string:"json:\"lastManualProviderSummary\" gorm:\"type:text\"":false
29:AiRecommendYieldDirtyCode:7
0:ID:uint:"json:\"id\" gorm:\"primarykey\"":false
1:CreatedAt:time.Time:"json:\"createdAt\"":false
2:UpdatedAt:time.Time:"json:\"updatedAt\"":false
3:StockCode:string:"json:\"stockCode\" gorm:\"size:32;index;uniqueIndex:idx_ai_recommend_yield_dirty_scope\"":false
4:RecommendID:uint:"json:\"recommendId\" gorm:\"index;uniqueIndex:idx_ai_recommend_yield_dirty_scope\"":false
5:Reason:string:"json:\"reason\"":false
6:ModeNeeded:string:"json:\"modeNeeded\" gorm:\"size:16;index;uniqueIndex:idx_ai_recommend_yield_dirty_scope\"":false
30:AiRecommendMinuteBar:12
0:ID:uint:"json:\"id\" gorm:\"primarykey\"":false
1:CreatedAt:time.Time:"json:\"createdAt\"":false
2:UpdatedAt:time.Time:"json:\"updatedAt\"":false
3:StockCode:string:"json:\"stockCode\" gorm:\"size:32;index;uniqueIndex:idx_ai_rec_minute_code_time,priority:1\"":false
4:TradeTime:time.Time:"json:\"tradeTime\" gorm:\"index;uniqueIndex:idx_ai_rec_minute_code_time,priority:2\"":false
5:Open:float64:"json:\"open\"":false
6:High:float64:"json:\"high\"":false
7:Low:float64:"json:\"low\"":false
8:Close:float64:"json:\"close\"":false
9:Volume:float64:"json:\"volume\"":false
10:Amount:float64:"json:\"amount\"":false
11:Source:string:"json:\"source\"":false
31:AiRecommendDailyBar:12
0:ID:uint:"json:\"id\" gorm:\"primarykey\"":false
1:CreatedAt:time.Time:"json:\"createdAt\"":false
2:UpdatedAt:time.Time:"json:\"updatedAt\"":false
3:StockCode:string:"json:\"stockCode\" gorm:\"size:32;index;uniqueIndex:idx_ai_rec_daily_code_date,priority:1\"":false
4:TradeDate:time.Time:"json:\"tradeDate\" gorm:\"index;uniqueIndex:idx_ai_rec_daily_code_date,priority:2\"":false
5:Open:float64:"json:\"open\"":false
6:High:float64:"json:\"high\"":false
7:Low:float64:"json:\"low\"":false
8:Close:float64:"json:\"close\"":false
9:Volume:float64:"json:\"volume\"":false
10:Amount:float64:"json:\"amount\"":false
11:Source:string:"json:\"source\"":false
32:CronTaskRun:9
0:Model:gorm.io/gorm.Model:"":true
1:TaskName:string:"json:\"taskName\" gorm:\"size:128;index\"":false
2:TriggeredAt:time.Time:"json:\"triggeredAt\" gorm:\"index\"":false
3:Status:string:"json:\"status\" gorm:\"size:32;index\"":false
4:Attempts:int:"json:\"attempts\"":false
5:AiConfigId:int:"json:\"aiConfigId\" gorm:\"index\"":false
6:ModelName:string:"json:\"modelName\" gorm:\"size:128\"":false
7:ChatId:string:"json:\"chatId\" gorm:\"size:128\"":false
8:ErrorMessage:string:"json:\"errorMessage\" gorm:\"type:text\"":false
33:EmailSendLog:14
0:Model:gorm.io/gorm.Model:"":true
1:SendType:string:"json:\"sendType\" gorm:\"size:64;index\"":false
2:TriggeredAt:time.Time:"json:\"triggeredAt\" gorm:\"index\"":false
3:Status:string:"json:\"status\" gorm:\"size:32;index\"":false
4:Recipients:string:"json:\"recipients\" gorm:\"type:text\"":false
5:Subject:string:"json:\"subject\" gorm:\"size:255\"":false
6:ErrorMessage:string:"json:\"errorMessage\" gorm:\"type:text\"":false
7:ReportStockCode:string:"json:\"reportStockCode\" gorm:\"size:32;index\"":false
8:ReportStockName:string:"json:\"reportStockName\" gorm:\"size:128\"":false
9:ReportCreatedAt:*time.Time:"json:\"reportCreatedAt\" gorm:\"index\"":false
10:AttachmentNames:string:"json:\"attachmentNames\" gorm:\"type:text\"":false
11:AttachmentCount:int:"json:\"attachmentCount\"":false
12:AttachmentBytes:int64:"json:\"attachmentBytes\"":false
13:ExtraSummary:string:"json:\"extraSummary\" gorm:\"type:text\"":false
34:MarketSummaryRunDiagnostic:20
0:Model:gorm.io/gorm.Model:"":true
1:RunID:string:"json:\"runId\" gorm:\"size:64;uniqueIndex\"":false
2:SummaryVersion:string:"json:\"summaryVersion\" gorm:\"size:32;index\"":false
3:RunSlot:string:"json:\"runSlot\" gorm:\"size:32;index\"":false
4:StartedAt:time.Time:"json:\"startedAt\" gorm:\"index\"":false
5:FinishedAt:time.Time:"json:\"finishedAt\" gorm:\"index\"":false
6:IndicatorCandidateCount:int:"json:\"indicatorCandidateCount\"":false
7:IndicatorAIInputCount:int:"json:\"indicatorAiInputCount\"":false
8:DiscoveryCandidateCount:int:"json:\"discoveryCandidateCount\"":false
9:VerifiedCandidateCount:int:"json:\"verifiedCandidateCount\"":false
10:AIOutputCountFirst:int:"json:\"aiOutputCountFirst\"":false
11:AIOutputCountSecond:int:"json:\"aiOutputCountSecond\"":false
12:SavedCount:int:"json:\"savedCount\"":false
13:ProductionCount:int:"json:\"productionCount\"":false
14:AnalysisOnlyCount:int:"json:\"analysisOnlyCount\"":false
15:BlockedCount:int:"json:\"blockedCount\"":false
16:BlockedReasonTop:string:"json:\"blockedReasonTop\" gorm:\"type:text\"":false
17:ProductionDowngradeReasonTop:string:"json:\"productionDowngradeReasonTop\" gorm:\"type:text\"":false
18:EmptyRun:bool:"json:\"emptyRun\" gorm:\"index\"":false
19:NotesJSON:string:"json:\"notesJson\" gorm:\"type:text\"":false
strategy_persistence_models:9
0:StrategyRunSnapshot:23
0:ID:uint:"json:\"id\" gorm:\"primarykey\"":false
1:CreatedAt:time.Time:"json:\"createdAt\" gorm:\"autoCreateTime\"":false
2:RunID:string:"json:\"runId\" gorm:\"size:96;not null;uniqueIndex\"":false
3:StrategyVersion:string:"json:\"strategyVersion\" gorm:\"size:32;not null;index:idx_strategy_run_version_date,priority:1\"":false
4:TradeDate:string:"json:\"tradeDate\" gorm:\"size:10;not null;index:idx_strategy_run_version_date,priority:2\"":false
5:RunSlot:string:"json:\"runSlot\" gorm:\"size:32;index:idx_strategy_run_version_date,priority:3\"":false
6:StartedAt:time.Time:"json:\"startedAt\" gorm:\"not null;index\"":false
7:AsOf:time.Time:"json:\"asOf\" gorm:\"not null;index\"":false
8:DataCutoffAt:time.Time:"json:\"dataCutoffAt\" gorm:\"not null;index\"":false
9:DecisionAt:time.Time:"json:\"decisionAt\" gorm:\"not null;index\"":false
10:GeneratedAt:time.Time:"json:\"generatedAt\" gorm:\"not null\"":false
11:ValidFromAt:*time.Time:"json:\"validFromAt,omitempty\" gorm:\"index\"":false
12:Mode:string:"json:\"mode\" gorm:\"size:32;index\"":false
13:ConfigHash:string:"json:\"configHash\" gorm:\"size:128;index\"":false
14:InputHash:string:"json:\"inputHash\" gorm:\"size:128;index\"":false
15:CandidateCount:int:"json:\"candidateCount\"":false
16:RuleCount:int:"json:\"ruleCount\"":false
17:OrderEventCount:int:"json:\"orderEventCount\"":false
18:SecuritySnapshotCount:int:"json:\"securitySnapshotCount\"":false
19:CorporateActionCount:int:"json:\"corporateActionCount\"":false
20:SnapshotHash:string:"json:\"snapshotHash\" gorm:\"size:128;not null;index\"":false
21:PayloadJSON:string:"json:\"payloadJson\" gorm:\"type:text;not null\"":false
22:FrozenAt:*time.Time:"json:\"frozenAt,omitempty\" gorm:\"not null;index\"":false
1:CandidateSnapshot:20
0:ID:uint:"json:\"id\" gorm:\"primarykey\"":false
1:CreatedAt:time.Time:"json:\"createdAt\" gorm:\"autoCreateTime\"":false
2:CandidateID:string:"json:\"candidateId\" gorm:\"size:128;not null;uniqueIndex\"":false
3:RunID:string:"json:\"runId\" gorm:\"size:96;not null;index;uniqueIndex:idx_strategy_candidate_run_symbol,priority:1\"":false
4:StrategyVersion:string:"json:\"strategyVersion\" gorm:\"size:32;not null;index:idx_strategy_candidate_version_date,priority:1\"":false
5:TradeDate:string:"json:\"tradeDate\" gorm:\"size:10;not null;index:idx_strategy_candidate_version_date,priority:2\"":false
6:Symbol:string:"json:\"symbol\" gorm:\"size:32;not null;index;uniqueIndex:idx_strategy_candidate_run_symbol,priority:2\"":false
7:Name:string:"json:\"name\" gorm:\"size:128\"":false
8:Sector:string:"json:\"sector\" gorm:\"size:128;index\"":false
9:Market:string:"json:\"market\" gorm:\"size:32;index\"":false
10:Rank:int:"json:\"rank\" gorm:\"index\"":false
11:PreVerifyRank:int:"json:\"preVerifyRank\" gorm:\"index\"":false
12:FinalRank:int:"json:\"finalRank\" gorm:\"index\"":false
13:Decision:string:"json:\"decision\" gorm:\"size:32;index\"":false
14:Score:float64:"json:\"score\" gorm:\"index\"":false
15:Eligible:bool:"json:\"eligible\" gorm:\"index\"":false
16:RejectionReason:string:"json:\"rejectionReason,omitempty\" gorm:\"type:text\"":false
17:SnapshotHash:string:"json:\"snapshotHash\" gorm:\"size:128;not null;index\"":false
18:PayloadJSON:string:"json:\"payloadJson\" gorm:\"type:text;not null\"":false
19:FrozenAt:*time.Time:"json:\"frozenAt,omitempty\" gorm:\"not null;index\"":false
2:RuleSnapshot:16
0:ID:uint:"json:\"id\" gorm:\"primarykey\"":false
1:CreatedAt:time.Time:"json:\"createdAt\" gorm:\"autoCreateTime\"":false
2:RuleID:string:"json:\"ruleId\" gorm:\"size:128;not null;uniqueIndex\"":false
3:RunID:string:"json:\"runId\" gorm:\"size:96;not null;index;uniqueIndex:idx_strategy_rule_run_symbol_path,priority:1\"":false
4:CandidateID:string:"json:\"candidateId\" gorm:\"size:128;index\"":false
5:StrategyVersion:string:"json:\"strategyVersion\" gorm:\"size:32;not null;index:idx_strategy_rule_version_date,priority:1\"":false
6:TradeDate:string:"json:\"tradeDate\" gorm:\"size:10;not null;index:idx_strategy_rule_version_date,priority:2\"":false
7:Symbol:string:"json:\"symbol\" gorm:\"size:32;not null;index;uniqueIndex:idx_strategy_rule_run_symbol_path,priority:2\"":false
8:RuleVersion:string:"json:\"ruleVersion\" gorm:\"size:32;index\"":false
9:RuleType:string:"json:\"ruleType\" gorm:\"size:32;index\"":false
10:Path:string:"json:\"path\" gorm:\"size:32;index;uniqueIndex:idx_strategy_rule_run_symbol_path,priority:3\"":false
11:ValidFromAt:time.Time:"json:\"validFromAt\" gorm:\"not null;index\"":false
12:ExpiresAt:*time.Time:"json:\"expiresAt,omitempty\" gorm:\"index\"":false
13:SnapshotHash:string:"json:\"snapshotHash\" gorm:\"size:128;not null;index\"":false
14:PayloadJSON:string:"json:\"payloadJson\" gorm:\"type:text;not null\"":false
15:FrozenAt:*time.Time:"json:\"frozenAt,omitempty\" gorm:\"not null;index\"":false
3:OrderEvent:20
0:ID:uint:"json:\"id\" gorm:\"primarykey\"":false
1:CreatedAt:time.Time:"json:\"createdAt\" gorm:\"autoCreateTime\"":false
2:EventID:string:"json:\"eventId\" gorm:\"size:128;not null;uniqueIndex\"":false
3:RunID:string:"json:\"runId\" gorm:\"size:96;not null;index;uniqueIndex:idx_strategy_order_run_rule_sequence,priority:1\"":false
4:RuleID:string:"json:\"ruleId\" gorm:\"size:128;index;uniqueIndex:idx_strategy_order_run_rule_sequence,priority:2\"":false
5:StrategyVersion:string:"json:\"strategyVersion\" gorm:\"size:32;not null;index:idx_strategy_order_version_date,priority:1\"":false
6:TradeDate:string:"json:\"tradeDate\" gorm:\"size:10;not null;index:idx_strategy_order_version_date,priority:2\"":false
7:Symbol:string:"json:\"symbol\" gorm:\"size:32;not null;index\"":false
8:EventType:string:"json:\"eventType\" gorm:\"size:32;not null;index\"":false
9:Sequence:int:"json:\"sequence\" gorm:\"not null;uniqueIndex:idx_strategy_order_run_rule_sequence,priority:3\"":false
10:EventAt:time.Time:"json:\"eventAt\" gorm:\"not null;index\"":false
11:Price:float64:"json:\"price\"":false
12:Quantity:float64:"json:\"quantity\"":false
13:CashAmount:float64:"json:\"cashAmount\"":false
14:AdjustmentFactor:float64:"json:\"adjustmentFactor\"":false
15:Fees:float64:"json:\"fees\"":false
16:Reason:string:"json:\"reason,omitempty\" gorm:\"type:text\"":false
17:SnapshotHash:string:"json:\"snapshotHash\" gorm:\"size:128;not null;index\"":false
18:PayloadJSON:string:"json:\"payloadJson\" gorm:\"type:text;not null\"":false
19:FrozenAt:*time.Time:"json:\"frozenAt,omitempty\" gorm:\"not null;index\"":false
4:BacktestRun:20
0:ID:uint:"json:\"id\" gorm:\"primarykey\"":false
1:CreatedAt:time.Time:"json:\"createdAt\" gorm:\"autoCreateTime\"":false
2:BacktestID:string:"json:\"backtestId\" gorm:\"size:128;not null;uniqueIndex\"":false
3:StrategyVersion:string:"json:\"strategyVersion\" gorm:\"size:32;not null;index:idx_strategy_backtest_version_range,priority:1\"":false
4:StartDate:string:"json:\"startDate\" gorm:\"size:10;not null;index:idx_strategy_backtest_version_range,priority:2\"":false
5:EndDate:string:"json:\"endDate\" gorm:\"size:10;not null;index:idx_strategy_backtest_version_range,priority:3\"":false
6:InputHash:string:"json:\"inputHash\" gorm:\"size:128;not null;index\"":false
7:Status:string:"json:\"status\" gorm:\"size:32;not null;index\"":false
8:RunSnapshotCount:int:"json:\"runSnapshotCount\"":false
9:CandidateSnapshotCount:int:"json:\"candidateSnapshotCount\"":false
10:RuleSnapshotCount:int:"json:\"ruleSnapshotCount\"":false
11:OrderEventCount:int:"json:\"orderEventCount\"":false
12:SecuritySnapshotCount:int:"json:\"securitySnapshotCount\"":false
13:CorporateActionCount:int:"json:\"corporateActionCount\"":false
14:TradeCount:int:"json:\"tradeCount\"":false
15:MetricCount:int:"json:\"metricCount\"":false
16:SummaryJSON:string:"json:\"summaryJson\" gorm:\"type:text;not null\"":false
17:StartedAt:time.Time:"json:\"startedAt\" gorm:\"not null;index\"":false
18:CompletedAt:time.Time:"json:\"completedAt\" gorm:\"not null;index\"":false
19:FrozenAt:*time.Time:"json:\"frozenAt,omitempty\" gorm:\"not null;index\"":false
5:Trade:21
0:ID:uint:"json:\"id\" gorm:\"primarykey\"":false
1:CreatedAt:time.Time:"json:\"createdAt\" gorm:\"autoCreateTime\"":false
2:TradeID:string:"json:\"tradeId\" gorm:\"size:128;not null;uniqueIndex\"":false
3:BacktestID:string:"json:\"backtestId\" gorm:\"size:128;not null;index;uniqueIndex:idx_strategy_backtest_trade_seq,priority:1\"":false
4:StrategyVersion:string:"json:\"strategyVersion\" gorm:\"size:32;not null;index\"":false
5:Sequence:int:"json:\"sequence\" gorm:\"not null;uniqueIndex:idx_strategy_backtest_trade_seq,priority:2\"":false
6:Symbol:string:"json:\"symbol\" gorm:\"size:32;not null;index\"":false
7:EntryAt:time.Time:"json:\"entryAt\" gorm:\"not null;index\"":false
8:ExitAt:*time.Time:"json:\"exitAt,omitempty\" gorm:\"index\"":false
9:EntryPrice:float64:"json:\"entryPrice\"":false
10:ExitPrice:float64:"json:\"exitPrice\"":false
11:Quantity:float64:"json:\"quantity\"":false
12:Fees:float64:"json:\"fees\"":false
13:GrossPnL:float64:"json:\"grossPnl\"":false
14:NetPnL:float64:"json:\"netPnl\"":false
15:ReturnPct:float64:"json:\"returnPct\"":false
16:ExitReason:string:"json:\"exitReason,omitempty\" gorm:\"size:64;index\"":false
17:SourceOrderEventIDs:string:"json:\"sourceOrderEventIds\" gorm:\"type:text\"":false
18:SnapshotHash:string:"json:\"snapshotHash\" gorm:\"size:128;not null;index\"":false
19:PayloadJSON:string:"json:\"payloadJson\" gorm:\"type:text;not null\"":false
20:FrozenAt:*time.Time:"json:\"frozenAt,omitempty\" gorm:\"not null;index\"":false
6:Metric:12
0:ID:uint:"json:\"id\" gorm:\"primarykey\"":false
1:CreatedAt:time.Time:"json:\"createdAt\" gorm:\"autoCreateTime\"":false
2:MetricID:string:"json:\"metricId\" gorm:\"size:160;not null;uniqueIndex\"":false
3:BacktestID:string:"json:\"backtestId\" gorm:\"size:128;not null;index;uniqueIndex:idx_strategy_backtest_metric_key,priority:1\"":false
4:Name:string:"json:\"name\" gorm:\"size:64;not null;index;uniqueIndex:idx_strategy_backtest_metric_key,priority:2\"":false
5:Scope:string:"json:\"scope\" gorm:\"size:64;not null;default:summary;uniqueIndex:idx_strategy_backtest_metric_key,priority:3\"":false
6:Value:float64:"json:\"value\"":false
7:ValueText:string:"json:\"valueText,omitempty\" gorm:\"size:128\"":false
8:Unit:string:"json:\"unit,omitempty\" gorm:\"size:32\"":false
9:Ordinal:int:"json:\"ordinal\" gorm:\"index\"":false
10:PayloadJSON:string:"json:\"payloadJson\" gorm:\"type:text\"":false
11:FrozenAt:*time.Time:"json:\"frozenAt,omitempty\" gorm:\"not null;index\"":false
7:SecurityMasterHistory:24
0:ID:uint:"json:\"id\" gorm:\"primarykey\"":false
1:CreatedAt:time.Time:"json:\"createdAt\" gorm:\"autoCreateTime\"":false
2:RecordID:string:"json:\"recordId\" gorm:\"size:128;not null;uniqueIndex:idx_security_master_run_record,priority:2\"":false
3:RunID:string:"json:\"runId\" gorm:\"size:96;not null;index;uniqueIndex:idx_security_master_run_record,priority:1;uniqueIndex:idx_security_master_run_symbol_effective,priority:1\"":false
4:SnapshotVersion:string:"json:\"snapshotVersion\" gorm:\"size:32;not null;index:idx_security_master_version_symbol,priority:1\"":false
5:Symbol:string:"json:\"symbol\" gorm:\"size:32;not null;index;index:idx_security_master_version_symbol,priority:2;uniqueIndex:idx_security_master_run_symbol_effective,priority:2\"":false
6:Name:string:"json:\"name\" gorm:\"size:128\"":false
7:Market:string:"json:\"market\" gorm:\"size:32;index\"":false
8:Exchange:string:"json:\"exchange\" gorm:\"size:32;index\"":false
9:Board:string:"json:\"board\" gorm:\"size:64;index\"":false
10:Sector:string:"json:\"sector\" gorm:\"size:128;index\"":false
11:Industry:string:"json:\"industry\" gorm:\"size:128;index\"":false
12:Currency:string:"json:\"currency\" gorm:\"size:16\"":false
13:Status:string:"json:\"status\" gorm:\"size:32;index\"":false
14:IsST:bool:"json:\"isSt\" gorm:\"index\"":false
15:IsSuspended:bool:"json:\"isSuspended\" gorm:\"index\"":false
16:ListedAt:*time.Time:"json:\"listedAt,omitempty\" gorm:\"index\"":false
17:DelistedAt:*time.Time:"json:\"delistedAt,omitempty\" gorm:\"index\"":false
18:EffectiveFrom:time.Time:"json:\"effectiveFrom\" gorm:\"not null;index;uniqueIndex:idx_security_master_run_symbol_effective,priority:3\"":false
19:EffectiveTo:*time.Time:"json:\"effectiveTo,omitempty\" gorm:\"index\"":false
20:Source:string:"json:\"source\" gorm:\"size:64;index\"":false
21:SnapshotHash:string:"json:\"snapshotHash\" gorm:\"size:128;not null;index\"":false
22:PayloadJSON:string:"json:\"payloadJson\" gorm:\"type:text;not null\"":false
23:FrozenAt:*time.Time:"json:\"frozenAt,omitempty\" gorm:\"not null;index\"":false
8:CorporateActionEvent:27
0:ID:uint:"json:\"id\" gorm:\"primarykey\"":false
1:CreatedAt:time.Time:"json:\"createdAt\" gorm:\"autoCreateTime\"":false
2:EventID:string:"json:\"eventId\" gorm:\"size:128;not null;uniqueIndex:idx_corporate_action_run_event,priority:2\"":false
3:RunID:string:"json:\"runId\" gorm:\"size:96;not null;index;uniqueIndex:idx_corporate_action_run_event,priority:1;uniqueIndex:idx_corporate_action_run_symbol_type_exdate,priority:1\"":false
4:SnapshotVersion:string:"json:\"snapshotVersion\" gorm:\"size:32;not null;index:idx_corporate_action_version_date,priority:1\"":false
5:Symbol:string:"json:\"symbol\" gorm:\"size:32;not null;index;uniqueIndex:idx_corporate_action_run_symbol_type_exdate,priority:2\"":false
6:ActionType:string:"json:\"actionType\" gorm:\"size:32;not null;index;uniqueIndex:idx_corporate_action_run_symbol_type_exdate,priority:3\"":false
7:AnnouncedAt:*time.Time:"json:\"announcedAt,omitempty\" gorm:\"index\"":false
8:SourceAt:*time.Time:"json:\"sourceAt,omitempty\" gorm:\"index\"":false
9:AvailableAt:*time.Time:"json:\"availableAt,omitempty\" gorm:\"index\"":false
10:ObservationStatus:string:"json:\"observationStatus\" gorm:\"size:16;index\"":false
11:CoverageStart:*time.Time:"json:\"coverageStart,omitempty\" gorm:\"index\"":false
12:CoverageEnd:*time.Time:"json:\"coverageEnd,omitempty\" gorm:\"index\"":false
13:ExDate:time.Time:"json:\"exDate\" gorm:\"not null;index;index:idx_corporate_action_version_date,priority:2;uniqueIndex:idx_corporate_action_run_symbol_type_exdate,priority:4\"":false
14:RecordDate:*time.Time:"json:\"recordDate,omitempty\" gorm:\"index\"":false
15:PayDate:*time.Time:"json:\"payDate,omitempty\" gorm:\"index\"":false
16:CashDividend:float64:"json:\"cashDividend\"":false
17:SplitRatio:float64:"json:\"splitRatio\"":false
18:BonusRatio:float64:"json:\"bonusRatio\"":false
19:RightsRatio:float64:"json:\"rightsRatio\"":false
20:RightsPrice:float64:"json:\"rightsPrice\"":false
21:AdjustmentFactor:float64:"json:\"adjustmentFactor\"":false
22:Currency:string:"json:\"currency\" gorm:\"size:16\"":false
23:Source:string:"json:\"source\" gorm:\"size:64;index\"":false
24:SnapshotHash:string:"json:\"snapshotHash\" gorm:\"size:128;not null;index\"":false
25:PayloadJSON:string:"json:\"payloadJson\" gorm:\"type:text;not null\"":false
26:FrozenAt:*time.Time:"json:\"frozenAt,omitempty\" gorm:\"not null;index\"":false
strategy_runtime_control:1
0:StrategyRuntimeControl:8
0:ID:uint:"json:\"id\" gorm:\"primaryKey\"":false
1:Mode:string:"json:\"mode\" gorm:\"size:16;not null\"":false
2:CurrentStrategyVersion:string:"json:\"currentStrategyVersion\" gorm:\"size:32;not null\"":false
3:Reason:string:"json:\"reason\" gorm:\"size:512\"":false
4:ChangedBy:string:"json:\"changedBy\" gorm:\"size:128\"":false
5:ChangedAt:time.Time:"json:\"changedAt\" gorm:\"not null\"":false
6:CreatedAt:time.Time:"json:\"createdAt\"":false
7:UpdatedAt:time.Time:"json:\"updatedAt\"":false
strategy_write_guards
307:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_insert_ai_recommend_stocks
BEFORE INSERT ON ai_recommend_stocks
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
307:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_update_ai_recommend_stocks
BEFORE UPDATE ON ai_recommend_stocks
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
307:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_delete_ai_recommend_stocks
BEFORE DELETE ON ai_recommend_stocks
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
323:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_insert_ai_recommend_opening_review
BEFORE INSERT ON ai_recommend_opening_review
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
323:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_update_ai_recommend_opening_review
BEFORE UPDATE ON ai_recommend_opening_review
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
323:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_delete_ai_recommend_opening_review
BEFORE DELETE ON ai_recommend_opening_review
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
317:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_insert_ai_recommend_yield_state
BEFORE INSERT ON ai_recommend_yield_state
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
317:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_update_ai_recommend_yield_state
BEFORE UPDATE ON ai_recommend_yield_state
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
317:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_delete_ai_recommend_yield_state
BEFORE DELETE ON ai_recommend_yield_state
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
323:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_insert_ai_recommend_yield_override
BEFORE INSERT ON ai_recommend_yield_override
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
323:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_update_ai_recommend_yield_override
BEFORE UPDATE ON ai_recommend_yield_override
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
323:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_delete_ai_recommend_yield_override
BEFORE DELETE ON ai_recommend_yield_override
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
331:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_insert_ai_recommend_yield_record_state
BEFORE INSERT ON ai_recommend_yield_record_state
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
331:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_update_ai_recommend_yield_record_state
BEFORE UPDATE ON ai_recommend_yield_record_state
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
331:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_delete_ai_recommend_yield_record_state
BEFORE DELETE ON ai_recommend_yield_record_state
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
315:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_insert_ai_recommend_yield_meta
BEFORE INSERT ON ai_recommend_yield_meta
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
315:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_update_ai_recommend_yield_meta
BEFORE UPDATE ON ai_recommend_yield_meta
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
315:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_delete_ai_recommend_yield_meta
BEFORE DELETE ON ai_recommend_yield_meta
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
327:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_insert_ai_recommend_yield_dirty_code
BEFORE INSERT ON ai_recommend_yield_dirty_code
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
327:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_update_ai_recommend_yield_dirty_code
BEFORE UPDATE ON ai_recommend_yield_dirty_code
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
327:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_delete_ai_recommend_yield_dirty_code
BEFORE DELETE ON ai_recommend_yield_dirty_code
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
329:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_insert_market_summary_run_diagnostics
BEFORE INSERT ON market_summary_run_diagnostics
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
329:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_update_market_summary_run_diagnostics
BEFORE UPDATE ON market_summary_run_diagnostics
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
329:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_delete_market_summary_run_diagnostics
BEFORE DELETE ON market_summary_run_diagnostics
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
311:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_insert_strategy_run_snapshot
BEFORE INSERT ON strategy_run_snapshot
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
311:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_update_strategy_run_snapshot
BEFORE UPDATE ON strategy_run_snapshot
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
311:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_delete_strategy_run_snapshot
BEFORE DELETE ON strategy_run_snapshot
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
323:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_insert_strategy_candidate_snapshot
BEFORE INSERT ON strategy_candidate_snapshot
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
323:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_update_strategy_candidate_snapshot
BEFORE UPDATE ON strategy_candidate_snapshot
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
323:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_delete_strategy_candidate_snapshot
BEFORE DELETE ON strategy_candidate_snapshot
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
313:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_insert_strategy_rule_snapshot
BEFORE INSERT ON strategy_rule_snapshot
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
313:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_update_strategy_rule_snapshot
BEFORE UPDATE ON strategy_rule_snapshot
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
313:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_delete_strategy_rule_snapshot
BEFORE DELETE ON strategy_rule_snapshot
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
309:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_insert_strategy_order_event
BEFORE INSERT ON strategy_order_event
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
309:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_update_strategy_order_event
BEFORE UPDATE ON strategy_order_event
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
309:CREATE TRIGGER IF NOT EXISTS guard_strategy_paused_delete_strategy_order_event
BEFORE DELETE ON strategy_order_event
WHEN NOT EXISTS (SELECT 1 FROM strategy_runtime_control WHERE id = 1 AND mode = 'live' AND current_strategy_version = '1.5.0')
BEGIN
  SELECT RAISE(ABORT, 'strategy production is paused');
END
185:CREATE TRIGGER IF NOT EXISTS guard_strategy_immutable_update_strategy_run_snapshot
BEFORE UPDATE ON strategy_run_snapshot
BEGIN
  SELECT RAISE(ABORT, 'immutable strategy snapshot');
END
185:CREATE TRIGGER IF NOT EXISTS guard_strategy_immutable_delete_strategy_run_snapshot
BEFORE DELETE ON strategy_run_snapshot
BEGIN
  SELECT RAISE(ABORT, 'immutable strategy snapshot');
END
197:CREATE TRIGGER IF NOT EXISTS guard_strategy_immutable_update_strategy_candidate_snapshot
BEFORE UPDATE ON strategy_candidate_snapshot
BEGIN
  SELECT RAISE(ABORT, 'immutable strategy snapshot');
END
197:CREATE TRIGGER IF NOT EXISTS guard_strategy_immutable_delete_strategy_candidate_snapshot
BEFORE DELETE ON strategy_candidate_snapshot
BEGIN
  SELECT RAISE(ABORT, 'immutable strategy snapshot');
END
187:CREATE TRIGGER IF NOT EXISTS guard_strategy_immutable_update_strategy_rule_snapshot
BEFORE UPDATE ON strategy_rule_snapshot
BEGIN
  SELECT RAISE(ABORT, 'immutable strategy snapshot');
END
187:CREATE TRIGGER IF NOT EXISTS guard_strategy_immutable_delete_strategy_rule_snapshot
BEFORE DELETE ON strategy_rule_snapshot
BEGIN
  SELECT RAISE(ABORT, 'immutable strategy snapshot');
END
183:CREATE TRIGGER IF NOT EXISTS guard_strategy_immutable_update_strategy_order_event
BEFORE UPDATE ON strategy_order_event
BEGIN
  SELECT RAISE(ABORT, 'immutable strategy snapshot');
END
183:CREATE TRIGGER IF NOT EXISTS guard_strategy_immutable_delete_strategy_order_event
BEFORE DELETE ON strategy_order_event
BEGIN
  SELECT RAISE(ABORT, 'immutable strategy snapshot');
END
217:CREATE TRIGGER IF NOT EXISTS guard_legacy_recommend_insert
BEFORE INSERT ON ai_recommend_stocks
WHEN COALESCE(NEW.summary_version, '') <> '1.5.0'
BEGIN
  SELECT RAISE(ABORT, 'legacy strategy cohort is read-only');
END
293:CREATE TRIGGER IF NOT EXISTS guard_legacy_recommend_update
BEFORE UPDATE ON ai_recommend_stocks
WHEN COALESCE(OLD.summary_version, '') <> '1.5.0'
  OR COALESCE(NEW.summary_version, '') <> COALESCE(OLD.summary_version, '')
BEGIN
  SELECT RAISE(ABORT, 'legacy strategy cohort is read-only');
END
217:CREATE TRIGGER IF NOT EXISTS guard_legacy_recommend_delete
BEFORE DELETE ON ai_recommend_stocks
WHEN COALESCE(OLD.summary_version, '') <> '1.5.0'
BEGIN
  SELECT RAISE(ABORT, 'legacy strategy cohort is read-only');
END
`
