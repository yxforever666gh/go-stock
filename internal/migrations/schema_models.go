package migrations

import "gorm.io/gorm"

// IndexBasic is the migration-owned schema contract for the legacy index
// table. Keep it stable so published migration definitions remain immutable.
type IndexBasic struct {
	gorm.Model
	TsCode        string  `json:"ts_code" gorm:"index"`
	Symbol        string  `json:"symbol" gorm:"index"`
	Name          string  `json:"name" gorm:"index"`
	FullName      string  `json:"fullname"`
	IndexType     string  `json:"index_type"`
	IndexCategory string  `json:"category"`
	Market        string  `json:"market"`
	ListDate      string  `json:"list_date"`
	BaseDate      string  `json:"base_date"`
	BasePoint     float64 `json:"base_point"`
	Publisher     string  `json:"publisher"`
	WeightRule    string  `json:"weight_rule"`
	DESC          string  `json:"desc"`
}

func (IndexBasic) TableName() string { return "tushare_index_basic" }
