package data

import (
	"encoding/json"
	"errors"
	"fmt"
	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	appconfig "go-stock/internal/config"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/samber/lo"
	"gorm.io/gorm"
)

type Settings = models.Settings
type AIConfig = models.AIConfig
type SettingConfig = models.SettingConfig
type AIModelTestResult = models.AIModelTestResult

type SettingsApi struct {
	Config *SettingConfig
}

var strictYieldEmailCronTimeRegexp = regexp.MustCompile(`^([01]\d|2[0-3]):([0-5]\d)$`)

const defaultMarketSummaryCronTimes = "09:40,11:30,14:30"
const (
	AIAPIProtocolChatCompletions  = models.AIAPIProtocolChatCompletions
	AIAPIProtocolOpenAIResponses  = models.AIAPIProtocolOpenAIResponses
	AIAPIProtocolAnthropicMessage = models.AIAPIProtocolAnthropicMessage
)

func NewSettingsApi() *SettingsApi {
	return &SettingsApi{
		Config: GetSettingConfig(),
	}
}

func (s *SettingsApi) Export() string {
	d, _ := json.MarshalIndent(s.Config, "", "    ")
	return string(d)
}

func UpdateConfig(s *SettingConfig) string {
	if s == nil || s.Settings == nil {
		return "保存失败: 配置为空"
	}
	normalizedRecipients, err := NormalizeYieldEmailRecipients(s.YieldEmailTo)
	if err != nil {
		return "保存失败: " + err.Error()
	}
	s.YieldEmailTo = normalizedRecipients
	s.YieldEmailFrom = strings.TrimSpace(s.YieldEmailFrom)
	s.YieldEmailSMTPHost = strings.TrimSpace(s.YieldEmailSMTPHost)
	s.YieldEmailSMTPUsername = strings.TrimSpace(s.YieldEmailSMTPUsername)
	s.YieldEmailSMTPPassword = strings.TrimSpace(s.YieldEmailSMTPPassword)
	s.MinuteProviderMode = normalizeMinuteProviderMode(s.MinuteProviderMode)
	s.PrivateMinuteBaseURL = strings.TrimSpace(s.PrivateMinuteBaseURL)
	s.PrivateMinuteAPIKey = strings.TrimSpace(s.PrivateMinuteAPIKey)
	s.PrivateMinuteProxyMode = normalizePrivateMinuteProxyMode(s.PrivateMinuteProxyMode)
	s.PrivateMinuteLevel = normalizePrivateMinuteLevel(s.PrivateMinuteLevel)
	s.AkshareMinuteSourceMode = normalizeAkshareMinuteSourceMode(s.AkshareMinuteSourceMode)

	normalizedCronTimes, err := NormalizeYieldEmailCronTimes(s.YieldEmailCronTimes)
	if err != nil {
		return "保存失败: " + err.Error()
	}
	if s.YieldEmailCronEnabled && len(normalizedCronTimes) == 0 {
		return "保存失败: 请至少填写一个定时发送时间，例如 09:30,15:05"
	}
	s.YieldEmailCronTimes = strings.Join(normalizedCronTimes, ",")
	normalizedSummaryCronTimes, err := NormalizeMarketSummaryCronTimes(s.MarketSummaryCronTimes)
	if err != nil {
		return "保存失败: " + err.Error()
	}
	if s.MarketSummaryCronEnabled && len(normalizedSummaryCronTimes) == 0 {
		return "保存失败: 请至少填写一个市场资讯定时总结时间，例如 09:40,11:30,14:30"
	}
	s.MarketSummaryCronTimes = strings.Join(normalizedSummaryCronTimes, ",")
	if s.MinuteProviderMode == "private" {
		if !s.PrivateMinuteEnabled {
			return "保存失败: 私人分钟线模式需要先启用私人分钟线来源"
		}
		if s.PrivateMinuteBaseURL == "" {
			return "保存失败: 私人分钟线模式下，请填写调用 URL"
		}
		if s.PrivateMinuteAPIKey == "" {
			return "保存失败: 私人分钟线模式下，请填写 API Key"
		}
	}
	if s.MinuteProviderMode == "public" && !s.AkshareEnabled && !s.SinaMinuteEnabled && !s.TencentMinuteEnabled {
		return "保存失败: 公共分钟线模式下，至少保留一个公共数据源"
	}
	if s.PrivateMinuteTimeoutSec <= 0 {
		s.PrivateMinuteTimeoutSec = appconfig.DefaultDiemengTimeoutSec
	}
	if s.PrivateMinuteMinInterval < 0 {
		s.PrivateMinuteMinInterval = appconfig.DefaultDiemengMinIntervalMS
	}

	updateMap := map[string]any{
		"local_push_enable":                s.LocalPushEnable,
		"ding_push_enable":                 s.DingPushEnable,
		"ding_robot":                       s.DingRobot,
		"yield_email_enable":               s.YieldEmailEnable,
		"yield_email_to":                   s.YieldEmailTo,
		"yield_email_from":                 s.YieldEmailFrom,
		"yield_email_smtp_host":            s.YieldEmailSMTPHost,
		"yield_email_smtp_port":            s.YieldEmailSMTPPort,
		"yield_email_smtp_username":        s.YieldEmailSMTPUsername,
		"yield_email_smtp_password":        s.YieldEmailSMTPPassword,
		"yield_email_cron_enabled":         s.YieldEmailCronEnabled,
		"yield_email_cron_times":           s.YieldEmailCronTimes,
		"market_summary_email_enable":      s.MarketSummaryEmailEnable,
		"update_basic_info_on_start":       s.UpdateBasicInfoOnStart,
		"refresh_interval":                 s.RefreshInterval,
		"open_ai_enable":                   s.OpenAiEnable,
		"tushare_token":                    s.TushareToken,
		"prompt":                           s.Prompt,
		"check_update":                     s.CheckUpdate,
		"question_template":                s.QuestionTemplate,
		"crawl_time_out":                   s.CrawlTimeOut,
		"k_days":                           s.KDays,
		"enable_danmu":                     s.EnableDanmu,
		"browser_path":                     s.BrowserPath,
		"enable_news":                      s.EnableNews,
		"dark_theme":                       s.DarkTheme,
		"browser_pool_size":                s.BrowserPoolSize,
		"enable_fund":                      s.EnableFund,
		"enable_push_news":                 s.EnablePushNews,
		"enable_only_push_red_news":        s.EnableOnlyPushRedNews,
		"http_proxy":                       s.HttpProxy,
		"http_proxy_enabled":               s.HttpProxyEnabled,
		"force_no_proxy_for_fetch":         s.ForceNoProxyForFetch,
		"enable_agent":                     s.EnableAgent,
		"qgqp_b_id":                        s.QgqpBId,
		"market_summary_cron_enabled":      s.MarketSummaryCronEnabled,
		"market_summary_cron_times":        s.MarketSummaryCronTimes,
		"minute_provider_mode":             s.MinuteProviderMode,
		"minute_long_history_hint_enabled": s.MinuteLongHistoryHint,
		"private_minute_enabled":           s.PrivateMinuteEnabled,
		"private_minute_base_url":          s.PrivateMinuteBaseURL,
		"private_minute_api_key":           s.PrivateMinuteAPIKey,
		"private_minute_timeout_sec":       s.PrivateMinuteTimeoutSec,
		"private_minute_min_interval":      s.PrivateMinuteMinInterval,
		"private_minute_proxy_mode":        s.PrivateMinuteProxyMode,
		"private_minute_level":             s.PrivateMinuteLevel,
		"akshare_enabled":                  s.AkshareEnabled,
		"sina_minute_enabled":              s.SinaMinuteEnabled,
		"tencent_minute_enabled":           s.TencentMinuteEnabled,
		"eastmoney_minute_enabled":         s.EastmoneyMinuteEnabled,
		"akshare_minute_source_mode":       s.AkshareMinuteSourceMode,
	}

	err = db.Dao.Transaction(func(tx *gorm.DB) error {
		current, ensureErr := ensureSettingsRecord(tx)
		if ensureErr != nil {
			return ensureErr
		}
		if updateErr := tx.Model(&Settings{}).Where("id = ?", current.ID).Updates(updateMap).Error; updateErr != nil {
			return updateErr
		}
		// nil means the caller did not submit AI settings. An explicit empty
		// array means delete all AI configurations.
		if s.AiConfigs != nil {
			return updateAiConfigs(tx, s.AiConfigs)
		}
		return nil
	})
	if err != nil {
		logger.SugaredLogger.Errorf("更新配置失败: %v", err)
		return "更新配置失败: " + err.Error()
	}

	return "保存成功！"
}

