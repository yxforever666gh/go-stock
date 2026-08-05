package data

import (
	"errors"
	"go-stock/backend/db"
	"go-stock/backend/models"
	"strings"
	"time"

	"github.com/duke-git/lancet/v2/datetime"
	"github.com/duke-git/lancet/v2/strutil"
	"gorm.io/gorm"
)

type AIResponseResultService struct{}

var ErrMarketSummaryAIHistoryReadOnly = errors.New("market summary AI history is read-only")

func NewAIResponseResultService() *AIResponseResultService {
	return &AIResponseResultService{}
}

// GetAIResponseResultList 分页查询AI响应结果
func (s *AIResponseResultService) GetAIResponseResultList(query models.AIResponseResultQuery) (*models.AIResponseResultPageData, error) {
	var list []models.AIResponseResult
	var total int64

	q := db.Dao.Model(&models.AIResponseResult{})

	// 构建查询条件
	if query.ChatId != "" {
		q.Where("chat_id LIKE ?", "%"+query.ChatId+"%")
	}
	if query.ModelName != "" {
		q.Or("model_name LIKE ?", "%"+query.ModelName+"%")
		q.Or("provider_name LIKE ?", "%"+query.ModelName+"%")
	}
	if query.StockCode != "" {
		q.Or("stock_code LIKE ?", "%"+query.StockCode+"%")
	}
	if query.Question != "" {
		q.Or("question LIKE ?", "%"+query.Question+"%")
	}
	if query.StartDate != "" && query.EndDate != "" {
		query.StartDate = strutil.ReplaceWithMap(query.StartDate, map[string]string{
			"T": " ",
			"Z": "",
		})
		query.EndDate = strutil.ReplaceWithMap(query.EndDate, map[string]string{
			"T": " ",
			"Z": "",
		})

		startDate, err := time.Parse("2006-01-02 15:04:05", query.StartDate)
		if err != nil {
			startDate, _ = time.Parse("2006-01-02", query.StartDate)
		}

		endDate, err := time.Parse("2006-01-02 15:04:05", query.EndDate)
		if err != nil {
			endDate, _ = time.Parse("2006-01-02", query.EndDate)
		}
		q = q.Where("created_at BETWEEN ? AND ?", datetime.BeginOfDay(startDate), datetime.EndOfDay(endDate))
		//q = q.Where("created_at BETWEEN ? AND ?", query.StartDate, query.EndDate)
	}

	// 计算总数
	err := q.Count(&total).Error
	if err != nil {
		return nil, err
	}

	// 设置默认分页参数
	page := query.Page
	pageSize := query.PageSize
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 10
	}

	// 执行分页查询
	offset := (page - 1) * pageSize
	err = q.Offset(offset).Limit(pageSize).Order("created_at DESC").Find(&list).Error
	if err != nil {
		return nil, err
	}
	for i := range list {
		sanitizeAIResponseResultForDisplay(&list[i])
	}

	totalPages := int((total + int64(pageSize) - 1) / int64(pageSize))

	return &models.AIResponseResultPageData{
		List:       list,
		Total:      total,
		Page:       page,
		PageSize:   pageSize,
		TotalPages: totalPages,
	}, nil
}

// DeleteAIResponseResult 根据ID删除AI响应结果
func (s *AIResponseResultService) DeleteAIResponseResult(id uint) error {
	return deleteAIResponseResults([]uint{id})
}

// BatchDeleteAIResponseResult 批量删除AI响应结果
func (s *AIResponseResultService) BatchDeleteAIResponseResult(ids []uint) error {
	return deleteAIResponseResults(ids)
}

func deleteAIResponseResults(ids []uint) error {
	if len(ids) == 0 {
		return nil
	}
	if db.Dao == nil {
		return errors.New("AI response database is unavailable")
	}
	return db.Dao.Transaction(func(tx *gorm.DB) error {
		var rows []models.AIResponseResult
		if err := tx.Unscoped().
			Select("id", "stock_code", "stock_name").
			Where("id IN ?", ids).
			Find(&rows).Error; err != nil {
			return err
		}
		for idx := range rows {
			if isMarketSummaryAIResponse(rows[idx]) {
				return ErrMarketSummaryAIHistoryReadOnly
			}
		}
		return tx.Where("id IN ?", ids).Delete(&models.AIResponseResult{}).Error
	})
}

// AIResponseResult predates strategy cohorts, so the stable market-summary
// identity protects both legacy and current raw reports.
func isMarketSummaryAIResponse(result models.AIResponseResult) bool {
	const marketSummaryIdentity = "市场资讯"
	return strings.TrimSpace(result.StockCode) == marketSummaryIdentity ||
		strings.TrimSpace(result.StockName) == marketSummaryIdentity
}
