package bootstrap

import (
	"context"

	"go-stock/backend/data"
)

type legacyApplicationInitializer struct{}

func (legacyApplicationInitializer) EnsureSettings(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return data.EnsureSettingsRecord()
}

func (legacyApplicationInitializer) InitializeSentiment(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data.InitAnalyzeSentiment()
	return nil
}
