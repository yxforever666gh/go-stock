package data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-stock/backend/marketdata"
	"go-stock/backend/models"
)

const (
	breadthMinimumCoverage = 0.95
	breadthCacheTTL        = 24 * time.Hour
	breadthOverallTimeout  = 36 * time.Second
	breadthDelayTimeout    = 12 * time.Second
	breadthPageSize        = 100
	breadthPageWorkers     = 6
	breadthTencentBatch    = 80
	breadthTencentWorkers  = 6
)

type breadthCacheEntry struct {
	envelope marketdata.DataEnvelope[BreadthData]
	storedAt time.Time
}

type breadthObservation struct {
	key       string
	code      string
	name      string
	current   float64
	changePct float64
	high      float64
	low       float64
	currentOK bool
	changeOK  bool
	highOK    bool
	lowOK     bool
	quoteAt   time.Time
}

type eastmoneyBreadthPayload struct {
	RC   int `json:"rc"`
	Data *struct {
		Total int             `json:"total"`
		Diff  json.RawMessage `json:"diff"`
	} `json:"data"`
}

type eastmoneyBreadthProvider struct{ service *MarketEvidenceService }
type eastmoneyDelayedBreadthProvider struct{ service *MarketEvidenceService }
type tencentBreadthProvider struct{ service *MarketEvidenceService }

func (p *eastmoneyBreadthProvider) Name() string        { return "eastmoney" }
func (p *eastmoneyDelayedBreadthProvider) Name() string { return "eastmoney-delay" }
func (p *tencentBreadthProvider) Name() string          { return "tencent" }

func (s *MarketEvidenceService) collectBreadth(ctx context.Context) marketdata.DataEnvelope[BreadthData] {
	if ctx == nil {
		ctx = context.Background()
	}
	bounded, cancel := context.WithTimeout(ctx, breadthOverallTimeout)
	defer cancel()
	collector := marketdata.ProviderChainCollector[BreadthData]{Providers: []marketdata.Provider[BreadthData]{
		&eastmoneyBreadthProvider{service: s},
		&eastmoneyDelayedBreadthProvider{service: s},
		&tencentBreadthProvider{service: s},
	}}
	envelope := withEvidenceProfile(collector.Collect(bounded, marketdata.ProviderRequest{}))
	envelope.FetchedAt = s.now()
	if usableBreadthEnvelope(envelope) {
		s.storeBreadthSnapshot(envelope)
		return envelope
	}
	return s.staleBreadthSnapshot(envelope)
}

func usableBreadthEnvelope(envelope marketdata.DataEnvelope[BreadthData]) bool {
	return (envelope.Status == marketdata.StatusOK || envelope.Status == marketdata.StatusPartial) && envelope.Data.Total > 0
}

func (s *MarketEvidenceService) storeBreadthSnapshot(envelope marketdata.DataEnvelope[BreadthData]) {
	copyValue := cloneBreadthEnvelope(envelope)
	s.breadthMu.Lock()
	s.breadthCache = &breadthCacheEntry{envelope: copyValue, storedAt: s.now()}
	s.breadthMu.Unlock()
}

func (s *MarketEvidenceService) staleBreadthSnapshot(failed marketdata.DataEnvelope[BreadthData]) marketdata.DataEnvelope[BreadthData] {
	s.breadthMu.RLock()
	if s.breadthCache == nil {
		s.breadthMu.RUnlock()
		return failed
	}
	entry := breadthCacheEntry{envelope: cloneBreadthEnvelope(s.breadthCache.envelope), storedAt: s.breadthCache.storedAt}
	s.breadthMu.RUnlock()
	now := s.now()
	age := now.Sub(entry.storedAt)
	if age < 0 || age > breadthCacheTTL {
		return failed
	}
	stale := entry.envelope
	stale.Status = marketdata.StatusStale
	stale.FetchedAt = now
	stale.Errors = uniqueBreadthErrors(failed.Errors)
	stale.Sources = append(cloneSourceStates(failed.Sources), marketdata.SourceState{
		Provider: "memory-cache", Status: marketdata.StatusStale, AsOf: stale.AsOf, SourceRef: "process-memory",
		Message: fmt.Sprintf("实时来源不可用，返回 %.0f 分钟前的上一成功快照", age.Minutes()),
	})
	stale.Warnings = uniqueBreadthStrings(append(cloneStrings(failed.Warnings), "所有实时来源不可用，已返回24小时内的上一成功快照"))
	stale.EvidenceProfile = marketEvidenceProfile
	return stale
}

