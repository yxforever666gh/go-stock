package data

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go-stock/backend/marketdata"
	"go-stock/backend/models"
	"go-stock/backend/themes"
)

// AdaptHotTopics converts the loose map returned by MarketNewsApi.HotTopic.
func AdaptHotTopics(items []any, observedAt time.Time) []themes.RawThemeSignal {
	signals := make([]themes.RawThemeSignal, 0, len(items))
	for index, item := range items {
		row := themeSourceMap(item)
		name := themeSourceString(row, "TopicName", "topicName", "topic_name", "Name", "name", "title")
		if name == "" {
			continue
		}
		summary := themeSourceString(row, "TopicDesc", "topicDesc", "summary", "description", "desc", "content")
		title := themeSourceString(row, "title", "Title")
		if title == "" {
			title = name
		}
		published := themeSourceTime(row, "publishedAt", "publishTime", "createTime", "createdAt", "updateTime")
		signal := themes.RawThemeSignal{
			ThemeName: name, Kind: themes.ThemeSignalHotTopic, EventType: themes.ThemeSignalHotTopic,
			Title: title, Summary: summary, PublishedAt: published, FirstObservedAt: observedAt,
			SourceName: "东方财富热门话题", SourceRef: themeSourceString(row, "url", "link", "sourceRef"),
			Stance: themes.ThemeSignalSupports, SourceCredibilityScore: 65, Rank: index + 1,
			HeatScore:      themeSourceFloat(row, "Hot", "hot", "hotValue", "heat", "score"),
			Metadata:       map[string]interface{}{"topicId": themeSourceString(row, "TopicID", "topicId", "id")},
			RawPayloadHash: hashThemeSourcePayload(item),
		}
		signals = append(signals, themes.NormalizeRawThemeSignal(signal, signal.SourceName, observedAt))
	}
	return signals
}

// AdaptXueqiuHotEvents preserves the real provider identity. Although the old
// research collector labelled this feed as Eastmoney, its endpoint and model
// are Xueqiu and must be audited as such.
func AdaptXueqiuHotEvents(items []models.HotEvent, observedAt time.Time) []themes.RawThemeSignal {
	signals := make([]themes.RawThemeSignal, 0, len(items))
	for index, item := range items {
		name := strings.TrimSpace(item.Tag)
		if name == "" {
			name = strings.TrimSpace(item.Content)
		}
		if name == "" {
			continue
		}
		signal := themes.RawThemeSignal{
			ThemeName: name, Kind: themes.ThemeSignalHotEvent, EventType: themes.ThemeSignalHotEvent,
			Title: strings.TrimSpace(item.Content), Summary: strings.TrimSpace(item.Content),
			FirstObservedAt: observedAt, SourceName: "雪球热点事件",
			SourceRef: "https://xueqiu.com/hot_event/list.json", Stance: themes.ThemeSignalSupports,
			SourceCredibilityScore: 60, Rank: index + 1, HeatScore: float64(item.Hot),
			Metadata:       map[string]interface{}{"eventId": item.Id, "statusCount": item.StatusCount},
			RawPayloadHash: hashThemeSourcePayload(item),
		}
		if signal.Title == "" {
			signal.Title = name
		}
		signals = append(signals, themes.NormalizeRawThemeSignal(signal, signal.SourceName, observedAt))
	}
	return signals
}

