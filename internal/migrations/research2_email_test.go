package migrations

import (
	"testing"

	"go-stock/backend/models"
	"go-stock/backend/research2"
)

func TestSchema14CopiesCompleteLegacySMTPAndEnablesResearch2Email(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := applyFrozenMainSchemaV2(database); err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO settings (id, created_at, updated_at, yield_email_to, yield_email_from, yield_email_smtp_host, yield_email_smtp_port, yield_email_smtp_username, yield_email_smtp_password)
		VALUES (1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'recipient@example.com', 'sender@example.com', 'smtp.example.com', 465, 'sender@example.com', 'auth-code')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := applyResearch2ReportEmail(database); err != nil {
		t.Fatal(err)
	}
	var setting models.Settings
	if err := database.First(&setting, 1).Error; err != nil {
		t.Fatal(err)
	}
	if !setting.Research2EmailEnabled || setting.Research2EmailTo != "recipient@example.com" || setting.Research2EmailSMTPHost != "smtp.example.com" || setting.Research2EmailSMTPPass != "auth-code" {
		t.Fatalf("research2 email migration did not copy complete legacy settings: %+v", setting)
	}
	if !database.Migrator().HasTable(&research2.EmailDelivery{}) {
		t.Fatal("research2 email delivery table is missing")
	}
}

func TestSchema14LeavesIncompleteLegacySMTPDisabled(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := applyFrozenMainSchemaV2(database); err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO settings (id, created_at, updated_at, yield_email_to, yield_email_smtp_host, yield_email_smtp_port)
		VALUES (1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'recipient@example.com', 'smtp.example.com', 465)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := applyResearch2ReportEmail(database); err != nil {
		t.Fatal(err)
	}
	var setting models.Settings
	if err := database.First(&setting, 1).Error; err != nil {
		t.Fatal(err)
	}
	if setting.Research2EmailEnabled {
		t.Fatal("incomplete legacy SMTP settings must not enable Research Center 2 email")
	}
}

func TestSchema14InfersLegacySMTPHostAndUsernameFromSender(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := applyFrozenMainSchemaV2(database); err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO settings (id, created_at, updated_at, yield_email_to, yield_email_from, yield_email_smtp_password)
		VALUES (1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'recipient@example.com', 'sender@qq.com', 'auth-code')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := applyResearch2ReportEmail(database); err != nil {
		t.Fatal(err)
	}
	var setting models.Settings
	if err := database.First(&setting, 1).Error; err != nil {
		t.Fatal(err)
	}
	if !setting.Research2EmailEnabled || setting.Research2EmailSMTPHost != "smtp.qq.com" || setting.Research2EmailSMTPPort != 465 || setting.Research2EmailSMTPUser != "sender@qq.com" {
		t.Fatalf("legacy SMTP transport inference failed: %+v", setting)
	}
}
