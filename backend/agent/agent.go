package agent

import (
	"context"
	"go-stock/backend/agent/tools"
	"go-stock/backend/data"
	"go-stock/backend/logger"
	"strings"
	"time"

	"github.com/cloudwego/eino-ext/components/model/ark"
	"github.com/cloudwego/eino-ext/components/model/deepseek"
	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
)

// GetStockAiAgent @Author spark
// @Date 2025/8/4 16:17
// @Desc
// -----------------------------------------------------------------------------------
func GetStockAiAgent(ctx *context.Context, aiConfig data.AIConfig) *react.Agent {
	temperature := float32(aiConfig.Temperature)
	var toolableChatModel model.ToolCallingChatModel
	var err error
	if aiConfig.BaseUrl == "https://ark.cn-beijing.volces.com/api/v3" {
		toolableChatModel, err = ark.NewChatModel(context.Background(), &ark.ChatModelConfig{
			BaseURL:     aiConfig.BaseUrl,
			Model:       aiConfig.ModelName,
			APIKey:      aiConfig.ApiKey,
			MaxTokens:   &aiConfig.MaxTokens,
			Temperature: &temperature,
		})

	} else if aiConfig.BaseUrl == "https://api.deepseek.com" {
		toolableChatModel, err = deepseek.NewChatModel(*ctx, &deepseek.ChatModelConfig{
			BaseURL:     aiConfig.BaseUrl,
			Model:       aiConfig.ModelName,
			APIKey:      aiConfig.ApiKey,
			Timeout:     time.Duration(aiConfig.TimeOut) * time.Second,
			MaxTokens:   aiConfig.MaxTokens,
			Temperature: temperature,
		})

	} else {
		chatCfg := &openai.ChatModelConfig{
			BaseURL: aiConfig.BaseUrl,
			Model:   aiConfig.ModelName,
			APIKey:  aiConfig.ApiKey,
			Timeout: time.Duration(aiConfig.TimeOut) * time.Second,
		}
		if isReasoningModel(aiConfig.ModelName) {
			// Reasoning class models (e.g. o1/o3/gpt-5) reject temperature/top_p knobs.
			chatCfg.MaxCompletionTokens = &aiConfig.MaxTokens
		} else {
			chatCfg.MaxTokens = &aiConfig.MaxTokens
			chatCfg.Temperature = &temperature
		}
		toolableChatModel, err = openai.NewChatModel(*ctx, chatCfg)
	}

	if err != nil {
		logger.SugaredLogger.Error(err.Error())
		return nil
	}
	// 初始化所需的 tools
	aiTools := compose.ToolsNodeConfig{
		Tools: []tool.BaseTool{
			tools.GetQueryEconomicDataTool(),
			tools.GetQueryStockPriceInfoTool(),
			tools.GetQueryStockCodeInfoTool(),
			tools.GetQueryMarketNewsTool(),
			tools.GetChoiceStockByIndicatorsTool(),
			tools.GetStockKLineTool(),
			tools.GetInteractiveAnswerDataTool(),
			tools.GetFinancialReportTool(),
			tools.GetQueryStockNewsTool(),
			tools.GetIndustryResearchReportTool(),
			tools.GetQueryBKDictTool(),
		},
	}
	// 创建 agent
	agent, err := react.NewAgent(*ctx, &react.AgentConfig{
		ToolCallingModel: toolableChatModel,
		ToolsConfig:      aiTools,
		MaxStep:          len(aiTools.Tools)*1 + 3,
		MessageModifier: func(ctx context.Context, input []*schema.Message) []*schema.Message {
			return input
		},
	})
	if err != nil {
		logger.SugaredLogger.Error(err.Error())
		return nil
	}
	return agent
}

func isReasoningModel(modelName string) bool {
	model := strings.ToLower(strings.TrimSpace(modelName))
	return strings.HasPrefix(model, "o1") ||
		strings.HasPrefix(model, "o3") ||
		strings.HasPrefix(model, "gpt-5")
}
