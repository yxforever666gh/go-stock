package data

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"go-stock/backend/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestDecodeStockMasterPayloadCleansNilTextAndRejectsPartialData(t *testing.T) {
	fields := []string{"ts_code", "symbol", "name", "area", "industry", "cnspell", "market", "list_date", "act_name", "act_ent_type", "fullname", "exchange", "list_status", "curr_type", "enname", "delist_date", "is_hs"}
	items := make([][]any, 0, minimumStockMasterRows)
	for index := 0; index < minimumStockMasterRows; index++ {
		code := fmt.Sprintf("%06d.SZ", index)
		industry := any("industry")
		if index == 0 {
			industry = "<nil>"
		}
		items = append(items, []any{code, fmt.Sprintf("%06d", index), "name", "area", industry, "spell", "main", "20200101", nil, nil, "fullname", "SZSE", "L", "CNY", "name", nil, "N"})
	}
	payload, err := json.Marshal(TushareStockBasicResponse{TushareResponse: TushareResponse{Code: 0}, Data: StockBasicResponse{Fields: fields, Items: items}})
	if err != nil {
		t.Fatal(err)
	}
	rows, result, err := DecodeStockMasterPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if result.ValidRows != minimumStockMasterRows || result.SHA256 == "" || rows[0].Industry != "" {
		t.Fatalf("unexpected stock master result: %+v first=%+v", result, rows[0])
	}

	var partial TushareStockBasicResponse
	if err := json.Unmarshal(payload, &partial); err != nil {
		t.Fatal(err)
	}
	partial.Data.HasMore = true
	partialPayload, _ := json.Marshal(partial)
	if _, _, err := DecodeStockMasterPayload(partialPayload); err == nil {
		t.Fatal("partial stock master response was accepted")
	}
}

func TestReplaceStockMasterKeepsLastGoodRowsOnValidationFailure(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.StockBasic{}); err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&models.StockBasic{TsCode: "000001.SZ", Symbol: "000001", Name: "old"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := ReplaceStockMaster(context.Background(), database, []models.StockBasic{{TsCode: "000002.SZ"}}); err == nil {
		t.Fatal("undersized replacement was accepted")
	}
	var rows []models.StockBasic
	if err := database.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "old" {
		t.Fatalf("last good stock master changed: %+v", rows)
	}
}

func TestReplaceStockMasterWithMetadataRequiresMigratedSchemaAndCommitsAtomically(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.StockBasic{}); err != nil {
		t.Fatal(err)
	}
	rows := make([]models.StockBasic, minimumStockMasterRows)
	for index := range rows {
		rows[index] = models.StockBasic{TsCode: fmt.Sprintf("%06d.SZ", index), Symbol: fmt.Sprintf("%06d", index), Name: "name"}
	}
	result := models.StockMasterRefreshResult{Source: "test", RowCount: len(rows), ValidRows: len(rows), SHA256: "abc"}
	if err := ReplaceStockMasterWithMetadata(context.Background(), database, rows, result); err == nil {
		t.Fatal("replacement created an un-migrated metadata table")
	}
	if err := database.AutoMigrate(&models.StockMasterRefreshMetadata{}); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceStockMasterWithMetadata(context.Background(), database, rows, result); err != nil {
		t.Fatal(err)
	}
	var metadata models.StockMasterRefreshMetadata
	if err := database.First(&metadata, 1).Error; err != nil {
		t.Fatal(err)
	}
	if metadata.ValidRows != minimumStockMasterRows || metadata.Source != "test" {
		t.Fatalf("metadata = %+v", metadata)
	}
}
