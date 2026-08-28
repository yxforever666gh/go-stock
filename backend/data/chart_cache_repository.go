package data

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type marketChartBarRow struct {
	AssetType  string  `gorm:"column:asset_type;primaryKey"`
	Symbol     string  `gorm:"column:symbol;primaryKey"`
	Period     string  `gorm:"column:period;primaryKey"`
	Adjustment string  `gorm:"column:adjustment;primaryKey"`
	BarTime    int64   `gorm:"column:bar_time;primaryKey"`
	Open       float64 `gorm:"column:open"`
	High       float64 `gorm:"column:high"`
	Low        float64 `gorm:"column:low"`
	Close      float64 `gorm:"column:close"`
	Volume     float64 `gorm:"column:volume"`
	Amount     float64 `gorm:"column:amount"`
	Source     string  `gorm:"column:source"`
	UpdatedAt  int64   `gorm:"column:updated_at"`
}

func (marketChartBarRow) TableName() string { return "market_bar_cache" }

type chartCacheSnapshot struct {
	Bars      []ChartBar
	UpdatedAt time.Time
}

func loadChartBarsFromCache(database *gorm.DB, request ChartRequest) (chartCacheSnapshot, error) {
	result := chartCacheSnapshot{Bars: []ChartBar{}}
	if database == nil {
		return result, errors.New("minute database is unavailable")
	}
	rows := make([]marketChartBarRow, 0)
	err := database.Model(&marketChartBarRow{}).
		Where("asset_type = ? AND symbol = ? AND period = ? AND adjustment = ? AND bar_time >= ? AND bar_time <= ?",
			request.Instrument.AssetType, request.Instrument.Code, request.Period, request.Adjustment, request.From.UnixMilli(), request.To.UnixMilli()).
		Order("bar_time ASC").Find(&rows).Error
	if err != nil {
		return result, err
	}
	if request.Limit > 0 && len(rows) > request.Limit {
		rows = rows[len(rows)-request.Limit:]
	}
	var latestUpdate int64
	for _, row := range rows {
		if row.UpdatedAt > latestUpdate {
			latestUpdate = row.UpdatedAt
		}
		result.Bars = append(result.Bars, ChartBar{At: time.UnixMilli(row.BarTime).In(cnLocation()), Open: row.Open, High: row.High,
			Low: row.Low, Close: row.Close, Volume: row.Volume, Amount: row.Amount, Source: strings.TrimSpace(row.Source)})
	}
	if latestUpdate > 0 {
		result.UpdatedAt = time.UnixMilli(latestUpdate).In(cnLocation())
	}
	return result, nil
}

func upsertChartBarsToCache(database *gorm.DB, request ChartRequest, bars []ChartBar, now time.Time) error {
	if database == nil {
		return errors.New("minute database is unavailable")
	}
	rows := make([]marketChartBarRow, 0, len(bars))
	updatedAt := now.UnixMilli()
	for _, bar := range bars {
		if !validPublicChartBar(bar) || bar.At.IsZero() {
			continue
		}
		rows = append(rows, marketChartBarRow{AssetType: request.Instrument.AssetType, Symbol: request.Instrument.Code,
			Period: request.Period, Adjustment: request.Adjustment, BarTime: bar.At.UnixMilli(), Open: bar.Open, High: bar.High,
			Low: bar.Low, Close: bar.Close, Volume: bar.Volume, Amount: bar.Amount, Source: strings.TrimSpace(bar.Source), UpdatedAt: updatedAt})
	}
	if len(rows) == 0 {
		return nil
	}
	return database.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "asset_type"}, {Name: "symbol"}, {Name: "period"}, {Name: "adjustment"}, {Name: "bar_time"}},
		DoUpdates: clause.AssignmentColumns([]string{"open", "high", "low", "close", "volume", "amount", "source", "updated_at"}),
	}).CreateInBatches(rows, 800).Error
}
