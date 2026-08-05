package db

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type readOnlyFixture struct {
	ID   uint `gorm:"primaryKey"`
	Name string
}

func TestInitReadOnlyRejectsWritesAndDoesNotRequireMinuteDatabase(t *testing.T) {
	root := t.TempDir()
	mainPath := filepath.Join(root, "stock.db")
	seed, err := gorm.Open(sqlite.Open(mainPath), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.AutoMigrate(&readOnlyFixture{}); err != nil {
		t.Fatal(err)
	}
	if err := seed.Create(&readOnlyFixture{Name: "frozen"}).Error; err != nil {
		t.Fatal(err)
	}
	seedSQL, err := seed.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := seedSQL.Close(); err != nil {
		t.Fatal(err)
	}

	oldDao, oldMinuteDao := Dao, MinuteDao
	t.Cleanup(func() {
		_ = Close()
		Dao, MinuteDao = oldDao, oldMinuteDao
	})
	resolved, err := InitReadOnly(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != mainPath {
		t.Fatalf("resolved path = %q, want %q", resolved, mainPath)
	}
	if MinuteDao != nil {
		t.Fatal("missing sibling minute.db should stay unavailable, not be created")
	}
	var count int64
	if err := Dao.Model(&readOnlyFixture{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("read-only query count=%d err=%v", count, err)
	}
	if err := Dao.Create(&readOnlyFixture{Name: "forbidden"}).Error; err == nil {
		t.Fatal("read-only database accepted a write")
	}
}
