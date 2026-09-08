package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"go-stock/backend/data"
	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	"go-stock/backend/research2"
	"go-stock/backend/researchconfig"
)

func (a *App) researchSettingsStore() *researchconfig.Store {
	if a.researchConfigStore != nil {
		return a.researchConfigStore
	}
	return researchconfig.New(db.Dao)
}

func (a *App) researchConfiguration(ctx context.Context, center string) (researchconfig.Snapshot, error) {
	return a.researchSettingsStore().Load(ctx, center)
}

func (a *App) loadResearch2Settings() *models.SettingConfig {
	snapshot, err := a.researchConfiguration(a.taskContext(), researchconfig.Research2)
	if err != nil {
		logger.SugaredLogger.Errorf("读取研究中心2配置失败: %v", err)
		return nil
	}
	return snapshot.Settings
}

// The runtime owns only execution parameters. Email and enabled switches are
// read at their own task boundaries and must not discard active collectors.
func sameResearchRuntimeConfig(left, right *models.SettingConfig) bool {
	trim := func(source *models.SettingConfig) *models.SettingConfig {
		copy := researchconfig.Clone(source)
		if copy == nil || copy.Settings == nil {
			return copy
		}
		copy.AICapitalDeploymentEnabled, copy.Research2AutoEnabled = false, false
		copy.AIAnalysisAutoEnabled, copy.LegacyAIAnalysisEnable = nil, nil
		copy.Research2EmailEnabled = false
		copy.Research2EmailTo, copy.Research2EmailFrom = "", ""
		copy.Research2EmailSMTPHost, copy.Research2EmailSMTPUser, copy.Research2EmailSMTPPass = "", "", ""
		copy.Research2EmailSMTPPort = 0
		for _, model := range copy.AiConfigs {
			if model != nil {
				model.CreatedAt, model.UpdatedAt = time.Time{}, time.Time{}
			}
		}
		return copy
	}
	return reflect.DeepEqual(trim(left), trim(right))
}

func normalizeResearchSettings(center string, cfg *models.SettingConfig) error {
	if cfg == nil || cfg.Settings == nil {
		return researchconfig.ErrInvalidConfig
	}
	order, err := data.NormalizeMinuteProviderOrder(cfg.MinuteProviderOrder, cfg.MinuteProviderMode)
	if err != nil {
		return fmt.Errorf("%w: %v", researchconfig.ErrInvalidConfig, err)
	}
	cfg.MinuteProviderOrder = order
	cfg.Settings.MinuteProviderOrder = strings.Join(order, ",")
	cfg.PrivateMinuteBaseURL = strings.TrimSpace(cfg.PrivateMinuteBaseURL)
	cfg.PrivateMinuteAPIKey = strings.TrimSpace(cfg.PrivateMinuteAPIKey)
	if cfg.PrivateMinuteEnabled && (cfg.PrivateMinuteBaseURL == "" || cfg.PrivateMinuteAPIKey == "") {
		return fmt.Errorf("%w: 已启用的私人分钟来源需要 URL 和 API Key", researchconfig.ErrInvalidConfig)
	}
	if !cfg.AkshareEnabled && !cfg.SinaMinuteEnabled && !cfg.TencentMinuteEnabled && !(cfg.PrivateMinuteEnabled && cfg.PrivateMinuteLevel == "1min") {
		return fmt.Errorf("%w: 至少启用一个分钟图来源", researchconfig.ErrInvalidConfig)
	}
	if cfg.CrawlTimeOut <= 0 {
		cfg.CrawlTimeOut = 60
	}
	if cfg.KDays < 30 {
		cfg.KDays = 60
	}
	if cfg.PrivateMinuteTimeoutSec <= 0 {
		cfg.PrivateMinuteTimeoutSec = 60
	}
	if cfg.PrivateMinuteMinInterval < 0 {
		return fmt.Errorf("%w: 分钟来源间隔不能为负数", researchconfig.ErrInvalidConfig)
	}
	if center == researchconfig.Research1 {
		cfg.AITargetCapitalUtilization, cfg.AIMaxImmediateBuysPerRun, cfg.AIReanalysisIntervalMinutes, err = data.NormalizeAICapitalDeploymentSettings(cfg.AITargetCapitalUtilization, cfg.AIMaxImmediateBuysPerRun, cfg.AIReanalysisIntervalMinutes)
		if err == nil {
			cfg.AIReviewStartTime, cfg.AIReviewIntervalMinutes, err = data.NormalizeAIReviewSchedule(cfg.AIReviewStartTime, cfg.AIReviewIntervalMinutes)
		}
	} else if center == researchconfig.Research2 && cfg.Research2EmailEnabled {
		_, _, err = research2.ValidateEmailConfig(research2EmailConfig(cfg))
	}
	if err != nil {
		return fmt.Errorf("%w: %v", researchconfig.ErrInvalidConfig, err)
	}
	for _, model := range cfg.AiConfigs {
		if model == nil {
			return fmt.Errorf("%w: empty model", researchconfig.ErrInvalidConfig)
		}
		if model.TimeOut <= 0 {
			model.TimeOut = 300
		}
		model.ApiProtocol = models.NormalizeAIAPIProtocol(model.ApiProtocol)
	}
	return nil
}

func (a *App) saveResearchConfiguration(ctx context.Context, center string, revision int64, cfg *models.SettingConfig) (researchconfig.Snapshot, error) {
	if center == researchconfig.Research2 {
		a.research2ConfigMu.Lock()
		defer a.research2ConfigMu.Unlock()
	} else if center == researchconfig.Research1 {
		a.research1ConfigMu.Lock()
		defer a.research1ConfigMu.Unlock()
	}
	if err := normalizeResearchSettings(center, cfg); err != nil {
		return researchconfig.Snapshot{}, err
	}
	before, err := a.researchConfiguration(ctx, center)
	if err != nil {
		return researchconfig.Snapshot{}, err
	}
	saved, err := a.researchSettingsStore().Save(ctx, center, revision, cfg)
	if err != nil {
		return researchconfig.Snapshot{}, err
	}
	// Parameters are picked up at the next task entry. Only actual switch/email
	// changes need scheduler work; neither path performs interrupted-run recovery.
	if center == researchconfig.Research1 && !saved.Settings.AICapitalDeploymentEnabled {
		a.aiAnalysisRunMu.Lock()
		if a.activeAnalysisBuyPermit != nil {
			a.activeAnalysisBuyPermit.Disable()
		}
		a.aiAnalysisRunMu.Unlock()
	}
	if a.cron != nil && center == researchconfig.Research2 {
		if before.Settings.Research2AutoEnabled != saved.Settings.Research2AutoEnabled {
			a.reloadResearch2CronLocked(saved.Settings)
		} else if research2EmailConfig(before.Settings) != research2EmailConfig(saved.Settings) {
			a.reloadResearch2EmailCron(saved.Settings)
		}
	}
	return saved, nil
}

func researchModel(snapshot researchconfig.Snapshot, id int) (*models.AIConfig, error) {
	for _, model := range snapshot.Settings.AiConfigs {
		if model != nil && int(model.ID) == id && !model.Disabled {
			copy := *model
			return &copy, nil
		}
	}
	return nil, errors.Join(researchconfig.ErrModelOwnership, errors.New("模型不可用或不属于当前研究中心"))
}
