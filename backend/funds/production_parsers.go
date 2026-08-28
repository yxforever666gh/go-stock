package funds

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"go-stock/backend/data"
)

var (
	htmlTagPattern               = regexp.MustCompile(`(?s)<[^>]*>`)
	sixDigitPattern              = regexp.MustCompile(`\d{6}`)
	rankhandlerAllPagesPattern   = regexp.MustCompile(`(?i)["']?allPages["']?\s*:\s*(\d+)`)
	rankhandlerAllRecordsPattern = regexp.MustCompile(`(?i)["']?allRecords["']?\s*:\s*(\d+)`)
	dateInTextPattern            = regexp.MustCompile(`\d{4}[-/.]\d{1,2}[-/.]\d{1,2}`)
	numberInTextPattern          = regexp.MustCompile(`[-+]?\d+(?:\.\d+)?`)
)

func eastmoneyFundType(category FundCategory) string {
	switch category {
	case FundCategoryStock:
		return "gp"
	case FundCategoryMixed:
		return "hh"
	case FundCategoryBond:
		return "zq"
	case FundCategoryIndex:
		return "zs"
	case FundCategoryQDII:
		return "qdii"
	case FundCategoryFOF:
		return "fof"
	default:
		return "all"
	}
}

func eastmoneyFundSort(period FundPeriod) string {
	switch period {
	case FundPeriodDay:
		return "rzdf"
	case FundPeriodWeek:
		return "1zzf"
	case FundPeriodMonth:
		return "1yzf"
	case FundPeriodThreeMonths:
		return "3yzf"
	case FundPeriodSixMonths:
		return "6yzf"
	case FundPeriodOneYear:
		return "1nzf"
	case FundPeriodThreeYears:
		return "3nzf"
	case FundPeriodYearToDate:
		return "jnzf"
	case FundPeriodSinceInception:
		return "lnzf"
	case FundPeriodScale:
		return "jjgm"
	default:
		return "1nzf"
	}
}

func sinaFundSort(period FundPeriod) string {
	switch period {
	case FundPeriodDay:
		return "zdf"
	case FundPeriodWeek:
		return "z"
	case FundPeriodMonth:
		return "y"
	case FundPeriodThreeMonths:
		return "3y"
	case FundPeriodSixMonths:
		return "6y"
	case FundPeriodOneYear:
		return "1n"
	case FundPeriodThreeYears:
		return "3n"
	case FundPeriodYearToDate:
		return "jn"
	case FundPeriodSinceInception:
		return "ln"
	case FundPeriodScale:
		return "jjgm"
	default:
		return "1n"
	}
}

func parseEastmoneyFundRankings(body []byte) ([]FundRankingItem, error) {
	if strings.Contains(string(body), "rankData") || strings.Contains(string(body), "datas:") {
		return parseEastmoneyRankhandler(body)
	}
	value, err := decodeProviderJSON(body)
	if err != nil {
		return nil, err
	}
	rows := collectProviderRows(value, "FCODE", "fundCode", "code")
	items := make([]FundRankingItem, 0, len(rows))
	for _, row := range rows {
		code := providerString(row, "FCODE", "fundCode", "code", "symbol")
		name := providerString(row, "SHORTNAME", "fundName", "name", "sname")
		if normalizeFundCode(code) == "" || strings.TrimSpace(name) == "" {
			continue
		}
		items = append(items, FundRankingItem{
			Code: code, Name: name, Category: parseFundCategory(providerString(row, "FTYPE", "fundType", "category", "type")),
			NAV: providerFloat(row, "DWJZ", "unitNav", "nav", "dwjz"), NAVDate: providerString(row, "FSRQ", "navDate", "jzrq"),
			DayReturn: providerFloat(row, "RZDF", "dayReturn", "zdf", "syl_1d"), WeekReturn: providerFloat(row, "SYL_Z", "weekReturn", "syl_z"),
			MonthReturn: providerFloat(row, "SYL_Y", "monthReturn", "syl_y", "syl_1m"), ThreeMonthReturn: providerFloat(row, "SYL_3Y", "threeMonthReturn", "syl_3y", "syl_3m"),
			SixMonthReturn: providerFloat(row, "SYL_6Y", "sixMonthReturn", "syl_6y", "syl_6m"), OneYearReturn: providerFloat(row, "SYL_1N", "oneYearReturn", "syl_1n", "syl_1y"),
			ThreeYearReturn: providerFloat(row, "SYL_3N", "threeYearReturn", "syl_3n", "syl_3y_return"), YearToDateReturn: providerFloat(row, "SYL_JN", "yearToDateReturn", "syl_jn", "ytd"),
			SinceInceptionReturn: providerFloat(row, "SYL_LN", "sinceInceptionReturn", "syl_ln"), Scale: providerMoney(row, "JJGM", "fundSize", "scale", "ENDNAV"),
			ScaleDate: providerString(row, "GMRQ", "scaleDate", "fundSizeDate"),
		})
	}
	if len(rows) > 0 && len(items) == 0 {
		return nil, fmt.Errorf("eastmoney fund ranking rows did not contain usable fund identities")
	}
	return items, nil
}

