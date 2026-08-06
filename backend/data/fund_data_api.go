package data

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/duke-git/lancet/v2/convertor"
	"github.com/duke-git/lancet/v2/mathutil"
	"github.com/duke-git/lancet/v2/strutil"
	"github.com/go-resty/resty/v2"
	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

type FundApi struct {
	client *resty.Client
	config *SettingConfig
}

func NewFundApi() *FundApi {
	return &FundApi{
		client: newNoProxyRestyClient(),
		config: GetSettingConfig(),
	}
}

type FollowedFund = models.FollowedFund
type FundBasic = models.FundBasic

// CrawlFundBasic 爬取基金基本信息
func (f *FundApi) CrawlFundBasic(fundCode string) (*FundBasic, error) {
	defer func() {
		if r := recover(); r != nil {
			logger.SugaredLogger.Errorf("CrawlFundBasic panic: %v", r)
		}
	}()

	crawler := CrawlerApi{
		crawlerBaseInfo: CrawlerBaseInfo{
			Name:    "天天基金",
			BaseUrl: "http://fund.eastmoney.com",
			Headers: map[string]string{"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/114.0.0.0 Safari/537.36"},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(f.config.CrawlTimeOut)*time.Second)
	defer cancel()

	crawler = crawler.NewCrawler(ctx, crawler.crawlerBaseInfo)
	url := fmt.Sprintf("%s/%s.html", crawler.crawlerBaseInfo.BaseUrl, fundCode)
	//logger.SugaredLogger.Infof("CrawlFundBasic url:%s", url)

	// 使用现有爬虫框架解析页面
	htmlContent, ok := crawler.GetHtml(url, ".merchandiseDetail", true)
	if !ok {
		return nil, fmt.Errorf("页面解析失败")
	}

	fund := &FundBasic{Code: fundCode}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		return nil, err
	}

	// 解析基础信息
	name := doc.Find(".merchandiseDetail .fundDetail-tit").First().Text()
	fund.Name = strings.TrimSpace(strutil.ReplaceWithMap(name, map[string]string{"查看相关ETF>": ""}))
	//logger.SugaredLogger.Infof("基金名称:%s", fund.Name)

	doc.Find(".infoOfFund table td ").Each(func(i int, s *goquery.Selection) {
		text := strutil.RemoveWhiteSpace(s.Text(), true)
		//logger.SugaredLogger.Infof("基金信息:%+v", text)
		defer func() {
			if r := recover(); r != nil {
				logger.SugaredLogger.Debugf("parse fund info panic recovered: %v", r)
			}
		}()
		splitEx := strutil.SplitEx(text, "：", true)
		if strutil.ContainsAny(text, []string{"基金类型", "类型"}) {
			fund.Type = splitEx[1]
		}
		if strutil.ContainsAny(text, []string{"成立日期", "成立日"}) {
			fund.Establishment = splitEx[1]
		}
		if strutil.ContainsAny(text, []string{"基金规模", "规模"}) {
			fund.Scale = splitEx[1]
		}
		if strutil.ContainsAny(text, []string{"管理人", "基金公司"}) {
			fund.Company = splitEx[1]
		}
		if strutil.ContainsAny(text, []string{"基金经理", "经理人"}) {
			fund.Manager = splitEx[1]
		}
		if strutil.ContainsAny(text, []string{"基金评级", "评级"}) {
			fund.Rating = splitEx[1]
		}
		if strutil.ContainsAny(text, []string{"跟踪标的", "标的"}) {
			fund.TrackingTarget = splitEx[1]
		}
	})

	//获取基金净值涨跌幅信息
	doc.Find(".dataOfFund dl > dd").Each(func(i int, s *goquery.Selection) {
		text := strutil.RemoveWhiteSpace(s.Text(), true)
		//logger.SugaredLogger.Infof("净值涨跌幅信息:%+v", text)
		defer func() {
			if r := recover(); r != nil {
				logger.SugaredLogger.Debugf("parse fund day metric panic recovered: %v", r)
			}
		}()
		splitEx := strutil.SplitAndTrim(text, "：", "%")
		toFloat, err1 := convertor.ToFloat(splitEx[1])
		if err1 != nil {
			//logger.SugaredLogger.Errorf("转换失败:%+v", err)
			return
		}
		//logger.SugaredLogger.Infof("净值涨跌幅信息:%+v", toFloat)
		if strutil.ContainsAny(text, []string{"近1月"}) {
			fund.NetGrowth1 = &toFloat
		}
		if strutil.ContainsAny(text, []string{"近3月"}) {
			fund.NetGrowth3 = &toFloat
		}
		if strutil.ContainsAny(text, []string{"近6月"}) {
			fund.NetGrowth6 = &toFloat
		}
		if strutil.ContainsAny(text, []string{"近1年"}) {
			fund.NetGrowth12 = &toFloat
		}
		if strutil.ContainsAny(text, []string{"近3年"}) {
			fund.NetGrowth36 = &toFloat
		}
		if strutil.ContainsAny(text, []string{"近5年"}) {
			fund.NetGrowth60 = &toFloat
		}
		if strutil.ContainsAny(text, []string{"今年来"}) {
			fund.NetGrowthYTD = &toFloat
		}
		if strutil.ContainsAny(text, []string{"成立来"}) {
			fund.NetGrowthAll = &toFloat
		}
	})
	//doc.Find(".dataOfFund dl > dd.dataNums,.dataOfFund dl > dt").Each(func(i int, s *goquery.Selection) {
	//	//text := s.Text()
	//	defer func() {
	//		if r := recover(); r != nil {
	//			//logger.SugaredLogger.Errorf("panic: %v", r)
	//		}
	//	}()
	//	//logger.SugaredLogger.Infof("净值信息:%+v", text)
	//})

	//logger.SugaredLogger.Infof("基金信息:%+v", fund)

	count := int64(0)
	db.Dao.Model(fund).Where("code=?", fund.Code).Count(&count)
	if count == 0 {
		db.Dao.Create(fund)
	} else {
		db.Dao.Model(fund).Where("code=?", fund.Code).Updates(fund)
	}

	return fund, nil
}

