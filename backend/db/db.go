package db

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	appconfig "go-stock/internal/config"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/plugin/dbresolver"
)

var Dao *gorm.DB

func resolveDBBusyTimeoutMs() int {
	return appconfig.Load().DB.BusyTimeoutMS
}

func withBusyTimeoutDSN(sqlitePath string, ms int) string {
	if ms <= 0 {
		return sqlitePath
	}
	// Modernc sqlite DSN supports _pragma. This makes SQLite wait (instead of
	// returning SQLITE_BUSY immediately) which reduces "database is locked"
	// errors during background tasks.
	needle := "_pragma=busy_timeout"
	if strings.Contains(sqlitePath, needle) {
		return sqlitePath
	}
	sep := "?"
	if strings.Contains(sqlitePath, "?") {
		sep = "&"
	}
	return sqlitePath + sep + "_pragma=busy_timeout(" + strconv.Itoa(ms) + ")"
}

func resolveDBLogLevel() logger.LogLevel {
	level := appconfig.Load().DB.LogLevel
	switch level {
	case "silent":
		return logger.Silent
	case "error":
		return logger.Error
	case "warn", "warning":
		return logger.Warn
	case "info":
		return logger.Info
	default:
		return logger.Info
	}
}

func Init(sqlitePath string) {
	dbLogger := logger.New(
		log.New(os.Stdout, "\r\n", log.LstdFlags),
		logger.Config{
			SlowThreshold:             time.Second * 3,
			Colorful:                  false,
			IgnoreRecordNotFoundError: true,
			ParameterizedQueries:      false,
			LogLevel:                  resolveDBLogLevel(),
		},
	)
	var openDb *gorm.DB
	var err error
	if sqlitePath == "" {
		sqlitePath = appconfig.Load().DB.Path
	}
	if ms := resolveDBBusyTimeoutMs(); ms > 0 {
		sqlitePath = withBusyTimeoutDSN(sqlitePath, ms)
	}
	openDb, err = gorm.Open(sqlite.Open(sqlitePath), &gorm.Config{
		Logger:                                   dbLogger,
		DisableForeignKeyConstraintWhenMigrating: true,
		SkipDefaultTransaction:                   true,
		PrepareStmt:                              true,
	})
	if err != nil {
		log.Fatalf("db connection error is %s", err.Error())
	}
	//读写分离提高sqlite效率，防止锁库
	if err = openDb.Use(dbresolver.Register(
		dbresolver.Config{
			Replicas: []gorm.Dialector{sqlite.Open(sqlitePath)}},
	)); err != nil {
		log.Fatalf("db resolver init error is %s", err.Error())
	}

	dbCon, err := openDb.DB()
	if err != nil {
		log.Fatalf("openDb.DB error is  %s", err.Error())
	}
	dbCon.SetMaxIdleConns(4)
	dbCon.SetMaxOpenConns(10)
	dbCon.SetConnMaxLifetime(time.Hour)
	Dao = openDb
}