func parseEastmoneyRankhandler(body []byte) ([]FundRankingItem, error) {
	rows, err := rankhandlerDataRows(body)
	if err != nil {
		return nil, err
	}
	items := make([]FundRankingItem, 0, len(rows))
	for _, raw := range rows {
		fields := strings.Split(raw, ",")
		if len(fields) < 7 {
			continue
		}
		code := normalizeFundCode(fields[0])
		name := strings.TrimSpace(fields[1])
		if code == "" || name == "" {
			continue
		}
		item := FundRankingItem{Code: code, Name: name, Category: inferFundCategory(name), NAVDate: fieldAt(fields, 3),
			NAV: parseNumberPtr(fieldAt(fields, 4)), DayReturn: parseNumberPtr(fieldAt(fields, 6)), WeekReturn: parseNumberPtr(fieldAt(fields, 7)),
			MonthReturn: parseNumberPtr(fieldAt(fields, 8)), ThreeMonthReturn: parseNumberPtr(fieldAt(fields, 9)),
			SixMonthReturn: parseNumberPtr(fieldAt(fields, 10)), OneYearReturn: parseNumberPtr(fieldAt(fields, 11)),
			ThreeYearReturn: parseNumberPtr(fieldAt(fields, 13)), YearToDateReturn: parseNumberPtr(fieldAt(fields, 14)),
			SinceInceptionReturn: parseNumberPtr(fieldAt(fields, 15))}
		if len(fields) > 16 {
			item.Scale = rankhandlerScale(fields[len(fields)-1])
		}
		items = append(items, item)
	}
	if len(rows) > 0 && len(items) == 0 {
		return nil, fmt.Errorf("eastmoney rankhandler rows contained no usable fund identities")
	}
	return items, nil
}

func rankhandlerDataRows(body []byte) ([]string, error) {
	raw := strings.TrimSpace(strings.TrimPrefix(string(body), "\ufeff"))
	marker := strings.Index(raw, "datas")
	if marker < 0 {
		return nil, fmt.Errorf("eastmoney rankhandler response is missing datas")
	}
	arrayStart := strings.Index(raw[marker:], "[")
	if arrayStart < 0 {
		return nil, fmt.Errorf("eastmoney rankhandler response has invalid datas")
	}
	arrayStart += marker
	insideString, escaped, depth, arrayEnd := false, false, 0, -1
	for index := arrayStart; index < len(raw); index++ {
		char := raw[index]
		if insideString {
			if escaped {
				escaped = false
				continue
			}
			if char == '\\' {
				escaped = true
				continue
			}
			if char == '"' {
				insideString = false
			}
			continue
		}
		if char == '"' {
			insideString = true
			continue
		}
		switch char {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				arrayEnd = index
			}
		}
		if arrayEnd >= 0 {
			break
		}
	}
	if arrayEnd < arrayStart {
		return nil, fmt.Errorf("eastmoney rankhandler datas array is unterminated")
	}
	var rows []string
	if err := json.Unmarshal([]byte(raw[arrayStart:arrayEnd+1]), &rows); err != nil {
		return nil, fmt.Errorf("decode eastmoney rankhandler datas: %w", err)
	}
	return rows, nil
}

func rankhandlerMetadata(body []byte) (allPages, allRecords int) {
	raw := string(body)
	if match := rankhandlerAllPagesPattern.FindStringSubmatch(raw); len(match) == 2 {
		allPages, _ = strconv.Atoi(match[1])
	}
	if match := rankhandlerAllRecordsPattern.FindStringSubmatch(raw); len(match) == 2 {
		allRecords, _ = strconv.Atoi(match[1])
	}
	return allPages, allRecords
}

func fieldAt(values []string, index int) string {
	if index < 0 || index >= len(values) {
		return ""
	}
	return strings.TrimSpace(values[index])
}

func rankhandlerScale(raw string) *float64 {
	raw = strings.TrimSpace(raw)
	value := parseNumberPtr(raw)
	if value == nil {
		return nil
	}
	if strings.Contains(raw, "万亿") {
		result := *value * 1e12
		return &result
	}
	if strings.Contains(raw, "亿") || !strings.Contains(raw, "万") {
		result := *value * 1e8
		return &result
	}
	result := *value * 1e4
	return &result
}

