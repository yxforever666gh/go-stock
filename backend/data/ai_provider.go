package data

import "go-stock/backend/ai"

func DisplayAIProviderName(aiConfig *AIConfig) string {
	if aiConfig == nil {
		return ""
	}
	return ai.DisplayProviderName(aiConfig.Name, aiConfig.BaseUrl, aiConfig.ModelName)
}
