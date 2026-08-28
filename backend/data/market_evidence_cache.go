package data

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"go-stock/backend/marketdata"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	tradeTickRetentionDays = 30
	auctionRetentionDays   = 180
)

type marketTradeTickCache struct {
	AssetType string  `gorm:"column:asset_type;primaryKey"`
	Symbol    string  `gorm:"column:symbol;primaryKey"`
	TradedAt  int64   `gorm:"column:traded_at;primaryKey"`
	Sequence  int64   `gorm:"column:sequence;primaryKey"`
	Price     float64 `gorm:"column:price;not null"`
	Volume    float64 `gorm:"column:volume;not null"`
	Amount    float64 `gorm:"column:amount"`
	Side      string  `gorm:"column:side"`
	Source    string  `gorm:"column:source;not null"`
	UpdatedAt int64   `gorm:"column:updated_at;not null"`
}

func (marketTradeTickCache) TableName() string { return "market_trade_tick" }

type marketAuctionSnapshotCache struct {
	AssetType       string   `gorm:"column:asset_type;primaryKey"`
	Symbol          string   `gorm:"column:symbol;primaryKey"`
	TradeDate       string   `gorm:"column:trade_date;primaryKey"`
	ObservedAt      int64    `gorm:"column:observed_at;primaryKey"`
	Phase           string   `gorm:"column:phase;primaryKey"`
	IndicativePrice *float64 `gorm:"column:indicative_price"`
	MatchedVolume   *float64 `gorm:"column:matched_volume"`
	MatchedAmount   *float64 `gorm:"column:matched_amount"`
	UnmatchedVolume *float64 `gorm:"column:unmatched_volume"`
	UnmatchedSide   string   `gorm:"column:unmatched_side"`
	Source          string   `gorm:"column:source;not null"`
	UpdatedAt       int64    `gorm:"column:updated_at;not null"`
}

func (marketAuctionSnapshotCache) TableName() string { return "market_auction_snapshot" }

func (s *MarketEvidenceService) isHistoricalDate(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && value != s.now().In(shanghaiDataLocation()).Format("2006-01-02")
}

func (s *MarketEvidenceService) cacheTradesEnvelope(ctx context.Context, request marketdata.ProviderRequest, envelope marketdata.DataEnvelope[TradesData]) marketdata.DataEnvelope[TradesData] {
	if !cacheableEnvelopeStatus(envelope.Status) || len(envelope.Data.Items) == 0 {
		return envelope
	}
	if s.minuteDB == nil {
		return markCacheIssue(envelope, "cache_unavailable", errors.New("minute database is unavailable"))
	}
	date := firstNonEmpty(envelope.Data.Date, request.Date, s.now().In(shanghaiDataLocation()).Format("2006-01-02"))
	assetType, symbol := firstNonEmpty(envelope.Data.AssetType, request.AssetType), firstNonEmpty(envelope.Data.Code, request.Code)
	offset, _ := strconv.ParseInt(strings.TrimSpace(request.Cursor), 10, 64)
	updatedAt := s.now().UnixMilli()
	rows := make([]marketTradeTickCache, 0, len(envelope.Data.Items))
	for index, item := range envelope.Data.Items {
		at, err := evidenceCacheTime(date, item.Time)
		if err != nil {
			envelope = markCacheIssue(envelope, "cache_time_invalid", err)
			continue
		}
		rows = append(rows, marketTradeTickCache{AssetType: assetType, Symbol: symbol, TradedAt: at.UnixMilli(), Sequence: offset + int64(index), Price: item.Price, Volume: item.Volume, Amount: item.Amount, Side: item.Side, Source: envelope.Source, UpdatedAt: updatedAt})
	}
	if len(rows) > 0 {
		err := s.minuteDB.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "asset_type"}, {Name: "symbol"}, {Name: "traded_at"}, {Name: "sequence"}}, DoUpdates: clause.AssignmentColumns([]string{"price", "volume", "amount", "side", "source", "updated_at"})}).Create(&rows).Error
		if err != nil {
			envelope = markCacheIssue(envelope, "cache_write_failed", err)
		}
	}
	return envelope
}