func parseSinaFundRankings(body []byte) ([]FundRankingItem, error) {
	value, err := decodeProviderJSON(body)
	if err != nil {
		return nil, err
	}
	rows := collectProviderRows(value, "symbol", "fundcode", "code", "FCODE")
	items := make([]FundRankingItem, 0, len(rows))
	for _, row := range rows {
		code := providerString(row, "symbol", "fundcode", "code", "FCODE")
		name := providerString(row, "sname", "name", "fundname", "SHORTNAME")
		if normalizeFundCode(code) == "" || strings.TrimSpace(name) == "" {
			continue
		}
		items = append(items, FundRankingItem{
			Code: code, Name: name, Category: parseFundCategory(providerString(row, "type", "fundtype", "category")),
			NAV: providerFloat(row, "dwjz", "nav", "unit_nav"), NAVDate: providerString(row, "jzrq", "nav_date"),
			DayReturn: providerFloat(row, "zdf", "rzdf", "day_return"), WeekReturn: providerFloat(row, "z", "week_return", "syl_z"),
			MonthReturn: providerFloat(row, "y", "month_return", "syl_y"), ThreeMonthReturn: providerFloat(row, "3y", "three_month_return", "syl_3y"),
			SixMonthReturn: providerFloat(row, "6y", "six_month_return", "syl_6y"), OneYearReturn: providerFloat(row, "1n", "one_year_return", "syl_1n"),
			ThreeYearReturn: providerFloat(row, "3n", "three_year_return", "syl_3n"), YearToDateReturn: providerFloat(row, "jn", "year_to_date_return", "syl_jn"),
			SinceInceptionReturn: providerFloat(row, "ln", "since_inception_return", "syl_ln"), Scale: providerMoney(row, "jjgm", "fund_size", "scale"),
			ScaleDate: providerString(row, "gmdate", "scale_date"),
		})
	}
	if len(rows) > 0 && len(items) == 0 {
		return nil, fmt.Errorf("sina fund ranking rows did not contain usable fund identities")
	}
	return items, nil
}

func parseExchangeETFIdentities(body []byte, market string) ([]ETFIdentity, error) {
	rows, err := parseExchangeETFIdentityRows(body, market)
	if err != nil {
		return nil, err
	}
	items := make([]ETFIdentity, 0, len(rows))
	for _, row := range rows {
		items = append(items, row.Identity)
	}
	return items, nil
}

// parsedExchangeETFIdentity keeps exchange-only fields which are not part of
// the identity DTO. In particular SZSE dqgm is parsed into CNY here; the public
// ranking scale continues to come from the NAV/fundamentals source so identity
// authority is not conflated with time-varying fund data.
type parsedExchangeETFIdentity struct {
	Identity ETFIdentity
	Scale    *float64
}

func parseExchangeETFIdentityRows(body []byte, market string) ([]parsedExchangeETFIdentity, error) {
	value, err := decodeProviderJSON(body)
	if err != nil {
		return nil, err
	}
	rows := collectProviderRows(value, "SEC_CODE", "ZQDM", "zqdm", "code", "fundCode", "FUND_CODE", "sys_key")
	items := make([]parsedExchangeETFIdentity, 0, len(rows))
	for _, row := range rows {
		code := plainText(providerString(row, "SEC_CODE", "ZQDM", "zqdm", "code", "fundCode", "FUND_CODE", "sys_key"))
		if match := sixDigitPattern.FindString(code); match != "" {
			code = match
		}
		canonical, ok := canonicalETFCode(code, market)
		if !ok {
			continue
		}
		name := plainText(providerString(row, "SEC_NAME", "SEC_ABBR", "ZQJC", "zqjc", "name", "fundName", "FUND_ABBR", "fundAbbr", "secNameFull", "jjjcurl"))
		if name == "" {
			continue
		}
		fundClass := plainText(providerString(row, "jjlb", "JJLB", "subClass"))
		investmentClass := plainText(providerString(row, "tzlb", "TZLB"))
		if strings.EqualFold(strings.TrimSpace(market), "SZ") && fundClass != "" && !strings.Contains(strings.ToUpper(fundClass), "ETF") {
			continue
		}
		status := strings.ToLower(providerString(row, "STATUS", "status", "listingStatus", "SSZT"))
		listed := !strings.Contains(status, "退") && !strings.Contains(status, "终止") && status != "delisted"
		tracking := providerString(row, "INDEX_NAME", "indexName", "trackingIndex", "BZSM")
		category := inferETFCategory(name, tracking)
		categoryText := strings.ToLower(fundClass + " " + investmentClass + " " + name)
		switch {
		case strings.Contains(categoryText, "跨境"), strings.Contains(categoryText, "qdii"), strings.Contains(categoryText, "香港"), strings.Contains(categoryText, "海外"):
			category = ETFCategoryCrossBorder
		case strings.Contains(categoryText, "货币"), strings.Contains(categoryText, "现金"):
			category = ETFCategoryMoney
		case strings.Contains(categoryText, "商品"), strings.Contains(categoryText, "黄金"), strings.Contains(categoryText, "白银"), strings.Contains(categoryText, "原油"):
			category = ETFCategoryCommodity
		case strings.Contains(categoryText, "债"):
			category = ETFCategoryBond
		}
		items = append(items, parsedExchangeETFIdentity{Identity: ETFIdentity{
			Code: canonical, Name: name, Market: strings.ToUpper(market), Category: category,
			TrackingIndex: tracking, ManagementFee: providerFloat(row, "MANAGEMENT_FEE", "managementFee", "GLFL"),
			ListDate: providerString(row, "LIST_DATE", "LISTING_DATE", "SSRQ", "ssrq", "listDate", "listingDate"), Listed: listed,
		}, Scale: providerMoney(row, "dqgm", "DQGM")})
	}
	if len(rows) > 0 && len(items) == 0 {
		return nil, fmt.Errorf("exchange response contained no supported ETF codes for %s", market)
	}
	return items, nil
}

