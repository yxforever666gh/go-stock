package funds

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"go-stock/backend/data"
	"go-stock/backend/marketdata"
)

type Service struct {
	fundPrimary  FundRankingProvider
	fundFallback FundRankingProvider
	etfIdentity  ETFIdentityProvider
	etfQuotes    []ETFQuoteProvider
	etfPrimary   ETFFundamentalsProvider
	etfFallback  ETFFundamentalsProvider
	etfChart     ETFChartProvider
	now          func() time.Time
}

func NewService(
	fundPrimary FundRankingProvider,
	fundFallback FundRankingProvider,
	etfIdentity ETFIdentityProvider,
	etfQuotes []ETFQuoteProvider,
	etfPrimary ETFFundamentalsProvider,
	etfFallback ETFFundamentalsProvider,
	etfChart ETFChartProvider,
) *Service {
	return &Service{
		fundPrimary: fundPrimary, fundFallback: fundFallback, etfIdentity: etfIdentity,
		etfQuotes: append([]ETFQuoteProvider(nil), etfQuotes...), etfPrimary: etfPrimary,
		etfFallback: etfFallback, etfChart: etfChart, now: time.Now,
	}
}

// NormalizeFundRankingQuery is exported so the HTTP layer can return 400
// before starting provider work. Service methods repeat validation defensively.
func NormalizeFundRankingQuery(query FundRankingQuery) (FundRankingQuery, error) {
	return normalizeFundRankingQuery(query)
}

func NormalizeETFQuery(query ETFQuery) (ETFQuery, error) {
	return normalizeETFQuery(query)
}

func (s *Service) FundRankings(ctx context.Context, query FundRankingQuery) marketdata.DataEnvelope[FundRankingPage] {
	query, err := normalizeFundRankingQuery(query)
	if err != nil {
		return s.fundFailure(query, "validation", err)
	}
	if s.fundPrimary == nil && s.fundFallback == nil {
		return s.fundFailure(query, "provider_unconfigured", fmt.Errorf("fund ranking providers are not configured"))
	}

	primary := fetchFundProvider(ctx, s.fundPrimary, query)
	sources := make([]marketdata.SourceState, 0, 2)
	errorsOut := make([]marketdata.DataError, 0, 2)
	if s.fundPrimary != nil {
		sources = append(sources, providerState(s.fundPrimary.Name(), primary))
		errorsOut = appendProviderError(errorsOut, s.fundPrimary.Name(), primary)
	}
	items := normalizeFundItems(primary.Data, s.fundPrimary)
	usedNames := make([]string, 0, 2)
	if len(items) > 0 && s.fundPrimary != nil {
		usedNames = append(usedNames, s.fundPrimary.Name())
	}
	asOf := primary.AsOf
	needFallback := s.fundFallback != nil && (len(items) == 0 || providerStatus(primary) != marketdata.StatusOK)
	if needFallback {
		fallback := fetchFundProvider(ctx, s.fundFallback, query)
		sources = append(sources, providerState(s.fundFallback.Name(), fallback))
		errorsOut = appendProviderError(errorsOut, s.fundFallback.Name(), fallback)
		fallbackItems := normalizeFundItems(fallback.Data, s.fundFallback)
		if len(fallbackItems) > 0 {
			usedNames = append(usedNames, s.fundFallback.Name())
		}
		items = mergeFundItems(items, fallbackItems)
		asOf = latestTime(asOf, fallback.AsOf)
	}
	rawCount := len(items)
	items, dateErrors := prepareFundItems(items, query)
	errorsOut = append(errorsOut, dateErrors...)
	status := marketdata.StatusOK
	if rawCount == 0 {
		status = marketdata.StatusUnavailable
	} else if len(errorsOut) > 0 || needFallback || hasNonOKSource(sources) {
		status = marketdata.StatusPartial
	}
	if asOf.IsZero() {
		asOf = fundItemsAsOf(items)
	}
	page := paginateFundItems(items, query)
	return marketdata.DataEnvelope[FundRankingPage]{
		Data: page, Source: strings.Join(uniqueNonEmpty(usedNames), "+"), AsOf: asOf,
		FetchedAt: s.clock()(), Status: status, Errors: errorsOut, Sources: sources, Warnings: []string{},
	}
}

func (s *Service) ETFRankings(ctx context.Context, query ETFQuery) marketdata.DataEnvelope[ETFRankingPage] {
	query, err := normalizeETFQuery(query)
	if err != nil {
		return s.etfFailure(query, "validation", err)
	}
	identities, provenance := s.loadETFIdentities(ctx)
	if len(identities) == 0 {
		return marketdata.DataEnvelope[ETFRankingPage]{
			Data: emptyETFPage(query), Source: provenance.source(), AsOf: provenance.asOf, FetchedAt: s.clock()(),
			Status: marketdata.StatusUnavailable, Errors: provenance.errors, Sources: provenance.sources, Warnings: []string{},
		}
	}
	quotes, quoteProvenance, fundamentals, fundamentalProvenance := s.loadETFData(ctx, identities)
	provenance.append(quoteProvenance)
	provenance.append(fundamentalProvenance)
	items := assembleETFItems(identities, quotes, fundamentals)
	items = filterAndSortETFItems(items, query)
	page := paginateETFItems(items, query)
	status := marketdata.StatusOK
	if len(provenance.errors) > 0 || hasNonOKSource(provenance.sources) || etfDataIncomplete(page.Items) {
		status = marketdata.StatusPartial
	}
	return marketdata.DataEnvelope[ETFRankingPage]{
		Data: page, Source: provenance.source(), AsOf: provenance.asOf, FetchedAt: s.clock()(),
		Status: status, Errors: provenance.errors, Sources: provenance.sources, Warnings: []string{},
	}
}

