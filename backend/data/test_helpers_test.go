package data

import (
	"fmt"
	"testing"

	"go-stock/backend/db"
	"go-stock/backend/models"
)

type testSchema string

const (
	testSchemaSettings     testSchema = "settings"
	testSchemaMinuteCache  testSchema = "minute-cache"
	testSchemaChartCache   testSchema = "chart-cache"
	testSchemaChartDrawing testSchema = "chart-drawing"
	testSchemaMarketNews   testSchema = "market-news"
)

func initDatabaseForTest(t *testing.T, path string, schemas ...testSchema) {
	t.Helper()
	_ = db.Close()
	db.Dao = nil
	db.MinuteDao = nil
	db.InitSilent(path)
	for _, schema := range schemas {
		var err error
		switch schema {
		case testSchemaSettings:
			err = db.Dao.AutoMigrate(&Settings{}, &AIConfig{})
		case testSchemaMinuteCache:
			err = db.MinuteDao.AutoMigrate(&minuteCacheDBBar{})
		case testSchemaChartCache:
			err = db.MinuteDao.AutoMigrate(&marketChartBarRow{})
		case testSchemaChartDrawing:
			err = installChartDrawingTestSchema()
		case testSchemaMarketNews:
			err = db.Dao.AutoMigrate(&Settings{}, &AIConfig{}, &models.Telegraph{}, &models.Tags{}, &models.TelegraphTags{})
		default:
			err = fmt.Errorf("unknown test schema %q", schema)
		}
		if err != nil {
			t.Fatalf("install %s schema: %v", schema, err)
		}
	}
	t.Cleanup(func() {
		_ = db.Close()
		db.Dao = nil
		db.MinuteDao = nil
	})
}

func installChartDrawingTestSchema() error {
	return db.Dao.AutoMigrate(&chartDrawingDocumentRow{}, &chartDrawingRevisionRow{})
}
