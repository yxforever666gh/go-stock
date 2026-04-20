export namespace data {
	
	export class AIConfig {
	    ID: number;
	    // Go type: time
	    CreatedAt: any;
	    // Go type: time
	    UpdatedAt: any;
	    sort: number;
	    name: string;
	    baseUrl: string;
	    apiKey: string;
	    modelName: string;
	    maxTokens: number;
	    temperature: number;
	    timeOut: number;
	    httpProxy: string;
	    httpProxyEnabled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AIConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.CreatedAt = this.convertValues(source["CreatedAt"], null);
	        this.UpdatedAt = this.convertValues(source["UpdatedAt"], null);
	        this.sort = source["sort"];
	        this.name = source["name"];
	        this.baseUrl = source["baseUrl"];
	        this.apiKey = source["apiKey"];
	        this.modelName = source["modelName"];
	        this.maxTokens = source["maxTokens"];
	        this.temperature = source["temperature"];
	        this.timeOut = source["timeOut"];
	        this.httpProxy = source["httpProxy"];
	        this.httpProxyEnabled = source["httpProxyEnabled"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class FundBasic {
	    ID: number;
	    // Go type: time
	    CreatedAt: any;
	    // Go type: time
	    UpdatedAt: any;
	    // Go type: gorm
	    DeletedAt: any;
	    code: string;
	    name: string;
	    fullName: string;
	    type: string;
	    establishment: string;
	    scale: string;
	    company: string;
	    manager: string;
	    rating: string;
	    trackingTarget: string;
	    netUnitValue?: number;
	    netUnitValueDate: string;
	    netEstimatedUnit?: number;
	    netEstimatedUnitTime: string;
	    netAccumulated?: number;
	    netGrowth1?: number;
	    netGrowth3?: number;
	    netGrowth6?: number;
	    netGrowth12?: number;
	    netGrowth36?: number;
	    netGrowth60?: number;
	    netGrowthYTD?: number;
	    netGrowthAll?: number;
	
	    static createFrom(source: any = {}) {
	        return new FundBasic(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.CreatedAt = this.convertValues(source["CreatedAt"], null);
	        this.UpdatedAt = this.convertValues(source["UpdatedAt"], null);
	        this.DeletedAt = this.convertValues(source["DeletedAt"], null);
	        this.code = source["code"];
	        this.name = source["name"];
	        this.fullName = source["fullName"];
	        this.type = source["type"];
	        this.establishment = source["establishment"];
	        this.scale = source["scale"];
	        this.company = source["company"];
	        this.manager = source["manager"];
	        this.rating = source["rating"];
	        this.trackingTarget = source["trackingTarget"];
	        this.netUnitValue = source["netUnitValue"];
	        this.netUnitValueDate = source["netUnitValueDate"];
	        this.netEstimatedUnit = source["netEstimatedUnit"];
	        this.netEstimatedUnitTime = source["netEstimatedUnitTime"];
	        this.netAccumulated = source["netAccumulated"];
	        this.netGrowth1 = source["netGrowth1"];
	        this.netGrowth3 = source["netGrowth3"];
	        this.netGrowth6 = source["netGrowth6"];
	        this.netGrowth12 = source["netGrowth12"];
	        this.netGrowth36 = source["netGrowth36"];
	        this.netGrowth60 = source["netGrowth60"];
	        this.netGrowthYTD = source["netGrowthYTD"];
	        this.netGrowthAll = source["netGrowthAll"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class FollowedFund {
	    ID: number;
	    // Go type: time
	    CreatedAt: any;
	    // Go type: time
	    UpdatedAt: any;
	    // Go type: gorm
	    DeletedAt: any;
	    code: string;
	    name: string;
	    netUnitValue?: number;
	    netUnitValueDate: string;
	    netEstimatedUnit?: number;
	    netEstimatedUnitTime: string;
	    netAccumulated?: number;
	    netEstimatedRate?: number;
	    fundBasic: FundBasic;
	
	    static createFrom(source: any = {}) {
	        return new FollowedFund(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.CreatedAt = this.convertValues(source["CreatedAt"], null);
	        this.UpdatedAt = this.convertValues(source["UpdatedAt"], null);
	        this.DeletedAt = this.convertValues(source["DeletedAt"], null);
	        this.code = source["code"];
	        this.name = source["name"];
	        this.netUnitValue = source["netUnitValue"];
	        this.netUnitValueDate = source["netUnitValueDate"];
	        this.netEstimatedUnit = source["netEstimatedUnit"];
	        this.netEstimatedUnitTime = source["netEstimatedUnitTime"];
	        this.netAccumulated = source["netAccumulated"];
	        this.netEstimatedRate = source["netEstimatedRate"];
	        this.fundBasic = this.convertValues(source["fundBasic"], FundBasic);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Group {
	    ID: number;
	    // Go type: time
	    CreatedAt: any;
	    // Go type: time
	    UpdatedAt: any;
	    // Go type: gorm
	    DeletedAt: any;
	    name: string;
	    sort: number;
	
	    static createFrom(source: any = {}) {
	        return new Group(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.CreatedAt = this.convertValues(source["CreatedAt"], null);
	        this.UpdatedAt = this.convertValues(source["UpdatedAt"], null);
	        this.DeletedAt = this.convertValues(source["DeletedAt"], null);
	        this.name = source["name"];
	        this.sort = source["sort"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class GroupStock {
	    ID: number;
	    // Go type: time
	    CreatedAt: any;
	    // Go type: time
	    UpdatedAt: any;
	    // Go type: gorm
	    DeletedAt: any;
	    stockCode: string;
	    groupId: number;
	    groupInfo: Group;
	
	    static createFrom(source: any = {}) {
	        return new GroupStock(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.CreatedAt = this.convertValues(source["CreatedAt"], null);
	        this.UpdatedAt = this.convertValues(source["UpdatedAt"], null);
	        this.DeletedAt = this.convertValues(source["DeletedAt"], null);
	        this.stockCode = source["stockCode"];
	        this.groupId = source["groupId"];
	        this.groupInfo = this.convertValues(source["groupInfo"], Group);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class FollowedStock {
	    StockCode: string;
	    Name: string;
	    Volume: number;
	    CostPrice: number;
	    Price: number;
	    PriceChange: number;
	    ChangePercent: number;
	    AlarmChangePercent: number;
	    AlarmPrice: number;
	    // Go type: time
	    Time: any;
	    Sort: number;
	    Cron?: string;
	    IsDel: number;
	    Groups: GroupStock[];
	    AiConfigId: number;
	
	    static createFrom(source: any = {}) {
	        return new FollowedStock(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.StockCode = source["StockCode"];
	        this.Name = source["Name"];
	        this.Volume = source["Volume"];
	        this.CostPrice = source["CostPrice"];
	        this.Price = source["Price"];
	        this.PriceChange = source["PriceChange"];
	        this.ChangePercent = source["ChangePercent"];
	        this.AlarmChangePercent = source["AlarmChangePercent"];
	        this.AlarmPrice = source["AlarmPrice"];
	        this.Time = this.convertValues(source["Time"], null);
	        this.Sort = source["Sort"];
	        this.Cron = source["Cron"];
	        this.IsDel = source["IsDel"];
	        this.Groups = this.convertValues(source["Groups"], GroupStock);
	        this.AiConfigId = source["AiConfigId"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	
	export class SettingConfig {
	    ID: number;
	    // Go type: time
	    CreatedAt: any;
	    // Go type: time
	    UpdatedAt: any;
	    // Go type: gorm
	    DeletedAt: any;
	    tushareToken: string;
	    localPushEnable: boolean;
	    dingPushEnable: boolean;
	    dingRobot: string;
	    yieldEmailEnable: boolean;
	    yieldEmailTo: string;
	    yieldEmailFrom: string;
	    yieldEmailSmtpHost: string;
	    yieldEmailSmtpPort: number;
	    yieldEmailSmtpUsername: string;
	    yieldEmailSmtpPassword: string;
	    yieldEmailCronEnabled: boolean;
	    yieldEmailCronTimes: string;
	    marketSummaryEmailEnabled: boolean;
	    updateBasicInfoOnStart: boolean;
	    refreshInterval: number;
	    openAiEnable: boolean;
	    prompt: string;
	    checkUpdate: boolean;
	    questionTemplate: string;
	    crawlTimeOut: number;
	    kDays: number;
	    enableDanmu: boolean;
	    browserPath: string;
	    enableNews: boolean;
	    darkTheme: boolean;
	    browserPoolSize: number;
	    enableFund: boolean;
	    enablePushNews: boolean;
	    enableOnlyPushRedNews: boolean;
	    httpProxy: string;
	    httpProxyEnabled: boolean;
	    forceNoProxyForFetch: boolean;
	    enableAgent: boolean;
	    qgqpBId: string;
	    marketSummaryCronEnabled: boolean;
	    marketSummaryCronTimes: string;
	    minuteProviderMode: string;
	    minuteLongHistoryHintEnabled: boolean;
	    privateMinuteEnabled: boolean;
	    privateMinuteBaseUrl: string;
	    privateMinuteApiKey: string;
	    privateMinuteTimeoutSec: number;
	    privateMinuteMinIntervalMs: number;
	    privateMinuteProxyMode: string;
	    privateMinuteLevel: string;
	    akshareEnabled: boolean;
	    sinaMinuteEnabled: boolean;
	    tencentMinuteEnabled: boolean;
	    eastmoneyMinuteEnabled: boolean;
	    akshareMinuteSourceMode: string;
	    aiConfigs: AIConfig[];
	
	    static createFrom(source: any = {}) {
	        return new SettingConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.CreatedAt = this.convertValues(source["CreatedAt"], null);
	        this.UpdatedAt = this.convertValues(source["UpdatedAt"], null);
	        this.DeletedAt = this.convertValues(source["DeletedAt"], null);
	        this.tushareToken = source["tushareToken"];
	        this.localPushEnable = source["localPushEnable"];
	        this.dingPushEnable = source["dingPushEnable"];
	        this.dingRobot = source["dingRobot"];
	        this.yieldEmailEnable = source["yieldEmailEnable"];
	        this.yieldEmailTo = source["yieldEmailTo"];
	        this.yieldEmailFrom = source["yieldEmailFrom"];
	        this.yieldEmailSmtpHost = source["yieldEmailSmtpHost"];
	        this.yieldEmailSmtpPort = source["yieldEmailSmtpPort"];
	        this.yieldEmailSmtpUsername = source["yieldEmailSmtpUsername"];
	        this.yieldEmailSmtpPassword = source["yieldEmailSmtpPassword"];
	        this.yieldEmailCronEnabled = source["yieldEmailCronEnabled"];
	        this.yieldEmailCronTimes = source["yieldEmailCronTimes"];
	        this.marketSummaryEmailEnabled = source["marketSummaryEmailEnabled"];
	        this.updateBasicInfoOnStart = source["updateBasicInfoOnStart"];
	        this.refreshInterval = source["refreshInterval"];
	        this.openAiEnable = source["openAiEnable"];
	        this.prompt = source["prompt"];
	        this.checkUpdate = source["checkUpdate"];
	        this.questionTemplate = source["questionTemplate"];
	        this.crawlTimeOut = source["crawlTimeOut"];
	        this.kDays = source["kDays"];
	        this.enableDanmu = source["enableDanmu"];
	        this.browserPath = source["browserPath"];
	        this.enableNews = source["enableNews"];
	        this.darkTheme = source["darkTheme"];
	        this.browserPoolSize = source["browserPoolSize"];
	        this.enableFund = source["enableFund"];
	        this.enablePushNews = source["enablePushNews"];
	        this.enableOnlyPushRedNews = source["enableOnlyPushRedNews"];
	        this.httpProxy = source["httpProxy"];
	        this.httpProxyEnabled = source["httpProxyEnabled"];
	        this.forceNoProxyForFetch = source["forceNoProxyForFetch"];
	        this.enableAgent = source["enableAgent"];
	        this.qgqpBId = source["qgqpBId"];
	        this.marketSummaryCronEnabled = source["marketSummaryCronEnabled"];
	        this.marketSummaryCronTimes = source["marketSummaryCronTimes"];
	        this.minuteProviderMode = source["minuteProviderMode"];
	        this.minuteLongHistoryHintEnabled = source["minuteLongHistoryHintEnabled"];
	        this.privateMinuteEnabled = source["privateMinuteEnabled"];
	        this.privateMinuteBaseUrl = source["privateMinuteBaseUrl"];
	        this.privateMinuteApiKey = source["privateMinuteApiKey"];
	        this.privateMinuteTimeoutSec = source["privateMinuteTimeoutSec"];
	        this.privateMinuteMinIntervalMs = source["privateMinuteMinIntervalMs"];
	        this.privateMinuteProxyMode = source["privateMinuteProxyMode"];
	        this.privateMinuteLevel = source["privateMinuteLevel"];
	        this.akshareEnabled = source["akshareEnabled"];
	        this.sinaMinuteEnabled = source["sinaMinuteEnabled"];
	        this.tencentMinuteEnabled = source["tencentMinuteEnabled"];
	        this.eastmoneyMinuteEnabled = source["eastmoneyMinuteEnabled"];
	        this.akshareMinuteSourceMode = source["akshareMinuteSourceMode"];
	        this.aiConfigs = this.convertValues(source["aiConfigs"], AIConfig);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class StockBasic {
	    ID: number;
	    // Go type: time
	    CreatedAt: any;
	    // Go type: time
	    UpdatedAt: any;
	    // Go type: gorm
	    DeletedAt: any;
	    ts_code: string;
	    symbol: string;
	    name: string;
	    area: string;
	    industry: string;
	    fullname: string;
	    enname: string;
	    cnspell: string;
	    market: string;
	    exchange: string;
	    curr_type: string;
	    list_status: string;
	    list_date: string;
	    delist_date: string;
	    is_hs: string;
	    act_name: string;
	    act_ent_type: string;
	    bk_name: string;
	    bk_code: string;
	
	    static createFrom(source: any = {}) {
	        return new StockBasic(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.CreatedAt = this.convertValues(source["CreatedAt"], null);
	        this.UpdatedAt = this.convertValues(source["UpdatedAt"], null);
	        this.DeletedAt = this.convertValues(source["DeletedAt"], null);
	        this.ts_code = source["ts_code"];
	        this.symbol = source["symbol"];
	        this.name = source["name"];
	        this.area = source["area"];
	        this.industry = source["industry"];
	        this.fullname = source["fullname"];
	        this.enname = source["enname"];
	        this.cnspell = source["cnspell"];
	        this.market = source["market"];
	        this.exchange = source["exchange"];
	        this.curr_type = source["curr_type"];
	        this.list_status = source["list_status"];
	        this.list_date = source["list_date"];
	        this.delist_date = source["delist_date"];
	        this.is_hs = source["is_hs"];
	        this.act_name = source["act_name"];
	        this.act_ent_type = source["act_ent_type"];
	        this.bk_name = source["bk_name"];
	        this.bk_code = source["bk_code"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class StockInfo {
	    ID: number;
	    // Go type: time
	    CreatedAt: any;
	    // Go type: time
	    UpdatedAt: any;
	    // Go type: gorm
	    DeletedAt: any;
	    "日期": string;
	    "时间": string;
	    "股票代码": string;
	    "股票名称": string;
	    "上次当前价格": number;
	    "当前价格": string;
	    "成交的股票数": string;
	    "成交金额": string;
	    "今日开盘价": string;
	    "昨日收盘价": string;
	    "今日最高价": string;
	    "今日最低价": string;
	    "竞买价": string;
	    "竞卖价": string;
	    "买一报价": string;
	    "买一申报": string;
	    "买二报价": string;
	    "买二申报": string;
	    "买三报价": string;
	    "买三申报": string;
	    "买四报价": string;
	    "买四申报": string;
	    "买五报价": string;
	    "买五申报": string;
	    "卖一报价": string;
	    "卖一申报": string;
	    "卖二报价": string;
	    "卖二申报": string;
	    "卖三报价": string;
	    "卖三申报": string;
	    "卖四报价": string;
	    "卖四申报": string;
	    "卖五报价": string;
	    "卖五申报": string;
	    "市场": string;
	    "盘前盘后": string;
	    "盘前盘后涨跌幅": string;
	    changePercent: number;
	    changePrice: number;
	    highRate: number;
	    lowRate: number;
	    costPrice: number;
	    costVolume: number;
	    profit: number;
	    profitAmount: number;
	    profitAmountToday: number;
	    sort: number;
	    alarmChangePercent: number;
	    alarmPrice: number;
	    Groups: GroupStock[];
	
	    static createFrom(source: any = {}) {
	        return new StockInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.CreatedAt = this.convertValues(source["CreatedAt"], null);
	        this.UpdatedAt = this.convertValues(source["UpdatedAt"], null);
	        this.DeletedAt = this.convertValues(source["DeletedAt"], null);
	        this["日期"] = source["日期"];
	        this["时间"] = source["时间"];
	        this["股票代码"] = source["股票代码"];
	        this["股票名称"] = source["股票名称"];
	        this["上次当前价格"] = source["上次当前价格"];
	        this["当前价格"] = source["当前价格"];
	        this["成交的股票数"] = source["成交的股票数"];
	        this["成交金额"] = source["成交金额"];
	        this["今日开盘价"] = source["今日开盘价"];
	        this["昨日收盘价"] = source["昨日收盘价"];
	        this["今日最高价"] = source["今日最高价"];
	        this["今日最低价"] = source["今日最低价"];
	        this["竞买价"] = source["竞买价"];
	        this["竞卖价"] = source["竞卖价"];
	        this["买一报价"] = source["买一报价"];
	        this["买一申报"] = source["买一申报"];
	        this["买二报价"] = source["买二报价"];
	        this["买二申报"] = source["买二申报"];
	        this["买三报价"] = source["买三报价"];
	        this["买三申报"] = source["买三申报"];
	        this["买四报价"] = source["买四报价"];
	        this["买四申报"] = source["买四申报"];
	        this["买五报价"] = source["买五报价"];
	        this["买五申报"] = source["买五申报"];
	        this["卖一报价"] = source["卖一报价"];
	        this["卖一申报"] = source["卖一申报"];
	        this["卖二报价"] = source["卖二报价"];
	        this["卖二申报"] = source["卖二申报"];
	        this["卖三报价"] = source["卖三报价"];
	        this["卖三申报"] = source["卖三申报"];
	        this["卖四报价"] = source["卖四报价"];
	        this["卖四申报"] = source["卖四申报"];
	        this["卖五报价"] = source["卖五报价"];
	        this["卖五申报"] = source["卖五申报"];
	        this["市场"] = source["市场"];
	        this["盘前盘后"] = source["盘前盘后"];
	        this["盘前盘后涨跌幅"] = source["盘前盘后涨跌幅"];
	        this.changePercent = source["changePercent"];
	        this.changePrice = source["changePrice"];
	        this.highRate = source["highRate"];
	        this.lowRate = source["lowRate"];
	        this.costPrice = source["costPrice"];
	        this.costVolume = source["costVolume"];
	        this.profit = source["profit"];
	        this.profitAmount = source["profitAmount"];
	        this.profitAmountToday = source["profitAmountToday"];
	        this.sort = source["sort"];
	        this.alarmChangePercent = source["alarmChangePercent"];
	        this.alarmPrice = source["alarmPrice"];
	        this.Groups = this.convertValues(source["Groups"], GroupStock);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace models {
	
	export class AIResponseResult {
	    ID: number;
	    // Go type: time
	    CreatedAt: any;
	    // Go type: time
	    UpdatedAt: any;
	    // Go type: gorm
	    DeletedAt: any;
	    chatId: string;
	    providerName: string;
	    modelName: string;
	    stockCode: string;
	    stockName: string;
	    question: string;
	    content: string;
	    IsDel: number;
	
	    static createFrom(source: any = {}) {
	        return new AIResponseResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.CreatedAt = this.convertValues(source["CreatedAt"], null);
	        this.UpdatedAt = this.convertValues(source["UpdatedAt"], null);
	        this.DeletedAt = this.convertValues(source["DeletedAt"], null);
	        this.chatId = source["chatId"];
	        this.providerName = source["providerName"];
	        this.modelName = source["modelName"];
	        this.stockCode = source["stockCode"];
	        this.stockName = source["stockName"];
	        this.question = source["question"];
	        this.content = source["content"];
	        this.IsDel = source["IsDel"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AIResponseResultPageData {
	    list: AIResponseResult[];
	    total: number;
	    page: number;
	    pageSize: number;
	    totalPages: number;
	
	    static createFrom(source: any = {}) {
	        return new AIResponseResultPageData(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.list = this.convertValues(source["list"], AIResponseResult);
	        this.total = source["total"];
	        this.page = source["page"];
	        this.pageSize = source["pageSize"];
	        this.totalPages = source["totalPages"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AIResponseResultQuery {
	    page: number;
	    pageSize: number;
	    chatId: string;
	    modelName: string;
	    stockCode: string;
	    stockName: string;
	    question: string;
	    startDate: string;
	    endDate: string;
	
	    static createFrom(source: any = {}) {
	        return new AIResponseResultQuery(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.page = source["page"];
	        this.pageSize = source["pageSize"];
	        this.chatId = source["chatId"];
	        this.modelName = source["modelName"];
	        this.stockCode = source["stockCode"];
	        this.stockName = source["stockName"];
	        this.question = source["question"];
	        this.startDate = source["startDate"];
	        this.endDate = source["endDate"];
	    }
	}
	export class AgentChatMessage {
	    ID: number;
	    // Go type: time
	    CreatedAt: any;
	    // Go type: time
	    UpdatedAt: any;
	    // Go type: gorm
	    DeletedAt: any;
	    sessionId: string;
	    role: string;
	    content: string;
	    reasoning: string;
	    seq: number;
	    isDel: number;
	
	    static createFrom(source: any = {}) {
	        return new AgentChatMessage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.CreatedAt = this.convertValues(source["CreatedAt"], null);
	        this.UpdatedAt = this.convertValues(source["UpdatedAt"], null);
	        this.DeletedAt = this.convertValues(source["DeletedAt"], null);
	        this.sessionId = source["sessionId"];
	        this.role = source["role"];
	        this.content = source["content"];
	        this.reasoning = source["reasoning"];
	        this.seq = source["seq"];
	        this.isDel = source["isDel"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AgentChatSession {
	    ID: number;
	    // Go type: time
	    CreatedAt: any;
	    // Go type: time
	    UpdatedAt: any;
	    // Go type: gorm
	    DeletedAt: any;
	    sessionId: string;
	    title: string;
	    aiConfigId: number;
	    modelName: string;
	    // Go type: time
	    lastMessageAt?: any;
	    messageCount: number;
	    isPinned: boolean;
	    isDel: number;
	
	    static createFrom(source: any = {}) {
	        return new AgentChatSession(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.CreatedAt = this.convertValues(source["CreatedAt"], null);
	        this.UpdatedAt = this.convertValues(source["UpdatedAt"], null);
	        this.DeletedAt = this.convertValues(source["DeletedAt"], null);
	        this.sessionId = source["sessionId"];
	        this.title = source["title"];
	        this.aiConfigId = source["aiConfigId"];
	        this.modelName = source["modelName"];
	        this.lastMessageAt = this.convertValues(source["lastMessageAt"], null);
	        this.messageCount = source["messageCount"];
	        this.isPinned = source["isPinned"];
	        this.isDel = source["isDel"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AiRecommendOpeningReviewSummary {
	    recommendId: number;
	    tradeDate: string;
	    reviewScope: string;
	    reviewPhase: string;
	    openingPrice: number;
	    auctionPrice: number;
	    minutePrice: number;
	    minuteVolume: number;
	    minuteAmount: number;
	    gapType: string;
	    action: string;
	    reason: string;
	    suggestedStopLoss: number;
	    suggestedTakeProfit: number;
	    modelName: string;
	    rawSummary: string;
	
	    static createFrom(source: any = {}) {
	        return new AiRecommendOpeningReviewSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.recommendId = source["recommendId"];
	        this.tradeDate = source["tradeDate"];
	        this.reviewScope = source["reviewScope"];
	        this.reviewPhase = source["reviewPhase"];
	        this.openingPrice = source["openingPrice"];
	        this.auctionPrice = source["auctionPrice"];
	        this.minutePrice = source["minutePrice"];
	        this.minuteVolume = source["minuteVolume"];
	        this.minuteAmount = source["minuteAmount"];
	        this.gapType = source["gapType"];
	        this.action = source["action"];
	        this.reason = source["reason"];
	        this.suggestedStopLoss = source["suggestedStopLoss"];
	        this.suggestedTakeProfit = source["suggestedTakeProfit"];
	        this.modelName = source["modelName"];
	        this.rawSummary = source["rawSummary"];
	    }
	}
	export class AiRecommendStocks {
	    ID: number;
	    // Go type: time
	    CreatedAt: any;
	    // Go type: time
	    UpdatedAt: any;
	    // Go type: gorm
	    DeletedAt: any;
	    // Go type: time
	    dataTime?: any;
	    providerName: string;
	    modelName: string;
	    stockCode: string;
	    stockName: string;
	    bkCode: string;
	    bkName: string;
	    stockPrice: string;
	    stockCurrentPrice: string;
	    stockCurrentPriceTime: string;
	    stockClosePrice: string;
	    stockPrePrice: string;
	    recommendReason: string;
	    recommendBuyPrice: string;
	    recommendBuyPriceMin: number;
	    recommendBuyPriceMax: number;
	    recommendStopProfitPrice: string;
	    recommendStopProfitPriceMin: number;
	    recommendStopProfitPriceMax: number;
	    recommendStopLossPrice: string;
	    recommendCategory: string;
	    executionState: string;
	    buySignal: string;
	    buySignalDetail: string;
	    sellSignal: string;
	    sellSignalDetail: string;
	    invalidSignal: string;
	    coreCatalyst: string;
	    keyEvidence: string;
	    evidenceSources: string;
	    invalidCondition: string;
	    observePrice: string;
	    focusPrice: string;
	    expectedCycle: string;
	    eventStrength: number;
	    capitalConfirmation: number;
	    fundamentalFit: number;
	    technicalFit: number;
	    activationRuleJson: string;
	    activationRuleVersion: string;
	    activationRuleSource: string;
	    activationStatus: string;
	    activationInvalidReason: string;
	    recommendStatus: string;
	    summaryVersion: string;
	    riskRemarks: string;
	    remarks: string;
	    latestOpeningReview?: AiRecommendOpeningReviewSummary;
	
	    static createFrom(source: any = {}) {
	        return new AiRecommendStocks(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.CreatedAt = this.convertValues(source["CreatedAt"], null);
	        this.UpdatedAt = this.convertValues(source["UpdatedAt"], null);
	        this.DeletedAt = this.convertValues(source["DeletedAt"], null);
	        this.dataTime = this.convertValues(source["dataTime"], null);
	        this.providerName = source["providerName"];
	        this.modelName = source["modelName"];
	        this.stockCode = source["stockCode"];
	        this.stockName = source["stockName"];
	        this.bkCode = source["bkCode"];
	        this.bkName = source["bkName"];
	        this.stockPrice = source["stockPrice"];
	        this.stockCurrentPrice = source["stockCurrentPrice"];
	        this.stockCurrentPriceTime = source["stockCurrentPriceTime"];
	        this.stockClosePrice = source["stockClosePrice"];
	        this.stockPrePrice = source["stockPrePrice"];
	        this.recommendReason = source["recommendReason"];
	        this.recommendBuyPrice = source["recommendBuyPrice"];
	        this.recommendBuyPriceMin = source["recommendBuyPriceMin"];
	        this.recommendBuyPriceMax = source["recommendBuyPriceMax"];
	        this.recommendStopProfitPrice = source["recommendStopProfitPrice"];
	        this.recommendStopProfitPriceMin = source["recommendStopProfitPriceMin"];
	        this.recommendStopProfitPriceMax = source["recommendStopProfitPriceMax"];
	        this.recommendStopLossPrice = source["recommendStopLossPrice"];
	        this.recommendCategory = source["recommendCategory"];
	        this.executionState = source["executionState"];
	        this.buySignal = source["buySignal"];
	        this.buySignalDetail = source["buySignalDetail"];
	        this.sellSignal = source["sellSignal"];
	        this.sellSignalDetail = source["sellSignalDetail"];
	        this.invalidSignal = source["invalidSignal"];
	        this.coreCatalyst = source["coreCatalyst"];
	        this.keyEvidence = source["keyEvidence"];
	        this.evidenceSources = source["evidenceSources"];
	        this.invalidCondition = source["invalidCondition"];
	        this.observePrice = source["observePrice"];
	        this.focusPrice = source["focusPrice"];
	        this.expectedCycle = source["expectedCycle"];
	        this.eventStrength = source["eventStrength"];
	        this.capitalConfirmation = source["capitalConfirmation"];
	        this.fundamentalFit = source["fundamentalFit"];
	        this.technicalFit = source["technicalFit"];
	        this.activationRuleJson = source["activationRuleJson"];
	        this.activationRuleVersion = source["activationRuleVersion"];
	        this.activationRuleSource = source["activationRuleSource"];
	        this.activationStatus = source["activationStatus"];
	        this.activationInvalidReason = source["activationInvalidReason"];
	        this.recommendStatus = source["recommendStatus"];
	        this.summaryVersion = source["summaryVersion"];
	        this.riskRemarks = source["riskRemarks"];
	        this.remarks = source["remarks"];
	        this.latestOpeningReview = this.convertValues(source["latestOpeningReview"], AiRecommendOpeningReviewSummary);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AiRecommendStocksPageData {
	    list: AiRecommendStocks[];
	    total: number;
	    page: number;
	    pageSize: number;
	    totalPages: number;
	    strategyCohort?: string;
	
	    static createFrom(source: any = {}) {
	        return new AiRecommendStocksPageData(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.list = this.convertValues(source["list"], AiRecommendStocks);
	        this.total = source["total"];
	        this.page = source["page"];
	        this.pageSize = source["pageSize"];
	        this.totalPages = source["totalPages"];
	        this.strategyCohort = source["strategyCohort"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AiRecommendStocksQuery {
	    page: number;
	    pageSize: number;
	    modelName: string;
	    stockCode: string;
	    stockName: string;
	    bkCode: string;
	    bkName: string;
	    startDate: string;
	    endDate: string;
	    yieldMode: string;
	    strategyCohort: string;
	
	    static createFrom(source: any = {}) {
	        return new AiRecommendStocksQuery(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.page = source["page"];
	        this.pageSize = source["pageSize"];
	        this.modelName = source["modelName"];
	        this.stockCode = source["stockCode"];
	        this.stockName = source["stockName"];
	        this.bkCode = source["bkCode"];
	        this.bkName = source["bkName"];
	        this.startDate = source["startDate"];
	        this.endDate = source["endDate"];
	        this.yieldMode = source["yieldMode"];
	        this.strategyCohort = source["strategyCohort"];
	    }
	}
	export class AiRecommendStocksYieldItem {
	    recommendId: number;
	    rowKey: string;
	    calcMode?: string;
	    strictReady: boolean;
	    strictPendingReason?: string;
	    stockCode: string;
	    stockName: string;
	    modelNames?: string;
	    backtestEligibility: string;
	    backtestEligibilityReason?: string;
	    bkName: string;
	    recommendCategory?: string;
	    recommendCategoryLabel?: string;
	    executionState: string;
	    executionStateLabel: string;
	    buySignal: string;
	    buySignalDetail: string;
	    sellSignal: string;
	    sellSignalDetail: string;
	    invalidSignal: string;
	    activationRule?: string;
	    activationInvalidReason?: string;
	    recommendCount: number;
	    recommendTime: string;
	    signalTime: string;
	    activationStatus: string;
	    activationTime: string;
	    activationPrice: number;
	    recommendBuyPrice?: string;
	    buyTime: string;
	    buyAmount: number;
	    stopProfitAmount?: number;
	    stopLossAmount?: number;
	    sellTime: string;
	    sellAmount?: number;
	    sellAmountText: string;
	    positionStatus: string;
	    currentPrice: number;
	    currentPriceTime: string;
	    yieldRate: number;
	    yieldRateText: string;
	    benchmarkYieldRate: number;
	    benchmarkYieldRateText: string;
	    excessYieldRate: number;
	    excessYieldRateText: string;
	    dataStatus: string;
	    dataStatusReason?: string;
	    latestOpeningReview?: AiRecommendOpeningReviewSummary;
	
	    static createFrom(source: any = {}) {
	        return new AiRecommendStocksYieldItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.recommendId = source["recommendId"];
	        this.rowKey = source["rowKey"];
	        this.calcMode = source["calcMode"];
	        this.strictReady = source["strictReady"];
	        this.strictPendingReason = source["strictPendingReason"];
	        this.stockCode = source["stockCode"];
	        this.stockName = source["stockName"];
	        this.modelNames = source["modelNames"];
	        this.backtestEligibility = source["backtestEligibility"];
	        this.backtestEligibilityReason = source["backtestEligibilityReason"];
	        this.bkName = source["bkName"];
	        this.recommendCategory = source["recommendCategory"];
	        this.recommendCategoryLabel = source["recommendCategoryLabel"];
	        this.executionState = source["executionState"];
	        this.executionStateLabel = source["executionStateLabel"];
	        this.buySignal = source["buySignal"];
	        this.buySignalDetail = source["buySignalDetail"];
	        this.sellSignal = source["sellSignal"];
	        this.sellSignalDetail = source["sellSignalDetail"];
	        this.invalidSignal = source["invalidSignal"];
	        this.activationRule = source["activationRule"];
	        this.activationInvalidReason = source["activationInvalidReason"];
	        this.recommendCount = source["recommendCount"];
	        this.recommendTime = source["recommendTime"];
	        this.signalTime = source["signalTime"];
	        this.activationStatus = source["activationStatus"];
	        this.activationTime = source["activationTime"];
	        this.activationPrice = source["activationPrice"];
	        this.recommendBuyPrice = source["recommendBuyPrice"];
	        this.buyTime = source["buyTime"];
	        this.buyAmount = source["buyAmount"];
	        this.stopProfitAmount = source["stopProfitAmount"];
	        this.stopLossAmount = source["stopLossAmount"];
	        this.sellTime = source["sellTime"];
	        this.sellAmount = source["sellAmount"];
	        this.sellAmountText = source["sellAmountText"];
	        this.positionStatus = source["positionStatus"];
	        this.currentPrice = source["currentPrice"];
	        this.currentPriceTime = source["currentPriceTime"];
	        this.yieldRate = source["yieldRate"];
	        this.yieldRateText = source["yieldRateText"];
	        this.benchmarkYieldRate = source["benchmarkYieldRate"];
	        this.benchmarkYieldRateText = source["benchmarkYieldRateText"];
	        this.excessYieldRate = source["excessYieldRate"];
	        this.excessYieldRateText = source["excessYieldRateText"];
	        this.dataStatus = source["dataStatus"];
	        this.dataStatusReason = source["dataStatusReason"];
	        this.latestOpeningReview = this.convertValues(source["latestOpeningReview"], AiRecommendOpeningReviewSummary);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AiRecommendStocksYieldPageData {
	    list: AiRecommendStocksYieldItem[];
	    total: number;
	    page: number;
	    pageSize: number;
	    totalPages: number;
	    calcMode?: string;
	    totalYieldRate: number;
	    totalYieldRateText: string;
	    benchmarkCode: string;
	    benchmarkName: string;
	    benchmarkRate: number;
	    benchmarkRateText: string;
	    excessYieldRate: number;
	    excessYieldRateText: string;
	    strategyXirr: number;
	    strategyXirrText: string;
	    benchmarkXirr: number;
	    benchmarkXirrText: string;
	    excessXirr: number;
	    excessXirrText: string;
	    maxDrawdown: number;
	    maxDrawdownText: string;
	    winRateVsBenchmark: number;
	    winRateVsBenchmarkText: string;
	    medianExcessYieldRate: number;
	    medianExcessYieldRateText: string;
	    strategyCohort?: string;
	    sameDayActivationRate: number;
	    sameDayActivationRateText?: string;
	    staleActivationRate: number;
	    staleActivationRateText?: string;
	    structuredRuleCoverage: number;
	    structuredRuleCoverageText?: string;
	    analysisOnlyRate: number;
	    analysisOnlyRateText?: string;
	    stopLossCount: number;
	    takeProfitCount: number;
	    openCount: number;
	    dataAsOf: string;
	    recalcInProgress: boolean;
	    recalcProgress: number;
	    minuteDownloadDone: number;
	    minuteDownloadTotal: number;
	    minuteDownloadPending: number;
	    minuteDownloadUncoverable: number;
	    manualCooldownUntil: string;
	    manualCooldownRemainSec: number;
	    lastManualStartedAt?: string;
	    lastManualFinishedAt?: string;
	    lastManualScopeCount?: number;
	    lastManualPrefetchMs?: number;
	    lastManualRecalcMs?: number;
	    lastManualTotalMs?: number;
	    lastManualSqliteBusyCount?: number;
	    lastManualProviderSummary?: string;
	    lastManualAuditReady?: boolean;
	    diemengHealthStatus?: string;
	    diemengHealthSummary?: string;
	    diemengHealthCheckedAt?: string;
	
	    static createFrom(source: any = {}) {
	        return new AiRecommendStocksYieldPageData(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.list = this.convertValues(source["list"], AiRecommendStocksYieldItem);
	        this.total = source["total"];
	        this.page = source["page"];
	        this.pageSize = source["pageSize"];
	        this.totalPages = source["totalPages"];
	        this.calcMode = source["calcMode"];
	        this.totalYieldRate = source["totalYieldRate"];
	        this.totalYieldRateText = source["totalYieldRateText"];
	        this.benchmarkCode = source["benchmarkCode"];
	        this.benchmarkName = source["benchmarkName"];
	        this.benchmarkRate = source["benchmarkRate"];
	        this.benchmarkRateText = source["benchmarkRateText"];
	        this.excessYieldRate = source["excessYieldRate"];
	        this.excessYieldRateText = source["excessYieldRateText"];
	        this.strategyXirr = source["strategyXirr"];
	        this.strategyXirrText = source["strategyXirrText"];
	        this.benchmarkXirr = source["benchmarkXirr"];
	        this.benchmarkXirrText = source["benchmarkXirrText"];
	        this.excessXirr = source["excessXirr"];
	        this.excessXirrText = source["excessXirrText"];
	        this.maxDrawdown = source["maxDrawdown"];
	        this.maxDrawdownText = source["maxDrawdownText"];
	        this.winRateVsBenchmark = source["winRateVsBenchmark"];
	        this.winRateVsBenchmarkText = source["winRateVsBenchmarkText"];
	        this.medianExcessYieldRate = source["medianExcessYieldRate"];
	        this.medianExcessYieldRateText = source["medianExcessYieldRateText"];
	        this.strategyCohort = source["strategyCohort"];
	        this.sameDayActivationRate = source["sameDayActivationRate"];
	        this.sameDayActivationRateText = source["sameDayActivationRateText"];
	        this.staleActivationRate = source["staleActivationRate"];
	        this.staleActivationRateText = source["staleActivationRateText"];
	        this.structuredRuleCoverage = source["structuredRuleCoverage"];
	        this.structuredRuleCoverageText = source["structuredRuleCoverageText"];
	        this.analysisOnlyRate = source["analysisOnlyRate"];
	        this.analysisOnlyRateText = source["analysisOnlyRateText"];
	        this.stopLossCount = source["stopLossCount"];
	        this.takeProfitCount = source["takeProfitCount"];
	        this.openCount = source["openCount"];
	        this.dataAsOf = source["dataAsOf"];
	        this.recalcInProgress = source["recalcInProgress"];
	        this.recalcProgress = source["recalcProgress"];
	        this.minuteDownloadDone = source["minuteDownloadDone"];
	        this.minuteDownloadTotal = source["minuteDownloadTotal"];
	        this.minuteDownloadPending = source["minuteDownloadPending"];
	        this.minuteDownloadUncoverable = source["minuteDownloadUncoverable"];
	        this.manualCooldownUntil = source["manualCooldownUntil"];
	        this.manualCooldownRemainSec = source["manualCooldownRemainSec"];
	        this.lastManualStartedAt = source["lastManualStartedAt"];
	        this.lastManualFinishedAt = source["lastManualFinishedAt"];
	        this.lastManualScopeCount = source["lastManualScopeCount"];
	        this.lastManualPrefetchMs = source["lastManualPrefetchMs"];
	        this.lastManualRecalcMs = source["lastManualRecalcMs"];
	        this.lastManualTotalMs = source["lastManualTotalMs"];
	        this.lastManualSqliteBusyCount = source["lastManualSqliteBusyCount"];
	        this.lastManualProviderSummary = source["lastManualProviderSummary"];
	        this.lastManualAuditReady = source["lastManualAuditReady"];
	        this.diemengHealthStatus = source["diemengHealthStatus"];
	        this.diemengHealthSummary = source["diemengHealthSummary"];
	        this.diemengHealthCheckedAt = source["diemengHealthCheckedAt"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AiRecommendYieldChartMarker {
	    type: string;
	    time: string;
	    price: number;
	    label: string;
	    status: string;
	
	    static createFrom(source: any = {}) {
	        return new AiRecommendYieldChartMarker(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.time = source["time"];
	        this.price = source["price"];
	        this.label = source["label"];
	        this.status = source["status"];
	    }
	}
	export class AiRecommendYieldDailyOverviewPoint {
	    tradeDate: string;
	    costBasisNet: number;
	    dailyHoldingCostNet: number;
	    holdingCount: number;
	    cumulativeAmountChange: number;
	    cumulativeYieldRate: number;
	    dailyAmountChange: number;
	    dailyYieldRate: number;
	    benchmarkClose: number;
	    benchmarkCumulativeAmountChange: number;
	    benchmarkDailyAmountChange: number;
	    benchmarkCumulativeRate: number;
	    benchmarkDailyRate: number;
	    excessCumulativeAmountChange: number;
	    excessDailyAmountChange: number;
	    excessCumulativeRate: number;
	    excessDailyRate: number;
	    strategyNav: number;
	    benchmarkNav: number;
	
	    static createFrom(source: any = {}) {
	        return new AiRecommendYieldDailyOverviewPoint(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tradeDate = source["tradeDate"];
	        this.costBasisNet = source["costBasisNet"];
	        this.dailyHoldingCostNet = source["dailyHoldingCostNet"];
	        this.holdingCount = source["holdingCount"];
	        this.cumulativeAmountChange = source["cumulativeAmountChange"];
	        this.cumulativeYieldRate = source["cumulativeYieldRate"];
	        this.dailyAmountChange = source["dailyAmountChange"];
	        this.dailyYieldRate = source["dailyYieldRate"];
	        this.benchmarkClose = source["benchmarkClose"];
	        this.benchmarkCumulativeAmountChange = source["benchmarkCumulativeAmountChange"];
	        this.benchmarkDailyAmountChange = source["benchmarkDailyAmountChange"];
	        this.benchmarkCumulativeRate = source["benchmarkCumulativeRate"];
	        this.benchmarkDailyRate = source["benchmarkDailyRate"];
	        this.excessCumulativeAmountChange = source["excessCumulativeAmountChange"];
	        this.excessDailyAmountChange = source["excessDailyAmountChange"];
	        this.excessCumulativeRate = source["excessCumulativeRate"];
	        this.excessDailyRate = source["excessDailyRate"];
	        this.strategyNav = source["strategyNav"];
	        this.benchmarkNav = source["benchmarkNav"];
	    }
	}
	export class AiRecommendYieldDailyOverviewData {
	    rangeStart: string;
	    rangeEnd: string;
	    dataAsOf: string;
	    calcMode: string;
	    benchmarkCode: string;
	    benchmarkName: string;
	    strategyCohort?: string;
	    totalRecordCount: number;
	    includedRecordCount: number;
	    skippedRecordCount: number;
	    warnings: string[];
	    points: AiRecommendYieldDailyOverviewPoint[];
	
	    static createFrom(source: any = {}) {
	        return new AiRecommendYieldDailyOverviewData(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.rangeStart = source["rangeStart"];
	        this.rangeEnd = source["rangeEnd"];
	        this.dataAsOf = source["dataAsOf"];
	        this.calcMode = source["calcMode"];
	        this.benchmarkCode = source["benchmarkCode"];
	        this.benchmarkName = source["benchmarkName"];
	        this.strategyCohort = source["strategyCohort"];
	        this.totalRecordCount = source["totalRecordCount"];
	        this.includedRecordCount = source["includedRecordCount"];
	        this.skippedRecordCount = source["skippedRecordCount"];
	        this.warnings = source["warnings"];
	        this.points = this.convertValues(source["points"], AiRecommendYieldDailyOverviewPoint);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class AiRecommendYieldMinuteBarDTO {
	    tradeTime: string;
	    open: number;
	    high: number;
	    low: number;
	    close: number;
	    volume: number;
	    amount: number;
	
	    static createFrom(source: any = {}) {
	        return new AiRecommendYieldMinuteBarDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tradeTime = source["tradeTime"];
	        this.open = source["open"];
	        this.high = source["high"];
	        this.low = source["low"];
	        this.close = source["close"];
	        this.volume = source["volume"];
	        this.amount = source["amount"];
	    }
	}
	export class AiRecommendYieldMinuteChartData {
	    recommendId: number;
	    stockCode: string;
	    stockName: string;
	    signalTime: string;
	    buyTime: string;
	    sellTime: string;
	    currentPrice: number;
	    currentPriceTime: string;
	    activationStatus: string;
	    positionStatus: string;
	    dataStatus: string;
	    dataStatusReason?: string;
	    rangeStart: string;
	    rangeEnd: string;
	    rangeLabel: string;
	    chartStatus: string;
	    message?: string;
	    bars: AiRecommendYieldMinuteBarDTO[];
	    markers: AiRecommendYieldChartMarker[];
	    latestOpeningReview?: AiRecommendOpeningReviewSummary;
	
	    static createFrom(source: any = {}) {
	        return new AiRecommendYieldMinuteChartData(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.recommendId = source["recommendId"];
	        this.stockCode = source["stockCode"];
	        this.stockName = source["stockName"];
	        this.signalTime = source["signalTime"];
	        this.buyTime = source["buyTime"];
	        this.sellTime = source["sellTime"];
	        this.currentPrice = source["currentPrice"];
	        this.currentPriceTime = source["currentPriceTime"];
	        this.activationStatus = source["activationStatus"];
	        this.positionStatus = source["positionStatus"];
	        this.dataStatus = source["dataStatus"];
	        this.dataStatusReason = source["dataStatusReason"];
	        this.rangeStart = source["rangeStart"];
	        this.rangeEnd = source["rangeEnd"];
	        this.rangeLabel = source["rangeLabel"];
	        this.chartStatus = source["chartStatus"];
	        this.message = source["message"];
	        this.bars = this.convertValues(source["bars"], AiRecommendYieldMinuteBarDTO);
	        this.markers = this.convertValues(source["markers"], AiRecommendYieldChartMarker);
	        this.latestOpeningReview = this.convertValues(source["latestOpeningReview"], AiRecommendOpeningReviewSummary);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class EmailSendLog {
	    ID: number;
	    // Go type: time
	    CreatedAt: any;
	    // Go type: time
	    UpdatedAt: any;
	    // Go type: gorm
	    DeletedAt: any;
	    sendType: string;
	    // Go type: time
	    triggeredAt: any;
	    status: string;
	    recipients: string;
	    subject: string;
	    errorMessage: string;
	    reportStockCode: string;
	    reportStockName: string;
	    // Go type: time
	    reportCreatedAt?: any;
	    attachmentNames: string;
	    attachmentCount: number;
	    attachmentBytes: number;
	    extraSummary: string;
	
	    static createFrom(source: any = {}) {
	        return new EmailSendLog(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.CreatedAt = this.convertValues(source["CreatedAt"], null);
	        this.UpdatedAt = this.convertValues(source["UpdatedAt"], null);
	        this.DeletedAt = this.convertValues(source["DeletedAt"], null);
	        this.sendType = source["sendType"];
	        this.triggeredAt = this.convertValues(source["triggeredAt"], null);
	        this.status = source["status"];
	        this.recipients = source["recipients"];
	        this.subject = source["subject"];
	        this.errorMessage = source["errorMessage"];
	        this.reportStockCode = source["reportStockCode"];
	        this.reportStockName = source["reportStockName"];
	        this.reportCreatedAt = this.convertValues(source["reportCreatedAt"], null);
	        this.attachmentNames = source["attachmentNames"];
	        this.attachmentCount = source["attachmentCount"];
	        this.attachmentBytes = source["attachmentBytes"];
	        this.extraSummary = source["extraSummary"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class EmailSendLogPageData {
	    list: EmailSendLog[];
	    total: number;
	    page: number;
	    pageSize: number;
	    totalPages: number;
	
	    static createFrom(source: any = {}) {
	        return new EmailSendLogPageData(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.list = this.convertValues(source["list"], EmailSendLog);
	        this.total = source["total"];
	        this.page = source["page"];
	        this.pageSize = source["pageSize"];
	        this.totalPages = source["totalPages"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class EmailSendLogQuery {
	    page: number;
	    pageSize: number;
	    sendType: string;
	    status: string;
	    recipient: string;
	    subject: string;
	    reportStockCode: string;
	    reportStockName: string;
	    startDate: string;
	    endDate: string;
	
	    static createFrom(source: any = {}) {
	        return new EmailSendLogQuery(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.page = source["page"];
	        this.pageSize = source["pageSize"];
	        this.sendType = source["sendType"];
	        this.status = source["status"];
	        this.recipient = source["recipient"];
	        this.subject = source["subject"];
	        this.reportStockCode = source["reportStockCode"];
	        this.reportStockName = source["reportStockName"];
	        this.startDate = source["startDate"];
	        this.endDate = source["endDate"];
	    }
	}
	export class Prompt {
	    ID: number;
	    name: string;
	    content: string;
	    type: string;
	
	    static createFrom(source: any = {}) {
	        return new Prompt(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.name = source["name"];
	        this.content = source["content"];
	        this.type = source["type"];
	    }
	}
	export class SentimentResult {
	    Score: number;
	    Category: number;
	    PositiveCount: number;
	    NegativeCount: number;
	    Description: string;
	
	    static createFrom(source: any = {}) {
	        return new SentimentResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Score = source["Score"];
	        this.Category = source["Category"];
	        this.PositiveCount = source["PositiveCount"];
	        this.NegativeCount = source["NegativeCount"];
	        this.Description = source["Description"];
	    }
	}
	export class VersionInfo {
	    ID: number;
	    // Go type: time
	    CreatedAt: any;
	    // Go type: time
	    UpdatedAt: any;
	    // Go type: gorm
	    DeletedAt: any;
	    version: string;
	    content: string;
	    icon: string;
	    alipay: string;
	    wxpay: string;
	    wxgzh: string;
	    buildTimeStamp: number;
	    officialStatement: string;
	    selfUpdateEnabled: boolean;
	    manualUpdateHint: string;
	    IsDel: number;
	
	    static createFrom(source: any = {}) {
	        return new VersionInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.CreatedAt = this.convertValues(source["CreatedAt"], null);
	        this.UpdatedAt = this.convertValues(source["UpdatedAt"], null);
	        this.DeletedAt = this.convertValues(source["DeletedAt"], null);
	        this.version = source["version"];
	        this.content = source["content"];
	        this.icon = source["icon"];
	        this.alipay = source["alipay"];
	        this.wxpay = source["wxpay"];
	        this.wxgzh = source["wxgzh"];
	        this.buildTimeStamp = source["buildTimeStamp"];
	        this.officialStatement = source["officialStatement"];
	        this.selfUpdateEnabled = source["selfUpdateEnabled"];
	        this.manualUpdateHint = source["manualUpdateHint"];
	        this.IsDel = source["IsDel"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

