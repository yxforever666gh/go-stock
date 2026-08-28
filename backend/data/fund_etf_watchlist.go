package data

import (
	"fmt"
	"strings"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"

	"gorm.io/gorm/clause"
)

func (f *FundApi) GetFollowedETFs() ([]models.ETFWatchlistItem, error) {
	items := make([]models.ETFWatchlistItem, 0)
	err := db.Dao.Order("updated_at DESC, code ASC").Find(&items).Error
	return items, err
}

func (f *FundApi) FollowETF(item models.ETFWatchlistItem) error {
	now := time.Now().UTC()
	code, ok := NormalizeETFCode(item.Code)
	if !ok {
		return fmt.Errorf("invalid ETF code %q", item.Code)
	}
	expectedMarket := "SZ"
	if strings.HasPrefix(code, "sh") {
		expectedMarket = "SH"
	}
	item.Name = strings.TrimSpace(item.Name)
	item.Market = strings.ToUpper(strings.TrimSpace(item.Market))
	if item.Market != expectedMarket {
		return fmt.Errorf("ETF market %q does not match code %s", item.Market, code)
	}
	item.Code = code
	item.Category = strings.ToLower(strings.TrimSpace(item.Category))
	item.CreatedAt = now
	item.UpdatedAt = now
	return db.Dao.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "code"}},
		DoUpdates: clause.Assignments(map[string]any{
			"name": item.Name, "market": item.Market, "category": item.Category, "updated_at": now,
		}),
	}).Create(&item).Error
}

func (f *FundApi) UnFollowETF(code string) (bool, error) {
	canonical, ok := NormalizeETFCode(code)
	if !ok {
		return false, fmt.Errorf("invalid ETF code %q", code)
	}
	result := db.Dao.Where("code = ?", canonical).Delete(&models.ETFWatchlistItem{})
	return result.RowsAffected > 0, result.Error
}
