package data

import (
	"regexp"
	"strings"
)

var marketSummaryReportStockCodePattern = regexp.MustCompile(`(?i)(?:SH|SZ|BJ)?[0-9]{6}(?:\.(?:SH|SZ|BJ))?`)

// MarketSummarySupplementReportMergeStats describes the deterministic changes
// made while merging a confirmed second-round recommendation table into the
// first-round report.
type MarketSummarySupplementReportMergeStats struct {
	BaseTableFound               bool
	SupplementTableFound         bool
	MaximumOutput                int
	AcceptedCodeCount            int
	BaseRecommendationRows       int
	SupplementRecommendationRows int
	DuplicateRowsOmitted         int
	UnconfirmedRowsOmitted       int
	OutputRowsOmitted            int
	ReplacedCodes                []string
	AppendedCodes                []string
	VisibleCodes                 []string
	UnconfirmedCodes             []string
	MissingAcceptedCodes         []string
	OmittedByLimitCodes          []string
}

type marketSummaryReportMarkdownTable struct {
	start      int
	end        int
	header     string
	separator  string
	stockIndex int
	rows       []marketSummaryReportMarkdownRow
}

type marketSummaryReportMarkdownRow struct {
	raw  string
	code string
}

// MergeMarketSummarySupplementReport merges only backend-confirmed supplement
// rows into the first recommendation table in baseText. acceptedCodes must be
// derived from successfully saved or upgraded records; rows not in that set
// are ignored even when the supplement report claims they succeeded.
func MergeMarketSummarySupplementReport(baseText, supplementText string, acceptedCodes []string, maximumOutput int) (string, MarketSummarySupplementReportMergeStats) {
	stats := MarketSummarySupplementReportMergeStats{
		MaximumOutput:        normalizeMarketSummaryOutputLimit(maximumOutput),
		ReplacedCodes:        []string{},
		AppendedCodes:        []string{},
		VisibleCodes:         []string{},
		UnconfirmedCodes:     []string{},
		MissingAcceptedCodes: []string{},
		OmittedByLimitCodes:  []string{},
	}

	acceptedSet, acceptedOrder := normalizeMarketSummaryReportCodeSet(acceptedCodes)
	stats.AcceptedCodeCount = len(acceptedOrder)

	baseLines, baseLineEnding := splitMarketSummaryReportLines(baseText)
	baseTable, ok := findMarketSummaryRecommendationTable(baseLines)
	if !ok {
		return baseText, stats
	}
	stats.BaseTableFound = true

	supplementLines, _ := splitMarketSummaryReportLines(supplementText)
	supplementTable, supplementFound := findMarketSummaryRecommendationTable(supplementLines)
	stats.SupplementTableFound = supplementFound

	supplementRows := make(map[string]marketSummaryReportMarkdownRow, len(acceptedSet))
	supplementOrder := make([]string, 0, len(acceptedSet))
	supplementSeen := make(map[string]struct{})
	unconfirmedSeen := make(map[string]struct{})
	if supplementFound {
		for _, row := range supplementTable.rows {
			if row.code == "" {
				continue
			}
			stats.SupplementRecommendationRows++
			if _, duplicate := supplementSeen[row.code]; duplicate {
				stats.DuplicateRowsOmitted++
				continue
			}
			supplementSeen[row.code] = struct{}{}
			if _, accepted := acceptedSet[row.code]; !accepted {
				stats.UnconfirmedRowsOmitted++
				if _, seen := unconfirmedSeen[row.code]; !seen {
					unconfirmedSeen[row.code] = struct{}{}
					stats.UnconfirmedCodes = append(stats.UnconfirmedCodes, row.code)
				}
				continue
			}
			supplementRows[row.code] = row
			supplementOrder = append(supplementOrder, row.code)
		}
	}

	for _, code := range acceptedOrder {
		if _, found := supplementSeen[code]; !found {
			stats.MissingAcceptedCodes = append(stats.MissingAcceptedCodes, code)
		}
	}

	mergedRows := make([]marketSummaryReportMarkdownRow, 0, len(baseTable.rows)+len(supplementRows))
	mergedSeen := make(map[string]struct{}, len(baseTable.rows)+len(supplementRows))
	baseOpaqueRows := make([]marketSummaryReportMarkdownRow, 0, 1)
	for _, row := range baseTable.rows {
		if row.code == "" {
			baseOpaqueRows = append(baseOpaqueRows, row)
			continue
		}
		stats.BaseRecommendationRows++
		if _, duplicate := mergedSeen[row.code]; duplicate {
			stats.DuplicateRowsOmitted++
			continue
		}
		mergedSeen[row.code] = struct{}{}
		if replacement, replace := supplementRows[row.code]; replace {
			row = replacement
			stats.ReplacedCodes = append(stats.ReplacedCodes, row.code)
		}
		mergedRows = append(mergedRows, row)
	}

	for _, code := range supplementOrder {
		if _, exists := mergedSeen[code]; exists {
			continue
		}
		mergedSeen[code] = struct{}{}
		mergedRows = append(mergedRows, supplementRows[code])
		stats.AppendedCodes = append(stats.AppendedCodes, code)
	}

	if len(mergedRows) > stats.MaximumOutput {
		stats.OutputRowsOmitted = len(mergedRows) - stats.MaximumOutput
		for _, row := range mergedRows[stats.MaximumOutput:] {
			stats.OmittedByLimitCodes = append(stats.OmittedByLimitCodes, row.code)
		}
		mergedRows = mergedRows[:stats.MaximumOutput]
	}
	for _, row := range mergedRows {
		stats.VisibleCodes = append(stats.VisibleCodes, row.code)
	}

	replacementLines := []string{baseTable.header, baseTable.separator}
	if len(mergedRows) == 0 {
		for _, row := range baseOpaqueRows {
			replacementLines = append(replacementLines, row.raw)
		}
	} else {
		for _, row := range mergedRows {
			replacementLines = append(replacementLines, row.raw)
		}
	}

	resultLines := make([]string, 0, len(baseLines)-baseTable.end+baseTable.start+len(replacementLines))
	resultLines = append(resultLines, baseLines[:baseTable.start]...)
	resultLines = append(resultLines, replacementLines...)
	resultLines = append(resultLines, baseLines[baseTable.end:]...)
	return strings.Join(resultLines, baseLineEnding), stats
}