func NormalizeYieldEmailRecipients(input string) (string, error) {
	addrs, err := parseEmailList(input)
	if err != nil {
		return "", err
	}
	if len(addrs) == 0 {
		return "", nil
	}
	seen := make(map[string]struct{}, len(addrs))
	result := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		key := strings.ToLower(strings.TrimSpace(addr))
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, strings.TrimSpace(addr))
	}
	return strings.Join(result, ","), nil
}

func NormalizeYieldEmailCronTimes(input string) ([]string, error) {
	replacer := strings.NewReplacer("，", ",", "；", ",", ";", ",", "\n", ",", "\t", ",", " ", "")
	raw := replacer.Replace(input)
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}

	seen := make(map[string]struct{})
	times := make([]string, 0)
	invalid := make([]string, 0)
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if !strictYieldEmailCronTimeRegexp.MatchString(item) {
			invalid = append(invalid, item)
			continue
		}
		t, err := time.Parse("15:04", item)
		if err != nil {
			invalid = append(invalid, item)
			continue
		}
		key := t.Format("15:04")
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		times = append(times, key)
	}
	if len(invalid) > 0 {
		return nil, fmt.Errorf("定时发送时间格式错误: %s（正确格式：HH:mm，多个时间请用英文逗号分隔）", strings.Join(invalid, ", "))
	}
	sort.Strings(times)
	return times, nil
}