// AdaptTelegraphs converts persisted news and telegraphs. Subject tags are
// preferred as theme names; an untagged item remains usable as an observation
// under its title and can be reclassified by the theme normalization service.
func AdaptTelegraphs(items []*models.Telegraph, observedAt time.Time) []themes.RawThemeSignal {
	var signals []themes.RawThemeSignal
	for _, item := range items {
		if item == nil {
			continue
		}
		themeNames := themeSourceCleanStrings(item.SubjectTags)
		if len(themeNames) == 0 && strings.TrimSpace(item.Title) != "" {
			themeNames = []string{strings.TrimSpace(item.Title)}
		}
		published := item.DataTime
		if published == nil {
			if parsed := themeSourceParseTime(item.Time); parsed != nil {
				published = parsed
			}
		}
		firstObserved := observedAt
		if !item.CreatedAt.IsZero() {
			firstObserved = item.CreatedAt
		}
		for _, themeName := range themeNames {
			signal := themes.RawThemeSignal{
				ThemeName: themeName, Kind: themes.ThemeSignalNews, EventType: themes.ThemeSignalNews,
				Title: strings.TrimSpace(item.Title), Summary: strings.TrimSpace(item.Content),
				PublishedAt: published, FirstObservedAt: firstObserved, CollectedAt: observedAt,
				SourceName: strings.TrimSpace(item.Source), SourceRef: strings.TrimSpace(item.Url),
				Stance: themeSourceStance(item.SentimentResult), SourceCredibilityScore: 70,
				Securities: themeSourceSecurities(item.StocksTags), RawPayloadHash: hashThemeSourcePayload(item),
			}
			if signal.SourceName == "" {
				signal.SourceName = "新闻/电报"
			}
			if signal.Title == "" {
				signal.Title = themeName
			}
			signals = append(signals, themes.NormalizeRawThemeSignal(signal, signal.SourceName, observedAt))
		}
	}
	return signals
}

// AdaptAnnouncements converts the loose Eastmoney announcement response.
func AdaptAnnouncements(items []any, observedAt time.Time) []themes.RawThemeSignal {
	signals := make([]themes.RawThemeSignal, 0, len(items))
	for _, item := range items {
		row := themeSourceMap(item)
		title := themeSourceString(row, "title", "noticeTitle", "NOTICE_TITLE", "announcementTitle")
		if title == "" {
			continue
		}
		themeName := themeSourceString(row, "themeName", "BOARD_NAME", "boardName", "conceptName", "columnName", "category")
		securities := themeSourceSecuritiesFromAnnouncement(row)
		if themeName == "" && len(securities) > 0 {
			themeName = securities[0].Name
		}
		if themeName == "" {
			themeName = title
		}
		published := themeSourceTime(row, "notice_date", "noticeDate", "NOTICE_DATE", "display_time", "publishTime", "publishedAt")
		signal := themes.RawThemeSignal{
			ThemeName: themeName, Kind: themes.ThemeSignalAnnouncement, EventType: themes.ThemeSignalAnnouncement,
			Title: title, Summary: themeSourceString(row, "summary", "digest", "content"), PublishedAt: published,
			FirstObservedAt: observedAt, SourceName: "东方财富公告",
			SourceRef: themeSourceString(row, "url", "noticeUrl", "sourceRef"), Stance: themeSourceStance(themeSourceString(row, "stance", "sentiment")),
			SourceCredibilityScore: 90, Securities: securities, RawPayloadHash: hashThemeSourcePayload(item),
			Metadata: map[string]interface{}{"announcementId": themeSourceString(row, "art_code", "artCode", "id")},
		}
		signals = append(signals, themes.NormalizeRawThemeSignal(signal, signal.SourceName, observedAt))
	}
	return signals
}

func AdaptConceptInfo(items []models.StockConceptInfo, observedAt time.Time) []themes.RawThemeSignal {
	signals := make([]themes.RawThemeSignal, 0, len(items))
	for _, item := range items {
		name := strings.TrimSpace(item.BOARDNAME)
		if name == "" {
			continue
		}
		security := themeSourceSecurity(item.SECURITYCODE, item.SECURITYNAMEABBR)
		signal := themes.RawThemeSignal{
			ThemeName: name, Kind: themes.ThemeSignalConcept, EventType: themes.ThemeSignalConcept,
			Title: strings.TrimSpace(item.SELECTEDBOARDREASON), Summary: strings.TrimSpace(item.SELECTEDBOARDREASON),
			FirstObservedAt: observedAt, SourceName: "东方财富概念信息", Stance: themes.ThemeSignalSupports,
			SourceCredibilityScore: 75, Rank: item.BOARDRANK, Securities: []themes.RawThemeSecurity{security},
			Metadata:       map[string]interface{}{"boardCode": item.NEWBOARDCODE, "boardYield": item.BOARDYIELD},
			RawPayloadHash: hashThemeSourcePayload(item),
		}
		if signal.Title == "" {
			signal.Title = name
		}
		signals = append(signals, themes.NormalizeRawThemeSignal(signal, signal.SourceName, observedAt))
	}
	return signals
}

