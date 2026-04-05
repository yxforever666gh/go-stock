package cli

import (
	"fmt"
	"go-stock/backend/db"
	"go-stock/internal/bootstrap"

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
