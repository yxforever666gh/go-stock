package ai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"go-stock/backend/models"
)

const (
	researchModelAttemptTimeout          = 300 * time.Second
	researchModelMaxAttempts             = 5
	researchModelRetryBaseDelay          = time.Second
	researchModelRetryMaxDelay           = 8 * time.Second
	researchSameEndpointFallbackCooldown = 8 * time.Second
)

var errResearchModelInactive = errors.New("model stream had no activity before timeout")

type StreamActivity struct {
	EventType string
	State     string
}

// ProviderCallError is the provider-neutral failure information needed by the
// retry and audit policy. It never contains request or response payloads.
type ProviderCallError struct {
	Category      string
	StatusCode    int
	Message       string
	Retryable     bool
	LastEventType string
}

func (err *ProviderCallError) Error() string {
	if err == nil {
		return ""
	}
	if err.StatusCode > 0 {
		return fmt.Sprintf("HTTP %d: %s", err.StatusCode, err.Message)
	}
	return err.Message
}

type ResearchProviderCompletion func(context.Context, *models.AIConfig, []map[string]any, string, func(StreamActivity)) (string, string, string, error)

type ResearchClientOptions struct {
	LoadConfigs      func() []*models.AIConfig
	CompleteProvider ResearchProviderCompletion
	AttemptTimeout   time.Duration
	MaxAttempts      int
	RetryWait        func(context.Context, time.Duration) error
	Warnf            func(string, ...any)
}

type ResearchClient struct {
	configID         int
	forceConfig      bool
	loadConfigs      func() []*models.AIConfig
	completeProvider ResearchProviderCompletion
	attemptTimeout   time.Duration
	maxAttempts      int
	retryWait        func(context.Context, time.Duration) error
	warnfCallback    func(string, ...any)
}

func NewResearchClient(configID int, options ResearchClientOptions) *ResearchClient {
	return newResearchClient(configID, false, options)
}

// NewResearchReplayClient pins an isolated audit replay to the explicitly
// selected configuration. Formal research retains saved fallback order.
func NewResearchReplayClient(configID int, options ResearchClientOptions) *ResearchClient {
	return newResearchClient(configID, true, options)
}

func newResearchClient(configID int, force bool, options ResearchClientOptions) *ResearchClient {
	return &ResearchClient{
		configID: configID, forceConfig: force, loadConfigs: options.LoadConfigs,
		completeProvider: options.CompleteProvider, attemptTimeout: options.AttemptTimeout,
		maxAttempts: options.MaxAttempts, retryWait: options.RetryWait, warnfCallback: options.Warnf,
	}
}

func (client *ResearchClient) modelAttemptTimeout() time.Duration {
	if client != nil && client.attemptTimeout > 0 {
		return client.attemptTimeout
	}
	return researchModelAttemptTimeout
}

func (client *ResearchClient) modelMaxAttempts() int {
	if client != nil && client.maxAttempts > 0 {
		return client.maxAttempts
	}
	return researchModelMaxAttempts
}

func researchModelRetryDelay(failedAttempt int) time.Duration {
	if failedAttempt <= 1 {
		return researchModelRetryBaseDelay
	}
	delay := researchModelRetryBaseDelay << (failedAttempt - 1)
	if delay > researchModelRetryMaxDelay {
		return researchModelRetryMaxDelay
	}
	return delay
}

func waitResearchRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		return ctx.Err()
	}
}

func (client *ResearchClient) waitForRetry(ctx context.Context, delay time.Duration) error {
	if client != nil && client.retryWait != nil {
		return client.retryWait(ctx, delay)
	}
	return waitResearchRetry(ctx, delay)
}

func sameResearchEndpoint(left, right *models.AIConfig) bool {
	if left == nil || right == nil {
		return false
	}
	leftURL := strings.ToLower(strings.TrimRight(strings.TrimSpace(left.BaseUrl), "/"))
	rightURL := strings.ToLower(strings.TrimRight(strings.TrimSpace(right.BaseUrl), "/"))
	return leftURL != "" && leftURL == rightURL
}