func parseEastmoneyETFIdentities(body []byte) ([]ETFIdentity, error) {
	value, err := decodeProviderJSON(body)
	if err != nil {
		return nil, err
	}
	rows := collectProviderRows(value, "f12", "code")
	items := make([]ETFIdentity, 0, len(rows))
	for _, row := range rows {
		code := providerString(row, "f12", "code")
		market := "SZ"
		if providerString(row, "f13", "market") == "1" {
			market = "SH"
		}
		canonical, ok := canonicalETFCode(code, market)
		if !ok {
			continue
		}
		name := providerString(row, "f14", "name")
		if name == "" {
			continue
		}
		items = append(items, ETFIdentity{Code: canonical, Name: name, Market: market, Category: inferETFCategory(name, ""), Listed: true})
	}
	if len(rows) > 0 && len(items) == 0 {
		return nil, fmt.Errorf("eastmoney identity response contained no supported ETF codes")
	}
	return items, nil
}

func latestIdentityDate(items []ETFIdentity) time.Time {
	var latest time.Time
	for _, item := range items {
		date := normalizeDate(item.ListDate)
		if date == "" {
			continue
		}
		parsed, err := time.ParseInLocation(time.DateOnly, date, shanghaiLocation())
		if err == nil && parsed.After(latest) {
			latest = parsed
		}
	}
	return latest
}

func parseTencentETFQuotes(body []byte) (map[string]ETFQuote, error) {
	result := map[string]ETFQuote{}
	for _, line := range strings.Split(string(body), ";") {
		line = strings.TrimSpace(line)
		separator := strings.Index(line, "=")
		if separator < 1 {
			continue
		}
		variable := strings.TrimSpace(line[:separator])
		code := strings.TrimPrefix(strings.TrimPrefix(variable, "v_r_"), "v_")
		canonical, ok := data.NormalizeETFCode(code)
		if !ok {
			continue
		}
		payload := strings.Trim(strings.TrimSpace(line[separator+1:]), "\"")
		parts := strings.Split(payload, "~")
		if len(parts) < 6 {
			continue
		}
		quote := ETFQuote{Code: canonical, Price: parseNumberPtr(parts[3])}
		if len(parts) > 32 {
			quote.ChangeRate = parseNumberPtr(parts[32])
		}
		if len(parts) > 38 {
			quote.TurnoverRate = parseNumberPtr(parts[38])
		}
		if len(parts) > 35 {
			composite := strings.Split(parts[35], "/")
			if len(composite) >= 3 {
				// Tencent's composite quote field carries the成交额 in yuan.
				// Field 37 is only expressed in万元 and is a lower precision fallback.
				quote.Amount = parseNumberPtr(composite[2])
			}
		}
		if quote.Amount == nil && len(parts) > 37 {
			quote.Amount = scaledNumberPtr(parts[37], 10000)
		}
		// Tencent publishes current fund scale in亿元 and fund shares as an
		// absolute unit count. They complement Eastmoney's authoritative NAV.
		if len(parts) > 44 {
			quote.Scale = scaledNumberPtr(parts[44], 100000000)
		}
		if len(parts) > 72 {
			quote.Shares = parseNumberPtr(parts[72])
		}
		for _, index := range []int{30, 29} {
			if index < len(parts) {
				if normalized := normalizeDateTime(parts[index]); normalized != "" {
					quote.QuoteTime = normalized
					break
				}
			}
		}
		result[canonical] = quote
	}
	if len(result) == 0 && strings.TrimSpace(string(body)) != "" {
		return result, fmt.Errorf("tencent quote response contained no usable ETF rows")
	}
	return result, nil
}