func (s *Service) ETFSearch(ctx context.Context, q string, limit int) marketdata.DataEnvelope[[]ETFRankingItem] {
	q = strings.TrimSpace(q)
	if q == "" {
		return s.etfSearchFailure("validation", fmt.Errorf("q is required"))
	}
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > 50 {
		return s.etfSearchFailure("validation", fmt.Errorf("limit must be between 1 and 50"))
	}
	envelope := s.ETFRankings(ctx, ETFQuery{Category: ETFCategoryAll, Q: q, Sort: ETFSortAmount, SortDirection: SortDescending, Page: 1, PageSize: limit})
	return marketdata.DataEnvelope[[]ETFRankingItem]{
		Data: envelope.Data.Items, Source: envelope.Source, AsOf: envelope.AsOf, FetchedAt: envelope.FetchedAt,
		Status: envelope.Status, Errors: envelope.Errors, Sources: envelope.Sources, Warnings: envelope.Warnings,
	}
}

func (s *Service) ETFDetail(ctx context.Context, code string) marketdata.DataEnvelope[ETFDetail] {
	canonical, ok := data.NormalizeETFCode(code)
	if !ok {
		return s.etfDetailFailure("validation", fmt.Errorf("invalid ETF code %q", code))
	}
	identities, provenance := s.loadETFIdentities(ctx)
	var identity ETFIdentity
	found := false
	for _, candidate := range identities {
		if candidate.Code == canonical {
			identity, found = candidate, true
			break
		}
	}
	if !found {
		if len(identities) == 0 && (len(provenance.errors) > 0 || hasNonOKSource(provenance.sources)) {
			result := s.etfDetailFailure("provider_unavailable", fmt.Errorf("ETF identity source is unavailable; listing status for %s cannot be determined", canonical))
			result.Source = provenance.source()
			result.AsOf = provenance.asOf
			result.Errors = append(provenance.errors, result.Errors...)
			result.Sources = provenance.sources
			return result
		}
		result := s.etfDetailFailure("not_found", fmt.Errorf("ETF %s is not listed by the exchange identity source", canonical))
		result.Source = provenance.source()
		result.AsOf = provenance.asOf
		result.Errors = append(provenance.errors, result.Errors...)
		result.Sources = provenance.sources
		return result
	}
	quotes, quoteProvenance, fundamentals, fundamentalProvenance := s.loadETFData(ctx, []ETFIdentity{identity})
	provenance.append(quoteProvenance)
	provenance.append(fundamentalProvenance)
	item := assembleETFItems([]ETFIdentity{identity}, quotes, fundamentals)[0]
	detail := ETFDetail{ETFRankingItem: item, TrackingIndex: identity.TrackingIndex, ManagementFee: cloneFloat(identity.ManagementFee),
		ListDate: normalizeDate(identity.ListDate), Holdings: []ETFHolding{}}
	if values, exists := fundamentals[identity.Code]; exists {
		detail.Holdings = normalizeHoldings(values.Holdings)
		if strings.TrimSpace(detail.TrackingIndex) == "" {
			detail.TrackingIndex = strings.TrimSpace(values.TrackingIndex)
		}
		if detail.ManagementFee == nil {
			detail.ManagementFee = cloneFloat(values.ManagementFee)
		}
	}
	chartResult := marketdata.ProviderResult[data.InstrumentID]{}
	if s.etfChart != nil {
		chartResult = s.etfChart.ResolveETFChart(ctx, identity)
		provenance.sources = append(provenance.sources, providerState(s.etfChart.Name(), chartResult))
		provenance.errors = appendProviderError(provenance.errors, s.etfChart.Name(), chartResult)
		provenance.asOf = latestTime(provenance.asOf, chartResult.AsOf)
		if chartResult.Data.Code != "" {
			detail.ChartInstrument = chartResult.Data
		}
	}
	if detail.ChartInstrument.Code == "" {
		instrument, parseErr := data.ParseInstrumentID(identity.Code, "etf", identity.Market)
		if parseErr != nil {
			provenance.errors = append(provenance.errors, marketdata.DataError{Provider: "instrument", Code: "invalid_identity", Message: parseErr.Error()})
		} else {
			detail.ChartInstrument = instrument
		}
	}
	status := marketdata.StatusOK
	if len(provenance.errors) > 0 || hasNonOKSource(provenance.sources) || etfDataIncomplete([]ETFRankingItem{item}) {
		status = marketdata.StatusPartial
	}
	return marketdata.DataEnvelope[ETFDetail]{Data: detail, Source: provenance.source(), AsOf: provenance.asOf,
		FetchedAt: s.clock()(), Status: status, Errors: provenance.errors, Sources: provenance.sources, Warnings: []string{}}
}

