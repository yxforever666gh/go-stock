package data

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	aicontract "go-stock/backend/ai"
	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	"go-stock/backend/util"
	"io"
	"net"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/chromedp"
	"github.com/duke-git/lancet/v2/convertor"
	"github.com/duke-git/lancet/v2/random"
	"github.com/duke-git/lancet/v2/strutil"
	"github.com/go-resty/resty/v2"
	"github.com/samber/lo"
	"github.com/tidwall/gjson"
)

// @Author spark
// @Date 2025/1/16 13:19
// @Desc
// -----------------------------------------------------------------------------------
type OpenAi struct {
	ctx              context.Context
	BaseUrl          string  `json:"base_url"`
	ApiKey           string  `json:"api_key"`
	ApiProtocol      string  `json:"api_protocol"`
	ProviderName     string  `json:"provider_name"`
	Model            string  `json:"model"`
	MaxTokens        int     `json:"max_tokens"`
	Temperature      float64 `json:"temperature"`
	Prompt           string  `json:"prompt"`
	TimeOut          int     `json:"time_out"`
	QuestionTemplate string  `json:"question_template"`
	CrawlTimeOut     int64   `json:"crawl_time_out"`
	KDays            int64   `json:"kDays"`
	BrowserPath      string  `json:"browser_path"`
	HttpProxy        string  `json:"httpProxy"`
	HttpProxyEnabled bool    `json:"httpProxyEnabled"`
	// DisableRequestRetries lets orchestrators own retry and fallback ordering.
	// It is enabled by the 1.6.0 research workflow so one logical attempt maps
	// to one provider request instead of being multiplied by Resty retries.
	DisableRequestRetries bool `json:"-"`
}

func (o OpenAi) String() string {
	return fmt.Sprintf("OpenAi{BaseUrl: %s, Protocol: %s, Model: %s, MaxTokens: %d, Temperature: %.2f, Prompt: %s, TimeOut: %d, QuestionTemplate: %s, CrawlTimeOut: %d, KDays: %d, BrowserPath: %s, ApiKey: [MASKED]}",
		o.BaseUrl, NormalizeAIAPIProtocol(o.ApiProtocol), o.Model, o.MaxTokens, o.Temperature, o.Prompt, o.TimeOut, o.QuestionTemplate, o.CrawlTimeOut, o.KDays, o.BrowserPath)
}

func NewDeepSeekOpenAi(ctx context.Context, aiConfigId int) *OpenAi {
	settingConfig := GetSettingConfig()
	aiConfig, find := lo.Find(settingConfig.AiConfigs, func(item *AIConfig) bool {
		return uint(aiConfigId) == item.ID
	})
	if !find || aiConfigId <= 0 {
		aiConfig = SelectPrimaryAIConfig(settingConfig.AiConfigs)
	}
	if aiConfig == nil {
		aiConfig = &AIConfig{}
	}
	return NewOpenAiWithConfig(ctx, aiConfig)
}

func NewOpenAiWithConfig(ctx context.Context, aiConfig *AIConfig) *OpenAi {
	if aiConfig == nil {
		aiConfig = &AIConfig{}
	}
	settingConfig := GetSettingConfig()
	if aiConfig.TimeOut <= 0 {
		aiConfig.TimeOut = 60 * 5
	}
	if settingConfig.CrawlTimeOut <= 0 {
		settingConfig.CrawlTimeOut = 60
	}
	if settingConfig.KDays < 30 {
		settingConfig.KDays = 60
	}

	o := &OpenAi{
		ctx:              ctx,
		BaseUrl:          aiConfig.BaseUrl,
		ApiKey:           aiConfig.ApiKey,
		ApiProtocol:      NormalizeAIAPIProtocol(aiConfig.ApiProtocol),
		ProviderName:     DisplayAIProviderName(aiConfig),
		Model:            aiConfig.ModelName,
		MaxTokens:        aiConfig.MaxTokens,
		Temperature:      aiConfig.Temperature,
		TimeOut:          aiConfig.TimeOut,
		HttpProxy:        aiConfig.HttpProxy,
		HttpProxyEnabled: aiConfig.HttpProxyEnabled,
		Prompt:           settingConfig.Prompt,
		QuestionTemplate: settingConfig.QuestionTemplate,
		CrawlTimeOut:     settingConfig.CrawlTimeOut,
		KDays:            settingConfig.KDays,
		BrowserPath:      settingConfig.BrowserPath,
	}
	return o
}

type THSTokenResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data"`
}

type AiResponse struct {
	Id          string `json:"id"`
	Object      string `json:"object"`
	Created     int    `json:"created"`
	Model       string `json:"model"`
	ServiceTier string `json:"service_tier"`
	Choices     []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		Logprobs     interface{} `json:"logprobs"`
		FinishReason string      `json:"finish_reason"`
		Delta        struct {
			Content   string `json:"content"`
			Role      string `json:"role"`
			ToolCalls []struct {
				Function struct {
					Arguments string `json:"arguments"`
					Name      string `json:"name"`
				} `json:"function"`
				Id    string `json:"id"`
				Index int    `json:"index"`
				Type  string `json:"type"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Usage struct {
		PromptTokens          int `json:"prompt_tokens"`
		CompletionTokens      int `json:"completion_tokens"`
		TotalTokens           int `json:"total_tokens"`
		PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`
		PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"`
	} `json:"usage"`
	SystemFingerprint string `json:"system_fingerprint"`
}

// NewChatStreamLite provides a CLI-friendly chat stream path that avoids
// GUI events and browser crawler dependencies.
func (o *OpenAi) NewChatStreamLite(stock, stockCode, userQuestion string, thinking bool) <-chan map[string]any {
	ch := make(chan map[string]any, 512)

	go func() {
		defer func() {
			if err := recover(); err != nil {
				logger.SugaredLogger.Errorf("NewChatStreamLite panic: %v", err)
			}
			close(ch)
		}()

		sysPrompt := strutil.Trim(o.Prompt)
		if sysPrompt == "" {
			sysPrompt = "你是一名专业股票分析助手，请基于公开信息给出结构化、审慎的分析结论，不做收益承诺。"
		}

		msg := []map[string]interface{}{
			{
				"role":    "system",
				"content": sysPrompt,
			},
			{
				"role":    "user",
				"content": "当前时间",
			},
			{
				"role":    "assistant",
				"content": "当前本地时间是:" + time.Now().Format("2006-01-02 15:04:05"),
			},
		}

		stockName := strutil.Trim(stock)
		stockCode = strutil.Trim(stockCode)
		if stockCode != "" {
			if stockData, err := NewStockDataApi().GetStockCodeRealTimeDataReadOnly(context.Background(), stockCode); err == nil && len(*stockData) > 0 {
				msg = append(msg, map[string]interface{}{
					"role":    "user",
					"content": fmt.Sprintf("当前%s[%s]价格是多少？", stockName, stockCode),
				})
				msg = append(msg, map[string]interface{}{
					"role":    "assistant",
					"content": fmt.Sprintf("截止到%s,当前%s[%s]价格是%s", (*stockData)[0].Date+" "+(*stockData)[0].Time, stockName, stockCode, (*stockData)[0].Price),
				})
			}
		}

		question := strutil.Trim(userQuestion)
		if question == "" {
			question = fmt.Sprintf("请结合当前可获得信息，对%s[%s]做短中线分析，并给出风险提示。", stockName, stockCode)
		}
		msg = append(msg, map[string]interface{}{
			"role":    "user",
			"content": question,
		})

		AskAi(o, msg, ch, question, thinking)
	}()
	return ch
}

func (o *OpenAi) requestTimeoutSeconds() int {
	if o.TimeOut <= 0 {
		return 300
	}
	return o.TimeOut
}

func shouldRetryAIRequest(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	errMsg := strings.ToLower(err.Error())
	retryableErrHints := []string{
		"client.timeout exceeded while awaiting headers",
		"context deadline exceeded",
		"connection reset by peer",
		"tls handshake timeout",
		"temporary failure in name resolution",
		"i/o timeout",
		"unexpected eof",
	}
	for _, hint := range retryableErrHints {
		if strings.Contains(errMsg, hint) {
			return true
		}
	}
	return false
}

func (o *OpenAi) newAIClient() *resty.Client {
	return o.newAIClientWithProxy(true)
}

func (o *OpenAi) newAIClientWithProxy(enableProxy bool) *resty.Client {
	timeoutSeconds := o.requestTimeoutSeconds()
	client := resty.New()
	client.SetBaseURL(strutil.Trim(o.BaseUrl))
	client.SetHeader("Authorization", "Bearer "+o.ApiKey)
	client.SetHeader("Content-Type", "application/json")
	client.SetTimeout(time.Duration(timeoutSeconds) * time.Second)
	if !o.DisableRequestRetries {
		client.SetRetryCount(2)
		client.SetRetryWaitTime(1 * time.Second)
		client.SetRetryMaxWaitTime(6 * time.Second)
		client.AddRetryCondition(func(r *resty.Response, err error) bool {
			if shouldRetryAIRequest(err) {
				return true
			}
			if r == nil {
				return false
			}
			statusCode := r.StatusCode()
			return statusCode == 408 || statusCode == 429 || statusCode == 500 || statusCode == 502 || statusCode == 503 || statusCode == 504
		})
	}
	if enableProxy && o.HttpProxyEnabled && o.HttpProxy != "" {
		client.SetProxy(o.HttpProxy)
	}
	return client
}

func isProxyConnRefused(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	// 常见形态：
	// proxyconnect tcp: dial tcp 127.0.0.1:7890: connect: connection refused
	return strings.Contains(msg, "proxyconnect tcp") && strings.Contains(msg, "connection refused")
}

func (o *OpenAi) formatAIRequestError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	lowerMsg := strings.ToLower(msg)
	if strings.Contains(lowerMsg, "client.timeout exceeded while awaiting headers") || strings.Contains(lowerMsg, "context deadline exceeded") {
		return fmt.Sprintf("%s。请求在 %d 秒内未收到模型服务响应头，请将 Timeout 提高到 180-600 秒后重试，或检查该接口与代理连通性。", msg, o.requestTimeoutSeconds())
	}
	return msg
}

func (o *OpenAi) newAnthropicClient() *resty.Client {
	client := resty.New()
	client.SetBaseURL(strutil.Trim(o.BaseUrl))
	client.SetHeader("x-api-key", o.ApiKey)
	client.SetHeader("anthropic-version", "2023-06-01")
	client.SetHeader("Content-Type", "application/json")
	client.SetTimeout(time.Duration(o.requestTimeoutSeconds()) * time.Second)
	if !o.DisableRequestRetries {
		client.SetRetryCount(2)
		client.SetRetryWaitTime(1 * time.Second)
		client.SetRetryMaxWaitTime(6 * time.Second)
		client.AddRetryCondition(func(r *resty.Response, err error) bool {
			if shouldRetryAIRequest(err) {
				return true
			}
			if r == nil {
				return false
			}
			statusCode := r.StatusCode()
			return statusCode == 408 || statusCode == 429 || statusCode == 500 || statusCode == 502 || statusCode == 503 || statusCode == 504
		})
	}
	if o.HttpProxyEnabled && o.HttpProxy != "" {
		client.SetProxy(o.HttpProxy)
	}
	return client
}