func (s *MarketEvidenceService) cacheAuctionEnvelope(ctx context.Context, request marketdata.ProviderRequest, envelope marketdata.DataEnvelope[AuctionData]) marketdata.DataEnvelope[AuctionData] {
	if !cacheableEnvelopeStatus(envelope.Status) || len(envelope.Data.Snapshots) == 0 {
		return envelope
	}
	if s.minuteDB == nil {
		return markCacheIssue(envelope, "cache_unavailable", errors.New("minute database is unavailable"))
	}
	date := firstNonEmpty(envelope.Data.Date, request.Date, s.now().In(shanghaiDataLocation()).Format("2006-01-02"))
	assetType, symbol := firstNonEmpty(envelope.Data.AssetType, request.AssetType), firstNonEmpty(envelope.Data.Code, request.Code)
	updatedAt := s.now().UnixMilli()
	rows := make([]marketAuctionSnapshotCache, 0, len(envelope.Data.Snapshots)+1)
	for _, item := range envelope.Data.Snapshots {
		at, err := evidenceCacheTime(date, item.Time)
		if err != nil {
			envelope = markCacheIssue(envelope, "cache_time_invalid", err)
			continue
		}
		rows = append(rows, auctionCacheRow(assetType, symbol, date, auctionPhase(at), at, item, envelope.Source, updatedAt))
	}
	if envelope.Data.FinalSnapshot != nil {
		if at, err := evidenceCacheTime(date, envelope.Data.FinalSnapshot.Time); err == nil {
			rows = append(rows, auctionCacheRow(assetType, symbol, date, "final", at, *envelope.Data.FinalSnapshot, envelope.Source, updatedAt))
		} else {
			envelope = markCacheIssue(envelope, "cache_time_invalid", err)
		}
	} else if len(envelope.Data.Snapshots) > 0 {
		item := envelope.Data.Snapshots[len(envelope.Data.Snapshots)-1]
		if at, err := evidenceCacheTime(date, item.Time); err == nil {
			rows = append(rows, auctionCacheRow(assetType, symbol, date, "final", at, item, envelope.Source, updatedAt))
		}
	}
	if len(rows) > 0 {
		err := s.minuteDB.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "asset_type"}, {Name: "symbol"}, {Name: "trade_date"}, {Name: "observed_at"}, {Name: "phase"}}, DoUpdates: clause.AssignmentColumns([]string{"indicative_price", "matched_volume", "matched_amount", "unmatched_volume", "unmatched_side", "source", "updated_at"})}).Create(&rows).Error
		if err != nil {
			envelope = markCacheIssue(envelope, "cache_write_failed", err)
		}
	}
	return envelope
}

