package cli

import (
	"context"
	"errors"
	"fmt"

	"go-stock/backend/data"
	"go-stock/backend/db"

	"gorm.io/gorm"
)

type AIOptions struct {
	AIConfigID  int
	BaseURL     string
	APIKey      string
	Model       string
	MaxTokens   int
	Temperature float64
	Timeout     int
}

func ResolveAIForCommand(ctx context.Context, opts AIOptions) (*data.OpenAi, error) {
	allFromFlags := opts.BaseURL != "" && opts.APIKey != "" && opts.Model != ""
	anyFromFlags := opts.BaseURL != "" || opts.APIKey != "" || opts.Model != ""

	if anyFromFlags && !allFromFlags {
		return nil, errors.New("参数模式下必须同时提供 --base-url、--api-key、--model")
	}

	if allFromFlags {
		o := data.NewOpenAiWithConfig(ctx, &data.AIConfig{
			BaseUrl:     opts.BaseURL,
			ApiKey:      opts.APIKey,
			ModelName:   opts.Model,
			MaxTokens:   opts.MaxTokens,
			Temperature: opts.Temperature,
			TimeOut:     opts.Timeout,
		})
		return o, nil
	}

	cfg := &data.AIConfig{}
	tx := db.Dao.Model(&data.AIConfig{})
	var err error
	if opts.AIConfigID > 0 {
		err = tx.Where("id = ?", opts.AIConfigID).First(cfg).Error
	} else {
		err = tx.Order("id asc").First(cfg).Error
	}
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("未找到可用 AI 配置，请使用参数模式或先写入 ai_config 表")
		}
		return nil, fmt.Errorf("读取 AI 配置失败: %w", err)
	}
	if cfg.BaseUrl == "" || cfg.ApiKey == "" || cfg.ModelName == "" {
		return nil, errors.New("数据库 AI 配置不完整，请检查 base_url/api_key/model_name")
	}
	return data.NewOpenAiWithConfig(ctx, cfg), nil
}

func ResolveFingerprint(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	settings := &data.Settings{}
	if err := db.Dao.Model(&data.Settings{}).Order("id desc").First(settings).Error; err == nil {
		if settings.QgqpBId != "" {
			return settings.QgqpBId, nil
		}
	}
	return "", errors.New("缺少 qgqp_b_id，请通过 --qgqp-b-id 传入或先写入 settings.qgqp_b_id")
}