func cloneBreadthEnvelope(value marketdata.DataEnvelope[BreadthData]) marketdata.DataEnvelope[BreadthData] {
	clone := value
	clone.Errors = append([]marketdata.DataError(nil), value.Errors...)
	clone.Sources = cloneSourceStates(value.Sources)
	clone.Warnings = cloneStrings(value.Warnings)
	if value.Data.NewHighs != nil {
		n := *value.Data.NewHighs
		clone.Data.NewHighs = &n
	}
	if value.Data.NewLows != nil {
		n := *value.Data.NewLows
		clone.Data.NewLows = &n
	}
	return clone
}

func cloneSourceStates(values []marketdata.SourceState) []marketdata.SourceState {
	result := append([]marketdata.SourceState(nil), values...)
	for index := range result {
		if result[index].AvailableAt != nil {
			value := *result[index].AvailableAt
			result[index].AvailableAt = &value
		}
	}
	return result
}

func cloneStrings(values []string) []string { return append([]string(nil), values...) }

func uniqueBreadthStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func uniqueBreadthErrors(values []marketdata.DataError) []marketdata.DataError {
	seen := make(map[string]struct{}, len(values))
	result := make([]marketdata.DataError, 0, len(values))
	for _, value := range values {
		key := value.Provider + "\x00" + value.Code + "\x00" + value.Message
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

func (p *eastmoneyBreadthProvider) Collect(ctx context.Context, _ marketdata.ProviderRequest) marketdata.ProviderResult[BreadthData] {
	now := p.service.now()
	observations, reportedTotal, err := p.service.fetchEastmoneyBreadthPage(ctx, p.service.urls.breadth, 1, 6000, 1)
	if err != nil {
		return providerFailure[BreadthData](time.Time{}, p.service.urls.breadth, err)
	}
	return buildBreadthProviderResult(observations, reportedTotal, now, p.service.urls.breadth, false, nil)
}

func (p *eastmoneyDelayedBreadthProvider) Collect(ctx context.Context, _ marketdata.ProviderRequest) marketdata.ProviderResult[BreadthData] {
	now := p.service.now()
	if strings.TrimSpace(p.service.urls.breadthDelay) == "" {
		return providerFailure[BreadthData](time.Time{}, p.service.urls.breadthDelay, errors.New("东方财富延迟行情地址未配置"))
	}
	bounded, cancel := context.WithTimeout(ctx, breadthDelayTimeout)
	defer cancel()
	first, reportedTotal, err := p.service.fetchEastmoneyBreadthPage(bounded, p.service.urls.breadthDelay, 1, breadthPageSize, 3)
	if err != nil {
		return providerFailure[BreadthData](time.Time{}, p.service.urls.breadthDelay, err)
	}
	if reportedTotal <= 0 {
		return providerFailure[BreadthData](time.Time{}, p.service.urls.breadthDelay, errors.New("东方财富延迟行情未返回市场总数"))
	}
	pageCount := (reportedTotal + breadthPageSize - 1) / breadthPageSize
	pages := make([][]breadthObservation, pageCount)
	pages[0] = first
	if pageCount > 1 {
		type pageResult struct {
			page int
			rows []breadthObservation
			err  error
		}
		jobs := make(chan int)
		results := make(chan pageResult, pageCount-1)
		workerCount := breadthPageWorkers
		if pageCount-1 < workerCount {
			workerCount = pageCount - 1
		}
		var workers sync.WaitGroup
		workers.Add(workerCount)
		for worker := 0; worker < workerCount; worker++ {
			go func() {
				defer workers.Done()
				for page := range jobs {
					rows, _, pageErr := p.service.fetchEastmoneyBreadthPage(bounded, p.service.urls.breadthDelay, page, breadthPageSize, 3)
					results <- pageResult{page: page, rows: rows, err: pageErr}
				}
			}()
		}
		go func() {
			for page := 2; page <= pageCount; page++ {
				jobs <- page
			}
			close(jobs)
			workers.Wait()
			close(results)
		}()
		failedPages := make([]string, 0)
		for result := range results {
			if result.err != nil {
				failedPages = append(failedPages, fmt.Sprintf("第%d页: %v", result.page, result.err))
				continue
			}
			pages[result.page-1] = result.rows
		}
		observations := dedupeBreadthObservations(pages)
		warnings := make([]string, 0, 1)
		if len(failedPages) > 0 {
			warnings = append(warnings, fmt.Sprintf("%d/%d 个分页失败（%s）", len(failedPages), pageCount, failedPages[0]))
		}
		return buildBreadthProviderResult(observations, reportedTotal, now, p.service.urls.breadthDelay, len(failedPages) > 0, warnings)
	}
	return buildBreadthProviderResult(first, reportedTotal, now, p.service.urls.breadthDelay, false, nil)
}

func (s *MarketEvidenceService) fetchEastmoneyBreadthPage(ctx context.Context, endpoint string, page, pageSize, attempts int) ([]breadthObservation, int, error) {
	if strings.TrimSpace(endpoint) == "" {
		return nil, 0, errors.New("东方财富市场宽度地址未配置")
	}
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		response, err := s.client.R().SetContext(ctx).
			SetHeader("Referer", "https://quote.eastmoney.com/center/gridlist.html").
			SetHeader("User-Agent", marketEvidenceUserAgent()).
			SetQueryParams(map[string]string{
				"pn": strconv.Itoa(page), "pz": strconv.Itoa(pageSize), "po": "1", "np": "1", "fltt": "2", "invt": "2", "fid": "f3",
				"ut": "bd1d9ddb04089700cf9c27f6f7426281", "_": strconv.FormatInt(s.now().UnixMilli(), 10),
				"fs": "m:0+t:6,m:0+t:80,m:1+t:2,m:1+t:23", "fields": "f2,f3,f12,f13,f14,f15,f16,f124",
			}).Get(endpoint)
		if err != nil {
			lastErr = err
		} else if response.StatusCode() >= 400 {
			lastErr = fmt.Errorf("HTTP %d", response.StatusCode())
		} else {
			rows, total, parseErr := parseEastmoneyBreadthRows(response.Body())
			if parseErr == nil {
				return rows, total, nil
			}
			lastErr = parseErr
		}
		if attempt < attempts {
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, 0, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return nil, 0, lastErr
}

func parseEastmoneyBreadthRows(body []byte) ([]breadthObservation, int, error) {
	var payload eastmoneyBreadthPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, 0, err
	}
	if payload.Data == nil || payload.Data.Total <= 0 || len(payload.Data.Diff) == 0 || string(payload.Data.Diff) == "null" {
		return nil, 0, errors.New("empty breadth response")
	}
	var rows []map[string]any
	if err := json.Unmarshal(payload.Data.Diff, &rows); err != nil {
		var keyed map[string]map[string]any
		if keyedErr := json.Unmarshal(payload.Data.Diff, &keyed); keyedErr != nil {
			return nil, payload.Data.Total, fmt.Errorf("decode breadth rows: %w", err)
		}
		keys := make([]string, 0, len(keyed))
		for key := range keyed {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			left, leftErr := strconv.Atoi(keys[i])
			right, rightErr := strconv.Atoi(keys[j])
			if leftErr == nil && rightErr == nil {
				return left < right
			}
			return keys[i] < keys[j]
		})
		for _, key := range keys {
			rows = append(rows, keyed[key])
		}
	}
	observations := make([]breadthObservation, 0, len(rows))
	for _, row := range rows {
		code := anyString(row["f12"])
		if code == "" {
			continue
		}
		market := anyString(row["f13"])
		observation := breadthObservation{key: market + ":" + code, code: code, name: anyString(row["f14"])}
		observation.current, observation.currentOK = anyFloat(row["f2"])
		observation.changePct, observation.changeOK = anyFloat(row["f3"])
		observation.high, observation.highOK = anyFloat(row["f15"])
		observation.low, observation.lowOK = anyFloat(row["f16"])
		if timestamp, ok := anyFloat(row["f124"]); ok && timestamp > 0 {
			observation.quoteAt = time.Unix(int64(timestamp), 0).In(shanghaiDataLocation())
		}
		observations = append(observations, observation)
	}
	if len(observations) == 0 {
		return nil, payload.Data.Total, errors.New("empty breadth response")
	}
	return observations, payload.Data.Total, nil
}

func parseEastmoneyBreadth(body []byte) (BreadthData, int, time.Time, error) {
	rows, total, err := parseEastmoneyBreadthRows(body)
	if err != nil {
		return BreadthData{}, total, time.Time{}, err
	}
	data, asOf, changeSamples := calculateBreadth(rows)
	if changeSamples == 0 {
		return BreadthData{}, total, asOf, errors.New("breadth response has no comparable change samples")
	}
	return data, total, asOf, nil
}

func dedupeBreadthObservations(pages [][]breadthObservation) []breadthObservation {
	seen := make(map[string]struct{})
	result := make([]breadthObservation, 0)
	for _, page := range pages {
		for _, row := range page {
			if row.key == "" {
				continue
			}
			if _, exists := seen[row.key]; exists {
				continue
			}
			seen[row.key] = struct{}{}
			result = append(result, row)
		}
	}
	return result
}

func calculateBreadth(rows []breadthObservation) (BreadthData, time.Time, int) {
	result := BreadthData{Total: len(rows)}
	changes := make([]float64, 0, len(rows))
	newHighs, newLows := 0, 0
	highSamples, lowSamples := 0, 0
	var asOf time.Time
	for _, row := range rows {
		if row.quoteAt.After(asOf) {
			asOf = row.quoteAt
		}
		if row.currentOK && row.current > 0 && row.highOK && row.high > 0 {
			highSamples++
			if row.current >= row.high {
				newHighs++
			}
		}
		if row.currentOK && row.current > 0 && row.lowOK && row.low > 0 {
			lowSamples++
			if row.current <= row.low {
				newLows++
			}
		}
		if !row.changeOK {
			continue
		}
		changes = append(changes, row.changePct)
		switch {
		case row.changePct > 0:
			result.Advances++
		case row.changePct < 0:
			result.Declines++
		default:
			result.Flat++
		}
		threshold := breadthLimitThreshold(row.code, row.name)
		if row.changePct >= threshold {
			result.LimitUps++
		}
		if row.changePct <= -threshold {
			result.LimitDowns++
		}
	}
	if highSamples > 0 {
		result.NewHighs = &newHighs
	}
	if lowSamples > 0 {
		result.NewLows = &newLows
	}
	sort.Float64s(changes)
	if len(changes) > 0 {
		middle := len(changes) / 2
		if len(changes)%2 == 0 {
			result.MedianChangePct = (changes[middle-1] + changes[middle]) / 2
		} else {
			result.MedianChangePct = changes[middle]
		}
	}
	return result, asOf, len(changes)
}

func breadthLimitThreshold(code, name string) float64 {
	code = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(code), "sh"), "sz")
	name = strings.ToUpper(strings.TrimSpace(name))
	threshold := 9.8
	if strings.Contains(name, "ST") {
		threshold = 4.8
	}
	if strings.HasPrefix(code, "300") || strings.HasPrefix(code, "301") || strings.HasPrefix(code, "688") || strings.HasPrefix(code, "689") {
		threshold = 19.8
	}
	return threshold
}

