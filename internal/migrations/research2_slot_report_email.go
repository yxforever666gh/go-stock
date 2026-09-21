package migrations

import (
	"encoding/json"
	"errors"
	"fmt"

	"go-stock/backend/models"
	"go-stock/backend/research2"
	"go-stock/backend/researchconfig"

	"gorm.io/gorm"
)

func mainMigrationV32Definition() string {
	return "settings.research2_email_slots TEXT NOT NULL DEFAULT '[]'; migration clears slot selections, disables automatic Research Center 2 email, preserves SMTP credentials, and cancels unsent legacy aggregate deliveries"
}

func applyResearch2SlotReportEmail(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	if err := tx.AutoMigrate(&models.Settings{}); err != nil {
		return fmt.Errorf("add research2 email slot selection: %w", err)
	}
	if err := tx.Model(&models.Settings{}).
		Where("research2_email_enabled = 1 OR COALESCE(research2_email_slots, '') <> '[]'").
		Updates(map[string]any{
			"research2_email_enabled": false,
			"research2_email_slots":   "[]",
		}).Error; err != nil {
		return fmt.Errorf("initialize research2 email slot selection: %w", err)
	}
	var record researchconfig.Record
	if err := tx.Where("center = ?", researchconfig.Research2).First(&record).Error; err != nil {
		return fmt.Errorf("load research2 settings for email migration: %w", err)
	}
	config, err := researchconfig.DecodeConfig(researchconfig.Research2, []byte(record.ConfigJSON))
	if err != nil {
		return fmt.Errorf("decode research2 settings for email migration: %w", err)
	}
	wasEnabled := config.Research2EmailEnabled
	config.Research2EmailEnabled = false
	config.Research2EmailSlots = []string{}
	config.Research2EmailSlotsJSON = "[]"
	raw, err := researchconfig.ConfigJSON(researchconfig.Research2, config)
	if err != nil {
		return fmt.Errorf("encode research2 settings for email migration: %w", err)
	}
	var existing map[string]json.RawMessage
	_ = json.Unmarshal([]byte(record.ConfigJSON), &existing)
	_, hasSlotSelection := existing["research2EmailSlots"]
	if recordConfigNeedsEmailReset := wasEnabled || !hasSlotSelection || string(existing["research2EmailSlots"]) != "[]"; recordConfigNeedsEmailReset {
		if err := tx.Model(&researchconfig.Record{}).Where("center = ?", researchconfig.Research2).
			Updates(map[string]any{"config_json": string(raw), "revision": record.Revision + 1}).Error; err != nil {
			return fmt.Errorf("reset research2 email selection: %w", err)
		}
	}
	if tx.Migrator().HasTable(&research2.EmailDelivery{}) {
		if err := tx.Model(&research2.EmailDelivery{}).
			Where("status IN ?", []string{research2.EmailStatusPending, research2.EmailStatusRetryWait, research2.EmailStatusSending}).
			Updates(map[string]any{
				"status":          research2.EmailStatusCancelled,
				"next_attempt_at": nil,
				"last_error":      "升级为按时间段即时发送，旧汇总邮件已取消",
			}).Error; err != nil {
			return fmt.Errorf("cancel legacy research2 email deliveries: %w", err)
		}
	}
	return verifyMainSchema32Runtime(tx)
}

func verifyMainSchema32Runtime(database *gorm.DB) error {
	if database == nil {
		return errors.New("main database is unavailable")
	}
	if !database.Migrator().HasColumn(&models.Settings{}, "research2_email_slots") {
		return errors.New("main schema 32 missing settings.research2_email_slots")
	}
	var settings []models.Settings
	if err := database.Find(&settings).Error; err != nil {
		return err
	}
	for _, setting := range settings {
		slots, err := research2.ParseEmailSlots(setting.Research2EmailSlotsJSON)
		if err != nil {
			return fmt.Errorf("main schema 32 has invalid research2 email slots: %w", err)
		}
		encoded, _, err := research2.MarshalEmailSlots(slots)
		if err != nil || encoded != setting.Research2EmailSlotsJSON {
			return errors.New("main schema 32 has non-canonical global research2 email slots")
		}
		if setting.Research2EmailEnabled && len(slots) == 0 {
			return errors.New("main schema 32 has enabled research2 email without a selected slot")
		}
	}
	var record researchconfig.Record
	if err := database.Where("center = ?", researchconfig.Research2).First(&record).Error; err != nil {
		return err
	}
	config, err := researchconfig.DecodeConfig(researchconfig.Research2, []byte(record.ConfigJSON))
	if err != nil {
		return fmt.Errorf("main schema 32 has invalid research2 settings: %w", err)
	}
	slots, err := research2.NormalizeEmailSlots(config.Research2EmailSlots)
	if err != nil {
		return fmt.Errorf("main schema 32 has invalid research2 email slots: %w", err)
	}
	if config.Research2EmailEnabled && len(slots) == 0 {
		return errors.New("main schema 32 has enabled research2 email without a selected slot")
	}
	if len(slots) != len(config.Research2EmailSlots) {
		return errors.New("main schema 32 has non-canonical research2 email slots")
	}
	for index := range slots {
		if slots[index] != config.Research2EmailSlots[index] {
			return errors.New("main schema 32 has non-canonical research2 email slots")
		}
	}
	return nil
}
