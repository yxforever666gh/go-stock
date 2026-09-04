package data

import (
	"context"
	"errors"
	"hash/fnv"
	"math"
	"math/bits"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/marketdata"
	"go-stock/backend/models"

	"github.com/go-ego/gse"
	"golang.org/x/text/unicode/norm"
	"gorm.io/gorm"
)

const (
	defaultHotWordsHours        = 24
	defaultHotWordsBaselineDays = 7
	defaultHotWordsLimit        = 30
	hotWordsMaximumRows         = 50000
	hotWordsCacheTTL            = 5 * time.Minute
	hotWordsNearDuplicateWindow = 6 * time.Hour
)

type HotWordsQuery struct {
	Hours        int
	BaselineDays int
	Limit        int
}

func (q HotWordsQuery) Normalize() HotWordsQuery {
	if q.Hours == 0 {
		q.Hours = defaultHotWordsHours
	}
	if q.BaselineDays == 0 {
		q.BaselineDays = defaultHotWordsBaselineDays
	}
	if q.Limit == 0 {
		q.Limit = defaultHotWordsLimit
	}
	return q
}

type HotWordsWindow struct {
	From  time.Time `json:"from"`
	To    time.Time `json:"to"`
	Hours int       `json:"hours"`
}

type HotWordsBaseline struct {
	From          time.Time `json:"from"`
	To            time.Time `json:"to"`
	RequestedDays int       `json:"requestedDays"`
	EffectiveDays int       `json:"effectiveDays"`
	DocumentCount int       `json:"documentCount"`
	Available     bool      `json:"available"`
	Mode          string    `json:"mode"`
}

type HotWordsSentiment struct {
	Score         float64              `json:"score"`
	Category      models.SentimentType `json:"category"`
	Description   string               `json:"description"`
	PositiveCount int                  `json:"positiveCount"`
	NegativeCount int                  `json:"negativeCount"`
}

type HotWordRepresentativeNews struct {
	ID          uint      `json:"id"`
	Title       string    `json:"title"`
	Excerpt     string    `json:"excerpt"`
	Source      string    `json:"source"`
	PublishedAt time.Time `json:"publishedAt"`
	URL         string    `json:"url,omitempty"`
}

type HotWordItem struct {
	Rank                  int                         `json:"rank"`
	Word                  string                      `json:"word"`
	Score                 float64                     `json:"score"`
	DocumentCount         int                         `json:"documentCount"`
	OccurrenceCount       int                         `json:"occurrenceCount"`
	DocumentShare         float64                     `json:"documentShare"`
	BaselineDocumentCount int                         `json:"baselineDocumentCount"`
	BurstRatio            *float64                    `json:"burstRatio"`
	GrowthPct             *float64                    `json:"growthPct"`
	SourceCount           int                         `json:"sourceCount"`
	Sources               []string                    `json:"sources"`
	LatestAt              time.Time                   `json:"latestAt"`
	Confidence            string                      `json:"confidence"`
	RepresentativeNews    []HotWordRepresentativeNews `json:"representativeNews"`
}

type HotWordsData struct {
	Window               HotWordsWindow    `json:"window"`
	Baseline             HotWordsBaseline  `json:"baseline"`
	CurrentDocumentCount int               `json:"currentDocumentCount"`
	Sentiment            HotWordsSentiment `json:"sentiment"`
	Items                []HotWordItem     `json:"items"`
}

type hotWordsCacheEntry struct {
	expiresAt time.Time
	value     marketdata.DataEnvelope[HotWordsData]
}

type hotWordsFlight struct {
	done  chan struct{}
	value marketdata.DataEnvelope[HotWordsData]
}

type MarketHotWordsService struct {
	database *gorm.DB
	now      func() time.Time
	analyzer *hotWordAnalyzer

	cacheMu sync.Mutex
	cache   map[string]hotWordsCacheEntry
	flights map[string]*hotWordsFlight
}

func NewMarketHotWordsService() *MarketHotWordsService {
	return NewMarketHotWordsServiceWithDB(db.Dao, time.Now)
}

func NewMarketHotWordsServiceWithDB(database *gorm.DB, now func() time.Time) *MarketHotWordsService {
	if now == nil {
		now = time.Now
	}
	return &MarketHotWordsService{
		database: database,
		now:      now,
		analyzer: newHotWordAnalyzer(database),
		cache:    make(map[string]hotWordsCacheEntry),
		flights:  make(map[string]*hotWordsFlight),
	}
}

