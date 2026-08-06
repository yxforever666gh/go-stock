package bootstrap

import (
	"go-stock/backend/agent"
	"go-stock/backend/data"
	"go-stock/backend/models"
)

type compatibilityAgentConfigurationProvider struct{}

var _ agent.ConfigurationProvider = compatibilityAgentConfigurationProvider{}

// NewProductionAgentConfigurationProvider assembles the legacy-backed
// configuration reader used by the local production application.
func NewProductionAgentConfigurationProvider() agent.ConfigurationProvider {
	return compatibilityAgentConfigurationProvider{}
}

func (compatibilityAgentConfigurationProvider) AIConfigs() []*models.AIConfig {
	settings := data.GetSettingConfig()
	if settings == nil {
		return nil
	}
	return settings.AiConfigs
}

func (compatibilityAgentConfigurationProvider) PromptTemplateByID(id int) string {
	return data.NewPromptTemplateApi().GetPromptTemplateByID(id)
}