type provenance struct {
	names   []string
	asOf    time.Time
	sources []marketdata.SourceState
	errors  []marketdata.DataError
}

func (p *provenance) append(other provenance) {
	p.names = append(p.names, other.names...)
	p.asOf = latestTime(p.asOf, other.asOf)
	p.sources = append(p.sources, other.sources...)
	p.errors = append(p.errors, other.errors...)
}

func (p provenance) source() string { return strings.Join(uniqueNonEmpty(p.names), "+") }

func (s *Service) loadETFIdentities(ctx context.Context) ([]ETFIdentity, provenance) {
	result := marketdata.ProviderResult[[]ETFIdentity]{Status: marketdata.StatusUnavailable}
	name := "exchange"
	if s.etfIdentity != nil {
		name = s.etfIdentity.Name()
		result = s.etfIdentity.FetchETFIdentities(ctx)
	}
	items := normalizeETFIdentities(result.Data)
	p := provenance{asOf: result.AsOf, sources: []marketdata.SourceState{providerState(name, result)},
		errors: appendProviderError(nil, name, result)}
	if len(items) > 0 {
		p.names = append(p.names, name)
	}
	return items, p
}

func (s *Service) loadETFData(ctx context.Context, identities []ETFIdentity) (map[string]ETFQuote, provenance, map[string]ETFFundamentals, provenance) {
	type quoteResult struct {
		values     map[string]ETFQuote
		provenance provenance
	}
	type fundamentalResult struct {
		values     map[string]ETFFundamentals
		provenance provenance
	}
	quotesDone := make(chan quoteResult, 1)
	fundamentalsDone := make(chan fundamentalResult, 1)
	go func() {
		values, source := s.loadETFQuotes(ctx, identities)
		quotesDone <- quoteResult{values: values, provenance: source}
	}()
	go func() {
		values, source := s.loadETFFundamentals(ctx, identities)
		fundamentalsDone <- fundamentalResult{values: values, provenance: source}
	}()
	quotes := <-quotesDone
	fundamentals := <-fundamentalsDone
	return quotes.values, quotes.provenance, fundamentals.values, fundamentals.provenance
}

func (s *Service) loadETFQuotes(ctx context.Context, identities []ETFIdentity) (map[string]ETFQuote, provenance) {
	merged := make(map[string]ETFQuote, len(identities))
	p := provenance{}
	for _, provider := range s.etfQuotes {
		if provider == nil {
			continue
		}
		missing := quoteMissingIdentitiesForProvider(identities, merged, provider.Name())
		if len(missing) == 0 {
			continue
		}
		result := provider.FetchETFQuotes(ctx, missing)
		result.Data = normalizeETFQuotes(result.Data)
		p.sources = append(p.sources, providerState(provider.Name(), result))
		p.errors = appendProviderError(p.errors, provider.Name(), result)
		p.asOf = latestTime(p.asOf, result.AsOf)
		if len(result.Data) > 0 {
			p.names = append(p.names, provider.Name())
		}
		mergeETFQuotes(merged, result.Data)
	}
	return merged, p
}

func (s *Service) loadETFFundamentals(ctx context.Context, identities []ETFIdentity) (map[string]ETFFundamentals, provenance) {
	merged := make(map[string]ETFFundamentals, len(identities))
	p := provenance{}
	providers := []ETFFundamentalsProvider{s.etfPrimary, s.etfFallback}
	for _, provider := range providers {
		if provider == nil {
			continue
		}
		missing := fundamentalMissingIdentities(identities, merged)
		if len(missing) == 0 {
			break
		}
		result := provider.FetchETFFundamentals(ctx, missing)
		result.Data = normalizeETFFundamentals(result.Data)
		p.sources = append(p.sources, providerState(provider.Name(), result))
		p.errors = appendProviderError(p.errors, provider.Name(), result)
		p.asOf = latestTime(p.asOf, result.AsOf)
		if len(result.Data) > 0 {
			p.names = append(p.names, provider.Name())
		}
		mergeETFFundamentals(merged, result.Data)
	}
	return merged, p
}

func fetchFundProvider(ctx context.Context, provider FundRankingProvider, query FundRankingQuery) marketdata.ProviderResult[[]FundRankingItem] {
	if provider == nil {
		return marketdata.ProviderResult[[]FundRankingItem]{Status: marketdata.StatusUnavailable, Data: []FundRankingItem{}}
	}
	result := provider.FetchFundRankings(ctx, query)
	if result.Data == nil {
		result.Data = []FundRankingItem{}
	}
	return result
}

