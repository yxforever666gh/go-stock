package stocks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"go-stock/backend/models"
	appservice "go-stock/internal/service"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type fakeMasterSource struct {
	primaryRows  []models.StockBasic
	primary      models.StockMasterRefreshResult
	primaryErr   error
	publicRows   []models.StockBasic
	public       models.StockMasterRefreshResult
	publicErr    error
	primaryCalls int
	publicCalls  int
}

func (s *fakeMasterSource) FetchValidatedStockMaster(context.Context) ([]models.StockBasic, models.StockMasterRefreshResult, error) {
	s.primaryCalls++
	return s.primaryRows, s.primary, s.primaryErr
}

func (s *fakeMasterSource) FetchValidatedPublicStockMaster(context.Context) ([]models.StockBasic, models.StockMasterRefreshResult, error) {
	s.publicCalls++
	return s.publicRows, s.public, s.publicErr
}

func TestRefreshStockBaseInfoFallsBackToPublicAndSeedOnlyForEmptyDatabase(t *testing.T) {
	t.Run("controlled public", func(t *testing.T) {
		database := openServiceTestDB(t, &models.StockBasic{}, &models.StockMasterRefreshMetadata{})
		rows := validStockMasterRows("public")
		source := &fakeMasterSource{primaryErr: errors.New("primary unavailable"), publicRows: rows,
			public: models.StockMasterRefreshResult{Source: "controlled_public", RowCount: len(rows), ValidRows: len(rows), SHA256: "public"}}
		service := NewService(Dependencies{Database: database, Master: source})
		result, err := service.RefreshStockBaseInfo(context.Background())
		if err != nil || !result.Replaced || result.Source != "controlled_public" || source.primaryCalls != 1 || source.publicCalls != 1 {
			t.Fatalf("refresh result=%+v err=%v source=%+v", result, err, source)
		}
		assertStockMasterPrefix(t, database, "public")
	})

	t.Run("seed only for empty database", func(t *testing.T) {
		database := openServiceTestDB(t, &models.StockBasic{}, &models.StockMasterRefreshMetadata{})
		source := &fakeMasterSource{primaryErr: errors.New("primary unavailable"), publicErr: errors.New("public unavailable")}
		seedCalls := 0
		seed := func() ([]models.StockBasic, models.StockMasterRefreshResult, error) {
			seedCalls++
			rows := validStockMasterRows("seed")
			return rows, models.StockMasterRefreshResult{Source: "embedded", RowCount: len(rows), ValidRows: len(rows), SHA256: "seed", UsedSeed: true}, nil
		}
		service := NewService(Dependencies{Database: database, Master: source, StockMasterSeed: seed})
		if result, err := service.RefreshStockBaseInfo(context.Background()); err != nil || !result.UsedSeed || seedCalls != 1 {
			t.Fatalf("seed refresh result=%+v err=%v calls=%d", result, err, seedCalls)
		}
		assertStockMasterPrefix(t, database, "seed")

		database = openServiceTestDB(t, &models.StockBasic{}, &models.StockMasterRefreshMetadata{})
		if err := database.Create(&models.StockBasic{TsCode: "000001.SZ", Name: "old"}).Error; err != nil {
			t.Fatal(err)
		}
		seedCalls = 0
		service = NewService(Dependencies{Database: database, Master: source, StockMasterSeed: seed})
		if _, err := service.RefreshStockBaseInfo(context.Background()); err == nil || seedCalls != 0 {
			t.Fatalf("non-empty refresh err=%v seedCalls=%d", err, seedCalls)
		}
		assertStockMasterPrefix(t, database, "old")
	})
}

func TestServiceOwnsWatchlistAndRealtimePersistence(t *testing.T) {
	database := openServiceTestDB(t, &models.FollowedStock{}, &models.StockInfo{})
	realtime := func(_ context.Context, codes ...string) (*[]models.StockInfo, error) {
		items := []models.StockInfo{{Code: codes[0], Name: "Apple", Price: "123.45"}}
		return &items, nil
	}
	service := NewService(Dependencies{Database: database, Realtime: realtime})
	if message, err := service.Follow("usAAPL"); err != nil || message != "关注成功" {
		t.Fatalf("follow = %q, %v", message, err)
	}
	if _, err := service.Follow("usAAPL"); !errors.Is(err, appservice.ErrConflict) {
		t.Fatalf("duplicate follow error = %v", err)
	}
	if message, err := service.SetCostPriceAndVolume("gb_aapl", 120, 3); err != nil || message != "设置成功" {
		t.Fatalf("set cost = %q, %v", message, err)
	}
	if message, err := service.SetAlarmChangePercent(2.5, 130, "gb_aapl"); err != nil || message != "设置成功" {
		t.Fatalf("set alarm = %q, %v", message, err)
	}
	followed := service.GetFollowList(0)
	if len(*followed) != 1 || (*followed)[0].StockCode != "gb_aapl" || (*followed)[0].CostPrice != 120 || (*followed)[0].AlarmPrice != 130 {
		t.Fatalf("followed stocks = %+v", *followed)
	}
	deadline := time.Now().Add(time.Second)
	for {
		var count int64
		_ = database.Model(&models.StockInfo{}).Where("code = ?", "usaapl").Count(&count).Error
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("realtime stock_info row was not persisted")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if message, err := service.UnFollow("gb_aapl"); err != nil || message != "取消关注成功" {
		t.Fatalf("unfollow = %q, %v", message, err)
	}
}

func TestServiceOwnsIndexPersistenceAndStockSearch(t *testing.T) {
	database := openServiceTestDB(t, &models.StockBasic{}, &models.IndexBasic{}, &models.StockInfoHK{}, &models.StockInfoUS{})
	fetchCalls := 0
	service := NewService(Dependencies{
		Database: database,
		FetchIndex: func(context.Context) ([]models.IndexBasic, error) {
			fetchCalls++
			return []models.IndexBasic{{TsCode: "000001.SH", Symbol: "000001", Name: "上证指数", Market: "SSE"}}, nil
		},
	})
	if err := service.refreshIndexBaseInfo(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fetchCalls != 1 {
		t.Fatalf("index fetch calls = %d, want 1", fetchCalls)
	}
	var stored models.IndexBasic
	if err := database.Where("ts_code = ?", "000001.SH").First(&stored).Error; err != nil || stored.Name != "上证指数" {
		t.Fatalf("stored index = %+v err=%v", stored, err)
	}
	if err := database.Create(&models.StockBasic{TsCode: "600000.SH", Symbol: "600000", Name: "浦发银行"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&models.StockInfoHK{Code: "00700.HK", Name: "腾讯控股"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&models.StockInfoUS{Code: "usAAPL", Name: "苹果", EName: "Apple"}).Error; err != nil {
		t.Fatal(err)
	}
	items := service.GetStockList("")
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		seen[item.TsCode] = true
	}
	for _, code := range []string{"600000.SH", "000001.SH", "00700.HK", "gb_aapl"} {
		if !seen[code] {
			t.Fatalf("stock search missing %s: %+v", code, items)
		}
	}
}

func openServiceTestDB(t *testing.T, modelsToMigrate ...any) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s-%d?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "-"), time.Now().UnixNano())
	database, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(modelsToMigrate...); err != nil {
		t.Fatal(err)
	}
	return database
}

func assertStockMasterPrefix(t *testing.T, database *gorm.DB, prefix string) {
	t.Helper()
	var row models.StockBasic
	if err := database.Order("id ASC").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Name != prefix && !strings.HasPrefix(row.Name, prefix+"-") {
		t.Fatalf("first stock master name = %q, want prefix %q", row.Name, prefix)
	}
}
