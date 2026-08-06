package cli

import (
	"fmt"
	"go-stock/internal/bootstrap"
)

func Bootstrap(dataDir, dbPath string) (string, error) {
	resolvedDBPath, err := bootstrap.InitCLIStorage(dataDir, dbPath)
	if err != nil {
		return "", fmt.Errorf("初始化失败: %w", err)
	}
	return resolvedDBPath, nil
}

// BootstrapReadOnly is intentionally separate from normal CLI bootstrap: it
// neither creates directories nor runs migrations/settings initialization.
func BootstrapReadOnly(dataDir, dbPath string) (string, error) {
	resolvedDBPath, err := bootstrap.InitCLIStorageReadOnly(dataDir, dbPath)
	if err != nil {
		return "", fmt.Errorf("initialize read-only storage: %w", err)
	}
	return resolvedDBPath, nil
}