func buildBreadthProviderResult(rows []breadthObservation, reportedTotal int, now time.Time, sourceRef string, forcePartial bool, extraWarnings []string) marketdata.ProviderResult[BreadthData] {
	data, asOf, changeSamples := calculateBreadth(rows)
	if reportedTotal <= 0 {
		reportedTotal = data.Total
	}
	coverage := 0.0
	if reportedTotal > 0 {
		coverage = float64(data.Total) / float64(reportedTotal)
	}
	if data.Total == 0 || changeSamples == 0 {
		return providerFailure[BreadthData](time.Time{}, sourceRef, errors.New("市场宽度没有可比较的行情样本"))
	}
	if coverage < breadthMinimumCoverage {
		return providerFailure[BreadthData](time.Time{}, sourceRef, fmt.Errorf("市场宽度覆盖率 %.2f%% 低于 95%%（%d/%d）", coverage*100, data.Total, reportedTotal))
	}
	status := marketdata.StatusOK
	warnings := cloneStrings(extraWarnings)
	if forcePartial || data.Total < reportedTotal {
		status = marketdata.StatusPartial
		warnings = append(warnings, fmt.Sprintf("仅取得 %d/%d 条市场快照", data.Total, reportedTotal))
	}
	if data.NewHighs == nil || data.NewLows == nil {
		status = marketdata.StatusPartial
		warnings = append(warnings, "现价与最高/最低价没有可判定样本，对应新高/新低字段返回 null")
	}
	if asOf.IsZero() {
		status = marketdata.StatusPartial
		warnings = append(warnings, "来源没有提供可验证的行情时间，数据截至时间返回空值")
	}
	available := now
	return marketdata.ProviderResult[BreadthData]{Status: status, AsOf: asOf, AvailableAt: &available, Data: data, SourceRef: sourceRef, Warning: strings.Join(uniqueBreadthStrings(warnings), "；")}
}