func providerStatus[T any](result marketdata.ProviderResult[T]) string {
	status := strings.TrimSpace(result.Status)
	if result.Err != nil {
		if hasProviderData(result.Data) {
			return marketdata.StatusPartial
		}
		return marketdata.StatusUnavailable
	}
	switch status {
	case marketdata.StatusOK, marketdata.StatusPartial, marketdata.StatusStale, marketdata.StatusUnavailable, marketdata.StatusAfterCutoff:
		return status
	case marketdata.StatusEmpty:
		return marketdata.StatusUnavailable
	default:
		if hasProviderData(result.Data) {
			return marketdata.StatusOK
		}
		return marketdata.StatusUnavailable
	}
}

func hasProviderData[T any](dataValue T) bool {
	switch value := any(dataValue).(type) {
	case []FundRankingItem:
		return len(value) > 0
	case []ETFIdentity:
		return len(value) > 0
	case map[string]ETFQuote:
		return len(value) > 0
	case map[string]ETFFundamentals:
		return len(value) > 0
	case data.InstrumentID:
		return value.Code != ""
	default:
		return false
	}
}

func providerState[T any](name string, result marketdata.ProviderResult[T]) marketdata.SourceState {
	status := providerStatus(result)
	message := strings.TrimSpace(result.Warning)
	if message == "" && result.Err != nil {
		message = result.Err.Error()
	}
	if message == "" && status == marketdata.StatusUnavailable {
		message = "数据源返回空数据"
	}
	return marketdata.SourceState{Provider: name, Status: status, AsOf: result.AsOf, AvailableAt: result.AvailableAt,
		SourceRef: result.SourceRef, Message: message}
}

func appendProviderError[T any](out []marketdata.DataError, name string, result marketdata.ProviderResult[T]) []marketdata.DataError {
	if result.Err != nil {
		return append(out, marketdata.DataError{Provider: name, Code: providerErrorCode(result.Err), Message: result.Err.Error()})
	}
	if providerStatus(result) == marketdata.StatusUnavailable {
		return append(out, marketdata.DataError{Provider: name, Code: "empty_data", Message: "数据源返回空数据"})
	}
	return out
}

func normalizeFundItems(items []FundRankingItem, provider FundRankingProvider) []FundRankingItem {
	name := ""
	if provider != nil {
		name = provider.Name()
	}
	result := make([]FundRankingItem, 0, len(items))
	seen := make(map[string]int, len(items))
	for _, item := range items {
		item.Code = normalizeFundCode(item.Code)
		item.Name = strings.TrimSpace(item.Name)
		if item.Code == "" || item.Name == "" {
			continue
		}
		if !validFundCategory(item.Category) || item.Category == FundCategoryAll {
			item.Category = inferFundCategory(item.Name)
		}
		item.NAVDate = normalizeDate(item.NAVDate)
		item.ScaleDate = normalizeDate(item.ScaleDate)
		item.Source = name
		if index, ok := seen[item.Code]; ok {
			result[index] = mergeFundItem(result[index], item)
			continue
		}
		seen[item.Code] = len(result)
		result = append(result, item)
	}
	return result
}

func mergeFundItems(primary, fallback []FundRankingItem) []FundRankingItem {
	result := append([]FundRankingItem(nil), primary...)
	indexes := make(map[string]int, len(result))
	for index := range result {
		indexes[result[index].Code] = index
	}
	for _, item := range fallback {
		if index, ok := indexes[item.Code]; ok {
			result[index] = mergeFundItem(result[index], item)
			continue
		}
		indexes[item.Code] = len(result)
		result = append(result, item)
	}
	return result
}

func mergeFundItem(primary, fallback FundRankingItem) FundRankingItem {
	if primary.Name == "" {
		primary.Name = fallback.Name
	}
	if primary.Category == "" || primary.Category == FundCategoryAll {
		primary.Category = fallback.Category
	}
	primary.NAV = firstFloat(primary.NAV, fallback.NAV)
	primary.DayReturn = firstFloat(primary.DayReturn, fallback.DayReturn)
	primary.WeekReturn = firstFloat(primary.WeekReturn, fallback.WeekReturn)
	primary.MonthReturn = firstFloat(primary.MonthReturn, fallback.MonthReturn)
	primary.ThreeMonthReturn = firstFloat(primary.ThreeMonthReturn, fallback.ThreeMonthReturn)
	primary.SixMonthReturn = firstFloat(primary.SixMonthReturn, fallback.SixMonthReturn)
	primary.OneYearReturn = firstFloat(primary.OneYearReturn, fallback.OneYearReturn)
	primary.ThreeYearReturn = firstFloat(primary.ThreeYearReturn, fallback.ThreeYearReturn)
	primary.YearToDateReturn = firstFloat(primary.YearToDateReturn, fallback.YearToDateReturn)
	primary.SinceInceptionReturn = firstFloat(primary.SinceInceptionReturn, fallback.SinceInceptionReturn)
	primary.Scale = firstFloat(primary.Scale, fallback.Scale)
	if primary.NAVDate == "" {
		primary.NAVDate = fallback.NAVDate
	}
	if primary.ScaleDate == "" {
		primary.ScaleDate = fallback.ScaleDate
	}
	return primary
}

