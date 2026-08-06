package bootstrap

import (
	"context"
	"testing"

	"go-stock/backend/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestReplaceStockBaseInfoReplacesAllMarkets(t *testing.T) {
	database := openStockCompatibilityTestDB(t)
	if err := database.AutoMigrate(&models.StockBasic{}, &models.StockInfoHK{}, &models.StockInfoUS{}); err != nil {
		t.Fatalf("migrate stock base info tables: %v", err)
	}
	seedStockBaseInfo(t, database)

	adapter := compatibilityServiceAdapter{main: database}
	err := adapter.ReplaceStockBaseInfo(
		context.Background(),
		[]models.StockBasic{{Name: "new-cn"}},
		[]models.StockInfoHK{{Name: "new-hk"}},
		[]models.StockInfoUS{{Name: "new-us"}},
	)
	if err != nil {
		t.Fatalf("replace stock base info: %v", err)
	}

	assertOnlyStockBaseInfoName[models.StockBasic](t, database, "new-cn")
	assertOnlyStockBaseInfoName[models.StockInfoHK](t, database, "new-hk")
	assertOnlyStockBaseInfoName[models.StockInfoUS](t, database, "new-us")
}

func TestReplaceStockBaseInfoRollsBackEveryMarket(t *testing.T) {
	database := openStockCompatibilityTestDB(t)
	if err := database.AutoMigrate(&models.StockBasic{}, &models.StockInfoHK{}); err != nil {
		t.Fatalf("migrate partial stock base info tables: %v", err)
	}
	if err := database.Create(&models.StockBasic{Name: "old-cn"}).Error; err != nil {
		t.Fatalf("seed domestic stock base info: %v", err)
	}
	if err := database.Create(&models.StockInfoHK{Name: "old-hk"}).Error; err != nil {
		t.Fatalf("seed Hong Kong stock base info: %v", err)
	}

	adapter := compatibilityServiceAdapter{main: database}
	err := adapter.ReplaceStockBaseInfo(
		context.Background(),
		[]models.StockBasic{{Name: "new-cn"}},
		[]models.StockInfoHK{{Name: "new-hk"}},
		[]models.StockInfoUS{{Name: "new-us"}},
	)
	if err == nil {
		t.Fatal("replace stock base info succeeded without the US table")
	}

	assertOnlyStockBaseInfoName[models.StockBasic](t, database, "old-cn")
	assertOnlyStockBaseInfoName[models.StockInfoHK](t, database, "old-hk")
}

func openStockCompatibilityTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	database, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	return database
}

func seedStockBaseInfo(t *testing.T, database *gorm.DB) {
	t.Helper()
	for _, row := range []any{
		&models.StockBasic{Name: "old-cn"},
		&models.StockInfoHK{Name: "old-hk"},
		&models.StockInfoUS{Name: "old-us"},
	} {
		if err := database.Create(row).Error; err != nil {
			t.Fatalf("seed stock base info: %v", err)
		}
	}
}

func assertOnlyStockBaseInfoName[T any](t *testing.T, database *gorm.DB, want string) {
	t.Helper()
	var rows []T
	if err := database.Find(&rows).Error; err != nil {
		t.Fatalf("query stock base info: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("stock base info rows = %d, want 1", len(rows))
	}
	var name string
	switch row := any(rows[0]).(type) {
	case models.StockBasic:
		name = row.Name
	case models.StockInfoHK:
		name = row.Name
	case models.StockInfoUS:
		name = row.Name
	default:
		t.Fatalf("unsupported stock base info type %T", row)
	}
	if name != want {
		t.Fatalf("stock base info name = %q, want %q", name, want)
	}
}
