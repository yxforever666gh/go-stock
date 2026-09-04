package research2app

import (
	"testing"

	"go-stock/backend/research2"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestNewRuntimeOwnsResearch2ServiceComposition(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err = database.AutoMigrate(&research2.Account{}); err != nil {
		t.Fatal(err)
	}

	runtime, err := NewRuntime(database, Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Repository == nil || runtime.Valuation == nil || runtime.Runner == nil || runtime.Trading == nil || runtime.Email == nil {
		t.Fatalf("incomplete Research2 runtime: %+v", runtime)
	}
}

func TestNewRuntimeRejectsMissingStorage(t *testing.T) {
	if _, err := NewRuntime(nil, Dependencies{}); err == nil {
		t.Fatal("missing main storage was accepted")
	}
}
