package data

import "testing"

func TestResolveAIFallbackOrderSkipsDisabledModels(t *testing.T) {
	setting := &SettingConfig{AiConfigs: []*AIConfig{
		{ID: 1, Sort: 1, Name: "first"},
		{ID: 2, Sort: 2, Disabled: true, Name: "disabled"},
		{ID: 3, Sort: 3, Name: "third"},
	}}
	got := ResolveAIFallbackOrder(setting, 0)
	if len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("fallback order = %v, want [1 3]", got)
	}
	got = ResolveAIFallbackOrder(setting, 2)
	if len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("disabled requested model entered fallback order: %v", got)
	}
	got = ResolveAIFallbackOrder(setting, 3)
	if len(got) != 2 || got[0] != 3 || got[1] != 1 {
		t.Fatalf("enabled requested model was not preferred: %v", got)
	}
}

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