type ConceptFundFlowSnapshot struct {
	Rows       []FundFlowRow
	SourceName string
	SourceRef  string
	AsOf       time.Time
}

func AdaptConceptFundFlows(snapshot ConceptFundFlowSnapshot, observedAt time.Time) []themes.RawThemeSignal {
	signals := make([]themes.RawThemeSignal, 0, len(snapshot.Rows))
	sourceName := themeSourceProviderDisplayName(snapshot.SourceName)
	for index, row := range snapshot.Rows {
		if strings.TrimSpace(row.Name) == "" {
			continue
		}
		published := (*time.Time)(nil)
		if !snapshot.AsOf.IsZero() {
			value := snapshot.AsOf
			published = &value
		}
		signal := themes.RawThemeSignal{
			ThemeName: strings.TrimSpace(row.Name), Kind: themes.ThemeSignalFundFlow, EventType: themes.ThemeSignalFundFlow,
			Title: fmt.Sprintf("%s概念资金流", strings.TrimSpace(row.Name)), PublishedAt: published,
			FirstObservedAt: observedAt, SourceName: sourceName, SourceRef: strings.TrimSpace(snapshot.SourceRef),
			Stance: themes.ThemeSignalSupports, SourceCredibilityScore: 70, Rank: index + 1,
			Metadata:       map[string]interface{}{"code": row.Code, "netAmount": row.NetAmount, "inAmount": row.InAmount, "outAmount": row.OutAmount, "changePct": row.ChangePct},
			RawPayloadHash: hashThemeSourcePayload(row),
		}
		signals = append(signals, themes.NormalizeRawThemeSignal(signal, signal.SourceName, observedAt))
	}
	return signals
}

type ExistingThemeSourceOptions struct {
	News          *MarketNewsApi
	Stocks        *StockDataApi
	Market        *MarketEvidenceService
	StockCodes    []string
	HotTopicLimit int
	HotEventLimit int
	NewsLimit     int
}

