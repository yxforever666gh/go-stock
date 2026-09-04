package cli

import (
	"context"
	"errors"

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

type fingerprintResolver interface {
	ResolveFingerprint() (string, error)
}

func ResolveFingerprint(flagValue string, resolver fingerprintResolver) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if resolver != nil {
		if fingerprint, err := resolver.ResolveFingerprint(); err == nil && fingerprint != "" {
			return fingerprint, nil
		}
	}
	return "", errors.New("缺少 qgqp_b_id，请通过 --qgqp-b-id 传入或先写入 settings.qgqp_b_id")
}
