package config

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	DefaultWebListenAddr   = "127.0.0.1:34115"
	DefaultDBPath          = "data/stock.db?cache_size=-524288&journal_mode=WAL"
	DefaultMinuteDBPath    = "data/minute.db?cache_size=-524288&journal_mode=WAL"
	DefaultDBBusyTimeoutMS = 5000
	DefaultDBLogLevel      = "info"
	DefaultLogLevel        = "debug"
	// Minute provider used by yield/minute-bar cache sync.
	// Default to public providers in the released app; private minute providers
	// are optional and mainly used for longer historical windows.
	DefaultMinuteProvider        = "public"
	DefaultMinuteCoverTradeDays  = 0
	DefaultMinuteFallbackAkshare = false
	DefaultMinuteFallbackTencent = false
	DefaultAkshareMinuteSource   = "sina"
	DefaultAkshareProxyMode      = "disable"
	DefaultAkshareTimeoutSec     = 90
	DefaultAkshareMinIntervalMS  = 1500
	DefaultAkshareRetryWaitMS    = 2000
	DefaultSinaMinIntervalMS     = 650
	DefaultTencentMinIntervalMS  = 650
	DefaultDiemengBaseURL        = ""
	DefaultDiemengTimeoutSec     = 60
	DefaultDiemengMinIntervalMS  = 1200
	DefaultDiemengLevel          = "1min"
	DefaultDiemengProxyMode      = "disable"
	DefaultBrowserPath           = ""
)

type AppConfig struct {
	Web     WebConfig     `json:"web"`
	DB      DBConfig      `json:"db"`
	Log     LogConfig     `json:"log"`
	Runtime RuntimeConfig `json:"runtime"`
	Python  PythonConfig  `json:"python"`
	Update  UpdateConfig  `json:"update"`
	Minute  MinuteConfig  `json:"minute"`
	Akshare AkshareConfig `json:"akshare"`
	Diemeng DiemengConfig `json:"diemeng"`
	Browser BrowserConfig `json:"browser"`
}

type WebConfig struct {
	ListenAddr string `json:"listenAddr"`
}

type DBConfig struct {
	Path                string `json:"path"`
	MinutePath          string `json:"minutePath"`
	BusyTimeoutMS       int    `json:"busyTimeoutMs"`
	MinuteBusyTimeoutMS int    `json:"minuteBusyTimeoutMs"`
	LogLevel            string `json:"logLevel"`
}

type LogConfig struct {
	Level string `json:"level"`
}

type RuntimeConfig struct {
	Dir string `json:"dir,omitempty"`
}

type PythonConfig struct {
	Bin string `json:"bin,omitempty"`
}

type UpdateConfig struct {
	SelfUpdateEnabled bool `json:"selfUpdateEnabled"`
}

type MinuteConfig struct {
	Provider             string `json:"provider"`
	CoverTradeDays       int    `json:"coverTradeDays"`
	FallbackAkshare      bool   `json:"fallbackAkshare"`
	FallbackTencent      bool   `json:"fallbackTencent"`
	SinaMinIntervalMS    int    `json:"sinaMinIntervalMs"`
	TencentMinIntervalMS int    `json:"tencentMinIntervalMs"`
}

type AkshareConfig struct {
	MinuteSource  string `json:"minuteSource"`
	ProxyMode     string `json:"proxyMode"`
	TimeoutSec    int    `json:"timeoutSec"`
	MinIntervalMS int    `json:"minIntervalMs"`
	RetryWaitMS   int    `json:"retryWaitMs"`
}

type DiemengConfig struct {
	APIKey        string `json:"-"`
	BaseURL       string `json:"baseUrl"`
	TimeoutSec    int    `json:"timeoutSec"`
	MinIntervalMS int    `json:"minIntervalMs"`
	ProxyMode     string `json:"proxyMode"`
	Level         string `json:"level"`
}

type BrowserConfig struct {
	Path string `json:"path,omitempty"`
}