func parseSinaETFQuotes(body []byte) (map[string]ETFQuote, error) {
	result := map[string]ETFQuote{}
	for _, line := range strings.Split(string(body), ";") {
		line = strings.TrimSpace(line)
		separator := strings.Index(line, "=")
		if separator < 1 {
			continue
		}
		variable := strings.TrimSpace(line[:separator])
		code := strings.TrimPrefix(variable, "var hq_str_")
		canonical, ok := data.NormalizeETFCode(code)
		if !ok {
			continue
		}
		parts := strings.Split(strings.Trim(strings.TrimSpace(line[separator+1:]), "\""), ",")
		if len(parts) < 10 {
			continue
		}
		quote := ETFQuote{Code: canonical, Price: parseNumberPtr(parts[3]), Amount: parseNumberPtr(parts[9])}
		price, previous := numberValue(parts[3]), numberValue(parts[2])
		if previous > 0 {
			change := (price - previous) / previous * 100
			quote.ChangeRate = &change
		}
		if len(parts) > 31 {
			quote.QuoteTime = normalizeDateTime(strings.TrimSpace(parts[30]) + " " + strings.TrimSpace(parts[31]))
		}
		result[canonical] = quote
	}
	if len(result) == 0 && strings.TrimSpace(string(body)) != "" {
		return result, fmt.Errorf("sina quote response contained no usable ETF rows")
	}
	return result, nil
}

func parseEastmoneyETFQuotes(body []byte) (map[string]ETFQuote, error) {
	value, err := decodeProviderJSON(body)
	if err != nil {
		return nil, err
	}
	rows := collectProviderRows(value, "f12", "code")
	result := make(map[string]ETFQuote, len(rows))
	for _, row := range rows {
		market := "SZ"
		if providerString(row, "f13", "market") == "1" {
			market = "SH"
		}
		canonical, ok := canonicalETFCode(providerString(row, "f12", "code"), market)
		if !ok {
			continue
		}
		quote := ETFQuote{Code: canonical, Price: providerFloat(row, "f2", "price"), ChangeRate: providerFloat(row, "f3", "changeRate"),
			Amount: providerFloat(row, "f6", "amount"), TurnoverRate: providerFloat(row, "f8", "turnoverRate"), NetInflow: providerFloat(row, "f62", "netInflow")}
		if timestamp := providerFloat(row, "f124", "timestamp"); timestamp != nil && *timestamp > 0 {
			quote.QuoteTime = time.Unix(int64(*timestamp), 0).In(shanghaiLocation()).Format(time.RFC3339)
		}
		result[canonical] = quote
	}
	if len(rows) > 0 && len(result) == 0 {
		return nil, fmt.Errorf("eastmoney quote response contained no usable ETF rows")
	}
	return result, nil
}

func parseEastmoneyETFFundamentals(body []byte, identities []ETFIdentity) (map[string]ETFFundamentals, error) {
	if !strings.Contains(string(body), "rankData") && !strings.Contains(string(body), "datas:") {
		value, err := decodeProviderJSON(body)
		if err != nil {
			return nil, err
		}
		allowed := identityCodeSet(identities)
		rows := collectProviderRows(value, "FCODE", "fundCode", "code")
		result := map[string]ETFFundamentals{}
		for _, row := range rows {
			canonical, ok := canonicalETFFromUnknown(providerString(row, "FCODE", "fundCode", "code"))
			if !ok {
				continue
			}
			if _, exists := allowed[canonical]; !exists {
				continue
			}
			result[canonical] = ETFFundamentals{Code: canonical, NAV: providerFloat(row, "DWJZ", "nav", "unitNav"),
				NAVDate: providerString(row, "FSRQ", "navDate", "jzrq"), PremiumRate: providerFloat(row, "PREMIUMRATE", "premiumRate", "ZJL"),
				Shares: providerMoney(row, "TOTALSHARES", "shares", "FS"), Scale: providerMoney(row, "JJGM", "scale", "fundSize"),
				ScaleDate: providerString(row, "GMRQ", "scaleDate", "fundSizeDate"), Holdings: []ETFHolding{}}
		}
		if len(rows) > 0 && len(result) == 0 && len(identities) > 0 {
			return result, fmt.Errorf("eastmoney fundamentals did not match requested ETF identities")
		}
		return result, nil
	}
	funds, err := parseEastmoneyFundRankings(body)
	if err != nil {
		return nil, err
	}
	allowed := identityCodeSet(identities)
	result := map[string]ETFFundamentals{}
	for _, fund := range funds {
		canonical, ok := canonicalETFFromUnknown(fund.Code)
		if !ok {
			continue
		}
		if _, exists := allowed[canonical]; !exists {
			continue
		}
		value := ETFFundamentals{Code: canonical, NAV: cloneFloat(fund.NAV), NAVDate: fund.NAVDate, Scale: cloneFloat(fund.Scale),
			ScaleDate: fund.ScaleDate, Holdings: []ETFHolding{}}
		if value.Scale != nil && value.NAV != nil && *value.NAV > 0 {
			shares := *value.Scale / *value.NAV
			value.Shares = &shares
		}
		result[canonical] = value
	}
	if len(funds) > 0 && len(result) == 0 && len(identities) > 0 {
		return result, fmt.Errorf("eastmoney fundamentals did not match requested ETF identities")
	}
	return result, nil
}

