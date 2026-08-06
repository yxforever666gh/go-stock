//go:build !webonly

package main

import (
	"testing"

	"go-stock/backend/models"
	"go-stock/internal/service"
)

type desktopConfigOperations struct {
	service.ConfigOperations
	config *models.SettingConfig
}

func (o desktopConfigOperations) GetConfig() *models.SettingConfig { return o.config }

func TestDesktopDarkThemeUsesInjectedConfiguration(t *testing.T) {
	app := &App{services: service.AppServices{Config: service.NewConfigService(desktopConfigOperations{
		config: &models.SettingConfig{Settings: &models.Settings{DarkTheme: true}},
	})}}
	if !desktopDarkTheme(app) {
		t.Fatal("desktop theme did not use injected dark-theme setting")
	}
	if desktopDarkTheme(nil) {
		t.Fatal("nil app must use the light-theme default")
	}
}
