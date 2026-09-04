package aiapp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go-stock/backend/data"
	"go-stock/backend/models"
	cliports "go-stock/internal/cli/ports"
	"go-stock/internal/service"

	"gorm.io/gorm"
)

type Service struct{}

var _ service.AIService = (*Service)(nil)

func NewService() *Service { return &Service{} }

func (*Service) TestAIConfig(ctx context.Context, aiConfigID int) *models.AIModelTestResult {
	startedAt := time.Now()
	result := &models.AIModelTestResult{Message: "测试失败", Protocol: models.AIAPIProtocolChatCompletions}
	setting := data.GetSettingConfig()
	if aiConfigID <= 0 || setting == nil {
		result.Message = "请先保存 AI 配置后再测试"
		return result
	}
	var cfg *models.AIConfig
	for _, item := range setting.AiConfigs {
		if item != nil && int(item.ID) == aiConfigID {
			cfg = item
			break
		}
	}
	if cfg == nil || strings.TrimSpace(cfg.BaseUrl) == "" || strings.TrimSpace(cfg.ApiKey) == "" || strings.TrimSpace(cfg.ModelName) == "" {
		result.Message = "未找到完整的 AI 配置"
		return result
	}
	result.Protocol = models.NormalizeAIAPIProtocol(cfg.ApiProtocol)
	result.Model = strings.TrimSpace(cfg.ModelName)
	content, _, model, err := data.NewOpenAiWithConfig(ctx, cfg).CompleteChat([]map[string]any{{"role": "user", "content": "请只回复 OK"}}, false)
	result.LatencyMs = time.Since(startedAt).Milliseconds()
	if model != "" {
		result.Model = strings.TrimSpace(model)
	}
	if err != nil {
		result.Message = err.Error()
		return result
	}
	content = strings.TrimSpace(content)
	if content == "" {
		result.Message = "模型返回内容为空"
		return result
	}
	result.Success, result.Message = true, "测试成功"
	if runes := []rune(content); len(runes) > 120 {
		content = string(runes[:120])
	}
	result.ContentPreview = content
	return result
}

func (*Service) AnalyzeSentiment(text string) models.SentimentResult {
	return data.AnalyzeSentiment(text)
}

func (*Service) AnalyzeSentimentWithFreqWeight(text string) map[string]any {
	result, frequencies := data.NewsAnalyze(text, false)
	return map[string]any{"result": result, "frequencies": frequencies}
}

func (*Service) GetAIConfigs() []*models.AIConfig {
	cfg := data.GetSettingConfig()
	if cfg == nil {
		return []*models.AIConfig{}
	}
	return data.EnabledAIConfigs(cfg.AiConfigs)
}

type CommandResolver struct {
	main *gorm.DB
}

var _ cliports.CommandAIResolver = (*CommandResolver)(nil)

func NewCommandResolver(main *gorm.DB) (*CommandResolver, error) {
	if main == nil {
		return nil, errors.New("main database is not initialized")
	}
	return &CommandResolver{main: main}, nil
}

func (r *CommandResolver) ResolveCommandAI(ctx context.Context, opts cliports.CommandAIOptions) (cliports.CommandAIClient, error) {
	cfg, err := resolveCommandAIConfig(ctx, r.main, opts)
	if err != nil {
		return nil, err
	}
	return data.NewOpenAiWithConfig(ctx, cfg), nil
}

func resolveCommandAIConfig(ctx context.Context, main *gorm.DB, opts cliports.CommandAIOptions) (*models.AIConfig, error) {
	allFromFlags := opts.BaseURL != "" && opts.APIKey != "" && opts.Model != ""
	anyFromFlags := opts.BaseURL != "" || opts.APIKey != "" || opts.Model != ""
	if anyFromFlags && !allFromFlags {
		return nil, errors.New("参数模式下必须同时提供 --base-url、--api-key、--model")
	}
	if allFromFlags {
		return &models.AIConfig{BaseUrl: opts.BaseURL, ApiKey: opts.APIKey, ModelName: opts.Model,
			MaxTokens: opts.MaxTokens, Temperature: opts.Temperature, TimeOut: opts.Timeout}, nil
	}
	if main == nil {
		return nil, errors.New("main database is not initialized")
	}
	cfg := &models.AIConfig{}
	tx := main.WithContext(ctx).Model(&models.AIConfig{})
	var err error
	if opts.AIConfigID > 0 {
		err = tx.Where("id = ?", opts.AIConfigID).First(cfg).Error
	} else {
		err = tx.Order("id asc").First(cfg).Error
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errors.New("未找到可用 AI 配置，请使用参数模式或先写入 ai_config 表")
	}
	if err != nil {
		return nil, fmt.Errorf("读取 AI 配置失败: %w", err)
	}
	if cfg.BaseUrl == "" || cfg.ApiKey == "" || cfg.ModelName == "" {
		return nil, errors.New("数据库 AI 配置不完整，请检查 base_url/api_key/model_name")
	}
	return cfg, nil
}
