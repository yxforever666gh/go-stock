package data

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	"go-stock/backend/util"
	"io"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/coocood/freecache"
	"github.com/duke-git/lancet/v2/convertor"
	"github.com/duke-git/lancet/v2/strutil"
	"github.com/go-resty/resty/v2"
	"github.com/robertkrimen/otto"
	"github.com/samber/lo"
	"github.com/tidwall/gjson"
	"gorm.io/gorm"
)

// @Author spark
// @Date 2025/4/23 14:54
// @Desc
// -----------------------------------------------------------------------------------
type MarketNewsApi struct {
}

type NewsWindowStatus string

const (
	NewsWindowStatusOK     NewsWindowStatus = "ok"
	NewsWindowStatusEmpty  NewsWindowStatus = "empty"
	NewsWindowStatusFailed NewsWindowStatus = "failed"
	NewsWindowStatusStale  NewsWindowStatus = "stale"

	defaultNewsWindowLimit = 200
	maximumNewsWindowLimit = 1000

	marketNewsFetchKeyCLSTelegraphAPI = "cls_telegraph_api"
	marketNewsFetchKeyCLSTelegraphWeb = "cls_telegraph_web"
	marketNewsFetchKeySinaLive        = "sina_live_news"
	marketNewsFetchKeyTradingView     = "tradingview_news"

	marketNewsSourceCLSTelegraph = "财联社电报"
	marketNewsSourceSina         = "新浪财经"
	marketNewsSourceTradingView  = "外媒"
)

// NewsWindowResult describes an explicit event-time window read. Sources is
// the normalized source filter; an empty slice means all persisted sources.
type NewsWindowResult struct {
	Items   []*models.Telegraph `json:"items"`
	Status  NewsWindowStatus    `json:"status"`
	Sources []string            `json:"sources"`
	From    time.Time           `json:"from"`
	To      time.Time           `json:"to"`
	Warning string              `json:"warning,omitempty"`
}

type marketNewsFetchMeta struct {
	NetworkPath  string
	FallbackUsed bool
	Source       string
	AttemptedAt  time.Time
	CompletedAt  time.Time
	Succeeded    bool
	Completed    bool
	Error        string
	sequence     uint64
}

type marketNewsFetchOutcome struct {
	body        []byte
	networkPath string
}

type marketNewsFetchAttempt struct {
	label  string
	client *resty.Client
}

var (
	marketNewsFetchMetaMu       sync.RWMutex
	marketNewsFetchMetaBySource = map[string]marketNewsFetchMeta{}
	marketNewsFetchSequence     uint64
	sinaJSONPEnvelopeRegexp     = regexp.MustCompile(`(?s)^try\{callback\((.*)\);\}catch\(e\)\{\};?$`)
	sinaJSONPCallbackRegexp     = regexp.MustCompile(`(?s)^callback\((.*)\)$`)
)

func NewMarketNewsApi() *MarketNewsApi {
	return &MarketNewsApi{}
}

func marketNewsSetFetchMeta(source string, meta marketNewsFetchMeta) {
	source = strings.TrimSpace(source)
	if source == "" {
		return
	}
	marketNewsFetchMetaMu.Lock()
	defer marketNewsFetchMetaMu.Unlock()
	marketNewsFetchMetaBySource[source] = meta
}

func GetMarketNewsFetchMeta(source string) map[string]any {
	source = strings.TrimSpace(source)
	marketNewsFetchMetaMu.RLock()
	defer marketNewsFetchMetaMu.RUnlock()
	meta, ok := marketNewsFetchMetaBySource[source]
	if !ok {
		return map[string]any{}
	}
	result := map[string]any{}
	if meta.NetworkPath != "" {
		result["networkPath"] = meta.NetworkPath
	}
	result["fallbackUsed"] = meta.FallbackUsed
	if meta.Source != "" {
		result["source"] = meta.Source
	}
	if !meta.AttemptedAt.IsZero() {
		result["attemptedAt"] = meta.AttemptedAt.Format(time.RFC3339Nano)
	}
	if !meta.CompletedAt.IsZero() {
		result["completedAt"] = meta.CompletedAt.Format(time.RFC3339Nano)
	}
	if meta.Completed {
		if meta.Succeeded {
			result["status"] = "success"
		} else {
			result["status"] = "failed"
		}
	} else if !meta.AttemptedAt.IsZero() {
		result["status"] = "in_progress"
	}
	if meta.Error != "" {
		result["error"] = meta.Error
	}
	return result
}

func marketNewsBeginFetch(key, source string) uint64 {
	key = strings.TrimSpace(key)
	source = strings.TrimSpace(source)
	if key == "" || source == "" {
		return 0
	}
	marketNewsFetchMetaMu.Lock()
	defer marketNewsFetchMetaMu.Unlock()
	marketNewsFetchSequence++
	sequence := marketNewsFetchSequence
	marketNewsFetchMetaBySource[key] = marketNewsFetchMeta{
		Source:      source,
		AttemptedAt: time.Now(),
		sequence:    sequence,
	}
	return sequence
}

func marketNewsFinishFetch(key string, sequence uint64, networkPath string, fallbackUsed bool, fetchErr error) {
	key = strings.TrimSpace(key)
	if key == "" || sequence == 0 {
		return
	}
	marketNewsFetchMetaMu.Lock()
	defer marketNewsFetchMetaMu.Unlock()
	meta, exists := marketNewsFetchMetaBySource[key]
	// A slow older request must never overwrite a newer observation for the
	// same endpoint. The sequence is allocated under the same mutex at start.
	if !exists || meta.sequence != sequence {
		return
	}
	meta.NetworkPath = strings.TrimSpace(networkPath)
	meta.FallbackUsed = fallbackUsed
	meta.CompletedAt = time.Now()
	meta.Completed = true
	meta.Succeeded = fetchErr == nil
	if fetchErr != nil {
		meta.Error = strings.TrimSpace(fetchErr.Error())
	} else {
		meta.Error = ""
	}
	marketNewsFetchMetaBySource[key] = meta
}

func marketNewsFetchURL(url string, timeout time.Duration, configure func(*resty.Request)) (*marketNewsFetchOutcome, error) {
	attempts := []marketNewsFetchAttempt{
		{label: "direct", client: newNoProxyRestyClient()},
	}
	if proxyClient, ok := newSettingsProxyRestyClientIfConfigured(); ok {
		attempts = append(attempts, marketNewsFetchAttempt{label: "proxy", client: proxyClient})
	}
	if len(attempts) == 0 {
		return nil, errors.New("no fetch client available")
	}
	if len(attempts) == 1 {
		body, err := marketNewsFetchDoRequest(url, timeout, attempts[0].client, configure)
		if err != nil {
			return nil, err
		}
		return &marketNewsFetchOutcome{body: body, networkPath: attempts[0].label}, nil
	}

	type result struct {
		outcome *marketNewsFetchOutcome
		err     error
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan result, len(attempts))
	for _, attempt := range attempts {
		attempt := attempt
		go func() {
			body, err := marketNewsFetchDoRequestWithContext(ctx, url, timeout, attempt.client, configure)
			if err != nil {
				ch <- result{err: fmt.Errorf("%s: %w", attempt.label, err)}
				return
			}
			ch <- result{outcome: &marketNewsFetchOutcome{body: body, networkPath: attempt.label}}
		}()
	}
	var errs []string
	for range attempts {
		res := <-ch
		if res.err == nil && res.outcome != nil {
			cancel()
			return res.outcome, nil
		}
		if res.err != nil {
			errs = append(errs, res.err.Error())
		}
	}
	if len(errs) == 0 {
		return nil, errors.New("all news fetch attempts failed")
	}
	return nil, fmt.Errorf("all news fetch attempts failed: %s", strings.Join(errs, "; "))
}

func marketNewsFetchDoRequest(url string, timeout time.Duration, client *resty.Client, configure func(*resty.Request)) ([]byte, error) {
	return marketNewsFetchDoRequestWithContext(context.Background(), url, timeout, client, configure)
}

func marketNewsFetchDoRequestWithContext(ctx context.Context, url string, timeout time.Duration, client *resty.Client, configure func(*resty.Request)) ([]byte, error) {
	if client == nil {
		return nil, errors.New("nil client")
	}
	req := client.SetTimeout(timeout).R().SetContext(ctx)
	if configure != nil {
		configure(req)
	}
	resp, err := req.Get(url)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, errors.New("empty response")
	}
	if resp.StatusCode() >= 400 {
		return nil, fmt.Errorf("http %d", resp.StatusCode())
	}
	body := resp.Body()
	if len(body) == 0 {
		return nil, io.EOF
	}
	return append([]byte(nil), body...), nil
}

func stringsBuilderUserAgent() string {
	return "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/117.0.0.0 Safari/537.36 Edg/117.0.2045.60"
}

func safeString(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case json.Number:
		return strings.TrimSpace(v.String())
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	case float64:
		return strings.TrimSpace(strconv.FormatFloat(v, 'f', -1, 64))
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case uint64:
		return strconv.FormatUint(v, 10)
	default:
		return strings.TrimSpace(convertor.ToString(v))
	}
}

func safeInt64(value any) int64 {
	switch v := value.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	default:
		n, _ := convertor.ToInt(value)
		return int64(n)
	}
}

func safeMap(value any) map[string]any {
	if value == nil {
		return nil
	}
	if m, ok := value.(map[string]any); ok {
		return m
	}
	return nil
}

func safeSlice(value any) []any {
	if value == nil {
		return nil
	}
	if items, ok := value.([]any); ok {
		return items
	}
	return nil
}

func normalizeSinaJSONPBody(body []byte) ([]byte, error) {
	raw := strings.TrimSpace(string(body))
	if raw == "" {
		return nil, errors.New("empty body")
	}
	if matches := sinaJSONPEnvelopeRegexp.FindStringSubmatch(raw); len(matches) == 2 {
		return []byte(matches[1]), nil
	}
	if matches := sinaJSONPCallbackRegexp.FindStringSubmatch(raw); len(matches) == 2 {
		return []byte(matches[1]), nil
	}
	return nil, errors.New("unexpected jsonp payload")
}

