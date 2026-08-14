package service

type NotifyService struct {
	operations NotifyOperations
}

func NewNotifyService(operations NotifyOperations) NotifyService {
	return NotifyService{operations: operations}
}

func (s NotifyService) SendDingDingMessage(message string) string {
	if s.operations == nil {
		return "notification service unavailable"
	}
	return s.operations.SendDingDingMessage(message)
}

func (s NotifyService) SendAlert(title, subtitle, content, icon string) {
	if s.operations == nil {
		return
	}
	go s.operations.SendAlert(title, subtitle, content, icon)
}
