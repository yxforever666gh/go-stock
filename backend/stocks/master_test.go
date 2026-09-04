package stocks

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"go-stock/backend/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestDecodeStockMasterPayloadCleansNilAndRejectsPartialData(t *testing.T) {
	fields := []string{"ts_code", "symbol", "name", "area", "industry", "cnspell", "market", "list_date", "act_name", "act_ent_type", "fullname", "exchange", "list_status", "curr_type", "enname", "delist_date", "is_hs"}
	items := make([][]any, 0, minimumStockMasterRows)
	for index := 0; index < minimumStockMasterRows; index++ {
		industry := any("industry")
		if index == 0 {
			industry = "<nil>"
		}
		items = append(items, []any{fmt.Sprintf("%06d.SZ", index), fmt.Sprintf("%06d", index), "name", "area", industry, "spell", "main", "20200101", nil, nil, "fullname", "SZSE", "L", "CNY", "name", nil, "N"})
	}
	payload, err := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"fields": fields, "items": items, "has_more": false}})
	if err != nil {
		t.Fatal(err)
	}
	rows, result, err := DecodeStockMasterPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if result.ValidRows != minimumStockMasterRows || result.SHA256 == "" || rows[0].Industry != "" {
		t.Fatalf("unexpected result: %+v first=%+v", result, rows[0])
	}
	partial, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"fields": fields, "items": items, "has_more": true}})
	if _, _, err := DecodeStockMasterPayload(partial); err == nil {
		t.Fatal("partial stock master response was accepted")
	}
}

func TestReplaceStockMasterValidatesAndCommitsMetadataAtomically(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.StockBasic{}); err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&models.StockBasic{TsCode: "000001.SZ", Symbol: "000001", Name: "old"}).Error; err != nil {
		t.Fatal(err)
	}
	undersized := []models.StockBasic{{TsCode: "000002.SZ"}}
	result := models.StockMasterRefreshResult{Source: "test", RowCount: 1, ValidRows: 1, SHA256: "abc"}
	if err := replaceStockMaster(context.Background(), database, undersized, result); err == nil {
		t.Fatal("undersized replacement was accepted")
	}
	var existing []models.StockBasic
	if err := database.Find(&existing).Error; err != nil || len(existing) != 1 || existing[0].Name != "old" {
		t.Fatalf("last good rows changed: %+v err=%v", existing, err)
	}

	rows := validStockMasterRows("new")
	result = models.StockMasterRefreshResult{Source: "test", RowCount: len(rows), ValidRows: len(rows), SHA256: "abc"}
	if err := replaceStockMaster(context.Background(), database, rows, result); err == nil {
		t.Fatal("replacement created an un-migrated metadata table")
	}
	if err := database.AutoMigrate(&models.StockMasterRefreshMetadata{}); err != nil {
		t.Fatal(err)
	}
	if err := replaceStockMaster(context.Background(), database, rows, result); err != nil {
		t.Fatal(err)
	}
	var metadata models.StockMasterRefreshMetadata
	if err := database.First(&metadata, 1).Error; err != nil || metadata.ValidRows != minimumStockMasterRows || metadata.Source != "test" {
		t.Fatalf("metadata = %+v err=%v", metadata, err)
	}
}

func validStockMasterRows(prefix string) []models.StockBasic {
	rows := make([]models.StockBasic, minimumStockMasterRows)
	for index := range rows {
		rows[index] = models.StockBasic{TsCode: fmt.Sprintf("%06d.SZ", index), Symbol: fmt.Sprintf("%06d", index), Name: fmt.Sprintf("%s-%d", prefix, index)}
	}
	return rows
}