func (m MarketNewsApi) TelegraphList(crawlTimeOut int64) *[]models.Telegraph {
	var telegraphs []models.Telegraph
	fetchSequence := marketNewsBeginFetch(marketNewsFetchKeyCLSTelegraphAPI, marketNewsSourceCLSTelegraph)
	fetchErr := errors.New("CLS telegraph API fetch did not complete")
	networkPath := ""
	defer func() {
		marketNewsFinishFetch(marketNewsFetchKeyCLSTelegraphAPI, fetchSequence, networkPath, networkPath != "" && networkPath != "direct", fetchErr)
	}()
	url := "https://www.cls.cn/nodeapi/telegraphList"
	outcome, err := marketNewsFetchURL(url, time.Duration(crawlTimeOut)*time.Second, func(req *resty.Request) {
		req.SetHeader("Referer", "https://www.cls.cn/").
			SetHeader("User-Agent", stringsBuilderUserAgent())
	})
	if err != nil {
		logger.SugaredLogger.Errorf("TelegraphList err:%v", err)
		fetchErr = err
		return m.GetNewTelegraph(crawlTimeOut)
	}
	networkPath = outcome.networkPath

	res := map[string]any{}
	if err = json.Unmarshal(outcome.body, &res); err != nil {
		logger.SugaredLogger.Errorf("TelegraphList unmarshal err:%v", err)
		fetchErr = fmt.Errorf("decode CLS telegraph API response: %w", err)
		return &telegraphs
	}

	if v, _ := convertor.ToInt(res["error"]); v == 0 {
		data := safeMap(res["data"])
		if data == nil {
			fetchErr = errors.New("CLS telegraph API response is missing data")
			return m.GetNewTelegraph(30)
		}
		rollValue, exists := data["roll_data"]
		rollData, rollDataOK := rollValue.([]any)
		if !exists || !rollDataOK {
			fetchErr = errors.New("CLS telegraph API response is missing data.roll_data")
			return m.GetNewTelegraph(30)
		}
		if len(rollData) == 0 {
			fetchErr = errors.New("CLS telegraph API returned an empty data.roll_data")
			return m.GetNewTelegraph(crawlTimeOut)
		}
		for _, v := range rollData {
			news := safeMap(v)
			if news == nil {
				continue
			}
			content := safeString(news["content"])
			if content == "" {
				continue
			}
			ctime := safeInt64(news["ctime"])
			dataTime := time.Unix(ctime, 0).Local()
			telegraph := models.Telegraph{
				Title:           safeString(news["title"]),
				Content:         content,
				Time:            dataTime.Format("15:04:05"),
				DataTime:        &dataTime,
				Url:             safeString(news["shareurl"]),
				Source:          marketNewsSourceCLSTelegraph,
				IsRed:           safeString(news["level"]) != "C",
				SentimentResult: AnalyzeSentiment(content).Description,
			}
			cnt := int64(0)
			if telegraph.Title == "" {
				db.Dao.Model(telegraph).Where("content=?", telegraph.Content).Count(&cnt)
			} else {
				db.Dao.Model(telegraph).Where("title=?", telegraph.Title).Count(&cnt)
			}
			if cnt > 0 {
				continue
			}
			telegraphs = append(telegraphs, telegraph)
			db.Dao.Model(&models.Telegraph{}).Create(&telegraph)
			logger.SugaredLogger.Debugf("telegraph: %+v", &telegraph)
			subjects := safeSlice(news["subjects"])
			if len(subjects) == 0 {
				continue
			}
			for _, subject := range subjects {
				name := safeString(safeMap(subject)["subject_name"])
				if name == "" {
					continue
				}
				tag := &models.Tags{
					Name: name,
					Type: "subject",
				}
				db.Dao.Model(tag).Where("name=? and type=?", name, "subject").FirstOrCreate(&tag)
				db.Dao.Model(models.TelegraphTags{}).Where("telegraph_id=? and tag_id=?", telegraph.ID, tag.ID).FirstOrCreate(&models.TelegraphTags{
					TelegraphId: telegraph.ID,
					TagId:       tag.ID,
				})
			}

		}
		//db.Dao.Model(&models.Telegraph{}).Create(&telegraphs)
		//logger.SugaredLogger.Debugf("telegraphs: %+v", &telegraphs)
	} else {
		fetchErr = fmt.Errorf("CLS telegraph API returned error code %d", v)
		return &telegraphs
	}

	fetchErr = nil
	return &telegraphs
}

// RefreshResearchNews refreshes the two fast, text-complete market-news feeds
// before a research window is read. The calls are bounded by their provider
// timeouts and run concurrently so a broken CLS endpoint cannot prevent Sina
// from keeping the persisted event-time window current.
func (m MarketNewsApi) RefreshResearchNews(ctx context.Context, timeout time.Duration) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	timeoutSeconds := int64(math.Ceil(timeout.Seconds()))
	if timeoutSeconds < 1 {
		timeoutSeconds = 1
	}
	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		m.TelegraphList(timeoutSeconds)
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		m.GetSinaNews(uint(timeoutSeconds))
	}()
	for completed := 0; completed < cap(done); completed++ {
		select {
		case <-ctx.Done():
			return
		case <-done:
		}
	}
}

func (m MarketNewsApi) GetNewTelegraph(crawlTimeOut int64) *[]models.Telegraph {
	url := "https://www.cls.cn/telegraph"
	var telegraphs []models.Telegraph
	fetchSequence := marketNewsBeginFetch(marketNewsFetchKeyCLSTelegraphWeb, marketNewsSourceCLSTelegraph)
	fetchErr := errors.New("CLS telegraph web fetch did not complete")
	networkPath := ""
	defer func() {
		marketNewsFinishFetch(marketNewsFetchKeyCLSTelegraphWeb, fetchSequence, networkPath, networkPath != "" && networkPath != "direct", fetchErr)
	}()
	outcome, err := marketNewsFetchURL(url, time.Duration(crawlTimeOut)*time.Second, func(req *resty.Request) {
		req.SetHeader("Referer", "https://www.cls.cn/").
			SetHeader("User-Agent", stringsBuilderUserAgent())
	})
	if err != nil {
		logger.SugaredLogger.Errorf("GetNewTelegraph err:%v", err)
		fetchErr = err
		return &telegraphs
	}
	networkPath = outcome.networkPath
	document, err := goquery.NewDocumentFromReader(strings.NewReader(string(outcome.body)))
	if err != nil {
		logger.SugaredLogger.Errorf("GetNewTelegraph parse err:%v", err)
		fetchErr = fmt.Errorf("parse CLS telegraph page: %w", err)
		return &telegraphs
	}

	telegraphNodes := document.Find(".telegraph-content-box")
	if telegraphNodes.Length() == 0 {
		fetchErr = errors.New("CLS telegraph page is missing telegraph content nodes")
		return &telegraphs
	}
	telegraphNodes.Each(func(i int, selection *goquery.Selection) {
		telegraph := models.Telegraph{Source: marketNewsSourceCLSTelegraph}
		spans := selection.Find("span")
		if spans.Length() == 2 {
			telegraph.Time = strings.TrimSpace(spans.First().Text())
			telegraph.Content = strings.TrimSpace(spans.Last().Text())
			if spans.Last().HasClass("c-de0422") {
				telegraph.IsRed = true
			}
		}

		labels := selection.Find("div a.label-item")
		labels.Each(func(i int, selection *goquery.Selection) {
			if selection.HasClass("link-label-item") {
				telegraph.Url = selection.AttrOr("href", "")
			} else {
				tag := &models.Tags{
					Name: selection.Text(),
					Type: "subject",
				}
				db.Dao.Model(tag).Where("name=? and type=?", selection.Text(), "subject").FirstOrCreate(&tag)
				telegraph.SubjectTags = append(telegraph.SubjectTags, selection.Text())
			}
		})
		stocks := selection.Find("div.telegraph-stock-plate-box a")
		stocks.Each(func(i int, selection *goquery.Selection) {
			telegraph.StocksTags = append(telegraph.StocksTags, selection.Text())
		})

		if telegraph.Content != "" {
			telegraph.SentimentResult = AnalyzeSentiment(telegraph.Content).Description
			cnt := int64(0)
			db.Dao.Model(telegraph).Where("time=? and content=?", telegraph.Time, telegraph.Content).Count(&cnt)
			if cnt == 0 {
				db.Dao.Create(&telegraph)
				telegraphs = append(telegraphs, telegraph)
				for _, tag := range telegraph.SubjectTags {
					tagInfo := &models.Tags{}
					db.Dao.Model(models.Tags{}).Where("name=? and type=?", tag, "subject").First(&tagInfo)
					if tagInfo.ID > 0 {
						db.Dao.Model(models.TelegraphTags{}).Where("telegraph_id=? and tag_id=?", telegraph.ID, tagInfo.ID).FirstOrCreate(&models.TelegraphTags{
							TelegraphId: telegraph.ID,
							TagId:       tagInfo.ID,
						})
					}
				}
			}

		}
	})
	fetchErr = nil
	return &telegraphs
}
func (m MarketNewsApi) GetNewsList(source string, limit int) *[]*models.Telegraph {
	news := &[]*models.Telegraph{}
	if source != "" {
		db.Dao.Model(news).Preload("TelegraphTags").Where("source=?", source).Order("data_time desc,time desc").Limit(limit).Find(news)
	} else {
		db.Dao.Model(news).Preload("TelegraphTags").Order("data_time desc,time desc").Limit(limit).Find(news)
	}
	for _, item := range *news {
		tags := &[]models.Tags{}
		db.Dao.Model(&models.Tags{}).Where("id in ?", lo.Map(item.TelegraphTags, func(item models.TelegraphTags, index int) uint {
			return item.TagId
		})).Find(&tags)
		tagNames := lo.Map(*tags, func(item models.Tags, index int) string {
			return item.Name
		})
		item.SubjectTags = tagNames
		logger.SugaredLogger.Infof("tagNames %v ，SubjectTags：%s", tagNames, item.SubjectTags)
	}
	return news
}
func (m MarketNewsApi) GetNewsList2(source string, limit int) *[]*models.Telegraph {
	NewMarketNewsApi().TelegraphList(30)
	news := &[]*models.Telegraph{}
	if source != "" {
		db.Dao.Model(news).Preload("TelegraphTags").Where("source=?", source).Order("data_time desc,is_red desc").Limit(limit).Find(news)
	} else {
		db.Dao.Model(news).Preload("TelegraphTags").Order("data_time desc,is_red desc").Limit(limit).Find(news)
	}
	for _, item := range *news {
		tags := &[]models.Tags{}
		db.Dao.Model(&models.Tags{}).Where("id in ?", lo.Map(item.TelegraphTags, func(item models.TelegraphTags, index int) uint {
			return item.TagId
		})).Find(&tags)
		tagNames := lo.Map(*tags, func(item models.Tags, index int) string {
			return item.Name
		})
		item.SubjectTags = tagNames
		logger.SugaredLogger.Infof("tagNames %v ，SubjectTags：%s", tagNames, item.SubjectTags)
	}
	return news
}

func (m MarketNewsApi) GetTelegraphList(source string) *[]*models.Telegraph {
	news := &[]*models.Telegraph{}
	if source != "" {
		db.Dao.Model(news).Preload("TelegraphTags").Where("source=?", source).Order("data_time desc,time desc").Limit(50).Find(news)
	} else {
		db.Dao.Model(news).Preload("TelegraphTags").Order("data_time desc,time desc").Limit(50).Find(news)
	}
	for _, item := range *news {
		tags := &[]models.Tags{}
		db.Dao.Model(&models.Tags{}).Where("id in ?", lo.Map(item.TelegraphTags, func(item models.TelegraphTags, index int) uint {
			return item.TagId
		})).Find(&tags)
		tagNames := lo.Map(*tags, func(item models.Tags, index int) string {
			return item.Name
		})
		item.SubjectTags = tagNames
		logger.SugaredLogger.Infof("tagNames %v ，SubjectTags：%s", tagNames, item.SubjectTags)
	}
	return news
}
func (m MarketNewsApi) GetTelegraphListWithPaging(source string, page, pageSize int) *[]*models.Telegraph {
	// 计算偏移量
	offset := (page - 1) * pageSize

	news := &[]*models.Telegraph{}
	if source != "" {
		db.Dao.Model(news).Preload("TelegraphTags").Where("source=?", source).Order("data_time desc,time desc").Limit(pageSize).Offset(offset).Find(news)
	} else {
		db.Dao.Model(news).Preload("TelegraphTags").Order("data_time desc,time desc").Limit(pageSize).Offset(offset).Find(news)
	}
	for _, item := range *news {
		tags := &[]models.Tags{}
		db.Dao.Model(&models.Tags{}).Where("id in ?", lo.Map(item.TelegraphTags, func(item models.TelegraphTags, index int) uint {
			return item.TagId
		})).Find(&tags)
		tagNames := lo.Map(*tags, func(item models.Tags, index int) string {
			return item.Name
		})
		item.SubjectTags = tagNames
		logger.SugaredLogger.Infof("tagNames %v ，SubjectTags：%s", tagNames, item.SubjectTags)
	}
	return news
}

