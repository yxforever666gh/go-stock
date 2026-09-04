package data

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestTruncateResearchSourceJSONIsUTF8Safe(t *testing.T) {
	value := strings.Repeat("财联社行情", 4000)
	result := truncateResearchSourceJSON(value, 16000)
	if !utf8.ValidString(result) {
		t.Fatal("truncated source is invalid UTF-8")
	}
	var decoded string
	if len(result) > 16000 || json.Unmarshal([]byte(result), &decoded) != nil || decoded == value {
		t.Fatalf("truncated source is not bounded structural JSON")
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

func TestResearchProviderCanDisableNestedHTTPRetries(t *testing.T) {
	provider := &OpenAi{BaseUrl: "https://example.test", TimeOut: 30, DisableRequestRetries: true}
	if got := provider.newAIClient().RetryCount; got != 0 {
		t.Fatalf("OpenAI retry count=%d, want 0", got)
	}
	if got := provider.newAnthropicClient().RetryCount; got != 0 {
		t.Fatalf("Anthropic retry count=%d, want 0", got)
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
