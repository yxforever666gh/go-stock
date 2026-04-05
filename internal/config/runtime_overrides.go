package config

import "sync"

type RuntimeOverride struct {
	MinuteProvider      *string
	AkshareMinuteSource *string
	DiemengAPIKey       *string
	DiemengBaseURL      *string
	DiemengTimeoutSec   *int
	DiemengMinInterval  *int
	DiemengProxyMode    *string
	DiemengLevel        *string
}

var (
	runtimeOverrideMu sync.RWMutex
	runtimeOverride   *RuntimeOverride
)

func SetRuntimeOverride(override *RuntimeOverride) {
	runtimeOverrideMu.Lock()
	defer runtimeOverrideMu.Unlock()
	if override == nil {
		runtimeOverride = nil
		return
	}
	cloned := *override
	runtimeOverride = &cloned
}

func ResetRuntimeOverride() {
	SetRuntimeOverride(nil)
}

func applyRuntimeOverrides(cfg AppConfig) AppConfig {
	runtimeOverrideMu.RLock()
	override := runtimeOverride
	runtimeOverrideMu.RUnlock()
	if override == nil {
		return cfg
	}

	if override.MinuteProvider != nil && *override.MinuteProvider != "" {
		cfg.Minute.Provider = *override.MinuteProvider
	}
	if override.AkshareMinuteSource != nil && *override.AkshareMinuteSource != "" {
		cfg.Akshare.MinuteSource = *override.AkshareMinuteSource
	}
	if override.DiemengAPIKey != nil {
		cfg.Diemeng.APIKey = *override.DiemengAPIKey
	}
	if override.DiemengBaseURL != nil && *override.DiemengBaseURL != "" {
		cfg.Diemeng.BaseURL = normalizeDiemengBaseURL(*override.DiemengBaseURL)
	}
	if override.DiemengTimeoutSec != nil && *override.DiemengTimeoutSec > 0 {
		cfg.Diemeng.TimeoutSec = *override.DiemengTimeoutSec
	}
	if override.DiemengMinInterval != nil && *override.DiemengMinInterval >= 0 {
		cfg.Diemeng.MinIntervalMS = *override.DiemengMinInterval
	}
	if override.DiemengProxyMode != nil && *override.DiemengProxyMode != "" {
		cfg.Diemeng.ProxyMode = *override.DiemengProxyMode
	}
	if override.DiemengLevel != nil && *override.DiemengLevel != "" {
		cfg.Diemeng.Level = *override.DiemengLevel
	}
	return cfg
}