func (m MarketNewsApi) GetSinaNews(crawlTimeOut uint) *[]models.Telegraph {
	news := &[]models.Telegraph{}
	fetchSequence := marketNewsBeginFetch(marketNewsFetchKeySinaLive, marketNewsSourceSina)
	fetchErr := errors.New("Sina live news fetch did not complete")
	networkPath := ""
	defer func() {
		marketNewsFinishFetch(marketNewsFetchKeySinaLive, fetchSequence, networkPath, networkPath != "" && networkPath != "direct", fetchErr)
	}()
	url := "https://zhibo.sina.com.cn/api/zhibo/feed?callback=callback&page=1&page_size=20&zhibo_id=152&tag_id=0&dire=f&dpc=1&pagesize=20&id=4161089&type=0&_=" + strconv.FormatInt(time.Now().Unix(), 10)
	outcome, err := marketNewsFetchURL(url, time.Duration(crawlTimeOut)*time.Second, func(req *resty.Request) {
		req.SetHeader("Referer", "https://finance.sina.com.cn").
			SetHeader("User-Agent", stringsBuilderUserAgent())
	})
	if err != nil {
		logger.SugaredLogger.Errorf("GetSinaNews err:%v", err)
		fetchErr = err
		return news
	}
	networkPath = outcome.networkPath
	jsonBody, err := normalizeSinaJSONPBody(outcome.body)
	if err != nil {
		logger.SugaredLogger.Errorf("GetSinaNews normalize body err:%v", err)
		fetchErr = fmt.Errorf("normalize Sina live news response: %w", err)
		return news
	}
	payload := map[string]any{}
	err = json.Unmarshal(jsonBody, &payload)
	if err != nil {
		logger.SugaredLogger.Errorf("GetSinaNews json.Unmarshal err:%v", err)
		fetchErr = fmt.Errorf("decode Sina live news response: %w", err)
		return news
	}
	result := safeMap(payload["result"])
	resultPayload := safeMap(result["data"])
	resultData := safeMap(resultPayload["feed"])
	if result == nil || resultPayload == nil || resultData == nil {
		fetchErr = errors.New("Sina live news response is missing result.data.feed")
		return news
	}
	listValue, exists := resultData["list"]
	list, listOK := listValue.([]any)
	if !exists || !listOK {
		fetchErr = errors.New("Sina live news response is missing feed.list")
		return news
	}
	var telegraphs []models.Telegraph
	for _, item := range list {
		data := safeMap(item)
		if data == nil {
			continue
		}
		content := safeString(data["rich_text"])
		createTime := safeString(data["create_time"])
		if content == "" || createTime == "" {
			continue
		}
		telegraph := models.Telegraph{Source: marketNewsSourceSina}
		telegraph.Content = content
		telegraph.Title = strutil.SubInBetween(content, "【", "】")
		parts := strings.Split(createTime, " ")
		if len(parts) >= 2 {
			telegraph.Time = parts[1]
		}
		dataTime, parseErr := time.ParseInLocation("2006-01-02 15:04:05", createTime, time.Local)
		if parseErr == nil {
			telegraph.DataTime = &dataTime
		}
		for _, tagItem := range safeSlice(data["tag"]) {
			name := safeString(safeMap(tagItem)["name"])
			if name == "" {
				continue
			}
			tag := &models.Tags{
				Name: name,
				Type: "sina_subject",
			}
			db.Dao.Model(tag).Where("name=? and type=?", name, "sina_subject").FirstOrCreate(&tag)
			telegraph.SubjectTags = append(telegraph.SubjectTags, name)
		}
		if _, ok := lo.Find(telegraph.SubjectTags, func(item string) bool { return item == "焦点" }); ok {
			telegraph.IsRed = true
		}
		logger.SugaredLogger.Infof("telegraph.SubjectTags:%v %s", telegraph.SubjectTags, telegraph.Content)

		if telegraph.Content != "" {
			telegraph.SentimentResult = AnalyzeSentiment(telegraph.Content).Description
			cnt := int64(0)
			if telegraph.Title == "" {
				db.Dao.Model(telegraph).Where("content=?", telegraph.Content).Count(&cnt)
			} else {
				db.Dao.Model(telegraph).Where("title=?", telegraph.Title).Count(&cnt)
			}
			if cnt == 0 {
				db.Dao.Create(&telegraph)
				telegraphs = append(telegraphs, telegraph)
				for _, tag := range telegraph.SubjectTags {
					tagInfo := &models.Tags{}
					db.Dao.Model(models.Tags{}).Where("name=? and type=?", tag, "sina_subject").First(&tagInfo)
					if tagInfo.ID > 0 {
						db.Dao.Model(models.TelegraphTags{}).Where("telegraph_id=? and tag_id=?", telegraph.ID, tagInfo.ID).FirstOrCreate(&models.TelegraphTags{
							TelegraphId: telegraph.ID,
							TagId:       tagInfo.ID,
						})
					}
				}
			}
		}
	}
	fetchErr = nil
	if len(telegraphs) > 0 {
		return &telegraphs
	}
	return news
}

func (m MarketNewsApi) GlobalStockIndexes(crawlTimeOut uint) map[string]any {
	empty := map[string]any{
		"common":  []any{},
		"america": []any{},
		"europe":  []any{},
		"asia":    []any{},
		"other":   []any{},
	}
	response, err := newFetchRestyClient().SetTimeout(time.Duration(crawlTimeOut)*time.Second).R().
		SetHeader("Referer", "https://stockapp.finance.qq.com/mstats").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/117.0.0.0 Safari/537.36 Edg/117.0.2045.60").
		Get("https://proxy.finance.qq.com/ifzqgtimg/appstock/app/rank/indexRankDetail2")
	if err != nil || response == nil {
		if err != nil {
			logger.SugaredLogger.Errorf("GlobalStockIndexes err:%v", err)
		}
		return empty
	}
	js := string(response.Body())
	res := make(map[string]any)
	if err = json.Unmarshal([]byte(js), &res); err != nil {
		logger.SugaredLogger.Errorf("GlobalStockIndexes json.Unmarshal err:%v", err)
		return empty
	}
	dataMap, ok := res["data"].(map[string]any)
	if !ok || dataMap == nil {
		return empty
	}
	for key, fallback := range empty {
		if _, exists := dataMap[key]; !exists || dataMap[key] == nil {
			dataMap[key] = fallback
		}
	}
	return dataMap
}

func (m MarketNewsApi) GetIndustryRank(sort string, cnt int) map[string]any {
	url := fmt.Sprintf("https://proxy.finance.qq.com/ifzqgtimg/appstock/app/mktHs/rank?l=%d&p=1&t=01/averatio&ordertype=&o=%s", cnt, sort)
	response, err := newFetchRestyClient().SetTimeout(time.Duration(5)*time.Second).R().
		SetHeader("Referer", "https://stockapp.finance.qq.com/").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/117.0.0.0 Safari/537.36 Edg/117.0.2045.60").
		Get(url)
	if err != nil || response == nil {
		if err != nil {
			logger.SugaredLogger.Errorf("GetIndustryRank err:%v", err)
		}
		return map[string]any{"data": []any{}}
	}
	js := string(response.Body())
	res := make(map[string]any)
	if err = json.Unmarshal([]byte(js), &res); err != nil {
		logger.SugaredLogger.Errorf("GetIndustryRank json.Unmarshal err:%v", err)
		return map[string]any{"data": []any{}}
	}
	if _, ok := res["data"].([]any); !ok {
		res["data"] = []any{}
	}
	return res
}

func (m MarketNewsApi) GetIndustryMoneyRankSina(fenlei, sort string) []map[string]any {
	url := fmt.Sprintf("https://vip.stock.finance.sina.com.cn/quotes_service/api/json_v2.php/MoneyFlow.ssl_bkzj_bk?page=1&num=20&sort=%s&asc=0&fenlei=%s", sort, fenlei)

	response, _ := newFetchRestyClient().SetTimeout(time.Duration(5)*time.Second).R().
		SetHeader("Host", "vip.stock.finance.sina.com.cn").
		SetHeader("Referer", "https://finance.sina.com.cn").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/117.0.0.0 Safari/537.36 Edg/117.0.2045.60").
		Get(url)
	js := string(response.Body())
	res := &[]map[string]any{}
	err := json.Unmarshal([]byte(js), &res)
	if err != nil {
		logger.SugaredLogger.Error(err)
		return *res
	}
	return *res
}

func (m MarketNewsApi) GetMoneyRankSina(sort string) []map[string]any {
	if sort == "" {
		sort = "netamount"
	}
	url := fmt.Sprintf("https://vip.stock.finance.sina.com.cn/quotes_service/api/json_v2.php/MoneyFlow.ssl_bkzj_ssggzj?page=1&num=20&sort=%s&asc=0&bankuai=&shichang=", sort)
	response, _ := newFetchRestyClient().SetTimeout(time.Duration(5)*time.Second).R().
		SetHeader("Host", "vip.stock.finance.sina.com.cn").
		SetHeader("Referer", "https://finance.sina.com.cn").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/117.0.0.0 Safari/537.36 Edg/117.0.2045.60").
		Get(url)
	js := string(response.Body())
	res := &[]map[string]any{}
	err := json.Unmarshal([]byte(js), &res)
	if err != nil {
		logger.SugaredLogger.Error(err)
		return *res
	}
	return *res
}

func (m MarketNewsApi) GetStockMoneyTrendByDay(stockCode string, days int) []map[string]any {
	url := fmt.Sprintf("http://vip.stock.finance.sina.com.cn/quotes_service/api/json_v2.php/MoneyFlow.ssl_qsfx_zjlrqs?page=1&num=%d&sort=opendate&asc=0&daima=%s", days, stockCode)

	response, _ := newFetchRestyClient().SetTimeout(time.Duration(5)*time.Second).R().
		SetHeader("Host", "vip.stock.finance.sina.com.cn").
		SetHeader("Referer", "https://finance.sina.com.cn").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/117.0.0.0 Safari/537.36 Edg/117.0.2045.60").Get(url)
	js := string(response.Body())
	res := &[]map[string]any{}
	err := json.Unmarshal([]byte(js), &res)
	if err != nil {
		logger.SugaredLogger.Error(err)
		return *res
	}
	return *res

}

