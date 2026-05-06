package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"go-stock/backend/data"
	"go-stock/backend/logger"

	"github.com/samber/lo"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) UpdateConfig(settingConfig *data.SettingConfig) string {
	s1, _ := json.Marshal(settingConfig)
	logger.SugaredLogger.Infof("UpdateConfig:%s", s1)
	if settingConfig.RefreshInterval > 0 {
		if entryID, exists := a.getCronEntry("MonitorStockPrices"); exists {
			a.cron.Remove(entryID)
		}
		id, _ := a.cron.AddFunc(fmt.Sprintf("@every %ds", settingConfig.RefreshInterval), func() {
			MonitorStockPrices(a)
		})
		a.setCronEntry("MonitorStockPrices", id)
	}

	res := a.services.Config.UpdateConfig(settingConfig)
	if strings.Contains(res, "保存成功") {
		a.reloadSummaryStockNewsCron(settingConfig)
	}
	return res
}

func (a *App) GetConfig() *data.SettingConfig {
	return a.services.Config.GetConfig()
}

func (a *App) ExportConfig() string {
	config := a.services.Config.ExportConfig()
	file, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:                "导出配置文件",
		CanCreateDirectories: true,
		DefaultFilename:      "config.json",
	})
	if err != nil {
		logger.SugaredLogger.Errorf("导出配置文件失败:%s", err.Error())
		return err.Error()
	}
	err = os.WriteFile(file, []byte(config), os.ModePerm)
	if err != nil {
		logger.SugaredLogger.Errorf("导出配置文件失败:%s", err.Error())
		return err.Error()
	}
	return "导出成功:" + file
}

func (a *App) TestAIConfig(aiConfigId int) *data.AIModelTestResult {
	start := time.Now()
	result := &data.AIModelTestResult{
		Success:  false,
		Message:  "测试失败",
		Protocol: data.AIAPIProtocolChatCompletions,
	}
	if aiConfigId <= 0 {
		result.Message = "请先保存 AI 配置后再测试"
		return result
	}
	setting := a.services.Config.GetConfig()
	if setting == nil || len(setting.AiConfigs) == 0 {
		result.Message = "未找到 AI 配置"
		return result
	}
	aiConfig, ok := lo.Find(setting.AiConfigs, func(item *data.AIConfig) bool {
		return item != nil && int(item.ID) == aiConfigId
	})
	if !ok || aiConfig == nil {
		result.Message = "未找到指定 AI 配置，请保存后重试"
		return result
	}
	result.Protocol = data.NormalizeAIAPIProtocol(aiConfig.ApiProtocol)
	result.Model = strings.TrimSpace(aiConfig.ModelName)
	if strings.TrimSpace(aiConfig.BaseUrl) == "" || strings.TrimSpace(aiConfig.ApiKey) == "" || strings.TrimSpace(aiConfig.ModelName) == "" {
		result.Message = "请完整填写接口地址、API Key 和模型名称"
		return result
	}

	openAI := data.NewOpenAiWithConfig(context.Background(), aiConfig)
	content, _, modelName, err := openAI.CompleteChat([]map[string]any{
		{"role": "user", "content": "请只回复 OK"},
	}, false)
	result.LatencyMs = time.Since(start).Milliseconds()
	if strings.TrimSpace(modelName) != "" {
		result.Model = strings.TrimSpace(modelName)
	}
	if err != nil {
		result.Message = err.Error()
		return result
	}
	content = strings.TrimSpace(content)
	if content == "" {
		result.Message = "模型返回内容为空"
		return result
	}
	result.Success = true
	result.Message = "测试成功"
	runes := []rune(content)
	if len(runes) > 120 {
		content = string(runes[:120])
	}
	result.ContentPreview = content
	return result
}