func (client *ResearchClient) completeModelAttempt(ctx context.Context, config *models.AIConfig, messages []map[string]any, previousResponseID string, activity func(StreamActivity)) (string, string, string, error) {
	if client == nil || client.completeProvider == nil {
		return "", "", "", errors.New("research model provider is unavailable")
	}
	return client.completeProvider(ctx, config, messages, previousResponseID, activity)
}

func researchInactivityContext(parent context.Context, timeout time.Duration) (context.Context, func(), func()) {
	ctx, cancel := context.WithCancelCause(parent)
	touchCh := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		for {
			select {
			case <-timer.C:
				select {
				case <-touchCh:
					timer.Reset(timeout)
					continue
				default:
					cancel(errResearchModelInactive)
					return
				}
			case <-touchCh:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(timeout)
			case <-ctx.Done():
				return
			}
		}
	}()
	touch := func() {
		select {
		case touchCh <- struct{}{}:
		default:
		}
	}
	stop := func() {
		cancel(context.Canceled)
		<-done
	}
	return ctx, touch, stop
}

type researchProviderErrorInfo struct {
	category   string
	message    string
	statusCode int
	retryable  bool
	lastEvent  string
}

func classifyResearchProviderError(err error) researchProviderErrorInfo {
	if err == nil {
		return researchProviderErrorInfo{}
	}
	var providerErr *ProviderCallError
	if errors.As(err, &providerErr) {
		return researchProviderErrorInfo{
			category: providerErr.Category, message: providerErr.Message, statusCode: providerErr.StatusCode,
			retryable: providerErr.Retryable, lastEvent: providerErr.LastEventType,
		}
	}
	if errors.Is(err, errResearchModelInactive) {
		return researchProviderErrorInfo{category: "idle_timeout", message: err.Error(), retryable: true}
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return researchProviderErrorInfo{category: "stream_interrupted", message: err.Error(), retryable: true}
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return researchProviderErrorInfo{category: "network_error", message: err.Error(), retryable: true}
	}
	lower := strings.ToLower(err.Error())
	for _, marker := range []string{"connection reset", "connection refused", "unexpected eof", "tls handshake timeout", "i/o timeout", "temporary failure", "no such host"} {
		if strings.Contains(lower, marker) {
			return researchProviderErrorInfo{category: "network_error", message: err.Error(), retryable: true}
		}
	}
	return researchProviderErrorInfo{category: "request_error", message: err.Error(), retryable: false}
}

func sanitizeResearchProviderError(message string, config *models.AIConfig) string {
	message = strings.TrimSpace(message)
	if config != nil {
		for _, secret := range []string{config.ApiKey, config.BaseUrl, config.HttpProxy} {
			if secret = strings.TrimSpace(secret); secret != "" {
				message = strings.ReplaceAll(message, secret, "[REDACTED]")
			}
		}
	}
	message = strings.Join(strings.Fields(message), " ")
	const maxRunes = 2048
	runes := []rune(message)
	if len(runes) > maxRunes {
		message = string(runes[:maxRunes]) + "…"
	}
	if message == "" {
		return "模型服务返回了空错误"
	}
	return message
}

func emitResearchAttempt(callback func(ModelAttemptRecord), record ModelAttemptRecord) {
	if callback != nil {
		callback(record)
	}
}

func orderedResearchConfigs(configs []*models.AIConfig, requestedConfigID int, force bool) []*models.AIConfig {
	byID := make(map[int]*models.AIConfig, len(configs))
	order := make([]int, 0, len(configs))
	seen := make(map[int]struct{}, len(configs))
	for _, config := range configs {
		if config == nil {
			continue
		}
		byID[int(config.ID)] = config
		if config.Disabled {
			continue
		}
		id := int(config.ID)
		if _, exists := seen[id]; !exists {
			seen[id] = struct{}{}
			order = append(order, id)
		}
	}
	if force {
		order = []int{requestedConfigID}
	}
	result := make([]*models.AIConfig, 0, len(order))
	for _, id := range order {
		if config := byID[id]; config != nil && !config.Disabled {
			result = append(result, config)
		}
	}
	return result
}