func (m MarketNewsApi) TopStocksRankingList(date string) {
	url := fmt.Sprintf("http://vip.stock.finance.sina.com.cn/q/go.php/vInvestConsult/kind/lhb/index.phtml?tradedate=%s", date)
	response, _ := newFetchRestyClient().SetTimeout(time.Duration(5)*time.Second).R().
		SetHeader("Host", "vip.stock.finance.sina.com.cn").
		SetHeader("Referer", "https://finance.sina.com.cn").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/117.0.0.0 Safari/537.36 Edg/117.0.2045.60").Get(url)

	html, _ := convertor.GbkToUtf8(response.Body())
	//logger.SugaredLogger.Infof("html:%s", html)
	document, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return
	}
	document.Find("table.list_table").Each(func(i int, s *goquery.Selection) {
		title := strutil.Trim(s.Find("tr:first-child").First().Text())
		logger.SugaredLogger.Infof("title:%s", title)
		s.Find("tr:not(:first-child)").Each(func(i int, s *goquery.Selection) {
			logger.SugaredLogger.Infof("s:%s", strutil.RemoveNonPrintable(s.Text()))
		})
	})

}

func (m MarketNewsApi) LongTiger(date string) *[]models.LongTigerRankData {
	ranks := &[]models.LongTigerRankData{}
	url := "https://datacenter-web.eastmoney.com/api/data/v1/get"
	logger.SugaredLogger.Infof("url:%s", url)
	params := make(map[string]string)
	params["callback"] = "callback"
	params["sortColumns"] = "TURNOVERRATE,TRADE_DATE,SECURITY_CODE"
	params["sortTypes"] = "-1,-1,1"
	params["pageSize"] = "500"
	params["pageNumber"] = "1"
	params["reportName"] = "RPT_DAILYBILLBOARD_DETAILSNEW"
	params["columns"] = "SECURITY_CODE,SECUCODE,SECURITY_NAME_ABBR,TRADE_DATE,EXPLAIN,CLOSE_PRICE,CHANGE_RATE,BILLBOARD_NET_AMT,BILLBOARD_BUY_AMT,BILLBOARD_SELL_AMT,BILLBOARD_DEAL_AMT,ACCUM_AMOUNT,DEAL_NET_RATIO,DEAL_AMOUNT_RATIO,TURNOVERRATE,FREE_MARKET_CAP,EXPLANATION,D1_CLOSE_ADJCHRATE,D2_CLOSE_ADJCHRATE,D5_CLOSE_ADJCHRATE,D10_CLOSE_ADJCHRATE,SECURITY_TYPE_CODE"
	params["source"] = "WEB"
	params["client"] = "WEB"
	params["filter"] = fmt.Sprintf("(TRADE_DATE<='%s')(TRADE_DATE>='%s')", date, date)
	resp, err := newFetchRestyClient().SetTimeout(time.Duration(15)*time.Second).R().
		SetHeader("Host", "datacenter-web.eastmoney.com").
		SetHeader("Referer", "https://data.eastmoney.com/stock/tradedetail.html").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:140.0) Gecko/20100101 Firefox/140.0").
		SetQueryParams(params).
		Get(url)
	if err != nil {
		return ranks
	}
	js := string(resp.Body())
	logger.SugaredLogger.Infof("resp:%s", js)

	js = strutil.ReplaceWithMap(js, map[string]string{
		"callback(": "var data=",
		");":        ";",
	})
	//logger.SugaredLogger.Info(js)
	vm := otto.New()
	if _, err = vm.Run(js); err != nil {
		logger.SugaredLogger.Error(err)
		return ranks
	}
	if _, err = vm.Run("var data = JSON.stringify(data);"); err != nil {
		logger.SugaredLogger.Error(err)
		return ranks
	}
	value, _ := vm.Get("data")
	logger.SugaredLogger.Infof("resp-json:%s", value.String())
	data := gjson.Get(value.String(), "result.data")
	logger.SugaredLogger.Infof("resp:%v", data)
	err = json.Unmarshal([]byte(data.String()), ranks)
	if err != nil {
		logger.SugaredLogger.Error(err)
		return ranks
	}
	for _, rankData := range *ranks {
		temp := &models.LongTigerRankData{}
		db.Dao.Model(temp).Where(&models.LongTigerRankData{
			TRADEDATE: rankData.TRADEDATE,
			SECUCODE:  rankData.SECUCODE,
		}).First(temp)
		if temp.SECURITYTYPECODE == "" {
			db.Dao.Model(temp).Create(&rankData)
		}
	}
	return ranks
}

func (m MarketNewsApi) IndustryResearchReport(industryCode string, days int) []any {
	beginDate := time.Now().Add(-time.Duration(days) * 365 * time.Hour).Format("2006-01-02")
	endDate := time.Now().Format("2006-01-02")
	if strutil.Trim(industryCode) != "" {
		beginDate = time.Now().Add(-time.Duration(days) * 365 * time.Hour).Format("2006-01-02")
	}

	logger.SugaredLogger.Infof("IndustryResearchReport-name:%s", industryCode)
	params := map[string]string{
		"industry":     "*",
		"industryCode": industryCode,
		"beginTime":    beginDate,
		"endTime":      endDate,
		"pageNo":       "1",
		"pageSize":     "50",
		"p":            "1",
		"pageNum":      "1",
		"pageNumber":   "1",
		"qType":        "1",
	}

	url := "https://reportapi.eastmoney.com/report/list"

	logger.SugaredLogger.Infof("beginDate:%s endDate:%s", beginDate, endDate)
	resp, err := newFetchRestyClient().SetTimeout(time.Duration(15)*time.Second).R().
		SetHeader("Host", "reportapi.eastmoney.com").
		SetHeader("Origin", "https://data.eastmoney.com").
		SetHeader("Referer", "https://data.eastmoney.com/report/stock.jshtml").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:140.0) Gecko/20100101 Firefox/140.0").
		SetHeader("Content-Type", "application/json").
		SetQueryParams(params).Get(url)
	respMap := map[string]any{}

	if err != nil {
		return []any{}
	}
	if err := json.Unmarshal(resp.Body(), &respMap); err != nil {
		return []any{}
	}
	if data, ok := respMap["data"].([]any); ok {
		return data
	}
	return []any{}
}
func (m MarketNewsApi) StockResearchReport(stockCode string, days int) []any {
	return m.stockResearchReportAtTimes(stockCode, days, time.Now(), time.Now())
}

func (m MarketNewsApi) StockResearchReportAt(stockCode string, days int, now time.Time) []any {
	if now.IsZero() {
		return []any{}
	}
	return m.stockResearchReportAtTimes(stockCode, days, now, now)
}

func (m MarketNewsApi) stockResearchReportAtTimes(stockCode string, days int, endAt, beginAt time.Time) []any {
	beginDate := ""
	endDate := endAt.Format("2006-01-02")
	if strutil.ContainsAny(stockCode, []string{"."}) {
		stockCode = strings.Split(stockCode, ".")[0]
		beginDate = beginAt.Add(-time.Duration(days) * 365 * time.Hour).Format("2006-01-02")
	} else {
		stockCode = strutil.ReplaceWithMap(stockCode, map[string]string{
			"sh":  "",
			"sz":  "",
			"gb_": "",
			"us":  "",
			"us_": "",
		})
		beginDate = beginAt.Add(-time.Duration(days) * 365 * time.Hour).Format("2006-01-02")
	}

	logger.SugaredLogger.Infof("StockResearchReport-stockCode:%s", stockCode)

	type Req struct {
		BeginTime    string      `json:"beginTime"`
		EndTime      string      `json:"endTime"`
		IndustryCode string      `json:"industryCode"`
		RatingChange string      `json:"ratingChange"`
		Rating       string      `json:"rating"`
		OrgCode      interface{} `json:"orgCode"`
		Code         string      `json:"code"`
		Rcode        string      `json:"rcode"`
		PageSize     int         `json:"pageSize"`
		PageNo       int         `json:"pageNo"`
		P            int         `json:"p"`
		PageNum      int         `json:"pageNum"`
		PageNumber   int         `json:"pageNumber"`
	}

	url := "https://reportapi.eastmoney.com/report/list2"

	logger.SugaredLogger.Infof("beginDate:%s endDate:%s", beginDate, endDate)
	resp, err := newFetchRestyClient().SetTimeout(time.Duration(15)*time.Second).R().
		SetHeader("Host", "reportapi.eastmoney.com").
		SetHeader("Origin", "https://data.eastmoney.com").
		SetHeader("Referer", "https://data.eastmoney.com/report/stock.jshtml").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:140.0) Gecko/20100101 Firefox/140.0").
		SetHeader("Content-Type", "application/json").
		SetBody(&Req{
			Code:         stockCode,
			IndustryCode: "*",
			BeginTime:    beginDate,
			EndTime:      endDate,
			PageNo:       1,
			PageSize:     50,
			P:            1,
			PageNum:      1,
			PageNumber:   1,
		}).Post(url)
	respMap := map[string]any{}

	if err != nil {
		return []any{}
	}
	if err := json.Unmarshal(resp.Body(), &respMap); err != nil {
		return []any{}
	}
	if data, ok := respMap["data"].([]any); ok {
		return data
	}
	return []any{}
}

func (m MarketNewsApi) StockNotice(stock_list string) []any {
	var stockCodes []string
	for _, stockCode := range strings.Split(stock_list, ",") {
		if strutil.ContainsAny(stockCode, []string{"."}) {
			stockCode = strings.Split(stockCode, ".")[0]
			stockCodes = append(stockCodes, stockCode)
		} else {
			stockCode = strutil.ReplaceWithMap(stockCode, map[string]string{
				"sh":  "",
				"sz":  "",
				"gb_": "",
				"us":  "",
				"us_": "",
			})
			stockCodes = append(stockCodes, stockCode)
		}
	}

	url := "https://np-anotice-stock.eastmoney.com/api/security/ann?page_size=50&page_index=1&ann_type=SHA%2CCYB%2CSZA%2CBJA%2CINV&client_source=web&f_node=0&stock_list=" + strings.Join(stockCodes, ",")
	resp, err := newFetchRestyClient().SetTimeout(time.Duration(15)*time.Second).R().
		SetHeader("Host", "np-anotice-stock.eastmoney.com").
		SetHeader("Referer", "https://data.eastmoney.com/notices/hsa/5.html").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:140.0) Gecko/20100101 Firefox/140.0").
		Get(url)
	respMap := map[string]any{}

	if err != nil {
		return []any{}
	}
	if err := json.Unmarshal(resp.Body(), &respMap); err != nil {
		return []any{}
	}
	data, ok := respMap["data"].(map[string]any)
	if !ok {
		return []any{}
	}
	list, ok := data["list"].([]any)
	if !ok {
		return []any{}
	}
	return list
}

