package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"go-stock/backend/data"
	"go-stock/backend/recommendation"
)

func TestMarketSummaryEventVerifierCompatibilityAdapterForwardsSameOpenAIRequestAndResponse(t *testing.T) {
	messages := []map[string]any{
		{"role": "system", "content": "system"},
		{"role": "user", "content": "input"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("request path=%q", r.URL.Path)
		}
		var body struct {
			Messages []map[string]any `json:"messages"`
			Thinking map[string]any   `json:"thinking"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(body.Messages, messages) {
			t.Fatalf("messages changed across adapter: got=%#v want=%#v", body.Messages, messages)
		}
		if body.Thinking["type"] != "enabled" {
			t.Fatalf("think flag changed across adapter: %#v", body.Thinking)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"response-1","model":"model-x","choices":[{"message":{"content":"OK"}}]}`)
	}))
	defer server.Close()

	openAI := &data.OpenAi{
		BaseUrl: server.URL, ApiKey: "test-key", ApiProtocol: data.AIAPIProtocolChatCompletions,
		Model: "configured-model", MaxTokens: 64, TimeOut: 5,
	}
	adapter := &marketSummaryEventVerifierCompatibilityAdapter{openAI: openAI}
	completion, err := adapter.Verify(context.Background(), recommendation.EventVerificationCall{
		Messages: messages,
		Think:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := recommendation.EventVerificationCompletion{Content: "OK", ResponseID: "response-1", Model: "model-x"}
	if !reflect.DeepEqual(completion, want) {
		t.Fatalf("completion changed across adapter: got=%+v want=%+v", completion, want)
	}
}

func TestMarketSummaryEventVerifierCompatibilityAdapterPreservesPartialCompletionAndError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"response-error","model":"model-x","choices":[]}`)
	}))
	defer server.Close()

	openAI := &data.OpenAi{
		BaseUrl: server.URL, ApiKey: "test-key", ApiProtocol: data.AIAPIProtocolChatCompletions,
		Model: "configured-model", MaxTokens: 64, TimeOut: 5,
	}
	adapter := &marketSummaryEventVerifierCompatibilityAdapter{openAI: openAI}
	completion, err := adapter.Verify(context.Background(), recommendation.EventVerificationCall{
		Messages: []map[string]any{{"role": "user", "content": "input"}},
	})
	if err == nil || err.Error() != "empty choices from model provider" {
		t.Fatalf("provider error changed across adapter: %v", err)
	}
	want := recommendation.EventVerificationCompletion{ResponseID: "response-error", Model: "model-x"}
	if !reflect.DeepEqual(completion, want) {
		t.Fatalf("partial completion changed across adapter: got=%+v want=%+v", completion, want)
	}
}
