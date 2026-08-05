package data

import (
	"errors"
	"go-stock/backend/db"
	"go-stock/backend/models"
	"path/filepath"
	"testing"
)

// @Author spark
// @Date 2026/1/23 17:39
// @Desc
//-----------------------------------------------------------------------------------

func TestAIResponseResultService_GetAIResponseResultList(t *testing.T) {
	requireIntegration(t)
	initDatabaseForTest(t, "../../data/stock.db")
	service := NewAIResponseResultService()
	list, err := service.GetAIResponseResultList(models.AIResponseResultQuery{
		Page:     1,
		PageSize: 10,
	})
	if err != nil {
		return
	}
	t.Log(list)

}

func TestAIResponseResultServiceRejectsMarketSummaryHistoryDelete(t *testing.T) {
	initDatabaseForTest(t, filepath.Join(t.TempDir(), "ai-response-delete.db"))
	if err := db.Dao.AutoMigrate(&models.AIResponseResult{}); err != nil {
		t.Fatalf("auto migrate AI response: %v", err)
	}
	protected := models.AIResponseResult{StockCode: "市场资讯", StockName: "市场资讯", Content: "frozen"}
	normal := models.AIResponseResult{StockCode: "000001.SZ", StockName: "normal", Content: "deletable"}
	if err := db.Dao.Create(&protected).Error; err != nil {
		t.Fatalf("seed protected response: %v", err)
	}
	if err := db.Dao.Create(&normal).Error; err != nil {
		t.Fatalf("seed normal response: %v", err)
	}

	service := NewAIResponseResultService()
	if err := service.DeleteAIResponseResult(protected.ID); !errors.Is(err, ErrMarketSummaryAIHistoryReadOnly) {
		t.Fatalf("protected delete error = %v", err)
	}
	assertAIResponseVisible(t, protected.ID, true)

	if err := service.DeleteAIResponseResult(normal.ID); err != nil {
		t.Fatalf("delete normal response: %v", err)
	}
	assertAIResponseVisible(t, normal.ID, false)
}

func TestAIResponseResultServiceRejectsMixedBatchAtomically(t *testing.T) {
	initDatabaseForTest(t, filepath.Join(t.TempDir(), "ai-response-batch-delete.db"))
	if err := db.Dao.AutoMigrate(&models.AIResponseResult{}); err != nil {
		t.Fatalf("auto migrate AI response: %v", err)
	}
	protected := models.AIResponseResult{StockName: "市场资讯", Content: "legacy frozen"}
	normal := models.AIResponseResult{StockCode: "600000.SH", StockName: "normal", Content: "keep on rejection"}
	if err := db.Dao.Create(&protected).Error; err != nil {
		t.Fatalf("seed protected response: %v", err)
	}
	if err := db.Dao.Create(&normal).Error; err != nil {
		t.Fatalf("seed normal response: %v", err)
	}

	err := NewAIResponseResultService().BatchDeleteAIResponseResult([]uint{normal.ID, protected.ID})
	if !errors.Is(err, ErrMarketSummaryAIHistoryReadOnly) {
		t.Fatalf("mixed batch error = %v", err)
	}
	assertAIResponseVisible(t, protected.ID, true)
	assertAIResponseVisible(t, normal.ID, true)
}

func assertAIResponseVisible(t *testing.T, id uint, want bool) {
	t.Helper()
	var count int64
	if err := db.Dao.Model(&models.AIResponseResult{}).Where("id = ?", id).Count(&count).Error; err != nil {
		t.Fatalf("count AI response %d: %v", id, err)
	}
	if got := count == 1; got != want {
		t.Fatalf("AI response %d visible = %t, want %t", id, got, want)
	}
}