func (m MarketNewsApi) EMDictCode(code string, cache *freecache.Cache) []any {
	respMap := map[string]any{}

	d, _ := cache.Get([]byte(code))
	if d != nil {
		if err := json.Unmarshal(d, &respMap); err == nil {
			if data, ok := respMap["data"].([]any); ok {
				return data
			}
		}
		return []any{}
	}

	url := "https://reportapi.eastmoney.com/report/bk"

	params := map[string]string{
		"bkCode": code,
	}
	resp, err := newFetchRestyClient().SetTimeout(time.Duration(15)*time.Second).R().
		SetHeader("Host", "reportapi.eastmoney.com").
		SetHeader("Origin", "https://data.eastmoney.com").
		SetHeader("Referer", "https://data.eastmoney.com/report/industry.jshtml").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:140.0) Gecko/20100101 Firefox/140.0").
		SetHeader("Content-Type", "application/json").
		SetQueryParams(params).Get(url)

	if err != nil {
		return []any{}
	}
	if err := json.Unmarshal(resp.Body(), &respMap); err != nil {
		return []any{}
	}
	_ = cache.Set([]byte(code), resp.Body(), 60*60*24)
	if data, ok := respMap["data"].([]any); ok {
		return data
	}
	return []any{}
}

func (m MarketNewsApi) TradingViewNews() *[]models.Telegraph {
	client := newFetchRestyClient()
	TVNews := &[]models.TVNews{}
	news := &[]models.Telegraph{}
	fetchSequence := marketNewsBeginFetch(marketNewsFetchKeyTradingView, marketNewsSourceTradingView)
	fetchErr := errors.New("TradingView news fetch did not complete")
	defer func() {
		marketNewsFinishFetch(marketNewsFetchKeyTradingView, fetchSequence, "", false, fetchErr)
	}()
	//	url := "https://news-mediator.tradingview.com/news-flow/v2/news?filter=lang:zh-Hans&filter=area:WLD&client=screener&streaming=false"
	//url := "https://news-mediator.tradingview.com/news-flow/v2/news?filter=area%3AWLD&filter=lang%3Azh-Hans&client=screener&streaming=false"
	url := "https://news-mediator.tradingview.com/news-flow/v2/news?filter=lang%3Azh-Hans&client=screener&streaming=false"

	resp, err := client.SetTimeout(time.Duration(15)*time.Second).R().
		SetHeader("Host", "news-mediator.tradingview.com").
		SetHeader("Origin", "https://cn.tradingview.com").
		SetHeader("Referer", "https://cn.tradingview.com/").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:140.0) Gecko/20100101 Firefox/140.0").
		Get(url)
	if err != nil {
		logErrorEvery("MarketNewsApi.TradingViewNews.fetch", 10*time.Minute, "TradingViewNews err:%s", err.Error())
		fetchErr = err
		return news
	}
	if resp == nil {
		fetchErr = errors.New("TradingView news returned an empty response")
		return news
	}
	if resp.StatusCode() >= 400 {
		fetchErr = fmt.Errorf("TradingView news returned HTTP %d", resp.StatusCode())
		return news
	}
	if len(resp.Body()) == 0 {
		fetchErr = errors.New("TradingView news returned an empty body")
		return news
	}
	respMap := map[string]any{}
	err = json.Unmarshal(resp.Body(), &respMap)
	if err != nil {
		fetchErr = fmt.Errorf("decode TradingView news response: %w", err)
		return news
	}
	itemsValue, exists := respMap["items"]
	if !exists {
		fetchErr = errors.New("TradingView news response is missing items")
		return news
	}
	if _, itemsOK := itemsValue.([]any); !itemsOK {
		fetchErr = errors.New("TradingView news response items is not an array")
		return news
	}
	items, err := json.Marshal(itemsValue)
	if err != nil {
		fetchErr = fmt.Errorf("encode TradingView news items: %w", err)
		return news
	}
	if err := json.Unmarshal(items, TVNews); err != nil {
		fetchErr = fmt.Errorf("decode TradingView news items: %w", err)
		return news
	}

	for i, a := range *TVNews {
		if i > 10 {
			break
		}
		detail := NewMarketNewsApi().TradingViewNewsDetail(a.Id)
		dataTime := time.Unix(int64(a.Published), 0).Local()
		description := ""
		sentimentResult := ""
		if detail != nil {
			description = detail.ShortDescription
			sentimentResult = AnalyzeSentiment(description).Description
		}
		if a.Title == "" {
			continue
		}
		telegraph := &models.Telegraph{
			Title:           a.Title,
			Content:         description,
			DataTime:        &dataTime,
			IsRed:           false,
			Time:            dataTime.Format("15:04:05"),
			Source:          marketNewsSourceTradingView,
			Url:             fmt.Sprintf("https://cn.tradingview.com/news/%s", a.Id),
			SentimentResult: sentimentResult,
		}
		cnt := int64(0)
		if telegraph.Title == "" {
			db.Dao.Model(telegraph).Where("content=?", telegraph.Content).Count(&cnt)
		} else {
			db.Dao.Model(telegraph).Where("title=?", telegraph.Title).Count(&cnt)
		}
		if cnt > 0 {
			continue
		}
		db.Dao.Model(&models.Telegraph{}).Where("time=? and title=? and source=?", telegraph.Time, telegraph.Title, marketNewsSourceTradingView).FirstOrCreate(&telegraph)
		*news = append(*news, *telegraph)
	}
	fetchErr = nil
	return news
}
func (m MarketNewsApi) TradingViewNewsDetail(id string) *models.TVNewsDetail {
	//https://news-headlines.tradingview.com/v3/story?id=panews%3A9be7cf057e3f9%3A0&lang=zh-Hans
	newsDetail := &models.TVNewsDetail{}
	newsUrl := fmt.Sprintf("https://news-headlines.tradingview.com/v3/story?id=%s&lang=zh-Hans", url.QueryEscape(id))

	client := newFetchRestyClient()
	request := client.SetTimeout(time.Duration(3) * time.Second).R()
	_, err := request.
		SetHeader("Host", "news-headlines.tradingview.com").
		SetHeader("Origin", "https://cn.tradingview.com").
		SetHeader("Referer", "https://cn.tradingview.com/").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:146.0) Gecko/20100101 Firefox/146.0").
		//SetHeader("TE", "trailers").
		//SetHeader("Priority", "u=4").
		//SetHeader("Connection", "keep-alive").
		SetResult(newsDetail).
		Get(newsUrl)
	if err != nil {
		logger.SugaredLogger.Errorf("TradingViewNewsDetail err:%s", err.Error())
		return newsDetail
	}
	logger.SugaredLogger.Infof("resp:%+v", newsDetail)
	return newsDetail
}

func (m MarketNewsApi) XUEQIUHotStock(size int, marketType string) *[]models.HotItem {
	request := newFetchRestyClient().SetTimeout(time.Duration(30) * time.Second).R()
	_, _ = request.
		SetHeader("Host", "xueqiu.com").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:140.0) Gecko/20100101 Firefox/140.0").
		Get("https://xueqiu.com/hq#hot")

	//cookies := resp.Header().Get("Set-Cookie")
	//logger.SugaredLogger.Infof("cookies:%s", cookies)

	url := fmt.Sprintf("https://stock.xueqiu.com/v5/stock/hot_stock/list.json?page=1&size=%d&_type=%s&type=%s", size, marketType, marketType)
	res := &models.XUEQIUHot{}
	_, err := request.
		SetHeader("Host", "stock.xueqiu.com").
		SetHeader("Origin", "https://xueqiu.com").
		SetHeader("Referer", "https://xueqiu.com/").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:140.0) Gecko/20100101 Firefox/140.0").
		//SetHeader("Cookie", "cookiesu=871730774144180; device_id=ee75cebba8a35005c9e7baf7b7dead59; s=ch12b12pfi; Hm_lvt_1db88642e346389874251b5a1eded6e3=1746247619; xq_a_token=361dcfccb1d32a1d9b5b65f1a188b9c9ed1e687d; xqat=361dcfccb1d32a1d9b5b65f1a188b9c9ed1e687d; xq_r_token=450d1db0db9659a6af7cc9297bfa4fccf1776fae; xq_id_token=eyJ0eXAiOiJKV1QiLCJhbGciOiJSUzI1NiJ9.eyJ1aWQiOi0xLCJpc3MiOiJ1YyIsImV4cCI6MTc1MzgzODAwNiwiY3RtIjoxNzUxMjUxMzc2MDY3LCJjaWQiOiJkOWQwbjRBWnVwIn0.TjEtQ5WEN4ajnVjVnY3J-Qq9LjL-F0eat9Cefv_tLJLqsPhzD2y8Lc1CeIu0Ceqhlad7O_yW1tR9nb2dIjDpyOPzWKxvwSOKXLm8XMoz4LMgE2pysBCH4TsetzHsEOhBsY467q-JX3WoFuqo-dqv1FfLSondZCspjEMFdgPFt2V-2iXJY05YUwcBVUvL74mT9ZjNq0KaDeRBJk_il6UR8yibG7RMbe9xWYz5dSO_wJwWuxvnZ8u9EXC2m-TV7-QHVxFHR_5e8Fodrzg0yIcLU4wBTSoIIQDUKqngajX2W-nUAdo6fr78NNDmoswFVH7T7XMuQciMAqj9MpMCVW3Sog; u=871730774144180; ssxmod_itna=iq+h7KAImDORKYQ4Y5G=nxBKDtD7D3qCD0dGMDxeq7tDRDFqApKDHtA68oon7ziBA0+PbZ9xGN4oYxiNDAPq0iDC+Wjxs9Orw5KQb9iqP4MAn0TbNsbtU22eqbCe=S3vTv6xoDHxY=DU1GzeieDx=PD5xDTDWeDGDD3DmnsDi5YD0KDjBYpH+omDYPDEBYDaxDbDimwY4GCrDDCtc5Dw6bmzDDzznL5WWAPzWffZg3YcFgxf8GwD7y3Dla4rMhw23=cz0Efdk0A5hYDXotDvhoY1/H6neEvOt3o=Q0ruT+5RuxoRhDxCmh5tGP32xBD5G0xS2xcb4quDK0Dy2ZmY/DDWM0qmEeSEDeOCIq1fw1misCY=WAzoOtMwDzGdUjpRk5Z0xQBDI2IMw4H7qNiNBLxWiDD; ssxmod_itna2=iq+h7KAImDORKYQ4Y5G=nxBKDtD7D3qCD0dGMDxeq7tDRDFqApKDHtA68oon7ziBA0+PbZYxD3boBmiEPtDFOEPAeFmDDsuGSxf46oGKwGHd8wtUjFe+oV1lxUzutkGly=nCyCjq=UTHxMxFCr1DsFiKPuEpPVO7GrOyk5Aymnc0+11AFND7v16PvwrFQH4I72=3O1OpK7rGw+poWNCxjj=Ka5QDFWAvEzrDFQcIH=GpKpS90FAyIzGcTyck+yhQKaojn96dRqeIh=HkaFrlGnKwzO+a49=F7/c/MejoR3QM20K9IIOymrMN2bsk2TRdKFiaf4O0ut2MauiOER=iQNW2WVgDrkKzD=57r577wEx2hwkqhf8T8BDvkHZRDirC0bNK4O=G3TSkd3wYwq8bst0t9qF/e3M87NYtU2IWYWzqd=BqEfdqGq0R8wxmqLzpeGeuwSTq1OAiB87gDrozjnGkwDKRdrLz8uDjQKVlGhWk8Wd/rXQjx4pG=BNqpW/6TS1wpfxzGf5CrUhtt0j0wC5AUFo2GbX+QXPzD2guxKXrx8lZUQlwWIHyEUz+OLh0eWUkfHfM0YWXlgOejnuUa06rW9y5maDPipGms751hxKcqLq62pQty4iX3QDF6SRQd3tfEBf3CH7r2xe2qq0qdOI5Ge=GezD/Us5Z0xQBwVAZ2N/XvD0HDD").
		SetResult(res).
		Get(url)
	if err != nil {
		logger.SugaredLogger.Errorf("XUEQIUHotStock err:%s", err.Error())
		return &[]models.HotItem{}
	}
	//logger.SugaredLogger.Infof("XUEQIUHotStock:%+v", res)
	return &res.Data.Items
}

