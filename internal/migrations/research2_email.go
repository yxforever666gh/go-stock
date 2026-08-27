package migrations

import (
	"errors"
	"fmt"

	"go-stock/backend/models"
	"go-stock/backend/research2"

	"gorm.io/gorm"
)

func applyResearch2ReportEmail(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	if err := tx.AutoMigrate(&models.Settings{}, &research2.EmailDelivery{}); err != nil {
		return fmt.Errorf("create research2 email schema: %w", err)
	}
	// The legacy fields remain an inert compatibility source. Migration 14 copies
	// them once into the Research Center 2 namespace and preserves the old mailer's
	// documented host, port and username defaults before checking completeness.
	if err := tx.Exec(`UPDATE settings SET
		research2_email_to = COALESCE(yield_email_to, ''),
		research2_email_from = COALESCE(yield_email_from, ''),
		research2_email_smtp_host = COALESCE(yield_email_smtp_host, ''),
		research2_email_smtp_port = COALESCE(yield_email_smtp_port, 0),
		research2_email_smtp_user = COALESCE(yield_email_smtp_username, ''),
		research2_email_smtp_pass = COALESCE(yield_email_smtp_password, '')`).Error; err != nil {
		return fmt.Errorf("copy legacy SMTP settings: %w", err)
	}
	if err := tx.Exec(`UPDATE settings SET
		research2_email_smtp_user = CASE WHEN TRIM(COALESCE(research2_email_smtp_user, '')) = '' THEN TRIM(COALESCE(research2_email_from, '')) ELSE research2_email_smtp_user END,
		research2_email_smtp_host = CASE WHEN TRIM(COALESCE(research2_email_smtp_host, '')) <> '' THEN research2_email_smtp_host
			WHEN LOWER(research2_email_from) LIKE '%@qq.com' OR LOWER(research2_email_from) LIKE '%@foxmail.com' THEN 'smtp.qq.com'
			WHEN LOWER(research2_email_from) LIKE '%@163.com' THEN 'smtp.163.com'
			WHEN LOWER(research2_email_from) LIKE '%@126.com' THEN 'smtp.126.com'
			WHEN LOWER(research2_email_from) LIKE '%@yeah.net' THEN 'smtp.yeah.net'
			WHEN LOWER(research2_email_from) LIKE '%@gmail.com' THEN 'smtp.gmail.com'
			WHEN LOWER(research2_email_from) LIKE '%@outlook.com' OR LOWER(research2_email_from) LIKE '%@hotmail.com' OR LOWER(research2_email_from) LIKE '%@live.com' THEN 'smtp-mail.outlook.com'
			WHEN INSTR(research2_email_from, '@') > 1 THEN 'smtp.' || SUBSTR(LOWER(research2_email_from), INSTR(research2_email_from, '@') + 1)
			ELSE '' END`).Error; err != nil {
		return fmt.Errorf("infer legacy SMTP transport: %w", err)
	}
	if err := tx.Exec(`UPDATE settings SET research2_email_smtp_port = CASE
		WHEN COALESCE(research2_email_smtp_port, 0) BETWEEN 1 AND 65535 THEN research2_email_smtp_port
		WHEN LOWER(TRIM(COALESCE(research2_email_smtp_host, ''))) = 'smtp-mail.outlook.com' THEN 587
		WHEN TRIM(COALESCE(research2_email_smtp_host, '')) <> '' THEN 465
		ELSE 0 END`).Error; err != nil {
		return fmt.Errorf("infer legacy SMTP port: %w", err)
	}
	if err := tx.Exec(`UPDATE settings SET research2_email_enabled = CASE
		WHEN TRIM(COALESCE(research2_email_to, '')) <> ''
		 AND TRIM(COALESCE(research2_email_smtp_host, '')) <> ''
		 AND COALESCE(research2_email_smtp_port, 0) BETWEEN 1 AND 65535
		 AND TRIM(COALESCE(research2_email_smtp_user, '')) <> ''
		 AND TRIM(COALESCE(research2_email_smtp_pass, '')) <> '' THEN 1
		ELSE 0 END`).Error; err != nil {
		return fmt.Errorf("enable complete research2 SMTP settings: %w", err)
	}
	return verifyMainSchema14Runtime(tx)
}

func verifyMainSchema14Runtime(database *gorm.DB) error {
	if database == nil {
		return errors.New("main database is unavailable")
	}
	if !database.Migrator().HasTable(&research2.EmailDelivery{}) {
		return errors.New("main schema 14 missing research2_email_deliveries")
	}
	for _, column := range []string{
		"research2_email_enabled", "research2_email_to", "research2_email_from",
		"research2_email_smtp_host", "research2_email_smtp_port",
		"research2_email_smtp_user", "research2_email_smtp_pass",
	} {
		if !database.Migrator().HasColumn(&models.Settings{}, column) {
			return fmt.Errorf("main schema 14 missing settings.%s", column)
		}
	}
	var invalid int64
	if err := database.Raw(`SELECT COUNT(*) FROM settings WHERE research2_email_enabled = 1 AND (
		TRIM(COALESCE(research2_email_to, '')) = '' OR
		TRIM(COALESCE(research2_email_smtp_host, '')) = '' OR
		COALESCE(research2_email_smtp_port, 0) NOT BETWEEN 1 AND 65535 OR
		TRIM(COALESCE(research2_email_smtp_user, '')) = '' OR
		TRIM(COALESCE(research2_email_smtp_pass, '')) = '')`).Scan(&invalid).Error; err != nil {
		return err
	}
	if invalid != 0 {
		return fmt.Errorf("main schema 14 has %d enabled settings rows with incomplete SMTP configuration", invalid)
	}
	return nil
}
