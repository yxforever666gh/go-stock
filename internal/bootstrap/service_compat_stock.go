package bootstrap

import (
	"context"
	"errors"
	"fmt"

	"go-stock/backend/data"
	"go-stock/backend/models"

	"gorm.io/gorm"
)

func (a *compatibilityServiceAdapter) ReplaceStockBaseInfo(
	ctx context.Context,
	domestic []models.StockBasic,
	hongKong []models.StockInfoHK,
	unitedStates []models.StockInfoUS,
) error {
	return a.main.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := replaceAllRows(tx, domestic); err != nil {
			return err
		}
		if err := replaceAllRows(tx, hongKong); err != nil {
			return err
		}
		return replaceAllRows(tx, unitedStates)
	})
}

func replaceAllRows[T any](tx *gorm.DB, rows []T) error {
	var model T
	if err := tx.Unscoped().Where("1 = 1").Delete(&model).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	return tx.CreateInBatches(&rows, 400).Error
}

type stockMasterRefreshSource interface {
	FetchValidatedStockMaster(context.Context) ([]models.StockBasic, models.StockMasterRefreshResult, error)
	FetchValidatedPublicStockMaster(context.Context) ([]models.StockBasic, models.StockMasterRefreshResult, error)
}

func (a *compatibilityServiceAdapter) RefreshStockBaseInfo(ctx context.Context) (models.StockMasterRefreshResult, error) {
	if a == nil || a.main == nil {
		return models.StockMasterRefreshResult{}, fmt.Errorf("stock master database is unavailable")
	}
	return refreshStockBaseInfo(ctx, a.main, data.NewStockDataApi(), a.stockMasterSeed)
}

