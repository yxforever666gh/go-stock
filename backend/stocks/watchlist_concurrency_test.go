package stocks

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go-stock/backend/models"
	appservice "go-stock/internal/service"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestConcurrentStockFollowUsesOneSlotAndUniqueSort(t *testing.T) {
	for _, scenario := range []struct {
		name string
		seed int
		code string
	}{
		{name: "same code in empty watchlist", code: "sz000001"},
		{name: "last available slot", seed: 62, code: "sz000002"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			first, second := openWatchlistConnections(t)
			for index := 0; index < scenario.seed; index++ {
				if err := first.Create(&models.FollowedStock{StockCode: fmt.Sprintf("sh%06d", index), Sort: int64(index + 1)}).Error; err != nil {
					t.Fatal(err)
				}
			}
			ready, release := pauseStockWrite(t, first, second, false)
			firstDone := make(chan error, 1)
			go func() {
				_, err := NewService(Dependencies{Database: first}).followWithQuote("sz000001", models.StockInfo{Price: "10"})
				firstDone <- err
			}()
			waitStockWrite(t, ready)
			_, secondErr := NewService(Dependencies{Database: second}).followWithQuote(scenario.code, models.StockInfo{Price: "11"})
			release()
			firstErr := <-firstDone
			successes, conflicts := 0, 0
			for _, err := range []error{firstErr, secondErr} {
				switch {
				case err == nil:
					successes++
				case errors.Is(err, appservice.ErrConflict):
					conflicts++
				default:
					t.Fatalf("follow failed with an unexpected error: %v", err)
				}
			}
			var rows []models.FollowedStock
			if err := first.Order("sort").Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			if successes != 1 || conflicts != 1 || len(rows) != scenario.seed+1 {
				t.Fatalf("successes=%d conflicts=%d rows=%d; expected one new row", successes, conflicts, len(rows))
			}
			for index, row := range rows {
				if row.Sort != int64(index+1) {
					t.Fatalf("stock %s has sort %d at index %d", row.StockCode, row.Sort, index)
				}
			}
		})
	}
}

func TestConcurrentStockSortAndFollowPreserveBothChanges(t *testing.T) {
	first, second := openWatchlistConnections(t)
	for index, code := range []string{"sz000001", "sz000002", "sz000003"} {
		if err := first.Create(&models.FollowedStock{StockCode: code, Sort: int64(index + 1)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	ready, release := pauseStockWrite(t, first, second, true)
	firstDone := make(chan struct{})
	go func() {
		NewService(Dependencies{Database: first}).SetStockSort(1, "sz000003")
		close(firstDone)
	}()
	waitStockWrite(t, ready)
	_, err := NewService(Dependencies{Database: second}).followWithQuote("sz000004", models.StockInfo{Price: "10"})
	release()
	<-firstDone
	if err != nil {
		t.Fatal(err)
	}
	var rows []models.FollowedStock
	if err := first.Order("sort").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	want := []string{"sz000003", "sz000001", "sz000002", "sz000004"}
	if len(rows) != len(want) {
		t.Fatalf("rows=%d, want %d", len(rows), len(want))
	}
	for index, row := range rows {
		if row.StockCode != want[index] || row.Sort != int64(index+1) {
			t.Fatalf("row %d = %+v; want %s at sort %d", index, row, want[index], index+1)
		}
	}
}

func openWatchlistConnections(t *testing.T) (*gorm.DB, *gorm.DB) {
	t.Helper()
	path := filepath.ToSlash(filepath.Join(t.TempDir(), "watchlist.db"))
	open := func() *gorm.DB {
		database, err := gorm.Open(sqlite.Open(path+"?_pragma=busy_timeout(0)&_pragma=journal_mode(WAL)"), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
		if err != nil {
			t.Fatal(err)
		}
		connection, err := database.DB()
		if err != nil {
			t.Fatal(err)
		}
		connection.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = connection.Close() })
		return database
	}
	first := open()
	if err := first.AutoMigrate(&models.FollowedStock{}); err != nil {
		t.Fatal(err)
	}
	return first, open()
}

// Pause the first mutation after its reads. Without admission serialization,
// the competing connection can commit while those reads are already stale.
// A failed competing write-lock attempt releases the first writer instead,
// requiring the complete transaction to retry after SQLite reports busy.
func pauseStockWrite(t *testing.T, first, second *gorm.DB, sorting bool) (<-chan struct{}, func()) {
	t.Helper()
	ready, proceed := make(chan struct{}), make(chan struct{})
	var once, resume sync.Once
	release := func() { resume.Do(func() { close(proceed) }) }
	t.Cleanup(release)
	pause := func(*gorm.DB) { once.Do(func() { close(ready); <-proceed }) }
	var err error
	if sorting {
		err = first.Callback().Update().Before("gorm:update").Register("test:pause_write", pause)
	} else {
		err = first.Callback().Create().Before("gorm:create").Register("test:pause_write", pause)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Callback().Raw().After("gorm:raw").Register("test:release_writer", func(*gorm.DB) { release() }); err != nil {
		t.Fatal(err)
	}
	return ready, release
}

func waitStockWrite(t *testing.T, ready <-chan struct{}) {
	t.Helper()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("first write did not reach the concurrency barrier")
	}
}