func (m MarketNewsApi) HotEvent(size int) *[]models.HotEvent {
	events := &[]models.HotEvent{}
	url := fmt.Sprintf("https://xueqiu.com/hot_event/list.json?count=%d", size)
	resp, err := newFetchRestyClient().SetTimeout(time.Duration(30)*time.Second).R().
		SetHeader("Host", "xueqiu.com").
		SetHeader("Origin", "https://xueqiu.com").
		SetHeader("Referer", "https://xueqiu.com/").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:140.0) Gecko/20100101 Firefox/140.0").
		SetHeader("Cookie", "cookiesu=2617378771242871; s=c2121pp1u71; device_id=237a58584ec58d8e4d4e1040700a644f1; Hm_lvt_1db88642e346389874251b5a1eded6e3=1744100219,1744599115; xq_a_token=b7259d09435458cc3f1a963479abb270a1a016ce; xqat=b7259d09435458cc3f1a963479abb270a1a016ce; xq_r_token=28108bfa1d92ac8a46bbb57722633746218621a3; xq_id_token=eyJ0eXAiOiJKV1QiLCJhbGciOiJSUzI1NiJ9.eyJ1aWQiOi0xLCJpc3MiOiJ1YyIsImV4cCI6MTc1MjU0MTk4OCwiY3RtIjoxNzUwMjMwNjA2NzI0LCJjaWQiOiJkOWQwbjRBWnVwIn0.kU_fz0luJoE7nr-K4UrNUi5-mAG-vMdXtuC4mUKIppILId4UpF70LB70yunxGiNSw6tPFR3-hyLvztKAHtekCUTm3XjUl5b3tEDP-ZUVqHnWXO_5hoeMI8h-Cfx6ZGlIr5x3icvTPkT0OV5CD5A33-ZDTKhKPf-DhJ_-m7CG5GbX4MseOBeMXuLUQUiYHPKhX1QUc0GTGrCzi8Mki0z49D0LVqCSgbsx3UGfowOOyx85_cXb4OAFvIjwbs2p0o_h-ibIT0ngVkkAyEDetVvlcZ_bkardhseCB7k9BEMgH2z8ihgkVxyy3P0degLmDUruhmqn5uZOCi1pVBDvCv9lBg; u=261737877124287; ssxmod_itna=QuG=D5AKiKDIqCqGKi7G7DgmmPlSDWFqKGHDyx4YK0CDmxjKiddDUQivnb8xpnQcGyGYoYhoqEeDBubrDSxD67DK4GTm+ogiw1o3B=xedQHDgBtN=7/i1K53N+rOjquLMU=kbqYxB3DExGkqj0tPi4DxaPD5xDTDWeDGDD3DnnsDQKDRx0kL0oDIxD1D0bmHUEvh38mDYePLmOmDYPYx94Y8KoDeEgsD7HUl/vIGGEAqjLPFegXLD0HolCqr4DCid1qDm+ECfkjDn9sD0KP8fn+CRoDv=tYr4ibx+o=W+8vstf9mjGe3cXseWdBmoFrmf4DA3bFAxnAxD7vYxADaDoerDGHPoxHF+PKGPtDKmiqQGeB5qbi4eg4KDHKDe3DeG0qeEP9xVUoHDDWMYYM0ICr4FBimBDM7D0x4QOECmhul5QCN/m5/74lGm=7x9Wp7A+i7xQ7wlMD4D; ssxmod_itna2=QuG=D5AKiKDIqCqGKi7G7DgmmPlSDWFqKGHDyx4YK0CDmxjKiddDUQivnb8xpnQcGyGYoYhoqoDirSDhPmGD24GajjDuGE3m7or4DlxOSGewHl6iaus2Q62SRX5CFjCds6ltF9xy6iaUuB262UkhRA8UXST=4/b+y3kGKzlGE8T29FA008ljy9jXXC7f7m7QsK667mlUooWrofk=qGZjxtcUrN1NtuAnne1hj+rQP5UnlFkxf+o7VjmatH7u7bCDlbTt3cz6CH9Fl4vye16W/ellc8I3Q37W7ZwiLGD/zPpZcnd2nsqqo/+zRbKAmz4plzwaDqGUe7f9E+P0IFRKqpRv+buQFHBSpcbwND7Q+9XWmnjI2UwKd98jIS3gPXwxvbx4OuiyH8gZ+OEt7DgE/AY/9W4VxDZrlFWyWnC4y4/I0IpAfaGKpbPmauKbkqawqv93vSf+9HamGe0Dt2PNgT3yiEB4vQP2/DdVpcGBOjFujWoHP32OshLPYI20LRCKddwEGkKqPzPwKPc3X5zuB=w2fUdtwKsAW5kQtsl8clNwjC5uDYrxR0h9xaj0xmD+YuI3GPT7xYTalRImPj2wL2=+91a304xa4bTWtP=dLGARhb/efRi0uktaz8i8C04G0x/ZWUzqRza8GGU=FfRfvb4GZM/q2rVsl0nLvRjGeAKgocLouyXs/uwZu3YxbAx30qCbjG1A533zAxIeIgD=0VAc3ixD").
		Get(url)
	if err != nil {
		logger.SugaredLogger.Errorf("HotEvent err:%s", err.Error())
		return events
	}
	//logger.SugaredLogger.Infof("HotEvent:%s", resp.Body())
	respMap := map[string]any{}
	if err = json.Unmarshal(resp.Body(), &respMap); err != nil {
		return events
	}
	items, err := json.Marshal(respMap["list"])
	if err != nil {
		return events
	}
	if err := json.Unmarshal(items, events); err != nil {
		return events
	}
	return events

}

func (m MarketNewsApi) HotTopic(size int) []any {
	url := "https://gubatopic.eastmoney.com/interface/GetData.aspx?path=newtopic/api/Topic/HomePageListRead"
	resp, err := newFetchRestyClient().SetTimeout(time.Duration(30)*time.Second).R().
		SetHeader("Host", "gubatopic.eastmoney.com").
		SetHeader("Origin", "https://gubatopic.eastmoney.com").
		SetHeader("Referer", "https://gubatopic.eastmoney.com/").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:140.0) Gecko/20100101 Firefox/140.0").
		SetFormData(map[string]string{
			"param": fmt.Sprintf("ps=%d&p=1&type=0", size),
			"path":  "newtopic/api/Topic/HomePageListRead",
			"env":   "2",
		}).
		Post(url)
	if err != nil {
		logger.SugaredLogger.Errorf("HotTopic err:%s", err.Error())
		return []any{}
	}
	//logger.SugaredLogger.Infof("HotTopic:%s", resp.Body())
	respMap := map[string]any{}
	if err = json.Unmarshal(resp.Body(), &respMap); err != nil {
		return []any{}
	}
	if rows, ok := respMap["re"].([]any); ok {
		return rows
	}
	return []any{}

}

func (m MarketNewsApi) InvestCalendar(yearMonth string) []any {
	if yearMonth == "" {
		yearMonth = time.Now().Format("2006-01")
	}

	url := "https://app.jiuyangongshe.com/jystock-app/api/v1/timeline/list"
	resp, err := newFetchRestyClient().SetTimeout(time.Duration(30)*time.Second).R().
		SetHeader("Host", "app.jiuyangongshe.com").
		SetHeader("Origin", "https://www.jiuyangongshe.com").
		SetHeader("Referer", "https://www.jiuyangongshe.com/").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:140.0) Gecko/20100101 Firefox/140.0").
		SetHeader("Content-Type", "application/json").
		SetHeader("token", "1cc6380a05c652b922b3d85124c85473").
		SetHeader("platform", "3").
		SetHeader("Cookie", "SESSION=NDZkNDU2ODYtODEwYi00ZGZkLWEyY2ItNjgxYzY4ZWMzZDEy").
		SetHeader("timestamp", strconv.FormatInt(time.Now().UnixMilli(), 10)).
		SetBody(map[string]string{
			"date":  yearMonth,
			"grade": "0",
		}).
		Post(url)
	if err != nil {
		logger.SugaredLogger.Errorf("InvestCalendar err:%s", err.Error())
		return []any{}
	}
	//logger.SugaredLogger.Infof("InvestCalendar:%s", resp.Body())
	respMap := map[string]any{}
	if err = json.Unmarshal(resp.Body(), &respMap); err != nil {
		return []any{}
	}
	if rows, ok := respMap["data"].([]any); ok {
		return rows
	}
	return []any{}

}

func (m MarketNewsApi) ClsCalendar() []any {
	url := "https://www.cls.cn/api/calendar/web/list?app=CailianpressWeb&flag=0&os=web&sv=8.4.6&type=0&sign=4b839750dc2f6b803d1c8ca00d2b40be"
	resp, err := newFetchRestyClient().SetTimeout(time.Duration(30)*time.Second).R().
		SetHeader("Host", "www.cls.cn").
		SetHeader("Origin", "https://www.cls.cn").
		SetHeader("Referer", "https://www.cls.cn/").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:140.0) Gecko/20100101 Firefox/140.0").
		Get(url)
	if err != nil {
		logger.SugaredLogger.Errorf("ClsCalendar err:%s", err.Error())
		return []any{}
	}
	if resp == nil {
		logger.SugaredLogger.Errorf("ClsCalendar err: response is nil")
		return []any{}
	}
	respMap := map[string]any{}
	err = json.Unmarshal(resp.Body(), &respMap)
	if err != nil {
		logger.SugaredLogger.Errorf("ClsCalendar unmarshal err:%s", err.Error())
		return []any{}
	}
	if data, ok := respMap["data"].([]any); ok {
		return data
	}
	return []any{}
}

func (m MarketNewsApi) GetGDP() *models.GDPResp {
	res := &models.GDPResp{}

	url := "https://datacenter-web.eastmoney.com/api/data/v1/get?callback=data&columns=REPORT_DATE%2CTIME%2CDOMESTICL_PRODUCT_BASE%2CFIRST_PRODUCT_BASE%2CSECOND_PRODUCT_BASE%2CTHIRD_PRODUCT_BASE%2CSUM_SAME%2CFIRST_SAME%2CSECOND_SAME%2CTHIRD_SAME&pageNumber=1&pageSize=20&sortColumns=REPORT_DATE&sortTypes=-1&source=WEB&client=WEB&reportName=RPT_ECONOMY_GDP&p=1&pageNo=1&pageNum=1&_=" + strconv.FormatInt(time.Now().Unix(), 10)
	resp, err := newFetchRestyClient().SetTimeout(time.Duration(30)*time.Second).R().
		SetHeader("Host", "datacenter-web.eastmoney.com").
		SetHeader("Origin", "https://datacenter.eastmoney.com").
		SetHeader("Referer", "https://data.eastmoney.com/cjsj/gdp.html").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:140.0) Gecko/20100101 Firefox/140.0").
		Get(url)
	if err != nil {
		logger.SugaredLogger.Errorf("GDP err:%s", err.Error())
		return res
	}
	if resp == nil {
		logger.SugaredLogger.Errorf("GDP err: response is nil")
		return res
	}
	body := resp.Body()
	logger.SugaredLogger.Debugf("GDP:%s", body)
	vm := otto.New()
	if _, err = vm.Run("function data(res){return res};"); err != nil {
		logger.SugaredLogger.Errorf("GDP vm init err:%s", err.Error())
		return res
	}

	val, err := vm.Run(body)
	if err != nil {
		logger.SugaredLogger.Errorf("GDP err:%s", err.Error())
		return res
	}
	data, _ := val.Object().Value().Export()
	logger.SugaredLogger.Infof("GDP:%v", data)
	marshal, err := json.Marshal(data)
	if err != nil {
		return res
	}
	if err = json.Unmarshal(marshal, &res); err != nil {
		logger.SugaredLogger.Errorf("GDP unmarshal err:%s", err.Error())
		return res
	}
	logger.SugaredLogger.Infof("GDP:%+v", res)
	return res
}

