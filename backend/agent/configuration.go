package agent

import "go-stock/backend/models"

// ConfigurationProvider supplies the configuration needed to create an agent.
// It is owned by the agent consumer so provider and persistence details remain
// outside the agent package.
type ConfigurationProvider interface {
	AIConfigs() []*models.AIConfig
	PromptTemplateByID(id int) string
}

func resolveAIConfig(configs []*models.AIConfig, id int) *models.AIConfig {
	for _, config := range configs {
		if config != nil && uint(id) == config.ID {
			return config
		}
	}
	if len(configs) > 0 {
		return configs[0]
	}
	return nil
}
