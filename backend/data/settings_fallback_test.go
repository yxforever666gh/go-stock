package data

import "testing"

func TestResolveAIAnalysisConfigUsesFirstEnabledRow(t *testing.T) {
	setting := &SettingConfig{AiConfigs: []*AIConfig{
		{ID: 1, Sort: 1, Disabled: true, Name: "disabled"},
		{ID: 2, Sort: 2, Name: "primary"},
		{ID: 3, Sort: 3, Name: "fallback"},
	}}
	selected, err := ResolveAIAnalysisConfig(setting)
	if err != nil {
		t.Fatal(err)
	}
	if selected.ID != 2 {
		t.Fatalf("selected ID = %d, want 2", selected.ID)
	}
	setting.AiConfigs[1].Disabled = true
	setting.AiConfigs[2].Disabled = true
	if _, err := ResolveAIAnalysisConfig(setting); err == nil {
		t.Fatal("all-disabled configuration must not resolve a model")
	}
}
