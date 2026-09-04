package stocks

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"go-stock/backend/logger"
	"go-stock/backend/models"
	appservice "go-stock/internal/service"

	"gorm.io/gorm"
)

type MasterSource interface {
	FetchValidatedStockMaster(context.Context) ([]models.StockBasic, models.StockMasterRefreshResult, error)
	FetchValidatedPublicStockMaster(context.Context) ([]models.StockBasic, models.StockMasterRefreshResult, error)
}

type Dependencies struct {
	Database              *gorm.DB
	Master                MasterSource
	StockMasterSeed       func() ([]models.StockBasic, models.StockMasterRefreshResult, error)
	FetchIndex            func(context.Context) ([]models.IndexBasic, error)
	ListGroupStocks       func(int) []models.GroupStock
	StockKLine            func(string, int64) *[]models.KLineData
	StockMinutePriceLine  func(string, string) map[string]any
	Search                func(string) map[string]any
	SearchWithFingerprint func(string, string, int) map[string]any
	Realtime              func(context.Context, ...string) (*[]models.StockInfo, error)
}

type Service struct {
	dependencies Dependencies
	persisting   atomic.Bool
}

func NewService(dependencies Dependencies) *Service {
	return &Service{dependencies: dependencies}
}

func (s *Service) RefreshStockBaseInfo(ctx context.Context) (models.StockMasterRefreshResult, error) {
	if s == nil || s.dependencies.Database == nil {
		return models.StockMasterRefreshResult{}, fmt.Errorf("stock master database is unavailable")
	}
	if s.dependencies.Master == nil {
		return models.StockMasterRefreshResult{}, fmt.Errorf("stock master refresh source is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	rows, result, primaryErr := s.dependencies.Master.FetchValidatedStockMaster(ctx)
	if primaryErr != nil {
		var publicErr error
		rows, result, publicErr = s.dependencies.Master.FetchValidatedPublicStockMaster(ctx)
		if publicErr != nil {
			var rowCount int64
			if countErr := s.dependencies.Database.WithContext(ctx).Model(&models.StockBasic{}).Count(&rowCount).Error; countErr != nil {
				return result, errors.Join(primaryErr, publicErr, countErr)
			}
			if rowCount != 0 || s.dependencies.StockMasterSeed == nil {
				return result, errors.Join(primaryErr, publicErr)
			}
			var seedErr error
			rows, result, seedErr = s.dependencies.StockMasterSeed()
			if seedErr != nil {
				return result, errors.Join(primaryErr, publicErr, seedErr)
			}
			result.Warnings = append(result.Warnings, "Tushare and controlled public stock master were unavailable; initialized empty database from embedded seed")
		} else {
			result.Warnings = append(result.Warnings, "Tushare stock master was unavailable; used controlled public source")
		}
	}
	if err := replaceStockMaster(ctx, s.dependencies.Database, rows, result); err != nil {
		return result, err
	}
	result.Replaced = true
	return result, nil
}

func (s *Service) RefreshIndexBaseInfo() {
	if err := s.refreshIndexBaseInfo(context.Background()); err != nil {
		logger.SugaredLogger.Errorf("refresh index master failed: %v", err)
	}
}

func (s *Service) refreshIndexBaseInfo(ctx context.Context) error {
	if s == nil || s.dependencies.Database == nil {
		return errors.New("index database is unavailable")
	}
	if s.dependencies.FetchIndex == nil {
		return errors.New("index provider is unavailable")
	}
	rows, err := s.dependencies.FetchIndex(ctx)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return errors.New("index provider returned no rows")
	}
	return s.dependencies.Database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for index := range rows {
			row := rows[index]
			row.ID = 0
			var stored models.IndexBasic
			if err := tx.Where("ts_code = ?", row.TsCode).Assign(row).FirstOrCreate(&stored).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Service) Follow(stockCode string) (string, error) {
	if s == nil || s.dependencies.Database == nil || s.dependencies.Realtime == nil {
		return "关注失败", fmt.Errorf("%w: stock service is unavailable", appservice.ErrOperationFailed)
	}
	stockInfos, err := s.GetStockCodeRealTimeData(stockCode)
	if err != nil || stockInfos == nil || len(*stockInfos) == 0 {
		return "关注失败", fmt.Errorf("%w: load stock quote: %v", appservice.ErrOperationFailed, err)
	}
	code := normalizeWatchlistCode(stockCode)
	return s.followWithQuote(code, (*stockInfos)[0])
}

func (s *Service) followWithQuote(code string, stockInfo models.StockInfo) (string, error) {
	database := s.dependencies.Database
	var count int64
	if err := database.Model(&models.FollowedStock{}).Where("is_del = ?", 0).Count(&count).Error; err != nil {
		return "关注失败", fmt.Errorf("%w: count followed stocks: %v", appservice.ErrOperationFailed, err)
	}
	if count >= 63 {
		return "最多只能关注63只股票", fmt.Errorf("%w: stock watchlist limit reached", appservice.ErrConflict)
	}
	var existing models.FollowedStock
	err := database.Where("stock_code = ? AND is_del = ?", code, 0).First(&existing).Error
	if err == nil {
		return "已经关注了", fmt.Errorf("%w: stock %s is already followed", appservice.ErrConflict, code)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return "关注失败", fmt.Errorf("%w: query followed stock: %v", appservice.ErrOperationFailed, err)
	}
	var maxSort int64
	if err := database.Model(&models.FollowedStock{}).Select("COALESCE(MAX(sort), 0)").Scan(&maxSort).Error; err != nil {
		return "关注失败", fmt.Errorf("%w: query stock sort: %v", appservice.ErrOperationFailed, err)
	}
	price, err := strconv.ParseFloat(strings.TrimSpace(stockInfo.Price), 64)
	if err != nil {
		return "关注失败", fmt.Errorf("%w: invalid stock price %q", appservice.ErrOperationFailed, stockInfo.Price)
	}
	row := models.FollowedStock{
		StockCode: code, Name: stockInfo.Name, Price: price, Time: time.Now(), Sort: maxSort + 1,
		AlarmChangePercent: 3, AlarmPrice: price + 1,
	}
	if err := database.Create(&row).Error; err != nil {
		return "关注失败", fmt.Errorf("%w: follow stock %s: %v", appservice.ErrOperationFailed, code, err)
	}
	return "关注成功", nil
}

func (s *Service) UnFollow(stockCode string) (string, error) {
	if s == nil || s.dependencies.Database == nil {
		return "取消关注失败", fmt.Errorf("%w: stock database is unavailable", appservice.ErrOperationFailed)
	}
	result := s.dependencies.Database.Where("stock_code = ?", normalizeWatchlistCode(stockCode)).Delete(&models.FollowedStock{})
	if result.Error != nil {
		return "取消关注失败", fmt.Errorf("%w: unfollow stock: %v", appservice.ErrOperationFailed, result.Error)
	}
	return "取消关注成功", nil
}

func (s *Service) GetFollowList(groupID int) *[]models.FollowedStock {
	items := make([]models.FollowedStock, 0)
	if s == nil || s.dependencies.Database == nil {
		return &items
	}
	query := s.dependencies.Database.Order("sort ASC, time DESC")
	if groupID != 0 && s.dependencies.ListGroupStocks != nil {
		memberships := s.dependencies.ListGroupStocks(groupID)
		codes := make([]string, 0, len(memberships))
		for _, membership := range memberships {
			codes = append(codes, membership.StockCode)
		}
		query = query.Where("stock_code IN ?", codes)
	}
	_ = query.Find(&items).Error
	return &items
}

func (s *Service) GetStockList(key string) []models.StockBasic {
	result := make([]models.StockBasic, 0)
	if s == nil || s.dependencies.Database == nil {
		return []models.StockBasic{}
	}
	pattern := "%" + key + "%"
	_ = s.dependencies.Database.Where("name LIKE ? OR ts_code LIKE ?", pattern, pattern).Find(&result).Error
	var indexes []models.IndexBasic
	_ = s.dependencies.Database.Where("market IN ?", []string{"SSE", "SZSE"}).Where("name LIKE ? OR ts_code LIKE ?", pattern, pattern).Find(&indexes).Error
	var hongKong []models.StockInfoHK
	_ = s.dependencies.Database.Where("name LIKE ? OR code LIKE ?", pattern, pattern).Find(&hongKong).Error
	var unitedStates []models.StockInfoUS
	_ = s.dependencies.Database.Where("name LIKE ? OR code LIKE ? OR e_name LIKE ?", pattern, pattern, pattern).Find(&unitedStates).Error
	for _, item := range indexes {
		result = append(result, models.StockBasic{TsCode: item.TsCode, Name: item.Name, Fullname: item.FullName,
			Symbol: item.Symbol, Market: item.Market, ListDate: item.ListDate})
	}
	for _, item := range hongKong {
		result = append(result, models.StockBasic{TsCode: item.Code, Name: item.Name, Fullname: item.Name, Market: "HK"})
	}
	for _, item := range unitedStates {
		result = append(result, models.StockBasic{TsCode: strings.ToLower(strings.Replace(item.Code, "us", "gb_", 1)),
			Name: item.Name, Fullname: item.Name, Market: "US"})
	}
	return result
}

func (s *Service) SetCostPriceAndVolume(stockCode string, price float64, volume int64) (string, error) {
	if s == nil || s.dependencies.Database == nil {
		return "设置失败", fmt.Errorf("%w: stock database is unavailable", appservice.ErrOperationFailed)
	}
	err := s.dependencies.Database.Model(&models.FollowedStock{}).Where("stock_code = ?", normalizeWatchlistCode(stockCode)).Updates(map[string]any{"cost_price": price, "volume": volume}).Error
	if err != nil {
		return "设置失败", fmt.Errorf("%w: update stock cost: %v", appservice.ErrOperationFailed, err)
	}
	return "设置成功", nil
}

func (s *Service) SetAlarmChangePercent(value, alarmPrice float64, stockCode string) (string, error) {
	if s == nil || s.dependencies.Database == nil {
		return "设置失败", fmt.Errorf("%w: stock database is unavailable", appservice.ErrOperationFailed)
	}
	err := s.dependencies.Database.Model(&models.FollowedStock{}).Where("stock_code = ?", normalizeWatchlistCode(stockCode)).Updates(map[string]any{"alarm_change_percent": value, "alarm_price": alarmPrice}).Error
	if err != nil {
		return "设置失败", fmt.Errorf("%w: update stock alarm: %v", appservice.ErrOperationFailed, err)
	}
	return "设置成功", nil
}

func (s *Service) SetStockSort(newSort int64, stockCode string) {
	if s == nil || s.dependencies.Database == nil {
		return
	}
	code := normalizeWatchlistCode(stockCode)
	_ = s.dependencies.Database.Transaction(func(tx *gorm.DB) error {
		var current models.FollowedStock
		if err := tx.Where("stock_code = ?", code).First(&current).Error; err != nil {
			return err
		}
		if current.Sort == newSort {
			return nil
		}
		query := tx.Model(&models.FollowedStock{})
		if newSort < current.Sort {
			if err := query.Where("sort >= ? AND sort < ?", newSort, current.Sort).Update("sort", gorm.Expr("sort + 1")).Error; err != nil {
				return err
			}
		} else {
			if err := query.Where("sort > ? AND sort <= ?", current.Sort, newSort).Update("sort", gorm.Expr("sort - 1")).Error; err != nil {
				return err
			}
		}
		return tx.Model(&models.FollowedStock{}).Where("stock_code = ?", code).Update("sort", newSort).Error
	})
}

func (s *Service) GetAllFollowedStocks() []models.FollowedStock {
	items := make([]models.FollowedStock, 0)
	if s != nil && s.dependencies.Database != nil {
		_ = s.dependencies.Database.Find(&items).Error
	}
	return items
}

func (s *Service) GetFollowedStockDetail(stockCode string) *models.FollowedStock {
	item := &models.FollowedStock{StockCode: stockCode}
	if s != nil && s.dependencies.Database != nil {
		_ = s.dependencies.Database.Where("stock_code = ?", stockCode).Preload("Groups").Preload("Groups.GroupInfo").First(item).Error
	}
	return item
}

func (s *Service) UpdateFollowPrice(stockCode string, price float64) {
	if s != nil && s.dependencies.Database != nil {
		_ = s.dependencies.Database.Model(&models.FollowedStock{}).Where("stock_code = ?", stockCode).Update("price", price).Error
	}
}

func (s *Service) GetStockKLine(stockCode string, days int64) *[]models.KLineData {
	if s == nil || s.dependencies.StockKLine == nil {
		empty := []models.KLineData{}
		return &empty
	}
	return s.dependencies.StockKLine(stockCode, days)
}

func (s *Service) GetStockMinutePriceLineData(stockCode, stockName string) map[string]any {
	if s == nil || s.dependencies.StockMinutePriceLine == nil {
		return map[string]any{"stockCode": stockCode, "stockName": stockName}
	}
	return s.dependencies.StockMinutePriceLine(stockCode, stockName)
}

func (s *Service) SearchStock(words string) map[string]any {
	if s == nil || s.dependencies.Search == nil {
		return map[string]any{}
	}
	return s.dependencies.Search(words)
}

func (s *Service) SearchStockWithFingerprint(words, fingerprint string, pageSize int) map[string]any {
	if s == nil || s.dependencies.SearchWithFingerprint == nil {
		return map[string]any{}
	}
	return s.dependencies.SearchWithFingerprint(words, fingerprint, pageSize)
}

func (s *Service) GetStockCodeRealTimeData(stockCodes ...string) (*[]models.StockInfo, error) {
	if s == nil || s.dependencies.Realtime == nil {
		empty := []models.StockInfo{}
		return &empty, fmt.Errorf("%w: realtime stock provider is unavailable", appservice.ErrOperationFailed)
	}
	items, err := s.dependencies.Realtime(context.Background(), stockCodes...)
	if err == nil && items != nil {
		s.persistRealtimeStockInfos(*items)
	}
	return items, err
}

func (s *Service) persistRealtimeStockInfos(items []models.StockInfo) {
	if len(items) == 0 || s.dependencies.Database == nil {
		return
	}
	rows := make([]models.StockInfo, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		item.Code = strings.ToLower(strings.TrimSpace(item.Code))
		if item.Code == "" {
			continue
		}
		if _, exists := seen[item.Code]; exists {
			continue
		}
		seen[item.Code] = struct{}{}
		rows = append(rows, item)
	}
	if len(rows) == 0 || !s.persisting.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer s.persisting.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		err := s.dependencies.Database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			for index := range rows {
				row := rows[index]
				result := tx.Model(&models.StockInfo{}).Where("code = ?", row.Code).Updates(&row)
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected == 0 {
					if err := tx.Create(&row).Error; err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			logger.SugaredLogger.Errorf("persist realtime stock info failed: %v", err)
		}
	}()
}

func normalizeWatchlistCode(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	if strings.HasPrefix(code, "us") {
		return "gb_" + strings.TrimPrefix(code, "us")
	}
	return code
}
