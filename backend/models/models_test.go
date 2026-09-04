package models

import "testing"

func TestNormalizeAIAPIProtocol(t *testing.T) {
	for input, want := range map[string]string{
		"":                   AIAPIProtocolChatCompletions,
		" CHAT_COMPLETIONS ": AIAPIProtocolChatCompletions,
		"OPENAI_RESPONSES":   AIAPIProtocolOpenAIResponses,
		"anthropic_messages": AIAPIProtocolAnthropicMessage,
		"unsupported":        AIAPIProtocolChatCompletions,
	} {
		if got := NormalizeAIAPIProtocol(input); got != want {
			t.Errorf("NormalizeAIAPIProtocol(%q) = %q, want %q", input, got, want)
		}
	}
}
