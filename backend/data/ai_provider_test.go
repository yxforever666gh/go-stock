package data

import "testing"

func TestDetectAIProviderName(t *testing.T) {
	tests := []struct {
		name   string
		config *AIConfig
		want   string
	}{
		{
			name: "deepseek by base url",
			config: &AIConfig{
				BaseUrl:   "https://api.deepseek.com",
				ModelName: "deepseek-chat",
			},
			want: "DeepSeek",
		},
		{
			name: "ollama by local port",
			config: &AIConfig{
				BaseUrl:   "http://127.0.0.1:11434/v1",
				ModelName: "qwen2.5:14b",
			},
			want: "Ollama",
		},
		{
			name: "lm studio by config name",
			config: &AIConfig{
				Name:      "LM Studio 本地",
				BaseUrl:   "http://localhost:1234/v1",
				ModelName: "gpt-4o-mini",
			},
			want: "LM Studio",
		},
		{
			name: "openrouter by host",
			config: &AIConfig{
				BaseUrl:   "https://openrouter.ai/api/v1",
				ModelName: "anthropic/claude-sonnet-4",
			},
			want: "OpenRouter",
		},
		{
			name: "fallback to host",
			config: &AIConfig{
				BaseUrl:   "https://llm.example.com/v1",
				ModelName: "custom-model",
			},
			want: "llm.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectAIProviderName(tt.config); got != tt.want {
				t.Fatalf("DetectAIProviderName() = %q, want %q", got, tt.want)
			}
		})
	}
}