func (p *tencentBreadthProvider) Collect(ctx context.Context, _ marketdata.ProviderRequest) marketdata.ProviderResult[BreadthData] {
	now := p.service.now()
	universe, err := p.service.tencentBreadthUniverse(ctx)
	if err != nil {
		return providerFailure[BreadthData](time.Time{}, p.service.urls.breadthTencent, err)
	}
	if len(universe) == 0 {
		return providerFailure[BreadthData](time.Time{}, p.service.urls.breadthTencent, errors.New("腾讯降级源没有可用的沪深上市股票代码"))
	}
	type batchResult struct {
		rows []breadthObservation
		err  error
	}
	batches := make([][]string, 0, (len(universe)+breadthTencentBatch-1)/breadthTencentBatch)
	for start := 0; start < len(universe); start += breadthTencentBatch {
		end := start + breadthTencentBatch
		if end > len(universe) {
			end = len(universe)
		}
		batches = append(batches, universe[start:end])
	}
	jobs := make(chan []string)
	results := make(chan batchResult, len(batches))
	workerCount := breadthTencentWorkers
	if len(batches) < workerCount {
		workerCount = len(batches)
	}
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for worker := 0; worker < workerCount; worker++ {
		go func() {
			defer workers.Done()
			for batch := range jobs {
				rows, batchErr := p.service.fetchTencentBreadthBatch(ctx, batch, 2)
				results <- batchResult{rows: rows, err: batchErr}
			}
		}()
	}
	go func() {
		for _, batch := range batches {
			jobs <- batch
		}
		close(jobs)
		workers.Wait()
		close(results)
	}()
	pages := make([][]breadthObservation, 0, len(batches))
	failed := 0
	var firstErr error
	for result := range results {
		if result.err != nil {
			failed++
			if firstErr == nil {
				firstErr = result.err
			}
			continue
		}
		pages = append(pages, result.rows)
	}
	rows := dedupeBreadthObservations(pages)
	warnings := []string{"腾讯行情为独立降级源，市场宽度按本地主库上市股票清单推导"}
	if failed > 0 {
		warnings = append(warnings, fmt.Sprintf("%d/%d 个腾讯批次失败（%v）", failed, len(batches), firstErr))
	}
	return buildBreadthProviderResult(rows, len(universe), now, p.service.urls.breadthTencent, true, warnings)
}

