package service

import "go-stock/backend/models"

type ConfigService struct {
	operations ConfigOperations
}

func NewConfigService(operations ConfigOperations) ConfigService {
	return ConfigService{operations: operations}
}

func (s ConfigService) GetConfig() *models.SettingConfig {
	return s.operations.GetConfig()
}

func (s ConfigService) ExportConfig() string {
	return s.operations.ExportConfig()
}

func (s ConfigService) UpdateConfig(settingConfig *models.SettingConfig) string {
	return s.operations.UpdateConfig(settingConfig)
}
