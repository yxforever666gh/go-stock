package cli

import (
	"fmt"
	"go-stock/backend/db"
	"go-stock/internal/bootstrap"
	"path/filepath"
	"strings"

	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func Bootstrap(dataDir, dbPath string) (string, error) {
	resolvedDBPath, err := bootstrap.InitCLIStorage(dataDir, dbPath)
	if err != nil {
		return "", fmt.Errorf("初始化失败: %w", err)
	}
	db.Dao = db.Dao.Session(&gorm.Session{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	return resolvedDBPath, nil
}

// BootstrapReadOnly is intentionally separate from normal CLI bootstrap: it
// neither creates directories nor runs migrations/settings initialization.
func BootstrapReadOnly(dataDir, dbPath string) (string, error) {
	if strings.TrimSpace(dataDir) == "" {
		dataDir = "data"
	}
	if strings.TrimSpace(dbPath) == "" {
		dbPath = filepath.Join(dataDir, "stock.db")
	}
	resolvedDBPath, err := db.InitReadOnly(dbPath)
	if err != nil {
		return "", fmt.Errorf("initialize read-only storage: %w", err)
	}
	db.Dao = db.Dao.Session(&gorm.Session{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if db.MinuteDao != nil {
		db.MinuteDao = db.MinuteDao.Session(&gorm.Session{
			Logger: gormlogger.Default.LogMode(gormlogger.Silent),
		})
	}
	return resolvedDBPath, nil
}