func updateAiConfigs(tx *gorm.DB, aiConfigs []*AIConfig) error {
	if len(aiConfigs) == 0 {
		return tx.Exec("DELETE FROM ai_config").Error
	}
	for index, item := range aiConfigs {
		if item == nil {
			return fmt.Errorf("AI 配置第 %d 项为空", index+1)
		}
	}
	var ids []uint
	lo.ForEach(aiConfigs, func(item *AIConfig, index int) {
		ids = append(ids, item.ID)
	})
	var existAiConfigs []*AIConfig
	err := tx.Model(&AIConfig{}).Select("id").Where("id in (?) ", ids).Find(&existAiConfigs).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	idMap := make(map[uint]bool)
	lo.ForEach(existAiConfigs, func(item *AIConfig, index int) {
		idMap[item.ID] = true
	})
	var addAiConfigs []*AIConfig
	var notDeleteIds []uint
	var e error
	lo.ForEach(aiConfigs, func(item *AIConfig, index int) {
		if e != nil {
			return
		}
		if item.Sort <= 0 {
			item.Sort = index + 1
		}
		item.ApiProtocol = NormalizeAIAPIProtocol(item.ApiProtocol)
		if !idMap[item.ID] {
			addAiConfigs = append(addAiConfigs, item)
		} else {
			notDeleteIds = append(notDeleteIds, item.ID)
			e = tx.Model(&AIConfig{}).Where("id=?", item.ID).Updates(map[string]interface{}{
				"sort":               item.Sort,
				"name":               item.Name,
				"base_url":           item.BaseUrl,
				"api_key":            item.ApiKey,
				"model_name":         item.ModelName,
				"api_protocol":       item.ApiProtocol,
				"max_tokens":         item.MaxTokens,
				"temperature":        item.Temperature,
				"time_out":           item.TimeOut,
				"http_proxy":         item.HttpProxy,
				"http_proxy_enabled": item.HttpProxyEnabled,
			}).Error
			if e != nil {
				return
			}
		}
	})
	if e != nil {
		return e
	}
	//删除旧的配置
	if len(notDeleteIds) > 0 {
		err = tx.Exec("DELETE FROM ai_config WHERE id NOT IN ?", notDeleteIds).Error
		if err != nil {
			return err
		}
	} else {
		err = tx.Exec("DELETE FROM ai_config").Error
		if err != nil {
			return err
		}
	}
	//批量新增的配置
	if len(addAiConfigs) == 0 {
		return nil
	}
	return tx.CreateInBatches(addAiConfigs, len(addAiConfigs)).Error
}