// NewExistingThemeSourceAdapters wires the existing project fetchers into the
// injectable contract. It performs no I/O until the returned adapters run.
func NewExistingThemeSourceAdapters(options ExistingThemeSourceOptions) []themes.SourceAdapter {
	news := options.News
	if news == nil {
		news = NewMarketNewsApi()
	}
	hotTopicLimit := themeSourcePositive(options.HotTopicLimit, 30)
	hotEventLimit := themeSourcePositive(options.HotEventLimit, 30)
	newsLimit := themeSourcePositive(options.NewsLimit, 100)
	adapters := []themes.SourceAdapter{
		themes.SourceAdapterFunc{SourceName: "东方财富热门话题", CollectFunc: func(ctx context.Context, observedAt time.Time) ([]themes.RawThemeSignal, error) {
			return AdaptHotTopics(news.HotTopic(hotTopicLimit), observedAt), nil
		}},
		themes.SourceAdapterFunc{SourceName: "雪球热点事件", CollectFunc: func(ctx context.Context, observedAt time.Time) ([]themes.RawThemeSignal, error) {
			items := news.HotEvent(hotEventLimit)
			if items == nil {
				return []themes.RawThemeSignal{}, nil
			}
			return AdaptXueqiuHotEvents(*items, observedAt), nil
		}},
		themes.SourceAdapterFunc{SourceName: "新闻/电报", CollectFunc: func(ctx context.Context, observedAt time.Time) ([]themes.RawThemeSignal, error) {
			items := news.GetNewsList("", newsLimit)
			if items == nil {
				return []themes.RawThemeSignal{}, nil
			}
			return AdaptTelegraphs(*items, observedAt), nil
		}},
	}

	stockCodes := themeSourceCleanStrings(options.StockCodes)
	if len(stockCodes) > 0 {
		adapters = append(adapters, themes.SourceAdapterFunc{SourceName: "东方财富公告", CollectFunc: func(ctx context.Context, observedAt time.Time) ([]themes.RawThemeSignal, error) {
			return AdaptAnnouncements(news.StockNotice(strings.Join(stockCodes, ",")), observedAt), nil
		}})
		stocks := options.Stocks
		if stocks == nil {
			stocks = NewStockDataApi()
		}
		adapters = append(adapters, themes.SourceAdapterFunc{SourceName: "东方财富概念信息", CollectFunc: func(ctx context.Context, observedAt time.Time) ([]themes.RawThemeSignal, error) {
			var signals []themes.RawThemeSignal
			for _, code := range stockCodes {
				if err := ctx.Err(); err != nil {
					return signals, err
				}
				response := stocks.GetStockConceptInfo(code)
				signals = append(signals, AdaptConceptInfo(response.Result.Data, observedAt)...)
			}
			return signals, nil
		}})
	}

	market := options.Market
	if market == nil {
		market = NewMarketEvidenceService()
	}
	adapters = append(adapters, themes.SourceAdapterFunc{SourceName: "概念资金流", CollectFunc: func(ctx context.Context, observedAt time.Time) ([]themes.RawThemeSignal, error) {
		envelope := market.FundFlows(ctx, marketdata.ProviderRequest{Scope: "concept", Sort: "netamount", Limit: 50})
		if envelope.Status == marketdata.StatusUnavailable || envelope.Status == marketdata.StatusFailed {
			return nil, themeSourceEnvelopeError(envelope.Errors)
		}
		ref := ""
		for _, source := range envelope.Sources {
			if strings.EqualFold(source.Provider, envelope.Source) {
				ref = source.SourceRef
				break
			}
		}
		return AdaptConceptFundFlows(ConceptFundFlowSnapshot{Rows: envelope.Data, SourceName: envelope.Source, SourceRef: ref, AsOf: envelope.AsOf}, observedAt), nil
	}})
	return adapters
}

func themeSourceEnvelopeError(values []marketdata.DataError) error {
	if len(values) == 0 {
		return errors.New("concept fund flow is unavailable")
	}
	messages := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value.Message) != "" {
			messages = append(messages, value.Provider+": "+value.Message)
		}
	}
	if len(messages) == 0 {
		return errors.New("concept fund flow is unavailable")
	}
	return errors.New(strings.Join(messages, "; "))
}

func themeSourceProviderDisplayName(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "eastmoney", "东方财富":
		return "东方财富概念资金流"
	case "sina", "新浪":
		return "新浪概念资金流"
	case "":
		return "概念资金流"
	default:
		return strings.TrimSpace(value)
	}
}

func themeSourceStance(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "contradicts", "negative", "bearish", "利空", "负面", "反对", "矛盾":
		return themes.ThemeSignalContradicts
	default:
		return themes.ThemeSignalSupports
	}
}

func themeSourceSecurities(values []string) []themes.RawThemeSecurity {
	items := make([]themes.RawThemeSecurity, 0, len(values))
	for _, value := range themeSourceCleanStrings(values) {
		items = append(items, themeSourceSecurity(value, ""))
	}
	return items
}

func themeSourceSecurity(code, name string) themes.RawThemeSecurity {
	code = strings.TrimSpace(code)
	market := ""
	upper := strings.ToUpper(code)
	switch {
	case strings.HasPrefix(upper, "SH") || strings.HasSuffix(upper, ".SH"):
		market = "SH"
	case strings.HasPrefix(upper, "SZ") || strings.HasSuffix(upper, ".SZ"):
		market = "SZ"
	case len(code) == 6 && (strings.HasPrefix(code, "6") || strings.HasPrefix(code, "5")):
		market = "SH"
	case len(code) == 6:
		market = "SZ"
	}
	code = strings.TrimPrefix(strings.TrimPrefix(upper, "SH"), "SZ")
	code = strings.TrimSuffix(strings.TrimSuffix(code, ".SH"), ".SZ")
	return themes.RawThemeSecurity{AssetType: "stock", Market: market, Code: code, Name: strings.TrimSpace(name), Role: "constituent"}
}

