package data

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"

	"gorm.io/gorm"
)

const marketSummaryDiagnosticDefaultLimit = 10

func MarketSummaryCurrentVersion() string {
	return marketSummaryCurrentVersion
}

func SaveMarketSummaryRunDiagnostic(item *models.MarketSummaryRunDiagnostic) error {
	if item == nil {
		return nil
	}
	if err := requireStrategyProductionLive(nil, db.Dao); err != nil {
		return err
	}
	if strings.TrimSpace(item.RunID) == "" {
		item.RunID = "market-summary-" + time.Now().Format("20060102150405.000000000")
	}
	if strings.TrimSpace(item.SummaryVersion) == "" {
		item.SummaryVersion = marketSummaryCurrentVersion
	}
	if strings.TrimSpace(item.SummaryVersion) != marketSummaryCurrentVersion {
		return fmt.Errorf("strategy cohort %s is frozen; diagnostics are read-only", strings.TrimSpace(item.SummaryVersion))
	}
	if item.StartedAt.IsZero() {
		item.StartedAt = time.Now()
	}
	if item.FinishedAt.IsZero() {
		item.FinishedAt = time.Now()
	}
	item.EmptyRun = item.ProductionCount == 0
	return db.Dao.Create(item).Error
}

func GetMarketSummaryRunDiagnostics(query models.MarketSummaryRunDiagnosticQuery) (models.MarketSummaryRunDiagnosticSummary, error) {
	query.SummaryVersion = resolveMarketSummaryDiagnosticVersion(query.SummaryVersion, query.StrategyCohort)
	if query.Limit <= 0 {
		query.Limit = marketSummaryDiagnosticDefaultLimit
	}
	if query.Limit > 100 {
		query.Limit = 100
	}
	q := applyMarketSummaryDiagnosticQuery(db.Dao.Model(&models.MarketSummaryRunDiagnostic{}), query)
	rows := make([]models.MarketSummaryRunDiagnostic, 0, query.Limit)
	if err := q.Order("started_at DESC, id DESC").Limit(query.Limit).Find(&rows).Error; err != nil {
		return models.MarketSummaryRunDiagnosticSummary{}, err
	}
	emptyCount, err := GetMarketSummaryEmptyRunCount(query)
	if err != nil {
		return models.MarketSummaryRunDiagnosticSummary{}, err
	}
	top, err := GetMarketSummaryBlockedReasonTop(query)
	if err != nil {
		return models.MarketSummaryRunDiagnosticSummary{}, err
	}
	downgradeTop, err := GetMarketSummaryProductionDowngradeReasonTop(query)
	if err != nil {
		return models.MarketSummaryRunDiagnosticSummary{}, err
	}
	summary := models.MarketSummaryRunDiagnosticSummary{
		List:                         rows,
		BlockedReasonTop:             top,
		ProductionDowngradeReasonTop: downgradeTop,
		EmptyRunCount:                emptyCount,
		ConsecutiveEmptyRunCount:     countConsecutiveMarketSummaryEmptyRuns(rows),
		StrategyCohort:               normalizeStrategyCohort(query.StrategyCohort, strategyCohortCurrent),
		SummaryVersion:               query.SummaryVersion,
	}
	if len(rows) > 0 {
		latest := rows[0]
		summary.Latest = &latest
	}
	return summary, nil
}

