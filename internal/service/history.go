package service

import (
	"go-stock/backend/models"

	"github.com/cloudwego/eino/schema"
)

type HistoryService struct {
	operations HistoryOperations
}

func NewHistoryService(operations HistoryOperations) HistoryService {
	return HistoryService{operations: operations}
}

func (s HistoryService) DeleteSession(sessionID string) error {
	return s.operations.DeleteSession(sessionID)
}

func (s HistoryService) EnsureSession(sessionID, title string, aiConfigID int, modelName string) (*models.AgentChatSession, error) {
	return s.operations.EnsureSession(sessionID, title, aiConfigID, modelName)
}

func (s HistoryService) TrimSessions(maxSessions int) error {
	return s.operations.TrimSessions(maxSessions)
}

func (s HistoryService) ListRecentSessions(limit int) ([]models.AgentChatSession, error) {
	return s.operations.ListRecentSessions(limit)
}

func (s HistoryService) ListSessionMessages(sessionID string, limit int) ([]models.AgentChatMessage, error) {
	return s.operations.ListSessionMessages(sessionID, limit)
}

func (s HistoryService) GetSession(sessionID string) (*models.AgentChatSession, error) {
	return s.operations.GetSession(sessionID)
}

func (s HistoryService) FirstUserQuestion(sessionID string) (string, error) {
	return s.operations.FirstUserQuestion(sessionID)
}

func (s HistoryService) UpdateSessionTitle(sessionID, title string) error {
	return s.operations.UpdateSessionTitle(sessionID, title)
}

func (s HistoryService) UpdateSessionModel(sessionID string, aiConfigID int, modelName string) error {
	return s.operations.UpdateSessionModel(sessionID, aiConfigID, modelName)
}

func (s HistoryService) ListSessionMessagesForAgent(sessionID string, limit int) ([]*schema.Message, error) {
	return s.operations.ListSessionMessagesForAgent(sessionID, limit)
}

func (s HistoryService) AppendMessage(sessionID, role, content, reasoning string) error {
	return s.operations.AppendMessage(sessionID, role, content, reasoning)
}

func (s HistoryService) TrimSessionMessages(sessionID string, maxMessages int) error {
	return s.operations.TrimSessionMessages(sessionID, maxMessages)
}
