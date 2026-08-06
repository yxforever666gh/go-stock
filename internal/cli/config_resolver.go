package cli

import (
	"context"
	"errors"

	"go-stock/internal/bootstrap"
	cliports "go-stock/internal/cli/ports"
)

type AIOptions = cliports.CommandAIOptions
type CommandAIClient = cliports.CommandAIClient
type CommandAIResolver = cliports.CommandAIResolver

func ResolveAIForCommand(ctx context.Context, resolver CommandAIResolver, opts AIOptions) (CommandAIClient, error) {
	if resolver == nil {
		return nil, errors.New("command AI resolver is required")
	}
	return resolver.ResolveCommandAI(ctx, opts)
}

func ResolveFingerprint(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	services, err := bootstrap.NewProductionServices()
	if err != nil {
		return "", err
	}
	if fingerprint, err := services.Config.ResolveFingerprint(); err == nil && fingerprint != "" {
		return fingerprint, nil
	}
	return "", errors.New("缺少 qgqp_b_id，请通过 --qgqp-b-id 传入或先写入 settings.qgqp_b_id")
}
