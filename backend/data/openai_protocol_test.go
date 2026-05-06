package data

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testOpenAI(baseURL, protocol string) *OpenAi {
	return &OpenAi{
		BaseUrl:     baseURL,
		ApiKey:      "test-key",
		ApiProtocol: protocol,
		Model:       "test-model",
		MaxTokens:   64,
		Temperature: 0.1,
		TimeOut:     5,
	}
}

func TestCompleteChatOpenAIResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("unexpected authorization header: %s", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"resp_1","model":"test-model","output_text":"OK"}`)
	}))
	defer server.Close()

	content, chatID, model, err := testOpenAI(server.URL, AIAPIProtocolOpenAIResponses).CompleteChat([]map[string]any{
		{"role": "system", "content": "system prompt"},
		{"role": "user", "content": "ping"},
	}, false)
	if err != nil {
		t.Fatalf("CompleteChat failed: %v", err)
	}
	if content != "OK" || chatID != "resp_1" || model != "test-model" {
		t.Fatalf("unexpected response: content=%q chatID=%q model=%q", content, chatID, model)
	}
}

func TestCompleteChatAnthropicMessages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Fatalf("unexpected api key header: %s", got)
		}
		if got := r.Header.Get("anthropic-version"); got == "" {
			t.Fatalf("missing anthropic-version header")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"msg_1","model":"claude-test","content":[{"type":"text","text":"OK"}]}`)
	}))
	defer server.Close()

	content, chatID, model, err := testOpenAI(server.URL, AIAPIProtocolAnthropicMessage).CompleteChat([]map[string]any{
		{"role": "system", "content": "system prompt"},
		{"role": "user", "content": "ping"},
	}, false)
	if err != nil {
		t.Fatalf("CompleteChat failed: %v", err)
	}
	if content != "OK" || chatID != "msg_1" || model != "claude-test" {
		t.Fatalf("unexpected response: content=%q chatID=%q model=%q", content, chatID, model)
	}
}

func TestAskAiOpenAIResponsesStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"model\":\"test-model\"}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"O\",\"response\":{\"id\":\"resp_1\",\"model\":\"test-model\"}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"K\",\"response\":{\"id\":\"resp_1\",\"model\":\"test-model\"}}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	ch := make(chan map[string]any, 8)
	AskAi(testOpenAI(server.URL, AIAPIProtocolOpenAIResponses), []map[string]interface{}{{"role": "user", "content": "ping"}}, ch, "ping", false)
	got := drainAIContent(ch)
	if got != "OK" {
		t.Fatalf("unexpected stream content: %q", got)
	}
}

func TestAskAiAnthropicMessagesStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"claude-test\"}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"O\"}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"K\"}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()

	ch := make(chan map[string]any, 8)
	AskAi(testOpenAI(server.URL, AIAPIProtocolAnthropicMessage), []map[string]interface{}{{"role": "user", "content": "ping"}}, ch, "ping", false)
	got := drainAIContent(ch)
	if got != "OK" {
		t.Fatalf("unexpected stream content: %q", got)
	}
}

func TestAskAiWithToolsRejectsNonChatCompletions(t *testing.T) {
	ch := make(chan map[string]any, 1)
	AskAiWithTools(testOpenAI("https://example.com", AIAPIProtocolAnthropicMessage), []map[string]interface{}{{"role": "user", "content": "ping"}}, ch, "ping", nil, false)
	msg := <-ch
	if msg["code"] != 0 {
		t.Fatalf("expected error message, got %+v", msg)
	}
	if !strings.Contains(fmt.Sprint(msg["content"]), "暂不支持工具调用") {
		t.Fatalf("unexpected error content: %+v", msg)
	}
}

func drainAIContent(ch chan map[string]any) string {
	var b strings.Builder
	for {
		select {
		case msg := <-ch:
			if msg["code"] == 1 {
				b.WriteString(fmt.Sprint(msg["content"]))
			}
		default:
			return b.String()
		}
	}
}
