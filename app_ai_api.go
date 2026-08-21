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
		content = "1.7.5：修复 XD/XR/DR 除权除息行情名称误判，并支持按 13:30 已落库决策证据进行受控历史卖出纠正。"
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
