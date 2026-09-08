// Package researchconfig owns independently persisted research configuration.
// It never reads process-wide settings or modifies provider overrides.
package researchconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"go-stock/backend/models"
)

const (
	Global    = "global"
	Research1 = "research1"
	Research2 = "research2"
)

var (
	ErrInvalidCenter  = errors.New("invalid research center")
	ErrInvalidConfig  = errors.New("invalid research configuration")
	ErrConflict       = errors.New("research configuration revision conflict")
	ErrModelOwnership = errors.New("AI model does not belong to this configuration")
)

// Explicit ownership is the sole JSON projection boundary. Values keep the
// existing typed SettingConfig contract; adding a global field never exposes it.
const commonFields = `tushareToken crawlTimeOut kDays browserPath browserPoolSize
httpProxy httpProxyEnabled forceNoProxyForFetch qgqpBId experimentalEvidenceEnabled
minuteProviderMode minuteProviderOrder minuteLongHistoryHintEnabled
privateMinuteEnabled privateMinuteBaseUrl privateMinuteApiKey privateMinuteTimeoutSec
privateMinuteMinIntervalMs privateMinuteProxyMode privateMinuteLevel
akshareEnabled sinaMinuteEnabled tencentMinuteEnabled eastmoneyMinuteEnabled akshareMinuteSourceMode`
const research1Fields = `aiCapitalDeploymentEnabled aiTargetCapitalUtilization aiMaxImmediateBuysPerRun
aiReanalysisIntervalMinutes aiReviewStartTime aiReviewIntervalMinutes`
const research2Fields = `research2AutoEnabled research2EmailEnabled research2EmailTo research2EmailFrom
research2EmailSmtpHost research2EmailSmtpPort research2EmailSmtpUsername research2EmailSmtpPassword`

func ownedFields(center string) ([]string, error) {
	switch center {
	case Research1:
		return strings.Fields(commonFields + " " + research1Fields), nil
	case Research2:
		return strings.Fields(commonFields + " " + research2Fields), nil
	default:
		return nil, ErrInvalidCenter
	}
}

// ConfigJSON omits models, persisted IDs and fields belonging to other scopes.
func ConfigJSON(center string, cfg *models.SettingConfig) ([]byte, error) {
	fields, err := ownedFields(center)
	if err != nil {
		return nil, err
	}
	if cfg == nil || cfg.Settings == nil {
		return nil, ErrInvalidConfig
	}
	cfg = Clone(cfg)
	if cfg.MinuteProviderOrder == nil {
		cfg.MinuteProviderOrder = []string{}
		if cfg.Settings.MinuteProviderOrder != "" {
			cfg.MinuteProviderOrder = strings.Split(cfg.Settings.MinuteProviderOrder, ",")
		}
	}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	var source map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &source); err != nil {
		return nil, err
	}
	selected := make(map[string]json.RawMessage, len(fields))
	for _, field := range fields {
		selected[field] = source[field]
	}
	return json.Marshal(selected)
}

// DecodeConfig rejects unknown/global/other-center fields before decoding the
// existing typed values. API callers pass aiConfigs separately to Store.Save.
func DecodeConfig(center string, raw []byte) (*models.SettingConfig, error) {
	fields, err := ownedFields(center)
	if err != nil {
		return nil, err
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil || values == nil {
		return nil, fmt.Errorf("%w: expected a configuration object", ErrInvalidConfig)
	}
	allowed := make(map[string]bool, len(fields))
	for _, field := range fields {
		allowed[field] = true
	}
	for key, value := range values {
		if !allowed[key] || string(value) == "null" {
			return nil, fmt.Errorf("%w: field %s", ErrInvalidConfig, key)
		}
	}
	cfg := &models.SettingConfig{Settings: &models.Settings{}}
	if err := json.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	cfg.Settings.MinuteProviderOrder = strings.Join(cfg.MinuteProviderOrder, ",")
	auto := cfg.AICapitalDeploymentEnabled
	cfg.AIAnalysisAutoEnabled = &auto
	return cfg, nil
}

// Clone makes task snapshots independent, including model objects and order.
func Clone(cfg *models.SettingConfig) *models.SettingConfig {
	if cfg == nil {
		return nil
	}
	copy := *cfg
	if cfg.Settings != nil {
		settings := *cfg.Settings
		copy.Settings = &settings
	}
	copy.MinuteProviderOrder = append([]string(nil), cfg.MinuteProviderOrder...)
	if cfg.AIAnalysisAutoEnabled != nil {
		value := *cfg.AIAnalysisAutoEnabled
		copy.AIAnalysisAutoEnabled = &value
	}
	if cfg.LegacyAIAnalysisEnable != nil {
		value := *cfg.LegacyAIAnalysisEnable
		copy.LegacyAIAnalysisEnable = &value
	}
	if cfg.AiConfigs != nil {
		copy.AiConfigs = make([]*models.AIConfig, len(cfg.AiConfigs))
		for i, model := range cfg.AiConfigs {
			if model != nil {
				item := *model
				copy.AiConfigs[i] = &item
			}
		}
	}
	return &copy
}