func (s *MarketHotWordsService) HotWords(ctx context.Context, query HotWordsQuery) marketdata.DataEnvelope[HotWordsData] {
	query = query.Normalize()
	key := strconv.Itoa(query.Hours) + ":" + strconv.Itoa(query.BaselineDays) + ":" + strconv.Itoa(query.Limit)
	now := s.now()

	s.cacheMu.Lock()
	if cached, ok := s.cache[key]; ok && now.Before(cached.expiresAt) {
		s.cacheMu.Unlock()
		return cached.value
	}
	if flight, ok := s.flights[key]; ok {
		s.cacheMu.Unlock()
		select {
		case <-ctx.Done():
			return unavailableHotWords(now, query, "request_cancelled", ctx.Err())
		case <-flight.done:
			return flight.value
		}
	}
	flight := &hotWordsFlight{done: make(chan struct{})}
	s.flights[key] = flight
	s.cacheMu.Unlock()

	startedAt := time.Now()
	value := s.compute(ctx, query, now)
	logger.SugaredLogger.Infof(
		"热词分析完成 duration=%s current=%d baseline=%d candidates=%d mode=%s status=%s",
		time.Since(startedAt), value.Data.CurrentDocumentCount, value.Data.Baseline.DocumentCount,
		len(value.Data.Items), value.Data.Baseline.Mode, value.Status,
	)

	s.cacheMu.Lock()
	flight.value = value
	s.cache[key] = hotWordsCacheEntry{expiresAt: now.Add(hotWordsCacheTTL), value: value}
	delete(s.flights, key)
	close(flight.done)
	s.cacheMu.Unlock()
	return value
}

func (s *MarketHotWordsService) compute(ctx context.Context, query HotWordsQuery, now time.Time) marketdata.DataEnvelope[HotWordsData] {
	currentFrom := now.Add(-time.Duration(query.Hours) * time.Hour)
	baselineFrom := currentFrom.Add(-time.Duration(query.BaselineDays) * 24 * time.Hour)
	data := HotWordsData{
		Window: HotWordsWindow{From: currentFrom, To: now, Hours: query.Hours},
		Baseline: HotWordsBaseline{From: baselineFrom, To: currentFrom, RequestedDays: query.BaselineDays,
			Mode: "coverage_fallback"},
		Items: []HotWordItem{},
	}
	if s.database == nil {
		return unavailableHotWordsWithData(now, data, "database_unavailable", errors.New("market news database is not initialized"))
	}

	currentRows, currentTruncated, err := s.loadWindow(ctx, currentFrom, now, true)
	if err != nil {
		return unavailableHotWordsWithData(now, data, "hot_words_query_failed", err)
	}
	currentDocuments := dedupeHotWordNews(currentRows)
	data.CurrentDocumentCount = len(currentDocuments)
	if len(currentDocuments) == 0 {
		status := marketdata.StatusEmpty
		warnings := []string{"最近窗口内没有可分析的新闻"}
		if fetchErr := marketNewsFetchFailureForWindow(nil, currentFrom, now); fetchErr != nil {
			return unavailableHotWordsWithData(now, data, "market_news_fetch_failed", fetchErr)
		}
		return marketdata.DataEnvelope[HotWordsData]{Data: data, Source: "market_news", FetchedAt: now,
			Status: status, Errors: []marketdata.DataError{}, Warnings: warnings}
	}

	baselineRows, baselineTruncated, baselineErr := s.loadWindow(ctx, baselineFrom, currentFrom, false)
	baselineDocuments := dedupeHotWordNews(baselineRows)
	data.Baseline.DocumentCount = len(baselineDocuments)
	data.Baseline.EffectiveDays = qualifyingHotWordDays(baselineDocuments)
	data.Baseline.Available = hotWordBaselineAvailable(baselineDocuments, baselineTruncated, baselineErr)
	if data.Baseline.Available {
		data.Baseline.Mode = "burst"
	}

	currentStats, sentiment := s.analyzeDocuments(currentDocuments, now)
	data.Sentiment = sentiment
	baselineStats, _ := s.analyzeDocuments(baselineDocuments, now)
	data.Items = rankHotWords(currentStats, baselineStats, currentDocuments, len(currentDocuments), len(baselineDocuments), data.Baseline.Available, query.Limit, now)

	warnings := make([]string, 0, 4)
	status := marketdata.StatusOK
	if !data.Baseline.Available {
		status = marketdata.StatusPartial
		warnings = append(warnings, "历史基线不足，当前按最近窗口覆盖量排序")
	}
	if baselineErr != nil {
		status = marketdata.StatusPartial
		warnings = append(warnings, "历史基线读取失败："+baselineErr.Error())
	}
	if currentTruncated || baselineTruncated {
		status = marketdata.StatusPartial
		warnings = append(warnings, "新闻窗口超过 50000 条，热词分析使用了最新的 50000 条记录")
	}
	sourceStates, sourceNames, asOf := hotWordSourceStates(currentDocuments)
	if len(sourceNames) == 1 {
		status = marketdata.StatusPartial
		warnings = append(warnings, "最近窗口新闻仅来自一个来源，来源置信度有限")
	}
	return marketdata.DataEnvelope[HotWordsData]{
		Data: data, Source: "market_news", AsOf: asOf, FetchedAt: now, Status: status,
		Errors: []marketdata.DataError{}, Sources: sourceStates, Warnings: warnings,
		EvidenceProfile: "market_hot_words_v1",
	}
}

