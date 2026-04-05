package service

import "go-stock/backend/data"

type ConfigService struct{}

func NewConfigService() ConfigService {
	return ConfigService{}
}

func (s ConfigService) GetConfig() *data.SettingConfig {
	return data.GetSettingConfig()
}

func (s ConfigService) ExportConfig() string {
	return data.NewSettingsApi().Export()
}

func (s ConfigService) UpdateConfig(settingConfig *data.SettingConfig) string {
	return data.UpdateConfig(settingConfig)
}