func themeSourceSecuritiesFromAnnouncement(row map[string]interface{}) []themes.RawThemeSecurity {
	var securities []themes.RawThemeSecurity
	if columns, ok := themeSourceValue(row, "columns", "stocks", "securities").([]interface{}); ok {
		for _, value := range columns {
			item := themeSourceMap(value)
			code := themeSourceString(item, "stock_code", "stockCode", "SECURITY_CODE", "code")
			name := themeSourceString(item, "short_name", "stockName", "SECURITY_NAME_ABBR", "name")
			if code != "" || name != "" {
				securities = append(securities, themeSourceSecurity(code, name))
			}
		}
	}
	if len(securities) == 0 {
		code := themeSourceString(row, "stock_code", "stockCode", "SECURITY_CODE", "code")
		name := themeSourceString(row, "short_name", "stockName", "SECURITY_NAME_ABBR", "securityName")
		if code != "" || name != "" {
			securities = append(securities, themeSourceSecurity(code, name))
		}
	}
	return securities
}

func themeSourcePositive(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func themeSourceCleanStrings(values []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

func themeSourceMap(value any) map[string]interface{} {
	if value == nil {
		return map[string]interface{}{}
	}
	if row, ok := value.(map[string]interface{}); ok {
		return row
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return map[string]interface{}{}
	}
	row := map[string]interface{}{}
	if err := json.Unmarshal(encoded, &row); err != nil {
		return map[string]interface{}{}
	}
	return row
}

func themeSourceValue(row map[string]interface{}, keys ...string) interface{} {
	for _, key := range keys {
		if value, ok := row[key]; ok && value != nil {
			return value
		}
	}
	for existing, value := range row {
		for _, key := range keys {
			if strings.EqualFold(existing, key) && value != nil {
				return value
			}
		}
	}
	return nil
}

func themeSourceString(row map[string]interface{}, keys ...string) string {
	value := themeSourceValue(row, keys...)
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(typed), 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	default:
		return ""
	}
}

func themeSourceFloat(row map[string]interface{}, keys ...string) float64 {
	value := themeSourceValue(row, keys...)
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case json.Number:
		result, _ := typed.Float64()
		return result
	case string:
		result, _ := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(typed, "%")), 64)
		return result
	default:
		return 0
	}
}

func themeSourceTime(row map[string]interface{}, keys ...string) *time.Time {
	return themeSourceParseTime(themeSourceValue(row, keys...))
}

func themeSourceParseTime(value interface{}) *time.Time {
	switch typed := value.(type) {
	case time.Time:
		if typed.IsZero() {
			return nil
		}
		copy := typed
		return &copy
	case *time.Time:
		if typed == nil || typed.IsZero() {
			return nil
		}
		copy := *typed
		return &copy
	case float64:
		return themeSourceUnixTime(int64(typed))
	case int:
		return themeSourceUnixTime(int64(typed))
	case int64:
		return themeSourceUnixTime(typed)
	case json.Number:
		value, err := typed.Int64()
		if err == nil {
			return themeSourceUnixTime(value)
		}
	case string:
		raw := strings.TrimSpace(typed)
		if raw == "" {
			return nil
		}
		if numeric, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return themeSourceUnixTime(numeric)
		}
		location, err := time.LoadLocation("Asia/Shanghai")
		if err != nil {
			location = time.FixedZone("CST", 8*60*60)
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02", "2006/01/02 15:04:05"} {
			parsed, err := time.ParseInLocation(layout, raw, location)
			if err == nil {
				return &parsed
			}
		}
	}
	return nil
}

func themeSourceUnixTime(value int64) *time.Time {
	if value <= 0 {
		return nil
	}
	if value > 1_000_000_000_000 {
		value /= 1000
	}
	parsed := time.Unix(value, 0)
	return &parsed
}

func hashThemeSourcePayload(value interface{}) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		encoded = []byte(fmt.Sprintf("%v", value))
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
