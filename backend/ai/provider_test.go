package ai

import "testing"

func TestDetectProviderName(t *testing.T) {
	tests := []struct {
		name      string
		config    string
		baseURL   string
		modelName string
		want      string
	}{
		{name: "deepseek", baseURL: "https://api.deepseek.com", modelName: "deepseek-chat", want: "DeepSeek"},
		{name: "ollama", baseURL: "http://127.0.0.1:11434/v1", modelName: "qwen2.5:14b", want: "Ollama"},
		{name: "configured local", config: "LM Studio local", baseURL: "http://localhost:1234/v1", want: "LM Studio"},
		{name: "openrouter", baseURL: "https://openrouter.ai/api/v1", modelName: "anthropic/claude-sonnet-4", want: "OpenRouter"},
		{name: "host fallback", baseURL: "https://llm.example.com/v1", modelName: "custom-model", want: "llm.example.com"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := DetectProviderName(test.config, test.baseURL, test.modelName); got != test.want {
				t.Fatalf("provider = %q, want %q", got, test.want)
			}
		})
	}
}

func TestDisplayProviderNamePrefersConfiguredName(t *testing.T) {
	if got := DisplayProviderName("OpenAI Primary", "https://api.openai.com/v1", "gpt-5.4"); got != "OpenAI Primary" {
		t.Fatalf("display provider = %q", got)
	}
	if got := DisplayProviderName("", "", "deepseek-chat"); got != "DeepSeek" {
		t.Fatalf("fallback display provider = %q", got)
	}
}
