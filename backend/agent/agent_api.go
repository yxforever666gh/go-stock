package agent

import (
	"context"
	"errors"
	"fmt"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
	"github.com/samber/lo"
	"go-stock/backend/agent/tool_logger"
	"go-stock/backend/data"
	"go-stock/backend/logger"
	"io"
)

// @Author spark
// @Date 2025/8/7 9:07
// @Desc
// -----------------------------------------------------------------------------------
type StockAiAgent struct {
	*react.Agent
}

func NewStockAiAgentApi() *StockAiAgent {
	return &StockAiAgent{}
}

func (receiver StockAiAgent) newStockAiAgent(ctx *context.Context, aiConfigId int) (*StockAiAgent, string) {
	settingConfig := data.GetSettingConfig()
	if len(settingConfig.AiConfigs) == 0 {
		return nil, "AI智能体初始化失败，请检查 AI 模型配置（服务地址、模型名、API Key）"
	}
	aiConfig, ok := lo.Find(settingConfig.AiConfigs, func(item *data.AIConfig) bool {
		return uint(aiConfigId) == item.ID
	})
	if !ok {
		aiConfig = data.SelectPrimaryAIConfig(settingConfig.AiConfigs)
	}
	if aiConfig == nil {
		return nil, "AI智能体初始化失败，请检查 AI 模型配置（服务地址、模型名、API Key）"
	}
	if data.NormalizeAIAPIProtocol(aiConfig.ApiProtocol) != data.AIAPIProtocolChatCompletions {
		return nil, "AI智能体暂不支持 OpenAI Responses 或 Anthropic Messages，请切换到 Chat Completions 协议的模型配置"
	}
	agentInstance := GetStockAiAgent(ctx, *aiConfig)
	if agentInstance == nil {
		return nil, "AI智能体初始化失败，请检查 AI 模型配置（服务地址、模型名、API Key）"
	}
	return &StockAiAgent{
		Agent: agentInstance,
	}, ""
}

func (receiver StockAiAgent) Chat(question string, aiConfigId int, sysPromptId *int) chan *schema.Message {
	return receiver.ChatWithMessages([]*schema.Message{
		{
			Role:    schema.User,
			Content: question,
		},
	}, aiConfigId, sysPromptId)
}

func (receiver StockAiAgent) ChatWithMessages(messages []*schema.Message, aiConfigId int, sysPromptId *int) chan *schema.Message {
	ch := make(chan *schema.Message, 512)
	ctx := context.Background()
	stockAiAgent, initErr := receiver.newStockAiAgent(&ctx, aiConfigId)
	if stockAiAgent == nil || stockAiAgent.Agent == nil {
		if initErr == "" {
			initErr = "AI智能体初始化失败，请检查 AI 模型配置（服务地址、模型名、API Key）"
		}
		pushAgentError(ch, initErr)
		return ch
	}

	sysPrompt := ""
	if sysPromptId == nil || *sysPromptId == 0 {
		sysPrompt = "你现在扮演一位拥有20年实战经验的顶级股票投资大师，精通价值投资、趋势交易、量化分析等多种策略。你擅长结合宏观经济、行业周期和企业基本面进行全方位、精准的多维分析，尤其对A股、港股、美股市场有深刻理解，始终秉持“风险控制第一”的原则，善于用通俗易懂的方式传授投资智慧。"
	} else {
		sysPrompt = data.NewPromptTemplateApi().GetPromptTemplateByID(*sysPromptId)
	}
	agentOption := []agent.AgentOption{
		agent.WithComposeOptions(compose.WithCallbacks(&tool_logger.LoggerCallback{MessageChanel: ch})),
		//react.WithChatModelOptions(ark.WithCache(cacheOption)),
	}

	go func() {
		defer close(ch)
		streamMessages := []*schema.Message{
			{
				Role:    schema.System,
				Content: sysPrompt,
			},
		}
		for _, msg := range messages {
			if msg == nil {
				continue
			}
			if msg.Content == "" {
				continue
			}
			streamMessages = append(streamMessages, msg)
		}

		sr, err := stockAiAgent.Stream(ctx, streamMessages, agentOption...)
		if err != nil {
			logger.SugaredLogger.Errorf("stream error: %v", err)
			ch <- &schema.Message{
				Role:    schema.Assistant,
				Content: fmt.Sprintf("AI智能体请求失败：%v", err),
				ResponseMeta: &schema.ResponseMeta{
					FinishReason: "stop",
				},
			}
			return
		}
		defer sr.Close()
		for {
			msg, err := sr.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) {
					// finish
					break
				}
				// error
				logger.SugaredLogger.Errorf("failed to recv: %v", err)
				ch <- &schema.Message{
					Role:    schema.Assistant,
					Content: fmt.Sprintf("AI智能体响应中断：%v", err),
					ResponseMeta: &schema.ResponseMeta{
						FinishReason: "stop",
					},
				}
				break
			}
			logger.SugaredLogger.Infof("stream: %s", msg.String())
			ch <- msg
		}
		ch <- &schema.Message{
			Role: schema.Assistant,
			ResponseMeta: &schema.ResponseMeta{
				FinishReason: "stop",
			},
		}
	}()
	return ch
}

func pushAgentError(ch chan *schema.Message, message string) {
	defer close(ch)
	ch <- &schema.Message{
		Role:    schema.Assistant,
		Content: message,
		ResponseMeta: &schema.ResponseMeta{
			FinishReason: "stop",
		},
	}
}
