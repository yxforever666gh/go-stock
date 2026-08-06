package tools

import (
	"context"
	"encoding/json"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"go-stock/backend/util"
	"strings"
)

// @Author spark
// @Date 2025/8/4 16:38
// @Desc
//-----------------------------------------------------------------------------------

func GetQueryEconomicDataTool(provider EconomicDataProvider) tool.InvokableTool {
	return &ToolQueryEconomicData{provider: provider}
}

type ToolQueryEconomicData struct {
	provider EconomicDataProvider
}

func (t ToolQueryEconomicData) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "QueryEconomicData",
		Desc: "查询宏观经济数据(GDP,CPI,PPI,PMI)",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"flag": {
				Type:     "string",
				Desc:     "all:宏观经济数据(GDP,CPI,PPI,PMI);GDP:国内生产总值;CPI:居民消费价格指数;PPI:工业品出厂价格指数;PMI:采购经理人指数",
				Required: false,
			},
		}),
	}, nil
}

func (t ToolQueryEconomicData) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	parms := map[string]any{}
	err := json.Unmarshal([]byte(argumentsInJSON), &parms)
	if err != nil {
		return "", err
	}
	var market strings.Builder
	if t.provider == nil {
		return "", ErrToolDataProviderRequired
	}

	switch parms["flag"].(string) {
	case "GDP":
		res := t.provider.GDP()
		md := util.MarkdownTableWithTitle("国内生产总值(GDP)", res.GDPResult.Data)
		market.WriteString(md)
	case "CPI":
		res2 := t.provider.CPI()
		md2 := util.MarkdownTableWithTitle("居民消费价格指数(CPI)", res2.CPIResult.Data)
		market.WriteString(md2)
	case "PPI":
		res3 := t.provider.PPI()
		md3 := util.MarkdownTableWithTitle("工业品出厂价格指数(PPI)", res3.PPIResult.Data)
		market.WriteString(md3)
	case "PMI":
		res4 := t.provider.PMI()
		md4 := util.MarkdownTableWithTitle("商品价格指数(PMI)", res4.PMIResult.Data)
		market.WriteString(md4)
	default:
		res := t.provider.GDP()
		md := util.MarkdownTableWithTitle("国内生产总值(GDP)", res.GDPResult.Data)
		market.WriteString(md)
		res2 := t.provider.CPI()
		md2 := util.MarkdownTableWithTitle("居民消费价格指数(CPI)", res2.CPIResult.Data)
		market.WriteString(md2)
		res3 := t.provider.PPI()
		md3 := util.MarkdownTableWithTitle("工业品出厂价格指数(PPI)", res3.PPIResult.Data)
		market.WriteString(md3)
		res4 := t.provider.PMI()
		md4 := util.MarkdownTableWithTitle("采购经理人指数(PMI)", res4.PMIResult.Data)
		market.WriteString(md4)
	}
	return market.String(), nil
}