func (s *MarketEvidenceService) tencentBreadthUniverse(ctx context.Context) ([]string, error) {
	if s.mainDB == nil {
		return nil, errors.New("主数据库不可用，无法构建腾讯市场宽度股票清单")
	}
	var stocks []models.StockBasic
	if err := s.mainDB.WithContext(ctx).Select("ts_code", "symbol", "exchange", "list_status").
		Where("list_status = ?", "L").Find(&stocks).Error; err != nil {
		return nil, fmt.Errorf("读取沪深上市股票清单: %w", err)
	}
	seen := make(map[string]struct{}, len(stocks))
	result := make([]string, 0, len(stocks))
	for _, stock := range stocks {
		code := strings.TrimSpace(stock.Symbol)
		tsCode := strings.ToUpper(strings.TrimSpace(stock.TsCode))
		if code == "" && len(tsCode) >= 6 {
			code = tsCode[:6]
		}
		if len(code) != 6 {
			continue
		}
		exchange := strings.ToUpper(strings.TrimSpace(stock.Exchange))
		prefix := ""
		switch {
		case exchange == "SSE" || exchange == "SH" || strings.HasSuffix(tsCode, ".SH"):
			if strings.HasPrefix(code, "6") {
				prefix = "sh"
			}
		case exchange == "SZSE" || exchange == "SZ" || strings.HasSuffix(tsCode, ".SZ"):
			if strings.HasPrefix(code, "0") || strings.HasPrefix(code, "3") {
				prefix = "sz"
			}
		}
		if prefix == "" {
			continue
		}
		symbol := prefix + code
		if _, exists := seen[symbol]; exists {
			continue
		}
		seen[symbol] = struct{}{}
		result = append(result, symbol)
	}
	sort.Strings(result)
	return result, nil
}