func findMarketSummaryRecommendationTable(lines []string) (marketSummaryReportMarkdownTable, bool) {
	sectionStart, sectionEnd, sectionFound := findMarketSummaryRecommendationSection(lines)
	if sectionFound {
		return findMarketSummaryRecommendationTableInRange(lines, sectionStart, sectionEnd)
	}
	return findMarketSummaryRecommendationTableInRange(lines, 0, len(lines))
}

func findMarketSummaryRecommendationSection(lines []string) (int, int, bool) {
	for idx, line := range lines {
		level, title, ok := parseMarketSummaryMarkdownHeading(line)
		if !ok || title != "推荐股票池" {
			continue
		}
		end := len(lines)
		for next := idx + 1; next < len(lines); next++ {
			nextLevel, _, heading := parseMarketSummaryMarkdownHeading(lines[next])
			if heading && nextLevel <= level {
				end = next
				break
			}
		}
		return idx + 1, end, true
	}
	return 0, 0, false
}

func findMarketSummaryRecommendationTableInRange(lines []string, start, end int) (marketSummaryReportMarkdownTable, bool) {
	for idx := start; idx < end; {
		if !strings.HasPrefix(strings.TrimSpace(lines[idx]), "|") {
			idx++
			continue
		}
		blockStart := idx
		for idx < end && strings.HasPrefix(strings.TrimSpace(lines[idx]), "|") {
			idx++
		}
		blockEnd := idx
		if blockEnd-blockStart < 2 {
			continue
		}

		headers := splitMarkdownTableLine(lines[blockStart])
		stockIndex := marketSummaryReportStockColumnIndex(headers)
		separatorCells := splitMarkdownTableLine(lines[blockStart+1])
		if stockIndex < 0 || !isMarkdownSeparatorRow(separatorCells) {
			continue
		}

		table := marketSummaryReportMarkdownTable{
			start:      blockStart,
			end:        blockEnd,
			header:     lines[blockStart],
			separator:  lines[blockStart+1],
			stockIndex: stockIndex,
			rows:       make([]marketSummaryReportMarkdownRow, 0, blockEnd-blockStart-2),
		}
		for rowIndex := blockStart + 2; rowIndex < blockEnd; rowIndex++ {
			cells := splitMarkdownTableLine(lines[rowIndex])
			if len(cells) == 0 || isMarkdownSeparatorRow(cells) {
				continue
			}
			row := marketSummaryReportMarkdownRow{raw: lines[rowIndex]}
			if stockIndex < len(cells) {
				row.code = extractMarketSummaryReportStockCode(cells[stockIndex])
			}
			table.rows = append(table.rows, row)
		}
		return table, true
	}
	return marketSummaryReportMarkdownTable{}, false
}

func marketSummaryReportStockColumnIndex(headers []string) int {
	for idx, header := range headers {
		if strings.Contains(normalizeMarkdownCell(header), "股票") {
			return idx
		}
	}
	return -1
}

func parseMarketSummaryMarkdownHeading(line string) (int, string, bool) {
	trimmed := strings.TrimSpace(line)
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level == 0 || level > 6 {
		return 0, "", false
	}
	if level < len(trimmed) && trimmed[level] != ' ' && trimmed[level] != '\t' {
		return 0, "", false
	}
	return level, strings.TrimSpace(trimmed[level:]), true
}

func extractMarketSummaryReportStockCode(text string) string {
	match := marketSummaryReportStockCodePattern.FindString(normalizeMarkdownCell(text))
	return normalizeMarketSummaryReportCode(match)
}

func normalizeMarketSummaryReportCode(code string) string {
	match := strings.ToUpper(strings.TrimSpace(marketSummaryReportStockCodePattern.FindString(code)))
	if match == "" {
		return ""
	}
	if len(match) == 8 && (strings.HasPrefix(match, "SH") || strings.HasPrefix(match, "SZ") || strings.HasPrefix(match, "BJ")) {
		return match[2:] + "." + match[:2]
	}
	if strings.Contains(match, ".") {
		return match
	}
	return normalizeMarketSummaryStockCode(match)
}

func normalizeMarketSummaryReportCodeSet(codes []string) (map[string]struct{}, []string) {
	result := make(map[string]struct{}, len(codes))
	order := make([]string, 0, len(codes))
	for _, raw := range codes {
		code := normalizeMarketSummaryReportCode(raw)
		if code == "" {
			continue
		}
		if _, exists := result[code]; exists {
			continue
		}
		result[code] = struct{}{}
		order = append(order, code)
	}
	return result, order
}

func splitMarketSummaryReportLines(text string) ([]string, string) {
	lineEnding := "\n"
	if strings.Contains(text, "\r\n") {
		lineEnding = "\r\n"
	}
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	return strings.Split(normalized, "\n"), lineEnding
}
