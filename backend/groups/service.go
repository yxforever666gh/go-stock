package groups

import (
	"errors"

	"go-stock/backend/models"

	"gorm.io/gorm"
)

type Service struct {
	database *gorm.DB
}

func NewService(database *gorm.DB) *Service {
	return &Service{database: database}
}

func (s *Service) AddGroup(group models.Group) bool {
	if s == nil || s.database == nil {
		return false
	}
	return s.database.Transaction(func(tx *gorm.DB) error {
		var existing models.Group
		err := tx.Where("sort = ?", group.Sort).First(&existing).Error
		switch {
		case err == nil:
			if err := tx.Model(&models.Group{}).Where("sort >= ?", group.Sort).Update("sort", gorm.Expr("sort + ?", 1)).Error; err != nil {
				return err
			}
		case !errors.Is(err, gorm.ErrRecordNotFound):
			return err
		}
		return tx.Create(&group).Error
	}) == nil
}

func (s *Service) GetGroupList() []models.Group {
	groups := make([]models.Group, 0)
	if s == nil || s.database == nil {
		return groups
	}
	_ = s.database.Order("sort ASC").Find(&groups).Error
	return groups
}

func (s *Service) UpdateGroupSort(id, newSort int) bool {
	if s == nil || s.database == nil {
		return false
	}
	return s.database.Transaction(func(tx *gorm.DB) error {
		var current models.Group
		if err := tx.First(&current, id).Error; err != nil {
			return err
		}
		if current.Sort == newSort {
			return nil
		}
		query := tx.Model(&models.Group{}).Where("id != ?", id)
		if newSort > current.Sort {
			query = query.Where("sort > ? AND sort <= ?", current.Sort, newSort)
			if err := query.Update("sort", gorm.Expr("sort - ?", 1)).Error; err != nil {
				return err
			}
		} else {
			query = query.Where("sort >= ? AND sort < ?", newSort, current.Sort)
			if err := query.Update("sort", gorm.Expr("sort + ?", 1)).Error; err != nil {
				return err
			}
		}
		return tx.Model(&models.Group{}).Where("id = ?", id).Update("sort", newSort).Error
	}) == nil
}

func (s *Service) InitializeGroupSort() bool {
	if s == nil || s.database == nil {
		return false
	}
	return s.database.Transaction(func(tx *gorm.DB) error {
		var groups []models.Group
		if err := tx.Order("created_at ASC").Find(&groups).Error; err != nil {
			return err
		}
		for index, group := range groups {
			if err := tx.Model(&models.Group{}).Where("id = ?", group.ID).Update("sort", index+1).Error; err != nil {
				return err
			}
		}
		return nil
	}) == nil
}

func (s *Service) GetGroupStockList(groupID int) []models.GroupStock {
	stocks := make([]models.GroupStock, 0)
	if s == nil || s.database == nil {
		return stocks
	}
	_ = s.database.Preload("GroupInfo").Where("group_id = ?", groupID).Find(&stocks).Error
	return stocks
}

func (s *Service) AddStockGroup(groupID int, stockCode string) bool {
	if s == nil || s.database == nil {
		return false
	}
	row := models.GroupStock{GroupId: groupID, StockCode: stockCode}
	return s.database.Where("group_id = ? AND stock_code = ?", groupID, stockCode).FirstOrCreate(&row).Error == nil
}

func (s *Service) RemoveStockGroup(code, _ string, groupID int) bool {
	if s == nil || s.database == nil {
		return false
	}
	return s.database.Where("group_id = ? AND stock_code = ?", groupID, code).Delete(&models.GroupStock{}).Error == nil
}

func (s *Service) RemoveGroup(groupID int) bool {
	if s == nil || s.database == nil {
		return false
	}
	return s.database.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ?", groupID).Delete(&models.Group{}).Error; err != nil {
			return err
		}
		return tx.Where("group_id = ?", groupID).Delete(&models.GroupStock{}).Error
	}) == nil
}
