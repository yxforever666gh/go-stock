package service

import (
	"testing"

	"go-stock/backend/models"
)

type recordingStockOperations struct {
	StockOperations
	followedCode string
}

func (o *recordingStockOperations) Follow(code string) string {
	o.followedCode = code
	return "followed"
}

type recordingConfigOperations struct {
	ConfigOperations
	config *models.SettingConfig
}

func (o *recordingConfigOperations) GetConfig() *models.SettingConfig {
	return o.config
}

func TestServicesDelegateToInjectedConsumerPorts(t *testing.T) {
	stockOperations := &recordingStockOperations{}
	stockService := NewStockService(stockOperations)
	if got := stockService.Follow("600000.SH"); got != "followed" {
		t.Fatalf("Follow() = %q, want followed", got)
	}
	if stockOperations.followedCode != "600000.SH" {
		t.Fatalf("delegated stock code = %q", stockOperations.followedCode)
	}

	wantConfig := &models.SettingConfig{Settings: &models.Settings{OpenAiEnable: true}}
	configService := NewConfigService(&recordingConfigOperations{config: wantConfig})
	if got := configService.GetConfig(); got != wantConfig {
		t.Fatal("GetConfig() did not return the injected port result")
	}
}

func TestServiceOperationsValidateNamesMissingPort(t *testing.T) {
	err := (ServiceOperations{}).Validate()
	if err == nil || err.Error() != "ai operations are required" {
		t.Fatalf("Validate() error = %v", err)
	}
}
