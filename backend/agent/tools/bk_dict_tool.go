package tools

import (
	"context"
	"encoding/json"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// @Author spark
// @Date 2025/9/27 14:09
// @Desc
// -----------------------------------------------------------------------------------
type ToolQueryBKDict struct {
	provider BKDictProvider
}

func (t ToolQueryBKDict) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "QueryBKDictInfo",
		Desc: "获取所有板块/行业名称或者代码(bkCode,bkName)",
	}, nil
}

func (t ToolQueryBKDict) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	if t.provider == nil {
		return "", ErrToolDataProviderRequired
	}
	resp := t.provider.BoardDictionary()
	bytes, err := json.Marshal(resp)
	return string(bytes), err
}

func GetQueryBKDictTool(provider BKDictProvider) tool.InvokableTool {
	return &ToolQueryBKDict{provider: provider}
}
