package funds

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go-stock/backend/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestConcurrentFundFollowIsIdempotentAcrossConnections(t *testing.T) {
	path := filepath.ToSlash(filepath.Join(t.TempDir(), "fund-watchlist.db"))
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
	if err := first.AutoMigrate(&models.FundBasic{}, &models.FollowedFund{}); err != nil {
		t.Fatal(err)
	}
	if err := first.Create(&models.FundBasic{Code: "000001", Name: "价值混合"}).Error; err != nil {
		t.Fatal(err)
	}
	second := open()
	ready, proceed := make(chan struct{}), make(chan struct{})
	var once, resume sync.Once
	release := func() { resume.Do(func() { close(proceed) }) }
	t.Cleanup(release)
	if err := first.Callback().Create().Before("gorm:create").Register("test:pause_follow", func(*gorm.DB) {
		once.Do(func() { close(ready); <-proceed })
	}); err != nil {
		t.Fatal(err)
	}
	if err := second.Callback().Raw().After("gorm:raw").Register("test:release_writer", func(*gorm.DB) { release() }); err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan error, 1)
	go func() {
		_, err := NewApplicationService(first).FollowFund("000001")
		firstDone <- err
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("first follow did not reach the concurrency barrier")
	}
	_, secondErr := NewApplicationService(second).FollowFund("000001")
	release()
	firstErr := <-firstDone
	if firstErr != nil || secondErr != nil {
		t.Fatalf("concurrent follows returned errors: first=%v second=%v", firstErr, secondErr)
	}
	var rows []models.FollowedFund
	if err := first.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Code != "000001" || rows[0].Name != "价值混合" {
		t.Fatalf("concurrent follow must keep one fund row: %+v", rows)
	}
}
