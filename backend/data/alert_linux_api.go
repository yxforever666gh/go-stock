//go:build linux
// +build linux

package data

import "go-stock/backend/logger"

// AlertWindowsApi keeps a cross-platform notification interface.
type AlertWindowsApi struct {
	AppID   string
	Title   string
	Content string
	Icon    string
}

func NewAlertWindowsApi(AppID string, Title string, Content string, Icon string) *AlertWindowsApi {
	return &AlertWindowsApi{
		AppID:   AppID,
		Title:   Title,
		Content: Content,
		Icon:    Icon,
	}
}

func (a AlertWindowsApi) SendNotification() bool {
	if !GetSettingConfig().LocalPushEnable {
		logger.SugaredLogger.Error("本地推送未开启")
		return false
	}

	// Linux desktop notifications are intentionally disabled in this build.
	logger.SugaredLogger.Infof("skip local notification on linux: %s - %s", a.Title, a.Content)
	return true
}
