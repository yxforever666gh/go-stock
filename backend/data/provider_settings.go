package data

import "go-stock/backend/models"

// cloneProviderSettings gives a provider one immutable task configuration.
// Nil means an empty configuration, never a request to load global settings.
func cloneProviderSettings(source *models.SettingConfig) *models.SettingConfig {
	result := &models.SettingConfig{Settings: &models.Settings{}}
	if source == nil {
		return result
	}
	*result = *source
	result.Settings = &models.Settings{}
	if source.Settings != nil {
		*result.Settings = *source.Settings
	}
	result.MinuteProviderOrder = append([]string(nil), source.MinuteProviderOrder...)
	result.AiConfigs = make([]*models.AIConfig, len(source.AiConfigs))
	for i, config := range source.AiConfigs {
		if config != nil {
			copy := *config
			if config.ArchivedAt != nil {
				at := *config.ArchivedAt
				copy.ArchivedAt = &at
			}
			result.AiConfigs[i] = &copy
		}
	}
	if source.AIAnalysisAutoEnabled != nil {
		copy := *source.AIAnalysisAutoEnabled
		result.AIAnalysisAutoEnabled = &copy
	}
	if source.LegacyAIAnalysisEnable != nil {
		copy := *source.LegacyAIAnalysisEnable
		result.LegacyAIAnalysisEnable = &copy
	}
	return result
}