func (o *OpenAi) newAnthropicClientWithProxy(enableProxy bool) *resty.Client {
	client := o.newAnthropicClient()
	if !enableProxy {
		client.RemoveProxy()
	}
	return client
}

func (o *OpenAi) newResearchAIClientWithProxy(enableProxy bool) *resty.Client {
	client := o.newAIClientWithProxy(enableProxy)
	client.SetTimeout(0)
	client.SetRetryCount(0)
	return client
}

func (o *OpenAi) newResearchAnthropicClientWithProxy(enableProxy bool) *resty.Client {
	client := o.newAnthropicClientWithProxy(enableProxy)
	client.SetTimeout(0)
	client.SetRetryCount(0)
	return client
}

func emitAIStreamContent(ch chan map[string]any, question, chatID, model, content string) {
	if content == "" {
		return
	}
	if content == "###" || content == "##" || content == "#" {
		content = "\r\n" + content
	}
	ch <- map[string]any{
		"code":     1,
		"question": question,
		"chatId":   chatID,
		"model":    model,
		"content":  content,
		"time":     time.Now().Format(time.DateTime),
	}
}

func emitAIStreamError(ch chan map[string]any, question, content string) {
	ch <- map[string]any{
		"code":     0,
		"question": question,
		"content":  content,
	}
}

func parseAIHTTPError(statusCode int, body []byte) string {
	bodyText := strings.TrimSpace(string(body))
	if bodyText != "" {
		res := &models.Resp{}
		if err := json.Unmarshal(body, res); err == nil {
			if msg := strings.TrimSpace(res.Error.Message); msg != "" {
				return msg
			}
			if msg := strings.TrimSpace(res.Message); msg != "" {
				return msg
			}
		}
		var generic struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
			} `json:"error"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(body, &generic); err == nil {
			if msg := strings.TrimSpace(generic.Error.Message); msg != "" {
				return msg
			}
			if msg := strings.TrimSpace(generic.Message); msg != "" {
				return msg
			}
		}
		return bodyText
	}
	if statusCode > 0 {
		return fmt.Sprintf("model provider returned status %d", statusCode)
	}
	return "empty response from model provider"
}

func messageContentText(content any) string {
	switch v := content.(type) {
	case string:
		return strings.TrimSpace(v)
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if itemMap, ok := item.(map[string]any); ok {
				if text := strings.TrimSpace(convertor.ToString(itemMap["text"])); text != "" {
					parts = append(parts, text)
				}
				continue
			}
			if text := strings.TrimSpace(convertor.ToString(item)); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return strings.TrimSpace(convertor.ToString(v))
	}
}

func splitSystemAndDialogMessages(messages []map[string]interface{}) (string, []map[string]any) {
	systemParts := make([]string, 0)
	dialog := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		role := strings.ToLower(strings.TrimSpace(convertor.ToString(msg["role"])))
		content := messageContentText(msg["content"])
		if content == "" {
			continue
		}
		switch role {
		case "system", "developer":
			systemParts = append(systemParts, content)
		case "assistant":
			dialog = append(dialog, map[string]any{"role": "assistant", "content": content})
		default:
			dialog = append(dialog, map[string]any{"role": "user", "content": content})
		}
	}
	if len(dialog) == 0 {
		dialog = append(dialog, map[string]any{"role": "user", "content": "请继续"})
	}
	dialog = mergeAdjacentRoleMessages(dialog)
	if len(dialog) > 0 && dialog[0]["role"] == "assistant" {
		dialog = append([]map[string]any{{"role": "user", "content": "请继续"}}, dialog...)
	}
	return strings.Join(systemParts, "\n\n"), dialog
}

func mergeAdjacentRoleMessages(messages []map[string]any) []map[string]any {
	result := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		role := strings.TrimSpace(convertor.ToString(msg["role"]))
		content := strings.TrimSpace(convertor.ToString(msg["content"]))
		if role == "" || content == "" {
			continue
		}
		if len(result) > 0 && result[len(result)-1]["role"] == role {
			prev := strings.TrimSpace(convertor.ToString(result[len(result)-1]["content"]))
			result[len(result)-1]["content"] = strings.TrimSpace(prev + "\n\n" + content)
			continue
		}
		result = append(result, map[string]any{"role": role, "content": content})
	}
	return result
}

func (o *OpenAi) openAIResponsesBody(messages []map[string]interface{}, stream bool) map[string]any {
	return o.openAIResponsesBodyWithPrevious(messages, stream, "")
}

func (o *OpenAi) openAIResponsesBodyWithPrevious(messages []map[string]interface{}, stream bool, previousResponseID string) map[string]any {
	system, dialog := splitSystemAndDialogMessages(messages)
	bodyMap := map[string]any{
		"model":             o.Model,
		"max_output_tokens": o.MaxTokens,
		"temperature":       o.Temperature,
		"stream":            stream,
		"input":             dialog,
	}
	if system != "" {
		bodyMap["instructions"] = system
	}
	if strings.TrimSpace(previousResponseID) != "" {
		bodyMap["previous_response_id"] = strings.TrimSpace(previousResponseID)
	}
	return bodyMap
}

func (o *OpenAi) anthropicMessagesBody(messages []map[string]interface{}, stream bool) map[string]any {
	system, dialog := splitSystemAndDialogMessages(messages)
	bodyMap := map[string]any{
		"model":       o.Model,
		"max_tokens":  o.MaxTokens,
		"temperature": o.Temperature,
		"stream":      stream,
		"messages":    dialog,
	}
	if system != "" {
		bodyMap["system"] = system
	}
	return bodyMap
}

func readErrorResponseBody(resp *resty.Response) []byte {
	if resp == nil {
		return nil
	}
	if rawBody := resp.RawBody(); rawBody != nil {
		defer rawBody.Close()
		body, _ := io.ReadAll(rawBody)
		return body
	}
	return resp.Body()
}

func askAiOpenAIResponses(o *OpenAi, messages []map[string]interface{}, ch chan map[string]any, question string) {
	client := o.newAIClient()
	bodyMap := o.openAIResponsesBody(messages, true)
	resp, err := client.R().
		SetDoNotParseResponse(true).
		SetBody(bodyMap).
		Post("/responses")
	if err != nil && o.HttpProxyEnabled && o.HttpProxy != "" && isProxyConnRefused(err) {
		resp, err = o.newAIClientWithProxy(false).R().
			SetDoNotParseResponse(true).
			SetBody(bodyMap).
			Post("/responses")
	}
	if err != nil {
		logger.SugaredLogger.Infof("Responses stream error : %s, baseUrl:%s, timeout:%ds", err.Error(), strutil.Trim(o.BaseUrl), o.requestTimeoutSeconds())
		emitAIStreamError(ch, question, o.formatAIRequestError(err))
		return
	}
	if resp == nil {
		emitAIStreamError(ch, question, "empty response from model provider")
		return
	}
	if resp.IsError() {
		emitAIStreamError(ch, question, parseAIHTTPError(resp.StatusCode(), readErrorResponseBody(resp)))
		return
	}

	body := resp.RawBody()
	defer body.Close()
	scanner := bufio.NewScanner(body)
	chatID := ""
	model := o.Model
	for scanner.Scan() {
		line := scanner.Text()
		logger.SugaredLogger.Infof("Received responses data: %s", line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strutil.Trim(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var event struct {
			Type     string `json:"type"`
			Delta    string `json:"delta"`
			Response struct {
				ID    string `json:"id"`
				Model string `json:"model"`
			} `json:"response"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			logger.SugaredLogger.Infof("Responses stream data error : %s", err.Error())
			emitAIStreamError(ch, question, err.Error())
			continue
		}
		if event.Response.ID != "" {
			chatID = event.Response.ID
		}
		if event.Response.Model != "" {
			model = event.Response.Model
		}
		if event.Type == "response.output_text.delta" && event.Delta != "" {
			emitAIStreamContent(ch, question, chatID, model, event.Delta)
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		logger.SugaredLogger.Infof("Responses stream scanner error : %s", scanErr.Error())
		emitAIStreamError(ch, question, o.formatAIRequestError(scanErr))
	}
}

