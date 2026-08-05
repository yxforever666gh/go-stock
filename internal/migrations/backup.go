package migrations

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	gosqlite "github.com/glebarez/go-sqlite"
	glebarezsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	sqlite3 "modernc.org/sqlite/lib"
)

// Backup uses SQLite's native snapshot operation so WAL state is captured by
// SQLite rather than by copying database files at the filesystem level.
func Backup(database *gorm.DB, destination string) error {
	if database == nil {
		return fmt.Errorf("database is not initialized")
	}
	destination = strings.TrimSpace(destination)
	if destination == "" {
		return fmt.Errorf("backup destination is required")
	}
	absDestination, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	if _, err := os.Stat(absDestination); err == nil {
		return fmt.Errorf("backup destination already exists: %s", absDestination)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(absDestination), 0o755); err != nil {
		return err
	}
	if err := backupOnline(database, absDestination); err != nil {
		_ = os.Remove(absDestination)
		return err
	}
	return verifyBackupFile(absDestination)
}

type onlineBackuper interface {
	NewBackup(string) (*gosqlite.Backup, error)
}

func backupOnline(database *gorm.DB, destination string) error {
	sqlDB, err := database.DB()
	if err != nil {
		return err
	}
	connection, err := sqlDB.Conn(context.Background())
	if err != nil {
		return err
	}
	defer connection.Close()

	return connection.Raw(func(driverConnection any) error {
		source, ok := driverConnection.(onlineBackuper)
		if !ok {
			return fmt.Errorf("sqlite driver does not expose the online backup API")
		}
		backup, err := source.NewBackup(filepath.ToSlash(destination))
		if err != nil {
			return err
		}
		finished := false
		defer func() {
			if !finished {
				_ = backup.Finish()
			}
		}()
		deadline := time.Now().Add(30 * time.Second)
		for {
			more, stepErr := backup.Step(256)
			if stepErr != nil {
				var sqliteErr *gosqlite.Error
				if errors.As(stepErr, &sqliteErr) &&
					(sqliteErr.Code() == sqlite3.SQLITE_BUSY || sqliteErr.Code() == sqlite3.SQLITE_LOCKED) &&
					time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
					continue
				}
				return stepErr
			}
			if !more {
				break
			}
			time.Sleep(time.Millisecond)
		}
		finished = true
		if err := backup.Finish(); err != nil {
			return err
		}
		return nil
	})
}

func verifyBackupFile(path string) error {
	database, err := openSQLiteForVerification(path)
	if err != nil {
		return err
	}
	defer func() {
		if sqlDB, sqlErr := database.DB(); sqlErr == nil {
			_ = sqlDB.Close()
		}
	}()
	if err := VerifySQLiteIntegrity(database); err != nil {
		return fmt.Errorf("backup %w", err)
	}
	return nil
}

// VerifySQLiteIntegrity checks physical SQLite integrity without requiring the
// database to match the current application schema. Rollback uses this after
// restoring a pre-migration backup, before the previous binary is restarted.
func VerifySQLiteIntegrity(database *gorm.DB) error {
	if database == nil {
		return fmt.Errorf("database is not initialized")
	}
	result := quickCheck(database)
	if !strings.EqualFold(strings.TrimSpace(result), "ok") {
		return fmt.Errorf("quick_check returned %q", result)
	}
	return nil
}

func openSQLiteForVerification(path string) (*gorm.DB, error) {
	dsn := "file:" + filepath.ToSlash(path) + "?mode=ro&_pragma=query_only(1)"
	return gorm.Open(glebarezsqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
}
