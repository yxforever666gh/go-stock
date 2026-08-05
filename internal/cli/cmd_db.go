package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go-stock/backend/db"
	"go-stock/internal/migrations"
)

type dbCommandResult struct {
	Main    migrations.DatabaseStatus `json:"main"`
	Minute  migrations.DatabaseStatus `json:"minute"`
	Backups map[string]string         `json:"backups,omitempty"`
}

func runDB(args []string, opts GlobalOptions, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: db status|backup|migrate|verify")
	}
	subcommand := strings.ToLower(strings.TrimSpace(args[0]))
	if subcommand != "status" && subcommand != "backup" && subcommand != "migrate" && subcommand != "verify" {
		return fmt.Errorf("unknown db subcommand %q", subcommand)
	}
	db.Init(resolveCLIPrimaryDBPath(opts.DataDir, opts.DBPath))
	defer db.Close()

	switch subcommand {
	case "status":
		mainStatus, err := migrations.StatusMain(db.Dao)
		if err != nil {
			return err
		}
		minuteStatus, err := migrations.StatusMinute(db.MinuteDao)
		if err != nil {
			return err
		}
		return writeDBResult(stdout, opts.JSON, dbCommandResult{Main: mainStatus, Minute: minuteStatus})
	case "migrate":
		if err := migrations.MigrateAll(db.Dao, db.MinuteDao); err != nil {
			return err
		}
		fallthrough
	case "verify":
		mainStatus, err := migrations.VerifyMain(db.Dao)
		if err != nil {
			return err
		}
		minuteStatus, err := migrations.VerifyMinute(db.MinuteDao)
		if err != nil {
			return err
		}
		return writeDBResult(stdout, opts.JSON, dbCommandResult{Main: mainStatus, Minute: minuteStatus})
	case "backup":
		fs := flag.NewFlagSet("db backup", flag.ContinueOnError)
		fs.SetOutput(stderr)
		outputDir := fs.String("output", "", "backup output directory")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(*outputDir) == "" {
			*outputDir = filepath.Join("runtime", "backups", time.Now().Format("20060102-150405"))
		}
		mainBackup := filepath.Join(*outputDir, "stock.db")
		minuteBackup := filepath.Join(*outputDir, "minute.db")
		if err := migrations.Backup(db.Dao, mainBackup); err != nil {
			return fmt.Errorf("backup main database: %w", err)
		}
		if err := migrations.Backup(db.MinuteDao, minuteBackup); err != nil {
			_ = os.Remove(mainBackup)
			return fmt.Errorf("backup minute database: %w", err)
		}
		mainStatus, _ := migrations.StatusMain(db.Dao)
		minuteStatus, _ := migrations.StatusMinute(db.MinuteDao)
		return writeDBResult(stdout, opts.JSON, dbCommandResult{
			Main: mainStatus, Minute: minuteStatus,
			Backups: map[string]string{"main": mainBackup, "minute": minuteBackup},
		})
	}
	return nil
}

func resolveCLIPrimaryDBPath(dataDir, dbPath string) string {
	if value := strings.TrimSpace(dbPath); value != "" {
		return value
	}
	if strings.TrimSpace(dataDir) == "" {
		dataDir = "data"
	}
	return filepath.Join(dataDir, "stock.db")
}

func writeDBResult(w io.Writer, asJSON bool, result dbCommandResult) error {
	if asJSON {
		payload, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, string(payload))
		return err
	}
	fmt.Fprintf(w, "main schema: %d/%d pending=%v quick_check=%s\n", result.Main.CurrentVersion, result.Main.ExpectedVersion, result.Main.Pending, result.Main.QuickCheck)
	fmt.Fprintf(w, "minute schema: %d/%d pending=%v quick_check=%s\n", result.Minute.CurrentVersion, result.Minute.ExpectedVersion, result.Minute.Pending, result.Minute.QuickCheck)
	if len(result.Backups) != 0 {
		fmt.Fprintf(w, "main backup: %s\nminute backup: %s\n", result.Backups["main"], result.Backups["minute"])
	}
	return nil
}