func parseEastmoneyETFNetValueHTML(body []byte, identities []ETFIdentity) (map[string]ETFFundamentals, error) {
	document, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("parse eastmoney ETF NAV page: %w", err)
	}
	allowed := identityCodeSet(identities)
	navDate := ""
	document.Find("th, caption").EachWithBreak(func(_ int, selection *goquery.Selection) bool {
		if match := dateInTextPattern.FindString(strings.TrimSpace(selection.Text())); match != "" {
			navDate = normalizeDate(match)
			return false
		}
		return true
	})
	if navDate == "" {
		if match := dateInTextPattern.FindString(string(body)); match != "" {
			navDate = normalizeDate(match)
		}
	}
	result := make(map[string]ETFFundamentals, len(identities))
	recognizedRows := 0
	document.Find("tr").Each(func(_ int, row *goquery.Selection) {
		cells := make([]string, 0, 12)
		row.Find("td").Each(func(_ int, cell *goquery.Selection) {
			cells = append(cells, plainText(cell.Text()))
		})
		if len(cells) < 10 {
			return
		}
		code := ""
		codeIndex := -1
		for index, candidate := range cells {
			if match := sixDigitPattern.FindString(candidate); match != "" {
				code = match
				codeIndex = index
				break
			}
		}
		if codeIndex < 0 || codeIndex+9 >= len(cells) {
			return
		}
		canonical, ok := canonicalETFFromUnknown(code)
		if !ok {
			return
		}
		recognizedRows++
		if _, requested := allowed[canonical]; !requested {
			return
		}
		// The live table currently prefixes concern/compare/rank columns. Locate
		// the code first, then use stable relative offsets: name +1, unit NAV +2,
		// market price +8 and the published discount/premium rate +9.
		nav := parseNumberPtr(fieldAt(cells, codeIndex+2))
		premium := parseNumberPtr(fieldAt(cells, codeIndex+9))
		result[canonical] = ETFFundamentals{Code: canonical, NAV: nav, NAVDate: navDate, PremiumRate: premium, Holdings: []ETFHolding{}}
	})
	if recognizedRows == 0 {
		return nil, fmt.Errorf("eastmoney ETF NAV page contained no recognizable ETF rows")
	}
	return result, nil
}

func minIntValue(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func parseSinaETFFundamentals(body []byte, identities []ETFIdentity) (map[string]ETFFundamentals, error) {
	value, err := decodeProviderJSON(body)
	if err != nil {
		return nil, err
	}
	allowed := identityCodeSet(identities)
	rows := collectProviderRows(value, "symbol", "fundcode", "code")
	result := map[string]ETFFundamentals{}
	for _, row := range rows {
		canonical, ok := canonicalETFFromUnknown(providerString(row, "symbol", "fundcode", "code"))
		if !ok {
			continue
		}
		if _, exists := allowed[canonical]; !exists {
			continue
		}
		result[canonical] = ETFFundamentals{Code: canonical, NAV: providerFloat(row, "dwjz", "nav"), NAVDate: providerString(row, "jzrq", "navDate"),
			PremiumRate: providerFloat(row, "premium_rate", "premiumRate"), Shares: providerMoney(row, "shares", "total_shares"),
			Scale: providerMoney(row, "jjgm", "scale", "fund_size"), ScaleDate: providerString(row, "gmdate", "scaleDate"), Holdings: []ETFHolding{}}
	}
	if len(rows) > 0 && len(result) == 0 && len(identities) > 0 {
		return result, fmt.Errorf("sina fundamentals did not match requested ETF identities")
	}
	return result, nil
}

func parseEastmoneyETFBasic(value any, identity ETFIdentity) ETFFundamentals {
	rows := collectProviderRows(value, "FCODE", "fundCode", "code")
	row := firstProviderMap(value)
	if len(rows) > 0 {
		row = rows[0]
	}
	return ETFFundamentals{Code: identity.Code, NAV: providerFloat(row, "DWJZ", "NAV", "unitNav"), NAVDate: providerString(row, "FSRQ", "NAVDATE", "navDate"),
		PremiumRate: providerFloat(row, "PREMIUMRATE", "premiumRate"), Shares: providerMoney(row, "TOTALSHARES", "shares", "FS"),
		Scale: providerMoney(row, "JJGM", "fundSize", "scale"), ScaleDate: providerString(row, "GMRQ", "scaleDate"), Holdings: []ETFHolding{}}
}

func parseEastmoneyETFBasicHTML(body []byte, identity ETFIdentity) (ETFFundamentals, error) {
	document, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return ETFFundamentals{}, fmt.Errorf("parse eastmoney ETF basic page: %w", err)
	}
	result := ETFFundamentals{Code: identity.Code, Holdings: []ETFHolding{}}
	document.Find("tr").Each(func(_ int, row *goquery.Selection) {
		headings, values := row.Find("th"), row.Find("td")
		for index := 0; index < minIntValue(headings.Length(), values.Length()); index++ {
			key := plainText(headings.Eq(index).Text())
			value := plainText(values.Eq(index).Text())
			if key == "" || value == "" {
				continue
			}
			switch {
			case strings.Contains(key, "管理费率"):
				if raw := numberInTextPattern.FindString(value); raw != "" {
					result.ManagementFee = parseNumberPtr(raw)
				}
			case strings.Contains(key, "跟踪标的"):
				result.TrackingIndex = strings.TrimSpace(value)
			}
		}
	})
	if result.ManagementFee == nil && result.TrackingIndex == "" {
		return ETFFundamentals{}, fmt.Errorf("eastmoney ETF basic page contained neither management fee nor tracking index")
	}
	return result, nil
}