func askAiAnthropicMessages(o *OpenAi, messages []map[string]interface{}, ch chan map[string]any, question string) {
	client := o.newAnthropicClient()
	bodyMap := o.anthropicMessagesBody(messages, true)
	resp, err := client.R().
		SetDoNotParseResponse(true).
		SetBody(bodyMap).
		Post("/messages")
	if err != nil && o.HttpProxyEnabled && o.HttpProxy != "" && isProxyConnRefused(err) {
		resp, err = o.newAnthropicClientWithProxy(false).R().
			SetDoNotParseResponse(true).
			SetBody(bodyMap).
			Post("/messages")
	}
	if err != nil {
		logger.SugaredLogger.Infof("Anthropic stream error : %s, baseUrl:%s, timeout:%ds", err.Error(), strutil.Trim(o.BaseUrl), o.requestTimeoutSeconds())
		emitAIStreamError(ch, question, o.formatAIRequestError(err))
		return
	}
	if resp == nil {
		emitAIStreamError(ch, question, "empty response from model provider")
		return
	}
	if resp.IsError() {
		emitAIStreamError(ch, question, parseAIHTTPError(resp.StatusCode(), readErrorResponseBody(resp)))
		return
	}

	body := resp.RawBody()
	defer body.Close()
	scanner := bufio.NewScanner(body)
	chatID := ""
	model := o.Model
	for scanner.Scan() {
		line := scanner.Text()
		logger.SugaredLogger.Infof("Received anthropic data: %s", line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strutil.Trim(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var event struct {
			Type    string `json:"type"`
			Message struct {
				ID    string `json:"id"`
				Model string `json:"model"`
			} `json:"message"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			logger.SugaredLogger.Infof("Anthropic stream data error : %s", err.Error())
			emitAIStreamError(ch, question, err.Error())
			continue
		}
		if event.Message.ID != "" {
			chatID = event.Message.ID
		}
		if event.Message.Model != "" {
			model = event.Message.Model
		}
		if event.Type == "content_block_delta" && event.Delta.Text != "" {
			emitAIStreamContent(ch, question, chatID, model, event.Delta.Text)
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		logger.SugaredLogger.Infof("Anthropic stream scanner error : %s", scanErr.Error())
		emitAIStreamError(ch, question, o.formatAIRequestError(scanErr))
	}
}

func (o *OpenAi) completeOpenAIResponses(messages []map[string]any) (string, string, string, error) {
	return o.completeOpenAIResponsesWithContext(context.Background(), messages, "")
}

func (o *OpenAi) completeOpenAIResponsesWithContext(ctx context.Context, messages []map[string]any, previousResponseID string) (string, string, string, error) {
	interfaceMessages := make([]map[string]interface{}, 0, len(messages))
	for _, msg := range messages {
		interfaceMessages = append(interfaceMessages, map[string]interface{}(msg))
	}
	body := o.openAIResponsesBodyWithPrevious(interfaceMessages, false, previousResponseID)
	resp, err := o.newAIClient().R().SetContext(ctx).SetBody(body).Post("/responses")
	if err != nil && o.HttpProxyEnabled && o.HttpProxy != "" && isProxyConnRefused(err) {
		resp, err = o.newAIClientWithProxy(false).R().SetContext(ctx).SetBody(body).Post("/responses")
	}
	if err != nil {
		return "", "", "", err
	}
	if resp == nil {
		return "", "", "", errors.New("empty response from model provider")
	}
	if resp.IsError() {
		return "", "", "", errors.New(parseAIHTTPError(resp.StatusCode(), resp.Body()))
	}
	var result struct {
		ID         string `json:"id"`
		Model      string `json:"model"`
		OutputText string `json:"output_text"`
		Output     []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(resp.Body(), &result); err != nil {
		return "", "", "", err
	}
	content := strings.TrimSpace(result.OutputText)
	if content == "" {
		parts := make([]string, 0)
		for _, item := range result.Output {
			for _, block := range item.Content {
				if text := strings.TrimSpace(block.Text); text != "" {
					parts = append(parts, text)
				}
			}
		}
		content = strings.TrimSpace(strings.Join(parts, "\n"))
	}
	if content == "" {
		return "", result.ID, result.Model, errors.New("empty content from model provider")
	}
	return content, result.ID, result.Model, nil
}

func providerHTTPError(statusCode int, body []byte) error {
	retryable := statusCode == 408 || statusCode == 429 || statusCode >= 500
	return &aicontract.ProviderCallError{
		Category: "http_error", StatusCode: statusCode,
		Message: parseAIHTTPError(statusCode, body), Retryable: retryable,
	}
}

func providerProtocolError(category, message, lastEvent string, retryable bool) error {
	return &aicontract.ProviderCallError{Category: category, Message: message, Retryable: retryable, LastEventType: lastEvent}
}

func providerStreamError(message, lastEvent string) error {
	lower := strings.ToLower(message)
	retryable := true
	for _, marker := range []string{
		"authentication", "unauthorized", "invalid api key", "permission", "forbidden",
		"invalid_request", "invalid request", "model not found", "does not exist", "unsupported model",
		"max_output_tokens", "context_length", "context length", "maximum context",
	} {
		if strings.Contains(lower, marker) {
			retryable = false
			break
		}
	}
	return providerProtocolError("provider_error", message, lastEvent, retryable)
}

type sseFrame struct {
	Event string
	Data  string
}

func scanSSE(ctx context.Context, body io.Reader, onFrame func(sseFrame) error, onHeartbeat func()) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var eventName string
	dataLines := make([]string, 0, 1)
	flush := func() error {
		if len(dataLines) == 0 {
			if eventName != "" && onHeartbeat != nil {
				onHeartbeat()
			}
			eventName = ""
			return nil
		}
		if strings.TrimSpace(strings.Join(dataLines, "\n")) == "" {
			if onHeartbeat != nil {
				onHeartbeat()
			}
			eventName = ""
			dataLines = dataLines[:0]
			return nil
		}
		frame := sseFrame{Event: eventName, Data: strings.Join(dataLines, "\n")}
		eventName = ""
		dataLines = dataLines[:0]
		return onFrame(frame)
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			if onHeartbeat != nil {
				onHeartbeat()
			}
			continue
		}
		name, value, found := strings.Cut(line, ":")
		if !found {
			name, value = line, ""
		}
		value = strings.TrimPrefix(value, " ")
		switch name {
		case "event":
			eventName = value
		case "data":
			dataLines = append(dataLines, value)
		case "id", "retry":
			// Valid SSE metadata; it does not prove model activity by itself.
		default:
			return providerProtocolError("protocol_error", "模型服务返回了非 SSE 流内容", eventName, false)
		}
	}
	if err := flush(); err != nil {
		return err
	}
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	return scanner.Err()
}

func streamResponseOutputText(raw json.RawMessage) (string, string, string) {
	var response struct {
		ID         string `json:"id"`
		Model      string `json:"model"`
		OutputText string `json:"output_text"`
		Output     []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &response) != nil {
		return "", "", ""
	}
	parts := make([]string, 0)
	if strings.TrimSpace(response.OutputText) != "" {
		parts = append(parts, response.OutputText)
	}
	for _, output := range response.Output {
		for _, block := range output.Content {
			if strings.TrimSpace(block.Text) != "" {
				parts = append(parts, block.Text)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n")), response.ID, response.Model
}

func streamEventError(raw json.RawMessage) string {
	var payload struct {
		Message string `json:"message"`
		Code    string `json:"code"`
		Error   struct {
			Message string `json:"message"`
			Code    string `json:"code"`
			Type    string `json:"type"`
		} `json:"error"`
		Response struct {
			Error struct {
				Message string `json:"message"`
				Code    string `json:"code"`
			} `json:"error"`
			IncompleteDetails struct {
				Reason string `json:"reason"`
			} `json:"incomplete_details"`
		} `json:"response"`
	}
	_ = json.Unmarshal(raw, &payload)
	for _, value := range []string{payload.Error.Message, payload.Response.Error.Message, payload.Response.IncompleteDetails.Reason, payload.Message, payload.Error.Code, payload.Response.Error.Code, payload.Code} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return "model provider reported a failed stream"
}

func emitResearchActivity(callback func(aicontract.StreamActivity), eventType, state string) {
	if callback != nil {
		callback(aicontract.StreamActivity{EventType: eventType, State: state})
	}
}

func (o *OpenAi) completeOpenAIResponsesStream(ctx context.Context, messages []map[string]any, previousResponseID string, activity func(aicontract.StreamActivity)) (string, string, string, error) {
	interfaceMessages := make([]map[string]interface{}, 0, len(messages))
	for _, msg := range messages {
		interfaceMessages = append(interfaceMessages, map[string]interface{}(msg))
	}
	bodyMap := o.openAIResponsesBodyWithPrevious(interfaceMessages, true, previousResponseID)
	request := func(enableProxy bool) (*resty.Response, error) {
		return o.newResearchAIClientWithProxy(enableProxy).R().SetContext(ctx).SetDoNotParseResponse(true).SetBody(bodyMap).Post("/responses")
	}
	resp, err := request(true)
	if err != nil && o.HttpProxyEnabled && o.HttpProxy != "" && isProxyConnRefused(err) {
		resp, err = request(false)
	}
	if err != nil {
		return "", "", "", err
	}
	if resp == nil {
		return "", "", "", providerProtocolError("empty_response", "empty response from model provider", "", true)
	}
	if resp.IsError() {
		return "", "", "", providerHTTPError(resp.StatusCode(), readErrorResponseBody(resp))
	}
	emitResearchActivity(activity, "response_headers", "waiting")
	body := resp.RawBody()
	if body == nil {
		return "", "", "", providerProtocolError("empty_response", "model provider returned no stream body", "response_headers", true)
	}
	defer body.Close()
	var content strings.Builder
	responseID, model, lastEvent := "", o.Model, "response_headers"
	terminal := false
	err = scanSSE(ctx, body, func(frame sseFrame) error {
		data := strings.TrimSpace(frame.Data)
		if data == "[DONE]" {
			lastEvent, terminal = "done", true
			emitResearchActivity(activity, lastEvent, "streaming")
			return nil
		}
		var event struct {
			Type     string          `json:"type"`
			Delta    string          `json:"delta"`
			Response json.RawMessage `json:"response"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return providerProtocolError("protocol_error", "Responses 流事件不是有效 JSON", lastEvent, false)
		}
		if event.Type == "" {
			event.Type = strings.TrimSpace(frame.Event)
		}
		if event.Type == "" {
			return providerProtocolError("protocol_error", "Responses 流事件缺少 type", lastEvent, false)
		}
		lastEvent = event.Type
		state := "streaming"
		if strings.Contains(event.Type, "reasoning") || event.Type == "response.in_progress" || event.Type == "response.created" {
			state = "reasoning"
		}
		emitResearchActivity(activity, event.Type, state)
		if finalText, id, responseModel := streamResponseOutputText(event.Response); id != "" || responseModel != "" || finalText != "" {
			if id != "" {
				responseID = id
			}
			if responseModel != "" {
				model = responseModel
			}
			if event.Type == "response.completed" && content.Len() == 0 && finalText != "" {
				content.WriteString(finalText)
			}
		}
		switch event.Type {
		case "response.output_text.delta":
			content.WriteString(event.Delta)
		case "response.completed":
			terminal = true
		case "response.failed", "response.incomplete", "error":
			return providerStreamError(streamEventError([]byte(data)), lastEvent)
		}
		return nil
	}, func() {
		lastEvent = "heartbeat"
		emitResearchActivity(activity, lastEvent, "reasoning")
	})
	if err != nil {
		return "", responseID, model, err
	}
	if !terminal {
		return "", responseID, model, providerProtocolError("stream_interrupted", "Responses 流在完成事件前中断", lastEvent, true)
	}
	result := strings.TrimSpace(content.String())
	if result == "" {
		return "", responseID, model, providerProtocolError("empty_output", "Responses 流已完成但没有正文", lastEvent, false)
	}
	return result, responseID, model, nil
}

func (o *OpenAi) completeChatCompletionsStream(ctx context.Context, messages []map[string]any, activity func(aicontract.StreamActivity)) (string, string, string, error) {
	bodyMap := map[string]any{"model": o.Model, "max_tokens": o.MaxTokens, "temperature": o.Temperature, "stream": true, "messages": messages}
	request := func(enableProxy bool) (*resty.Response, error) {
		return o.newResearchAIClientWithProxy(enableProxy).R().SetContext(ctx).SetDoNotParseResponse(true).SetBody(bodyMap).Post("/chat/completions")
	}
	resp, err := request(true)
	if err != nil && o.HttpProxyEnabled && o.HttpProxy != "" && isProxyConnRefused(err) {
		resp, err = request(false)
	}
	if err != nil {
		return "", "", "", err
	}
	if resp == nil {
		return "", "", "", providerProtocolError("empty_response", "empty response from model provider", "", true)
	}
	if resp.IsError() {
		return "", "", "", providerHTTPError(resp.StatusCode(), readErrorResponseBody(resp))
	}
	emitResearchActivity(activity, "response_headers", "waiting")
	body := resp.RawBody()
	if body == nil {
		return "", "", "", providerProtocolError("empty_response", "model provider returned no stream body", "response_headers", true)
	}
	defer body.Close()
	var content strings.Builder
	responseID, model, lastEvent := "", o.Model, "response_headers"
	terminal := false
	err = scanSSE(ctx, body, func(frame sseFrame) error {
		data := strings.TrimSpace(frame.Data)
		if data == "[DONE]" {
			lastEvent, terminal = "done", true
			emitResearchActivity(activity, lastEvent, "streaming")
			return nil
		}
		var event struct {
			ID      string          `json:"id"`
			Model   string          `json:"model"`
			Error   json.RawMessage `json:"error"`
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return providerProtocolError("protocol_error", "Chat Completions 流事件不是有效 JSON", lastEvent, false)
		}
		if len(event.Error) > 0 && string(event.Error) != "null" {
			return providerStreamError(streamEventError([]byte(data)), lastEvent)
		}
		if event.ID != "" {
			responseID = event.ID
		}
		if event.Model != "" {
			model = event.Model
		}
		lastEvent = "chat.completion.chunk"
		state := "streaming"
		for _, choice := range event.Choices {
			if choice.Delta.ReasoningContent != "" {
				state = "reasoning"
			}
			content.WriteString(choice.Delta.Content)
			if choice.FinishReason != "" {
				terminal = true
				lastEvent = "finish_reason:" + choice.FinishReason
			}
		}
		emitResearchActivity(activity, lastEvent, state)
		return nil
	}, func() {
		lastEvent = "heartbeat"
		emitResearchActivity(activity, lastEvent, "reasoning")
	})
	if err != nil {
		return "", responseID, model, err
	}
	if !terminal {
		return "", responseID, model, providerProtocolError("stream_interrupted", "Chat Completions 流在完成事件前中断", lastEvent, true)
	}
	result := strings.TrimSpace(content.String())
	if result == "" {
		return "", responseID, model, providerProtocolError("empty_output", "Chat Completions 流已完成但没有正文", lastEvent, false)
	}
	return result, responseID, model, nil
}

func (o *OpenAi) completeAnthropicMessagesStream(ctx context.Context, messages []map[string]any, activity func(aicontract.StreamActivity)) (string, string, string, error) {
	interfaceMessages := make([]map[string]interface{}, 0, len(messages))
	for _, msg := range messages {
		interfaceMessages = append(interfaceMessages, map[string]interface{}(msg))
	}
	bodyMap := o.anthropicMessagesBody(interfaceMessages, true)
	request := func(enableProxy bool) (*resty.Response, error) {
		return o.newResearchAnthropicClientWithProxy(enableProxy).R().SetContext(ctx).SetDoNotParseResponse(true).SetBody(bodyMap).Post("/messages")
	}
	resp, err := request(true)
	if err != nil && o.HttpProxyEnabled && o.HttpProxy != "" && isProxyConnRefused(err) {
		resp, err = request(false)
	}
	if err != nil {
		return "", "", "", err
	}
	if resp == nil {
		return "", "", "", providerProtocolError("empty_response", "empty response from model provider", "", true)
	}
	if resp.IsError() {
		return "", "", "", providerHTTPError(resp.StatusCode(), readErrorResponseBody(resp))
	}
	emitResearchActivity(activity, "response_headers", "waiting")
	body := resp.RawBody()
	if body == nil {
		return "", "", "", providerProtocolError("empty_response", "model provider returned no stream body", "response_headers", true)
	}
	defer body.Close()
	var content strings.Builder
	responseID, model, lastEvent := "", o.Model, "response_headers"
	terminal := false
	err = scanSSE(ctx, body, func(frame sseFrame) error {
		data := strings.TrimSpace(frame.Data)
		if data == "[DONE]" {
			lastEvent, terminal = "done", true
			emitResearchActivity(activity, lastEvent, "streaming")
			return nil
		}
		var event struct {
			Type    string `json:"type"`
			Message struct {
				ID    string `json:"id"`
				Model string `json:"model"`
			} `json:"message"`
			Delta struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				Thinking string `json:"thinking"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return providerProtocolError("protocol_error", "Anthropic 流事件不是有效 JSON", lastEvent, false)
		}
		if event.Type == "" {
			event.Type = strings.TrimSpace(frame.Event)
		}
		if event.Type == "" {
			return providerProtocolError("protocol_error", "Anthropic 流事件缺少 type", lastEvent, false)
		}
		lastEvent = event.Type
		if event.Message.ID != "" {
			responseID = event.Message.ID
		}
		if event.Message.Model != "" {
			model = event.Message.Model
		}
		state := "streaming"
		if event.Type == "ping" || event.Delta.Thinking != "" || strings.Contains(event.Delta.Type, "thinking") {
			state = "reasoning"
		}
		emitResearchActivity(activity, event.Type, state)
		switch event.Type {
		case "content_block_delta":
			content.WriteString(event.Delta.Text)
		case "message_stop":
			terminal = true
		case "error":
			return providerStreamError(streamEventError([]byte(data)), lastEvent)
		}
		return nil
	}, func() {
		lastEvent = "heartbeat"
		emitResearchActivity(activity, lastEvent, "reasoning")
	})
	if err != nil {
		return "", responseID, model, err
	}
	if !terminal {
		return "", responseID, model, providerProtocolError("stream_interrupted", "Anthropic 流在完成事件前中断", lastEvent, true)
	}
	result := strings.TrimSpace(content.String())
	if result == "" {
		return "", responseID, model, providerProtocolError("empty_output", "Anthropic 流已完成但没有正文", lastEvent, false)
	}
	return result, responseID, model, nil
}

// CompleteResearchStream performs an activity-observable streaming request for
// research. The orchestrator owns the inactivity timer and retry ordering.
func (o *OpenAi) CompleteResearchStream(ctx context.Context, messages []map[string]any, previousResponseID string, activity func(aicontract.StreamActivity)) (string, string, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	switch NormalizeAIAPIProtocol(o.ApiProtocol) {
	case AIAPIProtocolOpenAIResponses:
		return o.completeOpenAIResponsesStream(ctx, messages, previousResponseID, activity)
	case AIAPIProtocolAnthropicMessage:
		return o.completeAnthropicMessagesStream(ctx, messages, activity)
	default:
		return o.completeChatCompletionsStream(ctx, messages, activity)
	}
}

// CompleteResearch is retained for non-research callers and compatibility
// tests. The 1.6.5 research workflow uses CompleteResearchStream.
func (o *OpenAi) CompleteResearch(ctx context.Context, messages []map[string]any, previousResponseID string) (string, string, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	switch NormalizeAIAPIProtocol(o.ApiProtocol) {
	case AIAPIProtocolOpenAIResponses:
		return o.completeOpenAIResponsesWithContext(ctx, messages, previousResponseID)
	case AIAPIProtocolAnthropicMessage:
		return o.completeAnthropicMessages(ctx, messages)
	default:
		return o.completeChatCompletions(ctx, messages)
	}
}

func (o *OpenAi) CompleteChat(messages []map[string]any, _ bool) (string, string, string, error) {
	return o.CompleteResearch(o.ctx, messages, "")
}

func (o *OpenAi) completeChatCompletions(ctx context.Context, messages []map[string]any) (string, string, string, error) {
	body := map[string]any{"model": o.Model, "max_tokens": o.MaxTokens, "temperature": o.Temperature, "stream": false, "messages": messages}
	resp, err := o.newAIClient().R().SetContext(ctx).SetBody(body).Post("/chat/completions")
	if err != nil && o.HttpProxyEnabled && o.HttpProxy != "" && isProxyConnRefused(err) {
		resp, err = o.newAIClientWithProxy(false).R().SetContext(ctx).SetBody(body).Post("/chat/completions")
	}
	if err != nil {
		return "", "", "", err
	}
	if resp == nil {
		return "", "", "", errors.New("empty response from model provider")
	}
	if resp.IsError() {
		return "", "", "", errors.New(parseAIHTTPError(resp.StatusCode(), resp.Body()))
	}
	var result AiResponse
	if err := json.Unmarshal(resp.Body(), &result); err != nil {
		return "", "", "", err
	}
	if len(result.Choices) == 0 || strings.TrimSpace(result.Choices[0].Message.Content) == "" {
		return "", result.Id, result.Model, errors.New("empty content from model provider")
	}
	return strings.TrimSpace(result.Choices[0].Message.Content), result.Id, result.Model, nil
}

func (o *OpenAi) completeAnthropicMessages(ctx context.Context, messages []map[string]any) (string, string, string, error) {
	interfaceMessages := make([]map[string]interface{}, 0, len(messages))
	for _, msg := range messages {
		interfaceMessages = append(interfaceMessages, map[string]interface{}(msg))
	}
	resp, err := o.newAnthropicClient().R().SetContext(ctx).SetBody(o.anthropicMessagesBody(interfaceMessages, false)).Post("/messages")
	if err != nil && o.HttpProxyEnabled && o.HttpProxy != "" && isProxyConnRefused(err) {
		resp, err = o.newAnthropicClientWithProxy(false).R().SetContext(ctx).SetBody(o.anthropicMessagesBody(interfaceMessages, false)).Post("/messages")
	}
	if err != nil {
		return "", "", "", err
	}
	if resp == nil {
		return "", "", "", errors.New("empty response from model provider")
	}
	if resp.IsError() {
		return "", "", "", errors.New(parseAIHTTPError(resp.StatusCode(), resp.Body()))
	}
	var result struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(resp.Body(), &result); err != nil {
		return "", "", "", err
	}
	parts := make([]string, 0, len(result.Content))
	for _, block := range result.Content {
		if text := strings.TrimSpace(block.Text); text != "" {
			parts = append(parts, text)
		}
	}
	content := strings.TrimSpace(strings.Join(parts, "\n"))
	if content == "" {
		return "", result.ID, result.Model, errors.New("empty content from model provider")
	}
	return content, result.ID, result.Model, nil
}

func AskAi(o *OpenAi, messages []map[string]interface{}, ch chan map[string]any, question string, think bool) {
	switch NormalizeAIAPIProtocol(o.ApiProtocol) {
	case AIAPIProtocolOpenAIResponses:
		askAiOpenAIResponses(o, messages, ch, question)
		return
	case AIAPIProtocolAnthropicMessage:
		askAiAnthropicMessages(o, messages, ch, question)
		return
	}
	client := o.newAIClient()
	thinking := "disabled"
	if think {
		thinking = "enabled"
	}
	bodyMap := map[string]interface{}{
		"model":       o.Model,
		"max_tokens":  o.MaxTokens,
		"temperature": o.Temperature,
		"stream":      true,
		"messages":    messages,
	}
	if think {
		bodyMap["thinking"] = map[string]any{
			//"type": "disabled",
			//"type": "enabled",
			"type": thinking,
		}
	}

	resp, err := client.R().
		SetDoNotParseResponse(true).
		SetBody(bodyMap).
		Post("/chat/completions")
	if err != nil {
		// 如果用户配置了本地代理，但代理没启动，定时任务会大量失败。
		// 这里做一次无代理兜底重试，避免“启动次数少”其实只是被代理拦死。
		if o.HttpProxyEnabled && o.HttpProxy != "" && isProxyConnRefused(err) {
			clientNoProxy := o.newAIClientWithProxy(false)
			resp, err = clientNoProxy.R().
				SetDoNotParseResponse(true).
				SetBody(bodyMap).
				Post("/chat/completions")
		}
	}
	if err != nil {
		logger.SugaredLogger.Infof("Stream error : %s, baseUrl:%s, timeout:%ds", err.Error(), strutil.Trim(o.BaseUrl), o.requestTimeoutSeconds())
		//ch <- err.Error()
		ch <- map[string]any{
			"code":     0,
			"question": question,
			"content":  o.formatAIRequestError(err),
		}
		return
	}
	if resp == nil {
		ch <- map[string]any{
			"code":     0,
			"question": question,
			"content":  "empty response from model provider",
		}
		return
	}

	body := resp.RawBody()
	defer body.Close()
	//location, _ := time.LoadLocation("Asia/Shanghai")

	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		line := scanner.Text()
		logger.SugaredLogger.Infof("Received data: %s", line)
		if strings.HasPrefix(line, "data:") {
			data := strutil.Trim(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				return
			}

			var streamResponse struct {
				Id      string `json:"id"`
				Model   string `json:"model"`
				Choices []struct {
					Delta struct {
						Content          string `json:"content"`
						ReasoningContent string `json:"reasoning_content"`
					} `json:"delta"`
					FinishReason string `json:"finish_reason"`
				} `json:"choices"`
			}

			if err := json.Unmarshal([]byte(data), &streamResponse); err == nil {
				for _, choice := range streamResponse.Choices {
					if content := choice.Delta.Content; content != "" {
						//ch <- content
						if content == "###" || content == "##" || content == "#" {
							ch <- map[string]any{
								"code":     1,
								"question": question,
								"chatId":   streamResponse.Id,
								"model":    streamResponse.Model,
								"content":  "\r\n" + content,
								"time":     time.Now().Format(time.DateTime),
							}
						} else {
							ch <- map[string]any{
								"code":     1,
								"question": question,
								"chatId":   streamResponse.Id,
								"model":    streamResponse.Model,
								"content":  content,
								"time":     time.Now().Format(time.DateTime),
							}
						}

						//logger.SugaredLogger.Infof("Content data: %s", content)
					}
					if reasoningContent := choice.Delta.ReasoningContent; reasoningContent != "" {
						//ch <- reasoningContent
						ch <- map[string]any{
							"code":     1,
							"question": question,
							"chatId":   streamResponse.Id,
							"model":    streamResponse.Model,
							"content":  reasoningContent,
							"time":     time.Now().Format(time.DateTime),
						}

						//logger.SugaredLogger.Infof("ReasoningContent data: %s", reasoningContent)
					}
					if choice.FinishReason == "stop" {
						return
					}
				}
			} else {
				if err != nil {
					logger.SugaredLogger.Infof("Stream data error : %s", err.Error())
					//ch <- err.Error()
					ch <- map[string]any{
						"code":     0,
						"question": question,
						"content":  err.Error(),
					}
				} else {
					logger.SugaredLogger.Infof("Stream data error : %s", data)
					//ch <- data
					ch <- map[string]any{
						"code":     0,
						"question": question,
						"content":  data,
					}
				}
			}
		} else {
			if strutil.RemoveNonPrintable(line) != "" {
				logger.SugaredLogger.Infof("Stream data error : %s", line)
				res := &models.Resp{}
				if err := json.Unmarshal([]byte(line), res); err == nil {
					//ch <- line
					msg := res.Message
					if res.Error.Message != "" {
						msg = res.Error.Message
					}
					ch <- map[string]any{
						"code":     0,
						"question": question,
						"content":  msg,
					}
				}
			}

		}

	}
	if scanErr := scanner.Err(); scanErr != nil {
		logger.SugaredLogger.Infof("Stream scanner error : %s", scanErr.Error())
		ch <- map[string]any{
			"code":     0,
			"question": question,
			"content":  o.formatAIRequestError(scanErr),
		}
	}
}
func AskAiWithTools(o *OpenAi, messages []map[string]interface{}, ch chan map[string]any, question string, tools []models.Tool, thinkingMode bool) {
	if NormalizeAIAPIProtocol(o.ApiProtocol) != AIAPIProtocolChatCompletions {
		emitAIStreamError(ch, question, "当前协议暂不支持工具调用，请切换到 Chat Completions 或关闭工具模式")
		return
	}
	bytes, _ := json.Marshal(messages)
	logger.SugaredLogger.Debugf("Stream request: \n%s\n", string(bytes))

	client := o.newAIClient()
	thinking := "disabled"
	if thinkingMode {
		thinking = "enabled"
	}
	bodyMap := map[string]interface{}{
		"model":       o.Model,
		"max_tokens":  o.MaxTokens,
		"temperature": o.Temperature,
		"stream":      true,
		"messages":    messages,
		"tools":       tools,
	}
	if thinkingMode {
		bodyMap["thinking"] = map[string]any{
			//"type": "disabled",
			//"type": "enabled",
			"type": thinking,
		}
	}

	resp, err := client.R().
		SetDoNotParseResponse(true).
		SetBody(bodyMap).
		Post("/chat/completions")
	if err != nil {
		if o.HttpProxyEnabled && o.HttpProxy != "" && isProxyConnRefused(err) {
			clientNoProxy := o.newAIClientWithProxy(false)
			resp, err = clientNoProxy.R().
				SetDoNotParseResponse(true).
				SetBody(bodyMap).
				Post("/chat/completions")
		}
	}
	if err != nil {
		logger.SugaredLogger.Infof("Stream error : %s, baseUrl:%s, timeout:%ds", err.Error(), strutil.Trim(o.BaseUrl), o.requestTimeoutSeconds())
		//ch <- err.Error()
		ch <- map[string]any{
			"code":     0,
			"question": question,
			"content":  o.formatAIRequestError(err),
		}
		return
	}
	if resp == nil {
		ch <- map[string]any{
			"code":     0,
			"question": question,
			"content":  "empty response from model provider",
		}
		return
	}

	body := resp.RawBody()
	defer body.Close()
	//location, _ := time.LoadLocation("Asia/Shanghai")

	scanner := bufio.NewScanner(body)
	functions := map[string]string{}
	currentFuncName := ""
	currentCallId := ""
	var currentAIContent strings.Builder
	var reasoningContentText strings.Builder
	var contentText strings.Builder

	for scanner.Scan() {
		line := scanner.Text()
		logger.SugaredLogger.Infof("Received data: %s", line)
		if strings.HasPrefix(line, "data:") {
			data := strutil.Trim(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				return
			}

			var streamResponse struct {
				Id      string `json:"id"`
				Model   string `json:"model"`
				Choices []struct {
					Delta struct {
						Content          string `json:"content"`
						ReasoningContent string `json:"reasoning_content"`
						Role             string `json:"role"`
						ToolCalls        []struct {
							Function struct {
								Arguments string `json:"arguments"`
								Name      string `json:"name"`
							} `json:"function"`
							Id    string `json:"id"`
							Index int    `json:"index"`
							Type  string `json:"type"`
						} `json:"tool_calls"`
					} `json:"delta"`
					FinishReason string `json:"finish_reason"`
				} `json:"choices"`
			}

			if err := json.Unmarshal([]byte(data), &streamResponse); err == nil {
				for _, choice := range streamResponse.Choices {
					if content := choice.Delta.Content; content != "" {
						contentText.WriteString(content)
						//ch <- content
						//logger.SugaredLogger.Infof("Content data: %s", content)

						if content == "###" || content == "##" || content == "#" {
							currentAIContent.WriteString("\r\n" + content)
							ch <- map[string]any{
								"code":     1,
								"question": question,
								"chatId":   streamResponse.Id,
								"model":    streamResponse.Model,
								"content":  "\r\n" + content,
								"time":     time.Now().Format(time.DateTime),
							}
						} else {
							currentAIContent.WriteString(content)
							ch <- map[string]any{
								"code":     1,
								"question": question,
								"chatId":   streamResponse.Id,
								"model":    streamResponse.Model,
								"content":  content,
								"time":     time.Now().Format(time.DateTime),
							}
						}

					}
					if reasoningContent := choice.Delta.ReasoningContent; reasoningContent != "" {
						reasoningContentText.WriteString(reasoningContent)
						//ch <- reasoningContent
						ch <- map[string]any{
							"code":     1,
							"question": question,
							"chatId":   streamResponse.Id,
							"model":    streamResponse.Model,
							"content":  reasoningContent,
							"time":     time.Now().Format(time.DateTime),
						}

						//logger.SugaredLogger.Infof("ReasoningContent data: %s", reasoningContent)
						currentAIContent.WriteString(reasoningContent)

					}
					if choice.Delta.ToolCalls != nil && len(choice.Delta.ToolCalls) > 0 {
						for _, call := range choice.Delta.ToolCalls {
							if call.Type != "function" {
								continue
							}
							if call.Function.Name != "" {
								currentFuncName = call.Function.Name
							}
							if call.Id != "" {
								currentCallId = call.Id
							}
							if currentFuncName == "" {
								continue
							}
							if _, ok := functions[currentFuncName]; !ok {
								functions[currentFuncName] = ""
							}
							functions[currentFuncName] += call.Function.Arguments
						}
					}

					if choice.FinishReason == "tool_calls" || (choice.FinishReason == "stop" && len(functions) > 0) {
						logger.SugaredLogger.Infof("functions: %+v", functions)
						for funcName, funcArguments := range functions {

							if funcName == "SearchBk" {
								words := gjson.Get(funcArguments, "words").String()
								ch <- map[string]any{
									"code":     1,
									"question": question,
									"chatId":   streamResponse.Id,
									"model":    streamResponse.Model,
									"content":  "\r\n```\r\n开始调用工具：SearchBk，\n参数：" + words + "\r\n```\r\n",
									"time":     time.Now().Format(time.DateTime),
								}

								content := "无符合条件的数据"

								res := NewSearchStockApi(words).SearchBk(random.RandInt(50, 120))
								if convertor.ToString(res["code"]) == "100" {
									resData := res["data"].(map[string]any)
									result := resData["result"].(map[string]any)
									dataList := result["dataList"].([]any)
									columns := result["columns"].([]any)
									headers := map[string]string{}
									for _, v := range columns {
										//logger.SugaredLogger.Infof("v:%+v", v)
										d := v.(map[string]any)
										//logger.SugaredLogger.Infof("key:%s title:%s dateMsg:%s unit:%s", d["key"], d["title"], d["dateMsg"], d["unit"])
										title := convertor.ToString(d["title"])
										if convertor.ToString(d["dateMsg"]) != "" {
											title = title + "[" + convertor.ToString(d["dateMsg"]) + "]"
										}
										if convertor.ToString(d["unit"]) != "" {
											title = title + "(" + convertor.ToString(d["unit"]) + ")"
										}
										headers[d["key"].(string)] = title
									}
									table := &[]map[string]any{}
									for _, v := range dataList {
										d := v.(map[string]any)
										tmp := map[string]any{}
										for key, title := range headers {
											tmp[title] = convertor.ToString(d[key])
										}
										*table = append(*table, tmp)
									}
									jsonData, _ := json.Marshal(*table)
									markdownTable, _ := JSONToMarkdownTable(jsonData)
									//logger.SugaredLogger.Infof("markdownTable=\n%s", markdownTable)
									content = "\r\n### 工具筛选出的相关板块/概念数据：\r\n" + markdownTable + "\r\n"
								}
								logger.SugaredLogger.Infof("SearchBk:words:%s  --> \n%s", words, content)

								messages = append(messages, map[string]interface{}{
									"role":              "assistant",
									"content":           currentAIContent.String(),
									"reasoning_content": reasoningContentText.String(),
									"tool_calls": []map[string]any{
										{
											"id":           currentCallId,
											"tool_call_id": currentCallId,
											"type":         "function",
											"function": map[string]string{
												"name":       funcName,
												"arguments":  funcArguments,
												"parameters": funcArguments,
											},
										},
									},
								})
								messages = append(messages, map[string]interface{}{
									"role":         "tool",
									"content":      content,
									"tool_call_id": currentCallId,
									//"reasoning_content": reasoningContentText.String(),
									//"tool_calls":        choice.Delta.ToolCalls,

								})
							}

							if funcName == "SearchETF" {
								words := gjson.Get(funcArguments, "words").String()
								ch <- map[string]any{
									"code":     1,
									"question": question,
									"chatId":   streamResponse.Id,
									"model":    streamResponse.Model,
									"content":  "\r\n```\r\n开始调用工具：SearchETF，\n参数：" + words + "\r\n```\r\n",
									"time":     time.Now().Format(time.DateTime),
								}

								content := "无符合条件的数据"

								res := NewSearchStockApi(words).SearchETF(random.RandInt(50, 120))
								if convertor.ToString(res["code"]) == "100" {
									resData := res["data"].(map[string]any)
									result := resData["result"].(map[string]any)
									dataList := result["dataList"].([]any)
									columns := result["columns"].([]any)
									headers := map[string]string{}
									for _, v := range columns {
										//logger.SugaredLogger.Infof("v:%+v", v)
										d := v.(map[string]any)
										//logger.SugaredLogger.Infof("key:%s title:%s dateMsg:%s unit:%s", d["key"], d["title"], d["dateMsg"], d["unit"])
										title := convertor.ToString(d["title"])
										if convertor.ToString(d["dateMsg"]) != "" {
											title = title + "[" + convertor.ToString(d["dateMsg"]) + "]"
										}
										if convertor.ToString(d["unit"]) != "" {
											title = title + "(" + convertor.ToString(d["unit"]) + ")"
										}
										headers[d["key"].(string)] = title
									}
									table := &[]map[string]any{}
									for _, v := range dataList {
										d := v.(map[string]any)
										tmp := map[string]any{}
										for key, title := range headers {
											tmp[title] = convertor.ToString(d[key])
										}
										*table = append(*table, tmp)
									}
									jsonData, _ := json.Marshal(*table)
									markdownTable, _ := JSONToMarkdownTable(jsonData)
									//logger.SugaredLogger.Infof("markdownTable=\n%s", markdownTable)
									content = "\r\n### 工具筛选出的相关ETF数据：\r\n" + markdownTable + "\r\n"
								}
								logger.SugaredLogger.Infof("SearchETF:words:%s  --> \n%s", words, content)

								messages = append(messages, map[string]interface{}{
									"role":              "assistant",
									"content":           currentAIContent.String(),
									"reasoning_content": reasoningContentText.String(),
									"tool_calls": []map[string]any{
										{
											"id":           currentCallId,
											"tool_call_id": currentCallId,
											"type":         "function",
											"function": map[string]string{
												"name":       funcName,
												"arguments":  funcArguments,
												"parameters": funcArguments,
											},
										},
									},
								})
								messages = append(messages, map[string]interface{}{
									"role":         "tool",
									"content":      content,
									"tool_call_id": currentCallId,
									//"reasoning_content": reasoningContentText.String(),
									//"tool_calls":        choice.Delta.ToolCalls,

								})
							}

							if funcName == "SearchStockByIndicators" {
								words := gjson.Get(funcArguments, "words").String()

								ch <- map[string]any{
									"code":     1,
									"question": question,
									"chatId":   streamResponse.Id,
									"model":    streamResponse.Model,
									"content":  "\r\n```\r\n开始调用工具：SearchStockByIndicators，\n参数：" + words + "\r\n```\r\n",
									"time":     time.Now().Format(time.DateTime),
								}

								content := "无符合条件的数据"
								res := NewSearchStockApi(words).SearchStock(random.RandInt(50, 120))
								if convertor.ToString(res["code"]) == "100" {
									resData := res["data"].(map[string]any)
									result := resData["result"].(map[string]any)
									dataList := result["dataList"].([]any)
									columns := result["columns"].([]any)
									headers := map[string]string{}
									for _, v := range columns {
										//logger.SugaredLogger.Infof("v:%+v", v)
										d := v.(map[string]any)
										//logger.SugaredLogger.Infof("key:%s title:%s dateMsg:%s unit:%s", d["key"], d["title"], d["dateMsg"], d["unit"])
										title := convertor.ToString(d["title"])
										if convertor.ToString(d["dateMsg"]) != "" {
											title = title + "[" + convertor.ToString(d["dateMsg"]) + "]"
										}
										if convertor.ToString(d["unit"]) != "" {
											title = title + "(" + convertor.ToString(d["unit"]) + ")"
										}
										headers[d["key"].(string)] = title
									}
									table := &[]map[string]any{}
									for _, v := range dataList {
										d := v.(map[string]any)
										tmp := map[string]any{}
										for key, title := range headers {
											tmp[title] = convertor.ToString(d[key])
										}
										*table = append(*table, tmp)
									}
									jsonData, _ := json.Marshal(*table)
									markdownTable, _ := JSONToMarkdownTable(jsonData)
									//logger.SugaredLogger.Infof("markdownTable=\n%s", markdownTable)
									content = "\r\n### 工具筛选出的相关股票数据：\r\n" + markdownTable + "\r\n"
								}
								logger.SugaredLogger.Infof("SearchStockByIndicators:words:%s  --> \n%s", words, content)

								messages = append(messages, map[string]interface{}{
									"role":              "assistant",
									"content":           currentAIContent.String(),
									"reasoning_content": reasoningContentText.String(),
									"tool_calls": []map[string]any{
										{
											"id":           currentCallId,
											"tool_call_id": currentCallId,
											"type":         "function",
											"function": map[string]string{
												"name":       funcName,
												"arguments":  funcArguments,
												"parameters": funcArguments,
											},
										},
									},
								})
								messages = append(messages, map[string]interface{}{
									"role":         "tool",
									"content":      content,
									"tool_call_id": currentCallId,
									//"reasoning_content": reasoningContentText.String(),
									//"tool_calls":        choice.Delta.ToolCalls,

								})

								//ch <- map[string]any{
								//	"code":     1,
								//	"question": question,
								//	"chatId":   streamResponse.Id,
								//	"model":    streamResponse.Model,
								//	"content":  "\r\n```\r\n调用工具：SearchStockByIndicators，\n结果：" + content + "\r\n```\r\n",
								//	"time":     time.Now().Format(time.DateTime),
								//}

							}

							if funcName == "GetStockKLine" {
								stockCode := gjson.Get(funcArguments, "stockCode").String()
								days := gjson.Get(funcArguments, "days").String()
								ch <- map[string]any{
									"code":     1,
									"question": question,
									"chatId":   streamResponse.Id,
									"model":    streamResponse.Model,
									"content":  "\r\n```\r\n开始调用工具：GetStockKLine，\n参数：" + stockCode + "," + days + "\r\n```\r\n",
									"time":     time.Now().Format(time.DateTime),
								}
								toIntDay, err := convertor.ToInt(days)
								if err != nil {
									toIntDay = 90
								}

								if strutil.HasPrefixAny(stockCode, []string{"sz", "sh", "hk", "us", "gb_"}) {
									K := &[]models.KLineData{}
									if strutil.HasPrefixAny(stockCode, []string{"sz", "sh"}) {
										K = NewStockDataApi().GetKLineData(stockCode, "240", o.KDays)
									}
									if strutil.HasPrefixAny(stockCode, []string{"hk", "us", "gb_"}) {
										K = NewStockDataApi().GetHK_KLineData(stockCode, "day", o.KDays)
									}
									Kmap := &[]map[string]any{}
									for _, kline := range *K {
										mapk := make(map[string]any, 6)
										mapk["日期"] = kline.Day
										mapk["开盘价"] = kline.Open
										mapk["最高价"] = kline.High
										mapk["最低价"] = kline.Low
										mapk["收盘价"] = kline.Close
										Volume, _ := convertor.ToFloat(kline.Volume)
										mapk["成交量(万手)"] = Volume / 10000.00 / 100.00
										*Kmap = append(*Kmap, mapk)
									}
									jsonData, _ := json.Marshal(Kmap)
									markdownTable, _ := JSONToMarkdownTable(jsonData)
									logger.SugaredLogger.Infof("getKLineData=\n%s", markdownTable)

									messages = append(messages, map[string]interface{}{
										"role":              "assistant",
										"content":           currentAIContent.String(),
										"reasoning_content": reasoningContentText.String(),
										"tool_calls": []map[string]any{
											{
												"id":           currentCallId,
												"tool_call_id": currentCallId,
												"type":         "function",
												"function": map[string]string{
													"name":       funcName,
													"arguments":  funcArguments,
													"parameters": funcArguments,
												},
											},
										},
									})
									res := "\r\n ### " + stockCode + convertor.ToString(toIntDay) + "日K线数据：\r\n" + markdownTable + "\r\n"
									messages = append(messages, map[string]interface{}{
										"role":         "tool",
										"content":      res,
										"tool_call_id": currentCallId,
										//"reasoning_content": reasoningContentText.String(),
										//"tool_calls":        choice.Delta.ToolCalls,
									})
									logger.SugaredLogger.Infof("GetStockKLine:stockCode:%s days:%s --> \n%s", stockCode, days, res)

									//ch <- map[string]any{
									//	"code":     1,
									//	"question": question,
									//	"chatId":   streamResponse.Id,
									//	"model":    streamResponse.Model,
									//	"content":  "\r\n```\r\n调用工具：GetStockKLine，\n结果：" + res + "\r\n```\r\n",
									//	"time":     time.Now().Format(time.DateTime),
									//}
								} else {
									messages = append(messages, map[string]interface{}{
										"role":              "assistant",
										"content":           currentAIContent.String(),
										"reasoning_content": reasoningContentText.String(),
										"tool_calls": []map[string]any{
											{
												"id":           currentCallId,
												"tool_call_id": currentCallId,
												"type":         "function",
												"function": map[string]string{
													"name":       funcName,
													"arguments":  funcArguments,
													"parameters": funcArguments,
												},
											},
										},
									})
									messages = append(messages, map[string]interface{}{
										"role":         "tool",
										"content":      "无数据，可能股票代码错误。（A股：sh,sz开头;港股hk开头,美股：us开头）",
										"tool_call_id": currentCallId,
										//"reasoning_content": reasoningContentText.String(),
										//"tool_calls":        choice.Delta.ToolCalls,
									})
								}
							}

							if funcName == "InteractiveAnswer" {
								page := gjson.Get(funcArguments, "page").String()
								pageSize := gjson.Get(funcArguments, "pageSize").String()
								keyWord := gjson.Get(funcArguments, "keyWord").String()
								ch <- map[string]any{
									"code":     1,
									"question": question,
									"chatId":   streamResponse.Id,
									"model":    streamResponse.Model,
									"content":  "\r\n```\r\n开始调用工具：InteractiveAnswer，\n参数：" + page + "," + pageSize + "," + keyWord + "\r\n```\r\n",
									"time":     time.Now().Format(time.DateTime),
								}
								pageNo, err := convertor.ToInt(page)
								if err != nil {
									pageNo = 1
								}
								pageSizeNum, err := convertor.ToInt(pageSize)
								if err != nil {
									pageSizeNum = 50
								}
								datas := NewMarketNewsApi().InteractiveAnswer(int(pageNo), int(pageSizeNum), keyWord)
								content := util.MarkdownTableWithTitle("投资互动数据", datas.Results)
								logger.SugaredLogger.Infof("InteractiveAnswer=\n%s", content)
								messages = append(messages, map[string]interface{}{
									"role":              "assistant",
									"content":           currentAIContent.String(),
									"reasoning_content": reasoningContentText.String(),
									"tool_calls": []map[string]any{
										{
											"id":           currentCallId,
											"tool_call_id": currentCallId,
											"type":         "function",
											"function": map[string]string{
												"name":       funcName,
												"arguments":  funcArguments,
												"parameters": funcArguments,
											},
										},
									},
								})
								messages = append(messages, map[string]interface{}{
									"role":         "tool",
									"content":      content,
									"tool_call_id": currentCallId,
									//"reasoning_content": reasoningContentText.String(),
									//"tool_calls":        choice.Delta.ToolCalls,
								})
							}
							//
							//if funcName == "QueryBKDictInfo" {
							//	ch <- map[string]any{
							//		"code":     1,
							//		"question": question,
							//		"chatId":   streamResponse.Id,
							//		"model":    streamResponse.Model,
							//		"content":  "\r\n```\r\n开始调用工具：QueryBKDictInfo，\n参数：" + funcArguments + "\r\n```\r\n",
							//		"time":     time.Now().Format(time.DateTime),
							//	}
							//	res := NewMarketNewsApi().EMDictCode("016", freecache.NewCache(100))
							//	bytes, err := json.Marshal(res)
							//	if err != nil {
							//		return
							//	}
							//	dict := &[]models.BKDict{}
							//	json.Unmarshal(bytes, dict)
							//	md := util.MarkdownTableWithTitle("行业/板块代码", dict)
							//	logger.SugaredLogger.Infof("行业/板块代码=\n%s", md)
							//	messages = append(messages, map[string]interface{}{
							//		"role":    "assistant",
							//		"content": currentAIContent.String(),
							//		"tool_calls": []map[string]any{
							//			{
							//				"id":           currentCallId,
							//				"tool_call_id": currentCallId,
							//				"type":         "function",
							//				"function": map[string]string{
							//					"name":       funcName,
							//					"arguments":  funcArguments,
							//					"parameters": funcArguments,
							//				},
							//			},
							//		},
							//	})
							//	messages = append(messages, map[string]interface{}{
							//		"role":         "tool",
							//		"content":      md,
							//		"tool_call_id": currentCallId,
							//	})
							//}

							//if funcName == "GetIndustryResearchReport" {
							//	bkCode := gjson.Get(funcArguments, "bkCode").String()
							//	ch <- map[string]any{
							//		"code":     1,
							//		"question": question,
							//		"chatId":   streamResponse.Id,
							//		"model":    streamResponse.Model,
							//		"content":  "\r\n```\r\n开始调用工具：GetIndustryResearchReport，\n参数：" + bkCode + "\r\n```\r\n",
							//		"time":     time.Now().Format(time.DateTime),
							//	}
							//	bkCode = strutil.ReplaceWithMap(bkCode, map[string]string{
							//		"-":   "",
							//		"_":   "",
							//		"bk":  "",
							//		"BK":  "",
							//		"bk0": "",
							//		"BK0": "",
							//	})
							//
							//	logger.SugaredLogger.Debugf("code:%s", bkCode)
							//	codeStr := convertor.ToString(bkCode)
							//	res := NewMarketNewsApi().IndustryResearchReport(codeStr, 7)
							//	md := strings.Builder{}
							//	for _, a := range res {
							//		d := a.(map[string]any)
							//		md.WriteString(NewMarketNewsApi().GetIndustryReportInfo(d["infoCode"].(string)))
							//	}
							//	logger.SugaredLogger.Infof("bkCode:%s IndustryResearchReport:\n %s", bkCode, md.String())
							//	messages = append(messages, map[string]interface{}{
							//		"role":    "assistant",
							//		"content": currentAIContent.String(),
							//		"tool_calls": []map[string]any{
							//			{
							//				"id":           currentCallId,
							//				"tool_call_id": currentCallId,
							//				"type":         "function",
							//				"function": map[string]string{
							//					"name":       funcName,
							//					"arguments":  funcArguments,
							//					"parameters": funcArguments,
							//				},
							//			},
							//		},
							//	})
							//	messages = append(messages, map[string]interface{}{
							//		"role":         "tool",
							//		"content":      md.String(),
							//		"tool_call_id": currentCallId,
							//	})
							//}

							if funcName == "GetStockResearchReport" {
								stockCode := gjson.Get(funcArguments, "stockCode").String()
								ch <- map[string]any{
									"code":     1,
									"question": question,
									"chatId":   streamResponse.Id,
									"model":    streamResponse.Model,
									"content":  "\r\n```\r\n开始调用工具：GetStockResearchReport，\n参数：" + stockCode + "\r\n```\r\n",
									"time":     time.Now().Format(time.DateTime),
								}
								res := NewMarketNewsApi().StockResearchReport(stockCode, 7)
								md := strings.Builder{}
								for _, a := range res {
									logger.SugaredLogger.Debugf("value: %+v", a)
									d := a.(map[string]any)
									logger.SugaredLogger.Debugf("value: %s  infoCode:%s", d["title"], d["infoCode"])
									md.WriteString(NewMarketNewsApi().GetIndustryReportInfo(d["infoCode"].(string)))
								}
								logger.SugaredLogger.Infof("stockCode:%s StockResearchReport:\n %s", stockCode, md.String())
								messages = append(messages, map[string]interface{}{
									"role":              "assistant",
									"content":           currentAIContent.String(),
									"reasoning_content": reasoningContentText.String(),
									"tool_calls": []map[string]any{
										{
											"id":           currentCallId,
											"tool_call_id": currentCallId,
											"type":         "function",
											"function": map[string]string{
												"name":       funcName,
												"arguments":  funcArguments,
												"parameters": funcArguments,
											},
										},
									},
								})
								messages = append(messages, map[string]interface{}{
									"role":         "tool",
									"content":      md.String(),
									"tool_call_id": currentCallId,
									//"reasoning_content": reasoningContentText.String(),
									//"tool_calls":        choice.Delta.ToolCalls,
								})
							}

							if funcName == "HotStockTable" {
								pageSize := gjson.Get(funcArguments, "pageSize").String()
								ch <- map[string]any{
									"code":     1,
									"question": question,
									"chatId":   streamResponse.Id,
									"model":    streamResponse.Model,
									"content":  "\r\n```\r\n开始调用工具：HotStockTable，\n参数：" + funcArguments + "\r\n```\r\n",
									"time":     time.Now().Format(time.DateTime),
								}
								pageSizeNum, err := convertor.ToInt(pageSize)
								if err != nil {
									pageSizeNum = 50
								}

								res := NewMarketNewsApi().XUEQIUHotStock(int(pageSizeNum), "10")
								md := util.MarkdownTableWithTitle("当前热门股票排名", res)
								logger.SugaredLogger.Infof("pageSize:%s HotStockTable:\n %s", pageSize, md)
								messages = append(messages, map[string]interface{}{
									"role":              "assistant",
									"content":           currentAIContent.String(),
									"reasoning_content": reasoningContentText.String(),
									"tool_calls": []map[string]any{
										{
											"id":           currentCallId,
											"tool_call_id": currentCallId,
											"type":         "function",
											"function": map[string]string{
												"name":       funcName,
												"arguments":  funcArguments,
												"parameters": funcArguments,
											},
										},
									},
								})
								messages = append(messages, map[string]interface{}{
									"role":         "tool",
									"content":      md,
									"tool_call_id": currentCallId,
									//"reasoning_content": reasoningContentText.String(),
									//"tool_calls":        choice.Delta.ToolCalls,
								})

							}

							if funcName == "GetStockMoneyData" {
								ch <- map[string]any{
									"code":     1,
									"question": question,
									"chatId":   streamResponse.Id,
									"model":    streamResponse.Model,
									"content":  "\r\n```\r\n开始调用工具：GetStockMoneyData，\n参数：" + funcArguments + "\r\n```\r\n",
									"time":     time.Now().Format(time.DateTime),
								}
								res := NewStockDataApi().GetStockMoneyData()
								md := util.MarkdownTableWithTitle("今日个股资金流向Top50", res.Data.Diff)
								logger.SugaredLogger.Infof("%s", md)
								messages = append(messages, map[string]interface{}{
									"role":              "assistant",
									"content":           currentAIContent.String(),
									"reasoning_content": reasoningContentText.String(),
									"tool_calls": []map[string]any{
										{
											"id":           currentCallId,
											"tool_call_id": currentCallId,
											"type":         "function",
											"function": map[string]string{
												"name":       funcName,
												"arguments":  funcArguments,
												"parameters": funcArguments,
											},
										},
									},
								})
								messages = append(messages, map[string]interface{}{
									"role":         "tool",
									"content":      md,
									"tool_call_id": currentCallId,
									//"reasoning_content": reasoningContentText.String(),
									//"tool_calls":        choice.Delta.ToolCalls,
								})
							}

							if funcName == "GetStockConceptInfo" {
								ch <- map[string]any{
									"code":     1,
									"question": question,
									"chatId":   streamResponse.Id,
									"model":    streamResponse.Model,
									"content":  "\r\n```\r\n开始调用工具：GetStockConceptInfo，\n参数：" + funcArguments + "\r\n```\r\n",
									"time":     time.Now().Format(time.DateTime),
								}
								code := gjson.Get(funcArguments, "code").String()
								res := NewStockDataApi().GetStockConceptInfo(code)
								md := util.MarkdownTableWithTitle(code+" 股票所属概念详细信息", res.Result.Data)
								logger.SugaredLogger.Infof("%s", md)
								messages = append(messages, map[string]interface{}{
									"role":              "assistant",
									"content":           currentAIContent.String(),
									"reasoning_content": reasoningContentText.String(),
									"tool_calls": []map[string]any{
										{
											"id":           currentCallId,
											"tool_call_id": currentCallId,
											"type":         "function",
											"function": map[string]string{
												"name":       funcName,
												"arguments":  funcArguments,
												"parameters": funcArguments,
											},
										},
									},
								})
								messages = append(messages, map[string]interface{}{
									"role":         "tool",
									"content":      md,
									"tool_call_id": currentCallId,
									//"reasoning_content": reasoningContentText.String(),
									//"tool_calls":        choice.Delta.ToolCalls,
								})
							}

							if funcName == "GetStockFinancialInfo" {
								ch <- map[string]any{
									"code":     1,
									"question": question,
									"chatId":   streamResponse.Id,
									"model":    streamResponse.Model,
									"content":  "\r\n```\r\n开始调用工具：GetStockFinancialInfo，\n参数：" + funcArguments + "\r\n```\r\n",
									"time":     time.Now().Format(time.DateTime),
								}
								res := NewStockDataApi().GetStockFinancialInfo(gjson.Get(funcArguments, "stockCode").String())
								md := util.MarkdownTableWithTitle("股票"+gjson.Get(funcArguments, "stockCode").String()+"财务报表信息", res.Result.Data)
								logger.SugaredLogger.Infof("%s", md)
								messages = append(messages, map[string]interface{}{
									"role":              "assistant",
									"content":           currentAIContent.String(),
									"reasoning_content": reasoningContentText.String(),
									"tool_calls": []map[string]any{
										{
											"id":           currentCallId,
											"tool_call_id": currentCallId,
											"type":         "function",
											"function": map[string]string{
												"name":       funcName,
												"arguments":  funcArguments,
												"parameters": funcArguments,
											},
										},
									},
								})
								messages = append(messages, map[string]interface{}{
									"role":         "tool",
									"content":      md,
									"tool_call_id": currentCallId,
									//"reasoning_content": reasoningContentText.String(),
									//"tool_calls":        choice.Delta.ToolCalls,
								})
							}
							if funcName == "GetStockHolderNum" {
								ch <- map[string]any{
									"code":     1,
									"question": question,
									"chatId":   streamResponse.Id,
									"model":    streamResponse.Model,
									"content":  "\r\n```\r\n开始调用工具：GetStockHolderNum，\n参数：" + funcArguments + "\r\n```\r\n",
									"time":     time.Now().Format(time.DateTime),
								}
								res := NewStockDataApi().GetStockHolderNum(gjson.Get(funcArguments, "stockCode").String())
								md := util.MarkdownTableWithTitle("股票"+gjson.Get(funcArguments, "stockCode").String()+"股东人数信息", res.Result.Data)
								logger.SugaredLogger.Infof("%s", md)
								messages = append(messages, map[string]interface{}{
									"role":              "assistant",
									"content":           currentAIContent.String(),
									"reasoning_content": reasoningContentText.String(),
									"tool_calls": []map[string]any{
										{
											"id":           currentCallId,
											"tool_call_id": currentCallId,
											"type":         "function",
											"function": map[string]string{
												"name":       funcName,
												"arguments":  funcArguments,
												"parameters": funcArguments,
											},
										},
									},
								})
								messages = append(messages, map[string]interface{}{
									"role":         "tool",
									"content":      md,
									"tool_call_id": currentCallId,
									//"reasoning_content": reasoningContentText.String(),
									//"tool_calls":        choice.Delta.ToolCalls,
								})
							}

						}
						AskAiWithTools(o, messages, ch, question, tools, thinkingMode)
						return
					}

					if choice.FinishReason == "stop" {
						return
					}
				}
			} else {
				if err != nil {
					logger.SugaredLogger.Infof("Stream data error : %s", err.Error())
					//ch <- err.Error()
					ch <- map[string]any{
						"code":     0,
						"question": question,
						"content":  err.Error(),
					}
				} else {
					logger.SugaredLogger.Infof("Stream data error : %s", data)
					//ch <- data
					ch <- map[string]any{
						"code":     0,
						"question": question,
						"content":  data,
					}
				}
			}
		} else {
			if strutil.RemoveNonPrintable(line) != "" {
				logger.SugaredLogger.Infof("Stream data error : %s", line)
				res := &models.Resp{}
				if err := json.Unmarshal([]byte(line), res); err == nil {
					//ch <- line
					msg := res.Message
					if res.Error.Message != "" {
						msg = res.Error.Message
					}

					if msg == "Function call is not supported for this model." {
						var newMessages []map[string]any
						for _, message := range messages {
							if message["role"] == "tool" {
								continue
							}
							if _, ok := message["tool_calls"]; ok {
								continue
							}
							newMessages = append(newMessages, message)
						}
						AskAi(o, newMessages, ch, question, thinkingMode)
					} else {
						ch <- map[string]any{
							"code":     0,
							"question": question,
							"content":  msg,
						}
					}

				}
			}

		}

	}
	if scanErr := scanner.Err(); scanErr != nil {
		logger.SugaredLogger.Infof("Stream scanner error : %s", scanErr.Error())
		ch <- map[string]any{
			"code":     0,
			"question": question,
			"content":  o.formatAIRequestError(scanErr),
		}
	}
}
func checkIsIndexBasic(stock string) bool {
	count := int64(0)
	db.Dao.Model(&models.IndexBasic{}).Where("name =  ?", stock).Count(&count)
	return count > 0
}

func SearchGuShiTongStockInfo(stock string, crawlTimeOut int64) *[]string {
	crawlerAPI := CrawlerApi{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(crawlTimeOut)*time.Second)
	defer cancel()

	crawlerAPI = crawlerAPI.NewCrawler(ctx, CrawlerBaseInfo{
		Name:    "百度股市通",
		BaseUrl: "https://gushitong.baidu.com",
		Headers: map[string]string{"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36 Edg/133.0.0.0"},
	})
	url := "https://gushitong.baidu.com/stock/ab-" + RemoveAllNonDigitChar(stock)

	if strutil.HasPrefixAny(stock, []string{"HK", "hk"}) {
		url = "https://gushitong.baidu.com/stock/hk-" + RemoveAllNonDigitChar(stock)
	}
	if strutil.HasPrefixAny(stock, []string{"SZ", "SH", "sh", "sz"}) {
		url = "https://gushitong.baidu.com/stock/ab-" + RemoveAllNonDigitChar(stock)
	}
	if strutil.HasPrefixAny(stock, []string{"us", "US", "gb_", "gb"}) {
		url = "https://gushitong.baidu.com/stock/us-" + strings.Replace(stock, "gb_", "", 1)
	}

	//logger.SugaredLogger.Infof("SearchGuShiTongStockInfo搜索股票-%s: %s", stock, url)
	actions := []chromedp.Action{
		chromedp.Navigate(url),
		chromedp.WaitVisible("div.cos-tab"),
		chromedp.Click("div.cos-tab:nth-child(5)", chromedp.ByQuery),
		chromedp.ScrollIntoView("div.body-box"),
		chromedp.WaitVisible("div.body-col"),
		chromedp.Evaluate(`window.scrollTo(0, document.body.scrollHeight);`, nil),
		chromedp.Sleep(1 * time.Second),
	}
	htmlContent, success := crawlerAPI.GetHtmlWithActions(&actions, true)
	var messages []string
	if success {
		document, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
		if err != nil {
			logger.SugaredLogger.Error(err.Error())
			return &[]string{}
		}
		document.Find("div.finance-hover,div.list-date").Each(func(i int, selection *goquery.Selection) {
			text := strutil.RemoveWhiteSpace(selection.Text(), false)
			messages = append(messages, ReplaceSensitiveWords(text))
			//logger.SugaredLogger.Infof("SearchGuShiTongStockInfo搜索到消息-%s: %s", "", text)
		})
		//logger.SugaredLogger.Infof("messages:%d", len(messages))
	}
	return &messages
}
func GetFinancialReportsByXUEQIU(stockCode string, crawlTimeOut int64) *[]string {
	if strutil.HasPrefixAny(stockCode, []string{"HK", "hk"}) {
		stockCode = strings.ReplaceAll(stockCode, "hk", "")
		stockCode = strings.ReplaceAll(stockCode, "HK", "")
	}
	if strutil.HasPrefixAny(stockCode, []string{"us", "gb_"}) {
		stockCode = strings.ReplaceAll(stockCode, "us", "")
		stockCode = strings.ReplaceAll(stockCode, "gb_", "")
	}
	url := fmt.Sprintf("https://xueqiu.com/snowman/S/%s/detail#/ZYCWZB", stockCode)
	waitVisible := "div.tab-table-responsive table"
	crawlerAPI := CrawlerApi{}
	crawlerBaseInfo := CrawlerBaseInfo{
		Name:        "TestCrawler",
		Description: "Test Crawler Description",
		BaseUrl:     "https://xueqiu.com",
		Headers:     map[string]string{"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36 Edg/133.0.0.0"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(crawlTimeOut)*time.Second)
	defer cancel()
	crawlerAPI = crawlerAPI.NewCrawler(ctx, crawlerBaseInfo)

	var markdown strings.Builder
	markdown.WriteString("\n## 财务数据：\n")
	html, ok := crawlerAPI.GetHtml(url, waitVisible, true)
	if !ok {
		return &[]string{""}
	}
	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		logger.SugaredLogger.Error(err.Error())
	}
	GetTableMarkdown(document, waitVisible, &markdown)
	return &[]string{markdown.String()}
}
func GetFinancialReports(stockCode string, crawlTimeOut int64) *[]string {
	url := "https://emweb.securities.eastmoney.com/pc_hsf10/pages/index.html?type=web&code=" + stockCode + "#/cwfx"
	waitVisible := "div.report_table table"
	if strutil.HasPrefixAny(stockCode, []string{"HK", "hk"}) {
		stockCode = strings.ReplaceAll(stockCode, "hk", "")
		stockCode = strings.ReplaceAll(stockCode, "HK", "")
		url = "https://emweb.securities.eastmoney.com/PC_HKF10/pages/home/index.html?code=" + stockCode + "&type=web&color=w#/NewFinancialAnalysis"
		waitVisible = "div table.commonTable"
	}
	if strutil.HasPrefixAny(stockCode, []string{"us", "gb_"}) {
		stockCode = strings.ReplaceAll(stockCode, "us", "")
		stockCode = strings.ReplaceAll(stockCode, "gb_", "")
		url = "https://emweb.securities.eastmoney.com/pc_usf10/pages/index.html?type=web&code=" + stockCode + "#/cwfx"
		waitVisible = "div.zyzb_table_detail table"

	}

	//logger.SugaredLogger.Infof("GetFinancialReports搜索股票-%s: %s", stockCode, url)

	crawlerAPI := CrawlerApi{}
	crawlerBaseInfo := CrawlerBaseInfo{
		Name:        "TestCrawler",
		Description: "Test Crawler Description",
		BaseUrl:     "https://emweb.securities.eastmoney.com",
		Headers:     map[string]string{"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36 Edg/133.0.0.0"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(crawlTimeOut)*time.Second)
	defer cancel()
	crawlerAPI = crawlerAPI.NewCrawler(ctx, crawlerBaseInfo)

	var markdown strings.Builder
	markdown.WriteString("\n## 财务数据：\n")
	html, ok := crawlerAPI.GetHtml(url, waitVisible, true)
	if !ok {
		return &[]string{""}
	}
	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		logger.SugaredLogger.Error(err.Error())
	}
	GetTableMarkdown(document, waitVisible, &markdown)
	return &[]string{markdown.String()}
}

func GetTelegraphList(crawlTimeOut int64) *[]string {
	url := "https://www.cls.cn/telegraph"
	response, err := newFetchRestyClient().SetTimeout(time.Duration(crawlTimeOut)*time.Second).R().
		SetHeader("Referer", "https://www.cls.cn/").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/117.0.0.0 Safari/537.36 Edg/117.0.2045.60").
		Get(url)
	if err != nil {
		return &[]string{}
	}
	//logger.SugaredLogger.Info(string(response.Body()))
	document, err := goquery.NewDocumentFromReader(strings.NewReader(string(response.Body())))
	if err != nil {
		return &[]string{}
	}
	var telegraph []string
	document.Find("div.telegraph-content-box").Each(func(i int, selection *goquery.Selection) {
		//logger.SugaredLogger.Info(selection.Text())
		telegraph = append(telegraph, ReplaceSensitiveWords(selection.Text()))
	})
	return &telegraph
}

func GetTopNewsList(crawlTimeOut int64) *[]string {
	url := "https://www.cls.cn"
	response, err := newFetchRestyClient().SetTimeout(time.Duration(crawlTimeOut)*time.Second).R().
		SetHeader("Referer", "https://www.cls.cn/").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/117.0.0.0 Safari/537.36 Edg/117.0.2045.60").
		Get(url)
	if err != nil {
		return &[]string{}
	}
	//logger.SugaredLogger.Info(string(response.Body()))
	document, err := goquery.NewDocumentFromReader(strings.NewReader(string(response.Body())))
	if err != nil {
		return &[]string{}
	}
	var telegraph []string
	document.Find("div.home-article-title a,div.home-article-rec a").Each(func(i int, selection *goquery.Selection) {
		//logger.SugaredLogger.Info(selection.Text())
		telegraph = append(telegraph, ReplaceSensitiveWords(selection.Text()))
	})
	return &telegraph
}