func GetMarketSummaryEmptyRunCount(query models.MarketSummaryRunDiagnosticQuery) (int64, error) {
	query.SummaryVersion = resolveMarketSummaryDiagnosticVersion(query.SummaryVersion, query.StrategyCohort)
	q := applyMarketSummaryDiagnosticQuery(db.Dao.Model(&models.MarketSummaryRunDiagnostic{}), query)
	var count int64
	if err := q.Where("empty_run = ?", true).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func GetMarketSummaryBlockedReasonTop(query models.MarketSummaryRunDiagnosticQuery) ([]models.MarketSummaryBlockedReasonItem, error) {
	query.SummaryVersion = resolveMarketSummaryDiagnosticVersion(query.SummaryVersion, query.StrategyCohort)
	limit := 5
	q := applyMarketSummaryDiagnosticQuery(db.Dao.Model(&models.MarketSummaryRunDiagnostic{}), query)
	rows := make([]models.MarketSummaryRunDiagnostic, 0, 100)
	if err := q.Order("started_at DESC, id DESC").Limit(100).Find(&rows).Error; err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, row := range rows {
		for _, item := range decodeMarketSummaryBlockedReasons(row.BlockedReasonTop) {
			reason := normalizeMarketSummaryBlockedReason(item.Reason)
			if reason == "" || item.Count <= 0 {
				continue
			}
			counts[reason] += item.Count
		}
	}
	items := make([]models.MarketSummaryBlockedReasonItem, 0, len(counts))
	for reason, count := range counts {
		items = append(items, models.MarketSummaryBlockedReasonItem{Reason: reason, Count: count})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Count != items[j].Count {
			return items[i].Count > items[j].Count
		}
		return items[i].Reason < items[j].Reason
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func GetMarketSummaryProductionDowngradeReasonTop(query models.MarketSummaryRunDiagnosticQuery) ([]models.MarketSummaryBlockedReasonItem, error) {
	query.SummaryVersion = resolveMarketSummaryDiagnosticVersion(query.SummaryVersion, query.StrategyCohort)
	limit := 5
	q := applyMarketSummaryDiagnosticQuery(db.Dao.Model(&models.MarketSummaryRunDiagnostic{}), query)
	rows := make([]models.MarketSummaryRunDiagnostic, 0, 100)
	if err := q.Order("started_at DESC, id DESC").Limit(100).Find(&rows).Error; err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, row := range rows {
		for _, item := range decodeMarketSummaryBlockedReasons(row.ProductionDowngradeReasonTop) {
			reason := normalizeMarketSummaryProductionDowngradeReason(item.Reason)
			if reason == "" || item.Count <= 0 {
				continue
			}
			counts[reason] += item.Count
		}
	}
	items := make([]models.MarketSummaryBlockedReasonItem, 0, len(counts))
	for reason, count := range counts {
		items = append(items, models.MarketSummaryBlockedReasonItem{Reason: reason, Count: count})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Count != items[j].Count {
			return items[i].Count > items[j].Count
		}
		return items[i].Reason < items[j].Reason
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func applyMarketSummaryDiagnosticQuery(q *gorm.DB, query models.MarketSummaryRunDiagnosticQuery) *gorm.DB {
	if q == nil {
		return q
	}
	version := strings.TrimSpace(query.SummaryVersion)
	switch version {
	case "", strategyCohortAll:
	case strategyCohortLegacy:
		q = q.Where("(TRIM(COALESCE(summary_version, '')) = '' OR summary_version NOT IN ?)", marketSummaryKnownVersions())
	default:
		q = q.Where("summary_version = ?", version)
	}
	if start := parseMarketSummaryDiagnosticDate(query.StartDate, false); !start.IsZero() {
		q = q.Where("started_at >= ?", start)
	}
	if end := parseMarketSummaryDiagnosticDate(query.EndDate, true); !end.IsZero() {
		q = q.Where("started_at < ?", end)
	}
	return q
}

func resolveMarketSummaryDiagnosticVersion(summaryVersion, cohort string) string {
	if strings.TrimSpace(summaryVersion) != "" {
		return normalizeStrategyCohort(summaryVersion, strategyCohortCurrent)
	}
	normalized := normalizeStrategyCohort(cohort, strategyCohortCurrent)
	switch normalized {
	case strategyCohortAll, strategyCohortLegacy:
		return normalized
	case strategyCohortCurrent:
		return marketSummaryCurrentVersion
	default:
		return normalized
	}
}

func countConsecutiveMarketSummaryEmptyRuns(rows []models.MarketSummaryRunDiagnostic) int {
	count := 0
	for _, row := range rows {
		if !row.EmptyRun {
			break
		}
		count++
	}
	return count
}

func parseMarketSummaryDiagnosticDate(raw string, endExclusive bool) time.Time {
	text := strings.TrimSpace(raw)
	if text == "" {
		return time.Time{}
	}
	loc := cnLocation()
	if t, err := time.ParseInLocation("2006-01-02", text, loc); err == nil {
		if endExclusive {
			return t.Add(24 * time.Hour)
		}
		return t
	}
	if t, err := parseDateTimeWithFallback(normalizeDateTime(text)); err == nil {
		if endExclusive {
			return t.Add(time.Second)
		}
		return t
	}
	return time.Time{}
}

func encodeMarketSummaryBlockedReasons(items []models.MarketSummaryBlockedReasonItem) string {
	if len(items) == 0 {
		return "[]"
	}
	b, err := json.Marshal(items)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func EncodeMarketSummaryBlockedReasons(items []models.MarketSummaryBlockedReasonItem) string {
	return encodeMarketSummaryBlockedReasons(items)
}

func decodeMarketSummaryBlockedReasons(raw string) []models.MarketSummaryBlockedReasonItem {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	items := []models.MarketSummaryBlockedReasonItem{}
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil
	}
	return items
}

func normalizeMarketSummaryBlockedReason(reason string) string {
	text := strings.TrimSpace(reason)
	lower := strings.ToLower(text)
	switch {
	case text == "":
		return ""
	case strings.Contains(text, "候选池为空") || strings.Contains(lower, "candidate pool"):
		return "候选池为空"
	case strings.Contains(text, "discovery") || strings.Contains(text, "发现"):
		return "discovery 为空"
	case strings.Contains(text, "证据") || strings.Contains(text, "核验"):
		return "证据核验不足"
	case strings.Contains(text, "同日") || strings.Contains(text, "重复"):
		return "同日已推荐排除"
	case strings.Contains(text, "交易计划") || strings.Contains(text, "源头质量") || strings.Contains(text, "硬校验"):
		return "保存前交易计划硬校验"
	case strings.Contains(text, "激活规则") || strings.Contains(text, "结构化"):
		return "激活规则缺失"
	case strings.Contains(text, "价格锚点") || strings.Contains(text, "偏离") || strings.Contains(text, "20%"):
		return "价格锚点偏离"
	case strings.Contains(text, "盈亏比") || strings.Contains(text, "止损空间") || strings.Contains(text, "止损"):
		return "盈亏比/止损空间不达标"
	case strings.Contains(text, "数据缺失") || strings.Contains(text, "分钟线") || strings.Contains(text, "行情"):
		return "数据缺失或分钟线缺失"
	default:
		return text
	}
}

func normalizeMarketSummaryProductionDowngradeReason(reason string) string {
	text := strings.TrimSpace(reason)
	lower := strings.ToLower(text)
	switch {
	case text == "":
		return ""
	case strings.Contains(text, "盈亏比") || strings.Contains(text, "鐩堜簭姣?") ||
		strings.Contains(text, "止损空间") || strings.Contains(text, "姝㈡崯绌洪棿"):
		return "盈亏比/止损空间不达标"
	case strings.Contains(text, "买入区间") || strings.Contains(text, "涔板叆鍖洪棿"):
		return "缺少有效买入区间"
	case strings.Contains(text, "止盈止损") || strings.Contains(text, "止盈") || strings.Contains(text, "止损") ||
		strings.Contains(text, "姝㈢泩") || strings.Contains(text, "姝㈡崯"):
		return "缺少有效止盈止损"
	case strings.Contains(text, "价格锚点") || strings.Contains(text, "偏离") || strings.Contains(text, "20%") ||
		strings.Contains(text, "浠锋牸閿氱偣") || strings.Contains(text, "鍋忕"):
		return "价格锚点偏离"
	case strings.Contains(text, "证据") || strings.Contains(text, "核验") || strings.Contains(text, "璇佹嵁") || strings.Contains(text, "鏍搁獙"):
		return "证据核验不足"
	case strings.Contains(text, "数据缺失") || strings.Contains(text, "分钟线") || strings.Contains(lower, "minute") ||
		strings.Contains(text, "鏁版嵁缂哄け") || strings.Contains(text, "鍒嗛挓绾?"):
		return "数据缺失或分钟线缺失"
	default:
		return normalizeMarketSummaryBlockedReason(text)
	}
}