func parseEastmoneyETFHoldingsBody(body []byte) ([]ETFHolding, error) {
	value, err := decodeProviderJSON(body)
	if err != nil {
		return nil, err
	}
	return parseEastmoneyETFHoldings(value), nil
}

func parseEastmoneyETFHoldings(value any) []ETFHolding {
	rows := collectProviderRows(value, "GPDM", "stockCode", "code", "ZQDM")
	items := make([]ETFHolding, 0, len(rows))
	for _, row := range rows {
		code := providerString(row, "GPDM", "stockCode", "code", "ZQDM")
		name := providerString(row, "GPJC", "stockName", "name", "ZQJC")
		if code == "" || name == "" {
			continue
		}
		items = append(items, ETFHolding{Code: code, Name: name, Weight: providerFloat(row, "JZBL", "weight", "ratio"), AsOf: normalizeDate(providerString(row, "FSRQ", "REPORTDATE", "asOf"))})
	}
	return items
}

func quoteValuesAsOf(values map[string]ETFQuote) time.Time {
	var latest time.Time
	for _, value := range values {
		if parsed, err := time.Parse(time.RFC3339, normalizeDateTime(value.QuoteTime)); err == nil && parsed.After(latest) {
			latest = parsed
		}
	}
	return latest
}

func fundamentalValuesAsOf(values map[string]ETFFundamentals) time.Time {
	var latest time.Time
	for _, value := range values {
		for _, raw := range []string{value.NAVDate, value.ScaleDate} {
			date := normalizeDate(raw)
			if date == "" {
				continue
			}
			if parsed, err := time.ParseInLocation(time.DateOnly, date, shanghaiLocation()); err == nil && parsed.After(latest) {
				latest = parsed
			}
		}
	}
	return latest
}

func unwrapJSONP(body []byte) []byte {
	trimmed := strings.TrimSpace(strings.TrimPrefix(string(body), "\ufeff"))
	objectStart, arrayStart := strings.Index(trimmed, "{"), strings.Index(trimmed, "[")
	start := objectStart
	endChar := byte('}')
	if start < 0 || (arrayStart >= 0 && arrayStart < start) {
		start = arrayStart
		endChar = ']'
	}
	if start < 0 {
		return []byte(trimmed)
	}
	end := strings.LastIndexByte(trimmed, endChar)
	if end < start {
		return []byte(trimmed)
	}
	return []byte(trimmed[start : end+1])
}

func decodeProviderJSON(body []byte) (any, error) {
	trimmed := unwrapJSONP(body)
	var value any
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return nil, fmt.Errorf("decode provider JSON: %w", err)
	}
	if businessErr := providerBusinessError(value); businessErr != nil {
		return nil, businessErr
	}
	return value, nil
}

func providerBusinessError(value any) error {
	root, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	rawCode, exists := providerValue(root, "ErrCode", "errCode", "errorCode")
	if !exists {
		return nil
	}
	code := numberValue(rawCode)
	if code == 0 {
		return nil
	}
	message := providerString(root, "ErrMsg", "errMsg", "errorMessage", "message")
	if message == "" {
		message = "provider business error"
	}
	return fmt.Errorf("provider business error code %s: %s", strings.TrimSpace(fmt.Sprint(rawCode)), message)
}

func szsePageCount(body []byte) int {
	value, err := decodeProviderJSON(body)
	if err != nil {
		return 1
	}
	pageCount := 0
	var walk func(any)
	walk = func(current any) {
		if pageCount > 0 {
			return
		}
		switch typed := current.(type) {
		case map[string]any:
			if raw, ok := providerValue(typed, "pagecount", "pageCount", "PAGECOUNT"); ok {
				pageCount = int(numberValue(raw))
				return
			}
			for _, nested := range typed {
				walk(nested)
			}
		case []any:
			for _, nested := range typed {
				walk(nested)
			}
		}
	}
	walk(value)
	if pageCount < 1 {
		return 1
	}
	if pageCount > 200 {
		return 200
	}
	return pageCount
}

func plainText(raw string) string {
	decoded := html.UnescapeString(strings.TrimSpace(raw))
	decoded = htmlTagPattern.ReplaceAllString(decoded, "")
	return strings.TrimSpace(decoded)
}

