package data

import (
	"strings"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/models"

	"github.com/duke-git/lancet/v2/datetime"
	"github.com/duke-git/lancet/v2/strutil"
)

type EmailSendLogService struct{}

func NewEmailSendLogService() *EmailSendLogService {
	return &EmailSendLogService{}
}

func (s *EmailSendLogService) GetEmailSendLogList(query models.EmailSendLogQuery) (*models.EmailSendLogPageData, error) {
	var list []models.EmailSendLog
	var total int64

	q := db.Dao.Model(&models.EmailSendLog{})

	if sendType := strings.TrimSpace(query.SendType); sendType != "" {
		q = q.Where("send_type = ?", sendType)
	}
	if status := strings.TrimSpace(query.Status); status != "" {
		q = q.Where("status = ?", status)
	}
	if recipient := strings.TrimSpace(query.Recipient); recipient != "" {
		q = q.Where("recipients LIKE ?", "%"+recipient+"%")
	}
	if subject := strings.TrimSpace(query.Subject); subject != "" {
		q = q.Where("subject LIKE ?", "%"+subject+"%")
	}
	if reportStockCode := strings.TrimSpace(query.ReportStockCode); reportStockCode != "" {
		q = q.Where("report_stock_code LIKE ?", "%"+reportStockCode+"%")
	}
	if reportStockName := strings.TrimSpace(query.ReportStockName); reportStockName != "" {
		q = q.Where("report_stock_name LIKE ?", "%"+reportStockName+"%")
	}

	if startDate, endDate, ok := parseEmailSendLogRange(query.StartDate, query.EndDate); ok {
		q = q.Where("triggered_at BETWEEN ? AND ?", startDate, endDate)
	}

	if err := q.Count(&total).Error; err != nil {
		return nil, err
	}

	page := query.Page
	pageSize := query.PageSize
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}

	offset := (page - 1) * pageSize
	if err := q.Offset(offset).Limit(pageSize).Order("triggered_at DESC, id DESC").Find(&list).Error; err != nil {
		return nil, err
	}

	totalPages := int((total + int64(pageSize) - 1) / int64(pageSize))
	return &models.EmailSendLogPageData{
		List:       list,
		Total:      total,
		Page:       page,
		PageSize:   pageSize,
		TotalPages: totalPages,
	}, nil
}

func parseEmailSendLogRange(startRaw, endRaw string) (time.Time, time.Time, bool) {
	startRaw = strutil.ReplaceWithMap(strings.TrimSpace(startRaw), map[string]string{
		"T": " ",
		"Z": "",
	})
	endRaw = strutil.ReplaceWithMap(strings.TrimSpace(endRaw), map[string]string{
		"T": " ",
		"Z": "",
	})
	if startRaw == "" || endRaw == "" {
		return time.Time{}, time.Time{}, false
	}

	startDate, ok := parseEmailSendLogTime(startRaw)
	if !ok {
		return time.Time{}, time.Time{}, false
	}
	endDate, ok := parseEmailSendLogTime(endRaw)
	if !ok {
		return time.Time{}, time.Time{}, false
	}
	return datetime.BeginOfDay(startDate), datetime.EndOfDay(endDate), true
}

func parseEmailSendLogTime(raw string) (time.Time, bool) {
	if raw == "" {
		return time.Time{}, false
	}
	if parsed, err := time.Parse("2006-01-02 15:04:05", raw); err == nil {
		return parsed, true
	}
	if parsed, err := time.Parse("2006-01-02", raw); err == nil {
		return parsed, true
	}
	return time.Time{}, false
}
