package data

import (
	"bytes"
	"context"
	"fmt"
	appconfig "go-stock/internal/config"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

var ensureAkShareRuntimeOnce sync.Once
var ensureAkShareRuntimeErr error

func EnsureAkShareRuntime() error {
	ensureAkShareRuntimeOnce.Do(func() {
		ensureAkShareRuntimeErr = ensureAkShareRuntimeImpl()
	})
	return ensureAkShareRuntimeErr
}

func ResetAkShareRuntimeCheck() {
	ensureAkShareRuntimeOnce = sync.Once{}
	ensureAkShareRuntimeErr = nil
}

func ensureAkShareRuntimeImpl() error {
	pythonBin, explicit, err := resolvePythonExecutable()
	if err != nil {
		return err
	}

	if err := runCommandWithTimeout(60*time.Second, pythonBin, "-c", "import akshare"); err == nil {
		return nil
	}

	if explicit {
		return fmt.Errorf("embedded akshare runtime unavailable: import akshare failed with %s", pythonBin)
	}

	installErr := runCommandWithTimeout(8*time.Minute, pythonBin, "-m", "pip", "install", "--upgrade", "akshare")
	if installErr != nil {
		return fmt.Errorf("install akshare failed: %w", installErr)
	}

	if err := runCommandWithTimeout(90*time.Second, pythonBin, "-c", "import akshare"); err != nil {
		return fmt.Errorf("akshare import failed after install: %w", err)
	}
	return nil
}

func resolvePythonExecutable() (string, bool, error) {
	cfg := appconfig.Load()
	configured := strings.TrimSpace(cfg.Python.Bin)
	if configured != "" {
		if resolved, err := resolveConfiguredPythonPath(configured); err == nil {
			return resolved, true, nil
		} else {
			return "", true, err
		}
	}

	candidates := []string{"python3", "python"}
	if runtime.GOOS == "windows" {
		candidates = []string{"python", "python3"}
	}
	for _, candidate := range candidates {
		path, err := exec.LookPath(candidate)
		if err == nil {
			return path, false, nil
		}
	}
	return "", false, fmt.Errorf("python executable not found in PATH")
}

func resolveConfiguredPythonPath(raw string) (string, error) {
	path := strings.TrimSpace(raw)
	if path == "" {
		return "", fmt.Errorf("python executable path is empty")
	}
	if filepath.IsAbs(path) {
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("python executable not found: %w", err)
		}
		return path, nil
	}
	if strings.ContainsAny(path, `\/`) {
		absPath, err := filepath.Abs(path)
		if err != nil {
			return "", fmt.Errorf("resolve python executable failed: %w", err)
		}
		if _, err = os.Stat(absPath); err != nil {
			return "", fmt.Errorf("python executable not found: %w", err)
		}
		return absPath, nil
	}
	resolved, err := exec.LookPath(path)
	if err != nil {
		return "", fmt.Errorf("python executable not found: %w", err)
	}
	return resolved, nil
}

func runCommandWithTimeout(timeout time.Duration, name string, args ...string) error {
	ctx, cancel := contextWithTimeout(timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	var out bytes.Buffer
	var errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if err != nil {
		stderr := strings.TrimSpace(errBuf.String())
		stdout := strings.TrimSpace(out.String())
		if stderr != "" {
			return fmt.Errorf("%w: %s", err, stderr)
		}
		if stdout != "" {
			return fmt.Errorf("%w: %s", err, stdout)
		}
		return err
	}
	return nil
}

func contextWithTimeout(timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return context.WithTimeout(context.Background(), timeout)
}
