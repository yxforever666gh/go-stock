package db

import (
	"io"
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
	return newDBLoggerWithWriter(os.Stdout)
}

func newDBLoggerWithWriter(writer io.Writer) gormlogger.Interface {
	return gormlogger.New(
		log.New(writer, "\r\n", log.LstdFlags),
		gormlogger.Config{
			SlowThreshold:             time.Second * 3,
			Colorful:                  false,
			IgnoreRecordNotFoundError: true,
			ParameterizedQueries:      true,
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
	initWithLogger(sqlitePath, newDBLogger())
}

// InitSilent opens the writable database pair without emitting SQL logs. It is
// reserved for machine-readable administrative CLI commands.
func InitSilent(sqlitePath string) {
	initWithLogger(sqlitePath, gormlogger.Default.LogMode(gormlogger.Silent))
}

func initWithLogger(sqlitePath string, dbLogger gormlogger.Interface) {
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

	initMinuteWithLogger(resolveMinutePathForMainPath(sqlitePath), dbLogger)
}

// InitReadOnly opens the primary database and its sibling minute cache without
// running schema initialization, migrations, PRAGMAs that write, or directory
// creation. It is used by audit commands that must be safe against the live
// cache even when the caller does not make a temporary copy first.
func InitReadOnly(sqlitePath string) (string, error) {
	if strings.TrimSpace(sqlitePath) == "" {
		sqlitePath = appconfig.Load().DB.Path
	}
	mainPath := strings.TrimSpace(removeBusyTimeoutPragma(sqlitePath))
	mainDSN, resolvedMainPath, err := readOnlySQLiteDSN(mainPath)
	if err != nil {
		return "", err
	}
	mainDB, err := openSQLite(mainDSN, newDBLogger())
	if err != nil {
		return "", err
	}
	mainSQL, err := mainDB.DB()
	if err != nil {
		return "", err
	}
	mainSQL.SetMaxIdleConns(1)
	mainSQL.SetMaxOpenConns(1)
	mainSQL.SetConnMaxLifetime(time.Hour)

	minutePath := resolveMinutePathForMainPath(resolvedMainPath)
	minuteDSN, resolvedMinutePath, minutePathErr := readOnlySQLiteDSN(minutePath)
	var minuteDB *gorm.DB
	if minutePathErr != nil && !os.IsNotExist(minutePathErr) {
		return "", minutePathErr
	}
	if minutePathErr == nil {
		minuteDB, err = openSQLite(minuteDSN, newDBLogger())
		if err == nil {
			minuteSQL, sqlErr := minuteDB.DB()
			if sqlErr != nil {
				return "", sqlErr
			}
			minuteSQL.SetMaxIdleConns(1)
			minuteSQL.SetMaxOpenConns(1)
			minuteSQL.SetConnMaxLifetime(time.Hour)
		}
	}
	if err != nil && resolvedMinutePath != "" {
		return "", err
	}
	Dao = mainDB
	MinuteDao = minuteDB
	return resolvedMainPath, nil
}

func readOnlySQLiteDSN(rawPath string) (string, string, error) {
	path := strings.TrimSpace(removeBusyTimeoutPragma(rawPath))
	if path == "" || path == ":memory:" || strings.HasPrefix(path, "file:") {
		return "", "", os.ErrInvalid
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	if _, err := os.Stat(absPath); err != nil {
		return "", absPath, err
	}
	dsn := "file:" + filepath.ToSlash(absPath) + "?mode=ro&_pragma=query_only(1)"
	if timeout := resolveDBBusyTimeoutMs(); timeout > 0 {
		dsn += "&_pragma=busy_timeout(" + strconv.Itoa(timeout) + ")"
	}
	return dsn, absPath, nil
}

func InitMinute(sqlitePath string) {
	initMinuteWithLogger(sqlitePath, newDBLogger())
}

func initMinuteWithLogger(sqlitePath string, dbLogger gormlogger.Interface) {
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
	if err := configureMinuteDB(openDb); err != nil {
		log.Fatalf("minute db configuration error is %s", err.Error())
	}
	MinuteDao = openDb
}

// Close releases every primary, replica, and minute-database connection pool.
// It is primarily used by tests and orderly shutdown paths.
func Close() error {
	var firstErr error
	closed := make(map[any]struct{})
	closePool := func(connPool gorm.ConnPool) error {
		if prepared, ok := connPool.(*gorm.PreparedStmtDB); ok {
			prepared.Close()
			if sqlDB, err := prepared.GetDBConn(); err == nil {
				connPool = sqlDB
			}
		}
		if _, exists := closed[connPool]; exists {
			return nil
		}
		closed[connPool] = struct{}{}
		if closer, ok := connPool.(interface{ Close() error }); ok {
			if err := closer.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return nil
	}
	if Dao != nil {
		for _, plugin := range Dao.Config.Plugins {
			if resolver, ok := plugin.(*dbresolver.DBResolver); ok {
				_ = resolver.Call(closePool)
			}
		}
		_ = closePool(Dao.Statement.ConnPool)
	}
	if MinuteDao != nil {
		_ = closePool(MinuteDao.Statement.ConnPool)
	}
	return firstErr
}

func configureMinuteDB(openDb *gorm.DB) error {
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
	return nil
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
