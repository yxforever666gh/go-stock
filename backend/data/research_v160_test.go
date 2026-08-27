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

func TestResearchDocumentMarksSemanticFailuresAndKeepsNormalEmpty(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	tests := []struct {
		name      string
		value     any
		wantError string
	}{
		{name: "business failure", value: map[string]any{"success": false, "message": "返回数据为空", "code": 9201}, wantError: "来源返回失败: 返回数据为空: code=9201"},
		{name: "failed status", value: map[string]any{"status": "failed", "warning": "refresh failed"}, wantError: "来源状态失败: refresh failed"},
		{name: "stale status", value: map[string]any{"status": "stale", "warning": "too old"}, wantError: "来源数据已过期: too old"},
		{name: "normal empty", value: map[string]any{"status": "empty", "items": []any{}}, wantError: ""},
		{name: "false error flag", value: map[string]any{"error": false, "items": []any{}}, wantError: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := researchDocument("source", "stock", now, test.value)
			if document.Error != test.wantError {
				t.Fatalf("error=%q want=%q content=%s", document.Error, test.wantError, document.Content)
			}
		})
	}
}

func TestExecuteResearchSourceJobUsesCompletionClock(t *testing.T) {
	completed := time.Date(2026, 8, 25, 10, 1, 2, 0, time.FixedZone("CST", 8*60*60))
	document := executeResearchSourceJob("source", "stock", func() time.Time { return completed }, func() any {
		return map[string]any{"status": "ok"}
	})
	if !document.CollectedAt.Equal(completed) {
		t.Fatalf("collectedAt=%s want=%s", document.CollectedAt, completed)
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
		retryWait:   func(context.Context, time.Duration) error { return nil },
		completeProvider: func(ctx context.Context, config *models.AIConfig, _ []map[string]any, _ string, _ func(AIStreamActivity)) (string, string, string, error) {
			called = append(called, config.ID)
			if _, ok := ctx.Deadline(); ok {
				t.Fatal("research attempt must use inactivity cancellation, not a fixed deadline")
			}
			if config.ID == 1 {
				return "", "", "", &aiProviderCallError{Category: "provider_error", Message: "Upstream request failed", Retryable: true}
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
	if got := client.modelAttemptTimeout(); got != 300*time.Second {
		t.Fatalf("attempt timeout=%s, want 300s", got)
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
		retryWait:      func(context.Context, time.Duration) error { return nil },
		completeProvider: func(ctx context.Context, config *models.AIConfig, _ []map[string]any, _ string, _ func(AIStreamActivity)) (string, string, string, error) {
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

func TestResearchAIClientBacksOffAndCoolsDownSharedEndpoint(t *testing.T) {
	setting := &SettingConfig{AiConfigs: []*AIConfig{
		{ID: 1, Sort: 1, Name: "primary", BaseUrl: "https://shared.example/v1/", ModelName: "model-1"},
		{ID: 2, Sort: 2, Name: "fallback", BaseUrl: "https://shared.example/v1", ModelName: "model-2"},
	}}
	waits := make([]time.Duration, 0, 2)
	client := &ResearchAIClient{
		loadSetting: func() *SettingConfig { return setting },
		maxAttempts: 2,
		retryWait: func(_ context.Context, delay time.Duration) error {
			waits = append(waits, delay)
			return nil
		},
		completeProvider: func(_ context.Context, config *models.AIConfig, _ []map[string]any, _ string, _ func(AIStreamActivity)) (string, string, string, error) {
			if config.ID == 1 {
				return "", "", "", &aiProviderCallError{Category: "network_error", Message: "connection reset", Retryable: true}
			}
			return "ok", "response-2", config.ModelName, nil
		},
	}
	result, err := client.Complete(context.Background(), research.CompletionRequest{Phase: "market_analysis", Prompt: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "ok" {
		t.Fatalf("result=%+v", result)
	}
	want := []time.Duration{time.Second, researchSameEndpointFallbackCooldown}
	if !reflect.DeepEqual(waits, want) {
		t.Fatalf("waits=%v want=%v", waits, want)
	}
}

func TestResearchModelRetryDelayIsExponentiallyBounded(t *testing.T) {
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second}
	for index, expected := range want {
		if got := researchModelRetryDelay(index + 1); got != expected {
			t.Fatalf("attempt=%d delay=%s want=%s", index+1, got, expected)
		}
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

func TestResearchAIClientActiveInferenceResetsInactivityTimeout(t *testing.T) {
	setting := &SettingConfig{AiConfigs: []*AIConfig{{ID: 1, Sort: 1, Name: "active", ModelName: "model-1"}}}
	client := &ResearchAIClient{
		loadSetting:    func() *SettingConfig { return setting },
		attemptTimeout: 25 * time.Millisecond,
		maxAttempts:    1,
		completeProvider: func(ctx context.Context, _ *models.AIConfig, _ []map[string]any, _ string, activity func(AIStreamActivity)) (string, string, string, error) {
			for index := 0; index < 8; index++ {
				select {
				case <-ctx.Done():
					return "", "", "", context.Cause(ctx)
				case <-time.After(10 * time.Millisecond):
					activity(AIStreamActivity{EventType: "response.in_progress", State: "reasoning"})
				}
			}
			return "ok", "response-1", "model-1", nil
		},
	}
	started := time.Now()
	result, err := client.Complete(context.Background(), research.CompletionRequest{Phase: "market_analysis", Prompt: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < 70*time.Millisecond {
		t.Fatalf("elapsed=%s, active stream should outlive one inactivity window", elapsed)
	}
	if result.Content != "ok" {
		t.Fatalf("result=%+v", result)
	}
}

func TestResearchAIClientFatalErrorFallsBackImmediately(t *testing.T) {
	setting := &SettingConfig{AiConfigs: []*AIConfig{
		{ID: 1, Sort: 1, Name: "bad-key", ModelName: "model-1", ApiKey: "secret-key", BaseUrl: "https://secret.example"},
		{ID: 2, Sort: 2, Name: "fallback", ModelName: "model-2"},
	}}
	called := make([]uint, 0, 2)
	records := make([]research.ModelAttemptRecord, 0)
	client := &ResearchAIClient{
		loadSetting: func() *SettingConfig { return setting },
		completeProvider: func(_ context.Context, config *models.AIConfig, _ []map[string]any, _ string, _ func(AIStreamActivity)) (string, string, string, error) {
			called = append(called, config.ID)
			if config.ID == 1 {
				return "", "", "", &aiProviderCallError{Category: "http_error", StatusCode: 401, Message: "secret-key rejected by https://secret.example", Retryable: false}
			}
			return "ok", "response-2", "model-2", nil
		},
	}
	result, err := client.Complete(context.Background(), research.CompletionRequest{
		Phase: "sector_analysis", Prompt: "test", OnAttempt: func(record research.ModelAttemptRecord) { records = append(records, record) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(called, []uint{1, 2}) {
		t.Fatalf("called=%v, fatal provider should fall back immediately", called)
	}
	if result.Content != "ok" {
		t.Fatalf("result=%+v", result)
	}
	var failed *research.ModelAttemptRecord
	for index := range records {
		if records[index].ConfigID == 1 && records[index].Status == "failed" {
			failed = &records[index]
		}
	}
	if failed == nil || failed.NextAction != "fallback_next_model" || failed.Retryable {
		t.Fatalf("failed record=%+v", failed)
	}
	if strings.Contains(failed.ErrorMessage, "secret-key") || strings.Contains(failed.ErrorMessage, "secret.example") {
		t.Fatalf("error was not sanitized: %q", failed.ErrorMessage)
	}
}

func TestResearchAIClientParentCancellationIsNotRetried(t *testing.T) {
	setting := &SettingConfig{AiConfigs: []*AIConfig{{ID: 1, Sort: 1, Name: "slow", ModelName: "model-1"}}}
	calls := 0
	client := &ResearchAIClient{
		loadSetting:    func() *SettingConfig { return setting },
		attemptTimeout: time.Second,
		completeProvider: func(ctx context.Context, _ *models.AIConfig, _ []map[string]any, _ string, _ func(AIStreamActivity)) (string, string, string, error) {
			calls++
			<-ctx.Done()
			return "", "", "", context.Cause(ctx)
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := client.Complete(ctx, research.CompletionRequest{Phase: "market_analysis", Prompt: "test"})
	if !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestValidateResearchQuoteResponseCodeRequiresActualMatchingCode(t *testing.T) {
	tests := []struct {
		name      string
		requested string
		response  string
		want      string
		wantErr   bool
	}{
		{name: "matching prefixed", requested: "sh601899", response: "SH601899", want: "sh601899"},
		{name: "matching digits", requested: "sh601899", response: "601899", want: "sh601899"},
		{name: "different valid code", requested: "sh601899", response: "sh600000", wantErr: true},
		{name: "missing response code", requested: "sh601899", response: "", wantErr: true},
		{name: "invalid response code", requested: "sh601899", response: "not-a-code", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := validateResearchQuoteResponseCode(test.requested, test.response)
			if test.wantErr {
				if err == nil {
					t.Fatalf("got=%q, expected error", got)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("got=%q err=%v want=%q", got, err, test.want)
			}
		})
	}
}
