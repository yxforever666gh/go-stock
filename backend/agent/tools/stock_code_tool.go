package tools

import (
	"context"
	"encoding/json"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"go-stock/backend/data"
)

// @Author spark
// @Date 2025/8/4 18:25
// @Desc
//-----------------------------------------------------------------------------------

func GetQueryStockCodeInfoTool() tool.InvokableTool {
	return &QueryStockCodeInfo{}
}

type QueryStockCodeInfo struct {
}

func (q QueryStockCodeInfo) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "QueryStockCodeInfo",
		Desc: "查询股票/指数信息(股票/指数名称,股票/指数代码,股票/指数拼音,股票/指数拼音首字母,股票/指数交易所等",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"searchWord": {
				Type:     "string",
				Desc:     "股票搜索关键词",
				Required: true,
			},
		}),
	}, nil
}

func (q QueryStockCodeInfo) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	parms := map[string]any{}
	err := json.Unmarshal([]byte(argumentsInJSON), &parms)
	if err != nil {
		return "", err
	}
	stockList := data.NewStockDataApi().GetStockList(parms["searchWord"].(string))
	marshal, err := json.Marshal(stockList)
	if err != nil {
		return "", err
	}
	return string(marshal), nil
}