func Load() AppConfig {
	runtimeDir := resolveRuntimeDir()
	cfg := AppConfig{
		Web: WebConfig{
			ListenAddr: stringOrDefault("GO_STOCK_WEB_ADDR", DefaultWebListenAddr),
		},
		DB: DBConfig{
			Path:                resolveDBPath(runtimeDir),
			MinutePath:          resolveMinuteDBPath(runtimeDir),
			BusyTimeoutMS:       intOrDefault("GO_STOCK_DB_BUSY_TIMEOUT_MS", DefaultDBBusyTimeoutMS, noMinLimit, noMaxLimit),
			MinuteBusyTimeoutMS: intOrDefault("GO_STOCK_MINUTE_DB_BUSY_TIMEOUT_MS", DefaultDBBusyTimeoutMS, noMinLimit, noMaxLimit),
			LogLevel:            enumOrDefault("GO_STOCK_DB_LOG_LEVEL", DefaultDBLogLevel, "silent", "error", "warn", "warning", "info"),
		},
		Log: LogConfig{
			Level: enumOrDefault("GO_STOCK_LOG_LEVEL", DefaultLogLevel, "silent", "error", "warn", "warning", "info", "debug"),
		},
		Runtime: RuntimeConfig{
			Dir: runtimeDir,
		},
		Python: PythonConfig{
			Bin: strings.TrimSpace(os.Getenv("GO_STOCK_PYTHON_BIN")),
		},
		Update: UpdateConfig{
			SelfUpdateEnabled: boolOrDefault("GO_STOCK_SELF_UPDATE_ENABLED", false),
		},
		Minute: MinuteConfig{
			Provider:             enumOrDefault("GO_STOCK_MINUTE_PROVIDER", DefaultMinuteProvider, "public", "diemeng", "akshare", "auto", "sina", "tencent"),
			CoverTradeDays:       intOrDefault("GO_STOCK_MINUTE_COVER_TRADE_DAYS", DefaultMinuteCoverTradeDays, 0, 30),
			FallbackAkshare:      boolOrDefault("GO_STOCK_MINUTE_FALLBACK_AKSHARE", DefaultMinuteFallbackAkshare),
			FallbackTencent:      boolOrDefault("GO_STOCK_MINUTE_FALLBACK_TENCENT", DefaultMinuteFallbackTencent),
			SinaMinIntervalMS:    intOrDefault("GO_STOCK_SINA_MIN_INTERVAL_MS", DefaultSinaMinIntervalMS, 0, noMaxLimit),
			TencentMinIntervalMS: intOrDefault("GO_STOCK_TENCENT_MIN_INTERVAL_MS", DefaultTencentMinIntervalMS, 0, noMaxLimit),
		},
		Akshare: AkshareConfig{
			MinuteSource:  enumOrDefault("GO_STOCK_AKSHARE_MINUTE_SOURCE", DefaultAkshareMinuteSource, "sina", "em", "auto"),
			ProxyMode:     enumOrDefault("GO_STOCK_AKSHARE_PROXY_MODE", DefaultAkshareProxyMode, "disable", "inherit"),
			TimeoutSec:    intOrDefault("GO_STOCK_AKSHARE_TIMEOUT_SEC", DefaultAkshareTimeoutSec, 1, noMaxLimit),
			MinIntervalMS: intOrDefault("GO_STOCK_AKSHARE_MIN_INTERVAL_MS", DefaultAkshareMinIntervalMS, 1, noMaxLimit),
			RetryWaitMS:   intOrDefault("GO_STOCK_AKSHARE_RETRY_WAIT_MS", DefaultAkshareRetryWaitMS, 1, noMaxLimit),
		},
		Diemeng: DiemengConfig{
			APIKey:        secretOrFile("GO_STOCK_DIEMENG_API_KEY"),
			BaseURL:       normalizeDiemengBaseURL(stringOrDefault("GO_STOCK_DIEMENG_BASE_URL", DefaultDiemengBaseURL)),
			TimeoutSec:    intOrDefault("GO_STOCK_DIEMENG_TIMEOUT_SEC", DefaultDiemengTimeoutSec, 1, noMaxLimit),
			MinIntervalMS: intOrDefault("GO_STOCK_DIEMENG_MIN_INTERVAL_MS", DefaultDiemengMinIntervalMS, 0, noMaxLimit),
			ProxyMode:     enumOrDefault("GO_STOCK_DIEMENG_PROXY_MODE", DefaultDiemengProxyMode, "disable", "inherit", "settings", "config", "off", "none", "0", "false"),
			Level:         enumOrDefault("GO_STOCK_DIEMENG_LEVEL", DefaultDiemengLevel, "1min", "5min", "15min", "30min", "60min"),
		},
		Browser: BrowserConfig{
			Path: strings.TrimSpace(os.Getenv("GO_STOCK_BROWSER_PATH")),
		},
	}
	return applyRuntimeOverrides(cfg)
}

// secretOrFile keeps the plain environment variable for legacy desktop
// deployments while preferring a file-backed credential.  A secret reference
// such as secret://diemeng_api_key is resolved only below GO_STOCK_SECRETS_DIR;
// malformed, ambiguous or unreadable inputs fail closed to an empty value.
func secretOrFile(key string) string {
	value := strings.TrimSpace(os.Getenv(key))
	filePath := strings.TrimSpace(os.Getenv(key + "_FILE"))
	if filePath != "" && value != "" {
		return ""
	}
	if filePath != "" {
		return readOneLineSecret(filePath)
	}
	if !strings.HasPrefix(value, "secret://") {
		return value
	}

	name := strings.TrimPrefix(value, "secret://")
	if !safeSecretName(name) {
		return ""
	}
	root := strings.TrimSpace(os.Getenv("GO_STOCK_SECRETS_DIR"))
	if root == "" {
		return ""
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return ""
	}
	candidate := filepath.Join(resolvedRoot, name)
	resolvedCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return ""
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedCandidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return ""
	}
	return readOneLineSecret(candidate)
}

