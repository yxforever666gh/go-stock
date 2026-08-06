package data

import (
	"errors"
	"go-stock/backend/db"
	"go-stock/backend/models"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
	"gorm.io/gorm"
)

const (
	DefaultAgentSessionLimit = models.DefaultAgentSessionLimit
	DefaultAgentMessageLimit = models.DefaultAgentMessageLimit
)

type AgentChatHistoryService struct{}

func NewAgentChatHistoryService() *AgentChatHistoryService {
	return &AgentChatHistoryService{}
}

func (s *AgentChatHistoryService) EnsureSession(sessionId, title string, aiConfigId int, modelName string) (*models.AgentChatSession, error) {
	sessionId = strings.TrimSpace(sessionId)
	if sessionId == "" {
		return nil, errors.New("sessionId is required")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "新对话"
	}

	session := &models.AgentChatSession{}
	err := db.Dao.Where("session_id = ?", sessionId).First(session).Error
	if err == nil {
		updateMap := map[string]any{}
		if modelName != "" && session.ModelName == "" {
			updateMap["model_name"] = modelName
		}
		if aiConfigId > 0 && session.AiConfigId == 0 {
			updateMap["ai_config_id"] = uint(aiConfigId)
		}
		if len(updateMap) > 0 {
			if uErr := db.Dao.Model(session).Updates(updateMap).Error; uErr == nil {
				if v, ok := updateMap["model_name"].(string); ok {
					session.ModelName = v
				}
				if v, ok := updateMap["ai_config_id"].(uint); ok {
					session.AiConfigId = v
				}
			}
		}
		return session, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	now := time.Now()
	session = &models.AgentChatSession{
		SessionId:     sessionId,
		Title:         title,
		AiConfigId:    uint(maxInt(aiConfigId, 0)),
		ModelName:     strings.TrimSpace(modelName),
		LastMessageAt: &now,
		MessageCount:  0,
		IsPinned:      false,
	}
	if err = db.Dao.Create(session).Error; err != nil {
		return nil, err
	}
	return session, nil
}

func (s *AgentChatHistoryService) GetSession(sessionId string) (*models.AgentChatSession, error) {
	sessionId = strings.TrimSpace(sessionId)
	if sessionId == "" {
		return nil, gorm.ErrRecordNotFound
	}
	session := &models.AgentChatSession{}
	err := db.Dao.Where("session_id = ?", sessionId).First(session).Error
	if err != nil {
		return nil, err
	}
	return session, nil
}

func (s *AgentChatHistoryService) ListRecentSessions(limit int) ([]models.AgentChatSession, error) {
	if limit <= 0 {
		limit = DefaultAgentSessionLimit
	}
	list := make([]models.AgentChatSession, 0, limit)
	err := db.Dao.Model(&models.AgentChatSession{}).
		Order("is_pinned desc, last_message_at desc, updated_at desc").
		Limit(limit).
		Find(&list).Error
	return list, err
}

func (s *AgentChatHistoryService) ListSessionMessages(sessionId string, limit int) ([]models.AgentChatMessage, error) {
	sessionId = strings.TrimSpace(sessionId)
	if sessionId == "" {
		return []models.AgentChatMessage{}, nil
	}
	if limit <= 0 {
		limit = DefaultAgentMessageLimit
	}

	descRows := make([]models.AgentChatMessage, 0, limit)
	err := db.Dao.Model(&models.AgentChatMessage{}).
		Where("session_id = ?", sessionId).
		Order("seq desc, id desc").
		Limit(limit).
		Find(&descRows).Error
	if err != nil {
		return nil, err
	}

	list := make([]models.AgentChatMessage, 0, len(descRows))
	for i := len(descRows) - 1; i >= 0; i-- {
		list = append(list, descRows[i])
	}
	return list, nil
}

func (s *AgentChatHistoryService) ListSessionMessagesForAgent(sessionId string, limit int) ([]*schema.Message, error) {
	rows, err := s.ListSessionMessages(sessionId, limit)
	if err != nil {
		return nil, err
	}
	messages := make([]*schema.Message, 0, len(rows))
	for _, item := range rows {
		if strings.TrimSpace(item.Content) == "" {
			continue
		}
		role := schema.Assistant
		if item.Role == "user" {
			role = schema.User
		}
		messages = append(messages, &schema.Message{
			Role:    role,
			Content: item.Content,
		})
	}
	return messages, nil
}

func (s *AgentChatHistoryService) AppendMessage(sessionId, role, content, reasoning string) error {
	sessionId = strings.TrimSpace(sessionId)
	if sessionId == "" {
		return errors.New("sessionId is required")
	}
	content = strings.TrimSpace(content)
	reasoning = strings.TrimSpace(reasoning)
	if content == "" && reasoning == "" {
		return nil
	}
	role = strings.TrimSpace(strings.ToLower(role))
	if role != "assistant" {
		role = "user"
	}

	return db.Dao.Transaction(func(tx *gorm.DB) error {
		seq := 0
		if err := tx.Model(&models.AgentChatMessage{}).
			Where("session_id = ?", sessionId).
			Select("COALESCE(MAX(seq), 0)").
			Scan(&seq).Error; err != nil {
			return err
		}
		seq++

		if err := tx.Create(&models.AgentChatMessage{
			SessionId: sessionId,
			Role:      role,
			Content:   content,
			Reasoning: reasoning,
			Seq:       seq,
		}).Error; err != nil {
			return err
		}

		now := time.Now()
		return tx.Model(&models.AgentChatSession{}).
			Where("session_id = ?", sessionId).
			Updates(map[string]any{
				"last_message_at": now,
				"message_count":   gorm.Expr("message_count + ?", 1),
			}).Error
	})
}

func (s *AgentChatHistoryService) TrimSessionMessages(sessionId string, maxMessages int) error {
	sessionId = strings.TrimSpace(sessionId)
	if sessionId == "" {
		return nil
	}
	if maxMessages <= 0 {
		maxMessages = DefaultAgentMessageLimit
	}

	return db.Dao.Transaction(func(tx *gorm.DB) error {
		keepIDs := make([]uint, 0, maxMessages)
		if err := tx.Model(&models.AgentChatMessage{}).
			Where("session_id = ?", sessionId).
			Order("seq desc, id desc").
			Limit(maxMessages).
			Pluck("id", &keepIDs).Error; err != nil {
			return err
		}

		q := tx.Where("session_id = ?", sessionId)
		if len(keepIDs) > 0 {
			q = q.Where("id NOT IN ?", keepIDs)
		}
		if err := q.Delete(&models.AgentChatMessage{}).Error; err != nil {
			return err
		}

		count := int64(0)
		if err := tx.Model(&models.AgentChatMessage{}).
			Where("session_id = ?", sessionId).
			Count(&count).Error; err != nil {
			return err
		}
		return tx.Model(&models.AgentChatSession{}).
			Where("session_id = ?", sessionId).
			Update("message_count", count).Error
	})
}

func (s *AgentChatHistoryService) TrimSessions(maxSessions int) error {
	if maxSessions <= 0 {
		maxSessions = DefaultAgentSessionLimit
	}
	deleteSessionIDs := make([]string, 0)
	err := db.Dao.Model(&models.AgentChatSession{}).
		Order("is_pinned desc, last_message_at desc, updated_at desc").
		Offset(maxSessions).
		Pluck("session_id", &deleteSessionIDs).Error
	if err != nil {
		return err
	}
	if len(deleteSessionIDs) == 0 {
		return nil
	}

	return db.Dao.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("session_id IN ?", deleteSessionIDs).Delete(&models.AgentChatMessage{}).Error; err != nil {
			return err
		}
		return tx.Where("session_id IN ?", deleteSessionIDs).Delete(&models.AgentChatSession{}).Error
	})
}

