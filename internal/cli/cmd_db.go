package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"go-stock/internal/bootstrap"
	cliports "go-stock/internal/cli/ports"
)

type dbCommandResult struct {
	Main    cliports.DatabaseStatus `json:"main"`
	Minute  cliports.DatabaseStatus `json:"minute"`
	Backups map[string]string       `json:"backups,omitempty"`
}

func runDB(args []string, opts GlobalOptions, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: db status|backup|migrate|verify")
	}
	subcommand := strings.ToLower(strings.TrimSpace(args[0]))
	if subcommand != "status" && subcommand != "backup" && subcommand != "migrate" && subcommand != "verify" {
		return fmt.Errorf("unknown db subcommand %q", subcommand)
	}
	if subcommand == "verify" {
		fs := flag.NewFlagSet("db verify", flag.ContinueOnError)
		fs.SetOutput(stderr)
		quickOnly := fs.Bool("quick-only", false, "verify SQLite integrity without requiring the current schema")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if len(fs.Args()) != 0 {
			return fmt.Errorf("db verify does not accept positional arguments: %s", strings.Join(fs.Args(), " "))
		}
		if *quickOnly {
			return runDBQuickCheck(opts, stdout)
		}
	} else if len(args) > 1 && subcommand != "backup" {
		return fmt.Errorf("db %s does not accept positional arguments: %s", subcommand, strings.Join(args[1:], " "))
	}
	admin, err := bootstrap.NewProductionCLIStorageAdmin(resolveCLIPrimaryDBPath(opts.DataDir, opts.DBPath), subcommand == "status")
	if err != nil {
		return err
	}
	defer admin.Close()
	return runDBWithAdmin(subcommand, args[1:], opts, stdout, stderr, admin)
}

func runDBWithAdmin(subcommand string, args []string, opts GlobalOptions, stdout, stderr io.Writer, admin cliports.StorageAdmin) error {
	if admin == nil {
		return fmt.Errorf("database storage admin is required")
	}
	ctx := context.Background()
	switch subcommand {
	case "status":
		mainStatus, minuteStatus, err := admin.Status(ctx)
		if err != nil {
			return err
		}
		return writeDBResult(stdout, opts.JSON, dbCommandResult{Main: mainStatus, Minute: minuteStatus})
	case "migrate":
		if err := admin.Migrate(ctx); err != nil {
			return err
		}
		fallthrough
	case "verify":
		mainStatus, minuteStatus, err := admin.Verify(ctx)
		if err != nil {
			return err
		}
		return writeDBResult(stdout, opts.JSON, dbCommandResult{Main: mainStatus, Minute: minuteStatus})
	case "backup":
		fs := flag.NewFlagSet("db backup", flag.ContinueOnError)
		fs.SetOutput(stderr)
		outputDir := fs.String("output", "", "backup output directory")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if strings.TrimSpace(*outputDir) == "" {
			*outputDir = filepath.Join("runtime", "backups", time.Now().Format("20060102-150405"))
		}
		mainBackup := filepath.Join(*outputDir, "stock.db")
		minuteBackup := filepath.Join(*outputDir, "minute.db")
		if err := admin.Backup(ctx, mainBackup, minuteBackup); err != nil {
			return err
		}
		mainStatus, minuteStatus, _ := admin.Status(ctx)
		return writeDBResult(stdout, opts.JSON, dbCommandResult{
			Main: mainStatus, Minute: minuteStatus,
			Backups: map[string]string{"main": mainBackup, "minute": minuteBackup},
		})
	}
	return nil
}

func runDBQuickCheck(opts GlobalOptions, stdout io.Writer) error {
	admin, err := bootstrap.NewProductionCLIStorageAdmin(resolveCLIPrimaryDBPath(opts.DataDir, opts.DBPath), true)
	if err != nil {
		return err
	}
	defer admin.Close()
	if err := admin.QuickCheck(context.Background()); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, "main quick_check=ok\nminute quick_check=ok")
	return err
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