func unavailableHotWords(now time.Time, query HotWordsQuery, code string, err error) marketdata.DataEnvelope[HotWordsData] {
	currentFrom := now.Add(-time.Duration(query.Hours) * time.Hour)
	return unavailableHotWordsWithData(now, HotWordsData{
		Window: HotWordsWindow{From: currentFrom, To: now, Hours: query.Hours},
		Baseline: HotWordsBaseline{From: currentFrom.Add(-time.Duration(query.BaselineDays) * 24 * time.Hour), To: currentFrom,
			RequestedDays: query.BaselineDays, Mode: "coverage_fallback"}, Items: []HotWordItem{},
	}, code, err)
}

func unavailableHotWordsWithData(now time.Time, data HotWordsData, code string, err error) marketdata.DataEnvelope[HotWordsData] {
	message := "热词分析不可用"
	if err != nil {
		message = err.Error()
	}
	return marketdata.DataEnvelope[HotWordsData]{Data: data, Source: "market_news", FetchedAt: now,
		Status: marketdata.StatusUnavailable, Errors: []marketdata.DataError{{Provider: "market_news", Code: code, Message: message}}}
}

func (s *MarketHotWordsService) loadWindow(ctx context.Context, from, to time.Time, inclusiveEnd bool) ([]*models.Telegraph, bool, error) {
	zeroTimeCutoff := time.Date(2, time.January, 1, 0, 0, 0, 0, time.UTC)
	endOperator := "<"
	if inclusiveEnd {
		endOperator = "<="
	}
	predicate := `(
		(data_time IS NOT NULL AND data_time > ? AND data_time >= ? AND data_time ` + endOperator + ` ?)
		OR
		((data_time IS NULL OR data_time <= ?) AND created_at >= ? AND created_at ` + endOperator + ` ?)
	)`
	rows := make([]*models.Telegraph, 0, 1024)
	err := s.database.WithContext(ctx).Model(&models.Telegraph{}).Preload("TelegraphTags").
		Where(predicate, zeroTimeCutoff, from, to, zeroTimeCutoff, from, to).
		Order("data_time DESC, is_red DESC, created_at DESC, id DESC").Limit(hotWordsMaximumRows + 1).Find(&rows).Error
	if err != nil {
		return nil, false, err
	}
	rows, truncated := limitHotWordRows(rows)
	if err := hydrateHotWordTags(s.database.WithContext(ctx), rows); err != nil {
		return nil, truncated, err
	}
	return rows, truncated, nil
}

func hydrateHotWordTags(database *gorm.DB, rows []*models.Telegraph) error {
	tagIDs := make([]uint, 0)
	seen := make(map[uint]struct{})
	for _, row := range rows {
		for _, link := range row.TelegraphTags {
			if _, ok := seen[link.TagId]; ok {
				continue
			}
			seen[link.TagId] = struct{}{}
			tagIDs = append(tagIDs, link.TagId)
		}
	}
	if len(tagIDs) == 0 {
		return nil
	}
	tags := make([]models.Tags, 0, len(tagIDs))
	if err := database.Model(&models.Tags{}).Where("id IN ?", tagIDs).Find(&tags).Error; err != nil {
		return err
	}
	byID := make(map[uint]string, len(tags))
	for _, tag := range tags {
		byID[tag.ID] = tag.Name
	}
	for _, row := range rows {
		row.SubjectTags = row.SubjectTags[:0]
		for _, link := range row.TelegraphTags {
			if name := strings.TrimSpace(byID[link.TagId]); name != "" {
				row.SubjectTags = append(row.SubjectTags, name)
			}
		}
	}
	return nil
}