func refreshStockBaseInfo(
	ctx context.Context,
	database *gorm.DB,
	source stockMasterRefreshSource,
	seed func() ([]models.StockBasic, models.StockMasterRefreshResult, error),
) (models.StockMasterRefreshResult, error) {
	if database == nil {
		return models.StockMasterRefreshResult{}, fmt.Errorf("stock master database is unavailable")
	}
	if source == nil {
		return models.StockMasterRefreshResult{}, fmt.Errorf("stock master refresh source is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	rows, result, primaryErr := source.FetchValidatedStockMaster(ctx)
	if primaryErr != nil {
		var publicErr error
		rows, result, publicErr = source.FetchValidatedPublicStockMaster(ctx)
		if publicErr != nil {
			var rowCount int64
			if countErr := database.WithContext(ctx).Model(&models.StockBasic{}).Count(&rowCount).Error; countErr != nil {
				return result, errors.Join(primaryErr, publicErr, countErr)
			}
			if rowCount != 0 || seed == nil {
				return result, errors.Join(primaryErr, publicErr)
			}
			var seedErr error
			rows, result, seedErr = seed()
			if seedErr != nil {
				return result, errors.Join(primaryErr, publicErr, seedErr)
			}
			result.Warnings = append(result.Warnings, "Tushare and controlled public stock master were unavailable; initialized empty database from embedded seed")
		} else {
			result.Warnings = append(result.Warnings, "Tushare stock master was unavailable; used controlled public source")
		}
	}
	if err := data.ReplaceStockMasterWithMetadata(ctx, database, rows, result); err != nil {
		return result, err
	}
	result.Replaced = true
	return result, nil
}

func (*compatibilityServiceAdapter) RefreshIndexBaseInfo() {
	data.NewStockDataApi().GetIndexBasic()
}

func (a *compatibilityServiceAdapter) AddGroup(group models.Group) bool {
	return data.NewStockGroupApi(a.main).AddGroup(group)
}

func (a *compatibilityServiceAdapter) GetGroupList() []models.Group {
	return data.NewStockGroupApi(a.main).GetGroupList()
}

func (a *compatibilityServiceAdapter) UpdateGroupSort(id, newSort int) bool {
	return data.NewStockGroupApi(a.main).UpdateGroupSort(id, newSort)
}

func (a *compatibilityServiceAdapter) InitializeGroupSort() bool {
	return data.NewStockGroupApi(a.main).InitializeGroupSort()
}

func (a *compatibilityServiceAdapter) GetGroupStockList(groupID int) []models.GroupStock {
	return data.NewStockGroupApi(a.main).GetGroupStockByGroupId(groupID)
}

func (a *compatibilityServiceAdapter) AddStockGroup(groupID int, stockCode string) bool {
	return data.NewStockGroupApi(a.main).AddStockGroup(groupID, stockCode)
}

func (a *compatibilityServiceAdapter) RemoveStockGroup(code, name string, groupID int) bool {
	return data.NewStockGroupApi(a.main).RemoveStockGroup(code, name, groupID)
}

func (a *compatibilityServiceAdapter) RemoveGroup(groupID int) bool {
	return data.NewStockGroupApi(a.main).RemoveGroup(groupID)
}

func (*compatibilityServiceAdapter) Follow(stockCode string) string {
	return data.NewStockDataApi().Follow(stockCode)
}

func (*compatibilityServiceAdapter) UnFollow(stockCode string) string {
	return data.NewStockDataApi().UnFollow(stockCode)
}

func (*compatibilityServiceAdapter) GetFollowList(groupID int) *[]models.FollowedStock {
	return data.NewStockDataApi().GetFollowList(groupID)
}

func (*compatibilityServiceAdapter) GetStockList(key string) []models.StockBasic {
	return data.NewStockDataApi().GetStockList(key)
}

func (*compatibilityServiceAdapter) SetCostPriceAndVolume(stockCode string, price float64, volume int64) string {
	return data.NewStockDataApi().SetCostPriceAndVolume(price, volume, stockCode)
}

func (*compatibilityServiceAdapter) SetAlarmChangePercent(value, alarmPrice float64, stockCode string) string {
	return data.NewStockDataApi().SetAlarmChangePercent(value, alarmPrice, stockCode)
}

func (*compatibilityServiceAdapter) SetStockSort(sort int64, stockCode string) {
	data.NewStockDataApi().SetStockSort(sort, stockCode)
}

func (*compatibilityServiceAdapter) SetStockAICron(cronText, stockCode string) {
	data.NewStockDataApi().SetStockAICron(cronText, stockCode)
}

func (*compatibilityServiceAdapter) GetFollowedStockByStockCode(stockCode string) *models.FollowedStock {
	followed := data.NewStockDataApi().GetFollowedStockByStockCode(stockCode)
	return &followed
}

func (a *compatibilityServiceAdapter) GetAllFollowedStocks() []models.FollowedStock {
	dest := make([]models.FollowedStock, 0)
	a.main.Model(&models.FollowedStock{}).Find(&dest)
	return dest
}

func (a *compatibilityServiceAdapter) GetFollowedStockDetail(stockCode string) *models.FollowedStock {
	followed := &models.FollowedStock{StockCode: stockCode}
	a.main.Model(followed).
		Where("stock_code = ?", stockCode).
		Preload("Groups").
		Preload("Groups.GroupInfo").
		First(followed)
	return followed
}

func (a *compatibilityServiceAdapter) UpdateFollowPrice(stockCode string, price float64) {
	a.main.Model(&models.FollowedStock{}).
		Where("stock_code = ?", stockCode).
		Updates(map[string]any{"price": price})
}

func (a *compatibilityServiceAdapter) GetStoredStockInfo(stockCode string) *models.StockInfo {
	stockInfo := &models.StockInfo{}
	a.main.Model(stockInfo).Where("code = ?", stockCode).First(stockInfo)
	return stockInfo
}

func (*compatibilityServiceAdapter) GetStockKLine(stockCode string, days int64) *[]models.KLineData {
	return data.NewStockDataApi().GetHK_KLineData(stockCode, "day", days)
}

func (*compatibilityServiceAdapter) GetStockCommonKLine(stockCode string, days int64) *[]models.KLineData {
	return data.NewStockDataApi().GetCommonKLineData(stockCode, "day", days)
}

func (*compatibilityServiceAdapter) GetStockMinutePriceLineData(stockCode, stockName string) map[string]any {
	priceData, date := data.NewStockDataApi().GetStockMinutePriceData(stockCode)
	return map[string]any{
		"priceData": priceData,
		"date":      date,
		"stockName": stockName,
		"stockCode": stockCode,
	}
}

func (*compatibilityServiceAdapter) SearchStock(words string) map[string]any {
	return data.NewSearchStockApi(words).SearchStock(5000)
}

func (*compatibilityServiceAdapter) SearchStockWithFingerprint(words, fingerprint string, pageSize int) map[string]any {
	return data.NewSearchStockApiWithFingerprint(words, fingerprint).SearchStock(pageSize)
}

func (*compatibilityServiceAdapter) GetStockCodeRealTimeData(stockCodes ...string) (*[]models.StockInfo, error) {
	return data.NewStockDataApi().GetStockCodeRealTimeData(stockCodes...)
}
