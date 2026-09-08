package migrations

import (
	"context"
	"reflect"
	"testing"

	"go-stock/backend/models"
	"go-stock/backend/researchconfig"
)

func TestSchema27CopiesOwnedConfigurationOnceAndPreservesHistory(t *testing.T) {
	db := openMigrationTestDB(t)
	if err := db.AutoMigrate(&models.Settings{}, &models.AIConfig{}); err != nil {
		t.Fatal(err)
	}
	// Exercise actual additions against the previous AI table shape.
	if err := db.Migrator().DropIndex(&models.AIConfig{}, "idx_ai_config_owner_archived"); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"Owner", "ArchivedAt"} {
		if err := db.Migrator().DropColumn(&models.AIConfig{}, field); err != nil {
			t.Fatal(err)
		}
	}
	settings := models.Settings{TushareToken: "old-token", Research2EmailSMTPPass: "old-mail-secret", AITargetCapitalUtilization: .7}
	if err := db.Create(&settings).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO ai_config(id, sort, disabled, name, api_key) VALUES(40, 2, 1, 'fallback', 'preserved-secret'),(50, 1, 0, 'primary', 'primary-key')").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE historical_model_reference(id INTEGER PRIMARY KEY, ai_config_id INTEGER, content TEXT); INSERT INTO historical_model_reference VALUES(1,40,'unchanged')").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(applyResearchSettingsSchema); err != nil {
		t.Fatal(err)
	}
	store := researchconfig.New(db)
	r1, err := store.Load(context.Background(), researchconfig.Research1)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := store.Load(context.Background(), researchconfig.Research2)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Settings.TushareToken != "old-token" || r2.Settings.TushareToken != "old-token" || r1.Settings.Research2EmailSMTPPass != "" || r2.Settings.Research2EmailSMTPPass != "old-mail-secret" {
		t.Fatal("incorrect ownership projection")
	}
	for _, snapshot := range []researchconfig.Snapshot{r1, r2} {
		if snapshot.Revision != 1 || len(snapshot.Settings.AiConfigs) != 2 || snapshot.Settings.AiConfigs[0].Name != "primary" || snapshot.Settings.AiConfigs[1].Disabled != true {
			t.Fatal("copied model order/enablement changed")
		}
		for _, model := range snapshot.Settings.AiConfigs {
			if model.ID <= 50 {
				t.Fatal("copy reused historical model ID")
			}
		}
	}
	if r1.Settings.AiConfigs[0].ID == r2.Settings.AiConfigs[0].ID {
		t.Fatal("centers share AI model ID")
	}
	r1.Settings.TushareToken = "changed-independently"
	if _, err := store.Save(t.Context(), researchconfig.Research1, 1, r1.Settings); err != nil {
		t.Fatal(err)
	}
	var before []models.AIConfig
	if err := db.Order("id").Find(&before).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(applyResearchSettingsSchema); err != nil {
		t.Fatal(err)
	}
	var after []models.AIConfig
	if err := db.Order("id").Find(&after).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("migration repeat changed model history")
	}
	r1, _ = store.Load(t.Context(), researchconfig.Research1)
	if r1.Settings.TushareToken != "changed-independently" || r1.Revision != 2 {
		t.Fatal("migration repeat overwrote center configuration")
	}
	var historical string
	if err := db.Raw("SELECT h.content || ':' || a.api_key FROM historical_model_reference h JOIN ai_config a ON h.ai_config_id=a.id").Scan(&historical).Error; err != nil {
		t.Fatal(err)
	}
	if historical != "unchanged:preserved-secret" {
		t.Fatal("historical ID/reference changed")
	}
}

func TestSchema27FreshInstallUsesFrozenDefaults(t *testing.T) {
	db := openMigrationTestDB(t)
	if err := MigrateMain(db); err != nil {
		t.Fatal(err)
	}
	r1, err := researchconfig.New(db).Load(t.Context(), researchconfig.Research1)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Settings.AITargetCapitalUtilization != .9 || r1.Settings.PrivateMinuteTimeoutSec != 60 || r1.Settings.TushareToken != "" || len(r1.Settings.AiConfigs) != 0 {
		t.Fatal("unexpected first-install defaults")
	}
	if err := MigrateMain(db); err != nil {
		t.Fatal(err)
	}
	if _, err := StatusMain(db); err != nil {
		t.Fatal(err)
	}
}
