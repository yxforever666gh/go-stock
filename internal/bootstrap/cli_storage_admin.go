package bootstrap

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go-stock/backend/db"
	cliports "go-stock/internal/cli/ports"
	"go-stock/internal/migrations"

	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type cliStorageAdmin struct {
	main   *gorm.DB
	minute *gorm.DB
}

var _ cliports.StorageAdmin = (*cliStorageAdmin)(nil)

// NewProductionCLIStorageAdmin opens the process-local pair used by a single
// db command. Read-only callers never create files or run migrations.
func NewProductionCLIStorageAdmin(dbPath string, readOnly bool) (cliports.StorageAdmin, error) {
	if strings.TrimSpace(dbPath) == "" {
		return nil, fmt.Errorf("main database path is required")
	}
	if readOnly {
		if _, err := db.InitReadOnly(dbPath); err != nil {
			return nil, err
		}
	} else {
		db.Init(dbPath)
	}
	if db.Dao == nil || db.MinuteDao == nil {
		_ = db.Close()
		return nil, fmt.Errorf("main and minute databases must be initialized")
	}
	installSilentCLIStorageSessions()
	return &cliStorageAdmin{main: db.Dao, minute: db.MinuteDao}, nil
}

func InitCLIStorageReadOnly(dataDir, dbPath string) (string, error) {
	resolvedPath := resolveCLIStoragePath(dataDir, dbPath)
	resolvedPath, err := db.InitReadOnly(resolvedPath)
	if err != nil {
		return "", err
	}
	if db.Dao == nil {
		_ = db.Close()
		return "", fmt.Errorf("main database must be initialized")
	}
	installSilentCLIStorageSessions()
	return resolvedPath, nil
}

func installSilentCLIStorageSessions() {
	if db.Dao != nil {
		db.Dao = db.Dao.Session(&gorm.Session{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	}
	if db.MinuteDao != nil {
		db.MinuteDao = db.MinuteDao.Session(&gorm.Session{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	}
}

func (a *cliStorageAdmin) Status(_ context.Context) (cliports.DatabaseStatus, cliports.DatabaseStatus, error) {
	main, err := migrations.StatusMain(a.main)
	if err != nil {
		return cliports.DatabaseStatus{}, cliports.DatabaseStatus{}, err
	}
	minute, err := migrations.StatusMinute(a.minute)
	if err != nil {
		return cliports.DatabaseStatus{}, cliports.DatabaseStatus{}, err
	}
	return mapDatabaseStatus(main), mapDatabaseStatus(minute), nil
}

func (a *cliStorageAdmin) Migrate(_ context.Context) error {
	return migrations.MigrateAll(a.main, a.minute)
}

func (a *cliStorageAdmin) Verify(_ context.Context) (cliports.DatabaseStatus, cliports.DatabaseStatus, error) {
	main, err := migrations.VerifyMain(a.main)
	if err != nil {
		return cliports.DatabaseStatus{}, cliports.DatabaseStatus{}, err
	}
	minute, err := migrations.VerifyMinute(a.minute)
	if err != nil {
		return cliports.DatabaseStatus{}, cliports.DatabaseStatus{}, err
	}
	return mapDatabaseStatus(main), mapDatabaseStatus(minute), nil
}

func (a *cliStorageAdmin) Backup(_ context.Context, mainPath, minutePath string) error {
	if err := migrations.Backup(a.main, mainPath); err != nil {
		return fmt.Errorf("backup main database: %w", err)
	}
	if err := migrations.Backup(a.minute, minutePath); err != nil {
		_ = os.Remove(mainPath)
		return fmt.Errorf("backup minute database: %w", err)
	}
	return nil
}

func (a *cliStorageAdmin) QuickCheck(_ context.Context) error {
	if err := migrations.VerifySQLiteIntegrity(a.main); err != nil {
		return fmt.Errorf("main database: %w", err)
	}
	if err := migrations.VerifySQLiteIntegrity(a.minute); err != nil {
		return fmt.Errorf("minute database: %w", err)
	}
	return nil
}

func (*cliStorageAdmin) Close() error { return db.Close() }

func mapDatabaseStatus(status migrations.DatabaseStatus) cliports.DatabaseStatus {
	records := make([]cliports.MigrationRecord, 0, len(status.Records))
	for _, record := range status.Records {
		records = append(records, cliports.MigrationRecord{
			ID:         record.ID,
			Name:       record.Name,
			Checksum:   record.Checksum,
			AppliedAt:  record.AppliedAt.Format(time.RFC3339Nano),
			AppVersion: record.AppVersion,
		})
	}
	return cliports.DatabaseStatus{
		Database:        status.Database,
		CurrentVersion:  status.CurrentVersion,
		ExpectedVersion: status.ExpectedVersion,
		Pending:         append([]int(nil), status.Pending...),
		Records:         records,
		QuickCheck:      status.QuickCheck,
	}
}

func resolveCLIStoragePath(dataDir, dbPath string) string {
	if value := strings.TrimSpace(dbPath); value != "" {
		return value
	}
	if strings.TrimSpace(dataDir) == "" {
		dataDir = "data"
	}
	return filepath.Join(dataDir, "stock.db")
}