func prepareFundItems(items []FundRankingItem, query FundRankingQuery) ([]FundRankingItem, []marketdata.DataError) {
	filtered := make([]FundRankingItem, 0, len(items))
	needle := strings.ToLower(query.Q)
	for _, item := range items {
		if query.Category != FundCategoryAll && item.Category != query.Category {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(item.Code), needle) && !strings.Contains(strings.ToLower(item.Name), needle) {
			continue
		}
		filtered = append(filtered, item)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		left, right := fundSortValue(filtered[i], query.Period), fundSortValue(filtered[j], query.Period)
		return compareNullable(left, right, query.SortDirection, filtered[i].Code, filtered[j].Code)
	})
	for index := range filtered {
		filtered[index].Rank = index + 1
	}
	return filtered, nil
}

func paginateFundItems(items []FundRankingItem, query FundRankingQuery) FundRankingPage {
	start, end := pageBounds(len(items), query.Page, query.PageSize)
	pageItems := append([]FundRankingItem(nil), items[start:end]...)
	return FundRankingPage{Items: pageItems, Total: len(items), Page: query.Page, PageSize: query.PageSize,
		Category: query.Category, Period: query.Period, NAVDate: latestFundNAVDate(items)}
}

func normalizeETFIdentities(items []ETFIdentity) []ETFIdentity {
	result := make([]ETFIdentity, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		canonical, ok := data.NormalizeETFCode(item.Code)
		if !ok {
			continue
		}
		if _, duplicate := seen[canonical]; duplicate {
			continue
		}
		item.Code = canonical
		item.Name = strings.TrimSpace(item.Name)
		if item.Name == "" {
			continue
		}
		if strings.HasPrefix(canonical, "sh") {
			item.Market = "SH"
		} else {
			item.Market = "SZ"
		}
		if !validETFCategory(item.Category) || item.Category == ETFCategoryAll {
			item.Category = inferETFCategory(item.Name, item.TrackingIndex)
		}
		item.ListDate = normalizeDate(item.ListDate)
		if !item.Listed {
			// Providers that omit the listing flag describe their current list;
			// a non-empty list date is therefore sufficient evidence of listing.
			item.Listed = item.ListDate != ""
		}
		if !item.Listed {
			continue
		}
		seen[canonical] = struct{}{}
		result = append(result, item)
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Code < result[j].Code })
	return result
}

func normalizeETFQuotes(values map[string]ETFQuote) map[string]ETFQuote {
	result := make(map[string]ETFQuote, len(values))
	for key, value := range values {
		code := value.Code
		if code == "" {
			code = key
		}
		canonical, ok := data.NormalizeETFCode(code)
		if !ok {
			continue
		}
		value.Code = canonical
		value.QuoteTime = normalizeDateTime(value.QuoteTime)
		if current, exists := result[canonical]; exists {
			result[canonical] = mergeETFQuote(current, value)
		} else {
			result[canonical] = value
		}
	}
	return result
}

func normalizeETFFundamentals(values map[string]ETFFundamentals) map[string]ETFFundamentals {
	result := make(map[string]ETFFundamentals, len(values))
	for key, value := range values {
		code := value.Code
		if code == "" {
			code = key
		}
		canonical, ok := data.NormalizeETFCode(code)
		if !ok {
			continue
		}
		value.Code = canonical
		value.NAVDate = normalizeDate(value.NAVDate)
		value.ScaleDate = normalizeDate(value.ScaleDate)
		value.TrackingIndex = strings.TrimSpace(value.TrackingIndex)
		value.Holdings = normalizeHoldings(value.Holdings)
		if current, exists := result[canonical]; exists {
			result[canonical] = mergeETFFundamental(current, value)
		} else {
			result[canonical] = value
		}
	}
	return result
}

func mergeETFQuotes(target map[string]ETFQuote, incoming map[string]ETFQuote) {
	for code, value := range incoming {
		if current, ok := target[code]; ok {
			target[code] = mergeETFQuote(current, value)
		} else {
			target[code] = value
		}
	}
}

func mergeETFQuote(primary, fallback ETFQuote) ETFQuote {
	primary.Price = firstFloat(primary.Price, fallback.Price)
	primary.ChangeRate = firstFloat(primary.ChangeRate, fallback.ChangeRate)
	primary.Amount = firstFloat(primary.Amount, fallback.Amount)
	primary.TurnoverRate = firstFloat(primary.TurnoverRate, fallback.TurnoverRate)
	primary.NetInflow = firstFloat(primary.NetInflow, fallback.NetInflow)
	primary.Shares = firstFloat(primary.Shares, fallback.Shares)
	primary.Scale = firstFloat(primary.Scale, fallback.Scale)
	if primary.QuoteTime == "" {
		primary.QuoteTime = fallback.QuoteTime
	}
	return primary
}

func mergeETFFundamentals(target map[string]ETFFundamentals, incoming map[string]ETFFundamentals) {
	for code, value := range incoming {
		if current, ok := target[code]; ok {
			target[code] = mergeETFFundamental(current, value)
		} else {
			target[code] = value
		}
	}
}

