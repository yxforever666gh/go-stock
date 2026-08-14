package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"go-stock/backend/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type fakeStockMasterSource struct {
	primaryRows   []models.StockBasic
	primaryResult models.StockMasterRefreshResult
	primaryErr    error
	publicRows    []models.StockBasic
	publicResult  models.StockMasterRefreshResult
	publicErr     error
	primaryCalls  int
	publicCalls   int
}

func (s *fakeStockMasterSource) FetchValidatedStockMaster(context.Context) ([]models.StockBasic, models.StockMasterRefreshResult, error) {
	s.primaryCalls++
	return s.primaryRows, s.primaryResult, s.primaryErr
}

func (s *fakeStockMasterSource) FetchValidatedPublicStockMaster(context.Context) ([]models.StockBasic, models.StockMasterRefreshResult, error) {
	s.publicCalls++
	return s.publicRows, s.publicResult, s.publicErr
}

func TestRefreshStockBaseInfoFallsBackToControlledPublicSource(t *testing.T) {
	database := openRefreshStockMasterTestDB(t)
	source := &fakeStockMasterSource{
		primaryErr: errors.New("Tushare unavailable"),
		publicRows: validRefreshStockMasterRows("public"),
		publicResult: models.StockMasterRefreshResult{
			Source: "controlled_public", RowCount: 5000, ValidRows: 5000, SHA256: "public-hash",
		},
	}

	result, err := refreshStockBaseInfo(context.Background(), database, source, nil)
	if err != nil {
		t.Fatalf("refresh stock master: %v", err)
	}
	if !result.Replaced || result.Source != "controlled_public" {
		t.Fatalf("refresh result = %+v, want replaced controlled_public", result)
	}
	if len(result.Warnings) != 1 || result.Warnings[0] == "" {
		t.Fatalf("refresh warnings = %#v, want fallback warning", result.Warnings)
	}
	if source.primaryCalls != 1 || source.publicCalls != 1 {
		t.Fatalf("source calls = primary %d, public %d; want 1 each", source.primaryCalls, source.publicCalls)
	}
	assertRefreshStockMasterPrefix(t, database, "public")
}

func TestRefreshStockBaseInfoUsesSeedOnlyForEmptyDatabase(t *testing.T) {
	database := openRefreshStockMasterTestDB(t)
	source := &fakeStockMasterSource{
		primaryErr: errors.New("Tushare unavailable"),
		publicErr:  errors.New("controlled public unavailable"),
	}
	seedCalls := 0
	seed := func() ([]models.StockBasic, models.StockMasterRefreshResult, error) {
		seedCalls++
		return validRefreshStockMasterRows("seed"), models.StockMasterRefreshResult{
			Source: "embedded_stock_basic", RowCount: 5000, ValidRows: 5000, SHA256: "seed-hash", UsedSeed: true,
		}, nil
	}

	result, err := refreshStockBaseInfo(context.Background(), database, source, seed)
	if err != nil {
		t.Fatalf("refresh stock master from seed: %v", err)
	}
	if !result.Replaced || !result.UsedSeed || result.Source != "embedded_stock_basic" {
		t.Fatalf("refresh result = %+v, want replaced embedded seed", result)
	}
	if len(result.Warnings) != 1 || result.Warnings[0] == "" {
		t.Fatalf("refresh warnings = %#v, want seed warning", result.Warnings)
	}
	if seedCalls != 1 {
		t.Fatalf("seed calls = %d, want 1", seedCalls)
	}
	assertRefreshStockMasterPrefix(t, database, "seed")
}

func TestRefreshStockBaseInfoPreservesNonEmptyDatabaseWhenAllSourcesFail(t *testing.T) {
	database := openRefreshStockMasterTestDB(t)
	oldRows := validRefreshStockMasterRows("old")
	if err := database.CreateInBatches(&oldRows, 400).Error; err != nil {
		t.Fatalf("seed existing stock master: %v", err)
	}
	source := &fakeStockMasterSource{
		primaryErr: errors.New("Tushare unavailable"),
		publicErr:  errors.New("controlled public unavailable"),
	}
	seedCalls := 0
	seed := func() ([]models.StockBasic, models.StockMasterRefreshResult, error) {
		seedCalls++
		return validRefreshStockMasterRows("seed"), models.StockMasterRefreshResult{
			Source: "embedded_stock_basic", RowCount: 5000, ValidRows: 5000, SHA256: "seed-hash", UsedSeed: true,
		}, nil
	}

	_, err := refreshStockBaseInfo(context.Background(), database, source, seed)
	if err == nil {
		t.Fatal("refresh succeeded despite both providers failing with non-empty database")
	}
	if seedCalls != 0 {
		t.Fatalf("seed calls = %d, want 0 for non-empty database", seedCalls)
	}
	assertRefreshStockMasterPrefix(t, database, "old")
}

func openRefreshStockMasterTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	database := openStockCompatibilityTestDB(t)
	if err := database.AutoMigrate(&models.StockBasic{}, &models.StockMasterRefreshMetadata{}); err != nil {
		t.Fatalf("migrate stock master tables: %v", err)
	}
	return database
}

func validRefreshStockMasterRows(prefix string) []models.StockBasic {
	rows := make([]models.StockBasic, 5000)
	for index := range rows {
		code := fmt.Sprintf("%06d.SZ", index)
		rows[index] = models.StockBasic{
			TsCode: code, Symbol: fmt.Sprintf("%06d", index), Name: fmt.Sprintf("%s-%d", prefix, index),
			Market: "主板", Exchange: "SZSE", ListStatus: "L", ListDate: "20200101", CurrType: "CNY",
		}
	}
	return rows
}

func assertRefreshStockMasterPrefix(t *testing.T, database *gorm.DB, prefix string) {
	t.Helper()
	var row models.StockBasic
	if err := database.Order("id asc").First(&row).Error; err != nil {
		t.Fatalf("query refreshed stock master: %v", err)
	}
	want := fmt.Sprintf("%s-0", prefix)
	if row.Name != want {
		t.Fatalf("first stock master name = %q, want %q", row.Name, want)
	}
}

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