func collectProviderRows(value any, codeKeys ...string) []map[string]any {
	rows := make([]map[string]any, 0)
	var walk func(any)
	walk = func(current any) {
		switch typed := current.(type) {
		case []any:
			for _, item := range typed {
				walk(item)
			}
		case map[string]any:
			if providerString(typed, codeKeys...) != "" {
				rows = append(rows, typed)
				return
			}
			for _, nested := range typed {
				walk(nested)
			}
		}
	}
	walk(value)
	return rows
}

func firstProviderMap(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range []string{"data", "Data", "result", "Result"} {
			if nested, exists := typed[key]; exists {
				if result := firstProviderMap(nested); len(result) > 0 {
					return result
				}
			}
		}
		return typed
	case []any:
		for _, nested := range typed {
			if result := firstProviderMap(nested); len(result) > 0 {
				return result
			}
		}
	}
	return map[string]any{}
}

func providerValue(row map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		if value, exists := row[key]; exists {
			return value, true
		}
	}
	for actual, value := range row {
		for _, key := range keys {
			if strings.EqualFold(actual, key) {
				return value, true
			}
		}
	}
	return nil, false
}

func providerString(row map[string]any, keys ...string) string {
	value, ok := providerValue(row, keys...)
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func providerFloat(row map[string]any, keys ...string) *float64 {
	value, ok := providerValue(row, keys...)
	if !ok {
		return nil
	}
	return parseNumberPtr(value)
}

func providerMoney(row map[string]any, keys ...string) *float64 {
	value, ok := providerValue(row, keys...)
	if !ok || value == nil {
		return nil
	}
	raw := strings.TrimSpace(fmt.Sprint(value))
	multiplier := 1.0
	switch {
	case strings.Contains(raw, "万亿"):
		multiplier = 1e12
	case strings.Contains(raw, "亿"):
		multiplier = 1e8
	case strings.Contains(raw, "万"):
		multiplier = 1e4
	}
	return scaledNumberPtr(raw, multiplier)
}

func parseNumberPtr(value any) *float64 {
	if value == nil {
		return nil
	}
	switch typed := value.(type) {
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return nil
		}
		copyValue := typed
		return &copyValue
	case float32:
		copyValue := float64(typed)
		return &copyValue
	case int:
		copyValue := float64(typed)
		return &copyValue
	case int64:
		copyValue := float64(typed)
		return &copyValue
	case json.Number:
		return parseNumberPtr(typed.String())
	}
	raw := strings.TrimSpace(fmt.Sprint(value))
	raw = strings.NewReplacer(",", "", "%", "", "亿元", "", "亿份", "", "亿", "", "万元", "", "万份", "", "万", "", "元", "", "份", "").Replace(raw)
	if raw == "" || raw == "-" || raw == "--" || strings.EqualFold(raw, "null") {
		return nil
	}
	parsed, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return nil
	}
	return &parsed
}

func scaledNumberPtr(value any, multiplier float64) *float64 {
	parsed := parseNumberPtr(value)
	if parsed == nil {
		return nil
	}
	result := *parsed * multiplier
	return &result
}

func numberValue(value any) float64 {
	parsed := parseNumberPtr(value)
	if parsed == nil {
		return 0
	}
	return *parsed
}

func parseFundCategory(raw string) FundCategory {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case strings.Contains(value, "fof"):
		return FundCategoryFOF
	case strings.Contains(value, "qdii"), strings.Contains(value, "海外"):
		return FundCategoryQDII
	case strings.Contains(value, "指数"), strings.Contains(value, "联接"):
		return FundCategoryIndex
	case strings.Contains(value, "债"):
		return FundCategoryBond
	case strings.Contains(value, "股票"), value == "gp", value == "stock":
		return FundCategoryStock
	case strings.Contains(value, "混合"), value == "hh", value == "mixed":
		return FundCategoryMixed
	default:
		return FundCategoryAll
	}
}

func canonicalETFCode(code, market string) (string, bool) {
	market = strings.ToUpper(strings.TrimSpace(market))
	prefix := ""
	if market == "SH" {
		prefix = "sh"
	} else if market == "SZ" {
		prefix = "sz"
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(code)), "sh") && !strings.HasPrefix(strings.ToLower(strings.TrimSpace(code)), "sz") {
		code = prefix + strings.TrimSpace(code)
	}
	return data.NormalizeETFCode(code)
}

func canonicalETFFromUnknown(code string) (string, bool) {
	if canonical, ok := data.NormalizeETFCode(code); ok {
		return canonical, true
	}
	digits := strings.TrimSpace(code)
	if len(digits) > 6 {
		digits = digits[len(digits)-6:]
	}
	return data.NormalizeETFCode(digits)
}

func identityCodeSet(identities []ETFIdentity) map[string]struct{} {
	result := make(map[string]struct{}, len(identities))
	for _, identity := range identities {
		if code, ok := data.NormalizeETFCode(identity.Code); ok {
			result[code] = struct{}{}
		}
	}
	return result
}
