package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"go-stock/backend/governance"
	"go-stock/internal/bootstrap"
	cliports "go-stock/internal/cli/ports"
	"go-stock/internal/releaseinfo"
)

func runStrategy(args []string, opts GlobalOptions, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: strategy status|pause|resume")
	}
	subcommand := strings.ToLower(strings.TrimSpace(args[0]))
	if subcommand != "status" && subcommand != "pause" && subcommand != "resume" {
		return fmt.Errorf("unknown strategy subcommand %q", subcommand)
	}

	controller, err := bootstrap.NewProductionStrategyRuntimeController(resolveStrategyDBPath(opts.DataDir, opts.DBPath))
	if err != nil {
		return err
	}
	defer controller.Close()
	return runStrategyWithController(args, opts, stdout, stderr, controller)
}

func runStrategyWithController(args []string, opts GlobalOptions, stdout, stderr io.Writer, controller cliports.StrategyRuntimeController) error {
	if controller == nil {
		return fmt.Errorf("strategy runtime controller is required")
	}
	subcommand := strings.ToLower(strings.TrimSpace(args[0]))
	manifest := releaseinfo.Manifest()
	ctx := context.Background()

	if subcommand == "status" {
		status := controller.Status(ctx, manifest.CurrentStrategyVersion)
		if err := writeStrategyStatus(stdout, opts.JSON, status); err != nil {
			return err
		}
		if !status.Ready {
			return governance.ErrStrategyRuntimeUnavailable
		}
		return nil
	}

	fs := flag.NewFlagSet("strategy "+subcommand, flag.ContinueOnError)
	fs.SetOutput(stderr)
	reason := fs.String("reason", "", "auditable reason for the mode change")
	version := fs.String("version", manifest.CurrentStrategyVersion, "target immutable Strategy version")
	operator := fs.String("operator", defaultStrategyOperator(), "operator identity recorded in the audit row")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if strings.TrimSpace(*reason) == "" {
		return fmt.Errorf("--reason is required")
	}
	if strings.TrimSpace(*version) != manifest.CurrentStrategyVersion {
		return fmt.Errorf("strategy version %q does not match release manifest %q", *version, manifest.CurrentStrategyVersion)
	}
	mode := governance.StrategyModePaused
	if subcommand == "resume" {
		mode = governance.StrategyModeLive
	}
	status, err := controller.SetMode(ctx, mode, manifest.CurrentStrategyVersion, *reason, *operator)
	if err != nil {
		return err
	}
	return writeStrategyStatus(stdout, opts.JSON, status)
}

func resolveStrategyDBPath(dataDir, dbPath string) string {
	if value := strings.TrimSpace(dbPath); value != "" {
		return value
	}
	if strings.TrimSpace(dataDir) == "" {
		dataDir = "data"
	}
	return filepath.Join(dataDir, "stock.db")
}

func defaultStrategyOperator() string {
	if current, err := user.Current(); err == nil && strings.TrimSpace(current.Username) != "" {
		return current.Username
	}
	for _, name := range []string{"USERNAME", "USER"} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return "local-cli"
}

func writeStrategyStatus(w io.Writer, asJSON bool, status governance.StrategyRuntimeStatus) error {
	if asJSON {
		payload, err := json.MarshalIndent(status, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, string(payload))
		return err
	}
	fmt.Fprintf(w, "strategy mode: %s\n", status.Mode)
	fmt.Fprintf(w, "strategy version: %s\n", status.CurrentStrategyVersion)
	fmt.Fprintf(w, "ready: %t\n", status.Ready)
	if !status.ChangedAt.IsZero() {
		fmt.Fprintf(w, "changed at: %s\n", status.ChangedAt.UTC().Format("2006-01-02T15:04:05Z"))
	}
	if status.ChangedBy != "" {
		fmt.Fprintf(w, "changed by: %s\n", status.ChangedBy)
	}
	if status.Reason != "" {
		fmt.Fprintf(w, "reason: %s\n", status.Reason)
	}
	return nil
}
