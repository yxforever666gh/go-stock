package migrations

import (
	"testing"
	"time"

	"go-stock/backend/models"
	"go-stock/backend/research2"
	"go-stock/backend/researchconfig"
)

func TestSchema32ResetsSelectionAndCancelsOnlyUnsentLegacyEmail(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := database.AutoMigrate(&models.Settings{}, &researchconfig.Record{}, &research2.EmailDelivery{}); err != nil {
		t.Fatal(err)
	}
	settings := models.Settings{
		Research2EmailEnabled: true, Research2EmailSlotsJSON: `["10:00"]`,
		Research2EmailTo: "kept@example.com", Research2EmailFrom: "sender@example.com",
		Research2EmailSMTPHost: "smtp.example.com", Research2EmailSMTPPort: 465,
		Research2EmailSMTPUser: "sender@example.com", Research2EmailSMTPPass: "keep-secret",
	}
	if err := database.Create(&settings).Error; err != nil {
		t.Fatal(err)
	}
	config := &models.SettingConfig{Settings: &settings, Research2EmailSlots: []string{"10:00", "09:30"}}
	raw, err := researchconfig.ConfigJSON(researchconfig.Research2, config)
	if err != nil {
		t.Fatal(err)
	}
	if err = database.Create(&researchconfig.Record{Center: researchconfig.Research2, ConfigJSON: string(raw), Revision: 7}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 22, 9, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*3600))
	statuses := []string{research2.EmailStatusPending, research2.EmailStatusRetryWait, research2.EmailStatusSending, research2.EmailStatusSent, research2.EmailStatusFailed}
	for index, status := range statuses {
		delivery := research2.EmailDelivery{
			AnalysisRunID: status, Status: status, Recipients: "recipient@example.com", Sender: "sender@example.com",
			Subject: status, Body: status, MessageID: "<" + status + "@example.com>", NextAttemptAt: &now,
		}
		if index == 3 {
			delivery.SentAt = &now
		}
		if err := database.Create(&delivery).Error; err != nil {
			t.Fatal(err)
		}
	}

	if err := database.Transaction(applyResearch2SlotReportEmail); err != nil {
		t.Fatal(err)
	}
	if err := database.Transaction(applyResearch2SlotReportEmail); err != nil {
		t.Fatal(err)
	}
	if err := verifyMainSchema32Runtime(database); err != nil {
		t.Fatal(err)
	}
	var stored models.Settings
	if err := database.First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Research2EmailEnabled || stored.Research2EmailSlotsJSON != "[]" || stored.Research2EmailTo != settings.Research2EmailTo || stored.Research2EmailSMTPPass != settings.Research2EmailSMTPPass {
		t.Fatalf("settings=%+v", stored)
	}
	var record researchconfig.Record
	if err := database.Where("center = ?", researchconfig.Research2).First(&record).Error; err != nil {
		t.Fatal(err)
	}
	decoded, err := researchconfig.DecodeConfig(researchconfig.Research2, []byte(record.ConfigJSON))
	if err != nil {
		t.Fatal(err)
	}
	if record.Revision != 8 || decoded.Research2EmailEnabled || len(decoded.Research2EmailSlots) != 0 || decoded.Research2EmailSMTPPass != "keep-secret" {
		t.Fatalf("record=%+v config=%+v", record, decoded)
	}
	var deliveries []research2.EmailDelivery
	if err := database.Order("id ASC").Find(&deliveries).Error; err != nil {
		t.Fatal(err)
	}
	for index, delivery := range deliveries {
		if index < 3 {
			if delivery.Status != research2.EmailStatusCancelled || delivery.NextAttemptAt != nil {
				t.Fatalf("active delivery was not cancelled: %+v", delivery)
			}
			continue
		}
		if delivery.Status != statuses[index] {
			t.Fatalf("terminal delivery changed: %+v", delivery)
		}
	}
}
