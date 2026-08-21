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
		content = "1.7.6：买入以 5 万元实际现金支出为目标，按最小整手向上取整；现金不足时回退至最大可承担整手，并补记中际旭创历史买入。"
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