func (client *ResearchClient) Complete(ctx context.Context, request CompletionRequest) (CompletionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var configs []*models.AIConfig
	if client != nil && client.loadConfigs != nil {
		configs = client.loadConfigs()
	}
	messages := make([]map[string]any, 0, len(request.Messages)+1)
	if len(request.Messages) > 0 {
		for _, message := range request.Messages {
			role := message.Role
			if role != "system" && role != "assistant" {
				role = "user"
			}
			messages = append(messages, map[string]any{"role": role, "content": message.Content})
		}
	} else {
		messages = append(messages, map[string]any{"role": "user", "content": request.Prompt})
	}
	forceConfig := client != nil && client.forceConfig
	configID := 0
	if client != nil {
		configID = client.configID
	}
	orderedConfigs := orderedResearchConfigs(configs, configID, forceConfig)
	if len(orderedConfigs) == 0 {
		return CompletionResult{}, errors.New("没有已启用的 AI 模型")
	}
	attemptErrors := make([]error, 0, len(orderedConfigs))
	attemptTimeout := client.modelAttemptTimeout()
	maxAttempts := client.modelMaxAttempts()
	for index, config := range orderedConfigs {
		label := strings.TrimSpace(config.Name)
		if label == "" {
			label = DisplayProviderName(config.Name, config.BaseUrl, config.ModelName)
		}
		var lastErr error
		var lastInfo researchProviderErrorInfo
		attemptsMade := 0
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			if err := ctx.Err(); err != nil {
				return CompletionResult{}, err
			}
			attemptsMade = attempt
			startedAt := time.Now()
			record := ModelAttemptRecord{
				ID:    fmt.Sprintf("%s-%d-%d-%d", request.Phase, config.ID, attempt, startedAt.UnixNano()),
				Phase: request.Phase, ConfigID: config.ID, ProviderName: label,
				ModelName: strings.TrimSpace(config.ModelName), APIProtocol: models.NormalizeAIAPIProtocol(config.ApiProtocol),
				MaxTokens: config.MaxTokens, Temperature: config.Temperature, RequestTimeoutSeconds: config.TimeOut,
				InactivityTimeoutSeconds: int(attemptTimeout / time.Second), FallbackIndex: index + 1, FallbackCount: len(orderedConfigs),
				ForcedConfig: forceConfig, PreviousResponseIDPresent: strings.TrimSpace(request.PreviousResponseID) != "",
				Attempt: attempt, MaxAttempts: maxAttempts, StartedAt: startedAt, Status: "waiting_response",
			}
			emitResearchAttempt(request.OnAttempt, record)
			attemptCtx, touch, stop := researchInactivityContext(ctx, attemptTimeout)
			lastPersistedAt := startedAt
			lastPersistedStatus := record.Status
			content, responseID, model, err := client.completeModelAttempt(attemptCtx, config, messages, request.PreviousResponseID, func(event StreamActivity) {
				touch()
				now := time.Now()
				record.LastActivityAt = &now
				record.LastEventType = strings.TrimSpace(event.EventType)
				if strings.TrimSpace(event.State) != "" {
					record.Status = strings.TrimSpace(event.State)
				}
				if record.Status != lastPersistedStatus || now.Sub(lastPersistedAt) >= 5*time.Second {
					record.DurationMS = now.Sub(startedAt).Milliseconds()
					emitResearchAttempt(request.OnAttempt, record)
					lastPersistedAt, lastPersistedStatus = now, record.Status
				}
			})
			cause := context.Cause(attemptCtx)
			stop()
			if errors.Is(cause, errResearchModelInactive) {
				err = fmt.Errorf("连续 %s 未收到有效模型事件: %w", attemptTimeout, errResearchModelInactive)
			}
			if err == nil {
				completedAt := time.Now()
				record.Status, record.CompletedAt, record.DurationMS = "success", &completedAt, completedAt.Sub(startedAt).Milliseconds()
				record.NextAction = "complete"
				emitResearchAttempt(request.OnAttempt, record)
				return CompletionResult{Content: content, ResponseID: responseID, Model: model}, nil
			}
			if parentErr := ctx.Err(); parentErr != nil {
				completedAt := time.Now()
				record.Status, record.CompletedAt, record.DurationMS = "cancelled", &completedAt, completedAt.Sub(startedAt).Milliseconds()
				record.ErrorCategory, record.ErrorMessage, record.NextAction = "cancelled", sanitizeResearchProviderError(parentErr.Error(), config), "stop"
				emitResearchAttempt(request.OnAttempt, record)
				return CompletionResult{}, parentErr
			}
			lastInfo = classifyResearchProviderError(err)
			lastInfo.message = sanitizeResearchProviderError(lastInfo.message, config)
			lastErr = errors.New(lastInfo.message)
			completedAt := time.Now()
			record.Status, record.CompletedAt, record.DurationMS = "failed", &completedAt, completedAt.Sub(startedAt).Milliseconds()
			record.HTTPStatus, record.ErrorCategory, record.ErrorMessage = lastInfo.statusCode, lastInfo.category, lastInfo.message
			record.Retryable = lastInfo.retryable
			if record.LastEventType == "" {
				record.LastEventType = lastInfo.lastEvent
			}
			if lastInfo.retryable && attempt < maxAttempts {
				record.NextAction = "retry_same_model"
			} else if index+1 < len(orderedConfigs) {
				record.NextAction = "fallback_next_model"
			} else {
				record.NextAction = "stop"
			}
			emitResearchAttempt(request.OnAttempt, record)
			if lastInfo.retryable && attempt < maxAttempts {
				retryDelay := researchModelRetryDelay(attempt)
				client.warnf("研究中心 AI 调用失败，退避后重试当前模型。phase=%s model=%s/%s attempt=%d/%d retry_in=%s inactivity_timeout=%s category=%s http_status=%d error=%s",
					request.Phase, label, strings.TrimSpace(config.ModelName), attempt, maxAttempts, retryDelay, attemptTimeout, lastInfo.category, lastInfo.statusCode, lastInfo.message)
				if waitErr := client.waitForRetry(ctx, retryDelay); waitErr != nil {
					return CompletionResult{}, waitErr
				}
				continue
			}
			break
		}
		attemptErrors = append(attemptErrors, fmt.Errorf("%s/%s 调用 %d 次后失败（%s）: %w",
			label, strings.TrimSpace(config.ModelName), attemptsMade, lastInfo.category, lastErr))
		if index+1 < len(orderedConfigs) {
			next := orderedConfigs[index+1]
			nextLabel := strings.TrimSpace(next.Name) + "/" + strings.TrimSpace(next.ModelName)
			client.warnf("研究中心 AI 调用失败，按回退顺序切换。phase=%s from=%s/%s attempts=%d to=%s inactivity_timeout=%s category=%s http_status=%d error=%s",
				request.Phase, label, strings.TrimSpace(config.ModelName), attemptsMade, nextLabel, attemptTimeout, lastInfo.category, lastInfo.statusCode, lastInfo.message)
			if lastInfo.retryable && sameResearchEndpoint(config, next) {
				client.warnf("研究中心 AI 回退配置共用同一端点，冷却后切换。phase=%s from=%s/%s to=%s cooldown=%s",
					request.Phase, label, strings.TrimSpace(config.ModelName), nextLabel, researchSameEndpointFallbackCooldown)
				if waitErr := client.waitForRetry(ctx, researchSameEndpointFallbackCooldown); waitErr != nil {
					return CompletionResult{}, waitErr
				}
			}
		}
	}
	if len(attemptErrors) == 0 {
		return CompletionResult{}, errors.New("没有已启用的 AI 模型")
	}
	return CompletionResult{}, fmt.Errorf("所有已启用模型均调用失败: %w", errors.Join(attemptErrors...))
}

func (client *ResearchClient) warnf(format string, args ...any) {
	if client != nil && client.warnfCallback != nil {
		client.warnfCallback(format, args...)
	}
}

var _ AIClient = (*ResearchClient)(nil)
