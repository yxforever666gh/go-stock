package tool_logger

import (
	"context"
	"encoding/json"
	"errors"
	"go-stock/backend/logger"
	"io"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
)

// @Author spark
// @Date 2025/8/5 10:21
// @Desc
//-----------------------------------------------------------------------------------

type LoggerCallback struct {
	MessageChanel            chan *schema.Message
	callbacks.HandlerBuilder // 可以用 callbacks.HandlerBuilder 来辅助实现 callback
}

func (cb *LoggerCallback) OnStart(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
	logger.SugaredLogger.Infof("==================")
	inputStr, _ := json.MarshalIndent(input, "", "  ") // nolint: byted_s_returned_err_check
	logger.SugaredLogger.Infof("[OnStart] %s\n", string(inputStr))

	// 不要把 prompt/history 消息透传到 MessageChanel。
	// 这些消息会包含历史 assistant 内容，如果前端把它们当作“流式增量”拼接，会导致回复重复、对话顺序混乱。
	_ = model.ConvCallbackInput(input)
	return ctx
}

func (cb *LoggerCallback) OnEnd(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
	logger.SugaredLogger.Infof("=========[OnEnd]=========")
	outputStr, _ := json.MarshalIndent(output, "", "  ") // nolint: byted_s_returned_err_check
	logger.SugaredLogger.Infof(string(outputStr))
	return ctx
}

func (cb *LoggerCallback) OnError(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
	logger.SugaredLogger.Infof("=========[OnError]=========")
	logger.SugaredLogger.Infof("%s", err.Error())
	return ctx
}

func (cb *LoggerCallback) OnEndWithStreamOutput(ctx context.Context, info *callbacks.RunInfo,
	output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {

	var graphInfoName = react.GraphName

	go func() {
		defer func() {
			if err := recover(); err != nil {
				logger.SugaredLogger.Infof("[OnEndStream] panic err:", err)
			}
		}()

		defer output.Close() // remember to close the stream in defer

		logger.SugaredLogger.Infof("=========[OnEndStream]=========")
		for {
			frame, err := output.Recv()
			if errors.Is(err, io.EOF) {
				// finish
				break
			}
			if err != nil {
				logger.SugaredLogger.Infof("internal error: %s\n", err)
				return
			}

			s, err := json.Marshal(frame)
			if err != nil {
				logger.SugaredLogger.Infof("internal error: %s\n", err)
				return
			}

			if info.Name == graphInfoName { // 仅打印 graph 的输出, 否则每个 stream 节点的输出都会打印一遍
				logger.SugaredLogger.Infof("%s: %s\n", info.Name, string(s))
			}
		}

	}()
	return ctx
}

func (cb *LoggerCallback) OnStartWithStreamInput(ctx context.Context, info *callbacks.RunInfo,
	input *schema.StreamReader[callbacks.CallbackInput]) context.Context {
	defer input.Close()
	return ctx
}