func mergeETFFundamental(primary, fallback ETFFundamentals) ETFFundamentals {
	primary.NAV = firstFloat(primary.NAV, fallback.NAV)
	primary.PremiumRate = firstFloat(primary.PremiumRate, fallback.PremiumRate)
	primary.Shares = firstFloat(primary.Shares, fallback.Shares)
	primary.Scale = firstFloat(primary.Scale, fallback.Scale)
	primary.ManagementFee = firstFloat(primary.ManagementFee, fallback.ManagementFee)
	if primary.NAVDate == "" {
		primary.NAVDate = fallback.NAVDate
	}
	if primary.ScaleDate == "" {
		primary.ScaleDate = fallback.ScaleDate
	}
	if primary.TrackingIndex == "" {
		primary.TrackingIndex = fallback.TrackingIndex
	}
	if len(primary.Holdings) == 0 {
		primary.Holdings = fallback.Holdings
	}
	return primary
}

func assembleETFItems(identities []ETFIdentity, quotes map[string]ETFQuote, fundamentals map[string]ETFFundamentals) []ETFRankingItem {
	items := make([]ETFRankingItem, 0, len(identities))
	for _, identity := range identities {
		quote := quotes[identity.Code]
		fundamental := fundamentals[identity.Code]
		premium := cloneFloat(fundamental.PremiumRate)
		if premium == nil && quote.Price != nil && fundamental.NAV != nil && *fundamental.NAV != 0 {
			value := (*quote.Price / *fundamental.NAV - 1) * 100
			premium = &value
		}
		items = append(items, ETFRankingItem{Code: identity.Code, Name: identity.Name, Market: identity.Market, Category: identity.Category,
			Price: cloneFloat(quote.Price), ChangeRate: cloneFloat(quote.ChangeRate), Amount: cloneFloat(quote.Amount),
			TurnoverRate: cloneFloat(quote.TurnoverRate), NAV: cloneFloat(fundamental.NAV), NAVDate: fundamental.NAVDate,
			PremiumRate: premium, Shares: firstFloat(cloneFloat(fundamental.Shares), cloneFloat(quote.Shares)),
			Scale:     firstFloat(fundamental.Scale, quote.Scale),
			NetInflow: cloneFloat(quote.NetInflow), QuoteTime: quote.QuoteTime})
	}
	return items
}

func filterAndSortETFItems(items []ETFRankingItem, query ETFQuery) []ETFRankingItem {
	filtered := make([]ETFRankingItem, 0, len(items))
	needle := strings.ToLower(query.Q)
	for _, item := range items {
		if query.Category != ETFCategoryAll && item.Category != query.Category {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(item.Code), needle) &&
			!strings.Contains(strings.ToLower(strings.TrimPrefix(item.Code, "sh")), needle) &&
			!strings.Contains(strings.ToLower(strings.TrimPrefix(item.Code, "sz")), needle) &&
			!strings.Contains(strings.ToLower(item.Name), needle) {
			continue
		}
		filtered = append(filtered, item)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return compareNullable(etfSortValue(filtered[i], query.Sort), etfSortValue(filtered[j], query.Sort), query.SortDirection,
			filtered[i].Code, filtered[j].Code)
	})
	for index := range filtered {
		filtered[index].Rank = index + 1
	}
	return filtered
}

func paginateETFItems(items []ETFRankingItem, query ETFQuery) ETFRankingPage {
	start, end := pageBounds(len(items), query.Page, query.PageSize)
	return ETFRankingPage{Items: append([]ETFRankingItem(nil), items[start:end]...), Total: len(items), Page: query.Page,
		PageSize: query.PageSize, Category: query.Category, Sort: query.Sort}
}

func quoteMissingIdentities(identities []ETFIdentity, quotes map[string]ETFQuote) []ETFIdentity {
	missing := make([]ETFIdentity, 0)
	for _, identity := range identities {
		quote, ok := quotes[identity.Code]
		if !ok || quote.Price == nil || quote.ChangeRate == nil || quote.Amount == nil || quote.TurnoverRate == nil || quote.NetInflow == nil || quote.QuoteTime == "" {
			missing = append(missing, identity)
		}
	}
	return missing
}

func quoteMissingIdentitiesForProvider(identities []ETFIdentity, quotes map[string]ETFQuote, providerName string) []ETFIdentity {
	if !strings.Contains(strings.ToLower(strings.TrimSpace(providerName)), "sina") {
		return quoteMissingIdentities(identities, quotes)
	}
	// Sina can repair core price fields, but does not publish turnover or
	// northbound-compatible ETF net inflow in this contract. Do not spend the
	// shared request deadline asking it to fill fields it cannot supply.
	missing := make([]ETFIdentity, 0)
	for _, identity := range identities {
		quote, ok := quotes[identity.Code]
		if !ok || quote.Price == nil || quote.ChangeRate == nil || quote.Amount == nil || quote.QuoteTime == "" {
			missing = append(missing, identity)
		}
	}
	return missing
}

