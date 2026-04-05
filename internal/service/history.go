package service

import (
	"go-stock/backend/data"
	"go-stock/backend/models"

	"github.com/cloudwego/eino/schema"
)

type HistoryService struct{}

func NewHistoryService() HistoryService {
	return HistoryService{}
}

func (s HistoryService) DeleteSession(sessionID string) error {
	return data.NewAgentChatHistoryService().DeleteSession(sessionID)
}

func (s HistoryService) EnsureSession(sessionID, title string, aiConfigID int, modelName string) (*models.AgentChatSession, error) {
	return data.NewAgentChatHistoryService().EnsureSession(sessionID, title, aiConfigID, modelName)
}

func (s HistoryService) TrimSessions(maxSessions int) error {
	return data.NewAgentChatHistoryService().TrimSessions(maxSessions)
}

func (s HistoryService) ListRecentSessions(limit int) ([]models.AgentChatSession, error) {
	return data.NewAgentChatHistoryService().ListRecentSessions(limit)
}

func (s HistoryService) ListSessionMessages(sessionID string, limit int) ([]models.AgentChatMessage, error) {
	return data.NewAgentChatHistoryService().ListSessionMessages(sessionID, limit)
}

func (s HistoryService) GetSession(sessionID string) (*models.AgentChatSession, error) {
	return data.NewAgentChatHistoryService().GetSession(sessionID)
}

func (s HistoryService) FirstUserQuestion(sessionID string) (string, error) {
	return data.NewAgentChatHistoryService().FirstUserQuestion(sessionID)
}

func (s HistoryService) UpdateSessionTitle(sessionID, title string) error {
	return data.NewAgentChatHistoryService().UpdateSessionTitle(sessionID, title)
}

func (s HistoryService) UpdateSessionModel(sessionID string, aiConfigID int, modelName string) error {
	return data.NewAgentChatHistoryService().UpdateSessionModel(sessionID, aiConfigID, modelName)
}

func (s HistoryService) ListSessionMessagesForAgent(sessionID string, limit int) ([]*schema.Message, error) {
	return data.NewAgentChatHistoryService().ListSessionMessagesForAgent(sessionID, limit)
}

func (s HistoryService) AppendMessage(sessionID, role, content, reasoning string) error {
	return data.NewAgentChatHistoryService().AppendMessage(sessionID, role, content, reasoning)
}

func (s HistoryService) TrimSessionMessages(sessionID string, maxMessages int) error {
	return data.NewAgentChatHistoryService().TrimSessionMessages(sessionID, maxMessages)
}
