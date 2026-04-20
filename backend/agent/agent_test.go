package agent

import (
	"context"
	"errors"
	"go-stock/backend/agent/tool_logger"
	"go-stock/backend/data"
	"go-stock/backend/db"
	"go-stock/backend/logger"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent"
	"github.com/cloudwego/eino/schema"
	"github.com/duke-git/lancet/v2/fileutil"
)

// @Author spark
// @Date 2025/8/4 17:32
// @Desc
//-----------------------------------------------------------------------------------

func TestGetStockAiAgent(t *testing.T) {
	// 这是一个需要外部模型服务/网络环境的集成测试，默认跳过，避免 CI/本地 go test 卡住。
	if os.Getenv("RUN_INTEGRATION_TESTS") != "1" {
		t.Skip("skip integration test; set RUN_INTEGRATION_TESTS=1 to enable")
	}

	ctx := context.Background()
	db.Init("../../data/stock.db")
	config := data.GetSettingConfig()
	aiAgent := GetStockAiAgent(&ctx, *config.AiConfigs[0])

	opt := []agent.AgentOption{
		agent.WithComposeOptions(compose.WithCallbacks(&tool_logger.LoggerCallback{})),
		//react.WithChatModelOptions(ark.WithCache(cacheOption)),
	}

	sr, err := aiAgent.Stream(ctx, []*schema.Message{
		{
			Role:    schema.System,
			Content: config.Settings.Prompt + "",
		},
		{
			Role:    schema.User,
			Content: "结合以上提供的宏观经济数据/市场指数行情/国内外市场资讯/电报/会议/事件/投资者关注的问题，\n结合宏观经济，事件驱动，政策支持，投资者关注的问题，分析当前市场情绪和热点 找出有潜力/优质的板块/行业/概念/标的/主题，\n多因子深度分析计算上涨或下跌的逻辑和概率，\n最后按风险和投资周期给出具体推荐标的操作建议",
		},
	}, opt...)
	if err != nil {
		logger.SugaredLogger.Errorf("stream error: %v", err)
		return
	}

	defer sr.Close() // remember to close the stream

	md := strings.Builder{}
	for {
		msg, err := sr.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				// finish
				break
			}
			// error
			logger.SugaredLogger.Errorf("failed to recv: %v", err)
			return
		}
		logger.SugaredLogger.Infof("stream recv: %v", msg)
		if msg.ReasoningContent != "" {
			md.WriteString(msg.ReasoningContent)
		}
		if msg.Content != "" {
			md.WriteString(msg.Content)
		}
	}
	logger.SugaredLogger.Info(md.String())
	//logger.SugaredLogger.Infof("stream done:\n%s", md.String())
}

func TestAgent(t *testing.T) {
	// 这是一个需要外部模型服务/网络环境的集成测试，默认跳过，避免 CI/本地 go test 卡住。
	if os.Getenv("RUN_INTEGRATION_TESTS") != "1" {
		t.Skip("skip integration test; set RUN_INTEGRATION_TESTS=1 to enable")
	}

	db.Init("../../data/stock.db")

	md := strings.Builder{}
	ch := NewStockAiAgentApi().Chat("分析一下立讯精密", 0, nil)
	for message := range ch {
		logger.SugaredLogger.Infof("res:%s", message.String())
		md.WriteString(message.String())
	}
	logger.SugaredLogger.Info(md.String())
	if err := fileutil.WriteStringToFile("../../data/result.md", md.String(), false); err != nil {
		t.Fatalf("write result.md failed: %v", err)
	}
}
