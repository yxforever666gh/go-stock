package settingsapp

import (
	"errors"
	"testing"

	"go-stock/backend/models"
	appservice "go-stock/internal/service"
)

type settingsProviderFixture struct {
	config       *models.SettingConfig
	export       string
	updateResult string
	updated      *models.SettingConfig
}

func (f *settingsProviderFixture) LoadSettings() *models.SettingConfig { return f.config }
func (f *settingsProviderFixture) ExportSettings() string              { return f.export }
func (f *settingsProviderFixture) UpdateSettings(config *models.SettingConfig) string {
	f.updated = config
	return f.updateResult
}

func TestServicePreservesLoadExportAndUpdateResults(t *testing.T) {
	config := &models.SettingConfig{Settings: &models.Settings{QgqpBId: " fingerprint "}}
	provider := &settingsProviderFixture{config: config, export: `{"ok":true}`, updateResult: "保存成功！"}
	settings := NewService(provider)

	if settings.GetConfig() != config || settings.ExportConfig() != provider.export {
		t.Fatal("provider values were not preserved")
	}
	message, err := settings.UpdateConfig(config)
	if err != nil || message != "保存成功！" || provider.updated != config {
		t.Fatalf("message=%q err=%v updated=%p", message, err, provider.updated)
	}
	fingerprint, err := settings.ResolveFingerprint()
	if err != nil || fingerprint != "fingerprint" {
		t.Fatalf("fingerprint=%q err=%v", fingerprint, err)
	}
}

func TestServicePreservesValidationAndMissingFingerprintErrors(t *testing.T) {
	provider := &settingsProviderFixture{updateResult: "保存失败: 配置为空"}
	settings := NewService(provider)
	message, err := settings.UpdateConfig(nil)
	if message != provider.updateResult || !errors.Is(err, appservice.ErrInvalidInput) {
		t.Fatalf("message=%q err=%v", message, err)
	}
	if _, err := settings.ResolveFingerprint(); err == nil || err.Error() != "missing qgqp_b_id" {
		t.Fatalf("missing fingerprint err=%v", err)
	}
}
