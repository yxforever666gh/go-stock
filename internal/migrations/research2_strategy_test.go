package migrations

import (
	"testing"

	"go-stock/backend/models"
	"go-stock/backend/research2"
)

func TestSchema13CreatesIsolatedResearch2AccountAndSwitch(t *testing.T) {
	database := openMigrationTestDB(t)
	if err := database.AutoMigrate(&models.Settings{}); err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&models.Settings{Research2AutoEnabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := applyResearch2OvernightStrengthStrategy(database); err != nil {
		t.Fatal(err)
	}
	if err := verifyMainSchema13Runtime(database); err != nil {
		t.Fatal(err)
	}
	var account research2.Account
	if err := database.First(&account, 1).Error; err != nil {
		t.Fatal(err)
	}
	if account.InitialCash != 12000 || account.Cash != 12000 {
		t.Fatalf("account=%+v", account)
	}
	var setting models.Settings
	if err := database.First(&setting).Error; err != nil {
		t.Fatal(err)
	}
	if !setting.Research2AutoEnabled {
		t.Fatal("research2 automatic strategy must default to enabled")
	}
}
