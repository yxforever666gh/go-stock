package service

import "go-stock/backend/data"

type NotifyService struct{}

func NewNotifyService() NotifyService {
	return NotifyService{}
}

func (s NotifyService) SendDingDingMessage(message string) string {
	return data.NewDingDingAPI().SendDingDingMessage(message)
}

func (s NotifyService) SendAlert(title, subtitle, content, icon string) {
	go data.NewAlertWindowsApi(title, subtitle, content, icon).SendNotification()
}