func (f *FundApi) GetFundList(key string) []FundBasic {
	var funds []FundBasic
	db.Dao.Where("code like ? or name like ?", "%"+key+"%", "%"+key+"%").Limit(10).Find(&funds)
	return funds
}

func (f *FundApi) GetFollowedFund() []FollowedFund {
	var funds []FollowedFund
	db.Dao.Preload("FundBasic").Find(&funds)
	for i, fund := range funds {
		if fund.NetUnitValue != nil && fund.NetEstimatedUnit != nil && *fund.NetUnitValue > 0 {
			netEstimatedRate := (*(funds[i].NetEstimatedUnit) - *(funds[i].NetUnitValue)) / *(fund.NetUnitValue) * 100
			netEstimatedRate = mathutil.RoundToFloat(netEstimatedRate, 2)
			funds[i].NetEstimatedRate = &netEstimatedRate
		}

	}
	return funds
}
func (f *FundApi) FollowFund(fundCode string) string {
	var fund FundBasic
	db.Dao.Where("code=?", fundCode).First(&fund)
	if fund.Code != "" {
		follow := &FollowedFund{
			Code: fundCode,
			Name: fund.Name,
		}
		err := db.Dao.Model(follow).Where("code = ?", fundCode).FirstOrCreate(follow, "code", fund.Code).Error
		if err != nil {
			return "关注失败"
		}
		return "关注成功"
	} else {
		return "基金信息不存在"
	}
}
func (f *FundApi) UnFollowFund(fundCode string) string {
	var fund FollowedFund
	db.Dao.Where("code=?", fundCode).First(&fund)
	if fund.Code != "" {
		err := db.Dao.Model(&fund).Delete(&fund).Error
		if err != nil {
			return "取消关注失败"
		}
		return "取消关注成功"
	} else {
		return "基金信息不存在"
	}
}

func (f *FundApi) AllFund() {
	defer func() {
		if r := recover(); r != nil {
			logger.SugaredLogger.Debugf("AllFund panic recovered: %v", r)
		}
	}()

	response, err := f.client.SetTimeout(time.Duration(f.config.CrawlTimeOut)*time.Second).R().
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/114.0.0.0 Safari/537.36").
		Get("https://fund.eastmoney.com/allfund.html")
	if err != nil {
		return
	}
	//中文编码
	htmlContent := GB18030ToUTF8(response.Body())

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		return
	}
	cnt := 0
	doc.Find("ul.num_right li").Each(func(i int, s *goquery.Selection) {
		text := strutil.SplitEx(s.Text(), "|", true)
		if len(text) > 0 {
			cnt++
			name := text[0]
			str := strutil.SplitAndTrim(name, "）", "（", "）")
			//logger.SugaredLogger.Infof("%d,基金信息 code:%s,name:%s", cnt, str[0], str[1])
			//go f.CrawlFundBasic(str[0])
			fund := &FundBasic{
				Code: str[0],
				Name: str[1],
			}
			count := int64(0)
			db.Dao.Model(fund).Where("code=?", fund.Code).Count(&count)
			if count == 0 {
				db.Dao.Create(fund)
			}

		}
	})

}