func (m MarketNewsApi) GetCPI() *models.CPIResp {
	res := &models.CPIResp{}

	url := "https://datacenter-web.eastmoney.com/api/data/v1/get?callback=data&columns=REPORT_DATE%2CTIME%2CNATIONAL_SAME%2CNATIONAL_BASE%2CNATIONAL_SEQUENTIAL%2CNATIONAL_ACCUMULATE%2CCITY_SAME%2CCITY_BASE%2CCITY_SEQUENTIAL%2CCITY_ACCUMULATE%2CRURAL_SAME%2CRURAL_BASE%2CRURAL_SEQUENTIAL%2CRURAL_ACCUMULATE&pageNumber=1&pageSize=20&sortColumns=REPORT_DATE&sortTypes=-1&source=WEB&client=WEB&reportName=RPT_ECONOMY_CPI&p=1&pageNo=1&pageNum=1&_=" + strconv.FormatInt(time.Now().Unix(), 10)
	resp, err := newFetchRestyClient().SetTimeout(time.Duration(30)*time.Second).R().
		SetHeader("Host", "datacenter-web.eastmoney.com").
		SetHeader("Origin", "https://datacenter.eastmoney.com").
		SetHeader("Referer", "https://data.eastmoney.com/cjsj/gdp.html").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:140.0) Gecko/20100101 Firefox/140.0").
		Get(url)
	if err != nil {
		logger.SugaredLogger.Errorf("GetCPI err:%s", err.Error())
		return res
	}
	if resp == nil {
		logger.SugaredLogger.Errorf("GetCPI err: response is nil")
		return res
	}
	body := resp.Body()
	logger.SugaredLogger.Debugf("GetCPI:%s", body)
	vm := otto.New()
	if _, err = vm.Run("function data(res){return res};"); err != nil {
		logger.SugaredLogger.Errorf("GetCPI vm init err:%s", err.Error())
		return res
	}

	val, err := vm.Run(body)
	if err != nil {
		logger.SugaredLogger.Errorf("GetCPI err:%s", err.Error())
		return res
	}
	data, _ := val.Object().Value().Export()
	logger.SugaredLogger.Infof("GetCPI:%v", data)
	marshal, err := json.Marshal(data)
	if err != nil {
		return res
	}
	if err = json.Unmarshal(marshal, &res); err != nil {
		logger.SugaredLogger.Errorf("GetCPI unmarshal err:%s", err.Error())
		return res
	}
	logger.SugaredLogger.Infof("GetCPI:%+v", res)
	return res
}

// GetPPI PPI
func (m MarketNewsApi) GetPPI() *models.PPIResp {
	res := &models.PPIResp{}
	url := "https://datacenter-web.eastmoney.com/api/data/v1/get?callback=data&columns=REPORT_DATE,TIME,BASE,BASE_SAME,BASE_ACCUMULATE&pageNumber=1&pageSize=20&sortColumns=REPORT_DATE&sortTypes=-1&source=WEB&client=WEB&reportName=RPT_ECONOMY_PPI&p=1&pageNo=1&pageNum=1&_=" + strconv.FormatInt(time.Now().Unix(), 10)
	resp, err := newFetchRestyClient().SetTimeout(time.Duration(30)*time.Second).R().
		SetHeader("Host", "datacenter-web.eastmoney.com").
		SetHeader("Origin", "https://datacenter.eastmoney.com").
		SetHeader("Referer", "https://data.eastmoney.com/cjsj/gdp.html").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:140.0) Gecko/20100101 Firefox/140.0").
		Get(url)
	if err != nil {
		logger.SugaredLogger.Errorf("GetPPI err:%s", err.Error())
		return res
	}
	if resp == nil {
		logger.SugaredLogger.Errorf("GetPPI err: response is nil")
		return res
	}
	body := resp.Body()
	vm := otto.New()
	if _, err = vm.Run("function data(res){return res};"); err != nil {
		logger.SugaredLogger.Errorf("GetPPI vm init err:%s", err.Error())
		return res
	}

	val, err := vm.Run(body)
	if err != nil {
		return res
	}
	data, _ := val.Object().Value().Export()
	marshal, err := json.Marshal(data)
	if err != nil {
		return res
	}
	if err = json.Unmarshal(marshal, &res); err != nil {
		logger.SugaredLogger.Errorf("GetPPI unmarshal err:%s", err.Error())
		return res
	}
	return res
}

func (m MarketNewsApi) GetPMI() *models.PMIResp {
	res := &models.PMIResp{}
	url := "https://datacenter-web.eastmoney.com/api/data/v1/get?callback=data&columns=REPORT_DATE%2CTIME%2CMAKE_INDEX%2CMAKE_SAME%2CNMAKE_INDEX%2CNMAKE_SAME&pageNumber=1&pageSize=20&sortColumns=REPORT_DATE&sortTypes=-1&source=WEB&client=WEB&reportName=RPT_ECONOMY_PMI&p=1&pageNo=1&pageNum=1&_=" + strconv.FormatInt(time.Now().Unix(), 10)
	resp, err := newFetchRestyClient().SetTimeout(time.Duration(30)*time.Second).R().
		SetHeader("Host", "datacenter-web.eastmoney.com").
		SetHeader("Origin", "https://datacenter.eastmoney.com").
		SetHeader("Referer", "https://data.eastmoney.com/cjsj/gdp.html").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:140.0) Gecko/20100101 Firefox/140.0").
		Get(url)
	if err != nil {
		return res
	}
	if resp == nil {
		logger.SugaredLogger.Errorf("GetPMI err: response is nil")
		return res
	}
	body := resp.Body()
	vm := otto.New()
	if _, err = vm.Run("function data(res){return res};"); err != nil {
		logger.SugaredLogger.Errorf("GetPMI vm init err:%s", err.Error())
		return res
	}

	val, err := vm.Run(body)
	if err != nil {
		return res
	}
	data, _ := val.Object().Value().Export()
	marshal, err := json.Marshal(data)
	if err != nil {
		return res
	}
	if err = json.Unmarshal(marshal, &res); err != nil {
		logger.SugaredLogger.Errorf("GetPMI unmarshal err:%s", err.Error())
		return res
	}
	return res

}
func (m MarketNewsApi) GetIndustryReportInfo(infoCode string) string {
	url := "https://data.eastmoney.com/report/zw_industry.jshtml?infocode=" + infoCode
	resp, err := newFetchRestyClient().SetTimeout(time.Duration(30)*time.Second).R().
		SetHeader("Host", "data.eastmoney.com").
		SetHeader("Origin", "https://data.eastmoney.com").
		SetHeader("Referer", "https://data.eastmoney.com/report/industry.jshtml").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:140.0) Gecko/20100101 Firefox/140.0").
		Get(url)
	if err != nil {
		logger.SugaredLogger.Errorf("GetIndustryReportInfo err:%s", err.Error())
		return ""
	}
	body := resp.Body()
	//logger.SugaredLogger.Debugf("GetIndustryReportInfo:%s", body)
	doc, parseErr := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if parseErr != nil {
		logger.SugaredLogger.Errorf("GetIndustryReportInfo parse html err:%s", parseErr.Error())
		return ""
	}
	title, _ := doc.Find("div.c-title").Html()
	content, _ := doc.Find("div.ctx-content").Html()
	//logger.SugaredLogger.Infof("GetIndustryReportInfo:\n%s\n%s", title, content)
	markdown, err := util.HTMLToMarkdown(title + content)
	if err != nil {
		return ""
	}
	logger.SugaredLogger.Infof("GetIndustryReportInfo markdown:\n%s", markdown)
	return markdown
}

func (m MarketNewsApi) ReutersNew() *models.ReutersNews {
	client := newFetchRestyClient()
	news := &models.ReutersNews{}
	//url := "https://www.reuters.com/pf/api/v3/content/fetch/articles-by-section-alias-or-id-v1?query={\"arc-site\":\"reuters\",\"fetch_type\":\"collection\",\"offset\":0,\"section_id\":\"/world/\",\"size\":9,\"uri\":\"/world/\",\"website\":\"reuters\"}&d=300&mxId=00000000&_website=reuters"
	url := "https://www.reuters.com/pf/api/v3/content/fetch/recent-stories-by-sections-v1?query=%7B%22section_ids%22%3A%22%2Fworld%2F%22%2C%22size%22%3A4%2C%22website%22%3A%22reuters%22%7D&d=334&mxId=00000000&_website=reuters"
	_, err := client.SetTimeout(time.Duration(5)*time.Second).R().
		SetHeader("Host", "www.reuters.com").
		SetHeader("Origin", "https://www.reuters.com").
		SetHeader("Referer", "https://www.reuters.com/world/china/").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:140.0) Gecko/20100101 Firefox/140.0").
		SetResult(news).
		Get(url)
	if err != nil {
		logger.SugaredLogger.Errorf("ReutersNew err:%s", err.Error())
		return news
	}
	logger.SugaredLogger.Infof("Articles:%+v", news.Result.Articles)
	return news
}

func (m MarketNewsApi) InteractiveAnswer(page int, pageSize int, keyWord string) *models.InteractiveAnswer {
	client := newFetchRestyClient()
	url := fmt.Sprintf("https://irm.cninfo.com.cn/newircs/index/search?_t=%d", time.Now().Unix())
	answers := &models.InteractiveAnswer{}
	logger.SugaredLogger.Infof("请求url:%s", url)
	resp, err := client.SetTimeout(time.Duration(5)*time.Second).R().
		SetHeader("Host", "irm.cninfo.com.cn").
		SetHeader("Origin", "https://irm.cninfo.com.cn").
		SetHeader("Referer", "https://irm.cninfo.com.cn/views/interactiveAnswer").
		SetHeader("handleError", "true").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:142.0) Gecko/20100101 Firefox/142.0").
		SetFormData(map[string]string{
			"pageNo":      convertor.ToString(page),
			"pageSize":    convertor.ToString(pageSize),
			"searchTypes": "11",
			"highLight":   "true",
			"keyWord":     keyWord,
		}).
		SetResult(answers).
		Post(url)
	if err != nil {
		logger.SugaredLogger.Errorf("InteractiveAnswer-err:%+v", err)
		return answers
	}
	if resp == nil {
		logger.SugaredLogger.Errorf("InteractiveAnswer err: response is nil")
		return answers
	}
	logger.SugaredLogger.Debugf("InteractiveAnswer-resp:%s", resp.Body())
	return answers

}