type hotWordDocument struct {
	news       *models.Telegraph
	eventAt    time.Time
	normalized string
	simhash    uint64
	sources    map[string]struct{}
	tags       map[string]struct{}
}

func dedupeHotWordNews(rows []*models.Telegraph) []hotWordDocument {
	documents := make([]hotWordDocument, 0, len(rows))
	exact := make(map[string]int, len(rows))
	buckets := [4]map[uint16][]int{{}, {}, {}, {}}
	for _, row := range rows {
		if row == nil {
			continue
		}
		text := strings.TrimSpace(row.Content)
		if text == "" {
			text = strings.TrimSpace(row.Title)
		}
		normalized := normalizeHotWordText(text)
		if normalized == "" {
			continue
		}
		eventAt := hotWordEventTime(row)
		if index, ok := exact[normalized]; ok {
			mergeHotWordDocument(&documents[index], row, eventAt)
			continue
		}
		hash := hotWordSimHash(normalized)
		nearIndex := -1
		candidateIndexes := make(map[int]struct{})
		for block := 0; block < 4; block++ {
			key := uint16(hash >> (block * 16))
			for _, index := range buckets[block][key] {
				candidateIndexes[index] = struct{}{}
			}
		}
		for index := range candidateIndexes {
			candidate := &documents[index]
			if hotWordNearDuplicate(candidate, normalized, hash, eventAt) {
				nearIndex = index
				break
			}
		}
		if nearIndex >= 0 {
			exact[normalized] = nearIndex
			mergeHotWordDocument(&documents[nearIndex], row, eventAt)
			continue
		}
		document := hotWordDocument{news: row, eventAt: eventAt, normalized: normalized, simhash: hash,
			sources: map[string]struct{}{}, tags: map[string]struct{}{}}
		mergeHotWordDocument(&document, row, eventAt)
		index := len(documents)
		documents = append(documents, document)
		exact[normalized] = index
		for block := 0; block < 4; block++ {
			key := uint16(hash >> (block * 16))
			buckets[block][key] = append(buckets[block][key], index)
		}
	}
	return documents
}

func mergeHotWordDocument(document *hotWordDocument, row *models.Telegraph, eventAt time.Time) {
	if source := strings.TrimSpace(row.Source); source != "" {
		document.sources[source] = struct{}{}
	}
	for _, tag := range append(append([]string{}, row.SubjectTags...), row.StocksTags...) {
		if tag = strings.TrimSpace(tag); tag != "" {
			document.tags[tag] = struct{}{}
		}
	}
	if document.news == nil || betterHotWordRepresentative(row, eventAt, document.news, document.eventAt) {
		document.news = row
		document.eventAt = eventAt
	}
}

func betterHotWordRepresentative(candidate *models.Telegraph, candidateAt time.Time, current *models.Telegraph, currentAt time.Time) bool {
	if candidate.IsRed != current.IsRed {
		return candidate.IsRed
	}
	if (strings.TrimSpace(candidate.Title) != "") != (strings.TrimSpace(current.Title) != "") {
		return strings.TrimSpace(candidate.Title) != ""
	}
	if (strings.TrimSpace(candidate.Url) != "") != (strings.TrimSpace(current.Url) != "") {
		return strings.TrimSpace(candidate.Url) != ""
	}
	return candidateAt.After(currentAt)
}

func hotWordNearDuplicate(candidate *hotWordDocument, normalized string, hash uint64, eventAt time.Time) bool {
	if candidate == nil || candidate.eventAt.IsZero() || eventAt.IsZero() || absDuration(candidate.eventAt.Sub(eventAt)) > hotWordsNearDuplicateWindow {
		return false
	}
	left, right := utf8.RuneCountInString(candidate.normalized), utf8.RuneCountInString(normalized)
	if left == 0 || right == 0 {
		return false
	}
	ratio := float64(left) / float64(right)
	if ratio < 0.85 || ratio > 1.18 {
		return false
	}
	return bits.OnesCount64(candidate.simhash^hash) <= 3
}

func absDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}

func normalizeHotWordText(value string) string {
	value = strings.ToLower(norm.NFKC.String(value))
	var builder strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Han, r) {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func hotWordSimHash(value string) uint64 {
	runes := []rune(value)
	if len(runes) == 0 {
		return 0
	}
	weights := [64]int{}
	width := 3
	if len(runes) < width {
		width = len(runes)
	}
	for index := 0; index+width <= len(runes); index++ {
		hasher := fnv.New64a()
		_, _ = hasher.Write([]byte(string(runes[index : index+width])))
		hash := hasher.Sum64()
		for bit := 0; bit < 64; bit++ {
			if hash&(uint64(1)<<bit) != 0 {
				weights[bit]++
			} else {
				weights[bit]--
			}
		}
	}
	var result uint64
	for bit, weight := range weights {
		if weight >= 0 {
			result |= uint64(1) << bit
		}
	}
	return result
}

func hotWordEventTime(item *models.Telegraph) time.Time {
	if item != nil && item.DataTime != nil && !item.DataTime.IsZero() && item.DataTime.After(time.Date(2, time.January, 1, 0, 0, 0, 0, time.UTC)) {
		return *item.DataTime
	}
	if item == nil {
		return time.Time{}
	}
	return item.CreatedAt
}

type hotWordAnalyzer struct {
	segmenter gse.Segmenter
	explicit  map[string]string
	aliases   map[string]string
	stopWords map[string]struct{}
}

func newHotWordAnalyzer(database *gorm.DB) *hotWordAnalyzer {
	analyzer := &hotWordAnalyzer{
		explicit: make(map[string]string),
		aliases: map[string]string{
			"ai": "人工智能", "a.i": "人工智能", "新能源车": "新能源汽车", "新能源车辆": "新能源汽车",
			"a股市场": "A股", "沪深股市": "A股", "chat gpt": "ChatGPT",
		},
		stopWords: hotWordStopWords(),
	}
	if err := analyzer.segmenter.LoadDictEmbed("zh_s"); err != nil {
		logger.SugaredLogger.Warnf("加载热词通用词典失败: %v", err)
	}
	if err := analyzer.segmenter.LoadDictEmbed(baseDict); err != nil {
		logger.SugaredLogger.Warnf("加载热词金融词典失败: %v", err)
	}
	analyzer.addExplicitDictionary(baseDict)
	analyzer.loadDynamicDictionary(database)
	analyzer.loadUserDictionary("data/dict/user.txt")
	analyzer.segmenter.CalcToken()
	return analyzer
}

func (a *hotWordAnalyzer) addExplicitDictionary(dictionary string) {
	for _, line := range strings.Split(dictionary, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 0 {
			a.addExplicit(fields[0])
		}
	}
}

func (a *hotWordAnalyzer) addExplicit(word string) {
	word = strings.TrimSpace(norm.NFKC.String(word))
	if word == "" {
		return
	}
	key := strings.ToLower(word)
	a.explicit[key] = word
	_, _, found := a.segmenter.Find(word)
	var err error
	if found {
		err = a.segmenter.ReAddToken(word, basefreq+100, "nz")
	} else {
		err = a.segmenter.AddToken(word, basefreq+100, "nz")
	}
	if err != nil {
		logger.SugaredLogger.Debugf("添加热词词典项 %s 失败: %v", word, err)
	}
}

func (a *hotWordAnalyzer) loadDynamicDictionary(database *gorm.DB) {
	if database == nil {
		return
	}
	if database.Migrator().HasTable(&models.StockBasic{}) {
		stocks := make([]models.StockBasic, 0)
		if err := database.Model(&models.StockBasic{}).Find(&stocks).Error; err == nil {
			for _, stock := range stocks {
				a.addExplicit(stock.Name)
				a.addExplicit(stock.BKName)
			}
		}
	}
	if database.Migrator().HasTable(&models.StockInfoHK{}) {
		stocks := make([]models.StockInfoHK, 0)
		if err := database.Model(&models.StockInfoHK{}).Find(&stocks).Error; err == nil {
			for _, stock := range stocks {
				a.addExplicit(stock.Name)
				a.addExplicit(stock.BKName)
			}
		}
	}
	if database.Migrator().HasTable(&models.Tags{}) {
		tags := make([]models.Tags, 0)
		if err := database.Model(&models.Tags{}).Find(&tags).Error; err == nil {
			for _, tag := range tags {
				a.addExplicit(tag.Name)
			}
		}
	}
}

func (a *hotWordAnalyzer) loadUserDictionary(path string) {
	content, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 0 {
			a.addExplicit(fields[0])
		}
	}
}

