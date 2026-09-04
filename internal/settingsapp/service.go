package settingsapp

import (
	"errors"
	"fmt"
	"strings"

	"go-stock/backend/models"
	"go-stock/internal/service"
)

type Provider interface {
	LoadSettings() *models.SettingConfig
	ExportSettings() string
	UpdateSettings(*models.SettingConfig) string
}

type Service struct {
	provider Provider
}

var _ service.ConfigService = (*Service)(nil)

func NewService(provider Provider) *Service { return &Service{provider: provider} }

func (s *Service) GetConfig() *models.SettingConfig { return s.provider.LoadSettings() }
func (s *Service) ExportConfig() string             { return s.provider.ExportSettings() }

func (s *Service) UpdateConfig(config *models.SettingConfig) (string, error) {
	message := s.provider.UpdateSettings(config)
	if strings.HasPrefix(message, "保存失败") {
		return message, fmt.Errorf("%w: %s", service.ErrInvalidInput, message)
	}
	return message, nil
}

func (s *Service) ResolveFingerprint() (string, error) {
	settings := s.provider.LoadSettings()
	if settings != nil && settings.Settings != nil && strings.TrimSpace(settings.QgqpBId) != "" {
		return strings.TrimSpace(settings.QgqpBId), nil
	}
	return "", errors.New("missing qgqp_b_id")
}