func (s *AgentChatHistoryService) DeleteSession(sessionId string) error {
	sessionId = strings.TrimSpace(sessionId)
	if sessionId == "" {
		return nil
	}
	return db.Dao.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("session_id = ?", sessionId).Delete(&models.AgentChatMessage{}).Error; err != nil {
			return err
		}
		return tx.Where("session_id = ?", sessionId).Delete(&models.AgentChatSession{}).Error
	})
}

func (s *AgentChatHistoryService) UpdateSessionTitle(sessionId, title string) error {
	sessionId = strings.TrimSpace(sessionId)
	title = strings.TrimSpace(title)
	if sessionId == "" || title == "" {
		return nil
	}
	return db.Dao.Model(&models.AgentChatSession{}).
		Where("session_id = ?", sessionId).
		Update("title", title).Error
}

func (s *AgentChatHistoryService) UpdateSessionModel(sessionId string, aiConfigId int, modelName string) error {
	sessionId = strings.TrimSpace(sessionId)
	modelName = strings.TrimSpace(modelName)
	if sessionId == "" {
		return nil
	}
	updateMap := map[string]any{}
	if aiConfigId > 0 {
		updateMap["ai_config_id"] = uint(aiConfigId)
	}
	if modelName != "" {
		updateMap["model_name"] = modelName
	}
	if len(updateMap) == 0 {
		return nil
	}
	return db.Dao.Model(&models.AgentChatSession{}).
		Where("session_id = ?", sessionId).
		Updates(updateMap).Error
}

func (s *AgentChatHistoryService) FirstUserQuestion(sessionId string) (string, error) {
	sessionId = strings.TrimSpace(sessionId)
	if sessionId == "" {
		return "", nil
	}
	record := &models.AgentChatMessage{}
	err := db.Dao.Model(&models.AgentChatMessage{}).
		Where("session_id = ? AND role = ?", sessionId, "user").
		Order("seq asc, id asc").
		Limit(1).
		Find(record).Error
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(record.Content), nil
}

func maxInt(value int, min int) int {
	if value < min {
		return min
	}
	return value
}