func hotWordStopWords() map[string]struct{} {
	words := []string{
		"公司", "上市公司", "有限公司", "公告", "股东", "市场", "其他", "焦点", "观点", "数据", "行业", "原创", "国际", "表示", "消息", "记者", "报道",
		"目前", "今日", "昨日", "近日", "相关", "进行", "已经", "其中", "以及", "对于", "由于", "通过", "预计", "同比",
		"上年", "今年", "一个", "一种", "方面", "情况", "问题", "工作", "时间", "内容", "可能", "没有", "成为", "超过",
		"经常性", "损益", "股本", "股份", "红利", "公积金", "计划", "报告", "项目", "企业", "业务", "产品",
		"新闻", "集团", "年度", "基本", "上市", "事项", "会议", "董事会",
	}
	result := make(map[string]struct{}, len(words))
	for _, word := range words {
		result[strings.ToLower(norm.NFKC.String(word))] = struct{}{}
	}
	return result
}

func (a *hotWordAnalyzer) extract(text string, tags map[string]struct{}) map[string]hotWordTermCount {
	result := make(map[string]hotWordTermCount)
	text = norm.NFKC.String(text)
	for _, token := range a.segmenter.Pos(text) {
		a.addCandidate(result, token.Text, token.Pos, 1, false)
	}
	for tag := range tags {
		a.addCandidate(result, tag, "nz", 1, true)
	}
	return result
}

type hotWordTermCount struct {
	display string
	count   int
}

func (a *hotWordAnalyzer) addCandidate(result map[string]hotWordTermCount, word, pos string, count int, force bool) {
	word = strings.TrimSpace(strings.Trim(word, "\t\r\n ，。！？；：、,.!?;:()（）[]【】{}<>《》\"'"))
	if word == "" {
		return
	}
	key := strings.ToLower(norm.NFKC.String(word))
	if alias, ok := a.aliases[key]; ok {
		word = alias
		key = strings.ToLower(alias)
	}
	display, explicit := a.explicit[key]
	if !explicit {
		display = word
	}
	if _, stopped := a.stopWords[key]; stopped || !validHotWordToken(word, explicit || force) {
		return
	}
	if !explicit && !force && !(strings.HasPrefix(pos, "n") || pos == "eng") {
		return
	}
	current := result[key]
	if current.display == "" {
		current.display = display
	}
	current.count += count
	result[key] = current
}

func validHotWordToken(word string, explicit bool) bool {
	if strings.HasPrefix(strings.ToLower(word), "http") || strings.HasPrefix(strings.ToLower(word), "www") {
		return false
	}
	runes := []rune(word)
	if len(runes) < 2 {
		return false
	}
	hasHan, hasLatin, hasOtherLetter := false, false, false
	for _, r := range runes {
		switch {
		case unicode.Is(unicode.Han, r):
			hasHan = true
		case unicode.IsLetter(r) && r <= unicode.MaxASCII:
			hasLatin = true
		case unicode.IsLetter(r):
			hasOtherLetter = true
		}
	}
	if !hasHan && !hasLatin && !hasOtherLetter {
		return false
	}
	if explicit {
		return len(runes) <= 24
	}
	if hasHan {
		return len(runes) <= 12
	}
	return len(runes) <= 24
}

type hotWordStats struct {
	display       string
	documentCount int
	occurrences   int
	recencySum    float64
	latestAt      time.Time
	sources       map[string]struct{}
	documentIDs   []int
}

