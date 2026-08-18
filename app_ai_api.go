package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"go-stock/backend/logger"
	"go-stock/backend/models"
	appconfig "go-stock/internal/config"

	"github.com/go-resty/resty/v2"
)

func (a *App) NewChatStream(stock, stockCode, question string, aiConfigId int, sysPromptId *int, enableTools bool, think bool) {
	order := a.services.AI.ResolveAIFallbackOrder(aiConfigId)
	if len(order) == 0 {
		emitEvent(a.ctx, "newChatStream", "DONE")
		return
	}

	var lastMsgs []map[string]any
	for idx, targetAIConfigID := range order {
		msgs := a.services.AI.NewChatStream(a.ctx, stock, stockCode, question, targetAIConfigID, sysPromptId, resolveChatTools(enableTools, a.AiTools), think)
		currentMsgs := make([]map[string]any, 0, 128)
		bufferedErrors := make([]map[string]any, 0, 8)
		for msg := range msgs {
			currentMsgs = append(currentMsgs, msg)
			if normalizeMsgCode(msg["code"]) == 0 {
				bufferedErrors = append(bufferedErrors, msg)
				continue
			}
			msg["aiConfigId"] = targetAIConfigID
			emitEvent(a.ctx, "newChatStream", msg)
		}
		lastMsgs = currentMsgs
		if !shouldChatFailover(currentMsgs) {
			if !chatAttemptHasVisibleContent(currentMsgs) {
				for _, msg := range bufferedErrors {
					emitEvent(a.ctx, "newChatStream", msg)
				}
			}
			break
		}
		if idx == len(order)-1 {
			for _, msg := range bufferedErrors {
				emitEvent(a.ctx, "newChatStream", msg)
			}
			break
		}
		if idx < len(order)-1 {
			logger.SugaredLogger.Warnf("股票分析AI请求失败，自动切换备用模型。from=%d to=%d attempt=%d", targetAIConfigID, order[idx+1], idx+2)
			go emitEvent(a.ctx, "warnMsg", "股票分析已自动切换到备用模型继续重试")
		}
	}

	_ = lastMsgs
	emitEvent(a.ctx, "newChatStream", "DONE")
}

func (a *App) SaveAIResponseResult(stockCode, stockName, result, chatId, question string, aiConfigId int) {
	a.services.AI.SaveAIResponseResult(a.ctx, stockCode, stockName, result, chatId, question, aiConfigId)
}

func (a *App) GetAIResponseResult(stock string) *models.AIResponseResult {
	return a.services.AI.GetAIResponseResult(a.ctx, stock)
}

func (a *App) ShareAnalysis(stockCode, stockName string) string {
	res := a.services.AI.GetAIResponseResult(a.ctx, stockCode)
	if res == nil || len(res.Content) <= 100 {
		return "分析结果异常"
	}
	uploadURL := shareUploadURL()
	if uploadURL == "" {
		return "公开版未配置分享服务，请改用 Markdown、图片或 Word 导出。"
	}
	analysisTime := res.CreatedAt.Format("2006/01/02")
	logger.SugaredLogger.Infof("%s analysisTime:%s", res.CreatedAt, analysisTime)
	response, err := resty.New().SetHeader("ua-x", "go-stock").R().SetFormData(map[string]string{
		"text":         res.Content,
		"stockCode":    stockCode,
		"stockName":    stockName,
		"analysisTime": analysisTime,
	}).Post(uploadURL)
	if err != nil {
		return err.Error()
	}
	return response.String()
}

func (a *App) SaveAsMarkdown(stockCode, stockName string) string {
	res := a.services.AI.GetAIResponseResult(a.ctx, stockCode)
	if res == nil || len(res.Content) <= 100 {
		return "分析结果异常,无法保存。"
	}
	analysisTime := res.CreatedAt.Format("2006-01-02_15_04_05")
	file, err := saveFileWithDialog(a.ctx, runtimeSaveFileOptions{
		Title:           "保存为Markdown",
		DefaultFilename: fmt.Sprintf("%s[%s]AI分析结果_%s.md", stockName, stockCode, analysisTime),
		Filters: []runtimeFileFilter{
			{
				DisplayName: "Markdown",
				Pattern:     "*.md;*.markdown",
			},
		},
	})
	if err != nil {
		return err.Error()
	}
	err = os.WriteFile(file, []byte(res.Content), 0o644)
	if err != nil {
		return err.Error()
	}
	return "已保存至：" + file
}

func (a *App) GetPromptTemplates(name, promptType string) *[]models.PromptTemplate {
	return a.services.AI.GetPromptTemplates(name, promptType)
}

func (a *App) AddPrompt(prompt models.Prompt) string {
	promptTemplate := models.PromptTemplate{
		ID:      prompt.ID,
		Content: prompt.Content,
		Name:    prompt.Name,
		Type:    prompt.Type,
	}
	return a.services.AI.AddPrompt(promptTemplate)
}

func (a *App) DelPrompt(id uint) string {
	return a.services.AI.DelPrompt(id)
}

func (a *App) GetVersionInfo() *models.VersionInfo {
	content := VersionCommit
	if strings.TrimSpace(content) == "" {
		content = "1.6.4：生命周期每15分钟复查实时行情、分钟量价与增量事件；关键行情失败不调用AI，激活和卖出必须引用本轮证据。"
	}
	return &models.VersionInfo{
		Version:           Version,
		Icon:              GetImageBase(icon),
		Alipay:            GetImageBase(alipay),
		Wxpay:             GetImageBase(wxpay),
		Wxgzh:             GetImageBase(wxgzh),
		Content:           content,
		OfficialStatement: OFFICIAL_STATEMENT,
		SelfUpdateEnabled: appconfig.Load().Update.SelfUpdateEnabled,
		ManualUpdateHint:  manualUpdateHint(),
	}
}

func manualUpdateHint() string {
	return "当前发布包已关闭应用内自动更新。请先运行 stop.bat，替换为新的 zip 解压目录，再运行 start.bat；%LOCALAPPDATA%/go-stock-win 下的用户数据不会丢失。"
}

func GetImageBase(bytes []byte) string {
	if len(bytes) == 0 {
		return ""
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(bytes)
}

func (a *App) GetAiConfigs() []*models.AIConfig {
	return a.services.AI.GetAiConfigs()
}