func fundamentalMissingIdentities(identities []ETFIdentity, values map[string]ETFFundamentals) []ETFIdentity {
	missing := make([]ETFIdentity, 0)
	for _, identity := range identities {
		value, ok := values[identity.Code]
		// The NAV provider fallback exists to repair NAV evidence. Current
		// shares and scale are supplied by the real-time quote chain, so their
		// absence must not trigger a full, redundant fund-list request.
		if !ok || value.NAV == nil || value.NAVDate == "" {
			missing = append(missing, identity)
		}
	}
	return missing
}

func etfDataIncomplete(items []ETFRankingItem) bool {
	for _, item := range items {
		if item.Price == nil || item.ChangeRate == nil || item.Amount == nil || item.TurnoverRate == nil || item.NetInflow == nil || item.QuoteTime == "" ||
			item.NAV == nil || item.NAVDate == "" || item.PremiumRate == nil || item.Shares == nil || item.Scale == nil {
			return true
		}
	}
	return false
}

func normalizeHoldings(values []ETFHolding) []ETFHolding {
	result := make([]ETFHolding, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value.Code = strings.TrimSpace(value.Code)
		value.Name = strings.TrimSpace(value.Name)
		value.AsOf = normalizeDate(value.AsOf)
		if value.Code == "" || value.Name == "" {
			continue
		}
		if _, ok := seen[value.Code]; ok {
			continue
		}
		seen[value.Code] = struct{}{}
		result = append(result, value)
	}
	sort.SliceStable(result, func(i, j int) bool {
		return compareNullable(result[i].Weight, result[j].Weight, SortDescending, result[i].Code, result[j].Code)
	})
	return result
}

func fundSortValue(item FundRankingItem, period FundPeriod) *float64 {
	switch period {
	case FundPeriodDay:
		return item.DayReturn
	case FundPeriodWeek:
		return item.WeekReturn
	case FundPeriodMonth:
		return item.MonthReturn
	case FundPeriodThreeMonths:
		return item.ThreeMonthReturn
	case FundPeriodSixMonths:
		return item.SixMonthReturn
	case FundPeriodOneYear:
		return item.OneYearReturn
	case FundPeriodThreeYears:
		return item.ThreeYearReturn
	case FundPeriodYearToDate:
		return item.YearToDateReturn
	case FundPeriodSinceInception:
		return item.SinceInceptionReturn
	case FundPeriodScale:
		return item.Scale
	default:
		return nil
	}
}

func etfSortValue(item ETFRankingItem, field ETFSort) *float64 {
	switch field {
	case ETFSortChangeRate:
		return item.ChangeRate
	case ETFSortAmount:
		return item.Amount
	case ETFSortTurnoverRate:
		return item.TurnoverRate
	case ETFSortPremiumRate:
		return item.PremiumRate
	case ETFSortScale:
		return item.Scale
	case ETFSortNetInflow:
		return item.NetInflow
	default:
		return nil
	}
}

func compareNullable(left, right *float64, direction SortDirection, leftCode, rightCode string) bool {
	if left == nil && right == nil {
		return leftCode < rightCode
	}
	if left == nil {
		return false
	}
	if right == nil {
		return true
	}
	if *left == *right {
		return leftCode < rightCode
	}
	if direction == SortAscending {
		return *left < *right
	}
	return *left > *right
}

func pageBounds(length, page, pageSize int) (int, int) {
	start := (page - 1) * pageSize
	if start > length {
		start = length
	}
	end := start + pageSize
	if end > length {
		end = length
	}
	return start, end
}

func normalizeFundCode(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	code = strings.TrimPrefix(strings.TrimPrefix(code, "sh"), "sz")
	code = strings.TrimPrefix(code, "of")
	if len(code) != 6 {
		return ""
	}
	if _, err := strconv.Atoi(code); err != nil {
		return ""
	}
	return code
}

func inferFundCategory(value string) FundCategory {
	lower := strings.ToLower(strings.TrimSpace(value))
	switch {
	case strings.Contains(lower, "qdii"):
		return FundCategoryQDII
	case strings.Contains(lower, "fof"):
		return FundCategoryFOF
	case strings.Contains(lower, "债"):
		return FundCategoryBond
	case strings.Contains(lower, "指数"), strings.Contains(lower, "联接"), strings.Contains(lower, "etf"):
		return FundCategoryIndex
	case strings.Contains(lower, "股票"):
		return FundCategoryStock
	default:
		return FundCategoryMixed
	}
}

func inferETFCategory(name, trackingIndex string) ETFCategory {
	value := strings.ToLower(strings.TrimSpace(name + " " + trackingIndex))
	switch {
	case strings.Contains(value, "货币"), strings.Contains(value, "现金"), strings.Contains(value, "添益"), strings.Contains(value, "保证金"):
		return ETFCategoryMoney
	case strings.Contains(value, "黄金"), strings.Contains(value, "白银"), strings.Contains(value, "有色"), strings.Contains(value, "豆粕"), strings.Contains(value, "原油"):
		return ETFCategoryCommodity
	case strings.Contains(value, "债"), strings.Contains(value, "国开"), strings.Contains(value, "国债"):
		return ETFCategoryBond
	case strings.Contains(value, "纳斯达克"), strings.Contains(value, "标普"), strings.Contains(value, "恒生"), strings.Contains(value, "日经"),
		strings.Contains(value, "德国"), strings.Contains(value, "法国"), strings.Contains(value, "沙特"), strings.Contains(value, "跨境"):
		return ETFCategoryCrossBorder
	case strings.Contains(value, "沪深300"), strings.Contains(value, "中证500"), strings.Contains(value, "中证1000"),
		strings.Contains(value, "上证50"), strings.Contains(value, "创业板"), strings.Contains(value, "科创50"), strings.Contains(value, "a500"):
		return ETFCategoryBroad
	default:
		return ETFCategoryIndustry
	}
}

