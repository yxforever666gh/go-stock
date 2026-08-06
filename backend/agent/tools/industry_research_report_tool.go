package tools

import (
	"context"
	log "go-stock/backend/logger"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/duke-git/lancet/v2/convertor"
	"github.com/duke-git/lancet/v2/strutil"
	"github.com/tidwall/gjson"
)

// @Author spark
// @Date 2025/8/9 18:48
// @Desc
//-----------------------------------------------------------------------------------

func GetIndustryResearchReportTool(provider IndustryResearchProvider) tool.InvokableTool {
	return &IndustryResearchReportTool{provider: provider}
}

type IndustryResearchReportTool struct {
	provider IndustryResearchProvider
}

func (i IndustryResearchReportTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "GetIndustryResearchReport",
		Desc: "获取行业/板块研究报告",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"name": {
				Type:     "string",
				Desc:     "行业/板块行业名称",
				Required: false,
			},
			"code": {
				Type:     "string",
				Desc:     "行业/板块代码",
				Required: true,
			},
		}),
	}, nil
}

func (i IndustryResearchReportTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	code := gjson.Get(argumentsInJSON, "code").String()
	if i.provider == nil {
		return "", ErrToolDataProviderRequired
	}
	code = strutil.ReplaceWithMap(code, map[string]string{
		"-":   "",
		"_":   "",
		"bk":  "",
		"BK":  "",
		"bk0": "",
		"BK0": "",
	})

	log.SugaredLogger.Debugf("code:%s", code)
	codeStr := convertor.ToString(code)
	resp := i.provider.IndustryResearchReports(codeStr, 7)
	md := strings.Builder{}
	for _, a := range resp {
		data := a.(map[string]any)
		md.WriteString(i.provider.IndustryReportInfo(data["infoCode"].(string)))
	}
	log.SugaredLogger.Debugf("codeNum:%s IndustryResearchReport:\n %s", code, md.String())
	return md.String(), nil
}
