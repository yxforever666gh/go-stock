package migrations

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"go-stock/backend/models"
	"go-stock/backend/researchconfig"
	"gorm.io/gorm"
)

func mainMigrationV27Definition() string {
	return strings.Join([]string{
		"research_settings(center primary key, config_json text not null, revision integer not null > 0)",
		"center constrained to research1/research2; owned settings projected once at revision 1",
		"ai_config.owner text not null default global; archived_at nullable datetime; owner/archived index",
		"copy active global AI models to each uninitialized center with fresh IDs preserving sort and disabled",
		"preserve global config, historical IDs and all research/account/audit records",
	}, "\n")
}

func applyResearchSettingsSchema(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	for _, column := range []string{"Owner", "ArchivedAt"} {
		if !tx.Migrator().HasColumn(&models.AIConfig{}, column) {
			if err := tx.Migrator().AddColumn(&models.AIConfig{}, column); err != nil {
				return err
			}
		}
	}
	if err := tx.Exec("CREATE INDEX IF NOT EXISTS idx_ai_config_owner_archived ON ai_config(owner, archived_at)").Error; err != nil {
		return err
	}
	if err := tx.AutoMigrate(&researchconfig.Record{}); err != nil {
		return err
	}

	// Fixed schema-27 first-install defaults. Do not read environment overrides or
	// current application defaults while upgrading a historical database.
	settings := models.Settings{
		CrawlTimeOut: 60, KDays: 60, BrowserPoolSize: 1, ForceNoProxyForFetch: true,
		AICapitalDeploymentEnabled: true, AITargetCapitalUtilization: 0.9,
		AIMaxImmediateBuysPerRun: 2, AIReanalysisIntervalMinutes: 30,
		AIReviewStartTime: "09:50", AIReviewIntervalMinutes: 15, Research2AutoEnabled: true,
		MinuteProviderMode: "public", MinuteProviderOrder: "tencent,sina,akshare,private",
		MinuteLongHistoryHint: true, AkshareEnabled: true, SinaMinuteEnabled: true,
		TencentMinuteEnabled: true, EastmoneyMinuteEnabled: true,
		PrivateMinuteTimeoutSec: 60, PrivateMinuteMinInterval: 1200,
		PrivateMinuteProxyMode: "disable", PrivateMinuteLevel: "1min", AkshareMinuteSourceMode: "auto",
	}
	var persisted models.Settings
	if err := tx.Order("id ASC").First(&persisted).Error; err == nil {
		settings = persisted
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	cfg := &models.SettingConfig{Settings: &settings}
	var originals []models.AIConfig
	if err := researchconfig.ActiveModels(tx, researchconfig.Global).Order("id ASC").Find(&originals).Error; err != nil {
		return err
	}
	for _, center := range []string{researchconfig.Research1, researchconfig.Research2} {
		var count int64
		if err := tx.Model(&researchconfig.Record{}).Where("center = ?", center).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			continue
		}
		raw, err := researchconfig.ConfigJSON(center, cfg)
		if err != nil {
			return err
		}
		record := researchconfig.Record{Center: center, ConfigJSON: string(raw), Revision: 1}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		for _, original := range originals {
			model := original
			model.ID, model.Owner = 0, center
			model.CreatedAt, model.UpdatedAt = time.Now().UTC(), time.Now().UTC()
			if err := tx.Create(&model).Error; err != nil {
				return err
			}
		}
	}
	return verifyMainSchema27Runtime(tx)
}

func verifyMainSchema27Runtime(database *gorm.DB) error {
	if database == nil {
		return errors.New("main database is unavailable")
	}
	if !database.Migrator().HasTable(&researchconfig.Record{}) {
		return errors.New("main schema 27 missing research_settings")
	}
	for _, column := range []string{"Owner", "ArchivedAt"} {
		if !database.Migrator().HasColumn(&models.AIConfig{}, column) {
			return fmt.Errorf("main schema 27 missing ai_config.%s", column)
		}
	}
	if !database.Migrator().HasIndex(&models.AIConfig{}, "idx_ai_config_owner_archived") {
		return errors.New("main schema 27 missing model owner index")
	}
	var records []researchconfig.Record
	if err := database.Find(&records).Error; err != nil {
		return err
	}
	if len(records) != 2 {
		return fmt.Errorf("main schema 27 requires two research settings, got %d", len(records))
	}
	for _, record := range records {
		if record.Revision <= 0 {
			return errors.New("main schema 27 invalid revision")
		}
		if _, err := researchconfig.DecodeConfig(record.Center, []byte(record.ConfigJSON)); err != nil {
			return fmt.Errorf("main schema 27 invalid configuration for %s: %w", record.Center, err)
		}
	}
	var invalid int64
	if err := database.Model(&models.AIConfig{}).Where("owner NOT IN ? OR owner IS NULL", []string{researchconfig.Global, researchconfig.Research1, researchconfig.Research2}).Count(&invalid).Error; err != nil {
		return err
	}
	if invalid != 0 {
		return errors.New("main schema 27 invalid AI model owner")
	}
	return nil
}
