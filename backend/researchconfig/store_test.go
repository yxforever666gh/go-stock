package researchconfig

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"go-stock/backend/models"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func fixtureStore(t *testing.T) (*Store, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "settings.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&Record{}, &models.AIConfig{}); err != nil {
		t.Fatal(err)
	}
	for _, center := range []string{Research1, Research2} {
		cfg := &models.SettingConfig{Settings: &models.Settings{TushareToken: center, AICapitalDeploymentEnabled: true}, MinuteProviderOrder: []string{"tencent", "sina"}}
		raw, err := ConfigJSON(center, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&Record{Center: center, Revision: 1, ConfigJSON: string(raw)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, owner := range []string{Global, Research1, Research2} {
		if err := db.Create(&models.AIConfig{Owner: owner, Name: owner, ApiKey: owner + "-key", Sort: 1}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return New(db), db
}

func TestIndependentSaveAndRevisionConflict(t *testing.T) {
	store, _ := fixtureStore(t)
	ctx := t.Context()
	r1, err := store.Load(ctx, Research1)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := store.Load(ctx, Research2)
	if err != nil {
		t.Fatal(err)
	}
	r1.Settings.TushareToken = "r1-new-token"
	r1.Settings.AiConfigs[0].ApiKey = "r1-new-key"
	updated, err := store.Save(ctx, Research1, r1.Revision, r1.Settings)
	if err != nil || updated.Revision != 2 {
		t.Fatalf("save revision=%d err=%v", updated.Revision, err)
	}
	if _, err := store.Save(ctx, Research1, r1.Revision, r1.Settings); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale save: %v", err)
	}
	r2.Settings.Research2AutoEnabled = false
	if _, err := store.Save(ctx, Research2, r2.Revision, r2.Settings); err != nil {
		t.Fatal(err)
	}
	r2, err = store.Load(ctx, Research2)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Settings.TushareToken != Research2 || r2.Settings.AiConfigs[0].ApiKey != Research2+"-key" {
		t.Fatal("Research 1 save changed Research 2")
	}
	if r1.Settings.AiConfigs[0].UpdatedAt != updated.Settings.AiConfigs[0].UpdatedAt && r1.Settings.AiConfigs[0].Sort != 1 {
		t.Fatal("save mutated input")
	}
}

func TestModelOwnershipRollbackAndArchive(t *testing.T) {
	store, db := fixtureStore(t)
	ctx := t.Context()
	r1, _ := store.Load(ctx, Research1)
	r2, _ := store.Load(ctx, Research2)
	original := Clone(r1.Settings)
	r1.Settings.TushareToken = "must-rollback"
	r1.Settings.AiConfigs = r2.Settings.AiConfigs
	if _, err := store.Save(ctx, Research1, 1, r1.Settings); !errors.Is(err, ErrModelOwnership) {
		t.Fatalf("cross-scope save: %v", err)
	}
	r1, _ = store.Load(ctx, Research1)
	if r1.Revision != 1 || r1.Settings.TushareToken != Research1 {
		t.Fatal("failed model save partially persisted config")
	}
	modelID := r1.Settings.AiConfigs[0].ID
	r1.Settings.AiConfigs = []*models.AIConfig{}
	removed, err := store.Save(ctx, Research1, 1, r1.Settings)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed.Settings.AiConfigs) != 0 || removed.Settings.AIAnalysisConfigID != 0 {
		t.Fatal("archived model remains executable")
	}
	var historical models.AIConfig
	if err := db.First(&historical, modelID).Error; err != nil || historical.ArchivedAt == nil || historical.ApiKey != original.AiConfigs[0].ApiKey {
		t.Fatalf("historical model lost: %v", err)
	}
	if _, err := store.Save(ctx, Research1, 2, original); !errors.Is(err, ErrModelOwnership) {
		t.Fatalf("archived model restored: %v", err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error { return SaveModels(tx, Global, nil) }); err != nil {
		t.Fatal(err)
	}
	r2, _ = store.Load(ctx, Research2)
	if len(r2.Settings.AiConfigs) != 1 {
		t.Fatal("global delete archived Research 2 models")
	}
}

func TestNewModelIDsAndSnapshotIsolation(t *testing.T) {
	store, _ := fixtureStore(t)
	original, _ := store.Load(t.Context(), Research1)
	draft := Clone(original.Settings)
	draft.AiConfigs = append(draft.AiConfigs, &models.AIConfig{Name: "new", Disabled: true})
	result, err := store.Save(t.Context(), Research1, 1, draft)
	if err != nil {
		t.Fatal(err)
	}
	if result.Settings.AiConfigs[1].ID == 0 || draft.AiConfigs[1].ID != 0 {
		t.Fatal("new model IDs must be generated without changing draft")
	}
	result.Settings.AiConfigs[0].ApiKey = "changed"
	result.Settings.MinuteProviderOrder[0] = "private"
	if original.Settings.AiConfigs[0].ApiKey != Research1+"-key" || original.Settings.MinuteProviderOrder[0] != "tencent" {
		t.Fatal("captured snapshot changed")
	}
	result.Settings.AiConfigs = nil
	if _, err := store.Save(t.Context(), Research1, 2, result.Settings); err != nil {
		t.Fatal(err)
	}
	loaded, _ := store.Load(t.Context(), Research1)
	if len(loaded.Settings.AiConfigs) != 2 {
		t.Fatal("omitted models must preserve list")
	}
}

func TestConfigOwnershipAndTypedValues(t *testing.T) {
	for _, input := range []struct{ center, raw string }{
		{Research1, `{"research2EmailTo":"somebody"}`}, {Research2, `{"aiTargetCapitalUtilization":0.8}`},
		{Research1, `{"darkTheme":true}`}, {Research1, `{"aiConfigs":[]}`},
		{Research1, `{"tushareToken":42}`}, {Research2, `null`}, {Research1, `{"tushareToken":null}`},
	} {
		if _, err := DecodeConfig(input.center, []byte(input.raw)); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("accepted %s: %v", input.raw, err)
		}
	}
	cfg := &models.SettingConfig{Settings: &models.Settings{DarkTheme: true, Research2EmailSMTPPass: "secret", AIAnalysisConfigID: 42}}
	raw, err := ConfigJSON(Research1, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"darkTheme", "research2", "secret", "aiAnalysisConfigId", "aiConfigs"} {
		if strings.Contains(string(raw), unwanted) {
			t.Fatalf("projection contains %s", unwanted)
		}
	}
	if _, err := DecodeConfig(Research1, raw); err != nil {
		t.Fatalf("projection cannot round-trip: %v", err)
	}
}