func GetSettingConfig() *SettingConfig {
	settingConfig := &SettingConfig{}
	settings := &Settings{}
	aiConfigs := make([]*AIConfig, 0)
	if db.Dao == nil {
		applySettingDefaults(settings)
		settingConfig.Settings = settings
		settingConfig.AiConfigs = aiConfigs
		applyRuntimeOverrideFromSettings(settings)
		return settingConfig
	}
	persistedSettings, err := ensureSettingsRecord(db.Dao)
	if err != nil {
		logger.SugaredLogger.Error("查询配置失败:", err)
		applySettingDefaults(settings)
	} else {
		settings = persistedSettings
	}
	err = db.Dao.Model(&AIConfig{}).
		Order("CASE WHEN sort <= 0 THEN id ELSE sort END ASC").
		Order("id ASC").
		Find(&aiConfigs).Error
	if err != nil {
		logger.SugaredLogger.Error("查询AI配置失败:", err)
	} else if len(aiConfigs) > 0 {
		lo.ForEach(aiConfigs, func(item *AIConfig, index int) {
			if item.Sort <= 0 {
				item.Sort = index + 1
			}
			item.ApiProtocol = NormalizeAIAPIProtocol(item.ApiProtocol)
			if item.TimeOut <= 0 {
				item.TimeOut = 60 * 5
			}
		})
	}
	settingConfig.Settings = settings
	settingConfig.AiConfigs = aiConfigs
	applyRuntimeOverrideFromSettings(settings)

	return settingConfig
}

func NormalizeAIAPIProtocol(value string) string {
	return models.NormalizeAIAPIProtocol(value)
}

func applySettingDefaults(settings *Settings) {
	if settings == nil {
		return
	}
	applyPrivateMinuteSettingsFromEnv(settings)
	if settings.YieldEmailSMTPPort <= 0 {
		settings.YieldEmailSMTPPort = 465
	}
	if settings.RefreshInterval <= 0 {
		settings.RefreshInterval = 1
	}
	if settings.CrawlTimeOut <= 0 {
		settings.CrawlTimeOut = 60
	}
	if settings.KDays < 30 {
		settings.KDays = 60
	}
	settings.LocalPushEnable = false
	settings.DingPushEnable = false
	settings.DingRobot = ""
	settings.EnableDanmu = false
	settings.EnableNews = false
	settings.EnablePushNews = false
	settings.EnableOnlyPushRedNews = false
	if settings.BrowserPath == "" {
		settings.BrowserPath, _ = CheckBrowser()
	}
	if settings.BrowserPoolSize <= 0 {
		settings.BrowserPoolSize = 1
	}
	if settings.ID == 0 {
		settings.ForceNoProxyForFetch = true
	}
	if strings.TrimSpace(settings.MarketSummaryCronTimes) == "" {
		settings.MarketSummaryCronTimes = defaultMarketSummaryCronTimes
		settings.MarketSummaryCronEnabled = true
	}
	if settings.MinuteProviderMode == "" {
		settings.MinuteProviderMode = "public"
	}
	settings.MinuteProviderMode = normalizeMinuteProviderMode(settings.MinuteProviderMode)
	if settings.PrivateMinuteProxyMode == "" {
		settings.PrivateMinuteProxyMode = appconfig.DefaultDiemengProxyMode
	}
	settings.PrivateMinuteProxyMode = normalizePrivateMinuteProxyMode(settings.PrivateMinuteProxyMode)
	if settings.PrivateMinuteLevel == "" {
		settings.PrivateMinuteLevel = appconfig.DefaultDiemengLevel
	}
	settings.PrivateMinuteLevel = normalizePrivateMinuteLevel(settings.PrivateMinuteLevel)
	if settings.PrivateMinuteTimeoutSec <= 0 {
		settings.PrivateMinuteTimeoutSec = appconfig.DefaultDiemengTimeoutSec
	}
	if settings.PrivateMinuteMinInterval < 0 {
		settings.PrivateMinuteMinInterval = appconfig.DefaultDiemengMinIntervalMS
	}
	if settings.AkshareMinuteSourceMode == "" {
		settings.AkshareMinuteSourceMode = appconfig.DefaultAkshareMinuteSource
	}
	settings.AkshareMinuteSourceMode = normalizeAkshareMinuteSourceMode(settings.AkshareMinuteSourceMode)
	if settings.ID == 0 {
		settings.MinuteLongHistoryHint = true
		settings.AkshareEnabled = true
		settings.SinaMinuteEnabled = true
		settings.TencentMinuteEnabled = true
	}
	settings.EastmoneyMinuteEnabled = true
	if settings.MinuteProviderMode == "public" && !settings.AkshareEnabled && !settings.SinaMinuteEnabled && !settings.TencentMinuteEnabled {
		settings.AkshareEnabled = true
		settings.SinaMinuteEnabled = true
		settings.TencentMinuteEnabled = true
	}
}

