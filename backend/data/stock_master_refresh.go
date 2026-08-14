package data

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go-stock/backend/models"

	"gorm.io/gorm"
)

const minimumStockMasterRows = 5000

const (
	StockMasterWarningAge = 7 * 24 * time.Hour
	StockMasterMaximumAge = 30 * 24 * time.Hour
)

type StockMasterRefreshResult = models.StockMasterRefreshResult

type StockMasterRefreshMetadata = models.StockMasterRefreshMetadata
type StockMasterHealth = models.StockMasterHealth

func EvaluateStockMasterHealth(ctx context.Context, database *gorm.DB, now time.Time) (StockMasterHealth, error) {
	result := StockMasterHealth{}
	if database == nil {
		result.FailureCode = "stock_master_unavailable"
		return result, fmt.Errorf("stock master database is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var rowCount int64
	if err := database.WithContext(ctx).Model(&models.StockBasic{}).Count(&rowCount).Error; err != nil {
		result.FailureCode = "stock_master_unavailable"
		return result, fmt.Errorf("query stock master health: %w", err)
	}
	result.RowCount = rowCount
	if rowCount < minimumStockMasterRows {
		result.FailureCode = "stock_master_empty"
		return result, nil
	}
	if !database.Migrator().HasTable(&models.StockMasterRefreshMetadata{}) {
		result.FailureCode = "stock_master_metadata_missing"
		return result, nil
	}
	var metadata models.StockMasterRefreshMetadata
	if err := database.WithContext(ctx).Where("id = ?", 1).First(&metadata).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			result.FailureCode = "stock_master_metadata_missing"
			return result, nil
		}
		result.FailureCode = "stock_master_unavailable"
		return result, fmt.Errorf("query stock master metadata: %w", err)
	}
	result.UpdatedAt = metadata.FetchedAt.UTC()
	age := now.UTC().Sub(result.UpdatedAt)
	if age < 0 {
		age = 0
	}
	result.Age = age.Round(time.Minute).String()
	if age > StockMasterMaximumAge {
		result.FailureCode = "stock_master_expired"
		result.Warning = "stock master is older than 30 days"
		return result, nil
	}
	result.Ready = true
	if age > StockMasterWarningAge {
		result.Warning = "stock master is older than 7 days"
	}
	return result, nil
}

func DecodeStockMasterPayload(payload []byte) ([]models.StockBasic, StockMasterRefreshResult, error) {
	result := StockMasterRefreshResult{FetchedAt: time.Now().UTC()}
	if len(payload) == 0 || !json.Valid(payload) {
		return nil, result, fmt.Errorf("stock master payload is empty or invalid JSON")
	}
	digest := sha256.Sum256(payload)
	result.SHA256 = hex.EncodeToString(digest[:])

	var response TushareStockBasicResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return nil, result, fmt.Errorf("decode stock master response: %w", err)
	}
	if response.Code != 0 {
		return nil, result, fmt.Errorf("stock master provider rejected request: code=%d message=%s", response.Code, strings.TrimSpace(response.Msg))
	}
	result.RowCount = len(response.Data.Items)
	if response.Data.HasMore {
		return nil, result, fmt.Errorf("stock master provider returned a partial page")
	}
	fieldIndex := make(map[string]int, len(response.Data.Fields))
	for index, field := range response.Data.Fields {
		fieldIndex[strings.TrimSpace(field)] = index
	}
	for _, required := range []string{"ts_code", "symbol", "name", "market", "exchange", "list_status", "list_date", "curr_type"} {
		if _, ok := fieldIndex[required]; !ok {
			return nil, result, fmt.Errorf("stock master payload is missing required field %s", required)
		}
	}

	rows := make([]models.StockBasic, 0, len(response.Data.Items))
	seen := make(map[string]struct{}, len(response.Data.Items))
	for index, item := range response.Data.Items {
		value := func(field string) string {
			position, ok := fieldIndex[field]
			if !ok || position >= len(item) {
				return ""
			}
			return cleanStockMasterText(item[position])
		}
		code := strings.ToUpper(value("ts_code"))
		if code == "" || value("symbol") == "" || value("name") == "" || value("market") == "" ||
			value("exchange") == "" || value("list_status") == "" || value("list_date") == "" || value("curr_type") == "" {
			return nil, result, fmt.Errorf("stock master row %d is missing required data", index)
		}
		if _, exists := seen[code]; exists {
			return nil, result, fmt.Errorf("stock master contains duplicate code %s", code)
		}
		seen[code] = struct{}{}
		rows = append(rows, models.StockBasic{
			TsCode: code, Symbol: value("symbol"), Name: value("name"), Area: value("area"),
			Industry: value("industry"), Fullname: value("fullname"), Ename: value("enname"),
			Cnspell: value("cnspell"), Market: value("market"), Exchange: value("exchange"),
			CurrType: value("curr_type"), ListStatus: value("list_status"), ListDate: value("list_date"),
			DelistDate: value("delist_date"), IsHs: value("is_hs"), ActName: value("act_name"), ActEntType: value("act_ent_type"),
		})
	}
	result.ValidRows = len(rows)
	if len(rows) < minimumStockMasterRows {
		return nil, result, fmt.Errorf("stock master has %d valid rows; need at least %d", len(rows), minimumStockMasterRows)
	}
	return rows, result, nil
}

func ReplaceStockMaster(ctx context.Context, database *gorm.DB, rows []models.StockBasic) error {
	if database == nil {
		return fmt.Errorf("stock master database is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if len(rows) < minimumStockMasterRows {
		return fmt.Errorf("refusing to replace stock master with %d rows", len(rows))
	}
	return database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Unscoped().Where("1 = 1").Delete(&models.StockBasic{}).Error; err != nil {
			return err
		}
		return tx.CreateInBatches(&rows, 400).Error
	})
}

func ReplaceStockMasterWithMetadata(ctx context.Context, database *gorm.DB, rows []models.StockBasic, result StockMasterRefreshResult) error {
	if database == nil {
		return fmt.Errorf("stock master database is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if len(rows) < minimumStockMasterRows || result.ValidRows != len(rows) || strings.TrimSpace(result.Source) == "" || strings.TrimSpace(result.SHA256) == "" {
		return fmt.Errorf("stock master replacement metadata is incomplete")
	}
	if result.FetchedAt.IsZero() {
		result.FetchedAt = time.Now().UTC()
	}
	return database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if !tx.Migrator().HasTable(&models.StockMasterRefreshMetadata{}) {
			return fmt.Errorf("stock master metadata schema is missing; run database migrations")
		}
		if err := tx.Unscoped().Where("1 = 1").Delete(&models.StockBasic{}).Error; err != nil {
			return err
		}
		if err := tx.CreateInBatches(&rows, 400).Error; err != nil {
			return err
		}
		metadata := models.StockMasterRefreshMetadata{
			ID: 1, Source: result.Source, FetchedAt: result.FetchedAt.UTC(), RowCount: result.RowCount,
			ValidRows: result.ValidRows, SHA256: result.SHA256, UsedSeed: result.UsedSeed,
			Warnings: strings.Join(result.Warnings, "\n"),
		}
		return tx.Where("id = ?", 1).Assign(metadata).FirstOrCreate(&metadata).Error
	})
}

func cleanStockMasterText(value any) string {
	if value == nil {
		return ""
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	switch strings.ToLower(text) {
	case "", "nil", "null", "<nil>", "<null>":
		return ""
	default:
		return text
	}
}
