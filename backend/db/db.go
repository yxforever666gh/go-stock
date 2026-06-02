package db

import (
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	appconfig "go-stock/internal/config"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/plugin/dbresolver"
)

var Dao *gorm.DB
var MinuteDao *gorm.DB

const minuteBarSchemaSQL = `
CREATE TABLE IF NOT EXISTS minute_bar (
  stock_code TEXT NOT NULL,
  trade_time INTEGER NOT NULL,
  open REAL NOT NULL,
  high REAL NOT NULL,
  low REAL NOT NULL,
  close REAL NOT NULL,
  volume REAL,
  amount REAL,
  source TEXT,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (stock_code, trade_time)
) WITHOUT ROWID;

CREATE INDEX IF NOT EXISTS idx_minute_bar_trade_time
ON minute_bar(trade_time);
`

func resolveDBBusyTimeoutMs() int {
	return appconfig.Load().DB.BusyTimeoutMS
}

func resolveMinuteDBBusyTimeoutMs() int {
	cfg := appconfig.Load()
	if cfg.DB.MinuteBusyTimeoutMS > 0 {
		return cfg.DB.MinuteBusyTimeoutMS
	}
	return cfg.DB.BusyTimeoutMS
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

func resolveDBLogLevel() gormlogger.LogLevel {
	level := appconfig.Load().DB.LogLevel
	switch level {
	case "silent":
		return gormlogger.Silent
	case "error":
		return gormlogger.Error
	case "warn", "warning":
		return gormlogger.Warn
	case "info":
		return gormlogger.Info
	default:
		return gormlogger.Info
	}
}

func newDBLogger() gormlogger.Interface {
	return gormlogger.New(
		log.New(os.Stdout, "\r\n", log.LstdFlags),
		gormlogger.Config{
			SlowThreshold:             time.Second * 3,
			Colorful:                  false,
			IgnoreRecordNotFoundError: true,
			ParameterizedQueries:      false,
			LogLevel:                  resolveDBLogLevel(),
		},
	)
}

func openSQLite(sqlitePath string, dbLogger gormlogger.Interface) (*gorm.DB, error) {
	return gorm.Open(sqlite.Open(sqlitePath), &gorm.Config{
		Logger:                                   dbLogger,
		DisableForeignKeyConstraintWhenMigrating: true,
		SkipDefaultTransaction:                   true,
		PrepareStmt:                              true,
	})
}

func Init(sqlitePath string) {
	dbLogger := newDBLogger()
	var openDb *gorm.DB
	var err error
	if sqlitePath == "" {
		sqlitePath = appconfig.Load().DB.Path
	}
	if ms := resolveDBBusyTimeoutMs(); ms > 0 {
		sqlitePath = withBusyTimeoutDSN(sqlitePath, ms)
	}
	ensureSQLiteDir(sqlitePath)
	openDb, err = openSQLite(sqlitePath, dbLogger)
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

	InitMinute(resolveMinutePathForMainPath(sqlitePath))
}

func InitMinute(sqlitePath string) {
	dbLogger := newDBLogger()
	if sqlitePath == "" {
		sqlitePath = appconfig.Load().DB.MinutePath
	}
	if ms := resolveMinuteDBBusyTimeoutMs(); ms > 0 {
		sqlitePath = withBusyTimeoutDSN(sqlitePath, ms)
	}
	ensureSQLiteDir(sqlitePath)
	openDb, err := openSQLite(sqlitePath, dbLogger)
	if err != nil {
		log.Fatalf("minute db connection error is %s", err.Error())
	}
	dbCon, err := openDb.DB()
	if err != nil {
		log.Fatalf("minute openDb.DB error is %s", err.Error())
	}
	dbCon.SetMaxIdleConns(2)
	dbCon.SetMaxOpenConns(4)
	dbCon.SetConnMaxLifetime(time.Hour)
	if err := initMinuteDBSchema(openDb); err != nil {
		log.Fatalf("minute db schema init error is %s", err.Error())
	}
	MinuteDao = openDb
}

func initMinuteDBSchema(openDb *gorm.DB) error {
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA temp_store=MEMORY",
	}
	for _, pragma := range pragmas {
		if err := openDb.Exec(pragma).Error; err != nil {
			return err
		}
	}
	if err := openDb.Exec(minuteBarSchemaSQL).Error; err != nil {
		return err
	}
	return openDb.Exec("PRAGMA optimize").Error
}

func resolveMinutePathForMainPath(mainPath string) string {
	cfg := appconfig.Load()
	if strings.TrimSpace(os.Getenv("GO_STOCK_MINUTE_DB_PATH")) != "" {
		return cfg.DB.MinutePath
	}
	mainPath = strings.TrimSpace(removeBusyTimeoutPragma(mainPath))
	if mainPath == "" || mainPath == strings.TrimSpace(cfg.DB.Path) {
		return cfg.DB.MinutePath
	}
	return deriveSiblingMinutePath(mainPath)
}

func removeBusyTimeoutPragma(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.Contains(raw, "_pragma=busy_timeout") {
		return raw
	}
	parts := strings.SplitN(raw, "?", 2)
	if len(parts) != 2 {
		return raw
	}
	queryParts := strings.Split(parts[1], "&")
	filtered := make([]string, 0, len(queryParts))
	for _, part := range queryParts {
		if strings.HasPrefix(part, "_pragma=busy_timeout") {
			continue
		}
		filtered = append(filtered, part)
	}
	if len(filtered) == 0 {
		return parts[0]
	}
	return parts[0] + "?" + strings.Join(filtered, "&")
}

func deriveSiblingMinutePath(raw string) string {
	parts := strings.SplitN(strings.TrimSpace(raw), "?", 2)
	filePart := strings.TrimSpace(parts[0])
	if filePart == "" || filePart == ":memory:" || strings.HasPrefix(filePart, "file:") {
		return raw
	}
	minutePath := filepath.Join(filepath.Dir(filePart), "minute.db")
	if len(parts) == 1 {
		return minutePath
	}
	return minutePath + "?" + parts[1]
}

func ensureSQLiteDir(raw string) {
	parts := strings.SplitN(strings.TrimSpace(raw), "?", 2)
	if len(parts) == 0 {
		return
	}
	filePart := strings.TrimSpace(parts[0])
	if filePart == "" || filePart == ":memory:" || strings.HasPrefix(filePart, "file:") {
		return
	}
	dir := filepath.Dir(filePart)
	if dir == "" || dir == "." {
		return
	}
	_ = os.MkdirAll(dir, os.ModePerm)
}
