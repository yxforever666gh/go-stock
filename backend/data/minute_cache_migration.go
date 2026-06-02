package data

import (
	"fmt"
	"strings"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"

	"gorm.io/gorm"
)

const minuteCacheMigrationBatchSize = 5000

type MinuteCacheMigrationSummary struct {
	LegacyRows   int64
	MinuteDBRows int64
	MigratedRows int64
	StockCount   int64
}

func MigrateMinuteCacheToMinuteDB() (MinuteCacheMigrationSummary, error) {
	summary := MinuteCacheMigrationSummary{}
	if db.Dao == nil {
		return summary, fmt.Errorf("main db is not initialized")
	}
	if db.MinuteDao == nil {
		return summary, fmt.Errorf("minute db is not initialized")
	}
	if !legacyMinuteBarTableAvailable() {
		return summary, nil
	}

	if err := db.Dao.Model(&models.AiRecommendMinuteBar{}).Count(&summary.LegacyRows).Error; err != nil {
		return summary, err
	}
	if summary.LegacyRows == 0 {
		err := countMinuteCacheMigrationSummary(&summary)
		return summary, err
	}

	rows := make([]models.AiRecommendMinuteBar, 0, minuteCacheMigrationBatchSize)
	err := db.Dao.Model(&models.AiRecommendMinuteBar{}).
		Order("id ASC").
		FindInBatches(&rows, minuteCacheMigrationBatchSize, func(tx *gorm.DB, batch int) error {
			if err := upsertMinuteBarsToMinuteDB(rows); err != nil {
				return err
			}
			summary.MigratedRows += int64(len(rows))
			return nil
		}).Error
	if err != nil {
		return summary, err
	}
	if err := validateMinuteCacheMigration(); err != nil {
		return summary, err
	}
	err = countMinuteCacheMigrationSummary(&summary)
	return summary, err
}

func countMinuteCacheMigrationSummary(summary *MinuteCacheMigrationSummary) error {
	if summary == nil {
		return nil
	}
	if db.MinuteDao == nil {
		return fmt.Errorf("minute db is not initialized")
	}
	if err := db.MinuteDao.Model(&minuteCacheDBBar{}).Count(&summary.MinuteDBRows).Error; err != nil {
		return err
	}
	type stockCountRow struct {
		Count int64 `gorm:"column:count"`
	}
	row := stockCountRow{}
	err := db.MinuteDao.Model(&minuteCacheDBBar{}).
		Select("COUNT(DISTINCT stock_code) AS count").
		Scan(&row).Error
	summary.StockCount = row.Count
	return err
}

func OptimizeMinuteCacheDB() error {
	if db.MinuteDao == nil {
		return fmt.Errorf("minute db is not initialized")
	}
	for _, stmt := range []string{
		"PRAGMA optimize",
		"ANALYZE minute_bar",
		"PRAGMA wal_checkpoint(TRUNCATE)",
	} {
		if err := db.MinuteDao.Exec(stmt).Error; err != nil {
			return err
		}
	}
	return nil
}

func minuteCacheMigrationWarning() string {
	if db.Dao == nil || db.MinuteDao == nil {
		return ""
	}
	if !legacyMinuteBarTableAvailable() {
		return ""
	}
	if hasAnyMinuteCacheRows(db.MinuteDao, "minute_bar") {
		return ""
	}
	if !hasAnyMinuteCacheRows(db.Dao, "ai_recommend_minute_bar") {
		return ""
	}
	return "分钟线新库尚未迁移：当前仍在读取旧表 fallback，建议执行 go run . migrate-minute-db"
}

func hasAnyMinuteCacheRows(dao *gorm.DB, table string) bool {
	if dao == nil || strings.TrimSpace(table) == "" {
		return false
	}
	type oneRow struct {
		One int `gorm:"column:one"`
	}
	row := oneRow{}
	err := dao.Raw("SELECT 1 AS one FROM " + table + " LIMIT 1").Scan(&row).Error
	return err == nil && row.One == 1
}

func validateMinuteCacheMigration() error {
	if err := validateMinuteCacheMigrationByStock(); err != nil {
		return err
	}
	return validateMinuteCacheMigrationSamples()
}

func validateMinuteCacheMigrationByStock() error {
	type legacyRow struct {
		StockCode string `gorm:"column:stock_code"`
		Count     int64  `gorm:"column:count"`
		Start     string `gorm:"column:start_time"`
		End       string `gorm:"column:end_time"`
	}
	rows := make([]legacyRow, 0)
	err := db.Dao.Model(&models.AiRecommendMinuteBar{}).
		Select("stock_code, COUNT(*) AS count, MIN(trade_time) AS start_time, MAX(trade_time) AS end_time").
		Group("stock_code").
		Scan(&rows).Error
	if err != nil {
		return err
	}
	for _, row := range rows {
		code := strings.ToUpper(strings.TrimSpace(row.StockCode))
		if code == "" {
			continue
		}
		start, okStart := parseSQLiteDateTimeText(row.Start)
		end, okEnd := parseSQLiteDateTimeText(row.End)
		if !okStart || !okEnd {
			return fmt.Errorf("legacy minute range parse failed for %s", code)
		}
		type newRow struct {
			Count int64  `gorm:"column:count"`
			Start *int64 `gorm:"column:start_time"`
			End   *int64 `gorm:"column:end_time"`
		}
		newScope := newRow{}
		err := db.MinuteDao.Model(&minuteCacheDBBar{}).
			Select("COUNT(*) AS count, MIN(trade_time) AS start_time, MAX(trade_time) AS end_time").
			Where("stock_code = ?", code).
			Scan(&newScope).Error
		if err != nil {
			return err
		}
		if newScope.Count < row.Count {
			return fmt.Errorf("minute migration count mismatch for %s: legacy=%d minute=%d", code, row.Count, newScope.Count)
		}
		if newScope.Start == nil || newScope.End == nil {
			return fmt.Errorf("minute migration range missing for %s", code)
		}
		if *newScope.Start > minuteTimeMillis(start) || *newScope.End < minuteTimeMillis(end) {
			return fmt.Errorf("minute migration range mismatch for %s", code)
		}
	}
	return nil
}

func validateMinuteCacheMigrationSamples() error {
	samples := make([]models.AiRecommendMinuteBar, 0, 20)
	err := db.Dao.Model(&models.AiRecommendMinuteBar{}).
		Order("stock_code ASC, trade_time ASC").
		Limit(20).
		Find(&samples).Error
	if err != nil {
		return err
	}
	for _, sample := range samples {
		row := minuteCacheDBBar{}
		err := db.MinuteDao.Model(&minuteCacheDBBar{}).
			Where("stock_code = ? AND trade_time = ?", strings.ToUpper(strings.TrimSpace(sample.StockCode)), minuteTimeMillis(sample.TradeTime)).
			First(&row).Error
		if err != nil {
			return err
		}
		if !sameMinuteCacheSample(row, sample) {
			return fmt.Errorf("minute migration sample mismatch for %s %s", sample.StockCode, sample.TradeTime.In(cnLocation()).Format(time.RFC3339))
		}
	}
	return nil
}

func sameMinuteCacheSample(row minuteCacheDBBar, sample models.AiRecommendMinuteBar) bool {
	return row.Open == sample.Open &&
		row.High == sample.High &&
		row.Low == sample.Low &&
		row.Close == sample.Close &&
		row.Volume == sample.Volume &&
		row.Amount == sample.Amount &&
		strings.TrimSpace(row.Source) == strings.TrimSpace(sample.Source)
}