func safeSecretName(name string) bool {
	if name == "" || strings.HasPrefix(name, ".") || strings.Contains(name, "..") {
		return false
	}
	for _, char := range name {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '_' || char == '-' || char == '.' {
			continue
		}
		return false
	}
	return true
}

func readOneLineSecret(path string) string {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil || !utf8.Valid(raw) || strings.IndexByte(string(raw), 0) >= 0 {
		return ""
	}
	value := string(raw)
	value = strings.TrimSuffix(value, "\n")
	value = strings.TrimSuffix(value, "\r")
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return ""
	}
	return value
}

func (c AppConfig) StartupSummary() string {
	summary := map[string]any{
		"web": map[string]any{
			"listenAddr": c.Web.ListenAddr,
		},
		"db": map[string]any{
			"path":                c.DB.Path,
			"minutePath":          c.DB.MinutePath,
			"busyTimeoutMs":       c.DB.BusyTimeoutMS,
			"minuteBusyTimeoutMs": c.DB.MinuteBusyTimeoutMS,
			"logLevel":            c.DB.LogLevel,
		},
		"log": map[string]any{
			"level": c.Log.Level,
		},
		"update": map[string]any{
			"selfUpdateEnabled": c.Update.SelfUpdateEnabled,
		},
		"minute": map[string]any{
			"provider":             c.Minute.Provider,
			"coverTradeDays":       c.Minute.CoverTradeDays,
			"fallbackAkshare":      c.Minute.FallbackAkshare,
			"fallbackTencent":      c.Minute.FallbackTencent,
			"sinaMinIntervalMs":    c.Minute.SinaMinIntervalMS,
			"tencentMinIntervalMs": c.Minute.TencentMinIntervalMS,
		},
		"akshare": map[string]any{
			"minuteSource":  c.Akshare.MinuteSource,
			"proxyMode":     c.Akshare.ProxyMode,
			"timeoutSec":    c.Akshare.TimeoutSec,
			"minIntervalMs": c.Akshare.MinIntervalMS,
			"retryWaitMs":   c.Akshare.RetryWaitMS,
		},
		"diemeng": map[string]any{
			"baseUrl":          c.Diemeng.BaseURL,
			"timeoutSec":       c.Diemeng.TimeoutSec,
			"minIntervalMs":    c.Diemeng.MinIntervalMS,
			"proxyMode":        c.Diemeng.ProxyMode,
			"level":            c.Diemeng.Level,
			"apiKeyConfigured": strings.TrimSpace(c.Diemeng.APIKey) != "",
		},
	}
	if c.Runtime.Dir != "" {
		summary["runtime"] = map[string]any{"dir": c.Runtime.Dir}
	}
	if c.Python.Bin != "" {
		summary["python"] = map[string]any{"bin": c.Python.Bin}
	}
	if c.Browser.Path != "" {
		summary["browser"] = map[string]any{"path": c.Browser.Path}
	}
	buf, err := json.Marshal(summary)
	if err != nil {
		return `{"error":"marshal startup summary failed"}`
	}
	return string(buf)
}

const (
	noMinLimit = -1
	noMaxLimit = -1
)

func stringOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func trimTrailingSlash(value string) string {
	if strings.TrimSpace(value) == "" {
		return value
	}
	return strings.TrimRight(value, "/")
}

func normalizeDiemengBaseURL(value string) string {
	trimmed := trimTrailingSlash(value)
	if trimmed == "" {
		return trimmed
	}

	parsed, err := url.Parse(trimmed)
	if err != nil || parsed == nil || strings.TrimSpace(parsed.Scheme) == "" || strings.TrimSpace(parsed.Host) == "" {
		return trimmed
	}

	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	path := trimTrailingSlash(parsed.Path)
	if host != "" && (path == "" || path == "/") {
		parsed.Path = "/api"
	} else {
		parsed.Path = path
	}

	return trimTrailingSlash(parsed.String())
}

func enumOrDefault(key, fallback string, allowed ...string) string {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	for _, candidate := range allowed {
		if value == candidate {
			return value
		}
	}
	return fallback
}

func boolOrDefault(key string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	case "":
		return fallback
	default:
		return fallback
	}
}

func intOrDefault(key string, fallback, minValue, maxValue int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	number, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	if minValue != noMinLimit && number < minValue {
		return fallback
	}
	if maxValue != noMaxLimit && number > maxValue {
		return fallback
	}
	return number
}