func (m MarketNewsApi) CailianpressWeb(searchWords string) *models.CailianpressWeb {
	res := &models.CailianpressWeb{}
	_, err := newFetchRestyClient().SetTimeout(time.Second*10).R().
		SetHeader("Content-Type", "application/json").
		SetHeader("Host", "www.cls.cn").
		SetHeader("Origin", "https://www.cls.cn").
		SetHeader("Referer", "https://www.cls.cn/telegraph").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/117.0.0.0 Safari/537.36 Edg/117.0.2045.60").
		SetBody(fmt.Sprintf(`{"app":"CailianpressWeb","os":"web","sv":"8.4.6","category":"","keyword":"%s"}`, searchWords)).
		SetResult(res).
		Post("https://www.cls.cn/api/csw?app=CailianpressWeb&os=web&sv=8.4.6&sign=9f8797a1f4de66c2370f7a03990d2737")
	if err != nil {
		return nil
	}
	logger.SugaredLogger.Debug(res)

	return res
}

// GetNewsWindow reads persisted news by its event time. CreatedAt is used only
// for legacy rows whose DataTime is nil or zero; it is never allowed to
// override a non-zero event timestamp.
func (m MarketNewsApi) GetNewsWindow(sources []string, from, to time.Time) (NewsWindowResult, error) {
	sources = normalizeNewsWindowSources(sources)
	if to.IsZero() {
		to = time.Now()
	}
	if from.IsZero() {
		from = to.Add(-24 * time.Hour)
	}
	result := NewsWindowResult{
		Items:   []*models.Telegraph{},
		Status:  NewsWindowStatusEmpty,
		Sources: sources,
		From:    from,
		To:      to,
	}
	if to.Before(from) {
		err := fmt.Errorf("invalid news window: from %s is after to %s", from.Format(time.RFC3339), to.Format(time.RFC3339))
		result.Status = NewsWindowStatusFailed
		result.Warning = err.Error()
		return result, err
	}
	limit := defaultNewsWindowLimit
	if db.Dao == nil {
		err := errors.New("market news database is not initialized")
		result.Status = NewsWindowStatusFailed
		result.Warning = err.Error()
		return result, err
	}

	zeroTimeCutoff := time.Date(2, time.January, 1, 0, 0, 0, 0, time.UTC)
	baseQuery := func() *gorm.DB {
		query := db.Dao.Model(&models.Telegraph{}).Preload("TelegraphTags")
		if len(sources) > 0 {
			query = query.Where("source IN ?", sources)
		}
		return query
	}
	query := baseQuery().
		Where(`(
			(data_time IS NOT NULL AND data_time > ? AND data_time >= ? AND data_time <= ?)
			OR
			((data_time IS NULL OR data_time <= ?) AND created_at >= ? AND created_at <= ?)
		)`, zeroTimeCutoff, from, to, zeroTimeCutoff, from, to)
	rows := make([]*models.Telegraph, 0, limit)
	if err := query.Order("data_time DESC, is_red DESC, created_at DESC, id DESC").Limit(limit).Find(&rows).Error; err != nil {
		wrapped := fmt.Errorf("query market news window: %w", err)
		result.Status = NewsWindowStatusFailed
		result.Warning = wrapped.Error()
		return result, wrapped
	}

	result.Items = dedupeNewsWindowItems(rows)
	if len(result.Items) == 0 {
		// Query success alone cannot distinguish an upstream-empty feed from a
		// failed refresh. Only a completed fetch attempt that belongs to this
		// event-time window can promote an otherwise empty read to failed.
		if fetchErr := marketNewsFetchFailureForWindow(sources, from, to); fetchErr != nil {
			result.Status = NewsWindowStatusFailed
			result.Warning = fetchErr.Error()
			return result, fetchErr
		}
		// A bounded stale probe distinguishes an actually empty source from a
		// source whose newest persisted event predates the requested window.
		// Keeping this separate from the window query prevents old event-time
		// rows with fresh ingestion timestamps from being treated as current.
		staleRows := make([]*models.Telegraph, 0, limit)
		staleQuery := baseQuery().Where(`(
			(data_time IS NOT NULL AND data_time > ? AND data_time < ? AND data_time <= ?)
			OR
			((data_time IS NULL OR data_time <= ?) AND created_at < ? AND created_at <= ?)
		)`, zeroTimeCutoff, from, to, zeroTimeCutoff, from, to)
		if err := staleQuery.Order("data_time DESC, is_red DESC, created_at DESC, id DESC").Limit(limit).Find(&staleRows).Error; err != nil {
			wrapped := fmt.Errorf("query stale market news window: %w", err)
			result.Status = NewsWindowStatusFailed
			result.Warning = wrapped.Error()
			return result, wrapped
		}
		result.Items = dedupeNewsWindowItems(staleRows)
		if len(result.Items) == 0 {
			result.Status = NewsWindowStatusEmpty
			result.Warning = "no market news at or before the requested window end"
			return result, nil
		}
	}
	if err := hydrateNewsWindowSubjectTags(result.Items); err != nil {
		wrapped := fmt.Errorf("load market news tags: %w", err)
		result.Status = NewsWindowStatusFailed
		result.Warning = wrapped.Error()
		return result, wrapped
	}
	if newsWindowItemsAreStale(result.Items, from) {
		result.Status = NewsWindowStatusStale
		result.Warning = "all market news event times are older than the requested window"
		return result, nil
	}
	result.Status = NewsWindowStatusOK
	return result, nil
}

func marketNewsFetchFailureForWindow(sources []string, from, to time.Time) error {
	selectedSources := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		selectedSources[strings.TrimSpace(source)] = struct{}{}
	}
	allRealSources := len(selectedSources) == 0
	latestBySource := make(map[string]marketNewsFetchMeta, 3)
	keys := []string{
		marketNewsFetchKeyCLSTelegraphAPI,
		marketNewsFetchKeyCLSTelegraphWeb,
		marketNewsFetchKeySinaLive,
		marketNewsFetchKeyTradingView,
	}
	marketNewsFetchMetaMu.RLock()
	for _, key := range keys {
		meta, ok := marketNewsFetchMetaBySource[key]
		if !ok || !meta.Completed || meta.AttemptedAt.IsZero() {
			continue
		}
		if !allRealSources {
			if _, selected := selectedSources[meta.Source]; !selected {
				continue
			}
		}
		// A refresh normally finishes just after a fixed decision cutoff. A
		// small grace interval keeps that causally related attempt attached to
		// the window without allowing today's failure to poison historical reads.
		if meta.CompletedAt.Before(from) || meta.CompletedAt.After(to.Add(30*time.Minute)) {
			continue
		}
		if current, exists := latestBySource[meta.Source]; !exists || meta.sequence > current.sequence {
			latestBySource[meta.Source] = meta
		}
	}
	marketNewsFetchMetaMu.RUnlock()

	failures := make([]string, 0, len(latestBySource))
	for _, source := range []string{marketNewsSourceCLSTelegraph, marketNewsSourceSina, marketNewsSourceTradingView} {
		meta, exists := latestBySource[source]
		if !exists || meta.Succeeded {
			continue
		}
		detail := strings.TrimSpace(meta.Error)
		if detail == "" {
			detail = "unknown fetch error"
		}
		failures = append(failures, fmt.Sprintf("%s: %s", source, detail))
	}
	if len(failures) == 0 {
		return nil
	}
	return fmt.Errorf("market news fetch failed for requested window: %s", strings.Join(failures, "; "))
}

func normalizeNewsWindowSources(sources []string) []string {
	result := make([]string, 0, len(sources))
	seen := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		source = strings.TrimSpace(source)
		if source == "" {
			continue
		}
		if _, exists := seen[source]; exists {
			continue
		}
		seen[source] = struct{}{}
		result = append(result, source)
	}
	return result
}

func dedupeNewsWindowItems(rows []*models.Telegraph) []*models.Telegraph {
	result := make([]*models.Telegraph, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, item := range rows {
		if item == nil {
			continue
		}
		key := strings.TrimSpace(item.Content)
		if key == "" {
			key = strings.TrimSpace(item.Title)
		}
		if key != "" {
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
		}
		result = append(result, item)
	}
	return result
}

func hydrateNewsWindowSubjectTags(items []*models.Telegraph) error {
	for _, item := range items {
		if item == nil || len(item.TelegraphTags) == 0 {
			continue
		}
		tags := make([]models.Tags, 0, len(item.TelegraphTags))
		tagIDs := lo.Map(item.TelegraphTags, func(item models.TelegraphTags, _ int) uint {
			return item.TagId
		})
		if err := db.Dao.Model(&models.Tags{}).Where("id IN ?", tagIDs).Find(&tags).Error; err != nil {
			return err
		}
		item.SubjectTags = lo.Map(tags, func(item models.Tags, _ int) string {
			return item.Name
		})
	}
	return nil
}

func newsWindowItemsAreStale(items []*models.Telegraph, from time.Time) bool {
	if from.IsZero() {
		return false
	}
	hasEventTime := false
	for _, item := range items {
		if item == nil {
			continue
		}
		eventTime := item.CreatedAt
		if item.DataTime != nil && !item.DataTime.IsZero() {
			eventTime = *item.DataTime
		}
		if eventTime.IsZero() {
			continue
		}
		hasEventTime = true
		if !eventTime.Before(from) {
			return false
		}
	}
	return hasEventTime
}

func (m MarketNewsApi) GetNews24HoursList(source string, limit int) *[]*models.Telegraph {
	news := &[]*models.Telegraph{}
	if source != "" {
		db.Dao.Model(news).Preload("TelegraphTags").Where("source=? and created_at>?", source, time.Now().Add(-24*time.Hour)).Order("data_time desc,is_red desc").Limit(limit).Find(news)
	} else {
		db.Dao.Model(news).Preload("TelegraphTags").Where("created_at>?", time.Now().Add(-24*time.Hour)).Order("data_time desc,is_red desc").Limit(limit).Find(news)
	}
	// 内容去重
	uniqueNews := make([]*models.Telegraph, 0)
	seenContent := make(map[string]bool)
	for _, item := range *news {
		tags := &[]models.Tags{}
		db.Dao.Model(&models.Tags{}).Where("id in ?", lo.Map(item.TelegraphTags, func(item models.TelegraphTags, index int) uint {
			return item.TagId
		})).Find(&tags)
		tagNames := lo.Map(*tags, func(item models.Tags, index int) string {
			return item.Name
		})
		item.SubjectTags = tagNames
		//logger.SugaredLogger.Infof("tagNames %v ，SubjectTags：%s", tagNames, item.SubjectTags)
		// 使用内容作为去重键值，可以考虑只使用内容的前几个字符或哈希值
		contentKey := strings.TrimSpace(item.Content)
		if contentKey != "" && !seenContent[contentKey] {
			seenContent[contentKey] = true
			uniqueNews = append(uniqueNews, item)
		}
	}
	return &uniqueNews
}
