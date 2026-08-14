package data

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"go-stock/backend/models"
	"go-stock/backend/research"
)

func TestTruncateResearchSourceJSONIsUTF8Safe(t *testing.T) {
	value := strings.Repeat("财联社行情", 4000)
	result := truncateResearchSourceJSON(value, 16000)
	if !utf8.ValidString(result) {
		t.Fatal("truncated source is invalid UTF-8")
	}
	if !strings.HasSuffix(result, "...<truncated>") {
		t.Fatalf("missing truncation marker: %q", result[len(result)-32:])
	}
}

func TestResearchAIClientRetriesFiveTimesThenFallsBackInEnabledTableOrder(t *testing.T) {
	setting := &SettingConfig{AiConfigs: []*AIConfig{
		{ID: 1, Sort: 1, Name: "primary", ModelName: "model-1"},
		{ID: 2, Sort: 2, Disabled: true, Name: "off", ModelName: "model-2"},
		{ID: 3, Sort: 3, Name: "fallback", ModelName: "model-3"},
	}}
	called := make([]uint, 0, 6)
	client := &ResearchAIClient{
		loadSetting: func() *SettingConfig { return setting },
		completeProvider: func(ctx context.Context, config *models.AIConfig, _ []map[string]any, _ string) (string, string, string, error) {
			called = append(called, config.ID)
			deadline, ok := ctx.Deadline()
			remaining := time.Until(deadline)
			if !ok || remaining <= 25*time.Second || remaining > 31*time.Second {
				t.Fatalf("attempt deadline remaining=%s ok=%v, want about 30s", remaining, ok)
			}
			if config.ID == 1 {
				return "", "", "", errors.New("Upstream request failed")
			}
			return "ok", "response-3", config.ModelName, nil
		},
	}
	result, err := client.Complete(context.Background(), research.CompletionRequest{Phase: "market_analysis", Prompt: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(called, []uint{1, 1, 1, 1, 1, 3}) {
		t.Fatalf("called configs = %v, want [1 1 1 1 1 3]", called)
	}
	if result.Content != "ok" || result.Model != "model-3" || result.ResponseID != "response-3" {
		t.Fatalf("result = %+v", result)
	}
}

func TestResearchAIClientDefaultRetryPolicy(t *testing.T) {
	client := &ResearchAIClient{}
	if got := client.modelAttemptTimeout(); got != 30*time.Second {
		t.Fatalf("attempt timeout=%s, want 30s", got)
	}
	if got := client.modelMaxAttempts(); got != 5 {
		t.Fatalf("max attempts=%d, want 5", got)
	}
}

func TestResearchAIClientHardTimeoutThenFallback(t *testing.T) {
	setting := &SettingConfig{AiConfigs: []*AIConfig{
		{ID: 1, Sort: 1, Name: "slow", ModelName: "model-1"},
		{ID: 2, Sort: 2, Name: "fallback", ModelName: "model-2"},
	}}
	called := make([]uint, 0, 3)
	client := &ResearchAIClient{
		loadSetting:    func() *SettingConfig { return setting },
		attemptTimeout: 10 * time.Millisecond,
		maxAttempts:    2,
		completeProvider: func(ctx context.Context, config *models.AIConfig, _ []map[string]any, _ string) (string, string, string, error) {
			called = append(called, config.ID)
			if config.ID == 1 {
				<-ctx.Done()
				return "", "", "", ctx.Err()
			}
			return "ok", "response-2", config.ModelName, nil
		},
	}
	started := time.Now()
	result, err := client.Complete(context.Background(), research.CompletionRequest{Phase: "market_analysis", Prompt: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < 20*time.Millisecond || elapsed > 500*time.Millisecond {
		t.Fatalf("elapsed=%s, want two bounded attempts", elapsed)
	}
	if !reflect.DeepEqual(called, []uint{1, 1, 2}) {
		t.Fatalf("called configs=%v, want [1 1 2]", called)
	}
	if result.Content != "ok" || result.Model != "model-2" {
		t.Fatalf("result=%+v", result)
	}
}

func TestResearchProviderCanDisableNestedHTTPRetries(t *testing.T) {
	provider := &OpenAi{BaseUrl: "https://example.test", TimeOut: 30, DisableRequestRetries: true}
	if got := provider.newAIClient().RetryCount; got != 0 {
		t.Fatalf("OpenAI retry count=%d, want 0", got)
	}
	if got := provider.newAnthropicClient().RetryCount; got != 0 {
		t.Fatalf("Anthropic retry count=%d, want 0", got)
	}
}
