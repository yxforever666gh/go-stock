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
