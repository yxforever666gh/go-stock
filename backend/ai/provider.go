package ai

import (
	"net/url"
	"strconv"
	"strings"
)

type providerRule struct {
	label    string
	keywords []string
}

var providerKeywordRules = []providerRule{
	{label: "DeepSeek", keywords: []string{"deepseek"}},
	{label: "OpenAI", keywords: []string{"openai"}},
	{label: "OpenRouter", keywords: []string{"openrouter"}},
	{label: "Ollama", keywords: []string{"ollama"}},
	{label: "LM Studio", keywords: []string{"lm studio", "lmstudio"}},
	{label: "AnythingLLM", keywords: []string{"anythingllm"}},
	{label: "火山方舟", keywords: []string{"ark.cn", "volces", "火山方舟", "doubao", "豆包"}},
	{label: "阿里云百炼", keywords: []string{"dashscope", "aliyuncs", "百炼", "通义", "qwen"}},
	{label: "硅基流动", keywords: []string{"siliconflow", "硅基流动"}},
	{label: "智谱AI", keywords: []string{"bigmodel", "智谱", "chatglm", "glm-4", "glm-4.5"}},
	{label: "Moonshot", keywords: []string{"moonshot", "kimi"}},
	{label: "Anthropic", keywords: []string{"anthropic", "claude"}},
	{label: "Google", keywords: []string{"gemini", "googleapis", "google"}},
	{label: "Groq", keywords: []string{"groq"}},
	{label: "xAI", keywords: []string{"x.ai", "xai", "grok"}},
	{label: "腾讯混元", keywords: []string{"hunyuan", "混元"}},
	{label: "百度千帆", keywords: []string{"wenxinworkshop", "qianfan", "千帆", "文心"}},
}

var providerModelFallbackRules = []providerRule{
	{label: "DeepSeek", keywords: []string{"deepseek"}},
	{label: "阿里云百炼", keywords: []string{"qwen"}},
	{label: "Anthropic", keywords: []string{"claude"}},
	{label: "Google", keywords: []string{"gemini"}},
	{label: "Moonshot", keywords: []string{"kimi"}},
	{label: "xAI", keywords: []string{"grok"}},
	{label: "智谱AI", keywords: []string{"glm"}},
	{label: "火山方舟", keywords: []string{"doubao"}},
	{label: "腾讯混元", keywords: []string{"hunyuan"}},
}

func DetectProviderName(name, baseURL, modelName string) string {
	if provider := matchProviderByText(name, providerKeywordRules); provider != "" {
		return provider
	}
	if provider := matchProviderByText(baseURL, providerKeywordRules); provider != "" {
		return provider
	}
	if provider := detectLocalProviderName(name, baseURL); provider != "" {
		return provider
	}
	if provider := matchProviderByText(modelName, providerModelFallbackRules); provider != "" {
		return provider
	}
	return fallbackProviderNameFromBaseURL(baseURL)
}

func DisplayProviderName(name, baseURL, modelName string) string {
	if configured := strings.TrimSpace(name); configured != "" {
		return configured
	}
	return strings.TrimSpace(DetectProviderName(name, baseURL, modelName))
}

func matchProviderByText(text string, rules []providerRule) string {
	lowerText := strings.ToLower(strings.TrimSpace(text))
	if lowerText == "" {
		return ""
	}
	for _, rule := range rules {
		for _, keyword := range rule.keywords {
			if strings.Contains(lowerText, keyword) {
				return rule.label
			}
		}
	}
	return ""
}

func detectLocalProviderName(name, baseURL string) string {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return ""
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if host != "localhost" && host != "127.0.0.1" && host != "0.0.0.0" {
		return ""
	}
	if provider := matchProviderByText(name, []providerRule{
		{label: "Ollama", keywords: []string{"ollama"}},
		{label: "LM Studio", keywords: []string{"lm studio", "lmstudio"}},
		{label: "AnythingLLM", keywords: []string{"anythingllm"}},
	}); provider != "" {
		return provider
	}
	switch normalizePort(u.Port()) {
	case 11434:
		return "Ollama"
	case 1234:
		return "LM Studio"
	default:
		return "本地兼容服务"
	}
}

func fallbackProviderNameFromBaseURL(baseURL string) string {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(u.Hostname())
}

func normalizePort(raw string) int {
	port, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return port
}
