package main

import (
	"encoding/base64"
	"strings"

	"go-stock/backend/models"
	appconfig "go-stock/internal/config"
)

func (a *App) versionInfo() *models.VersionInfo {
	content := VersionCommit
	if strings.TrimSpace(content) == "" {
		content = "1.7.3：收敛领域服务与后台任务生命周期，统一 Web-only 启动入口，市场、研究和模拟账户能力保持不变。"
	}
	return &models.VersionInfo{
		Version:           Version,
		Icon:              imageBase(icon),
		Alipay:            imageBase(alipay),
		Wxpay:             imageBase(wxpay),
		Wxgzh:             imageBase(wxgzh),
		Content:           content,
		OfficialStatement: OFFICIAL_STATEMENT,
		SelfUpdateEnabled: appconfig.Load().Update.SelfUpdateEnabled,
		ManualUpdateHint:  manualUpdateHint(),
	}
}

func manualUpdateHint() string {
	return "当前发布包已关闭应用内自动更新。请先运行 stop.bat，替换为新的 zip 解压目录，再运行 start.bat；%LOCALAPPDATA%/go-stock-win 下的用户数据不会丢失。"
}

func imageBase(bytes []byte) string {
	if len(bytes) == 0 {
		return ""
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(bytes)
}