func (s *MarketHotWordsService) analyzeDocuments(documents []hotWordDocument, now time.Time) (map[string]*hotWordStats, HotWordsSentiment) {
	stats := make(map[string]*hotWordStats)
	positiveCount, negativeCount := 0, 0
	for index := range documents {
		document := &documents[index]
		text := strings.TrimSpace(strings.Join([]string{document.news.Title, document.news.Content}, "\n"))
		terms := s.analyzer.extract(text, document.tags)
		ageHours := now.Sub(document.eventAt).Hours()
		if ageHours < 0 {
			ageHours = 0
		}
		decay := math.Exp(-math.Ln2 * ageHours / 12)
		for key, term := range terms {
			entry := stats[key]
			if entry == nil {
				entry = &hotWordStats{display: term.display, sources: make(map[string]struct{})}
				stats[key] = entry
			}
			entry.documentCount++
			entry.occurrences += term.count
			entry.recencySum += decay
			entry.documentIDs = append(entry.documentIDs, index)
			if document.eventAt.After(entry.latestAt) {
				entry.latestAt = document.eventAt
			}
			for source := range document.sources {
				entry.sources[source] = struct{}{}
			}
		}
		_, positive, negative := calculateScore(s.analyzer.segmenter.Cut(text, true))
		positiveCount += positive
		negativeCount += negative
	}
	return stats, normalizedHotWordsSentiment(positiveCount, negativeCount)
}

func normalizedHotWordsSentiment(positiveCount, negativeCount int) HotWordsSentiment {
	total := positiveCount + negativeCount
	score := 0.0
	if total > 0 {
		score = 100 * float64(positiveCount-negativeCount) / float64(total)
	}
	score = math.Max(-100, math.Min(100, score))
	category := Neutral
	if score > 10 {
		category = Positive
	} else if score < -10 {
		category = Negative
	}
	return HotWordsSentiment{Score: score, Category: category, Description: GetSentimentDescription(category),
		PositiveCount: positiveCount, NegativeCount: negativeCount}
}

func rankHotWords(current, baseline map[string]*hotWordStats, documents []hotWordDocument, currentDocuments, baselineDocuments int, baselineAvailable bool, limit int, now time.Time) []HotWordItem {
	items := make([]HotWordItem, 0, len(current))
	for key, stats := range current {
		if stats.documentCount < 2 {
			continue
		}
		recency := 0.5 + 0.5*(stats.recencySum/float64(stats.documentCount))
		score := math.Log1p(float64(stats.documentCount)) * recency
		baselineCount := 0
		var burstRatio, growthPct *float64
		if baselineStats := baseline[key]; baselineStats != nil {
			baselineCount = baselineStats.documentCount
		}
		if baselineAvailable && currentDocuments > 0 && baselineDocuments > 0 {
			ratio := ((float64(stats.documentCount) + 0.5) / (float64(currentDocuments) + 1)) /
				((float64(baselineCount) + 0.5) / (float64(baselineDocuments) + 1))
			ratio = math.Min(20, ratio)
			growth := (ratio - 1) * 100
			burstRatio, growthPct = &ratio, &growth
			score *= 1 + math.Log1p(math.Max(0, ratio-1))
		}
		sources := sortedHotWordSources(stats.sources)
		confidence := "low"
		if stats.documentCount >= 3 {
			confidence = "medium"
		}
		if baselineAvailable && stats.documentCount >= 5 && len(sources) >= 2 {
			confidence = "high"
		}
		items = append(items, HotWordItem{
			Word: stats.display, Score: score, DocumentCount: stats.documentCount, OccurrenceCount: stats.occurrences,
			DocumentShare: float64(stats.documentCount) / float64(currentDocuments), BaselineDocumentCount: baselineCount,
			BurstRatio: burstRatio, GrowthPct: growthPct, SourceCount: len(sources), Sources: sources,
			LatestAt: stats.latestAt, Confidence: confidence,
			RepresentativeNews: representativeHotWordNews(stats, documents, stats.display, now),
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if math.Abs(items[i].Score-items[j].Score) > 1e-12 {
			return items[i].Score > items[j].Score
		}
		if items[i].DocumentCount != items[j].DocumentCount {
			return items[i].DocumentCount > items[j].DocumentCount
		}
		if !items[i].LatestAt.Equal(items[j].LatestAt) {
			return items[i].LatestAt.After(items[j].LatestAt)
		}
		return items[i].Word < items[j].Word
	})
	if len(items) > limit {
		items = items[:limit]
	}
	for index := range items {
		items[index].Rank = index + 1
		items[index].Score = math.Round(items[index].Score*1e6) / 1e6
		items[index].DocumentShare = math.Round(items[index].DocumentShare*1e6) / 1e6
	}
	return items
}

func representativeHotWordNews(stats *hotWordStats, documents []hotWordDocument, word string, _ time.Time) []HotWordRepresentativeNews {
	candidates := append([]int(nil), stats.documentIDs...)
	sort.Slice(candidates, func(i, j int) bool {
		left, right := documents[candidates[i]], documents[candidates[j]]
		if left.news.IsRed != right.news.IsRed {
			return left.news.IsRed
		}
		return left.eventAt.After(right.eventAt)
	})
	selected := make([]int, 0, 3)
	used := make(map[string]struct{})
	chosen := make(map[int]struct{})
	for _, index := range candidates {
		if len(selected) == 3 {
			break
		}
		freshSource := false
		for source := range documents[index].sources {
			if _, exists := used[source]; !exists {
				freshSource = true
				break
			}
		}
		if !freshSource {
			continue
		}
		selected = append(selected, index)
		chosen[index] = struct{}{}
		for source := range documents[index].sources {
			used[source] = struct{}{}
		}
	}
	for _, index := range candidates {
		if len(selected) == 3 {
			break
		}
		if _, exists := chosen[index]; exists {
			continue
		}
		selected = append(selected, index)
	}
	result := make([]HotWordRepresentativeNews, 0, len(selected))
	for _, index := range selected {
		document := documents[index]
		title := strings.TrimSpace(document.news.Title)
		if title == "" {
			title = truncateHotWordRunes(strings.TrimSpace(document.news.Content), 48)
		}
		sources := sortedHotWordSources(document.sources)
		source := strings.TrimSpace(document.news.Source)
		if source == "" && len(sources) > 0 {
			source = sources[0]
		}
		result = append(result, HotWordRepresentativeNews{ID: document.news.ID, Title: title,
			Excerpt: hotWordExcerpt(document.news.Content, word, 120), Source: source,
			PublishedAt: document.eventAt, URL: strings.TrimSpace(document.news.Url)})
	}
	return result
}

func hotWordExcerpt(content, word string, limit int) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	runes := []rune(content)
	if len(runes) <= limit {
		return content
	}
	index := strings.Index(strings.ToLower(content), strings.ToLower(word))
	if index < 0 {
		return truncateHotWordRunes(content, limit)
	}
	startRune := utf8.RuneCountInString(content[:index]) - limit/3
	if startRune < 0 {
		startRune = 0
	}
	endRune := startRune + limit
	if endRune > len(runes) {
		endRune = len(runes)
		startRune = max(0, endRune-limit)
	}
	prefix, suffix := "", ""
	if startRune > 0 {
		prefix = "…"
	}
	if endRune < len(runes) {
		suffix = "…"
	}
	return prefix + string(runes[startRune:endRune]) + suffix
}

func truncateHotWordRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

func sortedHotWordSources(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func qualifyingHotWordDays(documents []hotWordDocument) int {
	counts := make(map[string]int)
	location := cnLocation()
	for _, document := range documents {
		if document.eventAt.IsZero() {
			continue
		}
		counts[document.eventAt.In(location).Format("2006-01-02")]++
	}
	qualifying := 0
	for _, count := range counts {
		if count >= 50 {
			qualifying++
		}
	}
	return qualifying
}

func hotWordBaselineAvailable(documents []hotWordDocument, truncated bool, err error) bool {
	return err == nil && !truncated && len(documents) >= 500 && qualifyingHotWordDays(documents) >= 3
}

func limitHotWordRows(rows []*models.Telegraph) ([]*models.Telegraph, bool) {
	if len(rows) <= hotWordsMaximumRows {
		return rows, false
	}
	return rows[:hotWordsMaximumRows], true
}

func hotWordSourceStates(documents []hotWordDocument) ([]marketdata.SourceState, []string, time.Time) {
	latest := make(map[string]time.Time)
	var asOf time.Time
	for _, document := range documents {
		if document.eventAt.After(asOf) {
			asOf = document.eventAt
		}
		for source := range document.sources {
			if document.eventAt.After(latest[source]) {
				latest[source] = document.eventAt
			}
		}
	}
	names := make([]string, 0, len(latest))
	for source := range latest {
		names = append(names, source)
	}
	sort.Strings(names)
	states := make([]marketdata.SourceState, 0, len(names))
	for _, source := range names {
		states = append(states, marketdata.SourceState{Provider: source, Status: marketdata.StatusOK, AsOf: latest[source]})
	}
	return states, names, asOf
}