func normalizeDate(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "-" || raw == "--" {
		return ""
	}
	location := shanghaiLocation()
	for _, layout := range []string{time.DateOnly, "20060102", "2006/01/02", "2006.01.02", "2006-1-2", time.RFC3339, "2006-01-02 15:04:05"} {
		if parsed, err := time.ParseInLocation(layout, raw, location); err == nil {
			return parsed.In(location).Format(time.DateOnly)
		}
	}
	if len(raw) >= len(time.DateOnly) {
		return normalizeDate(raw[:len(time.DateOnly)])
	}
	return ""
}

func normalizeDateTime(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "-" || raw == "--" {
		return ""
	}
	location := shanghaiLocation()
	for _, layout := range []string{"2006-01-02 15:04:05", "2006/01/02 15:04:05", time.RFC3339, "20060102150405"} {
		if parsed, err := time.ParseInLocation(layout, raw, location); err == nil {
			return parsed.In(location).Format(time.RFC3339)
		}
	}
	if date := normalizeDate(raw); date != "" {
		parsed, _ := time.ParseInLocation(time.DateOnly, date, location)
		return parsed.Format(time.RFC3339)
	}
	return ""
}

func shanghaiLocation() *time.Location {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*60*60)
	}
	return location
}

func fundItemsAsOf(items []FundRankingItem) time.Time {
	date := latestFundNAVDate(items)
	if date == "" {
		return time.Time{}
	}
	parsed, _ := time.ParseInLocation(time.DateOnly, date, shanghaiLocation())
	return parsed
}

func latestFundNAVDate(items []FundRankingItem) string {
	latest := ""
	for _, item := range items {
		if item.NAVDate > latest {
			latest = item.NAVDate
		}
	}
	return latest
}

func firstFloat(primary, fallback *float64) *float64 {
	if primary != nil {
		return cloneFloat(primary)
	}
	return cloneFloat(fallback)
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func hasNonOKSource(sources []marketdata.SourceState) bool {
	for _, source := range sources {
		if source.Status != marketdata.StatusOK {
			return true
		}
	}
	return false
}

func (s *Service) clock() func() time.Time {
	if s != nil && s.now != nil {
		return s.now
	}
	return time.Now
}

func (s *Service) fundFailure(query FundRankingQuery, code string, err error) marketdata.DataEnvelope[FundRankingPage] {
	return marketdata.DataEnvelope[FundRankingPage]{Data: FundRankingPage{Items: []FundRankingItem{}, Page: query.Page, PageSize: query.PageSize,
		Category: query.Category, Period: query.Period}, FetchedAt: s.clock()(), Status: marketdata.StatusUnavailable,
		Errors: []marketdata.DataError{{Provider: "funds", Code: code, Message: err.Error()}}, Sources: []marketdata.SourceState{}, Warnings: []string{}}
}

func emptyETFPage(query ETFQuery) ETFRankingPage {
	return ETFRankingPage{Items: []ETFRankingItem{}, Page: query.Page, PageSize: query.PageSize, Category: query.Category, Sort: query.Sort}
}

func (s *Service) etfFailure(query ETFQuery, code string, err error) marketdata.DataEnvelope[ETFRankingPage] {
	return marketdata.DataEnvelope[ETFRankingPage]{Data: emptyETFPage(query), FetchedAt: s.clock()(), Status: marketdata.StatusUnavailable,
		Errors: []marketdata.DataError{{Provider: "etfs", Code: code, Message: err.Error()}}, Sources: []marketdata.SourceState{}, Warnings: []string{}}
}

func (s *Service) etfSearchFailure(code string, err error) marketdata.DataEnvelope[[]ETFRankingItem] {
	return marketdata.DataEnvelope[[]ETFRankingItem]{Data: []ETFRankingItem{}, FetchedAt: s.clock()(), Status: marketdata.StatusUnavailable,
		Errors: []marketdata.DataError{{Provider: "etfs", Code: code, Message: err.Error()}}, Sources: []marketdata.SourceState{}, Warnings: []string{}}
}

func (s *Service) etfDetailFailure(code string, err error) marketdata.DataEnvelope[ETFDetail] {
	return marketdata.DataEnvelope[ETFDetail]{Data: ETFDetail{Holdings: []ETFHolding{}}, FetchedAt: s.clock()(), Status: marketdata.StatusUnavailable,
		Errors: []marketdata.DataError{{Provider: "etfs", Code: code, Message: err.Error()}}, Sources: []marketdata.SourceState{}, Warnings: []string{}}
}
