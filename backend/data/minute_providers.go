package data

import (
	"os"
	"strings"
	"sync"
	"time"

	"go-stock/backend/models"
	appconfig "go-stock/internal/config"
)

// minuteProviders owns captured credentials and failure state for one task.
// The raw minute-bar database remains shared; failed requests never cross tasks.
type minuteProviders struct {
	settings        *models.SettingConfig
	environment     appconfig.AppConfig
	state           *minuteProviderState
	calendar        ResearchTradingCalendar
	processEnv      []string
	adjustment      string
	scriptPath      string
	scriptErr       error
	keepDiemengHost bool
}

type minuteProviderState struct {
	tencentMinuteFetchMu          sync.Mutex
	tencentMinuteLastFetch        time.Time
	tencentMinuteCircuitMu        sync.Mutex
	tencentMinuteCircuitOpenUntil time.Time
	tencentMinuteCircuitFailCount int
	tencentMinuteCircuitLastErr   string
	sinaMinuteFetchMu             sync.Mutex
	sinaMinuteLastFetch           time.Time
	sinaMinuteCircuitMu           sync.Mutex
	sinaMinuteCircuitOpenUntil    time.Time
	sinaMinuteCircuitFailCount    int
	sinaMinuteCircuitLastErr      string
	diemengFetchMu                sync.Mutex
	diemengLastFetch              time.Time
	diemengCircuitMu              sync.Mutex
	diemengCircuitOpenUntil       time.Time
	diemengCircuitFailCount       int
	diemengCircuitLastErr         string
	akShareFetchMu                sync.Mutex
	akShareLastFetch              time.Time
	akShareCircuitMu              sync.Mutex
	akShareCircuitOpenUntil       time.Time
	akShareCircuitFailCount       int
	akShareCircuitLastErr         string

	suspensionMu     sync.Mutex
	suspensions      map[string]diemengSuspensionCacheEntry
	fetchSuspensions func(string, time.Time) ([]diemengSuspensionItem, error)
}

var globalMinuteProviderState = &minuteProviderState{suspensions: map[string]diemengSuspensionCacheEntry{}}

func newMinuteProviders(setting *models.SettingConfig) *minuteProviders {
	setting = cloneProviderSettings(setting)
	env := appconfig.LoadEnvironment()
	env.Diemeng.APIKey = setting.PrivateMinuteAPIKey
	env.Diemeng.BaseURL = setting.PrivateMinuteBaseURL
	env.Diemeng.ProxyMode = setting.PrivateMinuteProxyMode
	env.Diemeng.Level = normalizePrivateMinuteLevel(setting.PrivateMinuteLevel)
	if setting.PrivateMinuteTimeoutSec > 0 {
		env.Diemeng.TimeoutSec = setting.PrivateMinuteTimeoutSec
	}
	if setting.PrivateMinuteMinInterval >= 0 {
		env.Diemeng.MinIntervalMS = setting.PrivateMinuteMinInterval
	}
	if strings.TrimSpace(setting.AkshareMinuteSourceMode) != "" {
		env.Akshare.MinuteSource = setting.AkshareMinuteSourceMode
	}
	script, scriptErr := akShareScriptPath()
	return &minuteProviders{
		settings: setting, environment: env, state: &minuteProviderState{suspensions: map[string]diemengSuspensionCacheEntry{}},
		calendar: NewResearchTradingCalendar(setting), processEnv: os.Environ(),
		adjustment: strings.ToLower(strings.TrimSpace(os.Getenv("GO_STOCK_AKSHARE_MINUTE_ADJUST"))),
		scriptPath: script, scriptErr: scriptErr, keepDiemengHost: systemProxyContains("mohomoparty"),
	}
}

// Non-research market/CLI adapters retain the global configuration entry point.
func newGlobalMinuteProviders() *minuteProviders {
	providers := newMinuteProviders(GetSettingConfig())
	providers.environment = appconfig.Load()
	providers.state = globalMinuteProviderState
	providers.calendar = ResearchTradingCalendar{}
	return providers
}
func minuteProvidersForStocks(stocks *StockDataApi) *minuteProviders {
	if stocks != nil {
		if stocks.minutes != nil {
			return stocks.minutes
		}
		return newMinuteProviders(stocks.config)
	}
	return newMinuteProviders(nil)
}