// EnsureSettingsRecord creates the singleton settings row with all first-run
// defaults exactly once. Existing database values are never refilled from the
// environment on later reads.
func EnsureSettingsRecord() error {
	if db.Dao == nil {
		return errors.New("database is not initialized")
	}
	_, err := ensureSettingsRecord(db.Dao)
	return err
}

func ensureSettingsRecord(tx *gorm.DB) (*Settings, error) {
	settings := &Settings{}
	err := tx.Model(&Settings{}).First(settings).Error
	if err == nil {
		return settings, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	applySettingDefaults(settings)
	if err = tx.Create(settings).Error; err != nil {
		// A concurrent startup path may already have created the singleton.
		if lookupErr := tx.Model(&Settings{}).First(settings).Error; lookupErr == nil {
			return settings, nil
		}
		return nil, err
	}
	return settings, nil
}

func NormalizeMarketSummaryCronTimes(input string) ([]string, error) {
	replacer := strings.NewReplacer("，", ",", "；", ",", ";", ",", "\n", ",", "\t", ",")
	raw := replacer.Replace(strings.TrimSpace(input))
	if raw == "" {
		return nil, nil
	}

	seen := make(map[string]struct{})
	times := make([]string, 0)
	for _, item := range strings.Split(raw, ",") {
		text := strings.TrimSpace(item)
		if text == "" {
			continue
		}
		if !strictYieldEmailCronTimeRegexp.MatchString(text) {
			return nil, fmt.Errorf("市场资讯定时总结时间格式无效：%s", text)
		}
		if _, exists := seen[text]; exists {
			continue
		}
		seen[text] = struct{}{}
		times = append(times, text)
	}
	sort.Strings(times)
	if len(times) == 0 {
		return []string{}, nil
	}
	return times, nil
}

func applyPrivateMinuteSettingsFromEnv(settings *Settings) {
	if settings == nil {
		return
	}
	envBaseURL := strings.TrimSpace(os.Getenv("GO_STOCK_DIEMENG_BASE_URL"))
	envAPIKey := strings.TrimSpace(os.Getenv("GO_STOCK_DIEMENG_API_KEY"))
	if envBaseURL == "" {
		envBaseURL = appconfig.DefaultDiemengBaseURL
	}
	if settings.PrivateMinuteBaseURL == "" && envBaseURL != "" {
		settings.PrivateMinuteBaseURL = envBaseURL
	}
	if settings.PrivateMinuteAPIKey == "" && envAPIKey != "" {
		settings.PrivateMinuteAPIKey = envAPIKey
	}
	if settings.PrivateMinuteTimeoutSec <= 0 {
		settings.PrivateMinuteTimeoutSec = envIntOrDefault("GO_STOCK_DIEMENG_TIMEOUT_SEC", appconfig.DefaultDiemengTimeoutSec)
	}
	if settings.PrivateMinuteMinInterval <= 0 {
		settings.PrivateMinuteMinInterval = envIntOrDefault("GO_STOCK_DIEMENG_MIN_INTERVAL_MS", appconfig.DefaultDiemengMinIntervalMS)
	}
	if strings.TrimSpace(settings.PrivateMinuteProxyMode) == "" {
		settings.PrivateMinuteProxyMode = normalizePrivateMinuteProxyMode(strings.TrimSpace(os.Getenv("GO_STOCK_DIEMENG_PROXY_MODE")))
	}
	if strings.TrimSpace(settings.PrivateMinuteLevel) == "" {
		settings.PrivateMinuteLevel = normalizePrivateMinuteLevel(strings.TrimSpace(os.Getenv("GO_STOCK_DIEMENG_LEVEL")))
	}
	if !settings.PrivateMinuteEnabled && (strings.TrimSpace(settings.PrivateMinuteBaseURL) != "" || strings.TrimSpace(settings.PrivateMinuteAPIKey) != "") {
		settings.PrivateMinuteEnabled = true
	}
}

func envIntOrDefault(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	number, err := strconv.Atoi(value)
	if err != nil || number <= 0 {
		return fallback
	}
	return number
}

func normalizeMinuteProviderMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "private":
		return "private"
	default:
		return "public"
	}
}

func normalizePrivateMinuteProxyMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "inherit", "settings":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "disable"
	}
}

func normalizePrivateMinuteLevel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "5min", "15min", "30min", "60min":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "1min"
	}
}

func normalizeAkshareMinuteSourceMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "sina", "em":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "auto"
	}
}

func applyRuntimeOverrideFromSettings(settings *Settings) {
	if settings == nil {
		appconfig.ResetRuntimeOverride()
		return
	}
	if settings.ID == 0 {
		appconfig.ResetRuntimeOverride()
		return
	}

	override := &appconfig.RuntimeOverride{}
	switch normalizeMinuteProviderMode(settings.MinuteProviderMode) {
	case "private":
		provider := "diemeng"
		override.MinuteProvider = &provider
	default:
		provider := "public"
		override.MinuteProvider = &provider
	}

	akshareSource := normalizeAkshareMinuteSourceMode(settings.AkshareMinuteSourceMode)
	override.AkshareMinuteSource = &akshareSource

	if strings.TrimSpace(settings.PrivateMinuteAPIKey) != "" {
		key := strings.TrimSpace(settings.PrivateMinuteAPIKey)
		override.DiemengAPIKey = &key
	}
	if strings.TrimSpace(settings.PrivateMinuteBaseURL) != "" {
		baseURL := strings.TrimSpace(settings.PrivateMinuteBaseURL)
		override.DiemengBaseURL = &baseURL
	}
	if settings.PrivateMinuteTimeoutSec > 0 {
		timeout := settings.PrivateMinuteTimeoutSec
		override.DiemengTimeoutSec = &timeout
	}
	if settings.PrivateMinuteMinInterval >= 0 {
		interval := settings.PrivateMinuteMinInterval
		override.DiemengMinInterval = &interval
	}
	if settings.PrivateMinuteProxyMode != "" {
		proxyMode := normalizePrivateMinuteProxyMode(settings.PrivateMinuteProxyMode)
		override.DiemengProxyMode = &proxyMode
	}
	if settings.PrivateMinuteLevel != "" {
		level := normalizePrivateMinuteLevel(settings.PrivateMinuteLevel)
		override.DiemengLevel = &level
	}

	appconfig.SetRuntimeOverride(override)
}

// SelectPrimaryAIConfig returns the first AI config in current saved order.
func SelectPrimaryAIConfig(aiConfigs []*AIConfig) *AIConfig {
	if len(aiConfigs) >= 1 {
		return aiConfigs[0]
	}
	return nil
}

// SelectPrimaryAIConfigID returns the preferred AI config ID, or 0 if none.
func SelectPrimaryAIConfigID(setting *SettingConfig) int {
	if setting == nil {
		return 0
	}
	cfg := SelectPrimaryAIConfig(setting.AiConfigs)
	if cfg == nil {
		return 0
	}
	return int(cfg.ID)
}

// ResolveAIFallbackOrder returns the ordered AI config IDs used for failover.
// When a specific config is requested, it is tried first and the remaining
// configs are appended in current saved order without duplicates.
func ResolveAIFallbackOrder(setting *SettingConfig, requestedAIConfigID int) []int {
	if setting == nil || len(setting.AiConfigs) == 0 {
		if requestedAIConfigID > 0 {
			return []int{requestedAIConfigID}
		}
		return nil
	}

	ordered := make([]int, 0, len(setting.AiConfigs))
	seen := make(map[int]struct{}, len(setting.AiConfigs))
	appendID := func(id int) {
		if id <= 0 {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		ordered = append(ordered, id)
	}

	if requestedAIConfigID > 0 {
		appendID(requestedAIConfigID)
	}
	appendID(SelectPrimaryAIConfigID(setting))
	for _, cfg := range setting.AiConfigs {
		if cfg == nil {
			continue
		}
		appendID(int(cfg.ID))
	}
	return ordered
}
