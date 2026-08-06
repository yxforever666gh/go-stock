package agent

import (
	"strings"
	"testing"

	"go-stock/backend/models"
)

func TestResolveAIConfigPrefersRequestedIDThenFirstConfigured(t *testing.T) {
	configs := []*models.AIConfig{{ID: 7, ModelName: "primary"}, {ID: 9, ModelName: "requested"}}
	if got := resolveAIConfig(configs, 9); got == nil || got.ID != 9 {
		t.Fatalf("requested config = %#v", got)
	}
	if got := resolveAIConfig(configs, 0); got == nil || got.ID != 7 {
		t.Fatalf("fallback config = %#v", got)
	}
	if got := resolveAIConfig(nil, 1); got != nil {
		t.Fatalf("empty config result = %#v", got)
	}
	if got := resolveAIConfig([]*models.AIConfig{nil, {ID: 9}}, 7); got != nil {
		t.Fatalf("fallback must preserve saved first-entry semantics: %#v", got)
	}
}

func TestAgentRejectsMissingConfigurationProvider(t *testing.T) {
	ch := NewStockAiAgentApi(fakeAgentToolDataProvider{}, nil).Chat("test", 0, nil)
	message, ok := <-ch
	if !ok || message == nil || !strings.Contains(message.Content, "configuration provider") {
		t.Fatalf("missing configuration error = %#v, open=%t", message, ok)
	}
	if _, ok := <-ch; ok {
		t.Fatal("error channel must close after initialization failure")
	}
}
