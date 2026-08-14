package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go-stock/backend/data"
	"go-stock/backend/db"
	"go-stock/backend/models"
	cliports "go-stock/internal/cli/ports"
	"go-stock/internal/service"

	"gorm.io/gorm"
)

var _ service.AIOperations = (*compatibilityServiceAdapter)(nil)
var _ service.ConfigOperations = (*compatibilityServiceAdapter)(nil)

func NewProductionCommandAIResolver() (cliports.CommandAIResolver, error) {
	if db.Dao == nil {
		return nil, errors.New("main database is not initialized")
	}
	return &compatibilityServiceAdapter{main: db.Dao}, nil
}

func (a *compatibilityServiceAdapter) ResolveCommandAI(ctx context.Context, opts cliports.CommandAIOptions) (cliports.CommandAIClient, error) {
	cfg, err := resolveCommandAIConfig(ctx, a.main, opts)
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

func (*compatibilityServiceAdapter) TestAIConfig(ctx context.Context, aiConfigID int) *models.AIModelTestResult {
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

func (*compatibilityServiceAdapter) AnalyzeSentiment(text string) models.SentimentResult {
	return data.AnalyzeSentiment(text)
}

func (*compatibilityServiceAdapter) AnalyzeSentimentWithFreqWeight(text string) map[string]any {
	result, frequencies := data.NewsAnalyze(text, false)
	return map[string]any{"result": result, "frequencies": frequencies}
}

func (*compatibilityServiceAdapter) GetAIResponseResult(ctx context.Context, code string) *models.AIResponseResult {
	return data.NewDeepSeekOpenAi(ctx, 0).GetAIResponseResult(code)
}

func (*compatibilityServiceAdapter) SaveAIResponseResult(ctx context.Context, code, name, result, chatID, question string, configID int) {
	data.NewDeepSeekOpenAi(ctx, configID).SaveAIResponseResult(code, name, result, chatID, question)
}

func (*compatibilityServiceAdapter) GetPromptTemplates(name, promptType string) *[]models.PromptTemplate {
	return data.NewPromptTemplateApi().GetPromptTemplates(name, promptType)
}
func (*compatibilityServiceAdapter) AddPrompt(prompt models.PromptTemplate) string {
	return data.NewPromptTemplateApi().AddPrompt(prompt)
}
func (*compatibilityServiceAdapter) DelPrompt(id uint) string {
	return data.NewPromptTemplateApi().DelPrompt(id)
}
func (*compatibilityServiceAdapter) GetAIConfigs() []*models.AIConfig {
	cfg := data.GetSettingConfig()
	if cfg == nil {
		return []*models.AIConfig{}
	}
	return cfg.AiConfigs
}
func (*compatibilityServiceAdapter) ResolveDefaultAIConfigID() int {
	return data.SelectPrimaryAIConfigID(data.GetSettingConfig())
}
func (*compatibilityServiceAdapter) ResolveAIFallbackOrder(id int) []int {
	return data.ResolveAIFallbackOrder(data.GetSettingConfig(), id)
}
func (*compatibilityServiceAdapter) ResolveAIModelName(id int) string {
	for _, item := range data.GetSettingConfig().AiConfigs {
		if item != nil && int(item.ID) == id {
			return strings.TrimSpace(item.ModelName)
		}
	}
	return ""
}
func (*compatibilityServiceAdapter) NewChatStream(ctx context.Context, stock, code, question string, id int, promptID *int, tools []models.Tool, think bool) <-chan map[string]any {
	return data.NewDeepSeekOpenAi(ctx, id).NewChatStream(stock, code, question, promptID, tools, think)
}
func (*compatibilityServiceAdapter) GetConfig() *models.SettingConfig { return data.GetSettingConfig() }
func (*compatibilityServiceAdapter) ExportConfig() string             { return data.NewSettingsApi().Export() }
func (*compatibilityServiceAdapter) UpdateConfig(config *models.SettingConfig) string {
	return data.UpdateConfig(config)
}
func (*compatibilityServiceAdapter) ResolveFingerprint() (string, error) {
	settings := data.GetSettingConfig()
	if settings != nil && settings.Settings != nil && strings.TrimSpace(settings.QgqpBId) != "" {
		return strings.TrimSpace(settings.QgqpBId), nil
	}
	return "", errors.New("missing qgqp_b_id")
}
