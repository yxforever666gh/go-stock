package data

import (
	"context"

	"go-stock/backend/ai"
	"go-stock/backend/logger"
	"go-stock/backend/models"
)

// ResearchAIClientOptions adapts persisted settings and the concrete OpenAI
// implementation to the provider-neutral orchestration in backend/ai.
func ResearchAIClientOptionsForSettings(setting *models.SettingConfig) ai.ResearchClientOptions {
	snapshot := cloneProviderSettings(setting)
	return ai.ResearchClientOptions{
		LoadConfigs: func() []*models.AIConfig {
			return cloneProviderSettings(snapshot).AiConfigs
		},
		CompleteProvider: func(ctx context.Context, config *models.AIConfig, messages []map[string]any, previousResponseID string, activity func(ai.StreamActivity)) (string, string, string, error) {
			provider := NewOpenAiWithSettings(ctx, config, snapshot)
			provider.DisableRequestRetries = true
			return provider.CompleteResearchStream(ctx, messages, previousResponseID, activity)
		},
		Warnf: logger.SugaredLogger.Warnf,
	}
}