type FundNetUnitValue struct {
	Fundcode string `json:"fundcode"`
	Name     string `json:"name"`
	Jzrq     string `json:"jzrq"`
	Dwjz     string `json:"dwjz"`
	Gsz      string `json:"gsz"`
	Gszzl    string `json:"gszzl"`
	Gztime   string `json:"gztime"`
}

// CrawlFundNetEstimatedUnit 爬取净值估算值
func (f *FundApi) CrawlFundNetEstimatedUnit(code string) {
	var fundNetUnitValue FundNetUnitValue
	response, err := f.client.SetTimeout(time.Duration(f.config.CrawlTimeOut)*time.Second).R().
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/114.0.0.0 Safari/537.36").
		SetHeader("Referer", "https://fund.eastmoney.com/").
		SetQueryParams(map[string]string{"rt": strconv.FormatInt(time.Now().UnixMilli(), 10)}).
		Get(fmt.Sprintf("https://fundgz.1234567.com.cn/js/%s.js", code))
	if err != nil {
		logger.SugaredLogger.Errorf("err:%s", err.Error())
		return
	}
	if response.StatusCode() == 200 {
		htmlContent := string(response.Body())
		//logger.SugaredLogger.Infof("htmlContent:%s", htmlContent)
		if strings.Contains(htmlContent, "jsonpgz") {
			htmlContent = strutil.Trim(htmlContent, "jsonpgz(", ");")
			htmlContent = strutil.Trim(htmlContent, ");")
			//logger.SugaredLogger.Infof("基金净值信息:%s", htmlContent)
			err := json.Unmarshal([]byte(htmlContent), &fundNetUnitValue)
			if err != nil {
				//logger.SugaredLogger.Errorf("json.Unmarshal error:%s", err.Error())
				return
			}
			fund := &FollowedFund{
				Code:             fundNetUnitValue.Fundcode,
				Name:             fundNetUnitValue.Name,
				NetEstimatedTime: fundNetUnitValue.Gztime,
			}
			netEstimatedUnit, err := convertor.ToFloat(fundNetUnitValue.Gsz)
			if err == nil {
				fund.NetEstimatedUnit = &netEstimatedUnit
			}
			db.Dao.Model(fund).Where("code=?", fund.Code).Updates(fund)
		}
	}
}

// CrawlFundNetUnitValue 爬取净值
func (f *FundApi) CrawlFundNetUnitValue(code string) {
	//	var fundNetUnitValue FundNetUnitValue
	url := fmt.Sprintf("http://hq.sinajs.cn/rn=%d&list=f_%s", time.Now().UnixMilli(), code)
	//logger.SugaredLogger.Infof("url:%s", url)
	response, err := f.client.SetTimeout(time.Duration(f.config.CrawlTimeOut)*time.Second).R().
		SetHeader("Host", "hq.sinajs.cn").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36 Edg/119.0.0.0").
		SetHeader("Referer", "https://finance.sina.com.cn").
		Get(url)
	if err != nil {
		logger.SugaredLogger.Errorf("err:%s", err.Error())
		return
	}
	if response.StatusCode() == 200 {
		data := string(GB18030ToUTF8(response.Body()))
		//logger.SugaredLogger.Infof("data:%s", data)
		datas := strutil.SplitAndTrim(data, "=", "\"")
		if len(datas) >= 2 {
			//codex := strings.Split(datas[0], "hq_str_f_")[1]
			parts := strutil.SplitAndTrim(datas[1], ",", "\"")
			//logger.SugaredLogger.Infof("parts:%s", parts)
			val, err := convertor.ToFloat(parts[1])
			if err != nil {
				logger.SugaredLogger.Errorf("err:%s", err.Error())
				return
			}
			fund := &FollowedFund{
				Name:             parts[0],
				Code:             code,
				NetUnitValue:     &val,
				NetUnitValueDate: parts[4],
			}
			db.Dao.Model(fund).Where("code=?", fund.Code).Updates(fund)
		}

	}
}