func (s *MarketEvidenceService) cachedTrades(ctx context.Context, request marketdata.ProviderRequest) marketdata.DataEnvelope[TradesData] {
	empty := TradesData{Code: request.Code, AssetType: request.AssetType, Date: request.Date, Items: []TradeTick{}}
	if s.minuteDB == nil {
		return cacheUnavailable(empty, s.now(), "cache_unavailable", "minute database is unavailable")
	}
	start, err := evidenceCacheTime(request.Date, "00:00:00")
	if err != nil {
		return cacheUnavailable(empty, s.now(), "invalid_date", err.Error())
	}
	offset, _ := strconv.Atoi(strings.TrimSpace(request.Cursor))
	if offset < 0 {
		offset = 0
	}
	limit := request.Limit
	if limit <= 0 {
		limit = 100
	}
	var rows []marketTradeTickCache
	err = s.minuteDB.WithContext(ctx).Where("asset_type = ? AND symbol = ? AND traded_at >= ? AND traded_at < ?", request.AssetType, request.Code, start.UnixMilli(), start.AddDate(0, 0, 1).UnixMilli()).Order("traded_at, sequence").Offset(offset).Limit(limit + 1).Find(&rows).Error
	if err != nil {
		return cacheUnavailable(empty, s.now(), "cache_read_failed", err.Error())
	}
	if len(rows) == 0 {
		return cacheUnavailable(empty, s.now(), "cache_miss", "历史逐笔缓存不存在或已超过30日保留期")
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	items := make([]TradeTick, 0, len(rows))
	var latestAt, latestUpdated time.Time
	for _, row := range rows {
		at := time.UnixMilli(row.TradedAt).In(shanghaiDataLocation())
		updated := time.UnixMilli(row.UpdatedAt)
		if at.After(latestAt) {
			latestAt = at
		}
		if updated.After(latestUpdated) {
			latestUpdated = updated
		}
		items = append(items, TradeTick{Time: at.Format("15:04:05"), Price: row.Price, Volume: row.Volume, Amount: row.Amount, Side: row.Side})
	}
	next := ""
	if hasMore {
		next = strconv.Itoa(offset + len(rows))
	}
	available := latestUpdated
	sources := cacheProvenanceSources("market_trade_tick", rows, latestAt, available, func(row marketTradeTickCache) string { return row.Source })
	return withEvidenceProfile(marketdata.DataEnvelope[TradesData]{Data: TradesData{Code: request.Code, AssetType: request.AssetType, Date: request.Date, Items: items, NextCursor: next}, Source: "cache", AsOf: latestAt, FetchedAt: s.now(), Status: marketdata.StatusOK, Errors: []marketdata.DataError{}, Sources: sources})
}

func (s *MarketEvidenceService) cachedAuction(ctx context.Context, request marketdata.ProviderRequest) marketdata.DataEnvelope[AuctionData] {
	empty := AuctionData{Code: request.Code, AssetType: request.AssetType, Date: request.Date, Snapshots: []AuctionSnapshot{}}
	if s.minuteDB == nil {
		return cacheUnavailable(empty, s.now(), "cache_unavailable", "minute database is unavailable")
	}
	var rows []marketAuctionSnapshotCache
	err := s.minuteDB.WithContext(ctx).Where("asset_type = ? AND symbol = ? AND trade_date = ?", request.AssetType, request.Code, request.Date).Order("observed_at, phase").Find(&rows).Error
	if err != nil {
		return cacheUnavailable(empty, s.now(), "cache_read_failed", err.Error())
	}
	if len(rows) == 0 {
		return cacheUnavailable(empty, s.now(), "cache_miss", "历史竞价缓存不存在或已超过180日保留期")
	}
	snapshots := make([]AuctionSnapshot, 0, len(rows))
	var final *AuctionSnapshot
	var latestAt, latestUpdated time.Time
	for _, row := range rows {
		at := time.UnixMilli(row.ObservedAt).In(shanghaiDataLocation())
		updated := time.UnixMilli(row.UpdatedAt)
		if at.After(latestAt) {
			latestAt = at
		}
		if updated.After(latestUpdated) {
			latestUpdated = updated
		}
		item := AuctionSnapshot{Time: at.Format("15:04:05"), Price: derefFloat(row.IndicativePrice), MatchedVolume: derefFloat(row.MatchedVolume), MatchedAmount: derefFloat(row.MatchedAmount), UnmatchedVolume: row.UnmatchedVolume, UnmatchedSide: row.UnmatchedSide}
		if row.Phase == "final" {
			copyItem := item
			final = &copyItem
			continue
		}
		snapshots = append(snapshots, item)
	}
	if final == nil && len(snapshots) > 0 {
		copyItem := snapshots[len(snapshots)-1]
		final = &copyItem
	}
	if len(snapshots) == 0 && final != nil {
		snapshots = append(snapshots, *final)
	}
	var strength *float64
	if final != nil && len(snapshots) > 0 && snapshots[0].Price > 0 {
		value := (final.Price - snapshots[0].Price) / snapshots[0].Price * 100
		strength = &value
	}
	available := latestUpdated
	sources := cacheProvenanceSources("market_auction_snapshot", rows, latestAt, available, func(row marketAuctionSnapshotCache) string { return row.Source })
	if len(sources) > 0 {
		sources[0].Status = marketdata.StatusPartial
	}
	return withEvidenceProfile(marketdata.DataEnvelope[AuctionData]{Data: AuctionData{Code: request.Code, AssetType: request.AssetType, Date: request.Date, Snapshots: snapshots, FinalSnapshot: final, AuctionStrength: strength}, Source: "cache", AsOf: latestAt, FetchedAt: s.now(), Status: marketdata.StatusPartial, Errors: []marketdata.DataError{}, Sources: sources, Warnings: []string{"历史缓存不含前收盘价，gapPct 返回 null"}})
}

func auctionCacheRow(assetType, symbol, date, phase string, at time.Time, item AuctionSnapshot, source string, updatedAt int64) marketAuctionSnapshotCache {
	price, matchedVolume, matchedAmount := item.Price, item.MatchedVolume, item.MatchedAmount
	return marketAuctionSnapshotCache{AssetType: assetType, Symbol: symbol, TradeDate: date, ObservedAt: at.UnixMilli(), Phase: phase, IndicativePrice: &price, MatchedVolume: &matchedVolume, MatchedAmount: &matchedAmount, UnmatchedVolume: item.UnmatchedVolume, UnmatchedSide: item.UnmatchedSide, Source: source, UpdatedAt: updatedAt}
}

func auctionPhase(at time.Time) string {
	clock := at.Format("15:04:05")
	if clock < "09:20:00" {
		return "cancellable"
	}
	if clock < "09:25:00" {
		return "non_cancellable"
	}
	return "opening_match"
}

func evidenceCacheTime(date, clock string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(date)+" "+normalizeTickTime(clock), shanghaiDataLocation())
}

