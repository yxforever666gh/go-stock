package models

import "time"

// ETFWatchlistItem is intentionally isolated from the off-exchange fund
// watchlist and from every research, recommendation and trading model.
type ETFWatchlistItem struct {
	Code      string    `gorm:"column:code;primaryKey;size:16" json:"code"`
	Name      string    `gorm:"column:name;not null" json:"name"`
	Market    string    `gorm:"column:market;not null;size:8" json:"market"`
	Category  string    `gorm:"column:category;not null;size:32" json:"category"`
	CreatedAt time.Time `gorm:"column:created_at;not null" json:"createdAt"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null" json:"updatedAt"`
}

func (ETFWatchlistItem) TableName() string { return "etf_watchlist" }
