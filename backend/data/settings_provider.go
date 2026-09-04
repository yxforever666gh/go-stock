package data

import "go-stock/backend/models"

// SettingsProvider exposes the existing settings implementation through the
// narrow boundary consumed by the settings application service.
type SettingsProvider struct{}

func NewSettingsProvider() SettingsProvider { return SettingsProvider{} }

func (SettingsProvider) LoadSettings() *models.SettingConfig { return GetSettingConfig() }
func (SettingsProvider) ExportSettings() string              { return NewSettingsApi().Export() }
func (SettingsProvider) UpdateSettings(config *models.SettingConfig) string {
	return UpdateConfig(config)
}
