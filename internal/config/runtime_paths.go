package config

import (
	"os"
	"path/filepath"
	"strings"
)

func resolveRuntimeDir() string {
	return strings.TrimSpace(os.Getenv("GO_STOCK_RUNTIME_DIR"))
}

func resolveDBPath(runtimeDir string) string {
	if value := strings.TrimSpace(os.Getenv("GO_STOCK_DB_PATH")); value != "" {
		return value
	}
	if runtimeDir == "" {
		return DefaultDBPath
	}
	return resolveDSNPath(runtimeDir, DefaultDBPath)
}

func resolveMinuteDBPath(runtimeDir string) string {
	if value := strings.TrimSpace(os.Getenv("GO_STOCK_MINUTE_DB_PATH")); value != "" {
		return value
	}
	if runtimeDir == "" {
		return DefaultMinuteDBPath
	}
	return resolveDSNPath(runtimeDir, DefaultMinuteDBPath)
}

func resolveDSNPath(runtimeDir string, raw string) string {
	base := strings.TrimSpace(raw)
	if base == "" {
		return base
	}
	parts := strings.SplitN(base, "?", 2)
	filePart := strings.TrimSpace(parts[0])
	if filePart == "" || filePart == ":memory:" || filepath.IsAbs(filePart) {
		return base
	}

	resolved := filepath.Join(runtimeDir, filepath.FromSlash(filePart))
	if len(parts) == 1 {
		return resolved
	}
	return resolved + "?" + parts[1]
}

func dbFilePath(raw string) string {
	parts := strings.SplitN(strings.TrimSpace(raw), "?", 2)
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimSpace(parts[0])
}

func (c AppConfig) RuntimePath(parts ...string) string {
	if c.Runtime.Dir == "" {
		return filepath.Join(parts...)
	}
	all := append([]string{c.Runtime.Dir}, parts...)
	return filepath.Join(all...)
}

func (c AppConfig) DBFilePath() string {
	return dbFilePath(c.DB.Path)
}

func (c AppConfig) MinuteDBFilePath() string {
	return dbFilePath(c.DB.MinutePath)
}

func (c AppConfig) LogFilePath(name string) string {
	return c.RuntimePath("logs", name)
}

func (c AppConfig) ExportBaseDir() string {
	return c.RuntimePath("exports")
}