func (s *MarketEvidenceService) fetchTencentBreadthBatch(ctx context.Context, symbols []string, attempts int) ([]breadthObservation, error) {
	if strings.TrimSpace(s.urls.breadthTencent) == "" {
		return nil, errors.New("腾讯市场宽度地址未配置")
	}
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		request := s.client.R().SetContext(ctx).
			SetHeader("Referer", "https://gu.qq.com/").
			SetHeader("User-Agent", marketEvidenceUserAgent()).
			SetQueryParams(map[string]string{"_": strconv.FormatInt(s.now().Unix(), 10), "q": strings.Join(symbols, ",")})
		if strings.Contains(strings.ToLower(s.urls.breadthTencent), "gtimg.cn") {
			request.SetHeader("Host", "qt.gtimg.cn")
		}
		response, err := request.Get(s.urls.breadthTencent)
		if err != nil {
			lastErr = err
		} else if response.StatusCode() >= 400 {
			lastErr = fmt.Errorf("HTTP %d", response.StatusCode())
		} else {
			rows := make([]breadthObservation, 0, len(symbols))
			for _, line := range strings.Split(GB18030ToUTF8(response.Body()), ";") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				info, parseErr := parseTencentStockInfoLine(line)
				if parseErr != nil || info == nil {
					continue
				}
				observation, valid := breadthObservationFromTencent(*info)
				if valid {
					rows = append(rows, observation)
				}
			}
			if len(rows) > 0 {
				return rows, nil
			}
			lastErr = errors.New("腾讯批量行情没有可解析数据")
		}
		if attempt < attempts {
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return nil, lastErr
}

func breadthObservationFromTencent(info models.StockInfo) (breadthObservation, bool) {
	code := strings.ToLower(strings.TrimSpace(info.Code))
	if !strings.HasPrefix(code, "sh") && !strings.HasPrefix(code, "sz") {
		return breadthObservation{}, false
	}
	current, currentErr := strconv.ParseFloat(strings.TrimSpace(info.Price), 64)
	previous, previousErr := strconv.ParseFloat(strings.TrimSpace(info.PreClose), 64)
	high, highErr := strconv.ParseFloat(strings.TrimSpace(info.High), 64)
	low, lowErr := strconv.ParseFloat(strings.TrimSpace(info.Low), 64)
	observation := breadthObservation{key: code, code: code, name: info.Name}
	if currentErr == nil && current >= 0 {
		observation.current, observation.currentOK = current, current > 0
	}
	if highErr == nil && high > 0 {
		observation.high, observation.highOK = high, true
	}
	if lowErr == nil && low > 0 {
		observation.low, observation.lowOK = low, true
	}
	if currentErr == nil && previousErr == nil && current > 0 && previous > 0 {
		observation.changePct = (current - previous) / previous * 100
		observation.changeOK = true
	}
	if quoteAt, err := time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(info.Date)+" "+strings.TrimSpace(info.Time), shanghaiDataLocation()); err == nil {
		observation.quoteAt = quoteAt
	}
	return observation, true
}