func cleanupTradeTickCache(ctx context.Context, database *gorm.DB, retainDates int) error {
	if database == nil {
		return errors.New("minute database is unavailable")
	}
	if retainDates <= 0 {
		return errors.New("trade tick retention must be positive")
	}
	var dates []string
	if err := database.WithContext(ctx).Raw("SELECT DISTINCT strftime('%Y-%m-%d', traded_at / 1000, 'unixepoch', '+8 hours') AS trade_date FROM market_trade_tick WHERE strftime('%w', traded_at / 1000, 'unixepoch', '+8 hours') NOT IN ('0','6') ORDER BY trade_date DESC LIMIT ?", retainDates+1).Scan(&dates).Error; err != nil {
		return err
	}
	if len(dates) <= retainDates {
		return nil
	}
	threshold, err := evidenceCacheTime(dates[retainDates-1], "00:00:00")
	if err != nil {
		return err
	}
	return database.WithContext(ctx).Where("traded_at < ?", threshold.UnixMilli()).Delete(&marketTradeTickCache{}).Error
}

func cleanupAuctionSnapshotCache(ctx context.Context, database *gorm.DB, retainDates int) error {
	if database == nil {
		return errors.New("minute database is unavailable")
	}
	if retainDates <= 0 {
		return errors.New("auction retention must be positive")
	}
	var dates []string
	if err := database.WithContext(ctx).Raw("SELECT DISTINCT trade_date FROM market_auction_snapshot WHERE strftime('%w', trade_date) NOT IN ('0','6') ORDER BY trade_date DESC LIMIT ?", retainDates+1).Scan(&dates).Error; err != nil {
		return err
	}
	if len(dates) <= retainDates {
		return nil
	}
	return database.WithContext(ctx).Where("trade_date < ?", dates[retainDates-1]).Delete(&marketAuctionSnapshotCache{}).Error
}

func cacheProvenanceSources[T any](table string, rows []T, asOf, available time.Time, source func(T) string) []marketdata.SourceState {
	result := []marketdata.SourceState{{Provider: "cache", Status: marketdata.StatusOK, AsOf: asOf, AvailableAt: &available, SourceRef: table}}
	seen := map[string]struct{}{}
	for _, row := range rows {
		name := strings.TrimSpace(source(row))
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, marketdata.SourceState{Provider: name, Status: marketdata.StatusOK, AsOf: asOf, AvailableAt: &available, SourceRef: table})
	}
	return result
}

func cacheableEnvelopeStatus(status string) bool {
	return status == marketdata.StatusOK || status == marketdata.StatusPartial || status == marketdata.StatusStale
}

func markCacheIssue[T any](envelope marketdata.DataEnvelope[T], code string, err error) marketdata.DataEnvelope[T] {
	if envelope.Status == marketdata.StatusOK || envelope.Status == marketdata.StatusStale {
		envelope.Status = marketdata.StatusPartial
	}
	message := err.Error()
	envelope.Warnings = append(envelope.Warnings, "本地缓存: "+message)
	envelope.Errors = append(envelope.Errors, marketdata.DataError{Provider: "cache", Code: code, Message: message})
	return envelope
}

func cacheUnavailable[T any](data T, now time.Time, code, message string) marketdata.DataEnvelope[T] {
	return withEvidenceProfile(marketdata.DataEnvelope[T]{Data: data, Source: "cache", AsOf: time.Time{}, FetchedAt: now, Status: marketdata.StatusUnavailable, Errors: []marketdata.DataError{{Provider: "cache", Code: code, Message: message}}, Sources: []marketdata.SourceState{{Provider: "cache", Status: marketdata.StatusUnavailable, Message: message}}})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
func derefFloat(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}
